package camp

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Identity records are control-plane state: a workspace backup must never
// carry users, agents, or credentials, even when they exist and are active.
func TestIdentityRecordsExcludedFromWorkspaceBackup(t *testing.T) {
	ctx := context.Background()
	_, b, _, _ := setupBroker(t)
	state := NewMemState()
	state.CreateUser(ctx, User{ID: "user-backup-probe", DisplayName: "unique-backup-user-9f1", CreatedAt: time.Now()})
	state.CreateAgent(ctx, Agent{ID: "agent-backup-probe", OwnerUserID: "user-backup-probe", DisplayName: "unique-backup-agent-9f1", CreatedAt: time.Now(), Status: "active"})

	var archive strings.Builder
	if err := b.Store.Backup(ctx, &archive); err != nil {
		t.Fatal(err)
	}
	body := archive.String()
	for _, probe := range []string{"unique-backup-user-9f1", "unique-backup-agent-9f1"} {
		if strings.Contains(body, probe) {
			t.Fatalf("workspace backup leaked identity record %q", probe)
		}
	}
}
