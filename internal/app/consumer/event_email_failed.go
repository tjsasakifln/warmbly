package jobs

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// HandleEmailFailed records permanent SMTP recipient rejections as bounces.
func (s *JobsService) HandleEmailFailed(ctx context.Context, event *models.SendEmailResult) error {
	if event == nil || event.TaskID == uuid.Nil {
		return fmt.Errorf("invalid EMAIL_FAILED result")
	}
	if event.Error == nil || !strings.EqualFold(event.Error.Code, string(errx.MailErrorCodeRecipientRejected)) {
		return nil
	}
	if s.AdvancedService == nil {
		return fmt.Errorf("deliverability service is not configured")
	}
	if xerr := s.AdvancedService.RecordOutboundBounce(ctx, event.TaskID, event.Error.Message); xerr != nil {
		return fmt.Errorf("record outbound SMTP rejection: %w", xerr)
	}
	return nil
}
