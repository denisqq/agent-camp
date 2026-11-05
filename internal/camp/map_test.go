package camp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/denisqq/agent-camp/internal/objectstore"
	"github.com/denisqq/agent-camp/internal/teststore"
	"strings"
	"testing"
	"time"
)

func TestMapShowsLayoutAndSkipsInaccessible(t *testing.T) {
	ctx := context.Background()
	s := NewStore(teststore.New())
	a := NewAuth(NewMemState())
	if err := a.Bootstrap(ctx, "admin-test-token-32-characters-long"); err != nil {
		t.Fatal(err)
	}
	admin, _ := a.Authenticate(ctx, "admin-test-token-32-characters-long")
	shared := Space{ID: "shared", Kind: "shared", Owner: "system"}
	if _, err := s.CreateSpace(ctx, shared); err != nil {
		t.Fatal(err)
	}
	// Bob may read the team space, but the closed nested section is excluded.
	team := Space{ID: "team", Kind: "team", Owner: "other", Access: &Access{Share: []ShareEntry{{Subject: "bob", Role: "writer"}}}}
	snap, err := s.CreateSpace(ctx, team)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Commit(ctx, snap, []Revision{
		{ID: "r1", RecordID: "e1", Path: "/experiments/short-post/a", Kind: "experiment", Content: "one", Metadata: map[string]any{"outcome": "success"}, Actor: "t"},
		{ID: "r2", RecordID: "e2", Path: "/experiments/short-post/b", Kind: "experiment", Content: "two", Actor: "t"},
		{ID: "r3", RecordID: "p1", Path: "/practices/short-post", Kind: "practice", Content: "method", Metadata: map[string]any{"relations": []any{map[string]any{"record": "e1", "relation": "derived_from"}}}, Actor: "t"},
		{ID: "r4", RecordID: "s1", Path: "/sealed/secret", Kind: "section", Metadata: map[string]any{"access": map[string]any{"visibility": "closed"}}, Content: "hidden", Actor: "t"},
	}, "seed", Receipt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	token, err := a.Issue(ctx, admin, Principal{Subject: "bob", Role: "author", Spaces: []string{"team", "shared"}, Scopes: []string{"read", "write", "propose"}, Expires: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := a.Authenticate(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	b := NewBroker(s, a)
	e, err := b.Begin(ctx, bob, "team")
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.Call(ctx, e, "map", map[string]any{"path": "/", "depth": 2})
	if err != nil {
		t.Fatal(err)
	}
	data := map[string]any{}
	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(wire, &data); err != nil {
		t.Fatal(err)
	}
	num := func(v any) int { return int(v.(float64)) }
	if num(data["records"]) != 3 {
		t.Fatal("record count", data["records"])
	}
	if _, exists := data["inaccessible_records"]; exists {
		t.Fatal("hidden record counts must not be disclosed", data)
	}
	nodes := data["nodes"].([]any)
	byPath := map[string]map[string]any{}
	for _, raw := range nodes {
		n := raw.(map[string]any)
		byPath[n["path"].(string)] = n
	}
	exp, ok := byPath["/experiments"]
	if !ok || num(exp["count"]) != 2 || num(exp["kinds"].(map[string]any)["experiment"]) != 2 {
		t.Fatal("experiments node profile wrong", exp)
	}
	if _, ok := byPath["/sealed/secret"]; ok {
		t.Fatal("sealed path leaked into map")
	}
	kinds := data["kinds"].(map[string]any)
	if num(kinds["experiment"]) != 2 || num(kinds["practice"]) != 1 {
		t.Fatal("kind profile wrong", kinds)
	}
	hubs := data["hubs"].([]any)
	if len(hubs) != 1 {
		t.Fatal("link hub missing", hubs)
	}
	h := hubs[0].(map[string]any)
	if h["path"] != "/experiments/short-post/a" || num(h["in_links"]) != 1 {
		t.Fatal("link hub wrong", h)
	}
}

type mapReadCounter struct {
	objectstore.Store
	reads         int
	revisionError error
}

func (s *mapReadCounter) Get(ctx context.Context, key string) ([]byte, string, error) {
	s.reads++
	if s.revisionError != nil && strings.Contains(key, "/revisions/") {
		return nil, "", s.revisionError
	}
	return s.Store.Get(ctx, key)
}

type mapExecutor struct{}

func (mapExecutor) Execute(ctx context.Context, _ string, cb Callback) (RunOutput, error) {
	result, err := cb(ctx, "map", map[string]any{"path": "/", "depth": 2})
	return RunOutput{Value: result}, err
}
func seedMapRecords(t *testing.T, b *Broker, n int, longNames bool) {
	t.Helper()
	ctx := context.Background()
	snap, err := b.Store.Load(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	revisions := make([]Revision, 0, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("skill-%03d", i)
		if longNames {
			name += strings.Repeat("-long-name", 20)
		}
		revisions = append(revisions, Revision{ID: fmt.Sprintf("rev-%03d", i), RecordID: fmt.Sprintf("rec-%03d", i), Path: "/skills/" + name + "/SKILL.md", Kind: "skill", Content: "only metadata belongs in the map", Actor: "alice"})
	}
	if _, err = b.Store.Commit(ctx, snap, revisions, "map-seed", Receipt{}, nil); err != nil {
		t.Fatal(err)
	}
}
func TestMapRunBoundsObjectReadsForRootOrientation(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	seedMapRecords(t, b, 60, false)
	counter := &mapReadCounter{Store: b.Store.Objects}
	b.Store.Objects = counter
	sessions := NewSessions(b, mapExecutor{})
	opened, err := sessions.Start(ctx, p, StartRequest{Space: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	counter.reads = 0
	result, err := sessions.Run(ctx, p, RunRequest{SessionID: opened.ID, OperationID: "map-root", Code: "map"})
	if err != nil || result.Status != "no_changes" {
		t.Fatalf("root map: %+v %v", result, err)
	}
	raw, _ := json.Marshal(result.Result)
	var data map[string]any
	if err = json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if data["records"] != float64(60) {
		t.Fatalf("incomplete map: %s", raw)
	}
	// Includes permission revalidation and receipt checks, not just tree traversal.
	// Re-reading the same immutable paths for every record blows this S3 budget.
	if counter.reads > 1000 {
		t.Fatalf("root map made %d object GETs for 60 records (budget 1000)", counter.reads)
	}
	t.Logf("root map: %d object GETs", counter.reads)
}
func TestMapDoesNotHideStorageFailureAsCompleteCoverage(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	seedMapRecords(t, b, 1, false)
	unavailable := errors.New("object storage unavailable")
	b.Store.Objects = &mapReadCounter{Store: b.Store.Objects, revisionError: unavailable}
	e, err := b.Begin(ctx, p, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Call(ctx, e, "map", map[string]any{}); !errors.Is(err, unavailable) {
		t.Fatalf("storage failure swallowed: %v", err)
	}
}
func TestMapBoundsOutputAndReportsTruncation(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	seedMapRecords(t, b, 180, true)
	e, err := b.Begin(ctx, p, "alice")
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.Call(ctx, e, "map", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	var data map[string]any
	if err = json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if len(raw) > 16*1024 {
		t.Fatalf("orientation response is %d bytes, exceeds 16 KiB", len(raw))
	}
	if data["coverage"] != "truncated" {
		t.Fatalf("omitted nodes not declared: %s", raw)
	}
}

type mapFaultStore struct {
	objectstore.Store
	fail func(string) bool
	err  error
}

func (s *mapFaultStore) Get(ctx context.Context, key string) ([]byte, string, error) {
	if s.fail(key) {
		return nil, "", s.err
	}
	return s.Store.Get(ctx, key)
}
func TestMapPropagatesHeadFailuresDuringVisibilityChecks(t *testing.T) {
	// First HEAD loads the map snapshot; subsequent reads refresh per-record
	// visibility. Both snapshot and allow must preserve infrastructure errors.
	for _, failAt := range []int{2, 3} {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			ctx, b, p, _ := setupBroker(t)
			seedMapRecords(t, b, 1, false)
			e, err := b.Begin(ctx, p, "alice")
			if err != nil {
				t.Fatal(err)
			}
			heads := 0
			unavailable := errors.New("HEAD storage unavailable")
			b.Store.Objects = &mapFaultStore{Store: b.Store.Objects, err: unavailable, fail: func(key string) bool {
				if key == "spaces/alice/HEAD" {
					heads++
					return heads == failAt
				}
				return false
			}}
			result, err := b.Call(ctx, e, "map", map[string]any{})
			if !errors.Is(err, unavailable) {
				t.Fatalf("HEAD failure became successful omission: %+v %v", result, err)
			}
		})
	}
}
func TestMapPropagatesSourceStorageFailures(t *testing.T) {
	for _, faultPrefix := range []string{"spaces/bob/HEAD", "spaces/bob/nodes/"} {
		t.Run(faultPrefix, func(t *testing.T) {
			ctx, b, p, _ := setupBroker(t)
			source, err := b.Store.Load(ctx, "bob")
			if err != nil {
				t.Fatal(err)
			}
			owner := source.Space
			owner.Access = &Access{Share: []ShareEntry{{Subject: "brain-curator", Role: "reader"}}}
			source, err = b.Store.Commit(ctx, source, []Revision{{ID: "source-rev", RecordID: "source-record", Path: "/source", Kind: "experiment", Content: "source", Actor: "bob"}}, "source", Receipt{}, &owner)
			if err != nil {
				t.Fatal(err)
			}
			shared, err := b.Store.Load(ctx, "shared")
			if err != nil {
				t.Fatal(err)
			}
			_, err = b.Store.Commit(ctx, shared, []Revision{{ID: "practice-rev", RecordID: "practice", Path: "/practices/example", Kind: "practice", Content: "method", Actor: "camp-arbiter", Sources: []SourceRef{{Space: "bob", Record: "source-record", Revision: "source-rev", Path: "/source", Policy: source.Space.Policy}}}}, "practice", Receipt{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			e, err := b.Begin(ctx, p, "shared")
			if err != nil {
				t.Fatal(err)
			}
			unavailable := errors.New("source storage unavailable")
			b.Store.Objects = &mapFaultStore{Store: b.Store.Objects, err: unavailable, fail: func(key string) bool { return strings.HasPrefix(key, faultPrefix) }}
			result, err := b.Call(ctx, e, "map", map[string]any{})
			if !errors.Is(err, unavailable) {
				t.Fatalf("source failure became successful omission: %+v %v", result, err)
			}
		})
	}
}

func TestMapDiscoveryWithoutSession(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	p.Spaces = []string{"alice", "bob", "missing", "shared", "alice"}
	if err := b.Auth.Principals.SavePrincipal(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := b.Map(ctx, p, MapRequest{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	var data struct {
		Spaces []struct {
			ID      string   `json:"id"`
			Actions []string `json:"actions"`
		} `json:"spaces"`
		Coverage string `json:"coverage"`
	}
	if err = json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if data.Coverage != "complete" || len(data.Spaces) != 2 || data.Spaces[0].ID != "alice" || data.Spaces[1].ID != "shared" {
		t.Fatalf("discovery: %s", raw)
	}
	if strings.Contains(string(raw), `"bob"`) || strings.Contains(string(raw), `"owner"`) {
		t.Fatalf("leak: %s", raw)
	}
	if strings.Join(data.Spaces[0].Actions, ",") != "read,propose,write" || strings.Join(data.Spaces[1].Actions, ",") != "read,propose" {
		t.Fatalf("actions: %s", raw)
	}
	for _, sp := range data.Spaces {
		if _, err = b.Map(ctx, p, MapRequest{Space: sp.ID}); err != nil {
			t.Fatal(err)
		}
	}
	p.Revoked = true
	if err = b.Auth.Principals.SavePrincipal(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Map(ctx, p, MapRequest{}); !errors.Is(err, ErrDenied) {
		t.Fatalf("revoked: %v", err)
	}
}

func TestMapDiscoveryWildcardAndStorageFailure(t *testing.T) {
	ctx, b, _, _ := setupBroker(t)
	admin, err := b.Auth.Authenticate(ctx, "admin-test-token-32-characters-long")
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.Map(ctx, admin, MapRequest{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	if !strings.Contains(string(raw), `"id":"bob"`) {
		t.Fatalf("wildcard: %s", raw)
	}
	unavailable := errors.New("storage unavailable")
	b.Store.Objects = &mapFaultStore{Store: b.Store.Objects, err: unavailable, fail: func(key string) bool { return key == "spaces/bob/HEAD" }}
	if _, err = b.Map(ctx, admin, MapRequest{}); !errors.Is(err, unavailable) {
		t.Fatalf("storage failure hidden: %v", err)
	}
}

func TestMapDiscoveryEmptyAndBounded(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	p.Scopes = []string{"write"}
	if err := b.Auth.Principals.SavePrincipal(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := b.Map(ctx, p, MapRequest{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	if !strings.Contains(string(raw), `"spaces":[]`) {
		t.Fatalf("no read permission: %s", raw)
	}
	admin, _ := b.Auth.Authenticate(ctx, "admin-test-token-32-characters-long")
	for i := 0; i < 105; i++ {
		if _, err = b.Store.CreateSpace(ctx, Space{ID: fmt.Sprintf("team-%03d", i), Kind: "team", Owner: "owner"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err = b.Map(ctx, admin, MapRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got["coverage"] != "truncated" || len(got["spaces"].([]map[string]any)) != 100 {
		t.Fatalf("budget not enforced: %+v", got)
	}
}
