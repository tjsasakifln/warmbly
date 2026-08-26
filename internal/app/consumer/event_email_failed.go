package jobs

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/app/confenge"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// HandleEmailFailed reconciles SMTP outcomes without promoting ambiguity to hard bounce.
func (s *JobsService) HandleEmailFailed(ctx context.Context, event *models.SendEmailResult) error {
	if event == nil || event.TaskID == uuid.Nil {
		return fmt.Errorf("invalid EMAIL_FAILED result")
	}
	errorCode, errorText := "", strings.TrimSpace(event.LegacyErrorMsg)
	if event.Error != nil {
		errorCode = strings.TrimSpace(event.Error.Code)
		errorText = strings.TrimSpace(event.Error.Message)
	}
	observedErr := s.recordConfengeMailboxFailure(ctx, event.TaskID, errorCode, errorText, event.SentAt)
	if event.Error == nil {
		return observedErr
	}
	code := errx.MailErrorCode(strings.ToUpper(strings.TrimSpace(event.Error.Code)))
	if code != errx.MailErrorCodeRecipientRejected && code != errx.MailErrorCodeRecipientTemporaryRejected && code != errx.MailErrorCodeDeliveryUnknown {
		return observedErr
	}
	if code == errx.MailErrorCodeRecipientRejected {
		if s.AdvancedService == nil {
			return fmt.Errorf("deliverability service is not configured")
		}
		if xerr := s.AdvancedService.RecordOutboundBounce(ctx, event.TaskID, event.Error.Message); xerr != nil {
			return fmt.Errorf("record outbound SMTP rejection: %w", xerr)
		}
	}
	if s.ConfengeOutcomes == nil || !s.ConfengeOutcomes.Enabled() || s.TaskRepo == nil || s.EmailRepository == nil {
		return observedErr
	}
	task, err := s.TaskRepo.GetTask(ctx, event.TaskID)
	if err != nil {
		return fmt.Errorf("load failed send task: %w", err)
	}
	if task == nil {
		return observedErr
	}
	mailbox, xerr := s.EmailRepository.GetByID(ctx, task.EmailAccountID)
	if xerr != nil {
		return fmt.Errorf("load failed send mailbox: %w", xerr)
	}
	if mailbox == nil || mailbox.OrganizationID == nil {
		return observedErr
	}
	emailTask, err := s.TaskRepo.GetEmailTask(ctx, event.TaskID)
	if err != nil {
		return fmt.Errorf("load failed email task: %w", err)
	}
	if emailTask == nil || len(emailTask.To) == 0 {
		return observedErr
	}
	bounceClass := "SOFT"
	unknown := code == errx.MailErrorCodeDeliveryUnknown
	if code == errx.MailErrorCodeRecipientRejected {
		bounceClass = "HARD"
	} else if unknown {
		bounceClass = "UNKNOWN"
	}
	if structured, ok := s.ConfengeOutcomes.(confenge.StructuredBounceOutcomeSink); ok {
		if err := structured.NoteBounceObservation(ctx, *mailbox.OrganizationID, emailTask.To[0], confenge.BounceObservation{
			Class: bounceClass, DeliveryUnknown: unknown,
			EventID:   "smtp:" + strings.ToLower(string(code)) + ":" + event.TaskID.String(),
			MailboxID: task.EmailAccountID.String(), OccurredAt: event.SentAt,
			ProviderName: mailbox.Provider, Reason: event.Error.Message,
		}); err != nil {
			return fmt.Errorf("reconcile outbound SMTP outcome: %w", err)
		}
	}
	return observedErr
}
