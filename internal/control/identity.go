// Users and agents in control SQLite. Additive schema on top of the
// existing principals/sessions/executions tables: legacy rows and subjects
// (including brain-curator grants) are untouched.
package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/denisqq/agent-camp/internal/camp"
)

const identitySchema = `
CREATE TABLE IF NOT EXISTS users(
	id TEXT PRIMARY KEY,
	record TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS agents(
	id TEXT PRIMARY KEY,
	owner_user_id TEXT NOT NULL REFERENCES users(id),
	record TEXT NOT NULL,
	created_at TEXT NOT NULL,
	status TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS agents_by_owner ON agents(owner_user_id);`

func (s *Store) User(ctx context.Context, id string) (camp.User, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, "SELECT record FROM users WHERE id=?", id).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return camp.User{}, camp.ErrNotFound
		}
		return camp.User{}, err
	}
	var u camp.User
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		return camp.User{}, err
	}
	return u, nil
}

func (s *Store) CreateUser(ctx context.Context, u camp.User) error {
	raw, err := json.Marshal(u)
	if err != nil {
		return err
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
		raw, err = json.Marshal(u)
		if err != nil {
			return err
		}
	}
	res, err := s.db.ExecContext(ctx, "INSERT INTO users(id, record, created_at) VALUES(?,?,?)", u.ID, raw, u.CreatedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return camp.ErrConflict
	}
	return nil
}

func (s *Store) SaveUser(ctx context.Context, u camp.User) error {
	raw, err := json.Marshal(u)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, "UPDATE users SET record=? WHERE id=?", raw, u.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return camp.ErrNotFound
	}
	return nil
}

func (s *Store) Agent(ctx context.Context, id string) (camp.Agent, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, "SELECT record FROM agents WHERE id=?", id).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return camp.Agent{}, camp.ErrNotFound
		}
		return camp.Agent{}, err
	}
	var a camp.Agent
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		return camp.Agent{}, err
	}
	return a, nil
}

func (s *Store) CreateAgent(ctx context.Context, a camp.Agent) error {
	if a.Status == "" {
		a.Status = "active"
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
		raw, err = json.Marshal(a)
		if err != nil {
			return err
		}
	}
	// The foreign key on owner_user_id makes an orphan agent impossible;
	// a clearer error beats a driver-specific constraint message.
	if _, err := s.User(ctx, a.OwnerUserID); err != nil {
		return camp.ErrInvalid
	}
	res, err := s.db.ExecContext(ctx, "INSERT INTO agents(id, owner_user_id, record, created_at, status) VALUES(?,?,?,?,?)", a.ID, a.OwnerUserID, raw, a.CreatedAt.UTC().Format(time.RFC3339), a.Status)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return camp.ErrConflict
	}
	return nil
}

func (s *Store) SaveAgent(ctx context.Context, a camp.Agent) error {
	current, err := s.Agent(ctx, a.ID)
	if err != nil {
		return err
	}
	if a.OwnerUserID != current.OwnerUserID {
		return camp.ErrInvalid // ownership is immutable
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, "UPDATE agents SET record=?, status=? WHERE id=?", raw, a.Status, a.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return camp.ErrNotFound
	}
	return nil
}

func (s *Store) AgentsByOwner(ctx context.Context, userID string) ([]camp.Agent, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT record FROM agents WHERE owner_user_id=? ORDER BY created_at, id", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []camp.Agent
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var a camp.Agent
		if err := json.Unmarshal([]byte(raw), &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
