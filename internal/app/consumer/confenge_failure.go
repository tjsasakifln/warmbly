package jobs

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
)

func (s *JobsService) recordConfengeMailboxFailure(ctx context.Context, taskID uuid.UUID, errorCode, errorText string, occurredAt time.Time) error {
	if s == nil || s.ConfengeSends == nil || taskID == uuid.Nil {
		return nil
	}
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	return s.ConfengeSends.RecordCampaignEmailFailure(ctx, taskID, errorCode, errorText, occurredAt)
}

func (s *JobsService) recordConfengeEmailError(ctx context.Context, event models.EmailErrorEvent) error {
	taskID, err := uuid.Parse(event.TaskID)
	if err != nil {
		return nil
	}
	occurredAt := time.Unix(event.Timestamp, 0).UTC()
	if event.Timestamp <= 0 {
		occurredAt = time.Now().UTC()
	}
	return s.recordConfengeMailboxFailure(ctx, taskID, event.ErrorCode, event.Message, occurredAt)
}
