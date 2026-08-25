package confenge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

func humanGateTouchpointID(versionID, candidateID uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(HumanGateContractV1+"\x00touchpoint\x00"+versionID.String()+"\x00"+candidateID.String()))
}

func humanGateDraftID(versionID, candidateID, reviewID uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(HumanGateContractV1+"\x00draft\x00"+versionID.String()+"\x00"+candidateID.String()+"\x00"+reviewID.String()))
}

func humanGateTouchpointKey(versionID, candidateID uuid.UUID) string {
	return "human-gate:" + versionID.String() + ":" + candidateID.String()
}

func (s *service) humanGateScheduling(ctx context.Context, orgID, versionID, candidateID uuid.UUID) *HumanGateScheduling {
	if s == nil || s.humanGateDB == nil {
		return nil
	}
	row := s.humanGateDB.QueryRow(ctx, `
		SELECT d.touchpoint_id,d.draft_id,t.state,d.auto_send,d.due_at,d.scheduled_at,
		       d.invalidated_at,d.invalidation_reason
		FROM confenge_cohort_candidate_dispatches d
		JOIN outreach_touchpoints t ON t.id=d.touchpoint_id AND t.organization_id=d.organization_id
		WHERE d.organization_id=$1 AND d.cohort_version_id=$2 AND d.candidate_id=$3`,
		orgID, versionID, candidateID)
	out := &HumanGateScheduling{}
	if err := row.Scan(&out.TouchpointID, &out.DraftID, &out.State, &out.AutoSend, &out.DueAt,
		&out.ScheduledAt, &out.InvalidatedAt, &out.InvalidationReason); err != nil {
		return nil
	}
	return out
}

func (s *service) persistHumanGateScheduling(
	ctx context.Context,
	orgID, versionID, candidateID, reviewID uuid.UUID,
	tp *models.OutreachTouchpoint,
) error {
	if s == nil || s.humanGateDB == nil || tp == nil || tp.DraftID == nil {
		return fmt.Errorf("human-gate scheduling persistence is unavailable")
	}
	now := time.Now().UTC()
	_, err := s.humanGateDB.Exec(ctx, `
		INSERT INTO confenge_cohort_candidate_dispatches(
			organization_id,cohort_version_id,candidate_id,review_id,touchpoint_id,draft_id,
			auto_send,due_at,scheduled_at,updated_at
		) VALUES($1,$2,$3,$4,$5,$6,true,$7,$8,$8)
		ON CONFLICT (cohort_version_id,candidate_id) DO UPDATE SET
			review_id=EXCLUDED.review_id,
			touchpoint_id=EXCLUDED.touchpoint_id,
			draft_id=EXCLUDED.draft_id,
			auto_send=true,
			due_at=EXCLUDED.due_at,
			scheduled_at=CASE
				WHEN confenge_cohort_candidate_dispatches.touchpoint_id=EXCLUDED.touchpoint_id
				THEN confenge_cohort_candidate_dispatches.scheduled_at
				ELSE EXCLUDED.scheduled_at END,
			invalidated_at=NULL,invalidation_reason='',updated_at=EXCLUDED.updated_at`,
		orgID, versionID, candidateID, reviewID, tp.ID, *tp.DraftID, tp.DueAt.UTC(), now)
	return err
}

