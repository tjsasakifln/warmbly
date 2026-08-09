package confenge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/models"
)

// GateKind is a closed set of campaign-email gate outcomes.
// Permanent suppress/bounce is only valid for GateHardBlock.
type GateKind int

const (
	// GateProceed: reservation granted; caller may send then Commit/Release.
	GateProceed GateKind = iota
	// GateDeferred: no slot (cap/gap/pause/window); reschedule at NextSlot; no success count.
	GateDeferred
	// GateAlready: successful outbound already recorded for this message_key (idempotent).
	GateAlready
	// GateHardBlock: DNC/bounce/account block only — permanent progress skip + suppress.
	GateHardBlock
	// GateTransient: governor/store/infra failure — retry/backoff; never suppress/bounce.
	GateTransient
	// GateBypass: not a CONFENGE campaign or governor not wired; send without global cap.
	GateBypass
)

// Machine-readable reasons for GateHardBlock / GateDeferred / GateTransient.
const (
	ReasonDNCOrBounce = "dnc_or_bounce"
	ReasonAccountDNC  = "account_dnc"
	ReasonAlreadySent = "already_sent"
	ReasonNotConfenge = "not_confenge"
	ReasonNoGovernor  = "no_governor"
	ReasonGovernor    = "governor_error"
)

// CampaignGateResult is the discriminant result of GateCampaignEmail.
// Policy hard-blocks never use Err; infrastructure failures use Kind=GateTransient + Err.
type CampaignGateResult struct {
	Kind          GateKind
	ReservationID uuid.UUID
	NextSlot      time.Time
	Reason        string
	Err           error // only meaningful for GateTransient
}

// PermanentSuppress reports whether the campaign task may org-wide suppress / bounce-mark.
// Only GateHardBlock is true; Transient/Deferred/Already/Proceed never permanent-suppress.
func (r CampaignGateResult) PermanentSuppress() bool {
	return r.Kind == GateHardBlock
}

// IsConfengeCampaign reports whether a campaign is CONFENGE-attributed.
func IsConfengeCampaign(name string) bool {
	return strings.HasPrefix(strings.TrimSpace(name), "CONFENGE")
}

// MessageKeyCampaignEmail builds a stable idempotency key for a campaign step send.
func MessageKeyCampaignEmail(campaignID, contactID, sequenceID uuid.UUID) string {
	return fmt.Sprintf("email:campaign:%s:contact:%s:seq:%s", campaignID, contactID, sequenceID)
}

