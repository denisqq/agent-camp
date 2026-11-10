package camp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestStartExpectedSnapshot(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	svc := NewSessions(b, &testExecutor{})
	state := NewMemState()
	svc.State = state
	page, err := b.Map(ctx, p, MapRequest{Space: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	mapped := page["snapshot"].(string)
	start := func(snapshot, sessionID string) (SessionView, error) {
		t.Helper()
		// Decode the public wire contract so this test also catches a lost JSON field.
		raw, _ := json.Marshal(map[string]string{"primary_space": "alice", "expected_snapshot": snapshot, "session_id": sessionID})
		var req StartRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Fatal(err)
		}
		return svc.Start(ctx, p, req)
	}
	matched, err := start(mapped, "")
	if err != nil || matched.Snapshot != mapped || matched.ID == "" {
		t.Fatalf("matching snapshot: %+v %v", matched, err)
	}
	writer, err := b.Begin(ctx, p, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Call(ctx, writer, "write", map[string]any{"path": "/new", "content": "new head"}); err != nil {
		t.Fatal(err)
	}
	advanced, err := b.Finish(ctx, writer, "advance", Receipt{})
	if err != nil || advanced.Commit == mapped {
		t.Fatalf("advance: %+v %v", advanced, err)
	}
	for _, snapshot := range []string{mapped, "unknown-snapshot"} {
		before := len(state.sessions)
		view, err := start(snapshot, "")
		if !errors.Is(err, ErrConflict) || view.ID != "" || len(state.sessions) != before {
			t.Errorf("stale snapshot saved a session: %+v %v count=%d want=%d", view, err, len(state.sessions), before)
		}
	}
	fresh, err := start(advanced.Commit, "")
	if err != nil || fresh.Snapshot != advanced.Commit {
		t.Fatalf("fresh handoff: %+v %v", fresh, err)
	}
	current, err := svc.Start(ctx, p, StartRequest{Space: "alice"})
	if err != nil || current.Snapshot != advanced.Commit {
		t.Fatalf("omitted precondition: %+v %v", current, err)
	}
	for _, snapshot := range []string{mapped, advanced.Commit} {
		before := len(state.sessions)
		if _, err := start(snapshot, matched.ID); !errors.Is(err, ErrInvalid) {
			t.Errorf("resume with precondition: %v", err)
		}
		if len(state.sessions) != before {
			t.Fatal("invalid resume created session")
		}
	}
	resumed, err := svc.Start(ctx, p, StartRequest{SessionID: matched.ID})
	if err != nil || resumed.ID != matched.ID || resumed.Snapshot != mapped {
		t.Fatalf("resume must retain original snapshot after HEAD advance: %+v %v", resumed, err)
	}
	foreign, err := b.Store.Load(ctx, "bob")
	if err != nil {
		t.Fatal(err)
	}
	before := len(state.sessions)
	if _, err := svc.Start(ctx, p, StartRequest{Space: "bob", ExpectedSnapshot: foreign.Commit}); !errors.Is(err, ErrDenied) {
		t.Fatalf("snapshot must not grant access: %v", err)
	}
	if len(state.sessions) != before {
		t.Fatal("denied handoff saved a session")
	}
}

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

// Identity/ownership checks on credential validation (control-plane users
// and agents; see internal/camp/identity.go).
func agentPrincipal(t *testing.T, state *MemState, token, agentID, subject string) Principal {
	t.Helper()
	p := Principal{Subject: subject, Role: "writer", Spaces: []string{"workspace"}, Scopes: []string{"read", "write", "propose"}, Expires: time.Now().Add(time.Hour), TokenHash: digest(token), AgentID: agentID}
	if err := state.SavePrincipal(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDisabledOwnerDeniesAgents(t *testing.T) {
	ctx := context.Background()
	state := NewMemState()
	auth := NewAuth(state)
	auth.Identities = state

	mkUser := func(id string) User {
		u := User{ID: id, DisplayName: id, CreatedAt: time.Now()}
		if err := state.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		return u
	}
	mkUser("user-a")
	mkUser("user-b")
	mkAgent := func(id, owner string) Agent {
		a := Agent{ID: id, OwnerUserID: owner, CreatedAt: time.Now(), Status: "active"}
		if err := state.CreateAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
		return a
	}
	mkAgent("agent-a1", "user-a")
	mkAgent("agent-a2", "user-a")
	mkAgent("agent-b1", "user-b")

	agentPrincipal(t, state, "token-a1", "agent-a1", "agent-a1")
	agentPrincipal(t, state, "token-a2", "agent-a2", "agent-a2")
	agentPrincipal(t, state, "token-b1", "agent-b1", "agent-b1")

	if _, err := auth.Authenticate(ctx, "token-a1"); err != nil {
		t.Fatalf("token-a1 should work before disable: %v", err)
	}

	// After disabling user A both of A's agents are denied; user B is not.
	u, err := state.User(ctx, "user-a")
	if err != nil {
		t.Fatal(err)
	}
	u.Disabled = true
	if err = state.SaveUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if _, err = auth.Authenticate(ctx, "token-a1"); err != ErrDenied {
		t.Fatalf("token-a1 after owner disable: %v", err)
	}
	if _, err = auth.Authenticate(ctx, "token-a2"); err != ErrDenied {
		t.Fatalf("token-a2 after owner disable: %v", err)
	}
	if _, err = auth.Authenticate(ctx, "token-b1"); err != nil {
		t.Fatalf("token-b1 must keep working: %v", err)
	}

	// Documents authored by a disabled user's agent remain readable: the
	// workspace store is untouched; denial is authentication-level only.
	// (Reading is exercised through the broker in workspace tests.)
}

func TestRevokedAgentDeniedWhileSiblingWorks(t *testing.T) {
	ctx := context.Background()
	state := NewMemState()
	auth := NewAuth(state)
	auth.Identities = state
	if err := state.CreateUser(ctx, User{ID: "user-x", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"agent-x1", "agent-x2"} {
		if err := state.CreateAgent(ctx, Agent{ID: id, OwnerUserID: "user-x", CreatedAt: time.Now(), Status: "active"}); err != nil {
			t.Fatal(err)
		}
		agentPrincipal(t, state, "tok-"+id, id, id)
	}
	revoked, err := state.Agent(ctx, "agent-x1")
	if err != nil {
		t.Fatal(err)
	}
	revoked.Status = "revoked"
	if err = state.SaveAgent(ctx, revoked); err != nil {
		t.Fatal(err)
	}
	if _, err = auth.Authenticate(ctx, "tok-agent-x1"); err != ErrDenied {
		t.Fatalf("revoked agent accepted: %v", err)
	}
	if _, err = auth.Authenticate(ctx, "tok-agent-x2"); err != nil {
		t.Fatalf("sibling denied: %v", err)
	}
}

func TestLegacyPrincipalsUnaffectedByIdentityStore(t *testing.T) {
	ctx := context.Background()
	state := NewMemState()
	auth := NewAuth(state)
	auth.Identities = state
	legacy := Principal{Subject: "brain-curator", Role: "curator", Spaces: []string{"shared"}, Scopes: []string{"read", "propose"}, Expires: time.Now().Add(time.Hour), TokenHash: digest("legacy-token")}
	if err := state.SavePrincipal(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Authenticate(ctx, "legacy-token"); err != nil {
		t.Fatalf("legacy subject must authenticate unchanged: %v", err)
	}
}
