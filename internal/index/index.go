// Package index maintains a disposable local SQLite search projection.
package index

import (
	"github.com/datretyakov/agent-camp/internal/camp"
	"context"
	"database/sql"
	"errors"
	_ "modernc.org/sqlite"
	"strings"
	"sync"
)

type Index struct {
	db *sql.DB
	mu sync.Mutex
}

func Open(path string) (*Index, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS checkpoint(space TEXT PRIMARY KEY,commit_id TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS documents(space TEXT NOT NULL,id TEXT NOT NULL,revision TEXT NOT NULL,path TEXT NOT NULL,content TEXT NOT NULL,PRIMARY KEY(space,id));
CREATE INDEX IF NOT EXISTS documents_path ON documents(space,path);
CREATE VIRTUAL TABLE IF NOT EXISTS text_index USING fts5(space UNINDEXED,id UNINDEXED,content,tokenize='unicode61');`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Index{db: db}, nil
}
func (i *Index) Close() error { return i.db.Close() }
func (i *Index) Sync(ctx context.Context, s *camp.Store, space string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	head, err := s.Load(ctx, space)
	if err != nil {
		return err
	}
	var watermark string
	err = i.db.QueryRowContext(ctx, "SELECT commit_id FROM checkpoint WHERE space=?", space).Scan(&watermark)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if watermark == head.Commit {
		return nil
	}
	full := watermark == ""
	chain := []camp.Snapshot{}
	if !full {
		at := head
		for at.Commit != watermark {
			chain = append(chain, at)
			if at.Parent == "" || len(chain) > 10000 {
				full = true
				break
			}
			at, err = s.At(ctx, space, at.Parent)
			if err != nil {
				return err
			}
		}
	}
	// A crash during projection updates leaves an explicitly incomplete watermark.
	// The next sync rebuilds; ordinary restarts with a completed watermark do not.
	if _, err = i.db.ExecContext(ctx, "INSERT INTO checkpoint VALUES(?, '') ON CONFLICT(space) DO UPDATE SET commit_id=''", space); err != nil {
		return err
	}
	if full {
		if _, err = i.db.ExecContext(ctx, "DELETE FROM documents WHERE space=?", space); err != nil {
			return err
		}
		if _, err = i.db.ExecContext(ctx, "DELETE FROM text_index WHERE space=?", space); err != nil {
			return err
		}
		cursor := ""
		for {
			records, next, err := s.List(ctx, head, "/", cursor, 32)
			if err != nil {
				return err
			}
			revisions := make([]camp.Revision, 0, len(records))
			for _, rec := range records {
				rev, err := s.ReadRevision(ctx, space, rec.Revision)
				if err != nil {
					return err
				}
				revisions = append(revisions, rev)
			}
			if err = i.apply(ctx, space, revisions); err != nil {
				return err
			}
			if next == "" {
				break
			}
			cursor = next
		}
	} else {
		for n := len(chain) - 1; n >= 0; n-- {
			for _, id := range chain[n].Changes {
				rev, err := s.ReadRevision(ctx, space, id)
				if err != nil {
					return err
				}
				if err = i.apply(ctx, space, []camp.Revision{rev}); err != nil {
					return err
				}
			}
		}
	}
	_, err = i.db.ExecContext(ctx, "UPDATE checkpoint SET commit_id=? WHERE space=?", head.Commit, space)
	return err
}
func (i *Index) apply(ctx context.Context, space string, revisions []camp.Revision) error {
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, rev := range revisions {
		if _, err = tx.ExecContext(ctx, "DELETE FROM documents WHERE space=? AND id=?", space, rev.RecordID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM text_index WHERE space=? AND id=?", space, rev.RecordID); err != nil {
			return err
		}
		if rev.Deleted {
			continue
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO documents VALUES(?,?,?,?,?)", space, rev.RecordID, rev.ID, rev.Path, rev.Content); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO text_index(space,id,content) VALUES(?,?,?)", space, rev.RecordID, rev.Content); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Query is the first-page convenience API for internal callers.
func (i *Index) Query(ctx context.Context, space, mode, query string, limit int) ([]camp.SearchHit, string, error) {
	page, err := i.QueryPage(ctx, space, mode, query, 0, limit, "")
	return page.Hits, page.Watermark, err
}

func (i *Index) QueryPage(ctx context.Context, space, mode, query string, offset, limit int, expected string) (camp.SearchPage, error) {
	page := camp.SearchPage{Hits: []camp.SearchHit{}}
	if query == "" || len(query) > 1024 || limit < 1 || limit > 200 || offset < 0 || (mode != "grep" && mode != "search") {
		return page, camp.ErrInvalid
	}
	// The watermark and result rows must describe the same SQLite snapshot.
	tx, err := i.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, "SELECT commit_id FROM checkpoint WHERE space=?", space).Scan(&page.Watermark)
	if errors.Is(err, sql.ErrNoRows) {
		if expected != "" {
			return page, camp.ErrConflict
		}
		return page, nil
	}
	if err != nil {
		return page, err
	}
	if expected != "" && expected != page.Watermark {
		return page, camp.ErrConflict
	}
	if page.Watermark == "" {
		return page, nil
	}
	var rows *sql.Rows
	if mode == "grep" {
		rows, err = tx.QueryContext(ctx, `SELECT id,revision,path,substr(content,max(1,instr(content,?)-80),240) FROM documents WHERE space=? AND instr(content,?)>0 ORDER BY path,id LIMIT ? OFFSET ?`, query, space, query, limit+1, offset)
	} else {
		phrase := `"` + strings.ReplaceAll(query, `"`, `""`) + `"`
		rows, err = tx.QueryContext(ctx, `SELECT d.id,d.revision,d.path,snippet(text_index,2,'','', ' … ',32) FROM text_index JOIN documents d ON d.space=text_index.space AND d.id=text_index.id WHERE text_index MATCH ? AND text_index.space=? ORDER BY rank,d.path,d.id LIMIT ? OFFSET ?`, phrase, space, limit+1, offset)
	}
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var hit camp.SearchHit
		if err = rows.Scan(&hit.RecordID, &hit.Revision, &hit.Path, &hit.Snippet); err != nil {
			return page, err
		}
		page.Hits = append(page.Hits, hit)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	if len(page.Hits) > limit {
		page.More = true
		page.Hits = page.Hits[:limit]
		page.Next = offset + limit
	}
	return page, nil
}
