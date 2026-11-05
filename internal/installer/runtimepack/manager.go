// Package runtimepack manages versioned server runtime packs: a packaged
// Python interpreter, the runner adapter, and the locked Monty binaries a
// Camp server needs. The slim CLI can register with a remote server
// without any pack; local server setup acquires one through Ensure.
//
// Packs are downloaded to a temporary directory, verified (checksum and
// embedded manifest version), and only then atomically activated by
// rename — an interrupted or failing download always leaves the previous
// working pack usable.
package runtimepack

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Paths are absolute locations inside an activated runtime pack.
type Paths struct {
	Root   string `json:"root"`
	Python string `json:"python"`
	Script string `json:"script"`
	Monty  string `json:"monty"`
}

type manifest struct {
	Version string `json:"version"`
}

// Manager downloads and caches runtime packs.
type Manager struct {
	// BaseURL serves {version}/camp-runtime-{platform}.tar.gz.
	BaseURL string
	// CacheDir holds one activated directory per version.
	CacheDir string
	// Checksum, when set, is the expected sha256 of the pack archive.
	Checksum string
}

// Ensure returns absolute Python/adapter/Monty paths for the requested
// version, downloading and activating the pack when it is not cached.
func (m *Manager) Ensure(ctx context.Context, version, platform string) (Paths, error) {
	if version == "" || platform == "" || strings.Contains(version, "..") || strings.Contains(platform, "..") {
		return Paths{}, fmt.Errorf("runtimepack: invalid version or platform %q %q", version, platform)
	}
	root := filepath.Join(m.CacheDir, "runtime-"+version+"-"+platform)
	paths := Paths{
		Root:   root,
		Python: filepath.Join(root, "runtime", "bin", "python"),
		Script: filepath.Join(root, "runtime", "adapter", "camp_runner.py"),
		Monty:  filepath.Join(root, "runtime", "bin", "monty"),
	}
	if _, err := os.Stat(filepath.Join(root, "runtime", "manifest.json")); err == nil {
		return paths, nil
	}
	if err := os.MkdirAll(m.CacheDir, 0o755); err != nil {
		return Paths{}, err
	}
	body, err := m.download(ctx, version, platform)
	if err != nil {
		return Paths{}, err
	}
	if m.Checksum != "" {
		sum := sha256.Sum256(body)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), m.Checksum) {
			return Paths{}, errors.New("runtimepack: checksum mismatch, keeping previous runtime")
		}
	}
	if err := extract(body, root); err != nil {
		os.RemoveAll(root + ".tmp")
		return Paths{}, err
	}
	return paths, nil
}

func (m *Manager) download(ctx context.Context, version, platform string) ([]byte, error) {
	url := fmt.Sprintf("%s/%s/camp-runtime-%s.tar.gz", strings.TrimSuffix(m.BaseURL, "/"), version, platform)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	var redirects int
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		redirects++
		if redirects > 5 {
			return errors.New("runtimepack: too many redirects")
		}
		return nil
	}}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("runtimepack: %s returned %d", url, resp.StatusCode)
	}
	// Bound the body: a runtime pack is hundreds of megabytes at most.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<30))
	if err != nil {
		return nil, err
	}
	return body, nil
}

// extract unpacks the archive into root+".tmp" and activates it by rename
// only after the manifest version matches the request.
func extract(body []byte, root string) error {
	tmp := root + ".tmp"
	os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var found manifest
	seenManifest := false
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(header.Name)
		if header.Typeflag != tar.TypeReg || strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			continue
		}
		if filepath.Base(name) == "manifest.json" {
			raw, err := io.ReadAll(io.LimitReader(tr, 1<<20))
			if err != nil {
				return err
			}
			if err := json.Unmarshal(raw, &found); err != nil {
				return fmt.Errorf("runtimepack: bad manifest: %w", err)
			}
			seenManifest = true
		}
		// Entries must stay inside the temporary directory.
		target := filepath.Join(tmp, name)
		if !strings.HasPrefix(target, tmp+string(filepath.Separator)) {
			return errors.New("runtimepack: archive entry escapes pack root")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := writeLimited(target, tr, header.Size); err != nil {
			return err
		}
	}
	if !seenManifest {
		return errors.New("runtimepack: archive has no manifest.json")
	}
	want := filepath.Base(root) // runtime-<version>-<platform>
	if !strings.HasPrefix(want, "runtime-"+found.Version+"-") {
		return fmt.Errorf("runtimepack: archive version %q does not match requested %q", found.Version, strings.TrimSuffix(strings.TrimPrefix(want, "runtime-"), ""))
	}
	// Only now, fully extracted and verified, does the pack become active.
	return os.Rename(tmp, root)
}

func writeLimited(target string, r io.Reader, size int64) error {
	if size > 8<<30 {
		return errors.New("runtimepack: archive member too large")
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, io.LimitReader(r, size))
	return err
}
