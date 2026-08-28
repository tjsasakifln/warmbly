package confenge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

const (
	DelegatedFirstTouchManifestV1          = "confenge.delegated-first-touch.manifest.v1"
	DelegatedFirstTouchPolicyV1            = "CFG-FIRST-TOUCH-ROUTING-v1"
	DelegatedFirstTouchPolicyHashV1        = "3ea77bb047d9db19c061d96c62ae302768f25dbd075db7cccac5fe56d3fe3b99"
	DelegatedFirstTouchValidatorV1         = "confenge.first-touch.adversarial-qa.v1"
	DelegatedFirstTouchContactPolicyV1     = "confenge.first-touch-routing.v1"
	DelegatedFirstTouchEvidenceV1          = "contract-party-role.v1"
	DelegatedFirstTouchTemplateV1          = "confenge.first-touch-routing.template.v1"
	DelegatedFirstTouchAuthority           = "founder-approved-first-touch-policy"
	DelegatedFirstTouchAuthorityRef        = "tjsasakifln/Governance#129"
	DelegatedFirstTouchApprovalDecision    = "DELEGATED_POLICY_APPROVE"
	ContractorRoleConfirmed                = "CONTRACTOR_ROLE_CONFIRMED"
	ContractorRoleConflict                 = "PARTY_ROLE_CONFLICT"
	ContractorRoleUnknown                  = "UNKNOWN"
	ReconciliationCorroborated             = "DATALAKE+WEB_CORROBORATED"
	ReconciliationWebContact               = "DATALAKE_IDENTITY + WEB_CONTACT"
	DelegatedWebSourceKindOfficialRegistry = "OFFICIAL_COMPANY_REGISTRY"
)

// Contact evidence is proven fresh at the instant a decision is minted. The
// runway schedules sends days ahead, so transport re-proves the same window
// against that instant plus the widest legal runway, never a moving now.
const (
	delegatedContactEvidenceWindow  = 30 * 24 * time.Hour
	delegatedContactEvidenceRunway  = time.Duration(MaxDelegatedFirstTouchRunwayDays) * 24 * time.Hour
	delegatedContactEvidenceCeiling = delegatedContactEvidenceWindow + delegatedContactEvidenceRunway
)

var delegatedSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var delegatedDigitPattern = regexp.MustCompile(`[0-9]`)

type delegatedTransportAuthority struct {
	CampaignStatus     string
	MailboxStatus      string
	MailboxRiskBand    string
	WorkerAssigned     bool
	WorkerHealthy      bool
	CredentialsPresent bool
	SenderSelected     bool
	AuthState          string
	AuthSPF            bool
	AuthDKIM           bool
	AuthDMARC          bool
	AuthCheckedAt      *time.Time
	CampaignLimit      int
	MinWaitTime        int
}

// DelegatedFirstTouchManifest is authored by the CLI agent. The backend never
// calls a model and never invents evidence or a mailbox.
type DelegatedFirstTouchManifest struct {
	SchemaVersion         string                     `json:"schema_version"`
	BatchID               string                     `json:"batch_id"`
	AgentID               string                     `json:"agent_id"`
	PolicyVersion         string                     `json:"policy_version"`
	PolicyHash            string                     `json:"policy_hash"`
	AuthorityReference    string                     `json:"authority_reference"`
	PolicyAuthorizationID uuid.UUID                  `json:"policy_authorization_id"`
	SourceRunID           string                     `json:"source_run_id"`
	SourceSnapshotHash    string                     `json:"source_snapshot_hash"`
	EvidenceVersion       string                     `json:"evidence_version"`
	ComposerVersion       string                     `json:"composer_version"`
	TemplateVersion       string                     `json:"template_version"`
	PromptVersion         string                     `json:"prompt_version"`
	GeneratedAt           time.Time                  `json:"generated_at"`
	Entries               []DelegatedFirstTouchEntry `json:"entries"`
	CommercialAuthority   *FeedCommercialAuthority   `json:"commercial_authority,omitempty"`
	authority             *models.OutreachFeedSyncState
}

type DelegatedWebSource struct {
	URL        string    `json:"url"`
	Kind       string    `json:"kind"`
	Supports   string    `json:"supports"`
	ObservedAt time.Time `json:"observed_at"`
}

type DelegatedFirstTouchQA struct {
	Result            string   `json:"result"`
	Attempts          int      `json:"attempts"`
	Repaired          bool     `json:"repaired"`
	IdentityPassed    bool     `json:"identity_passed"`
	FactualPassed     bool     `json:"factual_passed"`
	CopyPassed        bool     `json:"copy_passed"`
	OperationalPassed bool     `json:"operational_passed"`
	Reviewer          string   `json:"reviewer"`
	ReasonCodes       []string `json:"reason_codes"`
}

type DelegatedFirstTouchEntry struct {
	IdempotencyKey            string                `json:"idempotency_key"`
	CorrelationID             string                `json:"correlation_id"`
	AccountID                 uuid.UUID             `json:"account_id"`
	ContactCandidateID        uuid.UUID             `json:"contact_candidate_id"`
	CNPJ14                    string                `json:"cnpj14"`
	SupplierCNPJ14            string                `json:"supplier_cnpj14"`
	BuyerCNPJ14               string                `json:"buyer_cnpj14"`
	ContractorRoleStatus      string                `json:"contractor_role_status"`
	TargetPartyRole           string                `json:"target_party_role"`
	ContractRoleSource        string                `json:"contract_role_source"`
	ContractEvidenceIDs       []string              `json:"contract_evidence_ids"`
	ContractEvidenceHash      string                `json:"contract_evidence_hash"`
	ContractEvidenceReference string                `json:"contract_evidence_reference"`
	SupplierIdentityRef       string                `json:"supplier_identity_ref"`
	BuyerIdentityRef          string                `json:"buyer_identity_ref"`
	RoleMatchMethod           string                `json:"role_match_method"`
	RoleConfidence            string                `json:"role_confidence"`
	ContractRoleReasonCodes   []string              `json:"contract_role_reason_codes"`
	EvidenceObservedAt        time.Time             `json:"evidence_observed_at"`
	ReconciliationStatus      string                `json:"reconciliation_status"`
	WebSources                []DelegatedWebSource  `json:"web_sources"`
	RouteClass                string                `json:"route_class"`
	Recipient                 string                `json:"recipient"`
	Subject                   string                `json:"subject"`
	BodyText                  string                `json:"body_text"`
	CopyRulesVersion          string                `json:"copy_rules_version"`
	FactUsed                  string                `json:"fact_used"`
	FactEvidenceIDs           []string              `json:"fact_evidence_ids"`
	Practice                  string                `json:"practice"`
	CTA                       string                `json:"cta"`
	SemanticSignature         string                `json:"semantic_signature"`
	SubjectHash               string                `json:"subject_hash"`
	BodyHash                  string                `json:"body_hash"`
	EvidenceIDs               []string              `json:"evidence_ids"`
	QA                        DelegatedFirstTouchQA `json:"qa"`
}

type DelegatedFirstTouchItemResult struct {
	IdempotencyKey string     `json:"idempotency_key"`
	State          string     `json:"state"`
	Blockers       []string   `json:"blockers,omitempty"`
	TouchpointID   uuid.UUID  `json:"touchpoint_id,omitempty"`
	DueAt          *time.Time `json:"due_at,omitempty"`
	Idempotent     bool       `json:"idempotent,omitempty"`
}

type DelegatedFirstTouchReport struct {
	BatchID              string                          `json:"batch_id"`
	ManifestHash         string                          `json:"manifest_hash"`
	DryRun               bool                            `json:"dry_run"`
	Generated            int                             `json:"generated"`
	QAPass               int                             `json:"qa_pass"`
	QARepaired           int                             `json:"qa_repaired"`
	Held                 int                             `json:"held"`
	DelegatedApproved    int                             `json:"delegated_approved"`
	Queued               int                             `json:"queued"`
	ApprovedNotScheduled int                             `json:"approved_not_scheduled"`
	Items                []DelegatedFirstTouchItemResult `json:"items"`
}

type DelegatedFirstTouchStatus struct {
	SchemaVersion        string                             `json:"schema_version"`
	RuntimeReleaseSHA    string                             `json:"runtime_release_sha,omitempty"`
	BatchID              string                             `json:"batch_id,omitempty"`
	PolicyID             string                             `json:"policy_id"`
	PolicyVersion        string                             `json:"policy_version"`
	PolicyHash           string                             `json:"policy_hash"`
	PolicyActive         bool                               `json:"policy_active"`
	Counts               map[string]int                     `json:"counts"`
	HumanApproved        int                                `json:"human_approved"`
	QueuedReadback       int                                `json:"queued_readback"`
	DuplicateLiveAccount int                                `json:"duplicate_live_account"`
	DuplicateLiveRoot    int                                `json:"duplicate_live_root"`
	Runway               DelegatedFirstTouchRunwayMetrics   `json:"runway"`
	Control              DelegatedFirstTouchControlReadback `json:"control"`
	Items                []DelegatedFirstTouchDecisionView  `json:"items"`
}

type DelegatedFirstTouchSourceReadback struct {
	RunID                    string     `json:"run_id,omitempty"`
	SnapshotHash             string     `json:"snapshot_hash,omitempty"`
	FreshnessState           string     `json:"freshness_state"`
	GeneratedAt              *time.Time `json:"generated_at,omitempty"`
	ExpiresAt                *time.Time `json:"expires_at,omitempty"`
	FreshnessHash            string     `json:"freshness_hash,omitempty"`
	TargetMembershipComplete bool       `json:"target_membership_complete"`
	TargetMembershipHash     string     `json:"target_membership_hash,omitempty"`
	TargetMembershipCount    int        `json:"target_membership_count"`
	SupplierConfirmedCount   int        `json:"supplier_confirmed_count"`
}

type DelegatedFirstTouchAuthorityReadback struct {
	Present                            bool       `json:"present"`
	State                              string     `json:"state"`
	NewAdmissionAllowed                bool       `json:"new_admission_allowed"`
	ExistingBoundTouchTransportAllowed bool       `json:"existing_bound_touch_transport_allowed"`
	BasisSourceRunID                   string     `json:"basis_source_run_id,omitempty"`
	BasisSnapshotHash                  string     `json:"basis_snapshot_hash,omitempty"`
	BasisMembershipHash                string     `json:"basis_membership_hash,omitempty"`
	BasisPublicationSemanticHash       string     `json:"basis_publication_semantic_hash,omitempty"`
	ProducerIdentity                   string     `json:"producer_identity,omitempty"`
	SourceRunID                        string     `json:"source_run_id,omitempty"`
	SnapshotID                         string     `json:"snapshot_id,omitempty"`
	MembershipHash                     string     `json:"membership_hash,omitempty"`
	ValidUntil                         *time.Time `json:"valid_until,omitempty"`
	ReasonCodes                        []string   `json:"reason_codes,omitempty"`
}

type DelegatedFirstTouchTransportReadback struct {
	ProviderAttempts  int    `json:"provider_attempts"`
	ProviderAccepted  int    `json:"provider_accepted"`
	Sent              int    `json:"sent"`
	KillSwitchEngaged bool   `json:"kill_switch_engaged"`
	DispatchPaused    bool   `json:"dispatch_paused"`
	PauseReason       string `json:"pause_reason,omitempty"`
}

type DelegatedFirstTouchControlReadback struct {
	SchemaVersion     string                               `json:"schema_version"`
	Source            DelegatedFirstTouchSourceReadback    `json:"source"`
	Commercial        DelegatedFirstTouchAuthorityReadback `json:"commercial_authority"`
	Prepared          int                                  `json:"prepared"`
	ReadyReservoir    int                                  `json:"ready_reservoir"`
	DelegatedApproved int                                  `json:"delegated_approved"`
	HumanApproved     int                                  `json:"human_approved"`
	Queued            int                                  `json:"queued"`
	Reserved          int                                  `json:"reserved"`
	NextDueAt         *time.Time                           `json:"next_due_at,omitempty"`
	FurthestDueAt     *time.Time                           `json:"furthest_due_at,omitempty"`
	Transport         DelegatedFirstTouchTransportReadback `json:"transport"`
	Outcomes          map[string]int                       `json:"outcomes"`
	Capacity          *dispatch.Status                     `json:"capacity,omitempty"`
	Blocker           string                               `json:"blocker,omitempty"`
}

type DelegatedFirstTouchDecisionView struct {
	BatchID               string     `json:"batch_id"`
	AccountID             *uuid.UUID `json:"account_id,omitempty"`
	CNPJ14                string     `json:"cnpj14"`
	SupplierCNPJ14        string     `json:"supplier_cnpj14"`
	BuyerCNPJ14           string     `json:"buyer_cnpj14,omitempty"`
	Recipient             string     `json:"recipient,omitempty"`
	RouteClass            string     `json:"route_class"`
	Decision              string     `json:"decision"`
	ApprovalSource        string     `json:"approval_source"`
	State                 string     `json:"state"`
	EvidenceReference     string     `json:"evidence_reference,omitempty"`
	EvidenceHash          string     `json:"evidence_hash,omitempty"`
	SourceRunID           string     `json:"source_run_id"`
	SourceSnapshotHash    string     `json:"source_snapshot_hash"`
	SourceExpiresAt       *time.Time `json:"source_expires_at,omitempty"`
	SourceFreshnessHash   string     `json:"source_freshness_hash,omitempty"`
	TargetMembershipHash  string     `json:"target_membership_hash,omitempty"`
	TargetMembershipCount int        `json:"target_membership_count"`
	ReasonCodes           []string   `json:"reason_codes,omitempty"`
	BlockerCodes          []string   `json:"blocker_codes,omitempty"`
	ContentHash           string     `json:"content_hash,omitempty"`
	RuntimeReleaseSHA     string     `json:"runtime_release_sha,omitempty"`
	DueAt                 *time.Time `json:"due_at,omitempty"`
	ReadbackAt            *time.Time `json:"readback_at,omitempty"`
	DecidedAt             time.Time  `json:"decided_at"`
}

type delegatedRecentBody struct {
	AccountID uuid.UUID
	Body      string
}

func (s *service) WireDelegatedFirstTouch(db *pgxpool.Pool) { s.delegatedDB = db }

func (s *service) WireOrgRisk(risk OrgRiskPolicy) { s.orgRisk = risk }

