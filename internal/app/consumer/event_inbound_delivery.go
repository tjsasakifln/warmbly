package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/warmbly/warmbly/internal/app/confenge"
	"github.com/warmbly/warmbly/internal/models"
)

func (s *JobsService) HandleInboundDelivery(ctx context.Context, event *models.JobEventInboundDelivery) error {
	if event == nil {
		return fmt.Errorf("invalid INBOUND_DELIVERY event")
	}
	if s.ConfengeOutcomes == nil || !s.ConfengeOutcomes.Enabled() || s.EmailRepository == nil {
		return nil
	}
	account, xerr := s.EmailRepository.GetByID(ctx, event.EmailID)
	if xerr != nil {
		return fmt.Errorf("load mailbox for inbound delivery: %w", xerr)
	}
	if account == nil || account.OrganizationID == nil {
		return nil
	}
	if sink, ok := s.ConfengeOutcomes.(confenge.StructuredEmailOutcomeSink); ok {
		if err := sink.NoteDeliveryObservation(ctx, *account.OrganizationID, event.Recipient, confenge.DeliveryObservation{
			EventID:   "dsn:delivered:" + event.OriginalMessageID,
			MailboxID: event.EmailID.String(), ProviderName: account.Provider,
			OriginalMessageID: event.OriginalMessageID, EnhancedStatus: event.EnhancedStatus,
			SMTPStatus: event.SMTPStatus, Diagnostic: event.Diagnostic, OccurredAt: time.Now().UTC(),
		}); err != nil {
			return fmt.Errorf("reconcile delivery: %w", err)
		}
	}
	return nil
}
