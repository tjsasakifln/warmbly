package liveintel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// EventSchemaV1 tags the opportunity-event envelope, following the same
// versioned-contract convention as the surrounding CONFENGE packages.
const EventSchemaV1 = "CONFENGE_OPPORTUNITY_EVENT/1.0"

// EventType is the closed set of things that can happen to a watched subject.
type EventType string

const (
	EventNewOpportunity     EventType = "NEW_OPPORTUNITY"
	EventOpportunityChanged EventType = "OPPORTUNITY_CHANGED"
	EventDeadlineChanged    EventType = "DEADLINE_CHANGED"
	EventFitBecameRelevant  EventType = "FIT_BECAME_RELEVANT"
)

// EventTypes is the closed set in declaration order. It mirrors the intent
// kinds a subscription may carry.
var EventTypes = []EventType{
	EventNewOpportunity,
	EventOpportunityChanged,
	EventDeadlineChanged,
	EventFitBecameRelevant,
}

// Rejection reasons for an inbound opportunity event.
const (
	ReasonEventSchemaMismatch = "event_schema_mismatch"
	ReasonEventIDMissing      = "event_id_missing"
	ReasonEventTypeUnknown    = "event_type_not_in_closed_set"
	ReasonEventOrgMissing     = "event_organization_missing"
	ReasonEventSubjectMissing = "event_subject_key_missing"
	ReasonEventPayloadEmpty   = "event_payload_empty"
)

// OpportunityEvent is what a producer emits when a watched subject changes.
// Payload carries the "what changed" content and is the only input to the
// semantic content hash, so a re-emission of unchanged content hashes equal.
type OpportunityEvent struct {
	Schema     string            `json:"schema"`
	EventID    string            `json:"event_id"`
	EventType  EventType         `json:"event_type"`
	SubjectKey string            `json:"subject_key"`
	OrgID      uuid.UUID         `json:"org_id"`
	OccurredAt time.Time         `json:"occurred_at"`
	Payload    map[string]string `json:"payload"`
	// Official extra-cli#530 attributes. Absent on the current webhook binding;
	// when present they are fail-closed, never defaulted to safe.
	OfficialOpportunityFields
}

// Validate reports whether the event can be acted on, and why not when it
// cannot. Like the rest of this package it never produces an error value.
func (e OpportunityEvent) Validate() (bool, string) {
	if strings.TrimSpace(e.Schema) != EventSchemaV1 {
		return false, ReasonEventSchemaMismatch
	}
	if strings.TrimSpace(e.EventID) == "" {
		return false, ReasonEventIDMissing
	}
	found := false
	for _, known := range EventTypes {
		if e.EventType == known {
			found = true
			break
		}
	}
	if !found {
		return false, ReasonEventTypeUnknown
	}
	if e.OrgID == uuid.Nil {
		return false, ReasonEventOrgMissing
	}
	if strings.TrimSpace(e.SubjectKey) == "" {
		return false, ReasonEventSubjectMissing
	}
	if len(e.Payload) == 0 {
		return false, ReasonEventPayloadEmpty
	}
	return true, ""
}

// ContentHash is the semantic hash of what the watcher would be told. It covers
// the event type, the subject and the payload, and deliberately excludes the
// event id and the emission time, so re-emitting unchanged content hashes equal.
func (e OpportunityEvent) ContentHash() string {
	keys := make([]string, 0, len(e.Payload))
	for key := range e.Payload {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	sum := sha256.New()
	sum.Write([]byte(EventSchemaV1 + "\n"))
	sum.Write([]byte(string(e.EventType) + "\n"))
	sum.Write([]byte(strings.TrimSpace(e.SubjectKey) + "\n"))
	for _, key := range keys {
		sum.Write([]byte(key + "=" + e.Payload[key] + "\n"))
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// EventProducer is the pluggable source of opportunity events. No production
// implementation exists yet; the interface fixes the shape a later slice fills.
type EventProducer interface {
	Subscribe(ctx context.Context) (<-chan OpportunityEvent, error)
}
