// Package teststore provides an object-store test double with real CAS semantics.
// It is imported only by tests; deployments require S3.
package teststore

import (
	"context"
	"fmt"
	"github.com/denisqq/agent-camp/camp-server/objectstore"
	"sort"
	"strings"
	"sync"
)

type item struct {
	data []byte
	etag string
}
type Memory struct {
	mu     sync.Mutex
	data   map[string]item
	writes int
}

func New() *Memory            { return &Memory{data: map[string]item{}} }
func (m *Memory) Writes() int { m.mu.Lock(); defer m.mu.Unlock(); return m.writes }
func (m *Memory) Get(ctx context.Context, k string) ([]byte, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	v, ok := m.data[k]
	if !ok {
		return nil, "", objectstore.ErrNotFound
	}
	return append([]byte(nil), v.data...), v.etag, nil
}
func (m *Memory) Put(ctx context.Context, k string, b []byte, condition string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	old, ok := m.data[k]
	if (condition == "*" && ok) || (condition != "" && condition != "*" && (!ok || old.etag != condition)) {
		return "", objectstore.ErrConflict
	}
	m.writes++
	etag := fmt.Sprint(m.writes)
	m.data[k] = item{append([]byte(nil), b...), etag}
	return etag, nil
}
func (m *Memory) Delete(ctx context.Context, k string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, k)
	return ctx.Err()
}
func (m *Memory) List(ctx context.Context, prefix, after string, limit int) ([]string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var keys []string
	for k := range m.data {
		if strings.HasPrefix(k, prefix) && k > after {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	next := ""
	if len(keys) > limit {
		keys = keys[:limit]
		next = keys[len(keys)-1]
	}
	return keys, next, ctx.Err()
}
