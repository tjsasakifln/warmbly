package confenge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/app/confenge/liveintel"
	"github.com/warmbly/warmbly/internal/app/whatsapp"
)

// Multi-vertical NET_NEW_INBOUND_HANDRAISER consumer.
//
// Admission is fail-closed on the published Governance authority:
// contract_id + version + policy_hash. RuntimeInboundAuthorityPin carries that
// authority; fixture schemas are never a production fallback. INTEL_WATCH / Live Intelligence
// factual envelopes stay on their own schema and never create a CONFENGE_WEB
// hand-raiser here.

const (
	NetNewInboundHandraiserSchema     = "NET_NEW_INBOUND_HANDRAISER/1.0.0-draft.20260904"
	NetNewInboundIntakeSchema         = "CONFENGE_WEB_INTAKE/2.0.0-draft.20260904"
	NetNewInboundTaxonomySchema       = "CONFENGE_CORPORATE_TAXONOMY/1.0.0-draft.20260904"
	NetNewInboundCatalogSchema        = "CONFENGE_OFFER_CATALOG/2.0.0-draft.20260904"
	NetNewInboundStateSchema          = "CONFENGE_HANDRAISER_STATE/1.0.0-draft.20260904"
	NetNewInboundMeetcfgSchema        = "MEETCFG_HANDRAISER_CONTEXT/1.0.0-draft.20260904"
	NetNewInboundSource               = "CONFENGE_WEB"
	NetNewInboundLane                 = "CONFENGE_WEB"
	NetNewInboundSourceAsset          = "private_project_technical_readiness_v1"
	NetNewInboundOfferCandidate       = "private_project_technical_readiness_assessment"
	NetNewInboundTriageSourceAsset    = "technical_triage_v1"
	NetNewInboundTriageOfferCandidate = "technical_triage_review"

	NetNewInboundFamilyPrefix = "NET_NEW_INBOUND_HANDRAISER/"
	NetNewInboundContractID   = "NET_NEW_INBOUND_HANDRAISER"
	NetNewInboundPinVersion   = "1.0.0-draft.20260904"

	NetNewInboundOutcomeAccepted = "ACCEPTED"
	NetNewInboundOutcomeRejected = "REJECTED_WITH_REASON"
	NetNewInboundOutcomeUnknown  = "UNKNOWN"

	NetNewInboundAckActor = "warmbly.net_new_inbound_consumer"

	NetNewInboundReasonHashUnpinned     = "schema_hash_unpinned"
	NetNewInboundReasonHashMismatch     = "schema_hash_mismatch"
	NetNewInboundReasonSchemaUnknown    = "schema_version_unknown"
	NetNewInboundReasonSchemaMismatch   = "schema_mismatch"
	NetNewInboundReasonContractMismatch = "contract_id_mismatch"
	NetNewInboundReasonSource           = "source_not_confenge_web"
	NetNewInboundReasonLane             = "lane_not_confenge_web"
	NetNewInboundReasonConsent          = "consent_missing"
	NetNewInboundReasonConflictDecline  = "conflict_decline"
	NetNewInboundReasonConflictHit      = "conflict_hit"
	NetNewInboundReasonConflictUnknown  = "conflict_unknown"
	NetNewInboundReasonContact          = "contact_channel_missing"
	NetNewInboundReasonPreferredChannel = "preferred_channel_invalid"
	NetNewInboundReasonLogicalID        = "logical_id_missing"
	NetNewInboundReasonNucleus          = "nucleus_unknown"
	NetNewInboundReasonOfferCandidate   = "offer_candidate_unknown"
	NetNewInboundReasonSourceAsset      = "source_asset_unknown"
	NetNewInboundReasonOutboundClaim    = "outbound_inheritance_forbidden"
	NetNewInboundReasonAutoSendClaim    = "auto_send_forbidden"
	NetNewInboundReasonIntelWatch       = "intel_watch_not_handraiser"
	NetNewInboundReasonDownstream       = "downstream_unavailable"
	NetNewInboundReasonStale            = "readback_stale"
	NetNewInboundReasonSensitiveRefOnly = "sensitive_data_ref_only"
	NetNewInboundReasonStoreUnavailable = "inbound_store_unavailable"
	// NetNewInboundReasonKeyConflict is returned when a logical_id is reused
	// with material that would decide differently. Fail-closed: the stored
	// decision is never handed to a payload that did not earn it.
	NetNewInboundReasonKeyConflict = "idempotency_key_conflict"

	netNewInboundConflictNone        = "NONE"
	netNewInboundConflictClear       = "CLEAR"
	netNewInboundConflictDecline     = "DECLINE"
	netNewInboundConflictHit         = "HIT"
	netNewInboundConflictUnknown     = "UNKNOWN"
	netNewInboundConflictNotScreened = "NOT_SCREENED"

	NetNewInboundQualificationNeedsContext          = "NEEDS_CONTEXT"
	NetNewInboundQualificationPotentialFit          = "POTENTIAL_FIT"
	NetNewInboundQualificationConflictCheckRequired = "CONFLICT_CHECK_REQUIRED"
	NetNewInboundQualificationDocumentGap           = "DOCUMENT_GAP"
	NetNewInboundQualificationCapacityReview        = "CAPACITY_REVIEW"
	NetNewInboundQualificationPartnerRequired       = "PARTNER_REQUIRED"
	NetNewInboundQualificationOutOfScope            = "OUT_OF_SCOPE"
	NetNewInboundQualificationQCO                   = "QCO"

	NetNewInboundPreferredEmail    = "EMAIL"
	NetNewInboundPreferredWhatsApp = "WHATSAPP"

	netNewProvPolicy         = "policy:"
	netNewProvOutcome        = "outcome:"
	netNewProvReason         = "reason:"
	netNewProvAckBy          = "ack_by:"
	netNewProvAckAt          = "ack_at:"
	netNewProvNucleus        = "nucleus:"
	netNewProvOffer          = "offer_candidate:"
	netNewProvAsset          = "source_asset:"
	netNewProvCity           = "city_class:"
	netNewProvUrgency        = "urgency:"
	netNewProvConflict       = "conflict_ref:"
	netNewProvConflictStatus = "conflict_status:"
	netNewProvQualification  = "qualification_state:"
	netNewProvChannel        = "preferred_channel:"
	netNewProvIntake         = "intake_schema:"
	netNewProvState          = "state_schema:"
	netNewProvHash           = "schema_hash:"
	netNewProvDigest         = "admission_digest:"
)

