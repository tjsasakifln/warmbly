//go:build minprofile

package kms

import (
	"context"
	"errors"
	"testing"
)

func TestFromEnv_MinProfileRejectsAWS(t *testing.T) {
	t.Setenv("KMS_PROVIDER", "aws")
	_, err := FromEnv(context.Background(), "alias/fallback")
	if !errors.Is(err, ErrAWSNotCompiled) {
		t.Fatalf("aws selection on minprofile: %v", err)
	}
}

func TestFromEnv_MinProfileRejectsAWSKMSAlias(t *testing.T) {
	t.Setenv("KMS_PROVIDER", "aws-kms")
	_, err := FromEnv(context.Background(), "alias/fallback")
	if !errors.Is(err, ErrAWSNotCompiled) {
		t.Fatalf("aws-kms selection on minprofile: %v", err)
	}
}
