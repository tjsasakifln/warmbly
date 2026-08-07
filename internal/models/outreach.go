package models

import (
	"time"

	"github.com/google/uuid"
)

// Outreach staging models: intelligence-plane feed import into a multi-tenant
// company staging queue. Companies may lack email; they are not forced into
// contacts until a candidate is promoted.

// Schema versions for the confenge/extra-cli integration contracts.
const (
	OutreachSchemaV1        = "confenge.outreach.v1"
	OutreachOutcomeSchemaV1 = "confenge.outcome.v1"
)

// Contact verification statuses allowed on outreach contact candidates.
const (
	OutreachVerifyOfficialSource       = "OFFICIAL_SOURCE"
	OutreachVerifyPublicDocumentRecent = "PUBLIC_DOCUMENT_RECENT"
	OutreachVerifyMultipleSources      = "MULTIPLE_PUBLIC_SOURCES"
	OutreachVerifyInstitutionalGeneric = "INSTITUTIONAL_GENERIC"
	OutreachVerifyPublicPossiblyStale  = "PUBLIC_POSSIBLY_STALE"
	OutreachVerifyCandidateUnverified  = "CANDIDATE_UNVERIFIED"
	OutreachVerifyNotFound             = "NOT_FOUND"
	OutreachVerifyInvalid              = "INVALID"
	OutreachVerifyBounced              = "BOUNCED"
	OutreachVerifyDoNotContact         = "DO_NOT_CONTACT"
)

// Epistemic classifications for evidence.
const (
	OutreachEpistemicConfirmedFact          = "CONFIRMED_FACT"
	OutreachEpistemicStrongInference        = "STRONG_INFERENCE"
	OutreachEpistemicWeakInference          = "WEAK_INFERENCE"
	OutreachEpistemicCommercialHypothesis   = "COMMERCIAL_HYPOTHESIS"
	OutreachEpistemicNotFound               = "NOT_FOUND"
	OutreachEpistemicRequiresCompanyConfirm = "REQUIRES_COMPANY_CONFIRMATION"
	OutreachEpistemicContradictoryEvidence  = "CONTRADICTORY_EVIDENCE"
)

// Queue states for outreach accounts (dashboard pipeline).
const (
	OutreachQueueNeedsContact    = "NEEDS_CONTACT"
	OutreachQueueReadyToGenerate = "READY_TO_GENERATE"
	OutreachQueueNeedsReview     = "NEEDS_REVIEW"
	OutreachQueueApproved        = "APPROVED"
	OutreachQueueEnrolled        = "ENROLLED"
	OutreachQueueSent            = "SENT"
	OutreachQueueReplied         = "REPLIED"
	OutreachQueueMeeting         = "MEETING"
	OutreachQueueProposal        = "PROPOSAL"
	OutreachQueueWon             = "WON"
	OutreachQueueLost            = "LOST"
	OutreachQueueBlocked         = "BLOCKED"
	OutreachQueueBounced         = "BOUNCED"
	OutreachQueueDoNotContact    = "DO_NOT_CONTACT"
	OutreachQueueSkipped         = "SKIPPED"
)

// Import run statuses.
const (
	OutreachImportPending   = "pending"
	OutreachImportRunning   = "running"
	OutreachImportCompleted = "completed"
	OutreachImportFailed    = "failed"
	OutreachImportPartial   = "partial"
)

// Verification statuses that must never be enrolled into a campaign.
var OutreachUnenrollableVerification = map[string]bool{
	OutreachVerifyCandidateUnverified: true,
	OutreachVerifyNotFound:            true,
	OutreachVerifyInvalid:             true,
	OutreachVerifyBounced:             true,
	OutreachVerifyDoNotContact:        true,
}

// OutreachImportCounts is the dry-run / apply summary for one import run.
type OutreachImportCounts struct {
	Creates           int `json:"creates"`
	Updates           int `json:"updates"`
	Unchanged         int `json:"unchanged"`
	MissingContact    int `json:"missing_contact"`
	Invalid           int `json:"invalid"`
	Blocked           int `json:"blocked"`
	Conflicts         int `json:"conflicts"`
	EvidenceAdded     int `json:"evidence_added"`
	ContactsPromoted  int `json:"contacts_promoted"`
	Warnings          int `json:"warnings"`
	LeadsProcessed    int `json:"leads_processed"`
	LeadsSkippedError int `json:"leads_skipped_error"`
}

