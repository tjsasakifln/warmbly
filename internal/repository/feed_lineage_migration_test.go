package repository

import (
	"os"
	"strings"
	"testing"
)

func TestCommittedFeedLineageMigrationIsReversibleAndBackfillsLastGood(t *testing.T) {
	up, err := os.ReadFile("../infrastructure/db/migrations/000140_confenge_feed_lineage.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../infrastructure/db/migrations/000140_confenge_feed_lineage.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(up)
	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS outreach_feed_committed_runs",
		"last_success_at IS NOT NULL",
		"CREATE OR REPLACE FUNCTION confenge_commercially_qualified",
		"qualified_until > as_of",
		"NOT coalesce(deactivated, false)",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("lineage migration missing %q", required)
		}
	}
	if strings.Contains(text, "ir.started_at <= fs.last_success_at") || strings.Contains(text, "SELECT DISTINCT a.organization_id") {
		t.Fatal("historical runs without an explicit last-good receipt were backfilled")
	}
	for _, cleanup := range []string{
		"UPDATE confenge_dispatch_reservations",
		"UPDATE confenge_dispatch_queue",
		"UPDATE confenge_delegated_first_touch_decisions",
		"UPDATE outreach_drafts",
		"UPDATE outreach_touchpoints",
		"q.status='attempted'",
		"feed_lineage_uncommitted",
	} {
		if !strings.Contains(text, cleanup) {
			t.Fatalf("upgrade cleanup missing %q", cleanup)
		}
	}
	if !strings.Contains(string(down), "DROP TABLE IF EXISTS outreach_feed_committed_runs") ||
		!strings.Contains(string(down), "DROP FUNCTION IF EXISTS confenge_commercially_qualified") {
		t.Fatal("lineage migration is not reversible")
	}
}

func TestInitialBacklogTerminalCleanupUsesProviderLedgerAndClosesLiveDuplicates(t *testing.T) {
	body, err := os.ReadFile("pg_outreach_backlog.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"FROM confenge_dispatch_sends sent",
		"UPDATE confenge_dispatch_reservations",
		"SET state='released'",
		"UPDATE confenge_dispatch_queue",
		"UPDATE confenge_delegated_first_touch_decisions",
		"UPDATE outreach_touchpoints t",
		"initial_already_contacted",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("terminal cleanup missing %q", required)
		}
	}
}
