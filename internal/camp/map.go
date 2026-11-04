package camp

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
)

type MapRequest struct {
	Space string `json:"space,omitempty" jsonschema:"Omit to discover accessible spaces; otherwise use an id returned by map()"`
	Path  string `json:"path,omitempty" jsonschema:"Directory prefix; defaults to /"`
	Depth *int   `json:"depth,omitempty" jsonschema:"Tree depth from 1 to 4; defaults to 2"`
}

// Map is a read-only orientation request: no durable session or Python runtime
// is needed. Call the same broker operation as the SDK, including final access
// checks, so switching transports cannot disclose more information.
func (b *Broker) Map(ctx context.Context, p Principal, r MapRequest) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	b = b.withReadCache()
	if r.Space == "" {
		if (r.Path != "" && r.Path != "/") || (r.Depth != nil && (*r.Depth < 1 || *r.Depth > 4)) {
			return nil, ErrInvalid
		}
		return b.discoverSpaces(ctx, p)
	}
	e, err := b.Begin(ctx, p, r.Space)
	if err != nil {
		return nil, err
	}
	args := map[string]any{"path": r.Path}
	if r.Depth != nil {
		args["depth"] = *r.Depth
	}
	result, err := b.Call(ctx, e, "map", args)
	if err != nil {
		return nil, err
	}
	if _, err = b.Auth.Current(ctx, p); err != nil {
		return nil, err
	}
	if err = b.CheckDependencies(ctx, p, e.Dependencies); err != nil {
		return nil, err
	}
	return result.(map[string]any), nil
}

// Discovery deliberately exposes no owners, ACL entries, or document content.
// Root actions are hints; every later operation rechecks its own path policy.
func (b *Broker) discoverSpaces(ctx context.Context, p Principal) (map[string]any, error) {
	p, err := b.Auth.Current(ctx, p)
	if err != nil {
		return nil, err
	}
	ids := append([]string(nil), p.Spaces...)
	coverage := "complete"
	if contains(p.Spaces, "*") {
		keys, next, err := b.Store.Objects.List(ctx, "registry/spaces/", "", 1000)
		if err != nil {
			return nil, err
		}
		ids = make([]string, 0, len(keys))
		for _, key := range keys {
			ids = append(ids, strings.TrimPrefix(key, "registry/spaces/"))
		}
		if next != "" {
			coverage = "truncated"
		}
	}
	sort.Strings(ids)
	if len(ids) > 1000 {
		ids = ids[:1000]
		coverage = "truncated"
	}
	spaces := []map[string]any{}
	previous := ""
	for _, id := range ids {
		if id == previous {
			continue
		}
		previous = id
		snap, err := b.Store.Load(ctx, id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		err = b.AuthorizePath(ctx, p, snap, "/", "read")
		if errors.Is(err, ErrDenied) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if len(spaces) == 100 {
			coverage = "truncated"
			break
		}
		actions := []string{"read"}
		for _, action := range []string{"propose", "write"} {
			err = b.AuthorizePath(ctx, p, snap, "/", action)
			if err == nil {
				actions = append(actions, action)
			} else if !errors.Is(err, ErrDenied) {
				return nil, err
			}
		}
		spaces = append(spaces, map[string]any{"id": id, "kind": snap.Space.Kind, "actions": actions})
	}
	if _, err := b.Auth.Current(ctx, p); err != nil {
		return nil, err
	}
	return map[string]any{"spaces": spaces, "coverage": coverage,
		"next_steps": "Choose a returned id: map(space=id) explores it; start(primary_space=id) opens a session. Then run the examples in start.help. Actions apply at the root; nested sections may differ. If no spaces are returned, ask the workspace owner to grant access. Truncated coverage is not a complete inventory."}, nil
}
