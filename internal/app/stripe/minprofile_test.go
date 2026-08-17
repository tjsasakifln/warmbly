//go:build minprofile

package stripe

import (
	"errors"
	"testing"

	"github.com/warmbly/warmbly/internal/config"
)

func TestNewFromEnv_MinProfileRejectsStripe(t *testing.T) {
	t.Setenv("BILLING_PROVIDER", "stripe")
	_, err := NewFromEnv(&config.StripeConfig{SecretKey: "sk_test"}, nil, nil, nil, nil)
	if !errors.Is(err, ErrStripeNotCompiled) {
		t.Fatalf("stripe selection on minprofile: %v", err)
	}
}

func TestNewFromEnv_MinProfileAllowsDisabled(t *testing.T) {
	t.Setenv("BILLING_PROVIDER", "none")
	svc, err := NewFromEnv(nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if svc == nil {
		t.Fatal("disabled billing must still construct")
	}
}
