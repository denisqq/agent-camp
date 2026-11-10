package camp

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
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
	depth := number(a, "depth", 2)
	if depth < 1 || depth > 4 {
		return nil, ErrInvalid
	}
	nodes := map[string]*MapNode{}
	inDegree := map[string]int{}
	paths := map[string]string{}
	kinds := map[string]int{}
	total, scanned := 0, 0
	after := ""
complete:
	for scanned < 3000 {
		records, next, err := b.Store.List(ctx, snap, root, after, 100)
		if err != nil {
			return nil, err
		}
		for _, rec := range records {
			scanned++
			resolved, rev, resolveErr := b.resolve(ctx, e, space, rec.Path, "")
			err = resolveErr
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if errors.Is(err, ErrDenied) || errors.Is(err, ErrNotFound) {
					continue
				}
				return nil, err
			}
			rec = resolved
			total++
			if rec.Path == root {
				card := navigationCard(rec, rev)
				card.Kinds = map[string]int{}
				nodes[root] = &card
			}
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
			if rec.Path == root {
				continue
			}
			rel := strings.TrimPrefix(strings.TrimPrefix(rec.Path, root), "/")
			parts := strings.Split(rel, "/")
			for d := 1; d <= depth && d <= len(parts); d++ {
				dir := root
				if root != "/" {
					dir = root + "/"
				}
				dir += strings.Join(parts[:d], "/")
				n := nodes[dir]
				if n == nil {
					n = &MapNode{Path: dir, Kinds: map[string]int{}}
					nodes[dir] = n
				}
				if rec.Path == dir {
					card := navigationCard(rec, rev)
					card.Count, card.Kinds = n.Count, n.Kinds
					*n = card
				}
				n.Count++
				n.Kinds[rec.Kind]++
			}
			if rec.Path != root {
				kinds[rec.Kind]++
			}
		}
		if next == "" {
			break complete
		}
		after = next
	}
	ordered := make([]*MapNode, 0, len(nodes))
	if focus := nodes[root]; focus != nil {
		focus.Count = total
		for kind, count := range kinds {
			focus.Kinds[kind] = count
		}
		focus.Kinds[focus.Kind]++
	}
	for _, n := range nodes {
		n.EntryType = "branch"
		n.Actions = []string{"map"}
		if n.ID != "" {
			n.EntryType = "document"
			n.Actions = []string{"outline", "read"}
			if n.Count > 1 {
				n.EntryType = "document_branch"
				n.Actions = append(n.Actions, "map")
			}
		}
		ordered = append(ordered, n)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })
	type hub struct {
		Path   string `json:"path"`
		InLink int    `json:"in_links"`
	}
	hubs := []hub{}
	for id, degree := range inDegree {
		if p, ok := paths[id]; ok && degree >= 1 {
			hubs = append(hubs, hub{p, degree})
		}
	}
	sort.Slice(hubs, func(i, j int) bool { return hubs[i].InLink > hubs[j].InLink })
	if len(hubs) > 10 {
		hubs = hubs[:10]
	}
	coverage := "complete"
	if scanned >= 3000 {
		coverage = "truncated"
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// A map is an orientation response, not a dump of every directory.
	if len(ordered) > 100 {
		ordered = ordered[:100]
		coverage = "truncated"
	}
	kindNames := make([]string, 0, len(kinds))
	for kind := range kinds {
		kindNames = append(kindNames, kind)
	}
	sort.Strings(kindNames)
	if len(kindNames) > 32 {
		for _, kind := range kindNames[32:] {
			delete(kinds, kind)
		}
		coverage = "truncated"
	}
	result := map[string]any{"root": root, "nodes": ordered, "kinds": kinds, "hubs": hubs, "records": total, "coverage": coverage, "snapshot": snap.Commit}
	for {
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		if len(raw) <= 12*1024 {
			return result, nil
		}
		result["coverage"] = "truncated"
		if len(ordered) > 0 {
			ordered = ordered[:len(ordered)-1]
			result["nodes"] = ordered
			continue
		}
		if len(hubs) > 0 {
			hubs = hubs[:len(hubs)-1]
			result["hubs"] = hubs
			continue
		}
		return nil, errors.New("map metadata exceeds response budget")
	}
}
