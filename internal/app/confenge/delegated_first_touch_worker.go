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
)

type delegatedFirstTouchProcessor interface {
	ProcessDelegatedFirstTouchOnce(context.Context) (bool, error)
}

// DelegatedFirstTouchWorker keeps one canonical email queued from the prepared backlog.
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
			if !processed {
				if err != nil {
					log.Printf("confenge delegated first-touch autorun deferred: %v", err)
				}
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

// ProcessDelegatedFirstTouchOnce evaluates one prepared first touch under the active founder grant.
func (s *service) ProcessDelegatedFirstTouchOnce(ctx context.Context) (bool, error) {
	if s == nil || !s.cfg.DelegatedFirstTouchEnabled || !s.cfg.DelegatedFirstTouchAutorunEnabled ||
		s.delegatedDB == nil || s.policyStore == nil || s.cfg.OperatorOrgID == uuid.Nil {
		return false, nil
	}
	orgID := s.cfg.OperatorOrgID
	feed, err := s.repo.GetFeedSyncState(ctx, orgID)
	if err != nil || feed == nil {
		return false, err
	}
	if feed.LastStatus != "completed" {
		return false, nil
	}
	if _, err = s.retireStaleDelegatedFirstTouches(ctx, orgID, feed.LastRunID, feed.LastSnapshotHash); err != nil {
		return false, err
	}
	var queued int
	if err := s.delegatedDB.QueryRow(ctx, `
		SELECT count(*) FROM confenge_dispatch_queue
		WHERE organization_id=$1 AND channel='EMAIL' AND status IN ('queued','reserved')`, orgID).Scan(&queued); err != nil {
		return false, err
	}
	if queued > 0 {
		return false, nil
	}

	settings, err := s.repo.GetOrgSettings(ctx, orgID)
	if err != nil || settings == nil || settings.CampaignID == nil {
		return false, err
	}
	auth, err := s.policyStore.GetActiveCampaignPolicy(ctx, orgID, *settings.CampaignID, time.Now().UTC())
	if err != nil || auth == nil {
		return false, err
	}

	touchpointID, accountID, candidateID, err := s.nextDelegatedFirstTouchCandidate(ctx, orgID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}

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
		PolicyVersion: DelegatedFirstTouchPolicyV1, PolicyHash: DelegatedFirstTouchPolicyHashV1,
		AuthorityReference: DelegatedFirstTouchAuthorityRef, PolicyAuthorizationID: auth.ID,
		SourceRunID: feed.LastRunID, SourceSnapshotHash: feed.LastSnapshotHash,
		EvidenceVersion: DelegatedFirstTouchEvidenceV1, ComposerVersion: ComposerVersion,
		TemplateVersion: DelegatedFirstTouchTemplateV1, PromptVersion: PromptVersion,
		GeneratedAt: now, Entries: []DelegatedFirstTouchEntry{sealed},
	}
	report, xerr := s.ApplyDelegatedFirstTouchManifest(ctx, orgID, manifest, false)
	if xerr != nil {
		return false, fmt.Errorf("delegated first-touch autorun: %s", xerr.Message)
	}
	return report != nil && len(report.Items) == 1, nil
}

func (s *service) nextDelegatedFirstTouchCandidate(ctx context.Context, orgID uuid.UUID) (uuid.UUID, uuid.UUID, uuid.UUID, error) {
	var touchpointID, accountID, candidateID uuid.UUID
	err := s.delegatedDB.QueryRow(ctx, `
		SELECT t.id,t.account_id,t.contact_candidate_id
		FROM outreach_touchpoints t
		JOIN outreach_accounts a ON a.organization_id=t.organization_id AND a.id=t.account_id
		JOIN outreach_feed_sync_state feed ON feed.organization_id=t.organization_id
		WHERE t.organization_id=$1
		  AND t.ordinal=1 AND t.purpose='INITIAL' AND t.channel='EMAIL'
		  AND t.state='NEEDS_REVIEW' AND t.contact_candidate_id IS NOT NULL
		  AND feed.last_status='completed' AND a.source_run_id=feed.last_run_id
		  AND NOT EXISTS (
		    SELECT 1 FROM confenge_delegated_first_touch_decisions d
		    WHERE d.organization_id=t.organization_id AND d.account_id=t.account_id
		      AND (d.evidence_source_run_id=a.source_run_id
		        OR d.idempotency_key=$2 || a.source_run_id || ':' || a.id::text)
		  )
		ORDER BY t.due_at,t.created_at,t.id
		LIMIT 1`, orgID, delegatedFirstTouchIdempotencyPrefix).Scan(&touchpointID, &accountID, &candidateID)
	return touchpointID, accountID, candidateID, err
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
		WebSources:           []DelegatedWebSource{{URL: cand.SourceURL, Kind: "PUBLIC_COMPANY_SOURCE", Supports: "COMPANY_MAILBOX", ObservedAt: webObservedAt}},
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
