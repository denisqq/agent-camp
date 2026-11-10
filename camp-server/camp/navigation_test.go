package camp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/denisqq/agent-camp/camp-server/objectstore"
)

type navigationHeadCounter struct {
	objectstore.Store
	reads int
}

func (s *navigationHeadCounter) Get(ctx context.Context, key string) ([]byte, string, error) {
	if strings.HasSuffix(key, "/HEAD") {
		s.reads++
	}
	return s.Store.Get(ctx, key)
}

func TestNavigationDeepAncestorsBoundStorageReads(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	root := strings.Repeat("/a", 500)
	snap, err := b.Store.Load(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.Store.Commit(ctx, snap, []Revision{{ID: "deep-revision", RecordID: "deep-document", Path: root + "/doc", Kind: "note", Content: "# Deep source", Actor: "alice"}}, "deep-seed", Receipt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	objects := b.Store.Objects
	for _, budget := range []int{12288, 32768} {
		counter := &navigationHeadCounter{Store: objects}
		b.Store.Objects = counter
		page, err := b.Map(ctx, p, MapRequest{Space: "alice", Path: root, BudgetBytes: &budget})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(page)
		var view NavigationPage
		if err := json.Unmarshal(raw, &view); err != nil {
			t.Fatal(err)
		}
		if len(raw) > budget || len(view.Nodes) != 1 || view.Nodes[0].ID != "deep-document" || view.UpPath != root[:len(root)-2] {
			t.Fatalf("lost source or orientation at budget %d: %s", budget, raw)
		}
		if counter.reads > 32 {
			t.Errorf("budget %d: %d HEAD reads for one source; ancestor work must be bounded before I/O", budget, counter.reads)
		}
		t.Logf("budget=%d HEAD reads=%d ancestors=%d", budget, counter.reads, len(view.Ancestors))
	}
}

func TestNavigationDeniedAncestorsBoundStorageReads(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	root := strings.Repeat("/a", 500)
	snap, err := b.Store.CreateSpace(ctx, Space{ID: "team", Kind: "team", Owner: "other"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.Store.Commit(ctx, snap, []Revision{
		{ID: "closed-revision", RecordID: "closed", Path: "/a", Kind: "section", Content: "# PRIVATE ANCESTOR", Metadata: map[string]any{"access": map[string]any{"visibility": "closed"}}, Actor: "other"},
		{ID: "open-revision", RecordID: "open", Path: root, Kind: "section", Content: "# Open focus", Metadata: map[string]any{"access": map[string]any{"visibility": "open"}}, Actor: "other"},
		{ID: "visible-revision", RecordID: "visible", Path: root + "/doc", Kind: "note", Content: "# Source", Actor: "other"},
	}, "ancestor-acls", Receipt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p.Spaces = append(p.Spaces, "team")
	if err = b.Auth.Principals.SavePrincipal(ctx, p); err != nil {
		t.Fatal(err)
	}
	counter := &navigationHeadCounter{Store: b.Store.Objects}
	b.Store.Objects = counter
	page, err := b.Map(ctx, p, MapRequest{Space: "team", Path: root})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(page)
	var view NavigationPage
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Ancestors) != 0 || strings.Contains(string(raw), "PRIVATE ANCESTOR") || view.Location.ID != "open" || len(view.Nodes) != 2 || view.Nodes[1].ID != "visible" {
		t.Fatalf("closed ancestors leaked or visible source lost: %s", raw)
	}
	if counter.reads > 32 {
		t.Fatalf("closed ancestor search made %d HEAD reads", counter.reads)
	}
	t.Logf("closed ancestors: HEAD reads=%d", counter.reads)
}

func TestNavigationCardsMetadataAndFallback(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	snap, err := b.Store.Load(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.Store.Commit(ctx, snap, []Revision{
		{ID: "nav-r1", RecordID: "nav-1", Path: "/guides", Kind: "section", Content: "# Other heading", Metadata: map[string]any{"title": "Operations", "description": "Procedures for running Camp", "status": "current"}, Actor: "alice"},
		{ID: "nav-r2", RecordID: "nav-2", Path: "/guides/rebuild", Kind: "note", Content: "```md\n# Example only\n```\n## Rebuild the index\nprivate body not for map", Metadata: map[string]any{"title": 42, "description": false}, Actor: "alice"},
		{ID: "nav-r3", RecordID: "nav-3", Path: "/long", Kind: "note", Content: "~~~\n# Not a heading\n~~~\nNo heading", Metadata: map[string]any{"description": strings.Repeat("界", 241)}, Actor: "alice"},
	}, "nav-seed", Receipt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.Map(ctx, p, MapRequest{Space: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	var data struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if err = json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	nodes := map[string]map[string]any{}
	for _, n := range data.Nodes {
		nodes[n["path"].(string)] = n
	}
	section := nodes["/guides"]
	if section["title"] != "Operations" || section["description"] != "Procedures for running Camp" || section["status"] != "current" || section["entry_type"] != "document_branch" {
		t.Fatalf("missing section card: %v", section)
	}
	if section["id"] != "nav-1" || section["revision"] != "nav-r1" || section["description_source"] != "metadata" {
		t.Fatalf("missing identity/provenance: %v", section)
	}
	doc := nodes["/guides/rebuild"]
	if doc["title"] != "Rebuild the index" || doc["entry_type"] != "document" || doc["description"] != nil {
		t.Fatalf("bad heading fallback: %v", doc)
	}
	long := nodes["/long"]
	description, _ := long["description"].(string)
	if long["title"] != "long" || !utf8.ValidString(description) || utf8.RuneCountInString(description) != 240 || long["text_truncated"] != true {
		t.Fatalf("unbounded card: %v", long)
	}
	if strings.Contains(string(raw), "private body not for map") {
		t.Fatal("map contains body text")
	}
}

func TestNavigationRootDocumentPath(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	snap, err := b.Store.Load(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.Store.Commit(ctx, snap, []Revision{{ID: "root-rev", RecordID: "root-doc", Path: "/", Kind: "section", Content: "# Workspace", Actor: "alice"}}, "root-navigation", Receipt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.Map(ctx, p, MapRequest{Space: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	var data struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if err = json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Nodes) != 1 || data.Nodes[0]["path"] != "/" || data.Nodes[0]["title"] != "Workspace" {
		t.Fatalf("invalid root card: %s", raw)
	}
}

func TestNavigationCardUsesOwnStagedMetadata(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	snap, err := b.Store.Load(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.Store.Commit(ctx, snap, []Revision{{ID: "stage-old", RecordID: "stage-doc", Path: "/note", Kind: "note", Content: "# Old", Metadata: map[string]any{"title": "Old title"}, Actor: "alice"}}, "stage-card-seed", Receipt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := b.Begin(ctx, p, "alice")
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.Call(ctx, execution, "write", map[string]any{"path": "/note", "content": "# New", "metadata": map[string]any{"title": "New title"}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.Call(ctx, execution, "map", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	var data struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if err = json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Nodes) != 1 || data.Nodes[0]["title"] != "New title" {
		t.Fatalf("mixed staged revision and old metadata: %s", raw)
	}
}