// OutreachImportError is a per-lead error recorded without aborting the run.
type OutreachImportError struct {
	SourceLeadID string `json:"source_lead_id,omitempty"`
	CNPJ14       string `json:"cnpj14,omitempty"`
	Message      string `json:"message"`
}

// OutreachImportRun is one feed import attempt (dry-run or apply).
type OutreachImportRun struct {
	ID              uuid.UUID             `json:"id"`
	OrganizationID  uuid.UUID             `json:"organization_id"`
	SourceSystem    string                `json:"source_system"`
	SourceRunID     string                `json:"source_run_id"`
	SchemaVersion   string                `json:"schema_version"`
	SnapshotHash    string                `json:"snapshot_hash"`
	RepoSHA         string                `json:"repo_sha"`
	PayloadHash     string                `json:"payload_hash"`
	ProfileID       string                `json:"profile_id"`
	ProfileVersion  string                `json:"profile_version"`
	Status          string                `json:"status"`
	DryRun          bool                  `json:"dry_run"`
	StartedAt       time.Time             `json:"started_at"`
	FinishedAt      *time.Time            `json:"finished_at,omitempty"`
	CursorIn        string                `json:"cursor_in,omitempty"`
	CursorOut       string                `json:"cursor_out,omitempty"`
	Counts          OutreachImportCounts  `json:"counts"`
	Errors          []OutreachImportError `json:"errors,omitempty"`
	Warnings        []string              `json:"warnings,omitempty"`
	CreatedByUserID *uuid.UUID            `json:"created_by_user_id,omitempty"`
	IdempotencyKey  string                `json:"idempotency_key,omitempty"`
	SourceURI       string                `json:"source_uri,omitempty"`
	CreatedAt       time.Time             `json:"created_at"`
	UpdatedAt       time.Time             `json:"updated_at"`
}

// OutreachAccount is a staged company from the intelligence feed.
type OutreachAccount struct {
	ID                 uuid.UUID  `json:"id"`
	OrganizationID     uuid.UUID  `json:"organization_id"`
	SourceLeadID       string     `json:"source_lead_id"`
	CNPJ14             string     `json:"cnpj14"`
	CNPJRoot           string     `json:"cnpj_root"`
	RazaoSocial        string     `json:"razao_social"`
	NomeFantasia       string     `json:"nome_fantasia"`
	Municipio          string     `json:"municipio"`
	UF                 string     `json:"uf"`
	Website            string     `json:"website"`
	PriorityRank       int        `json:"priority_rank"`
	PriorityScore      float64    `json:"priority_score"`
	PriorityTier       string     `json:"priority_tier"`
	PriorityConfidence string     `json:"priority_confidence"`
	MomentCode         string     `json:"moment_code"`
	MomentSummary      string     `json:"moment_summary"`
	MomentObservedAt   *time.Time `json:"moment_observed_at,omitempty"`
	MomentConfidence   string     `json:"moment_confidence"`
	MomentEvidenceIDs  []string   `json:"moment_evidence_ids,omitempty"`
	ServiceCode        string     `json:"service_code"`
	ServiceName        string     `json:"service_name"`
	EntryOffer         string     `json:"entry_offer"`
	OfferRationale     string     `json:"offer_rationale"`
	FactToMention      string     `json:"fact_to_mention"`
	QuestionToAsk      string     `json:"question_to_ask"`
	CTA                string     `json:"cta"`
	ClaimsToAvoid      []string   `json:"claims_to_avoid,omitempty"`
	CommercialState    string     `json:"commercial_state"`
	QueueState         string     `json:"queue_state"`
	HumanOverride      bool       `json:"human_override"`
	Blocked            bool       `json:"blocked"`
	BlockReason        string     `json:"block_reason,omitempty"`
	DoNotContact       bool       `json:"do_not_contact"`
	SourceSystem       string     `json:"source_system"`
	SourceRunID        string     `json:"source_run_id"`
	LastImportRunID    *uuid.UUID `json:"last_import_run_id,omitempty"`
	LastPayloadHash    string     `json:"last_payload_hash,omitempty"`
	ContractsJSON      []byte     `json:"contracts,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`

	// Joined / computed (not always filled).
	Contacts  []OutreachContactCandidate `json:"contacts,omitempty"`
	Evidence  []OutreachEvidence         `json:"evidence,omitempty"`
	ContactN  int                        `json:"contact_count,omitempty"`
	EvidenceN int                        `json:"evidence_count,omitempty"`
}

