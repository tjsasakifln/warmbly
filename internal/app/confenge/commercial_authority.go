package confenge

import (
	"strings"
	"time"
)

// Commercial authority is an additive extra-cli contract. Absence keeps the
// current fail-closed path. Presence never collapses source health into
// "this lead ceased to exist".
const (
	CommercialAuthoritySchemaV1 = "confenge.commercial_authority.v1"

	CommercialAuthorityCurrent               = "CURRENT"
	CommercialAuthorityDegraded              = "DEGRADED"
	CommercialAuthorityFrozenForNewAdmission = "FROZEN_FOR_NEW_ADMISSION"
	CommercialAuthorityExpired               = "EXPIRED"
	CommercialAuthorityUnknown               = "UNKNOWN"
	CommercialAuthorityAbsent                = "ABSENT"

	SourceHealthFresh    = "FRESH"
	SourceHealthDegraded = "DEGRADED"
	SourceHealthStale    = "STALE"
	SourceHealthMissing  = "MISSING"

	TransportHealthActive  = TransportActive
	TransportHealthPaused  = TransportPaused
	TransportHealthUnknown = TransportUnknown

	DelegatedFirstTouchPolicyV2 = "CFG-FIRST-TOUCH-ROUTING-v2"
	// File bytes of Governance commercial/outbound/cfg-first-touch-routing.v2.json.
	DelegatedFirstTouchPolicyHashV2 = "f9d031f4239bb1e17beb714ae5f691b973f6c2147a55efa7e47ed09d6d905932"

	ReasonAuthorityAbsent             = "commercial_authority_absent"
	ReasonAuthorityBindingMismatch    = "commercial_authority_binding_mismatch"
	ReasonAuthorityExpired            = "commercial_authority_expired"
	ReasonAuthorityUnknownState       = "commercial_authority_state_unknown"
	ReasonAuthorityValidUntilMissing  = "commercial_authority_valid_until_missing"
	ReasonAuthorityValidatedAtMissing = "commercial_authority_validated_at_missing"
	ReasonAuthorityAliasConflict      = "commercial_authority_alias_conflict"
	ReasonNewAdmissionFrozen          = "new_admission_frozen"
	ReasonBoundTransportForbidden     = "existing_bound_touch_transport_forbidden"
	ReasonPolicyUnknown               = "policy_version_unknown"
	ReasonPolicyHold                  = "policy_version_hold"
	ReasonSourceHealthStale           = "source_health_stale"
	ReasonSourceHealthDegraded        = "source_health_degraded"
	ReasonSourceHealthMissing         = "source_health_missing"
)

// FeedCommercialAuthority is the extra-cli payload. Every field is optional at
// the JSON layer so a missing object stays fail-closed.
type FeedCommercialAuthority struct {
	Schema                             string                      `json:"schema,omitempty"`
	SchemaVersion                      string                      `json:"schema_version,omitempty"`
	ContractVersion                    string                      `json:"contract_version,omitempty"`
	PolicyVersion                      string                      `json:"policy_version,omitempty"`
	BasisSourceRunID                   string                      `json:"basis_source_run_id,omitempty"`
	BasisSnapshotHash                  string                      `json:"basis_snapshot_hash,omitempty"`
	BasisMembershipHash                string                      `json:"basis_membership_hash,omitempty"`
	BasisPublicationSemanticHash       string                      `json:"basis_publication_semantic_hash,omitempty"`
	ProducerIdentity                   string                      `json:"producer_identity,omitempty"`
	SourceRunIDAlias                   string                      `json:"source_run_id,omitempty"`
	SnapshotIDAlias                    string                      `json:"snapshot_id,omitempty"`
	MembershipHashAlias                string                      `json:"membership_hash,omitempty"`
	ValidatedAt                        string                      `json:"validated_at,omitempty"`
	ValidUntil                         string                      `json:"valid_until,omitempty"`
	State                              string                      `json:"state,omitempty"`
	NewAdmissionAllowed                *bool                       `json:"new_admission_allowed,omitempty"`
	ExistingBoundTouchTransportAllowed *bool                       `json:"existing_bound_touch_transport_allowed,omitempty"`
	ReasonCodes                        []string                    `json:"reason_codes,omitempty"`
	WindowsHours                       *CommercialAuthorityWindows `json:"windows_hours,omitempty"`
}

