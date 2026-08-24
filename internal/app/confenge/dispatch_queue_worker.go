package confenge

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
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
	status, err := s.governor.Status(ctx, nil)
	if err != nil {
		return false, err
	}
	if status.Paused || !status.InSendWindow || FileKillSwitchActive() || !s.cfg.SendingAllowed() {
		return false, nil
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
	actor := uuid.Nil
	if tp.ApprovedBy != nil {
		actor = *tp.ApprovedBy
	}
	if actor == uuid.Nil {
		actor, _ = s.repo.GetOrgOwnerUserID(ctx, item.OrganizationID)
	}
	var xerr error
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
	// QueueSent means handed to the configured transport. Provider-confirmed
	// delivery remains recorded by CompleteCampaignEmail / WhatsApp completion.
	if err := s.governor.MarkQueue(ctx, item.ID, dispatch.QueueSent, ""); err != nil {
		return true, err
	}
	return true, nil
}
