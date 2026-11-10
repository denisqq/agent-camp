package camp

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

const defaultMapBudget = 12288

type NavigationHub struct {
	Path    string `json:"path"`
	InLinks int    `json:"in_links"`
}

// NavigationInput contains only authorized records from one immutable scan.
// The renderer neither reads sources nor infers prose about their contents.
type NavigationInput struct {
	Root, Snapshot string
	Cards          []MapNode
	Ancestors      []MapNode
	Hubs           []NavigationHub
	ScanComplete   bool
	PartialWindow  bool
}
type NavigationOptions struct {
	View                       string
	Depth, BudgetBytes, Offset int
	CursorForOffset            func(offset int, complete bool) (string, error)
}
type NavigationPage struct {
	Root           string          `json:"root"`
	Snapshot       string          `json:"snapshot"`
	View           string          `json:"view"`
	Location       MapNode         `json:"location"`
	Ancestors      []MapNode       `json:"ancestors"`
	UpPath         string          `json:"up_path,omitempty"`
	Nodes          []MapNode       `json:"nodes"`
	Kinds          map[string]int  `json:"kinds"`
	KindsTruncated bool            `json:"kinds_truncated,omitempty"`
	Hubs           []NavigationHub `json:"hubs"`
	Records        int             `json:"records"`
	Coverage       string          `json:"coverage"`
	ScanComplete   bool            `json:"scan_complete"`
	OutputComplete bool            `json:"output_complete"`
	CountsScope    string          `json:"counts_scope"`
	IndexState     string          `json:"index_state"`
	NextOffset     int             `json:"-"`
	Cursor         string          `json:"cursor"`
}

type navigationBranch struct {
	card     MapNode
	children []string
}

