package confenge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/app/confenge/liveintel"
)

// Multi-vertical NET_NEW_INBOUND_HANDRAISER consumer.
//
// Campaign 07 owns this path. Draft contract IDs are a fail-closed pin, not a
// production fallback: a missing or divergent hash is never ACCEPTED.
// INTEL_WATCH / Live Intelligence factual envelopes stay on their own schema
// and never create a CONFENGE_WEB hand-raiser here.

const (
	NetNewInboundHandraiserSchema = "NET_NEW_INBOUND_HANDRAISER/1.0.0-draft.20260904"
	NetNewInboundIntakeSchema     = "CONFENGE_WEB_INTAKE/2.0.0-draft.20260904"
	NetNewInboundTaxonomySchema   = "CONFENGE_CORPORATE_TAXONOMY/1.0.0-draft.20260904"
	NetNewInboundCatalogSchema    = "CONFENGE_OFFER_CATALOG/2.0.0-draft.20260904"
	NetNewInboundStateSchema      = "CONFENGE_HANDRAISER_STATE/1.0.0-draft.20260904"
	NetNewInboundMeetcfgSchema    = "MEETCFG_HANDRAISER_CONTEXT/1.0.0-draft.20260904"
	NetNewInboundSource           = "CONFENGE_WEB"
	NetNewInboundLane             = "CONFENGE_WEB"
	NetNewInboundSourceAsset      = "private_project_technical_readiness_v1"
	NetNewInboundOfferCandidate   = "private_project_technical_readiness_assessment"

	NetNewInboundFamilyPrefix = "NET_NEW_INBOUND_HANDRAISER/"

	NetNewInboundOutcomeAccepted = "ACCEPTED"
	NetNewInboundOutcomeRejected = "REJECTED_WITH_REASON"
	NetNewInboundOutcomeUnknown  = "UNKNOWN"

	NetNewInboundAckActor = "warmbly.net_new_inbound_consumer"

	NetNewInboundReasonHashUnpinned     = "schema_hash_unpinned"
	NetNewInboundReasonHashMismatch     = "schema_hash_mismatch"
	NetNewInboundReasonSchemaUnknown    = "schema_version_unknown"
	NetNewInboundReasonSchemaMismatch   = "schema_mismatch"
	NetNewInboundReasonSource           = "source_not_confenge_web"
	NetNewInboundReasonLane             = "lane_not_confenge_web"
	NetNewInboundReasonConsent          = "consent_missing"
	NetNewInboundReasonConflictDecline  = "conflict_decline"
	NetNewInboundReasonConflictUnknown  = "conflict_unknown"
	NetNewInboundReasonLogicalID        = "logical_id_missing"
	NetNewInboundReasonNucleus          = "nucleus_unknown"
	NetNewInboundReasonIntelWatch       = "intel_watch_not_handraiser"
	NetNewInboundReasonDownstream       = "downstream_unavailable"
	NetNewInboundReasonStale            = "readback_stale"
	NetNewInboundReasonSensitiveRefOnly = "sensitive_data_ref_only"
	NetNewInboundReasonStoreUnavailable = "inbound_store_unavailable"

	netNewInboundConflictNone    = "NONE"
	netNewInboundConflictDecline = "DECLINE"
	netNewInboundConflictUnknown = "UNKNOWN"

	netNewProvPolicy   = "policy:"
	netNewProvOutcome  = "outcome:"
	netNewProvReason   = "reason:"
	netNewProvAckBy    = "ack_by:"
	netNewProvAckAt    = "ack_at:"
	netNewProvNucleus  = "nucleus:"
	netNewProvOffer    = "offer_candidate:"
	netNewProvAsset    = "source_asset:"
	netNewProvCity     = "city_class:"
	netNewProvUrgency  = "urgency:"
	netNewProvConflict = "conflict_ref:"
	netNewProvIntake   = "intake_schema:"
	netNewProvState    = "state_schema:"
	netNewProvHash     = "schema_hash:"
)

// NetNewInboundPinnedHash is the SHA-256 of NetNewInboundPinMaterial.
// Tests recompute it from the published fixture. Runtime requires this exact
// hash on the envelope; the draft version string alone is never enough.
const NetNewInboundPinnedHash = "92bafd8b644b1355bcf457e2aa55a7a902030234cc65139bd1c2a24ff880a30b"

// NetNewInboundNuclei is the closed taxonomy set for this pin.
var NetNewInboundNuclei = []string{
	"expert_evidence_assistance",
	"property_valuation",
	"building_engineering_documentation",
	"occupational_safety",
	"public_works_b2g",
}

