package emailverify

import (
	"context"
	"net/textproto"
	"strings"
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
	if res.Code != ReasonIdentityNotConfigured {
		t.Fatalf("want code %s, got %q", ReasonIdentityNotConfigured, res.Code)
	}
	if !strings.HasPrefix(res.Reason, string(ReasonIdentityNotConfigured)+": ") {
		t.Fatalf("persisted reason must carry its code as a prefix, got %q", res.Reason)
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
		out, code, reason := classifyRcpt(&textproto.Error{Code: 504, Msg: msg})
		if out != probeUnknown {
			t.Fatalf("identity refusal must be UNKNOWN, got %v for %q", out, msg)
		}
		if reason == "" {
			t.Fatalf("identity refusal must carry a reason for %q", msg)
		}
		if !code.IsIdentity() {
			t.Fatalf("identity refusal must carry an identity code, got %q for %q", code, msg)
		}
	}
	// A genuine recipient rejection stays a hard rejection.
	out, code, _ := classifyRcpt(&textproto.Error{Code: 550, Msg: "5.1.1 User unknown in virtual mailbox table"})
	if out != probeRejected {
		t.Fatalf("real unknown-user rejection must stay REJECTED, got %v", out)
	}
	if code != "" {
		t.Fatalf("recipient rejection must carry no identity code, got %q", code)
	}
}

// TestIdentityDimensionsAreDistinguishable is the point of the reason codes:
// three refusals with three different fixes must not collapse into one string.
func TestIdentityDimensionsAreDistinguishable(t *testing.T) {
	cases := []struct {
		msg  string
		want ReasonCode
	}{
		{`5.5.2 <localhost>: Helo command rejected: need fully-qualified hostname`, ReasonIdentityHeloRefused},
		{`Access denied - Invalid HELO name (See RFC2821 4.1.1.1)`, ReasonIdentityHeloRefused},
		{`504 5.5.2 EHLO requires a fully qualified domain name`, ReasonIdentityHeloRefused},
		{`Client host rejected: cannot find your reverse hostname`, ReasonIdentityRDNSMissing},
		{`5.7.25 Reverse DNS lookup for your IP failed`, ReasonIdentityRDNSMissing},
		{`no PTR record for the connecting address`, ReasonIdentityRDNSMissing},
		{`5.7.1 Sender address rejected: Domain not found`, ReasonIdentitySenderRefused},
		{`MAIL FROM command rejected: unverified address`, ReasonIdentitySenderRefused},
		{`5.1.1 User unknown in virtual mailbox table`, ""},
		{`5.2.2 Mailbox full`, ""},
	}
	for _, tc := range cases {
		if got := identityRejection(tc.msg); got != tc.want {
			t.Fatalf("identityRejection(%q) = %q, want %q", tc.msg, got, tc.want)
		}
	}
}

// TestAbsentRDNSIsNotMaskedAsHelo guards the ordering in identityRejection: a
// server that names BOTH our reverse hostname and the HELO command is telling
// us the PTR is missing. Reporting that as a HELO problem sends the operator to
// edit a hostname that is already correct.
func TestAbsentRDNSIsNotMaskedAsHelo(t *testing.T) {
	msg := `450 4.7.1 <unknown[203.0.113.7]>: Helo command rejected: cannot find your reverse hostname`
	if got := identityRejection(msg); got != ReasonIdentityRDNSMissing {
		t.Fatalf("mixed rDNS/HELO refusal must report rDNS, got %q", got)
	}
	out, code, reason := classifyRcpt(&textproto.Error{Code: 550, Msg: msg})
	if out != probeUnknown || code != ReasonIdentityRDNSMissing {
		t.Fatalf("want UNKNOWN/%s, got %v/%q", ReasonIdentityRDNSMissing, out, code)
	}
	if !strings.HasPrefix(reason, string(ReasonIdentityRDNSMissing)+": ") {
		t.Fatalf("stored reason must be code-prefixed, got %q", reason)
	}
}

// TestAccessDeniedAloneIsNotIdentity pins the narrowing of the marker list. A
// bare "access denied" is a policy verdict about this delivery (Exchange
// returns it for genuine recipient/anti-spam blocks); only the variant that
// actually names our HELO is an identity refusal.
func TestAccessDeniedAloneIsNotIdentity(t *testing.T) {
	if got := identityRejection("5.7.1 Access denied"); got != "" {
		t.Fatalf("bare access denied must not be treated as an identity refusal, got %q", got)
	}
	if out, _, _ := classifyRcpt(&textproto.Error{Code: 550, Msg: "5.7.1 Access denied"}); out != probeRejected {
		t.Fatalf("bare access denied must stay a recipient verdict, got %v", out)
	}
	if got := identityRejection("Access denied - Invalid HELO name"); got != ReasonIdentityHeloRefused {
		t.Fatalf("the production message must still be caught, got %q", got)
	}
}

// TestTransientKeepsDimensionWithoutRejecting: a 4xx tarpit that names our PTR
// is still UNKNOWN, but must not lose which dimension the server complained
// about.
func TestTransientKeepsDimensionWithoutRejecting(t *testing.T) {
	out, code, _ := classifyRcpt(&textproto.Error{Code: 450, Msg: "4.7.1 cannot find your reverse hostname"})
	if out != probeUnknown {
		t.Fatalf("4xx must stay UNKNOWN, got %v", out)
	}
	if code != ReasonIdentityRDNSMissing {
		t.Fatalf("want %s, got %q", ReasonIdentityRDNSMissing, code)
	}
}
