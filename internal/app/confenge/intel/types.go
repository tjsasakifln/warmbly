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

	LearningRepeat  = "repeat"
	LearningChange  = "change"
	LearningStop    = "stop"
	LearningUnknown = "UNKNOWN"

	AssociationObserved = "observed_association"
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

	// Inbound durable receipt (PR #71).
	LeadID    string `json:"lead_id,omitempty"`
	ReceiptID string `json:"receipt_id,omitempty"`

	// extra-cli fact / identity / version authority.
	AccountID               string   `json:"account_id,omitempty"`
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

	Synthetic bool   `json:"synthetic,omitempty"`
	Label     string `json:"label,omitempty"`
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
	PersonID       string `json:"person_id"`
	ActionID       string `json:"action_id"`
	OutcomeID      string `json:"outcome_id"`
	OutboxEventID  string `json:"outbox_event_id"`
	IdempotencyKey string `json:"idempotency_key"`

	RouteFamily string `json:"route_family"`
	Trigger     string `json:"trigger"`
	OfferID     string `json:"offer_id"`
	Route       string `json:"route"`

	Versions Versions `json:"versions"`

	LeadCreatedAt  time.Time  `json:"lead_created_at,omitempty"`
	IngestedAt     time.Time  `json:"warmbly_ingested_at,omitempty"`
	EnrichmentAt   *time.Time `json:"enrichment_completed_at,omitempty"`
	FirstActionAt  *time.Time `json:"first_action_at,omitempty"`
	ConversationAt *time.Time `json:"conversation_at,omitempty"`
	ProposalAt     *time.Time `json:"proposal_at,omitempty"`
	CloseAt        *time.Time `json:"close_at,omitempty"`

	OutcomeType    string `json:"outcome_type"`
	HumanConfirmed bool   `json:"human_confirmed"`
	Qualified      bool   `json:"qualified"`
	Conversation   bool   `json:"conversation"`
	PipelineOpen   bool   `json:"pipeline_open"`
	Held           bool   `json:"held"`

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
	Held           bool      `json:"held"`
	Synthetic      bool      `json:"synthetic,omitempty"`
	At             time.Time `json:"at"`
}

// JoinResult is the shipped reconcile outcome.
type JoinResult struct {
	Chain      Chain       `json:"chain"`
	Exceptions []Exception `json:"exceptions,omitempty"`
	Created    bool        `json:"created"`
	Replay     bool        `json:"replay"`
	Held       bool        `json:"held"`
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

// LatencyMS is observed elapsed time between stamps. Missing stamps stay 0.
type LatencyMS struct {
	LeadToIngest           int64  `json:"lead_to_ingest_ms"`
	IngestToEnrichment     int64  `json:"ingest_to_enrichment_ms"`
	EnrichmentToAction     int64  `json:"enrichment_to_action_ms"`
	ActionToConversation   int64  `json:"action_to_conversation_ms"`
	ConversationToProposal int64  `json:"conversation_to_proposal_ms"`
	ProposalToClose        int64  `json:"proposal_to_close_ms"`
	SampledChains          int    `json:"sampled_chains"`
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
	SchemaVersion            string         `json:"schema_version"`
	Month                    string         `json:"month"`
	IncludeSynthetic         bool           `json:"include_synthetic"`
	InboundQualifiedPipeline int            `json:"inbound_qualified_pipeline"`
	QCO                      int            `json:"qco"`
	Conversations            int            `json:"conversations"`
	Meetings                 int            `json:"meetings"`
	Proposals                int            `json:"proposals"`
	Pipeline                 int            `json:"pipeline"`
	Won                      int            `json:"won"`
	Lost                     int            `json:"lost"`
	Unknown                  int            `json:"unknown"`
	Families                 []FamilyCounts `json:"families"`
	BySource                 []Breakdown    `json:"by_source"`
	ByAsset                  []Breakdown    `json:"by_asset"`
	ByTrigger                []Breakdown    `json:"by_trigger"`
	ByOffer                  []Breakdown    `json:"by_offer"`
	ByRoute                  []Breakdown    `json:"by_route"`
	Denominators             Denominators   `json:"denominators"`
	Latency                  LatencyMS      `json:"latency"`
	Freshness                Freshness      `json:"freshness"`
	AttributionKind          string         `json:"attribution_kind"`
	CausalProof              bool           `json:"causal_proof"`
	RealEmpty                bool           `json:"real_empty"`
	ChainCount               int            `json:"chain_count"`
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
