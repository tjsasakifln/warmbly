package advanced

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// RecordInboundBounce turns a permanent NDR the worker parsed into a bounce
// deliverability event. The worker only emits permanent (5.x.x) bounces with an
// original Message-ID, so this resolves that id to the campaign send and routes
// it through IngestDeliverabilityEvent (suppression + campaign progress +
// breaker), keyed idempotently so re-delivered NDRs don't double-count.
func (s *service) RecordInboundBounce(ctx context.Context, emailAccountID uuid.UUID, originalMessageID, failedRecipient, reason string) *errx.Error {
	return s.recordInboundDeliverability(ctx, emailAccountID, originalMessageID, failedRecipient, reason, models.DeliverabilityEventBounce, "inbound_ndr", "ndr:")
}

func (s *service) RecordInboundComplaint(ctx context.Context, emailAccountID uuid.UUID, originalMessageID, recipient, feedbackType string) *errx.Error {
	return s.recordInboundDeliverability(ctx, emailAccountID, originalMessageID, recipient, feedbackType, models.DeliverabilityEventComplaint, "inbound_fbl", "fbl:")
}

func (s *service) RecordOutboundBounce(ctx context.Context, taskID uuid.UUID, reason string) *errx.Error {
	if taskID == uuid.Nil {
		return errx.New(errx.BadRequest, "task_id is required")
	}

	task, err := s.taskRepo.GetTask(ctx, taskID)
	if err != nil {
		return toErrx(err)
	}
	if task == nil {
		return nil
	}

	account, xerr := s.emailRepo.GetByID(ctx, task.EmailAccountID)
	if xerr != nil {
		return xerr
	}
	if account == nil || account.OrganizationID == nil {
		return nil
	}

	campaignTask, err := s.taskRepo.GetCampaignTask(ctx, taskID)
	if err != nil {
		return toErrx(err)
	}
	if campaignTask == nil || campaignTask.ContactID == nil {
		return nil
	}

	contact, xerr := s.contactRepo.GetByID(ctx, *campaignTask.ContactID)
	if xerr != nil {
		return xerr
	}
	if contact == nil || strings.TrimSpace(contact.Email) == "" {
		return nil
	}

	return s.IngestDeliverabilityEvent(ctx, *account.OrganizationID, &models.IngestDeliverabilityEventRequest{
		CampaignID:     campaignTask.CampaignID,
		TaskID:         &task.ID,
		ContactID:      campaignTask.ContactID,
		EventType:      models.DeliverabilityEventBounce,
		Provider:       "worker_smtp",
		RecipientEmail: contact.Email,
		Reason:         reason,
		IdempotencyKey: "smtp_reject:" + taskID.String(),
	})
}

func (s *service) recordInboundDeliverability(ctx context.Context, emailAccountID uuid.UUID, originalMessageID, recipient, reason string, eventType models.DeliverabilityEventType, provider, idempotencyPrefix string) *errx.Error {
	originalMessageID = strings.Trim(strings.TrimSpace(originalMessageID), "<>")
	if originalMessageID == "" {
		return nil
	}

	task, err := s.taskRepo.GetTaskByMessageID(ctx, originalMessageID)
	if err != nil {
		return toErrx(err)
	}
	if task == nil {
		// Unknown message id (warmup mail, non-campaign send, or already
		// pruned) — nothing to attribute the bounce to.
		return nil
	}

	// The NDR should have landed in the same mailbox that sent it; if it didn't
	// resolve to this account, don't attribute a cross-account bounce.
	if task.EmailAccountID != emailAccountID {
		return nil
	}

	account, aerr := s.emailRepo.GetByID(ctx, emailAccountID)
	if aerr != nil {
		return aerr
	}
	if account == nil || account.OrganizationID == nil {
		return nil
	}

	req := &models.IngestDeliverabilityEventRequest{
		EventType:      eventType,
		Provider:       provider,
		TaskID:         &task.ID,
		RecipientEmail: recipient,
		Reason:         reason,
		// Same NDR re-synced (delta re-runs, reconnects) must not double-count.
		IdempotencyKey: idempotencyPrefix + originalMessageID,
	}

	ct, err := s.taskRepo.GetCampaignTask(ctx, task.ID)
	if err != nil {
		return toErrx(err)
	}
	if ct != nil {
		req.CampaignID = ct.CampaignID
		req.ContactID = ct.ContactID
		if req.RecipientEmail == "" && ct.ContactID != nil {
			contact, xerr := s.contactRepo.GetByID(ctx, *ct.ContactID)
			if xerr != nil {
				return xerr
			}
			if contact != nil {
				req.RecipientEmail = contact.Email
			}
		}
	}

	if req.RecipientEmail == "" {
		// IngestDeliverabilityEvent requires a recipient; without one we can't
		// suppress or record. Give up rather than guess.
		return nil
	}

	return s.IngestDeliverabilityEvent(ctx, *account.OrganizationID, req)
}
