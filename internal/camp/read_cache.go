package camp

import (
	"bytes"
	"github.com/denisqq/agent-camp/internal/objectstore"
	"context"
	"strings"
	"sync"
)

// Reuse immutable S3 objects only for the lifetime of one operation. HEAD,
// registry entries, token state and authorization decisions are never cached.
// This avoids repeatedly fetching the same persistent-tree nodes during ACL
// checks without making revocations or concurrent moves invisible.
type immutableReadCache struct {
	objectstore.Store
	mu      sync.Mutex
	entries map[string]cachedObject
	size    int
}
type cachedObject struct {
	data []byte
	etag string
}

const readCacheBytes = 8 << 20
const readCacheEntries = 4096

func (b *Broker) withReadCache() *Broker {
	if _, ok := b.Store.Objects.(*immutableReadCache); ok {
		return b
	}
	store := *b.Store
	store.Objects = &immutableReadCache{Store: store.Objects, entries: map[string]cachedObject{}}
	broker := *b
	broker.Store = &store
	return &broker
}
func immutableObject(key string) bool {
	parts := strings.Split(key, "/")
	return len(parts) == 4 && parts[0] == "spaces" && (parts[2] == "nodes" || parts[2] == "revisions" || parts[2] == "commits")
}
func (s *immutableReadCache) Get(ctx context.Context, key string) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if !immutableObject(key) {
		return s.Store.Get(ctx, key)
	}
	s.mu.Lock()
	entry, ok := s.entries[key]
	s.mu.Unlock()
	if ok {
		return bytes.Clone(entry.data), entry.etag, nil
	}
	data, etag, err := s.Store.Get(ctx, key)
	if err != nil {
		return nil, "", err
	}
	size := len(key) + len(data) + len(etag)
	s.mu.Lock()
	if _, exists := s.entries[key]; !exists && len(s.entries) < readCacheEntries && s.size+size <= readCacheBytes {
		s.entries[key] = cachedObject{bytes.Clone(data), etag}
		s.size += size
	}
	s.mu.Unlock()
	return data, etag, nil
}
func (s *immutableReadCache) forget(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.entries[key]; ok {
		s.size -= len(key) + len(old.data) + len(old.etag)
		delete(s.entries, key)
	}
}
func (s *immutableReadCache) Put(ctx context.Context, key string, data []byte, expect string) (string, error) {
	s.forget(key)
	return s.Store.Put(ctx, key, data, expect)
}
func (s *immutableReadCache) Delete(ctx context.Context, key string) error {
	s.forget(key)
	return s.Store.Delete(ctx, key)
}
