package advanced

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

type outboundAdvancedRepo struct {
	repository.AdvancedOutreachRepository
	event         *models.DeliverabilityEvent
	settingsReads int
	seen          map[string]bool
}

func (r *outboundAdvancedRepo) CreateDeliverabilityEvent(_ context.Context, event *models.DeliverabilityEvent) (bool, error) {
	if r.seen == nil {
		r.seen = make(map[string]bool)
	}
	if r.seen[event.IdempotencyKey] {
		return false, nil
	}
	r.seen[event.IdempotencyKey] = true
	copy := *event
	r.event = &copy
	return true, nil
}

func (r *outboundAdvancedRepo) GetOutreachSettings(context.Context, uuid.UUID) (*models.AdvancedOutreachSettings, error) {
	r.settingsReads++
	return &models.AdvancedOutreachSettings{}, nil
}

type outboundTaskRepo struct {
	repository.TaskRepository
	task         *repository.Task
	campaignTask *repository.CampaignTask
}

func (r outboundTaskRepo) GetTask(context.Context, uuid.UUID) (*repository.Task, error) {
	return r.task, nil
}

func (r outboundTaskRepo) GetCampaignTask(context.Context, uuid.UUID) (*repository.CampaignTask, error) {
	return r.campaignTask, nil
}

type outboundEmailRepo struct {
	repository.EmailRepository
	account *models.Email
}

func (r outboundEmailRepo) GetByID(context.Context, uuid.UUID) (*models.Email, *errx.Error) {
	return r.account, nil
}

type outboundContactRepo struct {
	repository.ContactRepository
	contact *models.Contact
}

func (r outboundContactRepo) GetByID(context.Context, uuid.UUID) (*models.Contact, *errx.Error) {
	return r.contact, nil
}

func TestRecordOutboundBounceResolvesTaskAndDeduplicatesSideEffects(t *testing.T) {
	taskID, accountID, orgID, contactID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	repo := &outboundAdvancedRepo{}
	svc := &service{
		repo: repo,
		taskRepo: outboundTaskRepo{
			task:         &repository.Task{ID: taskID, EmailAccountID: accountID},
			campaignTask: &repository.CampaignTask{TaskID: taskID, ContactID: &contactID},
		},
		emailRepo:   outboundEmailRepo{account: &models.Email{ID: accountID, OrganizationID: &orgID}},
		contactRepo: outboundContactRepo{contact: &models.Contact{ID: contactID, Email: "lead@example.com"}},
	}

	require.Nil(t, svc.RecordOutboundBounce(context.Background(), taskID, "550 5.1.1 mailbox unavailable"))
	require.Nil(t, svc.RecordOutboundBounce(context.Background(), taskID, "550 5.1.1 mailbox unavailable"))
	require.NotNil(t, repo.event)
	require.Equal(t, models.DeliverabilityEventBounce, repo.event.EventType)
	require.Equal(t, "worker_smtp", repo.event.Provider)
	require.Equal(t, "lead@example.com", repo.event.RecipientEmail)
	require.Equal(t, "smtp_reject:"+taskID.String(), repo.event.IdempotencyKey)
	require.Equal(t, 1, repo.settingsReads)
}
