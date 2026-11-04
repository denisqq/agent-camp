package camp

import (
	"github.com/denisqq/agent-camp/internal/objectstore"
	"github.com/denisqq/agent-camp/internal/teststore"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCommitRebasesDisjointDocuments(t *testing.T) {
	ctx := t.Context()
	s := NewStore(teststore.New())
	base, err := s.CreateSpace(ctx, Space{ID: "team", Kind: "team", Owner: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Commit(ctx, base, []Revision{{ID: "r1", RecordID: "a", Path: "/a", Content: "A"}}, "one", Receipt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Commit(ctx, base, []Revision{{ID: "r2", RecordID: "b", Path: "/b", Content: "B"}}, "two", Receipt{}, nil)
	if err != nil {
		t.Fatal("independent document conflicted", err)
	}
	if second.Parent != first.Commit {
		t.Fatal("did not append to actual HEAD")
	}
	for _, p := range []string{"/a", "/b"} {
		if _, err = s.RecordByPath(ctx, second, p); err != nil {
			t.Fatal(p, err)
		}
	}
	updated, err := s.Commit(ctx, second, []Revision{{ID: "r1-edit", RecordID: "a", Path: "/a", Parent: "r1", Content: "A edited"}}, "edit-a", Receipt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := s.Commit(ctx, second, []Revision{{ID: "r2-edit", RecordID: "b", Path: "/b", Parent: "r2", Content: "B edited"}}, "edit-b", Receipt{}, nil)
	if err != nil || merged.Parent != updated.Commit {
		t.Fatal("independent existing edits failed", err)
	}
	for _, id := range []string{"a", "b"} {
		rec, err := s.Record(ctx, merged, id)
		if err != nil {
			t.Fatal(err)
		}
		rev, err := s.ReadRevision(ctx, "team", rec.Revision)
		if err != nil || !strings.HasSuffix(rev.Content, "edited") {
			t.Fatal("lost existing edit", rev, err)
		}
	}
	_, err = s.Commit(ctx, base, []Revision{{ID: "r3", RecordID: "c", Path: "/a", Content: "collision"}}, "three", Receipt{}, nil)
	if !errors.Is(err, ErrConflict) {
		t.Fatal("occupied path overwritten", err)
	}
}
func TestBrokerRebasesIndependentEditsButFencesPolicy(t *testing.T) {
	b, alice, bob := workspace(t, "writer")
	ctx := t.Context()
	one, _ := b.Begin(ctx, alice, "alice")
	two, _ := b.Begin(ctx, bob, "alice")
	if _, err := b.Call(ctx, one, "write", map[string]any{"path": "/project/plan", "content": "owner edit"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Call(ctx, two, "write", map[string]any{"path": "/project/research", "content": "agent research"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Finish(ctx, one, ID(), Receipt{}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Finish(ctx, two, ID(), Receipt{}); err != nil {
		t.Fatal("unrelated write conflict", err)
	}
	old, _ := b.Store.Load(ctx, "alice")
	owner, _ := b.Begin(ctx, alice, "alice")
	if _, err := b.Call(ctx, owner, "annotate", map[string]any{"path": "/project", "metadata": map[string]any{"access": map[string]any{"visibility": "closed"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Finish(ctx, owner, ID(), Receipt{}); err != nil {
		t.Fatal(err)
	}
	// Simulates revocation after Broker's last check but before Store publishes.
	_, err := b.Store.Commit(ctx, old, []Revision{{ID: ID(), RecordID: ID(), Path: "/project/late", Content: "not allowed"}}, ID(), Receipt{}, nil)
	if !errors.Is(err, ErrConflict) {
		t.Fatal("ACL changed under rebased transaction", err)
	}
}

type racingHeads struct {
	objectstore.Store
	mu      sync.Mutex
	arrived int
	release chan struct{}
}

func (r *racingHeads) Put(ctx context.Context, key string, data []byte, expected string) (string, error) {
	if strings.HasSuffix(key, "/HEAD") && expected != "*" {
		r.mu.Lock()
		r.arrived++
		n := r.arrived
		if n == 2 {
			close(r.release)
		}
		r.mu.Unlock()
		if n <= 2 {
			select {
			case <-r.release:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
	}
	return r.Store.Put(ctx, key, data, expected)
}
func TestSimultaneousHEADPublicationRetriesWithoutLosingDocuments(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	memory := teststore.New()
	s := NewStore(memory)
	base, err := s.CreateSpace(ctx, Space{ID: "team", Kind: "team", Owner: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	s.Objects = &racingHeads{Store: memory, release: make(chan struct{})}
	results := make(chan error, 2)
	for _, id := range []string{"a", "b"} {
		go func(id string) {
			_, err := s.Commit(ctx, base, []Revision{{ID: "rev-" + id, RecordID: id, Path: "/" + id, Content: id}}, "operation-"+id, Receipt{}, nil)
			results <- err
		}(id)
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	current, err := s.Load(ctx, "team")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if _, err = s.RecordByPath(ctx, current, "/"+id); err != nil {
			t.Fatal(id, err)
		}
		if _, err = s.Operation(ctx, current, "operation-"+id); err != nil {
			t.Fatal("receipt missing", id, err)
		}
	}
}

func TestRebaseFencesPrimarySourceOfSharedRead(t *testing.T) {
	b, alice, _ := workspace(t, "writer")
	ctx := t.Context()
	owner, _ := b.Begin(ctx, alice, "alice")
	metadata := accessMeta("brain-curator", "reader")
	if _, err := b.Call(ctx, owner, "annotate", map[string]any{"path": "/project", "metadata": metadata}); err != nil {
		t.Fatal(err)
	}
	source, err := b.Finish(ctx, owner, ID(), Receipt{})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := b.Store.RecordByPath(ctx, source, "/project/plan")
	if err != nil {
		t.Fatal(err)
	}
	shared, _ := b.Store.Load(ctx, "shared")
	_, err = b.Store.Commit(ctx, shared, []Revision{{ID: ID(), RecordID: ID(), Path: "/practice", Content: "derived practice", Sources: []SourceRef{{Space: "alice", Record: rec.ID, Revision: rec.Revision, Path: rec.Path, Policy: source.Space.Policy}}}}, ID(), Receipt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := b.Begin(ctx, alice, "alice")
	if _, err = b.Call(ctx, e, "read", map[string]any{"space": "shared", "path": "/practice"}); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Call(ctx, e, "write", map[string]any{"path": "/notes/derived", "content": "depends on shared practice"}); err != nil {
		t.Fatal(err)
	}
	if err = b.CheckDependencies(ctx, alice, e.Dependencies); err != nil {
		t.Fatal(err)
	}
	// Revoke between the broker dependency check and the HEAD publication.
	owner, _ = b.Begin(ctx, alice, "alice")
	if _, err = b.Call(ctx, owner, "annotate", map[string]any{"path": "/project", "metadata": map[string]any{"access": map[string]any{"visibility": "closed"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Finish(ctx, owner, ID(), Receipt{}); err != nil {
		t.Fatal(err)
	}
	revisions := []Revision{}
	for _, rev := range e.Staged {
		revisions = append(revisions, rev)
	}
	_, err = b.Store.Commit(ctx, e.Snapshots[e.Primary], revisions, ID(), Receipt{Dependencies: e.Dependencies}, nil)
	if !errors.Is(err, ErrConflict) {
		t.Fatal("rebase bypassed source revocation", err)
	}
}

func TestRebasePreservesPendingSeedMarker(t *testing.T) {
	ctx := t.Context()
	s := NewStore(teststore.New())
	base, err := s.CreateSpace(ctx, Space{ID: "shared", Kind: "shared", Owner: "system"})
	if err != nil {
		t.Fatal(err)
	}
	pending := base
	if err = s.ControlSet(ctx, &pending, "seed/library", true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Commit(ctx, base, []Revision{{ID: "other-rev", RecordID: "other", Path: "/other", Content: "other"}}, "other", Receipt{}, nil); err != nil {
		t.Fatal(err)
	}
	accepted, err := s.Commit(ctx, pending, []Revision{{ID: "seed-rev", RecordID: "seed", Path: "/skills/example", Content: "skill"}}, "seed", Receipt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var marker bool
	if err = s.ControlGet(ctx, accepted, "seed/library", &marker); err != nil || !marker {
		t.Fatal("seed marker lost during rebase", err)
	}
	if _, err = s.RecordByPath(ctx, accepted, "/other"); err != nil {
		t.Fatal("concurrent document lost", err)
	}
}

// A read whose document only gained newer content (same path, not deleted)
// does not fence a rebase: the agent reasoned about an immutable revision,
// and its write set is disjoint. Structural moves of read records still fence.
func TestRebaseAllowsNewerContentOnReadDependencies(t *testing.T) {
	b, alice, bob := workspace(t, "writer")
	ctx := t.Context()
	one, _ := b.Begin(ctx, alice, "alice")
	if _, err := b.Call(ctx, one, "write", map[string]any{"path": "/project/doc", "content": "v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Finish(ctx, one, "seed", Receipt{}); err != nil {
		t.Fatal(err)
	}
	reader, _ := b.Begin(ctx, bob, "alice")
	if _, err := b.Call(ctx, reader, "read", map[string]any{"path": "/project/doc"}); err != nil {
		t.Fatal(err)
	}
	writer, _ := b.Begin(ctx, alice, "alice")
	if _, err := b.Call(ctx, writer, "write", map[string]any{"path": "/project/doc", "content": "v2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Finish(ctx, writer, "update", Receipt{}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Call(ctx, reader, "write", map[string]any{"path": "/project/other", "content": "note"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Finish(ctx, reader, "note", Receipt{}); err != nil {
		t.Fatal("disjoint write fenced by newer content on a read dependency", err)
	}
	snap, _ := b.Store.Load(ctx, "alice")
	rec, err := b.Store.RecordByPath(ctx, snap, "/project/other")
	if err != nil {
		t.Fatal(err)
	}
	rev, _ := b.Store.ReadRevision(ctx, "alice", rec.Revision)
	if rev.Content != "note" || rec.ID == "" {
		t.Fatal(rev)
	}
}
