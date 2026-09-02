// Package liveintel carries the optional live-intelligence lookup that the
// CONFENGE outbound gate consults after a send is already allowed. Everything
// here is fail-open by construction: no value, error or panic produced in this
// package may change an outbound decision.
package liveintel

import (
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SchemaLiveIntelligenceV1 tags every payload this package accepts.
const SchemaLiveIntelligenceV1 = "CONFENGE_LIVE_INTELLIGENCE/1.0"

// Closed set of intelligence shapes a resolver may return.
const (
	KindOpportunity = "OPPORTUNITY"
	KindRadar       = "RADAR"
	KindProfile     = "PROFILE"
)

// Machine-readable rejection reasons from Validate.
const (
	ReasonSchemaMismatch   = "schema_mismatch"
	ReasonSubjectMissing   = "subject_key_missing"
	ReasonKindUnknown      = "kind_not_in_closed_set"
	ReasonPublicURLMissing = "public_url_missing"
	ReasonPublicURLInvalid = "public_url_not_a_public_https_url"
	ReasonAttestationEmpty = "attestation_missing"
	ReasonObservedAtUnset  = "observed_at_missing"
	ReasonPayloadNil       = "payload_nil"
)

// LiveIntelligenceV1 is the opportunity/radar/profile payload a resolver may
// attach to an allowed first touch. PublicURL is mandatory: intelligence that
// cannot be pointed at a specific public source is not usable in outbound copy.
type LiveIntelligenceV1 struct {
	Schema         string    `json:"schema"`
	OrganizationID uuid.UUID `json:"organization_id"`
	AccountID      uuid.UUID `json:"account_id"`
	SubjectKey     string    `json:"subject_key"`
	Kind           string    `json:"kind"`
	Headline       string    `json:"headline"`
	Summary        string    `json:"summary"`
	PublicURL      string    `json:"public_url"`
	ObservedAt     time.Time `json:"observed_at"`
	// Attestation is the producer's signed claim over this payload. Empty is
	// not an error, only a reason to ignore the payload.
	Attestation string `json:"attestation"`
}

// Validate reports whether the payload may be attached, and why not when it may
// not. It never returns an error: a malformed payload is absent intelligence,
// never a blocked send.
func (v *LiveIntelligenceV1) Validate() (bool, string) {
	if v == nil {
		return false, ReasonPayloadNil
	}
	if strings.TrimSpace(v.Schema) != SchemaLiveIntelligenceV1 {
		return false, ReasonSchemaMismatch
	}
	if strings.TrimSpace(v.SubjectKey) == "" {
		return false, ReasonSubjectMissing
	}
	switch strings.ToUpper(strings.TrimSpace(v.Kind)) {
	case KindOpportunity, KindRadar, KindProfile:
	default:
		return false, ReasonKindUnknown
	}
	raw := strings.TrimSpace(v.PublicURL)
	if raw == "" {
		return false, ReasonPublicURLMissing
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return false, ReasonPublicURLInvalid
	}
	if strings.TrimSpace(v.Attestation) == "" {
		return false, ReasonAttestationEmpty
	}
	if v.ObservedAt.IsZero() {
		return false, ReasonObservedAtUnset
	}
	return true, ""
}
