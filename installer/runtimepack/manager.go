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
	"time"

	"github.com/denisqq/agent-camp/internal/config"
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
	// Checksum, when set, is the expected sha256 of the pack archive;
	// otherwise the <archive>.sha256 sidecar is fetched and required.
	Checksum string
}

// Platform maps the running OS/arch to the release pack platform tag.
func Platform(goos, goarch string) string { return goos + "-" + goarch }

// Ensure returns absolute Python/adapter/Monty paths for the requested
// version, downloading and activating the pack when it is not cached.
func (m *Manager) Ensure(ctx context.Context, version, platform string) (Paths, error) {
	if !validComponent(version) || !validComponent(platform) {
		return Paths{}, fmt.Errorf("runtimepack: invalid version or platform %q %q", version, platform)
	}
	cache, err := filepath.Abs(m.CacheDir)
	if err != nil {
		return Paths{}, err
	}
	root := filepath.Join(cache, "runtime-"+version+"-"+platform)
	paths := Paths{
		Root:   root,
		Python: filepath.Join(root, "runtime", "bin", "python"),
		Script: filepath.Join(root, "runtime", "adapter", "camp_runner.py"),
		Monty:  filepath.Join(root, "runtime", "bin", "monty"),
	}
	if _, err := os.Stat(filepath.Join(root, "runtime", "manifest.json")); err == nil {
		if err := validatePack(root, version); err != nil {
			return Paths{}, fmt.Errorf("invalid cached runtime: %w", err)
		}
		return paths, nil
	}
	if err := os.MkdirAll(m.CacheDir, 0o755); err != nil {
		return Paths{}, err
	}
	body, err := m.download(ctx, version, platform)
	if err != nil {
		return Paths{}, err
	}
	expected := m.Checksum
	if expected == "" {
		expected, err = m.sidecar(ctx, version, platform)
		if err != nil {
			return Paths{}, fmt.Errorf("runtimepack: refusing unverified download: %w", err)
		}
	}
	if expected != "" {
		sum := sha256.Sum256(body)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), expected) {
			return Paths{}, errors.New("runtimepack: checksum mismatch, keeping previous runtime")
		}
	}
	if err := extract(body, root); err != nil {
		return Paths{}, err
	}
	return paths, nil
}

// sidecar fetches the published <archive>.sha256 for the pack.
func (m *Manager) sidecar(ctx context.Context, version, platform string) (string, error) {
	url := m.packURL(version, platform) + ".sha256"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksum sidecar %s returned %d", url, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 || len(fields[0]) != 64 {
		return "", errors.New("malformed checksum sidecar")
	}
	return strings.ToLower(fields[0]), nil
}

func (m *Manager) packURL(version, platform string) string {
	return fmt.Sprintf("%s/%s/camp-runtime-%s.tar.gz", strings.TrimSuffix(m.BaseURL, "/"), version, platform)
}

func (m *Manager) download(ctx context.Context, version, platform string) ([]byte, error) {
	url := m.packURL(version, platform)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	var redirects int
	client := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
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
	tmp, err := os.MkdirTemp(filepath.Dir(root), ".camp-runtime-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	tmp, err = filepath.EvalSymlinks(tmp)
	if err != nil {
		return err
	}
	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	type link struct {
		name, target string
		hard         bool
	}
	var links []link
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(h.Name)
		if name != "runtime" && !strings.HasPrefix(name, "runtime"+string(filepath.Separator)) {
			return errors.New("runtimepack: archive entry outside runtime")
		}
		target := filepath.Join(tmp, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			if err := writeLimited(target, tr, h.Size); err != nil {
				return err
			}
		case tar.TypeSymlink, tar.TypeLink:
			dest := filepath.Join(filepath.Dir(name), h.Linkname)
			if h.Typeflag == tar.TypeLink {
				dest = filepath.Clean(h.Linkname)
			}
			if filepath.IsAbs(h.Linkname) || !strings.HasPrefix(dest, "runtime"+string(filepath.Separator)) {
				return errors.New("runtimepack: link escapes runtime")
			}
			links = append(links, link{name, h.Linkname, h.Typeflag == tar.TypeLink})
		default:
			return fmt.Errorf("runtimepack: unsupported archive entry %s", name)
		}
	}
	// Regular files are written before any links, so extraction never writes
	// through an archive-provided symlink. Validate resolved links before activation.
	for _, l := range links {
		dest := filepath.Join(tmp, l.name)
		parent, err := filepath.EvalSymlinks(filepath.Dir(dest))
		if err != nil || !strings.HasPrefix(parent, tmp+string(filepath.Separator)) {
			return errors.New("runtimepack: link parent escapes runtime")
		}
		if l.hard {
			source, err := filepath.EvalSymlinks(filepath.Join(tmp, l.target))
			if err != nil || !strings.HasPrefix(source, tmp+string(filepath.Separator)) {
				return errors.New("runtimepack: hard link escapes runtime")
			}
			if err := os.Link(filepath.Join(tmp, l.target), dest); err != nil {
				return err
			}
		} else if err := os.Symlink(l.target, dest); err != nil {
			return err
		}
	}
	for _, l := range links {
		resolved, err := filepath.EvalSymlinks(filepath.Join(tmp, l.name))
		if err != nil {
			return err
		}
		if !strings.HasPrefix(resolved, tmp+string(filepath.Separator)) {
			return errors.New("runtimepack: resolved link escapes runtime")
		}
	}
	version := strings.TrimPrefix(filepath.Base(root), "runtime-")
	// Platforms contain exactly the final OS and architecture components.
	parts := strings.Split(version, "-")
	if len(parts) < 3 {
		return errors.New("runtimepack: invalid cache path")
	}
	version = strings.Join(parts[:len(parts)-2], "-")
	if err := validatePack(tmp, version); err != nil {
		return err
	}
	if err := os.Rename(tmp, root); err != nil {
		// Another setup may have activated the same verified version first.
		if validErr := validatePack(root, version); validErr != nil {
			return err
		}
	}
	return nil
}

func validatePack(root, version string) error {
	raw, err := os.ReadFile(filepath.Join(root, "runtime", "manifest.json"))
	if err != nil {
		return err
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	if m.Version != version {
		return fmt.Errorf("runtimepack: manifest version %q does not match %q", m.Version, version)
	}
	for _, name := range []string{"bin/python", "bin/monty", "adapter/camp_runner.py"} {
		file, err := os.Stat(filepath.Join(root, "runtime", name))
		if err != nil {
			return err
		}
		if !file.Mode().IsRegular() {
			return fmt.Errorf("runtimepack: invalid file %s", name)
		}
	}
	return nil
}

func validComponent(s string) bool {
	if s == "" || strings.Contains(s, "..") {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_') {
			return false
		}
	}
	return true
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

// DefaultCacheDir holds installed runtimes beside private client profiles.
// These are persistent dependencies, not disposable OS/npm cache entries.
func DefaultCacheDir() string { return filepath.Join(config.ClientDir(), "runtimes") }

// DefaultBaseURL is the published release location of runtime packs.
const DefaultBaseURL = "https://github.com/denisqq/agent-camp/releases/download"
