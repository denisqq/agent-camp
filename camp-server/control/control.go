// Package control owns the operational state of an Agent Camp installation:
// credentials, sessions and execution receipts. It is deliberately separate
// from the workspace store: losing this database locks operators out, but
// never loses workspace memory. Curation pipeline state also lives here when
// its plugin is installed.
package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/denisqq/agent-camp/camp-server/camp"

	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;
CREATE TABLE IF NOT EXISTS principals(hash TEXT PRIMARY KEY, principal TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS sessions(id TEXT PRIMARY KEY, state TEXT NOT NULL, version INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS executions(key TEXT PRIMARY KEY, state TEXT NOT NULL, version INTEGER NOT NULL);` + identitySchema)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}
func (s *Store) Close() error { return s.db.Close() }

// Principals implement camp.PrincipalStore.

func (s *Store) Principal(ctx context.Context, key string) (camp.Principal, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, "SELECT principal FROM principals WHERE hash=?", key).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return camp.Principal{}, camp.ErrNotFound
		}
		return camp.Principal{}, err
	}
	var p camp.Principal
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return camp.Principal{}, err
	}
	return p, nil
}
func (s *Store) SavePrincipal(ctx context.Context, p camp.Principal) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO principals(hash, principal) VALUES(?,?) ON CONFLICT(hash) DO UPDATE SET principal=excluded.principal", p.TokenHash, raw)
	return err
}

// Sessions and executions implement camp.RunStateStore. Tags are optimistic
// concurrency versions; an empty tag creates, a mismatched tag conflicts.

func (s *Store) Session(ctx context.Context, id string) (camp.SessionState, string, error) {
	var raw string
	var version int64
	if err := s.db.QueryRowContext(ctx, "SELECT state, version FROM sessions WHERE id=?", id).Scan(&raw, &version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return camp.SessionState{}, "", camp.ErrNotFound
		}
		return camp.SessionState{}, "", err
	}
	var st camp.SessionState
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return camp.SessionState{}, "", err
	}
	return st, versionTag(version), nil
}
func (s *Store) SaveSession(ctx context.Context, st *camp.SessionState, expect string) (string, error) {
	raw, err := json.Marshal(st)
	if err != nil {
		return "", err
	}
	if expect == "" {
		res, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO sessions(id, state, version) VALUES(?,?,1)", st.ID, raw)
		if err != nil {
			return "", err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return "", camp.ErrConflict
		}
		return versionTag(1), nil
	}
	res, err := s.db.ExecContext(ctx, "UPDATE sessions SET state=?, version=version+1 WHERE id=? AND version=?", raw, st.ID, tagVersion(expect))
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", camp.ErrConflict
	}
	return versionTag(tagVersion(expect) + 1), nil
}
func (s *Store) Execution(ctx context.Context, key string) (camp.ExecutionState, string, error) {
	var raw string
	var version int64
	if err := s.db.QueryRowContext(ctx, "SELECT state, version FROM executions WHERE key=?", key).Scan(&raw, &version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return camp.ExecutionState{}, "", camp.ErrNotFound
		}
		return camp.ExecutionState{}, "", err
	}
	var x camp.ExecutionState
	if err := json.Unmarshal([]byte(raw), &x); err != nil {
		return camp.ExecutionState{}, "", err
	}
	return x, versionTag(version), nil
}
func (s *Store) CreateExecution(ctx context.Context, key string, x camp.ExecutionState) (string, error) {
	raw, err := json.Marshal(x)
	if err != nil {
		return "", err
	}
	res, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO executions(key, state, version) VALUES(?,?,1)", key, raw)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", camp.ErrConflict
	}
	return versionTag(1), nil
}
func (s *Store) SaveExecution(ctx context.Context, key string, x camp.ExecutionState, expect string) error {
	raw, err := json.Marshal(x)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, "UPDATE executions SET state=?, version=version+1 WHERE key=? AND version=?", raw, key, tagVersion(expect))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return camp.ErrConflict
	}
	return nil
}
func versionTag(v int64) string { return strconv.FormatInt(v, 10) }
func tagVersion(tag string) int64 {
	v, _ := strconv.ParseInt(strings.TrimSpace(tag), 10, 64)
	return v
}
