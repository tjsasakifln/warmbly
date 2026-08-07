package confenge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
)

// IsConfengeCampaign reports whether a campaign is CONFENGE-attributed.
func IsConfengeCampaign(name string) bool {
	return strings.HasPrefix(strings.TrimSpace(name), "CONFENGE")
}

// MessageKeyCampaignEmail builds a stable idempotency key for a campaign step send.
func MessageKeyCampaignEmail(campaignID, contactID, sequenceID uuid.UUID) string {
	return fmt.Sprintf("email:campaign:%s:contact:%s:seq:%s", campaignID, contactID, sequenceID)
}

// GateCampaignEmail is the final CONFENGE email outbound gate (pre worker/SMTP).
// deferred=true means no slot: caller must reschedule at nextSlot (no success count).
func (s *service) GateCampaignEmail(ctx context.Context, orgID uuid.UUID, campaignName, recipientEmail string, campaignID, contactID, sequenceID uuid.UUID) (reservationID uuid.UUID, already, deferred bool, nextSlot time.Time, reason string, err error) {
	if s == nil || !s.cfg.Enabled || s.governor == nil {
		return uuid.Nil, false, false, time.Time{}, "", nil
	}
	if !IsConfengeCampaign(campaignName) {
		return uuid.Nil, false, false, time.Time{}, "", nil
	}
	// Dominant blocks: DNC/opt-out must not send even with a free slot.
	if recipientEmail != "" {
		cand, acc, err := s.repo.FindCandidateByEmail(ctx, orgID, strings.TrimSpace(strings.ToLower(recipientEmail)))
		if err == nil {
			if cand != nil && (cand.DoNotContact || cand.Bounced) {
				return uuid.Nil, false, false, time.Time{}, "dnc_or_bounce", fmt.Errorf("contact blocked (DNC/bounce)")
			}
			if acc != nil && (acc.DoNotContact || acc.Blocked) {
				return uuid.Nil, false, false, time.Time{}, "account_dnc", fmt.Errorf("account blocked or DO_NOT_CONTACT")
			}
		}
	}
	key := MessageKeyCampaignEmail(campaignID, contactID, sequenceID)
	res, err := s.governor.TryReserve(ctx, dispatch.ReserveRequest{
		OrganizationID: orgID,
		Channel:        dispatch.ChannelEmail,
		MessageKey:     key,
	})
	if err != nil {
		return uuid.Nil, false, false, time.Time{}, "", fmt.Errorf("dispatch governor: %w", err)
	}
	if res.AlreadyCommitted {
		return uuid.Nil, true, false, time.Time{}, "already_sent", nil
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
		return uuid.Nil, false, true, due, res.Reason, nil
	}
	return res.Reservation.ID, false, false, time.Time{}, res.Reason, nil
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
