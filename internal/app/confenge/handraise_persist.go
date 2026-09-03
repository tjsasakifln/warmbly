package confenge

import (
	"context"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// PersistHandRaise converges one signal and files it on the commercial-action
// surface the Control Center already reads.
//
// It is the single writer for every engine, so engine_lane is stamped in one
// place. Convergence is idempotent on HandRaiseIdempotencyKey: the same person
// raising their hand twice through the same engine is one row, and an existing
// row is returned untouched rather than overwritten, because a human may
// already have worked it.
//
// A nil action with a nil error means the signal did not converge (unknown
// signal, or no organization/account to file against). That is a legible
// no-op, never an invented row.
func (s *service) PersistHandRaise(ctx context.Context, raise HandRaise) (*models.OutreachCommercialAction, *errx.Error) {
	if s == nil {
		return nil, errx.New(errx.Internal, "confenge service is not available")
	}
	action := ConvergeHandRaise(raise)
	if action == nil {
		return nil, nil
	}
	store := s.actionStore()
	if store == nil {
		return nil, errx.New(errx.Internal, "commercial action store is not wired")
	}
	if existing, err := store.GetCommercialActionByIdempotency(ctx, action.OrganizationID, action.IdempotencyKey); err == nil && existing != nil {
		return existing, nil
	}
	if err := store.UpsertCommercialAction(ctx, action); err != nil {
		return nil, errx.New(errx.Internal, "persist hand raise: "+err.Error())
	}
	return action, nil
}
