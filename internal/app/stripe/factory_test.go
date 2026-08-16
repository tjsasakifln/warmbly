package stripe

import (
	"testing"
)

func TestNewFromEnv_DisabledWhenNotStripe(t *testing.T) {
	t.Setenv("BILLING_PROVIDER", "none")
	svc, err := NewFromEnv(nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := svc.(*disabledService); !ok {
		t.Fatalf("expected disabledService, got %T", svc)
	}
}
