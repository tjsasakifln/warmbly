package tasks

import (
	"context"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/warmbly/warmbly/internal/app/confenge"
)

// ConfengeOutboundGate is the optional final gate for CONFENGE-attributed campaign email.
// Implemented by confenge.Service; nil means no CONFENGE mailbox pacing.
type ConfengeOutboundGate interface {
	GateCampaignEmailForTransport(ctx context.Context, orgID uuid.UUID, campaignName, recipientEmail string, campaignID, contactID, sequenceID uuid.UUID, binding confenge.CampaignTransportBinding) confenge.CampaignGateResult
	ReleaseCampaignEmail(ctx context.Context, reservationID uuid.UUID, errText string)
}

// WireConfengeDispatch attaches CONFENGE mailbox pacing to campaign sends.
func (s *tasksService) WireConfengeDispatch(g ConfengeOutboundGate) {
	s.confengeGate = g
}

// advancePastCommercialBlock moves campaign routing past a lead the CONFENGE
// commercial gate rejected. The block is reversible, so this records no bounce
// and no global recipient suppression: it only stops the campaign reselecting
// the same ineligible contact on every cycle, which would otherwise head-of-line
// block every other contact behind it.
func (s *tasksService) advancePastCommercialBlock(ctx context.Context, campaignID, contactID, sequenceID uuid.UUID) {
	s.advancePastSkippedPair(ctx, campaignID, contactID, sequenceID, "confenge gate: failed to advance past commercial block")
}

// advancePastSkippedPair moves campaign routing past a lead this cycle will not
// send. It records no bounce and no global suppression. Without it the scheduler
// reselects the same skipped contact forever, which is how an unverifiable
// mailbox (550 / no MX) starved every eligible first-touch behind it.
func (s *tasksService) advancePastSkippedPair(ctx context.Context, campaignID, contactID, sequenceID uuid.UUID, warnMsg string) {
	if s.campaignProgressRepo == nil {
		return
	}
	if err := s.campaignProgressRepo.RecordEmailSent(ctx, campaignID, contactID, sequenceID); err != nil {
		log.Warn().Err(err).Str("campaign_id", campaignID.String()).Msg(warnMsg)
	}
}
