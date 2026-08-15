package models

import (
	"time"

	"github.com/google/uuid"
)

// Inbound lead contract and commercial-latency stamps. Warmbly owns the
// receipt and the action; extra-cli remains the fact authority.

const (
	InboundSchemaV1 = "confenge.inbound.v1"

	InboundEnrichmentUnknown     = "UNKNOWN"
	InboundEnrichmentCompleted   = "COMPLETED"
	InboundEnrichmentFailed      = "FAILED"
	InboundEnrichmentUnavailable = "UNAVAILABLE"

	InboundStatusOpen       = "OPEN"
	InboundStatusSuppressed = "SUPPRESSED"
	InboundStatusClosed     = "CLOSED"

	InboundOwnerUnknown = "UNKNOWN"

	InboundNextCall            = "CALL"
	InboundNextWhatsApp        = "WHATSAPP"
	InboundNextSendEmail       = "SEND_EMAIL"
	InboundNextRoutedCall      = "ROUTED_CALL"
	InboundNextManualOutreach  = "MANUAL_OUTREACH"
	InboundNextNeedsEnrichment = "NEEDS_ENRICHMENT"
	InboundNextSuppressed      = "SUPPRESSED"
)

// OutreachInboundLead is the durable web-cfg receipt. Persisted before
// enrichment so a lookup failure cannot drop the lead.
type OutreachInboundLead struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organization_id"`

	LeadID      string `json:"lead_id"`
	ReceiptID   string `json:"receipt_id"`
	IdentityKey string `json:"identity_key,omitempty"`

	LeadCreatedAt         time.Time  `json:"lead_created_at"`
	WarmblyIngestedAt     time.Time  `json:"warmbly_ingested_at"`
	EnrichmentCompletedAt *time.Time `json:"enrichment_completed_at,omitempty"`
	OwnerAssignedAt       *time.Time `json:"owner_assigned_at,omitempty"`
	FirstActionAt         *time.Time `json:"first_action_at,omitempty"`
	ConversationAt        *time.Time `json:"conversation_at,omitempty"`
	ProposalAt            *time.Time `json:"proposal_at,omitempty"`
	CloseAt               *time.Time `json:"close_at,omitempty"`

	Source        string `json:"source,omitempty"`
	RouteFamily   string `json:"route_family,omitempty"`
	AssetID       string `json:"asset_id,omitempty"`
	CTAID         string `json:"cta_id,omitempty"`
	LandingURL    string `json:"landing_url,omitempty"`
	ContractID    string `json:"contract_public_id,omitempty"`
	EntityID      string `json:"entity_public_id,omitempty"`
	CNPJ14        string `json:"cnpj14,omitempty"`
	CompanyName   string `json:"company_name,omitempty"`
	LeadName      string `json:"lead_name,omitempty"`
	LeadEmail     string `json:"lead_email,omitempty"`
	LeadPhone     string `json:"lead_phone,omitempty"`
	Referrer      string `json:"referrer,omitempty"`
	Message       string `json:"message,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`

	ConsentJSON []byte `json:"consent,omitempty"`
	UTMJSON     []byte `json:"utm,omitempty"`
	RawPayload  []byte `json:"raw_payload,omitempty"`

	EnrichmentStatus string   `json:"enrichment_status"`
	NextAction       string   `json:"next_action"`
	Channel          string   `json:"channel,omitempty"`
	WhyNow           string   `json:"why_now,omitempty"`
	Owner            string   `json:"owner"`
	Status           string   `json:"status"`
	SuppressReason   string   `json:"suppress_reason,omitempty"`
	DedupeOfLeadID   string   `json:"dedupe_of_lead_id,omitempty"`
	PersonID         string   `json:"person_id,omitempty"`
	PersonName       string   `json:"person_name,omitempty"`
	Evidence         []string `json:"evidence,omitempty"`
	Provenance       []string `json:"provenance,omitempty"`
	Warnings         []string `json:"warnings,omitempty"`

	AccountID   *uuid.UUID `json:"account_id,omitempty"`
	CandidateID *uuid.UUID `json:"candidate_id,omitempty"`
	ActionID    *uuid.UUID `json:"action_id,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
