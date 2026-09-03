package models

import (
	"time"

	"github.com/google/uuid"
)

// IntelWatchInboxEvent is one durably stored inbound opportunity-event
// envelope, the row confenge_intel_watch_inbox holds.
//
// It exists because the real upstream posts once and cannot be asked to post
// again. Persisting the envelope at ingestion is what lets a PENDING delivery
// be reprocessed later: the delivery ledger stores identity, not content, so
// without this row there would be nothing to rebuild the notification from.
type IntelWatchInboxEvent struct {
	// OrganizationID is resolved by Warmbly's webhook auth, never read from the
	// posted payload. It is part of the row identity for that reason.
	OrganizationID uuid.UUID         `json:"organization_id"`
	EventID        string            `json:"event_id"`
	Schema         string            `json:"schema"`
	EventType      string            `json:"event_type"`
	SubjectKey     string            `json:"subject_key"`
	OccurredAt     time.Time         `json:"occurred_at"`
	Payload        map[string]string `json:"payload"`
	ReceivedAt     time.Time         `json:"received_at"`
	// EmittedCount and LastEmittedAt are observability only. Emission never
	// consumes a row, so neither field gates whether it is replayed again.
	EmittedCount  int        `json:"emitted_count"`
	LastEmittedAt *time.Time `json:"last_emitted_at,omitempty"`
}
