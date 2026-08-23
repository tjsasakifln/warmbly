package emailverify

import (
	"context"
	"net/textproto"
	"testing"
)

func TestUnusableHeloHostNeverProbes(t *testing.T) {
	for _, name := range []string{"", "localhost", "LOCALHOST", "backend", "bad host.com", ".leading", "a..b"} {
		if usableHeloHost(name) {
			t.Fatalf("%q must not be accepted as an EHLO identity", name)
		}
	}
	for _, name := range []string{"verify.warmbly.com", "api.confenge.com.br", "mx1.example.org."} {
		if !usableHeloHost(name) {
			t.Fatalf("%q is a fully-qualified hostname and must be accepted", name)
		}
	}
}

func TestVerifyRefusesToProbeWithoutIdentity(t *testing.T) {
	v := New(Config{})
	if v.cfg.HeloHost != "" {
		t.Fatalf("no fallback identity may be invented, got %q", v.cfg.HeloHost)
	}
	if v.cfg.MailFrom != "" {
		t.Fatalf("MailFrom must stay empty without a usable HeloHost, got %q", v.cfg.MailFrom)
	}
	res := v.Verify(context.Background(), "someone@gmail.com")
	if res.Status != StatusUnknown {
		t.Fatalf("unconfigured verifier must yield UNKNOWN, got %s (%s)", res.Status, res.Reason)
	}
	if res.Reason == "" || res.Status == StatusInvalid {
		t.Fatalf("unconfigured verifier must never mark an address invalid: %+v", res)
	}
}

func TestIdentityRejectionIsNotRecipientRejection(t *testing.T) {
	// Postfix defers a HELO refusal to RCPT with a 5xx that names our identity.
	identity := []string{
		`5.5.2 <localhost>: Helo command rejected: need fully-qualified hostname`,
		`Access denied - Invalid HELO name (See RFC2821 4.1.1.1)`,
		`Client host rejected: cannot find your reverse hostname`,
		`Sender address rejected: Domain not found`,
	}
	for _, msg := range identity {
		out, reason := classifyRcpt(&textproto.Error{Code: 504, Msg: msg})
		if out != probeUnknown {
			t.Fatalf("identity refusal must be UNKNOWN, got %v for %q", out, msg)
		}
		if reason == "" {
			t.Fatalf("identity refusal must carry a reason for %q", msg)
		}
	}
	// A genuine recipient rejection stays a hard rejection.
	out, _ := classifyRcpt(&textproto.Error{Code: 550, Msg: "5.1.1 User unknown in virtual mailbox table"})
	if out != probeRejected {
		t.Fatalf("real unknown-user rejection must stay REJECTED, got %v", out)
	}
}
