package confenge

import (
	"strings"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/models"
)

// InboundClassification is a fact-only next_action decision.
type InboundClassification struct {
	NextAction        string
	ActionType        string
	Lane              string
	Channel           string
	RecommendedAction string
	SuppressReason    string
	Status            string
	Actionable        bool
	EmailSendable     bool
	WhyNow            string
	Warnings          []string
}

// ClassifyInboundNextAction uses only observed facts. It never auto-sends
// and never promotes inferred or generic email to VALIDATED.
func ClassifyInboundNextAction(lead InboundLeadV1, facts InboundFacts) InboundClassification {
	out := InboundClassification{
		Lane:          models.LaneInboundNow,
		WhyNow:        firstNonEmpty(facts.WhyNow, defaultInboundWhy(lead)),
		Status:        models.InboundStatusOpen,
		Actionable:    true,
		EmailSendable: false,
	}
	if reason := inboundSyntheticSkipReason(lead); reason != "" {
		out.NextAction = models.InboundNextSuppressed
		out.ActionType = models.ActionOtherManual
		out.Lane = models.LaneBlockedAction
		out.Status = models.InboundStatusSuppressed
		out.SuppressReason = reason
		out.RecommendedAction = "Receipt labeled " + reason + ". No commercial action."
		out.Actionable = false
		out.Warnings = append(out.Warnings, "synthetic_or_internal_receipt")
		return out
	}
	pref := preferredInboundChannel(lead)

	if facts.DNC || lead.Consent.DNC {
		out.NextAction = models.InboundNextSuppressed
		out.ActionType = models.ActionOtherManual
		out.Lane = models.LaneBlockedAction
		out.Status = models.InboundStatusSuppressed
		out.SuppressReason = "DNC"
		out.RecommendedAction = "Nao contatar. DNC fail-closed."
		out.Actionable = false
		out.Warnings = append(out.Warnings, "DNC observado. Nenhuma acao comercial.")
		return out
	}
	if lead.Consent.Spam {
		out.NextAction = models.InboundNextSuppressed
		out.ActionType = models.ActionOtherManual
		out.Lane = models.LaneBlockedAction
		out.Status = models.InboundStatusSuppressed
		out.SuppressReason = "spam"
		out.RecommendedAction = "Suprimido: sinal de spam no receipt."
		out.Actionable = false
		return out
	}

	hasAccount := facts.AccountID != "" || lead.CNPJ != "" || lead.CompanyName != "" || lead.EntityID != "" || lead.ContractID != ""
	hasPerson := facts.NamedHuman || strings.TrimSpace(lead.Name) != "" || facts.PersonID != ""
	hasChannel := facts.PhonePresent || strings.TrimSpace(facts.Email) != "" || strings.TrimSpace(lead.Email) != "" || strings.TrimSpace(lead.Phone) != ""
	if !hasAccount && !hasPerson && !hasChannel {
		return needsEnrichment(out, "Identidade insuficiente. Enriquecer antes de qualquer contato.")
	}

	if facts.GenericMailbox && !facts.NamedHuman && !leadSuppliedName(lead, facts.PersonName) && strings.TrimSpace(lead.Name) == "" {
		out.Warnings = append(out.Warnings, "Caixa generica. Nao tratar como pessoa nomeada.")
	}

	wantsContractReread := inboundWantsContractReread(lead)
	if facts.PhonePresent && (wantsContractReread || lead.HighIntentHint) {
		if pref == "whatsapp" {
			return classifyWhatsApp(out, facts)
		}
		return classifyCall(out, facts, wantsContractReread)
	}

	if lead.HighIntentHint && facts.EmailValidated && !facts.GenericMailbox {
		out.NextAction = models.InboundNextSendEmail
		out.ActionType = models.ActionDirectEmail
		out.Lane = models.LaneEmailNeedsReview
		out.Channel = "email"
		out.EmailSendable = false // review only; dispatch stays human
		out.RecommendedAction = "Preparar e-mail em revisao. Nao enviar automaticamente."
		out.Warnings = append(out.Warnings, "SEND_EMAIL exige VALIDATED+READY e aprovacao humana. Auto-send desligado.")
		return out
	}

	if facts.NamedHuman && !facts.PhonePresent && !facts.EmailValidated {
		out.NextAction = models.InboundNextRoutedCall
		out.ActionType = models.ActionRoutedCall
		out.Lane = models.LaneRoutedCallQueue
		out.Channel = firstNonEmpty(facts.RouteType, "phone")
		person := firstNonEmpty(facts.PersonName, "a pessoa alvo")
		out.RecommendedAction = "Ligacao roteada: pedir para falar com " + person + ". Sem canal direto publicado."
		out.Warnings = append(out.Warnings, "Pessoa conhecida sem canal direto. Nao inventar telefone ou e-mail.")
		return out
	}

	if facts.PhonePresent {
		if pref == "whatsapp" {
			return classifyWhatsApp(out, facts)
		}
		return classifyCall(out, facts, false)
	}

	if strings.TrimSpace(lead.Email) != "" || strings.TrimSpace(facts.Email) != "" {
		if facts.GenericMailbox {
			out.NextAction = models.InboundNextManualOutreach
			out.ActionType = models.ActionOtherManual
			out.Channel = "email"
			out.RecommendedAction = "Abordagem manual da caixa funcional. Nao tratar como pessoa nomeada."
			return out
		}
		out.NextAction = models.InboundNextManualOutreach
		out.ActionType = models.ActionInferredEmailReview
		out.Lane = models.LaneHumanReviewEmail
		out.Channel = "email"
		out.RecommendedAction = "E-mail fornecido pelo lead, ainda nao VALIDATED. Revisar. Nao auto-enviar."
		out.Warnings = append(out.Warnings, "E-mail do lead nao e VALIDATED. Fail-closed.")
		return out
	}

	if hasPerson && !hasChannel {
		out.NextAction = models.InboundNextRoutedCall
		out.ActionType = models.ActionRoutedCall
		out.Lane = models.LaneRoutedCallQueue
		out.Channel = "phone"
		out.RecommendedAction = "Pessoa conhecida sem canal direto. ROUTED_CALL / MANUAL_OUTREACH."
		return out
	}

	if !hasChannel || !hasAccount {
		return needsEnrichment(out, "Identidade ou canal insuficiente. NEEDS_ENRICHMENT.")
	}

	out.NextAction = models.InboundNextManualOutreach
	out.ActionType = models.ActionOtherManual
	out.Channel = firstNonEmpty(pref, "manual")
	out.RecommendedAction = "Abordagem manual com os fatos observados. Sem auto-envio."
	return out
}

