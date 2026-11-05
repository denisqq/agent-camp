package camp

import (
	"context"
	"testing"
	"time"
)

func registrationFixture(t *testing.T, mode string) (context.Context, *RegistrationService, *Store, *MemState) {
	t.Helper()
	ctx, b, _, _ := setupBroker(t)
	state := NewMemState()
	auth := NewAuth(state)
	auth.Identities = state
	workspace := "workspace"
	if _, err := b.Store.CreateSpace(ctx, Space{ID: workspace, Kind: "team", Owner: "system"}); err != nil && err != ErrConflict {
		t.Fatal(err)
	}
	svc := &RegistrationService{Auth: auth, Identities: state, Store: b.Store, Policy: RegistrationPolicy{Mode: mode, StarterWorkspace: workspace}}
	return ctx, svc, b.Store, state
}

func TestRegistrationDisabled(t *testing.T) {
	ctx, svc, _, _ := registrationFixture(t, "disabled")
	if _, err := svc.Register(ctx, nil, RegisterRequest{DisplayName: "codex"}); err != ErrRegistrationDisabled {
		t.Fatalf("want ErrRegistrationDisabled, got %v", err)
	}
}

func TestOpenRegistrationCreatesDistinctUsers(t *testing.T) {
	ctx, svc, store, state := registrationFixture(t, "open")
	first, err := svc.Register(ctx, nil, RegisterRequest{DisplayName: "same-name"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Register(ctx, nil, RegisterRequest{DisplayName: "same-name"})
	if err != nil {
		t.Fatal(err)
	}
	if first.UserID == second.UserID || first.AgentID == second.AgentID {
		t.Fatalf("matching names merged identities: %+v vs %+v", first, second)
	}
	// Both agents can act in the starter workspace with their own tokens.
	svc.Auth.Identities = state
	if _, err = svc.Auth.Authenticate(ctx, first.Token); err != nil {
		t.Fatalf("first token unusable: %v", err)
	}
	if _, err = svc.Auth.Authenticate(ctx, second.Token); err != nil {
		t.Fatalf("second token unusable: %v", err)
	}
	snap, err := store.Load(ctx, "workspace")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Space.Access == nil || len(snap.Space.Access.Share) != 2 {
		t.Fatalf("expected both grants committed, got %+v", snap.Space.Access)
	}
}

func TestRegistrationCannotChooseOwnerOrScopes(t *testing.T) {
	ctx, svc, store, _ := registrationFixture(t, "disabled")
	// Owner provisioning: a user context exists; request is just a name.
	if err := svc.Identities.CreateUser(ctx, User{ID: "user-owner", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	reg, err := svc.Register(ctx, &UserContext{User: User{ID: "user-owner"}}, RegisterRequest{DisplayName: "codex-laptop"})
	if err != nil {
		t.Fatal(err)
	}
	if reg.UserID != "user-owner" {
		t.Fatalf("owner not respected: %+v", reg)
	}
	p, err := svc.Auth.Authenticate(ctx, reg.Token)
	if err != nil {
		t.Fatal(err)
	}
	if p.Role != "writer" || len(p.Spaces) != 1 || p.Spaces[0] != "workspace" {
		t.Fatalf("credential must carry only the server-chosen grant: %+v", p)
	}
	if p.AgentID != reg.AgentID {
		t.Fatalf("credential not agent-bound: %+v", p)
	}
	// RegisterRequest has no owner/role/space fields — compile-time
	// guarantee; assert the JSON contract rejects unknown fields implicitly
	// at the API layer (see internal/api/registration_test.go).
	_ = store
}

func TestKnownNameCannotRecoverCredentials(t *testing.T) {
	ctx, svc, _, state := registrationFixture(t, "open")
	first, err := svc.Register(ctx, nil, RegisterRequest{DisplayName: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	// A later registration with the same name creates a distinct identity
	// and never returns the first token.
	again, err := svc.Register(ctx, nil, RegisterRequest{DisplayName: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if again.Token == first.Token || again.AgentID == first.AgentID {
		t.Fatal("name reuse recovered the original identity")
	}
	// Disabled user: no new registrations for that user.
	u, err := state.User(ctx, first.UserID)
	if err != nil {
		t.Fatal(err)
	}
	u.Disabled = true
	if err = state.SaveUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Register(ctx, &UserContext{User: User{ID: first.UserID}}, RegisterRequest{DisplayName: "another"}); err != ErrDenied {
		t.Fatalf("disabled user registered an agent: %v", err)
	}
}

func TestPendingRegistrationGrantFailure(t *testing.T) {
	ctx, svc, store, state := registrationFixture(t, "disabled")
	if err := svc.Identities.CreateUser(ctx, User{ID: "user-p", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// Simulate a grant failure by pointing the service at a workspace that
	// does not exist.
	svc.Policy.StarterWorkspace = "missing-space"
	if _, err := svc.Register(ctx, &UserContext{User: User{ID: "user-p"}}, RegisterRequest{DisplayName: "codex"}); err == nil {
		t.Fatal("registration must fail when the grant cannot commit")
	}
	// The agent stays pending; no usable credential exists.
	agents, err := state.AgentsByOwner(ctx, "user-p")
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || agents[0].Status != AgentPending {
		t.Fatalf("want one pending agent, got %+v", agents)
	}
	if _, err = svc.Auth.Authenticate(ctx, agents[0].ID); err == nil {
		t.Fatal("pending agent must not authenticate")
	}
	// No membership was added anywhere.
	snap, err := store.Load(ctx, "workspace")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Space.Access != nil && len(snap.Space.Access.Share) > 0 {
		t.Fatalf("grant leaked despite failure: %+v", snap.Space.Access)
	}

	// Repair and retry: the pending registration resumes and completes.
	svc.Policy.StarterWorkspace = "workspace"
	reg, err := svc.Register(ctx, &UserContext{User: User{ID: "user-p"}}, RegisterRequest{DisplayName: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if reg.AgentID != agents[0].ID {
		t.Fatalf("resume created a duplicate agent: %s vs %s", reg.AgentID, agents[0].ID)
	}
	if _, err = svc.Auth.Authenticate(ctx, reg.Token); err != nil {
		t.Fatalf("resumed credential unusable: %v", err)
	}
}