func renderNavigation(input NavigationInput, opts NavigationOptions) (NavigationPage, error) {
	if opts.View == "" {
		opts.View = "adaptive"
	}
	if (opts.View != "adaptive" && opts.View != "tree") || opts.Depth < 1 || opts.Depth > 4 || opts.BudgetBytes < 4096 || opts.BudgetBytes > 32768 || opts.Offset < 0 {
		return NavigationPage{}, ErrInvalid
	}
	branches := navigationTree(input, opts.Depth, opts.BudgetBytes/8)
	focus := branches[input.Root].card
	page := NavigationPage{Root: input.Root, Snapshot: input.Snapshot, View: opts.View, Location: focus, Ancestors: []MapNode{}, Nodes: []MapNode{}, Kinds: focus.Kinds, KindsTruncated: focus.KindsTruncated, Hubs: []NavigationHub{}, Records: len(input.Cards), Coverage: "truncated", ScanComplete: input.ScanComplete, CountsScope: "scan_window", IndexState: "authoritative"}
	// Use exact ciphertext envelopes for the largest position in this window
	// and for the next scan boundary. This keeps structural expansion independent
	// of the current output offset while leaving room for every actual cursor.
	if opts.CursorForOffset != nil {
		for _, complete := range []bool{false, true} {
			token, err := opts.CursorForOffset(len(branches), complete)
			if err != nil {
				return NavigationPage{}, err
			}
			if len(token) > len(page.Cursor) {
				page.Cursor = token
			}
		}
	}
	// Ancestors are nearest-first and supplied only after authorization. Reserve
	// no more than one quarter of the actual JSON budget for these anchors.
	for _, ancestor := range input.Ancestors {
		candidate := append(append([]MapNode{}, page.Ancestors...), ancestor)
		raw, err := json.Marshal(candidate)
		if err != nil {
			return NavigationPage{}, err
		}
		if len(raw) > opts.BudgetBytes/4 {
			break
		}
		page.Ancestors = candidate
	}
	if input.Root != "/" {
		page.UpPath = path.Dir(input.Root)
	}
	frontier := []MapNode{}
	if focus.ID != "" {
		frontier = append(frontier, focus)
	}
	if opts.View == "tree" {
		for p, b := range branches {
			if p != input.Root && navigationDepth(input.Root, p) <= opts.Depth {
				frontier = append(frontier, b.card)
			}
		}
	} else {
		compress := func(p string) MapNode {
			origin := p
			if input.ScanComplete && !input.PartialWindow {
				for branches[p].card.ID == "" && len(branches[p].children) == 1 {
					p = branches[p].children[0]
				}
			}
			card := branches[p].card
			if origin != p {
				card.CollapsedFrom = origin
			}
			return card
		}
		type candidate struct {
			path  string
			depth int
		}
		queue := []candidate{}
		for _, p := range branches[input.Root].children {
			card := compress(p)
			frontier = append(frontier, card)
			queue = append(queue, candidate{card.Path, 1})
		}
		// Breadth-first, then path: deterministic structural expansion. A partial
		// scan must never be treated as proof of a small or single-child branch.
		probe := page
		probe.Nodes = frontier
		initial, err := json.Marshal(probe)
		if err != nil {
			return NavigationPage{}, err
		}
		for len(queue) > 0 && input.ScanComplete && !input.PartialWindow && len(initial) <= opts.BudgetBytes {
			current := queue[0]
			queue = queue[1:]
			children := branches[current.path].children
			if current.depth >= opts.Depth || len(children) == 0 || len(children) > 8 {
				continue
			}
			added := []MapNode{}
			for _, p := range children {
				added = append(added, compress(p))
			}
			proposed := append(append([]MapNode{}, frontier...), added...)
			probe := page
			probe.Nodes = proposed
			raw, err := json.Marshal(probe)
			if err != nil {
				return NavigationPage{}, err
			}
			if len(raw) > opts.BudgetBytes {
				continue
			}
			frontier = proposed
			for _, card := range added {
				queue = append(queue, candidate{card.Path, current.depth + 1})
			}
		}
	}
	sort.Slice(frontier, func(i, j int) bool { return frontier[i].Path < frontier[j].Path })
	if opts.Offset > len(frontier) {
		return NavigationPage{}, ErrInvalid
	}
	// Leave room for the real envelope, including a complete/truncated label.
	// Raw offsets stay internal; the broker supplies authenticated envelopes.
	page.OutputComplete = opts.Offset == len(frontier)
	updateCoverage := func() error {
		if opts.CursorForOffset != nil {
			token, err := opts.CursorForOffset(opts.Offset+len(page.Nodes), page.OutputComplete)
			if err != nil {
				return err
			}
			page.Cursor = token
		}
		page.Coverage = "truncated"
		if page.ScanComplete && page.OutputComplete {
			page.Coverage = "complete"
		}
		return nil
	}
	if err := updateCoverage(); err != nil {
		return NavigationPage{}, err
	}
	size := func() (int, error) { raw, err := json.Marshal(page); return len(raw), err }
	for index := opts.Offset; index < len(frontier); index++ {
		page.Nodes = append(page.Nodes, frontier[index])
		page.OutputComplete = index+1 == len(frontier)
		if err := updateCoverage(); err != nil {
			return NavigationPage{}, err
		}
		length, err := size()
		if err != nil {
			return NavigationPage{}, err
		}
		if length > opts.BudgetBytes && page.OutputComplete && opts.CursorForOffset != nil && !page.ScanComplete {
			// A long private scan boundary can require an extra empty transition
			// page. First finish this window using its shorter in-window cursor.
			page.Cursor, err = opts.CursorForOffset(opts.Offset+len(page.Nodes), false)
			if err != nil {
				return NavigationPage{}, err
			}
			length, err = size()
			if err != nil {
				return NavigationPage{}, err
			}
		}
		for length > opts.BudgetBytes && len(page.Nodes) == 1 && len(page.Ancestors) > 0 {
			page.Ancestors = page.Ancestors[:len(page.Ancestors)-1]
			length, err = size()
			if err != nil {
				return NavigationPage{}, err
			}
		}
		if length > opts.BudgetBytes && len(page.Nodes) == 1 {
			// Counts by kind are auxiliary. Never let them strand an otherwise
			// fitting source card, even when their per-dictionary cap is obeyed.
			omitKinds := func(card *MapNode) {
				if len(card.Kinds) > 0 {
					card.Kinds = map[string]int{}
					card.KindsTruncated = true
				}
			}
			if len(page.Kinds) > 0 {
				page.Kinds = map[string]int{}
				page.KindsTruncated = true
			}
			omitKinds(&page.Location)
			omitKinds(&page.Nodes[0])
			length, err = size()
			if err != nil {
				return NavigationPage{}, err
			}
		}
		if length > opts.BudgetBytes {
			page.Nodes = page.Nodes[:len(page.Nodes)-1]
			page.OutputComplete = false
			if err := updateCoverage(); err != nil {
				return NavigationPage{}, err
			}
			if len(page.Nodes) == 0 {
				return NavigationPage{}, fmt.Errorf("%w: map card requires budget_bytes of at least %d (required size)", ErrInvalid, length)
			}
			break
		}
	}
	page.NextOffset = opts.Offset + len(page.Nodes)
	// Connectivity hints may use only space remaining after navigable cards.
	for _, hub := range input.Hubs {
		if len(page.Hubs) == 10 {
			break
		}
		page.Hubs = append(page.Hubs, hub)
		length, err := size()
		if err != nil {
			return NavigationPage{}, err
		}
		if length > opts.BudgetBytes {
			page.Hubs = page.Hubs[:len(page.Hubs)-1]
			break
		}
	}
	length, err := size()
	if err != nil {
		return NavigationPage{}, err
	}
	// Empty views also need room for their mandatory location anchor.
	for length > opts.BudgetBytes && len(page.Ancestors) > 0 {
		page.Ancestors = page.Ancestors[:len(page.Ancestors)-1]
		length, err = size()
		if err != nil {
			return NavigationPage{}, err
		}
	}
	if length > opts.BudgetBytes {
		return NavigationPage{}, fmt.Errorf("%w: map location requires budget_bytes of at least %d (required size)", ErrInvalid, length)
	}
	return page, nil
}

