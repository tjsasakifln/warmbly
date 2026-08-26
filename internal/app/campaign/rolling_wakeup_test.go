package campaign

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/repository"
	"github.com/warmbly/warmbly/internal/scheduler"
	"github.com/warmbly/warmbly/internal/tasks/proto"
)

type rollingWakeupCampaignRepo struct {
	repository.CampaignRepository
	ready    bool
	readyErr error
	statuses []string
}

func (r *rollingWakeupCampaignRepo) HasReadyDelegatedFirstTouch(context.Context, uuid.UUID) (bool, error) {
	return r.ready, r.readyErr
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
		readyErr   error
		wantErr    bool
		wantStatus string
	}{
		{name: "ready delegated queue", ready: true},
		{name: "ordinary completed campaign", wantErr: true, wantStatus: "completed"},
		{name: "queue lookup failure", readyErr: errors.New("db unavailable"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &rollingWakeupCampaignRepo{ready: tt.ready, readyErr: tt.readyErr}
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
