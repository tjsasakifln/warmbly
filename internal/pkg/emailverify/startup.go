package emailverify

import (
	"fmt"
	"net/mail"
	"strings"
)

// Environment variables that carry the prober's identity. Named here so the
// error text an operator reads matches the key they have to set.
const (
	EnvHeloHost = "EMAIL_VERIFY_HELO_HOST"
	EnvMailFrom = "EMAIL_VERIFY_MAIL_FROM"
)

// IsProductionEnv reports a production-like APP_ENV, matching the convention
// the rest of the platform uses ("prod" or "production", case-insensitive).
func IsProductionEnv(appEnv string) bool {
	e := strings.ToLower(strings.TrimSpace(appEnv))
	return e == "prod" || e == "production"
}

// ValidateStartup fails the process closed when a production outreach plane is
// configured to verify addresses with an unusable identity.
//
// Why this exists as a *boot* gate and not only a per-probe guard: Verify()
// already degrades to StatusUnknown when the identity is unusable, which is the
// right per-address behaviour but is silent. In production that silence is the
// whole defect — a backend deployed without EMAIL_VERIFY_HELO_HOST announced
// "localhost", collected 5xx replies that named *our* HELO, and those verdicts
// were stored as if the RECIPIENT were the problem. Nobody notices a column
// full of "unknown" either; they notice a container that will not start.
//
// Scope is deliberately narrow. Outside production, or with outreach disabled,
// the old behaviour stands (UNKNOWN per probe) so a dev stack, a test, and a
// self-hosted install with no verifier configured all still boot.
//
// This can never make an address INVALID: it runs before any probe and its only
// outcome is a refusal to start.
func (c Config) ValidateStartup(appEnv string, outreachEnabled bool) error {
	if from := strings.TrimSpace(c.MailFrom); from != "" {
		if _, err := mail.ParseAddress(from); err != nil {
			return fmt.Errorf("%s is not a valid email address: %w", EnvMailFrom, err)
		}
	}
	if !IsProductionEnv(appEnv) || !outreachEnabled {
		return nil
	}
	host := strings.TrimSpace(c.HeloHost)
	if host == "" {
		return fmt.Errorf(
			"%s is required when APP_ENV=%s and CONFENGE_OUTREACH_ENABLED=true: "+
				"without it the SMTP prober announces no usable EHLO name, remote servers "+
				"refuse the connection, and their refusal is recorded against the recipient",
			EnvHeloHost, strings.TrimSpace(appEnv))
	}
	if !usableHeloHost(host) {
		// Do not echo the value: it is operator-supplied config that lands in
		// logs. Naming the key and the rule is enough to fix it.
		return fmt.Errorf(
			"%s must be a fully-qualified hostname (RFC 5321 4.1.1.1) when APP_ENV=%s and "+
				"CONFENGE_OUTREACH_ENABLED=true; %q and any dotless or whitespace-bearing name "+
				"are refused by remote MTAs",
			EnvHeloHost, strings.TrimSpace(appEnv), "localhost")
	}
	return nil
}
