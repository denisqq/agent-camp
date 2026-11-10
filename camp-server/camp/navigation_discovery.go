package camp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type discoverySpace struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Actions []string `json:"actions"`
}
type discoveryPage struct {
	Spaces       []discoverySpace `json:"spaces"`
	Cursor       string           `json:"cursor"`
	Coverage     string           `json:"coverage"`
	RegistryMode string           `json:"registry_mode"`
	NextSteps    string           `json:"next_steps"`
}

// Registry continuation is live: there is no atomic snapshot across spaces.
// Raw denied IDs used as scan boundaries remain inside authenticated ciphertext.
func (b *Broker) discoverSpaces(ctx context.Context, p Principal, token string) (map[string]any, error) {
	p, err := b.Auth.Current(ctx, p)
	if err != nil {
		return nil, err
	}
	continuation := MapCursor{Version: 1, TokenHash: p.TokenHash, Subject: p.Subject, Expires: time.Now().Add(mapCursorLifetime).Unix()}
	if token != "" {
		key, err := b.mapCursorKey()
		if err != nil {
			return nil, err
		}
		continuation, err = openMapCursor(key, token)
		if err != nil {
			return nil, err
		}
		if !continuation.matches(p, "", "", NavigationOptions{}) || continuation.Snapshot != "" || continuation.Offset != 0 || continuation.WindowDigest != "" {
			return nil, invalidMapCursor
		}
		if continuation.ScanStart != "" && !safeID.MatchString(continuation.ScanStart) {
			return nil, invalidMapCursor
		}
	}
	ids, registryMore, err := b.discoveryIDs(ctx, p, continuation.ScanStart)
	if err != nil {
		return nil, err
	}
	page := discoveryPage{Spaces: []discoverySpace{}, Coverage: "complete", RegistryMode: "live", NextSteps: "Choose a returned id: map(space=id) explores it; start(primary_space=id) opens a session. Follow cursor with map(cursor=cursor) until it is empty. Discovery pages use a live registry, not an atomic cross-space snapshot. Actions apply at the root; nested sections may differ. If no spaces are returned after all pages, ask the workspace owner to grant access."}
	tokens := map[string]string{}
	setCursor := func(after string, more bool) error {
		page.Cursor = ""
		page.Coverage = "complete"
		if !more {
			return nil
		}
		page.Coverage = "truncated"
		if saved, ok := tokens[after]; ok {
			page.Cursor = saved
			return nil
		}
		next := continuation
		next.ScanStart = after
		key, err := b.mapCursorKey()
		if err != nil {
			return err
		}
		page.Cursor, err = sealMapCursor(key, next)
		if err != nil {
			return err
		}
		tokens[after] = page.Cursor
		return nil
	}
	after := continuation.ScanStart
	for index, id := range ids {
		card, err := b.discoveryCard(ctx, p, id)
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrDenied) {
			after = id
			continue
		}
		if err != nil {
			return nil, err
		}
		page.Spaces = append(page.Spaces, card)
		if err = setCursor(id, index+1 < len(ids) || registryMore); err != nil {
			return nil, err
		}
		raw, err := json.Marshal(page)
		if err != nil {
			return nil, err
		}
		if len(page.Spaces) > 100 || len(raw) > defaultMapBudget {
			page.Spaces = page.Spaces[:len(page.Spaces)-1]
			if len(page.Spaces) == 0 {
				return nil, fmt.Errorf("%w: discovery card exceeds response budget", ErrInvalid)
			}
			if err = setCursor(after, true); err != nil {
				return nil, err
			}
			break
		}
		after = id
	}
	// Includes denied-only windows and denied tails; raw scan position advances
	// even when the visible page is empty.
	if len(ids) == 0 || after == ids[len(ids)-1] {
		if err = setCursor(after, registryMore); err != nil {
			return nil, err
		}
	}
	p, err = b.Auth.Current(ctx, p)
	if err != nil {
		return nil, err
	}
	for i, space := range page.Spaces {
		current, err := b.discoveryCard(ctx, p, space.ID)
		if err != nil {
			return nil, err
		}
		actions := []string{}
		for _, action := range space.Actions {
			if contains(current.Actions, action) {
				actions = append(actions, action)
			}
		}
		page.Spaces[i].Actions = actions
	}
	raw, err := json.Marshal(page)
	if err != nil {
		return nil, err
	}
	if len(raw) > defaultMapBudget {
		return nil, fmt.Errorf("%w: discovery metadata exceeds response budget", ErrInvalid)
	}
	spaces := make([]map[string]any, 0, len(page.Spaces))
	for _, space := range page.Spaces {
		spaces = append(spaces, map[string]any{"id": space.ID, "kind": space.Kind, "actions": space.Actions})
	}
	return map[string]any{"spaces": spaces, "cursor": page.Cursor, "coverage": page.Coverage, "registry_mode": page.RegistryMode, "next_steps": page.NextSteps}, nil
}
func (b *Broker) discoveryIDs(ctx context.Context, p Principal, after string) ([]string, bool, error) {
	ids := []string{}
	more := false
	if contains(p.Spaces, "*") {
		const prefix = "registry/spaces/"
		start := ""
		if after != "" {
			start = prefix + after
		}
		keys, next, err := b.Store.Objects.List(ctx, prefix, start, 1001)
		if err != nil {
			return nil, false, err
		}
		for _, key := range keys {
			ids = append(ids, strings.TrimPrefix(key, prefix))
		}
		more = next != ""
	} else {
		for _, id := range p.Spaces {
			if id > after {
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)
	unique := ids[:0]
	for _, id := range ids {
		if len(unique) == 0 || unique[len(unique)-1] != id {
			unique = append(unique, id)
		}
	}
	if len(unique) > 1000 {
		return unique[:1000], true, nil
	}
	return unique, more, nil
}
func (b *Broker) discoveryCard(ctx context.Context, p Principal, id string) (discoverySpace, error) {
	snap, err := b.Store.Load(ctx, id)
	if err != nil {
		return discoverySpace{}, err
	}
	if err = b.AuthorizePath(ctx, p, snap, "/", "read"); err != nil {
		return discoverySpace{}, err
	}
	card := discoverySpace{ID: id, Kind: snap.Space.Kind, Actions: []string{"read"}}
	for _, action := range []string{"propose", "write"} {
		if err = b.AuthorizePath(ctx, p, snap, "/", action); err == nil {
			card.Actions = append(card.Actions, action)
		} else if !errors.Is(err, ErrDenied) {
			return discoverySpace{}, err
		}
	}
	return card, nil
}
