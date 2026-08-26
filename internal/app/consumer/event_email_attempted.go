package jobs

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/app/confenge"
	"github.com/warmbly/warmbly/internal/models"
)

// HandleEmailAttempted projects the worker's pre-transport marker. Ordinary
// campaigns remain unaffected; controlled cohorts fail closed on a projection
// error so the provider call is never observationally invisible.
func (s *JobsService) HandleEmailAttempted(ctx context.Context, event *models.SendEmailResult) error {
	if event == nil || event.TaskID == uuid.Nil {
		return fmt.Errorf("invalid EMAIL_ATTEMPTED result")
	}
	if s.ConfengeSends == nil {
		return nil
	}
	if s.TaskRepo == nil || s.CampaignRepo == nil {
		return fmt.Errorf("CONFENGE send attempt dependencies are not configured")
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
	observeErr := s.ConfengeSends.ObserveCampaignEmailAttempt(
		ctx,
		*campaign.OrganizationID,
		*campaignTask.CampaignID,
		*campaignTask.ContactID,
		valueOrNilUUID(campaignTask.SequenceID),
		event.TaskID,
		mailboxID,
		provider,
		event.SentAt,
	)
	if errors.Is(observeErr, confenge.ErrCampaignTouchpointNotFound) && !confenge.IsConfengeCampaign(campaign.Name) {
		return nil
	}
	return observeErr
}
