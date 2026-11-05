package tree

import (
	"context"
	"fmt"
	"github.com/denisqq/agent-camp/internal/teststore"
	"testing"
)

func TestPersistentTreePagingAndOldRoot(t *testing.T) {
	ctx := context.Background()
	s := teststore.New()
	tr := New(s, "test/")
	root := ""
	for i := 199; i >= 0; i-- {
		var err error
		root, err = tr.Set(ctx, root, fmt.Sprintf("doc/%03d", i), []byte(fmt.Sprintf("%d", i)))
		if err != nil {
			t.Fatal(err)
		}
	}
	before := root
	count := s.Writes()
	root, err := tr.Set(ctx, root, "doc/100", []byte(`"changed"`))
	if err != nil {
		t.Fatal(err)
	}
	if s.Writes()-count > 40 {
		t.Fatal("rewrote too much of manifest")
	}
	old, err := tr.Get(ctx, before, "doc/100")
	if err != nil || string(old) != "100" {
		t.Fatalf("old root: %s %v", old, err)
	}
	page, err := tr.Page(ctx, root, "doc/", "doc/098", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 3 || page[0].Key != "doc/099" || page[1].Key != "doc/100" || string(page[1].Value) != `"changed"` {
		t.Fatalf("page: %+v", page)
	}
	reopened := New(s, "test/")
	value, err := reopened.Get(ctx, root, "doc/100")
	if err != nil || string(value) != `"changed"` {
		t.Fatal("reopen lost state", err)
	}
}

func TestDeleteKeepsOldRootAndOtherKeys(t *testing.T) {
	ctx := context.Background()
	tr := New(teststore.New(), "delete/")
	root := ""
	for _, k := range []string{"a", "b", "c", "d"} {
		var err error
		root, err = tr.Set(ctx, root, k, []byte(`1`))
		if err != nil {
			t.Fatal(err)
		}
	}
	old := root
	root, err := tr.Delete(ctx, root, "b")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := tr.Page(ctx, root, "", "", 10)
	if err != nil || len(entries) != 3 || entries[1].Key != "c" {
		t.Fatal(entries, err)
	}
	if _, err = tr.Get(ctx, old, "b"); err != nil {
		t.Fatal("history mutated", err)
	}
}
