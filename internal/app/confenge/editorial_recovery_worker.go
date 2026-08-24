package confenge

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/warmbly/warmbly/internal/models"
)

const editorialRecoveryLease = 5 * time.Minute

// EditorialRecoveryWorker gives recoverable drafts an asynchronous AI rewrite
// opportunity. It never approves, queues or sends: a successful rewrite goes
// back to NEEDS_REVIEW for an explicit human decision.
type EditorialRecoveryWorker struct {
	service  Service
	interval time.Duration
}

func NewEditorialRecoveryWorker(service Service, interval time.Duration) *EditorialRecoveryWorker {
	if interval <= 0 {
		interval = time.Minute
	}
	return &EditorialRecoveryWorker{service: service, interval: interval}
}

func (w *EditorialRecoveryWorker) Run(ctx context.Context) {
	if w == nil || w.service == nil {
		return
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		_, _ = w.service.ProcessEditorialRecoveryOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *service) ProcessEditorialRecoveryOnce(ctx context.Context) (bool, error) {
	if s == nil || s.humanGateDB == nil {
		return false, nil
	}
	now := time.Now().UTC()
	var orgID, touchpointID uuid.UUID
	var state string
	var attempts int
	err := s.humanGateDB.QueryRow(ctx, `
		WITH next AS (
			SELECT id
			FROM outreach_touchpoints
			WHERE (state = 'ENRICHMENT_PENDING'
			       OR ($3 AND state IN ('AI_REWRITE_PENDING','REJECTED_REWRITE_PENDING')))
			  AND editorial_retry_at <= $1
			  AND (editorial_reserved_until IS NULL OR editorial_reserved_until <= $1)
			ORDER BY editorial_retry_at, created_at, id
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE outreach_touchpoints t
		SET editorial_reserved_until=$2, editorial_attempts=t.editorial_attempts+1, updated_at=$1
		FROM next
		WHERE t.id=next.id
		RETURNING t.organization_id,t.id,t.state,t.editorial_attempts`, now, now.Add(editorialRecoveryLease), s.ai != nil).Scan(&orgID, &touchpointID, &state, &attempts)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	actorID, err := s.repo.GetOrgOwnerUserID(ctx, orgID)
	if err != nil || actorID == uuid.Nil {
		s.deferEditorialRecovery(ctx, touchpointID, attempts, "organization owner unavailable")
		return true, err
	}
	tp, xerr := s.GenerateTouchpointDraft(ctx, orgID, actorID, touchpointID)
	if xerr != nil {
		s.deferEditorialRecovery(ctx, touchpointID, attempts, xerr.Message)
		return true, xerr
	}
	if tp == nil || tp.State == models.TouchpointAIRewritePending || tp.State == models.TouchpointEnrichmentPending || tp.State == models.TouchpointRejectedRewritePending {
		s.deferEditorialRecovery(ctx, touchpointID, attempts, "recovery did not clear "+state)
		return true, nil
	}
	_, err = s.humanGateDB.Exec(ctx, `
		UPDATE outreach_touchpoints
		SET editorial_reserved_until=NULL, editorial_retry_at=now(), updated_at=now()
		WHERE id=$1`, touchpointID)
	return true, err
}

func (s *service) deferEditorialRecovery(ctx context.Context, touchpointID uuid.UUID, attempts int, reason string) {
	delay := 15 * time.Minute
	for i := 1; i < attempts && delay < 24*time.Hour; i++ {
		delay *= 2
	}
	if delay > 24*time.Hour {
		delay = 24 * time.Hour
	}
	_, _ = s.humanGateDB.Exec(ctx, `
		UPDATE outreach_touchpoints
		SET editorial_reserved_until=NULL, editorial_retry_at=$2,
			stop_reason=LEFT($3,500), updated_at=now()
		WHERE id=$1`, touchpointID, time.Now().UTC().Add(delay), reason)
}
