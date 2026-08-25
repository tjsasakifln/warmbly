package emailverify

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeMTA is a minimal in-process SMTP server. It is NOT a real MTA: it speaks
// just enough of RFC 5321 for smtp.Client to complete EHLO / MAIL / RCPT, and
// it records every command line it received so a test can assert what we put on
// the wire. It never accepts mail (no DATA), so nothing can be sent.
type fakeMTA struct {
	ln net.Listener

	mu    sync.Mutex
	lines []string
	// rcptReplies are consumed in order, one per RCPT TO. Anything past the end
	// of the slice gets the last entry.
	rcptReplies []string
}

func newFakeMTA(t *testing.T, rcptReplies ...string) *fakeMTA {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeMTA{ln: ln, rcptReplies: rcptReplies}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, aerr := ln.Accept()
		if aerr != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		f.serve(conn)
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	})
	return f
}

func (f *fakeMTA) serve(conn net.Conn) {
	w := bufio.NewWriter(conn)
	r := bufio.NewReader(conn)
	write := func(s string) bool {
		if _, err := w.WriteString(s + "\r\n"); err != nil {
			return false
		}
		return w.Flush() == nil
	}
	if !write("220 mta.test ESMTP fake") {
		return
	}
	rcpts := 0
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		f.mu.Lock()
		f.lines = append(f.lines, line)
		f.mu.Unlock()

		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"):
			// Deliberately advertise no STARTTLS and no AUTH: the prober must
			// never negotiate either, and a missing extension proves it.
			if !write("250-mta.test") || !write("250 SIZE 10240000") {
				return
			}
		case strings.HasPrefix(upper, "HELO"):
			if !write("250 mta.test") {
				return
			}
		case strings.HasPrefix(upper, "MAIL FROM"):
			if !write("250 2.1.0 Ok") {
				return
			}
		case strings.HasPrefix(upper, "RCPT TO"):
			reply := "250 2.1.5 Ok"
			if len(f.rcptReplies) > 0 {
				idx := rcpts
				if idx >= len(f.rcptReplies) {
					idx = len(f.rcptReplies) - 1
				}
				reply = f.rcptReplies[idx]
			}
			rcpts++
			if !write(reply) {
				return
			}
		case strings.HasPrefix(upper, "QUIT"):
			_ = write("221 2.0.0 Bye")
			return
		default:
			if !write("502 5.5.2 Command not implemented") {
				return
			}
		}
	}
}

func (f *fakeMTA) received() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.lines...)
}

func (f *fakeMTA) port() string {
	_, p, _ := net.SplitHostPort(f.ln.Addr().String())
	return p
}

// verifierFor points a real SMTPVerifier at the fake MTA with DNS disabled. The
// lookupHosts seam is the whole reason this test can exist without touching the
// network: nothing here resolves MX, and nothing dials :25.
func verifierFor(t *testing.T, f *fakeMTA, cfg Config) *SMTPVerifier {
	t.Helper()
	v := New(cfg)
	v.smtpPort = f.port()
	v.lookupHosts = func(_ context.Context, _ string) ([]string, error) {
		return []string{"127.0.0.1"}, nil
	}
	return v
}

func findLine(lines []string, prefix string) string {
	for _, l := range lines {
		if strings.HasPrefix(strings.ToUpper(l), prefix) {
			return l
		}
	}
	return ""
}

