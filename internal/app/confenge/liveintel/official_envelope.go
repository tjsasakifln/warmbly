package liveintel

import (
	"encoding/json"
	"strings"
	"time"
)

// Official extra-cli#530 admission at the Warmbly border.
//
// The producer binding extra-cli/scripts/confenge_live_intelligence/warmbly_delivery.py
// posts CONFENGE_OPPORTUNITY_EVENT/1.0 (schema, event_id, event_type,
// subject_key, org_id, occurred_at, payload). That shape is the live webhook
// contract. Optional official fields (source_run_id, hashes, as_of, provenance,
// public_decision, freshness, schema_version) are validated fail-closed when
// present and left absent when the producer did not send them.
//
// A CONFENGE_LIVE_INTELLIGENCE/1.0 web bundle is not an opportunity event and
// must not fall through into inbound-lead ingest.

const (
	OfficialLiveIntelligenceSchema = "CONFENGE_LIVE_INTELLIGENCE/1.0"
	OfficialSchemaVersionV1        = "confenge-live-intelligence-schema/1.0"

	PublicDecisionPublicSafe    = "public_safe"
	PublicDecisionNotPublicSafe = "not_public_safe"

	FreshnessFresh = "FRESH"
	FreshnessStale = "STALE"

	ReasonEventStale            = "event_stale"
	ReasonEventRejected         = "event_rejected"
	ReasonEventNotPublicSafe    = "event_not_public_safe"
	ReasonEventHashMismatch     = "event_hash_mismatch"
	ReasonEventSchemaDrift      = "event_schema_drift"
	ReasonEventFreshnessInvalid = "event_freshness_invalid"
	ReasonEventUnknownStatus    = "event_status_unknown"
	ReasonEventOfficialBundle   = "event_official_bundle_not_opportunity_event"
)

// OfficialOpportunityFields are the extra-cli official attributes that may
// ride alongside CONFENGE_OPPORTUNITY_EVENT/1.0. Missing stays missing.
type OfficialOpportunityFields struct {
	SchemaVersion      string `json:"schema_version,omitempty"`
	SourceRunID        string `json:"source_run_id,omitempty"`
	ClaimedContentHash string `json:"content_hash,omitempty"`
	ManifestHash       string `json:"manifest_hash,omitempty"`
	AsOf               string `json:"as_of,omitempty"`
	Provenance         string `json:"provenance,omitempty"`
	PublicDecision     string `json:"public_decision,omitempty"`
	Freshness          string `json:"freshness,omitempty"`
	Status             string `json:"status,omitempty"`
}

// AdmitOfficialOpportunityEvent validates the official envelope at the border.
// On success it returns the event the existing inbox already understands.
// On failure it returns a reason code and no action may follow.
func AdmitOfficialOpportunityEvent(event OpportunityEvent) (OpportunityEvent, string) {
	if ok, reason := event.Validate(); !ok {
		return OpportunityEvent{}, reason
	}
	fields := event.OfficialOpportunityFields
	if ver := strings.TrimSpace(fields.SchemaVersion); ver != "" && ver != OfficialSchemaVersionV1 && ver != EventSchemaV1 {
		return OpportunityEvent{}, ReasonEventSchemaDrift
	}
	if decision := strings.ToLower(strings.TrimSpace(fields.PublicDecision)); decision != "" {
		switch decision {
		case PublicDecisionPublicSafe:
		case PublicDecisionNotPublicSafe:
			return OpportunityEvent{}, ReasonEventNotPublicSafe
		default:
			return OpportunityEvent{}, ReasonEventUnknownStatus
		}
	}
	if freshness := strings.ToUpper(strings.TrimSpace(fields.Freshness)); freshness != "" {
		switch freshness {
		case FreshnessFresh:
		case FreshnessStale:
			return OpportunityEvent{}, ReasonEventStale
		default:
			return OpportunityEvent{}, ReasonEventFreshnessInvalid
		}
	}
	if status := strings.ToLower(strings.TrimSpace(fields.Status)); status != "" {
		switch status {
		case "accepted", "ok", "pending", PublicDecisionPublicSafe:
		case "rejected", "invalid":
			return OpportunityEvent{}, ReasonEventRejected
		case "stale":
			return OpportunityEvent{}, ReasonEventStale
		case "unknown":
			return OpportunityEvent{}, ReasonEventUnknownStatus
		default:
			return OpportunityEvent{}, ReasonEventUnknownStatus
		}
	}
	if claimed := strings.TrimSpace(fields.ClaimedContentHash); claimed != "" && claimed != event.ContentHash() {
		return OpportunityEvent{}, ReasonEventHashMismatch
	}
	if asOf := strings.TrimSpace(fields.AsOf); asOf != "" {
		if _, err := time.Parse(time.RFC3339, asOf); err != nil {
			if _, err2 := time.Parse(time.RFC3339Nano, asOf); err2 != nil {
				return OpportunityEvent{}, ReasonEventFreshnessInvalid
			}
		}
	}
	return event, ""
}

// IsOfficialLiveIntelligenceBundle reports the extra-cli web export identity.
// That bundle is not an opportunity event and must not create a lead or watch.
func IsOfficialLiveIntelligenceBundle(raw []byte) bool {
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
	got := strings.TrimSpace(peek.Schema)
	if got == "" {
		got = strings.TrimSpace(peek.Version)
	}
	return got == OfficialLiveIntelligenceSchema
}
