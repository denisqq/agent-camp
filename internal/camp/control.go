package camp

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/denisqq/agent-camp/internal/tree"
)

func (s *Store) ControlGet(ctx context.Context, snap Snapshot, key string, out any) error {
	raw, err := s.tree(snap.Space.ID).Get(ctx, snap.Control, key)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
func (s *Store) ControlSet(ctx context.Context, snap *Snapshot, key string, value any) error {
	raw, err := encode(value)
	if err != nil {
		return err
	}
	snap.Control, err = s.tree(snap.Space.ID).Set(ctx, snap.Control, key, raw)
	return err
}
func (s *Store) ControlPage(ctx context.Context, snap Snapshot, prefix, after string, limit int) ([]tree.Entry, error) {
	return s.tree(snap.Space.ID).Page(ctx, snap.Control, prefix, after, limit)
}
func (s *Store) ControlDelete(ctx context.Context, snap *Snapshot, key string) error {
	var err error
	snap.Control, err = s.tree(snap.Space.ID).Delete(ctx, snap.Control, key)
	return err
}
func (b *Broker) CheckSources(ctx context.Context, sources []SourceRef) error {
	for _, source := range sources {
		snap, err := b.Store.Load(ctx, source.Space)
		if errors.Is(err, ErrNotFound) {
			return ErrDenied
		}
		if err != nil {
			return err
		}
		if snap.Space.Policy != source.Policy {
			return ErrDenied
		}
		rec, err := b.Store.Record(ctx, snap, source.Record)
		if errors.Is(err, ErrNotFound) {
			return ErrDenied
		}
		if err != nil {
			return err
		}
		if rec.Deleted || rec.Path != source.Path {
			return ErrDenied
		}
		// Persisted ACLs may grant this subject access. Keep its pre-Camp name
		// until an explicit ACL migration changes both stored grants and checks.
		p := Principal{Subject: "brain-curator", Role: "curator", Spaces: []string{source.Space}, Scopes: []string{"read", "collective:process"}}
		if err = b.AuthorizePath(ctx, p, snap, source.Path, "collective:process"); err != nil {
			return err
		}
		if err = b.AuthorizePath(ctx, p, snap, source.Path, "read"); err != nil {
			return err
		}
	}
	return nil
}

// JobKey derives the durable curation job identity for a source revision.
// The curator and the proposal status projection must agree on it.
func JobKey(space, revision, policy string) string { return digest(space + revision + policy) }

// proposalStatus projects operational fields only: worker drafts and source evidence
// remain internal even when the caller can read the shared space.
func (b *Broker) proposalStatus(ctx context.Context, e *Execution, space string, rev Revision) (any, error) {
	if rev.Kind == "proposal" {
		id, _ := rev.Metadata["source_record"].(string)
		revision, _ := rev.Metadata["source_revision"].(string)
		snap, err := b.snapshot(ctx, e, space)
		if err != nil {
			return nil, err
		}
		rec, err := b.Store.Record(ctx, snap, id)
		if err != nil {
			return nil, err
		}
		_, rev, err = b.resolve(ctx, e, space, rec.Path, revision)
		if err != nil {
			return nil, err
		}
	}
	if b.Status == nil {
		return map[string]any{"state": "unavailable", "attempts": 0}, nil
	}
	state, attempts, found, err := b.Status(ctx, JobKey(space, rev.ID, e.Snapshots[space].Space.Policy))
	if err != nil {
		return nil, err
	}
	if !found {
		return map[string]any{"state": "awaiting_discovery", "attempts": 0}, nil
	}
	return map[string]any{"state": state, "attempts": attempts}, nil
}
