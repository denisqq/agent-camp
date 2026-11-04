package filestore

import (
	"github.com/datretyakov/agent-camp/internal/objectstore"
	"context"
	"errors"
	"testing"
)

func TestConditionalWritesAndList(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tag, err := s.Put(ctx, "spaces/a/HEAD", []byte("one"), "*")
	if err != nil || tag == "" {
		t.Fatal(tag, err)
	}
	if _, err = s.Put(ctx, "spaces/a/HEAD", []byte("other"), "*"); !errors.Is(err, objectstore.ErrConflict) {
		t.Fatal("create-only write accepted twice", err)
	}
	if _, err = s.Put(ctx, "spaces/a/HEAD", []byte("two"), "stale"); !errors.Is(err, objectstore.ErrConflict) {
		t.Fatal("stale condition accepted", err)
	}
	next, err := s.Put(ctx, "spaces/a/HEAD", []byte("two"), tag)
	if err != nil {
		t.Fatal(err)
	}
	b, current, err := s.Get(ctx, "spaces/a/HEAD")
	if err != nil || string(b) != "two" || current != next {
		t.Fatal(b, current, err)
	}
	if _, _, err = s.Get(ctx, "spaces/missing"); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = s.Put(ctx, "../escape", []byte("x"), ""); err == nil {
		t.Fatal("traversal key accepted")
	}
	for _, key := range []string{"spaces/b/HEAD", "spaces/c/x", "other"} {
		if _, err = s.Put(ctx, key, []byte(key), "*"); err != nil {
			t.Fatal(err)
		}
	}
	keys, cursor, err := s.List(ctx, "spaces/", "", 2)
	if err != nil || len(keys) != 2 || keys[0] != "spaces/a/HEAD" || cursor == "" {
		t.Fatal(keys, cursor, err)
	}
	keys, cursor, err = s.List(ctx, "spaces/", cursor, 2)
	if err != nil || len(keys) != 1 || keys[0] != "spaces/c/x" || cursor != "" {
		t.Fatal(keys, cursor, err)
	}
	if err = s.Delete(ctx, "spaces/b/HEAD"); err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(ctx, "spaces/b/HEAD"); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatal(err)
	}
}
