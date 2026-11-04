package camp

import (
	"github.com/denisqq/agent-camp/internal/teststore"
	"context"
	"errors"
	"testing"
)

func TestChangesetConflictHistoryAndRecovery(t *testing.T) {
	ctx := context.Background()
	objects := teststore.New()
	s := NewStore(objects)
	first, err := s.CreateSpace(ctx, Space{ID: "alice", Kind: "personal", Owner: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	rev := Revision{ID: "r1", RecordID: "doc1", Path: "/goals/growth", Kind: "goal", Content: "first", Metadata: map[string]any{"status": "active"}}
	accepted, err := s.Commit(ctx, first, []Revision{rev}, "op1", Receipt{Status: "completed", Result: map[string]any{"ok": true}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Commit(ctx, first, []Revision{{ID: "r2", RecordID: "doc2", Path: "/goals/growth", Kind: "note", Content: "lost"}}, "op2", Receipt{}, nil)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("overlapping changeset accepted: %v", err)
	}
	s = NewStore(objects)
	current, err := s.Load(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := s.Operation(ctx, current, "op1")
	if err != nil || receipt.Status != "completed" {
		t.Fatalf("lost acknowledgement recovery: %+v %v", receipt, err)
	}
	rev.ID = "r3"
	rev.Content = "second"
	rev.Parent = "r1"
	current, err = s.Commit(ctx, current, []Revision{rev}, "op3", Receipt{Status: "completed"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.ReadRevision(ctx, "alice", "r1")
	if err != nil || old.Content != "first" {
		t.Fatalf("history lost: %+v %v", old, err)
	}
	rec, err := s.RecordByPath(ctx, current, "/goals/growth")
	if err != nil || rec.ID != "doc1" || rec.Revision != "r3" {
		t.Fatalf("stable id: %+v %v", rec, err)
	}
	if record, err := s.RecordByPath(ctx, accepted, "/goals/growth"); err != nil || record.Revision != "r1" {
        t.Fatal("old snapshot changed", err)
    }
}
