// Package intel is CONFENGE commercial intelligence: join, exception queue,
// executive rollup, and LEARNING CANDIDATE records. It is not a CRM and
// not a second ledger. extra-cli stays facts; web-cfg stays attribution;
// Warmbly stays human action and outcomes.
package intel

import (
	"strings"
	"time"
)

const (
	SchemaV1 = "confenge.commercial_intel.v1"

	LabelSynthetic = "SYNTHETIC"
	LabelReal      = "REAL"

	Unknown = "UNKNOWN"

	FamilyInbound   = "inbound"
	FamilyOutbound  = "outbound"
	FamilyPartner   = "partner"
	FamilyExpansion = "expansion"

	ExceptionOrphan             = "orphan"
	ExceptionDuplicate          = "duplicate"
	ExceptionConflictingAccount = "conflicting_account"
	ExceptionMissingVersion     = "missing_version"
	ExceptionStaleAttribution   = "stale_attribution"
	ExceptionOutOfOrder         = "out_of_order"
	ExceptionUnconfirmedWon     = "unconfirmed_won"
	ExceptionUnconfirmedLost    = "unconfirmed_lost"
	ExceptionUnavailable        = "ledger_unavailable"

	OutcomeWon                   = "WON"
	OutcomeLost                  = "LOST"
	OutcomeUnknown               = "UNKNOWN"
	OutcomeMeeting               = "MEETING"
	OutcomeProposal              = "PROPOSAL"
	OutcomeQualifiedConversation = "QUALIFIED_CONVERSATION"
	OutcomeContacted             = "CONTACTED"
	OutcomeReplied               = "REPLIED"
	OutcomeClient                = "CLIENT"
	OutcomeNoResponse            = "NO_RESPONSE"
	OutcomeDoNotContact          = "DO_NOT_CONTACT"

	LearningKind = "LEARNING_CANDIDATE"

	TargetDemand           = "demand"
	TargetAsset            = "asset"
	TargetOffer            = "offer"
	TargetContent          = "content"
	TargetDistribution     = "distribution"
	LearningFromCorrection = "correction"
	LearningFromOutcome    = "outcome"
	LearningPending        = "PENDING"

	LearningRepeat   = "REPEAT"
	LearningChange   = "CHANGE"
	LearningStop     = "STOP"
	LearningNeedMore = "NEED_MORE_DATA"
	LearningUnknown  = "NEED_MORE_DATA"

	AssociationObserved = "observed_association"

	EventSchemaV1 = "confenge.commercial_event.v1"

	EventLeadReceived              = "lead_received"
	EventLeadValidated             = "lead_validated"
	EventLeadRejected              = "lead_rejected"
	EventHandoffAccepted           = "handoff_accepted"
	EventHandoffException          = "handoff_exception"
	EventActionApproved            = "action_approved"
	EventActionExecuted            = "action_executed"
	EventReply                     = "reply"
	EventOutcomeObserved           = "outcome_observed"
	EventMeeting                   = "meeting"
	EventProposal                  = "proposal"
	EventPipelineCreated           = "pipeline_created"
	EventPipelineUpdated           = "pipeline_updated"
	EventWon                       = "won"
	EventLost                      = "lost"
	EventUnknownState              = "unknown"
	EventRevenueEvidenced          = "revenue_evidenced"
	EventLearningCandidate         = "learning_candidate"
	EventXRayCompleted             = "xray_completed"
	EventPageView                  = "page_view"
	EventCitation                  = "citation"
	EventCorrection                = "correction"
	EventOperatorAlertCreated      = "operator_alert_created"
	EventOperatorAlertEmitted      = "operator_alert_emitted"
	EventOperatorAlertFailed       = "operator_alert_failed"
	EventOperatorAlertAcknowledged = "operator_alert_acknowledged"
	EventEmailAttempted            = "email_attempted"
	EventProviderAccepted          = "provider_accepted"
	EventDelivered                 = "delivered"
	EventHardBounce                = "hard_bounce"
	EventSoftBounce                = "soft_bounce"
	EventOptOut                    = "opt_out"
	EventSpamComplaint             = "spam_complaint"
	EventNoReply                   = "no_reply"
	EventFirstHumanActionRecorded  = "first_human_action_recorded"
	EventInboundResolvedNoAction   = "inbound_resolved_no_action"

	AssetFamilyMarketAnswer     = "market_answer"
	AssetFamilyContractAnalysis = "contract_analysis"
	AssetFamilyB2GXRay          = "b2g_xray"

	LaneInbound          = "inbound"
	LaneOutbound         = "outbound"
	LanePartner          = "partner"
	LaneExpansion        = "customer_expansion"
	LaneCustomerProof    = "customer_proof"
	LaneMarketAnswer     = "market_answer_assisted"
	LaneContractAnalysis = "contract_analysis_assisted"
	LaneB2GXRay          = "b2g_xray_assisted"

	ExceptionImpossibleTransition = "impossible_transition"
	ExceptionNegativeLatency      = "negative_latency"
	ExceptionOverlappingLatency   = "overlapping_latency"
	ExceptionOutboundAsInbound    = "outbound_as_inbound"
	ExceptionInvalidAssetFamily   = "invalid_asset_family"
	ExceptionMissingAttribution   = "missing_attribution"
	ExceptionAlertStoreFailed     = "alert_store_failed"

	ExceptionLeadWithoutAssetID      = "lead_without_asset_id"
	ExceptionUnknownAssetVersion     = "unknown_asset_version"
	ExceptionContradictorySource     = "contradictory_source"
	ExceptionSyntheticTreatedAsReal  = "synthetic_treated_as_real"
	ExceptionMissingConsent          = "missing_consent"
	ExceptionPipelineWithoutEvidence = "pipeline_without_evidence"
	ExceptionRevenueWithoutFinancial = "revenue_without_financial_event"
	ExceptionGSCQueryOnLead          = "gsc_query_on_lead"
	ExceptionQueryHashOnLead         = "query_hash_on_lead"

	SourceOrganicSearch = "organic_search"
	SourceDirect        = "direct"
	SourceReferral      = "referral"
	SourceAIReferral    = "ai_referral"
	SourcePartner       = "partner"
	SourceOutbound      = "outbound"

	RecordKindReal      = "real"
	RecordKindSynthetic = "synthetic"

	OrganicAttributionV1      = "confenge.organic_attribution.v1"
	OrganicScoreboardSchemaV1 = "confenge.organic_learning_scoreboard.v1"
	OrganicFeedbackSchemaV1   = "confenge.organic_editorial_feedback.v1"
	OrganicDiscoveryContract  = "confenge.search_observation.v1"

	EventSearchObservation = "search_observation"

	LayerEligible          = "ELIGIBLE"
	LayerAppeared          = "APPEARED"
	LayerClicked           = "CLICKED"
	LayerEngaged           = "ENGAGED"
	LayerLeadValid         = "LEAD_VALID"
	LayerQualifiedLead     = "QUALIFIED_LEAD"
	LayerAcknowledged      = "ACKNOWLEDGED"
	LayerConversation      = "CONVERSATION"
	LayerMeeting           = "MEETING"
	LayerProposal          = "PROPOSAL"
	LayerQualifiedPipeline = "QUALIFIED_PIPELINE"
	LayerWonLostUnknown    = "WON_LOST_UNKNOWN"
	LayerRevenue           = "REVENUE"

	Window7dComplete  = "7d_complete"
	Window28dComplete = "28d_complete"
	Window90d         = "90d"
	WindowOpen        = "open_censored"

	AttributionDirect   = "direct"
	AttributionAssisted = "assisted"

	BaselineSynthetic = "BASELINE_SYNTHETIC"
	BaselineObserved  = "BASELINE_OBSERVED"
	BaselineNone      = "insufficient_data"

	RecommendReady  = "READY_FOR_REAL_EVENTS"
	RecommendAdjust = "ADJUST"
	RecommendNoGo   = "NO_GO"

	RecommendNeedsWebCfg = "NEEDS_WEB_CFG_EVENT"
	RecommendNeedsReal   = "NEEDS_REAL_EVENT"
	RecommendReadyInteg  = "READY_FOR_INTEGRATION"

	ReportSchemaV1 = "confenge.inbound_learning_report.v1"
)

