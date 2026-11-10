package camp

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"testing"
)

func viewCard(path string) MapNode {
	return MapNode{Path: path, ID: path, Revision: "revision", Kind: "note", Title: "Same title", Description: strings.Repeat("界", 240)}
}

func TestAdaptiveNavigationFrontier(t *testing.T) {
	cards := []MapNode{viewCard("/guides"), viewCard("/guides/a"), viewCard("/guides/b"), viewCard("/empty/chain/leaf")}
	for i := 0; i < 9; i++ {
		cards = append(cards, viewCard(fmt.Sprintf("/wide/%02d", i)))
	}
	input := NavigationInput{Root: "/", Snapshot: "s", Cards: cards, ScanComplete: true}
	page, err := renderNavigation(input, NavigationOptions{Depth: 2, BudgetBytes: 12288})
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]MapNode{}
	for _, n := range page.Nodes {
		nodes[n.Path] = n
	}
	if n := nodes["/guides"]; n.EntryType != "document_branch" || !hasAction(n, "read") || !hasAction(n, "map") {
		t.Fatalf("document branch lost: %+v", n)
	}
	if _, ok := nodes["/guides/a"]; !ok {
		t.Fatal("small branch not expanded")
	}
	if n := nodes["/empty/chain/leaf"]; n.ID == "" || n.CollapsedFrom != "/empty" {
		t.Fatalf("empty chain not shortened: %+v", n)
	}
	if n := nodes["/wide"]; n.EntryType != "branch" || n.Description != "" || n.Count != 9 {
		t.Fatalf("wide branch not folded: %+v", n)
	}
	if _, ok := nodes["/wide/00"]; ok {
		t.Fatal("wide branch expanded")
	}
	if page.Location.Path != "/" || !page.OutputComplete {
		t.Fatalf("missing orientation: %+v", page)
	}
	// An unfinished scan cannot certify that a branch is small or single-child.
	input.ScanComplete = false
	page, err = renderNavigation(input, NavigationOptions{Depth: 4, BudgetBytes: 12288})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range page.Nodes {
		if strings.Count(n.Path, "/") > 1 {
			t.Fatalf("expanded partial branch: %+v", n)
		}
	}
}
func hasAction(n MapNode, action string) bool {
	for _, a := range n.Actions {
		if a == action {
			return true
		}
	}
	return false
}

func TestNavigationDepthAndDirectExpansion(t *testing.T) {
	cards := []MapNode{viewCard("/a"), viewCard("/a/b"), viewCard("/a/b/c")}
	page, err := renderNavigation(NavigationInput{Root: "/", Cards: cards, ScanComplete: true}, NavigationOptions{Depth: 1, BudgetBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Nodes) != 1 || page.Nodes[0].Path != "/a" {
		t.Fatalf("depth ignored: %+v", page.Nodes)
	}
	page, err = renderNavigation(NavigationInput{Root: "/a/b", Cards: cards[1:], ScanComplete: true}, NavigationOptions{Depth: 1, BudgetBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Nodes) != 2 || page.Location.ID != "/a/b" {
		t.Fatalf("direct expansion lost focus or child: %+v", page)
	}
}

func TestNavigationBudgetAndCoverage(t *testing.T) {
	for _, budget := range []int{4096, 12288, 32768} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			input := NavigationInput{Root: "/", Snapshot: "s", ScanComplete: true}
			for i := 0; i < 250; i++ {
				input.Cards = append(input.Cards, viewCard(fmt.Sprintf("/%03d-%s", i, strings.Repeat("x", 1000))))
			}
			seen := map[string]bool{}
			offset := 0
			for calls := 0; calls < 251; calls++ {
				page, err := renderNavigation(input, NavigationOptions{View: "adaptive", Depth: 2, BudgetBytes: budget, Offset: offset})
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(page)
				if len(raw) > budget {
					t.Fatalf("%d > %d", len(raw), budget)
				}
				for _, n := range page.Nodes {
					if seen[n.ID] {
						t.Fatal("duplicate card")
					}
					seen[n.ID] = true
				}
				if page.OutputComplete {
					break
				}
				if page.NextOffset <= offset {
					t.Fatalf("no progress: %+v", page)
				}
				if page.Coverage != "truncated" {
					t.Fatal("output omission hidden")
				}
				offset = page.NextOffset
			}
			if len(seen) != 250 {
				t.Fatalf("lost cards: %d", len(seen))
			}
		})
	}
	empty, err := renderNavigation(NavigationInput{Root: "/", ScanComplete: true}, NavigationOptions{Depth: 2, BudgetBytes: 4096})
	if err != nil || !empty.OutputComplete || empty.Coverage != "complete" || empty.Nodes == nil {
		t.Fatalf("empty map: %+v %v", empty, err)
	}
	for _, opts := range []NavigationOptions{{Depth: 0, BudgetBytes: 4096}, {Depth: 5, BudgetBytes: 4096}, {Depth: 2, BudgetBytes: 4000}, {Depth: 2, BudgetBytes: 32769}, {View: "ranked", Depth: 2, BudgetBytes: 4096}} {
		if _, err := renderNavigation(NavigationInput{Root: "/", ScanComplete: true}, opts); !errors.Is(err, ErrInvalid) {
			t.Fatalf("bad options accepted %+v: %v", opts, err)
		}
	}
	// Required paths must remain exact; an unrenderable card fails explicitly.
	_, err = renderNavigation(NavigationInput{Root: "/", Cards: []MapNode{viewCard("/" + strings.Repeat("x", 8000))}, ScanComplete: true}, NavigationOptions{Depth: 2, BudgetBytes: 4096})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "required") {
		t.Fatalf("missing required size error: %v", err)
	}
}

