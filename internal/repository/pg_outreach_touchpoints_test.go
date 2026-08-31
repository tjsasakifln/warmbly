package repository

import (
	"os"
	"strings"
	"testing"
)

func TestOutreachTouchpointSelectQualifiesJoinedColumns(t *testing.T) {
	for _, column := range []string{
		"id", "organization_id", "account_id", "contact_candidate_id",
		"draft_id", "created_at", "updated_at",
	} {
		if !strings.Contains(outreachTouchpointSelect, "t."+column) {
			t.Fatalf("touchpoint select must qualify %s for joined queries", column)
		}
	}
}

func TestAcceptedInitialQueryRecognizesProviderEvidenceAndTerminalLegacyQueue(t *testing.T) {
	// The send ledger branch is authoritative. The legacy branch is deliberately
	// strict for SENT projections, while attempted/failed queue rows are a
	// conservative no-resend fence rather than provider acceptance.
	source, err := os.ReadFile("pg_outreach.go")
	if err != nil {
		t.Fatal(err)
	}
	query := string(source)
	for _, required := range []string{
		"FROM confenge_dispatch_sends sent",
		"t.state='SENT'", "COALESCE(t.provider_message_id,'') <> ''", "t.sent_at IS NOT NULL",
		"FROM confenge_dispatch_queue q", "q.status IN ('attempted','failed','sent')",
	} {
		if !strings.Contains(query, required) {
			t.Fatalf("accepted-initial query missing %q", required)
		}
	}
}
