//go:build minprofile

package config

import (
	"context"
	"errors"
)

// ErrAWSNotCompiled is returned when AWS_CONFIG_ENABLED=true on a min-profile build.
var ErrAWSNotCompiled = errors.New("config: AWS config backend not compiled in; rebuild without -tags minprofile (or set AWS_CONFIG_ENABLED=false)")

func (c *Config) initAWS(_ context.Context) error {
	return ErrAWSNotCompiled
}
