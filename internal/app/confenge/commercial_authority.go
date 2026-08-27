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

	ReasonAuthorityAbsent            = "commercial_authority_absent"
	ReasonAuthorityBindingMismatch   = "commercial_authority_binding_mismatch"
	ReasonAuthorityExpired           = "commercial_authority_expired"
	ReasonAuthorityUnknownState      = "commercial_authority_state_unknown"
	ReasonAuthorityValidUntilMissing = "commercial_authority_valid_until_missing"
	ReasonNewAdmissionFrozen         = "new_admission_frozen"
	ReasonBoundTransportForbidden    = "existing_bound_touch_transport_forbidden"
	ReasonPolicyUnknown              = "policy_version_unknown"
	ReasonPolicyHold                 = "policy_version_hold"
	ReasonSourceHealthStale          = "source_health_stale"
	ReasonSourceHealthDegraded       = "source_health_degraded"
	ReasonSourceHealthMissing        = "source_health_missing"
)

// FeedCommercialAuthority is the extra-cli payload. Every field is optional at
// the JSON layer so a missing object stays fail-closed.
type FeedCommercialAuthority struct {
	SchemaVersion                      string   `json:"schema_version,omitempty"`
	BasisSourceRunID                   string   `json:"basis_source_run_id,omitempty"`
	BasisSnapshotHash                  string   `json:"basis_snapshot_hash,omitempty"`
	BasisMembershipHash                string   `json:"basis_membership_hash,omitempty"`
	ValidatedAt                        string   `json:"validated_at,omitempty"`
	ValidUntil                         string   `json:"valid_until,omitempty"`
	State                              string   `json:"state,omitempty"`
	NewAdmissionAllowed                *bool    `json:"new_admission_allowed,omitempty"`
	ExistingBoundTouchTransportAllowed *bool    `json:"existing_bound_touch_transport_allowed,omitempty"`
	ReasonCodes                        []string `json:"reason_codes,omitempty"`
}

// CommercialAuthorityBinding is the live feed identity the payload must close
// against. A new membership never inherits an old approval by similarity.
type CommercialAuthorityBinding struct {
	SourceRunID    string
	SnapshotHash   string
	MembershipHash string
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
	return strings.TrimSpace(p.State) != "" ||
		strings.TrimSpace(p.BasisSourceRunID) != "" ||
		strings.TrimSpace(p.BasisSnapshotHash) != "" ||
		strings.TrimSpace(p.BasisMembershipHash) != "" ||
		strings.TrimSpace(p.ValidUntil) != "" ||
		strings.TrimSpace(p.SchemaVersion) != "" ||
		p.NewAdmissionAllowed != nil ||
		p.ExistingBoundTouchTransportAllowed != nil
}

