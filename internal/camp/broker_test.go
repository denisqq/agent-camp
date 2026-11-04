package camp

import (
	"github.com/denisqq/agent-camp/internal/teststore"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func setupBroker(t *testing.T) (context.Context, *Broker, Principal, string) {
	t.Helper()
	ctx := context.Background()
	s := NewStore(teststore.New())
	a := NewAuth(NewMemState())
	if err := a.Bootstrap(ctx, "admin-test-token-32-characters-long"); err != nil {
		t.Fatal(err)
	}
	admin, err := a.Authenticate(ctx, "admin-test-token-32-characters-long")
	if err != nil {
		t.Fatal(err)
	}
	for _, sp := range []Space{{ID: "alice", Kind: "personal", Owner: "alice"}, {ID: "bob", Kind: "personal", Owner: "bob"}, {ID: "shared", Kind: "shared", Owner: "system"}} {
		if _, err = s.CreateSpace(ctx, sp); err != nil {
			t.Fatal(err)
		}
	}
	token, err := a.Issue(ctx, admin, Principal{Subject: "alice", Role: "author", Spaces: []string{"alice", "shared"}, Scopes: []string{"read", "write", "propose"}, Expires: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	alice, err := a.Authenticate(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, NewBroker(s, a), alice, token
}
func TestBrokerIsolationAndAtomicStage(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	e, err := b.Begin(ctx, p, "alice")
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.Call(ctx, e, "write", map[string]any{"path": "/notes/a", "content": "private", "kind": "custom", "metadata": map[string]any{"outcome": "failure"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.Call(ctx, e, "read", map[string]any{"path": "/notes/a"})
	if err != nil || got.(map[string]any)["content"] != "private" {
		t.Fatalf("read staging: %v %v", got, err)
	}
	current, _ := b.Store.Load(ctx, "alice")
	if _, err = b.Store.RecordByPath(ctx, current, "/notes/a"); !errors.Is(err, ErrNotFound) {
		t.Fatal("staging visible")
	}
	if _, err = b.Call(ctx, e, "read", map[string]any{"space": "bob", "path": "/secret"}); !errors.Is(err, ErrDenied) {
		t.Fatalf("foreign read: %v", err)
	}
	sharedExec, err := b.Begin(ctx, p, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Call(ctx, sharedExec, "write", map[string]any{"path": "/illegal", "content": "x"}); !errors.Is(err, ErrDenied) {
		t.Fatalf("author can write shared: %v", err)
	}
	if _, err = b.Call(ctx, e, "write", map[string]any{"path": "/x/../escape", "content": "bad"}); !errors.Is(err, ErrInvalid) {
		t.Fatal("path traversal", err)
	}
	if _, err = b.Finish(ctx, e, "op", Receipt{Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	current, _ = b.Store.Load(ctx, "alice")
	rec, err := b.Store.RecordByPath(ctx, current, "/notes/a")
	if err != nil || rec.Kind != "custom" {
		t.Fatalf("commit missing: %+v %v", rec, err)
	}
}
func TestRevocationAppliesToStagedWritesAndHistory(t *testing.T) {
	ctx, b, p, token := setupBroker(t)
	e, _ := b.Begin(ctx, p, "alice")
	_, err := b.Call(ctx, e, "write", map[string]any{"path": "/secret", "content": "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Auth.Revoke(ctx, p, p.TokenHash); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Auth.Authenticate(ctx, token); !errors.Is(err, ErrDenied) {
		t.Fatal("revoked token works", err)
	}
	if _, err = b.Finish(ctx, e, "op", Receipt{}); !errors.Is(err, ErrDenied) {
		t.Fatal("revoked execution committed", err)
	}
}
func TestHistoryMoveRevertAndClosedSpace(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	e, _ := b.Begin(ctx, p, "alice")
	b.Call(ctx, e, "write", map[string]any{"path": "/a", "content": "one"})
	if _, err := b.Finish(ctx, e, "a", Receipt{Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	snap, _ := b.Store.Load(ctx, "alice")
	old, _ := b.Store.RecordByPath(ctx, snap, "/a")
	e, _ = b.Begin(ctx, p, "alice")
	if _, err := b.Call(ctx, e, "move", map[string]any{"path": "/a", "to": "/b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Finish(ctx, e, "b", Receipt{Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	snap, _ = b.Store.Load(ctx, "alice")
	moved, _ := b.Store.RecordByPath(ctx, snap, "/b")
	if old.ID != moved.ID {
		t.Fatal("move changed identity")
	}
	e, _ = b.Begin(ctx, p, "alice")
	if _, err := b.Call(ctx, e, "revert", map[string]any{"path": "/b", "revision": old.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Finish(ctx, e, "c", Receipt{Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	curator := Principal{Subject: "curator", Role: "curator", Spaces: []string{"alice"}, Scopes: []string{"read", "collective:process"}}
	if err := b.AuthorizePath(ctx, curator, snap, "/a", "read"); !errors.Is(err, ErrDenied) {
		t.Fatal("curator has unshared space access", err)
	}
}

func TestCachedViewRechecksSourceGrant(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	source, _ := b.Store.Load(ctx, "bob")
	space := source.Space
	space.Access = &Access{Share: []ShareEntry{{Subject: "brain-curator", Role: "reader"}}}
	source, err := b.Store.Commit(ctx, source, []Revision{{ID: "source-rev", RecordID: "source", Path: "/case", Content: "private"}}, ID(), Receipt{}, &space)
	if err != nil {
		t.Fatal(err)
	}
	shared, _ := b.Store.Load(ctx, "shared")
	_, err = b.Store.Commit(ctx, shared, []Revision{{ID: "view-rev", RecordID: "view", Path: "/practice", Content: "generalized", Sources: []SourceRef{{Space: "bob", Record: "source", Revision: "source-rev", Policy: source.Space.Policy, Path: "/case"}}}}, ID(), Receipt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := b.Begin(ctx, p, "alice")
	if _, err = b.Call(ctx, e, "read", map[string]any{"space": "shared", "path": "/practice"}); err != nil {
		t.Fatal(err)
	}
	if err = b.CheckDependencies(ctx, p, e.Dependencies); err != nil {
		t.Fatal(err)
	}
	space = source.Space
	space.Access = nil
	if _, err = b.Store.Commit(ctx, source, nil, ID(), Receipt{}, &space); err != nil {
		t.Fatal(err)
	}
	if err = b.CheckDependencies(ctx, p, e.Dependencies); !errors.Is(err, ErrDenied) {
		t.Fatalf("cached view survived source revocation: %v", err)
	}
}

func TestAnnotatePreservesContentAndKindAndAllowsStagedReads(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	e, _ := b.Begin(ctx, p, "alice")
	if _, err := b.Call(ctx, e, "write", map[string]any{"path": "/goal", "content": "full content", "kind": "goal", "metadata": map[string]any{"status": "active", "channel_id": 1.0}}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Finish(ctx, e, ID(), Receipt{}); err != nil {
		t.Fatal(err)
	}
	e, _ = b.Begin(ctx, p, "alice")
	if _, err := b.Call(ctx, e, "annotate", map[string]any{"path": "/goal", "metadata": map[string]any{"status": "expired"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Call(ctx, e, "read", map[string]any{"path": "/goal"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Finish(ctx, e, ID(), Receipt{}); err != nil {
		t.Fatal(err)
	}
	snap, _ := b.Store.Load(ctx, "alice")
	rec, _ := b.Store.RecordByPath(ctx, snap, "/goal")
	rev, _ := b.Store.ReadRevision(ctx, "alice", rec.Revision)
	if rev.Content != "full content" || rec.Kind != "goal" || rec.Metadata["channel_id"] != 1.0 || rec.Metadata["status"] != "expired" {
		t.Fatal(rec, rev)
	}
}

func TestDocumentsHelpExplainsWorkspaceContract(t *testing.T) {
	help := SDKHelp("documents")
	if help["contract"] == nil {
		t.Fatal("workspace contract is not discoverable")
	}
}

func TestReadNewStagedRecordDoesNotDependOnUnpublishedRevision(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	e, _ := b.Begin(ctx, p, "alice")
	if _, err := b.Call(ctx, e, "write", map[string]any{"path": "/new", "content": "new"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Call(ctx, e, "read", map[string]any{"path": "/new"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Call(ctx, e, "patch", map[string]any{"path": "/new", "old": "new", "new": "final"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Finish(ctx, e, ID(), Receipt{}); err != nil {
		t.Fatal(err)
	}
	if err := b.CheckDependencies(ctx, p, e.Dependencies); err != nil {
		t.Fatalf("successful receipt rejected: %v", err)
	}
}

func TestFindPagesMergeStagedPathsAndApplyMetadata(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	e, _ := b.Begin(ctx, p, "alice")
	for _, path := range []string{"/a", "/c", "/e"} {
		if _, err := b.Call(ctx, e, "write", map[string]any{"path": path, "content": "saved", "metadata": map[string]any{"status": "active"}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Finish(ctx, e, "seed", Receipt{}); err != nil {
		t.Fatal(err)
	}
	e, _ = b.Begin(ctx, p, "alice")
	for _, path := range []string{"/b", "/d", "/f"} {
		if _, err := b.Call(ctx, e, "write", map[string]any{"path": path, "content": "staged", "metadata": map[string]any{"status": "active"}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Call(ctx, e, "write", map[string]any{"path": "/hidden", "content": "inactive", "metadata": map[string]any{"status": "inactive"}}); err != nil {
		t.Fatal(err)
	}
	var paths []string
	token := ""
	for page := 0; page < 10; page++ {
		result, err := b.Call(ctx, e, "find", map[string]any{"path": "/", "limit": 2, "cursor": token, "metadata": map[string]any{"status": "active"}})
		if err != nil {
			t.Fatal(err)
		}
		data := result.(map[string]any)
		for _, rec := range data["items"].([]Record) {
			paths = append(paths, rec.Path)
		}
		token = data["cursor"].(string)
		if token == "" {
			break
		}
	}
	if got := strings.Join(paths, ","); got != "/a,/b,/c,/d,/e,/f" {
		t.Fatal("incomplete or unfiltered listing", got)
	}
}

func TestDeletedRecordHistoryAndRevert(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	e, _ := b.Begin(ctx, p, "alice")
	if _, err := b.Call(ctx, e, "write", map[string]any{"path": "/a", "content": "original", "kind": "goal"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Finish(ctx, e, "seed", Receipt{}); err != nil {
		t.Fatal(err)
	}
	snap, _ := b.Store.Load(ctx, "alice")
	original, err := b.Store.RecordByPath(ctx, snap, "/a")
	if err != nil {
		t.Fatal(err)
	}
	e, _ = b.Begin(ctx, p, "alice")
	if _, err = b.Call(ctx, e, "delete", map[string]any{"path": "/a"}); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Finish(ctx, e, "delete", Receipt{}); err != nil {
		t.Fatal(err)
	}
	e, _ = b.Begin(ctx, p, "alice")
	if _, err = b.Call(ctx, e, "read", map[string]any{"path": "/a"}); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted content visible", err)
	}
	history, err := b.Call(ctx, e, "history", map[string]any{"path": "/a"})
	if err != nil {
		t.Fatal("deleted history missing", err)
	}
	if len(history.(map[string]any)["items"].([]map[string]any)) != 2 {
		t.Fatal(history)
	}
	if _, err = b.Call(ctx, e, "revert", map[string]any{"path": "/a", "revision": original.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Call(ctx, e, "read", map[string]any{"path": "/a"}); err != nil {
		t.Fatal("staged restore unreadable", err)
	}
	if _, err = b.Finish(ctx, e, "restore", Receipt{}); err != nil {
		t.Fatal(err)
	}
	snap, _ = b.Store.Load(ctx, "alice")
	restored, err := b.Store.RecordByPath(ctx, snap, "/a")
	if err != nil {
		t.Fatal(err)
	}
	if restored.ID != original.ID || restored.Kind != "goal" {
		t.Fatal(restored)
	}
	rev, err := b.Store.ReadRevision(ctx, "alice", restored.Revision)
	if err != nil || rev.Content != "original" {
		t.Fatal(rev, err)
	}
}

func TestProposalStatusReturnsOnlyOperationalFields(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	e, _ := b.Begin(ctx, p, "alice")
	if _, err := b.Call(ctx, e, "write", map[string]any{"path": "/case", "content": "case", "kind": "experiment"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Finish(ctx, e, "seed", Receipt{}); err != nil {
		t.Fatal(err)
	}
	snap, _ := b.Store.Load(ctx, "alice")
	rec, _ := b.Store.RecordByPath(ctx, snap, "/case")
	b.Status = func(ctx context.Context, key string) (string, int, bool, error) {
		if key != JobKey("alice", rec.Revision, snap.Space.Policy) {
			return "", 0, false, nil
		}
		return "completed", 1, true, nil
	}
	e, _ = b.Begin(ctx, p, "alice")
	result, err := b.Call(ctx, e, "proposal_status", map[string]any{"path": "/case"})
	if err != nil {
		t.Fatal(err)
	}
	data := result.(map[string]any)
	if data["state"] != "completed" || data["attempts"] != 1 {
		t.Fatal(data)
	}
	if _, ok := data["proposal"]; ok {
		t.Fatal("private draft leaked", data)
	}
}

func TestWriteCannotEscapeReadonlyWorkspace(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	snap, _ := b.Store.Load(ctx, "alice")
	space := snap.Space
	space.Kind = "team"
	space.Owner = "other"
	space.Access = &Access{Share: []ShareEntry{{Subject: "alice", Role: "reader"}}}
	_, err := b.Store.Commit(ctx, snap, []Revision{{ID: ID(), RecordID: ID(), Path: "/protected", Kind: "note", Content: "original"}}, "seed", Receipt{}, &space)
	if err != nil {
		t.Fatal(err)
	}
	e, err := b.Begin(ctx, p, "alice")
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.Call(ctx, e, "write", map[string]any{"path": "/protected", "content": "overwritten"})
	if !errors.Is(err, ErrDenied) {
		t.Fatal("read-only source rewritten", err)
	}
}
