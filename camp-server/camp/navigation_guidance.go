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
func (b *Broker) guidancePage(ctx context.Context, e *Execution, space, root string, opts NavigationOptions, c MapCursor) (NavigationPage, bool, error) {
	scopes := []string{root}
	for p := root; p != "/"; {
		p = path.Dir(p)
		scopes = append(scopes, p)
	}
	for i, j := 0, len(scopes)-1; i < j; i, j = i+1, j-1 {
		scopes[i], scopes[j] = scopes[j], scopes[i]
	}
	names := []string{"ABOUT.md", "RULES.md", "WORKFLOW.md"}
	roles := []string{"orientation", "rules", "workflow"}
	total := len(scopes) * len(names)
	if c.GuidancePos < 0 || c.GuidancePos > total || (c.Phase != "" && c.Phase != "guidance") {
		return NavigationPage{}, false, invalidMapCursor
	}
	pg := NavigationPage{Root: root, Snapshot: e.Snapshots[space].Commit, View: opts.View, Location: MapNode{Path: root, Kinds: map[string]int{}, EntryType: "branch", Actions: []string{"map"}}, Ancestors: []MapNode{}, Nodes: []MapNode{}, Kinds: map[string]int{}, Hubs: []NavigationHub{}, Coverage: "truncated", CountsScope: "scan_window", IndexState: "authoritative", Guidance: MapGuidance{AppliesTo: root, Items: []GuidanceItem{}}}
	if root != "/" {
		pg.UpPath = path.Dir(root)
	}
	pos := c.GuidancePos
	key, err := b.mapCursorKey()
	if err != nil {
		return pg, false, err
	}
	setCursor := func(next int) error {
		n := c
		n.GuidancePos = next
		n.Phase = "guidance"
		if next == total {
			n.Phase = "nodes"
			n.GuidancePos = 0
		}
		pg.Cursor, err = sealMapCursor(key, n)
		return err
	}
	// Reserve the largest cursor for this traversal before fitting cards. A
	// continuation after omitted/denied candidates may be longer than the
	// cursor immediately following the last visible card.
	if err = setCursor(total - 1); err != nil {
		return pg, false, err
	}
	reservedCursor := pg.Cursor
	if err = setCursor(total); err != nil {
		return pg, false, err
	}
	if len(pg.Cursor) > len(reservedCursor) {
		reservedCursor = pg.Cursor
	}
	for inspected := 0; pos < total && inspected < 24; inspected++ {
		scope := scopes[pos/3]
		idx := pos % 3
		rec, rev, err := b.resolve(ctx, e, space, path.Join(scope, names[idx]), "")
		if err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrDenied) {
			return pg, false, err
		}
		if err == nil {
			card := navigationCard(rec, rev)
			item := GuidanceItem{Path: rec.Path, Scope: scope, Role: roles[idx], ID: rec.ID, Revision: rev.ID, Title: card.Title, Actions: []string{"read"}}
			pg.Guidance.Items = append(pg.Guidance.Items, item)
			pg.Cursor = reservedCursor
			pg.Guidance.Complete = false
			raw, _ := json.Marshal(pg)
			if len(raw) > opts.BudgetBytes {
				pg.Guidance.Items[len(pg.Guidance.Items)-1].Title = ""
				raw, _ = json.Marshal(pg)
				if len(raw) > opts.BudgetBytes && len(pg.Guidance.Items) == 1 && pos+1 == total {
					// The last card can use a shorter transition cursor than the
					// conservative reservation for intermediate pages.
					if err = setCursor(total); err != nil {
						return pg, false, err
					}
					pg.Guidance.Complete = true
					raw, _ = json.Marshal(pg)
				}
				if len(raw) > opts.BudgetBytes {
					pg.Guidance.Items = pg.Guidance.Items[:len(pg.Guidance.Items)-1]
					if len(pg.Guidance.Items) == 0 {
						return pg, false, fmt.Errorf("%w: guidance card requires budget_bytes of at least %d", ErrInvalid, len(raw))
					}
					break
				}
			}
		}
		pos++
	}
	pg.Guidance.Complete = pos == total
	if pos == total && len(pg.Guidance.Items) == 0 && c.GuidancePos == 0 {
		pg.Guidance.Complete = true
		return pg, false, nil
	}
	if err = setCursor(pos); err != nil {
		return pg, false, err
	}
	raw, err := json.Marshal(pg)
	if err != nil {
		return pg, false, err
	}
	if len(raw) > opts.BudgetBytes {
		return pg, false, fmt.Errorf("%w: guidance page requires budget_bytes of at least %d", ErrInvalid, len(raw))
	}
	return pg, true, nil
}
