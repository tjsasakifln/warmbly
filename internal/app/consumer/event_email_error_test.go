package jobs

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

type authFailureEmailRepository struct {
	repository.EmailRepository
	updateErr    *errx.Error
	updateCalls  int
	updatedState string
}

func (r *authFailureEmailRepository) Update(_ context.Context, _, _ string, update *models.UpdateEmail) (*models.Email, *errx.Error) {
	r.updateCalls++
	if update != nil && update.Status != nil {
		r.updatedState = *update.Status
	}
	return nil, r.updateErr
}

func authErrorEvent() models.EmailErrorEvent {
	return models.EmailErrorEvent{
		EmailAccountID: uuid.NewString(),
		UserID:         uuid.NewString(),
		Message:        "authentication failed",
	}
}

func TestHandleEmailAuthErrorPersistsInactiveState(t *testing.T) {
	repo := &authFailureEmailRepository{}
	service := &JobsService{EmailRepository: repo}

	if err := service.HandleEmailAuthError(context.Background(), authErrorEvent()); err != nil {
		t.Fatalf("HandleEmailAuthError error: %v", err)
	}
	if repo.updateCalls != 1 || repo.updatedState != "inactive" {
		t.Fatalf("mailbox safety write: update=%d state=%q", repo.updateCalls, repo.updatedState)
	}
}

func TestHandleEmailAuthErrorFailsClosedWithoutEmailRepository(t *testing.T) {
	if err := (&JobsService{}).HandleEmailAuthError(context.Background(), authErrorEvent()); err == nil {
		t.Fatal("expected missing email repository to prevent event acknowledgement")
	}
}

func TestHandleEmailAuthErrorRetriesWhenDeactivationFails(t *testing.T) {
	repo := &authFailureEmailRepository{updateErr: errx.InternalError()}
	service := &JobsService{EmailRepository: repo}

	err := service.HandleEmailAuthError(context.Background(), authErrorEvent())

	if err == nil {
		t.Fatal("expected deactivation failure to be returned for redelivery")
	}
	if repo.updateCalls != 1 {
		t.Fatalf("mailbox safety writes: update=%d", repo.updateCalls)
	}
}