// CommercialAuthorityWindows is COMMERCIAL_AUTHORITY_POLICY/1.0. Defaults match extra-cli.
type CommercialAuthorityWindows struct {
	CurrentMaxHours  float64 `json:"current_max_hours,omitempty"`
	DegradedMaxHours float64 `json:"degraded_max_hours,omitempty"`
	FrozenMaxHours   float64 `json:"frozen_max_hours,omitempty"`
}

func (w *CommercialAuthorityWindows) durations() (current, degraded, frozen time.Duration) {
	currentH, degradedH, frozenH := 24.0, 72.0, 168.0
	if w != nil {
		if w.CurrentMaxHours > 0 {
			currentH = w.CurrentMaxHours
		}
		if w.DegradedMaxHours > 0 {
			degradedH = w.DegradedMaxHours
		}
		if w.FrozenMaxHours > 0 {
			frozenH = w.FrozenMaxHours
		}
	}
	return time.Duration(currentH * float64(time.Hour)),
		time.Duration(degradedH * float64(time.Hour)),
		time.Duration(frozenH * float64(time.Hour))
}

// NormalizeAliases copies lossless aliases into canonical fields. Disagreeing
// alias+canonical is a conflict; semantic hash and producer identity have no alias.
func (p *FeedCommercialAuthority) NormalizeAliases() (conflicts []string) {
	if p == nil {
		return nil
	}
	p.BasisSourceRunID, conflicts = mergeAlias(p.BasisSourceRunID, p.SourceRunIDAlias, "basis_source_run_id", conflicts)
	p.BasisSnapshotHash, conflicts = mergeAlias(p.BasisSnapshotHash, p.SnapshotIDAlias, "basis_snapshot_hash", conflicts)
	p.BasisMembershipHash, conflicts = mergeAlias(p.BasisMembershipHash, p.MembershipHashAlias, "basis_membership_hash", conflicts)
	return conflicts
}

func mergeAlias(canonical, alias, field string, conflicts []string) (string, []string) {
	c := strings.TrimSpace(canonical)
	a := strings.TrimSpace(alias)
	if c != "" && a != "" && c != a {
		return c, append(conflicts, field)
	}
	if c != "" {
		return c, conflicts
	}
	return a, conflicts
}

// CommercialAuthorityBinding is the live feed identity the payload must close
// against. A new membership never inherits an old approval by similarity.
type CommercialAuthorityBinding struct {
	SourceRunID             string
	SnapshotHash            string
	MembershipHash          string
	PublicationSemanticHash string
	ProducerIdentity        string
}

// CommercialAuthorityDecision is independent of source health and transport.
type CommercialAuthorityDecision struct {
	Present                            bool       `json:"present"`
	State                              string     `json:"state"`
	SchemaVersion                      string     `json:"schema_version,omitempty"`
	NewAdmissionAllowed                bool       `json:"new_admission_allowed"`
	ExistingBoundTouchTransportAllowed bool       `json:"existing_bound_touch_transport_allowed"`
	ValidUntil                         *time.Time `json:"valid_until,omitempty"`
	ValidatedAt                        *time.Time `json:"validated_at,omitempty"`
	BasisSourceRunID                   string     `json:"basis_source_run_id,omitempty"`
	BasisSnapshotHash                  string     `json:"basis_snapshot_hash,omitempty"`
	BasisMembershipHash                string     `json:"basis_membership_hash,omitempty"`
	BasisPublicationSemanticHash       string     `json:"basis_publication_semantic_hash,omitempty"`
	ProducerIdentity                   string     `json:"producer_identity,omitempty"`
	ReasonCodes                        []string   `json:"reason_codes,omitempty"`
}

