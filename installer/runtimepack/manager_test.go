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
	"strings"
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
	sum := sha256.Sum256(body)
	sidecar := hex.EncodeToString(sum[:]) + "  camp-runtime.tar.gz\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		if strings.HasSuffix(r.URL.Path, ".sha256") {
			w.Write([]byte(sidecar))
			return
		}
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
	if hits != 2 { // archive + checksum sidecar
		t.Fatalf("download hits: %d", hits)
	}
	// A second Ensure reuses the activated pack without downloading.
	if _, err = m.Ensure(context.Background(), "1.2.3", "linux-amd64"); err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
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

func TestMissingChecksumSidecarRefused(t *testing.T) {
	body := packArchive(t, map[string]string{
		"runtime/bin/python":             "python",
		"runtime/adapter/camp_runner.py": "runner",
		"runtime/bin/monty":              "monty",
		"runtime/manifest.json":          `{"version":"1.0.0"}`,
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sha256") {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}))
	defer server.Close()
	m := &Manager{BaseURL: server.URL, CacheDir: filepath.Join(t.TempDir(), "packs")}
	if _, err := m.Ensure(context.Background(), "1.0.0", "linux-amd64"); err == nil || !strings.Contains(err.Error(), "refusing unverified") {
		t.Fatalf("want refusal, got %v", err)
	}
}

func TestPackPreservesRelativeLinksAndManifest(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	files := map[string]string{"runtime/manifest.json": `{"version":"v1.2.3"}`, "runtime/python-real": "python", "runtime/bin/monty": "monty", "runtime/adapter/camp_runner.py": "runner"}
	for n, b := range files {
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: 0755, Size: int64(len(b))}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(b))
	}
	tw.WriteHeader(&tar.Header{Name: "runtime/bin/python", Typeflag: tar.TypeSymlink, Linkname: "../python-real"})
	tw.Close()
	gz.Close()
	root := filepath.Join(t.TempDir(), "runtime-v1.2.3-linux-amd64")
	if err := extract(buf.Bytes(), root); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "runtime/bin/python"))
	if err != nil || string(data) != "python" {
		t.Fatalf("relative interpreter link lost: %q %v", data, err)
	}
	data, err = os.ReadFile(filepath.Join(root, "runtime/manifest.json"))
	if err != nil || string(data) != files["runtime/manifest.json"] {
		t.Fatalf("manifest lost: %q %v", data, err)
	}
}

func TestPackRejectsMissingFilesAndWrongExactVersion(t *testing.T) {
	for _, files := range []map[string]string{
		{"runtime/manifest.json": `{"version":"v1.2.3"}`},
		{"runtime/manifest.json": `{"version":"v1.2"}`, "runtime/bin/python": "python", "runtime/bin/monty": "monty", "runtime/adapter/camp_runner.py": "runner"},
	} {
		root := filepath.Join(t.TempDir(), "runtime-v1.2.3-linux-amd64")
		if err := extract(packArchive(t, files), root); err == nil {
			t.Fatal("invalid pack activated")
		}
	}
}

func TestPackRejectsEscapingSymlinks(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "runtime/bin/python", Typeflag: tar.TypeSymlink, Linkname: "../../../outside"})
	tw.Close()
	gz.Close()
	root := filepath.Join(t.TempDir(), "runtime-v1-linux-amd64")
	if err := extract(buf.Bytes(), root); err == nil {
		t.Fatal("escaping link accepted")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("invalid pack activated")
	}
}

func TestPackCannotCreateDirectoriesThroughSymlinkParent(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "outside")
	if err := os.Mkdir(outside, 0755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "runtime/d", Typeflag: tar.TypeDir, Mode: 0755})
	for _, l := range [][2]string{{"runtime/d/x", "."}, {"runtime/d/a", "x/x/../../../outside"}, {"runtime/d/a/new/link", "."}} {
		tw.WriteHeader(&tar.Header{Name: l[0], Typeflag: tar.TypeSymlink, Linkname: l[1]})
	}
	tw.Close()
	gz.Close()
	if err := extract(buf.Bytes(), filepath.Join(dir, "runtime-v1-linux-amd64")); err == nil {
		t.Fatal("escaping pack accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "new")); !os.IsNotExist(err) {
		t.Fatal("archive created directory outside staging")
	}
}

func TestPackUnderSymlinkedCacheDirectory(t *testing.T) {
	real := t.TempDir()
	alias := filepath.Join(t.TempDir(), "cache")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for n, b := range map[string]string{"runtime/manifest.json": `{"version":"v1"}`, "runtime/bin/monty": "monty", "runtime/adapter/camp_runner.py": "runner", "runtime/python-real": "python"} {
		tw.WriteHeader(&tar.Header{Name: n, Mode: 0755, Size: int64(len(b))})
		tw.Write([]byte(b))
	}
	tw.WriteHeader(&tar.Header{Name: "runtime/bin/python", Typeflag: tar.TypeSymlink, Linkname: "../python-real"})
	tw.Close()
	gz.Close()
	if err := extract(buf.Bytes(), filepath.Join(alias, "runtime-v1-linux-amd64")); err != nil {
		t.Fatalf("valid cache below symlink rejected: %v", err)
	}
}
