package camp

import (
	"archive/tar"
	"bytes"
	"github.com/datretyakov/agent-camp/internal/teststore"
	"io"
	"strings"
	"testing"
)

func TestBackupRestoresHistoryWithoutSQLite(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	e, _ := b.Begin(ctx, p, "alice")
	b.Call(ctx, e, "write", map[string]any{"path": "/goal", "content": "first"})
	if _, err := b.Finish(ctx, e, "first", Receipt{Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	e, _ = b.Begin(ctx, p, "alice")
	b.Call(ctx, e, "write", map[string]any{"path": "/goal", "content": "second"})
	if _, err := b.Finish(ctx, e, "second", Receipt{Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := b.Store.Backup(ctx, &archive); err != nil {
		t.Fatal(err)
	}
	restored := NewStore(teststore.New())
	if err := restored.Restore(ctx, bytes.NewReader(archive.Bytes())); err != nil {
		t.Fatal(err)
	}
	snap, err := restored.Load(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	rec, err := restored.RecordByPath(ctx, snap, "/goal")
	if err != nil {
		t.Fatal(err)
	}
	rev, err := restored.ReadRevision(ctx, "alice", rec.Revision)
	if err != nil || rev.Content != "second" {
		t.Fatal(rev, err)
	}
	old, err := restored.ReadRevision(ctx, "alice", rev.Parent)
	if err != nil || old.Content != "first" {
		t.Fatal("history missing", old, err)
	}
	if err := restored.Restore(ctx, bytes.NewReader(archive.Bytes())); err == nil {
		t.Fatal("restore overwrote existing installation")
	}
}

func TestRestoreRejectsMissingReachableObjectBeforePublishing(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	e, err := b.Begin(ctx, p, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Call(ctx, e, "write", map[string]any{"path": "/goal", "content": "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Finish(ctx, e, "seed", Receipt{}); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err = b.Store.Backup(ctx, &archive); err != nil {
		t.Fatal(err)
	}
	var broken bytes.Buffer
	reader := tar.NewReader(bytes.NewReader(archive.Bytes()))
	writer := tar.NewWriter(&broken)
	removed := false
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if !removed && strings.Contains(header.Name, "/nodes/") {
			removed = true
			continue
		}
		if err = writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err = writer.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if !removed {
		t.Fatal("fixture has no referenced nodes")
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	restored := NewStore(teststore.New())
	if err := restored.Restore(ctx, bytes.NewReader(broken.Bytes())); err == nil {
		t.Fatal("accepted incomplete reachability")
	}
	spaces, err := restored.Spaces(ctx)
	if err != nil || len(spaces) != 0 {
		t.Fatal("partial roots published", spaces, err)
	}
}
