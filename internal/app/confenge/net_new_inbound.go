package confenge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
	NetNewInboundDecisionSchema       = "net-new-inbound-handraiser-admission.1.0.0-draft.20260904"
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
	NetNewInboundReasonOptOut           = "opt_out_present"
	NetNewInboundReasonConflictDecline  = "conflict_decline"
	NetNewInboundReasonConflictHit      = "conflict_hit"
	NetNewInboundReasonConflictUnknown  = "conflict_unknown"
	NetNewInboundReasonContact          = "contact_channel_missing"
	NetNewInboundReasonContactUnknown   = "contact_evidence_unknown"
	NetNewInboundReasonFuzzyIdentity    = "fuzzy_identity_forbidden"
	NetNewInboundReasonIntent           = "intent_kind_not_admitted"
	NetNewInboundReasonLocation         = "location_not_minimized"
	NetNewInboundReasonSensitiveContent = "sensitive_content_forbidden"
	NetNewInboundReasonConflictCoercion = "conflict_clear_coercion_forbidden"
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
	NetNewInboundReasonRequestInvalid   = "request_invalid"
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
	NetNewInboundPreferredPhone    = "PHONE"
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
	netNewProvRequestReceipt = "request_receipt:"
	netNewProvOrigin         = "origin:"
	netNewProvLane           = "acquisition_lane:"
	netNewProvIntent         = "intent_kind:"
	netNewProvIntakeSource   = "intake_source:"
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
	RequestSchemaVersion    string                        `json:"schema_version"`
	Schema                  string                        `json:"schema"`
	ContractID              string                        `json:"contract_id"`
	PolicyID                string                        `json:"policy_id"`
	Version                 string                        `json:"version"`
	PolicyVersion           string                        `json:"policy_version"`
	ContentHash             string                        `json:"content_hash"`
	Hash                    string                        `json:"hash"`
	SchemaHash              string                        `json:"schema_hash"`
	Policy                  string                        `json:"policy"`
	PolicyHash              string                        `json:"policy_hash"`
	GovernanceSourceSHA     string                        `json:"governance_source_sha"`
	IntakeSchema            string                        `json:"intake_schema"`
	Taxonomy                string                        `json:"taxonomy"`
	Catalog                 string                        `json:"catalog"`
	Source                  string                        `json:"source"`
	ProducerSystem          string                        `json:"-"`
	Lane                    string                        `json:"lane"`
	IntentKind              string                        `json:"intent_kind"`
	LogicalID               string                        `json:"logical_id"`
	EventID                 string                        `json:"event_id"`
	IdempotencyKey          string                        `json:"idempotency_key"`
	CorrelationID           string                        `json:"correlation_id"`
	ReceiptID               string                        `json:"receipt_id"`
	SubjectRef              string                        `json:"subject_ref"`
	AccountRef              string                        `json:"account_ref"`
	OptOut                  bool                          `json:"opt_out"`
	CanonicalEntityID       string                        `json:"canonical_entity_id"`
	Nucleus                 string                        `json:"nucleus"`
	OfferCandidate          string                        `json:"offer_candidate"`
	SourceAsset             string                        `json:"source_asset"`
	CityClass               string                        `json:"city_class"`
	Urgency                 string                        `json:"urgency"`
	WhyNow                  string                        `json:"why_now"`
	Person                  NetNewInboundParty            `json:"person"`
	Company                 NetNewInboundParty            `json:"company"`
	ProtectedContact        NetNewInboundProtectedContact `json:"protected_contact"`
	ContactEvidence         NetNewInboundContactEvidence  `json:"contact_evidence"`
	PreferredChannel        string                        `json:"preferred_channel"`
	QualificationState      string                        `json:"qualification_state"`
	Consent                 NetNewInboundConsent          `json:"consent"`
	ConsentEvidence         NetNewInboundConsentEvidence  `json:"consent_evidence"`
	Conflict                NetNewInboundConflict         `json:"conflict"`
	SensitiveData           NetNewInboundSensitiveData    `json:"sensitive_data"`
	IntakeSource            string                        `json:"intake_source"`
	PartyKind               string                        `json:"party_kind"`
	DecisionRole            string                        `json:"decision_role"`
	WhyNowClass             string                        `json:"why_now_class"`
	DesiredDecision         string                        `json:"desired_decision_or_deliverable"`
	DocumentAvailability    string                        `json:"document_availability_class"`
	City                    string                        `json:"city"`
	UF                      string                        `json:"uf"`
	LocationMaterial        bool                          `json:"location_material"`
	PartnerRequired         bool                          `json:"partner_required"`
	CapacityReviewRequired  bool                          `json:"capacity_review_required"`
	OutboundEligible        bool                          `json:"outbound_eligible"`
	AutoSend                bool                          `json:"auto_send"`
	DispatchAttempted       bool                          `json:"dispatch_attempted"`
	OccurredAt              *time.Time                    `json:"occurred_at"`
	RequestDigest           string                        `json:"-"`
	FinalContractClaim      bool                          `json:"-"`
	FinalContractConformant bool                          `json:"-"`
	FinalPreOutcome         string                        `json:"-"`
	FinalPreReason          string                        `json:"-"`
}

