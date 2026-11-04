package camp

import (
	"bytes"
	"github.com/datretyakov/agent-camp/internal/objectstore"
	"github.com/datretyakov/agent-camp/internal/tree"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Store struct {
	Objects objectstore.Store
	// OnCommit, when set, is invoked once per accepted changeset. Indexers use
	// it for event-driven projection sync instead of polling every space.
	OnCommit func(space string)
}

func NewStore(s objectstore.Store) *Store { return &Store{Objects: s} }

var safeID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

func prefix(space string) string              { return "spaces/" + space + "/" }
func (s *Store) tree(space string) *tree.Tree { return tree.New(s.Objects, prefix(space)+"nodes/") }
func (s *Store) CreateSpace(ctx context.Context, space Space) (Snapshot, error) {
	if ValidateAccess(space.Access) != nil || (space.Kind == "shared" && space.Access != nil) {
		return Snapshot{}, ErrInvalid
	}
	if !safeID.MatchString(space.ID) || space.Owner == "" {
		return Snapshot{}, ErrInvalid
	}
	if space.Kind != "personal" && space.Kind != "team" && space.Kind != "shared" && space.Kind != "system" {
		return Snapshot{}, ErrInvalid
	}
	if space.Policy == "" {
		space.Policy = "v1"
	}
	if _, err := s.Objects.Put(ctx, "registry/spaces/"+space.ID, []byte(space.ID), "*"); err != nil && !errors.Is(err, ErrConflict) {
		return Snapshot{}, err
	}
	snap := Snapshot{Space: space, Commit: ID()}
	b, _ := encode(snap)
	if _, err := s.Objects.Put(ctx, prefix(space.ID)+"commits/"+snap.Commit, b, "*"); err != nil {
		return Snapshot{}, err
	}
	etag, err := s.Objects.Put(ctx, prefix(space.ID)+"HEAD", b, "*")
	snap.ETag = etag
	return snap, err
}

// Spaces reads the durable discovery registry, then verifies each accepted HEAD.
func (s *Store) Spaces(ctx context.Context) ([]Space, error) {
	var out []Space
	cursor := ""
	for {
		keys, next, err := s.Objects.List(ctx, "registry/spaces/", cursor, 1000)
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			id := key[len("registry/spaces/"):]
			snap, err := s.Load(ctx, id)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			out = append(out, snap.Space)
		}
		if next == "" {
			return out, nil
		}
		cursor = next
	}
}
func (s *Store) Load(ctx context.Context, space string) (Snapshot, error) {
	var snap Snapshot
	if !safeID.MatchString(space) {
		return snap, ErrInvalid
	}
	b, etag, err := s.Objects.Get(ctx, prefix(space)+"HEAD")
	if err != nil {
		return snap, err
	}
	err = json.Unmarshal(b, &snap)
	snap.ETag = etag
	return snap, err
}
func (s *Store) At(ctx context.Context, space, commit string) (Snapshot, error) {
	var snap Snapshot
	if !safeID.MatchString(space) || !safeID.MatchString(commit) {
		return snap, ErrInvalid
	}
	b, _, err := s.Objects.Get(ctx, prefix(space)+"commits/"+commit)
	if err != nil {
		return snap, err
	}
	err = json.Unmarshal(b, &snap)
	return snap, err
}
func (s *Store) ReadRevision(ctx context.Context, space, id string) (Revision, error) {
	var rev Revision
	if !safeID.MatchString(space) || !safeID.MatchString(id) {
		return rev, ErrInvalid
	}
	b, _, err := s.Objects.Get(ctx, prefix(space)+"revisions/"+id)
	if err != nil {
		return rev, err
	}
	err = json.Unmarshal(b, &rev)
	return rev, err
}
func (s *Store) Record(ctx context.Context, snap Snapshot, id string) (Record, error) {
	var rec Record
	b, err := s.tree(snap.Space.ID).Get(ctx, snap.Records, id)
	if err != nil {
		return rec, err
	}
	err = json.Unmarshal(b, &rec)
	return rec, err
}
func (s *Store) RecordByPath(ctx context.Context, snap Snapshot, path string, includeDeleted ...bool) (Record, error) {
	b, err := s.tree(snap.Space.ID).Get(ctx, snap.Paths, path)
	if err != nil {
		return Record{}, err
	}
	var id string
	if err = json.Unmarshal(b, &id); err != nil {
		return Record{}, err
	}
	if id == "" {
		return Record{}, ErrNotFound
	}
	rec, err := s.Record(ctx, snap, id)
	if err == nil && rec.Deleted && !(len(includeDeleted) > 0 && includeDeleted[0]) {
		return Record{}, ErrNotFound
	}
	return rec, err
}
func (s *Store) Operation(ctx context.Context, snap Snapshot, key string) (Receipt, error) {
	var rec Receipt
	b, err := s.tree(snap.Space.ID).Get(ctx, snap.Operations, key)
	if err != nil {
		return rec, err
	}
	err = json.Unmarshal(b, &rec)
	return rec, err
}

