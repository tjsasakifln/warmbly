//go:build !minprofile

package stripe

import (
	"testing"

	"github.com/warmbly/warmbly/internal/config"
)

func TestNewFromEnv_StripeStillConstructsOnHostedBuild(t *testing.T) {
	t.Setenv("BILLING_PROVIDER", "stripe")
	svc, err := NewFromEnv(&config.StripeConfig{SecretKey: "sk_test"}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := svc.(*stripeService); !ok {
		t.Fatalf("hosted stripe provider should construct live service, got %T", svc)
	}
}
