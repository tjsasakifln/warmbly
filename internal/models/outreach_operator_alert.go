package models

import (
	"time"

	"github.com/google/uuid"
)

const (
	OperatorAlertTypeInboundAttention = "inbound_operator_attention"
	OperatorAlertPolicyV1             = "v1"

	OperatorAlertBandNew       = "NEW"
	OperatorAlertBandAttention = "ATTENTION"
	OperatorAlertBandAged      = "AGED"

	OperatorAlertStateAcknowledged     = "ACKNOWLEDGED"
	OperatorAlertStateActionRecorded   = "ACTION_RECORDED"
	OperatorAlertStateResolvedNoAction = "RESOLVED_NO_ACTION"
	OperatorAlertStateAlertFailed      = "ALERT_FAILED"

	OperatorAlertResolveDuplicate = "DUPLICATE"
	OperatorAlertResolveNotALead  = "NOT_A_LEAD"
	OperatorAlertResolveSpam      = "SPAM"
	OperatorAlertResolveTest      = "TEST"
	OperatorAlertResolveOther     = "OTHER"
)

// OutreachOperatorAlert is one durable operator-attention record per
// inbound lead/idempotency key. It is not a CRM row and does not send
// to the lead.
type OutreachOperatorAlert struct {
	ID             uuid.UUID `json:"alert_id"`
	OrganizationID uuid.UUID `json:"organization_id"`
	LeadID         string    `json:"lead_id"`
	ReceiptID      string    `json:"receipt_id,omitempty"`
	EventID        string    `json:"event_id"`
	AlertType      string    `json:"alert_type"`
	Synthetic      bool      `json:"synthetic"`

	CreatedAt      time.Time  `json:"created_at"`
	FirstEmittedAt *time.Time `json:"first_emitted_at,omitempty"`
	LastEmittedAt  *time.Time `json:"last_emitted_at,omitempty"`

	ChannelStates map[string]string `json:"channel_states,omitempty"`

	AcknowledgedAt   *time.Time `json:"acknowledged_at,omitempty"`
	AcknowledgedBy   string     `json:"acknowledged_by,omitempty"`
	FirstActionAt    *time.Time `json:"first_action_at,omitempty"`
	FirstActionType  string     `json:"first_action_type,omitempty"`
	ResolvedAt       *time.Time `json:"resolved_at,omitempty"`
	ResolutionReason string     `json:"resolution_reason,omitempty"`

	AttemptCount  int        `json:"attempt_count"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	FailureCode   string     `json:"failure_code,omitempty"`
	Owner         string     `json:"owner,omitempty"`
	Freshness     string     `json:"freshness,omitempty"`
	State         string     `json:"state"`
	PolicyVersion string     `json:"policy_version"`
	UpdatedAt     time.Time  `json:"updated_at"`
}
