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
	draftGenerationLease    = 5 * time.Minute
	draftGenerationMaxBurst = 100
)

type draftGenerationProcessor interface {
	ProcessDraftGenerationOnce(context.Context) (bool, error)
}

// DraftGenerationWorker drains contact-ready accounts into first-touch drafts.
// It has no approval, scheduling, queueing or transport authority itself: a
// separate delegated-policy evaluator may authorize an eligible first touch,
// while every exception remains on the human review surface.
type DraftGenerationWorker struct {
	processor draftGenerationProcessor
	interval  time.Duration
}

func NewDraftGenerationWorker(processor draftGenerationProcessor, interval time.Duration) *DraftGenerationWorker {
	if interval <= 0 {
		interval = time.Minute
	}
	return &DraftGenerationWorker{processor: processor, interval: interval}
}

func (w *DraftGenerationWorker) Run(ctx context.Context) {
	if w == nil || w.processor == nil {
		return
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		for i := 0; i < draftGenerationMaxBurst; i++ {
			processed, err := w.processor.ProcessDraftGenerationOnce(ctx)
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

// ProcessDraftGenerationOnce atomically leases one eligible account, creates
// its idempotent cadence, and generates only the first touchpoint. Every
// approval and transport gate remains downstream and intact.
func (s *service) ProcessDraftGenerationOnce(ctx context.Context) (bool, error) {
	if s == nil || s.humanGateDB == nil {
		return false, nil
	}
	s.reviewBacklogMu.Lock()
	defer s.reviewBacklogMu.Unlock()
	now := time.Now().UTC()
	var orgID, accountID uuid.UUID
	var attempts int
	err := s.humanGateDB.QueryRow(ctx, `
		WITH next AS (
			SELECT a.id
			FROM outreach_accounts a
			JOIN outreach_feed_sync_state feed
			  ON feed.organization_id=a.organization_id
			WHERE a.queue_state = 'READY_TO_GENERATE'
			  AND feed.last_status='completed'
			  AND a.source_run_id=feed.last_run_id
			  AND (
				SELECT count(*)
				FROM outreach_touchpoints review_backlog
				JOIN outreach_accounts review_account
				  ON review_account.organization_id=review_backlog.organization_id
				 AND review_account.id=review_backlog.account_id
				JOIN outreach_feed_sync_state review_feed
				  ON review_feed.organization_id=review_backlog.organization_id
				WHERE review_backlog.organization_id=a.organization_id
				  AND review_backlog.ordinal=1
				  AND review_backlog.state='NEEDS_REVIEW'
				  AND review_feed.last_status='completed'
				  AND review_account.source_run_id=review_feed.last_run_id
				  AND NOT EXISTS (
					SELECT 1
					FROM confenge_delegated_first_touch_decisions decision
					WHERE decision.organization_id=review_backlog.organization_id
					  AND decision.account_id=review_backlog.account_id
					  AND decision.evidence_source_run_id=review_account.source_run_id
				  )
			  ) < $3
			  AND a.target_fit_eligible = true
			  AND a.blocked = false
			  AND a.do_not_contact = false
			  AND EXISTS (
				SELECT 1
				FROM outreach_contact_candidates c
				WHERE c.organization_id = a.organization_id
				  AND c.account_id = a.id
				  AND (a.last_import_run_id IS NULL OR c.last_import_run_id = a.last_import_run_id)
				  AND c.email <> ''
				  AND c.blocked = false
				  AND c.do_not_contact = false
				  AND c.bounced = false
				  AND (
					(c.email_send_ready = true
					  AND c.mailbox_purpose_send_blocked = false
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
			  )
			  AND a.draft_generation_retry_at <= $1
			  AND (a.draft_generation_reserved_until IS NULL OR a.draft_generation_reserved_until <= $1)
			  AND NOT EXISTS (
				SELECT 1
				FROM outreach_touchpoints t
				WHERE t.organization_id = a.organization_id
				  AND t.account_id = a.id
				  AND t.state IN (
					'AI_REWRITE_PENDING','ENRICHMENT_PENDING','REJECTED_REWRITE_PENDING',
					'NEEDS_REVIEW','APPROVED','QUEUED'
				  )
			  )
			  AND NOT ($4 AND EXISTS (
				SELECT 1 FROM outreach_touchpoints prepared
				WHERE prepared.organization_id=a.organization_id AND prepared.account_id=a.id
				  AND prepared.ordinal=1 AND prepared.purpose='INITIAL' AND prepared.channel='EMAIL'
				  AND prepared.source_run_id=feed.last_run_id AND prepared.state='DUE'
			  ))
			ORDER BY a.draft_generation_retry_at, a.created_at, a.id
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE outreach_accounts a
		SET draft_generation_reserved_until = $2,
			draft_generation_attempts = a.draft_generation_attempts + 1,
			updated_at = $1
		FROM next
		WHERE a.id = next.id
		RETURNING a.organization_id, a.id, a.draft_generation_attempts`, now, now.Add(draftGenerationLease),
		s.draftReviewBacklogTarget(), s.cfg.DelegatedFirstTouchEnabled).Scan(&orgID, &accountID, &attempts)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}

	actorID, err := s.repo.GetOrgOwnerUserID(ctx, orgID)
	if err != nil || actorID == uuid.Nil {
		s.deferDraftGeneration(ctx, accountID, attempts, "organization owner unavailable")
		return true, err
	}
	touchpoints, xerr := s.PlanAccountCadence(ctx, orgID, actorID, accountID, nil, models.OutreachChannelEmail)
	if xerr != nil {
		s.deferDraftGeneration(ctx, accountID, attempts, xerr.Message)
		return true, xerr
	}
	var touchpointID uuid.UUID
	for i := range touchpoints {
		if touchpoints[i].State == models.TouchpointDue || touchpoints[i].State == models.TouchpointDrafted {
			touchpointID = touchpoints[i].ID
			break
		}
	}
	if touchpointID == uuid.Nil {
		s.deferDraftGeneration(ctx, accountID, attempts, "no due touchpoint available for generation")
		return true, nil
	}

	tp, xerr := s.GenerateTouchpointDraft(ctx, orgID, actorID, touchpointID)
	if xerr != nil {
		s.deferDraftGeneration(ctx, accountID, attempts, xerr.Message)
		return true, xerr
	}
	if tp == nil {
		s.deferDraftGeneration(ctx, accountID, attempts, "generation returned no touchpoint")
		return true, nil
	}
	switch tp.State {
	case models.TouchpointNeedsReview,
		models.TouchpointAIRewritePending,
		models.TouchpointEnrichmentPending,
		models.TouchpointRejectedRewritePending:
		_, err = s.humanGateDB.Exec(ctx, `
			UPDATE outreach_accounts
			SET draft_generation_reserved_until = NULL,
				draft_generation_retry_at = now(),
				draft_generation_last_error = '',
				updated_at = now()
			WHERE id = $1`, accountID)
		return true, err
	default:
		s.deferDraftGeneration(ctx, accountID, attempts, "generation stopped in state "+tp.State)
		return true, nil
	}
}

func draftGenerationRetryDelay(attempts int) time.Duration {
	delay := 15 * time.Minute
	for i := 1; i < attempts && delay < 24*time.Hour; i++ {
		delay *= 2
	}
	if delay > 24*time.Hour {
		return 24 * time.Hour
	}
	return delay
}

func (s *service) deferDraftGeneration(ctx context.Context, accountID uuid.UUID, attempts int, reason string) {
	_, _ = s.humanGateDB.Exec(ctx, `
		UPDATE outreach_accounts
		SET draft_generation_reserved_until = NULL,
			draft_generation_retry_at = $2,
			draft_generation_last_error = LEFT($3, 500),
			updated_at = now()
		WHERE id = $1`, accountID, time.Now().UTC().Add(draftGenerationRetryDelay(attempts)), reason)
}
