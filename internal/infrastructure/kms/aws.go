//go:build !minprofile

package kms

import (
	"context"

	awsconf "github.com/aws/aws-sdk-go-v2/config"
)

func newAWS(ctx context.Context, keyID string) (Provider, error) {
	awscfg, err := awsconf.LoadDefaultConfig(ctx, awsconf.WithDefaultRegion("us-east-1"))
	if err != nil {
		return nil, err
	}
	return New(ctx, awscfg, keyID)
}