// JoinKeys is the typed, idempotent join contract. Metric keys use these
// IDs only. Email, phone, name, and company never enter a metric key.
type JoinKeys struct {
	OrganizationID string `json:"organization_id,omitempty"`

	// web-cfg attribution authority.
	Source        string `json:"source,omitempty"`
	Query         string `json:"query,omitempty"`
	AssetID       string `json:"asset_id,omitempty"`
	CTAID         string `json:"cta_id,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
	// Commercial events prefer an explicit correlation over transport IDs.
	PreferCorrelation bool `json:"-"`

	// Inbound durable receipt identity.
	LeadID    string `json:"lead_id,omitempty"`
	ReceiptID string `json:"receipt_id,omitempty"`

	// extra-cli fact / identity / version authority.
	AccountID               string   `json:"account_id,omitempty"`
	OpportunityID           string   `json:"opportunity_id,omitempty"`
	ProposalID              string   `json:"proposal_id,omitempty"`
	ChargeID                string   `json:"charge_id,omitempty"`
	PaymentID               string   `json:"payment_id,omitempty"`
	SourceLeadID            string   `json:"source_lead_id,omitempty"`
	PersonID                string   `json:"person_id,omitempty"`
	EventIDs                []string `json:"event_ids,omitempty"`
	TargetFitVersion        string   `json:"target_fit_version,omitempty"`
	ActivationPolicyVersion string   `json:"activation_policy_version,omitempty"`
	TargetFitWatermark      string   `json:"target_fit_watermark,omitempty"`
	TargetFitFresh          bool     `json:"target_fit_fresh,omitempty"`

	// Warmbly action / outcome / outbox authority.
	ActionID       string `json:"action_id,omitempty"`
	OutcomeID      string `json:"outcome_id,omitempty"`
	OutboxEventID  string `json:"outbox_event_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`

	RouteFamily string `json:"route_family,omitempty"`
	Trigger     string `json:"trigger,omitempty"`
	OfferID     string `json:"offer_id,omitempty"`
	Route       string `json:"route,omitempty"`

	// Envelope / asset attribution. IDs and pointers only.
	EventID           string `json:"event_id,omitempty"`
	AssetFamily       string `json:"asset_family,omitempty"`
	MarketAnswerID    string `json:"market_answer_id,omitempty"`
	AnalysisID        string `json:"analysis_id,omitempty"`
	Referrer          string `json:"referrer,omitempty"`
	IntentClass       string `json:"intent_class,omitempty"`
	CitationRoute     string `json:"citation_route,omitempty"`
	DistributionRoute string `json:"distribution_route,omitempty"`
	ActorRef          string `json:"actor_ref,omitempty"`
	EvidenceRef       string `json:"evidence_ref,omitempty"`
	Consent           string `json:"consent,omitempty"`
	ProducerSHA       string `json:"producer_sha,omitempty"`
	Schema            string `json:"schema,omitempty"`
	RevenueDocumentID string `json:"revenue_document_id,omitempty"`
	CustomerProofLane bool   `json:"customer_proof_lane,omitempty"`

	OfferVersion      string `json:"offer_version,omitempty"`
	TermsVersion      string `json:"terms_version,omitempty"`
	ExternalReference string `json:"external_reference,omitempty"`
	ProviderEventID   string `json:"provider_event_id,omitempty"`
	CompanyRef        string `json:"company_ref,omitempty"`
	CNPJHash          string `json:"cnpj_hash,omitempty"`
	HoldID            string `json:"hold_id,omitempty"`
	QueryClass        string `json:"query_class,omitempty"`
	ReferrerClass     string `json:"referrer_class,omitempty"`

	// Organic attribution contract (confenge.organic_attribution.v1).
	// OrganicSource is the unmixed taxonomy. Source may remain a producer
	// identity (web-cfg / CONFENGE_WEB). Query hash is never stored here.
	OrganicSource  string     `json:"organic_source,omitempty"`
	Medium         string     `json:"medium,omitempty"`
	Campaign       string     `json:"campaign,omitempty"`
	LandingPath    string     `json:"landing_path,omitempty"`
	AssetVersion   string     `json:"asset_version,omitempty"`
	CTAVersion     string     `json:"cta_version,omitempty"`
	RecordKind     string     `json:"record_kind,omitempty"`
	ConsentVersion string     `json:"consent_version,omitempty"`
	PageVersion    string     `json:"page_version,omitempty"`
	ContentVersion string     `json:"content_version,omitempty"`
	FirstTouchAt   *time.Time `json:"first_touch_at,omitempty"`
	LastTouchAt    *time.Time `json:"last_touch_at,omitempty"`
}

