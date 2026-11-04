package camp

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type Execution struct {
	Principal    Principal
	Primary      string
	Snapshots    map[string]Snapshot
	Staged       map[string]Revision
	Proposals    map[string]string
	Dependencies []Dependency
	CursorKey    []byte
	Calls        int
	Bytes        int
	Closed       bool
}
type Broker struct {
	Store  *Store
	Auth   *Auth
	Search Searcher
	// Status, when set, reports curation job state for a JobKey. Deployments
	// wire it to the control-plane job store.
	Status func(ctx context.Context, key string) (state string, attempts int, found bool, err error)
}
type Searcher interface {
	QueryPage(context.Context, string, string, string, int, int, string) (SearchPage, error)
}
type SearchPage struct {
	Hits      []SearchHit
	Watermark string
	Next      int
	More      bool
}

type SearchHit struct {
	RecordID string `json:"record_id"`
	Revision string `json:"revision"`
	Path     string `json:"path"`
	Snippet  string `json:"snippet"`
}

func NewBroker(s *Store, a *Auth) *Broker { return &Broker{Store: s, Auth: a} }
func (b *Broker) Begin(ctx context.Context, p Principal, space string) (*Execution, error) {
	p, err := b.Auth.Current(ctx, p)
	if err != nil {
		return nil, err
	}
	snap, err := b.Store.Load(ctx, space)
	if err != nil {
		return nil, ErrDenied
	}
	if !contains(p.Spaces, space) || !contains(p.Scopes, "read") {
		return nil, ErrDenied
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return nil, err
	}
	return &Execution{Principal: p, Primary: space, Snapshots: map[string]Snapshot{space: snap}, Staged: map[string]Revision{}, CursorKey: key}, nil
}
func cleanPath(p string) (string, error) {
	if !utf8.ValidString(p) || len(p) > 1024 || !strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00\r\n") {
		return "", ErrInvalid
	}
	for _, s := range strings.Split(p, "/") {
		if s == ".." || s == "." {
			return "", ErrInvalid
		}
	}
	return path.Clean(p), nil
}

