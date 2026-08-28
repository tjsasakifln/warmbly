package confenge

import (
	"os"
	"strings"
	"testing"
)

func TestRetireStaleAcceptsFirstTouchV2(t *testing.T) {
	b, err := os.ReadFile("delegated_first_touch_refresh.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "DelegatedFirstTouchPolicyV2") || !strings.Contains(s, "DelegatedFirstTouchPolicyHashV2") {
		t.Fatal("retireStale must keep v2 approvals; hardcoding v1 cancelled live DELEGATED_POLICY_APPROVE")
	}
	if !strings.Contains(s, "d.policy_version NOT IN (") {
		t.Fatal("v1 remains valid; v2 is additive")
	}
}

func TestRetireStaleIgnoresAcquisitionProvenance(t *testing.T) {
	b, err := os.ReadFile("delegated_first_touch_refresh.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, provenance := range []string{
		"a.source_run_id<>",
		"d.evidence_source_run_id<>",
		"d.source_snapshot_hash<>",
		"d.source_freshness_hash<>",
		"d.source_expires_at IS DISTINCT FROM",
	} {
		if strings.Contains(s, provenance) {
			t.Fatalf("%s is acquisition provenance and must not retire a qualified decision", provenance)
		}
	}
	if !strings.Contains(s, "a.commercial_qualification_state<>'QUALIFIED'") {
		t.Fatal("retirement must gate on the commercial fact, not on which run emitted the row")
	}
	for _, integrity := range []string{"d.runtime_release_sha<>", "d.policy_hash<>", "d.target_membership_hash<>", "d.target_membership_count<>"} {
		if !strings.Contains(s, integrity) {
			t.Fatalf("%s is a content/binding integrity term and must stay", integrity)
		}
	}
}

func TestNextDelegatedCandidateSkipsCancelledMatchingBinding(t *testing.T) {
	b, err := os.ReadFile("delegated_first_touch_worker.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "d.state<>'CANCELLED'") {
		t.Fatal("a CANCELLED v2 approval with the current binding must not be selected again; that stalls the autorun burst")
	}
}