// NetNewInboundPinMaterial is the canonical pin document. Goal 97 ratifies it.
func NetNewInboundPinMaterial() string {
	return strings.Join([]string{
		NetNewInboundTaxonomySchema,
		"nuclei=" + strings.Join(NetNewInboundNuclei, ","),
		NetNewInboundCatalogSchema,
		NetNewInboundIntakeSchema,
		NetNewInboundHandraiserSchema,
		NetNewInboundStateSchema,
		NetNewInboundMeetcfgSchema,
		"source=" + NetNewInboundSource,
		"lane=" + NetNewInboundLane,
		"asset=" + NetNewInboundSourceAsset,
		"offer=" + NetNewInboundOfferCandidate,
		"invariants=outbound_eligible=false,auto_send=false",
	}, "\n")
}

// NetNewInboundPinHash is SHA-256 of the pin material.
func NetNewInboundPinHash() string {
	sum := sha256.Sum256([]byte(NetNewInboundPinMaterial()))
	return hex.EncodeToString(sum[:])
}

// NetNewInboundEnvelope is the decoded body. Nothing here admits.
type NetNewInboundEnvelope struct {
	Schema            string                `json:"schema"`
	Version           string                `json:"version"`
	SchemaHash        string                `json:"schema_hash"`
	Policy            string                `json:"policy"`
	PolicyHash        string                `json:"policy_hash"`
	IntakeSchema      string                `json:"intake_schema"`
	Taxonomy          string                `json:"taxonomy"`
	Catalog           string                `json:"catalog"`
	Source            string                `json:"source"`
	Lane              string                `json:"lane"`
	LogicalID         string                `json:"logical_id"`
	EventID           string                `json:"event_id"`
	IdempotencyKey    string                `json:"idempotency_key"`
	CorrelationID     string                `json:"correlation_id"`
	CanonicalEntityID string                `json:"canonical_entity_id"`
	Nucleus           string                `json:"nucleus"`
	OfferCandidate    string                `json:"offer_candidate"`
	SourceAsset       string                `json:"source_asset"`
	CityClass         string                `json:"city_class"`
	Urgency           string                `json:"urgency"`
	WhyNow            string                `json:"why_now"`
	Person            NetNewInboundParty    `json:"person"`
	Company           NetNewInboundParty    `json:"company"`
	Consent           NetNewInboundConsent  `json:"consent"`
	Conflict          NetNewInboundConflict `json:"conflict"`
	SensitiveData     bool                  `json:"sensitive_data"`
	OccurredAt        *time.Time            `json:"occurred_at"`
}

// NetNewInboundParty is a person or company on the envelope.
type NetNewInboundParty struct {
	CanonicalID string `json:"canonical_id"`
	Email       string `json:"email"`
	Name        string `json:"name"`
}

// NetNewInboundConsent is observed opt-in. Absence is fail-closed.
type NetNewInboundConsent struct {
	Granted bool       `json:"granted"`
	Source  string     `json:"source"`
	At      *time.Time `json:"at"`
}

// NetNewInboundConflict is a protected reference only. Parts and corpus never
// travel with it into analytics.
type NetNewInboundConflict struct {
	Status string `json:"status"`
	Ref    string `json:"ref"`
}

// NetNewInboundDecision is the pure admission result.
type NetNewInboundDecision struct {
	Outcome string
	Reason  string
}

