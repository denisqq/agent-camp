package api

import (
	"bytes"
	"github.com/denisqq/agent-camp/internal/camp"
	"github.com/denisqq/agent-camp/internal/index"
	"github.com/denisqq/agent-camp/internal/objectstore"
	"github.com/denisqq/agent-camp/internal/runner"
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestActualS3MontyMCPStack(t *testing.T) {
	endpoint := os.Getenv("BRAIN_TEST_S3_ENDPOINT")
	python := os.Getenv("BRAIN_TEST_PYTHON")
	if endpoint == "" || python == "" {
		t.Skip("real S3 and Monty required")
	}
	ctx := context.Background()
	objects, err := objectstore.NewS3(ctx, objectstore.S3Config{Endpoint: endpoint, Region: "us-east-1", Bucket: "brain-stack-" + camp.ID(), AccessKey: "brain-local-test", SecretKey: "brain-local-test-password"})
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	store := camp.NewStore(objects)
	a := camp.NewAuth(camp.NewMemState())
	adminToken := "stack-admin-" + camp.ID()
	if err = a.Bootstrap(ctx, adminToken); err != nil {
		t.Fatal(err)
	}
	admin, _ := a.Authenticate(ctx, adminToken)
	store.CreateSpace(ctx, camp.Space{ID: "alice", Kind: "personal", Owner: "alice"})
	if _, err = store.CreateSpace(ctx, camp.Space{ID: "shared", Kind: "shared", Owner: "system"}); err != nil {
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
	for _, name := range []string{"catalog", "rules", "help"} {
		var result camp.Receipt
		call("run", map[string]any{"session_id": entry.ID, "operation_id": "entry-" + name, "code": examples[name]}, &result)
		if result.Status != "no_changes" {
			t.Fatalf("entry example %s: %+v", name, result)
		}
	}
	var overview map[string]any
	began := time.Now()
	call("map", map[string]any{"space": "shared", "depth": 2}, &overview)
	if overview["coverage"] != "complete" || overview["records"] != float64(56) {
		t.Fatalf("seeded root map incomplete: %+v", overview)
	}
	t.Logf("real S3 MCP root map: %s", time.Since(began))
	var start camp.SessionView
	call("start", map[string]any{"primary_space": "alice"}, &start)
	var sdkOverview camp.Receipt
	call("run", map[string]any{"session_id": start.ID, "operation_id": "sdk-root-map", "code": "from brain_sdk import brain\nbrain.map(space='shared')"}, &sdkOverview)
	if sdkOverview.Status != "no_changes" {
		t.Fatalf("SDK map: %+v", sdkOverview)
	}
	directJSON, _ := json.Marshal(overview)
	sdkJSON, _ := json.Marshal(sdkOverview.Result)
	if !bytes.Equal(directJSON, sdkJSON) {
		t.Fatal("real SDK and direct map differ")
	}
	var catalog camp.Receipt
	call("run", map[string]any{"session_id": start.ID, "operation_id": "catalog-visible", "code": "from brain_sdk import brain\nbrain.find(path='/skills/', kind='skill', space='shared', limit=5)"}, &catalog)
	if catalog.Status != "no_changes" {
		t.Fatal("model-visible skill catalog exceeded output budget", catalog)
	}
	instructions, err := os.ReadFile("../../seeds/coauthor/skills/using-company-brain/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	readExample := regexp.MustCompile("brain\\.read\\('/skills/post-writing-partner/SKILL\\.md'[^`]+\\)").FindString(string(instructions))
	if readExample == "" {
		t.Fatal("missing documented skill read example")
	}
	var visibleSkill camp.Receipt
	call("run", map[string]any{"session_id": start.ID, "operation_id": "documented-skill-read", "code": "from brain_sdk import brain\n" + readExample}, &visibleSkill)
	if visibleSkill.Status != "no_changes" {
		t.Fatal("documented skill read exceeds runtime output budget", visibleSkill)
	}
	var constitution camp.Receipt
	call("run", map[string]any{"session_id": start.ID, "operation_id": "constitution", "code": "from brain_sdk import brain\nbrain.read('/constitutions/coauthor/CONSTITUTION.md', space='shared', limit=40)"}, &constitution)
	if constitution.Status != "no_changes" {
		t.Fatal("MCP constitution read", constitution)
	}
	payload, _ := json.Marshal(constitution.Result)
	if !strings.Contains(string(payload), "Конституция агентов Coauthor") {
		t.Fatal("constitution missing from result")
	}
	var library camp.Receipt
	call("run", map[string]any{"session_id": start.ID, "operation_id": "library", "code": "from brain_sdk import brain\npage = camp.find(path='/skills/', kind='skill', space='shared', limit=2)\nassert len(page['items']) == 2\nassert page['cursor']\ntext = camp.read('/skills/post-writing-partner/SKILL.md', space='shared')\nassert text['content']\nresource = camp.read('/skills/styling-deliverables/scripts/theme.py', space='shared')\nassert resource['content']\n{'loaded': True}"}, &library)
	if library.Status != "no_changes" {
		t.Fatal("MCP skill discovery", library)
	}
	var parallel camp.SessionView
	call("start", map[string]any{"primary_space": "alice"}, &parallel)
	req := map[string]any{"session_id": start.ID, "operation_id": "create", "code": "from brain_sdk import brain\nbrain.write('/goals/growth', 'Рост канала', kind='goal', metadata={'status': 'active'})\nbrain.write('/cases/failure', 'Неудачный опыт', kind='experiment', metadata={'outcome': 'failure'})\n{'saved': 2}"}
	var receipt camp.Receipt
	call("run", req, &receipt)
	if receipt.Status != "completed" || receipt.ChangeID == "" {
		t.Fatalf("run: %+v", receipt)
	}
	first := receipt.ChangeID
	var independent camp.Receipt
	call("run", map[string]any{"session_id": parallel.ID, "operation_id": "independent", "code": "from brain_sdk import brain\nbrain.write('/research/other', 'Independent agent work')"}, &independent)
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
	call("run", map[string]any{"session_id": start.ID, "operation_id": "search", "code": "from brain_sdk import brain\nbrain.search('Неудачный')"}, &receipt)
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
	call("run", map[string]any{"session_id": start.ID, "operation_id": "rollback", "code": "from brain_sdk import brain\nbrain.write('/must-not-exist','no')\nraise ValueError('stop')"}, &receipt)
	if receipt.Status != "failed" {
		t.Fatal(receipt)
	}
	snap, _ := store.Load(ctx, "alice")
	if _, err = store.RecordByPath(ctx, snap, "/must-not-exist"); err == nil {
		t.Fatal("partial commit")
	}
}
