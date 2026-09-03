package models

import (
	"time"

	"github.com/google/uuid"
)

// IntelSeedSend is one recorded INTEL_SEED touch, the row
// confenge_intel_seed_sends holds.
//
// INTEL_SEED takes no dispatch reservation and no queue row, so this table is
// the only place a seed send is counted. It is both the lane's own daily-cap
// counter and its no-resend record: the cap must never be a slice of the
// first-touch budget, and a crash between recording and sending must not be
// able to send the same recipient twice.
type IntelSeedSend struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	MessageKey     string    `json:"message_key"`
	CandidateID    uuid.UUID `json:"candidate_id"`
	AccountID      uuid.UUID `json:"account_id"`
	Recipient      string    `json:"recipient"`
	SubjectKey     string    `json:"subject_key"`
	SentAt         time.Time `json:"sent_at"`
}
