package camp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
)

type GuidanceItem struct {
	Path     string   `json:"path"`
	Scope    string   `json:"scope"`
	Role     string   `json:"role"`
	ID       string   `json:"id"`
	Revision string   `json:"revision"`
	Title    string   `json:"title,omitempty"`
	Actions  []string `json:"actions"`
}
type MapGuidance struct {
	AppliesTo string         `json:"applies_to"`
	Items     []GuidanceItem `json:"items"`
	Complete  bool           `json:"complete"`
}

// guidancePage advances by candidate positions, including absent and denied entries.
func (b *Broker) guidancePage(ctx context.Context, e *Execution, space, root string, opts NavigationOptions, continuation MapCursor) (NavigationPage, bool, error) {
	scopes := []string{root}
	for p := root; p != "/"; {
		p = path.Dir(p)
		scopes = append(scopes, p)
	}
	for i, j := 0, len(scopes)-1; i < j; i, j = i+1, j-1 {
		scopes[i], scopes[j] = scopes[j], scopes[i]
	}
	files := []struct{ name, role string }{
		{"ABOUT.md", "orientation"},
		{"RULES.md", "rules"},
		{"WORKFLOW.md", "workflow"},
	}
	total := len(scopes) * len(files)
	if continuation.GuidancePos < 0 || continuation.GuidancePos > total || (continuation.Phase != "" && continuation.Phase != "guidance") {
		return NavigationPage{}, false, invalidMapCursor
	}
	page := NavigationPage{
		Root: root, Snapshot: e.Snapshots[space].Commit, View: opts.View,
		Location: MapNode{
			Path: root, Kinds: map[string]int{},
			EntryType: "branch", Actions: []string{"map"},
		},
		Ancestors: []MapNode{}, Nodes: []MapNode{},
		Kinds: map[string]int{}, Hubs: []NavigationHub{},
		Coverage: "truncated", CountsScope: "scan_window", IndexState: "authoritative",
		Guidance: MapGuidance{AppliesTo: root, Items: []GuidanceItem{}},
	}
	if root != "/" {
		page.UpPath = path.Dir(root)
	}
	position := continuation.GuidancePos
	key, err := b.mapCursorKey()
	if err != nil {
		return page, false, err
	}
	setCursor := func(next int) error {
		n := continuation
		n.GuidancePos = next
		n.Phase = "guidance"
		if next == total {
			n.Phase = "nodes"
			n.GuidancePos = 0
		}
		page.Cursor, err = sealMapCursor(key, n)
		return err
	}
	// Reserve the largest cursor for this traversal before fitting cards. A
	// continuation after omitted/denied candidates may be longer than the
	// cursor immediately following the last visible card.
	if err = setCursor(total - 1); err != nil {
		return page, false, err
	}
	reservedCursor := page.Cursor
	if err = setCursor(total); err != nil {
		return page, false, err
	}
	if len(page.Cursor) > len(reservedCursor) {
		reservedCursor = page.Cursor
	}
	for inspected := 0; position < total && inspected < 24; inspected++ {
		scope := scopes[position/len(files)]
		file := files[position%len(files)]
		rec, rev, err := b.resolve(ctx, e, space, path.Join(scope, file.name), "")
		if err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrDenied) {
			return page, false, err
		}
		if err == nil {
			card := navigationCard(rec, rev)
			item := GuidanceItem{Path: rec.Path, Scope: scope, Role: file.role, ID: rec.ID, Revision: rev.ID, Title: card.Title, Actions: []string{"read"}}
			page.Guidance.Items = append(page.Guidance.Items, item)
			page.Cursor = reservedCursor
			page.Guidance.Complete = false
			raw, _ := json.Marshal(page)
			if len(raw) > opts.BudgetBytes {
				page.Guidance.Items[len(page.Guidance.Items)-1].Title = ""
				raw, _ = json.Marshal(page)
				if len(raw) > opts.BudgetBytes && len(page.Guidance.Items) == 1 && position+1 == total {
					// The last card can use a shorter transition cursor than the
					// conservative reservation for intermediate pages.
					if err = setCursor(total); err != nil {
						return page, false, err
					}
					page.Guidance.Complete = true
					raw, _ = json.Marshal(page)
				}
				if len(raw) > opts.BudgetBytes {
					page.Guidance.Items = page.Guidance.Items[:len(page.Guidance.Items)-1]
					if len(page.Guidance.Items) == 0 {
						return page, false, fmt.Errorf("%w: guidance card requires budget_bytes of at least %d", ErrInvalid, len(raw))
					}
					break
				}
			}
		}
		position++
	}
	page.Guidance.Complete = position == total
	if position == total && len(page.Guidance.Items) == 0 && continuation.GuidancePos == 0 {
		return page, false, nil
	}
	if err = setCursor(position); err != nil {
		return page, false, err
	}
	raw, err := json.Marshal(page)
	if err != nil {
		return page, false, err
	}
	if len(raw) > opts.BudgetBytes {
		return page, false, fmt.Errorf("%w: guidance page requires budget_bytes of at least %d", ErrInvalid, len(raw))
	}
	return page, true, nil
}
