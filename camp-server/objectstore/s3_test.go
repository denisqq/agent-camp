package objectstore

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestS3ConditionalWrites(t *testing.T) {
	endpoint := os.Getenv("CAMP_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("set CAMP_TEST_S3_ENDPOINT for real MinIO contract")
	}
	ctx := context.Background()
	s, err := NewS3(ctx, S3Config{Endpoint: endpoint, Region: "us-east-1", Bucket: "camp-contract", AccessKey: "camp-local-test", SecretKey: "camp-local-test-password"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	key := "contract/" + t.Name()
	_ = s.Delete(ctx, key)
	etag, err := s.Put(ctx, key, []byte("first"), "*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Put(ctx, key, []byte("duplicate"), "*"); !errors.Is(err, ErrConflict) {
		t.Fatalf("create conflict: %v", err)
	}
	if _, err = s.Put(ctx, key, []byte("second"), etag); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Put(ctx, key, []byte("lost update"), etag); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update: %v", err)
	}
	b, _, err := s.Get(ctx, key)
	if err != nil || string(b) != "second" {
		t.Fatalf("read after write: %q %v", b, err)
	}
}
