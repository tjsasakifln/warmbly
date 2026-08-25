package confenge

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	ReleaseGO                            = "GO_FOR_CONTROLLED_PILOT"
	ReleaseNOGO                          = "NO_GO"
	ReleaseReadyForControlledEmailReview = "READY_FOR_CONTROLLED_EMAIL_GO_REVIEW"
	ReleaseGOForControlledEmailPilot     = "GO_FOR_CONTROLLED_EMAIL_PILOT"
)

// CheckState is a live evidence result. Empty and any value other than PASS
// are UNKNOWN. UNKNOWN is never treated as PASS.
type CheckState string

const (
	EvidenceUnknown CheckState = "UNKNOWN"
	EvidencePass    CheckState = "PASS"
	EvidenceFail    CheckState = "FAIL"
)

func (s CheckState) Normalized() CheckState {
	switch strings.ToUpper(strings.TrimSpace(string(s))) {
	case "PASS":
		return EvidencePass
	case "FAIL":
		return EvidenceFail
	case "UNKNOWN", "":
		return EvidenceUnknown
	default:
		return EvidenceUnknown
	}
}

func (s CheckState) IsPass() bool { return s.Normalized() == EvidencePass }

func (s CheckState) IsFail() bool { return s.Normalized() == EvidenceFail }

func (s CheckState) Label() string {
	switch s.Normalized() {
	case EvidencePass:
		return "PASS"
	case EvidenceFail:
		return "FAIL"
	default:
		return "UNKNOWN"
	}
}

func (s CheckState) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.Label())
}

func (s *CheckState) UnmarshalJSON(b []byte) error {
	if s == nil {
		return nil
	}
	raw := strings.TrimSpace(string(b))
	// JSON booleans cannot prove a live check. Missing evidence stays UNKNOWN.
	if raw == "null" || raw == "true" || raw == "false" || raw == "" {
		*s = EvidenceUnknown
		return nil
	}
	var str string
	if err := json.Unmarshal(b, &str); err != nil {
		*s = EvidenceUnknown
		return nil
	}
	*s = CheckState(str).Normalized()
	return nil
}

// ReleaseManifest binds the exact release a canary may use.
// Operational CheckState fields are live evidence: empty/UNKNOWN is not PASS.
type ReleaseManifest struct {
	RepositorySHA          string   `json:"repository_sha"`
	ImageDigests           []string `json:"image_digests,omitempty"`
	Schema                 string   `json:"schema"`
	FeedHash               string   `json:"feed_hash"`
	CohortHash             string   `json:"cohort_hash"`
	RecipientSetHash       string   `json:"recipient_set_hash,omitempty"`
	PolicyVersion          string   `json:"policy_version"`
	ComposerVersion        string   `json:"composer_version"`
	DoctrineVersion        string   `json:"doctrine_version"`
	RecipientPolicyVersion string   `json:"recipient_policy_version"`
	ApprovalsHash          string   `json:"approvals_hash"`
	CIResults              string   `json:"ci_results"`
	RuntimeResults         string   `json:"runtime_results"`
	HumanApprovals         int      `json:"human_approvals"`
	DelegatedApprovals     int      `json:"delegated_approvals,omitempty"`
	ApprovalDecisionSource string   `json:"approval_decision_source,omitempty"`
	ReadyCount             int      `json:"ready_count"`
	// KillSwitch is the canary-path "mechanism present" flag used by EvaluateRelease.
	// Controlled-email GO review uses KillSwitchOperational and SendingPaused instead.
	KillSwitch           bool       `json:"kill_switch"`
	AutoSend             bool       `json:"auto_send"`
	RequireHumanApproval bool       `json:"require_human_approval"`
	EvaluatedAt          time.Time  `json:"evaluated_at"`
	AllowedRouteClasses  []string   `json:"allowed_route_classes,omitempty"`
	VolumeCap            int        `json:"volume_cap,omitempty"`
	SMTPReady            CheckState `json:"smtp_ready"`
	ObservabilityReady   CheckState `json:"observability_ready"`
	ReplyIngestReady     CheckState `json:"reply_ingest_ready"`
	DispatchWiring       CheckState `json:"dispatch_wiring"`
	SenderProviderConfig CheckState `json:"sender_provider_config"`
	GreenAutorun         bool       `json:"green_autorun"`
	TTLValid             CheckState `json:"ttl_valid"`
	SuppressionClear     CheckState `json:"suppression_clear"`
	DBCohortAuthority    CheckState `json:"db_cohort_authority"`
	EvidenceVersion      string     `json:"evidence_version,omitempty"`
	// KillSwitchOperational is PASS only when the pause mechanism can be observed
	// (file present or confirmed absent). It is not "sending is paused".
	KillSwitchOperational CheckState `json:"kill_switch_operational"`
	// SendingPaused is the live pause bit when SendingPausedState is not UNKNOWN.
	SendingPaused bool `json:"sending_paused"`
	// SendingPausedState is PASS when sending is proven not paused, FAIL when
	// paused/engaged, UNKNOWN when the pause state could not be proven.
	SendingPausedState CheckState `json:"sending_paused_state"`
	// AutoSendState is PASS when auto-send is proven disabled.
	AutoSendState CheckState `json:"auto_send_state"`
	// GreenAutorunState is PASS when GREEN autorun is proven disabled.
	GreenAutorunState CheckState `json:"green_autorun_state"`
	// SuppressionReason is set when SuppressionClear is FAIL (e.g. post-freeze).
	SuppressionReason string `json:"suppression_reason,omitempty"`
}

