package models

import "github.com/google/uuid"

// JobEventInboundDelivery is emitted only for an explicit positive DSN.
type JobEventInboundDelivery struct {
	UserID            uuid.UUID `json:"user_id"`
	EmailID           uuid.UUID `json:"email_id"`
	OriginalMessageID string    `json:"original_message_id"`
	Recipient         string    `json:"recipient,omitempty"`
	EnhancedStatus    string    `json:"enhanced_status,omitempty"`
	SMTPStatus        string    `json:"smtp_status,omitempty"`
	Diagnostic        string    `json:"diagnostic,omitempty"`
}
