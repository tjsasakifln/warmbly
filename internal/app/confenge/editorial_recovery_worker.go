package confenge

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/warmbly/warmbly/internal/models"
)

const (
	editorialRecoveryLease    = 5 * time.Minute
	editorialRecoveryMaxBurst = 100
)

type editorialRecoveryProcessor interface {
	ProcessEditorialRecoveryOnce(context.Context) (bool, error)
}

// EditorialRecoveryWorker gives recoverable drafts an asynchronous AI rewrite
// opportunity. It never approves, queues or sends: a successful rewrite goes
// back to NEEDS_REVIEW for an explicit human decision.
type EditorialRecoveryWorker struct {
	processor editorialRecoveryProcessor
	interval  time.Duration
}

func NewEditorialRecoveryWorker(processor editorialRecoveryProcessor, interval time.Duration) *EditorialRecoveryWorker {
	if interval <= 0 {
		interval = time.Minute
	}
	return &EditorialRecoveryWorker{processor: processor, interval: interval}
}

func (w *EditorialRecoveryWorker) Run(ctx context.Context) {
	if w == nil || w.processor == nil {
		return
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		for i := 0; i < editorialRecoveryMaxBurst; i++ {
			processed, err := w.processor.ProcessEditorialRecoveryOnce(ctx)
			if !processed {
				_ = err
				break
			}
			select {
			case <-ctx.Done():
				return
			default:
			}
		}
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

// wakeEligibleEnrichmentRecovery advances stale retry timers only after target and recipient blockers disappear.
func (s *service) wakeEligibleEnrichmentRecovery(ctx context.Context, orgID uuid.UUID) (int, error) {
	if s == nil || s.humanGateDB == nil {
		return 0, nil
	}
	result, err := s.humanGateDB.Exec(ctx, `
		UPDATE outreach_touchpoints t
		SET editorial_retry_at=now(),
			editorial_reserved_until=NULL,
			stop_reason='eligibility restored by feed sync; editorial recovery due now',
			updated_at=now()
		FROM outreach_accounts a
		WHERE t.organization_id=$1
		  AND t.state='ENRICHMENT_PENDING'
		  AND (t.editorial_retry_at > now() OR t.editorial_reserved_until IS NOT NULL)
		  AND a.organization_id=t.organization_id
		  AND a.id=t.account_id
		  AND a.target_fit_fresh=true
		  AND a.target_fit_eligible=true
		  AND a.blocked=false
		  AND a.do_not_contact=false
		  AND EXISTS (
			SELECT 1
			FROM outreach_contact_candidates c
			WHERE c.organization_id=t.organization_id
			  AND c.account_id=t.account_id
			  AND c.email <> ''
			  AND c.blocked=false
			  AND c.do_not_contact=false
			  AND c.bounced=false
			  AND (
				(c.email_send_ready=true
				  AND c.mailbox_purpose_send_blocked=false
				  AND c.verification_status NOT IN (
					'CANDIDATE_UNVERIFIED','NOT_FOUND','INVALID','BOUNCED','DO_NOT_CONTACT'
				  ))
				OR (
				  c.discovery_json @> '{"controlled_email_eligible":true}'::jsonb
				  AND upper(COALESCE(c.discovery_json->>'route_class','')) IN (
					'DIRECT_PERSON','ROLE_OR_DEPARTMENT','GENERIC_COMPANY','PUBLIC_COMPANY_FREEMAIL'
				  )
				)
			  )
		  )`, orgID)
	if err != nil {
		return 0, err
	}
	return int(result.RowsAffected()), nil
}
