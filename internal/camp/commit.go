package camp

import (
	"context"
	"errors"
	"reflect"
	"time"
)

var errHeadAdvanced = errors.New("HEAD advanced during publication")

// Commit rebases an unchanged write set over unrelated commits, then publishes
// one atomic HEAD. Same-record changes and occupied destinations remain conflicts.
func (s *Store) Commit(ctx context.Context, base Snapshot, revisions []Revision, operation string, receipt Receipt, space *Space) (Snapshot, error) {
	revisions = append([]Revision(nil), revisions...)
	for n := range revisions {
		if revisions[n].Time.IsZero() {
			revisions[n].Time = time.Now().UTC()
		}
	}
	for attempt := 0; attempt < 16; attempt++ {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		current, err := s.Load(ctx, base.Space.ID)
		if err != nil {
			return Snapshot{}, err
		}
		// Acknowledgement recovery precedes stale-policy and write-set checks.
		if operation != "" {
			prior, err := s.Operation(ctx, current, operation)
			if err == nil {
				if prior.Fingerprint != receipt.Fingerprint {
					return Snapshot{}, ErrConflict
				}
				return current, nil
			}
			if !errors.Is(err, ErrNotFound) {
				return Snapshot{}, err
			}
		}
		target := base
		if current.Commit != base.Commit {
			if space != nil {
				return Snapshot{}, ErrConflict
			} // Administrative expected_commit.
			if err = s.checkRebase(ctx, base, current, revisions, receipt); err != nil {
				return Snapshot{}, err
			}
			target = current
			original, err := s.At(ctx, base.Space.ID, base.Commit)
			if err != nil {
				return Snapshot{}, err
			}
			if base.Control != original.Control {
				if current.Control != original.Control {
					return Snapshot{}, ErrConflict
				}
				target.Control = base.Control // Preserve a pending atomic seed marker.
			}
		}
		accepted, err := s.commitOnce(ctx, target, revisions, operation, receipt, space)
		if !errors.Is(err, errHeadAdvanced) {
			return accepted, err
		}
	}
	return Snapshot{}, ErrConflict
}

// Authorization was checked by the broker. A rebase must not publish across a
// change to any effective ACL involved in those checks, including ancestor ACLs.
func (s *Store) checkRebase(ctx context.Context, base, current Snapshot, revisions []Revision, receipt Receipt) error {
	if !reflect.DeepEqual(base.Space, current.Space) {
		return ErrConflict
	}
	paths := map[string]bool{}
	for _, rev := range revisions {
		paths[rev.Path] = true
		old, err := s.Record(ctx, base, rev.RecordID)
		if err == nil {
			paths[old.Path] = true
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		latest, err := s.Record(ctx, current, rev.RecordID)
		if err == nil {
			if latest.Revision != rev.Parent {
				return ErrConflict
			}
		} else if !errors.Is(err, ErrNotFound) {
			return err
		} else if rev.Parent != "" {
			return ErrConflict
		}
	}
	for _, dep := range receipt.Dependencies {
		rev, err := s.ReadRevision(ctx, dep.Space, dep.Revision)
		if err != nil {
			return err
		}
		// A shared view may depend on a source in the space being rebased.
		// Its source policy was part of Broker.CheckDependencies too.
		for _, source := range rev.Sources {
			if source.Space != base.Space.ID {
				continue
			}
			paths[source.Path] = true
			rec, err := s.Record(ctx, current, source.Record)
			if err != nil || rec.Deleted || rec.Path != source.Path || current.Space.Policy != source.Policy {
				return ErrConflict
			}
		}
		if dep.Space != base.Space.ID {
			continue
		}
		paths[dep.Path] = true
		paths[rev.Path] = true
		before, err := s.Record(ctx, base, dep.Record)
		if err != nil {
			return ErrConflict
		}
		after, err := s.Record(ctx, current, dep.Record)
		if err != nil || before.Path != after.Path || before.Deleted != after.Deleted {
			return ErrConflict
		}
		paths[after.Path] = true
	}
	for path := range paths {
		before, err := s.SectionACL(ctx, base, path)
		if err != nil {
			return err
		}
		after, err := s.SectionACL(ctx, current, path)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(before, after) {
			return ErrConflict
		}
	}
	return nil
}