// IsNetNewInboundHandraiserEnvelope reports this family before lead fallthrough.
// Unknown versions of the family still match so they fail closed here instead
// of being swallowed as an inbound lead.
func IsNetNewInboundHandraiserEnvelope(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	var peek struct {
		Schema  string `json:"schema"`
		Version string `json:"version"`
		Policy  string `json:"policy"`
		Type    string `json:"type"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil {
		return false
	}
	if liveintel.IsOpportunityEventEnvelope(raw) || liveintel.IsOfficialLiveIntelligenceBundle(raw) {
		return false
	}
	if intel.IsWebIntentEnvelope(raw) {
		return false
	}
	for _, v := range []string{peek.Schema, peek.Version, peek.Policy, peek.Type} {
		v = strings.TrimSpace(v)
		if v == NetNewInboundHandraiserSchema || strings.HasPrefix(v, NetNewInboundFamilyPrefix) {
			return true
		}
	}
	return false
}

// ParseNetNewInboundEnvelope decodes without admitting.
func ParseNetNewInboundEnvelope(raw []byte) (NetNewInboundEnvelope, error) {
	var env NetNewInboundEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return NetNewInboundEnvelope{}, err
	}
	var extra struct {
		Consent  NetNewInboundConsent  `json:"consent"`
		Conflict NetNewInboundConflict `json:"conflict"`
		Email    string                `json:"email"`
		Name     string                `json:"name"`
		Company  string                `json:"company"`
	}
	_ = json.Unmarshal(raw, &extra)
	env.Consent = extra.Consent
	env.Conflict = extra.Conflict
	if strings.TrimSpace(env.Person.Email) == "" {
		env.Person.Email = extra.Email
	}
	if strings.TrimSpace(env.Person.Name) == "" {
		env.Person.Name = extra.Name
	}
	if strings.TrimSpace(env.Company.Name) == "" && extra.Company != "" {
		env.Company.Name = extra.Company
	}
	env.LogicalID = firstNonEmpty(strings.TrimSpace(env.LogicalID), strings.TrimSpace(env.EventID), strings.TrimSpace(env.IdempotencyKey))
	return env, nil
}

// DecideNetNewInbound is the fail-closed admission function. It never persists
// and never talks to SMTP.
func DecideNetNewInbound(env NetNewInboundEnvelope) NetNewInboundDecision {
	schema := strings.TrimSpace(env.Schema)
	if schema == "" {
		schema = strings.TrimSpace(env.Version)
	}
	if schema != NetNewInboundHandraiserSchema {
		if strings.HasPrefix(schema, NetNewInboundFamilyPrefix) {
			return NetNewInboundDecision{Outcome: NetNewInboundOutcomeUnknown, Reason: NetNewInboundReasonSchemaUnknown}
		}
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonSchemaMismatch}
	}
	if strings.TrimSpace(env.LogicalID) == "" {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonLogicalID}
	}
	hash := firstNonEmpty(strings.TrimSpace(env.SchemaHash), strings.TrimSpace(env.PolicyHash))
	if hash == "" {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonHashUnpinned}
	}
	if !strings.EqualFold(hash, NetNewInboundPinnedHash) {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonHashMismatch}
	}
	if pol := strings.TrimSpace(env.Policy); pol != "" && pol != NetNewInboundHandraiserSchema {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonSchemaMismatch}
	}
	if intake := strings.TrimSpace(env.IntakeSchema); intake != "" && intake != NetNewInboundIntakeSchema {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonSchemaMismatch}
	}
	if src := strings.ToUpper(strings.TrimSpace(env.Source)); src != NetNewInboundSource {
		if src == "INTEL_WATCH" || src == strings.ToUpper(EngineLaneIntelWatch) {
			return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonIntelWatch}
		}
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonSource}
	}
	if !netNewLaneOK(env.Lane) {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonLane}
	}
	if !netNewNucleusOK(env.Nucleus) {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonNucleus}
	}
	if !env.Consent.Granted || strings.TrimSpace(env.Consent.Source) == "" || env.Consent.At == nil || env.Consent.At.IsZero() {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonConsent}
	}
	switch strings.ToUpper(strings.TrimSpace(env.Conflict.Status)) {
	case "", netNewInboundConflictNone:
	case netNewInboundConflictDecline:
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonConflictDecline}
	case netNewInboundConflictUnknown:
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeUnknown, Reason: NetNewInboundReasonConflictUnknown}
	default:
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeUnknown, Reason: NetNewInboundReasonConflictUnknown}
	}
	return NetNewInboundDecision{Outcome: NetNewInboundOutcomeAccepted}
}

func netNewLaneOK(lane string) bool {
	switch strings.TrimSpace(lane) {
	case NetNewInboundLane, EngineLaneConfengeWeb:
		return true
	default:
		return false
	}
}

func netNewNucleusOK(nucleus string) bool {
	nucleus = strings.TrimSpace(nucleus)
	for _, n := range NetNewInboundNuclei {
		if n == nucleus {
			return true
		}
	}
	return false
}

// MeetcfgHandoffAllowed is true only after ACCEPTED.
func MeetcfgHandoffAllowed(outcome string) bool {
	return strings.TrimSpace(outcome) == NetNewInboundOutcomeAccepted
}

// NetNewConflictRef stores only the protected reference. Corpus is dropped.
func NetNewConflictRef(conflict NetNewInboundConflict) string {
	ref := strings.TrimSpace(conflict.Ref)
	if ref == "" {
		return ""
	}
	ref = strings.TrimSpace(strings.Split(ref, "\n")[0])
	if i := strings.Index(ref, " "); i > 0 {
		ref = ref[:i]
	}
	return SanitizeText(ref, 120)
}

func netNewCanonicalEntityID(env NetNewInboundEnvelope) string {
	return firstNonEmpty(
		strings.TrimSpace(env.CanonicalEntityID),
		strings.TrimSpace(env.Company.CanonicalID),
		strings.TrimSpace(env.Person.CanonicalID),
	)
}

func netNewLogicalID(env NetNewInboundEnvelope) string {
	return SanitizeText(firstNonEmpty(env.LogicalID, env.EventID, env.IdempotencyKey), 160)
}