// GateCampaignEmail is the final CONFENGE email outbound gate (pre worker/SMTP).
// Outcomes are a closed GateKind set — callers must switch on Kind, not raw error.
// CONFENGE campaigns never fail-open: missing/unhealthy governor is GateTransient (zero send).
// Non-CONFENGE campaigns retain legacy GateBypass.
func (s *service) GateCampaignEmail(ctx context.Context, orgID uuid.UUID, campaignName, recipientEmail string, campaignID, contactID, sequenceID uuid.UUID) CampaignGateResult {
	if !IsConfengeCampaign(campaignName) {
		return CampaignGateResult{Kind: GateBypass, Reason: ReasonNotConfenge}
	}
	// CONFENGE path: fail-closed without a healthy governor. Never GateBypass.
	if s == nil || !s.cfg.Enabled || s.governor == nil {
		return CampaignGateResult{
			Kind:   GateTransient,
			Reason: ReasonNoGovernor,
			Err:    fmt.Errorf("confenge governor not wired or disabled; refusing send"),
		}
	}

	// Dominant blocks: DNC/opt-out/bounce — hard block without consuming a slot.
	// Err is deliberately nil so callers never treat policy as infrastructure failure.
	var acc *models.OutreachAccount
	if recipientEmail != "" {
		cand, found, err := s.repo.FindCandidateByEmail(ctx, orgID, strings.TrimSpace(strings.ToLower(recipientEmail)))
		if err != nil {
			// Lookup failure is transient (DB blip), not a permanent DNC.
			return CampaignGateResult{Kind: GateTransient, Reason: ReasonGovernor, Err: fmt.Errorf("contact lookup: %w", err)}
		}
		acc = found
		if cand != nil && (cand.DoNotContact || cand.Bounced) {
			return CampaignGateResult{Kind: GateHardBlock, Reason: ReasonDNCOrBounce}
		}
		if acc != nil && (acc.DoNotContact || acc.Blocked) {
			return CampaignGateResult{Kind: GateHardBlock, Reason: ReasonAccountDNC}
		}
	}
	// Final material-context check at pre-SMTP gate: any approved/queued touchpoint
	// for this account must still match message_context_hash (rank-only changes OK).
	// Stale context is NOT permanent suppress (must not bounce/suppress recipient).
	// Clear approval and defer so the operator regenerates + re-approves.
	if acc != nil && strings.TrimSpace(acc.MessageContextHash) != "" {
		for _, st := range []string{models.TouchpointApproved, models.TouchpointQueued} {
			open, err := s.repo.ListTouchpoints(ctx, orgID, acc.ID, st, 10, 0)
			if err != nil {
				continue
			}
			for i := range open {
				tp := &open[i]
				if tp.GeneratedContextHash == "" {
					continue
				}
				if err := AssertMessageContextFresh(acc, tp.GeneratedContextHash); err != nil {
					// Canonical invalidation: clears human + CAMPAIGN_POLICY binding.
					ClearApproval(tp)
					tp.StopReason = "context_stale"
					tp.ContextStale = true
					_ = s.repo.UpdateTouchpoint(ctx, tp)
					_ = s.repo.SetAccountHumanFlags(ctx, orgID, acc.ID, acc.Blocked, acc.DoNotContact, acc.BlockReason, models.OutreachQueueNeedsReview)
					due := time.Now().UTC().Add(time.Minute)
					return CampaignGateResult{
						Kind:     GateDeferred,
						NextSlot: due,
						Reason:   "context_stale",
						Err:      err,
					}
				}
				// Final transport revalidation for CAMPAIGN_POLICY grants.
				if strings.TrimSpace(tp.AuthorizationMode) == AuthorizationModeCampaignPolicy {
					if block := s.revalidateCampaignPolicyAtSend(ctx, orgID, tp); block != nil {
						return *block
					}
				}
			}
		}
	}
	_ = sequenceID // campaign step id (not a draft/touchpoint id)

	// Effective hourly cap = min(adaptive runtime, active campaign policy rate).
	capOverride := 0
	if s.policyStore != nil {
		if pol, _ := s.policyStore.GetActiveCampaignPolicy(ctx, orgID, campaignID, time.Now().UTC()); pol != nil && pol.MaxRatePerHour > 0 {
			capOverride = pol.MaxRatePerHour
		}
	}

	key := MessageKeyCampaignEmail(campaignID, contactID, sequenceID)
	res, err := s.governor.TryReserve(ctx, dispatch.ReserveRequest{
		OrganizationID: orgID,
		Channel:        dispatch.ChannelEmail,
		MessageKey:     key,
		CapOverride:    capOverride,
	})
	if err != nil {
		// Store / control / clock failures are always transient.
		return CampaignGateResult{Kind: GateTransient, Reason: ReasonGovernor, Err: fmt.Errorf("dispatch governor: %w", err)}
	}
	if res.AlreadyCommitted {
		return CampaignGateResult{Kind: GateAlready, Reason: ReasonAlreadySent}
	}
	if !res.Allowed {
		due := res.NextSlot
		if due.IsZero() {
			due = time.Now().UTC().Add(s.governor.Config().MinGap)
		}
		_ = s.governor.Enqueue(ctx, dispatch.EnqueueRequest{
			OrganizationID: orgID,
			Channel:        dispatch.ChannelEmail,
			DraftID:        uuid.Nil,
			MessageKey:     key,
			RecipientRef:   strings.TrimSpace(strings.ToLower(recipientEmail)),
			DueAt:          due,
		})
		return CampaignGateResult{Kind: GateDeferred, NextSlot: due, Reason: res.Reason}
	}
	return CampaignGateResult{
		Kind:          GateProceed,
		ReservationID: res.Reservation.ID,
		Reason:        res.Reason,
	}
}

