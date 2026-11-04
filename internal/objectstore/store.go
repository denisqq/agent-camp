// Package objectstore defines the conditional object operations required by Brain.
package objectstore

import (
	"context"
	"errors"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conditional write conflict")

// Put condition is empty for unconditional, * for create-only, or an opaque ETag.
type Store interface {
	Get(context.Context, string) ([]byte, string, error)
	Put(context.Context, string, []byte, string) (string, error)
	List(context.Context, string, string, int) ([]string, string, error)
	Delete(context.Context, string) error
}
