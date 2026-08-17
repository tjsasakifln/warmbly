//go:build !minprofile

package config

import (
	"context"
	"fmt"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/warmbly/warmbly/internal/infrastructure/secrets"
	"github.com/warmbly/warmbly/internal/infrastructure/ssm"
)

func (c *Config) initAWS(ctx context.Context) error {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to load AWS config: %w", err)
	}

	params, err := ssm.NewSSMParameterStore(ctx, awsCfg)
	if err != nil {
		return fmt.Errorf("failed to create SSM client: %w", err)
	}
	c.params = params

	secretsClient, err := secrets.NewSecretsManagerClient(ctx, awsCfg)
	if err != nil {
		return fmt.Errorf("failed to create Secrets Manager client: %w", err)
	}
	c.secrets = secretsClient
	return nil
}

// Load creates a Config with pre-initialized AWS clients (legacy compatibility).
// Deprecated: Use NewConfig for new code.
func Load(params *ssm.SSMParameterStore, secretsClient *secrets.SecretsManagerClient) *Config {
	env := getEnvOrDefault("APP_ENV", "dev")

	return &Config{
		Env:              env,
		AWSConfigEnabled: true, // Legacy mode always has AWS enabled
		params:           params,
		secrets:          secretsClient,
	}
}