// OutreachContactCandidate is a possible recipient before promotion to contacts.
type OutreachContactCandidate struct {
	ID              uuid.UUID `json:"id"`
	OrganizationID  uuid.UUID `json:"organization_id"`
	AccountID       uuid.UUID `json:"account_id"`
	SourceContactID string    `json:"source_contact_id"`
	Name            string    `json:"name"`
	Role            string    `json:"role"`
	Email           string    `json:"email"`
	Phone           string    `json:"phone"`
	// Structured phone + WhatsApp consent (additive; public phone is not opt-in).
	PhoneE164                   string     `json:"phone_e164,omitempty"`
	PhoneSource                 string     `json:"phone_source,omitempty"`
	PhoneSourceURL              string     `json:"phone_source_url,omitempty"`
	WhatsAppConsentStatus       string     `json:"whatsapp_consent_status,omitempty"`
	WhatsAppConsentSource       string     `json:"whatsapp_consent_source,omitempty"`
	WhatsAppConsentAt           *time.Time `json:"whatsapp_consent_at,omitempty"`
	WhatsAppConsentProvenanceOK bool       `json:"whatsapp_consent_provenance_ok,omitempty"`
	LinkedInURL                 string     `json:"linkedin_url,omitempty"`
	SourceURL                   string     `json:"source_url,omitempty"`
	SourceDocument              string     `json:"source_document,omitempty"`
	SourceDate                  *time.Time `json:"source_date,omitempty"`
	VerificationStatus          string     `json:"verification_status"`
	Confidence                  string     `json:"confidence"`
	Recommended                 bool       `json:"recommended"`
	WarmblyContactID            *uuid.UUID `json:"warmbly_contact_id,omitempty"`
	PromotedAt                  *time.Time `json:"promoted_at,omitempty"`
	Blocked                     bool       `json:"blocked"`
	BlockReason                 string     `json:"block_reason,omitempty"`
	DoNotContact                bool       `json:"do_not_contact"`
	Bounced                     bool       `json:"bounced"`
	LastImportRunID             *uuid.UUID `json:"last_import_run_id,omitempty"`
	CreatedAt                   time.Time  `json:"created_at"`
	UpdatedAt                   time.Time  `json:"updated_at"`
}

// CanEnroll reports whether this candidate may be put into a campaign.
func (c *OutreachContactCandidate) CanEnroll() bool {
	if c == nil {
		return false
	}
	if c.Blocked || c.DoNotContact || c.Bounced {
		return false
	}
	if c.Email == "" {
		return false
	}
	if OutreachUnenrollableVerification[c.VerificationStatus] {
		return false
	}
	return true
}

