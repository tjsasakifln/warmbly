package models

import "github.com/google/uuid"

// JobEventInboundBounce is emitted by the worker for an attributable delivery
// status notification. The consumer suppresses only a definitive HARD bounce;
// SOFT and UNKNOWN remain observable without suppressing the recipient.
type JobEventInboundBounce struct {
	UserID  uuid.UUID `json:"user_id"`
	EmailID uuid.UUID `json:"email_id"`
	// OriginalMessageID is the RFC Message-ID of the bounced outbound message,
	// used to resolve the campaign/contact/task.
	OriginalMessageID string `json:"original_message_id"`
	// FailedRecipient is the address that bounced, when the DSN exposed it.
	FailedRecipient string `json:"failed_recipient"`
	// Reason is a short human string (the bounce subject) for the event record.
	Reason string `json:"reason"`
	// BounceClass is HARD, SOFT, or UNKNOWN and comes from machine-readable DSN
	// status. Codes/diagnostic are additive provenance for the commercial ledger.
	BounceClass    string `json:"bounce_class"`
	EnhancedStatus string `json:"enhanced_status,omitempty"`
	SMTPStatus     string `json:"smtp_status,omitempty"`
	Diagnostic     string `json:"diagnostic,omitempty"`
}