// ObservedFacts is one observed commercial record. Missing optional IDs
// stay empty and render as UNKNOWN. Nothing is invented.
type ObservedFacts struct {
	Keys JoinKeys `json:"keys"`

	LeadCreatedAt  time.Time  `json:"lead_created_at,omitempty"`
	IngestedAt     time.Time  `json:"warmbly_ingested_at,omitempty"`
	EnrichmentAt   *time.Time `json:"enrichment_completed_at,omitempty"`
	FirstActionAt  *time.Time `json:"first_action_at,omitempty"`
	ConversationAt *time.Time `json:"conversation_at,omitempty"`
	ProposalAt     *time.Time `json:"proposal_at,omitempty"`
	CloseAt        *time.Time `json:"close_at,omitempty"`

	ActionOccurredAt  time.Time `json:"action_occurred_at,omitempty"`
	OutcomeOccurredAt time.Time `json:"outcome_occurred_at,omitempty"`

	OutcomeType    string `json:"outcome_type,omitempty"`
	HumanConfirmed bool   `json:"human_confirmed,omitempty"`
	Qualified      bool   `json:"qualified,omitempty"`
	Conversation   bool   `json:"conversation,omitempty"`
	PipelineOpen   bool   `json:"pipeline_open,omitempty"`

	AttributionStale bool `json:"attribution_stale,omitempty"`
	RequiresFresh    bool `json:"requires_fresh,omitempty"`

	PublishedAt *time.Time `json:"published_at,omitempty"`
	DetectedAt  *time.Time `json:"detected_at,omitempty"`
	FollowUpAt  *time.Time `json:"follow_up_at,omitempty"`

	EventType        string `json:"event_type,omitempty"`
	NotALead         bool   `json:"not_a_lead,omitempty"`
	Correction       bool   `json:"correction,omitempty"`
	RevenueEvidenced bool   `json:"revenue_evidenced,omitempty"`
	RevenueCents     int64  `json:"revenue_cents,omitempty"`
	HandRaise        bool   `json:"hand_raise,omitempty"`
	Suppression      bool   `json:"suppression,omitempty"`
	Timezone         string `json:"timezone,omitempty"`

	Commercial CommercialState `json:"commercial,omitempty"`

	Synthetic bool   `json:"synthetic,omitempty"`
	Label     string `json:"label,omitempty"`

	RecordKind           string `json:"record_kind,omitempty"`
	ConsentValid         bool   `json:"consent_valid,omitempty"`
	StrippedGSCQuery     bool   `json:"stripped_gsc_query,omitempty"`
	StrippedQueryHash    bool   `json:"stripped_query_hash,omitempty"`
	ContradictorySource  bool   `json:"contradictory_source,omitempty"`
	SyntheticLabeledReal bool   `json:"synthetic_labeled_real,omitempty"`
	InvalidAssetVersion  bool   `json:"invalid_asset_version,omitempty"`
}