// NetNewInboundParty is a person or company on the envelope.
type NetNewInboundParty struct {
	CanonicalID string `json:"canonical_id"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	Phone       string `json:"phone"`
	WhatsApp    string `json:"whatsapp"`
}

// NetNewInboundProtectedContact is accepted only on the authenticated HMAC
// body. It is persisted for the human action and never projected to metrics or
// the public receipt/readback.
type NetNewInboundProtectedContact struct {
	Name             string `json:"name"`
	Organization     string `json:"organization"`
	Email            string `json:"email"`
	Phone            string `json:"phone"`
	WhatsApp         string `json:"whatsapp"`
	PreferredChannel string `json:"preferred_channel"`
}

// NetNewInboundContactEvidence is the Governance admission material supplied
// by the producer. Warmbly validates its shape but does not redefine the
// Governance allowlists that give it policy meaning.
type NetNewInboundContactEvidence struct {
	Present             bool   `json:"present"`
	Channel             string `json:"channel"`
	EvidenceRef         string `json:"evidence_ref"`
	IdentityMatchMethod string `json:"identity_match_method"`
}

// NetNewInboundConsentEvidence is the corresponding Governance admission
// material. Protected contact values remain separate from this evidence.
type NetNewInboundConsentEvidence struct {
	Captured    bool   `json:"captured"`
	Basis       string `json:"basis"`
	EvidenceRef string `json:"evidence_ref"`
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
	canonical, _, canonicalErr := decodeCanonicalNetNewJSON(raw)
	if canonicalErr != nil {
		return strings.Contains(string(raw), NetNewInboundContractID)
	}
	raw = canonical
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
	canonical, decoded, err := decodeCanonicalNetNewJSON(raw)
	if err != nil {
		return NetNewInboundEnvelope{}, err
	}
	topAny, ok := decoded.(map[string]any)
	if !ok {
		return NetNewInboundEnvelope{}, fmt.Errorf("net-new inbound body must be a JSON object")
	}
	decodeRaw := canonical
	var top map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &top); err == nil {
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
			Organization     string                        `json:"organization"`
			Email            string                        `json:"email"`
			Phone            string                        `json:"phone"`
			WhatsApp         string                        `json:"whatsapp"`
			PreferredChannel string                        `json:"preferred_channel"`
		} `json:"protected_payload"`
		SourceObject struct {
			System string `json:"system"`
		} `json:"source"`
	}
	_ = json.Unmarshal(canonical, &extra)
	if strings.TrimSpace(env.Schema) == "" {
		env.Schema = firstNonEmpty(extra.CanonicalName, extra.SchemaVersion)
	}
	if strings.TrimSpace(env.Source) == "" {
		env.Source = firstNonEmpty(extra.Origin, extra.SourceObject.System)
	}
	env.ProducerSystem = strings.TrimSpace(extra.SourceObject.System)
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
	protected.Organization = firstNonEmpty(protected.Organization, extra.ProtectedPayload.Organization)
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
		env.Person.Phone = firstNonEmpty(protected.Phone, extra.Phone)
	}
	if strings.TrimSpace(env.Person.WhatsApp) == "" {
		env.Person.WhatsApp = protected.WhatsApp
	}
	if strings.TrimSpace(env.Company.Name) == "" {
		env.Company.Name = firstNonEmpty(protected.Organization, extra.Company)
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
	env.ContentHash = firstNonEmpty(strings.TrimSpace(env.ContentHash), strings.TrimSpace(env.Hash), strings.TrimSpace(env.SchemaHash), strings.TrimSpace(env.PolicyHash))
	env.FinalContractClaim = normalizeContentHash(env.ContentHash) == GovernanceInboundPolicyHash
	if env.FinalContractClaim {
		// Under the merged Governance pin, the published request fields are the
		// sole admission authority. Compatibility aliases may be transported by
		// web-cfg, but can neither select the ledger key nor override policy input.
		env.Schema = ""
		env.Policy = ""
		env.ContractID = stringField(topAny, "policy_id")
		env.PolicyID = env.ContractID
		env.Version = stringField(topAny, "policy_version")
		env.PolicyVersion = env.Version
		env.Source = stringField(topAny, "origin")
		env.Lane = stringField(topAny, "acquisition_lane")
		env.LogicalID = stringField(topAny, "idempotency_key")
		env.IdempotencyKey = env.LogicalID
		env.IntakeSource = stringField(topAny, "intake_source")
		env.Nucleus = stringField(topAny, "nucleus_id")
		env.OfferCandidate = stringField(topAny, "offer_candidate_id")
		if asset, ok := objectField(topAny, "landing_asset"); ok {
			env.SourceAsset = stringField(asset, "id")
		}
		if conflict, ok := objectField(topAny, "conflict_screening"); ok {
			env.Conflict.Status = stringField(conflict, "status")
			env.Conflict.Ref, _ = optionalStringField(conflict, "protected_ref")
		}
		env.Consent.Granted = extra.ConsentEvidence.Captured
		env.Consent.Source = extra.ConsentEvidence.Basis
		env.Consent.EvidenceRef = extra.ConsentEvidence.EvidenceRef
		env.PreferredChannel = extra.ContactEvidence.Channel
		env.City = extra.SiteLocation.City
		env.UF = extra.SiteLocation.UF
		env.LocationMaterial = extra.SiteLocation.Material
		env.FinalPreOutcome, env.FinalPreReason = governanceDraftSafetyDisposition(topAny)
	}
	sum := sha256.Sum256(canonical)
	env.RequestDigest = hex.EncodeToString(sum[:])
	env.FinalContractConformant = !env.FinalContractClaim || governanceDraftRequestConformant(topAny)
	return env, nil
}

// netNewGovernanceDraftConformanceSnapshotID binds this structural/safety
// snapshot to the exact published Governance authority. A new policy hash or
// source SHA requires a new reviewed snapshot; these sets are not a parallel,
// independently evolving policy.
const netNewGovernanceDraftConformanceSnapshotID = "405ac86064a90641b843352d21cd21703744115de9592558e100671d92276df7@0074722ce66f16af06dd4799ee88064ea8a12fc1"

func governanceDraftRequestConformant(payload map[string]any) bool {
	if netNewGovernanceDraftConformanceSnapshotID != GovernanceInboundPolicyHash+"@"+GovernanceInboundSourceSHA {
		return false
	}
	if payload == nil || stringField(payload, "schema_version") != "net-new-inbound-handraiser-request.1.0.0-draft.20260904" {
		return false
	}
	for _, key := range []string{
		"policy_id", "policy_version", "canonical_name", "origin", "acquisition_lane", "intent_kind", "idempotency_key", "correlation_id", "receipt_id",
		"intake_source", "nucleus_id", "offer_candidate_id", "party_kind", "decision_role", "urgency",
		"why_now_class", "desired_decision_or_deliverable", "document_availability_class",
	} {
		if stringField(payload, key) == "" {
			return false
		}
	}
	if !stringIn(stringField(payload, "party_kind"), "PERSON", "COMPANY") ||
		!stringIn(stringField(payload, "decision_role"), "DECISION_MAKER", "INFLUENCER", "OPERATOR", "UNKNOWN") ||
		!stringIn(stringField(payload, "urgency"), "IMMEDIATE", "THIS_CYCLE", "EXPLORATORY", "UNKNOWN") ||
		!stringIn(stringField(payload, "why_now_class"), "DEADLINE", "INCIDENT", "AUDIT", "PROCUREMENT", "CAPACITY", "UNKNOWN") ||
		!stringIn(stringField(payload, "desired_decision_or_deliverable"), "ASSESSMENT", "OPINION", "DOCUMENT_SET", "SITE_VISIT", "UNKNOWN") ||
		!stringIn(stringField(payload, "document_availability_class"), "COMPLETE", "PARTIAL", "GAP", "UNKNOWN") {
		return false
	}
	for _, key := range []string{"idempotency_key", "correlation_id", "receipt_id"} {
		if normalizeNetNewOpaqueRef(stringField(payload, key)) == "" {
			return false
		}
	}
	contact, ok := objectField(payload, "contact_evidence")
	if !ok || triBoolField(contact, "present") < 0 || stringField(contact, "channel") == "" ||
		normalizeNetNewOpaqueRef(stringField(contact, "evidence_ref")) == "" || stringField(contact, "identity_match_method") == "" {
		return false
	}
	if !onlyFoldedKeys(contact, "present", "channel", "evidence_ref", "identity_match_method") ||
		!stringIn(stringField(contact, "channel"), "EMAIL", "PHONE", "WHATSAPP") ||
		!stringIn(stringField(contact, "identity_match_method"),
			"EXPLICIT_CONTACT", "EXPLICIT_SUBJECT_REF", "NAME", "DISPLAY_NAME", "FUZZY", "FUZZY_NAME", "FUZZY_IDENTITY") {
		return false
	}
	consent, ok := objectField(payload, "consent_evidence")
	if !ok || triBoolField(consent, "captured") < 0 || stringField(consent, "basis") == "" ||
		normalizeNetNewOpaqueRef(stringField(consent, "evidence_ref")) == "" {
		return false
	}
	if !onlyFoldedKeys(consent, "captured", "basis", "evidence_ref") ||
		!stringIn(stringField(consent, "basis"), "EXPLICIT_FORM_SUBMIT", "EXPLICIT_HUMAN_REVIEW_REQUEST") {
		return false
	}
	asset, ok := objectField(payload, "landing_asset")
	if !ok || stringField(asset, "id") == "" || stringField(asset, "kind") == "" {
		return false
	}
	if !onlyFoldedKeys(asset, "id", "kind") || normalizeNetNewOpaqueRef(stringField(asset, "id")) == "" ||
		normalizeNetNewOpaqueRef(stringField(asset, "kind")) == "" {
		return false
	}
	location, ok := objectField(payload, "site_location")
	if !ok || triBoolField(location, "material") < 0 || !onlyFoldedKeys(location, "material", "city", "uf", "ibge_municipality_code") {
		return false
	}
	city := stringField(location, "city")
	uf := stringField(location, "uf")
	ibge := stringField(location, "ibge_municipality_code")
	if triBoolField(location, "material") == 0 && (city != "" || uf != "" || ibge != "") {
		return false
	}
	if triBoolField(location, "material") == 1 && (!netNewCityMinimized(city) || !netNewUFValid(uf) || (ibge != "" && !netNewIBGEValid(ibge))) {
		return false
	}
	sensitive, ok := objectField(payload, "sensitive_data")
	if !ok || triBoolField(sensitive, "present") < 0 || stringField(sensitive, "class") == "" ||
		!onlyFoldedKeys(sensitive, "present", "class", "protected_ref") ||
		!stringIn(stringField(sensitive, "class"), "NONE", "LGPD_SPECIAL", "LEGAL_PRIVILEGE", "HEALTH", "UNKNOWN") {
		return false
	}
	if triBoolField(sensitive, "present") == 1 && strings.EqualFold(stringField(sensitive, "class"), "NONE") {
		return false
	}
	if !optionalOpaqueRefFieldValid(sensitive, "protected_ref", true) {
		return false
	}
	conflict, ok := objectField(payload, "conflict_screening")
	if !ok || stringField(conflict, "status") == "" ||
		!onlyFoldedKeys(conflict, "status", "protected_ref", "claimed_clear") ||
		// DECLINE is the documented legacy spelling canonicalized by Governance
		// to HIT. Let it reach the explicit rejection below; never treat it as an
		// unknown status or as clearance.
		!stringIn(stringField(conflict, "status"), "CLEAR", "UNKNOWN", "HIT", "DECLINE", "NOT_SCREENED") {
		return false
	}
	if _, exists := conflict["claimed_clear"]; exists && triBoolField(conflict, "claimed_clear") < 0 {
		return false
	}
	if claimed := triBoolField(conflict, "claimed_clear"); claimed == 1 && !strings.EqualFold(stringField(conflict, "status"), "CLEAR") {
		return false
	}
	if !optionalOpaqueRefFieldValid(conflict, "protected_ref", true) {
		return false
	}
	for _, key := range []string{"subject_ref", "account_ref"} {
		if ref, present := optionalStringField(payload, key); present && (ref == "" || normalizeNetNewOpaqueRef(ref) == "") {
			return false
		}
	}
	subjectRef, _ := optionalStringField(payload, "subject_ref")
	accountRef, _ := optionalStringField(payload, "account_ref")
	if subjectRef != "" && accountRef != "" && subjectRef != accountRef {
		return false
	}
	if strings.EqualFold(stringField(contact, "identity_match_method"), "EXPLICIT_SUBJECT_REF") && subjectRef == "" {
		return false
	}
	if _, exists := payload["opt_out"]; exists && triBoolField(payload, "opt_out") < 0 {
		return false
	}
	if protected, exists := payload["protected_contact"]; exists {
		protectedMap, ok := protected.(map[string]any)
		if !ok || !onlyFoldedKeys(protectedMap, "name", "organization", "email", "phone", "whatsapp", "preferred_channel") {
			return false
		}
		if channel, present := optionalStringField(protectedMap, "preferred_channel"); present && channel != stringField(contact, "channel") {
			return false
		}
	}
	if channel, present := optionalStringField(payload, "preferred_channel"); present && channel != stringField(contact, "channel") {
		return false
	}
	// web-cfg currently transports a small compatibility envelope around the
	// official Governance request. If present, those aliases must agree; they
	// are never allowed to become a second admission authority.
	for _, pair := range [][2]string{
		{"logical_id", "idempotency_key"},
		{"event_id", "idempotency_key"},
		{"nucleus", "nucleus_id"},
		{"offer_candidate", "offer_candidate_id"},
		{"source_asset", "landing_asset.id"},
		{"why_now", "why_now_class"},
	} {
		if !optionalAliasMatches(payload, pair[0], pair[1]) {
			return false
		}
	}
	if legacyConflict, exists := payload["conflict"]; exists {
		legacy, ok := legacyConflict.(map[string]any)
		if !ok || stringField(legacy, "status") != stringField(conflict, "status") {
			return false
		}
		legacyRef, _ := optionalStringField(legacy, "ref")
		officialRef, _ := optionalStringField(conflict, "protected_ref")
		if legacyRef != officialRef {
			return false
		}
	}
	return true
}

func governanceDraftSafetyDisposition(payload map[string]any) (string, string) {
	if canonical := stringField(payload, "canonical_name"); canonical != "" && canonical != NetNewInboundHandraiserSchema {
		return NetNewInboundOutcomeRejected, NetNewInboundReasonSchemaMismatch
	}
	if location, ok := objectField(payload, "site_location"); ok {
		if !onlyFoldedKeys(location, "material", "city", "uf", "ibge_municipality_code") {
			return NetNewInboundOutcomeRejected, NetNewInboundReasonLocation
		}
		city := stringField(location, "city")
		uf := stringField(location, "uf")
		ibge := stringField(location, "ibge_municipality_code")
		if triBoolField(location, "material") == 0 && (city != "" || uf != "" || ibge != "") {
			return NetNewInboundOutcomeRejected, NetNewInboundReasonLocation
		}
		if triBoolField(location, "material") == 1 && city != "" && uf != "" &&
			(!netNewCityMinimized(city) || !netNewUFValid(uf) || (ibge != "" && !netNewIBGEValid(ibge))) {
			return NetNewInboundOutcomeRejected, NetNewInboundReasonLocation
		}
	}
	if sensitive, ok := objectField(payload, "sensitive_data"); ok {
		if !onlyFoldedKeys(sensitive, "present", "class", "protected_ref") {
			return NetNewInboundOutcomeRejected, NetNewInboundReasonSensitiveContent
		}
		if ref, present := optionalStringField(sensitive, "protected_ref"); present && ref != "" && normalizeNetNewOpaqueRef(ref) == "" {
			return NetNewInboundOutcomeRejected, NetNewInboundReasonSensitiveContent
		}
	}
	if conflict, ok := objectField(payload, "conflict_screening"); ok {
		status := strings.ToUpper(stringField(conflict, "status"))
		if triBoolField(conflict, "claimed_clear") == 1 && status != netNewInboundConflictClear {
			return NetNewInboundOutcomeRejected, NetNewInboundReasonConflictCoercion
		}
	}
	return "", ""
}

func optionalAliasMatches(payload map[string]any, alias, officialPath string) bool {
	aliasValue, present := optionalStringField(payload, alias)
	if !present {
		return true
	}
	var officialValue string
	parts := strings.Split(officialPath, ".")
	if len(parts) == 1 {
		officialValue = stringField(payload, parts[0])
	} else if object, ok := objectField(payload, parts[0]); ok {
		officialValue = stringField(object, parts[1])
	}
	return aliasValue != "" && aliasValue == officialValue
}

func stringInFold(value string, allowed ...string) bool {
	for _, item := range allowed {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(item)) {
			return true
		}
	}
	return false
}

func stringIn(value string, allowed ...string) bool {
	value = strings.TrimSpace(value)
	for _, item := range allowed {
		if value == strings.TrimSpace(item) {
			return true
		}
	}
	return false
}

func onlyFoldedKeys(payload map[string]any, allowed ...string) bool {
	for key := range payload {
		if !stringInFold(key, allowed...) {
			return false
		}
	}
	return true
}

func netNewCityMinimized(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 80 || strings.ContainsAny(value, "0123456789@,;:/\\#|<>[]{}()\"=+*&%$!?\t\r\n") {
		return false
	}
	return true
}

func netNewUFValid(value string) bool {
	return len(value) == 2 && value == strings.ToUpper(value) && value[0] >= 'A' && value[0] <= 'Z' && value[1] >= 'A' && value[1] <= 'Z'
}

func netNewIBGEValid(value string) bool {
	if len(value) != 7 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func stringField(payload map[string]any, key string) string {
	value, ok := payload[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func optionalStringField(payload map[string]any, key string) (string, bool) {
	value, exists := payload[key]
	if !exists || value == nil {
		return "", false
	}
	text, ok := value.(string)
	if !ok {
		return "", true
	}
	return strings.TrimSpace(text), true
}

func optionalOpaqueRefFieldValid(payload map[string]any, key string, allowEmpty bool) bool {
	value, exists := payload[key]
	if !exists || value == nil {
		return true
	}
	text, ok := value.(string)
	if !ok {
		return false
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return allowEmpty
	}
	return normalizeNetNewOpaqueRef(text) != ""
}

func objectField(payload map[string]any, key string) (map[string]any, bool) {
	value, ok := payload[key].(map[string]any)
	return value, ok
}

// boolField returns 1 for true, 0 for false and -1 for missing/wrong type.
func triBoolField(payload map[string]any, key string) int {
	value, ok := payload[key].(bool)
	if !ok {
		return -1
	}
	if value {
		return 1
	}
	return 0
}

// decodeCanonicalNetNewJSON rejects duplicate (including case-variant) keys
// and returns a stable whole-request representation. This is the exactly-once
// material: unknown fields and protected values are intentionally included.
func decodeCanonicalNetNewJSON(raw []byte) ([]byte, any, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	value, err := decodeUniqueNetNewJSONValue(dec)
	if err != nil {
		return nil, nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return nil, nil, fmt.Errorf("net-new inbound body contains trailing JSON")
		}
		return nil, nil, err
	}
	canonical, err := marshalCanonicalNetNewJSON(value)
	if err != nil {
		return nil, nil, err
	}
	return canonical, value, nil
}

func marshalCanonicalNetNewJSON(value any) ([]byte, error) {
	var out strings.Builder
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return nil, err
	}
	return []byte(strings.TrimSuffix(out.String(), "\n")), nil
}

func decodeUniqueNetNewJSONValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, isDelim := tok.(json.Delim)
	if !isDelim {
		return tok, nil
	}
	switch delim {
	case '{':
		out := make(map[string]any)
		seen := make(map[string]struct{})
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("net-new inbound object key is not a string")
			}
			folded := strings.ToLower(key)
			if _, duplicate := seen[folded]; duplicate {
				return nil, fmt.Errorf("net-new inbound body has duplicate key %q", key)
			}
			seen[folded] = struct{}{}
			item, err := decodeUniqueNetNewJSONValue(dec)
			if err != nil {
				return nil, err
			}
			out[key] = item
		}
		if end, err := dec.Token(); err != nil || end != json.Delim('}') {
			return nil, fmt.Errorf("invalid net-new inbound object")
		}
		return out, nil
	case '[':
		out := make([]any, 0)
		for dec.More() {
			item, err := decodeUniqueNetNewJSONValue(dec)
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
		if end, err := dec.Token(); err != nil || end != json.Delim(']') {
			return nil, fmt.Errorf("invalid net-new inbound array")
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unexpected net-new inbound delimiter %q", delim)
	}
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
		if env.FinalContractClaim {
			if strings.HasPrefix(contractID, "CFG-FIRST-TOUCH-ROUTING") || contractID == "ACQUISITION_PRESSURE" {
				return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonSchemaMismatch}
			}
			return NetNewInboundDecision{Outcome: NetNewInboundOutcomeUnknown, Reason: NetNewInboundReasonContractMismatch}
		}
		if strings.HasPrefix(contractID, NetNewInboundFamilyPrefix) || contractID == NetNewInboundContractID {
			return NetNewInboundDecision{Outcome: NetNewInboundOutcomeUnknown, Reason: NetNewInboundReasonSchemaUnknown}
		}
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonContractMismatch}
	}
	if !pin.acceptsVersion(version) {
		if env.FinalContractClaim && (version == "v1" || version == "NET_NEW_INBOUND_HANDRAISER/v1") {
			return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonSchemaMismatch}
		}
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
	if !netNewEnvelopeHashesConsistent(env) {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonHashMismatch}
	}
	if env.FinalPreOutcome != "" {
		return NetNewInboundDecision{Outcome: env.FinalPreOutcome, Reason: env.FinalPreReason}
	}
	if env.FinalContractClaim && !env.FinalContractConformant {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeUnknown, Reason: NetNewInboundReasonRequestInvalid}
	}
	if env.FinalContractClaim && strings.TrimSpace(pin.SourceSHA) != "" && strings.TrimSpace(env.GovernanceSourceSHA) != strings.TrimSpace(pin.SourceSHA) {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonHashMismatch}
	}
	if env.FinalContractClaim {
		if !env.ContactEvidence.Present {
			return NetNewInboundDecision{Outcome: NetNewInboundOutcomeUnknown, Reason: NetNewInboundReasonContactUnknown}
		}
		switch strings.TrimSpace(env.ContactEvidence.IdentityMatchMethod) {
		case "NAME", "DISPLAY_NAME", "FUZZY", "FUZZY_NAME", "FUZZY_IDENTITY":
			return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonFuzzyIdentity}
		}
		if !env.ConsentEvidence.Captured {
			return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonConsent}
		}
	}
	if pol := strings.TrimSpace(env.Policy); pol != "" && !pin.acceptsVersion(pol) && pol != NetNewInboundHandraiserSchema {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonSchemaMismatch}
	}
	// The final Governance pin owns its request conformance. intake_schema is
	// producer coordination metadata (currently web-cfg 2.1), not a Warmbly
	// admission authority. The legacy test adapter retains its old exact check.
	if intake := strings.TrimSpace(env.IntakeSchema); !env.FinalContractClaim && intake != "" && intake != NetNewInboundIntakeSchema {
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
	switch strings.ToUpper(strings.TrimSpace(env.IntentKind)) {
	case "HUMAN_REVIEW", "DEEP_DIVE", "REQUEST_HUMAN_REVIEW", "REQUEST_DEEP_DIVE":
	case "":
		if env.FinalContractClaim {
			return NetNewInboundDecision{Outcome: NetNewInboundOutcomeUnknown, Reason: NetNewInboundReasonIntent}
		}
	default:
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonIntent}
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
	if env.OptOut {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonOptOut}
	}
	if !env.Consent.Granted || strings.TrimSpace(env.Consent.Source) == "" ||
		((env.Consent.At == nil || env.Consent.At.IsZero()) && strings.TrimSpace(env.Consent.EvidenceRef) == "") {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonConsent}
	}
	channel := netNewPreferredChannel(env)
	if channel == "" {
		return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonPreferredChannel}
	}
	if env.FinalContractClaim {
		evidenceChannel := netNewChannelName(env.ContactEvidence.Channel)
		if evidenceChannel == "" || evidenceChannel != channel {
			return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonPreferredChannel}
		}
		if !env.ConsentEvidence.Captured {
			return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonConsent}
		}
	}
	switch channel {
	case NetNewInboundPreferredEmail:
		if netNewSelectedEmail(env) == "" {
			return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonContact}
		}
	case NetNewInboundPreferredPhone:
		if netNewSelectedPhone(env) == "" {
			return NetNewInboundDecision{Outcome: NetNewInboundOutcomeRejected, Reason: NetNewInboundReasonContact}
		}
	case NetNewInboundPreferredWhatsApp:
		if netNewSelectedWhatsApp(env) == "" {
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

// NetNewAdmissionDigest binds exactly-once behavior to the entire canonical
// request JSON, including unknown fields and protected values. Key ordering and
// whitespace do not affect it; any material value change does.
func NetNewAdmissionDigest(env NetNewInboundEnvelope) string {
	if env.RequestDigest != "" {
		return env.RequestDigest
	}
	fallback, _ := json.Marshal(env)
	sum := sha256.Sum256(fallback)
	return hex.EncodeToString(sum[:])
}

func netNewEnvelopeHashesConsistent(env NetNewInboundEnvelope) bool {
	want := ""
	for _, value := range []string{env.ContentHash, env.Hash, env.SchemaHash, env.PolicyHash} {
		value = normalizeContentHash(value)
		if value == "" {
			continue
		}
		if want == "" {
			want = value
			continue
		}
		if value != want {
			return false
		}
	}
	return true
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
	if channel := netNewChannelName(env.PreferredChannel); channel != "" {
		return channel
	}
	if strings.TrimSpace(env.PreferredChannel) != "" {
		return ""
	}
	switch strings.ToUpper(strings.TrimSpace(env.PreferredChannel)) {
	case "":
		available := ""
		count := 0
		for channel, present := range map[string]bool{
			NetNewInboundPreferredEmail:    netNewSelectedEmail(env) != "",
			NetNewInboundPreferredPhone:    netNewSelectedPhone(env) != "",
			NetNewInboundPreferredWhatsApp: normalizeNetNewPhone(env.Person.WhatsApp) != "",
		} {
			if present {
				available = channel
				count++
			}
		}
		if count == 1 {
			return available
		}
	}
	return ""
}

func netNewChannelName(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case NetNewInboundPreferredEmail, "E-MAIL":
		return NetNewInboundPreferredEmail
	case NetNewInboundPreferredWhatsApp:
		return NetNewInboundPreferredWhatsApp
	case NetNewInboundPreferredPhone, "TELEFONE":
		return NetNewInboundPreferredPhone
	default:
		return ""
	}
}

func netNewSelectedEmail(env NetNewInboundEnvelope) string {
	return normalizeWebIntentEmail(env.Person.Email)
}

func netNewSelectedPhone(env NetNewInboundEnvelope) string {
	return normalizeNetNewPhone(env.Person.Phone)
}

func netNewSelectedWhatsApp(env NetNewInboundEnvelope) string {
	if value := normalizeNetNewPhone(env.Person.WhatsApp); value != "" {
		return value
	}
	// web-cfg carries one protected `phone` slot and an explicit channel. It is
	// WhatsApp only when the producer explicitly labels it WHATSAPP.
	if strings.EqualFold(strings.TrimSpace(env.PreferredChannel), NetNewInboundPreferredWhatsApp) {
		return normalizeNetNewPhone(env.Person.Phone)
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
	if env.FinalContractClaim {
		// Governance derives this output from the admitted inputs. A producer's
		// qualification_state is readback metadata, never an admission input.
		return NetNewInboundQualificationPotentialFit
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
	return normalizeNetNewOpaqueRef(conflict.Ref)
}

func netNewCanonicalEntityID(env NetNewInboundEnvelope) string {
	return firstNonEmpty(
		strings.TrimSpace(env.CanonicalEntityID),
		strings.TrimSpace(env.Company.CanonicalID),
		strings.TrimSpace(env.Person.CanonicalID),
	)
}

func netNewLogicalID(env NetNewInboundEnvelope) string {
	return normalizeNetNewOpaqueRef(firstNonEmpty(env.LogicalID, env.EventID, env.IdempotencyKey))
}

func normalizeNetNewOpaqueRef(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return ""
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || strings.ContainsRune(":._-", r) {
			continue
		}
		return ""
	}
	return value
}

const netNewInboundReadbackSignaturePrefix = "GET\n/api/v1/webhooks/confenge/inbound/handraisers/"

// NetNewInboundReadbackHMACPayload is the canonical material signed for
// producer readback. Binding the method, route and logical ID prevents a fresh
// empty-body signature from being replayed against another receipt.
func NetNewInboundReadbackHMACPayload(logicalID string) []byte {
	logicalID = normalizeNetNewOpaqueRef(logicalID)
	if logicalID == "" {
		return nil
	}
	return []byte(netNewInboundReadbackSignaturePrefix + logicalID)
}