// ReleaseVerdict is exactly GO or NO_GO. Never "quase GO".
type ReleaseVerdict struct {
	Verdict string   `json:"verdict"`
	Reasons []string `json:"reasons,omitempty"`
}

// EvaluateRelease is fail-closed. Exact human or delegated authority is required.
func EvaluateRelease(want, got ReleaseManifest) ReleaseVerdict {
	var reasons []string
	check := func(ok bool, code string) {
		if !ok {
			reasons = append(reasons, code)
		}
	}
	check(got.RepositorySHA != "" && got.RepositorySHA == want.RepositorySHA, "sha_drift")
	check(sameStringSet(want.ImageDigests, got.ImageDigests), "image_drift")
	check(got.Schema != "" && got.Schema == want.Schema, "schema_drift")
	check(got.FeedHash != "" && got.FeedHash == want.FeedHash, "feed_drift")
	check(got.CohortHash != "" && got.CohortHash == want.CohortHash, "cohort_drift")
	check(got.ComposerVersion == ComposerVersion, "composer_drift")
	check(got.DoctrineVersion == OutreachDoctrineVersion, "doctrine_drift")
	check(got.RecipientPolicyVersion == RecipientPolicyVersion, "recipient_policy_drift")
	check(got.ApprovalsHash != "" && got.ApprovalsHash == want.ApprovalsHash, "approvals_drift")
	check(strings.EqualFold(got.CIResults, "pass"), "ci_not_green")
	check(strings.EqualFold(got.RuntimeResults, "pass"), "runtime_not_green")
	check(got.KillSwitch, "kill_switch_off")
	check(!got.AutoSend, "auto_send_enabled")
	check(got.RequireHumanApproval, "human_approval_disabled")
	wantSource := strings.ToUpper(strings.TrimSpace(want.ApprovalDecisionSource))
	gotSource := strings.ToUpper(strings.TrimSpace(got.ApprovalDecisionSource))
	switch gotSource {
	case "", "HUMAN_APPROVE":
		check(wantSource == "" || wantSource == "HUMAN_APPROVE", "approval_source_drift")
		check(got.HumanApprovals > 0 && got.HumanApprovals == got.ReadyCount && got.ReadyCount > 0, "insufficient_human_approvals")
	case DelegatedFirstTouchApprovalDecision:
		check(wantSource == DelegatedFirstTouchApprovalDecision, "approval_source_drift")
		check(got.PolicyVersion == DelegatedFirstTouchPolicyV1 && got.PolicyVersion == want.PolicyVersion, "delegated_policy_drift")
		check(got.DelegatedApprovals > 0 && got.DelegatedApprovals == got.ReadyCount && got.ReadyCount > 0, "insufficient_delegated_approvals")
	default:
		check(false, "approval_source_unknown")
	}
	if len(reasons) > 0 {
		return ReleaseVerdict{Verdict: ReleaseNOGO, Reasons: reasons}
	}
	return ReleaseVerdict{Verdict: ReleaseGO}
}

// ReleaseCheck is one expected-vs-live comparison row for the operator preview.
type ReleaseCheck struct {
	Name     string     `json:"name"`
	Expected string     `json:"expected,omitempty"`
	Live     string     `json:"live,omitempty"`
	State    CheckState `json:"state"`
	Reason   string     `json:"reason,omitempty"`
}

// ReleaseComparison is the operator-facing GO-review preview.
type ReleaseComparison struct {
	AuthorizationID uuid.UUID       `json:"authorization_id"`
	Want            ReleaseManifest `json:"want"`
	Got             ReleaseManifest `json:"got"`
	Checks          []ReleaseCheck  `json:"checks"`
	Verdict         ReleaseVerdict  `json:"verdict"`
}

