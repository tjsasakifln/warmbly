package main

import (
	"os"
	"strings"
	"testing"
)

// TestConfengeReplyPathWiresCRM guards the production email handoff path:
// ProcessIncomingReply runs in the consumer and calls OnClassifiedReply →
// ProcessInboundHandoff → applyReplyCRM. Without WireCRM, CRM tasks/deals
// never create on real inbound email.
func TestConfengeReplyPathWiresCRM(t *testing.T) {
	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	newIdx := strings.Index(s, "confenge.NewService")
	wireIntel := strings.Index(s, "WireIntel")
	wireCRM := strings.Index(s, "WireCRM")
	wireReply := strings.Index(s, "WireConfengeReply")
	if newIdx < 0 {
		t.Fatal("consumer must construct confenge.Service when enabled")
	}
	if wireCRM < 0 {
		t.Fatal("consumer must WireCRM on confenge so email reply handoff creates CRM tasks/deals")
	}
	if wireIntel < 0 || !(newIdx < wireIntel && wireIntel < wireReply) {
		t.Fatalf("consumer must WireIntel(Postgres) before reply/DSN handling; new=%d intel=%d reply=%d", newIdx, wireIntel, wireReply)
	}
	if wireReply < 0 {
		t.Fatal("consumer must WireConfengeReply for email handoff")
	}
	if !(newIdx < wireCRM && wireCRM < wireReply) {
		t.Fatalf("WireCRM must sit between confenge.NewService (at %d) and WireConfengeReply (at %d); WireCRM at %d", newIdx, wireReply, wireCRM)
	}
	// Reject the old "CRM not wired on consumer" posture if reintroduced near the wire site.
	snippet := s
	if wireReply > 0 && wireReply+200 < len(s) {
		snippet = s[newIdx : wireReply+80]
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
