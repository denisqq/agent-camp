package index

import (
	"github.com/denisqq/agent-camp/internal/camp"
	"github.com/denisqq/agent-camp/internal/teststore"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func TestIndexIncrementalReopenAndRebuild(t *testing.T) {
	ctx := context.Background()
	s := camp.NewStore(teststore.New())
	snap, err := s.CreateSpace(ctx, camp.Space{ID: "alice", Owner: "alice", Kind: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	snap, err = s.Commit(ctx, snap, []camp.Revision{{ID: "r1", RecordID: "a", Path: "/a", Kind: "experiment", Content: "успех на границе\nследующей строки", Metadata: map[string]any{"outcome": "success"}}, {ID: "r2", RecordID: "b", Path: "/b", Kind: "experiment", Content: "неудача", Metadata: map[string]any{"outcome": "failure"}}}, "first", camp.Receipt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "index.sqlite")
	idx, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	if err = idx.Sync(ctx, s, "alice"); err != nil {
		t.Fatal(err)
	}
	hits, water, err := idx.Query(ctx, "alice", "grep", "границе\nследующей", 50)
	if err != nil || len(hits) != 1 || hits[0].RecordID != "a" || water != snap.Commit {
		t.Fatalf("literal crossing lines: %+v %s %v", hits, water, err)
	}
	hits, _, err = idx.Query(ctx, "alice", "search", "неудача", 50)
	if err != nil || len(hits) != 1 || hits[0].RecordID != "b" {
		t.Fatalf("failure excluded: %+v %v", hits, err)
	}
	snap, err = s.Commit(ctx, snap, []camp.Revision{{ID: "r3", RecordID: "a", Parent: "r1", Path: "/a", Kind: "experiment", Content: "новое"}}, "second", camp.Receipt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = idx.Sync(ctx, s, "alice"); err != nil {
		t.Fatal(err)
	}
	hits, _, err = idx.Query(ctx, "alice", "grep", "успех", 50)
	if err != nil || len(hits) != 0 {
		t.Fatal("stale content", hits, err)
	}
	idx.Close()
	idx, err = Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	hits, water, err = idx.Query(ctx, "alice", "grep", "новое", 50)
	if err != nil || len(hits) != 1 || water != snap.Commit {
		t.Fatal("restart lost index", err)
	}
	fresh, err := Open(filepath.Join(t.TempDir(), "rebuilt.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if err = fresh.Sync(ctx, s, "alice"); err != nil {
		t.Fatal(err)
	}
	hits, water, err = fresh.Query(ctx, "alice", "grep", "новое", 50)
	if err != nil || len(hits) != 1 || water != snap.Commit {
		t.Fatal("rebuild lost memory", err)
	}
}

func TestSearchPagesCoverAllMatchesAndFenceIndexChanges(t *testing.T) {
	ctx := context.Background()
	s := camp.NewStore(teststore.New())
	snap, _ := s.CreateSpace(ctx, camp.Space{ID: "alice", Kind: "personal", Owner: "alice"})
	var revisions []camp.Revision
	for n := range 7 {
		id := fmt.Sprintf("r%d", n)
		revisions = append(revisions, camp.Revision{ID: id, RecordID: id, Path: "/" + id, Content: "я и неудача"})
	}
	var err error
	snap, err = s.Commit(ctx, snap, revisions, "seed", camp.Receipt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	if err = idx.Sync(ctx, s, "alice"); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"grep", "search"} {
		seen := map[string]bool{}
		offset := 0
		watermark := ""
		for {
			page, err := idx.QueryPage(ctx, "alice", mode, "я", offset, 2, watermark)
			if err != nil {
				t.Fatal(err)
			}
			watermark = page.Watermark
			for _, hit := range page.Hits {
				if seen[hit.RecordID] {
					t.Fatal("duplicate", hit)
				}
				seen[hit.RecordID] = true
			}
			if !page.More {
				break
			}
			offset = page.Next
		}
		if len(seen) != 7 {
			t.Fatal("missing matches", mode, seen)
		}
	}
	old := snap.Commit
	_, err = s.Commit(ctx, snap, []camp.Revision{{ID: "new", RecordID: "r0", Parent: "r0", Path: "/r0", Content: "changed"}}, "change", camp.Receipt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = idx.Sync(ctx, s, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err = idx.QueryPage(ctx, "alice", "grep", "я", 2, 2, old); !errors.Is(err, camp.ErrConflict) {
		t.Fatal("stale page accepted", err)
	}
}

func TestSearchCursorRejectsLostIndex(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	if _, err = idx.QueryPage(context.Background(), "alice", "grep", "word", 2, 2, "previous-checkpoint"); !errors.Is(err, camp.ErrConflict) {
		t.Fatal("lost index silently accepted cursor", err)
	}
}

func TestReviewHiddenSearchCount(t *testing.T) {
	ctx := context.Background()
	s := camp.NewStore(teststore.New())
	snap, err := s.CreateSpace(ctx, camp.Space{ID: "alice", Kind: "personal", Owner: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	var revs []camp.Revision
	for n := range 3 {
		id := fmt.Sprintf("hidden%d", n)
		revs = append(revs, camp.Revision{ID: id, RecordID: id, Path: "/" + id, Kind: "note", Content: "secretquery"})
	}
	if _, err = s.Commit(ctx, snap, revs, "seed", camp.Receipt{}, nil); err != nil {
		t.Fatal(err)
	}
	idx, err := Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	if err = idx.Sync(ctx, s, "alice"); err != nil {
		t.Fatal(err)
	}
	auth := camp.NewAuth(camp.NewMemState())
	if err = auth.Bootstrap(ctx, "admin-test-token-32-characters-long"); err != nil {
		t.Fatal(err)
	}
	admin, err := auth.Authenticate(ctx, "admin-test-token-32-characters-long")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := auth.Issue(ctx, admin, camp.Principal{Subject: "bob", Role: "agent", Spaces: []string{"alice"}, Scopes: []string{"read"}, Expires: admin.Expires})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := auth.Authenticate(ctx, tok)
	if err != nil {
		t.Fatal(err)
	}
	b := camp.NewBroker(s, auth)
	b.Search = idx
	e, err := b.Begin(ctx, bob, "alice")
	if err != nil {
		t.Fatal(err)
	}
	pages := 0
	next := ""
	for {
		out, err := b.Call(ctx, e, "grep", map[string]any{"query": "secretquery", "limit": 1, "cursor": next})
		if err != nil {
			t.Fatal(err)
		}
		result := out.(map[string]any)
		if len(result["items"].([]camp.SearchHit)) != 0 {
			t.Fatal("unexpected visible data")
		}
		pages++
		next = result["cursor"].(string)
		if next == "" {
			break
		}
	}
	if pages != 1 {
		t.Fatalf("all matches hidden, but %d observable empty pages disclose hidden match count", pages)
	}
	snap, _ = s.Load(ctx, "alice")
	revs = nil
	for n := range 5 {
		id := fmt.Sprintf("visible%d", n)
		revs = append(revs, camp.Revision{ID: id, RecordID: id, Path: "/" + id, Kind: "section", Content: "secretquery", Metadata: map[string]any{"access": map[string]any{"visibility": "open"}}})
	}
	if _, err = s.Commit(ctx, snap, revs, "visible", camp.Receipt{}, nil); err != nil {
		t.Fatal(err)
	}
	if err = idx.Sync(ctx, s, "alice"); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"grep", "search", "find"} {
		e, err = b.Begin(ctx, bob, "alice")
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		next = ""
		for {
			out, err := b.Call(ctx, e, mode, map[string]any{"query": "secretquery", "limit": 2, "cursor": next})
			if err != nil {
				t.Fatal(err)
			}
			page := out.(map[string]any)
			paths := []string{}
			if mode == "find" {
				for _, rec := range page["items"].([]camp.Record) {
					paths = append(paths, rec.Path)
				}
			} else {
				for _, hit := range page["items"].([]camp.SearchHit) {
					paths = append(paths, hit.Path)
				}
			}
			if len(paths) == 0 {
				t.Fatal("observable empty continuation", mode)
			}
			for _, path := range paths {
				if seen[path] {
					t.Fatal("duplicate", path)
				}
				seen[path] = true
			}
			next = page["cursor"].(string)
			if next == "" {
				break
			}
		}
		if len(seen) != 5 {
			t.Fatal("missing authorized results", mode, seen)
		}
	}

}
