package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/denisqq/agent-camp/camp-server/camp"
	"github.com/denisqq/agent-camp/camp-server/index"
	"github.com/denisqq/agent-camp/camp-server/objectstore"
	"github.com/denisqq/agent-camp/camp-server/runner"
	"github.com/denisqq/agent-camp/camp-server/teststore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMontyMCPStack(t *testing.T) {
	python := os.Getenv("CAMP_TEST_PYTHON")
	if python == "" {
		t.Skip("set CAMP_TEST_PYTHON for real Monty")
	}
	testMontyMCPStack(t, teststore.New(), python)
}

func TestActualS3MontyMCPStack(t *testing.T) {
	endpoint := os.Getenv("CAMP_TEST_S3_ENDPOINT")
	python := os.Getenv("CAMP_TEST_PYTHON")
	if endpoint == "" || python == "" {
		t.Skip("real S3 and Monty required")
	}
	ctx := context.Background()
	objects, err := objectstore.NewS3(ctx, objectstore.S3Config{Endpoint: endpoint, Region: "us-east-1", Bucket: "camp-stack-" + camp.ID(), AccessKey: "camp-local-test", SecretKey: "camp-local-test-password"})
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	testMontyMCPStack(t, objects, python)
}

func testMontyMCPStack(t *testing.T, objects objectstore.Store, python string) {
	t.Helper()
	ctx := context.Background()
	store := camp.NewStore(objects)
	a := camp.NewAuth(camp.NewMemState())
	adminToken := "stack-admin-" + camp.ID()
	if err := a.Bootstrap(ctx, adminToken); err != nil {
		t.Fatal(err)
	}
	admin, _ := a.Authenticate(ctx, adminToken)
	if _, err := store.CreateSpace(ctx, camp.Space{ID: "alice", Kind: "personal", Owner: "alice"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSpace(ctx, camp.Space{ID: "shared", Kind: "shared", Owner: "system"}); err != nil {
		t.Fatal(err)
	}
	token, err := a.Issue(ctx, admin, camp.Principal{Subject: "alice", Role: "author", Spaces: []string{"alice", "shared"}, Scopes: []string{"read", "write", "propose"}, Expires: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	script, _ := filepath.Abs("../../runner/camp_runner.py")
	pool := runner.New(runner.Config{Python: python, Script: script, Monty: filepath.Join(filepath.Dir(python), "monty"), Workers: 1})
	defer pool.Close()
	idx, err := index.Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	b := camp.NewBroker(store, a)
	b.Search = idx
	// Seed a small shared library for discovery and pagination checks.
	seed, err := b.Begin(ctx, admin, "shared")
	if err != nil {
		t.Fatal(err)
	}
	for _, doc := range []struct{ path, kind, content string }{
		{"/skills/review", "skill", "Review the change and report actionable findings."},
		{"/skills/test", "skill", "Run the relevant tests."},
		{"/skills/document", "skill", "Keep documentation consistent with behavior."},
		{"/rules/workspace", "constitution", "Read existing documents before proposing changes."},
	} {
		if _, err := b.Call(ctx, seed, "write", map[string]any{"path": doc.path, "kind": doc.kind, "content": doc.content}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Finish(ctx, seed, "seed", camp.Receipt{}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(camp.NewSessions(b, pool)))
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "acceptance", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: tokenTransport{token}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	call := func(name string, args map[string]any, out any) {
		t.Helper()
		r, e := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if e != nil || r.IsError {
			t.Fatalf("%s: %+v %v", name, r, e)
		}
		data, _ := json.Marshal(r.StructuredContent)
		if e = json.Unmarshal(data, out); e != nil {
			t.Fatal(e)
		}
	}
	var discovered struct {
		Spaces []struct {
			ID string `json:"id"`
		} `json:"spaces"`
	}
	call("map", map[string]any{}, &discovered)
	if len(discovered.Spaces) != 2 {
		t.Fatalf("discovery: %+v", discovered)
	}
	var entry camp.SessionView
	call("start", map[string]any{"primary_space": discovered.Spaces[1].ID}, &entry)
	examples := entry.Help["examples"].(map[string]any)
	for _, name := range []string{"catalog", "rules", "read", "help"} {
		var result camp.Receipt
		call("run", map[string]any{"session_id": entry.ID, "operation_id": "entry-" + name, "code": strings.ReplaceAll(examples[name].(string), "/path/from/catalog", "/skills/review")}, &result)
		if result.Status != "no_changes" {
			t.Fatalf("entry example %s: %+v", name, result)
		}
	}
	var overview map[string]any
	began := time.Now()
	call("map", map[string]any{"space": "shared", "depth": 2}, &overview)
	if overview["coverage"] != "complete" || overview["records"] != float64(4) {
		t.Fatalf("seeded root map incomplete: %+v", overview)
	}
	t.Logf("MCP root map: %s", time.Since(began))
	var start camp.SessionView
	call("start", map[string]any{"primary_space": "alice"}, &start)
	var sdkOverview camp.Receipt
	call("run", map[string]any{"session_id": start.ID, "operation_id": "sdk-root-map", "code": "from camp_sdk import camp\ncamp.map(space='shared')"}, &sdkOverview)
	if sdkOverview.Status != "no_changes" {
		t.Fatalf("SDK map: %+v", sdkOverview)
	}
	directJSON, _ := json.Marshal(overview)
	sdkJSON, _ := json.Marshal(sdkOverview.Result)
	if !bytes.Equal(directJSON, sdkJSON) {
		t.Fatal("real SDK and direct map differ")
	}
	var treeOverview camp.Receipt
	call("run", map[string]any{"session_id": start.ID, "operation_id": "sdk-tree-map", "code": "from camp_sdk import camp\ncamp.map('/', 2, 'shared', view='tree', budget_bytes=4096)"}, &treeOverview)
	if treeOverview.Status != "no_changes" {
		t.Fatalf("SDK tree options: %+v", treeOverview)
	}
	var treeResult map[string]any
	rawTree, _ := json.Marshal(treeOverview.Result)
	if err := json.Unmarshal(rawTree, &treeResult); err != nil {
		t.Fatal(err)
	}
	if treeResult["view"] != "tree" || len(rawTree) > 4096 {
		t.Fatalf("SDK map options not honored: %s", rawTree)
	}
	var catalog camp.Receipt
	call("run", map[string]any{"session_id": start.ID, "operation_id": "catalog-visible", "code": "from camp_sdk import camp\ncamp.find(path='/skills/', kind='skill', space='shared', limit=5)"}, &catalog)
	if catalog.Status != "no_changes" {
		t.Fatal("model-visible skill catalog exceeded output budget", catalog)
	}

	var library camp.Receipt
	call("run", map[string]any{"session_id": start.ID, "operation_id": "library", "code": "from camp_sdk import camp\npage = camp.find(path='/skills/', kind='skill', space='shared', limit=2)\nassert len(page['items']) == 2\nassert page['cursor']\ntext = camp.read('/skills/review', space='shared')\nassert text['content']\n{'loaded': True}"}, &library)
	if library.Status != "no_changes" {
		t.Fatal("MCP skill discovery", library)
	}
	var rules camp.Receipt
	call("run", map[string]any{"session_id": start.ID, "operation_id": "rules", "code": "from camp_sdk import camp\ncamp.read('/rules/workspace', space='shared')"}, &rules)
	payload, _ := json.Marshal(rules.Result)
	if rules.Status != "no_changes" || !strings.Contains(string(payload), "Read existing documents") {
		t.Fatal("MCP rules read", rules)
	}
	var parallel camp.SessionView
	call("start", map[string]any{"primary_space": "alice"}, &parallel)
	req := map[string]any{"session_id": start.ID, "operation_id": "create", "code": "from camp_sdk import camp\ncamp.write('/goals/growth', 'Рост канала', kind='goal', metadata={'status': 'active'})\ncamp.write('/cases/failure', 'Неудачный опыт', kind='experiment', metadata={'outcome': 'failure'})\n{'saved': 2}"}
	var receipt camp.Receipt
	call("run", req, &receipt)
	if receipt.Status != "completed" || receipt.ChangeID == "" {
		t.Fatalf("run: %+v", receipt)
	}
	first := receipt.ChangeID
	var independent camp.Receipt
	call("run", map[string]any{"session_id": parallel.ID, "operation_id": "independent", "code": "from camp_sdk import camp\ncamp.write('/research/other', 'Independent agent work')"}, &independent)
	if independent.Status != "completed" {
		t.Fatal("independent MCP session conflicted", independent)
	}
	call("run", req, &receipt)
	if receipt.ChangeID != first {
		t.Fatal("duplicate changeset")
	}
	if err = idx.Sync(ctx, store, "alice"); err != nil {
		t.Fatal(err)
	}
	call("run", map[string]any{"session_id": start.ID, "operation_id": "search", "code": "from camp_sdk import camp\ncamp.search('Неудачный')"}, &receipt)
	if receipt.Status != "no_changes" {
		t.Fatal(receipt)
	}
	encoded, _ := json.Marshal(receipt.Result)
	var result struct {
		Items []camp.SearchHit `json:"items"`
	}
	json.Unmarshal(encoded, &result)
	if len(result.Items) != 1 || result.Items[0].Path != "/cases/failure" {
		t.Fatalf("search: %s", encoded)
	}
	call("run", map[string]any{"session_id": start.ID, "operation_id": "rollback", "code": "from camp_sdk import camp\ncamp.write('/must-not-exist','no')\nraise ValueError('stop')"}, &receipt)
	if receipt.Status != "failed" {
		t.Fatal(receipt)
	}
	snap, _ := store.Load(ctx, "alice")
	if _, err = store.RecordByPath(ctx, snap, "/must-not-exist"); err == nil {
		t.Fatal("partial commit")
	}
	var seededPages camp.Receipt
	call("run", map[string]any{"session_id": start.ID, "operation_id": "seed-map-pages", "code": "from camp_sdk import camp\nfor i in range(18):\n    camp.write('/cursor-pages/doc-' + str(i), '# Source', kind='note', metadata={'description': '界' * 240})\n{'seeded': 18}"}, &seededPages)
	if seededPages.Status != "completed" {
		t.Fatalf("seed cursor stack: %+v", seededPages)
	}
	var firstPage camp.NavigationPage
	call("map", map[string]any{"space": "alice", "path": "/cursor-pages", "view": "tree", "budget_bytes": 4096}, &firstPage)
	if firstPage.Cursor == "" {
		t.Fatal("stack fixture needs cursor")
	}
	var secondPage camp.NavigationPage
	call("map", map[string]any{"space": "alice", "path": "/cursor-pages", "view": "tree", "budget_bytes": 4096, "cursor": firstPage.Cursor}, &secondPage)
	seen := map[string]bool{}
	page := firstPage
	for i := 0; i < 20; i++ {
		for _, node := range page.Nodes {
			if seen[node.ID] {
				t.Fatal("duplicate SDK continuation")
			}
			seen[node.ID] = true
		}
		if page.Cursor == "" {
			break
		}
		var continued camp.Receipt
		code := fmt.Sprintf("from camp_sdk import camp\ncamp.map('/cursor-pages', view='tree', cursor=%q, budget_bytes=4096)", page.Cursor)
		call("run", map[string]any{"session_id": start.ID, "operation_id": fmt.Sprintf("map-page-%d", i), "code": code}, &continued)
		if continued.Status != "no_changes" {
			t.Fatalf("SDK cursor: %+v", continued)
		}
		raw, _ := json.Marshal(continued.Result)
		if len(raw) > 4096 {
			t.Fatal("SDK cursor exceeded budget")
		}
		page = camp.NavigationPage{}
		if err = json.Unmarshal(raw, &page); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			a, _ := json.Marshal(page.Nodes)
			b, _ := json.Marshal(secondPage.Nodes)
			if !bytes.Equal(a, b) {
				t.Fatal("MCP and SDK continuation disagree")
			}
		}
	}
	if len(seen) != 18 {
		t.Fatalf("SDK pagination stranded sources: %d", len(seen))
	}
}
