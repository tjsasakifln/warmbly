package jobs

import (
	"context"
	"fmt"

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
	return nil
}
