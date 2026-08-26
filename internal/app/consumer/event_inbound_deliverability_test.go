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

type inboundDeliverabilityRecorder struct {
	advanced.Service
	bounceErr    *errx.Error
	complaintErr *errx.Error
}

func (r inboundDeliverabilityRecorder) RecordInboundBounce(context.Context, uuid.UUID, string, string, string) *errx.Error {
	return r.bounceErr
}

func (r inboundDeliverabilityRecorder) RecordInboundComplaint(context.Context, uuid.UUID, string, string, string) *errx.Error {
	return r.complaintErr
}

func TestInboundDeliverabilityHandlersReturnPersistenceFailures(t *testing.T) {
	svc := &JobsService{AdvancedService: inboundDeliverabilityRecorder{
		bounceErr:    errx.InternalError(),
		complaintErr: errx.InternalError(),
	}}

	require.Error(t, svc.HandleInboundBounce(context.Background(), &models.JobEventInboundBounce{BounceClass: "HARD"}))
	require.Error(t, svc.HandleInboundComplaint(context.Background(), &models.JobEventInboundComplaint{}))
}

func TestInboundDeliverabilityHandlersRejectMissingPayloads(t *testing.T) {
	svc := &JobsService{AdvancedService: inboundDeliverabilityRecorder{}}

	require.Error(t, svc.HandleInboundBounce(context.Background(), nil))
	require.Error(t, svc.HandleInboundComplaint(context.Background(), nil))
	require.Error(t, svc.HandleInboundDelivery(context.Background(), nil))
}