// Chain is one reconstructed observed path. Replay of the same IDs
// returns this row; it does not create a second chain.
type Chain struct {
	SchemaVersion string   `json:"schema_version"`
	Identity      string   `json:"identity"`
	MetricKey     string   `json:"metric_key"`
	Keys          JoinKeys `json:"keys"`

	Source         string `json:"source"`
	Query          string `json:"query"`
	AssetID        string `json:"asset_id"`
	LeadID         string `json:"lead_id"`
	ReceiptID      string `json:"receipt_id"`
	CorrelationID  string `json:"correlation_id"`
	AccountID      string `json:"account_id"`
	OpportunityID  string `json:"opportunity_id"`
	ProposalID     string `json:"proposal_id"`
	ChargeID       string `json:"charge_id"`
	PaymentID      string `json:"payment_id"`
	PersonID       string `json:"person_id"`
	ActionID       string `json:"action_id"`
	OutcomeID      string `json:"outcome_id"`
	OutboxEventID  string `json:"outbox_event_id"`
	IdempotencyKey string `json:"idempotency_key"`

	RouteFamily string `json:"route_family"`
	Trigger     string `json:"trigger"`
	OfferID     string `json:"offer_id"`
	Route       string `json:"route"`

	CTAID             string `json:"cta_id,omitempty"`
	AssetFamily       string `json:"asset_family,omitempty"`
	MarketAnswerID    string `json:"market_answer_id,omitempty"`
	AnalysisID        string `json:"analysis_id,omitempty"`
	Referrer          string `json:"referrer,omitempty"`
	IntentClass       string `json:"intent_class,omitempty"`
	CitationRoute     string `json:"citation_route,omitempty"`
	DistributionRoute string `json:"distribution_route,omitempty"`

	Versions Versions `json:"versions"`

	LeadCreatedAt  time.Time  `json:"lead_created_at,omitempty"`
	IngestedAt     time.Time  `json:"warmbly_ingested_at,omitempty"`
	EnrichmentAt   *time.Time `json:"enrichment_completed_at,omitempty"`
	FirstActionAt  *time.Time `json:"first_action_at,omitempty"`
	ConversationAt *time.Time `json:"conversation_at,omitempty"`
	ProposalAt     *time.Time `json:"proposal_at,omitempty"`
	CloseAt        *time.Time `json:"close_at,omitempty"`
	PublishedAt    *time.Time `json:"published_at,omitempty"`
	DetectedAt     *time.Time `json:"detected_at,omitempty"`
	FollowUpAt     *time.Time `json:"follow_up_at,omitempty"`

	OutcomeType       string `json:"outcome_type"`
	HumanConfirmed    bool   `json:"human_confirmed"`
	Qualified         bool   `json:"qualified"`
	Conversation      bool   `json:"conversation"`
	PipelineOpen      bool   `json:"pipeline_open"`
	Held              bool   `json:"held"`
	NotALead          bool   `json:"not_a_lead,omitempty"`
	RevenueEvidenced  bool   `json:"revenue_evidenced,omitempty"`
	RevenueCents      int64  `json:"revenue_cents,omitempty"`
	CorrectionApplied bool   `json:"correction_applied,omitempty"`
	EventType         string `json:"event_type,omitempty"`
	Timezone          string `json:"timezone,omitempty"`

	Commercial CommercialState `json:"commercial,omitempty"`

	AttributionKind string `json:"attribution_kind"`
	CausalProof     bool   `json:"causal_proof"`

	Synthetic bool      `json:"synthetic"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
}

// Versions are extra-cli watermarks copied, never invented.
type Versions struct {
	TargetFit          string `json:"target_fit_version"`
	ActivationPolicy   string `json:"activation_policy_version"`
	TargetFitWatermark string `json:"target_fit_watermark"`
	Fresh              bool   `json:"target_fit_fresh"`
}

const (
	ResolveLink             = "link"
	ResolveDefer            = "defer"
	ResolveReject           = "reject"
	ResolveExternalEvidence = "mark_external_evidence_required"

	StatusOpen             = "open"
	StatusDeferred         = "deferred"
	StatusRejected         = "rejected"
	StatusLinked           = "linked"
	StatusExternalEvidence = "external_evidence_required"

	SeverityHigh   = "high"
	SeverityMedium = "medium"
	SeverityLow    = "low"
)

// EvidenceItem is one observed join ID or classification fact. Nothing is invented.
type EvidenceItem struct {
	Kind  string `json:"kind"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

// QueueEvent is one audited step on an exception (classify, resolve, replay, refuse).
type QueueEvent struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"`
	Actor  string    `json:"actor,omitempty"`
	Action string    `json:"action,omitempty"`
	Reason string    `json:"reason,omitempty"`
	Detail string    `json:"detail,omitempty"`
}

// ExceptionResolution is the last legal operator action. Replay returns this.
type ExceptionResolution struct {
	Action         string    `json:"action"`
	Actor          string    `json:"actor"`
	Reason         string    `json:"reason"`
	At             time.Time `json:"at"`
	IdempotencyKey string    `json:"idempotency_key,omitempty"`
	BeforeStatus   string    `json:"before_status"`
	AfterStatus    string    `json:"after_status"`
	LinkIdentity   string    `json:"link_identity,omitempty"`
	LinkLeadID     string    `json:"link_lead_id,omitempty"`
	LinkActionID   string    `json:"link_action_id,omitempty"`
	LinkAccountID  string    `json:"link_account_id,omitempty"`
}

