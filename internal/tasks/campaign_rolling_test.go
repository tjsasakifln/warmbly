package tasks

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
	"github.com/warmbly/warmbly/internal/scheduler"
	"github.com/warmbly/warmbly/internal/tasks/proto"
)

type rollingCampaignRepo struct {
	repository.CampaignRepository
	campaign *models.Campaign
	ready    bool
	pending  bool
	statuses []string
}

func (r *rollingCampaignRepo) ListCampaignScheduleCandidates(context.Context, int) ([]uuid.UUID, error) {
	return []uuid.UUID{r.campaign.ID}, nil
}

func (r *rollingCampaignRepo) GetByID(context.Context, uuid.UUID) (*models.Campaign, error) {
	return r.campaign, nil
}

func (r *rollingCampaignRepo) HasReadyDelegatedFirstTouch(context.Context, uuid.UUID) (bool, error) {
	return r.ready, nil
}

func (r *rollingCampaignRepo) HasPendingDelegatedFirstTouch(context.Context, uuid.UUID) (bool, error) {
	return r.pending, nil
}

func (r *rollingCampaignRepo) UpdateStatus(_ context.Context, _ uuid.UUID, status string) error {
	r.statuses = append(r.statuses, status)
	return nil
}

type rollingTaskRepo struct {
	repository.TaskRepository
	taskID     uuid.UUID
	campaignID uuid.UUID
	statuses   []string
}

func (r *rollingTaskRepo) CancelOverduePendingTasks(context.Context, string, time.Duration) (int64, error) {
	return 0, nil
}

func (r *rollingTaskRepo) GetTask(context.Context, uuid.UUID) (*repository.Task, error) {
	return &repository.Task{ID: r.taskID, Status: "pending"}, nil
}

func (r *rollingTaskRepo) GetCampaignTask(context.Context, uuid.UUID) (*repository.CampaignTask, error) {
	return &repository.CampaignTask{TaskID: r.taskID, CampaignID: &r.campaignID}, nil
}

func (r *rollingTaskRepo) UpdateTaskStatus(_ context.Context, _ uuid.UUID, status string) error {
	r.statuses = append(r.statuses, status)
	return nil
}

func (r *rollingTaskRepo) UpdateTaskStatusWithLock(_ context.Context, _ uuid.UUID, status string) error {
	r.statuses = append(r.statuses, status)
	return nil
}

type rollingProgressRepo struct {
	repository.CampaignProgressRepository
}

func (rollingProgressRepo) GetCampaignProgress(context.Context, uuid.UUID) (*repository.CampaignProgress, error) {
	return &repository.CampaignProgress{}, nil
}

type rollingCompletedScheduler struct{}

func (rollingCompletedScheduler) CalculateNextWarmupTime(context.Context, uuid.UUID) (time.Time, error) {
	return time.Time{}, nil
}

func (rollingCompletedScheduler) CalculateNextCampaignTime(context.Context, uuid.UUID) (time.Time, *repository.ContactSequencePair, uuid.UUID, error) {
	return time.Time{}, nil, uuid.Nil, scheduler.ErrCampaignCompleted
}

func (rollingCompletedScheduler) CalculateNextEmailTime(context.Context, uuid.UUID) (time.Time, error) {
	return time.Time{}, nil
}

func TestCampaignReconcilerPreservesReadyRollingQueue(t *testing.T) {
	campaign := &models.Campaign{ID: uuid.New(), Status: "active"}
	for _, tt := range []struct {
		name, wantStatus string
		ready, pending   bool
	}{
		{name: "read-back queue", ready: true},
		{name: "current pending backlog", pending: true},
		{name: "exhausted", wantStatus: "completed"},
	} {
		repo := &rollingCampaignRepo{campaign: campaign, ready: tt.ready, pending: tt.pending}
		svc := &tasksService{
			campaignRepo: repo,
			taskRepo:     &rollingTaskRepo{},
			scheduler:    rollingCompletedScheduler{},
		}
		if _, err := svc.ReconcileCampaignSchedules(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
		if (tt.ready || tt.pending) && len(repo.statuses) != 0 {
			t.Fatalf("rolling campaign with work was completed: %v", repo.statuses)
		}
		if tt.wantStatus != "" && (len(repo.statuses) != 1 || repo.statuses[0] != tt.wantStatus) {
			t.Fatalf("ordinary exhausted campaign did not complete: %v", repo.statuses)
		}
	}
}

func TestCampaignTaskPreservesReadyRollingQueue(t *testing.T) {
	for _, tt := range []struct {
		name, wantStatus string
		ready, pending   bool
	}{
		{name: "read-back queue", ready: true},
		{name: "current pending backlog", pending: true},
		{name: "exhausted", wantStatus: "completed"},
	} {
		campaignID, taskID := uuid.New(), uuid.New()
		repo := &rollingCampaignRepo{campaign: &models.Campaign{ID: campaignID, Status: "active"}, ready: tt.ready, pending: tt.pending}
		tasks := &rollingTaskRepo{taskID: taskID, campaignID: campaignID}
		svc := &tasksService{
			campaignRepo:         repo,
			taskRepo:             tasks,
			campaignProgressRepo: rollingProgressRepo{},
			scheduler:            rollingCompletedScheduler{},
		}
		if xerr := svc.HandleCampaignTask(&proto.ProcessTask{TaskId: taskID.String()}); xerr != nil {
			t.Fatalf("handler failed: %v", xerr)
		}
		if (tt.ready || tt.pending) && len(repo.statuses) != 0 {
			t.Fatalf("rolling campaign with work was completed: %v", repo.statuses)
		}
		if tt.wantStatus != "" && (len(repo.statuses) != 1 || repo.statuses[0] != tt.wantStatus) {
			t.Fatalf("ordinary exhausted campaign did not complete: %v", repo.statuses)
		}
		if got := tasks.statuses[len(tasks.statuses)-1]; got != "completed" {
			t.Fatalf("task did not close cleanly: %v", tasks.statuses)
		}
	}
}
