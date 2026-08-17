//go:build minprofile

package config

import (
	"context"
	"errors"
	"testing"
)

func TestNewConfig_MinProfileRejectsAWSConfig(t *testing.T) {
	t.Setenv("AWS_CONFIG_ENABLED", "true")
	_, err := NewConfig(context.Background())
	if !errors.Is(err, ErrAWSNotCompiled) {
		t.Fatalf("AWS_CONFIG_ENABLED on minprofile: %v", err)
	}
}

func TestNewConfig_MinProfileAllowsEnvOnly(t *testing.T) {
	t.Setenv("AWS_CONFIG_ENABLED", "false")
	cfg, err := NewConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AWSConfigEnabled {
		t.Fatal("env-only config must not enable AWS")
	}
}
