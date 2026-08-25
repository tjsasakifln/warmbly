package advanced

import (
	"context"
	"errors"
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
	createCalls   int
	settingsErr   error
	autoSuppress  bool
	suppressErr   error
	suppressCalls int
	seen          map[string]bool
}

func (r *outboundAdvancedRepo) CreateDeliverabilityEvent(_ context.Context, event *models.DeliverabilityEvent) (bool, error) {
	r.createCalls++
	if r.seen == nil {
		r.seen = make(map[string]bool)
	}
	key := event.OrganizationID.String() + ":" + event.IdempotencyKey
	if r.seen[key] {
		return false, nil
	}
	r.seen[key] = true
	copy := *event
	r.event = &copy
	return true, nil
}

func (r *outboundAdvancedRepo) GetOutreachSettings(context.Context, uuid.UUID) (*models.AdvancedOutreachSettings, error) {
	r.settingsReads++
	if r.settingsErr != nil {
		return nil, r.settingsErr
	}
	settings := &models.AdvancedOutreachSettings{}
	settings.BouncePipeline.AutoSuppressOnBounce = r.autoSuppress
	return settings, nil
}

func (r *outboundAdvancedRepo) UpsertSuppressedRecipient(context.Context, *models.SuppressedRecipient) error {
	r.suppressCalls++
	return r.suppressErr
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
	require.Equal(t, 2, repo.settingsReads)
}

func TestIngestDeliverabilityEventRetriesCoreSideEffectsAfterClaim(t *testing.T) {
	repoErr := errors.New("suppression unavailable")
	repo := &outboundAdvancedRepo{autoSuppress: true, suppressErr: repoErr}
	svc := &service{repo: repo}
	req := &models.IngestDeliverabilityEventRequest{
		EventType:      models.DeliverabilityEventBounce,
		RecipientEmail: "lead@example.com",
		IdempotencyKey: "bounce:retry-core",
	}

	orgID := uuid.New()
	require.NotNil(t, svc.IngestDeliverabilityEvent(context.Background(), orgID, req))
	repo.suppressErr = nil
	require.Nil(t, svc.IngestDeliverabilityEvent(context.Background(), orgID, req))
	require.Equal(t, 2, repo.createCalls)
	require.Equal(t, 2, repo.suppressCalls)
}

func TestIngestDeliverabilityEventLoadsSettingsBeforeClaim(t *testing.T) {
	repoErr := errors.New("settings unavailable")
	repo := &outboundAdvancedRepo{settingsErr: repoErr}
	svc := &service{repo: repo}

	xerr := svc.IngestDeliverabilityEvent(context.Background(), uuid.New(), &models.IngestDeliverabilityEventRequest{
		EventType:      models.DeliverabilityEventBounce,
		RecipientEmail: "lead@example.com",
		IdempotencyKey: "bounce:settings-first",
	})

	require.NotNil(t, xerr)
	require.Zero(t, repo.createCalls)
}
