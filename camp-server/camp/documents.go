package camp

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"sort"
	"strings"
)

func SDKHelp(topic string) map[string]any {
	if topic == "documents" {
		return map[string]any{
			"sdk_version": "v1",
			"contract": map[string]any{
				"storage":     "write(path, content, kind, metadata) creates or updates a document; annotate(path, metadata) merges fields; patch(path, old, new) edits text. Reuse the same path to keep identity and immutable history.",
				"structure":   "You own the tree: choose any paths and kinds. kind=section marks a directory page (metadata.access controls subtree sharing); other kinds are yours to invent. link(path, target, relation) connects records; map() shows the layout.",
				"discussions": "kind=message records (comment(path, text, to='agent') is the helper) with metadata.to=<subject> form agent-to-agent threads; keep one discussion per path under /discussions/... . Agents poll what is new since their previous snapshot.",
				"history":     "history(path) lists revisions; revert(path, revision) restores old content as a new version; diff(path) previews staged changes before commit.",
				"example":     "from camp_sdk import camp\ncamp.write('/experiments/short-posts', 'Tried short posts for a week', kind='experiment', metadata={'outcome': 'success', 'measured': True})",
			},
		}
	}

	groups := map[string]any{
		"navigation": map[string]string{"spaces()": "permitted spaces", "ls(path='/', cursor='', limit=50, space='')": "paged catalog", "map(path='/', depth=2, space='')": "orientation: directory tree with kind profiles and link hubs, budgeted before reading", "find(path='/', kind='', metadata={}, cursor='', limit=50, space='')": "metadata filter", "stat(path, space='')": "record metadata"},
		"read":       map[string]string{"backlinks(path, space='')": "records linking to this one (wiki-style mentions)", "read(path, start_line=1, limit=200, revision='', space='')": "bounded text", "outline(path, space='')": "headings and line numbers", "history(path, limit=20, space='')": "version lineage", "diff(path)": "before/after staged text"},
		"write":      map[string]string{"write(path, content, kind='', metadata={})": "stage document; omitted kind preserves existing kind; new documents default to note; section metadata.access controls subtree permissions", "annotate(path, metadata)": "merge metadata without replacing content", "patch(path, old, new)": "replace one exact occurrence", "move(path, to)": "retain stable identity", "delete(path)": "tombstone", "revert(path, revision)": "new version of old content; restores a deleted path until that path is reused", "link(path, target, relation)": "link to current target revision"},
		"search":     map[string]string{"search(query, limit=50, space='', cursor='')": "ranked index results with watermark", "grep(query, limit=50, space='', cursor='')": "literal matches; explicit coverage"},
		"collective": map[string]string{"propose(path)": "immutable source revision proposal", "proposal_status(path)": "curator job state for a proposal or experiment; completed does not mean recommended", "related(path)": "record relations"},
		"session":    map[string]string{"refresh(space='')": "refresh snapshot only with empty staging", "help(topic='')": "discover SDK", "describe(topic)": "operation signatures"},
	}
	if v, ok := groups[topic]; ok {
		return map[string]any{"sdk_version": "v1", "operations": v}
	}
	return map[string]any{"sdk_version": "v1", "groups": []string{"navigation", "read", "write", "search", "session", "documents"}, "bootstrap": "from camp_sdk import camp", "note": "Fresh Python globals per run. Last expression is JSON result. Successful writes commit atomically in the primary space."}
}
func (b *Broker) write(ctx context.Context, e *Execution, p string, a map[string]any) (any, error) {
	if _, legacy := a["segment"]; legacy {
		return nil, ErrInvalid
	}
	if p == "/" && str(a, "kind") != "section" {
		return nil, ErrInvalid
	}
	content, ok := a["content"].(string)
	if !ok {
		return nil, ErrInvalid
	}
	rev := Revision{RecordID: ID(), Path: p, Content: content, Kind: str(a, "kind")}
	rec, old, err := b.resolve(ctx, e, e.Primary, p, "")
	if err == nil {
		// Rewriting requires write access at the existing path.
		if err = b.allow(ctx, e, e.Primary, rec.Path, "write"); err != nil {
			return nil, err
		}
		rev = old
		rev.Content = content
		rev.Parent = rec.Revision
		if v := str(a, "kind"); v != "" {
			rev.Kind = v
		}
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if rev.Kind == "" {
		rev.Kind = "note"
	}
	if len(rev.Kind) > 128 {
		return nil, ErrInvalid
	}
	if raw, exists := a["metadata"]; exists {
		meta, ok := raw.(map[string]any)
		if !ok {
			return nil, ErrInvalid
		}
		rev.Metadata = meta
	}
	if err = b.stage(ctx, e, rev); err != nil {
		return nil, err
	}
	return map[string]any{"id": rev.RecordID, "path": p, "staged": true}, nil
}
func (b *Broker) mutate(ctx context.Context, e *Execution, method string, rec Record, rev Revision, a map[string]any) (any, error) {
	action := "write"
	if method == "propose" {
		action = "propose"
	}
	if err := b.allow(ctx, e, e.Primary, rec.Path, action); err != nil {
		return nil, err
	}
	rev.Parent = rec.Revision
	switch method {
	case "move":
		to, err := cleanPath(str(a, "to"))
		if err != nil || to == "/" {
			return nil, ErrInvalid
		}
		if _, _, err = b.resolve(ctx, e, e.Primary, to, ""); err == nil {
			return nil, ErrConflict
		} else if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		rev.Path = to
	case "delete":
		rev.Deleted = true
	case "annotate":
		metadata, ok := a["metadata"].(map[string]any)
		if !ok {
			return nil, ErrInvalid
		}
		merged := map[string]any{}
		for k, v := range rev.Metadata {
			merged[k] = v
		}
		for k, v := range metadata {
			merged[k] = v
		}
		rev.Metadata = merged
	case "patch":
		old := str(a, "old")
		if old == "" || strings.Count(rev.Content, old) != 1 {
			return nil, errors.New("patch requires one exact occurrence")
		}
		rev.Content = strings.Replace(rev.Content, old, str(a, "new"), 1)
	case "revert":
		old, err := b.Store.ReadRevision(ctx, e.Primary, str(a, "revision"))
		if err != nil || old.RecordID != rec.ID {
			return nil, ErrNotFound
		}
		if err = b.allow(ctx, e, e.Primary, old.Path, "read"); err != nil {
			return nil, err
		}
		if old.Deleted {
			return nil, ErrInvalid
		}
		if err = b.CheckSources(ctx, old.Sources); err != nil {
			return nil, err
		}
		rev.Deleted = false
		rev.Sources = old.Sources
		rev.Content = old.Content
		rev.Metadata = old.Metadata
		rev.Blob = old.Blob
		rev.Size = old.Size
		rev.Path = rec.Path
	case "link":
		target, _, err := b.resolve(ctx, e, e.Primary, str(a, "target"), "")
		if err != nil {
			return nil, err
		}
		if rev.Metadata == nil {
			rev.Metadata = map[string]any{}
		}
		relations, _ := rev.Metadata["relations"].([]any)
		relations = append(relations, map[string]any{"record": target.ID, "revision": target.Revision, "relation": str(a, "relation")})
		rev.Metadata["relations"] = relations
	case "propose":
		if rec.Revision == "" {
			return nil, ErrInvalid
		}
		if _, ok := e.Staged[rec.ID]; ok {
			return nil, errors.New("commit source before proposing its revision")
		}
		if err := b.allow(ctx, e, e.Primary, rec.Path, "propose"); err != nil {
			return nil, err
		}
		rev = Revision{RecordID: ID(), Path: path.Join(path.Dir(rec.Path), "proposals", ID()), Kind: "proposal", Content: "", Metadata: map[string]any{"source_record": rec.ID, "source_revision": rec.Revision, "state": "pending"}}
		if e.Proposals == nil {
			e.Proposals = map[string]string{}
		}
		e.Proposals[rev.RecordID] = rec.Path
	}
	if err := b.stage(ctx, e, rev); err != nil {
		return nil, err
	}
	return map[string]any{"id": rev.RecordID, "path": rev.Path, "staged": true}, nil
}
func (b *Broker) history(ctx context.Context, e *Execution, space string, rev Revision, limit int) (any, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	out := []map[string]any{}
	for {
		if err := b.allow(ctx, e, space, rev.Path, "read"); err != nil {
			return nil, err
		}
		if err := b.CheckSources(ctx, rev.Sources); err != nil {
			return nil, err
		}
		// Every disclosed historic revision is a dependency, including its old path.
		staged, pending := e.Staged[rev.RecordID]
		if space != e.Primary || !pending || staged.ID != rev.ID {
			b.depend(e, e.Snapshots[space], Record{ID: rev.RecordID, Revision: rev.ID, Path: rev.Path, Deleted: true})
		}
		out = append(out, map[string]any{"revision": rev.ID, "parent": rev.Parent, "actor": rev.Actor, "time": rev.Time, "deleted": rev.Deleted})
		if rev.Parent == "" || len(out) >= limit {
			break
		}
		var err error
		rev, err = b.Store.ReadRevision(ctx, space, rev.Parent)
		if err != nil {
			return nil, err
		}
	}
	return map[string]any{"items": out, "truncated": rev.Parent != ""}, nil
}
func (b *Broker) list(ctx context.Context, e *Execution, space string, a map[string]any) (any, error) {
	p := str(a, "path")
	if p == "" {
		p = "/"
	}
	p, err := cleanPath(p)
	if err != nil {
		return nil, err
	}
	snap, err := b.snapshot(ctx, e, space)
	if err != nil {
		return nil, err
	}

	limit := number(a, "limit", 50)
	if limit < 1 || limit > 200 {
		return nil, ErrInvalid
	}
	after, err := cursor(e, str(a, "cursor"), true)
	if err != nil {
		return nil, err
	}
	out := []Record{}
	last := after
	more := false
	for len(out) <= limit {
		items, next, err := b.Store.List(ctx, snap, p, last, 100)
		if err != nil {
			return nil, err
		}
		// Merge the bounded durable page with staged records in the same key range.
		// A staged move suppresses the old path and contributes its new path.
		if space == e.Primary {
			merged := make([]Record, 0, len(items)+len(e.Staged))
			for _, rec := range items {
				if _, changed := e.Staged[rec.ID]; !changed {
					merged = append(merged, rec)
				}
			}
			for _, staged := range e.Staged {
				if staged.Deleted || !pathUnder(staged.Path, p) || staged.Path <= last || (next != "" && staged.Path > next) {
					continue
				}
				merged = append(merged, Record{ID: staged.RecordID, Path: staged.Path, Revision: staged.ID, Kind: staged.Kind, Metadata: staged.Metadata})
			}
			items = merged
			sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
		}
		for _, rec := range items {
			last = rec.Path
			if _, _, err = b.resolve(ctx, e, space, rec.Path, ""); err != nil {
				if errors.Is(err, ErrDenied) || errors.Is(err, ErrNotFound) {
					continue
				}
				return nil, err
			}
			if k := str(a, "kind"); k != "" && k != rec.Kind {
				continue
			}
			if filter, ok := a["metadata"].(map[string]any); ok {
				matches := true
				for k, v := range filter {
					left, _ := json.Marshal(v)
					right, _ := json.Marshal(rec.Metadata[k])
					if string(left) != string(right) {
						matches = false
						break
					}
				}
				if !matches {
					continue
				}
			}
			out = append(out, rec)
			if len(out) > limit {
				more = true
				break
			}
		}
		if len(out) > limit {
			break
		}
		if next == "" {
			more = false
			break
		}
		last = next
		more = true
	}

	token := ""
	if more {
		token, err = cursor(e, out[limit-1].Path, false)
		if err != nil {
			return nil, err
		}
	}
	if more {
		out = out[:limit]
	}
	return map[string]any{"items": out, "cursor": token, "snapshot": snap.Commit, "truncated": more, "index_state": "authoritative"}, nil
}
func (b *Broker) spaces(ctx context.Context, e *Execution) (any, error) {
	out := []Space{}
	for _, id := range e.Principal.Spaces {
		if id == "*" {
			continue
		}
		snap, err := b.Store.Load(ctx, id)
		if err != nil {
			continue
		}
		if err = b.AuthorizePath(ctx, e.Principal, snap, "/", "read"); err == nil {
			out = append(out, Space{ID: id, Kind: snap.Space.Kind, Policy: snap.Space.Policy})
		}
	}
	return out, nil
}

// spaceMap is the orientation view over a space: a token-budgeted map of how
// knowledge is laid out (directory tree with kind profiles and link hubs),
// in the spirit of a code map — but derived from record metadata, no index
// and no model required. It answers "where do skills/experiments/conclusions
// live here" before an agent starts reading.
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
	type node struct {
		Path  string         `json:"path"`
		Count int            `json:"count"`
		Kinds map[string]int `json:"kinds"`
	}
	nodes := map[string]*node{}
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
			if _, _, err = b.resolve(ctx, e, space, rec.Path, ""); err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if errors.Is(err, ErrDenied) || errors.Is(err, ErrNotFound) {
					continue
				}
				return nil, err
			}
			total++
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
					n = &node{Path: dir, Kinds: map[string]int{}}
					nodes[dir] = n
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
	ordered := make([]*node, 0, len(nodes))
	for _, n := range nodes {
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

type searchCursor struct {
	Space, Mode, Query, Snapshot, Watermark string
	Offset                                  int
}

func (b *Broker) search(ctx context.Context, e *Execution, space, mode string, a map[string]any) (any, error) {
	if b.Search == nil {
		return map[string]any{"items": []any{}, "index_state": "unavailable", "truncated": true, "cursor": ""}, nil
	}
	snap, err := b.snapshot(ctx, e, space)
	if err != nil {
		return nil, err
	}
	limit := number(a, "limit", 50)
	if limit < 1 || limit > 200 {
		return nil, ErrInvalid
	}
	query := str(a, "query")
	pageKey := searchCursor{Space: space, Mode: mode, Query: digest(query), Snapshot: snap.Commit}
	if token := str(a, "cursor"); token != "" {
		decoded, err := cursor(e, token, true)
		if err != nil {
			return nil, err
		}
		var previous searchCursor
		if err = json.Unmarshal([]byte(decoded), &previous); err != nil {
			return nil, ErrInvalid
		}
		if previous.Space != space || previous.Mode != mode || previous.Query != pageKey.Query || previous.Snapshot != snap.Commit {
			return nil, ErrInvalid
		}
		pageKey = previous
	}
	allowed := []SearchHit{}
	more := false
	watermark := pageKey.Watermark
scan:
	for {
		page, err := b.Search.QueryPage(ctx, space, mode, query, pageKey.Offset, 100, watermark)
		if err != nil {
			return nil, err
		}
		watermark = page.Watermark
		for n, hit := range page.Hits {
			rec, _, err := b.resolve(ctx, e, space, hit.Path, "")
			if err != nil {
				if errors.Is(err, ErrDenied) || errors.Is(err, ErrNotFound) {
					continue
				}
				return nil, err
			}
			if rec.Revision != hit.Revision {
				continue
			}
			if len(allowed) == limit {
				pageKey.Offset += n
				more = true
				break scan
			}
			allowed = append(allowed, hit)
		}
		if !page.More {
			break
		}
		if page.Next <= pageKey.Offset {
			return nil, ErrInvalid
		}
		pageKey.Offset = page.Next
	}
	state := "ready"
	if watermark != snap.Commit {
		state = "stale"
	}
	if watermark == "" {
		state = "building"
	}
	next := ""
	if more {
		pageKey.Watermark = watermark
		encoded, err := json.Marshal(pageKey)
		if err != nil {
			return nil, err
		}
		next, err = cursor(e, string(encoded), false)
		if err != nil {
			return nil, err
		}
	}
	return map[string]any{"items": allowed, "snapshot": snap.Commit, "watermark": watermark, "index_state": state, "truncated": more || state != "ready", "cursor": next, "coverage": "indexed_text"}, nil
}

// backlinks lists records whose relations point at the resolved record — the
// wiki-style "linked mentions" view, bounded like map().
func (b *Broker) backlinks(ctx context.Context, e *Execution, space string, target Record) (any, error) {
	snap, err := b.snapshot(ctx, e, space)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	after := ""
	for len(out) < 100 {
		records, next, err := b.Store.List(ctx, snap, "/", after, 100)
		if err != nil {
			return nil, err
		}
		for _, rec := range records {
			relations, _ := rec.Metadata["relations"].([]any)
			for _, raw := range relations {
				link, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				if id, _ := link["record"].(string); id == target.ID {
					out = append(out, map[string]any{"path": rec.Path, "record": rec.ID, "relation": link["relation"]})
				}
			}
			if len(out) >= 100 {
				break
			}
		}
		if next == "" {
			break
		}
		after = next
	}
	return map[string]any{"items": out, "truncated": len(out) >= 100, "snapshot": snap.Commit}, nil
}

// comment stages a discussion message about a document: kind=message under
// <path>/discussion/<id>, with the addressed agent in metadata.to. This is the
// agent-to-agent channel — asynchronous, versioned, readable by humans.
func (b *Broker) comment(ctx context.Context, e *Execution, about Record, a map[string]any) (any, error) {
	text, ok := a["text"].(string)
	if !ok || text == "" || len(text) > 65536 {
		return nil, ErrInvalid
	}
	if err := b.allow(ctx, e, e.Primary, about.Path, "read"); err != nil {
		return nil, err
	}
	slug := strings.Trim(about.Path, "/")
	slug = strings.ReplaceAll(slug, "/", "-")
	path, err := cleanPath("/discussions/" + slug + "/" + ID()[:8])
	if err != nil {
		return nil, err
	}
	rev := Revision{RecordID: ID(), Path: path, Kind: "message", Content: text, Metadata: map[string]any{"about": about.Path, "about_record": about.ID, "about_revision": about.Revision}}
	if to := str(a, "to"); to != "" {
		rev.Metadata["to"] = to
	}
	if err = b.stage(ctx, e, rev); err != nil {
		return nil, err
	}
	return map[string]any{"id": rev.RecordID, "path": path, "staged": true}, nil
}
