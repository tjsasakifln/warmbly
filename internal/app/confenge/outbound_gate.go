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
func (s *service) GateCampaignEmail(ctx context.Context, orgID uuid.UUID, campaignName, recipientEmail string, campaignID, contactID, sequenceID uuid.UUID) CampaignGateResult {
	if s == nil || !s.cfg.Enabled || s.governor == nil {
		return CampaignGateResult{Kind: GateBypass, Reason: ReasonNoGovernor}
	}
	if !IsConfengeCampaign(campaignName) {
		return CampaignGateResult{Kind: GateBypass, Reason: ReasonNotConfenge}
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
					// Invalidate approval so stale copy cannot be retried as "approved".
					tp.State = models.TouchpointNeedsReview
					tp.StopReason = "context_stale"
					tp.ContextStale = true
					tp.ApprovedBy, tp.ApprovedAt = nil, nil
					tp.ApprovedContentHash = ""
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
			}
		}
	}
	_ = sequenceID // campaign step id (not a draft/touchpoint id)

	key := MessageKeyCampaignEmail(campaignID, contactID, sequenceID)
	res, err := s.governor.TryReserve(ctx, dispatch.ReserveRequest{
		OrganizationID: orgID,
		Channel:        dispatch.ChannelEmail,
		MessageKey:     key,
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
