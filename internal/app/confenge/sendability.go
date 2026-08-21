package confenge

import (
	"strings"

	"github.com/warmbly/warmbly/internal/models"
)

// ConsultantSendabilityPack is the operator "send without editing?" card.
// It is never interpolated into recipient copy.
type ConsultantSendabilityPack struct {
	Company            string          `json:"company"`
	Person             string          `json:"person,omitempty"`
	WhyThisPerson      string          `json:"why_this_person,omitempty"`
	Email              string          `json:"email,omitempty"`
	EmailEvidence      string          `json:"email_evidence,omitempty"`
	Derivation         string          `json:"derivation,omitempty"`
	VerificationStatus string          `json:"verification_status,omitempty"`
	EpistemicClass     string          `json:"epistemic_class,omitempty"`
	Freshness          string          `json:"freshness,omitempty"`
	Suppression        string          `json:"suppression,omitempty"`
	ServiceCode        string          `json:"service_code,omitempty"`
	SupportingFact     string          `json:"supporting_fact,omitempty"`
	Subject            string          `json:"subject,omitempty"`
	Body               string          `json:"body,omitempty"`
	Warnings           []string        `json:"warnings,omitempty"`
	HardGates          map[string]bool `json:"hard_gates,omitempty"`
	SendWithoutEditing string          `json:"send_without_editing"`
	RecipientState     string          `json:"recipient_state,omitempty"`
	Messageability     string          `json:"messageability,omitempty"`
	Lane               string          `json:"lane,omitempty"`
}

// BuildConsultantSendabilityPack answers the operator yes/no quickly.
func BuildConsultantSendabilityPack(
	acc *models.OutreachAccount,
	cand *models.OutreachContactCandidate,
	rec RecipientResolution,
	plan OutboundMessagePlan,
	out DraftOutput,
	val ValidationResult,
) ConsultantSendabilityPack {
	pack := ConsultantSendabilityPack{
		Company:            firstNonEmpty(accountCompany(acc), rec.Company),
		Email:              firstNonEmpty(rec.Email, candidateEmail(cand)),
		VerificationStatus: firstNonEmpty(candidateVerify(cand), rec.Provenance),
		ServiceCode:        firstNonEmpty(plan.ServiceCode, out.ServiceCode, accService(acc)),
		SupportingFact:     firstNonEmpty(plan.Hook, out.FactUsed),
		Subject:            out.Subject,
		Body:               out.BodyText,
		RecipientState:     rec.State,
		Messageability:     plan.Messageability,
		HardGates:          map[string]bool{},
		SendWithoutEditing: "nao",
	}
	if cand != nil {
		pack.Derivation = firstNonEmpty(cand.EmailDerivation, derivationFromClass(cand.ReachabilityClass))
		pack.EpistemicClass = cand.ChannelEpistemic
		pack.Freshness = cand.RouteFreshness
		pack.Suppression = cand.RouteSuppression
		if len(cand.DiscoveryJSON) > 0 {
			pack.EmailEvidence = string(cand.DiscoveryJSON)
		}
		if provenPersonName(cand) {
			pack.Person = strings.TrimSpace(cand.Name)
			pack.WhyThisPerson = firstNonEmpty(cand.Role, rec.Role, "Pessoa nomeada publicada pelo extra-cli.")
		}
	}
	if rec.Name != "" {
		pack.Person = firstNonEmpty(pack.Person, rec.Name)
	}
	if pack.EmailEvidence == "" && cand != nil {
		pack.EmailEvidence = firstNonEmpty(cand.SourceURL, cand.SourceDocument, cand.VerificationStatus)
	}
	pack.Warnings = append([]string{}, val.Errors...)
	pack.Warnings = append(pack.Warnings, val.Warnings...)
	pack.Warnings = append(pack.Warnings, plan.ReasonCodes...)
	pack.Warnings = append(pack.Warnings, rec.ReasonCodes...)

	sendable := val.OK &&
		RecipientStateAuthorizable(rec.State) &&
		plan.Messageability == MessageabilityReady &&
		strings.TrimSpace(out.BodyText) != "" &&
		strings.TrimSpace(out.Subject) != "" &&
		(!isGenericRecipient(cand) || CandidateControlledEligible(cand)) &&
		CandidateRouteClass(cand) != RouteClassProbabilisticOrRisky
	pack.HardGates["validation_ok"] = val.OK
	pack.HardGates["recipient_validated"] = rec.State == RecipientValidated
	pack.HardGates["recipient_authorizable"] = RecipientStateAuthorizable(rec.State)
	pack.HardGates["messageability_ready"] = plan.Messageability == MessageabilityReady
	pack.HardGates["body_present"] = strings.TrimSpace(out.BodyText) != ""
	pack.HardGates["subject_present"] = strings.TrimSpace(out.Subject) != ""
	pack.HardGates["not_generic"] = !isGenericRecipient(cand) || CandidateControlledEligible(cand)
	pack.HardGates["not_inferred"] = pack.Derivation != "INFERRED"
	if sendable {
		pack.SendWithoutEditing = "sim"
		pack.Lane = LaneNeedsReviewEmail
	} else if rec.State == RecipientException || isGenericRecipient(cand) {
		pack.Lane = firstNonEmpty(ClassifyContactTier(acc, cand, rec.ValidatedAt).Lane, LaneLowConfidenceManual)
	} else if plan.Messageability == MessageabilityNeedsEnrichment {
		pack.Lane = LaneManualOutreach
	} else {
		pack.Lane = LaneBlockedExhausted
	}
	return pack
}

func (p ConsultantSendabilityPack) AsMap() map[string]any {
	return map[string]any{
		"company":              p.Company,
		"person":               p.Person,
		"why_this_person":      p.WhyThisPerson,
		"email":                p.Email,
		"email_evidence":       p.EmailEvidence,
		"derivation":           p.Derivation,
		"verification_status":  p.VerificationStatus,
		"epistemic_class":      p.EpistemicClass,
		"freshness":            p.Freshness,
		"suppression":          p.Suppression,
		"service_code":         p.ServiceCode,
		"supporting_fact":      p.SupportingFact,
		"subject":              p.Subject,
		"body":                 p.Body,
		"warnings":             p.Warnings,
		"hard_gates":           p.HardGates,
		"send_without_editing": p.SendWithoutEditing,
		"recipient_state":      p.RecipientState,
		"messageability":       p.Messageability,
		"lane":                 p.Lane,
	}
}

func candidateEmail(c *models.OutreachContactCandidate) string {
	if c == nil {
		return ""
	}
	return c.Email
}

func candidateVerify(c *models.OutreachContactCandidate) string {
	if c == nil {
		return ""
	}
	return c.VerificationStatus
}

func derivationFromClass(class string) string {
	if class == models.ReachabilityR2Inferred {
		return "INFERRED"
	}
	if class == models.ReachabilityR1Direct {
		return "OBSERVED"
	}
	return ""
}
