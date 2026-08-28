package confenge

import (
	"os"
	"strings"
	"testing"
)

// The runway watermark asks how much sending capacity is already committed.
// Scoping that count to the current run, snapshot, release sha and policy
// authorization hid every carried-forward row, so the planner believed the
// runway was empty and scheduled a second full runway on top of it. Production
// reached 40 sends a business day against an intended 20, and would have added
// another runway on every deploy.
func TestRunwaySnapshotCountsAllScheduledWork(t *testing.T) {
	b, err := os.ReadFile("delegated_first_touch_runway.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	start := strings.Index(s, "func (s *service) delegatedFirstTouchQueueSnapshot(")
	if start < 0 {
		t.Fatal("delegatedFirstTouchQueueSnapshot not found")
	}
	end := strings.Index(s[start:], "\nfunc ")
	if end < 0 {
		end = len(s) - start
	}
	body := s[start : start+end]

	for _, identity := range []string{
		"d.evidence_source_run_id=",
		"d.source_snapshot_hash=",
		"d.runtime_release_sha=",
		"d.policy_authorization_id=",
	} {
		if strings.Contains(body, identity) {
			t.Fatalf("%s scopes the capacity count to one identity and hides carried-forward scheduled work", identity)
		}
	}
	// A committed slot is still committed regardless of which run minted it.
	if !strings.Contains(body, "d.state='QUEUED' AND q.status IN ('queued','reserved')") {
		t.Fatal("the capacity count must still measure real queued and reserved work")
	}
}
