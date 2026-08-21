package confenge

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

type CadenceStepSpec struct {
	Ordinal        int
	Purpose        string
	CadenceStep    string
	DelayAfterPrev time.Duration
}

func CadencePolicyV1() []CadenceStepSpec {
	return []CadenceStepSpec{
		{1, models.TouchpointPurposeInitial, "initial", 0},
		{2, models.TouchpointPurposeFollowUp, "follow_up_1", 3 * 24 * time.Hour},
		{3, models.TouchpointPurposeFollowUp, "follow_up_2", 4 * 24 * time.Hour},
		{4, models.TouchpointPurposeClose, "close", 7 * 24 * time.Hour},
	}
}

func ContentHash(channel, recipient, subject, body, purpose string) string {
	return ContentBindingHash(channel, recipient, subject, body, purpose, nil, "")
}

// ContentBindingHash binds recipient, content, and evidence/context versions.
// A later evidence or context drift produces a different hash and cannot ride
// an earlier individual approval.
func ContentBindingHash(channel, recipient, subject, body, purpose string, evidenceIDs []string, contextHash string) string {
	var b strings.Builder
	b.WriteString(strings.ToUpper(strings.TrimSpace(channel)))
	b.WriteByte(0)
	b.WriteString(strings.TrimSpace(recipient))
	b.WriteByte(0)
	b.WriteString(strings.TrimSpace(subject))
	b.WriteByte(0)
	b.WriteString(strings.TrimSpace(body))
	b.WriteByte(0)
	b.WriteString(strings.ToUpper(strings.TrimSpace(purpose)))
	b.WriteByte(0)
	ev := append([]string(nil), evidenceIDs...)
	sort.Strings(ev)
	b.WriteString(strings.Join(ev, ","))
	b.WriteByte(0)
	b.WriteString(strings.TrimSpace(contextHash))
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// TouchpointBindingHash is the live recipient/content/evidence version stamp.
func TouchpointBindingHash(tp *models.OutreachTouchpoint) string {
	if tp == nil {
		return ""
	}
	return ContentBindingHash(tp.Channel, tp.Recipient, tp.Subject, tp.BodyText, tp.Purpose, tp.EvidenceIDs, tp.GeneratedContextHash)
}

func RecomputeContentHash(tp *models.OutreachTouchpoint) {
	if tp == nil {
		return
	}
	tp.ContentHash = TouchpointBindingHash(tp)
}

// ClearApproval is the single canonical invalidation of any authorization
// (human or CAMPAIGN_POLICY). Material mutations and context_stale MUST use this.
func ClearApproval(tp *models.OutreachTouchpoint) {
	if tp == nil {
		return
	}
	tp.ApprovedBy = nil
	tp.ApprovedAt = nil
	tp.ApprovedContentHash = ""
	tp.AuthorizationMode = ""
	tp.CampaignPolicyAuthorizationID = nil
	tp.AuthorizationPolicyHash = ""
	tp.AuthorizationAt = nil
	if tp.State == models.TouchpointApproved || tp.State == models.TouchpointQueued {
		tp.State = models.TouchpointNeedsReview
	}
}

func ApplyHumanApproval(tp *models.OutreachTouchpoint, humanUserID uuid.UUID, now time.Time) error {
	if tp == nil {
		return fmt.Errorf("nil touchpoint")
	}
	if humanUserID == uuid.Nil {
		return fmt.Errorf("approved_by requires a human user id")
	}
	RecomputeContentHash(tp)
	if tp.ContentHash == "" || tp.BodyText == "" {
		return fmt.Errorf("cannot approve empty content")
	}
	if tp.Recipient == "" {
		return fmt.Errorf("cannot approve without exact recipient")
	}
	switch tp.State {
	case models.TouchpointDrafted, models.TouchpointNeedsReview, models.TouchpointApproved:
	default:
		return fmt.Errorf("cannot approve from state %s", tp.State)
	}
	tp.ApprovedBy = &humanUserID
	tp.ApprovedAt = &now
	tp.ApprovedContentHash = tp.ContentHash
	tp.AuthorizationMode = AuthorizationModeHumanTouchpoint
	tp.State = models.TouchpointApproved
	return nil
}

// ApplyCampaignPolicyAuthorization marks a GREEN touchpoint queueable under
// CAMPAIGN_POLICY without forging approved_by to a human who never reviewed it.
// auth must be the active grant; its ID and policy hash are bound for audit/revalidation.
func ApplyCampaignPolicyAuthorization(tp *models.OutreachTouchpoint, auth *models.CampaignPolicyAuthorization, now time.Time) error {
	if tp == nil {
		return fmt.Errorf("nil touchpoint")
	}
	if auth == nil || auth.ID == uuid.Nil {
		return fmt.Errorf("campaign policy authorization id required")
	}
	if tp.ContentHash == "" {
		RecomputeContentHash(tp)
	}
	if tp.ContentHash == "" || tp.BodyText == "" {
		return fmt.Errorf("cannot policy-authorize empty content")
	}
	if tp.Recipient == "" {
		return fmt.Errorf("cannot policy-authorize without exact recipient")
	}
	if tp.ContactCandidateID == nil || *tp.ContactCandidateID == uuid.Nil {
		return fmt.Errorf("cannot policy-authorize without contact candidate")
	}
	switch tp.State {
	case models.TouchpointDrafted, models.TouchpointNeedsReview, models.TouchpointApproved:
	default:
		return fmt.Errorf("cannot policy-authorize from state %s", tp.State)
	}
	// Explicit: no human approved_by for policy path.
	tp.ApprovedBy = nil
	tp.ApprovedAt = &now
	tp.ApprovedContentHash = tp.ContentHash
	tp.AuthorizationMode = AuthorizationModeCampaignPolicy
	id := auth.ID
	tp.CampaignPolicyAuthorizationID = &id
	tp.AuthorizationPolicyHash = PolicyAuthorizationHash(auth)
	at := now
	tp.AuthorizationAt = &at
	if tp.SignatureVersion == "" {
		tp.SignatureVersion = SignatureVersion
	}
	tp.State = models.TouchpointApproved
	return nil
}

func CanTransport(tp *models.OutreachTouchpoint) error {
	if tp == nil {
		return fmt.Errorf("nil touchpoint")
	}
	switch tp.State {
	case models.TouchpointApproved, models.TouchpointQueued:
	default:
		return fmt.Errorf("touchpoint state %s is not transportable", tp.State)
	}
	if tp.ContentHash == "" || tp.ApprovedContentHash == "" {
		return fmt.Errorf("missing content hash")
	}
	// Recompute from the live recipient, copy, evidence ids, and context
	// version so a stale approval cannot ride mutated fields.
	want := TouchpointBindingHash(tp)
	if want == "" || tp.ContentHash != want || tp.ApprovedContentHash != want {
		return fmt.Errorf("approved_content_hash does not match live recipient/content/evidence versions")
	}
	if FileKillSwitchActive() {
		return fmt.Errorf("kill switch engaged")
	}
	mode := strings.TrimSpace(tp.AuthorizationMode)
	if mode == AuthorizationModeCampaignPolicy {
		// CAMPAIGN_POLICY is not an individual approval record. Isolated env
		// cannot reactivate bulk/green autorun as a transport authority.
		return fmt.Errorf("campaign_policy cannot transport; individual approval required")
	}
	if mode == AuthorizationModeBoundedCohort {
		if tp.ApprovedBy == nil || *tp.ApprovedBy == uuid.Nil {
			return fmt.Errorf("bounded cohort requires human actor")
		}
		if tp.CampaignPolicyAuthorizationID == nil || *tp.CampaignPolicyAuthorizationID == uuid.Nil {
			return fmt.Errorf("bounded cohort missing authorization binding")
		}
		if strings.TrimSpace(tp.AuthorizationPolicyHash) == "" {
			return fmt.Errorf("bounded cohort missing authorization hash")
		}
		return nil
	}
	if tp.ApprovedBy == nil || *tp.ApprovedBy == uuid.Nil {
		return fmt.Errorf("missing human approved_by")
	}
	return nil
}

func TransitionToQueued(tp *models.OutreachTouchpoint, now time.Time) error {
	if err := CanTransport(tp); err != nil {
		return err
	}
	if tp.State == models.TouchpointQueued {
		return nil
	}
	if tp.State != models.TouchpointApproved {
		return fmt.Errorf("expected APPROVED, got %s", tp.State)
	}
	tp.State = models.TouchpointQueued
	tp.QueuedAt = &now
	return nil
}

func TransitionToSent(tp *models.OutreachTouchpoint, now time.Time, providerMsgID string) error {
	if tp == nil {
		return fmt.Errorf("nil touchpoint")
	}
	if tp.State != models.TouchpointQueued && tp.State != models.TouchpointApproved {
		return fmt.Errorf("cannot mark sent from state %s", tp.State)
	}
	if err := CanTransport(tp); err != nil {
		return err
	}
	tp.State = models.TouchpointSent
	tp.SentAt = &now
	if providerMsgID != "" {
		tp.ProviderMessageID = providerMsgID
	}
	return nil
}

func ApplyContentMutation(tp *models.OutreachTouchpoint, channel, recipient, subject, body string) {
	if tp == nil {
		return
	}
	if channel != "" {
		tp.Channel = channel
	}
	if recipient != "" {
		tp.Recipient = recipient
	}
	tp.Subject = subject
	tp.BodyText = body
	RecomputeContentHash(tp)
	ClearApproval(tp)
	if tp.State == models.TouchpointDue || tp.State == models.TouchpointPlanned {
		tp.State = models.TouchpointNeedsReview
	} else if tp.State != models.TouchpointNeedsReview && tp.State != models.TouchpointDrafted {
		if !models.TouchpointTerminalStates[tp.State] {
			tp.State = models.TouchpointNeedsReview
		}
	}
}

func TerminalStopReason(reason string) (state, stop string) {
	r := strings.ToUpper(strings.TrimSpace(reason))
	switch {
	case r == "DNC" || strings.Contains(r, "DO_NOT_CONTACT") || strings.Contains(r, "OPT_OUT"):
		return models.TouchpointDNC, r
	case r == "REPLY" || strings.Contains(r, "REPLIED"):
		return models.TouchpointReplied, r
	case r == "BOUNCE" || strings.Contains(r, "BOUNCED"):
		return models.TouchpointBounced, r
	case r == "SKIP" || r == "SKIPPED":
		return models.TouchpointSkipped, r
	case r == "REJECT" || r == "REJECTED":
		return models.TouchpointRejected, r
	default:
		return models.TouchpointCancelled, r
	}
}

func IsOpen(state string) bool { return models.TouchpointOpenStates[state] }

func NextMayRelease(prior *models.OutreachTouchpoint) bool {
	if prior == nil {
		return true
	}
	switch prior.State {
	case models.TouchpointSent, models.TouchpointSkipped, models.TouchpointRejected:
		return true
	default:
		return false
	}
}

// PriorReleased reports whether every lower ordinal is SENT/SKIPPED/REJECTED.
// Open states and other terminals (DNC/REPLIED/…) block releasing the next touch.
func PriorReleased(priors []models.OutreachTouchpoint, ordinal int) bool {
	if ordinal <= 1 {
		return true
	}
	for o := 1; o < ordinal; o++ {
		found := false
		for _, p := range priors {
			if p.Ordinal != o {
				continue
			}
			found = true
			switch p.State {
			case models.TouchpointSent, models.TouchpointSkipped, models.TouchpointRejected:
				// releasable
			default:
				return false
			}
			break
		}
		if !found {
			return false
		}
	}
	return true
}
