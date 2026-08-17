//go:build !minprofile

package kms

import (
	"context"
	"testing"
)

func TestFromEnv_AWSFallsBackToProvidedKeyID(t *testing.T) {
	t.Setenv("KMS_PROVIDER", "aws")
	t.Setenv("KMS_AWS_KEY_ID", "")
	p, err := FromEnv(context.Background(), "alias/fallback")
	if err != nil {
		t.Fatalf("aws provider with fallback key should construct: %v", err)
	}
	if p.Name() != "aws-kms" {
		t.Fatalf("expected aws-kms, got %q", p.Name())
	}
}

func TestFromEnv_DefaultsToAWS(t *testing.T) {
	t.Setenv("KMS_PROVIDER", "")
	p, err := FromEnv(context.Background(), "alias/x")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "aws-kms" {
		t.Fatalf("unset KMS_PROVIDER should default to aws-kms, got %q", p.Name())
	}
}
