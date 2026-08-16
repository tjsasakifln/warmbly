//go:build !minprofile

package storage

import (
	"context"
	"testing"
)

func TestNewFromEnv_S3WithFallbackBucket(t *testing.T) {
	t.Setenv("BLOB_PROVIDER", "s3")
	t.Setenv("BLOB_BUCKET", "")
	s, err := NewFromEnv(context.Background(), "fallback-bucket")
	if err != nil {
		t.Fatal(err)
	}
	if s.Name() != "s3" {
		t.Fatalf("expected s3, got %q", s.Name())
	}
}
