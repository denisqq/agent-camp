package camp

import (
	"context"
	"errors"
	"github.com/denisqq/agent-camp/internal/teststore"
	"testing"
)

func TestCommitVacatesPathBeforeOccupation(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	e, _ := b.Begin(ctx, p, "alice")
	if _, err := b.Call(ctx, e, "write", map[string]any{"path": "/a", "content": "old"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Finish(ctx, e, "seed", Receipt{}); err != nil {
		t.Fatal(err)
	}
	// Delete plus recreate at the same path in one changeset must not depend on
	// the random record id ordering inside Commit.
	for i := range 10 {
		op := Receipt{}
		e, _ = b.Begin(ctx, p, "alice")
		if _, err := b.Call(ctx, e, "delete", map[string]any{"path": "/a"}); err != nil {
			t.Fatal(err)
		}
		if _, err := b.Call(ctx, e, "write", map[string]any{"path": "/a", "content": "recreated"}); err != nil {
			t.Fatal(err)
		}
		if _, err := b.Finish(ctx, e, "swap", op); err != nil {
			t.Fatal("iteration", i, err)
		}
	}
	snap, _ := b.Store.Load(ctx, "alice")
	rec, err := b.Store.RecordByPath(ctx, snap, "/a")
	if err != nil {
		t.Fatal(err)
	}
	rev, err := b.Store.ReadRevision(ctx, "alice", rec.Revision)
	if err != nil || rev.Content != "recreated" {
		t.Fatal(rev, err)
	}
}

func TestCommitOrderIndependentOfRecordID(t *testing.T) {
	ctx := context.Background()
	s := NewStore(teststore.New())
	snap, err := s.CreateSpace(ctx, Space{ID: "sp", Kind: "personal", Owner: "a"})
	if err != nil {
		t.Fatal(err)
	}
	snap, err = s.Commit(ctx, snap, []Revision{{ID: "r1", RecordID: "zzz-old", Path: "/a", Kind: "note", Content: "old", Actor: "t"}}, "seed", Receipt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// aaa-new sorts before zzz-old: without vacate-first ordering the new
	// record would collide with the still-live old path.
	revisions := []Revision{
		{ID: "r2", RecordID: "aaa-new", Path: "/a", Kind: "note", Content: "new", Actor: "t"},
		{ID: "r3", RecordID: "zzz-old", Path: "/a", Kind: "note", Content: "old", Actor: "t", Deleted: true, Parent: "r1"},
	}
	next, err := s.Commit(ctx, snap, revisions, "swap", Receipt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := s.RecordByPath(ctx, next, "/a")
	if err != nil || rec.ID != "aaa-new" {
		t.Fatalf("path not handed over: %+v %v", rec, err)
	}
}

func TestCommitRejectsPathSwapCycle(t *testing.T) {
	ctx := context.Background()
	s := NewStore(teststore.New())
	snap, err := s.CreateSpace(ctx, Space{ID: "sp", Kind: "personal", Owner: "a"})
	if err != nil {
		t.Fatal(err)
	}
	snap, err = s.Commit(ctx, snap, []Revision{
		{ID: "p1", RecordID: "rec-a", Path: "/a", Kind: "note", Content: "a", Actor: "t"},
		{ID: "p2", RecordID: "rec-b", Path: "/b", Kind: "note", Content: "b", Actor: "t"},
	}, "seed", Receipt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	swap := []Revision{
		{ID: "p3", RecordID: "rec-a", Path: "/b", Kind: "note", Content: "a", Actor: "t", Parent: "p1"},
		{ID: "p4", RecordID: "rec-b", Path: "/a", Kind: "note", Content: "b", Actor: "t", Parent: "p2"},
	}
	if _, err = s.Commit(ctx, snap, swap, "cycle", Receipt{}, nil); !errors.Is(err, ErrConflict) {
		t.Fatal("swap cycle accepted", err)
	}
}

func TestListDirectoryExcludesSiblingStringPrefixes(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	e, _ := b.Begin(ctx, p, "alice")
	for _, path := range []string{"/goals/monthly", "/goals2/other", "/goals"} {
		if _, err := b.Call(ctx, e, "write", map[string]any{"path": path, "content": "x"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Finish(ctx, e, "seed", Receipt{}); err != nil {
		t.Fatal(err)
	}
	e, _ = b.Begin(ctx, p, "alice")
	result, err := b.Call(ctx, e, "ls", map[string]any{"path": "/goals"})
	if err != nil {
		t.Fatal(err)
	}
	items := result.(map[string]any)["items"].([]Record)
	if len(items) != 2 || items[0].Path != "/goals" || items[1].Path != "/goals/monthly" {
		paths := []string{}
		for _, rec := range items {
			paths = append(paths, rec.Path)
		}
		t.Fatal("sibling prefix leaked into directory listing", paths)
	}
	root, err := b.Call(ctx, e, "ls", map[string]any{"path": "/"})
	if err != nil {
		t.Fatal(err)
	}
	if items := root.(map[string]any)["items"].([]Record); len(items) != 3 {
		t.Fatal("root listing incomplete", len(items))
	}
}
