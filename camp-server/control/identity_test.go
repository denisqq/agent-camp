package control

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/denisqq/agent-camp/camp-server/camp"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "control.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestUserOwnsMultipleAgents(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	user := camp.User{ID: "user-1", DisplayName: "alice", CreatedAt: time.Now()}
	if err := s.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"codex-laptop", "claude-laptop"} {
		agent := camp.Agent{ID: "agent-" + string(rune('a'+i)), OwnerUserID: "user-1", DisplayName: name, Client: name, CreatedAt: time.Now(), Status: "active"}
		if err := s.CreateAgent(ctx, agent); err != nil {
			t.Fatal(err)
		}
	}
	agents, err := s.AgentsByOwner(ctx, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 2 {
		t.Fatalf("want 2 agents, got %d", len(agents))
	}
	// Disabling one agent leaves the sibling usable.
	one := agents[0]
	one.Status = "revoked"
	if err := s.SaveAgent(ctx, one); err != nil {
		t.Fatal(err)
	}
	other, err := s.Agent(ctx, agents[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if other.Status != "active" {
		t.Fatalf("sibling agent affected: %+v", other)
	}
}

func TestNamesDoNotMergeUsers(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	for _, id := range []string{"user-1", "user-2"} {
		if err := s.CreateUser(ctx, camp.User{ID: id, DisplayName: "same-laptop-name", CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	u1, err := s.User(ctx, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if u1.DisplayName != "same-laptop-name" || u1.ID != "user-1" {
		t.Fatalf("user-1: %+v", u1)
	}
	if _, err = s.User(ctx, "user-3"); err != camp.ErrNotFound {
		t.Fatalf("unknown user must be ErrNotFound, got %v", err)
	}
}

func TestIdentityUpgradePreservesExistingPrincipals(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := camp.Principal{Subject: "brain-curator", Role: "curator", Spaces: []string{"shared"}, Scopes: []string{"read", "propose"}, Expires: time.Now().Add(time.Hour), TokenHash: "legacy-hash"}
	if err = s.SavePrincipal(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Reopen on the same file: additive migrations must keep the legacy
	// subject and its grants working.
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got, err := s2.Principal(ctx, "legacy-hash")
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != "brain-curator" || got.Role != "curator" {
		t.Fatalf("legacy principal changed: %+v", got)
	}
	// And the new tables exist and work.
	if err = s2.CreateUser(ctx, camp.User{ID: "user-9", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

func TestAgentRequiresExistingOwner(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	agent := camp.Agent{ID: "agent-orphan", OwnerUserID: "user-missing", CreatedAt: time.Now(), Status: "active"}
	if err := s.CreateAgent(ctx, agent); err == nil {
		t.Fatal("agent without owner must be rejected")
	}
}

func TestIdentityRecordsExcludedFromWorkspaceBackup(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	if err := s.CreateUser(ctx, camp.User{ID: "user-backup", DisplayName: "backup-probe", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAgent(ctx, camp.Agent{ID: "agent-backup", OwnerUserID: "user-backup", DisplayName: "backup-probe-agent", CreatedAt: time.Now(), Status: "active"}); err != nil {
		t.Fatal(err)
	}
	// The control database is not workspace memory: identity records live in
	// SQLite and are reachable only through this store. The end-to-end proof
	// that workspace backups carry none of them lives in
	// internal/camp/backup_identity_test.go.
	if _, err := s.User(ctx, "user-backup"); err != nil {
		t.Fatal(err)
	}
}

func TestDuplicateIdentityReturnsConflictWithoutReplacing(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	original := camp.User{ID: "owner", DisplayName: "original"}
	if err := s.CreateUser(ctx, original); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateUser(ctx, camp.User{ID: "owner", DisplayName: "replacement"}); !errors.Is(err, camp.ErrConflict) {
		t.Fatalf("duplicate user: %v", err)
	}
	got, err := s.User(ctx, "owner")
	if err != nil || got.DisplayName != "original" {
		t.Fatalf("existing identity replaced: %+v %v", got, err)
	}
	a := camp.Agent{ID: "agent", OwnerUserID: "owner", DisplayName: "original", Status: "active"}
	if err := s.CreateAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAgent(ctx, a); !errors.Is(err, camp.ErrConflict) {
		t.Fatalf("duplicate agent: %v", err)
	}
}
