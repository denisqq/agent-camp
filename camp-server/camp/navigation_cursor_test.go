package camp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMapCursorEncryptionAndExpiry(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	original := MapCursor{Version: 1, TokenHash: "token", Subject: "alice", Space: "alice", Path: "/", View: "tree", Depth: 2, BudgetBytes: 4096, Snapshot: "snapshot", ScanStart: "/private/raw-boundary", Offset: 3, Expires: time.Now().Add(time.Minute).Unix()}
	token, err := sealMapCursor(key, original)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("private")) || strings.Contains(token, "private") {
		t.Fatal("raw scan path exposed")
	}
	opened, err := openMapCursor(key, token)
	if err != nil || opened != original {
		t.Fatalf("roundtrip: %+v %v", opened, err)
	}
	raw[len(raw)-1] ^= 1
	for _, bad := range []string{"bad", base64.RawURLEncoding.EncodeToString(raw)} {
		if _, err = openMapCursor(key, bad); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "restart") {
			t.Fatalf("bad cursor accepted: %v", err)
		}
	}
	if _, err = openMapCursor(bytes.Repeat([]byte{8}, 32), token); !errors.Is(err, ErrInvalid) {
		t.Fatal("restart key accepted")
	}
	original.Expires = time.Now().Add(-time.Second).Unix()
	token, err = sealMapCursor(key, original)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = openMapCursor(key, token); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "restart") {
		t.Fatalf("expired cursor: %v", err)
	}
}

func seedCursorCards(t *testing.T, b *Broker, n int) {
	t.Helper()
	ctx := context.Background()
	snap, err := b.Store.Load(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	revs := []Revision{}
	for i := 0; i < n; i++ {
		revs = append(revs, Revision{ID: fmt.Sprintf("cursor-r-%03d", i), RecordID: fmt.Sprintf("cursor-%03d", i), Path: fmt.Sprintf("/doc-%03d", i), Kind: "note", Content: "# Same title", Metadata: map[string]any{"description": strings.Repeat("界", 240)}, Actor: "alice"})
	}
	if _, err = b.Store.Commit(ctx, snap, revs, "cursor-seed", Receipt{}, nil); err != nil {
		t.Fatal(err)
	}
}
func mapCursorPage(t *testing.T, b *Broker, p Principal, r MapRequest) (NavigationPage, []byte) {
	t.Helper()
	result, err := b.Map(context.Background(), p, r)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var page NavigationPage
	if err = json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	return page, raw
}
func TestMapCursorBinding(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	seedCursorCards(t, b, 18)
	depth, budget := 2, 4096
	req := MapRequest{Space: "alice", View: "tree", Depth: &depth, BudgetBytes: &budget}
	first, _ := mapCursorPage(t, b, p, req)
	if first.Cursor == "" {
		t.Fatal("missing continuation")
	}
	req.Cursor = first.Cursor
	for _, change := range []func(*MapRequest){func(r *MapRequest) { r.Space = "shared" }, func(r *MapRequest) { r.Path = "/elsewhere" }, func(r *MapRequest) { r.View = "adaptive" }, func(r *MapRequest) { d := 1; r.Depth = &d }, func(r *MapRequest) { n := 8192; r.BudgetBytes = &n }} {
		changed := req
		change(&changed)
		if _, err := b.Map(ctx, p, changed); !errors.Is(err, ErrInvalid) {
			t.Fatalf("query binding ignored: %+v %v", changed, err)
		}
	}
	admin, err := b.Auth.Authenticate(ctx, "admin-test-token-32-characters-long")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Map(ctx, admin, req); !errors.Is(err, ErrInvalid) {
		t.Fatalf("token binding ignored: %v", err)
	}
	restarted := NewBroker(b.Store, b.Auth)
	if _, err = restarted.Map(ctx, p, req); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "restart") {
		t.Fatalf("restart binding: %v", err)
	}
}