func navigationDepth(root, p string) int {
	return strings.Count(strings.TrimPrefix(strings.TrimPrefix(p, root), "/"), "/") + 1
}

func navigationTree(input NavigationInput, depth, kindBudget int) map[string]*navigationBranch {
	branches := map[string]*navigationBranch{}
	ensure := func(p string) *navigationBranch {
		b := branches[p]
		if b == nil {
			b = &navigationBranch{card: MapNode{Path: p, Kinds: map[string]int{}}}
			branches[p] = b
		}
		return b
	}
	ensure(input.Root)
	paths := make([]string, 0, len(input.Cards))
	for _, card := range input.Cards {
		card.Count = 1
		card.Kinds = map[string]int{card.Kind: 1}
		ensure(card.Path).card = card
		paths = append(paths, card.Path)
		if card.Path == input.Root {
			continue
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(card.Path, input.Root), "/")
		parts := strings.SplitN(rel, "/", depth+1)
		for d := 1; d <= depth && d <= len(parts); d++ {
			ensure(strings.TrimRight(input.Root, "/") + "/" + strings.Join(parts[:d], "/"))
		}
	}
	// Keep branch points and leading depth anchors, not every empty prefix of
	// every long path. Adjacent sorted documents identify all shared prefixes.
	sort.Slice(paths, func(i, j int) bool { return navigationPathLess(paths[i], paths[j]) })
	for i := 1; i < len(paths); i++ {
		ensure(commonNavigationParent(paths[i-1], paths[i]))
	}
	paths = paths[:0]
	for p := range branches {
		paths = append(paths, p)
	}
	sort.Slice(paths, func(i, j int) bool { return navigationPathLess(paths[i], paths[j]) })
	parents := map[string]string{}
	stack := []string{input.Root}
	for _, p := range paths {
		if p == input.Root {
			continue
		}
		for !strings.HasPrefix(p, strings.TrimRight(stack[len(stack)-1], "/")+"/") {
			stack = stack[:len(stack)-1]
		}
		parent := stack[len(stack)-1]
		parents[p] = parent
		branches[parent].children = append(branches[parent].children, p)
		stack = append(stack, p)
	}
	for i := len(paths) - 1; i >= 0; i-- {
		p := paths[i]
		if p == input.Root {
			continue
		}
		child, parent := branches[p], branches[parents[p]]
		parent.card.Count += child.card.Count
		for kind, count := range child.card.Kinds {
			parent.card.Kinds[kind] += count
		}
	}
	for _, b := range branches {
		sort.Strings(b.children)
		b.card.Kinds, b.card.KindsTruncated = boundedNavigationKinds(b.card.Kinds, kindBudget)
		b.card.EntryType = "branch"
		b.card.Actions = []string{"map"}
		if b.card.ID != "" {
			b.card.EntryType = "document"
			b.card.Actions = []string{"outline", "read"}
			if len(b.children) > 0 || !input.ScanComplete || input.PartialWindow {
				b.card.EntryType = "document_branch"
				b.card.Actions = append(b.card.Actions, "map")
			}
		}
	}
	return branches
}
func commonNavigationParent(a, b string) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	if i == len(a) && (i == len(b) || b[i] == '/') {
		return a
	}
	if end := strings.LastIndex(a[:i], "/"); end > 0 {
		return a[:end]
	}
	return "/"
}

// Order ancestors and their descendants together. Bytewise path ordering puts
// /a-b before /a/x, which cannot be used to build parent edges with a stack.
func navigationPathLess(a, b string) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] == b[i] {
			continue
		}
		if a[i] == '/' {
			return true
		}
		if b[i] == '/' {
			return false
		}
		return a[i] < b[i]
	}
	return len(a) < len(b)
}

func boundedNavigationKinds(kinds map[string]int, budget int) (map[string]int, bool) {
	keys := make([]string, 0, len(kinds))
	for kind := range kinds {
		keys = append(keys, kind)
	}
	sort.Strings(keys)
	result := map[string]int{}
	for _, kind := range keys[:min(32, len(keys))] {
		result[kind] = kinds[kind]
		raw, _ := json.Marshal(result)
		if len(raw) > budget {
			delete(result, kind)
			break
		}
	}
	return result, len(result) < len(keys)
}
