//go:build minprofile

package storage

import (
	"context"
	"errors"
	"testing"
)

func TestNewFromEnv_MinProfileRejectsS3(t *testing.T) {
	t.Setenv("BLOB_PROVIDER", "s3")
	_, err := NewFromEnv(context.Background(), "fallback-bucket")
	if !errors.Is(err, ErrS3NotCompiled) {
		t.Fatalf("s3 selection on minprofile: %v", err)
	}
}