// OutreachEvidence is one sanitized evidence row for an account.
type OutreachEvidence struct {
	ID               uuid.UUID  `json:"id"`
	OrganizationID   uuid.UUID  `json:"organization_id"`
	AccountID        uuid.UUID  `json:"account_id"`
	SourceEvidenceID string     `json:"source_evidence_id"`
	EvidenceType     string     `json:"evidence_type"`
	Title            string     `json:"title"`
	URL              string     `json:"url"`
	Document         string     `json:"document"`
	EvidenceDate     *time.Time `json:"evidence_date,omitempty"`
	Location         string     `json:"location"`
	Excerpt          string     `json:"excerpt"`
	Synthesis        string     `json:"synthesis"`
	EpistemicClass   string     `json:"epistemic_class"`
	Reliability      string     `json:"reliability"`
	ConsultedAt      *time.Time `json:"consulted_at,omitempty"`
	LastImportRunID  *uuid.UUID `json:"last_import_run_id,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// OutreachQueueSummary is the dashboard overview counters.
type OutreachQueueSummary struct {
	NeedsContact    int `json:"needs_contact"`
	ReadyToGenerate int `json:"ready_to_generate"`
	NeedsReview     int `json:"needs_review"`
	Approved        int `json:"approved"`
	Enrolled        int `json:"enrolled"`
	Sent            int `json:"sent"`
	Replied         int `json:"replied"`
	Meeting         int `json:"meeting"`
	Proposal        int `json:"proposal"`
	Won             int `json:"won"`
	Blocked         int `json:"blocked"`
	Bounced         int `json:"bounced"`
	DoNotContact    int `json:"do_not_contact"`
	Total           int `json:"total"`
}

// OutreachAccountListResult is a cursor-paginated account list.
type OutreachAccountListResult struct {
	Data       []OutreachAccount `json:"data"`
	Pagination Pagination        `json:"pagination"`
}

// OutreachImportRunListResult lists recent import runs.
type OutreachImportRunListResult struct {
	Data       []OutreachImportRun `json:"data"`
	Pagination Pagination          `json:"pagination"`
}

// Draft statuses.
const (
	OutreachDraftNotGenerated = "NOT_GENERATED"
	OutreachDraftGenerating   = "GENERATING"
	OutreachDraftNeedsReview  = "NEEDS_REVIEW"
	OutreachDraftApproved     = "APPROVED"
	OutreachDraftRejected     = "REJECTED"
	OutreachDraftSkipped      = "SKIPPED"
	OutreachDraftBlocked      = "BLOCKED"
	OutreachDraftEnrolled     = "ENROLLED"
	OutreachDraftSent         = "SENT"
	OutreachDraftReplied      = "REPLIED"
)

// Outreach draft channels.
const (
	OutreachChannelEmail    = "EMAIL"
	OutreachChannelWhatsApp = "WHATSAPP"
)

// OutreachDraft is one generated/reviewed message for a staged account.
type OutreachDraft struct {
	ID                 uuid.UUID  `json:"id"`
	OrganizationID     uuid.UUID  `json:"organization_id"`
	AccountID          uuid.UUID  `json:"account_id"`
	ContactCandidateID *uuid.UUID `json:"contact_candidate_id,omitempty"`

	// Channel is EMAIL (default) or WHATSAPP. Threads stay separate per channel.
	Channel            string `json:"channel"`
	RecipientName      string `json:"recipient_name"`
	RecipientRole      string `json:"recipient_role"`
	RecipientEmail     string `json:"recipient_email"`
	RecipientPhoneE164 string `json:"recipient_phone_e164,omitempty"`
	VerificationStatus string `json:"verification_status"`

	Subject       string `json:"subject"`
	BodyText      string `json:"body_text"`
	BodyHTML      string `json:"body_html"`
	FollowupsJSON []byte `json:"followups,omitempty"`

	ServiceCode  string   `json:"service_code"`
	StrategyCode string   `json:"strategy_code"`
	FactUsed     string   `json:"fact_used"`
	EvidenceIDs  []string `json:"evidence_ids,omitempty"`
	Question     string   `json:"question"`
	CTA          string   `json:"cta"`

	Provider      string `json:"provider"`
	Model         string `json:"model"`
	PromptVersion string `json:"prompt_version"`
	Generation    int    `json:"generation"`

	ValidationJSON []byte   `json:"validation,omitempty"`
	ValidationOK   *bool    `json:"validation_ok,omitempty"`
	RiskClass      string   `json:"risk_class"`
	RiskFlags      []string `json:"risk_flags,omitempty"`
	RedTeamResult  string   `json:"red_team_result,omitempty"`
	RedTeamReasons []string `json:"red_team_reasons,omitempty"`

	Status        string     `json:"status"`
	HumanEdited   bool       `json:"human_edited"`
	ApprovedBy    *uuid.UUID `json:"approved_by,omitempty"`
	ApprovedAt    *time.Time `json:"approved_at,omitempty"`
	ReviewSeconds int        `json:"review_seconds"`

	CampaignID          *uuid.UUID `json:"campaign_id,omitempty"`
	EnrollmentContactID *uuid.UUID `json:"enrollment_contact_id,omitempty"`
	EnrolledAt          *time.Time `json:"enrolled_at,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Joined for review UI
	Account *OutreachAccount `json:"account,omitempty"`
}

// OutreachOrgSettings stores confenge campaign bootstrap pointer.
type OutreachOrgSettings struct {
	OrganizationID uuid.UUID  `json:"organization_id"`
	CampaignID     *uuid.UUID `json:"campaign_id,omitempty"`
	CampaignName   string     `json:"campaign_name"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// OutreachOutcome is one outbox row for confenge.outcome.v1 delivery.
type OutreachOutcome struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID uuid.UUID  `json:"organization_id"`
	EventID        uuid.UUID  `json:"event_id"`
	IdempotencyKey string     `json:"idempotency_key"`
	SourceLeadID   string     `json:"source_lead_id"`
	CNPJ14         string     `json:"cnpj14"`
	ContactEmail   string     `json:"contact_email"`
	EventType      string     `json:"event_type"`
	Payload        []byte     `json:"payload,omitempty"`
	OccurredAt     time.Time  `json:"occurred_at"`
	Attempts       int        `json:"attempts"`
	NextAttemptAt  time.Time  `json:"next_attempt_at"`
	DeliveredAt    *time.Time `json:"delivered_at,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
	DeadLetter     bool       `json:"dead_letter"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}