// revalidateCampaignPolicyAtSend blocks transport when the bound grant is gone,
// revoked, hash-mismatched, or channel/risk no longer match.
func (s *service) revalidateCampaignPolicyAtSend(ctx context.Context, orgID uuid.UUID, tp *models.OutreachTouchpoint) *CampaignGateResult {
	if tp == nil {
		return &CampaignGateResult{Kind: GateTransient, Reason: "nil_touchpoint", Err: fmt.Errorf("nil touchpoint")}
	}
	if tp.CampaignPolicyAuthorizationID == nil || *tp.CampaignPolicyAuthorizationID == uuid.Nil {
		ClearApproval(tp)
		_ = s.repo.UpdateTouchpoint(ctx, tp)
		return &CampaignGateResult{Kind: GateDeferred, Reason: "policy_binding_missing", NextSlot: time.Now().UTC().Add(time.Minute)}
	}
	if s.policyStore == nil {
		return &CampaignGateResult{Kind: GateTransient, Reason: "policy_store_missing", Err: fmt.Errorf("policy store not wired")}
	}
	auth, err := s.policyStore.GetCampaignPolicyByID(ctx, orgID, *tp.CampaignPolicyAuthorizationID)
	if err != nil {
		return &CampaignGateResult{Kind: GateTransient, Reason: ReasonGovernor, Err: err}
	}
	now := time.Now().UTC()
	if auth == nil || !auth.Active(now) {
		ClearApproval(tp)
		tp.StopReason = "policy_revoked"
		_ = s.repo.UpdateTouchpoint(ctx, tp)
		return &CampaignGateResult{Kind: GateDeferred, Reason: "policy_revoked", NextSlot: now.Add(time.Minute)}
	}
	wantHash := PolicyAuthorizationHash(auth)
	if tp.AuthorizationPolicyHash != "" && tp.AuthorizationPolicyHash != wantHash {
		ClearApproval(tp)
		tp.StopReason = "policy_hash_mismatch"
		_ = s.repo.UpdateTouchpoint(ctx, tp)
		return &CampaignGateResult{Kind: GateDeferred, Reason: "policy_hash_mismatch", NextSlot: now.Add(time.Minute)}
	}
	if strings.ToUpper(strings.TrimSpace(auth.Channel)) != "" &&
		strings.ToUpper(strings.TrimSpace(auth.Channel)) != strings.ToUpper(strings.TrimSpace(tp.Channel)) {
		ClearApproval(tp)
		tp.StopReason = "policy_channel_mismatch"
		_ = s.repo.UpdateTouchpoint(ctx, tp)
		return &CampaignGateResult{Kind: GateDeferred, Reason: "policy_channel_mismatch", NextSlot: now.Add(time.Minute)}
	}
	if err := CanTransport(tp); err != nil {
		ClearApproval(tp)
		tp.StopReason = "transport_invalid"
		_ = s.repo.UpdateTouchpoint(ctx, tp)
		return &CampaignGateResult{Kind: GateDeferred, Reason: "transport_invalid", NextSlot: now.Add(time.Minute), Err: err}
	}
	return nil
}

// CommitCampaignEmail records a successful CONFENGE campaign outbound.
func (s *service) CommitCampaignEmail(ctx context.Context, reservationID uuid.UUID) error {
	if s == nil || s.governor == nil || reservationID == uuid.Nil {
		return nil
	}
	return s.governor.Commit(ctx, reservationID)
}

// ReleaseCampaignEmail frees a lease after provider/worker publish failure.
func (s *service) ReleaseCampaignEmail(ctx context.Context, reservationID uuid.UUID, errText string) {
	if s == nil || s.governor == nil || reservationID == uuid.Nil {
		return
	}
	_ = s.governor.Release(ctx, reservationID, errText)
	_ = s.governor.RecordFailure(ctx, uuid.Nil, dispatch.ChannelEmail, "", nil, errText)
}