// EvaluateControlledEmailRelease is fail-closed. Every required live check
// must be PASS to emit GO_FOR_CONTROLLED_EMAIL_PILOT. Empty or partial
// evidence is NO_GO. UNKNOWN is never PASS.
func EvaluateControlledEmailRelease(want, got ReleaseManifest) ReleaseVerdict {
	cmp := CompareControlledEmailRelease(want, got)
	return cmp.Verdict
}

// CompareControlledEmailRelease compares live evidence to the frozen expected
// grant. UNKNOWN / not-checked is never PASS.
func CompareControlledEmailRelease(want, got ReleaseManifest) ReleaseComparison {
	var reasons []string
	var checks []ReleaseCheck
	add := func(ch ReleaseCheck, ok bool) {
		n := ch.State.Normalized()
		switch {
		case n == EvidencePass:
			ch.State = EvidencePass
		case n == EvidenceFail:
			ch.State = EvidenceFail
		case ch.State != "" || strings.EqualFold(strings.TrimSpace(ch.Live), "UNKNOWN"):
			// readyCheck already decided UNKNOWN. Never rewrite Live="UNKNOWN" to FAIL.
			ch.State = EvidenceUnknown
		case ok:
			ch.State = EvidencePass
		case strings.TrimSpace(ch.Live) == "":
			ch.State = EvidenceUnknown
		default:
			ch.State = EvidenceFail
		}
		if !ok && ch.Reason != "" {
			reasons = append(reasons, ch.Reason)
		}
		checks = append(checks, ch)
	}

	add(stringIdentityCheck("repository_sha", want.RepositorySHA, got.RepositorySHA, "repository_sha_missing", "sha_drift"))
	add(stringIdentityCheck("schema", want.Schema, got.Schema, "schema_missing", "schema_drift"))
	add(stringIdentityCheck("feed_hash", want.FeedHash, got.FeedHash, "feed_identity_missing", "feed_drift"))
	add(stringIdentityCheck("cohort_hash", want.CohortHash, got.CohortHash, "cohort_hash_missing", "cohort_hash_drift"))
	add(stringIdentityCheck("recipient_set_hash", want.RecipientSetHash, got.RecipientSetHash, "recipient_set_missing", "recipient_set_drift"))
	add(stringIdentityCheck("policy_version", want.PolicyVersion, got.PolicyVersion, "policy_version_missing", "policy_drift"))
	add(stringIdentityCheck("composer_version", want.ComposerVersion, got.ComposerVersion, "composer_version_missing", "composer_drift"))
	add(stringIdentityCheck("evidence_version", want.EvidenceVersion, got.EvidenceVersion, "evidence_version_missing", "evidence_drift"))

	routeOK := len(want.AllowedRouteClasses) > 0 && sameStringSet(want.AllowedRouteClasses, got.AllowedRouteClasses)
	routeReason := "route_class_drift"
	if len(got.AllowedRouteClasses) == 0 {
		routeReason = "route_class_missing"
		routeOK = false
	}
	add(ReleaseCheck{
		Name:     "route_classes",
		Expected: strings.Join(want.AllowedRouteClasses, ","),
		Live:     strings.Join(got.AllowedRouteClasses, ","),
		Reason:   routeReason,
	}, routeOK)

	volOK := got.VolumeCap > 0 && got.VolumeCap == want.VolumeCap
	volReason := "volume_cap_drift"
	if got.VolumeCap <= 0 {
		volReason = "volume_cap_missing"
	}
	add(ReleaseCheck{
		Name:     "volume_cap",
		Expected: itoa(want.VolumeCap),
		Live:     itoa(got.VolumeCap),
		Reason:   volReason,
	}, volOK)

	add(readyCheck("smtp_ready", got.SMTPReady, "smtp_readiness_not_proven", "smtp_not_ready"))
	add(readyCheck("reply_ingest_ready", got.ReplyIngestReady, "reply_ingest_not_proven", "reply_ingest_not_ready"))
	add(readyCheck("observability_ready", got.ObservabilityReady, "observability_not_proven", "observability_not_ready"))
	add(readyCheck("dispatch_wiring", got.DispatchWiring, "dispatch_wiring_not_proven", "dispatch_wiring_not_ready"))
	add(readyCheck("sender_provider_config", got.SenderProviderConfig, "sender_provider_not_proven", "sender_provider_missing"))
	add(readyCheck("db_cohort_authority", got.DBCohortAuthority, "db_cohort_authority_not_proven", "db_cohort_authority_missing"))
	suppFail := "suppression_not_clear"
	if strings.TrimSpace(got.SuppressionReason) != "" {
		suppFail = got.SuppressionReason
	}
	add(readyCheck("suppression_clear", got.SuppressionClear, "suppression_not_proven", suppFail))
	add(readyCheck("ttl_valid", got.TTLValid, "ttl_not_proven", "ttl_invalid"))
	add(readyCheck("kill_switch_available", got.KillSwitchOperational, "kill_switch_not_proven", "kill_switch_not_operational"))

	pausedOK := got.SendingPausedState.IsPass() && !got.SendingPaused
	pausedReason := "sending_paused"
	pausedState := got.SendingPausedState.Normalized()
	if pausedState != EvidencePass && pausedState != EvidenceFail {
		pausedReason = "kill_switch_state_unknown"
		pausedState = EvidenceUnknown
	} else if got.SendingPaused || pausedState == EvidenceFail {
		pausedReason = "sending_paused"
		pausedState = EvidenceFail
		pausedOK = false
	}
	add(ReleaseCheck{
		Name:   "sending_paused",
		Live:   boolLabel(got.SendingPaused),
		State:  pausedState,
		Reason: pausedReason,
	}, pausedOK)

	autoOK := got.AutoSendState.IsPass() && !got.AutoSend
	autoReason := "auto_send_enabled"
	autoState := got.AutoSendState.Normalized()
	if autoState != EvidencePass && autoState != EvidenceFail {
		autoReason = "auto_send_not_proven"
		autoState = EvidenceUnknown
	} else if got.AutoSend || autoState == EvidenceFail {
		autoReason = "auto_send_enabled"
		autoState = EvidenceFail
		autoOK = false
	}
	add(ReleaseCheck{
		Name:   "auto_send",
		Live:   boolLabel(got.AutoSend),
		State:  autoState,
		Reason: autoReason,
	}, autoOK)

	greenOK := got.GreenAutorunState.IsPass() && !got.GreenAutorun
	greenReason := "green_autorun_enabled"
	greenState := got.GreenAutorunState.Normalized()
	if greenState != EvidencePass && greenState != EvidenceFail {
		greenReason = "green_autorun_not_proven"
		greenState = EvidenceUnknown
	} else if got.GreenAutorun || greenState == EvidenceFail {
		greenReason = "green_autorun_enabled"
		greenState = EvidenceFail
		greenOK = false
	}
	add(ReleaseCheck{
		Name:   "green_autorun",
		Live:   boolLabel(got.GreenAutorun),
		State:  greenState,
		Reason: greenReason,
	}, greenOK)

	if containsRiskyClass(got.AllowedRouteClasses) || containsRiskyClass(want.AllowedRouteClasses) {
		reasons = append(reasons, "risky_in_allowed_classes")
		checks = append(checks, ReleaseCheck{
			Name:   "risky_class",
			Live:   "true",
			State:  EvidenceFail,
			Reason: "risky_in_allowed_classes",
		})
	}

	verdict := ReleaseVerdict{Verdict: ReleaseGOForControlledEmailPilot}
	if len(reasons) > 0 {
		verdict = ReleaseVerdict{Verdict: ReleaseNOGO, Reasons: reasons}
	}
	return ReleaseComparison{Want: want, Got: got, Checks: checks, Verdict: verdict}
}

