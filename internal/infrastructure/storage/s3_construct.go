//go:build !minprofile

package storage

import (
	"context"

	awsconf "github.com/aws/aws-sdk-go-v2/config"
)

func newS3(ctx context.Context, bucket, publicBaseURL string) (Store, error) {
	awscfg, err := awsconf.LoadDefaultConfig(ctx, awsconf.WithDefaultRegion("us-east-1"))
	if err != nil {
		return nil, err
	}
	client, err := NewClient(ctx, awscfg, bucket)
	if err != nil {
		return nil, err
	}
	client.PublicBaseURL = publicBaseURL
	return client, nil
}