// NetNewInboundPinnedHash is the SHA-256 of NetNewInboundPinMaterial: this
// repository's LOCAL drift digest over its own restatement of the nuclei and
// schemas. It is NOT the Governance policy_hash and is never an admission key.
// RuntimeInboundAuthorityPin does not use this constant.
const NetNewInboundPinnedHash = "1357748cc0c269d406665d9fa7219694c0e63f2389196ce8e19d5122e86fce86"

// NetNewInboundNuclei is the closed taxonomy set for this pin.
var NetNewInboundNuclei = []string{
	"expert_evidence_assistance",
	"property_valuation",
	"building_engineering_documentation",
	"occupational_safety",
	"public_works_b2g",
	"other_technical_need",
}

// NetNewInboundOfferCandidates and NetNewInboundSourceAssets preserve the
// donor readiness intake while admitting the MV-03 technical-triage surface.
var NetNewInboundOfferCandidates = []string{
	NetNewInboundOfferCandidate,
	NetNewInboundTriageOfferCandidate,
}

var NetNewInboundSourceAssets = []string{
	NetNewInboundSourceAsset,
	NetNewInboundTriageSourceAsset,
}

// NetNewInboundEnvelope is the decoded body. Nothing here admits.
type NetNewInboundEnvelope struct {
	Schema                 string                        `json:"schema"`
	ContractID             string                        `json:"contract_id"`
	PolicyID               string                        `json:"policy_id"`
	Version                string                        `json:"version"`
	PolicyVersion          string                        `json:"policy_version"`
	ContentHash            string                        `json:"content_hash"`
	SchemaHash             string                        `json:"schema_hash"`
	Policy                 string                        `json:"policy"`
	PolicyHash             string                        `json:"policy_hash"`
	IntakeSchema           string                        `json:"intake_schema"`
	Taxonomy               string                        `json:"taxonomy"`
	Catalog                string                        `json:"catalog"`
	Source                 string                        `json:"source"`
	Lane                   string                        `json:"lane"`
	IntentKind             string                        `json:"intent_kind"`
	LogicalID              string                        `json:"logical_id"`
	EventID                string                        `json:"event_id"`
	IdempotencyKey         string                        `json:"idempotency_key"`
	CorrelationID          string                        `json:"correlation_id"`
	CanonicalEntityID      string                        `json:"canonical_entity_id"`
	Nucleus                string                        `json:"nucleus"`
	OfferCandidate         string                        `json:"offer_candidate"`
	SourceAsset            string                        `json:"source_asset"`
	CityClass              string                        `json:"city_class"`
	Urgency                string                        `json:"urgency"`
	WhyNow                 string                        `json:"why_now"`
	Person                 NetNewInboundParty            `json:"person"`
	Company                NetNewInboundParty            `json:"company"`
	ProtectedContact       NetNewInboundProtectedContact `json:"protected_contact"`
	PreferredChannel       string                        `json:"preferred_channel"`
	QualificationState     string                        `json:"qualification_state"`
	Consent                NetNewInboundConsent          `json:"consent"`
	Conflict               NetNewInboundConflict         `json:"conflict"`
	SensitiveData          NetNewInboundSensitiveData    `json:"sensitive_data"`
	IntakeSource           string                        `json:"intake_source"`
	PartyKind              string                        `json:"party_kind"`
	DecisionRole           string                        `json:"decision_role"`
	WhyNowClass            string                        `json:"why_now_class"`
	DesiredDecision        string                        `json:"desired_decision_or_deliverable"`
	DocumentAvailability   string                        `json:"document_availability_class"`
	City                   string                        `json:"city"`
	UF                     string                        `json:"uf"`
	LocationMaterial       bool                          `json:"location_material"`
	PartnerRequired        bool                          `json:"partner_required"`
	CapacityReviewRequired bool                          `json:"capacity_review_required"`
	OutboundEligible       bool                          `json:"outbound_eligible"`
	AutoSend               bool                          `json:"auto_send"`
	DispatchAttempted      bool                          `json:"dispatch_attempted"`
	OccurredAt             *time.Time                    `json:"occurred_at"`
}