// Exception is a durable queue item. Out-of-order items are held.
type Exception struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organization_id,omitempty"`
	Code           string    `json:"code"`
	Reason         string    `json:"reason"`
	NextAction     string    `json:"next_action"`
	Identity       string    `json:"identity,omitempty"`
	MetricKey      string    `json:"metric_key,omitempty"`
	ActionID       string    `json:"action_id,omitempty"`
	OutcomeID      string    `json:"outcome_id,omitempty"`
	AccountID      string    `json:"account_id,omitempty"`
	LeadID         string    `json:"lead_id,omitempty"`
	ReceiptID      string    `json:"receipt_id,omitempty"`
	Held           bool      `json:"held"`
	Synthetic      bool      `json:"synthetic,omitempty"`
	At             time.Time `json:"at"`

	// Operator queue presentation. Additive; old payload rows get defaults on read.
	Lane           string               `json:"lane,omitempty"`
	Source         string               `json:"source,omitempty"`
	Severity       string               `json:"severity,omitempty"`
	Status         string               `json:"status,omitempty"`
	AgeSeconds     int64                `json:"age_seconds"`
	Evidence       []EvidenceItem       `json:"evidence,omitempty"`
	History        []QueueEvent         `json:"history,omitempty"`
	AllowedActions []string             `json:"allowed_actions,omitempty"`
	Resolution     *ExceptionResolution `json:"resolution,omitempty"`
	LinkedIdentity string               `json:"linked_identity,omitempty"`

	CodeVersion  string     `json:"code_version,omitempty"`
	Owner        string     `json:"owner,omitempty"`
	EvidenceRefs []string   `json:"evidence_refs,omitempty"`
	OpenedAt     time.Time  `json:"opened_at,omitempty"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`
	RetryState   string     `json:"retry_state,omitempty"`
}

// JoinResult is the shipped reconcile outcome.
type JoinResult struct {
	Chain           Chain       `json:"chain"`
	Exceptions      []Exception `json:"exceptions,omitempty"`
	Created         bool        `json:"created"`
	Replay          bool        `json:"replay"`
	Held            bool        `json:"held"`
	EventID         string      `json:"event_id,omitempty"`
	AcceptedVersion string      `json:"accepted_version,omitempty"`
	ReceiptID       string      `json:"receipt_id,omitempty"`
	Persisted       bool        `json:"persisted,omitempty"`
	RecordKind      string      `json:"record_kind,omitempty"`
	NotALead        bool        `json:"not_a_lead,omitempty"`
}

// LearningCandidate stays inside this capability. It never writes extra-cli,
// web-cfg, or SmartLic.
type LearningCandidate struct {
	ID              string    `json:"id"`
	OrganizationID  string    `json:"organization_id,omitempty"`
	Kind            string    `json:"kind"`
	Target          string    `json:"target"`
	Source          string    `json:"source"`
	Reason          string    `json:"reason"`
	Status          string    `json:"status"`
	Identity        string    `json:"identity,omitempty"`
	MetricKey       string    `json:"metric_key,omitempty"`
	AssetID         string    `json:"asset_id,omitempty"`
	OfferID         string    `json:"offer_id,omitempty"`
	ActionID        string    `json:"action_id,omitempty"`
	OutcomeID       string    `json:"outcome_id,omitempty"`
	LeadID          string    `json:"lead_id,omitempty"`
	CorrectionCodes []string  `json:"correction_codes,omitempty"`
	OutcomeType     string    `json:"outcome_type,omitempty"`
	UpstreamWrites  []string  `json:"upstream_writes"`
	Recommendation  string    `json:"recommendation"`
	CausalProof     bool      `json:"causal_proof"`
	Synthetic       bool      `json:"synthetic,omitempty"`
	At              time.Time `json:"at"`
}

// LearningInput is a human correction or a recorded outcome.
type LearningInput struct {
	From            string   `json:"from"`
	Reason          string   `json:"reason,omitempty"`
	CorrectionCodes []string `json:"correction_codes,omitempty"`
	OutcomeType     string   `json:"outcome_type,omitempty"`
	HumanConfirmed  bool     `json:"human_confirmed,omitempty"`
	Keys            JoinKeys `json:"keys"`
	Synthetic       bool     `json:"synthetic,omitempty"`
}

// FamilyCounts is one route-family slice of the executive view.
type FamilyCounts struct {
	Family                   string `json:"route_family"`
	InboundQualifiedPipeline int    `json:"inbound_qualified_pipeline"`
	QCO                      int    `json:"qco"`
	Conversations            int    `json:"conversations"`
	Meetings                 int    `json:"meetings"`
	Proposals                int    `json:"proposals"`
	Pipeline                 int    `json:"pipeline"`
	Won                      int    `json:"won"`
	Lost                     int    `json:"lost"`
	Unknown                  int    `json:"unknown"`
}

// Breakdown is one ID-keyed rollup. Keys are IDs, never PII.
type Breakdown struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// Denominators keep rates honest. Zero stays zero.
type Denominators struct {
	Leads         int `json:"leads"`
	Actions       int `json:"actions"`
	Outcomes      int `json:"outcomes"`
	Qualified     int `json:"qualified"`
	Conversations int `json:"conversations"`
	Meetings      int `json:"meetings"`
	Proposals     int `json:"proposals"`
	Closed        int `json:"closed"`
}

