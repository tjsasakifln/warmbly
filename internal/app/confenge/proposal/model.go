package proposal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	ProposalSchemaVersion      = "confenge.proposal.v1"
	ProposalEventSchemaVersion = "confenge.proposal_event.v1"
	DeliveryRequestSchema      = "confenge.delivery_order_requested.v1"
	FinancialGateSchema        = "confenge.financial_gate.v1"
	FinancialGateReconciled    = "confenge.financial_gate_reconciled.v1"
)

type State string

const (
	StateDraft          State = "DRAFT"
	StatePrepared       State = "PREPARED"
	StateApprovedToSend State = "APPROVED_TO_SEND"
	StateSent           State = "SENT"
	StateNegotiating    State = "NEGOTIATING"
	StateAccepted       State = "ACCEPTED"
	StateRejected       State = "REJECTED"
	StateExpired        State = "EXPIRED"
	StateUnknown        State = "UNKNOWN"
)

type Proposal struct {
	SchemaVersion        string     `json:"schema_version"`
	ProposalID           uuid.UUID  `json:"proposal_id"`
	ProposalVersion      int        `json:"proposal_version"`
	OrganizationID       uuid.UUID  `json:"organization_id"`
	AccountID            string     `json:"account_id"`
	ClientRef            string     `json:"client_ref"`
	OpportunityID        string     `json:"opportunity_id"`
	QCOID                string     `json:"qco_id"`
	DealID               string     `json:"deal_id,omitempty"`
	SourceLeadID         string     `json:"source_lead_id,omitempty"`
	CorrelationID        string     `json:"correlation_id"`
	OfferID              string     `json:"offer_id"`
	OfferVersion         string     `json:"offer_version"`
	DeliverableID        string     `json:"deliverable_id"`
	DeliverableVersion   string     `json:"deliverable_version"`
	ScopeVersion         string     `json:"scope_version"`
	PriceVersion         string     `json:"price_version"`
	TermsVersion         string     `json:"terms_version"`
	Amount               int64      `json:"amount"`
	Currency             string     `json:"currency"`
	Credits              []string   `json:"credits"`
	Addons               []string   `json:"addons"`
	Inputs               []string   `json:"inputs"`
	Exclusions           []string   `json:"exclusions"`
	Deadline             time.Time  `json:"deadline"`
	ValidUntil           time.Time  `json:"valid_until"`
	PreparedAt           *time.Time `json:"prepared_at,omitempty"`
	ApprovedAt           *time.Time `json:"approved_at,omitempty"`
	SentAt               *time.Time `json:"sent_at,omitempty"`
	DecisionState        State      `json:"decision_state"`
	DecisionAt           *time.Time `json:"decision_at,omitempty"`
	LiteralReasonRef     string     `json:"literal_reason_ref,omitempty"`
	AcceptedSnapshotHash string     `json:"accepted_snapshot_hash,omitempty"`
	EvidenceRefs         []string   `json:"evidence_refs"`
	CreatedBy            string     `json:"created_by"`
	Synthetic            bool       `json:"synthetic"`
	Version              int64      `json:"version"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type Draft struct {
	AccountID          string
	ClientRef          string
	OpportunityID      string
	QCOID              string
	DealID             string
	SourceLeadID       string
	CorrelationID      string
	OfferID            string
	OfferVersion       string
	DeliverableID      string
	DeliverableVersion string
	ScopeVersion       string
	PriceVersion       string
	TermsVersion       string
	Amount             int64
	Currency           string
	Credits            []string
	Addons             []string
	Inputs             []string
	Exclusions         []string
	Deadline           time.Time
	ValidUntil         time.Time
	EvidenceRefs       []string
	Synthetic          bool
}

type ProposalEvent struct {
	SchemaVersion   string    `json:"schema_version"`
	EventID         uuid.UUID `json:"event_id"`
	EventType       string    `json:"event_type"`
	OrganizationID  uuid.UUID `json:"organization_id"`
	ProposalID      uuid.UUID `json:"proposal_id"`
	ProposalVersion int       `json:"proposal_version"`
	State           State     `json:"state"`
	Actor           string    `json:"actor"`
	OccurredAt      time.Time `json:"occurred_at"`
	EvidenceRefs    []string  `json:"evidence_refs"`
}

type FinancialGateState string

const (
	FinancialGateUnknown        FinancialGateState = "UNKNOWN"
	FinancialGateSyntheticValid FinancialGateState = "SYNTHETIC_VALID"
	FinancialGateAuthorized     FinancialGateState = "AUTHORIZED"
)

type FinancialGate struct {
	SchemaVersion   string             `json:"schema_version"`
	State           FinancialGateState `json:"state"`
	Synthetic       bool               `json:"synthetic"`
	SourceEventID   *string            `json:"source_event_id"`
	ReceivedRevenue bool               `json:"received_revenue"`
	EvidenceRefs    []string           `json:"evidence_refs"`
}

type DeliveryOrderRequested struct {
	EventID              uuid.UUID     `json:"event_id"`
	SchemaVersion        string        `json:"schema_version"`
	Synthetic            bool          `json:"synthetic"`
	CorrelationID        string        `json:"correlation_id"`
	CausationID          string        `json:"causation_id"`
	IdempotencyKey       string        `json:"idempotency_key"`
	OrganizationID       uuid.UUID     `json:"organization_id"`
	AccountID            string        `json:"account_id"`
	ClientRef            string        `json:"client_ref"`
	OpportunityID        string        `json:"opportunity_id"`
	QCOID                string        `json:"qco_id"`
	ProposalID           uuid.UUID     `json:"proposal_id"`
	ProposalVersion      int           `json:"proposal_version"`
	AcceptedSnapshotHash string        `json:"accepted_snapshot_hash"`
	OfferID              string        `json:"offer_id"`
	OfferVersion         string        `json:"offer_version"`
	DeliverableID        string        `json:"deliverable_id"`
	DeliverableVersion   string        `json:"deliverable_version"`
	ScopeVersion         string        `json:"scope_version"`
	PriceVersion         string        `json:"price_version"`
	TermsVersion         string        `json:"terms_version"`
	FinancialGate        FinancialGate `json:"financial_gate"`
	OnboardingRef        string        `json:"onboarding_ref"`
	OccurredAt           time.Time     `json:"occurred_at"`
	EvidenceRefs         []string      `json:"evidence_refs"`
}

type Result struct {
	Proposal Proposal                `json:"proposal"`
	Events   []ProposalEvent         `json:"events"`
	Handoff  *DeliveryOrderRequested `json:"handoff,omitempty"`
	Replay   bool                    `json:"replay"`
}

func (p Proposal) AcceptedHash() (string, error) {
	if p.ProposalID == uuid.Nil || p.ProposalVersion < 1 {
		return "", fmt.Errorf("proposal identity required")
	}
	snapshot := struct {
		ProposalID         uuid.UUID `json:"proposal_id"`
		ProposalVersion    int       `json:"proposal_version"`
		OrganizationID     uuid.UUID `json:"organization_id"`
		AccountID          string    `json:"account_id"`
		ClientRef          string    `json:"client_ref"`
		OpportunityID      string    `json:"opportunity_id"`
		QCOID              string    `json:"qco_id"`
		DealID             string    `json:"deal_id,omitempty"`
		OfferID            string    `json:"offer_id"`
		OfferVersion       string    `json:"offer_version"`
		DeliverableID      string    `json:"deliverable_id"`
		DeliverableVersion string    `json:"deliverable_version"`
		ScopeVersion       string    `json:"scope_version"`
		PriceVersion       string    `json:"price_version"`
		TermsVersion       string    `json:"terms_version"`
		Amount             int64     `json:"amount"`
		Currency           string    `json:"currency"`
		Credits            []string  `json:"credits"`
		Addons             []string  `json:"addons"`
		Inputs             []string  `json:"inputs"`
		Exclusions         []string  `json:"exclusions"`
		Deadline           time.Time `json:"deadline"`
		ValidUntil         time.Time `json:"valid_until"`
	}{
		ProposalID: p.ProposalID, ProposalVersion: p.ProposalVersion,
		OrganizationID: p.OrganizationID, AccountID: strings.TrimSpace(p.AccountID),
		ClientRef: strings.TrimSpace(p.ClientRef), OpportunityID: strings.TrimSpace(p.OpportunityID),
		QCOID: strings.TrimSpace(p.QCOID), DealID: strings.TrimSpace(p.DealID),
		OfferID: strings.TrimSpace(p.OfferID), OfferVersion: strings.TrimSpace(p.OfferVersion),
		DeliverableID: strings.TrimSpace(p.DeliverableID), DeliverableVersion: strings.TrimSpace(p.DeliverableVersion),
		ScopeVersion: strings.TrimSpace(p.ScopeVersion), PriceVersion: strings.TrimSpace(p.PriceVersion),
		TermsVersion: strings.TrimSpace(p.TermsVersion), Amount: p.Amount, Currency: strings.ToUpper(strings.TrimSpace(p.Currency)),
		Credits: sortedCopy(p.Credits), Addons: sortedCopy(p.Addons), Inputs: sortedCopy(p.Inputs),
		Exclusions: sortedCopy(p.Exclusions), Deadline: p.Deadline.UTC(), ValidUntil: p.ValidUntil.UTC(),
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func sortedCopy(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
