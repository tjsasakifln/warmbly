package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/app/confenge"
	"github.com/warmbly/warmbly/internal/models"
)

// HandleEmailSent projects a successful provider/SMTP acceptance callback into
// CONFENGE state. It is not proof of final delivery; delivered remains UNKNOWN
// until a DSN/webhook explicitly proves it.
func (s *JobsService) HandleEmailSent(ctx context.Context, event *models.SendEmailResult) error {
	if event == nil || !event.Success {
		return fmt.Errorf("invalid EMAIL_SENT result")
	}
	if s.ConfengeSends == nil {
		return nil
	}
	if s.TaskRepo == nil || s.CampaignRepo == nil {
		return fmt.Errorf("CONFENGE send completion dependencies are not configured")
	}

	campaignTask, err := s.TaskRepo.GetCampaignTask(ctx, event.TaskID)
	if err != nil {
		return fmt.Errorf("load campaign task: %w", err)
	}
	if campaignTask == nil || campaignTask.CampaignID == nil || campaignTask.ContactID == nil {
		return nil
	}
	campaign, err := s.CampaignRepo.GetByID(ctx, *campaignTask.CampaignID)
	if err != nil {
		return fmt.Errorf("load campaign: %w", err)
	}
	if campaign == nil || campaign.OrganizationID == nil {
		return nil
	}
	task, err := s.TaskRepo.GetTask(ctx, event.TaskID)
	if err != nil {
		return fmt.Errorf("load send task: %w", err)
	}
	var mailboxID uuid.UUID
	provider := "smtp"
	if task != nil {
		mailboxID = task.EmailAccountID
		if s.EmailRepository != nil {
			if mailbox, xerr := s.EmailRepository.GetByID(ctx, mailboxID); xerr != nil {
				return fmt.Errorf("load sender mailbox: %w", xerr)
			} else if mailbox != nil {
				provider = mailbox.Provider
			}
		}
	}

	providerMessageID := strings.TrimSpace(event.ProviderMsgID)
	if providerMessageID == "" {
		providerMessageID = strings.TrimSpace(event.MessageID)
	}
	completionErr := s.ConfengeSends.CompleteCampaignEmail(
		ctx,
		*campaign.OrganizationID,
		*campaignTask.CampaignID,
		*campaignTask.ContactID,
		valueOrNilUUID(campaignTask.SequenceID),
		event.TaskID,
		mailboxID,
		providerMessageID,
		provider,
		event.SentAt,
	)
	if errors.Is(completionErr, confenge.ErrCampaignTouchpointNotFound) && !confenge.IsConfengeCampaign(campaign.Name) {
		return nil
	}
	return completionErr
}

func valueOrNilUUID(value *uuid.UUID) uuid.UUID {
	if value == nil {
		return uuid.Nil
	}
	return *value
}
