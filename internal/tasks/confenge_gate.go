package tasks

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ConfengeOutboundGate is the optional final gate for CONFENGE-attributed campaign email.
// Implemented by confenge.Service; nil means no global CONFENGE pacing.
type ConfengeOutboundGate interface {
	// GateCampaignEmail returns:
	//   already=true: success already recorded (idempotent skip of re-send or treat as done)
	//   deferred=true: no slot; reschedule at nextSlot
	//   reservationID: lease to commit/release after worker publish
	GateCampaignEmail(ctx context.Context, orgID uuid.UUID, campaignName, recipientEmail string, campaignID, contactID, sequenceID uuid.UUID) (reservationID uuid.UUID, already, deferred bool, nextSlot time.Time, reason string, err error)
	CommitCampaignEmail(ctx context.Context, reservationID uuid.UUID) error
	ReleaseCampaignEmail(ctx context.Context, reservationID uuid.UUID, errText string)
}

// WireConfengeDispatch attaches the CONFENGE global outbound governor to campaign sends.
func (s *tasksService) WireConfengeDispatch(g ConfengeOutboundGate) {
	s.confengeGate = g
}
