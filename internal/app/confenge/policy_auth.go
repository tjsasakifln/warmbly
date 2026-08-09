package confenge

import (
	"strings"
	"time"

	"github.com/warmbly/warmbly/internal/models"
)

// AuthorizationMode distinguishes per-touch human approval from campaign policy.
const (
	AuthorizationModeHumanTouchpoint = "HUMAN_TOUCHPOINT_APPROVAL"
	AuthorizationModeCampaignPolicy  = "CAMPAIGN_POLICY"
)

// CampaignPolicyAuthorization is an alias of the models type for package-local use.
type CampaignPolicyAuthorization = models.CampaignPolicyAuthorization

// GreenAutorunInput is the deterministic predicate set for policy autoqueue (§12).
type GreenAutorunInput struct {
	Channel                   string
	EmailSendReady            bool
	TargetFitSendTier         string // A_AUTOMATIC | B_EVIDENCE_SUPPORTED
	OwnershipAllowed          bool
	MailboxPurposeAllowed     bool
	VerificationAllowed       bool
	DNC                       bool
	Bounce                    bool
	Replied                   bool
	Blocked                   bool
	ContactFresh              bool
	ContextFresh              bool
	ServiceCode               string
	SingleService             bool
	FactualHookAnchored       bool
	NoUnknownEvidenceIDs      bool
	NoHypothesisAsFact        bool
	NoClaimsToAvoidViolated   bool
	ValidationOK              bool
	RiskClass                 string
	MessageContextHashCurrent bool
	NoEditAfterAuthorization  bool
	CopyWithinLimits          bool
	GovernorHealthy           bool
	InSendWindow              bool
	ProviderHealthy           bool
	// Template path: only when policy allows and template is the authorized version.
	UsedPolicyApprovedTemplate bool
	GenericUnauditedTemplate   bool
}

// GreenAutorunDecision is the fail-closed evaluation result.
type GreenAutorunDecision struct {
	Allow             bool
	AuthorizationMode string
	Reasons           []string
}

// EvaluateGreenAutorun returns allow=true only when ALL predicates pass and
// a valid campaign policy authorization exists with GreenAutorunEnabled.
// Does not set approved_by to a human identity.
func EvaluateGreenAutorun(
	enabled bool,
	auth *CampaignPolicyAuthorization,
	in GreenAutorunInput,
	now time.Time,
) GreenAutorunDecision {
	reasons := make([]string, 0, 8)
	if !enabled {
		return GreenAutorunDecision{Allow: false, Reasons: []string{"green_autorun_disabled"}}
	}
	if auth == nil || !auth.Active(now) {
		return GreenAutorunDecision{Allow: false, Reasons: []string{"no_active_campaign_policy_authorization"}}
	}
	if ch := strings.ToUpper(strings.TrimSpace(in.Channel)); ch != "EMAIL" {
		reasons = append(reasons, "channel_not_email")
	}
	if authCh := strings.ToUpper(strings.TrimSpace(auth.Channel)); authCh != "" && authCh != "EMAIL" {
		reasons = append(reasons, "auth_channel_not_email")
	}
	if !in.EmailSendReady {
		reasons = append(reasons, "email_send_ready_false")
	}
	tier := strings.ToUpper(strings.TrimSpace(in.TargetFitSendTier))
	if tier != "A_AUTOMATIC" && tier != "B_EVIDENCE_SUPPORTED" {
		reasons = append(reasons, "target_fit_not_send_tier")
	}
	if !in.OwnershipAllowed {
		reasons = append(reasons, "ownership_not_allowed")
	}
	if !in.MailboxPurposeAllowed {
		reasons = append(reasons, "mailbox_purpose_blocked")
	}
	if !in.VerificationAllowed {
		reasons = append(reasons, "verification_not_allowed")
	}
	if in.DNC {
		reasons = append(reasons, "dnc")
	}
	if in.Bounce {
		reasons = append(reasons, "bounce")
	}
	if in.Replied {
		reasons = append(reasons, "replied")
	}
	if in.Blocked {
		reasons = append(reasons, "blocked")
	}
	if !in.ContactFresh || !in.ContextFresh {
		reasons = append(reasons, "stale_contact_or_context")
	}
	if strings.TrimSpace(in.ServiceCode) == "" {
		reasons = append(reasons, "service_code_missing")
	}
	if !in.SingleService {
		reasons = append(reasons, "not_exactly_one_service")
	}
	if !in.FactualHookAnchored {
		reasons = append(reasons, "factual_hook_not_anchored")
	}
	if !in.NoUnknownEvidenceIDs {
		reasons = append(reasons, "unknown_evidence_ids")
	}
	if !in.NoHypothesisAsFact {
		reasons = append(reasons, "hypothesis_as_fact")
	}
	if !in.NoClaimsToAvoidViolated {
		reasons = append(reasons, "claims_to_avoid_violated")
	}
	if !in.ValidationOK {
		reasons = append(reasons, "validation_not_ok")
	}
	rc := strings.ToUpper(strings.TrimSpace(in.RiskClass))
	allowedRC := strings.ToUpper(strings.TrimSpace(auth.AllowedRiskClass))
	if allowedRC == "" {
		allowedRC = "GREEN"
	}
	if rc != "GREEN" || (allowedRC != "GREEN" && rc != allowedRC) {
		reasons = append(reasons, "risk_class_not_green")
	}
	if !in.MessageContextHashCurrent {
		reasons = append(reasons, "message_context_stale")
	}
	if !in.NoEditAfterAuthorization {
		reasons = append(reasons, "edited_after_authorization")
	}
	if !in.CopyWithinLimits {
		reasons = append(reasons, "copy_outside_limits")
	}
	if !in.GovernorHealthy {
		reasons = append(reasons, "governor_unhealthy")
	}
	if !in.InSendWindow {
		reasons = append(reasons, "outside_send_window")
	}
	if !in.ProviderHealthy {
		reasons = append(reasons, "provider_unhealthy")
	}
	// Generic unaudited template stays YELLOW — never autorun.
	if in.GenericUnauditedTemplate {
		reasons = append(reasons, "generic_unaudited_template")
	}
	// Policy-approved template is allowed when flag set; AI path needs no template flag.
	if in.UsedPolicyApprovedTemplate && !auth.AllowPolicyTemplateGREEN {
		reasons = append(reasons, "policy_template_not_authorized")
	}

	if len(reasons) > 0 {
		return GreenAutorunDecision{Allow: false, AuthorizationMode: AuthorizationModeCampaignPolicy, Reasons: reasons}
	}
	return GreenAutorunDecision{
		Allow:             true,
		AuthorizationMode: AuthorizationModeCampaignPolicy,
		Reasons:           []string{"all_green_predicates_pass"},
	}
}