// NetNewInboundParty is a person or company on the envelope.
type NetNewInboundParty struct {
	CanonicalID string `json:"canonical_id"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	Phone       string `json:"phone"`
}

// NetNewInboundProtectedContact is accepted only on the authenticated HMAC
// body. It is persisted for the human action and never projected to metrics or
// the public receipt/readback.
type NetNewInboundProtectedContact struct {
	Name             string `json:"name"`
	Email            string `json:"email"`
	Phone            string `json:"phone"`
	WhatsApp         string `json:"whatsapp"`
	PreferredChannel string `json:"preferred_channel"`
}

// NetNewInboundConsent is observed opt-in. Absence is fail-closed.
type NetNewInboundConsent struct {
	Granted     bool       `json:"granted"`
	Source      string     `json:"source"`
	EvidenceRef string     `json:"evidence_ref"`
	At          *time.Time `json:"at"`
}

// NetNewInboundConflict is a protected reference only. Parts and corpus never
// travel with it into analytics.
type NetNewInboundConflict struct {
	Status string `json:"status"`
	Ref    string `json:"ref"`
}

// NetNewInboundSensitiveData accepts both the legacy boolean and the official
// class/ref object without copying sensitive content into the intake.
type NetNewInboundSensitiveData struct {
	Present      bool   `json:"present"`
	Class        string `json:"class"`
	ProtectedRef string `json:"protected_ref"`
}

func (s *NetNewInboundSensitiveData) UnmarshalJSON(raw []byte) error {
	var present bool
	if err := json.Unmarshal(raw, &present); err == nil {
		s.Present = present
		return nil
	}
	type alias NetNewInboundSensitiveData
	var value alias
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	*s = NetNewInboundSensitiveData(value)
	return nil
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
		if v == NetNewInboundHandraiserSchema || strings.HasPrefix(v, NetNewInboundFamilyPrefix) || v == NetNewInboundContractID {
			return true
		}
	}
	var ids struct {
		ContractID string `json:"contract_id"`
		PolicyID   string `json:"policy_id"`
	}
	if err := json.Unmarshal(raw, &ids); err == nil {
		for _, v := range []string{ids.ContractID, ids.PolicyID} {
			v = strings.TrimSpace(v)
			if v == NetNewInboundContractID || strings.HasPrefix(v, NetNewInboundFamilyPrefix) {
				return true
			}
		}
	}
	return false
}

// ParseNetNewInboundEnvelope decodes without admitting.
func ParseNetNewInboundEnvelope(raw []byte) (NetNewInboundEnvelope, error) {
	var env NetNewInboundEnvelope
	decodeRaw := raw
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err == nil {
		if source := top["source"]; len(source) > 0 && source[0] == '{' {
			delete(top, "source")
			decodeRaw, _ = json.Marshal(top)
		}
	}
	if err := json.Unmarshal(decodeRaw, &env); err != nil {
		return NetNewInboundEnvelope{}, err
	}
	var extra struct {
		SchemaVersion    string `json:"schema_version"`
		CanonicalName    string `json:"canonical_name"`
		Origin           string `json:"origin"`
		AcquisitionLane  string `json:"acquisition_lane"`
		IntakeSource     string `json:"intake_source"`
		NucleusID        string `json:"nucleus_id"`
		OfferCandidateID string `json:"offer_candidate_id"`
		Qualification    string `json:"qualification_state"`
		Email            string `json:"email"`
		Name             string `json:"name"`
		Phone            string `json:"phone"`
		PreferredChannel string `json:"preferred_channel"`
		Company          string `json:"company"`
		LandingAsset     struct {
			ID string `json:"id"`
		} `json:"landing_asset"`
		SiteLocation struct {
			Material bool   `json:"material"`
			City     string `json:"city"`
			UF       string `json:"uf"`
			IBGE     string `json:"ibge_municipality_code"`
		} `json:"site_location"`
		ContactEvidence struct {
			Channel string `json:"channel"`
		} `json:"contact_evidence"`
		ConsentEvidence struct {
			Captured    bool       `json:"captured"`
			Basis       string     `json:"basis"`
			EvidenceRef string     `json:"evidence_ref"`
			At          *time.Time `json:"at"`
			CapturedAt  *time.Time `json:"captured_at"`
		} `json:"consent_evidence"`
		ConflictScreening struct {
			Status       string `json:"status"`
			ProtectedRef string `json:"protected_ref"`
		} `json:"conflict_screening"`
		ProtectedPayload struct {
			Contact          NetNewInboundProtectedContact `json:"contact"`
			Name             string                        `json:"name"`
			Email            string                        `json:"email"`
			Phone            string                        `json:"phone"`
			WhatsApp         string                        `json:"whatsapp"`
			PreferredChannel string                        `json:"preferred_channel"`
		} `json:"protected_payload"`
		SourceObject struct {
			System string `json:"system"`
		} `json:"source"`
	}
	_ = json.Unmarshal(raw, &extra)
	if strings.TrimSpace(env.Schema) == "" {
		env.Schema = firstNonEmpty(extra.CanonicalName, extra.SchemaVersion)
	}
	if strings.TrimSpace(env.Source) == "" {
		env.Source = firstNonEmpty(extra.Origin, extra.SourceObject.System)
	}
	if strings.TrimSpace(env.Lane) == "" {
		env.Lane = extra.AcquisitionLane
	}
	env.IntakeSource = firstNonEmpty(env.IntakeSource, extra.IntakeSource)
	env.Nucleus = firstNonEmpty(env.Nucleus, extra.NucleusID)
	env.OfferCandidate = firstNonEmpty(env.OfferCandidate, extra.OfferCandidateID)
	env.SourceAsset = firstNonEmpty(env.SourceAsset, extra.LandingAsset.ID)
	env.QualificationState = firstNonEmpty(env.QualificationState, extra.Qualification)
	if strings.TrimSpace(env.Conflict.Status) == "" {
		env.Conflict.Status = extra.ConflictScreening.Status
	}
	if strings.TrimSpace(env.Conflict.Ref) == "" {
		env.Conflict.Ref = extra.ConflictScreening.ProtectedRef
	}
	if !env.Consent.Granted && extra.ConsentEvidence.Captured {
		env.Consent.Granted = true
	}
	env.Consent.Source = firstNonEmpty(env.Consent.Source, extra.ConsentEvidence.Basis)
	env.Consent.EvidenceRef = firstNonEmpty(env.Consent.EvidenceRef, extra.ConsentEvidence.EvidenceRef)
	if env.Consent.At == nil {
		env.Consent.At = extra.ConsentEvidence.At
		if env.Consent.At == nil {
			env.Consent.At = extra.ConsentEvidence.CapturedAt
		}
	}
	protected := env.ProtectedContact
	if protected == (NetNewInboundProtectedContact{}) {
		protected = extra.ProtectedPayload.Contact
	}
	protected.Name = firstNonEmpty(protected.Name, extra.ProtectedPayload.Name)
	protected.Email = firstNonEmpty(protected.Email, extra.ProtectedPayload.Email)
	protected.Phone = firstNonEmpty(protected.Phone, extra.ProtectedPayload.Phone)
	protected.WhatsApp = firstNonEmpty(protected.WhatsApp, extra.ProtectedPayload.WhatsApp)
	protected.PreferredChannel = firstNonEmpty(protected.PreferredChannel, extra.ProtectedPayload.PreferredChannel)
	env.ProtectedContact = protected
	if strings.TrimSpace(env.Person.Email) == "" {
		env.Person.Email = firstNonEmpty(protected.Email, extra.Email)
	}
	if strings.TrimSpace(env.Person.Name) == "" {
		env.Person.Name = firstNonEmpty(protected.Name, extra.Name)
	}
	if strings.TrimSpace(env.Person.Phone) == "" {
		env.Person.Phone = firstNonEmpty(protected.Phone, protected.WhatsApp, extra.Phone)
	}
	if strings.TrimSpace(env.Company.Name) == "" && extra.Company != "" {
		env.Company.Name = extra.Company
	}
	env.PreferredChannel = firstNonEmpty(env.PreferredChannel, protected.PreferredChannel, extra.PreferredChannel, extra.ContactEvidence.Channel)
	env.City = firstNonEmpty(env.City, extra.SiteLocation.City)
	env.UF = firstNonEmpty(env.UF, extra.SiteLocation.UF)
	env.LocationMaterial = env.LocationMaterial || extra.SiteLocation.Material
	if env.CityClass == "" && extra.SiteLocation.Material {
		env.CityClass = strings.Join(filterEmpty([]string{env.City, strings.ToUpper(env.UF)}), "/")
	}
	env.LogicalID = firstNonEmpty(strings.TrimSpace(env.LogicalID), strings.TrimSpace(env.EventID), strings.TrimSpace(env.IdempotencyKey))
	env.ContractID = firstNonEmpty(strings.TrimSpace(env.ContractID), strings.TrimSpace(env.PolicyID))
	env.Version = firstNonEmpty(strings.TrimSpace(env.Version), strings.TrimSpace(env.PolicyVersion))
	env.ContentHash = firstNonEmpty(strings.TrimSpace(env.ContentHash), strings.TrimSpace(env.SchemaHash), strings.TrimSpace(env.PolicyHash))
	return env, nil
}

func netNewEnvelopeContractID(env NetNewInboundEnvelope) string {
	return firstNonEmpty(strings.TrimSpace(env.ContractID), strings.TrimSpace(env.PolicyID), strings.TrimSpace(env.Schema), strings.TrimSpace(env.Policy))
}

func netNewEnvelopeVersion(env NetNewInboundEnvelope) string {
	schema := strings.TrimSpace(env.Schema)
	if strings.HasPrefix(schema, NetNewInboundFamilyPrefix) {
		return schema
	}
	return firstNonEmpty(strings.TrimSpace(env.Version), strings.TrimSpace(env.PolicyVersion), schema)
}

func netNewEnvelopeContentHash(env NetNewInboundEnvelope) string {
	return normalizeContentHash(firstNonEmpty(env.ContentHash, env.SchemaHash, env.PolicyHash))
}

// DecideNetNewInbound is the fail-closed admission function. It never persists
// and never talks to SMTP. pin must be contract_id + version + content hash;
// an unpinned runtime pin is never ACCEPTED.
func DecideNetNewInbound(env NetNewInboundEnvelope, pin InboundAuthorityPin) NetNewInboundDecision {
	if !pin.Pinned() {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonHashUnpinned}
	}
	contractID := netNewEnvelopeContractID(env)
	version := netNewEnvelopeVersion(env)
	if !pin.acceptsContractID(contractID) {
		if strings.HasPrefix(contractID, NetNewInboundFamilyPrefix) || contractID == NetNewInboundContractID {
			return NetNewInboundDecision{Outcome: NetNewInboundOutcomeUnknown, Reason: NetNewInboundReasonSchemaUnknown}
		}
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonContractMismatch}
	}
	if !pin.acceptsVersion(version) {
		if strings.HasPrefix(version, NetNewInboundFamilyPrefix) || strings.HasPrefix(contractID, NetNewInboundFamilyPrefix) || contractID == NetNewInboundContractID {
			return NetNewInboundDecision{Outcome: NetNewInboundOutcomeUnknown, Reason: NetNewInboundReasonSchemaUnknown}
		}
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonSchemaMismatch}
	}
	if strings.TrimSpace(env.LogicalID) == "" {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonLogicalID}
	}
	hash := netNewEnvelopeContentHash(env)
	if hash == "" {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonHashUnpinned}
	}
	if hash != normalizeContentHash(pin.ContentHash) {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonHashMismatch}
	}
	if pol := strings.TrimSpace(env.Policy); pol != "" && !pin.acceptsVersion(pol) && pol != NetNewInboundHandraiserSchema {
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
	if intakeSource := strings.ToUpper(strings.TrimSpace(env.IntakeSource)); intakeSource != "" && intakeSource != NetNewInboundSource {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonSource}
	}
	if !netNewLaneOK(env.Lane) {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonLane}
	}
	if !netNewNucleusOK(env.Nucleus) {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonNucleus}
	}
	if !netNewOfferCandidateOK(env.OfferCandidate) {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonOfferCandidate}
	}
	if !netNewSourceAssetOK(env.SourceAsset) {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonSourceAsset}
	}
	if env.OutboundEligible {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonOutboundClaim}
	}
	if env.AutoSend || env.DispatchAttempted {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonAutoSendClaim}
	}
	if !env.Consent.Granted || strings.TrimSpace(env.Consent.Source) == "" ||
		((env.Consent.At == nil || env.Consent.At.IsZero()) && strings.TrimSpace(env.Consent.EvidenceRef) == "") {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonConsent}
	}
	channel := netNewPreferredChannel(env)
	if channel == "" {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonPreferredChannel}
	}
	switch channel {
	case NetNewInboundPreferredEmail:
		if normalizeWebIntentEmail(env.Person.Email) == "" {
			return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonContact}
		}
	case NetNewInboundPreferredWhatsApp:
		if normalizeNetNewPhone(env.Person.Phone) == "" {
			return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonContact}
		}
	}
	switch strings.ToUpper(strings.TrimSpace(env.Conflict.Status)) {
	case "", netNewInboundConflictNone, netNewInboundConflictClear:
	case netNewInboundConflictDecline:
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonConflictDecline}
	case netNewInboundConflictHit:
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonConflictHit}
	case netNewInboundConflictUnknown, netNewInboundConflictNotScreened:
	default:
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeUnknown, Reason: NetNewInboundReasonConflictUnknown}
	}
	return NetNewInboundDecision{Outcome: NetNewInboundOutcomeAccepted}
}

// NetNewAdmissionDigest is a canonical digest over exactly the fields
// DecideNetNewInbound reads. It is stable across JSON key ordering and
// cosmetic re-serialization, and changes if and only if the admission-relevant
// material changes. It is the idempotency-key binding: a reused logical_id
// carrying different admission material is a conflict, not a replay.
func NetNewAdmissionDigest(env NetNewInboundEnvelope) string {
	consentAt := ""
	if env.Consent.At != nil {
		consentAt = env.Consent.At.UTC().Format(time.RFC3339)
	}
	parts := []string{
		"contract_id=" + netNewEnvelopeContractID(env),
		"version=" + netNewEnvelopeVersion(env),
		"content_hash=" + netNewEnvelopeContentHash(env),
		"policy=" + strings.TrimSpace(env.Policy),
		"intake_schema=" + strings.TrimSpace(env.IntakeSchema),
		"logical_id=" + netNewLogicalID(env),
		"source=" + strings.ToUpper(strings.TrimSpace(env.Source)),
		"lane=" + strings.TrimSpace(env.Lane),
		"intent_kind=" + strings.ToUpper(strings.TrimSpace(env.IntentKind)),
		"nucleus=" + strings.TrimSpace(env.Nucleus),
		"offer_candidate=" + strings.TrimSpace(env.OfferCandidate),
		"source_asset=" + strings.TrimSpace(env.SourceAsset),
		"preferred_channel=" + netNewPreferredChannel(env),
		"contact_email=" + normalizeWebIntentEmail(env.Person.Email),
		"contact_phone=" + normalizeNetNewPhone(env.Person.Phone),
		"qualification_state=" + NetNewQualificationState(env),
		"outbound_eligible=" + strconv.FormatBool(env.OutboundEligible),
		"auto_send=" + strconv.FormatBool(env.AutoSend),
		"dispatch_attempted=" + strconv.FormatBool(env.DispatchAttempted),
		"consent_granted=" + strconv.FormatBool(env.Consent.Granted),
		"consent_source=" + strings.TrimSpace(env.Consent.Source),
		"consent_evidence_ref=" + strings.TrimSpace(env.Consent.EvidenceRef),
		"consent_at=" + consentAt,
		"conflict_status=" + strings.ToUpper(strings.TrimSpace(env.Conflict.Status)),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

func netNewLaneOK(lane string) bool {
	switch strings.ToUpper(strings.TrimSpace(lane)) {
	case NetNewInboundLane, "NET_NEW_INBOUND":
		return true
	default:
		return false
	}
}

func netNewOfferCandidateOK(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		value = NetNewInboundOfferCandidate
	}
	for _, admitted := range NetNewInboundOfferCandidates {
		if value == admitted {
			return true
		}
	}
	return false
}

func netNewSourceAssetOK(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		value = NetNewInboundSourceAsset
	}
	for _, admitted := range NetNewInboundSourceAssets {
		if value == admitted {
			return true
		}
	}
	return false
}

func normalizeNetNewPhone(value string) string {
	result := whatsapp.NormalizePhone(value, "BR")
	if !result.Valid {
		return ""
	}
	return result.E164
}

func netNewPreferredChannel(env NetNewInboundEnvelope) string {
	switch strings.ToUpper(strings.TrimSpace(env.PreferredChannel)) {
	case NetNewInboundPreferredEmail, "E-MAIL":
		return NetNewInboundPreferredEmail
	case NetNewInboundPreferredWhatsApp, "PHONE", "TELEFONE":
		return NetNewInboundPreferredWhatsApp
	case "":
		hasEmail := normalizeWebIntentEmail(env.Person.Email) != ""
		hasPhone := normalizeNetNewPhone(env.Person.Phone) != ""
		if hasEmail != hasPhone {
			if hasEmail {
				return NetNewInboundPreferredEmail
			}
			return NetNewInboundPreferredWhatsApp
		}
	}
	return ""
}

// NetNewQualificationState derives the safe intake state. An unknown conflict
// stays admitted for human review but never becomes CLEAR. The fallback nucleus
// always needs context, including when it has not been screened yet.
func NetNewQualificationState(env NetNewInboundEnvelope) string {
	if strings.TrimSpace(env.Nucleus) == "other_technical_need" {
		return NetNewInboundQualificationNeedsContext
	}
	conflict := strings.ToUpper(strings.TrimSpace(env.Conflict.Status))
	if conflict == netNewInboundConflictUnknown || conflict == netNewInboundConflictNotScreened {
		return NetNewInboundQualificationConflictCheckRequired
	}
	if env.PartnerRequired {
		return NetNewInboundQualificationPartnerRequired
	}
	if env.CapacityReviewRequired {
		return NetNewInboundQualificationCapacityReview
	}
	if strings.EqualFold(strings.TrimSpace(env.DocumentAvailability), "GAP") {
		return NetNewInboundQualificationDocumentGap
	}
	if strings.EqualFold(strings.TrimSpace(env.DecisionRole), "UNKNOWN") ||
		strings.EqualFold(strings.TrimSpace(env.Urgency), "UNKNOWN") ||
		strings.EqualFold(strings.TrimSpace(env.WhyNowClass), "UNKNOWN") ||
		strings.EqualFold(strings.TrimSpace(env.DocumentAvailability), "UNKNOWN") {
		return NetNewInboundQualificationNeedsContext
	}
	if conflict == netNewInboundConflictClear &&
		strings.EqualFold(strings.TrimSpace(env.DocumentAvailability), "COMPLETE") &&
		strings.EqualFold(strings.TrimSpace(env.DecisionRole), "DECISION_MAKER") {
		return NetNewInboundQualificationQCO
	}
	requested := strings.ToUpper(strings.TrimSpace(env.QualificationState))
	switch requested {
	case NetNewInboundQualificationNeedsContext,
		NetNewInboundQualificationPotentialFit,
		NetNewInboundQualificationConflictCheckRequired,
		NetNewInboundQualificationDocumentGap,
		NetNewInboundQualificationCapacityReview,
		NetNewInboundQualificationPartnerRequired,
		NetNewInboundQualificationOutOfScope,
		NetNewInboundQualificationQCO:
		return requested
	default:
		return NetNewInboundQualificationPotentialFit
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