func (s *service) ensureHumanGateDraft(
	ctx context.Context,
	orgID, versionID, reviewID, actorID uuid.UUID,
	m FrozenCohortMember,
	tp *models.OutreachTouchpoint,
) error {
	if tp == nil {
		return fmt.Errorf("nil touchpoint")
	}
	draftID := humanGateDraftID(versionID, m.CandidateID, reviewID)
	if tp.DraftID != nil && *tp.DraftID != draftID {
		if previous, err := s.repo.GetDraft(ctx, orgID, *tp.DraftID); err == nil && previous != nil &&
			(previous.Status == models.OutreachDraftApproved || previous.Status == models.OutreachDraftNeedsReview) {
			previous.Status = models.OutreachDraftRejected
			previous.ApprovedBy = nil
			previous.ApprovedAt = nil
			if err := s.repo.UpdateDraftStatus(ctx, previous); err != nil {
				return err
			}
		}
	}
	d, _ := s.repo.GetDraft(ctx, orgID, draftID)
	now := time.Now().UTC()
	if d == nil {
		d = &models.OutreachDraft{
			ID:             draftID,
			OrganizationID: orgID,
			AccountID:      tp.AccountID,
			CreatedAt:      now,
		}
	}
	candidateID := m.CandidateID
	d.ContactCandidateID = &candidateID
	d.Channel = models.OutreachChannelEmail
	d.RecipientEmail = m.Mailbox
	d.VerificationStatus = "VALID"
	d.Subject = m.Subject
	d.BodyText = m.BodyText
	d.ServiceCode = m.ServiceCode
	d.FactUsed = firstNonEmpty(m.ObservedFact, m.SourceFact)
	d.CTA = m.CTA
	d.Provider = "human-gate"
	d.PromptVersion = PromptVersion + "+human-gate"
	d.RiskClass = "YELLOW"
	d.Status = models.OutreachDraftApproved
	d.ApprovedBy = &actorID
	d.ApprovedAt = &now
	if acc, err := s.repo.GetAccount(ctx, orgID, tp.AccountID); err == nil && acc != nil {
		d.ServiceCode = firstNonEmpty(d.ServiceCode, acc.ServiceCode)
		d.FactUsed = firstNonEmpty(d.FactUsed, controlledDraftFact(acc))
	}
	if err := s.repo.UpsertDraft(ctx, d); err != nil {
		return err
	}
	id := d.ID
	tp.DraftID = &id
	return nil
}