func stringIdentityCheck(name, want, got, missing, drift string) (ReleaseCheck, bool) {
	ch := ReleaseCheck{Name: name, Expected: want, Live: got}
	if strings.TrimSpace(got) == "" {
		ch.State = EvidenceUnknown
		ch.Reason = missing
		return ch, false
	}
	if got != want || strings.TrimSpace(want) == "" {
		ch.State = EvidenceFail
		ch.Reason = drift
		return ch, false
	}
	ch.State = EvidencePass
	return ch, true
}

func readyCheck(name string, st CheckState, unknown, fail string) (ReleaseCheck, bool) {
	n := st.Normalized()
	ch := ReleaseCheck{Name: name, Live: n.Label(), State: n}
	switch n {
	case EvidencePass:
		return ch, true
	case EvidenceFail:
		ch.Reason = fail
		return ch, false
	default:
		ch.Reason = unknown
		return ch, false
	}
}

func boolLabel(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func containsRiskyClass(classes []string) bool {
	for _, c := range classes {
		if strings.EqualFold(strings.TrimSpace(c), RouteClassProbabilisticOrRisky) {
			return true
		}
	}
	return false
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	idx := map[string]int{}
	for _, s := range a {
		idx[s]++
	}
	for _, s := range b {
		idx[s]--
		if idx[s] < 0 {
			return false
		}
	}
	return true
}

// InvalidateOnDrift returns true when any bound hash moved after review.
func InvalidateOnDrift(approved, current ReleaseManifest) bool {
	v := EvaluateRelease(approved, current)
	return v.Verdict != ReleaseGO
}