// pathUnder reports whether p is the directory itself or lives under it.
func pathUnder(p, dir string) bool {
	if dir == "" || dir == "/" {
		return true
	}
	return p == dir || strings.HasPrefix(p, dir+"/")
}
func str(a map[string]any, k string) string { v, _ := a[k].(string); return v }
func number(a map[string]any, k string, def int) int {
	switch v := a[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		n, e := v.Int64()
		if e == nil {
			return int(n)
		}
	}
	return def
}
func (b *Broker) snapshot(ctx context.Context, e *Execution, space string) (Snapshot, error) {
	current, err := b.Store.Load(ctx, space)
	if errors.Is(err, ErrNotFound) {
		return Snapshot{}, ErrDenied
	}
	if err != nil {
		return Snapshot{}, err
	}
	if !contains(e.Principal.Spaces, space) {
		return Snapshot{}, ErrDenied
	}
	if snap, ok := e.Snapshots[space]; ok {
		return snap, nil
	}
	e.Snapshots[space] = current
	return current, nil
}
func (b *Broker) allow(ctx context.Context, e *Execution, space, path, action string) error {
	current, err := b.Store.Load(ctx, space)
	if errors.Is(err, ErrNotFound) {
		return ErrDenied
	}
	if err != nil {
		return err
	}
	return b.AuthorizePath(ctx, e.Principal, current, path, action)
}
func (b *Broker) depend(e *Execution, snap Snapshot, rec Record) {
	for _, d := range e.Dependencies {
		if d.Space == snap.Space.ID && d.Record == rec.ID && d.Path == rec.Path && d.Revision == rec.Revision && d.AllowDeleted == rec.Deleted {
			return
		}
	}
	e.Dependencies = append(e.Dependencies, Dependency{Space: snap.Space.ID, Record: rec.ID, Path: rec.Path, Policy: snap.Space.Policy, Revision: rec.Revision, AllowDeleted: rec.Deleted})
}
func (b *Broker) resolve(ctx context.Context, e *Execution, space, p, revision string, includeDeleted ...bool) (Record, Revision, error) {
	allowDeleted := len(includeDeleted) > 0 && includeDeleted[0]
	snap, err := b.snapshot(ctx, e, space)
	if err != nil {
		return Record{}, Revision{}, err
	}
	var rec Record
	var rev Revision
	if space == e.Primary {
		for _, v := range e.Staged {
			if v.Path == p && (!v.Deleted || allowDeleted) {
				rec = Record{ID: v.RecordID, Path: v.Path, Revision: v.ID, Kind: v.Kind, Metadata: v.Metadata, Deleted: v.Deleted}
				rev = v
				break
			}
		}
	}
	if rec.ID == "" {
		rec, err = b.Store.RecordByPath(ctx, snap, p, allowDeleted)
		if err != nil {
			return rec, rev, err
		}
		if space == e.Primary {
			if staged, ok := e.Staged[rec.ID]; ok && ((staged.Deleted && !allowDeleted) || staged.Path != p) {
				return rec, rev, ErrNotFound
			}
		}
		rev, err = b.Store.ReadRevision(ctx, space, rec.Revision)
		if err != nil {
			return rec, rev, err
		}
	}
	if err = b.allow(ctx, e, space, rec.Path, "read"); err != nil {
		return Record{}, Revision{}, err
	}
	current, err := b.Store.Load(ctx, space)
	if err != nil {
		return Record{}, Revision{}, err
	}
	latest, err := b.Store.Record(ctx, current, rec.ID)
	if err == nil {
		staged, restoring := e.Staged[rec.ID]
		restoring = space == e.Primary && restoring && !staged.Deleted && staged.Parent == latest.Revision
		if latest.Deleted && !allowDeleted && !restoring {
			return Record{}, Revision{}, ErrNotFound
		}
		if err = b.AuthorizePath(ctx, e.Principal, current, latest.Path, "read"); err != nil {
			return Record{}, Revision{}, err
		}
	} else if !errors.Is(err, ErrNotFound) {
		return Record{}, Revision{}, err
	}
	if revision != "" {
		rev, err = b.Store.ReadRevision(ctx, space, revision)
		if err != nil || rev.RecordID != rec.ID {
			return Record{}, Revision{}, ErrNotFound
		}
		if err = b.allow(ctx, e, space, rev.Path, "read"); err != nil {
			return Record{}, Revision{}, err
		}
	}
	if err = b.CheckSources(ctx, rev.Sources); err != nil {
		return Record{}, Revision{}, err
	}
	dependencyRecord := rec
	dependencyRecord.Revision = rev.ID
	if staged, ok := e.Staged[rec.ID]; space == e.Primary && ok && staged.ID == rev.ID {
		if staged.Parent == "" {
			return rec, rev, nil
		}
		dependencyRecord.Revision = staged.Parent
		dependencyRecord.Deleted = latest.Deleted
	}
	b.depend(e, current, dependencyRecord)
	return rec, rev, nil
}
func (b *Broker) stage(ctx context.Context, e *Execution, rev Revision) error {
	if _, proposal := e.Proposals[rev.RecordID]; proposal {
		if _, exists := e.Staged[rev.RecordID]; exists {
			return ErrDenied
		}
	}
	if err := b.checkWrite(ctx, e, rev); err != nil {
		return err
	}
	size := len(rev.Content)
	meta, err := encode(rev.Metadata)
	if err != nil {
		return ErrInvalid
	}
	size += len(meta)
	if size > 1024*1024 || e.Bytes+size > 8*1024*1024 {
		return errors.New("staging limit exceeded")
	}
	if prior, ok := e.Staged[rev.RecordID]; ok {
		rev.Parent = prior.Parent
	}
	rev.ID = ID()
	rev.Actor = e.Principal.Subject
	rev.Time = time.Now().UTC()
	e.Staged[rev.RecordID] = rev
	e.Bytes += size
	return nil
}
func (b *Broker) Finish(ctx context.Context, e *Execution, operation string, r Receipt) (Snapshot, error) {
	if e.Closed {
		return Snapshot{}, ErrConflict
	}
	defer func() { e.Closed = true }()
	if _, err := b.Auth.Current(ctx, e.Principal); err != nil {
		return Snapshot{}, err
	}
	if err := b.CheckDependencies(ctx, e.Principal, e.Dependencies); err != nil {
		return Snapshot{}, err
	}
	revs := make([]Revision, 0, len(e.Staged))
	for _, v := range e.Staged {
		if err := b.checkWrite(ctx, e, v); err != nil {
			return Snapshot{}, err
		}
		revs = append(revs, v)
	}
	sort.Slice(revs, func(i, j int) bool { return revs[i].RecordID < revs[j].RecordID })
	r.Dependencies = e.Dependencies
	return b.Store.Commit(ctx, e.Snapshots[e.Primary], revs, operation, r, nil)
}
func (b *Broker) CheckDependencies(ctx context.Context, p Principal, deps []Dependency) error {
	for _, d := range deps {
		snap, err := b.Store.Load(ctx, d.Space)
		if err != nil {
			return ErrDenied
		}
		if snap.Space.Policy != d.Policy {
			return ErrDenied
		}
		if err = b.AuthorizePath(ctx, p, snap, d.Path, "read"); err != nil {
			return err
		}
		rec, err := b.Store.Record(ctx, snap, d.Record)
		if errors.Is(err, ErrNotFound) {
			return ErrDenied
		}
		if err != nil || (rec.Deleted && !d.AllowDeleted) {
			return ErrDenied
		}
		if err = b.AuthorizePath(ctx, p, snap, rec.Path, "read"); err != nil {
			return err
		}
		revision, err := b.Store.ReadRevision(ctx, d.Space, d.Revision)
		if err != nil || revision.RecordID != d.Record {
			return ErrDenied
		}
		if err = b.AuthorizePath(ctx, p, snap, revision.Path, "read"); err != nil {
			return err
		}
		if err = b.CheckSources(ctx, revision.Sources); err != nil {
			return err
		}
	}
	return nil
}
func cursor(e *Execution, value string, decode bool) (string, error) {
	block, err := aes.NewCipher(e.CursorKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if decode {
		if value == "" {
			return "", nil
		}
		buf, err := base64.RawURLEncoding.DecodeString(value)
		if err != nil || len(buf) < gcm.NonceSize() {
			return "", ErrInvalid
		}
		data, err := gcm.Open(nil, buf[:gcm.NonceSize()], buf[gcm.NonceSize():], nil)
		if err != nil {
			return "", ErrInvalid
		}
		return string(data), nil
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(value), nil)), nil
}
func (b *Broker) Call(ctx context.Context, e *Execution, method string, a map[string]any) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.Closed {
		return nil, ErrConflict
	}
	e.Calls++
	if e.Calls > 1000 {
		return nil, errors.New("callback limit exceeded")
	}
	if _, err := b.Auth.Current(ctx, e.Principal); err != nil {
		return nil, err
	}
	if method == "help" || method == "describe" {
		return SDKHelp(str(a, "topic")), nil
	}
	space := str(a, "space")
	if space == "" {
		space = e.Primary
	}
	if !contains(e.Principal.Spaces, space) {
		return nil, ErrDenied
	}
	switch method {
	case "spaces":
		return b.spaces(ctx, e)
	case "ls", "find":
		return b.list(ctx, e, space, a)
	case "map":
		return b.spaceMap(ctx, e, space, a)
	case "search", "grep":
		return b.search(ctx, e, space, method, a)
	case "refresh":
		if len(e.Staged) > 0 {
			return nil, ErrConflict
		}
		snap, err := b.Store.Load(ctx, space)
		if err != nil {
			return nil, err
		}

		e.Snapshots[space] = snap
		return map[string]any{"snapshot": snap.Commit}, nil
	}
	p, err := cleanPath(str(a, "path"))
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(method, "files.") {
		return b.fileCall(ctx, e, space, method, p, a)
	}
	if method == "write" {
		if space != e.Primary {
			return nil, ErrDenied
		}
		return b.write(ctx, e, p, a)
	}
	rec, rev, err := b.resolve(ctx, e, space, p, str(a, "revision"), method == "history" || method == "revert")
	if err != nil {
		return nil, err
	}
	switch method {
	case "read":
		lines := strings.Split(rev.Content, "\n")
		start := number(a, "start_line", 1)
		limit := number(a, "limit", 200)
		if start < 1 || limit < 1 || limit > 1000 {
			return nil, ErrInvalid
		}
		end := min(len(lines), start-1+limit)
		content := ""
		if start <= len(lines) {
			content = strings.Join(lines[start-1:end], "\n")
		}
		if len(content) > 65536 {
			return nil, errors.New("read too large; request fewer lines or chunks")
		}
		return map[string]any{"record": rec, "revision": rev.ID, "content": content, "truncated": end < len(lines), "snapshot": e.Snapshots[space].Commit}, nil
	case "stat":
		return rec, nil
	case "outline":
		headings := []map[string]any{}
		for i, line := range strings.Split(rev.Content, "\n") {
			if strings.HasPrefix(line, "#") {
				headings = append(headings, map[string]any{"line": i + 1, "heading": line})
				if len(headings) == 200 {
					break
				}
			}
		}
		return map[string]any{"headings": headings, "revision": rev.ID, "truncated": len(headings) == 200}, nil
	case "history":
		return b.history(ctx, e, space, rev, number(a, "limit", 20))
	case "diff":
		before := rev.Content
		after := before
		if staged, ok := e.Staged[rec.ID]; ok {
			after = staged.Content
			if staged.Parent != "" {
				old, er := b.Store.ReadRevision(ctx, space, staged.Parent)
				if er != nil {
					return nil, er
				}
				before = old.Content
			} else {
				before = ""
			}
		}
		if len(before)+len(after) > 65536 {
			return nil, errors.New("diff too large")
		}
		return map[string]any{"before": before, "after": after}, nil
	case "related":
		return rev.Metadata["relations"], nil
	case "proposal_status":
		return b.proposalStatus(ctx, e, space, rev)
	case "move", "delete", "patch", "annotate", "revert", "link", "propose":
		if space != e.Primary {
			return nil, ErrDenied
		}
		return b.mutate(ctx, e, method, rec, rev, a)
	default:
		return nil, fmt.Errorf("%w: unknown SDK method", ErrInvalid)
	}
}