// scheduleHumanGateApproval is the one code path for a new APPROVE and for
// reconciliation. It converges concurrent approvals on deterministic ids and
// the dispatch queue's message-key uniqueness.
func (s *service) scheduleHumanGateApproval(
	ctx context.Context,
	orgID, versionID, candidateID, reviewID, actorID uuid.UUID,
) (bool, *models.OutreachTouchpoint, *errx.Error) {
	if s == nil || s.humanGateDB == nil || s.repo == nil {
		return false, nil, humanGateError(errx.ServiceUnavailable, "human_gate_store_unavailable", "human-gate scheduling store is unavailable")
	}
	if s.governor == nil {
		return false, nil, humanGateError(errx.ServiceUnavailable, "dispatch_governor_unavailable", "APPROVE was not accepted because durable scheduling is unavailable")
	}
	v, xerr := s.GetHumanGateCohort(ctx, orgID, versionID, time.Now().UTC())
	if xerr != nil {
		return false, nil, xerr
	}
	if !v.Actionable || !v.IsCurrentVersion {
		return false, nil, humanGateError(errx.Conflict, "approval_not_schedulable", "the approved cohort version is not current")
	}
	var candidate *HumanGateCandidate
	for i := range v.Candidates {
		if v.Candidates[i].CandidateID == candidateID {
			candidate = &v.Candidates[i]
			break
		}
	}
	if candidate == nil {
		return false, nil, humanGateError(errx.NotFound, "candidate_not_found", "candidate is not in this immutable version")
	}
	if candidate.Review == nil || candidate.Review.ID != reviewID || candidate.Review.Decision != "APPROVE" || !candidate.Review.Effective {
		return false, nil, humanGateError(errx.Conflict, "approval_not_effective", "the latest server review is not an effective APPROVE")
	}
	if len(candidate.BlockedBy) > 0 {
		return false, nil, humanGateError(errx.Conflict, "approval_not_schedulable", strings.Join(candidate.BlockedBy, ","))
	}
	if scheduled := candidate.Scheduling; scheduled != nil && scheduled.TouchpointID != uuid.Nil {
		tp, err := s.repo.GetTouchpoint(ctx, orgID, scheduled.TouchpointID)
		if err == nil && tp != nil && tp.DraftID != nil &&
			(tp.State == models.TouchpointQueued || tp.State == models.TouchpointSent) &&
			strings.EqualFold(strings.TrimSpace(tp.Recipient), strings.TrimSpace(candidate.Mailbox)) &&
			tp.Subject == candidate.Subject && tp.BodyText == candidate.BodyText &&
			tp.ApprovedContentHash == tp.ContentHash {
			if err := s.persistHumanGateScheduling(ctx, orgID, versionID, candidateID, reviewID, tp); err != nil {
				return false, nil, humanGateError(errx.Internal, "approval_schedule_store_failed", err.Error())
			}
			return true, tp, nil
		}
	}
	// A previous attempt may have committed the queue transition and then lost
	// the response before the binding row was stored. Recover that exact
	// candidate/content here so retrying the same APPROVE heals the partial
	// result instead of reporting a false route collision.
	if list, err := s.repo.ListTouchpoints(ctx, orgID, candidate.AccountID, "", 50, 0); err == nil {
		for i := range list {
			existing := &list[i]
			if existing.ContactCandidateID == nil || *existing.ContactCandidateID != candidateID ||
				(existing.State != models.TouchpointQueued && existing.State != models.TouchpointSent) ||
				!strings.EqualFold(strings.TrimSpace(existing.Recipient), strings.TrimSpace(candidate.Mailbox)) ||
				existing.Subject != candidate.Subject || existing.BodyText != candidate.BodyText ||
				existing.ApprovedContentHash != existing.ContentHash || existing.DraftID == nil {
				continue
			}
			if err := s.persistHumanGateScheduling(ctx, orgID, versionID, candidateID, reviewID, existing); err != nil {
				return false, nil, humanGateError(errx.Internal, "approval_schedule_store_failed", err.Error())
			}
			return true, existing, nil
		}
	}

	m := candidate.FrozenCohortMember
	key := humanGateTouchpointKey(versionID, candidateID)
	tp, err := s.repo.GetTouchpointByIdempotency(ctx, orgID, key)
	create := false
	if err != nil {
		return false, nil, humanGateError(errx.Internal, "touchpoint_read_failed", err.Error())
	}
	if tp == nil {
		tp, create, err = locateOrBuildTouchpoint(ctx, s.repo, orgID, m, time.Now().UTC())
		if err != nil {
			return false, nil, humanGateError(errx.Conflict, "approval_touchpoint_blocked", err.Error())
		}
	}
	if tp.State == models.TouchpointQueued || tp.State == models.TouchpointSent {
		if strings.EqualFold(strings.TrimSpace(tp.Recipient), strings.TrimSpace(m.Mailbox)) &&
			tp.Subject == m.Subject && tp.BodyText == m.BodyText && tp.ApprovedContentHash == tp.ContentHash {
			if err := s.persistHumanGateScheduling(ctx, orgID, versionID, candidateID, reviewID, tp); err != nil {
				return false, nil, humanGateError(errx.Internal, "approval_schedule_store_failed", err.Error())
			}
			return true, tp, nil
		}
		return false, nil, humanGateError(errx.Conflict, "route_already_dispatched", "an initial message for this route is already queued or sent")
	}

	tp.OrganizationID = orgID
	tp.ID = func() uuid.UUID {
		if create || tp.ID == uuid.Nil {
			return humanGateTouchpointID(versionID, candidateID)
		}
		return tp.ID
	}()
	tp.AccountID = m.AccountID
	cid := candidateID
	tp.ContactCandidateID = &cid
	tp.Ordinal = 1
	tp.Channel = models.OutreachChannelEmail
	tp.Purpose = models.TouchpointPurposeInitial
	tp.Recipient = m.Mailbox
	tp.Subject = m.Subject
	tp.BodyText = m.BodyText
	tp.PolicyVersion = RecipientPolicyVersion
	if create || tp.IdempotencyKey == "" {
		tp.IdempotencyKey = key
	}
	stampAccountContext(ctx, s.repo, orgID, tp)
	RecomputeContentHash(tp)
	if create {
		if err := s.repo.InsertTouchpoint(ctx, tp); err != nil {
			persisted, getErr := s.repo.GetTouchpointByIdempotency(ctx, orgID, key)
			if getErr != nil || persisted == nil {
				return false, nil, humanGateError(errx.Internal, "touchpoint_store_failed", err.Error())
			}
			tp = persisted
		}
	}
	if err := s.ensureHumanGateDraft(ctx, orgID, versionID, reviewID, actorID, m, tp); err != nil {
		return false, nil, humanGateError(errx.Internal, "draft_store_failed", err.Error())
	}
	if tp.State != models.TouchpointDrafted && tp.State != models.TouchpointNeedsReview && tp.State != models.TouchpointApproved {
		tp.State = models.TouchpointNeedsReview
	}
	if err := ApplyHumanApproval(tp, actorID, time.Now().UTC()); err != nil {
		return false, nil, humanGateError(errx.Conflict, "approval_apply_failed", err.Error())
	}
	tp.AuthorizationMode = AuthorizationModeHumanGate
	if err := s.repo.UpdateTouchpoint(ctx, tp); err != nil {
		return false, nil, humanGateError(errx.Internal, "approval_persist_failed", err.Error())
	}
	queued, qerr := s.QueueTouchpoint(ctx, orgID, actorID, tp.ID)
	if qerr != nil {
		return false, nil, humanGateError(qerr.Code, "approval_schedule_failed", qerr.Message)
	}
	if queued == nil || queued.State != models.TouchpointQueued || queued.DraftID == nil {
		return false, nil, humanGateError(errx.Conflict, "approval_schedule_unconfirmed", "APPROVE did not produce a QUEUED touchpoint")
	}
	if err := s.persistHumanGateScheduling(ctx, orgID, versionID, candidateID, reviewID, queued); err != nil {
		return false, nil, humanGateError(errx.Internal, "approval_schedule_store_failed", err.Error())
	}
	return false, queued, nil
}

