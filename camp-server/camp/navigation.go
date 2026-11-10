package camp

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path"
	"sort"
)

func (b *Broker) spaceMap(ctx context.Context, e *Execution, space string, a map[string]any) (any, error) {
	b = b.withReadCache()
	snap, err := b.snapshot(ctx, e, space)
	if err != nil {
		return nil, err
	}
	root := str(a, "path")
	if root == "" {
		root = "/"
	}
	root, err = cleanPath(root)
	if err != nil {
		return nil, err
	}
	opts, err := navigationOptions(a)
	if err != nil {
		return nil, err
	}
	if err = b.allow(ctx, e, space, root, "read"); err != nil {
		return nil, err
	}
	input := NavigationInput{Root: root, Snapshot: snap.Commit, ScanComplete: true}
	inDegree := map[string]int{}
	paths := map[string]string{}
	scanned := 0
	after := ""
scan:
	for {
		records, next, err := b.Store.List(ctx, snap, root, after, min(100, 3001-scanned))
		if err != nil {
			return nil, err
		}
		for _, rec := range records {
			// One-record lookahead distinguishes an exact full window from overflow.
			if scanned == 3000 {
				input.ScanComplete = false
				break scan
			}
			scanned++
			rec, rev, err := b.resolve(ctx, e, space, rec.Path, "")
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if errors.Is(err, ErrDenied) || errors.Is(err, ErrNotFound) {
					continue
				}
				return nil, err
			}
			input.Cards = append(input.Cards, navigationCard(rec, rev))
			paths[rec.ID] = rec.Path
			if relations, ok := rec.Metadata["relations"].([]any); ok {
				for _, raw := range relations {
					if link, ok := raw.(map[string]any); ok {
						if target, ok := link["record"].(string); ok && target != "" {
							inDegree[target]++
						}
					}
				}
			}
		}
		if next == "" {
			break
		}
		after = next
	}
	for id, degree := range inDegree {
		if p, ok := paths[id]; ok {
			input.Hubs = append(input.Hubs, NavigationHub{Path: p, InLinks: degree})
		}
	}
	sort.Slice(input.Hubs, func(i, j int) bool {
		if input.Hubs[i].InLinks != input.Hubs[j].InLinks {
			return input.Hubs[i].InLinks > input.Hubs[j].InLinks
		}
		return input.Hubs[i].Path < input.Hubs[j].Path
	})
	if input.Ancestors, err = b.navigationAncestors(ctx, e, space, root); err != nil {
		return nil, err
	}
	page, err := renderNavigation(input, opts)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	// The broker and SDK use the same legacy top-level map representation.
	raw, err := json.Marshal(page)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func navigationOptions(args map[string]any) (NavigationOptions, error) {
	opts := NavigationOptions{}
	if raw, exists := args["view"]; exists {
		var ok bool
		if opts.View, ok = raw.(string); !ok {
			return opts, ErrInvalid
		}
	}
	if opts.View != "" && opts.View != "adaptive" && opts.View != "tree" {
		return opts, ErrInvalid
	}
	var err error
	if opts.Depth, err = navigationInteger(args, "depth", 2, 1, 4); err != nil {
		return opts, err
	}
	opts.BudgetBytes, err = navigationInteger(args, "budget_bytes", defaultMapBudget, 4096, 32768)
	return opts, err
}

func navigationInteger(args map[string]any, key string, fallback, lower, upper int) (int, error) {
	raw, exists := args[key]
	if !exists {
		return fallback, nil
	}
	var n int
	switch value := raw.(type) {
	case int:
		n = value
	case float64:
		if value < float64(lower) || value > float64(upper) || value != math.Trunc(value) {
			return 0, ErrInvalid
		}
		n = int(value)
	case json.Number:
		integer, err := value.Int64()
		if err != nil || integer < int64(lower) || integer > int64(upper) {
			return 0, ErrInvalid
		}
		n = int(integer)
	default:
		return 0, ErrInvalid
	}
	if n < lower || n > upper {
		return 0, ErrInvalid
	}
	return n, nil
}

func (b *Broker) navigationAncestors(ctx context.Context, e *Execution, space, root string) ([]MapNode, error) {
	anchors := []MapNode{}
	for p := root; p != "/"; {
		p = path.Dir(p)
		if err := b.allow(ctx, e, space, p, "read"); err != nil {
			if errors.Is(err, ErrDenied) {
				continue
			}
			return nil, err
		}
		anchor := MapNode{Path: p, Title: path.Base(p), Kinds: map[string]int{}, EntryType: "branch", Actions: []string{"map"}}
		rec, rev, err := b.resolve(ctx, e, space, p, "")
		if err == nil {
			card := navigationCard(rec, rev)
			anchor.ID, anchor.Revision, anchor.Kind, anchor.Title, anchor.TextTruncated = card.ID, card.Revision, card.Kind, card.Title, card.TextTruncated
			anchor.EntryType = "document_branch"
			anchor.Actions = []string{"map", "outline", "read"}
		} else if !errors.Is(err, ErrNotFound) {
			if errors.Is(err, ErrDenied) {
				continue
			}
			return nil, err
		}
		anchors = append(anchors, anchor)
	}
	return anchors, nil
}
