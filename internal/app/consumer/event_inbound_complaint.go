package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/warmbly/warmbly/internal/app/confenge"
	"github.com/warmbly/warmbly/internal/models"
)

func (s *JobsService) HandleInboundComplaint(ctx context.Context, event *models.JobEventInboundComplaint) error {
	if event == nil {
		return fmt.Errorf("invalid INBOUND_COMPLAINT event")
	}
	if s.AdvancedService == nil {
		return fmt.Errorf("deliverability service is not configured")
	}
	if xerr := s.AdvancedService.RecordInboundComplaint(ctx, event.EmailID, event.OriginalMessageID, event.Recipient, event.FeedbackType); xerr != nil {
		return fmt.Errorf("record inbound complaint: %w", xerr)
	}
	if s.ConfengeOutcomes != nil && s.ConfengeOutcomes.Enabled() && s.EmailRepository != nil {
		account, xerr := s.EmailRepository.GetByID(ctx, event.EmailID)
		if xerr != nil {
			return fmt.Errorf("load mailbox for inbound complaint: %w", xerr)
		}
		if account != nil && account.OrganizationID != nil {
			if sink, ok := s.ConfengeOutcomes.(confenge.StructuredEmailOutcomeSink); ok {
				if err := sink.NoteComplaintObservation(ctx, *account.OrganizationID, event.Recipient, confenge.ComplaintObservation{
					EventID: "fbl:" + event.OriginalMessageID, MailboxID: event.EmailID.String(),
					ProviderName: account.Provider, OriginalMessageID: event.OriginalMessageID,
					FeedbackType: event.FeedbackType, OccurredAt: time.Now().UTC(),
				}); err != nil {
					return fmt.Errorf("reconcile complaint: %w", err)
				}
			}
		}
	}
	return nil
}