// LatencyMS is observed elapsed time between stamps. Missing stamps are
// censored, not coerced to zero. Negative/overlapping spans never enter
// these averages; they fail reconciliation instead.
type LatencyMS struct {
	PublishedToDetected    int64  `json:"published_to_detected_ms"`
	DetectedToLead         int64  `json:"detected_to_lead_ms"`
	LeadToIngest           int64  `json:"lead_to_ingest_ms"`
	LeadToFirstAction      int64  `json:"lead_to_first_action_ms"`
	IngestToEnrichment     int64  `json:"ingest_to_enrichment_ms"`
	EnrichmentToAction     int64  `json:"enrichment_to_action_ms"`
	ActionToConversation   int64  `json:"action_to_conversation_ms"`
	ConversationToProposal int64  `json:"conversation_to_proposal_ms"`
	ProposalToClose        int64  `json:"proposal_to_close_ms"`
	LeadToPayment          int64  `json:"lead_to_payment_ms"`
	PaymentToOnboarding    int64  `json:"payment_to_onboarding_ms"`
	OnboardingToActivation int64  `json:"onboarding_to_activation_ms"`
	SampledChains          int    `json:"sampled_chains"`
	CensoredCycles         int    `json:"censored_cycles"`
	Baseline               string `json:"baseline"`
}

// Freshness is the version/watermark surface. Empty stays UNKNOWN.
type Freshness struct {
	TargetFitVersions        []string `json:"target_fit_versions"`
	ActivationPolicyVersions []string `json:"activation_policy_versions"`
	Watermarks               []string `json:"watermarks"`
	StaleChains              int      `json:"stale_chains"`
	MissingVersionChains     int      `json:"missing_version_chains"`
}

// ExecutiveView is the monthly query payload. Not a CRM board.
type ExecutiveView struct {
	SchemaVersion            string               `json:"schema_version"`
	Month                    string               `json:"month"`
	IncludeSynthetic         bool                 `json:"include_synthetic"`
	InboundQualifiedPipeline int                  `json:"inbound_qualified_pipeline"`
	QCO                      int                  `json:"qco"`
	Conversations            int                  `json:"conversations"`
	Meetings                 int                  `json:"meetings"`
	Proposals                int                  `json:"proposals"`
	Pipeline                 int                  `json:"pipeline"`
	Won                      int                  `json:"won"`
	Lost                     int                  `json:"lost"`
	Unknown                  int                  `json:"unknown"`
	Families                 []FamilyCounts       `json:"families"`
	BySource                 []Breakdown          `json:"by_source"`
	ByAsset                  []Breakdown          `json:"by_asset"`
	ByTrigger                []Breakdown          `json:"by_trigger"`
	ByOffer                  []Breakdown          `json:"by_offer"`
	ByRoute                  []Breakdown          `json:"by_route"`
	ByIntent                 []Breakdown          `json:"by_intent,omitempty"`
	MarketAnswer             AssetSlice           `json:"market_answer"`
	ContractAnalysis         AssetSlice           `json:"contract_analysis"`
	B2GXRay                  AssetSlice           `json:"b2g_xray"`
	CustomerProof            int                  `json:"customer_proof"`
	RevenueCents             int64                `json:"revenue_cents"`
	RevenueStatus            string               `json:"revenue_status"`
	Commercial               CommercialCounts     `json:"commercial"`
	ByTerms                  []Breakdown          `json:"by_terms,omitempty"`
	ByCTA                    []Breakdown          `json:"by_cta,omitempty"`
	ByOfferVersion           []OfferExecutiveRow  `json:"by_offer_version,omitempty"`
	WeeklyRevenueChains      []WeeklyRevenueChain `json:"weekly_revenue_chains"`
	Denominators             Denominators         `json:"denominators"`
	Latency                  LatencyMS            `json:"latency"`
	Freshness                Freshness            `json:"freshness"`
	AttributionKind          string               `json:"attribution_kind"`
	CausalProof              bool                 `json:"causal_proof"`
	RealEmpty                bool                 `json:"real_empty"`
	ChainCount               int                  `json:"chain_count"`
}

// AssetSlice is one assisted-asset lane. It is not a CRM stage.
type AssetSlice struct {
	AssetFamily        string `json:"asset_family"`
	Leads              int    `json:"leads"`
	Qualified          int    `json:"qualified"`
	Actions            int    `json:"actions"`
	Meetings           int    `json:"meetings"`
	Proposals          int    `json:"proposals"`
	Pipeline           int    `json:"pipeline"`
	Won                int    `json:"won"`
	Lost               int    `json:"lost"`
	Unknown            int    `json:"unknown"`
	Completions        int    `json:"completions,omitempty"`
	HandRaises         int    `json:"hand_raises,omitempty"`
	RevenueCents       int64  `json:"revenue_cents"`
	RevenueStatus      string `json:"revenue_status"`
	MissingAttribution int    `json:"missing_attribution"`
}

func idOrUnknown(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return Unknown
	}
	return v
}

func normalizeFamily(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case FamilyInbound:
		return FamilyInbound
	case FamilyOutbound:
		return FamilyOutbound
	case FamilyPartner, "parceiro":
		return FamilyPartner
	case FamilyExpansion, "expansao", "expansão":
		return FamilyExpansion
	case "":
		return Unknown
	default:
		return Unknown
	}
}

func isWonType(t string) bool {
	u := strings.ToUpper(strings.TrimSpace(t))
	return u == OutcomeWon || u == OutcomeClient
}

func isLostType(t string) bool {
	return strings.EqualFold(strings.TrimSpace(t), OutcomeLost)
}

