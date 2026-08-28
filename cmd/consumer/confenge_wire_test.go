package main

import (
	"os"
	"strings"
	"testing"
)

// TestConfengeReplyPathWiresCRM keeps one reconciler for each IMAP reply.
func TestConfengeReplyPathWiresCRM(t *testing.T) {
	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	newIdx := strings.Index(s, "confenge.NewService")
	wireIntel := strings.Index(s, "WireIntel")
	wireCRM := strings.Index(s, "WireCRM")
	if newIdx < 0 {
		t.Fatal("consumer must construct confenge.Service when enabled")
	}
	if wireCRM < 0 {
		t.Fatal("consumer must WireCRM on confenge so email reply handoff creates CRM tasks/deals")
	}
	if wireIntel < 0 || !(newIdx < wireIntel && wireIntel < wireCRM) {
		t.Fatalf("consumer must WireIntel(Postgres) before reply/DSN handling; new=%d intel=%d crm=%d", newIdx, wireIntel, wireCRM)
	}
	if !(newIdx < wireCRM) {
		t.Fatalf("WireCRM must follow confenge.NewService (at %d); WireCRM at %d", newIdx, wireCRM)
	}
	if strings.Contains(s, "advancedService.WireConfengeReply") {
		t.Fatal("consumer must not wire a second CONFENGE reply reconciler")
	}
	// Reject the old "CRM not wired on consumer" posture if reintroduced near the wire site.
	snippet := s
	if wireCRM > 0 && wireCRM+200 < len(s) {
		snippet = s[newIdx : wireCRM+80]
	}
	if strings.Contains(snippet, "CRM not wired on consumer") {
		t.Fatal("stale comment: CRM must be wired on consumer for reply handoff")
	}
	if !strings.Contains(s, "WireCohortAuth") {
		t.Fatal("consumer must WireCohortAuth so bounded cohort grants exist at transport")
	}
	if !strings.Contains(s, "NewPostgresCohortStore") {
		t.Fatal("consumer production boot must construct NewPostgresCohortStore")
	}
	if strings.Contains(s, "NewMemoryCohortStore") {
		t.Fatal("consumer production boot must not use NewMemoryCohortStore as cohort authority")
	}
}

// The consumer observes the provider result, so it is the only process that can
// commit the dispatch reservation. Without the governor wired, every accepted
// CONFENGE send failed with "dispatch governor unavailable for provider
// attempt" and confenge_dispatch_sends could never be written.
func TestConsumerWiresConfengeDispatchGovernor(t *testing.T) {
	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "confengeSvc.WireDispatch(primaryDB.Pool)") {
		t.Fatal("the consumer must wire the CONFENGE dispatch governor to commit provider-accepted sends")
	}
	if !strings.Contains(string(b), "confengeSvc.WireDelegatedFirstTouch(primaryDB.Pool)") {
		t.Fatal("the consumer must wire delegated first-touch state so provider-accepted sends close QUEUED decisions")
	}
}
