package runtimepack

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// packArchive builds an in-memory tar.gz runtime pack with the given files.
func packArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func servePack(t *testing.T, body []byte, hits *int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		w.Write(body)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestEnsureDownloadsAndActivates(t *testing.T) {
	body := packArchive(t, map[string]string{
		"runtime/bin/python":             "#!/bin/sh\n",
		"runtime/adapter/camp_runner.py": "# runner\n",
		"runtime/bin/monty":              "#!/bin/sh\n",
		"runtime/manifest.json":          `{"version":"1.2.3"}`,
	})
	hits := 0
	server := servePack(t, body, &hits)
	cache := filepath.Join(t.TempDir(), "packs")
	m := &Manager{BaseURL: server.URL, CacheDir: cache}

	paths, err := m.Ensure(context.Background(), "1.2.3", "linux-amd64")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{paths.Python, paths.Script, paths.Monty} {
		if !filepath.IsAbs(p) {
			t.Fatalf("path not absolute: %s", p)
		}
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("pack file missing: %s (%v)", p, err)
		}
	}
	if hits != 1 {
		t.Fatalf("download hits: %d", hits)
	}
	// A second Ensure reuses the activated pack without downloading.
	if _, err = m.Ensure(context.Background(), "1.2.3", "linux-amd64"); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("cached pack re-downloaded: %d hits", hits)
	}
}

func TestRuntimeInterruptedDownloadPreservesPrevious(t *testing.T) {
	good := packArchive(t, map[string]string{
		"runtime/bin/python":             "python-1",
		"runtime/adapter/camp_runner.py": "runner-1",
		"runtime/bin/monty":              "monty-1",
		"runtime/manifest.json":          `{"version":"1.0.0"}`,
	})
	hits := 0
	server := servePack(t, good, &hits)
	cache := filepath.Join(t.TempDir(), "packs")
	m := &Manager{BaseURL: server.URL, CacheDir: cache}
	if _, err := m.Ensure(context.Background(), "1.0.0", "linux-amd64"); err != nil {
		t.Fatal(err)
	}

	// Next download fails mid-transfer: the server closes the connection.
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "4096")
		w.Write(good[:64])
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		panic(http.ErrAbortHandler)
	}))
	defer broken.Close()
	m.BaseURL = broken.URL
	if _, err := m.Ensure(context.Background(), "2.0.0", "linux-amd64"); err == nil {
		t.Fatal("truncated download must fail")
	}
	// The previous pack is still intact and usable.
	m.BaseURL = server.URL
	paths, err := m.Ensure(context.Background(), "1.0.0", "linux-amd64")
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(paths.Python)
	if err != nil || string(body) != "python-1" {
		t.Fatalf("previous pack damaged: %q %v", body, err)
	}
}

func TestRuntimeVersionMismatch(t *testing.T) {
	// Pack declares a different version than requested: activation must
	// fail instead of silently running mismatched runtimes.
	body := packArchive(t, map[string]string{
		"runtime/bin/python":             "python",
		"runtime/adapter/camp_runner.py": "runner",
		"runtime/bin/monty":              "monty",
		"runtime/manifest.json":          `{"version":"9.9.9"}`,
	})
	hits := 0
	server := servePack(t, body, &hits)
	m := &Manager{BaseURL: server.URL, CacheDir: filepath.Join(t.TempDir(), "packs")}
	if _, err := m.Ensure(context.Background(), "1.0.0", "linux-amd64"); err == nil {
		t.Fatal("version mismatch accepted")
	}
}

func TestChecksumEnforced(t *testing.T) {
	body := packArchive(t, map[string]string{
		"runtime/bin/python":             "python",
		"runtime/adapter/camp_runner.py": "runner",
		"runtime/bin/monty":              "monty",
		"runtime/manifest.json":          `{"version":"1.0.0"}`,
	})
	hits := 0
	server := servePack(t, body, &hits)
	sum := sha256.Sum256(body)
	goodChecksum := hex.EncodeToString(sum[:])
	m := &Manager{BaseURL: server.URL, CacheDir: filepath.Join(t.TempDir(), "packs"), Checksum: goodChecksum}
	if _, err := m.Ensure(context.Background(), "1.0.0", "linux-amd64"); err != nil {
		t.Fatal(err)
	}
	// Corrupt checksum: refuse.
	m2 := &Manager{BaseURL: server.URL, CacheDir: filepath.Join(t.TempDir(), "packs2"), Checksum: "deadbeef"}
	if _, err := m2.Ensure(context.Background(), "1.0.0", "linux-amd64"); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
}