// CommercialEvent is the versioned envelope consumed by IngestEvent.
// PII lives only at PIIPointer and is never copied into metric keys.
type CommercialEvent struct {
	EventID            string     `json:"event_id"`
	Version            string     `json:"version"`
	Type               string     `json:"type"`
	OccurredAt         time.Time  `json:"occurred_at"`
	IngestedAt         time.Time  `json:"ingested_at"`
	Timezone           string     `json:"timezone"`
	CorrelationID      string     `json:"correlation_id,omitempty"`
	IdempotencyKey     string     `json:"idempotency_key,omitempty"`
	AssetFamily        string     `json:"asset_family,omitempty"`
	MarketAnswerID     string     `json:"market_answer_id,omitempty"`
	AnalysisID         string     `json:"analysis_id,omitempty"`
	Source             string     `json:"source,omitempty"`
	Referrer           string     `json:"referrer,omitempty"`
	Query              string     `json:"query,omitempty"`
	IntentClass        string     `json:"intent_class,omitempty"`
	CTAID              string     `json:"cta_id,omitempty"`
	OfferID            string     `json:"offer_id,omitempty"`
	Route              string     `json:"route,omitempty"`
	AccountPublicID    string     `json:"account_public_id,omitempty"`
	OpportunityID      string     `json:"opportunity_id,omitempty"`
	ProposalID         string     `json:"proposal_id,omitempty"`
	ChargeID           string     `json:"charge_id,omitempty"`
	PaymentID          string     `json:"payment_id,omitempty"`
	EntityPublicID     string     `json:"entity_public_id,omitempty"`
	ContractPublicID   string     `json:"contract_public_id,omitempty"`
	Consent            string     `json:"consent,omitempty"`
	Suppression        bool       `json:"suppression,omitempty"`
	ActorRef           string     `json:"actor_ref,omitempty"`
	EvidenceRef        string     `json:"evidence_ref,omitempty"`
	OutcomeState       string     `json:"outcome_state,omitempty"`
	ProducerSHA        string     `json:"producer_sha,omitempty"`
	Schema             string     `json:"schema,omitempty"`
	PIIPointer         string     `json:"pii_pointer,omitempty"`
	LeadID             string     `json:"lead_id,omitempty"`
	ReceiptID          string     `json:"receipt_id,omitempty"`
	ActionID           string     `json:"action_id,omitempty"`
	OutcomeID          string     `json:"outcome_id,omitempty"`
	RouteFamily        string     `json:"route_family,omitempty"`
	Trigger            string     `json:"trigger,omitempty"`
	AssetID            string     `json:"asset_id,omitempty"`
	Qualified          bool       `json:"qualified,omitempty"`
	HumanConfirmed     bool       `json:"human_confirmed,omitempty"`
	RevenueCents       int64      `json:"revenue_cents,omitempty"`
	RevenueDocumentID  string     `json:"revenue_document_id,omitempty"`
	PublishedAt        *time.Time `json:"published_at,omitempty"`
	DetectedAt         *time.Time `json:"detected_at,omitempty"`
	FollowUpAt         *time.Time `json:"follow_up_at,omitempty"`
	HandRaise          bool       `json:"hand_raise,omitempty"`
	Correction         bool       `json:"correction,omitempty"`
	CustomerProofLane  bool       `json:"customer_proof_lane,omitempty"`
	OrganizationID     string     `json:"organization_id,omitempty"`
	Synthetic          bool       `json:"synthetic,omitempty"`
	DeliverableID      string     `json:"deliverable_id,omitempty"`
	CommercialDecision string     `json:"commercial_decision,omitempty"`
	Responsible        string     `json:"responsible,omitempty"`
	Deadline           *time.Time `json:"deadline,omitempty"`
	NextAction         string     `json:"next_action,omitempty"`
	AllowReceiptRetry  bool       `json:"-"`

	Offer             OfferSnapshot    `json:"offer,omitempty"`
	Capacity          CapacitySnapshot `json:"capacity,omitempty"`
	Provider          ProviderRefs     `json:"provider,omitempty"`
	Payment           PaymentState     `json:"payment,omitempty"`
	Gates             GateStates       `json:"gates,omitempty"`
	ExternalReference string           `json:"external_reference,omitempty"`
	ProviderEventID   string           `json:"provider_event_id,omitempty"`
	RawEventType      string           `json:"raw_event_type,omitempty"`
	RawProviderStatus string           `json:"raw_provider_status,omitempty"`
	SMTPStatus        string           `json:"smtp_status,omitempty"`
	EnhancedStatus    string           `json:"enhanced_status,omitempty"`
	Diagnostic        string           `json:"diagnostic,omitempty"`
	CompanyRef        string           `json:"company_ref,omitempty"`
	CNPJ              string           `json:"-"`
	CNPJHash          string           `json:"cnpj_hash,omitempty"`
	QueryClass        string           `json:"query_class,omitempty"`
	ReferrerClass     string           `json:"referrer_class,omitempty"`
	CallbackOnly      bool             `json:"callback_only,omitempty"`

	// Producer aliases from web-cfg scripts/offers/events.cjs. Claims
	// (financial_confirmation / received_revenue / revenue) are never cash.
	ProducerOfferVersion    string `json:"offer_version,omitempty"`
	ProducerTermsVersion    string `json:"terms_version,omitempty"`
	ProducerScopeVersion    string `json:"scope_version,omitempty"`
	ProducerAmountCents     int64  `json:"amount_cents,omitempty"`
	ProducerCurrency        string `json:"currency,omitempty"`
	ProducerCanonicalStatus string `json:"canonical_status,omitempty"`
	ProducerExceptionCode   string `json:"exception_code,omitempty"`
	FinancialConfirmation   bool   `json:"financial_confirmation,omitempty"`
	ReceivedRevenueClaim    bool   `json:"received_revenue,omitempty"`
	RevenueClaim            bool   `json:"revenue,omitempty"`

	Medium         string     `json:"medium,omitempty"`
	Campaign       string     `json:"campaign,omitempty"`
	LandingPath    string     `json:"landing_path,omitempty"`
	LandingURL     string     `json:"landing_url,omitempty"`
	AssetVersion   string     `json:"asset_version,omitempty"`
	CTAVersion     string     `json:"cta_version,omitempty"`
	RecordKind     string     `json:"record_kind,omitempty"`
	ConsentVersion string     `json:"consent_version,omitempty"`
	PageVersion    string     `json:"page_version,omitempty"`
	ContentVersion string     `json:"content_version,omitempty"`
	FirstTouchAt   *time.Time `json:"first_touch_at,omitempty"`
	LastTouchAt    *time.Time `json:"last_touch_at,omitempty"`
	OrganicSource  string     `json:"organic_source,omitempty"`
	QueryHash      string     `json:"-"`
	GSCQuery       string     `json:"-"`

	Window        string     `json:"window,omitempty"`
	Coverage      string     `json:"coverage,omitempty"`
	Freshness     string     `json:"freshness,omitempty"`
	Eligible      *int       `json:"eligible,omitempty"`
	Appeared      *int       `json:"appeared,omitempty"`
	Clicked       *int       `json:"clicked,omitempty"`
	Engaged       *int       `json:"engaged,omitempty"`
	MeasurementAt *time.Time `json:"measurement_at,omitempty"`
	ConsentPolicy string     `json:"consent_policy,omitempty"`

	// Controlled email observability slices. UNKNOWN stays UNKNOWN.
	EmailRouteClass string `json:"email_route_class,omitempty"`
	CohortID        string `json:"cohort_id,omitempty"`
	PolicyVersion   string `json:"policy_version,omitempty"`
	ProviderName    string `json:"provider_name,omitempty"`
	BounceClass     string `json:"bounce_class,omitempty"`
	ReplyClass      string `json:"reply_class,omitempty"`
}

