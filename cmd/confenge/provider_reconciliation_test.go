package main

import "testing"

func TestProviderAcceptedMailboxProviderDefaultsAfterTextScan(t *testing.T) {
	if got := providerAcceptedMailboxProvider(nil); got != "smtp" {
		t.Fatalf("nil provider = %q, want smtp", got)
	}
	empty := "  "
	if got := providerAcceptedMailboxProvider(&empty); got != "smtp" {
		t.Fatalf("empty provider = %q, want smtp", got)
	}
	smtpIMAP := " smtp_imap "
	if got := providerAcceptedMailboxProvider(&smtpIMAP); got != "smtp_imap" {
		t.Fatalf("provider = %q, want smtp_imap", got)
	}
}
