package camp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"
	"time"
)

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func TestFilesUploadAttachChunksAndForeignHandle(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	data := bytes.Repeat([]byte("абв"), 500000)
	sum := sha256.Sum256(data)
	handle, err := b.Upload(ctx, p, "alice", "/file", "text/plain", int64(len(data)), hex.EncodeToString(sum[:]), bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	e, _ := b.Begin(ctx, p, "alice")
	_, err = b.Call(ctx, e, "files.attach", map[string]any{"path": "/file", "handle": handle.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Finish(ctx, e, "upload", Receipt{Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	e, _ = b.Begin(ctx, p, "alice")
	got, err := b.ReadFileChunk(ctx, e, "alice", "/file", 1024*1024-3, 12)
	if err != nil || !bytes.Equal(got, data[1024*1024-3:1024*1024+9]) {
		t.Fatal("chunk boundary corrupted", err)
	}
	other := p
	other.TokenHash = "fake"
	if _, err = b.Upload(ctx, other, "alice", "/file", "text/plain", 1, "", bytes.NewReader([]byte("x"))); !errors.Is(err, ErrDenied) {
		t.Fatal("foreign upload accepted", err)
	}
	if _, err = b.Upload(ctx, p, "alice", "/file", "text/plain", 123, "", brokenReader{}); err == nil {
		t.Fatal("interrupted upload accepted")
	}
	if _, err = b.Upload(ctx, p, "alice", "/file", "text/plain", 1, "bad", bytes.NewReader([]byte("x"))); err == nil {
		t.Fatal("hash mismatch accepted")
	}
}

type lateExecutor struct{}

func (lateExecutor) Execute(ctx context.Context, code string, cb Callback) (RunOutput, error) {
	time.Sleep(30 * time.Millisecond)
	_, err := cb(ctx, "write", map[string]any{"path": "/late", "content": "late"})
	return RunOutput{}, err
}
func TestRunnerDeadlineDiscardsLateCallback(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	svc := NewSessions(b, lateExecutor{})
	svc.Timeout = 10 * time.Millisecond
	session, err := svc.Start(ctx, p, StartRequest{Space: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := svc.Run(ctx, p, RunRequest{SessionID: session.ID, OperationID: "late", Code: "late"})
	if err != nil || r.Status != "timeout" {
		t.Fatal(r, err)
	}
	snap, _ := b.Store.Load(ctx, "alice")
	if _, err = b.Store.RecordByPath(ctx, snap, "/late"); !errors.Is(err, ErrNotFound) {
		t.Fatal("late callback published", err)
	}
}
