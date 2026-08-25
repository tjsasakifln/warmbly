package confenge

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
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

func TestHumanGateLateSuppressionOptOutBounceRemovalAndRecipientDriftInvalidate(t *testing.T) {
	accountID, candidateID := uuid.New(), uuid.New()
	member := FrozenCohortMember{AccountID: accountID, CandidateID: candidateID, Mailbox: "review@fixture.invalid"}
	live := CohortAccountInput{
		Account: models.OutreachAccount{ID: accountID, Blocked: true, DoNotContact: true},
		Candidates: []models.OutreachContactCandidate{{
			ID: candidateID, Email: "changed@fixture.invalid", Blocked: true, DoNotContact: true, Bounced: true,
		}},
	}
	reasons := humanGateLiveInvalidations(member, live, true)
	for _, want := range []string{
		"late_account_suppression", "late_account_opt_out", "late_recipient_suppression",
		"late_recipient_opt_out", "late_hard_bounce", "recipient_drift",
	} {
		if !slices.Contains(reasons, want) {
			t.Fatalf("missing %s in %v", want, reasons)
		}
	}
	if got := humanGateLiveInvalidations(member, CohortAccountInput{Account: models.OutreachAccount{ID: accountID}}, true); !slices.Contains(got, "candidate_removed") {
		t.Fatalf("removed candidate must fail closed: %v", got)
	}
	if got := humanGateLiveInvalidations(member, CohortAccountInput{}, false); !slices.Contains(got, "live_candidate_state_unknown") {
		t.Fatalf("unreadable live state must fail closed: %v", got)
	}
}

func TestHumanGateGOBlockersCoverEmptyStaleValidationAndLateState(t *testing.T) {
	candidateID := uuid.New()
	approved := &HumanGateReview{Decision: "APPROVE", Effective: true}
	for _, status := range []string{"RISKY", "INVALID", "UNKNOWN", "STALE"} {
		cohort := &HumanGateCohort{Freshness: "FRESH", Manifest: currentComposerManifest(), Candidates: []HumanGateCandidate{{
			FrozenCohortMember: FrozenCohortMember{CandidateID: candidateID, ComposerVersion: ComposerVersion},
			Validation:         &HumanGateValidation{Status: status},
			Review:             approved,
		}}}
		if got := humanGateDecisionBlockers(cohort); len(got) != 1 || got[0] != "candidate_not_approved:"+candidateID.String() {
			t.Fatalf("%s must block GO: %v", status, got)
		}
	}
	if got := humanGateDecisionBlockers(&HumanGateCohort{Freshness: "STALE"}); !slices.Contains(got, "cohort_empty") || !slices.Contains(got, "source_evidence_stale") {
		t.Fatalf("empty stale cohort must report both blockers: %v", got)
	}
	validButSuppressed := &HumanGateCohort{Freshness: "FRESH", Manifest: currentComposerManifest(), Candidates: []HumanGateCandidate{{
		FrozenCohortMember: FrozenCohortMember{CandidateID: candidateID, ComposerVersion: ComposerVersion},
		Validation:         &HumanGateValidation{Status: "VALID"},
		Review:             approved,
		BlockedBy:          []string{"late_recipient_suppression"},
	}}}
	if got := humanGateDecisionBlockers(validButSuppressed); len(got) != 1 {
		t.Fatalf("late suppression must block otherwise-valid GO: %v", got)
	}
}

func TestHumanGateApprovalAcknowledgementIsServerEnforced(t *testing.T) {
	svc := &service{}
	actor := uuid.New()
	if _, got := svc.ReviewHumanGateCandidate(t.Context(), uuid.New(), actor, uuid.New(), uuid.New(), HumanGateReviewInput{
		Decision: "APPROVE", Reason: "fixture", IdempotencyKey: "fixture-review-key",
	}); got == nil || got.Identifier != "approval_acknowledgement_required" {
		t.Fatalf("APPROVE without acknowledgement must fail before storage, got %#v", got)
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

// currentComposerManifest is a manifest stamped by the composer this build
// ships, so a test about some other blocker is not refused for copy age.
func currentComposerManifest() FrozenCohortSnapshot {
	return FrozenCohortSnapshot{ComposerVersion: ComposerVersion, PolicyVersion: BoundedCohortPolicyV1}
}
