package control

import (
	"github.com/datretyakov/agent-camp/internal/camp"
	"context"
	"errors"
	"testing"
)

func setup(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir() + "/control.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPrincipalsRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := setup(t)
	p := camp.Principal{Subject: "agent-one", Role: "author", TokenHash: "a" + string(make([]byte, 63))}
	if _, err := s.Principal(ctx, p.TokenHash); !errors.Is(err, camp.ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.SavePrincipal(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := s.Principal(ctx, p.TokenHash)
	if err != nil || got.Subject != "agent-one" {
		t.Fatal(got, err)
	}
}

func TestSessionAndExecutionConcurrencyTags(t *testing.T) {
	ctx := context.Background()
	s := setup(t)
	st := camp.SessionState{ID: "sess", Principal: "hash", Primary: "workspace"}
	tag, err := s.SaveSession(ctx, &st, "")
	if err != nil || tag != "1" {
		t.Fatal(tag, err)
	}
	if _, err = s.SaveSession(ctx, &camp.SessionState{ID: "sess"}, ""); !errors.Is(err, camp.ErrConflict) {
		t.Fatal("duplicate create accepted", err)
	}
	st.Active = "op"
	tag, err = s.SaveSession(ctx, &st, "1")
	if err != nil || tag != "2" {
		t.Fatal(tag, err)
	}
	if _, err = s.SaveSession(ctx, &st, "1"); !errors.Is(err, camp.ErrConflict) {
		t.Fatal("stale tag accepted", err)
	}
	etag, err := s.CreateExecution(ctx, "executions/sess/op", camp.ExecutionState{Receipt: camp.Receipt{Status: "pending"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateExecution(ctx, "executions/sess/op", camp.ExecutionState{}); !errors.Is(err, camp.ErrConflict) {
		t.Fatal("duplicate execution accepted", err)
	}
	if err = s.SaveExecution(ctx, "executions/sess/op", camp.ExecutionState{Receipt: camp.Receipt{Status: "completed"}}, "wrong"); !errors.Is(err, camp.ErrConflict) {
		t.Fatal("stale execution tag accepted", err)
	}
	if err = s.SaveExecution(ctx, "executions/sess/op", camp.ExecutionState{Receipt: camp.Receipt{Status: "completed"}}, etag); err != nil {
		t.Fatal(err)
	}
}