func (s *service) scheduleLatestHumanGateApproval(
	ctx context.Context,
	orgID, versionID, candidateID uuid.UUID,
) (bool, *models.OutreachTouchpoint, *errx.Error) {
	v, xerr := s.GetHumanGateCohort(ctx, orgID, versionID, time.Now().UTC())
	if xerr != nil {
		return false, nil, xerr
	}
	for i := range v.Candidates {
		candidate := &v.Candidates[i]
		if candidate.CandidateID != candidateID {
			continue
		}
		if candidate.Review == nil || candidate.Review.Decision != "APPROVE" {
			return false, nil, humanGateError(errx.Conflict, "approval_not_effective", "the latest server review is not APPROVE")
		}
		return s.scheduleHumanGateApproval(ctx, orgID, versionID, candidateID,
			candidate.Review.ID, candidate.Review.ActorID)
	}
	return false, nil, humanGateError(errx.NotFound, "candidate_not_found", "candidate is not in this immutable version")
}

func (s *service) ReconcileApprovedHumanGateCandidates(ctx context.Context, orgID, actorID uuid.UUID) (*HumanGateReconcileReport, *errx.Error) {
	if actorID == uuid.Nil {
		return nil, errx.ErrUnauthorized
	}
	if s == nil || s.humanGateDB == nil {
		return nil, humanGateError(errx.ServiceUnavailable, "human_gate_store_unavailable", "human-gate scheduling store is unavailable")
	}
	report := &HumanGateReconcileReport{Failures: []HumanGateReconcileFailure{}}
	if err := s.humanGateDB.QueryRow(ctx, `SELECT count(*) FROM confenge_cohort_candidate_reviews WHERE organization_id=$1 AND decision='APPROVE'`, orgID).Scan(&report.ApprovalRecords); err != nil {
		return nil, humanGateError(errx.Internal, "approval_reconcile_read_failed", err.Error())
	}
	rows, err := s.humanGateDB.Query(ctx, `
		WITH latest AS (
			SELECT DISTINCT ON (cohort_version_id,candidate_id)
				id,cohort_version_id,candidate_id,decision,actor_id
			FROM confenge_cohort_candidate_reviews
			WHERE organization_id=$1
			ORDER BY cohort_version_id,candidate_id,created_at DESC,id DESC
		)
		SELECT id,cohort_version_id,candidate_id,actor_id
		FROM latest WHERE decision='APPROVE'
		ORDER BY cohort_version_id,candidate_id`, orgID)
	if err != nil {
		return nil, humanGateError(errx.Internal, "approval_reconcile_read_failed", err.Error())
	}
	type approvedCandidate struct {
		reviewID, versionID, candidateID, reviewerID uuid.UUID
	}
	approved := []approvedCandidate{}
	for rows.Next() {
		var item approvedCandidate
		if err := rows.Scan(&item.reviewID, &item.versionID, &item.candidateID, &item.reviewerID); err != nil {
			rows.Close()
			return nil, humanGateError(errx.Internal, "approval_reconcile_read_failed", err.Error())
		}
		approved = append(approved, item)
	}
	if err := rows.Err(); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		rows.Close()
		return nil, humanGateError(errx.Internal, "approval_reconcile_read_failed", err.Error())
	}
	rows.Close()
	uniqueCandidates := map[uuid.UUID]struct{}{}
	for _, item := range approved {
		report.LatestApprovedBindings++
		uniqueCandidates[item.candidateID] = struct{}{}
		already, _, xerr := s.scheduleHumanGateApproval(ctx, orgID, item.versionID, item.candidateID, item.reviewID, item.reviewerID)
		if xerr != nil {
			report.Failed++
			report.Failures = append(report.Failures, HumanGateReconcileFailure{CohortVersionID: item.versionID, CandidateID: item.candidateID, Reason: xerr.Identifier + ":" + xerr.Message})
			continue
		}
		if already {
			report.AlreadyScheduled++
		} else {
			report.Scheduled++
		}
	}
	report.UniqueApprovedCandidates = len(uniqueCandidates)
	return report, nil
}