// ReconcileDelegatedFirstTouchLedger repairs an audit projection that can be
// left behind if an older runtime cancels the canonical touchpoint/queue first.
// It never creates approval or queue work; it only makes an already-safe
// cancellation explicit and releases stale one-live-account/root bindings.
func (s *service) ReconcileDelegatedFirstTouchLedger(ctx context.Context, orgID uuid.UUID) (int64, error) {
	if s == nil || s.delegatedDB == nil || orgID == uuid.Nil {
		return 0, fmt.Errorf("delegated first-touch store is not wired")
	}
	tx, err := s.delegatedDB.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `
		WITH invalid AS (
			SELECT d.id,
				COALESCE(NULLIF(q.last_error,''),NULLIF(q.cancel_reason,''),'canonical_queue_state_drift') AS reason
			FROM confenge_delegated_first_touch_decisions d
			JOIN outreach_touchpoints t
			  ON t.organization_id=d.organization_id AND t.id=d.touchpoint_id
			LEFT JOIN confenge_dispatch_queue q
			  ON q.organization_id=d.organization_id AND q.draft_id=d.draft_id AND q.message_key=d.queue_message_key
			WHERE d.organization_id=$1
			  AND d.state IN ('APPROVED','QUEUED','APPROVED_NOT_SCHEDULED')
			  AND (
				(d.state IN ('APPROVED','APPROVED_NOT_SCHEDULED') AND t.state <> 'APPROVED')
				OR (d.state='QUEUED' AND (
					t.state NOT IN ('QUEUED','SENT')
					OR (t.state='QUEUED' AND (q.status IS NULL OR q.status NOT IN ('queued','reserved','attempted','sent')))
				))
			  )
		)
		UPDATE confenge_delegated_first_touch_decisions d
		SET state='CANCELLED',
			blocker_codes=CASE
				WHEN d.blocker_codes @> jsonb_build_array(invalid.reason)
					THEN d.blocker_codes
				ELSE d.blocker_codes || jsonb_build_array(invalid.reason)
			END,
			updated_at=now()
		FROM invalid WHERE d.id=invalid.id`, orgID)
	if err != nil {
		return 0, err
	}
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT t.id
		FROM outreach_touchpoints t
		JOIN confenge_delegated_first_touch_decisions d
		  ON d.organization_id=t.organization_id AND d.touchpoint_id=t.id
		LEFT JOIN confenge_dispatch_queue q
		  ON q.organization_id=d.organization_id AND q.draft_id=d.draft_id AND q.message_key=d.queue_message_key
		WHERE t.organization_id=$1 AND t.state='QUEUED' AND d.state='CANCELLED'
		  AND (q.status IS NULL OR q.status NOT IN ('queued','reserved','attempted','sent'))
		  AND NOT EXISTS (
			SELECT 1 FROM confenge_delegated_first_touch_decisions live
			JOIN confenge_dispatch_queue live_q
			  ON live_q.organization_id=live.organization_id AND live_q.draft_id=live.draft_id
			 AND live_q.message_key=live.queue_message_key
			WHERE live.organization_id=t.organization_id AND live.touchpoint_id=t.id
			  AND live.state='QUEUED' AND live_q.status IN ('queued','reserved','attempted','sent')
		  )`, orgID)
	if err != nil {
		return 0, err
	}
	var touchpointIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		touchpointIDs = append(touchpointIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	if len(touchpointIDs) > 0 {
		if _, err = tx.Exec(ctx, `
			UPDATE outreach_drafts d
			SET status='NEEDS_REVIEW',approved_by=NULL,approved_at=NULL,
				campaign_id=NULL,enrollment_contact_id=NULL,enrolled_at=NULL,updated_at=now()
			FROM outreach_touchpoints t
			WHERE t.organization_id=$1 AND t.id=ANY($2::uuid[])
			  AND t.draft_id=d.id AND d.organization_id=t.organization_id`, orgID, touchpointIDs); err != nil {
			return 0, err
		}
		if _, err = tx.Exec(ctx, `
			UPDATE outreach_touchpoints
			SET state='NEEDS_REVIEW',approved_content_hash='',approved_by=NULL,approved_at=NULL,
				authorization_mode='',campaign_policy_authorization_id=NULL,authorization_policy_hash='',
				authorization_at=NULL,signature_version='',queued_at=NULL,
				stop_reason='canonical_queue_state_drift',updated_at=now()
			WHERE organization_id=$1 AND id=ANY($2::uuid[])`, orgID, touchpointIDs); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return result.RowsAffected() + int64(len(touchpointIDs)), nil
}

func feedSourceFreshnessFromState(feed *models.OutreachFeedSyncState) *FeedSourceFreshness {
	if feed == nil || feed.SourceGeneratedAt == nil {
		return nil
	}
	f := &FeedSourceFreshness{
		ContractVersion: AuthoritativeFreshnessContractV1,
		Status:          SourceHealthFresh,
		AsOf:            feed.SourceGeneratedAt.UTC().Format(time.RFC3339Nano),
		RunID:           feed.LastRunID,
	}
	if feed.SourceExpiresAt != nil {
		f.ExpiresAt = feed.SourceExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	return f
}

func firstPresentAuthority(parts ...*FeedCommercialAuthority) *FeedCommercialAuthority {
	for _, p := range parts {
		if authorityPresent(p) {
			return p
		}
	}
	return nil
}

func commercialBindingFromStoredFeed(feed *models.OutreachFeedSyncState, payload *FeedCommercialAuthority) CommercialAuthorityBinding {
	semantic, producer := "", ""
	if payload != nil {
		payload.NormalizeAliases()
		semantic = payload.BasisPublicationSemanticHash
		producer = payload.ProducerIdentity
	}
	if feed == nil {
		return CommercialAuthorityBinding{PublicationSemanticHash: semantic, ProducerIdentity: producer}
	}
	return CommercialAuthorityBinding{
		SourceRunID:             feed.LastRunID,
		SnapshotHash:            feed.LastSnapshotHash,
		MembershipHash:          feed.TargetMembershipHash,
		PublicationSemanticHash: semantic,
		ProducerIdentity:        producer,
	}
}

// EvaluateStoredSourceHealth reports acquisition health only. Callers must not
// turn its result into a commercial hold.
func EvaluateStoredSourceHealth(feed *models.OutreachFeedSyncState, now time.Time, maxAge time.Duration) SourceHealthDecision {
	return ClassifySourceHealth(feedSourceFreshnessFromState(feed), now, maxAge)
}

// commercialReadbackV2 projects the V2 qualification decision onto the
// operator readback. Basis fields stay pure provenance.
func commercialReadbackV2(feed *models.OutreachFeedSyncState, d CommercialQualificationDecision) DelegatedFirstTouchAuthorityReadback {
	out := DelegatedFirstTouchAuthorityReadback{
		Present:                            d.Present,
		State:                              firstNonEmpty(d.State, CommercialUnknown),
		NewAdmissionAllowed:                d.AllowsNewAdmission(),
		ExistingBoundTouchTransportAllowed: d.AllowsTransport(),
		ValidUntil:                         d.QualifiedUntil,
		ReasonCodes:                        append([]string{}, d.ReasonCodes...),
	}
	payload := storedCommercialAuthorityV2(feed)
	if payload == nil {
		return out
	}
	out.BasisSourceRunID, out.BasisSnapshotHash = payload.BasisSourceRunID, payload.BasisSnapshotHash
	out.BasisMembershipHash = payload.BasisMembershipHash
	out.BasisPublicationSemanticHash = payload.BasisPublicationSemanticHash
	out.ProducerIdentity = payload.ProducerIdentity
	out.SourceRunID, out.SnapshotID, out.MembershipHash = payload.BasisSourceRunID, payload.BasisSnapshotHash, payload.BasisMembershipHash
	return out
}

func firstHold(reasons []string) string {
	if len(reasons) == 0 {
		return ""
	}
	return reasons[0]
}

func (s *service) delegatedTouchAlreadyBound(ctx context.Context, orgID, accountID uuid.UUID) bool {
	if s == nil || s.delegatedDB == nil || accountID == uuid.Nil {
		return false
	}
	var n int
	err := s.delegatedDB.QueryRow(ctx, `
		SELECT count(*)::int FROM confenge_delegated_first_touch_decisions
		WHERE organization_id=$1 AND account_id=$2
		  AND decision='DELEGATED_POLICY_APPROVE'
		  AND state IN ('APPROVED','APPROVED_NOT_SCHEDULED','QUEUED','SENT')`, orgID, accountID).Scan(&n)
	return err == nil && n > 0
}

func hashText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// SealDelegatedFirstTouchEntry binds exact operational mailbox data without logging it.
func SealDelegatedFirstTouchEntry(entry DelegatedFirstTouchEntry, cand *models.OutreachContactCandidate) (DelegatedFirstTouchEntry, error) {
	if cand == nil {
		return entry, fmt.Errorf("contact candidate not found")
	}
	if entry.ContactCandidateID == uuid.Nil || cand.ID != entry.ContactCandidateID {
		return entry, fmt.Errorf("contact candidate id mismatch")
	}
	if entry.AccountID == uuid.Nil || cand.AccountID != entry.AccountID {
		return entry, fmt.Errorf("contact candidate account mismatch")
	}
	if strings.TrimSpace(entry.Recipient) != "" || strings.TrimSpace(entry.RouteClass) != "" ||
		strings.TrimSpace(entry.SubjectHash) != "" || strings.TrimSpace(entry.BodyHash) != "" {
		return entry, fmt.Errorf("research entry must not contain sealed mailbox or hash fields")
	}
	recipient := strings.ToLower(strings.TrimSpace(cand.Email))
	routeClass := CandidateRouteClass(cand)
	if recipient == "" || routeClass == "" || routeClass == RouteClassProbabilisticOrRisky {
		return entry, fmt.Errorf("contact candidate has no eligible attributable mailbox route")
	}
	entry.Recipient = recipient
	entry.RouteClass = routeClass
	entry.SubjectHash = hashText(entry.Subject)
	entry.BodyHash = hashText(entry.BodyText)
	return entry, nil
}

func manifestHash(manifest DelegatedFirstTouchManifest) string {
	raw, _ := json.Marshal(manifest)
	return hashText(string(raw))
}

func digits(value string) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.IsDigit(r) && r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s *service) ApplyDelegatedFirstTouchManifest(ctx context.Context, orgID uuid.UUID, manifest DelegatedFirstTouchManifest, dryRun bool) (*DelegatedFirstTouchReport, *errx.Error) {
	return s.applyDelegatedFirstTouchManifest(ctx, orgID, manifest, dryRun, nil)
}

func (s *service) applyDelegatedFirstTouchManifest(ctx context.Context, orgID uuid.UUID, manifest DelegatedFirstTouchManifest, dryRun bool, plannedDueAt *time.Time) (*DelegatedFirstTouchReport, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	if !s.cfg.DelegatedFirstTouchEnabled {
		return nil, errx.New(errx.ServiceUnavailable, EnvDelegatedFirstTouch+" is false")
	}
	if s.delegatedDB == nil || s.policyStore == nil || s.governor == nil {
		return nil, errx.New(errx.ServiceUnavailable, "delegated first-touch stores are not fully wired")
	}
	if s.orgRisk == nil || s.orgRisk.SendingSuspended(ctx, orgID) {
		return nil, errx.New(errx.Forbidden, "organization_sending_risk_blocked")
	}
	if !dryRun && strings.TrimSpace(s.cfg.RepositorySHA) == "" {
		return nil, errx.New(errx.ServiceUnavailable, "runtime_release_sha_missing")
	}
	if blockers := validateDelegatedManifestHeader(manifest); len(blockers) > 0 {
		return nil, errx.New(errx.BadRequest, strings.Join(blockers, ","))
	}
	feedLockKey := feedSyncAdvisoryKey(orgID)
	feedLocked, feedLockErr := s.repo.TryAdvisoryLock(ctx, feedLockKey)
	if feedLockErr != nil {
		return nil, errx.New(errx.ServiceUnavailable, "authoritative feed lock unavailable")
	}
	if !feedLocked {
		return nil, errx.New(errx.Conflict, "authoritative feed refresh or delegated approval is already in progress")
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.repo.AdvisoryUnlock(unlockCtx, feedLockKey)
	}()
	auth, err := s.policyStore.GetCampaignPolicyByID(ctx, orgID, manifest.PolicyAuthorizationID)
	if err != nil {
		return nil, errx.New(errx.Internal, "load delegated policy: "+err.Error())
	}
	policyBlockers := append(validateDelegatedPolicy(auth, manifest, time.Now().UTC()), s.validateDelegatedFounderBinding(orgID, auth)...)
	if blockers := uniqueStrings(policyBlockers); len(blockers) > 0 {
		return nil, errx.New(errx.Forbidden, strings.Join(blockers, ","))
	}
	if blockers := s.validateDelegatedTransportAuthority(ctx, orgID, auth); len(blockers) > 0 {
		return nil, errx.New(errx.Forbidden, strings.Join(blockers, ","))
	}
	feedState, feedErr := s.repo.GetFeedSyncState(ctx, orgID)
	if feedErr != nil || feedState == nil {
		return nil, errx.New(errx.Forbidden, "authoritative_feed_attestation_invalid")
	}
	// The snapshot must be structurally whole. Its AGE is acquisition health
	// and is deliberately not consulted here.
	if err := validateAuthoritativeFeedStructure(feedState, true); err != nil {
		return nil, errx.New(errx.Forbidden, "authoritative_feed_attestation_invalid")
	}
	// Same posture as transport: the population attestation only blocks when it
	// is present and not qualified. Absent, the per-account three-year rule in
	// validateDelegatedEntry still gates every entry and fails closed.
	if authority := FeedCommercialAuthorityState(feedState); authority.Present && authority.State != CommercialQualified {
		return nil, errx.New(errx.Forbidden, firstNonEmpty(firstHold(authority.ReasonCodes), ReasonQualificationMissing))
	}
	// Manifest-vs-state integrity, not producer age: this manifest was built
	// against a different snapshot than the one actually applied. A carried
	// forward qualified account is unaffected.
	if feedState.LastRunID != manifest.SourceRunID || feedState.LastSnapshotHash != manifest.SourceSnapshotHash {
		return nil, errx.New(errx.Forbidden, "stale_source_run")
	}
	manifest.authority = feedState
	report := &DelegatedFirstTouchReport{
		BatchID: manifest.BatchID, ManifestHash: manifestHash(manifest), DryRun: dryRun,
		Items: []DelegatedFirstTouchItemResult{}, Generated: len(manifest.Entries),
	}
	duplicateRoots := duplicateManifestRoots(manifest.Entries)
	recentBodies := []delegatedRecentBody{}
	corpusAvailable := true
	if recent, listErr := s.repo.ListDrafts(ctx, orgID, "", 500, 0); listErr != nil {
		corpusAvailable = false
	} else {
		for i := range recent {
			if body := strings.TrimSpace(recent[i].BodyText); body != "" {
				recentBodies = append(recentBodies, delegatedRecentBody{AccountID: recent[i].AccountID, Body: body})
			}
		}
	}
	if !dryRun {
		if err := s.reserveDelegatedBatch(ctx, orgID, manifest, report.ManifestHash); err != nil {
			return nil, errx.New(errx.Conflict, err.Error())
		}
	}
	for i := range manifest.Entries {
		entry := manifest.Entries[i]
		unlock, lockErr := s.lockDelegatedAccount(ctx, orgID, entry.CNPJ14)
		var item DelegatedFirstTouchItemResult
		var qaPass, repaired bool
		if lockErr != nil {
			item = DelegatedFirstTouchItemResult{IdempotencyKey: entry.IdempotencyKey, State: "HOLD", Blockers: []string{"account_batch_lock_failed"}}
			if !dryRun {
				_ = s.persistDelegatedHold(ctx, orgID, manifest, entry, item.Blockers)
			}
		} else {
			item, qaPass, repaired = s.applyDelegatedFirstTouchEntry(ctx, orgID, manifest, auth, entry, dryRun, duplicateRoots, recentBodies, corpusAvailable, plannedDueAt)
			unlock()
		}
		report.Items = append(report.Items, item)
		if item.State == "DRY_RUN_PASS" || item.State == "APPROVED" || item.State == "APPROVED_NOT_SCHEDULED" || item.State == "QUEUED" {
			recentBodies = append(recentBodies, delegatedRecentBody{AccountID: entry.AccountID, Body: entry.BodyText})
		}
		if qaPass {
			report.QAPass++
		}
		if repaired {
			report.QARepaired++
		}
		switch item.State {
		case "DRY_RUN_PASS":
			report.DelegatedApproved++
		case "QUEUED":
			report.DelegatedApproved++
			report.Queued++
		case "APPROVED":
			report.DelegatedApproved++
		case "APPROVED_NOT_SCHEDULED":
			report.DelegatedApproved++
			report.ApprovedNotScheduled++
		default:
			report.Held++
		}
	}
	if !dryRun {
		if err := s.finishDelegatedBatch(ctx, orgID, report); err != nil {
			return report, errx.New(errx.Internal, "delegated batch audit finalization failed: "+err.Error())
		}
	}
	return report, nil
}

func (s *service) lockDelegatedAccount(ctx context.Context, orgID uuid.UUID, cnpj string) (func(), error) {
	conn, err := s.delegatedDB.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	key := orgID.String() + ":" + digits(cnpj)
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, key); err != nil {
		conn.Release()
		return nil, err
	}
	return func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockCtx, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, key)
		conn.Release()
	}, nil
}

func expectedFirstTouchPolicyHash(version string) (string, bool) {
	switch strings.TrimSpace(version) {
	case DelegatedFirstTouchPolicyV1:
		return DelegatedFirstTouchPolicyHashV1, true
	case DelegatedFirstTouchPolicyV2:
		return DelegatedFirstTouchPolicyHashV2, true
	case DelegatedFirstTouchPolicyV3:
		return DelegatedFirstTouchPolicyHashV3, true
	default:
		return "", false
	}
}

func validateDelegatedManifestHeader(manifest DelegatedFirstTouchManifest) []string {
	var out []string
	if manifest.SchemaVersion != DelegatedFirstTouchManifestV1 {
		out = append(out, "manifest_schema_mismatch")
	}
	if strings.TrimSpace(manifest.BatchID) == "" || len(manifest.BatchID) > 160 {
		out = append(out, "batch_id_invalid")
	}
	if strings.TrimSpace(manifest.AgentID) == "" || len(manifest.AgentID) > 160 {
		out = append(out, "agent_id_invalid")
	}
	if known, hold, reason := RecognizeFirstTouchPolicy(manifest.PolicyVersion); !known {
		out = append(out, ReasonPolicyUnknown)
	} else if hold {
		out = append(out, firstNonEmpty(reason, ReasonPolicyHold))
	}
	if expected, ok := expectedFirstTouchPolicyHash(manifest.PolicyVersion); !ok {
		out = append(out, "policy_version_mismatch")
	} else if manifest.PolicyHash != expected {
		out = append(out, "policy_hash_mismatch")
	}
	if manifest.AuthorityReference != DelegatedFirstTouchAuthorityRef {
		out = append(out, "authority_reference_mismatch")
	}
	if manifest.PolicyAuthorizationID == uuid.Nil {
		out = append(out, "policy_authorization_missing")
	}
	if strings.TrimSpace(manifest.SourceRunID) == "" {
		out = append(out, "evidence_run_missing")
	}
	if !delegatedSHA256Pattern.MatchString(manifest.SourceSnapshotHash) {
		out = append(out, "source_snapshot_hash_invalid")
	}
	if manifest.EvidenceVersion != DelegatedFirstTouchEvidenceV1 {
		out = append(out, "evidence_version_mismatch")
	}
	if manifest.ComposerVersion != ComposerVersion || manifest.TemplateVersion != DelegatedFirstTouchTemplateV1 || manifest.PromptVersion != PromptVersion {
		out = append(out, "copy_runtime_version_mismatch")
	}
	if manifest.GeneratedAt.IsZero() || manifest.GeneratedAt.After(time.Now().UTC().Add(5*time.Minute)) {
		out = append(out, "manifest_generated_at_invalid")
	}
	if len(manifest.Entries) == 0 || len(manifest.Entries) > 1000 {
		out = append(out, "manifest_entry_count_invalid")
	}
	return out
}

func validateDelegatedPolicy(auth *models.CampaignPolicyAuthorization, manifest DelegatedFirstTouchManifest, now time.Time) []string {
	var out []string
	if auth == nil || !auth.Active(now) || auth.ID != manifest.PolicyAuthorizationID {
		out = append(out, "delegated_policy_not_active")
		return out
	}
	if auth.PromptPolicyVersion != DelegatedFirstTouchPolicyV1 || auth.ValidatorVersion != DelegatedFirstTouchValidatorV1 ||
		auth.ContactPolicyVersion != DelegatedFirstTouchContactPolicyV1 || auth.TemplatePolicyVersion != DelegatedFirstTouchTemplateV1 ||
		!auth.AllowPolicyTemplateGREEN {
		out = append(out, "delegated_policy_contract_mismatch")
	}
	if auth.AuthorizedBy == uuid.Nil || auth.AuthorizedByLabel != DelegatedFirstTouchAuthority {
		out = append(out, "founder_authority_missing")
	}
	if !strings.EqualFold(auth.Channel, models.OutreachChannelEmail) || !strings.EqualFold(auth.AllowedRiskClass, "GREEN") {
		out = append(out, "delegated_policy_scope_mismatch")
	}
	if strings.TrimSpace(auth.SenderMailbox) == "" || auth.MaxRatePerHour < 1 {
		out = append(out, "delegated_policy_transport_bounds_missing")
	}
	return out
}

func (s *service) validateDelegatedFounderBinding(orgID uuid.UUID, auth *models.CampaignPolicyAuthorization) []string {
	if auth == nil || s.cfg.OperatorOrgID == uuid.Nil || s.cfg.OperatorUserID == uuid.Nil ||
		orgID != s.cfg.OperatorOrgID || auth.AuthorizedBy != s.cfg.OperatorUserID {
		return []string{"founder_authority_binding_mismatch"}
	}
	return nil
}

// validateDelegatedTransportAuthority binds delegated approval to the existing
// CONFENGE campaign pointer and to the same mailbox records used by campaign
// transport. It deliberately does not turn the observe-only DNS auth state into
// a new gate. Global pause and kill-switch remain final transport controls.
func (s *service) validateDelegatedTransportAuthority(ctx context.Context, orgID uuid.UUID, auth *models.CampaignPolicyAuthorization) []string {
	settings, err := s.repo.GetOrgSettings(ctx, orgID)
	if err != nil || settings == nil || settings.CampaignID == nil || *settings.CampaignID == uuid.Nil {
		return []string{"canonical_campaign_pointer_unavailable"}
	}
	if auth == nil || auth.CampaignID != *settings.CampaignID {
		return []string{"delegated_policy_campaign_mismatch"}
	}
	var state delegatedTransportAuthority
	err = s.delegatedDB.QueryRow(ctx, `
		SELECT c.status::text,ea.status::text,ea.risk_band::text,
		       ea.worker_id IS NOT NULL,
		       EXISTS (
		         SELECT 1 FROM workers w WHERE w.id=ea.worker_id AND w.active
		           AND w.last_seen_at > now() - interval '10 minutes'
		       ),
		       CASE WHEN ea.provider='smtp_imap' THEN EXISTS (
		           SELECT 1 FROM email_accounts_smtp_imap smtp WHERE smtp.email_account_id=ea.id
		       ) ELSE EXISTS (
		           SELECT 1 FROM email_accounts_oauth oauth WHERE oauth.email_account_id=ea.id
		       ) END,
		       CASE
		         WHEN EXISTS (SELECT 1 FROM campaign_senders cs WHERE cs.campaign_id=c.id AND cs.enabled)
		           THEN EXISTS (SELECT 1 FROM campaign_senders cs WHERE cs.campaign_id=c.id AND cs.email_account_id=ea.id AND cs.enabled)
		         WHEN EXISTS (SELECT 1 FROM campaign_email_tags cet WHERE cet.campaign_id=c.id)
		           THEN EXISTS (
		             SELECT 1 FROM campaign_email_tags cet
		             JOIN email_tags et ON et.tag_id=cet.tag_id
		             WHERE cet.campaign_id=c.id AND et.email_id=ea.id
		           )
		         ELSE true
		       END,
		       ea.campaign_limit,ea.min_wait_time,
		       ea.auth_state,ea.auth_spf,ea.auth_dkim,ea.auth_dmarc,ea.auth_checked_at
		FROM campaigns c
		JOIN email_accounts ea ON ea.user_id=c.user_id
		WHERE c.id=$2 AND c.organization_id=$1
		  AND ea.organization_id=$1 AND lower(ea.email)=lower($3)
	`, orgID, auth.CampaignID, strings.TrimSpace(auth.SenderMailbox)).Scan(
		&state.CampaignStatus, &state.MailboxStatus, &state.MailboxRiskBand,
		&state.WorkerAssigned, &state.WorkerHealthy, &state.CredentialsPresent, &state.SenderSelected,
		&state.CampaignLimit, &state.MinWaitTime, &state.AuthState, &state.AuthSPF,
		&state.AuthDKIM, &state.AuthDMARC, &state.AuthCheckedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return []string{"canonical_sender_mailbox_not_found"}
		}
		return []string{"canonical_sender_mailbox_authority_unavailable"}
	}
	out := validateDelegatedTransportState(state)
	if s.governor == nil {
		out = append(out, "canonical_scheduler_unavailable")
	} else {
		out = append(out, validateDelegatedRuntimeTransportBounds(state, auth.MaxRatePerHour, s.governor.Config())...)
	}
	return uniqueStrings(out)
}

func validateDelegatedTransportState(state delegatedTransportAuthority) []string {
	var out []string
	switch state.CampaignStatus {
	case "draft", "active", "paused":
	default:
		out = append(out, "canonical_campaign_not_schedulable")
	}
	if state.MailboxStatus != "active" {
		out = append(out, "canonical_sender_mailbox_inactive")
	}
	if state.MailboxRiskBand == "quarantine" {
		out = append(out, "canonical_sender_mailbox_quarantined")
	}
	if !state.WorkerAssigned {
		out = append(out, "canonical_sender_mailbox_worker_missing")
	}
	if state.WorkerAssigned && !state.WorkerHealthy {
		out = append(out, "canonical_sender_mailbox_worker_unhealthy")
	}
	if !state.CredentialsPresent {
		out = append(out, "canonical_sender_mailbox_credentials_missing")
	}
	if !state.SenderSelected {
		out = append(out, "canonical_sender_mailbox_not_in_campaign_pool")
	}
	if state.AuthCheckedAt == nil || !strings.EqualFold(state.AuthState, "passing") ||
		!state.AuthSPF || !state.AuthDKIM || !state.AuthDMARC {
		out = append(out, "canonical_sender_mailbox_dns_auth_not_passing")
	}
	if state.CampaignLimit < 1 || state.MinWaitTime < 1 {
		out = append(out, "canonical_sender_mailbox_rate_bounds_invalid")
	}
	return uniqueStrings(out)
}

func validateDelegatedRuntimeTransportBounds(state delegatedTransportAuthority, policyMaxRate int, cfg dispatch.Config) []string {
	var out []string
	if policyMaxRate < 1 || (state.CampaignLimit > 0 && policyMaxRate > state.CampaignLimit) {
		out = append(out, "delegated_policy_rate_exceeds_mailbox_bounds")
	}
	if state.MinWaitTime < 1 || cfg.MinGap < time.Duration(state.MinWaitTime)*time.Second {
		out = append(out, "canonical_scheduler_min_gap_below_mailbox")
	}
	return out
}

func duplicateManifestRoots(entries []DelegatedFirstTouchEntry) map[string]bool {
	counts := map[string]int{}
	for i := range entries {
		cnpj := digits(entries[i].CNPJ14)
		if len(cnpj) == 14 {
			counts[cnpj[:8]]++
		}
	}
	out := map[string]bool{}
	for root, count := range counts {
		out[root] = count > 1
	}
	return out
}

func (s *service) applyDelegatedFirstTouchEntry(
	ctx context.Context,
	orgID uuid.UUID,
	manifest DelegatedFirstTouchManifest,
	auth *models.CampaignPolicyAuthorization,
	entry DelegatedFirstTouchEntry,
	dryRun bool,
	duplicateRoots map[string]bool,
	recentBodies []delegatedRecentBody,
	corpusAvailable bool,
	plannedDueAt *time.Time,
) (DelegatedFirstTouchItemResult, bool, bool) {
	item := DelegatedFirstTouchItemResult{IdempotencyKey: entry.IdempotencyKey}
	acc, cand, blockers := s.validateDelegatedEntry(ctx, orgID, manifest, entry, duplicateRoots, recentBodies, corpusAvailable)
	qaPass := entry.QA.Result == "PASS" && entry.QA.IdentityPassed && entry.QA.FactualPassed && entry.QA.CopyPassed && entry.QA.OperationalPassed
	if len(blockers) > 0 {
		item.State, item.Blockers = "HOLD", blockers
		if !dryRun {
			_ = s.persistDelegatedHold(ctx, orgID, manifest, entry, blockers)
		}
		return item, qaPass, entry.QA.Repaired
	}
	if dryRun {
		item.State = "DRY_RUN_PASS"
		return item, true, entry.QA.Repaired
	}
	if existing, err := s.loadDelegatedDecision(ctx, orgID, entry.IdempotencyKey); err == nil && existing != nil {
		if existing.SubjectHash != entry.SubjectHash || existing.BodyHash != entry.BodyHash ||
			existing.EvidenceHash != entry.ContractEvidenceHash || existing.MaterialBindingHash != delegatedMaterialBinding(manifest, entry) {
			item.State, item.Blockers = "HOLD", []string{"idempotency_payload_mismatch"}
			return item, true, entry.QA.Repaired
		}
		item.Idempotent = true
		if existing.State == "HOLD" {
			item.State, item.Blockers = "HOLD", existing.BlockerCodes
			return item, true, entry.QA.Repaired
		}
		if existing.TouchpointID != nil {
			item.TouchpointID = *existing.TouchpointID
		}
		if existing.State == "QUEUED" || existing.State == "SENT" {
			item.State, item.DueAt = existing.State, existing.DueAt
			return item, true, entry.QA.Repaired
		}
		if existing.TouchpointID != nil && (existing.State == "APPROVED" || existing.State == "APPROVED_NOT_SCHEDULED") {
			queued, xerr := s.queueTouchpointAt(ctx, orgID, uuid.Nil, *existing.TouchpointID, plannedDueAt)
			if xerr != nil || queued == nil {
				item.State = "APPROVED_NOT_SCHEDULED"
				item.Blockers = []string{schedulingBlocker(xerr)}
				if err := s.updateDelegatedScheduling(ctx, orgID, entry.IdempotencyKey, item.State, "", nil, false, item.Blockers); err != nil {
					_ = s.cancelDelegatedDecision(ctx, orgID, *existing.TouchpointID, "canonical_queue_audit_store_failed")
					item.State = "HOLD"
					item.Blockers = appendUnique(item.Blockers, "canonical_queue_audit_store_failed")
				}
				return item, true, entry.QA.Repaired
			}
			due, messageKey, ok := s.delegatedQueueReadback(ctx, orgID, queued)
			if !ok {
				item.State, item.DueAt = "APPROVED_NOT_SCHEDULED", due
				item.Blockers = []string{"canonical_queue_readback_failed"}
				if err := s.updateDelegatedScheduling(ctx, orgID, entry.IdempotencyKey, item.State, messageKey, due, false, item.Blockers); err != nil {
					_ = s.cancelDelegatedDecision(ctx, orgID, *existing.TouchpointID, "canonical_queue_audit_store_failed")
					item.State, item.DueAt = "HOLD", nil
					item.Blockers = appendUnique(item.Blockers, "canonical_queue_audit_store_failed")
				}
				return item, true, entry.QA.Repaired
			}
			item.State, item.DueAt = "QUEUED", due
			if err := s.updateDelegatedScheduling(ctx, orgID, entry.IdempotencyKey, item.State, messageKey, due, true, nil); err != nil {
				_ = s.cancelDelegatedDecision(ctx, orgID, *existing.TouchpointID, "canonical_queue_audit_store_failed")
				item.State, item.DueAt = "HOLD", nil
				item.Blockers = []string{"canonical_queue_audit_store_failed"}
			}
			return item, true, entry.QA.Repaired
		}
		item.State, item.Blockers = "HOLD", []string{"idempotency_decision_not_replayable"}
		return item, true, entry.QA.Repaired
	}
	tp, draft, err := s.prepareDelegatedTouchpoint(ctx, orgID, acc, cand, manifest, entry)
	if err != nil {
		item.State, item.Blockers = "HOLD", []string{err.Error()}
		_ = s.persistDelegatedHold(ctx, orgID, manifest, entry, item.Blockers)
		return item, true, entry.QA.Repaired
	}
	now := time.Now().UTC()
	if err := ApplyCampaignPolicyAuthorization(tp, auth, now); err != nil {
		item.State, item.Blockers = "HOLD", []string{"delegated_authorization_failed"}
		_ = s.persistDelegatedHold(ctx, orgID, manifest, entry, item.Blockers)
		return item, true, entry.QA.Repaired
	}
	draft.Status, draft.ApprovedBy, draft.ApprovedAt = models.OutreachDraftApproved, nil, &now
	if err := s.repo.UpsertDraft(ctx, draft); err != nil {
		item.State, item.Blockers = "HOLD", []string{"delegated_draft_approval_store_failed"}
		return item, true, entry.QA.Repaired
	}
	if err := s.repo.UpdateTouchpoint(ctx, tp); err != nil {
		item.State, item.Blockers = "HOLD", []string{"delegated_touchpoint_approval_store_failed"}
		draft.Status, draft.ApprovedBy, draft.ApprovedAt = models.OutreachDraftNeedsReview, nil, nil
		_ = s.repo.UpsertDraft(ctx, draft)
		return item, true, entry.QA.Repaired
	}
	if err := s.persistDelegatedApproval(ctx, orgID, manifest, entry, tp, draft); err != nil {
		item.State, item.Blockers = "HOLD", []string{"delegated_decision_store_failed"}
		ClearApproval(tp)
		tp.State = models.TouchpointNeedsReview
		draft.Status, draft.ApprovedBy, draft.ApprovedAt = models.OutreachDraftNeedsReview, nil, nil
		_ = s.repo.UpdateTouchpoint(ctx, tp)
		_ = s.repo.UpsertDraft(ctx, draft)
		return item, true, entry.QA.Repaired
	}
	item.State, item.TouchpointID = "APPROVED", tp.ID
	queued, xerr := s.queueTouchpointAt(ctx, orgID, uuid.Nil, tp.ID, plannedDueAt)
	if xerr != nil || queued == nil {
		item.State = "APPROVED_NOT_SCHEDULED"
		item.Blockers = []string{schedulingBlocker(xerr)}
		if err := s.updateDelegatedScheduling(ctx, orgID, entry.IdempotencyKey, item.State, "", nil, false, item.Blockers); err != nil {
			_ = s.cancelDelegatedDecision(ctx, orgID, tp.ID, "canonical_queue_audit_store_failed")
			item.State = "HOLD"
			item.Blockers = appendUnique(item.Blockers, "canonical_queue_audit_store_failed")
		}
		return item, true, entry.QA.Repaired
	}
	due, messageKey, ok := s.delegatedQueueReadback(ctx, orgID, queued)
	if !ok {
		item.State = "APPROVED_NOT_SCHEDULED"
		item.Blockers = []string{"canonical_queue_readback_failed"}
		if err := s.updateDelegatedScheduling(ctx, orgID, entry.IdempotencyKey, item.State, messageKey, due, false, item.Blockers); err != nil {
			_ = s.cancelDelegatedDecision(ctx, orgID, tp.ID, "canonical_queue_audit_store_failed")
			item.State, item.DueAt = "HOLD", nil
			item.Blockers = appendUnique(item.Blockers, "canonical_queue_audit_store_failed")
		}
		return item, true, entry.QA.Repaired
	}
	item.State, item.DueAt = "QUEUED", due
	if err := s.updateDelegatedScheduling(ctx, orgID, entry.IdempotencyKey, item.State, messageKey, due, true, nil); err != nil {
		_ = s.cancelDelegatedDecision(ctx, orgID, tp.ID, "canonical_queue_audit_store_failed")
		item.State, item.DueAt = "HOLD", nil
		item.Blockers = []string{"canonical_queue_audit_store_failed"}
	}
	return item, true, entry.QA.Repaired
}

func schedulingBlocker(xerr *errx.Error) string {
	if xerr == nil {
		return "canonical_scheduling_failed"
	}
	message := strings.ToLower(xerr.Message)
	switch {
	case strings.Contains(message, "kill switch"):
		return "canonical_scheduling_failed:kill_switch_engaged"
	case strings.Contains(message, "context"):
		return "canonical_scheduling_failed:context_stale"
	case strings.Contains(message, "delegated decision"):
		return "canonical_scheduling_failed:delegated_decision_invalid"
	case strings.Contains(message, "policy"):
		return "canonical_scheduling_failed:policy_invalid"
	case strings.Contains(message, "recipient") || strings.Contains(message, "contact"):
		return "canonical_scheduling_failed:recipient_invalid"
	case strings.Contains(message, "target-fit"):
		return "canonical_scheduling_failed:target_fit_invalid"
	default:
		return "canonical_scheduling_failed:transport_gate"
	}
}

func (s *service) validateDelegatedEntry(ctx context.Context, orgID uuid.UUID, manifest DelegatedFirstTouchManifest, entry DelegatedFirstTouchEntry, duplicateRoots map[string]bool, recentBodies []delegatedRecentBody, corpusAvailable bool) (*models.OutreachAccount, *models.OutreachContactCandidate, []string) {
	blockers := []string{}
	add := func(code string) { blockers = appendUnique(blockers, code) }
	cnpj := digits(entry.CNPJ14)
	if strings.TrimSpace(entry.IdempotencyKey) == "" || len(entry.IdempotencyKey) > 240 {
		add("idempotency_key_invalid")
	}
	if strings.TrimSpace(entry.CorrelationID) == "" || len(entry.CorrelationID) > 240 {
		add("correlation_id_invalid")
	}
	blockers = append(blockers, validateDelegatedPartyRole(entry)...)
	if len(cnpj) == 14 && duplicateRoots[cnpj[:8]] {
		add("multiple_routes_for_account_root")
	}
	if strings.TrimSpace(entry.ContractRoleSource) == "" || len(entry.ContractEvidenceIDs) == 0 || entry.EvidenceObservedAt.IsZero() {
		add("contractor_role_provenance_missing")
	}
	if strings.TrimSpace(entry.ContractRoleSource) != "extra-cli:v_contracts_canonical_v2" {
		add("contractor_role_source_not_authoritative")
	}
	for _, id := range entry.ContractEvidenceIDs {
		if strings.TrimSpace(id) == "" {
			add("contractor_role_provenance_missing")
		}
	}
	// No age ceiling: a proven contractor role does not stop being true because
	// it was observed a while ago. Only future-dating is impossible.
	if entry.EvidenceObservedAt.IsZero() || entry.EvidenceObservedAt.After(time.Now().UTC().Add(5*time.Minute)) {
		add("contractor_role_evidence_stale")
	}
	if !delegatedSHA256Pattern.MatchString(entry.ContractEvidenceHash) {
		add("contractor_role_evidence_hash_invalid")
	}
	if entry.ContractEvidenceReference != "extra-cli:v_contracts_canonical_v2:sha256:"+entry.ContractEvidenceHash {
		add("contractor_role_evidence_reference_invalid")
	}
	if entry.ReconciliationStatus != ReconciliationCorroborated && entry.ReconciliationStatus != ReconciliationWebContact {
		add("identity_contact_reconciliation_blocked")
	}
	if len(entry.WebSources) == 0 {
		add("web_source_missing")
	}
	for i := range entry.WebSources {
		if !delegatedWebSourceAllowed(entry.WebSources[i], time.Now().UTC()) {
			add("web_source_invalid")
		}
	}
	if entry.ReconciliationStatus == ReconciliationCorroborated && !webIdentityCorroborated(entry.WebSources) {
		add("web_identity_corroboration_missing")
	}
	if entry.AccountID == uuid.Nil || entry.ContactCandidateID == uuid.Nil {
		add("account_or_contact_id_missing")
	}
	acc, err := s.repo.GetAccount(ctx, orgID, entry.AccountID)
	if err != nil || acc == nil {
		add("account_not_found")
		return acc, nil, blockers
	}
	if acc.CNPJ14 != cnpj {
		add("account_cnpj_mismatch")
	}
	feedState, feedErr := s.repo.GetFeedSyncState(ctx, orgID)
	if feedErr != nil || feedState == nil {
		add("authoritative_feed_state_unavailable")
	} else {
		now := s.now()
		if err := validateAuthoritativeFeedStructure(feedState, true); err != nil {
			add("authoritative_feed_attestation_invalid")
		}
		// A present-but-unqualified population attestation blocks; an absent one
		// falls through to the per-account rule, which is never optional.
		if authority := FeedCommercialAuthorityState(feedState); authority.Present && authority.State != CommercialQualified {
			add(firstNonEmpty(firstHold(authority.ReasonCodes), ReasonQualificationMissing))
		}
		// The company itself must still be inside the rolling three-year
		// window. Producer age never substitutes for this and never revokes it.
		qual := AccountCommercialQualification(acc, now)
		if !qual.AllowsNewAdmission() {
			add(firstNonEmpty(firstHold(qual.ReasonCodes), ReasonQualificationMissing))
		}
		// Manifest-vs-state integrity, not producer age. See applyDelegated.
		if manifest.SourceRunID != feedState.LastRunID || manifest.SourceSnapshotHash != feedState.LastSnapshotHash {
			add("stale_source_run")
		}
	}
	blockers = append(blockers, validatePersistedContractorRole(acc, entry)...)
	if err := RequireTargetFit(acc); err != nil {
		add("target_confirmed_gate_failed")
	}
	if acc.DoNotContact || acc.Blocked || acc.QueueState == models.OutreachQueueBounced || acc.QueueState == models.OutreachQueueReplied {
		add("account_suppressed_or_interacted")
	}
	cand, err := s.repo.GetCandidate(ctx, orgID, entry.ContactCandidateID)
	if err != nil || cand == nil || cand.AccountID != acc.ID {
		add("contact_candidate_not_found")
		return acc, cand, blockers
	}
	if acc.LastImportRunID == nil || cand.LastImportRunID == nil || *acc.LastImportRunID != *cand.LastImportRunID {
		add("recipient_stale_import_run")
	}
	if !strings.EqualFold(strings.TrimSpace(cand.Email), strings.TrimSpace(entry.Recipient)) {
		add("recipient_candidate_mismatch")
	}
	if !CandidateControlledEligible(cand) || !ControlledRouteAllowed(cand, nil) || !CandidateEnrollable(cand) {
		add("recipient_not_controlled_eligible")
	}
	// Public application requires delegatedDB before entering this validator.
	// The nil branch exists only for the pure deterministic gate unit tests.
	if s.delegatedDB != nil {
		if conflict, conflictErr := s.delegatedRecipientSharedAcrossCNPJIdentities(ctx, orgID, cand.Email); conflictErr != nil {
			add("recipient_identity_conflict_check_unavailable")
		} else if conflict {
			add("recipient_shared_across_cnpj_identities")
		}
	}
	if strings.EqualFold(strings.TrimSpace(cand.EmailDerivation), "INFERRED") ||
		!strings.EqualFold(strings.TrimSpace(cand.ChannelEpistemic), "OBSERVED") ||
		!strings.EqualFold(strings.TrimSpace(cand.RouteFreshness), "FRESH") ||
		!strings.EqualFold(strings.TrimSpace(cand.OwnershipStatus), "COMPANY_OWNED") ||
		(strings.TrimSpace(cand.RouteSuppression) != "" && !strings.EqualFold(strings.TrimSpace(cand.RouteSuppression), "NONE")) {
		add("recipient_attribution_or_freshness_invalid")
	}
	if CandidateRouteClass(cand) != strings.ToUpper(strings.TrimSpace(entry.RouteClass)) {
		add("route_class_mismatch")
	}
	if !candidateSourceCorroborated(cand, entry.WebSources) {
		add("mailbox_company_association_unproven")
	}
	if err := RequireEmailOutbound(acc, cand); err != nil {
		add("email_outbound_gate_failed")
	}
	blockers = append(blockers, validateDelegatedCopy(entry, acc, cand)...)
	if !corpusAvailable {
		add("near_duplicate_authority_unavailable")
	} else {
		blockers = append(blockers, s.validateDelegatedDeterministicQA(ctx, orgID, entry, acc, cand, recentBodies)...)
	}
	return acc, cand, uniqueStrings(blockers)
}

func (s *service) validateDelegatedDeterministicQA(ctx context.Context, orgID uuid.UUID, entry DelegatedFirstTouchEntry, acc *models.OutreachAccount, cand *models.OutreachContactCandidate, recentBodies []delegatedRecentBody) []string {
	evidence, err := s.repo.ListEvidence(ctx, orgID, acc.ID)
	if err != nil {
		return []string{"evidence_bundle_unavailable"}
	}
	var blockers []string
	add := func(code string) { blockers = appendUnique(blockers, code) }
	if entry.CopyRulesVersion != DelegatedFirstTouchCopyRulesV1 {
		add("copy_rules_version_mismatch")
	}
	expected := buildDelegatedRoutingCopy(acc, cand, evidence)
	if expected.Subject == "" || expected.Body == "" {
		add("deterministic_copy_unavailable")
	} else {
		if entry.Subject != expected.Subject || entry.BodyText != expected.Body ||
			entry.FactUsed != expected.FactUsed || entry.Practice != expected.Practice ||
			entry.CTA != expected.CTA || entry.SemanticSignature != expected.SemanticSignature {
			add("deterministic_copy_projection_mismatch")
		}
		if canonicalStringSet(entry.FactEvidenceIDs) != canonicalStringSet(expected.FactEvidenceIDs) {
			add("fact_evidence_binding_mismatch")
		}
	}
	wantEvidence := uniqueStrings(append(append([]string{}, entry.ContractEvidenceIDs...), entry.FactEvidenceIDs...))
	if len(entry.ContractEvidenceIDs) == 0 || canonicalStringSet(entry.EvidenceIDs) != canonicalStringSet(wantEvidence) {
		add("fact_evidence_binding_mismatch")
	}
	if admissionNow := time.Now().UTC(); !delegatedEvidenceRowsCurrent(evidence, entry.EvidenceIDs, acc.LastImportRunID, admissionNow, admissionNow) {
		add("fact_evidence_not_current_confirmed")
	}
	out := &DraftOutput{
		Channel: ChannelEmailInitial, Subject: entry.Subject, BodyText: entry.BodyText,
		FactUsed: entry.FactUsed, EvidenceIDs: entry.EvidenceIDs,
		Claims:      []DraftClaim{{Phrase: "Atuação como contratada no setor público confirmada.", EvidenceIDs: entry.ContractEvidenceIDs}},
		ServiceCode: acc.ServiceCode, Question: "Quem é o responsável interno por contratos públicos?",
		CTA: expected.CTA, Followups: []DraftFollowup{}, RiskFlags: []string{},
	}
	if len(entry.FactEvidenceIDs) > 0 {
		out.Claims = append(out.Claims, DraftClaim{Phrase: entry.FactUsed, EvidenceIDs: entry.FactEvidenceIDs})
	}
	for i := range recentBodies {
		if recentBodies[i].AccountID != acc.ID && normalizeForCorpus(recentBodies[i].Body) == normalizeForCorpus(entry.BodyText) {
			add("corpus_exact_content_limit")
			break
		}
	}
	qa := ValidateDraft(out, acc, cand, ValidateOpts{
		MaxWords: s.cfg.MaxInitialEmailWords, Evidence: evidence, Channel: ChannelEmailInitial,
		PromptVersion: PromptVersion,
	})
	for _, item := range qa.Errors {
		code := "deterministic_qa:" + reasonCode(item)
		add(code)
	}
	if risk, _ := ClassifyRisk(acc, cand, out, qa); risk != "GREEN" {
		add("deterministic_qa:risk_class_not_green")
	}
	return blockers
}

// ref is the instant the currency question is asked (now at admission, the
// decision instant at transport); now still caps the absolute ceiling.
func delegatedEvidenceRowsCurrent(evidence []models.OutreachEvidence, required []string, importID *uuid.UUID, ref, now time.Time) bool {
	if len(required) == 0 || importID == nil || *importID == uuid.Nil {
		return false
	}
	current := map[string]bool{}
	for i := range evidence {
		row := evidence[i]
		if row.EpistemicClass != models.OutreachEpistemicConfirmedFact || row.LastImportRunID == nil || *row.LastImportRunID != *importID ||
			row.ConsultedAt == nil || row.ConsultedAt.After(now.Add(5*time.Minute)) ||
			ref.Sub(row.ConsultedAt.UTC()) > delegatedContactEvidenceWindow ||
			now.Sub(row.ConsultedAt.UTC()) > delegatedContactEvidenceCeiling {
			continue
		}
		if id := strings.TrimSpace(row.SourceEvidenceID); id != "" {
			current[id] = true
		}
		if row.ID != uuid.Nil {
			current[row.ID.String()] = true
		}
	}
	for _, id := range required {
		if !current[strings.TrimSpace(id)] {
			return false
		}
	}
	return true
}

func reasonCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else if b.Len() > 0 && !strings.HasSuffix(b.String(), "_") {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func validateDelegatedPartyRole(entry DelegatedFirstTouchEntry) []string {
	var out []string
	cnpj, supplier, buyer := digits(entry.CNPJ14), digits(entry.SupplierCNPJ14), digits(entry.BuyerCNPJ14)
	if len(cnpj) != 14 || len(supplier) != 14 || len(buyer) != 14 {
		out = append(out, "party_cnpj_invalid")
		return out
	}
	if entry.ContractorRoleStatus != ContractorRoleConfirmed {
		out = append(out, "contractor_role_not_confirmed")
	}
	if strings.ToUpper(strings.TrimSpace(entry.TargetPartyRole)) != "SUPPLIER" {
		out = append(out, "target_party_role_not_supplier")
	}
	if cnpj != supplier {
		out = append(out, "lead_supplier_identity_mismatch")
		if cnpj[:8] == supplier[:8] {
			out = append(out, "supplier_branch_binding_unproven")
		}
	}
	if cnpj == buyer || cnpj[:8] == buyer[:8] {
		out = append(out, "party_role_inversion")
	}
	if entry.SupplierIdentityRef != "cnpj:"+supplier || entry.BuyerIdentityRef != "cnpj:"+buyer {
		out = append(out, "party_identity_reference_mismatch")
	}
	method := strings.ToUpper(strings.TrimSpace(entry.RoleMatchMethod))
	if method != "SUPPLIER_EXACT_CNPJ14" {
		out = append(out, "supplier_role_match_method_invalid")
	}
	confidence := strings.ToUpper(strings.TrimSpace(entry.RoleConfidence))
	if confidence != "HIGH" {
		out = append(out, "supplier_role_confidence_invalid")
	}
	if len(entry.ContractRoleReasonCodes) == 0 || strings.TrimSpace(entry.ContractEvidenceReference) == "" {
		out = append(out, "party_role_audit_missing")
	}
	if !containsStr(entry.ContractRoleReasonCodes, "lead_matches_supplier") || !containsStr(entry.ContractRoleReasonCodes, "lead_differs_from_buyer") {
		out = append(out, "party_role_reason_codes_invalid")
	}
	return out
}

func validatePersistedContractorRole(acc *models.OutreachAccount, entry DelegatedFirstTouchEntry) []string {
	if acc == nil {
		return []string{"contractor_role_account_missing"}
	}
	var out []string
	add := func(code string) { out = appendUnique(out, code) }
	if acc.ContractorRoleStatus != ContractorRoleConfirmed || acc.TargetPartyRole != "SUPPLIER" {
		add("persisted_contractor_role_not_confirmed")
	}
	if acc.ContractorRolePolicyVersion != DelegatedFirstTouchEvidenceV1 || acc.ContractorRoleSource != "extra-cli:v_contracts_canonical_v2" {
		add("persisted_contractor_role_authority_mismatch")
	}
	// No run-id equality here: which run emitted the role is provenance, and
	// the semantic bindings below are what actually prove it.
	if acc.ContractorRoleObservedAt == nil || !acc.ContractorRoleObservedAt.Equal(entry.EvidenceObservedAt.UTC()) {
		add("contractor_role_observed_at_mismatch")
	}
	if acc.CNPJ14 != digits(entry.CNPJ14) || acc.SupplierCNPJ14 != digits(entry.SupplierCNPJ14) || acc.BuyerCNPJ14 != digits(entry.BuyerCNPJ14) {
		add("persisted_party_identity_mismatch")
	}
	if acc.ContractorRoleEvidenceHash != entry.ContractEvidenceHash || acc.ContractorRoleEvidenceReference != entry.ContractEvidenceReference ||
		acc.SupplierIdentityRef != entry.SupplierIdentityRef || acc.BuyerIdentityRef != entry.BuyerIdentityRef {
		add("contractor_role_evidence_binding_mismatch")
	}
	if acc.ContractorRoleMatchMethod != strings.ToUpper(strings.TrimSpace(entry.RoleMatchMethod)) ||
		acc.ContractorRoleConfidence != strings.ToUpper(strings.TrimSpace(entry.RoleConfidence)) {
		add("contractor_role_method_or_confidence_drift")
	}
	if canonicalStringSet(acc.ContractorRoleEvidenceIDs) != canonicalStringSet(entry.ContractEvidenceIDs) ||
		canonicalStringSet(acc.ContractorRoleReasonCodes) != canonicalStringSet(entry.ContractRoleReasonCodes) {
		add("contractor_role_evidence_set_drift")
	}
	return uniqueStrings(out)
}

func canonicalStringSet(values []string) string {
	out := append([]string{}, values...)
	for i := range out {
		out[i] = strings.TrimSpace(out[i])
	}
	sort.Strings(out)
	return strings.Join(out, "\x00")
}

// ref is the instant the freshness question is legitimately asked: now at
// admission, the decision instant at transport.
func delegatedWebSourceObservedFresh(source DelegatedWebSource, ref time.Time) bool {
	return !source.ObservedAt.IsZero() && !source.ObservedAt.After(ref.Add(5*time.Minute)) &&
		ref.Sub(source.ObservedAt) <= delegatedContactEvidenceWindow
}

// delegatedWebSourceTransportable is the absolute ceiling at send time: a
// decision parked past the approval window plus the widest runway must be
// re-proved, not shipped.
func delegatedWebSourceTransportable(source DelegatedWebSource, now time.Time) bool {
	return !source.ObservedAt.IsZero() && !source.ObservedAt.After(now.Add(5*time.Minute)) &&
		now.Sub(source.ObservedAt) <= delegatedContactEvidenceCeiling
}

func delegatedWebSourceSupportsMailbox(source DelegatedWebSource) bool {
	switch strings.ToUpper(strings.TrimSpace(source.Supports)) {
	case "COMPANY_IDENTITY", "COMPANY_MAILBOX", "COMPANY_IDENTITY_AND_MAILBOX":
		return true
	default:
		return false
	}
}

func delegatedWebSourceIsOfficialRegistry(source DelegatedWebSource) bool {
	switch strings.ToUpper(strings.TrimSpace(source.Kind)) {
	case DelegatedWebSourceKindOfficialRegistry, "COMPANY_REGISTRY", "OFFICIAL_REGISTRY":
		return true
	default:
		return false
	}
}

func delegatedWebSourceAllowed(source DelegatedWebSource, now time.Time) bool {
	if !delegatedWebSourceObservedFresh(source, now) || !delegatedWebSourceSupportsMailbox(source) {
		return false
	}
	if delegatedWebSourceIsOfficialRegistry(source) {
		return strings.TrimSpace(source.URL) == ""
	}
	u, err := url.Parse(strings.TrimSpace(source.URL))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, blocked := range []string{"google.", "bing.", "duckduckgo.", "search.yahoo."} {
		if strings.Contains(host, blocked) {
			return false
		}
	}
	return true
}

func candidateSourceCorroborated(cand *models.OutreachContactCandidate, sources []DelegatedWebSource) bool {
	if cand == nil {
		return false
	}
	if candidateIsObservedRegistry(cand) && strings.TrimSpace(cand.SourceURL) == "" {
		for i := range sources {
			if delegatedWebSourceIsOfficialRegistry(sources[i]) {
				supports := strings.ToUpper(strings.TrimSpace(sources[i].Supports))
				return supports == "COMPANY_MAILBOX" || supports == "COMPANY_IDENTITY_AND_MAILBOX"
			}
		}
		return false
	}
	if strings.TrimSpace(cand.SourceURL) == "" {
		return false
	}
	want := strings.TrimRight(strings.TrimSpace(cand.SourceURL), "/")
	for i := range sources {
		got := strings.TrimRight(strings.TrimSpace(sources[i].URL), "/")
		if strings.EqualFold(want, got) {
			supports := strings.ToUpper(strings.TrimSpace(sources[i].Supports))
			return supports == "COMPANY_MAILBOX" || supports == "COMPANY_IDENTITY_AND_MAILBOX"
		}
	}
	return false
}

func webIdentityCorroborated(sources []DelegatedWebSource) bool {
	for i := range sources {
		supports := strings.ToUpper(strings.TrimSpace(sources[i].Supports))
		if supports == "COMPANY_IDENTITY" || supports == "COMPANY_IDENTITY_AND_MAILBOX" {
			return true
		}
	}
	return false
}

func validateDelegatedCopy(entry DelegatedFirstTouchEntry, acc *models.OutreachAccount, cand *models.OutreachContactCandidate) []string {
	var out []string
	add := func(code string) { out = appendUnique(out, code) }
	subject, body := strings.TrimSpace(entry.Subject), strings.TrimSpace(entry.BodyText)
	if hashText(subject) != entry.SubjectHash || hashText(body) != entry.BodyHash {
		add("copy_hash_mismatch")
	}
	words := len(strings.Fields(body))
	if subject == "" {
		add("subject_empty")
	}
	if body == "" {
		add("body_empty")
	}
	if len([]rune(subject)) > 100 || words < 45 || words > 150 {
		add("copy_length_invalid")
	}
	if entry.CopyRulesVersion != DelegatedFirstTouchCopyRulesV1 {
		add("copy_rules_version_mismatch")
	}
	low := strings.ToLower(subject + "\n" + body)
	if !strings.Contains(low, "tiago sasaki") || !strings.Contains(low, "confenge") || !strings.Contains(low, "confenge.com.br") {
		add("sender_identity_missing")
	}
	if !delegatedContainsAny(low, "contratos públicos", "contratos publicos", "licitações", "licitacoes", "setor público", "setor publico", "administração pública", "administracao publica") {
		add("routing_purpose_missing")
	}
	if !delegatedContainsAny(low, "contratada", "fornecedora", "executora") ||
		delegatedContainsAny(low, "como contratante", "figura como contratante", "aparece como contratante", "órgão contratante", "orgao contratante") {
		add("target_role_claim_mismatch")
	}
	class := CandidateRouteClass(cand)
	switch class {
	case RouteClassDirectPerson:
		if !delegatedContainsAny(low, "essa frente passa por você", "essa frente passa por voce") ||
			delegatedContainsAny(low, "encaminhar esta mensagem", "indicar a pessoa", "indicar quem") {
			add("route_cta_mismatch")
		}
	case RouteClassRoleOrDepartment:
		if !delegatedContainsAny(low, "essa frente fica com a", "essa frente fica com a sua área", "essa frente fica com a sua area") ||
			!delegatedContainsAny(low, "devo procurar outra", "devo procurar outra área", "devo procurar outra area") {
			add("route_cta_mismatch")
		}
	case RouteClassGenericCompany, RouteClassPublicCompanyFreemail:
		if !delegatedContainsAny(low, "encaminhar esta mensagem") || !delegatedContainsAny(low, "quem cuida dessa frente") {
			add("route_cta_mismatch")
		}
	default:
		add("route_cta_mismatch")
	}
	if !strings.Contains(low, strings.ToLower(delegatedContactExit)) {
		add("contact_exit_missing")
	}
	if delegatedContainsAny(low, "marcar uma reunião", "agendar uma reunião", "agendar uma reuniao", "proposta comercial", "diagnóstico", "diagnostico", "r$", "contrato nº", "processo nº", "pregão nº", "pregao nº") {
		add("copy_exceeds_first_touch_scope")
	}
	if delegatedContainsAny(low,
		"grande volume", "alto volume", "muitos contratos", "diversos contratos", "vários contratos", "varios contratos",
		"dezenas de", "centenas de", "milhares de", "ampla atuação", "ampla atuacao", "líder", "lider",
		"faturamento", "receita", "lucro", "margem", "crédito", "credito", "dívida", "divida",
		"pagamento em atraso", "irregularidade", "ilegalidade", "litígio", "litigio", "condenação", "condenacao", "sanção", "sancao",
		"penalidade", "inadimplência", "inadimplencia", "rescisão", "rescisao") {
		add("unsupported_factual_claim")
	}
	// Numeric amounts, dates, quantities and identifiers require a separate
	// typed projection. Hold them instead of trusting a factual-PASS assertion.
	if delegatedDigitPattern.MatchString(subject + "\n" + body) {
		add("unsupported_specific_fact")
	}
	if delegatedContainsAny(low, "soluções inovadoras", "solucoes inovadoras", "potencializar resultados", "sinergia", "transformar desafios") {
		add("marketing_template_language")
	}
	if delegatedContainsAny(low,
		"idiota", "incompetente", "burro", "preguiçoso", "preguicoso",
		"responda agora", "última chance", "ultima chance", "não perca", "nao perca", "você precisa", "voce precisa") {
		add("offensive_or_manipulative_language")
	}
	if strings.Contains(low, "—") || LooksLikeInternalReasoning(subject+"\n"+body) {
		add("copy_artifact")
	}
	if looksLikeMetadataDump(subject+"\n"+body) || qaKeyValueRe.MatchString(subject+"\n"+body) ||
		qaScoreRe.MatchString(subject+"\n"+body) || qaEnumRe.MatchString(subject+"\n"+body) {
		add("internal_metadata_leak")
	}
	company := strings.ToLower(editorialCompanyName(acc))
	if company != "" && !strings.Contains(low, company) {
		add("company_name_missing")
	}
	personProven := class == RouteClassDirectPerson && composerMaySeePersonName(cand)
	if personProven {
		first := strings.ToLower(titleFirstName(firstName(cand.Name)))
		if first == "" || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(body)), "olá, "+first) {
			add("hallucinated_person")
		}
	} else if looksInventedPersonGreeting(strings.SplitN(body, "\n", 2)[0]) {
		add("hallucinated_person")
	}
	if class != RouteClassDirectPerson && delegatedContainsAny(low, "como responsável", "como responsavel", "sei que você cuida", "sei que voce cuida", "seus contratos") {
		add("generic_recipient_false_role")
	}
	out = append(out, ValidateCopyForRouteClass(class, body, subject, cand)...)
	qa := entry.QA
	if qa.Result != "PASS" || qa.Attempts < 1 || qa.Attempts > 3 || strings.TrimSpace(qa.Reviewer) == "" || !qa.IdentityPassed || !qa.FactualPassed || !qa.CopyPassed || !qa.OperationalPassed {
		add("adversarial_qa_not_passed")
	}
	if qa.Repaired && qa.Attempts < 2 {
		add("repair_loop_receipt_invalid")
	}
	return uniqueStrings(out)
}

func delegatedContainsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func uniqueStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func (s *service) prepareDelegatedTouchpoint(ctx context.Context, orgID uuid.UUID, acc *models.OutreachAccount, cand *models.OutreachContactCandidate, manifest DelegatedFirstTouchManifest, entry DelegatedFirstTouchEntry) (*models.OutreachTouchpoint, *models.OutreachDraft, error) {
	all, err := s.repo.ListTouchpoints(ctx, orgID, acc.ID, "", 100, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("first_touch_lookup_failed")
	}
	var tp *models.OutreachTouchpoint
	var legacy *models.OutreachTouchpoint
	for i := range all {
		if all[i].Ordinal != 1 || all[i].Purpose != models.TouchpointPurposeInitial ||
			all[i].Channel != models.OutreachChannelEmail {
			continue
		}
		if all[i].SourceRunID == "" {
			row := all[i]
			legacy = &row
			continue
		}
		if all[i].SourceRunID != manifest.SourceRunID {
			continue
		}
		if tp != nil {
			return nil, nil, fmt.Errorf("duplicate_first_touch_exists")
		}
		row := all[i]
		tp = &row
	}
	if tp == nil {
		tp = legacy
	}
	if tp != nil {
		switch tp.State {
		case models.TouchpointPlanned, models.TouchpointDue, models.TouchpointDrafted, models.TouchpointNeedsReview:
		default:
			return nil, nil, fmt.Errorf("first_touch_already_authorized_or_terminal")
		}
	} else {
		tp = &models.OutreachTouchpoint{
			ID:             uuid.NewSHA1(uuid.NameSpaceOID, []byte(DelegatedFirstTouchPolicyV1+"\x00touchpoint\x00"+orgID.String()+"\x00"+manifest.SourceRunID+"\x00"+acc.ID.String())),
			OrganizationID: orgID, AccountID: acc.ID, Ordinal: 1, CadenceStep: "INITIAL",
			Channel: models.OutreachChannelEmail, Purpose: models.TouchpointPurposeInitial,
			DueAt: time.Now().UTC(), State: models.TouchpointNeedsReview,
			IdempotencyKey: "delegated-first-touch:" + manifest.SourceRunID + ":" + acc.ID.String(),
		}
	}
	draftID := delegatedFirstTouchDraftID(orgID, manifest.SourceRunID, acc.ID)
	if tp.DraftID != nil {
		draftID = *tp.DraftID
	}
	cid := cand.ID
	tp.ContactCandidateID = &cid
	tp.DraftID = &draftID
	tp.Recipient, tp.Subject, tp.BodyText = strings.ToLower(strings.TrimSpace(entry.Recipient)), strings.TrimSpace(entry.Subject), strings.TrimSpace(entry.BodyText)
	tp.State = models.TouchpointNeedsReview
	if strings.TrimSpace(tp.PolicyVersion) == "" {
		tp.PolicyVersion = models.CadencePolicyVersionV1
	}
	tp.ServiceCode = acc.ServiceCode
	tp.FactUsed = entry.FactUsed
	tp.EvidenceIDs = append([]string{}, entry.EvidenceIDs...)
	tp.GeneratedContextHash = acc.MessageContextHash
	tp.SourceRunID = manifest.SourceRunID
	tp.StopReason = ""
	ClearApproval(tp)
	RecomputeContentHash(tp)
	if tp.GeneratedContextHash == "" {
		return nil, nil, fmt.Errorf("message_context_hash_missing")
	}
	qaJSON, _ := json.Marshal(map[string]any{
		"ok": true, "policy_version": DelegatedFirstTouchPolicyV1,
		"qa": entry.QA, "contractor_role_status": entry.ContractorRoleStatus,
		"reconciliation_status": entry.ReconciliationStatus,
	})
	ok := true
	draft := &models.OutreachDraft{
		ID: draftID, OrganizationID: orgID, AccountID: acc.ID, ContactCandidateID: &cid,
		Channel: models.OutreachChannelEmail, RecipientName: cand.Name, RecipientRole: cand.Role,
		RecipientEmail: tp.Recipient, VerificationStatus: cand.VerificationStatus,
		Subject: tp.Subject, BodyText: tp.BodyText, FollowupsJSON: []byte("[]"),
		ServiceCode: acc.ServiceCode, StrategyCode: "FIRST_TOUCH_ROUTING",
		FactUsed: tp.FactUsed, EvidenceIDs: tp.EvidenceIDs,
		Question: entry.CTA,
		CTA:      entry.CTA,
		Provider: "agent_cli", Model: manifest.AgentID, PromptVersion: PromptVersion,
		Generation: entry.QA.Attempts, ValidationJSON: qaJSON, ValidationOK: &ok,
		RiskClass: "GREEN", RiskFlags: []string{}, RedTeamResult: "PASS",
		RedTeamReasons: entry.QA.ReasonCodes, Status: models.OutreachDraftNeedsReview,
	}
	if err := s.repo.UpsertDraft(ctx, draft); err != nil {
		return nil, nil, fmt.Errorf("delegated_draft_store_failed")
	}
	if tp.ID == uuid.Nil {
		return nil, nil, fmt.Errorf("touchpoint_id_missing")
	}
	persisted, getErr := s.repo.GetTouchpoint(ctx, orgID, tp.ID)
	if getErr == nil && persisted != nil {
		if err := s.repo.UpdateTouchpoint(ctx, tp); err != nil {
			return nil, nil, fmt.Errorf("delegated_touchpoint_store_failed")
		}
	} else if err := s.repo.InsertTouchpoint(ctx, tp); err != nil {
		return nil, nil, fmt.Errorf("delegated_touchpoint_store_failed")
	}
	return tp, draft, nil
}

func delegatedFirstTouchDraftID(orgID uuid.UUID, sourceRunID string, accountID uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(
		DelegatedFirstTouchPolicyV1+"\x00draft\x00"+orgID.String()+"\x00"+sourceRunID+"\x00"+accountID.String(),
	))
}

type delegatedDecisionRow struct {
	State               string
	SubjectHash         string
	BodyHash            string
	EvidenceHash        string
	MaterialBindingHash string
	BlockerCodes        []string
	TouchpointID        *uuid.UUID
	DueAt               *time.Time
}

func (s *service) loadDelegatedDecision(ctx context.Context, orgID uuid.UUID, key string) (*delegatedDecisionRow, error) {
	row := s.delegatedDB.QueryRow(ctx, `
		SELECT state,subject_hash,body_hash,evidence_hash,material_binding_hash,blocker_codes,touchpoint_id,due_at
		FROM confenge_delegated_first_touch_decisions
		WHERE organization_id=$1 AND idempotency_key=$2`, orgID, key)
	var out delegatedDecisionRow
	var blockers []byte
	if err := row.Scan(&out.State, &out.SubjectHash, &out.BodyHash, &out.EvidenceHash, &out.MaterialBindingHash, &blockers, &out.TouchpointID, &out.DueAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	_ = json.Unmarshal(blockers, &out.BlockerCodes)
	return &out, nil
}

func (s *service) reserveDelegatedBatch(ctx context.Context, orgID uuid.UUID, manifest DelegatedFirstTouchManifest, hash string) error {
	authority, err := delegatedManifestAuthority(manifest)
	if err != nil {
		return err
	}
	ct, err := s.delegatedDB.Exec(ctx, `
		INSERT INTO confenge_delegated_first_touch_batches (
			organization_id,batch_id,agent_id,policy_version,policy_authorization_id,
			source_run_id,evidence_version,policy_hash,authority_reference,source_snapshot_hash,
			source_expires_at,source_freshness_hash,target_membership_hash,target_membership_count,
			composer_version,template_version,prompt_version,runtime_release_sha,
			manifest_hash,status,generated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,'RESERVED',$20)
		ON CONFLICT (organization_id,batch_id) DO NOTHING`,
		orgID, manifest.BatchID, manifest.AgentID, manifest.PolicyVersion, manifest.PolicyAuthorizationID,
		manifest.SourceRunID, manifest.EvidenceVersion, manifest.PolicyHash, manifest.AuthorityReference,
		manifest.SourceSnapshotHash, authority.SourceExpiresAt, authority.SourceFreshnessHash,
		authority.TargetMembershipHash, authority.TargetMembershipCount,
		manifest.ComposerVersion, manifest.TemplateVersion, manifest.PromptVersion,
		s.cfg.RepositorySHA, hash, manifest.GeneratedAt.UTC())
	if err != nil {
		return err
	}
	if ct.RowsAffected() > 0 {
		return nil
	}
	var stored, sourceFreshnessHash, targetMembershipHash string
	var sourceExpiresAt *time.Time
	var targetMembershipCount int
	if err := s.delegatedDB.QueryRow(ctx, `
		SELECT manifest_hash,source_expires_at,source_freshness_hash,target_membership_hash,target_membership_count
		FROM confenge_delegated_first_touch_batches
		WHERE organization_id=$1 AND batch_id=$2`, orgID, manifest.BatchID).
		Scan(&stored, &sourceExpiresAt, &sourceFreshnessHash, &targetMembershipHash, &targetMembershipCount); err != nil {
		return err
	}
	if stored != hash {
		return fmt.Errorf("batch_id_reused_with_different_manifest")
	}
	if sourceExpiresAt == nil || !sourceExpiresAt.Equal(authority.SourceExpiresAt.UTC()) ||
		sourceFreshnessHash != authority.SourceFreshnessHash || targetMembershipHash != authority.TargetMembershipHash ||
		targetMembershipCount != authority.TargetMembershipCount {
		return fmt.Errorf("batch_id_reused_with_different_authority")
	}
	return nil
}

func delegatedManifestAuthority(manifest DelegatedFirstTouchManifest) (*models.OutreachFeedSyncState, error) {
	authority := manifest.authority
	if authority == nil || authority.SourceExpiresAt == nil ||
		!validSHA256(authority.SourceFreshnessHash) || !authority.TargetMembershipComplete ||
		!validSHA256(authority.TargetMembershipHash) || authority.TargetMembershipCount < 1 ||
		authority.LastRunID != manifest.SourceRunID || authority.LastSnapshotHash != manifest.SourceSnapshotHash {
		return nil, fmt.Errorf("delegated_authority_binding_unavailable")
	}
	return authority, nil
}

func (s *service) persistDelegatedHold(ctx context.Context, orgID uuid.UUID, manifest DelegatedFirstTouchManifest, entry DelegatedFirstTouchEntry, blockers []string) error {
	return s.persistDelegatedDecision(ctx, orgID, manifest, entry, nil, nil, "HOLD", "HOLD", blockers)
}

func (s *service) persistDelegatedApproval(ctx context.Context, orgID uuid.UUID, manifest DelegatedFirstTouchManifest, entry DelegatedFirstTouchEntry, tp *models.OutreachTouchpoint, draft *models.OutreachDraft) error {
	return s.persistDelegatedDecision(ctx, orgID, manifest, entry, tp, draft, DelegatedFirstTouchApprovalDecision, "APPROVED", nil)
}

func (s *service) persistDelegatedDecision(ctx context.Context, orgID uuid.UUID, manifest DelegatedFirstTouchManifest, entry DelegatedFirstTouchEntry, tp *models.OutreachTouchpoint, draft *models.OutreachDraft, decision, state string, blockers []string) error {
	if len(digits(entry.CNPJ14)) != 14 || (decision != "HOLD" && len(digits(entry.SupplierCNPJ14)) != 14) {
		return fmt.Errorf("cannot persist malformed party identity")
	}
	authority, err := delegatedManifestAuthority(manifest)
	if err != nil {
		return err
	}
	reasons, _ := json.Marshal(entry.QA.ReasonCodes)
	blockerJSON, _ := json.Marshal(blockers)
	contractEvidenceIDs, _ := json.Marshal(entry.ContractEvidenceIDs)
	roleReasons, _ := json.Marshal(entry.ContractRoleReasonCodes)
	web, _ := json.Marshal(entry.WebSources)
	var touchpointID, draftID *uuid.UUID
	contentHash := ""
	if tp != nil {
		id := tp.ID
		touchpointID, contentHash = &id, tp.ContentHash
	}
	if draft != nil {
		id := draft.ID
		draftID = &id
	}
	result, err := s.delegatedDB.Exec(ctx, `
		INSERT INTO confenge_delegated_first_touch_decisions (
			organization_id,batch_id,account_id,contact_candidate_id,touchpoint_id,draft_id,
			policy_authorization_id,policy_version,agent_id,authority,approved_by_type,decision,state,
			cnpj14,cnpj_root,supplier_cnpj14,buyer_cnpj14,contractor_role_status,contract_role_source,contract_evidence_ids,
			reconciliation_status,route_class,evidence_version,evidence_source_run_id,source_snapshot_hash,evidence_hash,evidence_reference,
			source_expires_at,source_freshness_hash,target_membership_hash,target_membership_count,
			evidence_observed_at,web_sources,subject_hash,body_hash,content_hash,recipient,target_party_role,
			supplier_identity_ref,buyer_identity_ref,role_match_method,role_confidence,role_reason_codes,
			policy_hash,authority_reference,composer_version,template_version,prompt_version,runtime_release_sha,material_binding_hash,qa_result,qa_attempts,
			qa_repaired,reason_codes,blocker_codes,correlation_id,idempotency_key)
		VALUES (
			@organization_id,@batch_id,@account_id,@contact_candidate_id,@touchpoint_id,@draft_id,
			@policy_authorization_id,@policy_version,@agent_id,@authority,'delegated_agent',@decision,@state,
			@cnpj14,@cnpj_root,@supplier_cnpj14,@buyer_cnpj14,@contractor_role_status,@contract_role_source,@contract_evidence_ids,
			@reconciliation_status,@route_class,@evidence_version,@evidence_source_run_id,@source_snapshot_hash,@evidence_hash,@evidence_reference,
			@source_expires_at,@source_freshness_hash,@target_membership_hash,@target_membership_count,
			@evidence_observed_at,@web_sources,@subject_hash,@body_hash,@content_hash,@recipient,@target_party_role,
			@supplier_identity_ref,@buyer_identity_ref,@role_match_method,@role_confidence,@role_reason_codes,
			@policy_hash,@authority_reference,@composer_version,@template_version,@prompt_version,@runtime_release_sha,@material_binding_hash,
			@qa_result,@qa_attempts,@qa_repaired,@reason_codes,@blocker_codes,@correlation_id,@idempotency_key)
		ON CONFLICT (organization_id,idempotency_key) DO NOTHING`, pgx.NamedArgs{
		"organization_id": orgID, "batch_id": manifest.BatchID, "account_id": nullUUID(entry.AccountID),
		"contact_candidate_id": nullUUID(entry.ContactCandidateID), "touchpoint_id": touchpointID, "draft_id": draftID,
		"policy_authorization_id": manifest.PolicyAuthorizationID, "policy_version": manifest.PolicyVersion,
		"agent_id": manifest.AgentID, "authority": DelegatedFirstTouchAuthority, "decision": decision, "state": state,
		"cnpj14": digits(entry.CNPJ14), "cnpj_root": digits(entry.CNPJ14)[:8],
		"supplier_cnpj14": digits(entry.SupplierCNPJ14), "buyer_cnpj14": digits(entry.BuyerCNPJ14),
		"contractor_role_status": normalizedRole(entry.ContractorRoleStatus), "contract_role_source": strings.TrimSpace(entry.ContractRoleSource),
		"contract_evidence_ids": contractEvidenceIDs, "reconciliation_status": normalizedReconciliation(entry.ReconciliationStatus),
		"route_class": normalizedRoute(entry.RouteClass), "evidence_version": manifest.EvidenceVersion,
		"evidence_source_run_id": manifest.SourceRunID, "source_snapshot_hash": manifest.SourceSnapshotHash,
		"source_expires_at": authority.SourceExpiresAt, "source_freshness_hash": authority.SourceFreshnessHash,
		"target_membership_hash": authority.TargetMembershipHash, "target_membership_count": authority.TargetMembershipCount,
		"evidence_hash": entry.ContractEvidenceHash, "evidence_reference": entry.ContractEvidenceReference,
		"evidence_observed_at": entry.EvidenceObservedAt.UTC(), "web_sources": web, "subject_hash": entry.SubjectHash,
		"body_hash": entry.BodyHash, "content_hash": contentHash, "recipient": strings.ToLower(strings.TrimSpace(entry.Recipient)),
		"target_party_role": normalizedTargetPartyRole(entry.TargetPartyRole), "supplier_identity_ref": entry.SupplierIdentityRef,
		"buyer_identity_ref": entry.BuyerIdentityRef, "role_match_method": strings.ToUpper(strings.TrimSpace(entry.RoleMatchMethod)),
		"role_confidence": strings.ToUpper(strings.TrimSpace(entry.RoleConfidence)), "role_reason_codes": roleReasons,
		"policy_hash": manifest.PolicyHash, "authority_reference": manifest.AuthorityReference,
		"composer_version": manifest.ComposerVersion, "template_version": manifest.TemplateVersion,
		"prompt_version": manifest.PromptVersion, "runtime_release_sha": s.cfg.RepositorySHA,
		"material_binding_hash": delegatedMaterialBinding(manifest, entry), "qa_result": firstNonEmpty(entry.QA.Result, "NOT_RUN"),
		"qa_attempts": entry.QA.Attempts, "qa_repaired": entry.QA.Repaired, "reason_codes": reasons,
		"blocker_codes": blockerJSON, "correlation_id": entry.CorrelationID, "idempotency_key": entry.IdempotencyKey,
	})
	if err != nil {
		return err
	}
	if result.RowsAffected() > 0 {
		return nil
	}
	// ON CONFLICT is only an idempotent success when the exact material
	// binding and decision target already exist. A reused key must never
	// authorize a second touchpoint or revive a cancelled decision.
	var storedBinding, storedDecision, storedState string
	var storedTouchpointID *uuid.UUID
	if err := s.delegatedDB.QueryRow(ctx, `
		SELECT material_binding_hash,decision,state,touchpoint_id
		FROM confenge_delegated_first_touch_decisions
		WHERE organization_id=$1 AND idempotency_key=$2`, orgID, entry.IdempotencyKey).
		Scan(&storedBinding, &storedDecision, &storedState, &storedTouchpointID); err != nil {
		return err
	}
	if storedBinding != delegatedMaterialBinding(manifest, entry) || storedDecision != decision || storedState != state || !sameOptionalUUID(storedTouchpointID, touchpointID) {
		return fmt.Errorf("idempotency_key_conflict")
	}
	return nil
}

func sameOptionalUUID(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func delegatedMaterialBinding(manifest DelegatedFirstTouchManifest, entry DelegatedFirstTouchEntry) string {
	material := struct {
		PolicyVersion, PolicyHash, AuthorityReference, SourceRunID, SourceSnapshotHash string
		EvidenceVersion, ComposerVersion, TemplateVersion, PromptVersion               string
		SourceFreshnessHash, TargetMembershipHash                                      string
		SourceExpiresAt                                                                *time.Time
		TargetMembershipCount                                                          int
		Entry                                                                          DelegatedFirstTouchEntry
	}{
		manifest.PolicyVersion, manifest.PolicyHash, manifest.AuthorityReference,
		manifest.SourceRunID, manifest.SourceSnapshotHash, manifest.EvidenceVersion,
		manifest.ComposerVersion, manifest.TemplateVersion, manifest.PromptVersion,
		authoritySourceFreshnessHash(manifest.authority), authorityTargetMembershipHash(manifest.authority),
		authoritySourceExpiresAt(manifest.authority), authorityTargetMembershipCount(manifest.authority), entry,
	}
	raw, _ := json.Marshal(material)
	return hashText(string(raw))
}

func authoritySourceFreshnessHash(state *models.OutreachFeedSyncState) string {
	if state == nil {
		return ""
	}
	return state.SourceFreshnessHash
}

func authorityTargetMembershipHash(state *models.OutreachFeedSyncState) string {
	if state == nil {
		return ""
	}
	return state.TargetMembershipHash
}

func authoritySourceExpiresAt(state *models.OutreachFeedSyncState) *time.Time {
	if state == nil {
		return nil
	}
	return state.SourceExpiresAt
}

func authorityTargetMembershipCount(state *models.OutreachFeedSyncState) int {
	if state == nil {
		return 0
	}
	return state.TargetMembershipCount
}

func nullUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func normalizedRole(value string) string {
	switch value {
	case ContractorRoleConfirmed, ContractorRoleConflict:
		return value
	default:
		return ContractorRoleUnknown
	}
}

func normalizedTargetPartyRole(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "SUPPLIER":
		return "SUPPLIER"
	case "BUYER_CONFLICT":
		return "BUYER_CONFLICT"
	default:
		return "UNKNOWN"
	}
}

func normalizedReconciliation(value string) string {
	switch value {
	case ReconciliationCorroborated, ReconciliationWebContact, "CONFLICT":
		return value
	default:
		return "UNKNOWN"
	}
}

func normalizedRoute(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if defaultPilotRouteClasses[value] {
		return value
	}
	return "UNKNOWN"
}

func (s *service) delegatedQueueReadback(ctx context.Context, orgID uuid.UUID, tp *models.OutreachTouchpoint) (*time.Time, string, bool) {
	if tp == nil || tp.DraftID == nil {
		return nil, "", false
	}
	key := dispatch.MessageKeyEmail(*tp.DraftID)
	var touchState, queueState, queueKey string
	var touchDue, queueDue time.Time
	err := s.delegatedDB.QueryRow(ctx, `
		SELECT t.state,t.due_at,q.status,q.due_at,q.message_key
		FROM outreach_touchpoints t
		JOIN confenge_dispatch_queue q ON q.organization_id=t.organization_id AND q.draft_id=t.draft_id
		WHERE t.organization_id=$1 AND t.id=$2 AND q.message_key=$3`, orgID, tp.ID, key).
		Scan(&touchState, &touchDue, &queueState, &queueDue, &queueKey)
	if err != nil || touchState != models.TouchpointQueued || (queueState != "queued" && queueState != "reserved") || queueKey != key || !touchDue.Equal(queueDue) {
		return &queueDue, key, false
	}
	return &queueDue, key, true
}

func (s *service) updateDelegatedScheduling(ctx context.Context, orgID uuid.UUID, key, state, messageKey string, due *time.Time, readback bool, blockers []string) error {
	blockerJSON, _ := json.Marshal(blockers)
	result, err := s.delegatedDB.Exec(ctx, `
		UPDATE confenge_delegated_first_touch_decisions
		SET state=$3,queue_message_key=$4,due_at=$5,
			readback_at=CASE WHEN $6 THEN now() ELSE readback_at END,
			blocker_codes=$7,updated_at=now()
		WHERE organization_id=$1 AND idempotency_key=$2
		  AND state IN ('APPROVED','APPROVED_NOT_SCHEDULED','QUEUED')`, orgID, key, state, messageKey, due, readback, blockerJSON)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("delegated scheduling audit row unavailable or terminal")
	}
	return nil
}

func (s *service) markDelegatedFirstTouchSent(ctx context.Context, orgID, touchpointID uuid.UUID, sentAt time.Time) error {
	if s.delegatedDB == nil || touchpointID == uuid.Nil {
		return nil
	}
	_, err := s.delegatedDB.Exec(ctx, `
		UPDATE confenge_delegated_first_touch_decisions
		SET state='SENT',sent_at=$3,updated_at=now()
		WHERE organization_id=$1 AND touchpoint_id=$2
		  AND decision='DELEGATED_POLICY_APPROVE'
		  AND state IN ('APPROVED','APPROVED_NOT_SCHEDULED','QUEUED','SENT')`, orgID, touchpointID, sentAt.UTC())
	return err
}

func (s *service) finishDelegatedBatch(ctx context.Context, orgID uuid.UUID, report *DelegatedFirstTouchReport) error {
	status := "APPLIED"
	if report.Held > 0 || report.ApprovedNotScheduled > 0 {
		status = "PARTIAL"
	}
	counts, _ := json.Marshal(report)
	_, err := s.delegatedDB.Exec(ctx, `
		UPDATE confenge_delegated_first_touch_batches
		SET status=$3,counts=$4,completed_at=now(),updated_at=now()
		WHERE organization_id=$1 AND batch_id=$2`, orgID, report.BatchID, status, counts)
	return err
}

func (s *service) DelegatedFirstTouchStatus(ctx context.Context, orgID uuid.UUID, batchID string) (*DelegatedFirstTouchStatus, *errx.Error) {
	if s.delegatedDB == nil {
		return nil, errx.New(errx.ServiceUnavailable, "delegated first-touch store is not wired")
	}
	out := &DelegatedFirstTouchStatus{
		SchemaVersion: "warmbly.confenge.first-touch-control.v1", RuntimeReleaseSHA: s.cfg.RepositorySHA,
		BatchID: batchID, PolicyID: DelegatedFirstTouchPolicyV2, PolicyVersion: DelegatedFirstTouchPolicyV2,
		PolicyHash: DelegatedFirstTouchPolicyHashV2, Counts: map[string]int{}, Items: []DelegatedFirstTouchDecisionView{},
	}
	if settings, settingsErr := s.repo.GetOrgSettings(ctx, orgID); settingsErr == nil && settings != nil && settings.CampaignID != nil && s.policyStore != nil {
		if auth, authErr := s.policyStore.GetActiveCampaignPolicy(ctx, orgID, *settings.CampaignID, time.Now().UTC()); authErr == nil && auth != nil {
			manifest := DelegatedFirstTouchManifest{PolicyAuthorizationID: auth.ID}
			out.PolicyActive = len(validateDelegatedPolicy(auth, manifest, time.Now().UTC())) == 0 &&
				len(s.validateDelegatedFounderBinding(orgID, auth)) == 0 && len(s.validateDelegatedTransportAuthority(ctx, orgID, auth)) == 0
		}
	}
	rows, err := s.delegatedDB.Query(ctx, `
		SELECT state,count(*)::int
		FROM confenge_delegated_first_touch_decisions
		WHERE organization_id=$1 AND ($2='' OR batch_id=$2)
		GROUP BY state`, orgID, batchID)
	if err != nil {
		return nil, errx.New(errx.Internal, err.Error())
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return nil, errx.New(errx.Internal, err.Error())
		}
		out.Counts[state] = count
	}
	if err := rows.Err(); err != nil {
		return nil, errx.New(errx.Internal, err.Error())
	}
	if err := s.delegatedDB.QueryRow(ctx, `
		SELECT count(*)::int FROM confenge_delegated_first_touch_decisions d
		JOIN outreach_touchpoints t ON t.organization_id=d.organization_id AND t.id=d.touchpoint_id
		JOIN confenge_dispatch_queue q ON q.organization_id=d.organization_id AND q.draft_id=d.draft_id AND q.message_key=d.queue_message_key
		WHERE d.organization_id=$1 AND ($2='' OR d.batch_id=$2) AND d.state='QUEUED'
		  AND d.readback_at IS NOT NULL AND t.state='QUEUED' AND q.status IN ('queued','reserved') AND q.due_at=d.due_at`, orgID, batchID).Scan(&out.QueuedReadback); err != nil {
		return nil, errx.New(errx.Internal, err.Error())
	}
	if err := s.delegatedDB.QueryRow(ctx, `
		SELECT count(*)::int FROM (
			SELECT account_id FROM confenge_delegated_first_touch_decisions
			WHERE organization_id=$1 AND state IN ('APPROVED','QUEUED','SENT','APPROVED_NOT_SCHEDULED')
			GROUP BY account_id HAVING count(*)>1
		) x`, orgID).Scan(&out.DuplicateLiveAccount); err != nil {
		return nil, errx.New(errx.Internal, err.Error())
	}
	out.Runway = s.delegatedFirstTouchRunwayMetrics(ctx, orgID)
	if err := s.delegatedDB.QueryRow(ctx, `
		SELECT count(*)::int FROM (
			SELECT cnpj_root FROM confenge_delegated_first_touch_decisions
			WHERE organization_id=$1 AND state IN ('APPROVED','QUEUED','SENT','APPROVED_NOT_SCHEDULED')
			GROUP BY cnpj_root HAVING count(*)>1
		) x`, orgID).Scan(&out.DuplicateLiveRoot); err != nil {
		return nil, errx.New(errx.Internal, err.Error())
	}
	if err := s.delegatedDB.QueryRow(ctx, `
		SELECT count(*)::int FROM outreach_touchpoints
		WHERE organization_id=$1 AND approved_by IS NOT NULL
		  AND authorization_mode='HUMAN_TOUCHPOINT_APPROVAL' AND state IN ('APPROVED','QUEUED','SENT')`, orgID).Scan(&out.HumanApproved); err != nil {
		return nil, errx.New(errx.Internal, err.Error())
	}
	if err := s.populateDelegatedFirstTouchControl(ctx, orgID, out); err != nil {
		return nil, errx.New(errx.Internal, err.Error())
	}
	itemRows, itemErr := s.delegatedDB.Query(ctx, `
		SELECT batch_id,account_id,cnpj14,supplier_cnpj14,buyer_cnpj14,recipient,route_class,decision,state,
			evidence_reference,evidence_hash,evidence_source_run_id,source_snapshot_hash,reason_codes,blocker_codes,
			source_expires_at,source_freshness_hash,target_membership_hash,target_membership_count,
			content_hash,runtime_release_sha,due_at,readback_at,decided_at
		FROM confenge_delegated_first_touch_decisions
		WHERE organization_id=$1 AND ($2='' OR batch_id=$2)
		ORDER BY decided_at DESC LIMIT 50`, orgID, batchID)
	if itemErr != nil {
		return nil, errx.New(errx.Internal, itemErr.Error())
	}
	defer itemRows.Close()
	for itemRows.Next() {
		var item DelegatedFirstTouchDecisionView
		var reasons, blockers []byte
		if err := itemRows.Scan(&item.BatchID, &item.AccountID, &item.CNPJ14, &item.SupplierCNPJ14, &item.BuyerCNPJ14,
			&item.Recipient, &item.RouteClass, &item.Decision, &item.State, &item.EvidenceReference, &item.EvidenceHash,
			&item.SourceRunID, &item.SourceSnapshotHash, &reasons, &blockers,
			&item.SourceExpiresAt, &item.SourceFreshnessHash, &item.TargetMembershipHash, &item.TargetMembershipCount, &item.ContentHash,
			&item.RuntimeReleaseSHA, &item.DueAt, &item.ReadbackAt, &item.DecidedAt); err != nil {
			return nil, errx.New(errx.Internal, err.Error())
		}
		if item.Decision == DelegatedFirstTouchApprovalDecision {
			item.ApprovalSource = DelegatedFirstTouchApprovalDecision
		} else {
			item.ApprovalSource = "POLICY_EVALUATION_HOLD"
		}
		_ = json.Unmarshal(reasons, &item.ReasonCodes)
		_ = json.Unmarshal(blockers, &item.BlockerCodes)
		out.Items = append(out.Items, item)
	}
	if err := itemRows.Err(); err != nil {
		return nil, errx.New(errx.Internal, err.Error())
	}
	return out, nil
}

func (s *service) populateDelegatedFirstTouchControl(ctx context.Context, orgID uuid.UUID, out *DelegatedFirstTouchStatus) error {
	if out == nil {
		return fmt.Errorf("delegated first-touch status is required")
	}
	now := s.now()
	control := DelegatedFirstTouchControlReadback{
		SchemaVersion:  "warmbly.confenge.first-touch-control.v1",
		ReadyReservoir: out.Runway.ReadyReservoirCount,
		Queued:         out.Runway.QueuedCount,
		Reserved:       out.Runway.ReservedCount,
		FurthestDueAt:  out.Runway.FurthestDueAt,
		Outcomes:       map[string]int{},
		Transport: DelegatedFirstTouchTransportReadback{
			KillSwitchEngaged: !s.cfg.SendingAllowed(),
		},
	}
	feed, feedErr := s.repo.GetFeedSyncState(ctx, orgID)
	if feedErr != nil {
		return feedErr
	}
	sourceRunID := ""
	if feed != nil {
		sourceRunID = feed.LastRunID
		control.Source = DelegatedFirstTouchSourceReadback{
			RunID: feed.LastRunID, SnapshotHash: feed.LastSnapshotHash,
			FreshnessState: delegatedSourceFreshnessState(feed, now, s.cfg.FeedMaxAge),
			GeneratedAt:    feed.SourceGeneratedAt, ExpiresAt: feed.SourceExpiresAt,
			FreshnessHash:            feed.SourceFreshnessHash,
			TargetMembershipComplete: feed.TargetMembershipComplete,
			TargetMembershipHash:     feed.TargetMembershipHash,
			TargetMembershipCount:    feed.TargetMembershipCount,
			SupplierConfirmedCount:   feed.SupplierConfirmedCount,
		}
	} else {
		control.Source.FreshnessState = "missing"
	}
	if err := s.delegatedDB.QueryRow(ctx, `SELECT
		(SELECT count(*)::int FROM outreach_touchpoints t
		 WHERE t.organization_id=$1 AND t.source_run_id=$2
		   AND t.ordinal=1 AND t.purpose='INITIAL' AND t.channel='EMAIL'),
		(SELECT count(*)::int FROM confenge_delegated_first_touch_decisions d
		 WHERE d.organization_id=$1 AND d.evidence_source_run_id=$2
		   AND d.decision='DELEGATED_POLICY_APPROVE'
		   AND d.state IN ('APPROVED','APPROVED_NOT_SCHEDULED','QUEUED','SENT')),
		(SELECT count(*)::int FROM outreach_touchpoints t
		 WHERE t.organization_id=$1 AND t.source_run_id=$2 AND t.approved_by IS NOT NULL
		   AND t.authorization_mode='HUMAN_TOUCHPOINT_APPROVAL'
		   AND t.state IN ('APPROVED','QUEUED','SENT')),
		(SELECT min(q.due_at) FROM confenge_dispatch_queue q
		 JOIN outreach_touchpoints t ON t.organization_id=q.organization_id AND t.draft_id=q.draft_id
		 WHERE q.organization_id=$1 AND t.source_run_id=$2 AND q.status IN ('queued','reserved'))`,
		orgID, sourceRunID).Scan(&control.Prepared, &control.DelegatedApproved, &control.HumanApproved, &control.NextDueAt); err != nil {
		return err
	}
	if err := s.delegatedDB.QueryRow(ctx, `SELECT
		(SELECT count(*)::int FROM confenge_dispatch_reservations r
		 JOIN outreach_touchpoints t ON t.organization_id=r.organization_id AND t.draft_id=r.draft_id
		 WHERE r.organization_id=$1 AND t.source_run_id=$2 AND r.attempted_at IS NOT NULL),
		(SELECT count(*)::int FROM confenge_dispatch_sends ds
		 JOIN outreach_touchpoints t ON t.organization_id=ds.organization_id AND t.draft_id=ds.draft_id
		 WHERE ds.organization_id=$1 AND t.source_run_id=$2),
		(SELECT count(*)::int FROM outreach_touchpoints t
		 WHERE t.organization_id=$1 AND t.source_run_id=$2
		   AND t.ordinal=1 AND t.purpose='INITIAL' AND t.channel='EMAIL' AND t.state='SENT')`,
		orgID, sourceRunID).Scan(&control.Transport.ProviderAttempts, &control.Transport.ProviderAccepted, &control.Transport.Sent); err != nil {
		return err
	}
	// Separate identifier joins keep large control-center reads indexable.
	rows, err := s.delegatedDB.Query(ctx, `
		WITH matched AS (
			SELECT o.id,o.event_type
			FROM outreach_outcome_outbox o
			JOIN outreach_accounts a
			  ON a.organization_id=o.organization_id AND a.source_run_id=$2
			 AND o.cnpj14<>'' AND a.cnpj14=o.cnpj14
			WHERE o.organization_id=$1
			UNION
			SELECT o.id,o.event_type
			FROM outreach_outcome_outbox o
			JOIN outreach_accounts a
			  ON a.organization_id=o.organization_id AND a.source_run_id=$2
			 AND o.source_lead_id<>'' AND a.source_lead_id=o.source_lead_id
			WHERE o.organization_id=$1
		)
		SELECT event_type,count(*)::int
		FROM matched
		GROUP BY event_type`, orgID, sourceRunID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var eventType string
		var count int
		if err := rows.Scan(&eventType, &count); err != nil {
			rows.Close()
			return err
		}
		control.Outcomes[eventType] = count
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if s.governor != nil {
		capacity, err := s.governor.Status(ctx, &orgID)
		if err != nil {
			control.Blocker = "capacity_read_failed"
		} else {
			control.Capacity = &capacity
			control.Transport.DispatchPaused = capacity.Paused
			control.Transport.PauseReason = capacity.PauseReason
		}
	} else {
		control.Blocker = "dispatch_governor_unavailable"
	}
	// V2 labels the operator blocker; the V1 age bands would report an expiry
	// for a population that is commercially QUALIFIED.
	commercial := FeedCommercialAuthorityState(feed)
	control.Commercial = commercialReadbackV2(feed, commercial)
	switch {
	case !commercial.Present && control.Source.FreshnessState != "fresh":
		control.Blocker = "authoritative_feed_" + control.Source.FreshnessState
	case commercial.Present && commercial.State != CommercialQualified:
		control.Blocker = firstNonEmpty(firstHold(commercial.ReasonCodes), ReasonQualificationMissing)
	case out.DuplicateLiveAccount > 0 || out.DuplicateLiveRoot > 0:
		control.Blocker = "duplicate_live_first_touch"
	case !out.PolicyActive:
		control.Blocker = "delegated_policy_inactive"
	case out.Runway.CapacityBlocked > 0:
		control.Blocker = firstNonEmpty(out.Runway.CapacityBlocker, "runway_capacity_blocked")
	case control.Transport.KillSwitchEngaged || control.Transport.DispatchPaused:
		control.Blocker = "transport_paused_pre_go"
	}
	out.Control = control
	return nil
}

func delegatedSourceFreshnessState(feed *models.OutreachFeedSyncState, now time.Time, maxAge time.Duration) string {
	if feed == nil || feed.LastRunID == "" || feed.LastSnapshotHash == "" || feed.SourceGeneratedAt == nil {
		return "missing"
	}
	if feed.SourceExpiresAt != nil && !now.Before(feed.SourceExpiresAt.UTC()) {
		return "expired"
	}
	if maxAge <= 0 {
		maxAge = 24 * time.Hour
	}
	if feed.SourceGeneratedAt.After(now.Add(5*time.Minute)) || now.Sub(feed.SourceGeneratedAt.UTC()) > maxAge {
		return "stale"
	}
	if validateAuthoritativeFeedState(feed, now, maxAge, true) != nil {
		return "invalid"
	}
	return "fresh"
}

func (s *service) assertDelegatedFirstTouchDecision(ctx context.Context, orgID uuid.UUID, tp *models.OutreachTouchpoint) error {
	if !s.cfg.DelegatedFirstTouchEnabled {
		return fmt.Errorf("delegated first-touch is disabled")
	}
	if s.delegatedDB == nil || tp == nil {
		return fmt.Errorf("delegated first-touch decision store unavailable")
	}
	if s.orgRisk == nil || s.orgRisk.SendingSuspended(ctx, orgID) {
		return fmt.Errorf("organization sending risk blocks delegated first-touch")
	}
	// Source run id, snapshot hash, expiry and freshness hash are read for the
	// audit record only; they are acquisition provenance and gate nothing.
	type binding struct {
		State, PolicyVersion, PolicyHash, AuthorityReference, ContentHash, ActorType, Authority string
		SourceRunID, SourceSnapshotHash, EvidenceVersion, EvidenceHash, EvidenceReference       string
		Recipient, RouteClass                                                                   string
		RoleStatus, TargetPartyRole, SupplierCNPJ14, BuyerCNPJ14                                string
		SupplierIdentityRef, BuyerIdentityRef, RoleMatchMethod, RoleConfidence                  string
		ComposerVersion, TemplateVersion, PromptVersion, RuntimeReleaseSHA                      string
		SourceFreshnessHash, TargetMembershipHash                                               string
		PolicyAuthorizationID, ContactCandidateID                                               uuid.UUID
		EvidenceObservedAt, DecidedAt                                                           time.Time
		SourceExpiresAt                                                                         *time.Time
		TargetMembershipCount                                                                   int
		ContractEvidenceIDs, RoleReasonCodes, WebSources                                        []byte
	}
	var got binding
	err := s.delegatedDB.QueryRow(ctx, `
		SELECT state,policy_version,policy_hash,authority_reference,content_hash,approved_by_type,authority,
			evidence_source_run_id,source_snapshot_hash,evidence_version,evidence_hash,evidence_reference,evidence_observed_at,
			source_expires_at,source_freshness_hash,target_membership_hash,target_membership_count,
			contract_evidence_ids,role_reason_codes,web_sources,recipient,route_class,
			contractor_role_status,target_party_role,supplier_cnpj14,buyer_cnpj14,supplier_identity_ref,
			buyer_identity_ref,role_match_method,role_confidence,composer_version,template_version,prompt_version,
			runtime_release_sha,policy_authorization_id,contact_candidate_id,decided_at
		FROM confenge_delegated_first_touch_decisions
		WHERE organization_id=$1 AND touchpoint_id=$2
		  AND decision='DELEGATED_POLICY_APPROVE'
		  AND state IN ('APPROVED','QUEUED','SENT','APPROVED_NOT_SCHEDULED')`, orgID, tp.ID).
		Scan(&got.State, &got.PolicyVersion, &got.PolicyHash, &got.AuthorityReference, &got.ContentHash, &got.ActorType, &got.Authority,
			&got.SourceRunID, &got.SourceSnapshotHash, &got.EvidenceVersion, &got.EvidenceHash, &got.EvidenceReference,
			&got.EvidenceObservedAt, &got.SourceExpiresAt, &got.SourceFreshnessHash, &got.TargetMembershipHash,
			&got.TargetMembershipCount, &got.ContractEvidenceIDs, &got.RoleReasonCodes, &got.WebSources, &got.Recipient, &got.RouteClass,
			&got.RoleStatus, &got.TargetPartyRole, &got.SupplierCNPJ14, &got.BuyerCNPJ14, &got.SupplierIdentityRef,
			&got.BuyerIdentityRef, &got.RoleMatchMethod, &got.RoleConfidence, &got.ComposerVersion, &got.TemplateVersion,
			&got.PromptVersion, &got.RuntimeReleaseSHA, &got.PolicyAuthorizationID, &got.ContactCandidateID, &got.DecidedAt)
	if err != nil {
		return fmt.Errorf("delegated decision missing")
	}
	fail := func(code string) error {
		_ = s.cancelDelegatedDecision(ctx, orgID, tp.ID, code)
		return fmt.Errorf("delegated decision invalidated: %s", code)
	}
	stateBound := false
	switch tp.State {
	case models.TouchpointApproved:
		stateBound = got.State == "APPROVED" || got.State == "APPROVED_NOT_SCHEDULED"
	case models.TouchpointQueued:
		stateBound = got.State == "QUEUED"
	case models.TouchpointSent:
		stateBound = got.State == "SENT"
	}
	if !stateBound {
		return fail("delegated_decision_queue_state_drift")
	}
	// The runtime release sha is the build that made the decision, not the
	// authority behind it. Comparing it here cancelled every approved touch the
	// moment a new release shipped: the policy version, policy hash, authority
	// reference, actor type and evidence version below are what actually bind
	// this decision, and copy is bound separately by editorial authority.
	if expected, ok := expectedFirstTouchPolicyHash(got.PolicyVersion); !ok || got.PolicyHash != expected ||
		got.AuthorityReference != DelegatedFirstTouchAuthorityRef || got.ActorType != "delegated_agent" || got.Authority != DelegatedFirstTouchAuthority ||
		got.EvidenceVersion != DelegatedFirstTouchEvidenceV1 {
		return fail("policy_or_authority_drift")
	}
	if s.policyStore == nil {
		return fail("delegated_policy_store_unavailable")
	}
	auth, authErr := s.policyStore.GetCampaignPolicyByID(ctx, orgID, got.PolicyAuthorizationID)
	if authErr != nil || auth == nil || !auth.Active(time.Now().UTC()) {
		return fail("delegated_policy_revoked_or_unavailable")
	}
	manifest := DelegatedFirstTouchManifest{PolicyAuthorizationID: auth.ID}
	if len(validateDelegatedPolicy(auth, manifest, time.Now().UTC())) > 0 || len(s.validateDelegatedFounderBinding(orgID, auth)) > 0 {
		return fail("delegated_policy_authority_contract_drift")
	}
	if blockers := s.validateDelegatedTransportAuthority(ctx, orgID, auth); len(blockers) > 0 {
		return fail("mailbox_or_campaign_eligibility_drift:" + strings.Join(blockers, ","))
	}
	if got.ContentHash != tp.ContentHash || !strings.EqualFold(got.Recipient, tp.Recipient) ||
		got.ComposerVersion != ComposerVersion || got.TemplateVersion != DelegatedFirstTouchTemplateV1 || got.PromptVersion != PromptVersion {
		return fail("content_recipient_or_copy_version_drift")
	}
	if tp.CampaignPolicyAuthorizationID == nil || *tp.CampaignPolicyAuthorizationID != got.PolicyAuthorizationID ||
		tp.ContactCandidateID == nil || *tp.ContactCandidateID != got.ContactCandidateID {
		return fail("authorization_or_recipient_binding_drift")
	}
	acc, accErr := s.repo.GetAccount(ctx, orgID, tp.AccountID)
	cand, candErr := s.repo.GetCandidate(ctx, orgID, got.ContactCandidateID)
	if accErr != nil || candErr != nil || acc == nil || cand == nil || cand.AccountID != tp.AccountID {
		return fail("account_or_recipient_missing")
	}
	if err := RequireEmailOutbound(acc, cand); err != nil || !CandidateControlledEligible(cand) ||
		!ControlledRouteAllowed(cand, nil) || !CandidateEnrollable(cand) {
		return fail("compliance_or_recipient_gate_drift")
	}
	if strings.TrimSpace(acc.MessageContextHash) != "" && strings.TrimSpace(tp.GeneratedContextHash) == "" {
		return fail("target_fit_or_message_context_drift")
	}
	if err := AssertMessageContextFresh(acc, tp.GeneratedContextHash); err != nil {
		return fail("target_fit_or_message_context_drift")
	}
	if !strings.EqualFold(cand.Email, got.Recipient) || CandidateRouteClass(cand) != got.RouteClass {
		return fail("recipient_or_route_class_drift")
	}
	if conflict, conflictErr := s.delegatedRecipientSharedAcrossCNPJIdentities(ctx, orgID, cand.Email); conflictErr != nil {
		return fail("recipient_identity_conflict_check_unavailable")
	} else if conflict {
		return fail("recipient_shared_across_cnpj_identities")
	}
	now := time.Now().UTC()
	// Contact freshness was legitimately proven when the decision was minted;
	// the runway ships days later, so re-prove it against that instant plus an
	// absolute ceiling instead of a moving now.
	decidedAt := got.DecidedAt.UTC()
	if decidedAt.IsZero() || decidedAt.After(now.Add(5*time.Minute)) {
		return fail("recipient_evidence_freshness_drift")
	}
	var webSources []DelegatedWebSource
	if json.Unmarshal(got.WebSources, &webSources) != nil || !candidateSourceCorroborated(cand, webSources) {
		return fail("recipient_evidence_association_drift")
	}
	for i := range webSources {
		if !delegatedWebSourceAllowed(webSources[i], decidedAt) || !delegatedWebSourceTransportable(webSources[i], now) {
			return fail("recipient_evidence_freshness_drift")
		}
	}
	if acc.LastImportRunID == nil || cand.LastImportRunID == nil || *acc.LastImportRunID != *cand.LastImportRunID {
		return fail("recipient_import_run_drift")
	}
	// The feed must be readable and structurally whole. Which run last emitted
	// this row is acquisition provenance and never cancels a bound decision.
	feedState, feedErr := s.repo.GetFeedSyncState(ctx, orgID)
	if feedErr != nil || feedState == nil {
		return fail("source_run_or_snapshot_drift")
	}
	if err := validateAuthoritativeFeedStructure(feedState, true); err != nil {
		return fail("authoritative_feed_attestation_invalid")
	}
	// The commercial question at the last gate before SMTP: is this company
	// still a proven public-engineering supplier inside the three-year window?
	if qual := AccountCommercialQualification(acc, now); !qual.AllowsTransport() {
		return fail(firstNonEmpty(firstHold(qual.ReasonCodes), ReasonQualificationMissing))
	}
	// Membership binding only: snapshot expiry and freshness hash move on every
	// refresh and are acquisition provenance recorded on the decision.
	if got.TargetMembershipHash != feedState.TargetMembershipHash ||
		got.TargetMembershipCount != feedState.TargetMembershipCount {
		return fail("source_authority_binding_drift")
	}
	var evidenceIDs, roleReasons []string
	if json.Unmarshal(got.ContractEvidenceIDs, &evidenceIDs) != nil || json.Unmarshal(got.RoleReasonCodes, &roleReasons) != nil {
		return fail("contractor_role_audit_decode_failed")
	}
	if acc.ContractorRoleStatus != ContractorRoleConfirmed || acc.TargetPartyRole != "SUPPLIER" ||
		acc.ContractorRolePolicyVersion != DelegatedFirstTouchEvidenceV1 || acc.ContractorRoleSource != "extra-cli:v_contracts_canonical_v2" ||
		acc.ContractorRoleEvidenceHash != got.EvidenceHash ||
		acc.ContractorRoleEvidenceReference != got.EvidenceReference || acc.SupplierCNPJ14 != got.SupplierCNPJ14 ||
		acc.BuyerCNPJ14 != got.BuyerCNPJ14 || acc.SupplierIdentityRef != got.SupplierIdentityRef ||
		acc.BuyerIdentityRef != got.BuyerIdentityRef || acc.ContractorRoleMatchMethod != got.RoleMatchMethod ||
		acc.ContractorRoleConfidence != got.RoleConfidence || got.RoleStatus != ContractorRoleConfirmed || got.TargetPartyRole != "SUPPLIER" ||
		canonicalStringSet(acc.ContractorRoleEvidenceIDs) != canonicalStringSet(evidenceIDs) ||
		canonicalStringSet(acc.ContractorRoleReasonCodes) != canonicalStringSet(roleReasons) {
		return fail("contractor_role_or_evidence_drift")
	}
	leadCNPJ := digits(acc.CNPJ14)
	if len(leadCNPJ) != 14 || len(got.SupplierCNPJ14) != 14 || len(got.BuyerCNPJ14) != 14 ||
		leadCNPJ != got.SupplierCNPJ14 || leadCNPJ[:8] == got.BuyerCNPJ14[:8] ||
		got.SupplierIdentityRef != "cnpj:"+got.SupplierCNPJ14 || got.BuyerIdentityRef != "cnpj:"+got.BuyerCNPJ14 ||
		got.RoleMatchMethod != "SUPPLIER_EXACT_CNPJ14" || got.RoleConfidence != "HIGH" ||
		!containsStr(roleReasons, "lead_matches_supplier") || !containsStr(roleReasons, "lead_differs_from_buyer") ||
		got.EvidenceReference != "extra-cli:v_contracts_canonical_v2:sha256:"+got.EvidenceHash {
		return fail("contractor_role_semantic_binding_drift")
	}
	// The role observation must match the decision exactly and cannot be
	// future-dated. It deliberately carries NO age ceiling: a proven
	// contractor role does not stop being true because it was observed a
	// while ago. Expiry comes from the three-year qualification window.
	if acc.ContractorRoleObservedAt == nil || !acc.ContractorRoleObservedAt.Equal(got.EvidenceObservedAt.UTC()) ||
		acc.ContractorRoleObservedAt.After(now.Add(5*time.Minute)) {
		return fail("contractor_role_observation_drift")
	}
	evidence, evidenceErr := s.repo.ListEvidence(ctx, orgID, acc.ID)
	if evidenceErr != nil || !delegatedEvidenceRowsCurrent(evidence, tp.EvidenceIDs, acc.LastImportRunID, decidedAt, now) {
		return fail("fact_evidence_row_drift")
	}
	return nil
}

// delegatedRecipientSharedAcrossCNPJIdentities rejects a mailbox that the
// current authoritative feed attributes to more than one legal identity. An
// exact email/domain match is not evidence of a matrix/branch/group relation;
// until the producer publishes such a typed relationship, delegated approval
// must fail closed and leave the ambiguity for human reconciliation.
func (s *service) delegatedRecipientSharedAcrossCNPJIdentities(ctx context.Context, orgID uuid.UUID, recipient string) (bool, error) {
	if s == nil || s.delegatedDB == nil || !validExactEmail(recipient) {
		return false, fmt.Errorf("delegated recipient identity store unavailable")
	}
	var identities int
	err := s.delegatedDB.QueryRow(ctx, `
		SELECT count(DISTINCT a.cnpj14)::int
		FROM outreach_contact_candidates c
		JOIN outreach_accounts a
		  ON a.organization_id=c.organization_id AND a.id=c.account_id
		JOIN outreach_import_runs r
		  ON r.organization_id=c.organization_id AND r.id=c.last_import_run_id
		JOIN outreach_feed_sync_state feed
		  ON feed.organization_id=c.organization_id
		WHERE c.organization_id=$1
		  AND lower(btrim(c.email))=lower(btrim($2))
		  AND a.cnpj14 <> ''
		  AND a.source_run_id=feed.last_run_id
		  AND r.source_run_id=feed.last_run_id AND r.status='completed'
		  AND c.blocked=false AND c.do_not_contact=false AND c.bounced=false
		  AND c.channel_epistemic_class='OBSERVED'
		  AND c.ownership_status='COMPANY_OWNED'
		  AND c.route_freshness='FRESH'
		  AND (c.route_suppression='' OR c.route_suppression='NONE')
		  AND c.discovery_json @> '{"mailbox_company_evidence":"OBSERVED"}'::jsonb`, orgID, strings.TrimSpace(recipient)).Scan(&identities)
	return identities > 1, err
}

func (s *service) cancelDelegatedDecision(ctx context.Context, orgID, touchpointID uuid.UUID, reason string) error {
	tx, err := s.delegatedDB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `
		UPDATE confenge_dispatch_queue q
		SET status='cancelled',cancel_reason=$3,last_error=$3,updated_at=now()
		FROM outreach_touchpoints t
		WHERE t.organization_id=$1 AND t.id=$2 AND t.draft_id=q.draft_id
		  AND q.organization_id=t.organization_id AND q.status IN ('queued','reserved')`, orgID, touchpointID, reason); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE outreach_drafts d SET status='NEEDS_REVIEW',approved_by=NULL,approved_at=NULL,updated_at=now()
		FROM outreach_touchpoints t
		WHERE t.organization_id=$1 AND t.id=$2 AND t.draft_id=d.id AND d.organization_id=t.organization_id`, orgID, touchpointID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE outreach_touchpoints SET state='NEEDS_REVIEW',approved_content_hash='',approved_by=NULL,approved_at=NULL,
			authorization_mode='',campaign_policy_authorization_id=NULL,authorization_policy_hash='',authorization_at=NULL,
			queued_at=NULL,stop_reason=$3,updated_at=now()
		WHERE organization_id=$1 AND id=$2`, orgID, touchpointID, reason); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE confenge_delegated_first_touch_decisions
		SET state='CANCELLED',blocker_codes=blocker_codes || jsonb_build_array($3::text),updated_at=now()
		WHERE organization_id=$1 AND touchpoint_id=$2 AND state IN ('APPROVED','QUEUED','APPROVED_NOT_SCHEDULED')`, orgID, touchpointID, reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
