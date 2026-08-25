package confenge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStaleReviewBacklogMigrationCannotCreateSendAuthority(t *testing.T) {
	path := filepath.Join("..", "..", "infrastructure", "db", "migrations", "000124_confenge_stale_review_backlog.up.sql")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(string(raw))
	for _, required := range []string{
		"t.state='needs_review'", "c.last_import_run_id is distinct from a.last_import_run_id",
		"status='blocked'", "state='cancelled'", "recipient_not_in_current_snapshot",
		"queue_state='ready_to_generate'", "c.last_import_run_id=a.last_import_run_id",
		"target_fit_class='target_confirmed'", "c.do_not_contact=false", "c.bounced=false",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("stale review migration missing guard %q", required)
		}
	}
	for _, forbidden := range []string{"state='approved'", "state='queued'", "status='queued'", "insert into confenge_dispatch", "insert into confenge_cohort_candidate_dispatches"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("stale review migration contains authority mutation %q", forbidden)
		}
	}
}