func (s *service) invalidateHumanGateCandidateScheduling(ctx context.Context, orgID, versionID, candidateID uuid.UUID, reason string) {
	if s == nil || s.humanGateDB == nil || s.repo == nil {
		return
	}
	var touchpointID uuid.UUID
	if err := s.humanGateDB.QueryRow(ctx, `
		SELECT touchpoint_id FROM confenge_cohort_candidate_dispatches
		WHERE organization_id=$1 AND cohort_version_id=$2 AND candidate_id=$3`,
		orgID, versionID, candidateID).Scan(&touchpointID); err != nil {
		return
	}
	tp, err := s.repo.GetTouchpoint(ctx, orgID, touchpointID)
	if err != nil || tp == nil || tp.State == models.TouchpointSent {
		return
	}
	s.invalidateHumanGateDispatch(ctx, orgID, tp, reason)
}

func (s *service) humanGateDispatchInvalidation(
	ctx context.Context,
	orgID uuid.UUID,
	tp *models.OutreachTouchpoint,
) (bool, string) {
	if s == nil || s.humanGateDB == nil || tp == nil {
		return false, ""
	}
	var versionID, candidateID uuid.UUID
	err := s.humanGateDB.QueryRow(ctx, `
		SELECT cohort_version_id,candidate_id
		FROM confenge_cohort_candidate_dispatches
		WHERE organization_id=$1 AND touchpoint_id=$2
		ORDER BY scheduled_at DESC,updated_at DESC,cohort_version_id DESC
		LIMIT 1`, orgID, tp.ID).Scan(&versionID, &candidateID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ""
	}
	if err != nil {
		return true, "human_gate_binding_unreadable"
	}
	v, xerr := s.GetHumanGateCohort(ctx, orgID, versionID, time.Now().UTC())
	if xerr != nil || v == nil {
		return true, "human_gate_review_unreadable"
	}
	if !v.Actionable || !v.IsCurrentVersion {
		return true, "human_gate_version_superseded"
	}
	for i := range v.Candidates {
		candidate := &v.Candidates[i]
		if candidate.CandidateID != candidateID {
			continue
		}
		if candidate.Review == nil || candidate.Review.Decision != "APPROVE" || !candidate.Review.Effective {
			return true, "human_gate_approval_invalid"
		}
		if len(candidate.BlockedBy) > 0 {
			return true, "human_gate_approval_invalid:" + strings.Join(candidate.BlockedBy, ",")
		}
		if candidate.Scheduling == nil || !candidate.Scheduling.AutoSend || candidate.Scheduling.TouchpointID != tp.ID {
			return true, "human_gate_schedule_binding_invalid"
		}
		return true, ""
	}
	return true, "human_gate_candidate_missing"
}

func (s *service) invalidateHumanGateDispatch(ctx context.Context, orgID uuid.UUID, tp *models.OutreachTouchpoint, reason string) {
	if s == nil || s.repo == nil || tp == nil || strings.TrimSpace(reason) == "" {
		return
	}
	now := time.Now().UTC()
	if tp.DraftID != nil {
		if draft, err := s.repo.GetDraft(ctx, orgID, *tp.DraftID); err == nil && draft != nil && draft.Status != models.OutreachDraftSent {
			draft.Status = models.OutreachDraftRejected
			draft.ApprovedBy = nil
			draft.ApprovedAt = nil
			_ = s.repo.UpdateDraftStatus(ctx, draft)
		}
	}
	ClearApproval(tp)
	tp.StopReason = reason
	_ = s.repo.UpdateTouchpoint(ctx, tp)
	if s.humanGateDB != nil {
		_, _ = s.humanGateDB.Exec(ctx, `
			UPDATE confenge_cohort_candidate_dispatches
			SET invalidated_at=COALESCE(invalidated_at,$3),invalidation_reason=$4,updated_at=$3
			WHERE organization_id=$1 AND touchpoint_id=$2`, orgID, tp.ID, now, reason)
	}
	if s.governor != nil && tp.DraftID != nil {
		_ = s.governor.CancelQueued(ctx, dispatchMessageKey(tp), reason)
	}
}

func dispatchMessageKey(tp *models.OutreachTouchpoint) string {
	if tp == nil || tp.DraftID == nil {
		return ""
	}
	return "email:draft:" + tp.DraftID.String()
}
