package intel

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// The CONFENGE_WEB intent envelope.
//
// web-cfg posts this when a person asks, on the public site, to be told about
// something or to have a human look. It is the inbound counterpart of the
// outbound engines: the person is talking to us first, so the consent basis is
// their own act, recorded on the envelope rather than inferred later.
//
// This file is syntax only. It recognises and decodes the body; it decides
// nothing about whether the request may be acted on. Semantic validation and
// routing live in the confenge package, which owns the correlation-key rules
// and the subscription and hand-raise stores.

// WebIntentSchemaV1 tags the envelope, following the same versioned-contract
// convention as the other inbound bodies this endpoint accepts.
const WebIntentSchemaV1 = "CONFENGE_WEB_INTENT/1.0"

// The closed intent set. MONITOR_* asks for standing notification; REQUEST_*
// asks for a person. The two REQUEST_* values are byte-identical to the
// hand-raise signals they compose into, so no translation table exists.
const (
	WebIntentMonitorCompany     = "MONITOR_COMPANY"
	WebIntentMonitorOpportunity = "MONITOR_OPPORTUNITY"
	WebIntentRequestDeepDive    = "REQUEST_DEEP_DIVE"
	WebIntentRequestHumanReview = "REQUEST_HUMAN_REVIEW"
)

// WebIntentKinds is the closed set in declaration order.
var WebIntentKinds = []string{
	WebIntentMonitorCompany,
	WebIntentMonitorOpportunity,
	WebIntentRequestDeepDive,
	WebIntentRequestHumanReview,
}

// WebIntentEnvelope is the decoded body. Every field is carried as written;
// nothing here trims, defaults or infers.
type WebIntentEnvelope struct {
	Schema     string `json:"schema"`
	IntentKind string `json:"intent_kind"`
	// Lane must be the confenge_web engine lane. It is checked, never trusted
	// as a routing instruction: an external caller does not choose our lanes.
	Lane          string `json:"lane"`
	CompanyRef    string `json:"company_ref"`
	OpportunityID string `json:"opportunity_id"`
	ContactEmail  string `json:"contact_email"`
	ContactName   string `json:"contact_name"`
	Topic         string `json:"topic"`
	Cadence       string `json:"cadence"`
	// Consent provenance, in the same shape the subscription row stores.
	ConsentSource       string     `json:"consent_source"`
	ConsentAt           *time.Time `json:"consent_at"`
	ConsentProvenanceOK bool       `json:"consent_provenance_ok"`
	OccurredAt          *time.Time `json:"occurred_at"`
	Evidence            string     `json:"evidence"`
	Notes               string     `json:"notes"`
}

// IsWebIntentEnvelope reports a CONFENGE_WEB_INTENT/1.0 body. It is checked
// before the inbound-lead fallthrough, which would otherwise swallow it.
func IsWebIntentEnvelope(raw []byte) bool {
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
	return strings.TrimSpace(peek.Schema) == WebIntentSchemaV1 ||
		strings.TrimSpace(peek.Version) == WebIntentSchemaV1
}

// ParseWebIntentEnvelope decodes the body without judging it.
func ParseWebIntentEnvelope(raw []byte) (WebIntentEnvelope, error) {
	if !IsWebIntentEnvelope(raw) {
		return WebIntentEnvelope{}, fmt.Errorf("not %s", WebIntentSchemaV1)
	}
	var env WebIntentEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return WebIntentEnvelope{}, fmt.Errorf("%s: %w", WebIntentSchemaV1, err)
	}
	env.Schema = WebIntentSchemaV1
	return env, nil
}