// SectionACL resolves the deepest kind=section record at path or any of its
// ancestors. That record's Access override governs the whole subtree and
// replaces anything above it. Missing overrides inherit; malformed overrides deny.
func (s *Store) SectionACL(ctx context.Context, snap Snapshot, path string) (*Access, error) {
	check := func(p string) (*Access, bool, error) {
		rec, err := s.RecordByPath(ctx, snap, p)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, false, nil
			}
			return nil, false, err
		}
		if rec.Kind != "section" {
			return nil, false, nil
		}
		raw, exists := rec.Metadata["access"]
		if !exists {
			return nil, false, nil
		}
		access, ok := ParseAccess(raw)
		if !ok {
			return nil, false, ErrDenied
		}
		return access, true, nil
	}
	for p := path; ; {
		if access, ok, err := check(p); err != nil || ok {
			return access, err
		}
		if p == "/" {
			return nil, nil
		}
		p = strings.TrimSuffix(p, "/")
		if cut := strings.LastIndex(p, "/"); cut > 0 {
			p = p[:cut]
		} else {
			p = "/"
		}
	}
}

// ParseAccess validates an access override; invalid values are ignored so a
// malformed overrides are rejected, never inherited through.
func ParseAccess(raw any) (*Access, bool) {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, false
	}
	access := &Access{}
	visibility, _ := m["visibility"].(string)
	if raw, exists := m["visibility"]; exists {
		if _, ok := raw.(string); !ok {
			return nil, false
		}
	}
	if visibility != "open" && visibility != "closed" && visibility != "" {
		return nil, false
	}
	access.Visibility = visibility
	entries, _ := m["share"].([]any)
	if raw, exists := m["share"]; exists {
		if _, ok := raw.([]any); !ok {
			return nil, false
		}
	}
	for _, item := range entries {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, false
		}
		subject, _ := entry["subject"].(string)
		role, _ := entry["role"].(string)
		if subject == "" || len(subject) > 128 || roleRank[role] == 0 {
			return nil, false
		}
		access.Share = append(access.Share, ShareEntry{Subject: subject, Role: role})
	}
	if access.Visibility == "" && len(access.Share) == 0 {
		return nil, false
	}
	return access, true
}

func (s *Store) commitOnce(ctx context.Context, base Snapshot, revisions []Revision, operation string, receipt Receipt, space *Space) (Snapshot, error) {
	current, err := s.Load(ctx, base.Space.ID)
	if err != nil {
		return Snapshot{}, err
	}
	if operation != "" {
		prior, e := s.Operation(ctx, current, operation)
		if e == nil {
			if prior.Fingerprint != receipt.Fingerprint {
				return Snapshot{}, ErrConflict
			}
			return current, nil
		}
		if !errors.Is(e, ErrNotFound) {
			return Snapshot{}, e
		}
	}
	if current.Commit != base.Commit {
		return Snapshot{}, errHeadAdvanced
	}
	next := base
	next.Parent = base.Commit
	next.Commit = ID()
	next.Changes = nil
	next.ETag = ""
	if space != nil {
		if space.ID != base.Space.ID {
			return Snapshot{}, ErrInvalid
		}
		next.Space = *space
	}
	tr := s.tree(base.Space.ID)
	ordered, err := orderRevisions(ctx, s, base, revisions)
	if err != nil {
		return Snapshot{}, err
	}
	for _, rev := range ordered {
		if !safeID.MatchString(rev.ID) || !safeID.MatchString(rev.RecordID) {
			return Snapshot{}, ErrInvalid
		}
		if rev.Time.IsZero() {
			rev.Time = time.Now().UTC()
		}
		old, e := s.Record(ctx, next, rev.RecordID)
		if e != nil && !errors.Is(e, ErrNotFound) {
			return Snapshot{}, e
		}
		if errors.Is(e, ErrNotFound) && rev.Parent != "" {
			return Snapshot{}, ErrConflict
		}
		if e == nil {
			if rev.Parent != old.Revision {
				return Snapshot{}, ErrConflict
			}
			if old.Path != rev.Path {
				next.Paths, err = tr.Set(ctx, next.Paths, old.Path, json.RawMessage(`""`))
				if err != nil {
					return Snapshot{}, err
				}
			}
		}
		existing, e := s.RecordByPath(ctx, next, rev.Path)
		if e == nil && existing.ID != rev.RecordID {
			return Snapshot{}, fmt.Errorf("%w: path occupied", ErrConflict)
		}
		if e != nil && !errors.Is(e, ErrNotFound) {
			return Snapshot{}, e
		}
		b, e := encode(rev)
		if e != nil {
			return Snapshot{}, e
		}
		if _, err = s.Objects.Put(ctx, prefix(base.Space.ID)+"revisions/"+rev.ID, b, "*"); err != nil {
			if !errors.Is(err, ErrConflict) {
				return Snapshot{}, err
			}
			existing, _, readErr := s.Objects.Get(ctx, prefix(base.Space.ID)+"revisions/"+rev.ID)
			if readErr != nil {
				return Snapshot{}, readErr
			}
			if !bytes.Equal(existing, b) {
				return Snapshot{}, ErrConflict
			}
		}
		rec := Record{ID: rev.RecordID, Path: rev.Path, Revision: rev.ID, Kind: rev.Kind, Metadata: rev.Metadata, Deleted: rev.Deleted}
		b, _ = encode(rec)
		next.Records, err = tr.Set(ctx, next.Records, rec.ID, b)
		if err != nil {
			return Snapshot{}, err
		}
		b, _ = encode(rec.ID)
		next.Paths, err = tr.Set(ctx, next.Paths, rec.Path, b)
		if err != nil {
			return Snapshot{}, err
		}
		next.Changes = append(next.Changes, rev.ID)
	}
	if operation != "" {
		receipt.ChangeID = next.Commit
		b, e := encode(receipt)
		if e != nil {
			return Snapshot{}, e
		}
		next.Operations, err = tr.Set(ctx, next.Operations, operation, b)
		if err != nil {
			return Snapshot{}, err
		}
	}
	b, err := encode(next)
	if err != nil {
		return Snapshot{}, err
	}
	if _, err = s.Objects.Put(ctx, prefix(base.Space.ID)+"commits/"+next.Commit, b, "*"); err != nil {
		return Snapshot{}, err
	}
	etag, err := s.Objects.Put(ctx, prefix(base.Space.ID)+"HEAD", b, current.ETag)
	next.ETag = etag
	if errors.Is(err, ErrConflict) {
		return Snapshot{}, errHeadAdvanced
	}
	if err == nil && s.OnCommit != nil {
		go s.OnCommit(base.Space.ID)
	}
	return next, err
}

