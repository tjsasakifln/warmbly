package worker

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/warmbly/warmbly/internal/app/worker/wmail"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/infrastructure/codec"
	"github.com/warmbly/warmbly/internal/infrastructure/eventbus"
	"github.com/warmbly/warmbly/internal/models"
)

type capturedWorkerBus struct {
	payloads [][]byte
	keys     []string
}

func (b *capturedWorkerBus) Publish(_ context.Context, _, key string, payload []byte) error {
	b.keys = append(b.keys, key)
	b.payloads = append(b.payloads, payload)
	return nil
}

func (b *capturedWorkerBus) Subscribe(context.Context, []string, string, eventbus.Handler) error {
	return nil
}

func (b *capturedWorkerBus) Close() error { return nil }
func (b *capturedWorkerBus) Name() string { return "capture" }

type capturedJobEvent struct {
	Type models.JobEventType `json:"type"`
	Body json.RawMessage     `json:"body"`
}

func TestSendEmailErrorPublishesTypedAuthPayloadOnce(t *testing.T) {
	bus := &capturedWorkerBus{}
	svc := &WorkerService{Bus: bus, Codec: codec.NewJSON()}
	taskID, emailID, userID := uuid.New(), uuid.New(), uuid.New()

	svc.sendEmailError(taskID, emailID, &wmail.WMail{UserID: userID}, errx.ErrMailAuthenticationFailed)

	require.Len(t, bus.payloads, 1)
	require.Equal(t, emailID.String(), bus.keys[0])
	var event capturedJobEvent
	require.NoError(t, json.Unmarshal(bus.payloads[0], &event))
	require.Equal(t, models.JobEventTypeEmailAuthError, event.Type)
	var body models.EmailErrorEvent
	require.NoError(t, json.Unmarshal(event.Body, &body))
	require.Equal(t, taskID.String(), body.TaskID)
	require.Equal(t, emailID.String(), body.EmailAccountID)
	require.Equal(t, userID.String(), body.UserID)
}

func TestSendEmailErrorPublishesRecipientFailureForBounceBridge(t *testing.T) {
	bus := &capturedWorkerBus{}
	svc := &WorkerService{Bus: bus, Codec: codec.NewJSON()}
	taskID, emailID, userID := uuid.New(), uuid.New(), uuid.New()

	svc.sendEmailError(taskID, emailID, &wmail.WMail{UserID: userID}, errx.ErrMailRecipientRejected)

	require.Len(t, bus.payloads, 1)
	require.Equal(t, taskID.String(), bus.keys[0])
	var event capturedJobEvent
	require.NoError(t, json.Unmarshal(bus.payloads[0], &event))
	require.Equal(t, models.JobEventTypeEmailFailed, event.Type)
	var body models.SendEmailResult
	require.NoError(t, json.Unmarshal(event.Body, &body))
	require.Equal(t, taskID, body.TaskID)
	require.NotNil(t, body.Error)
	require.Equal(t, string(errx.MailErrorCodeRecipientRejected), body.Error.Code)
}
