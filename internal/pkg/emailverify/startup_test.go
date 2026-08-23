package emailverify

import (
	"strings"
	"testing"
)

// TestProductionOutreachRefusesUnusableIdentity is the boot gate. The
// production defect was a backend that started happily with no
// EMAIL_VERIFY_HELO_HOST and then attributed its own HELO refusals to
// recipients; in a production outreach plane that must be a startup failure.
func TestProductionOutreachRefusesUnusableIdentity(t *testing.T) {
	for _, host := range []string{"", "   ", "localhost", "LocalHost", "backend", "api", "bad host.com", ".x.com", "a..b.com"} {
		cfg := Config{HeloHost: host}
		err := cfg.ValidateStartup("production", true)
		if err == nil {
			t.Fatalf("APP_ENV=production + outreach must refuse HeloHost %q", host)
		}
		if !strings.Contains(err.Error(), EnvHeloHost) {
			t.Fatalf("error must name %s so an operator knows the knob: %v", EnvHeloHost, err)
		}
	}
	if err := (Config{HeloHost: "api.confenge.example"}).ValidateStartup("production", true); err != nil {
		t.Fatalf("a fully-qualified identity must boot in production: %v", err)
	}
	if err := (Config{HeloHost: "verify.example.org"}).ValidateStartup("prod", true); err != nil {
		t.Fatalf("APP_ENV=prod is production-like and must accept a FQDN: %v", err)
	}
}

// TestNonProductionKeepsPerProbeUnknown: dev, test, and self-host installs with
// no verifier configured must still boot. Their per-probe behaviour is UNKNOWN,
// which is already covered by TestVerifyRefusesToProbeWithoutIdentity.
func TestNonProductionKeepsPerProbeUnknown(t *testing.T) {
	for _, env := range []string{"", "dev", "development", "test", "staging"} {
		if err := (Config{}).ValidateStartup(env, true); err != nil {
			t.Fatalf("APP_ENV=%q must not fail boot on an empty identity: %v", env, err)
		}
	}
	// Production with outreach OFF is not an outreach plane; nothing probes.
	if err := (Config{}).ValidateStartup("production", false); err != nil {
		t.Fatalf("outreach-disabled production must still boot: %v", err)
	}
}

// TestValidateStartupNeverLeaksConfiguredIdentity: the error text goes to logs
// and to Sentry. It may name the KEY, never the operator's value.
func TestValidateStartupNeverLeaksConfiguredIdentity(t *testing.T) {
	// Dotless, so it is unusable — and distinctive, so a leak is unmistakable.
	const secretish = "internal-relay-7"
	err := Config{HeloHost: secretish}.ValidateStartup("production", true)
	if err == nil {
		t.Fatalf("%q is not fully qualified and must be refused", secretish)
	}
	if strings.Contains(err.Error(), secretish) {
		t.Fatalf("startup error must not echo the configured hostname: %v", err)
	}
}

func TestValidateStartupRejectsMalformedMailFrom(t *testing.T) {
	// Bad sender syntax is a config error in every environment: it can only ever
	// produce MAIL FROM refusals, never a useful verdict.
	for _, env := range []string{"dev", "production"} {
		err := Config{HeloHost: "verify.example.org", MailFrom: "not an address"}.ValidateStartup(env, true)
		if err == nil {
			t.Fatalf("APP_ENV=%s must reject a malformed %s", env, EnvMailFrom)
		}
		if !strings.Contains(err.Error(), EnvMailFrom) {
			t.Fatalf("error must name %s: %v", EnvMailFrom, err)
		}
	}
	if err := (Config{HeloHost: "verify.example.org", MailFrom: "probe@verify.example.org"}).ValidateStartup("production", true); err != nil {
		t.Fatalf("a well-formed sender must pass: %v", err)
	}
}

func TestIsProductionEnv(t *testing.T) {
	for _, e := range []string{"prod", "production", "PRODUCTION", " Prod "} {
		if !IsProductionEnv(e) {
			t.Fatalf("%q must be production-like", e)
		}
	}
	for _, e := range []string{"", "dev", "staging", "preprod", "production-like"} {
		if IsProductionEnv(e) {
			t.Fatalf("%q must not be production-like", e)
		}
	}
}
