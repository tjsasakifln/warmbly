package confenge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestControlledReviewRecoveryMigrationKeepsTerminalSuppressions(t *testing.T) {
	path := filepath.Join("..", "..", "infrastructure", "db", "migrations", "000123_confenge_controlled_review_route_recovery.up.sql")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(string(raw))
	required := []string{
		"last_snapshot_hash", "last_run_id", "ir.status = 'completed'",
		"c.block_reason in ('published_exhausted', 'provenance_chain_invalid')",
		"c.do_not_contact = false", "c.bounced = false",
		`'{"controlled_email_eligible":true}'`, "mailbox_company_evidence",
		"channel_epistemic_class", "route_freshness", "route_suppression",
		"ownership_status", "verification_status", "fixture", "synthetic",
	}
	for _, guard := range required {
		if !strings.Contains(sql, guard) {
			t.Fatalf("recovery migration missing guard %q", guard)
		}
	}
	for _, forbidden := range []string{"approved_by", "approved_at", "queue_state = 'approved'", "outreach_dispatch_queue"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("preparation migration contains authority mutation %q", forbidden)
		}
	}
}