// SourceHealthDecision classifies ingest/producer freshness without implying
// that a previously proven lead disappeared.
type SourceHealthDecision struct {
	State       string     `json:"state"`
	AsOf        *time.Time `json:"as_of,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	ReasonCodes []string   `json:"reason_codes,omitempty"`
}

// OutboundEligibility answers the three questions independently.
type OutboundEligibility struct {
	SourceHealth                     SourceHealthDecision        `json:"source_health"`
	CommercialAuthority              CommercialAuthorityDecision `json:"commercial_authority"`
	TransportHealth                  string                      `json:"transport_health"`
	TransportReasons                 []string                    `json:"transport_reasons,omitempty"`
	AllowNewAdmission                bool                        `json:"allow_new_admission"`
	AllowExistingBoundTouchTransport bool                        `json:"allow_existing_bound_touch_transport"`
	HoldReasons                      []string                    `json:"hold_reasons,omitempty"`
}

func boolVal(v *bool) bool { return v != nil && *v }

func authorityPresent(p *FeedCommercialAuthority) bool {
	if p == nil {
		return false
	}
	p.NormalizeAliases()
	return strings.TrimSpace(p.State) != "" ||
		strings.TrimSpace(p.BasisSourceRunID) != "" ||
		strings.TrimSpace(p.BasisSnapshotHash) != "" ||
		strings.TrimSpace(p.BasisMembershipHash) != "" ||
		strings.TrimSpace(p.BasisPublicationSemanticHash) != "" ||
		strings.TrimSpace(p.ProducerIdentity) != "" ||
		strings.TrimSpace(p.ValidUntil) != "" ||
		strings.TrimSpace(p.ValidatedAt) != "" ||
		strings.TrimSpace(p.SchemaVersion) != "" ||
		strings.TrimSpace(p.Schema) != "" ||
		p.NewAdmissionAllowed != nil ||
		p.ExistingBoundTouchTransportAllowed != nil
}

func classifyAuthorityAge(age time.Duration, windows *CommercialAuthorityWindows) string {
	current, degraded, frozen := windows.durations()
	if age <= current {
		return CommercialAuthorityCurrent
	}
	if age <= degraded {
		return CommercialAuthorityDegraded
	}
	if age <= frozen {
		return CommercialAuthorityFrozenForNewAdmission
	}
	return CommercialAuthorityExpired
}

func explicitRevocation(reasons []string) bool {
	for _, reason := range reasons {
		if strings.EqualFold(strings.TrimSpace(reason), "EXPLICIT_REVOCATION") {
			return true
		}
	}
	return false
}

func flagsForState(state string) (newAdmission, bound bool, reasons []string) {
	switch state {
	case CommercialAuthorityCurrent:
		return true, true, []string{"COMMERCIAL_AUTHORITY_CURRENT"}
	case CommercialAuthorityDegraded:
		return true, true, []string{"COMMERCIAL_AUTHORITY_DEGRADED", "NEW_ADMISSION_REQUIRES_VALID_EVIDENCE_AND_NO_DRIFT"}
	case CommercialAuthorityFrozenForNewAdmission:
		return false, true, []string{"COMMERCIAL_AUTHORITY_FROZEN_FOR_NEW_ADMISSION", "NEW_ADMISSION_FROZEN", "EXISTING_BOUND_TOUCH_MAY_CONTINUE"}
	case CommercialAuthorityExpired:
		return false, false, []string{"COMMERCIAL_AUTHORITY_EXPIRED", "ALL_NEW_TRANSPORT_EXPIRED"}
	default:
		return false, false, []string{"COMMERCIAL_AUTHORITY_UNKNOWN"}
	}
}

// EvaluateCommercialAuthority is a pure function of the attested payload, the
// live binding, and an explicit now. Tests inject the clock. Live state is
// recomputed from validated_at; a static snapshot state is never trusted forever.
func EvaluateCommercialAuthority(payload *FeedCommercialAuthority, binding CommercialAuthorityBinding, now time.Time) CommercialAuthorityDecision {
	out := CommercialAuthorityDecision{State: CommercialAuthorityAbsent}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if !authorityPresent(payload) {
		out.ReasonCodes = []string{ReasonAuthorityAbsent}
		return out
	}
	conflicts := payload.NormalizeAliases()
	out.Present = true
	out.SchemaVersion = firstNonEmpty(strings.TrimSpace(payload.Schema), strings.TrimSpace(payload.ContractVersion), strings.TrimSpace(payload.SchemaVersion))
	out.BasisSourceRunID = strings.TrimSpace(payload.BasisSourceRunID)
	out.BasisSnapshotHash = strings.TrimSpace(payload.BasisSnapshotHash)
	out.BasisMembershipHash = strings.ToLower(strings.TrimSpace(payload.BasisMembershipHash))
	out.BasisPublicationSemanticHash = strings.ToLower(strings.TrimSpace(payload.BasisPublicationSemanticHash))
	out.ProducerIdentity = strings.ToLower(strings.TrimSpace(payload.ProducerIdentity))
	out.ReasonCodes = append([]string{}, payload.ReasonCodes...)
	if len(conflicts) > 0 {
		out.State = CommercialAuthorityUnknown
		out.ReasonCodes = appendUnique(out.ReasonCodes, ReasonAuthorityAliasConflict)
		out.ReasonCodes = appendUnique(out.ReasonCodes, ReasonAuthorityBindingMismatch)
		return out
	}

	bindRun := strings.TrimSpace(binding.SourceRunID)
	bindSnap := strings.TrimSpace(binding.SnapshotHash)
	bindMem := strings.ToLower(strings.TrimSpace(binding.MembershipHash))
	bindSem := strings.ToLower(strings.TrimSpace(binding.PublicationSemanticHash))
	bindProd := strings.ToLower(strings.TrimSpace(binding.ProducerIdentity))
	if out.BasisSourceRunID == "" || out.BasisSnapshotHash == "" || out.BasisMembershipHash == "" ||
		out.BasisPublicationSemanticHash == "" || out.ProducerIdentity == "" ||
		bindRun == "" || bindSnap == "" || bindMem == "" || bindSem == "" || bindProd == "" ||
		out.BasisSourceRunID != bindRun || out.BasisSnapshotHash != bindSnap || out.BasisMembershipHash != bindMem ||
		out.BasisPublicationSemanticHash != bindSem || out.ProducerIdentity != bindProd {
		out.State = CommercialAuthorityUnknown
		out.ReasonCodes = appendUnique(out.ReasonCodes, ReasonAuthorityBindingMismatch)
		return out
	}

	if explicitRevocation(payload.ReasonCodes) {
		out.State = CommercialAuthorityExpired
		out.NewAdmissionAllowed = false
		out.ExistingBoundTouchTransportAllowed = false
		out.ReasonCodes = appendUnique(out.ReasonCodes, ReasonAuthorityExpired)
		return out
	}

	validated, err := parseFreshnessTime(payload.ValidatedAt)
	if err != nil {
		out.State = CommercialAuthorityUnknown
		out.ReasonCodes = appendUnique(out.ReasonCodes, ReasonAuthorityValidatedAtMissing)
		return out
	}
	t := validated.UTC()
	out.ValidatedAt = &t
	if t.After(now.Add(5 * time.Minute)) {
		out.State = CommercialAuthorityUnknown
		out.ReasonCodes = appendUnique(out.ReasonCodes, ReasonAuthorityUnknownState)
		return out
	}
	age := now.Sub(t)
	if age < 0 {
		age = 0
	}
	state := classifyAuthorityAge(age, payload.WindowsHours)
	out.State = state
	newAdmission, bound, reasons := flagsForState(state)
	out.NewAdmissionAllowed = newAdmission
	out.ExistingBoundTouchTransportAllowed = bound
	for _, reason := range reasons {
		out.ReasonCodes = appendUnique(out.ReasonCodes, reason)
	}
	current, degraded, frozen := payload.WindowsHours.durations()
	var until time.Time
	switch state {
	case CommercialAuthorityCurrent:
		until = t.Add(current)
	case CommercialAuthorityDegraded:
		until = t.Add(degraded)
	default:
		until = t.Add(frozen)
	}
	u := until.UTC()
	out.ValidUntil = &u
	return out
}

// ClassifySourceHealth never deactivates a lead. DEGRADED/STALE mean the
// producer/ingest window, not that a previously proven membership vanished.
func ClassifySourceHealth(f *FeedSourceFreshness, now time.Time, maxAge time.Duration) SourceHealthDecision {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if maxAge <= 0 {
		maxAge = 24 * time.Hour
	}
	if f == nil {
		return SourceHealthDecision{State: SourceHealthMissing, ReasonCodes: []string{ReasonSourceHealthMissing}}
	}
	status := strings.ToUpper(strings.TrimSpace(f.Status))
	out := SourceHealthDecision{ReasonCodes: append([]string{}, f.ReasonCodes...)}
	if asOf, err := parseFreshnessTime(f.AsOf); err == nil {
		t := asOf.UTC()
		out.AsOf = &t
	}
	if expires, err := parseFreshnessTime(f.ExpiresAt); err == nil {
		t := expires.UTC()
		out.ExpiresAt = &t
	}
	expired := out.ExpiresAt != nil && !now.Before(*out.ExpiresAt)
	staleByAge := out.AsOf != nil && (out.AsOf.After(now.Add(5*time.Minute)) || now.Sub(*out.AsOf) > maxAge)
	switch {
	case status == SourceHealthStale || expired || staleByAge:
		out.State = SourceHealthStale
		out.ReasonCodes = appendUnique(out.ReasonCodes, ReasonSourceHealthStale)
	case status == SourceHealthDegraded:
		out.State = SourceHealthDegraded
		out.ReasonCodes = appendUnique(out.ReasonCodes, ReasonSourceHealthDegraded)
	case status == SourceHealthFresh && !expired && !staleByAge:
		out.State = SourceHealthFresh
	case status == "":
		out.State = SourceHealthMissing
		out.ReasonCodes = appendUnique(out.ReasonCodes, ReasonSourceHealthMissing)
	default:
		out.State = SourceHealthStale
		out.ReasonCodes = appendUnique(out.ReasonCodes, ReasonSourceHealthStale)
	}
	return out
}

// EvaluateOutboundEligibility keeps the three questions separate. Explicit
// suppression, deactivation, recipient expiry, party-role conflict, and
// evidence expiry are supplied by the caller as liveGateHold; they always win.
func EvaluateOutboundEligibility(
	source *FeedSourceFreshness,
	payload *FeedCommercialAuthority,
	binding CommercialAuthorityBinding,
	transport TransportState,
	now time.Time,
	maxAge time.Duration,
	liveGateHold []string,
	boundExisting bool,
) OutboundEligibility {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	out := OutboundEligibility{
		SourceHealth:        ClassifySourceHealth(source, now, maxAge),
		CommercialAuthority: EvaluateCommercialAuthority(payload, binding, now),
		TransportHealth:     transport.State,
		TransportReasons:    append([]string{}, transport.Blockers...),
	}
	if !out.CommercialAuthority.Present {
		// Fail closed on the commercial fact itself. A FRESH crawler is NOT a
		// substitute for commercial authority and must never grant admission
		// or transport by fallback.
		out.HoldReasons = appendUnique(out.HoldReasons, ReasonQualificationMissing)
	} else {
		out.AllowNewAdmission = out.CommercialAuthority.NewAdmissionAllowed
		out.AllowExistingBoundTouchTransport = out.CommercialAuthority.ExistingBoundTouchTransportAllowed
		if !out.AllowNewAdmission {
			out.HoldReasons = appendUnique(out.HoldReasons, ReasonNewAdmissionFrozen)
		}
		if !out.AllowExistingBoundTouchTransport {
			out.HoldReasons = appendUnique(out.HoldReasons, ReasonBoundTransportForbidden)
		}
		// Source health is reported but never the sole reason a bound lead
		// "ceased to exist". It can still HOLD new admission when the
		// authority itself forbids it.
		_ = boundExisting
	}
	for _, reason := range liveGateHold {
		if strings.TrimSpace(reason) == "" {
			continue
		}
		out.AllowNewAdmission = false
		out.AllowExistingBoundTouchTransport = false
		out.HoldReasons = appendUnique(out.HoldReasons, reason)
	}
	// Pause and kill switch are transport facts. They do not un-prove a lead
	// and they do not, by themselves, block scheduling to QUEUED.
	return out
}

// RecognizeFirstTouchPolicy is exact. Partial names and fuzzy prefixes HOLD.
func RecognizeFirstTouchPolicy(policyID string) (known bool, hold bool, reason string) {
	switch strings.TrimSpace(policyID) {
	case DelegatedFirstTouchPolicyV1, DelegatedFirstTouchPolicyV2:
		return true, false, ""
	case "":
		return false, true, ReasonPolicyUnknown
	default:
		return false, true, ReasonPolicyUnknown
	}
}