// TestProbeAnnouncesConfiguredIdentityOnTheWire is the proof the whole fix
// hangs on: whatever EMAIL_VERIFY_HELO_HOST / EMAIL_VERIFY_MAIL_FROM are set
// to is exactly what leaves the socket. Every value below is an example.* /
// .invalid placeholder — no real mailbox, domain, or credential appears.
func TestProbeAnnouncesConfiguredIdentityOnTheWire(t *testing.T) {
	const (
		helo   = "verify.example.org"
		sender = "probe@verify.example.org"
	)
	// Second RCPT (the random catch-all control) is refused, so the real
	// address comes back VALID rather than RISKY.
	f := newFakeMTA(t, "250 2.1.5 Ok", "550 5.1.1 User unknown")
	v := verifierFor(t, f, Config{HeloHost: helo, MailFrom: sender})

	res := v.Verify(context.Background(), "someone@example.com")
	if res.Status != StatusValid {
		t.Fatalf("accepted recipient on a non-catch-all domain must be VALID, got %s (%s)", res.Status, res.Reason)
	}

	lines := f.received()
	if got := findLine(lines, "EHLO"); got != "EHLO "+helo {
		t.Fatalf("wire EHLO = %q, want %q (lines: %v)", got, "EHLO "+helo, lines)
	}
	if got := findLine(lines, "MAIL FROM"); got != "MAIL FROM:<"+sender+">" {
		t.Fatalf("wire MAIL FROM = %q, want %q (lines: %v)", got, "MAIL FROM:<"+sender+">", lines)
	}
	// The "localhost" that produced the contaminated production verdicts must
	// appear nowhere on the wire.
	for _, l := range lines {
		if strings.Contains(strings.ToLower(l), "localhost") {
			t.Fatalf("prober announced localhost on the wire: %q", l)
		}
	}
}

// TestDerivedSenderStillMatchesConfiguredHelo: when only HELO is configured the
// derived MAIL FROM must stay on the configured FQDN, never on a fallback host.
func TestDerivedSenderStillMatchesConfiguredHelo(t *testing.T) {
	const helo = "mx-probe.example.net"
	f := newFakeMTA(t, "250 2.1.5 Ok", "550 5.1.1 User unknown")
	v := verifierFor(t, f, Config{HeloHost: helo})

	if res := v.Verify(context.Background(), "someone@example.com"); res.Status != StatusValid {
		t.Fatalf("want VALID, got %s (%s)", res.Status, res.Reason)
	}
	if got := findLine(f.received(), "MAIL FROM"); got != "MAIL FROM:<verify@"+helo+">" {
		t.Fatalf("derived sender = %q, want verify@%s", got, helo)
	}
}

// TestCatchAllDomainIsRisky: both RCPTs accepted means acceptance proves
// nothing. Kept here because the fake MTA is the only place the control-probe
// path is exercised end to end.
func TestCatchAllDomainIsRisky(t *testing.T) {
	f := newFakeMTA(t, "250 2.1.5 Ok")
	v := verifierFor(t, f, Config{HeloHost: "verify.example.org"})

	res := v.Verify(context.Background(), "someone@example.com")
	if res.Status != StatusRisky || !res.IsCatchAll {
		t.Fatalf("catch-all domain must be RISKY, got %s catchAll=%v", res.Status, res.IsCatchAll)
	}
}

// TestWireIdentityRefusalNeverMarksRecipientInvalid closes the loop end to end:
// a server that refuses the RCPT while naming our own reverse DNS must leave
// the address UNKNOWN and carry the rDNS code — the exact production shape that
// previously produced a false INVALID.
func TestWireIdentityRefusalNeverMarksRecipientInvalid(t *testing.T) {
	f := newFakeMTA(t, "550 5.7.1 Client host rejected: cannot find your reverse hostname")
	v := verifierFor(t, f, Config{HeloHost: "verify.example.org"})

	res := v.Verify(context.Background(), "someone@example.com")
	if res.Status != StatusUnknown {
		t.Fatalf("identity refusal must be UNKNOWN, got %s (%s)", res.Status, res.Reason)
	}
	if res.Code != ReasonIdentityRDNSMissing {
		t.Fatalf("want %s, got %q (%s)", ReasonIdentityRDNSMissing, res.Code, res.Reason)
	}
}

// TestVerifyNeverDialsWithoutIdentity: the unconfigured guard must short-circuit
// before any TCP connection, not merely reinterpret the reply.
func TestVerifyNeverDialsWithoutIdentity(t *testing.T) {
	f := newFakeMTA(t, "250 2.1.5 Ok")
	v := verifierFor(t, f, Config{})

	res := v.Verify(context.Background(), "someone@example.com")
	if res.Status != StatusUnknown || res.Code != ReasonIdentityNotConfigured {
		t.Fatalf("want UNKNOWN/%s, got %s/%q", ReasonIdentityNotConfigured, res.Status, res.Code)
	}
	if lines := f.received(); len(lines) != 0 {
		t.Fatalf("no SMTP command may reach the wire without an identity, got %v", lines)
	}
}
