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
	if !strings.Contains(s, "NOT IN ($5,$6)") {
		t.Fatal("v1 remains valid; v2 is additive")
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
