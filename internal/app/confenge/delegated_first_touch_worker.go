package confenge

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/warmbly/warmbly/internal/models"
)

const (
	delegatedFirstTouchMaxBurst          = 100
	delegatedFirstTouchIdempotencyPrefix = "delegated-first-touch:"
	delegatedFirstTouchLease             = 5 * time.Minute
	delegatedFirstTouchRetryDelay        = 15 * time.Minute
)

// delegatedFirstTouchSafeCandidateLateralSQL is the authoritative recipient
// selector shared by the worker and the runway readback. It deliberately does
// not trust the candidate currently attached to an unadmitted touchpoint: a
// later import may have replaced that route with a safer one. Conversely, a
// historical candidate can never be revived merely because its email still
// looks usable; current import lineage and the complete controlled-route
// provenance are required.
const delegatedFirstTouchSafeCandidateLateralSQL = `
		JOIN LATERAL (
			SELECT c.id
			FROM outreach_contact_candidates c
			WHERE c.organization_id=t.organization_id AND c.account_id=t.account_id
			  AND a.last_import_run_id IS NOT NULL
			  AND c.last_import_run_id=a.last_import_run_id
			  AND c.email<>'' AND c.email ~* '^[^[:space:]@]+@[^[:space:]@]+[.][^[:space:]@]+$'
			  AND NOT c.blocked AND NOT c.do_not_contact AND NOT c.bounced
			  AND upper(COALESCE(c.ownership_status,''))='COMPANY_OWNED'
			  AND upper(COALESCE(c.channel_epistemic_class,''))='OBSERVED'
			  AND upper(COALESCE(c.route_freshness,''))='FRESH'
			  AND upper(COALESCE(c.email_derivation,''))<>'INFERRED'
			  AND upper(COALESCE(c.route_suppression,'')) IN ('','NONE')
			  AND c.discovery_json @> '{"controlled_email_eligible":true}'::jsonb
			  AND c.discovery_json @> '{"preferred_initial":true}'::jsonb
			  AND upper(COALESCE(c.discovery_json->>'mailbox_company_evidence',''))='OBSERVED'
			  AND upper(COALESCE(c.discovery_json->>'route_class','')) IN
			    ('ROLE_OR_DEPARTMENT','GENERIC_COMPANY','PUBLIC_COMPANY_FREEMAIL')
			  AND upper(COALESCE(c.verification_status,''))='OFFICIAL_SOURCE'
			  AND c.source_date IS NOT NULL
			  AND (
			    (c.source_url ~* '^https?://[^/[:space:]]+' AND lower(c.source_url) !~
			      'google[.]|bing[.]|duckduckgo[.]|search[.]yahoo[.]')
			    OR (c.source_url='' AND upper(c.verification_status)='OFFICIAL_SOURCE')
			  )
			  AND lower(btrim(c.email)) NOT LIKE 'fixture@%'
			  AND lower(btrim(c.email)) NOT LIKE 'synthetic@%'
			  AND NOT (lower(btrim(c.email)) LIKE '%@demo%' AND lower(btrim(c.email)) LIKE '%obra.com.br')
			  AND lower(COALESCE(c.block_reason,'')) NOT LIKE '%provenance_taint%'
			  AND lower(COALESCE(c.block_reason,'')) NOT LIKE '%provenance_chain%'
			  AND upper(COALESCE(c.block_reason,'')) NOT LIKE '%PROVENANCE_CONTAMINATION%'
			  -- A terminal HOLD/CANCELLED consumes this recipient binding, not
			  -- every other current-safe recipient on the same account.
			  AND NOT EXISTS (
			    SELECT 1 FROM confenge_delegated_first_touch_decisions candidate_decision
			    WHERE candidate_decision.organization_id=c.organization_id
			      AND candidate_decision.account_id=c.account_id
			      AND candidate_decision.contact_candidate_id=c.id
			      AND candidate_decision.evidence_source_run_id=$2
			      AND candidate_decision.source_snapshot_hash=$3
			      AND candidate_decision.runtime_release_sha=$4
			      AND candidate_decision.policy_authorization_id=$5
			  )
			ORDER BY
			  CASE WHEN c.recommended THEN 0 ELSE 1 END,
			  CASE upper(COALESCE(c.discovery_json->>'route_class',''))
			    WHEN 'ROLE_OR_DEPARTMENT' THEN 0 WHEN 'GENERIC_COMPANY' THEN 1 ELSE 2 END,
			  lower(btrim(c.email)),c.id
			LIMIT 1
		) selected_candidate ON true`

type delegatedFirstTouchProcessor interface {
	ProcessDelegatedFirstTouchOnce(context.Context) (bool, error)
}

