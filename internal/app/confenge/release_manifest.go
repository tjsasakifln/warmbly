package confenge

import (
	"strings"
	"time"
)

const (
	ReleaseGO                            = "GO_FOR_CONTROLLED_PILOT"
	ReleaseNOGO                          = "NO_GO"
	ReleaseReadyForControlledEmailReview = "READY_FOR_CONTROLLED_EMAIL_GO_REVIEW"
	ReleaseGOForControlledEmailPilot     = "GO_FOR_CONTROLLED_EMAIL_PILOT"
)

// ReleaseManifest binds the exact release a canary may use.
type ReleaseManifest struct {
	RepositorySHA          string    `json:"repository_sha"`
	ImageDigests           []string  `json:"image_digests,omitempty"`
	Schema                 string    `json:"schema"`
	FeedHash               string    `json:"feed_hash"`
	CohortHash             string    `json:"cohort_hash"`
	PolicyVersion          string    `json:"policy_version"`
	ComposerVersion        string    `json:"composer_version"`
	DoctrineVersion        string    `json:"doctrine_version"`
	RecipientPolicyVersion string    `json:"recipient_policy_version"`
	ApprovalsHash          string    `json:"approvals_hash"`
	CIResults              string    `json:"ci_results"`
	RuntimeResults         string    `json:"runtime_results"`
	HumanApprovals         int       `json:"human_approvals"`
	ReadyCount             int       `json:"ready_count"`
	KillSwitch             bool      `json:"kill_switch"`
	AutoSend               bool      `json:"auto_send"`
	RequireHumanApproval   bool      `json:"require_human_approval"`
	EvaluatedAt            time.Time `json:"evaluated_at"`
	AllowedRouteClasses    []string  `json:"allowed_route_classes,omitempty"`
	VolumeCap              int       `json:"volume_cap,omitempty"`
	SMTPReady              bool      `json:"smtp_ready,omitempty"`
	ObservabilityReady     bool      `json:"observability_ready,omitempty"`
	GreenAutorun           bool      `json:"green_autorun,omitempty"`
	TTLValid               bool      `json:"ttl_valid,omitempty"`
	SuppressionClear       bool      `json:"suppression_clear,omitempty"`
	DBCohortAuthority      bool      `json:"db_cohort_authority,omitempty"`
	EvidenceVersion        string    `json:"evidence_version,omitempty"`
}

// ReleaseVerdict is exactly GO or NO_GO. Never "quase GO".
type ReleaseVerdict struct {
	Verdict string   `json:"verdict"`
	Reasons []string `json:"reasons,omitempty"`
}

// EvaluateRelease is fail-closed. Missing human approvals or any drift is NO_GO.
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
	check(got.HumanApprovals > 0 && got.HumanApprovals == got.ReadyCount && got.ReadyCount > 0, "insufficient_human_approvals")
	if len(reasons) > 0 {
		return ReleaseVerdict{Verdict: ReleaseNOGO, Reasons: reasons}
	}
	return ReleaseVerdict{Verdict: ReleaseGO}
}

// EvaluateControlledEmailRelease never emits GO_FOR_CONTROLLED_EMAIL_PILOT.
// Matching frozen manifests yield READY_FOR_CONTROLLED_EMAIL_GO_REVIEW.
func EvaluateControlledEmailRelease(want, got ReleaseManifest) ReleaseVerdict {
	var reasons []string
	check := func(ok bool, code string) {
		if !ok {
			reasons = append(reasons, code)
		}
	}
	check(got.RepositorySHA != "" && got.RepositorySHA == want.RepositorySHA, "sha_drift")
	check(got.Schema != "" && got.Schema == want.Schema, "schema_drift")
	check(got.FeedHash != "" && got.FeedHash == want.FeedHash, "feed_drift")
	check(got.CohortHash != "" && got.CohortHash == want.CohortHash, "cohort_drift")
	check(got.PolicyVersion != "" && got.PolicyVersion == want.PolicyVersion, "policy_drift")
	check(sameStringSet(want.AllowedRouteClasses, got.AllowedRouteClasses), "route_class_drift")
	check(got.VolumeCap > 0 && got.VolumeCap == want.VolumeCap, "volume_cap_drift")
	check(got.ComposerVersion != "" && got.ComposerVersion == want.ComposerVersion, "composer_drift")
	check(got.KillSwitch, "kill_switch_off")
	check(!got.AutoSend, "auto_send_enabled")
	check(!got.GreenAutorun, "green_autorun_enabled")
	check(got.SMTPReady, "smtp_not_ready")
	check(got.ObservabilityReady, "observability_not_ready")
	check(got.TTLValid, "ttl_invalid")
	check(got.SuppressionClear, "suppression_not_clear")
	check(got.DBCohortAuthority, "db_cohort_authority_missing")
	check(got.EvidenceVersion != "" && got.EvidenceVersion == want.EvidenceVersion, "evidence_drift")
	if containsRiskyClass(got.AllowedRouteClasses) {
		reasons = append(reasons, "risky_in_allowed_classes")
	}
	if len(reasons) > 0 {
		return ReleaseVerdict{Verdict: ReleaseNOGO, Reasons: reasons}
	}
	return ReleaseVerdict{Verdict: ReleaseReadyForControlledEmailReview}
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