func TestMapCursorSnapshotAndRevocation(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	seedCursorCards(t, b, 18)
	budget := 4096
	req := MapRequest{Space: "alice", View: "tree", BudgetBytes: &budget}
	first, _ := mapCursorPage(t, b, p, req)
	req.Cursor = first.Cursor
	before, _ := mapCursorPage(t, b, p, req)
	snap, err := b.Store.Load(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.Store.Commit(ctx, snap, []Revision{{ID: "later-r", RecordID: "later", Path: "/000-new", Kind: "note", Content: "new", Actor: "alice"}}, "later", Receipt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	after, raw := mapCursorPage(t, b, p, req)
	a, _ := json.Marshal(before.Nodes)
	z, _ := json.Marshal(after.Nodes)
	if before.Snapshot != after.Snapshot || after.Snapshot != first.Snapshot || !bytes.Equal(a, z) || bytes.Contains(raw, []byte("000-new")) {
		t.Fatal("continuation mixed snapshots")
	}
	e, err := b.Begin(ctx, p, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Call(ctx, e, "map", map[string]any{"view": "tree", "budget_bytes": 4096, "cursor": req.Cursor}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("SDK accepted different snapshot: %v", err)
	}
	e.Snapshots["alice"], err = b.Store.At(ctx, "alice", first.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Call(ctx, e, "refresh", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Call(ctx, e, "map", map[string]any{"view": "tree", "budget_bytes": 4096, "cursor": req.Cursor}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("refresh accepted old map cursor: %v", err)
	}
	e.Snapshots["alice"], err = b.Store.At(ctx, "alice", first.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	e.Staged["staged"] = Revision{ID: "s", RecordID: "staged", Path: "/staged", Kind: "note"}
	if _, err = b.Call(ctx, e, "map", map[string]any{"view": "tree", "budget_bytes": 4096, "cursor": req.Cursor}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("SDK accepted staged continuation: %v", err)
	}
	p.Revoked = true
	if err = b.Auth.Principals.SavePrincipal(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Map(ctx, p, req); !errors.Is(err, ErrDenied) {
		t.Fatalf("revoked principal continued: %v", err)
	}
}

func TestMapContinuationExhaustive(t *testing.T) {
	_, b, p, _ := setupBroker(t)
	seedCursorCards(t, b, 18)
	budget := 4096
	req := MapRequest{Space: "alice", View: "tree", BudgetBytes: &budget}
	ids := map[string]bool{}
	for i := 0; i < 30; i++ {
		page, raw := mapCursorPage(t, b, p, req)
		if len(raw) > budget {
			t.Fatalf("cursor exceeds budget: %d", len(raw))
		}
		for _, node := range page.Nodes {
			if ids[node.ID] {
				t.Fatal("duplicate within one scan window")
			}
			ids[node.ID] = true
		}
		if page.Cursor == "" {
			if !page.OutputComplete || !page.ScanComplete {
				t.Fatal("incomplete terminal page")
			}
			break
		}
		if page.Cursor == req.Cursor || page.Coverage != "truncated" {
			t.Fatal("no progress or false completeness")
		}
		req.Cursor = page.Cursor
	}
	if len(ids) != 18 {
		t.Fatalf("stranded tail: %d", len(ids))
	}
}
func TestMapCursorKeySharedByReadCacheCopies(t *testing.T) {
	_, b, _, _ := setupBroker(t)
	literal := &Broker{Store: b.Store, Auth: b.Auth}
	keys := make(chan string, 20)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key, err := literal.withReadCache().mapCursorKey()
			if err != nil {
				keys <- "error"
				return
			}
			keys <- string(key)
		}()
	}
	wg.Wait()
	close(keys)
	var first string
	for key := range keys {
		if key == "error" || len(key) != 32 {
			t.Fatal("key initialization failed")
		}
		if first == "" {
			first = key
		}
		if first != key {
			t.Fatal("read-cache copies have different keys")
		}
	}
}

func TestMapDiscoveryContinuation(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	for i := 0; i < 120; i++ {
		id := fmt.Sprintf("space-%03d", i)
		if _, err := b.Store.CreateSpace(ctx, Space{ID: id, Kind: "personal", Owner: "alice"}); err != nil {
			t.Fatal(err)
		}
		p.Spaces = append(p.Spaces, id)
	}
	if err := b.Auth.Principals.SavePrincipal(ctx, p); err != nil {
		t.Fatal(err)
	}
	admin, err := b.Auth.Authenticate(ctx, "admin-test-token-32-characters-long")
	if err != nil {
		t.Fatal(err)
	}
	for _, principal := range []Principal{p, admin} {
		seen := map[string]bool{}
		request := MapRequest{}
		for calls := 0; calls < 10; calls++ {
			result, err := b.Map(ctx, principal, request)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(result)
			if len(raw) > defaultMapBudget {
				t.Fatalf("discovery exceeds budget: %d", len(raw))
			}
			var data struct {
				Spaces []struct {
					ID string `json:"id"`
				} `json:"spaces"`
				Cursor   string `json:"cursor"`
				Coverage string `json:"coverage"`
			}
			if err = json.Unmarshal(raw, &data); err != nil {
				t.Fatal(err)
			}
			for _, space := range data.Spaces {
				if seen[space.ID] {
					t.Fatal("duplicate space")
				}
				seen[space.ID] = true
			}
			if data.Cursor == "" {
				if data.Coverage != "complete" {
					t.Fatal("incomplete terminal discovery")
				}
				break
			}
			if data.Coverage != "truncated" {
				t.Fatal("discovery omission hidden")
			}
			if calls == 0 {
				wrong := MapRequest{Space: "alice", Cursor: data.Cursor}
				if _, err = b.Map(ctx, principal, wrong); !errors.Is(err, ErrInvalid) {
					t.Fatal("discovery cursor accepted for documents")
				}
			}
			request.Cursor = data.Cursor
		}
		expected := 122
		if principal.Subject == admin.Subject {
			expected = 123
		}
		if len(seen) != expected {
			t.Fatalf("discovery stranded spaces: %d want%d", len(seen), expected)
		}
	}
	one := 1
	budget := 4096
	for _, r := range []MapRequest{{Path: "/"}, {Depth: &one}, {View: "tree"}, {BudgetBytes: &budget}} {
		if _, err = b.Map(ctx, p, r); !errors.Is(err, ErrInvalid) {
			t.Fatalf("discovery accepted space-only parameters: %+v %v", r, err)
		}
	}
}

func seedLargeCursorCorpus(t *testing.T, b *Broker, space string, hidden bool) []string {
	t.Helper()
	ctx := context.Background()
	snap, err := b.Store.Load(ctx, space)
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{}
	// Small commits keep this a navigation test, rather than repeatedly decoding
	// one 3000-change HEAD during every existing current-access check.
	for batch := 0; batch < 32; batch++ {
		revs := []Revision{}
		for i := batch * 100; i < min((batch+1)*100, 3101); i++ {
			p := fmt.Sprintf("/g-%03d/d-%04d", i%121, i)
			if hidden && i < 3000 {
				p = fmt.Sprintf("/a-private/d-%04d", i)
			}
			revs = append(revs, Revision{ID: fmt.Sprintf("large-r-%04d", i), RecordID: fmt.Sprintf("large-%04d", i), Path: p, Kind: "note", Content: "# Source", Actor: "alice"})
			if !hidden || i >= 3000 {
				expected = append(expected, p)
			}
		}
		if len(revs) == 0 {
			continue
		}
		snap, err = b.Store.Commit(ctx, snap, revs, fmt.Sprintf("large-%02d", batch), Receipt{}, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	return expected
}
func TestMapContinuationLargeSpace(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	expected := seedLargeCursorCorpus(t, b, "alice", false)
	budget := 12288
	request := MapRequest{Space: "alice", BudgetBytes: &budget}
	routes := map[string]bool{}
	snapshot := ""
	pages := 0
	total := 0
	seenWindows := map[string]bool{}
	windowStart := ""
	for ; pages < 15; pages++ {
		page, raw := mapCursorWindowPage(t, b, p, request)
		if len(raw) > budget {
			t.Fatal("cursor exceeded page budget")
		}
		if snapshot == "" {
			if page.Records != 3000 || page.ScanComplete {
				t.Fatal("public map scan limit is not 3000 with lookahead")
			}
			snapshot = page.Snapshot
		}
		if page.Snapshot != snapshot {
			t.Fatal("window changed snapshot")
		}
		if !seenWindows[windowStart] {
			total += page.Records
			seenWindows[windowStart] = true
		}
		for _, n := range page.Nodes {
			routes[n.Path] = true
		}
		if page.Cursor == "" {
			break
		}
		key, err := b.mapCursorKey()
		if err != nil {
			t.Fatal(err)
		}
		next, err := openMapCursor(key, page.Cursor)
		if err != nil {
			t.Fatal(err)
		}
		if next.ScanStart == "" && next.Offset == 0 {
			t.Fatal("cursor made no progress")
		}
		windowStart = next.ScanStart
		request.Cursor = page.Cursor
	}
	if len(routes) != 121 {
		t.Fatalf("lost direct group routes: %d", len(routes))
	}
	if pages >= 15 || total != 3101 {
		t.Fatalf("scan windows stranded records: pages%d records%d", pages, total)
	}
	for _, p := range expected {
		reachable := false
		for route := range routes {
			if pathUnder(p, route) {
				reachable = true
				break
			}
		}
		if !reachable {
			t.Fatalf("stranded source %s", p)
		}
	}
	// Cursor/key initialization never writes to document storage.
	counter := b.Store.Objects.(interface{ Writes() int })
	before := counter.Writes()
	if _, err := b.Map(ctx, p, MapRequest{Space: "alice", Path: "/g-001"}); err != nil {
		t.Fatal(err)
	}
	if counter.Writes() != before {
		t.Fatal("navigation key/cursor persisted to workspace objects")
	}

}
func TestMapDeniedOnlyWindowAdvancesWithoutDisclosure(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	space := Space{ID: "team", Kind: "team", Owner: "other", Access: &Access{Share: []ShareEntry{{Subject: "alice", Role: "reader"}}}}
	snap, err := b.Store.CreateSpace(ctx, space)
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.Store.Commit(ctx, snap, []Revision{{ID: "private-section-r", RecordID: "private-section", Path: "/a-private", Kind: "section", Content: "# HIDDEN", Metadata: map[string]any{"access": map[string]any{"visibility": "closed"}}, Actor: "other"}}, "private-section", Receipt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	expected := seedLargeCursorCorpus(t, b, "team", true)
	p.Spaces = append(p.Spaces, "team")
	if err = b.Auth.Principals.SavePrincipal(ctx, p); err != nil {
		t.Fatal(err)
	}
	budget := 32768
	req := MapRequest{Space: "team", BudgetBytes: &budget}
	first, raw := mapCursorWindowPage(t, b, p, req)
	if len(first.Nodes) != 0 || first.Records != 0 || first.Cursor == "" || bytes.Contains(raw, []byte("a-private")) || bytes.Contains(raw, []byte("HIDDEN")) {
		t.Fatalf("denied window leaked or failed to advance: %s", raw)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(first.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(decoded, []byte("a-private")) {
		t.Fatal("private boundary exposed by decoding cursor")
	}
	req.Cursor = first.Cursor
	tail, _ := mapCursorWindowPage(t, b, p, req)
	routes := map[string]bool{}
	for _, n := range tail.Nodes {
		routes[n.Path] = true
	}
	for _, p := range expected {
		found := false
		for route := range routes {
			if pathUnder(p, route) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("hidden window stranded public source %s", p)
		}
	}
}

// Exercise large-window membership independently of a machine's wall-clock
// speed. The real Map/MCP surfaces are exercised separately with bounded pages.
func mapCursorWindowPage(t *testing.T, b *Broker, p Principal, r MapRequest) (NavigationPage, []byte) {
	t.Helper()
	ctx := context.Background()
	b = b.withReadCache()
	e, err := b.Begin(ctx, p, r.Space)
	if err != nil {
		t.Fatal(err)
	}
	e.mapSessionless = true
	args := map[string]any{"path": r.Path, "cursor": r.Cursor}
	if r.View != "" {
		args["view"] = r.View
	}
	if r.Depth != nil {
		args["depth"] = *r.Depth
	}
	if r.BudgetBytes != nil {
		args["budget_bytes"] = *r.BudgetBytes
	}
	value, err := b.Call(ctx, e, "map", args)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Auth.Current(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err = b.CheckDependencies(ctx, p, e.Dependencies); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var page NavigationPage
	if err = json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	return page, raw
}

func TestMapCursorVisibilityChangeRequiresRestart(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	seedCursorCards(t, b, 18)
	snap, err := b.Store.CreateSpace(ctx, Space{ID: "team", Kind: "team", Owner: "other", Access: &Access{Share: []ShareEntry{{Subject: "alice", Role: "reader"}}}})
	if err != nil {
		t.Fatal(err)
	}
	source, err := b.Store.Load(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	records, _, err := b.Store.List(ctx, source, "/", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	revisions := []Revision{}
	for _, rec := range records {
		rev, err := b.Store.ReadRevision(ctx, "alice", rec.Revision)
		if err != nil {
			t.Fatal(err)
		}
		revisions = append(revisions, rev)
	}
	_, err = b.Store.Commit(ctx, snap, revisions, "copy", Receipt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p.Spaces = append(p.Spaces, "team")
	if err = b.Auth.Principals.SavePrincipal(ctx, p); err != nil {
		t.Fatal(err)
	}
	budget := 4096
	req := MapRequest{Space: "team", View: "tree", BudgetBytes: &budget}
	first, _ := mapCursorPage(t, b, p, req)
	if first.Cursor == "" {
		t.Fatal("fixture needs pages")
	}
	req.Cursor = first.Cursor
	current, err := b.Store.Load(ctx, "team")
	if err != nil {
		t.Fatal(err)
	}
	rec, err := b.Store.RecordByPath(ctx, current, "/doc-000")
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.Store.Commit(ctx, current, []Revision{{ID: "closed-r", RecordID: rec.ID, Parent: rec.Revision, Path: rec.Path, Kind: "section", Metadata: map[string]any{"access": map[string]any{"visibility": "closed"}}, Content: "PRIVATE AFTER REVOCATION", Actor: "other"}}, "revoke", Receipt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.Map(ctx, p, req)
	if result != nil || !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "restart") {
		t.Fatalf("visibility change shifted offset or leaked data: %+v %v", result, err)
	}
}

func TestMapCursorMoveAndDelete(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(fmt.Sprint(deleted), func(t *testing.T) {
			ctx, b, p, _ := setupBroker(t)
			seedCursorCards(t, b, 18)
			budget := 4096
			req := MapRequest{Space: "alice", View: "tree", BudgetBytes: &budget}
			first, _ := mapCursorPage(t, b, p, req)
			req.Cursor = first.Cursor
			snap, err := b.Store.Load(ctx, "alice")
			if err != nil {
				t.Fatal(err)
			}
			rec, err := b.Store.RecordByPath(ctx, snap, "/doc-000")
			if err != nil {
				t.Fatal(err)
			}
			rev, err := b.Store.ReadRevision(ctx, "alice", rec.Revision)
			if err != nil {
				t.Fatal(err)
			}
			rev.ID = "lifecycle-r"
			rev.Parent = rec.Revision
			rev.Deleted = deleted
			if !deleted {
				rev.Path = "/z-moved"
			}
			_, err = b.Store.Commit(ctx, snap, []Revision{rev}, "lifecycle", Receipt{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			result, err := b.Map(ctx, p, req)
			if deleted {
				if result != nil || !errors.Is(err, ErrInvalid) {
					t.Fatalf("deletion shifted membership: %+v %v", result, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("authorized move broke immutable snapshot continuation: %v", err)
			}
			raw, _ := json.Marshal(result)
			if bytes.Contains(raw, []byte("z-moved")) || result["snapshot"] != first.Snapshot {
				t.Fatal("move retargeted old snapshot")
			}
		})
	}
}
func TestMapCursorSharedSourceRevocation(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	source, err := b.Store.Load(ctx, "bob")
	if err != nil {
		t.Fatal(err)
	}
	settings := source.Space
	settings.Access = &Access{Share: []ShareEntry{{Subject: "brain-curator", Role: "reader"}}}
	source, err = b.Store.Commit(ctx, source, []Revision{{ID: "evidence-r", RecordID: "evidence", Path: "/evidence", Kind: "note", Content: "source", Actor: "bob"}}, "source", Receipt{}, &settings)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := b.Store.Load(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	revs := []Revision{}
	for i := 0; i < 18; i++ {
		revs = append(revs, Revision{ID: fmt.Sprintf("practice-r-%d", i), RecordID: fmt.Sprintf("practice-%d", i), Path: fmt.Sprintf("/practice-%02d", i), Kind: "practice", Content: "# Source-derived title", Metadata: map[string]any{"description": strings.Repeat("界", 240)}, Actor: "camp-arbiter", Sources: []SourceRef{{Space: "bob", Record: "evidence", Revision: "evidence-r", Path: "/evidence", Policy: source.Space.Policy}}})
	}
	_, err = b.Store.Commit(ctx, shared, revs, "shared-seed", Receipt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	budget := 4096
	req := MapRequest{Space: "shared", View: "tree", BudgetBytes: &budget}
	first, _ := mapCursorPage(t, b, p, req)
	if first.Cursor == "" {
		t.Fatal("fixture needs cursor")
	}
	req.Cursor = first.Cursor
	source, err = b.Store.Load(ctx, "bob")
	if err != nil {
		t.Fatal(err)
	}
	settings = source.Space
	settings.Access = nil
	_, err = b.Store.Commit(ctx, source, nil, "revoke-source", Receipt{}, &settings)
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.Map(ctx, p, req)
	if result != nil || !errors.Is(err, ErrInvalid) {
		t.Fatalf("shared-source revocation leaked old map: %+v %v", result, err)
	}
}
func TestMapCursorKeyFailurePropagates(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	failure := errors.New("entropy unavailable")
	state := &mapCursorKeyState{}
	state.once.Do(func() { state.err = failure })
	b.mapCursors = state
	if _, err := b.Map(ctx, p, MapRequest{Space: "alice"}); !errors.Is(err, failure) {
		t.Fatalf("key failure hidden: %v", err)
	}
}

func TestMapDiscoveryDeniedOnlyRegistryWindow(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	for i := 0; i < 1001; i++ {
		if _, err := b.Store.CreateSpace(ctx, Space{ID: fmt.Sprintf("a-private-%04d", i), Kind: "personal", Owner: "other"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Store.CreateSpace(ctx, Space{ID: "z-public", Kind: "personal", Owner: "alice"}); err != nil {
		t.Fatal(err)
	}
	p.Spaces = []string{"*"}
	if err := b.Auth.Principals.SavePrincipal(ctx, p); err != nil {
		t.Fatal(err)
	}
	first, err := b.Map(ctx, p, MapRequest{})
	if err != nil {
		t.Fatal(err)
	}
	spaces := first["spaces"].([]map[string]any)
	cursor, _ := first["cursor"].(string)
	if len(spaces) != 0 || cursor == "" || first["coverage"] != "truncated" {
		t.Fatalf("denied registry window did not advance: %+v", first)
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("a-private")) {
		t.Fatal("discovery boundary disclosed private ID")
	}
	tail, err := b.Map(ctx, p, MapRequest{Cursor: cursor})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, space := range tail["spaces"].([]map[string]any) {
		if space["id"] == "z-public" {
			found = true
		}
	}
	if !found || tail["cursor"] != "" || tail["coverage"] != "complete" || tail["registry_mode"] != "live" {
		t.Fatalf("discovery stranded public tail: %+v", tail)
	}
}

func TestNavigationScanWindowLookahead(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	seedCursorCards(t, b, 2)
	b = b.withReadCache()
	e, err := b.Begin(ctx, p, "alice")
	if err != nil {
		t.Fatal(err)
	}
	input, end, err := b.collectNavigationWindow(ctx, e, e.Snapshots["alice"], "alice", "/", "", 2)
	if err != nil || !input.ScanComplete || len(input.Cards) != 2 || end != "/doc-001" {
		t.Fatalf("exact window: %+v %s %v", input, end, err)
	}
	snapshot, err := b.Store.Load(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.Store.Commit(ctx, snapshot, []Revision{{ID: "tail-r", RecordID: "tail", Path: "/doc-002", Kind: "note", Actor: "alice"}}, "lookahead-tail", Receipt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	e, err = b.Begin(ctx, p, "alice")
	if err != nil {
		t.Fatal(err)
	}
	input, end, err = b.collectNavigationWindow(ctx, e, e.Snapshots["alice"], "alice", "/", "", 2)
	if err != nil || input.ScanComplete || len(input.Cards) != 2 || end != "/doc-001" {
		t.Fatalf("one-record overflow: %+v %s %v", input, end, err)
	}
	input, end, err = b.collectNavigationWindow(ctx, e, e.Snapshots["alice"], "alice", "/", end, 2)
	if err != nil || !input.ScanComplete || len(input.Cards) != 1 || input.Cards[0].ID != "tail" || end != "/doc-002" {
		t.Fatalf("tail window: %+v %s %v", input, end, err)
	}
}
