package camp

import (
	"bytes"
	"context"
	"errors"
	"github.com/denisqq/agent-camp/camp-server/teststore"
	"testing"
)

func TestReadCacheReusesImmutableObjectsButAlwaysReloadsHead(t *testing.T) {
	ctx := context.Background()
	base := teststore.New()
	counter := &mapReadCounter{Store: base}
	cached := NewBroker(NewStore(counter), nil).withReadCache().Store.Objects
	key := "spaces/team/nodes/node"
	if _, err := base.Put(ctx, key, []byte("original"), "*"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		data, _, err := cached.Get(ctx, key)
		if err != nil || string(data) != "original" {
			t.Fatal(string(data), err)
		}
		data[0] = 'X' // Consumers cannot corrupt the shared cached bytes.
	}
	if counter.reads != 1 {
		t.Fatal("immutable node fetched repeatedly", counter.reads)
	}
	head := "spaces/team/HEAD"
	tag, err := base.Put(ctx, head, []byte("old-policy"), "*")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = cached.Get(ctx, head); err != nil {
		t.Fatal(err)
	}
	if _, err = base.Put(ctx, head, []byte("revoked-policy"), tag); err != nil {
		t.Fatal(err)
	}
	data, _, err := cached.Get(ctx, head)
	if err != nil || string(data) != "revoked-policy" {
		t.Fatal("stale authorization HEAD", string(data), err)
	}
	if counter.reads != 3 {
		t.Fatal("HEAD was cached", counter.reads)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err = cached.Get(cancelled, key); !errors.Is(err, context.Canceled) {
		t.Fatal("cache ignored cancellation", err)
	}
}
func TestReadCacheBoundsMemoryAndInvalidatesWrites(t *testing.T) {
	ctx := context.Background()
	base := teststore.New()
	cached := NewBroker(NewStore(base), nil).withReadCache().Store.Objects.(*immutableReadCache)
	large := "spaces/team/revisions/large"
	if _, err := base.Put(ctx, large, bytes.Repeat([]byte("x"), readCacheBytes+1), "*"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cached.Get(ctx, large); err != nil {
		t.Fatal(err)
	}
	if cached.size != 0 || len(cached.entries) != 0 {
		t.Fatal("oversized object retained")
	}
	key := "spaces/team/nodes/node"
	tag, err := cached.Put(ctx, key, []byte("one"), "*")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = cached.Get(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err = cached.Put(ctx, key, []byte("two"), tag); err != nil {
		t.Fatal(err)
	}
	data, _, err := cached.Get(ctx, key)
	if err != nil || string(data) != "two" {
		t.Fatal("write did not invalidate cache", err)
	}
	if err = cached.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, _, err = cached.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatal("delete did not invalidate cache", err)
	}
	if cached.size != 0 {
		t.Fatal("invalidated bytes retained", cached.size)
	}
}
