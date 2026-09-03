package liveintel

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Inbound recognition for opportunity events, parallel to the other envelope
// recognizers the CONFENGE inbound webhook branches on.
//
// It must be checked before the inbound-lead fallthrough: an opportunity-event
// body carries neither the search-observation nor the commercial-event schema,
// so without this it would be misrouted into lead ingestion.

// IsOpportunityEventEnvelope reports a CONFENGE_OPPORTUNITY_EVENT/1.0 body.
func IsOpportunityEventEnvelope(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	var peek struct {
		Schema  string `json:"schema"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil {
		return false
	}
	return strings.TrimSpace(peek.Schema) == EventSchemaV1 ||
		strings.TrimSpace(peek.Version) == EventSchemaV1
}

// ParseOpportunityEvent decodes the body. It does not validate: the caller
// binds the organization first, because Validate requires one and the payload's
// own org id is never trusted.
func ParseOpportunityEvent(raw []byte) (OpportunityEvent, error) {
	if !IsOpportunityEventEnvelope(raw) {
		return OpportunityEvent{}, fmt.Errorf("not %s", EventSchemaV1)
	}
	var event OpportunityEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		return OpportunityEvent{}, fmt.Errorf("%s: %w", EventSchemaV1, err)
	}
	event.Schema = EventSchemaV1
	event.EventType = EventType(strings.ToUpper(strings.TrimSpace(string(event.EventType))))
	return event, nil
}
