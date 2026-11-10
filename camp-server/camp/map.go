package camp

import (
	"context"
	"time"
)

type MapRequest struct {
	Space       string `json:"space,omitempty" jsonschema:"Omit to discover accessible spaces; otherwise use an id returned by map()"`
	Path        string `json:"path,omitempty" jsonschema:"Directory prefix; defaults to /"`
	Depth       *int   `json:"depth,omitempty" jsonschema:"Tree depth from 1 to 4; defaults to 2"`
	View        string `json:"view,omitempty" jsonschema:"adaptive (default) expands small branches and shortens empty chains; tree preserves the depth-based outline"`
	BudgetBytes *int   `json:"budget_bytes,omitempty" jsonschema:"Maximum serialized result bytes, 4096 to 32768; defaults to 12288"`
	Cursor      string `json:"cursor,omitempty" jsonschema:"Opaque continuation; repeat the same map parameters. Omit to restart at the current snapshot"`
}

// Map is a read-only orientation request: no durable session or Python runtime
// is needed. Call the same broker operation as the SDK, including final access
// checks, so switching transports cannot disclose more information.
func (b *Broker) Map(ctx context.Context, p Principal, r MapRequest) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	b = b.withReadCache()
	if r.Space == "" {
		if r.Path != "" || r.Depth != nil || r.View != "" || r.BudgetBytes != nil {
			return nil, ErrInvalid
		}
		return b.discoverSpaces(ctx, p, r.Cursor)
	}
	e, err := b.Begin(ctx, p, r.Space)
	if err != nil {
		return nil, err
	}
	args := map[string]any{"path": r.Path}
	e.mapSessionless = true
	if r.Cursor != "" {
		args["cursor"] = r.Cursor
	}
	if r.View != "" {
		args["view"] = r.View
	}
	if r.BudgetBytes != nil {
		args["budget_bytes"] = *r.BudgetBytes
	}
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