// EvaluateCommercialAuthority is a pure function of the attested payload, the
// live binding, and an explicit now. Tests inject the clock.
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
	out.Present = true
	out.SchemaVersion = strings.TrimSpace(payload.SchemaVersion)
	out.BasisSourceRunID = strings.TrimSpace(payload.BasisSourceRunID)
	out.BasisSnapshotHash = strings.TrimSpace(payload.BasisSnapshotHash)
	out.BasisMembershipHash = strings.ToLower(strings.TrimSpace(payload.BasisMembershipHash))
	out.ReasonCodes = append([]string{}, payload.ReasonCodes...)

	if validated, err := parseFreshnessTime(payload.ValidatedAt); err == nil {
		t := validated.UTC()
		out.ValidatedAt = &t
	}
	if strings.TrimSpace(payload.ValidUntil) == "" {
		out.State = CommercialAuthorityUnknown
		out.ReasonCodes = appendUnique(out.ReasonCodes, ReasonAuthorityValidUntilMissing)
		return out
	}
	until, err := parseFreshnessTime(payload.ValidUntil)
	if err != nil {
		out.State = CommercialAuthorityUnknown
		out.ReasonCodes = appendUnique(out.ReasonCodes, ReasonAuthorityValidUntilMissing)
		return out
	}
	u := until.UTC()
	out.ValidUntil = &u

	bindRun := strings.TrimSpace(binding.SourceRunID)
	bindSnap := strings.TrimSpace(binding.SnapshotHash)
	bindMem := strings.ToLower(strings.TrimSpace(binding.MembershipHash))
	if out.BasisSourceRunID == "" || out.BasisSnapshotHash == "" || out.BasisMembershipHash == "" ||
		bindRun == "" || bindSnap == "" || bindMem == "" ||
		out.BasisSourceRunID != bindRun || out.BasisSnapshotHash != bindSnap || out.BasisMembershipHash != bindMem {
		out.State = CommercialAuthorityUnknown
		out.ReasonCodes = appendUnique(out.ReasonCodes, ReasonAuthorityBindingMismatch)
		return out
	}

	state := strings.ToUpper(strings.TrimSpace(payload.State))
	if !now.Before(u) || state == CommercialAuthorityExpired {
		out.State = CommercialAuthorityExpired
		out.ReasonCodes = appendUnique(out.ReasonCodes, ReasonAuthorityExpired)
		return out
	}

	switch state {
	case CommercialAuthorityCurrent, CommercialAuthorityDegraded:
		out.State = state
		out.NewAdmissionAllowed = boolVal(payload.NewAdmissionAllowed)
		out.ExistingBoundTouchTransportAllowed = boolVal(payload.ExistingBoundTouchTransportAllowed)
		if !out.NewAdmissionAllowed {
			out.ReasonCodes = appendUnique(out.ReasonCodes, ReasonNewAdmissionFrozen)
		}
		if !out.ExistingBoundTouchTransportAllowed {
			out.ReasonCodes = appendUnique(out.ReasonCodes, ReasonBoundTransportForbidden)
		}
	case CommercialAuthorityFrozenForNewAdmission:
		out.State = CommercialAuthorityFrozenForNewAdmission
		out.NewAdmissionAllowed = false
		out.ExistingBoundTouchTransportAllowed = boolVal(payload.ExistingBoundTouchTransportAllowed)
		out.ReasonCodes = appendUnique(out.ReasonCodes, ReasonNewAdmissionFrozen)
		if !out.ExistingBoundTouchTransportAllowed {
			out.ReasonCodes = appendUnique(out.ReasonCodes, ReasonBoundTransportForbidden)
		}
	case CommercialAuthorityUnknown, "":
		out.State = CommercialAuthorityUnknown
		out.ReasonCodes = appendUnique(out.ReasonCodes, ReasonAuthorityUnknownState)
	default:
		out.State = CommercialAuthorityUnknown
		out.ReasonCodes = appendUnique(out.ReasonCodes, ReasonAuthorityUnknownState)
	}
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
		// Current fail-closed path: source must be FRESH and the producer
		// contract must still validate. Source DEGRADED/STALE is not a
		// deactivation, but it also does not authorize new work.
		if err := ValidateAuthoritativeSourceFreshness(source, now); err != nil {
			out.HoldReasons = appendUnique(out.HoldReasons, ReasonAuthorityAbsent)
			if out.SourceHealth.State == SourceHealthStale {
				out.HoldReasons = appendUnique(out.HoldReasons, ReasonSourceHealthStale)
			}
			if out.SourceHealth.State == SourceHealthDegraded {
				out.HoldReasons = appendUnique(out.HoldReasons, ReasonSourceHealthDegraded)
			}
			if out.SourceHealth.State == SourceHealthMissing {
				out.HoldReasons = appendUnique(out.HoldReasons, ReasonSourceHealthMissing)
			}
		} else if len(liveGateHold) == 0 {
			out.AllowNewAdmission = true
			out.AllowExistingBoundTouchTransport = true
		}
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
	case DelegatedFirstTouchPolicyV1:
		return true, false, ""
	case DelegatedFirstTouchPolicyV2:
		return true, true, ReasonPolicyHold
	case "":
		return false, true, ReasonPolicyUnknown
	default:
		return false, true, ReasonPolicyUnknown
	}
}
