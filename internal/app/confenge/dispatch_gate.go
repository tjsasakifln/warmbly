package confenge

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// WireDispatch attaches the global outbound governor (email + WhatsApp share one cap).
// Safe to call with nil db (uses memory store for tests) or with pool for production.
func (s *service) WireDispatch(db *pgxpool.Pool) {
	cfg := dispatch.LoadConfig()
	var store dispatch.Store
	if db != nil {
		pg := dispatch.NewPGStore(db)
		_ = pg.EnsureControl(context.Background())
		store = pg
	} else {
		store = dispatch.NewMemoryStore()
	}
	s.governor = dispatch.NewGovernor(cfg, store, nil)
}

// WireDispatchGovernor injects a pre-built governor (tests).
func (s *service) WireDispatchGovernor(g *dispatch.Governor) {
	s.governor = g
}

// Governor returns the dispatch governor (may be nil if not wired).
func (s *service) Governor() *dispatch.Governor {
	return s.governor
}

// reserveOutbound is the final gate before provider/campaign send.
// On denial, enqueues for a future slot and returns a 429-style errx.
// On AlreadyCommitted, returns (nil, nil) and sets already=true.
func (s *service) reserveOutbound(ctx context.Context, orgID uuid.UUID, channel string, draftID uuid.UUID) (res *dispatch.Reservation, already bool, xerr *errx.Error) {
	if s.governor == nil {
		// Fail-open only when governor not wired (unit tests of unrelated paths).
		// Production always wires via WireDispatch.
		return nil, false, nil
	}
	var key string
	switch channel {
	case dispatch.ChannelWhatsApp:
		key = dispatch.MessageKeyWhatsApp(draftID)
	default:
		key = dispatch.MessageKeyEmail(draftID)
	}
	did := draftID
	result, err := s.governor.TryReserve(ctx, dispatch.ReserveRequest{
		OrganizationID: orgID,
		Channel:        channel,
		MessageKey:     key,
		DraftID:        &did,
	})
	if err != nil {
		return nil, false, errx.New(errx.Internal, "dispatch governor: "+err.Error())
	}
	if result.AlreadyCommitted {
		return nil, true, nil
	}
	if !result.Allowed {
		// Queue for fair future slot; catch-up must not burst.
		due := result.NextSlot
		if due.IsZero() {
			due = time.Now().UTC().Add(time.Duration(s.governor.Config().MinGap))
		}
		_ = s.governor.Enqueue(ctx, dispatch.EnqueueRequest{
			OrganizationID: orgID,
			Channel:        channel,
			DraftID:        draftID,
			MessageKey:     key,
			DueAt:          due,
		})
		msg := fmt.Sprintf("dispatch quota full (%s); queued until %s", result.Reason, due.UTC().Format(time.RFC3339))
		return nil, false, errx.New(errx.TooManyRequests, msg)
	}
	return result.Reservation, false, nil
}

func (s *service) commitOutbound(ctx context.Context, res *dispatch.Reservation) {
	if s.governor == nil || res == nil {
		return
	}
	_ = s.governor.Commit(ctx, res.ID)
}

func (s *service) releaseOutbound(ctx context.Context, res *dispatch.Reservation, errText string) {
	if s.governor == nil || res == nil {
		return
	}
	_ = s.governor.Release(ctx, res.ID, errText)
	_ = s.governor.RecordFailure(ctx, res.OrganizationID, res.Channel, res.MessageKey, res.DraftID, errText)
}

// DispatchStatus returns observability for GET /confenge/dispatch/status.
func (s *service) DispatchStatus(ctx context.Context, orgID uuid.UUID) (dispatch.Status, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return dispatch.Status{}, xerr
	}
	if s.governor == nil {
		return dispatch.Status{}, errx.New(errx.ServiceUnavailable, "dispatch governor not wired")
	}
	st, err := s.governor.Status(ctx, &orgID)
	if err != nil {
		return dispatch.Status{}, errx.New(errx.Internal, err.Error())
	}
	return st, nil
}

// PauseDispatch sets the durable kill switch.
func (s *service) PauseDispatch(ctx context.Context, orgID, userID uuid.UUID, reason string) *errx.Error {
	if xerr := s.requireEnabled(); xerr != nil {
		return xerr
	}
	if s.governor == nil {
		return errx.New(errx.ServiceUnavailable, "dispatch governor not wired")
	}
	by := userID
	if err := s.governor.Pause(ctx, reason, &by); err != nil {
		return errx.New(errx.Internal, err.Error())
	}
	if s.audit != nil {
		s.audit.LogAction(ctx, orgID, userID, models.AuditActionPause, models.AuditEntityOutreachAccount, nil, "", "",
			map[string]string{"action": "dispatch_pause", "reason": reason}, nil)
	}
	return nil
}

// ResumeDispatch clears the durable kill switch.
func (s *service) ResumeDispatch(ctx context.Context, orgID, userID uuid.UUID) *errx.Error {
	if xerr := s.requireEnabled(); xerr != nil {
		return xerr
	}
	if s.governor == nil {
		return errx.New(errx.ServiceUnavailable, "dispatch governor not wired")
	}
	by := userID
	if err := s.governor.Resume(ctx, &by); err != nil {
		return errx.New(errx.Internal, err.Error())
	}
	if s.audit != nil {
		s.audit.LogAction(ctx, orgID, userID, models.AuditActionResume, models.AuditEntityOutreachAccount, nil, "", "",
			map[string]string{"action": "dispatch_resume"}, nil)
	}
	return nil
}