func classifyCall(out InboundClassification, facts InboundFacts, contract bool) InboundClassification {
	out.NextAction = models.InboundNextCall
	out.ActionType = models.ActionDirectCall
	out.Lane = models.LaneCallQueue
	out.Channel = "phone"
	if contract {
		out.RecommendedAction = "Ligar para revisar o contrato no telefone fornecido pelo lead."
	} else {
		out.RecommendedAction = "Ligar para o telefone fornecido pelo lead."
	}
	if facts.ReachabilityClass == models.ReachabilityR3Routed {
		out.NextAction = models.InboundNextRoutedCall
		out.ActionType = models.ActionRoutedCall
		out.Lane = models.LaneRoutedCallQueue
		out.RecommendedAction = "Ligacao roteada no telefone da empresa. Nao tratar como ramal pessoal."
	}
	return out
}

func classifyWhatsApp(out InboundClassification, facts InboundFacts) InboundClassification {
	_ = facts
	out.NextAction = models.InboundNextWhatsApp
	out.ActionType = models.ActionWhatsApp
	out.Lane = models.LaneWhatsAppQueue
	out.Channel = "whatsapp"
	out.RecommendedAction = "WhatsApp manual no numero fornecido. O sistema nao envia."
	return out
}

func needsEnrichment(out InboundClassification, reason string) InboundClassification {
	out.NextAction = models.InboundNextNeedsEnrichment
	out.ActionType = models.ActionOtherManual
	out.Lane = models.LaneNeedsEnrichment
	out.Channel = ""
	out.RecommendedAction = reason
	out.Actionable = true
	out.Warnings = append(out.Warnings, reason)
	return out
}

func preferredInboundChannel(lead InboundLeadV1) string {
	p := strings.ToLower(strings.TrimSpace(firstNonEmpty(lead.Consent.PreferredChannel, lead.Consent.Channel)))
	switch {
	case strings.Contains(p, "whats"):
		return "whatsapp"
	case strings.Contains(p, "mail") || p == "email" || p == "e-mail":
		return "email"
	case strings.Contains(p, "call") || strings.Contains(p, "phone") || strings.Contains(p, "tel"):
		return "phone"
	}
	return ""
}

func inboundSyntheticSkipReason(lead InboundLeadV1) string {
	if lead.Synthetic {
		return intel.InboundSkipSynthetic
	}
	kind := strings.ToLower(strings.TrimSpace(lead.RecordKind))
	switch kind {
	case intel.RecordKindSynthetic, intel.InboundSkipQA, intel.InboundSkipInternal:
		return kind
	}
	return intel.InboundCommercialSkipReason(models.OutreachInboundLead{
		LeadID: lead.LeadID, ReceiptID: lead.ReceiptID, Source: lead.Source,
		CompanyName: lead.CompanyName, LeadName: lead.Name, LeadEmail: lead.Email, Message: lead.Message,
	})
}

func inboundWantsContractReread(lead InboundLeadV1) bool {
	if lead.ContractID == "" {
		return false
	}
	blob := strings.ToLower(lead.CTAID + " " + lead.Message + " " + lead.RouteFamily)
	for _, tok := range []string{"segunda leitura", "reler", "re-leitura", "leitura", "contrato"} {
		if strings.Contains(blob, tok) {
			return true
		}
	}
	return lead.ContractID != "" && lead.HighIntentHint
}

func defaultInboundWhy(lead InboundLeadV1) string {
	if lead.ContractID != "" {
		return "Lead inbound pediu contexto do contrato " + lead.ContractID + "."
	}
	if lead.AssetID != "" {
		return "Lead inbound no asset " + lead.AssetID + "."
	}
	if strings.TrimSpace(lead.Message) != "" {
		return "Lead inbound: " + truncateRunes(lead.Message, 180)
	}
	return "Lead inbound de " + firstNonEmpty(lead.Source, "web-cfg") + "."
}

func inboundActionIdempotency(leadID string) string {
	return "inbound:" + strings.TrimSpace(leadID)
}
