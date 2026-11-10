package camp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"path"
	"sort"
	"time"
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
	continuation := MapCursor{Version: 1, TokenHash: e.Principal.TokenHash, Subject: e.Principal.Subject, Space: space, Path: root, View: opts.View, Depth: opts.Depth, BudgetBytes: opts.BudgetBytes, Snapshot: snap.Commit, Expires: time.Now().Add(mapCursorLifetime).Unix()}
	if raw, exists := a["cursor"]; exists {
		token, ok := raw.(string)
		if !ok {
			return nil, ErrInvalid
		}
		if token != "" {
			if len(e.Staged) > 0 {
				return nil, invalidMapCursor
			}
			key, err := b.mapCursorKey()
			if err != nil {
				return nil, err
			}
			continuation, err = openMapCursor(key, token)
			if err != nil {
				return nil, err
			}
			if !continuation.matches(e.Principal, space, root, opts) {
				return nil, invalidMapCursor
			}
			if continuation.ScanStart != "" {
				clean, err := cleanPath(continuation.ScanStart)
				if err != nil || clean != continuation.ScanStart || !pathUnder(clean, root) {
					return nil, invalidMapCursor
				}
			}
			if e.mapSessionless {
				snap, err = b.Store.At(ctx, space, continuation.Snapshot)
				if errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalid) {
					return nil, invalidMapCursor
				}
				if err != nil {
					return nil, err
				}
				if snap.Space.ID != space || snap.Commit != continuation.Snapshot {
					return nil, invalidMapCursor
				}
				e.Snapshots[space] = snap
			} else if snap.Commit != continuation.Snapshot {
				return nil, invalidMapCursor
			}
		}
	}
	if err = b.allow(ctx, e, space, root, "read"); err != nil {
		return nil, err
	}
	input, windowEnd, err := b.collectNavigationWindow(ctx, e, snap, space, root, continuation.ScanStart, 3000)
	if err != nil {
		return nil, err
	}
	if input.Ancestors, err = b.navigationAncestors(ctx, e, space, root, opts.BudgetBytes/4); err != nil {
		return nil, err
	}
	digestSource, err := json.Marshal(struct{ Cards, Ancestors []MapNode }{input.Cards, input.Ancestors})
	if err != nil {
		return nil, err
	}
	digestBytes := sha256.Sum256(digestSource)
	digest := hex.EncodeToString(digestBytes[:])
	if continuation.WindowDigest != "" && continuation.WindowDigest != digest {
		return nil, invalidMapCursor
	}
	if continuation.Offset > 0 && continuation.WindowDigest == "" {
		return nil, invalidMapCursor
	}
	opts.Offset = continuation.Offset
	type position struct {
		offset   int
		complete bool
	}
	tokens := map[position]string{}
	opts.CursorForOffset = func(offset int, complete bool) (string, error) {
		if complete && input.ScanComplete {
			return "", nil
		}
		p := position{offset, complete}
		if token, ok := tokens[p]; ok {
			return token, nil
		}
		next := continuation
		if complete {
			next.ScanStart = windowEnd
			next.Offset = 0
			next.WindowDigest = ""
		} else {
			next.Offset = offset
			next.WindowDigest = digest
		}
		key, err := b.mapCursorKey()
		if err != nil {
			return "", err
		}
		token, err := sealMapCursor(key, next)
		if err != nil {
			return "", err
		}
		tokens[p] = token
		return token, nil
	}
	page, err := renderNavigation(input, opts)
	if err != nil {
		if err == ErrInvalid && str(a, "cursor") != "" {
			return nil, invalidMapCursor
		}
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
	if opts.View == "" {
		opts.View = "adaptive"
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

func (b *Broker) navigationAncestors(ctx context.Context, e *Execution, space, root string, budget int) ([]MapNode, error) {
	anchors := []MapNode{}
	spent := 2 // JSON array brackets; reserve a comma for each attempted anchor.
	for p := root; p != "/"; {
		p = path.Dir(p)
		anchor := MapNode{Path: p, Kinds: map[string]int{}, EntryType: "branch", Actions: []string{"map"}}
		minimum, err := json.Marshal(anchor)
		if err != nil {
			return nil, err
		}
		if spent+len(minimum)+1 > budget {
			break
		}
		// Charge denied candidates too, so a closed ancestor chain cannot force
		// unbounded current-HEAD reads while looking for a distant visible anchor.
		spent += len(minimum) + 1
		if err := b.allow(ctx, e, space, p, "read"); err != nil {
			if errors.Is(err, ErrDenied) {
				continue
			}
			return nil, err
		}
		anchor.Title = path.Base(p)
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
		encoded, err := json.Marshal(anchor)
		if err != nil {
			return nil, err
		}
		spent += len(encoded) - len(minimum)
		if spent > budget {
			break
		}
		anchors = append(anchors, anchor)
	}
	return anchors, nil
}

// Collect at most limit stored records plus one lookahead. Keeping collection
// separate makes exact-window/one-record-tail behavior testable without creating
// thousands of records for each boundary case. The public limit remains 3000.
func (b *Broker) collectNavigationWindow(ctx context.Context, e *Execution, snap Snapshot, space, root, after string, limit int) (NavigationInput, string, error) {
	if limit < 1 {
		return NavigationInput{}, "", ErrInvalid
	}
	input := NavigationInput{Root: root, Snapshot: snap.Commit, ScanComplete: true, PartialWindow: after != ""}
	inDegree := map[string]int{}
	paths := map[string]string{}
	scanned := 0
	windowEnd := after
scan:
	for {
		records, next, err := b.Store.List(ctx, snap, root, after, min(100, limit+1-scanned))
		if err != nil {
			return NavigationInput{}, "", err
		}
		for _, rec := range records {
			// One-record lookahead distinguishes an exact full window from overflow.
			if scanned == limit {
				input.ScanComplete = false
				break scan
			}
			scanned++
			windowEnd = rec.Path
			rec, rev, err := b.resolve(ctx, e, space, rec.Path, "")
			if err != nil {
				if ctx.Err() != nil {
					return NavigationInput{}, "", ctx.Err()
				}
				if errors.Is(err, ErrDenied) || errors.Is(err, ErrNotFound) {
					continue
				}
				return NavigationInput{}, "", err
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
	return input, windowEnd, nil
}
