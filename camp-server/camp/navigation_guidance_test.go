package camp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestMapGuidanceScopesAndOrder(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	snap, err := b.Store.Load(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	docs := []Revision{
		{ID: "gr1", RecordID: "gd1", Path: "/RULES.md", Kind: "note", Content: "# Rules", Actor: "alice"},
		{ID: "gr2", RecordID: "gd2", Path: "/runbooks/ABOUT.md", Kind: "note", Content: "# About", Actor: "alice"},
		{ID: "gr3", RecordID: "gd3", Path: "/runbooks/db/WORKFLOW.md", Kind: "note", Content: "# Workflow", Actor: "alice"},
		{ID: "gr4", RecordID: "gd4", Path: "/other/RULES.md", Kind: "note", Content: "# Other", Actor: "alice"},
	}
	if _, err = b.Store.Commit(ctx, snap, docs, "guidance-seed", Receipt{}, nil); err != nil {
		t.Fatal(err)
	}
	out, err := b.Map(ctx, p, MapRequest{Space: "alice", Path: "/runbooks/db"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	var data struct {
		Guidance struct {
			AppliesTo string                                         `json:"applies_to"`
			Complete  bool                                           `json:"complete"`
			Items     []struct{ Path, Scope, Role, Revision string } `json:"items"`
		} `json:"guidance"`
	}
	if err = json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if !data.Guidance.Complete || data.Guidance.AppliesTo != "/runbooks/db" || len(data.Guidance.Items) != 3 {
		t.Fatalf("bad guidance: %s", raw)
	}
	for i, want := range []string{"/RULES.md", "/runbooks/ABOUT.md", "/runbooks/db/WORKFLOW.md"} {
		if data.Guidance.Items[i].Path != want || data.Guidance.Items[i].Revision == "" {
			t.Fatalf("wrong order: %s", raw)
		}
	}
}

func TestMapGuidanceContinuesBeforeNodes(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	root := ""
	for i := 0; i < 10; i++ {
		root += "/x"
	}
	snap, err := b.Store.Load(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	docs := []Revision{
		{ID: "pgr1", RecordID: "pgd1", Path: "/RULES.md", Kind: "note", Content: "root", Actor: "alice"},
		{ID: "pgr2", RecordID: "pgd2", Path: root + "/WORKFLOW.md", Kind: "note", Content: "leaf", Actor: "alice"},
		{ID: "pgr3", RecordID: "pgd3", Path: root + "/doc", Kind: "note", Content: "doc", Actor: "alice"},
	}
	if _, err = b.Store.Commit(ctx, snap, docs, "guidance-pages", Receipt{}, nil); err != nil {
		t.Fatal(err)
	}
	req := MapRequest{Space: "alice", Path: root}
	got := []string{}
	seenNodes := false
	for i := 0; i < 10; i++ {
		out, err := b.Map(ctx, p, req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(out)
		if len(raw) > 12288 {
			t.Fatalf("oversized page %d", len(raw))
		}
		var pg NavigationPage
		if err = json.Unmarshal(raw, &pg); err != nil {
			t.Fatal(err)
		}
		for _, item := range pg.Guidance.Items {
			got = append(got, item.Path)
		}
		if len(pg.Nodes) > 0 {
			if !pg.Guidance.Complete {
				t.Fatal("nodes preceded guidance")
			}
			seenNodes = true
		}
		if pg.Cursor == "" {
			break
		}
		req.Cursor = pg.Cursor
	}
	if len(got) != 2 || got[0] != "/RULES.md" || got[1] != root+"/WORKFLOW.md" || !seenNodes {
		t.Fatalf("incomplete traversal: %v nodes=%v", got, seenNodes)
	}
}

func TestMapGuidanceOmitsDeniedAncestors(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	snap, err := b.Store.CreateSpace(ctx, Space{ID: "team", Kind: "team", Owner: "other"})
	if err != nil {
		t.Fatal(err)
	}
	docs := []Revision{
		{ID: "ar1", RecordID: "ad1", Path: "/secret", Kind: "section", Content: "closed", Metadata: map[string]any{"access": map[string]any{"visibility": "closed"}}, Actor: "other"},
		{ID: "ar2", RecordID: "ad2", Path: "/secret/RULES.md", Kind: "note", Content: "# PRIVATE GUIDANCE", Actor: "other"},
		{ID: "ar3", RecordID: "ad3", Path: "/secret/open", Kind: "section", Content: "open", Metadata: map[string]any{"access": map[string]any{"visibility": "open"}}, Actor: "other"},
		{ID: "ar4", RecordID: "ad4", Path: "/secret/open/ABOUT.md", Kind: "note", Content: "# public", Actor: "other"},
	}
	if _, err = b.Store.Commit(ctx, snap, docs, "guidance-access", Receipt{}, nil); err != nil {
		t.Fatal(err)
	}
	p.Spaces = append(p.Spaces, "team")
	if err = b.Auth.Principals.SavePrincipal(ctx, p); err != nil {
		t.Fatal(err)
	}
	out, err := b.Map(ctx, p, MapRequest{Space: "team", Path: "/secret/open"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	var pg NavigationPage
	if err = json.Unmarshal(raw, &pg); err != nil {
		t.Fatal(err)
	}
	if len(pg.Guidance.Items) != 1 || pg.Guidance.Items[0].Path != "/secret/open/ABOUT.md" {
		t.Fatalf("wrong visible guidance: %s", raw)
	}
	if strings.Contains(string(raw), "PRIVATE GUIDANCE") || strings.Contains(string(raw), "/secret/RULES.md") {
		t.Fatalf("private guidance leaked: %s", raw)
	}
}

func TestMapGuidanceRespectsSmallBudgetAcrossPages(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	snap, err := b.Store.Load(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	var docs []Revision
	root := ""
	for i := 0; i < 12; i++ {
		root += "/branch"
		docs = append(docs, Revision{ID: fmt.Sprintf("budget-rev-%d", i), RecordID: fmt.Sprintf("budget-rec-%d", i), Path: root + "/RULES.md", Kind: "note", Content: "# " + strings.Repeat("Wide title ", 30), Actor: "alice"})
	}
	if _, err = b.Store.Commit(ctx, snap, docs, "guidance-budget", Receipt{}, nil); err != nil {
		t.Fatal(err)
	}
	budget := 4096
	req := MapRequest{Space: "alice", Path: root, BudgetBytes: &budget}
	seen := map[string]bool{}
	for i := 0; i < 30; i++ {
		result, err := b.Map(ctx, p, req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(result)
		if len(raw) > budget {
			t.Fatalf("page %d bytes exceeds budget", len(raw))
		}
		var page NavigationPage
		if err = json.Unmarshal(raw, &page); err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Guidance.Items {
			if seen[item.ID] {
				t.Fatalf("duplicate %s", item.ID)
			}
			seen[item.ID] = true
		}
		if page.Cursor == "" {
			break
		}
		req.Cursor = page.Cursor
	}
	if len(seen) != 12 {
		t.Fatalf("found %d instructions", len(seen))
	}
}
