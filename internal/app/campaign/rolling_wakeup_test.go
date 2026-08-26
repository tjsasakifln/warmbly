package campaign

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
	"github.com/warmbly/warmbly/internal/scheduler"
	"github.com/warmbly/warmbly/internal/tasks/proto"
)

type rollingWakeupCampaignRepo struct {
	repository.CampaignRepository
	campaign   *models.Campaign
	ready      bool
	pending    bool
	readyErr   error
	pendingErr error
	statuses   []string
}

func (r *rollingWakeupCampaignRepo) GetByID(context.Context, uuid.UUID) (*models.Campaign, error) {
	return r.campaign, nil
}

func (r *rollingWakeupCampaignRepo) HasReadyDelegatedFirstTouch(context.Context, uuid.UUID) (bool, error) {
	return r.ready, r.readyErr
}

func (r *rollingWakeupCampaignRepo) HasPendingDelegatedFirstTouch(context.Context, uuid.UUID) (bool, error) {
	return r.pending, r.pendingErr
}

func (r *rollingWakeupCampaignRepo) ValidateCampaignReady(context.Context, uuid.UUID) error {
	return nil
}

func (r *rollingWakeupCampaignRepo) GetSequencesByCampaignID(context.Context, uuid.UUID) ([]models.Sequence, error) {
	return nil, nil
}

func (r *rollingWakeupCampaignRepo) CountActiveForOrganization(context.Context, uuid.UUID) (int, error) {
	return 0, nil
}

func (r *rollingWakeupCampaignRepo) StartCampaign(_ context.Context, _ uuid.UUID) error {
	r.statuses = append(r.statuses, "active")
	return nil
}

func (r *rollingWakeupCampaignRepo) UpdateStatusWithLock(_ context.Context, _ uuid.UUID, status string) error {
	r.statuses = append(r.statuses, status)
	return nil
}

type rollingWakeupTaskRepo struct{ repository.TaskRepository }

type rollingWakeupScheduler struct{}

func (rollingWakeupScheduler) CalculateNextWarmupTime(context.Context, uuid.UUID) (time.Time, error) {
	return time.Time{}, nil
}

func (rollingWakeupScheduler) CalculateNextCampaignTime(context.Context, uuid.UUID) (time.Time, *repository.ContactSequencePair, uuid.UUID, error) {
	return time.Time{}, nil, uuid.Nil, scheduler.ErrCampaignCompleted
}

func (rollingWakeupScheduler) CalculateNextEmailTime(context.Context, uuid.UUID) (time.Time, error) {
	return time.Time{}, nil
}

type rollingWakeupTaskScheduler struct{}

func (rollingWakeupTaskScheduler) CreateTask(context.Context, *proto.ProcessTask, time.Time) (string, error) {
	return "", nil
}

func (rollingWakeupTaskScheduler) DeleteTask(context.Context, string) error { return nil }

func TestEnqueueCampaignWakeupPreservesActiveRollingCampaign(t *testing.T) {
	tests := []struct {
		name       string
		ready      bool
		pending    bool
		readyErr   error
		pendingErr error
		wantErr    bool
		wantStatus string
	}{
		{name: "ready delegated queue", ready: true},
		{name: "pending current delegated backlog", pending: true},
		{name: "ordinary completed campaign", wantErr: true, wantStatus: "completed"},
		{name: "queue lookup failure", readyErr: errors.New("db unavailable"), wantErr: true},
		{name: "backlog lookup failure", pendingErr: errors.New("db unavailable"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &rollingWakeupCampaignRepo{ready: tt.ready, pending: tt.pending, readyErr: tt.readyErr, pendingErr: tt.pendingErr}
			svc := &campaignService{
				campaignRepository: repo,
				taskRepo:           &rollingWakeupTaskRepo{},
				scheduler:          rollingWakeupScheduler{},
				tasksClient:        rollingWakeupTaskScheduler{},
			}
			xerr := svc.enqueueCampaignWakeup(context.Background(), uuid.New())
			if (xerr != nil) != tt.wantErr {
				t.Fatalf("error mismatch: got=%v wantErr=%v", xerr, tt.wantErr)
			}
			if len(repo.statuses) == 0 && tt.wantStatus != "" {
				t.Fatalf("missing status update %q", tt.wantStatus)
			}
			if len(repo.statuses) > 0 && repo.statuses[0] != tt.wantStatus {
				t.Fatalf("unexpected status update: %v", repo.statuses)
			}
		})
	}
}

func TestCompletedCampaignCanRestartOnlyForDelegatedRollingWork(t *testing.T) {
	orgID, campaignID := uuid.New(), uuid.New()
	for _, tt := range []struct {
		name, status            string
		ready, pending, wantErr bool
	}{
		{name: "read-back queue", status: "completed", ready: true},
		{name: "pending current backlog", status: "completed", pending: true},
		{name: "ordinary completed", status: "completed", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := &rollingWakeupCampaignRepo{
				campaign: &models.Campaign{ID: campaignID, OrganizationID: &orgID, Status: tt.status},
				ready:    tt.ready, pending: tt.pending,
			}
			svc := &campaignService{
				campaignRepository: repo,
				taskRepo:           &rollingWakeupTaskRepo{}, scheduler: rollingWakeupScheduler{},
				tasksClient: rollingWakeupTaskScheduler{},
			}
			xerr := svc.StartCampaign(context.Background(), orgID, campaignID.String())
			if (xerr != nil) != tt.wantErr {
				t.Fatalf("start error mismatch: got=%v wantErr=%v", xerr, tt.wantErr)
			}
			if !tt.wantErr && (len(repo.statuses) != 1 || repo.statuses[0] != "active") {
				t.Fatalf("rolling campaign did not restart: %v", repo.statuses)
			}
		})
	}
}
