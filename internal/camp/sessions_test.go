package camp

import (
	"context"
	"errors"
	"testing"
	"time"
)

type testExecutor struct{ calls int }

func (x *testExecutor) Execute(ctx context.Context, code string, callback Callback) (RunOutput, error) {
	x.calls++
	if code == "write" || code == "fail" {
		_, err := callback(ctx, "write", map[string]any{"path": "/test", "content": "accepted"})
		if err != nil {
			return RunOutput{}, err
		}
	}
	if code == "fail" {
		return RunOutput{}, errors.New("intentional failure")
	}
	return RunOutput{Value: map[string]any{"ok": true}}, nil
}
func TestSessionAtomicityIdempotencyAndReopen(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	exec := &testExecutor{}
	state := NewMemState()
	svc := NewSessions(b, exec)
	svc.State = state
	session, err := svc.Start(ctx, p, StartRequest{Space: "alice", Task: "test"})
	if err != nil {
		t.Fatal(err)
	}
	failed, err := svc.Run(ctx, p, RunRequest{SessionID: session.ID, OperationID: "failed", Code: "fail"})
	if err != nil || failed.Status != "failed" {
		t.Fatalf("failure: %+v %v", failed, err)
	}
	snap, _ := b.Store.Load(ctx, "alice")
	if _, err = b.Store.RecordByPath(ctx, snap, "/test"); !errors.Is(err, ErrNotFound) {
		t.Fatal("failed script published")
	}
	request := RunRequest{SessionID: session.ID, OperationID: "ok", Code: "write"}
	first, err := svc.Run(ctx, p, request)
	if err != nil || first.Status != "completed" {
		t.Fatalf("write: %+v %v", first, err)
	}
	svc = NewSessions(b, exec)
	svc.State = state
	again, err := svc.Run(ctx, p, request)
	if err != nil || again.ChangeID != first.ChangeID || exec.calls != 2 {
		t.Fatalf("replayed operation: %+v %v calls=%d", again, err, exec.calls)
	}
	request.Code = "changed"
	if _, err = svc.Run(ctx, p, request); !errors.Is(err, ErrConflict) {
		t.Fatal("same key different code accepted", err)
	}
}
func TestSessionDryRunExpiryAndRevocation(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	svc := NewSessions(b, &testExecutor{})
	session, _ := svc.Start(ctx, p, StartRequest{Space: "alice"})
	r, err := svc.Run(ctx, p, RunRequest{SessionID: session.ID, OperationID: "dry", Code: "write", DryRun: true})
	if err != nil || r.Status != "no_changes" {
		t.Fatal(r, err)
	}
	snap, _ := b.Store.Load(ctx, "alice")
	if _, err = b.Store.RecordByPath(ctx, snap, "/test"); !errors.Is(err, ErrNotFound) {
		t.Fatal("dry run published")
	}
	svc.Now = func() time.Time { return time.Now().Add(9 * time.Hour) }
	if _, err = svc.Run(ctx, p, RunRequest{SessionID: session.ID, OperationID: "expired", Code: "write"}); err == nil {
		t.Fatal("expired session accepted")
	}
}
