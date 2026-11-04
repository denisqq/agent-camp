package camp

import (
	"errors"
	"testing"
	"time"
)

func accessMeta(subject, role string) map[string]any {
	return map[string]any{"access": map[string]any{"visibility": "closed", "share": []any{map[string]any{"subject": subject, "role": role}}}}
}
func workspace(t *testing.T, role string) (*Broker, Principal, Principal) {
	t.Helper()
	ctx, b, alice, _ := setupBroker(t)
	admin, _ := b.Auth.Authenticate(ctx, "admin-test-token-32-characters-long")
	tok, err := b.Auth.Issue(ctx, admin, Principal{Subject: "bob", Role: "agent", Spaces: []string{"alice", "shared"}, Scopes: []string{"read", "write", "propose"}, Expires: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	bob, _ := b.Auth.Authenticate(ctx, tok)
	e, _ := b.Begin(ctx, alice, "alice")
	for _, a := range []map[string]any{
		{"path": "/project", "kind": "section", "content": "Project", "metadata": accessMeta("bob", role)},
		{"path": "/project/plan", "content": "v1"},
		{"path": "/project/private", "kind": "section", "content": "private", "metadata": map[string]any{"access": map[string]any{"visibility": "closed"}}},
		{"path": "/project/private/secret", "content": "hidden"},
	} {
		if _, err = b.Call(ctx, e, "write", a); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = b.Finish(ctx, e, ID(), Receipt{}); err != nil {
		t.Fatal(err)
	}
	return b, alice, bob
}
func TestWorkspaceSharedSubtreeAndRevocation(t *testing.T) {
	b, alice, bob := workspace(t, "writer")
	ctx := t.Context()
	e, err := b.Begin(ctx, bob, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Call(ctx, e, "read", map[string]any{"path": "/project/plan"}); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Call(ctx, e, "read", map[string]any{"path": "/project/private/secret"}); !errors.Is(err, ErrDenied) {
		t.Fatal("nested closed section", err)
	}
	if _, err = b.Call(ctx, e, "write", map[string]any{"path": "/project/plan", "content": "v2"}); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Finish(ctx, e, ID(), Receipt{}); err != nil {
		t.Fatal(err)
	}
	e, _ = b.Begin(ctx, bob, "alice")
	if _, err = b.Call(ctx, e, "read", map[string]any{"path": "/project/plan"}); err != nil {
		t.Fatal(err)
	}
	owner, _ := b.Begin(ctx, alice, "alice")
	if _, err = b.Call(ctx, owner, "annotate", map[string]any{"path": "/project", "metadata": map[string]any{"access": map[string]any{"visibility": "closed"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Finish(ctx, owner, ID(), Receipt{}); err != nil {
		t.Fatal(err)
	}
	if err = b.CheckDependencies(ctx, bob, e.Dependencies); !errors.Is(err, ErrDenied) {
		t.Fatal("cached receipt survived revoke", err)
	}
	if _, err = b.Call(ctx, e, "read", map[string]any{"path": "/project/plan"}); !errors.Is(err, ErrDenied) {
		t.Fatal("snapshot survived revoke", err)
	}
}
func TestWorkspaceWriterCannotChangePermissions(t *testing.T) {
	for _, method := range []string{"annotate", "delete", "move", "write"} {
		t.Run(method, func(t *testing.T) {
			b, _, bob := workspace(t, "writer")
			ctx := t.Context()
			e, _ := b.Begin(ctx, bob, "alice")
			a := map[string]any{"path": "/project", "to": "/stolen", "content": "x", "kind": "note", "metadata": map[string]any{"access": map[string]any{"visibility": "open"}}}
			if _, err := b.Call(ctx, e, method, a); !errors.Is(err, ErrDenied) && !errors.Is(err, ErrInvalid) {
				t.Fatal("writer changed permission-bearing section", err)
			}
		})
	}
}
func TestWorkspaceCommenterCanProposeButNotEdit(t *testing.T) {
	b, _, bob := workspace(t, "commenter")
	ctx := t.Context()
	e, _ := b.Begin(ctx, bob, "alice")
	if _, err := b.Call(ctx, e, "write", map[string]any{"path": "/project/plan", "content": "bad"}); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if _, err := b.Call(ctx, e, "propose", map[string]any{"path": "/project/plan"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Finish(ctx, e, ID(), Receipt{}); err != nil {
		t.Fatal(err)
	}
}
func TestWorkspaceMalformedAccessFailsClosed(t *testing.T) {
	b, alice, _ := workspace(t, "writer")
	ctx := t.Context()
	e, _ := b.Begin(ctx, alice, "alice")
	if _, err := b.Call(ctx, e, "annotate", map[string]any{"path": "/project", "metadata": map[string]any{"access": map[string]any{"visibility": "open", "share": "bad"}}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("malformed ACL accepted", err)
	}
}

func TestWorkspaceMoveRequiresBothPathsAndConflicts(t *testing.T) {
	b, alice, bob := workspace(t, "writer")
	ctx := t.Context()
	e, _ := b.Begin(ctx, bob, "alice")
	if _, err := b.Call(ctx, e, "move", map[string]any{"path": "/project/plan", "to": "/outside"}); !errors.Is(err, ErrDenied) {
		t.Fatal("destination bypass", err)
	}
	one, _ := b.Begin(ctx, alice, "alice")
	two, _ := b.Begin(ctx, bob, "alice")
	for _, e := range []*Execution{one, two} {
		if _, err := b.Call(ctx, e, "write", map[string]any{"path": "/project/plan", "content": e.Principal.Subject}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Finish(ctx, one, ID(), Receipt{}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Finish(ctx, two, ID(), Receipt{}); !errors.Is(err, ErrConflict) {
		t.Fatal("concurrent edit silently lost", err)
	}
}
func TestLegacySegmentIsRejected(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	e, _ := b.Begin(ctx, p, "alice")
	if _, err := b.Call(ctx, e, "write", map[string]any{"path": "/secret", "content": "x", "segment": "sealed"}); !errors.Is(err, ErrInvalid) {
		t.Fatal("obsolete privacy parameter silently ignored", err)
	}
}

func TestReviewHistoricalDependencyRevocation(t *testing.T) {
	b, alice, bob := workspace(t, "writer")
	ctx := t.Context()
	run := func(p Principal, method string, args map[string]any) any {
		e, err := b.Begin(ctx, p, "alice")
		if err != nil {
			t.Fatal(err)
		}
		out, err := b.Call(ctx, e, method, args)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = b.Finish(ctx, e, ID(), Receipt{}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	run(alice, "write", map[string]any{"path": "/other", "kind": "section", "content": "other", "metadata": accessMeta("bob", "writer")})
	snap, _ := b.Store.Load(ctx, "alice")
	rec, _ := b.Store.RecordByPath(ctx, snap, "/project/plan")
	historical := rec.Revision
	run(alice, "move", map[string]any{"path": "/project/plan", "to": "/other/plan"})
	e, _ := b.Begin(ctx, bob, "alice")
	if _, err := b.Call(ctx, e, "read", map[string]any{"path": "/other/plan", "revision": historical}); err != nil {
		t.Fatal(err)
	}
	run(alice, "annotate", map[string]any{"path": "/project", "metadata": map[string]any{"access": map[string]any{"visibility": "closed"}}})
	if _, err := b.Call(ctx, e, "read", map[string]any{"path": "/other/plan", "revision": historical}); !errors.Is(err, ErrDenied) {
		t.Fatal("direct read should deny", err)
	}
	if err := b.CheckDependencies(ctx, bob, e.Dependencies); !errors.Is(err, ErrDenied) {
		t.Fatalf("historical cached data allowed after revoke; dependencies=%+v, error=%v", e.Dependencies, err)
	}
}

func TestReviewHistoryDependencyRevocation(t *testing.T) {
	b, alice, bob := workspace(t, "writer")
	ctx := t.Context()
	run := func(p Principal, method string, args map[string]any) any {
		e, err := b.Begin(ctx, p, "alice")
		if err != nil {
			t.Fatal(err)
		}
		out, err := b.Call(ctx, e, method, args)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = b.Finish(ctx, e, ID(), Receipt{}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	run(alice, "write", map[string]any{"path": "/other", "kind": "section", "content": "other", "metadata": accessMeta("bob", "writer")})
	snap, _ := b.Store.Load(ctx, "alice")
	rec, _ := b.Store.RecordByPath(ctx, snap, "/project/plan")
	_ = rec.Revision
	run(alice, "move", map[string]any{"path": "/project/plan", "to": "/other/plan"})
	e, _ := b.Begin(ctx, bob, "alice")
	if _, err := b.Call(ctx, e, "history", map[string]any{"path": "/other/plan"}); err != nil {
		t.Fatal(err)
	}
	run(alice, "annotate", map[string]any{"path": "/project", "metadata": map[string]any{"access": map[string]any{"visibility": "closed"}}})
	if _, err := b.Call(ctx, e, "history", map[string]any{"path": "/other/plan"}); !errors.Is(err, ErrDenied) {
		t.Fatal("direct read should deny", err)
	}
	if err := b.CheckDependencies(ctx, bob, e.Dependencies); !errors.Is(err, ErrDenied) {
		t.Fatalf("historical cached data allowed after revoke; dependencies=%+v, error=%v", e.Dependencies, err)
	}
}