// ObservabilityReport is the executive JSON/MD payload for the learning loop.
type ObservabilityReport struct {
	SchemaVersion            string                        `json:"schema_version"`
	Month                    string                        `json:"month"`
	IncludeSynthetic         bool                          `json:"include_synthetic"`
	InboundQualifiedPipeline int                           `json:"inbound_qualified_pipeline"`
	ValidLeads               int                           `json:"valid_leads"`
	QualifiedLeads           int                           `json:"qualified_leads"`
	Actions                  int                           `json:"actions"`
	Outcomes                 int                           `json:"outcomes"`
	Meetings                 int                           `json:"meetings"`
	Proposals                int                           `json:"proposals"`
	Pipeline                 int                           `json:"pipeline"`
	Won                      int                           `json:"won"`
	Lost                     int                           `json:"lost"`
	Unknown                  int                           `json:"unknown"`
	Missing                  int                           `json:"missing"`
	Lanes                    map[string]int                `json:"lanes"`
	BySource                 []Breakdown                   `json:"by_source"`
	ByAsset                  []Breakdown                   `json:"by_asset"`
	ByIntent                 []Breakdown                   `json:"by_intent"`
	ByOffer                  []Breakdown                   `json:"by_offer"`
	ByRoute                  []Breakdown                   `json:"by_route"`
	MarketAnswer             AssetSlice                    `json:"market_answer"`
	ContractAnalysis         AssetSlice                    `json:"contract_analysis"`
	B2GXRay                  AssetSlice                    `json:"b2g_xray"`
	CustomerProof            int                           `json:"customer_proof"`
	Denominators             Denominators                  `json:"denominators"`
	ExceptionCounts          map[string]int                `json:"exception_counts"`
	EventsConsumed           int                           `json:"events_consumed"`
	Joins                    int                           `json:"joins"`
	Orphans                  int                           `json:"orphans"`
	Conflicts                int                           `json:"conflicts"`
	AttributionCompleteness  float64                       `json:"attribution_completeness"`
	Latency                  LatencyMS                     `json:"latency"`
	RevenueCents             int64                         `json:"revenue_cents"`
	RevenueStatus            string                        `json:"revenue_status"`
	Freshness                Freshness                     `json:"freshness"`
	LearningCandidates       []LearningSummary             `json:"learning_candidates"`
	Blockers                 []string                      `json:"blockers"`
	Recommendation           string                        `json:"recommendation"`
	CausalProof              bool                          `json:"causal_proof"`
	UpstreamWrites           []string                      `json:"upstream_writes"`
	RealEmpty                bool                          `json:"real_empty"`
	AutoSend                 bool                          `json:"auto_send"`
	EmailSideEffects         bool                          `json:"email_side_effects"`
	ControlledEmail          []ControlledEmailOutcomeSlice `json:"controlled_email"`
}

// LearningSummary is the PII-free learning row on the report.
type LearningSummary struct {
	Identity       string `json:"identity"`
	Recommendation string `json:"recommendation"`
	Reason         string `json:"reason"`
	Target         string `json:"target"`
	Synthetic      bool   `json:"synthetic"`
}
