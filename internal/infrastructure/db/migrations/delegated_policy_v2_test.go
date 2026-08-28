package migrations

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDelegatedBatchConstraintAllowsFirstTouchV2(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("000137_confenge_delegated_policy_v2.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "CFG-FIRST-TOUCH-ROUTING-v1") || !strings.Contains(s, "CFG-FIRST-TOUCH-ROUTING-v2") {
		t.Fatal("v2 must be additive to v1; do not drop the original policy id")
	}
	if !strings.Contains(s, "confenge_delegated_batch_policy_v1") || !strings.Contains(s, "confenge_delegated_decision_policy_v1") {
		t.Fatal("both batch and decision policy checks must accept v2")
	}
}
