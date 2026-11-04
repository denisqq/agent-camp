// Package filestore is the local mode of Agent Camp: the same object-store
// contract as the S3 backend, implemented over a plain directory. Conditional
// writes keep the workspace's compare-and-swap semantics; the on-disk layout
// is identical in shape to S3 keys, so a camp can move from local to S3 (or
// back) by copying the tree.
package filestore

import (
	"github.com/datretyakov/agent-camp/internal/objectstore"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type Store struct {
	root string
	mu   sync.Mutex
}

func Open(root string) (*Store, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(abs, 0o700); err != nil {
		return nil, err
	}
	return &Store{root: abs}, nil
}

func (s *Store) path(key string) (string, error) {
	if strings.ContainsAny(key, "\x00") || key == "" || strings.HasPrefix(key, "/") {
		return "", objectstore.ErrNotFound
	}
	clean := filepath.Clean(key)
	if strings.HasPrefix(clean, "..") {
		return "", objectstore.ErrNotFound
	}
	return filepath.Join(s.root, clean), nil
}
func etag(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (s *Store) Get(ctx context.Context, key string) ([]byte, string, error) {
	path, err := s.path(key)
	if err != nil {
		return nil, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", objectstore.ErrNotFound
		}
		return nil, "", err
	}
	return b, etag(b), nil
}

func (s *Store) Put(ctx context.Context, key string, b []byte, condition string) (string, error) {
	path, err := s.path(key)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	current, readErr := os.ReadFile(path)
	exists := readErr == nil
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return "", readErr
	}
	switch {
	case condition == "*":
		if exists {
			return "", objectstore.ErrConflict
		}
	case condition != "":
		if !exists || etag(current) != condition {
			return "", objectstore.ErrConflict
		}
	}
	tmp := path + ".tmp"
	if err = os.WriteFile(tmp, b, 0o600); err != nil {
		return "", err
	}
	return etag(b), os.Rename(tmp, path)
}

func (s *Store) List(ctx context.Context, prefix, after string, limit int) ([]string, string, error) {
	if limit < 1 || limit > 1000 {
		limit = 1000
	}
	keys := []string{}
	err := filepath.WalkDir(s.root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		rel, err := filepath.Rel(s.root, path)
		if err != nil {
			return nil
		}
		key := filepath.ToSlash(rel)
		if strings.HasPrefix(key, prefix) && key > after && !strings.HasSuffix(key, ".tmp") {
			keys = append(keys, key)
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	sort.Strings(keys)
	next := ""
	if len(keys) > limit {
		keys = keys[:limit]
		next = keys[len(keys)-1]
	}
	return keys, next, nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	path, err := s.path(key)
	if err != nil {
		return err
	}
	if err = os.Remove(path); err != nil && errors.Is(err, os.ErrNotExist) {
		return objectstore.ErrNotFound
	}
	return err
}