// orderRevisions sequences a changeset so revisions vacating a path (delete or
// move-away) apply before revisions occupying that path. Without this, delete
// plus recreate at the same path in one run fails or succeeds depending on the
// random record id order. Path swap cycles are reported as conflicts.
func orderRevisions(ctx context.Context, s *Store, base Snapshot, revisions []Revision) ([]Revision, error) {
	sort.Slice(revisions, func(i, j int) bool { return revisions[i].RecordID < revisions[j].RecordID })
	type node struct {
		rev     Revision
		oldPath string
		waiting int
		done    bool
	}
	nodes := make([]node, len(revisions))
	vacaters := map[string][]int{}
	for i, rev := range revisions {
		nodes[i] = node{rev: rev}
		old, err := s.Record(ctx, base, rev.RecordID)
		if err != nil {
			if !errors.Is(err, ErrNotFound) {
				return nil, err
			}
			continue
		}
		nodes[i].oldPath = old.Path
		if rev.Deleted || rev.Path != old.Path {
			vacaters[old.Path] = append(vacaters[old.Path], i)
		}
	}
	dependents := make([][]int, len(revisions))
	for i, n := range nodes {
		if n.rev.Deleted {
			continue
		}
		for _, v := range vacaters[n.rev.Path] {
			if v != i {
				nodes[i].waiting++
				dependents[v] = append(dependents[v], i)
			}
		}
	}
	ordered := make([]Revision, 0, len(revisions))
	for len(ordered) < len(revisions) {
		picked := -1
		for i := range nodes {
			if !nodes[i].done && nodes[i].waiting == 0 {
				picked = i
				break
			}
		}
		if picked < 0 {
			return nil, fmt.Errorf("%w: cyclic path changes", ErrConflict)
		}
		nodes[picked].done = true
		ordered = append(ordered, nodes[picked].rev)
		for _, i := range dependents[picked] {
			nodes[i].waiting--
		}
	}
	return ordered, nil
}

// List enumerates a directory: the record stored exactly at the path plus the
// records under path/… Sibling paths that merely share a string prefix, such
// as /goals2 under /goals, are excluded.
func (s *Store) List(ctx context.Context, snap Snapshot, dir, after string, limit int) ([]Record, string, error) {
	prefix := ""
	if dir != "" && dir != "/" {
		prefix = strings.TrimSuffix(dir, "/") + "/"
	}
	var entries []tree.Entry
	if exact := strings.TrimSuffix(prefix, "/"); exact != "" && exact > after {
		if value, err := s.tree(snap.Space.ID).Get(ctx, snap.Paths, exact); err == nil {
			entries = append(entries, tree.Entry{Key: exact, Value: value})
		} else if !errors.Is(err, ErrNotFound) {
			return nil, "", err
		}
	}
	page, err := s.tree(snap.Space.ID).Page(ctx, snap.Paths, prefix, after, limit)
	if err != nil {
		return nil, "", err
	}
	entries = append(entries, page...)
	records := []Record{}
	cursor := ""
	for _, entry := range entries {
		cursor = entry.Key
		var id string
		if err = json.Unmarshal(entry.Value, &id); err != nil {
			return nil, "", err
		}
		if id == "" {
			continue
		}
		rec, e := s.Record(ctx, snap, id)
		if e != nil {
			return nil, "", e
		}
		if !rec.Deleted {
			records = append(records, rec)
		}
	}
	if len(entries) < limit {
		cursor = ""
	}
	return records, cursor, nil
}
