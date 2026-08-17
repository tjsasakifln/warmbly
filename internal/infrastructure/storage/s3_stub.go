//go:build minprofile

package storage

import (
	"context"
	"errors"
)

// ErrS3NotCompiled is returned when BLOB_PROVIDER=s3 on a min-profile build.
var ErrS3NotCompiled = errors.New("storage: s3 backend not compiled in; rebuild without -tags minprofile (or use BLOB_PROVIDER=filesystem)")

func newS3(_ context.Context, _, _ string) (Store, error) {
	return nil, ErrS3NotCompiled
}
