package jobs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/warmbly/warmbly/internal/app/confenge"
	"github.com/warmbly/warmbly/internal/models"
)

// HandleInboundBounce records an attributable NDR. Only a machine-proven HARD
// result suppresses the recipient or feeds the permanent-bounce breaker.
func (s *JobsService) HandleInboundBounce(ctx context.Context, e *models.JobEventInboundBounce) error {
	if e == nil {
		return fmt.Errorf("invalid INBOUND_BOUNCE event")
	}
	if strings.EqualFold(e.BounceClass, "HARD") {
		if s.AdvancedService == nil {
			return fmt.Errorf("deliverability service is not configured")
		}
		if xerr := s.AdvancedService.RecordInboundBounce(ctx, e.EmailID, e.OriginalMessageID, e.FailedRecipient, e.Reason); xerr != nil {
			return fmt.Errorf("record inbound bounce: %w", xerr)
		}
	}
	// CONFENGE: attribute bounce to staged contact when org is known.
	if s.ConfengeOutcomes != nil && s.ConfengeOutcomes.Enabled() && s.EmailRepository != nil {
		account, xerr := s.EmailRepository.GetByID(ctx, e.EmailID)
		if xerr != nil {
			return fmt.Errorf("load mailbox for inbound bounce: %w", xerr)
		}
		if account != nil && account.OrganizationID != nil {
			observation := confenge.BounceObservation{
				EventID:   "dsn:" + e.BounceClass + ":" + e.OriginalMessageID,
				MailboxID: e.EmailID.String(), OccurredAt: time.Now().UTC(),
				Class: e.BounceClass, ProviderName: account.Provider, OriginalMessageID: e.OriginalMessageID,
				EnhancedStatus: e.EnhancedStatus, SMTPStatus: e.SMTPStatus,
				Diagnostic: e.Diagnostic, Reason: e.Reason,
			}
			if structured, ok := s.ConfengeOutcomes.(confenge.StructuredBounceOutcomeSink); ok {
				if err := structured.NoteBounceObservation(ctx, *account.OrganizationID, e.FailedRecipient, observation); err != nil {
					return fmt.Errorf("record structured bounce outcome: %w", err)
				}
			} else if strings.EqualFold(e.BounceClass, "HARD") {
				if err := s.ConfengeOutcomes.NoteBounce(ctx, *account.OrganizationID, e.FailedRecipient, e.Reason); err != nil {
					return fmt.Errorf("record bounce outcome: %w", err)
				}
			}
		}
	}
	return nil
}
