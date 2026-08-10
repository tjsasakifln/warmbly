package confenge

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/warmbly/warmbly/internal/models"
)

// StructuralApproveBlockers reconstructs commercial completeness from persisted
// account/candidate/strategy data. Human approve must not promote incomplete
// strategy to send-ready solely because body text passed superficial checks.
//
// Returns empty slice when the draft may be approved; otherwise explicit reasons.
func StructuralApproveBlockers(
	acc *models.OutreachAccount,
	cand *models.OutreachContactCandidate,
	st *OutreachStrategy,
	d *models.OutreachDraft,
	pb *Playbook,
) []string {
	var blockers []string
	add := func(code, msg string) {
		blockers = append(blockers, code+": "+msg)
	}

	if d == nil {
		add("missing_draft", "draft is required")
		return blockers
	}
	if acc == nil {
		add("missing_account", "account is required")
		return blockers
	}
	if acc.DoNotContact {
		add("account_dnc", "account is DO_NOT_CONTACT")
	}
	if acc.Blocked {
		add("account_blocked", "account is blocked")
	}

	// Target confirmation: when tier is present, only CONFIRMED is approvable.
	tier := strings.ToUpper(strings.TrimSpace(acc.TargetFitSendTier))
	if tier != "" && tier != "TARGET_CONFIRMED" && tier != "CONFIRMED" {
		add("target_not_confirmed", "target_fit_send_tier="+tier)
	}

	// Contact send-ready / DNC / bounce / block.
	if cand == nil {
		add("missing_contact", "contact candidate is required")
	} else {
		if cand.DoNotContact {
			add("contact_dnc", "contact is DO_NOT_CONTACT")
		}
		if cand.Bounced {
			add("contact_bounce", "contact address bounced")
		}
		if cand.Blocked {
			add("contact_blocked", "contact is blocked")
		}
		if !cand.CanEnroll() {
			add("contact_not_send_ready", "contact is not enrollable")
		}
	}

	// Prefer reconstructed strategy; fall back to draft risk flags / validation JSON.
	if st == nil {
		st = strategyFromDraft(d)
	}
	if st == nil {
		// Last resort: replan from account/evidence-less candidate.
		tmp := PlanOutreachStrategy(pb, acc, cand, nil, 1)
		st = &tmp
	}

	flags := append([]string{}, st.RiskFlags...)
	if d != nil {
		flags = append(flags, d.RiskFlags...)
	}

	serviceCode := strings.TrimSpace(firstNonEmpty(st.ServiceCode, d.ServiceCode, acc.ServiceCode))
	if serviceCode == "" {
		add("missing_service_code", "service code is empty")
	} else if pb != nil && pb.ResolveServicePlaybook(serviceCode) == nil {
		add("unknown_service_code", "service has no canonical playbook mapping: "+serviceCode)
	}

	if containsAnyFlag(flags, "unknown_service_code", "service_unmapped") {
		add("unknown_service_code", "strategy marked unknown_service_code")
	}
	if containsAnyFlag(flags, "missing_service_code") {
		add("missing_service_code", "strategy marked missing_service_code")
	}
	if containsAnyFlag(flags, "incomplete_strategy") {
		add("incomplete_strategy", "strategy is incomplete")
	}
	if containsAnyFlag(flags, "incomplete_copy_context") {
		add("incomplete_copy_context", "copy context is incomplete")
	}

	micro := strings.TrimSpace(st.MicroOfferCode)
	if micro == "" {
		// Draft may not store micro_offer; strategy is authoritative.
		add("missing_micro_offer", "MicroOfferCode is empty")
	} else if pb != nil && serviceCode != "" {
		if o := pb.FindOffer(micro); o != nil {
			if !pb.OfferApplicable(o, serviceCode, true) {
				add("service_offer_mismatch", "micro-offer "+micro+" not applicable to service "+serviceCode)
			}
		}
	}

	whyYou := strings.TrimSpace(st.WhyThisAccount)
	whyNow := strings.TrimSpace(st.WhyNow)
	obs := strings.TrimSpace(firstNonEmpty(st.ObservedFact, d.FactUsed, acc.FactToMention))
	if whyYou == "" || isGenericWhyThisAccount(whyYou) {
		add("missing_why_this_account", "WhyThisAccount empty or generic")
	}
	if whyNow == "" || isGenericWhyNow(whyNow) {
		add("missing_why_now", "WhyNow empty or generic")
	}
	if obs == "" || isGenericPublicFact(obs) {
		add("missing_observed_fact", "ObservedFact empty or generic")
	}

	// Evidence required when service/playbook expects it (fact used without anchors).
	if obs != "" && len(st.EvidenceIDs) == 0 && len(acc.MomentEvidenceIDs) == 0 && len(d.EvidenceIDs) == 0 {
		add("missing_evidence", "service fact lacks referenced evidence ids")
	}

	// RiskClass alone is not authority, but RED with incomplete flags is hard-block.
	if strings.EqualFold(d.RiskClass, "RED") && containsAnyFlag(flags,
		"incomplete_copy_context", "incomplete_strategy", "unknown_service_code", "missing_service_code") {
		add("red_incomplete", "RED draft with structural incompleteness cannot be approved")
	}

	return blockers
}

func containsAnyFlag(flags []string, want ...string) bool {
	for _, f := range flags {
		for _, w := range want {
			if strings.EqualFold(strings.TrimSpace(f), w) {
				return true
			}
		}
	}
	return false
}

// strategyFromDraft reconstructs strategy from persisted ValidationJSON when present.
func strategyFromDraft(d *models.OutreachDraft) *OutreachStrategy {
	if d == nil || len(d.ValidationJSON) == 0 {
		return nil
	}
	var val ValidationResult
	if err := json.Unmarshal(d.ValidationJSON, &val); err != nil {
		return nil
	}
	if val.Strategy == nil {
		return nil
	}
	return val.Strategy
}

// FormatApproveBlockers joins structural blockers for errx messages.
func FormatApproveBlockers(blockers []string) string {
	if len(blockers) == 0 {
		return ""
	}
	return fmt.Sprintf("draft not structurally approvable: %s", strings.Join(blockers, "; "))
}
