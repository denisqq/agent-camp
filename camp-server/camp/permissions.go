package camp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
)

// ValidateAccess is also used at the administrative HTTP boundary.
func ValidateAccess(access *Access) error {
	if access == nil {
		return nil
	}
	raw, err := json.Marshal(access)
	if err != nil {
		return ErrInvalid
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ErrInvalid
	}
	if _, ok := ParseAccess(value); !ok {
		return ErrInvalid
	}
	return nil
}
func (b *Broker) checkWrite(ctx context.Context, e *Execution, rev Revision) error {
	snap, err := b.Store.Load(ctx, e.Primary)
	if err != nil {
		return err
	}
	if source, ok := e.Proposals[rev.RecordID]; ok {
		if err = b.AuthorizePath(ctx, e.Principal, snap, source, "propose"); err != nil {
			return err
		}
		return b.AuthorizePath(ctx, e.Principal, snap, rev.Path, "propose")
	}
	if err = b.AuthorizePath(ctx, e.Principal, snap, rev.Path, "write"); err != nil {
		return err
	}
	old, err := b.Store.Record(ctx, snap, rev.RecordID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if err == nil {
		if err = b.AuthorizePath(ctx, e.Principal, snap, old.Path, "write"); err != nil {
			return err
		}
	}
	access, hasAccess := rev.Metadata["access"]
	if hasAccess {
		if rev.Kind != "section" {
			return ErrInvalid
		}
		if _, valid := ParseAccess(access); !valid {
			return ErrInvalid
		}
		if snap.Space.Kind == "shared" {
			return ErrDenied
		}
	}
	oldAccess, hadAccess := old.Metadata["access"]
	changed := hasAccess != hadAccess || !reflect.DeepEqual(access, oldAccess) || ((hasAccess || hadAccess) && (rev.Path != old.Path || rev.Kind != old.Kind || rev.Deleted != old.Deleted))
	if changed && e.Principal.Role != "admin" && e.Principal.Subject != snap.Space.Owner {
		return ErrDenied
	}
	return nil
}
