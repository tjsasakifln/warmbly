package confenge

import (
	"testing"
	"time"

	"github.com/google/uuid"
	verify "github.com/warmbly/warmbly/internal/pkg/emailverify"
)

func TestNormalizeHumanGateValidationStatus(t *testing.T) {
	cases := map[verify.Status]string{verify.StatusValid: "VALID", verify.StatusRisky: "RISKY", verify.StatusInvalid: "INVALID", verify.StatusUnknown: "UNKNOWN"}
	for input, want := range cases {
		if got := normalizeValidationStatus(input); got != want {
			t.Fatalf("%s: got %s want %s", input, got, want)
		}
	}
}

func TestHumanGateApprovalInvalidatesEveryBoundDimension(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	validationID := uuid.New()
	expires := now.Add(time.Hour)
	m := FrozenCohortMember{Mailbox: "ops@company.invalid", ContentHash: "content-v1", EvidenceHash: "evidence-v1"}
	valid := &HumanGateValidation{ID: validationID, Status: "VALID", ExpiresAt: expires}
	base := func() *HumanGateReview { return &HumanGateReview{Decision: "APPROVE", InvalidatedBy: []string{}} }
	tests := []struct {
		name, recipient, content, policy, evidence string
		vid                                        *uuid.UUID
		expiry                                     *time.Time
		validation                                 *HumanGateValidation
		want                                       string
	}{
		{"recipient", HashRecipientSet([]string{"other@company.invalid"}), m.ContentHash, BoundedCohortPolicyV1, m.EvidenceHash, &validationID, &expires, valid, "recipient_drift"},
		{"message", HashRecipientSet([]string{m.Mailbox}), "content-v2", BoundedCohortPolicyV1, m.EvidenceHash, &validationID, &expires, valid, "message_drift"},
		{"policy", HashRecipientSet([]string{m.Mailbox}), m.ContentHash, "policy-v2", m.EvidenceHash, &validationID, &expires, valid, "policy_drift"},
		{"evidence", HashRecipientSet([]string{m.Mailbox}), m.ContentHash, BoundedCohortPolicyV1, "evidence-v2", &validationID, &expires, valid, "evidence_drift"},
		{"stale", HashRecipientSet([]string{m.Mailbox}), m.ContentHash, BoundedCohortPolicyV1, m.EvidenceHash, &validationID, func() *time.Time { x := now; return &x }(), valid, "validation_expired"},
		{"unknown", HashRecipientSet([]string{m.Mailbox}), m.ContentHash, BoundedCohortPolicyV1, m.EvidenceHash, &validationID, &expires, &HumanGateValidation{ID: validationID, Status: "UNKNOWN", ExpiresAt: expires}, "validation_changed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := base()
			evaluateHumanGateReview(r, m, BoundedCohortPolicyV1, tc.recipient, tc.content, tc.policy, tc.evidence, tc.vid, tc.expiry, tc.validation, now)
			if r.Effective {
				t.Fatal("approval must be invalidated")
			}
			found := false
			for _, reason := range r.InvalidatedBy {
				if reason == tc.want {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing %s in %v", tc.want, r.InvalidatedBy)
			}
		})
	}
}

func TestHumanGateValidApprovalRemainsEffectiveAndHoldNeverDoes(t *testing.T) {
	now := time.Now().UTC()
	id := uuid.New()
	expires := now.Add(time.Hour)
	m := FrozenCohortMember{Mailbox: "ops@company.invalid", ContentHash: "c", EvidenceHash: "e"}
	v := &HumanGateValidation{ID: id, Status: "VALID", ExpiresAt: expires}
	for _, decision := range []string{"APPROVE", "HOLD", "REJECT"} {
		r := &HumanGateReview{Decision: decision}
		evaluateHumanGateReview(r, m, BoundedCohortPolicyV1, HashRecipientSet([]string{m.Mailbox}), m.ContentHash, BoundedCohortPolicyV1, m.EvidenceHash, &id, &expires, v, now)
		if r.Effective != (decision == "APPROVE") {
			t.Fatalf("%s effective=%v", decision, r.Effective)
		}
	}
}
