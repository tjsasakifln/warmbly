//go:build minprofile

package kms

import (
	"context"
	"errors"
)

// ErrAWSNotCompiled is returned when KMS_PROVIDER=aws on a min-profile build.
var ErrAWSNotCompiled = errors.New("kms: aws backend not compiled in; rebuild without -tags minprofile (or use KMS_PROVIDER=local)")

func newAWS(_ context.Context, _ string) (Provider, error) {
	return nil, ErrAWSNotCompiled
}
