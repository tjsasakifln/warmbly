package kms

import (
	"context"
	"fmt"
	"os"
)

// FromEnv constructs the active KMS provider from environment variables.
//
//	KMS_PROVIDER=aws    -> newAWS (KMS_AWS_KEY_ID or fallbackAWSKeyID)
//	KMS_PROVIDER=local  -> NewLocalFromEnv
//	(unset)             -> defaults to "aws" for backwards compatibility
//
// The fallbackAWSKeyID is used when KMS_AWS_KEY_ID is empty — this
// preserves the historical "alias/master-key[-dev]" default used at boot.
// AWS config is loaded inside the hosted AWS implementation so the
// min-profile build never imports the AWS SDK.
func FromEnv(ctx context.Context, fallbackAWSKeyID string) (Provider, error) {
	provider := os.Getenv("KMS_PROVIDER")
	if provider == "" {
		provider = "aws"
	}
	switch provider {
	case "aws", "aws-kms":
		keyID := os.Getenv("KMS_AWS_KEY_ID")
		if keyID == "" {
			keyID = fallbackAWSKeyID
		}
		if keyID == "" {
			return nil, fmt.Errorf("kms: aws provider requires KMS_AWS_KEY_ID or fallback key id")
		}
		return newAWS(ctx, keyID)
	case "local":
		return NewLocalFromEnv()
	default:
		return nil, fmt.Errorf("kms: unknown KMS_PROVIDER %q (want: aws, local)", provider)
	}
}
