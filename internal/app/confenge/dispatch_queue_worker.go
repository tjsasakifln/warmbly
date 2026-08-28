package confenge

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// DispatchQueueWorker drains only exact-hash, explicitly approved messages.
// Pause, business window and live transport validation are checked before a
// claim and again inside the transport path.
type DispatchQueueWorker struct {
	service  Service
	interval time.Duration
}

func NewDispatchQueueWorker(service Service, interval time.Duration) *DispatchQueueWorker {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &DispatchQueueWorker{service: service, interval: interval}
}

func (w *DispatchQueueWorker) Run(ctx context.Context) {
	if w == nil || w.service == nil {
		return
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		// Reconcile first: a provider outcome that arrived while this worker was
		// idle must be recorded before any new claim, so the ledger never lags
		// behind the queue it governs.
		_, _ = w.service.ReconcileAttemptedDispatches(ctx)
		_, _ = w.service.ProcessDispatchQueueOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *service) ProcessDispatchQueueOnce(ctx context.Context) (bool, error) {
	if s.governor == nil {
		return false, nil
	}
	// One resolved state, shared with the status projection, so the dispatcher
	// and the founder's dashboard can never disagree about whether transport is
	// live. Anything other than ACTIVE — including an unreadable control —
	// refuses the claim.
	if transport := s.ResolveTransportState(ctx, nil); !transport.Active {
		return false, nil
	}
	if _, err := s.governor.Status(ctx, nil); err != nil {
		return false, err
	}
	item, err := s.governor.ClaimNextQueued(ctx)
	if err != nil || item == nil {
		return false, err
	}
	tp, err := s.repo.GetTouchpointByDraft(ctx, item.OrganizationID, item.DraftID)
	if err != nil || tp == nil {
		_ = s.governor.MarkQueue(ctx, item.ID, dispatch.QueueFailed, "approved touchpoint not found")
		return true, err
	}
	if tp.State != models.TouchpointQueued || tp.ContentHash == "" || tp.ApprovedContentHash != tp.ContentHash {
		_ = s.governor.MarkQueue(ctx, item.ID, dispatch.QueueCancelled, "approval or content hash changed")
		return true, nil
	}
	if linked, reason := s.humanGateDispatchInvalidation(ctx, item.OrganizationID, tp); linked && reason != "" {
		s.invalidateHumanGateDispatch(ctx, item.OrganizationID, tp, reason)
		_ = s.governor.MarkQueue(ctx, item.ID, dispatch.QueueCancelled, reason)
		return true, nil
	}
	actor := uuid.Nil
	if tp.ApprovedBy != nil {
		actor = *tp.ApprovedBy
	}
	if actor == uuid.Nil {
		actor, _ = s.repo.GetOrgOwnerUserID(ctx, item.OrganizationID)
	}
	// Keep the concrete pointer type so a nil *errx.Error stays nil.
	var xerr *errx.Error
	if tp.Channel == models.OutreachChannelWhatsApp {
		xerr = s.dispatchWhatsAppTouch(ctx, item.OrganizationID, actor, tp)
	} else {
		xerr = s.dispatchEmailTouch(ctx, item.OrganizationID, actor, tp)
	}
	if xerr != nil {
		if item.Attempts < 5 {
			delay := 5 * time.Minute * time.Duration(1<<(item.Attempts-1))
			_ = s.governor.RetryQueue(ctx, item.ID, time.Now().UTC().Add(delay), xerr.Error())
		} else {
			_ = s.governor.MarkQueue(ctx, item.ID, dispatch.QueueFailed, xerr.Error())
		}
		return true, xerr
	}
	// The hand-off is an attempt, not a send. dispatchEmailTouch enrolls the
	// draft and leaves SENT to the consumer that sees the provider result, so
	// writing "sent" here asserted an acceptance nobody had observed. That is how
	// six queue rows came to read 'sent' with an empty provider-fact table and no
	// touchpoint ever reaching SENT. ReconcileAttemptedDispatches promotes the row
	// once a provider fact exists; until then acceptance stays UNKNOWN.
	if err := s.governor.MarkQueue(ctx, item.ID, dispatch.QueueAttempted, ""); err != nil {
		return true, err
	}
	return true, nil
}

// ReconcileAttemptedDispatches closes the loop between the queue, the touchpoint
// projection and the provider fact.
//
// An attempted row is promoted to sent only when the touchpoint reached SENT and
// carries a provider message id; a touchpoint that failed or was cancelled moves
// the row to the matching terminal state. Anything else is left alone: an
// outcome that has not been observed is not an outcome.
func (s *service) ReconcileAttemptedDispatches(ctx context.Context) (int, error) {
	if s.governor == nil {
		return 0, nil
	}
	items, err := s.governor.ListQueueByStatus(ctx, dispatch.QueueAttempted, 100)
	if err != nil {
		return 0, err
	}
	reconciled := 0
	for i := range items {
		item := items[i]
		tp, err := s.repo.GetTouchpointByDraft(ctx, item.OrganizationID, item.DraftID)
		if err != nil {
			return reconciled, err
		}
		if tp == nil {
			continue
		}
		switch {
		case tp.State == models.TouchpointSent && strings.TrimSpace(tp.ProviderMessageID) != "":
			if err := s.governor.MarkQueue(ctx, item.ID, dispatch.QueueSent, ""); err != nil {
				return reconciled, err
			}
			reconciled++
		case tp.State == models.TouchpointFailed:
			if err := s.governor.MarkQueue(ctx, item.ID, dispatch.QueueFailed, "provider_rejected"); err != nil {
				return reconciled, err
			}
			reconciled++
		case tp.State == models.TouchpointCancelled:
			if err := s.governor.MarkQueue(ctx, item.ID, dispatch.QueueCancelled, "touchpoint_cancelled"); err != nil {
				return reconciled, err
			}
			reconciled++
		case tp.State == models.TouchpointNeedsReview:
			// The touchpoint lost its approval before the outcome was observed, so
			// no outcome can ever arrive. QUEUED is deliberately not treated this
			// way: that is the healthy in-flight state while acceptance is still
			// unknown. Enqueue treats 'attempted' as terminal to prevent a
			// duplicate dispatch, so leaving the row here would block this draft
			// from ever being queued again. Fail it with a diagnosable reason; a
			// re-approval can then legitimately re-queue it.
			if err := s.governor.MarkQueue(ctx, item.ID, dispatch.QueueFailed, "touchpoint_reverted_before_provider_outcome"); err != nil {
				return reconciled, err
			}
			log.Warn().Str("queue_item_id", item.ID.String()).Str("draft_id", item.DraftID.String()).
				Str("touchpoint_state", tp.State).
				Msg("confenge: attempted dispatch orphaned by touchpoint revert")
			reconciled++
		}
	}
	return reconciled, nil
}
