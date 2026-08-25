package orgrisk

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

type orgRiskRepoStub struct {
	repository.OrgRiskRepository
	risk *models.OrganizationRisk
	err  error
}

func (r orgRiskRepoStub) Get(context.Context, uuid.UUID) (*models.OrganizationRisk, error) {
	return r.risk, r.err
}

func TestRiskPolicyFailsClosedWhenRiskStateCannotBeLoaded(t *testing.T) {
	svc := NewService(orgRiskRepoStub{err: errors.New("database unavailable")}, nil)
	orgID := uuid.New()

	require.Zero(t, svc.EffectiveCap(context.Background(), orgID, 50))
	require.Equal(t, "free", svc.WarmupPool(context.Background(), orgID, "premium"))
	require.True(t, svc.SendingSuspended(context.Background(), orgID))
}

func TestRiskPolicyPreservesDefaultsForOrganizationWithoutRiskState(t *testing.T) {
	svc := NewService(orgRiskRepoStub{}, nil)
	orgID := uuid.New()

	require.Equal(t, 50, svc.EffectiveCap(context.Background(), orgID, 50))
	require.Equal(t, "premium", svc.WarmupPool(context.Background(), orgID, "premium"))
	require.False(t, svc.SendingSuspended(context.Background(), orgID))
}