// DelegatedFirstTouchWorker maintains a rolling, capacity-derived queue runway.
type DelegatedFirstTouchWorker struct {
	processor delegatedFirstTouchProcessor
	interval  time.Duration
}

func NewDelegatedFirstTouchWorker(processor delegatedFirstTouchProcessor, interval time.Duration) *DelegatedFirstTouchWorker {
	if interval <= 0 {
		interval = time.Minute
	}
	return &DelegatedFirstTouchWorker{processor: processor, interval: interval}
}

func (w *DelegatedFirstTouchWorker) Run(ctx context.Context) {
	if w == nil || w.processor == nil {
		return
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		for i := 0; i < delegatedFirstTouchMaxBurst; i++ {
			processed, err := w.processor.ProcessDelegatedFirstTouchOnce(ctx)
			if err != nil {
				log.Printf("confenge delegated first-touch autorun deferred: %v", err)
			}
			if !processed {
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

// ProcessDelegatedFirstTouchOnce evaluates one prepared first touch for the next capacity slot.
func (s *service) ProcessDelegatedFirstTouchOnce(ctx context.Context) (processed bool, returnErr error) {
	if s == nil || !s.cfg.DelegatedFirstTouchEnabled || !s.cfg.DelegatedFirstTouchAutorunEnabled ||
		s.cfg.DelegatedFirstTouchRunwayDays < 1 || s.delegatedDB == nil || s.policyStore == nil ||
		s.cfg.OperatorOrgID == uuid.Nil {
		return false, nil
	}
	orgID := s.cfg.OperatorOrgID
	unlock, locked, err := s.lockDelegatedFirstTouchRunway(ctx, orgID)
	if err != nil || !locked {
		return false, err
	}
	defer unlock()

	feed, err := s.repo.GetFeedSyncState(ctx, orgID)
	if err != nil || feed == nil {
		return false, err
	}
	if validateAuthoritativeFeedStructure(feed, true) != nil {
		return false, nil
	}
	settings, err := s.repo.GetOrgSettings(ctx, orgID)
	if err != nil || settings == nil || settings.CampaignID == nil {
		return false, err
	}
	auth, err := s.policyStore.GetActiveCampaignPolicy(ctx, orgID, *settings.CampaignID, time.Now().UTC())
	if err != nil {
		// A policy read that failed is not a revoked policy. Retiring here would
		// cancel every approved touch in the org on a transient database error.
		return false, err
	}
	if auth == nil {
		// No active campaign policy authorization: nothing is authorized to
		// send, so approvals made under a previous authorization do retire. The
		// nil pointer is the sentinel the predicate reads as "matches none".
		var noAuthorization *uuid.UUID
		if _, retireErr := s.retireStaleDelegatedFirstTouches(ctx, orgID, feed.LastRunID, feed.LastSnapshotHash, noAuthorization); retireErr != nil {
			return false, retireErr
		}
		return false, nil
	}
	if _, err = s.retireStaleDelegatedFirstTouches(ctx, orgID, feed.LastRunID, feed.LastSnapshotHash, &auth.ID); err != nil {
		return false, err
	}
	plan, err := s.delegatedFirstTouchRunwayPlan(ctx, orgID, feed, auth, time.Now().UTC())
	if err != nil || !plan.CapacityKnown || plan.TargetReached() {
		return false, err
	}

	touchpointID, accountID, candidateID, err := s.nextDelegatedFirstTouchCandidate(ctx, orgID, feed, auth)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	deferred := true
	defer func() {
		if deferred {
			s.deferDelegatedFirstTouch(ctx, orgID, touchpointID, returnErr)
		}
	}()

	acc, err := s.repo.GetAccount(ctx, orgID, accountID)
	if err != nil || acc == nil {
		return false, err
	}
	cand, err := s.repo.GetCandidate(ctx, orgID, candidateID)
	if err != nil || cand == nil {
		return false, err
	}
	evidence, err := s.repo.ListEvidence(ctx, orgID, acc.ID)
	if err != nil {
		return false, err
	}
	copy := buildDelegatedRoutingCopy(acc, cand, evidence)
	entry := delegatedEntryFromCurrentState(acc, cand, touchpointID, copy)
	entry.IdempotencyKey = delegatedFirstTouchRunwayIdempotencyKey(feed, auth.ID, s.cfg.RepositorySHA, accountID)
	sealed, err := SealDelegatedFirstTouchEntry(entry, cand)
	if err != nil {
		sealed = entry
		sealed.Recipient = strings.ToLower(strings.TrimSpace(cand.Email))
		sealed.RouteClass = CandidateRouteClass(cand)
		sealed.SubjectHash = hashText(sealed.Subject)
		sealed.BodyHash = hashText(sealed.BodyText)
	}
	now := time.Now().UTC()
	manifest := DelegatedFirstTouchManifest{
		SchemaVersion: DelegatedFirstTouchManifestV1,
		BatchID:       "autorun-" + uuid.NewString(), AgentID: "warmbly:delegated-first-touch-worker",
		PolicyVersion: DelegatedFirstTouchPolicyV2, PolicyHash: DelegatedFirstTouchPolicyHashV2,
		AuthorityReference: DelegatedFirstTouchAuthorityRef, PolicyAuthorizationID: auth.ID,
		SourceRunID: feed.LastRunID, SourceSnapshotHash: feed.LastSnapshotHash,
		EvidenceVersion: DelegatedFirstTouchEvidenceV1, ComposerVersion: ComposerVersion,
		TemplateVersion: DelegatedFirstTouchTemplateV1, PromptVersion: PromptVersion,
		GeneratedAt: now, Entries: []DelegatedFirstTouchEntry{sealed},
	}
	nextDue := plan.NextDueAt()
	report, xerr := s.applyDelegatedFirstTouchManifest(ctx, orgID, manifest, false, &nextDue)
	if xerr != nil {
		return true, fmt.Errorf("delegated first-touch autorun: %s", xerr.Message)
	}
	deferred = false
	_, _ = s.delegatedDB.Exec(ctx, `
		UPDATE outreach_touchpoints
		SET delegated_reserved_until=NULL,delegated_last_error='',updated_at=now()
		WHERE organization_id=$1 AND id=$2`, orgID, touchpointID)
	// A terminal HOLD consumed this candidate and the burst should keep filling from the reservoir.
	return report != nil && len(report.Items) == 1, nil
}

func (s *service) nextDelegatedFirstTouchCandidate(ctx context.Context, orgID uuid.UUID, feed *models.OutreachFeedSyncState, auth *models.CampaignPolicyAuthorization) (uuid.UUID, uuid.UUID, uuid.UUID, error) {
	var touchpointID, accountID, candidateID uuid.UUID
	if feed == nil || auth == nil {
		return touchpointID, accountID, candidateID, pgx.ErrNoRows
	}
	now := time.Now().UTC()
	err := s.delegatedDB.QueryRow(ctx, `
		WITH next AS (
			SELECT t.id,selected_candidate.id AS candidate_id
			FROM outreach_touchpoints t
			JOIN outreach_accounts a ON a.organization_id=t.organization_id AND a.id=t.account_id
			`+delegatedFirstTouchSafeCandidateLateralSQL+`
			JOIN outreach_feed_sync_state feed ON feed.organization_id=t.organization_id
			JOIN outreach_feed_committed_runs account_lineage
			  ON account_lineage.organization_id=a.organization_id AND account_lineage.source_run_id=a.source_run_id
			JOIN outreach_feed_committed_runs touchpoint_lineage
			  ON touchpoint_lineage.organization_id=t.organization_id AND touchpoint_lineage.source_run_id=t.source_run_id
			WHERE t.organization_id=$1
			  AND t.ordinal=1 AND t.purpose='INITIAL' AND t.channel='EMAIL'
			  AND t.state IN ('DUE','NEEDS_REVIEW')
			  AND `+authoritativeLastGoodFeedSQL+`
			  AND confenge_commercially_qualified(a.commercial_qualification_state,
			    a.commercial_qualified_until,a.commercial_qualification_deactivated,$6::date)
			  AND a.initial_backlog_reason_code=''
			  AND a.last_import_run_id IS NOT NULL
			  AND a.queue_state NOT IN ('SENT','REPLIED','MEETING','PROPOSAL','WON','LOST','ENROLLED')
			  AND t.delegated_retry_at <= $6
			  AND (t.delegated_reserved_until IS NULL OR t.delegated_reserved_until <= $6)
			  AND NOT EXISTS (
			    SELECT 1 FROM confenge_delegated_first_touch_decisions d
			    WHERE d.organization_id=t.organization_id AND d.account_id=t.account_id
			      AND d.state IN ('SENT','APPROVED','QUEUED','APPROVED_NOT_SCHEDULED')
			  )
			ORDER BY t.delegated_retry_at,t.due_at,t.created_at,t.id
			LIMIT 1 FOR UPDATE OF t SKIP LOCKED
		)
		UPDATE outreach_touchpoints t
		SET delegated_reserved_until=$7,delegated_attempts=t.delegated_attempts+1,
			delegated_last_error='',updated_at=$6
		FROM next WHERE t.id=next.id
		RETURNING t.id,t.account_id,next.candidate_id`, orgID, feed.LastRunID, feed.LastSnapshotHash,
		s.cfg.RepositorySHA, auth.ID, now, now.Add(delegatedFirstTouchLease)).Scan(&touchpointID, &accountID, &candidateID)
	return touchpointID, accountID, candidateID, err
}

func (s *service) deferDelegatedFirstTouch(ctx context.Context, orgID, touchpointID uuid.UUID, cause error) {
	reason := "delegated first-touch processing failed"
	if cause != nil {
		reason = cause.Error()
	}
	_, _ = s.delegatedDB.Exec(ctx, `
		UPDATE outreach_touchpoints
		SET delegated_reserved_until=NULL,delegated_retry_at=$3,
			delegated_last_error=LEFT($4,500),updated_at=now()
		WHERE organization_id=$1 AND id=$2`, orgID, touchpointID,
		time.Now().UTC().Add(delegatedFirstTouchRetryDelay), reason)
}

func delegatedFirstTouchRunwayIdempotencyKey(feed *models.OutreachFeedSyncState, policyAuthorizationID uuid.UUID, runtimeSHA string, accountID uuid.UUID) string {
	binding := ""
	if feed != nil {
		binding = feed.LastRunID + "\x00" + feed.LastSnapshotHash
	}
	binding += "\x00" + runtimeSHA + "\x00" + policyAuthorizationID.String()
	return delegatedFirstTouchIdempotencyPrefix + "runway-v1:" + hashText(binding) + ":" + accountID.String()
}

func delegatedEntryFromCurrentState(acc *models.OutreachAccount, cand *models.OutreachContactCandidate, touchpointID uuid.UUID, copy delegatedRoutingCopy) DelegatedFirstTouchEntry {
	observedAt := time.Time{}
	if acc.ContractorRoleObservedAt != nil {
		observedAt = acc.ContractorRoleObservedAt.UTC()
	}
	// SourceDate is the imported observation date for the public mailbox
	// evidence. UpdatedAt is only the Warmbly row/import timestamp and must
	// never refresh external evidence. Missing source provenance stays zero so
	// delegatedWebSourceAllowed fails closed.
	webObservedAt := time.Time{}
	if cand.SourceDate != nil {
		webObservedAt = cand.SourceDate.UTC()
	}
	key := delegatedFirstTouchIdempotencyPrefix + acc.SourceRunID + ":" + acc.ID.String()
	webSources := []DelegatedWebSource{{URL: cand.SourceURL, Kind: "PUBLIC_COMPANY_SOURCE", Supports: "COMPANY_MAILBOX", ObservedAt: webObservedAt}}
	if candidateIsObservedRegistry(cand) && strings.TrimSpace(cand.SourceURL) == "" {
		webSources = []DelegatedWebSource{{Kind: DelegatedWebSourceKindOfficialRegistry, Supports: "COMPANY_MAILBOX", ObservedAt: webObservedAt}}
	}
	return DelegatedFirstTouchEntry{
		IdempotencyKey: key, CorrelationID: "touchpoint:" + touchpointID.String(),
		AccountID: acc.ID, ContactCandidateID: cand.ID, CNPJ14: acc.CNPJ14,
		SupplierCNPJ14: acc.SupplierCNPJ14, BuyerCNPJ14: acc.BuyerCNPJ14,
		ContractorRoleStatus: acc.ContractorRoleStatus, TargetPartyRole: acc.TargetPartyRole,
		ContractRoleSource: acc.ContractorRoleSource, ContractEvidenceIDs: append([]string{}, acc.ContractorRoleEvidenceIDs...),
		ContractEvidenceHash: acc.ContractorRoleEvidenceHash, ContractEvidenceReference: acc.ContractorRoleEvidenceReference,
		SupplierIdentityRef: acc.SupplierIdentityRef, BuyerIdentityRef: acc.BuyerIdentityRef,
		RoleMatchMethod: acc.ContractorRoleMatchMethod, RoleConfidence: acc.ContractorRoleConfidence,
		ContractRoleReasonCodes: append([]string{}, acc.ContractorRoleReasonCodes...), EvidenceObservedAt: observedAt,
		ReconciliationStatus: ReconciliationWebContact,
		WebSources:           webSources,
		Subject:              copy.Subject, BodyText: copy.Body, CopyRulesVersion: DelegatedFirstTouchCopyRulesV1,
		FactUsed: copy.FactUsed, FactEvidenceIDs: append([]string{}, copy.FactEvidenceIDs...),
		Practice: copy.Practice, CTA: copy.CTA,
		SemanticSignature: copy.SemanticSignature,
		EvidenceIDs:       uniqueStrings(append(append([]string{}, acc.ContractorRoleEvidenceIDs...), copy.FactEvidenceIDs...)),
		QA: DelegatedFirstTouchQA{Result: "PASS", Attempts: 1, IdentityPassed: true, FactualPassed: true,
			CopyPassed: true, OperationalPassed: true, Reviewer: DelegatedFirstTouchValidatorV1,
			ReasonCodes: []string{"deterministic_factual_copy", "current_supplier_evidence", "current_attributed_recipient"}},
	}
}