func TestMapAdaptiveIntegration(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	snap, err := b.Store.Load(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.Store.Commit(ctx, snap, []Revision{
		{ID: "r1", RecordID: "a", Path: "/work", Kind: "section", Content: "# Work", Actor: "alice"},
		{ID: "r2", RecordID: "b", Path: "/work/chain/deep/task", Kind: "note", Content: "# Task", Actor: "alice"},
	}, "adaptive-seed", Receipt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	depth := 1
	budget := 4096
	result, err := b.Map(ctx, p, MapRequest{Space: "alice", Path: "/work", Depth: &depth, BudgetBytes: &budget, View: "adaptive"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	var page NavigationPage
	if err = json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	if len(raw) > budget || page.Location.ID != "a" || !page.ScanComplete || !page.OutputComplete || page.IndexState != "authoritative" {
		t.Fatalf("bad page: %s", raw)
	}
	if len(page.Ancestors) != 1 || page.Ancestors[0].Path != "/" {
		t.Fatalf("no upward route: %s", raw)
	}
	if len(page.Nodes) != 2 || page.Nodes[1].ID != "b" || page.Nodes[1].CollapsedFrom != "/work/chain" {
		t.Fatalf("no short route: %s", raw)
	}
	tree, err := b.Map(ctx, p, MapRequest{Space: "alice", Path: "/work", Depth: &depth, View: "tree"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(tree)
	page = NavigationPage{}
	_ = json.Unmarshal(raw, &page)
	if len(page.Nodes) != 2 || page.Nodes[1].Path != "/work/chain" || page.Nodes[1].ID != "" {
		t.Fatalf("tree compatibility: %s", raw)
	}
	for _, request := range []MapRequest{{Space: "alice", View: "ranked"}, {Space: "alice", BudgetBytes: new(int)}} {
		if _, err = b.Map(ctx, p, request); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid request accepted: %+v %v", request, err)
		}
	}
}

func TestMapRejectsMalformedViewOptions(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	for _, args := range []map[string]any{{"view": 42}, {"budget_bytes": "bad"}, {"budget_bytes": 4096.5}, {"depth": 1.5}, {"depth": false}} {
		e, err := b.Begin(ctx, p, "alice")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = b.Call(ctx, e, "map", args); !errors.Is(err, ErrInvalid) {
			t.Fatalf("malformed options accepted: %v %v", args, err)
		}
	}
}

func TestNavigationKindsAndAncestorsStayBounded(t *testing.T) {
	input := NavigationInput{Root: "/a/b", Snapshot: "s", ScanComplete: true}
	for i := 0; i < 40; i++ {
		card := viewCard(fmt.Sprintf("/a/b/%03d", i))
		card.Kind = fmt.Sprintf("kind%03d", i)
		input.Cards = append(input.Cards, card)
	}
	input.Ancestors = []MapNode{{Path: "/a", Title: strings.Repeat("界", 120), EntryType: "branch", Actions: []string{"map"}}, {Path: "/", EntryType: "branch", Actions: []string{"map"}}}
	offset := 0
	for {
		page, err := renderNavigation(input, NavigationOptions{View: "tree", Depth: 2, BudgetBytes: 4096, Offset: offset})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(page.Ancestors)
		if len(raw) > 1024 || (len(page.Kinds) == 0 || len(page.Kinds) > 32) || !page.KindsTruncated || (len(page.Location.Kinds) == 0 || len(page.Location.Kinds) > 32) || !page.Location.KindsTruncated {
			t.Fatalf("unbounded aggregates: %+v", page)
		}
		if page.UpPath != "/a" {
			t.Fatal("missing upward action")
		}
		if page.OutputComplete {
			break
		}
		if page.NextOffset <= offset {
			t.Fatal("no progress")
		}
		offset = page.NextOffset
	}
}

func TestPartialNavigationPreservesDocumentExpansion(t *testing.T) {
	page, err := renderNavigation(NavigationInput{Root: "/", Cards: []MapNode{viewCard("/a")}, ScanComplete: false}, NavigationOptions{Depth: 2, BudgetBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Nodes) != 1 || !hasAction(page.Nodes[0], "read") || !hasAction(page.Nodes[0], "map") {
		t.Fatalf("partial scan implied a leaf: %+v", page.Nodes)
	}
}

func TestNavigationOptionalMetadataCannotBlockProgress(t *testing.T) {
	t.Run("long kinds", func(t *testing.T) {
		input := NavigationInput{Root: "/", ScanComplete: true}
		for i := 0; i < 32; i++ {
			input.Cards = append(input.Cards, MapNode{Path: fmt.Sprintf("/%02d", i), ID: fmt.Sprint(i), Revision: "r", Kind: fmt.Sprintf("%03d%s", i, strings.Repeat("k", 125))})
		}
		page, err := renderNavigation(input, NavigationOptions{View: "tree", Depth: 2, BudgetBytes: 4096})
		if err != nil || len(page.Nodes) == 0 || !page.KindsTruncated {
			t.Fatalf("kind dictionary blocks navigation: %+v %v", page, err)
		}
	})
	t.Run("ancestor", func(t *testing.T) {
		root := "/" + strings.Repeat("a", 500) + "/b"
		card := MapNode{Path: root + "/leaf", ID: "id", Revision: "r", Kind: "note", Description: strings.Repeat("界", 240)}
		input := NavigationInput{Root: root, Cards: []MapNode{card}, ScanComplete: true, Ancestors: []MapNode{{Path: path.Dir(root), Title: strings.Repeat("界", 120), Actions: []string{"map"}}}}
		page, err := renderNavigation(input, NavigationOptions{View: "tree", Depth: 2, BudgetBytes: 4096})
		if err != nil || len(page.Nodes) != 1 {
			t.Fatalf("optional ancestor blocks navigation: %+v %v", page, err)
		}
	})
}

func TestNavigationDeepPathsUseCompactBranches(t *testing.T) {
	input := NavigationInput{Root: "/", ScanComplete: true}
	for i := 0; i < 100; i++ {
		input.Cards = append(input.Cards, MapNode{Path: fmt.Sprintf("/%03d/", i) + strings.Repeat("a/", 450) + "leaf", ID: fmt.Sprint(i), Revision: "r", Kind: "note"})
	}
	branches := navigationTree(input, 1, 512)
	if len(branches) > 301 {
		t.Fatalf("materialized empty chains: %d branches for 100 records", len(branches))
	}
	page, err := renderNavigation(input, NavigationOptions{Depth: 1, BudgetBytes: 4096})
	if err != nil || len(page.Nodes) == 0 {
		t.Fatalf("deep paths not navigable: %+v %v", page, err)
	}
	if page.Nodes[0].CollapsedFrom != "/000" || page.Nodes[0].ID != "0" {
		t.Fatalf("chain shortcut lost: %+v", page.Nodes[0])
	}
}

func TestNavigationSegmentOrderingKeepsParents(t *testing.T) {
	for _, cards := range [][]MapNode{
		{viewCard("/a"), viewCard("/a-b"), viewCard("/a/x")},
		{viewCard("/a/x"), viewCard("/a-b/y")},
	} {
		page, err := renderNavigation(NavigationInput{Root: "/", Cards: cards, ScanComplete: true}, NavigationOptions{View: "tree", Depth: 2, BudgetBytes: 12288})
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range page.Nodes {
			if n.Path == "/a" {
				expected := 1
				if len(cards) == 3 {
					expected = 2
				}
				if n.Count != expected || !hasAction(n, "map") {
					t.Fatalf("sibling stole child: %+v", n)
				}
			}
		}
	}
}

func TestNavigationDropsOptionalKindsAtTightBoundary(t *testing.T) {
	root := "/" + strings.Repeat("a", 600) + "/b"
	card := MapNode{Path: root + "/leaf", ID: "id", Revision: "r", Kind: strings.Repeat("k", 128), Description: strings.Repeat("界", 240)}
	page, err := renderNavigation(NavigationInput{Root: root, Cards: []MapNode{card}, ScanComplete: true}, NavigationOptions{View: "tree", Depth: 2, BudgetBytes: 4096})
	if err != nil || len(page.Nodes) != 1 || !page.KindsTruncated {
		t.Fatalf("optional kinds blocked a fitting card: %+v %v", page, err)
	}
}

func TestNavigationTreeCountsMatchAuthorizedSources(t *testing.T) {
	cards := []MapNode{}
	for _, parent := range []string{"/a", "/a-b", "/a.b", "/界", "/a/b", "/a/b-c"} {
		cards = append(cards, MapNode{Path: parent, ID: parent, Revision: "r", Kind: "section"})
		for i := 0; i < 3; i++ {
			p := fmt.Sprintf("%s/x%d/y/z", parent, i)
			cards = append(cards, MapNode{Path: p, ID: p, Revision: "r", Kind: "note"})
		}
	}
	// Reverse source order: aggregation must depend on paths, not input order.
	for i, j := 0, len(cards)-1; i < j; i, j = i+1, j-1 {
		cards[i], cards[j] = cards[j], cards[i]
	}
	page, err := renderNavigation(NavigationInput{Root: "/", Cards: cards, ScanComplete: true}, NavigationOptions{View: "tree", Depth: 4, BudgetBytes: 32768})
	if err != nil || !page.OutputComplete {
		t.Fatalf("fixture failed: %v", err)
	}
	for _, node := range page.Nodes {
		count := 0
		for _, card := range cards {
			if card.Path == node.Path || strings.HasPrefix(card.Path, node.Path+"/") {
				count++
			}
		}
		if count != node.Count {
			t.Fatalf("%s count=%d, want %d", node.Path, node.Count, count)
		}
	}
}

func TestMapScanWindowBoundary(t *testing.T) {
	for _, count := range []int{3000, 3001} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			ctx, b, p, _ := setupBroker(t)
			seedMapRecords(t, b, count, false)
			// Keep the same large corpus, with an ordinary single-record final
			// changeset. Repeated decoding of a 3000-change HEAD is a separate
			// store performance concern, not the scan-window boundary contract.
			snapshot, err := b.Store.Load(ctx, "alice")
			if err != nil {
				t.Fatal(err)
			}
			record, err := b.Store.RecordByPath(ctx, snapshot, "/skills/skill-000/SKILL.md")
			if err != nil {
				t.Fatal(err)
			}
			revision, err := b.Store.ReadRevision(ctx, "alice", record.Revision)
			if err != nil {
				t.Fatal(err)
			}
			revision.ID, revision.Parent = "boundary-final-revision", record.Revision
			if _, err = b.Store.Commit(ctx, snapshot, []Revision{revision}, "boundary-final", Receipt{}, nil); err != nil {
				t.Fatal(err)
			}
			execution, err := b.Begin(ctx, p, "alice")
			if err != nil {
				t.Fatal(err)
			}
			// Test scan-window membership independently of the sessionless
			// transport's wall-clock deadline (especially under -race).
			value, err := b.Call(ctx, execution, "map", map[string]any{})
			if err != nil {
				t.Fatal(err)
			}
			result := value.(map[string]any)
			if result["scan_complete"] != (count == 3000) || result["records"] != float64(3000) {
				t.Fatalf("wrong scan boundary: %+v", result)
			}
		})
	}
}

func TestEmptyFocusedNavigationDropsOptionalAncestors(t *testing.T) {
	parent := "/" + strings.Repeat("a", 900)
	root := parent + "/" + strings.Repeat("b", 120)
	page, err := renderNavigation(NavigationInput{Root: root, ScanComplete: true, Ancestors: []MapNode{{Path: parent, EntryType: "branch", Actions: []string{"map"}}}}, NavigationOptions{Depth: 2, BudgetBytes: 4096})
	if err != nil || !page.OutputComplete || len(page.Nodes) != 0 {
		t.Fatalf("optional ancestor blocked empty result: %+v %v", page, err)
	}
}
