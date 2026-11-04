// Package tree implements a content-addressed persistent ordered treap.
// Only the search path and rotated nodes are written for a changed key.
package tree

import (
	"github.com/denisqq/agent-camp/internal/objectstore"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

type Entry struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}
type node struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
	Left  string          `json:"left,omitempty"`
	Right string          `json:"right,omitempty"`
}
type Tree struct {
	store  objectstore.Store
	prefix string
}

func New(s objectstore.Store, prefix string) *Tree { return &Tree{s, prefix} }
func hash(b []byte) string                         { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func (t *Tree) load(ctx context.Context, id string) (node, error) {
	var n node
	if id == "" {
		return n, objectstore.ErrNotFound
	}
	b, _, err := t.store.Get(ctx, t.prefix+id)
	if err != nil {
		return n, err
	}
	if hash(b) != id {
		return n, errors.New("corrupt manifest node")
	}
	err = json.Unmarshal(b, &n)
	return n, err
}
func (t *Tree) save(ctx context.Context, n node) (string, error) {
	b, err := json.Marshal(n)
	if err != nil {
		return "", err
	}
	id := hash(b)
	_, err = t.store.Put(ctx, t.prefix+id, b, "*")
	if errors.Is(err, objectstore.ErrConflict) {
		err = nil
	}
	return id, err
}
func (t *Tree) Get(ctx context.Context, root, key string) (json.RawMessage, error) {
	for root != "" {
		n, err := t.load(ctx, root)
		if err != nil {
			return nil, err
		}
		if key == n.Key {
			return n.Value, nil
		}
		if key < n.Key {
			root = n.Left
		} else {
			root = n.Right
		}
	}
	return nil, objectstore.ErrNotFound
}
func (t *Tree) Set(ctx context.Context, root, key string, value json.RawMessage) (string, error) {
	if root == "" {
		return t.save(ctx, node{Key: key, Value: value})
	}
	n, err := t.load(ctx, root)
	if err != nil {
		return "", err
	}
	if key == n.Key {
		n.Value = value
		return t.save(ctx, n)
	}
	if key < n.Key {
		n.Left, err = t.Set(ctx, n.Left, key, value)
		if err != nil {
			return "", err
		}
		child, e := t.load(ctx, n.Left)
		if e != nil {
			return "", e
		}
		if hash([]byte(child.Key)) < hash([]byte(n.Key)) {
			n.Left = child.Right
			child.Right, err = t.save(ctx, n)
			if err != nil {
				return "", err
			}
			return t.save(ctx, child)
		}
	} else {
		n.Right, err = t.Set(ctx, n.Right, key, value)
		if err != nil {
			return "", err
		}
		child, e := t.load(ctx, n.Right)
		if e != nil {
			return "", e
		}
		if hash([]byte(child.Key)) < hash([]byte(n.Key)) {
			n.Right = child.Left
			child.Left, err = t.save(ctx, n)
			if err != nil {
				return "", err
			}
			return t.save(ctx, child)
		}
	}
	return t.save(ctx, n)
}
func (t *Tree) Page(ctx context.Context, root, prefix, after string, limit int) ([]Entry, error) {
	entries := []Entry{}
	var walk func(string) error
	walk = func(id string) error {
		if id == "" || len(entries) >= limit {
			return nil
		}
		n, err := t.load(ctx, id)
		if err != nil {
			return err
		}
		if n.Key > after && n.Key >= prefix {
			if err = walk(n.Left); err != nil {
				return err
			}
		}
		if len(entries) >= limit {
			return nil
		}
		if n.Key > after && strings.HasPrefix(n.Key, prefix) {
			entries = append(entries, Entry{n.Key, n.Value})
		}
		if prefix == "" || n.Key < prefix || strings.HasPrefix(n.Key, prefix) {
			return walk(n.Right)
		}
		return nil
	}
	err := walk(root)
	return entries, err
}

func (t *Tree) Delete(ctx context.Context, root, key string) (string, error) {
	if root == "" {
		return "", nil
	}
	n, err := t.load(ctx, root)
	if err != nil {
		return "", err
	}
	if key == n.Key {
		return t.merge(ctx, n.Left, n.Right)
	}
	if key < n.Key {
		n.Left, err = t.Delete(ctx, n.Left, key)
	} else {
		n.Right, err = t.Delete(ctx, n.Right, key)
	}
	if err != nil {
		return "", err
	}
	return t.save(ctx, n)
}
func (t *Tree) merge(ctx context.Context, left, right string) (string, error) {
	if left == "" {
		return right, nil
	}
	if right == "" {
		return left, nil
	}
	l, err := t.load(ctx, left)
	if err != nil {
		return "", err
	}
	r, err := t.load(ctx, right)
	if err != nil {
		return "", err
	}
	if hash([]byte(l.Key)) < hash([]byte(r.Key)) {
		l.Right, err = t.merge(ctx, l.Right, right)
		if err != nil {
			return "", err
		}
		return t.save(ctx, l)
	}
	r.Left, err = t.merge(ctx, left, r.Left)
	if err != nil {
		return "", err
	}
	return t.save(ctx, r)
}
