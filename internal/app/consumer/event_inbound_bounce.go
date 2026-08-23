package jobs

import (
	"context"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/warmbly/warmbly/internal/app/confenge"
	"github.com/warmbly/warmbly/internal/models"
)

// HandleInboundBounce records an attributable NDR. Only a machine-proven HARD
// result suppresses the recipient or feeds the permanent-bounce breaker.
func (s *JobsService) HandleInboundBounce(ctx context.Context, e *models.JobEventInboundBounce) error {
	if e == nil {
		return nil
	}
	if s.AdvancedService != nil && strings.EqualFold(e.BounceClass, "HARD") {
		if xerr := s.AdvancedService.RecordInboundBounce(ctx, e.EmailID, e.OriginalMessageID, e.FailedRecipient, e.Reason); xerr != nil {
			log.Warn().
				Str("email_id", e.EmailID.String()).
				Str("original_message_id", e.OriginalMessageID).
				Str("error", xerr.Message).
				Msg("Failed to record inbound bounce")
		}
	}
	// CONFENGE: attribute bounce to staged contact when org is known.
	if s.ConfengeOutcomes != nil && s.ConfengeOutcomes.Enabled() && s.EmailRepository != nil {
		if account, err := s.EmailRepository.GetByID(ctx, e.EmailID); err == nil && account != nil && account.OrganizationID != nil {
			observation := confenge.BounceObservation{
				Class: e.BounceClass, ProviderName: account.Provider, OriginalMessageID: e.OriginalMessageID,
				EnhancedStatus: e.EnhancedStatus, SMTPStatus: e.SMTPStatus,
				Diagnostic: e.Diagnostic, Reason: e.Reason,
			}
			if structured, ok := s.ConfengeOutcomes.(confenge.StructuredBounceOutcomeSink); ok {
				_ = structured.NoteBounceObservation(ctx, *account.OrganizationID, e.FailedRecipient, observation)
			} else if strings.EqualFold(e.BounceClass, "HARD") {
				_ = s.ConfengeOutcomes.NoteBounce(ctx, *account.OrganizationID, e.FailedRecipient, e.Reason)
			}
		}
	}
	return nil
}
