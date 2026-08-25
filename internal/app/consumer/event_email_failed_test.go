package jobs

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/warmbly/warmbly/internal/app/advanced"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

type outboundBounceRecorder struct {
	advanced.Service
	taskID uuid.UUID
	reason string
	calls  int
	err    *errx.Error
}

func (r *outboundBounceRecorder) RecordOutboundBounce(_ context.Context, taskID uuid.UUID, reason string) *errx.Error {
	r.calls++
	r.taskID = taskID
	r.reason = reason
	return r.err
}

func TestHandleEmailFailedRecordsPermanentRecipientRejection(t *testing.T) {
	taskID := uuid.New()
	recorder := &outboundBounceRecorder{}
	svc := &JobsService{AdvancedService: recorder}

	err := svc.HandleEmailFailed(context.Background(), &models.SendEmailResult{
		TaskID: taskID,
		Error: &models.EmailSendError{
			Code:    string(errx.MailErrorCodeRecipientRejected),
			Message: "550 5.1.1 mailbox unavailable",
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, recorder.calls)
	require.Equal(t, taskID, recorder.taskID)
	require.Equal(t, "550 5.1.1 mailbox unavailable", recorder.reason)
}

func TestHandleEmailFailedIgnoresNonRecipientFailure(t *testing.T) {
	recorder := &outboundBounceRecorder{}
	svc := &JobsService{AdvancedService: recorder}

	require.NoError(t, svc.HandleEmailFailed(context.Background(), &models.SendEmailResult{
		TaskID: uuid.New(),
		Error:  &models.EmailSendError{Code: string(errx.MailErrorCodeServerUnreachable)},
	}))
	require.Zero(t, recorder.calls)
}

func TestHandleEmailFailedRetriesDeliverabilityFailure(t *testing.T) {
	recorder := &outboundBounceRecorder{err: errx.InternalError()}
	svc := &JobsService{AdvancedService: recorder}

	err := svc.HandleEmailFailed(context.Background(), &models.SendEmailResult{
		TaskID: uuid.New(),
		Error:  &models.EmailSendError{Code: string(errx.MailErrorCodeRecipientRejected)},
	})

	require.Error(t, err)
	require.Equal(t, 1, recorder.calls)
}
