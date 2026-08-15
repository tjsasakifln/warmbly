package confenge

import (
	"strings"

	"github.com/warmbly/warmbly/internal/models"
)

// CommercialActionContent is channel-appropriate copy. It is not a
// subject-less email: calls get a short script, role mailboxes address a
// function, WhatsApp/social/form stay short and human-executed.
type CommercialActionContent struct {
	Kind             string   `json:"kind"`
	Subject          string   `json:"subject,omitempty"`
	Body             string   `json:"body,omitempty"`
	CTA              string   `json:"cta,omitempty"`
	Opening          string   `json:"opening,omitempty"`
	ReasonForCall    string   `json:"reason_for_call,omitempty"`
	ValueProposition string   `json:"value_proposition,omitempty"`
	Ask              string   `json:"ask,omitempty"`
	ObjectionNotes   string   `json:"objection_notes,omitempty"`
	DoNotClaim       []string `json:"do_not_claim,omitempty"`
	PersonID         string   `json:"person_id,omitempty"`
	MailboxLabel     string   `json:"mailbox_label,omitempty"`
}

// FounderFacingCopy is the single string the founder revises and copies.
// CALL uses opening + reason + ask; other routes use Body.
func FounderFacingCopy(c CommercialActionContent) string {
	if body := strings.TrimSpace(c.Body); body != "" {
		return body
	}
	var parts []string
	for _, p := range []string{c.Opening, c.ReasonForCall, c.Ask} {
		p = strings.TrimSpace(p)
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

// ComposeActionContent builds executable copy from the planned action.
func ComposeActionContent(a models.OutreachCommercialAction) CommercialActionContent {
	company := founderCompanyName(firstNonEmpty(a.CompanyName, "a empresa"))
	hook := founderSafeFact(a.FactualHook)
	offer := firstNonEmpty(a.ServiceContext, a.ServiceCode)
	ask := firstNonEmpty(a.RecommendedAction, "Posso enviar um recorte objetivo?")
	var c CommercialActionContent
	switch a.ActionType {
	case models.ActionDirectEmail:
		c = composeDirectEmail(a, company, hook, offer)
	case models.ActionInferredEmailReview:
		c = composeDirectEmail(a, company, hook, offer)
		c.Kind = "INFERRED_EMAIL"
		c.DoNotClaim = append(c.DoNotClaim, "Nao tratar este endereco como e-mail validado para envio.")
	case models.ActionRoleEmail:
		c = composeRoleEmail(a, company, hook)
	case models.ActionGenericEmail, models.ActionOtherManual:
		c = composeGenericMailbox(a, company, hook)
	case models.ActionDirectCall:
		c = composeCall(a, company, hook, ask, false)
	case models.ActionRoutedCall:
		c = composeCall(a, company, hook, ask, true)
	case models.ActionWhatsApp:
		c = composeWhatsAppAction(a, hook)
	case models.ActionProfessionalSocial:
		c = composeSocial(a, company, hook)
	case models.ActionContactForm:
		c = composeForm(a, company, hook, offer)
	default:
		c = CommercialActionContent{Kind: "MANUAL", Body: strings.TrimSpace(a.RecommendedAction)}
	}
	c.Body = ApplyCopyHygiene(c.Body)
	c.Opening = ApplyCopyHygiene(c.Opening)
	c.ReasonForCall = ApplyCopyHygiene(c.ReasonForCall)
	c.Ask = ApplyCopyHygiene(c.Ask)
	c.Subject = ApplyCopyHygiene(c.Subject)
	if c.Kind == "EMAIL" || c.Kind == "INFERRED_EMAIL" || c.Kind == "ROLE_EMAIL" {
		c.Body = ensureEmailSender(c.Body)
	}
	c.PersonID = firstNonEmpty(a.PersonID, c.PersonID)
	if len(a.Warnings) > 0 {
		for _, w := range a.Warnings {
			if strings.Contains(strings.ToLower(w), "nao alegar") || strings.Contains(strings.ToLower(w), "não alegar") {
				c.DoNotClaim = appendUnique(c.DoNotClaim, w)
			}
		}
	}
	return c
}

func composeDirectEmail(a models.OutreachCommercialAction, company, hook, offer string) CommercialActionContent {
	name := givenName(a.PersonName)
	greet := "Ola"
	if name != "" {
		greet = "Ola, " + name
	}
	body := greet + "."
	if hook != "" {
		body += " Pelo que esta publico, " + hook + "."
	}
	cta := firstNonEmpty(a.RecommendedAction, "Posso te mandar o recorte do que eu conferiria?")
	body += " " + cta
	subj := ""
	if hook != "" {
		subj = "Sobre " + clipRunes(hook, 80)
	} else if offer != "" {
		subj = offer + ": " + company
	}
	return CommercialActionContent{
		Kind:    "EMAIL",
		Subject: subj,
		Body:    strings.TrimSpace(body),
		CTA:     cta,
	}
}

func composeRoleEmail(a models.OutreachCommercialAction, company, hook string) CommercialActionContent {
	label := observedMailboxLabel(a.ChannelValue, a.TargetRole)
	body := "Olá. Escrevo para " + label + " de " + company + "."
	if hook != "" {
		body += " Vi " + ensureLowerStart(hook) + "."
	}
	cta := "Posso enviar um recorte para a equipe?"
	body += " " + cta
	return CommercialActionContent{
		Kind:         "ROLE_EMAIL",
		Subject:      "Contrato publicado: " + company,
		Body:         body,
		CTA:          cta,
		MailboxLabel: label,
		DoNotClaim:   []string{"Não tratar esta caixa como o e-mail pessoal de uma pessoa inventada."},
	}
}

func composeGenericMailbox(a models.OutreachCommercialAction, company, hook string) CommercialActionContent {
	label := observedMailboxLabel(a.ChannelValue, "")
	if guessedContractsArea(label) {
		label = "caixa da empresa"
	}
	body := "Olá. Escrevo para a " + label + " de " + company + "."
	if strings.HasSuffix(label, "@") {
		body = "Olá. Escrevo para " + label + " de " + company + "."
	}
	if hook != "" {
		body += " Vi " + ensureLowerStart(hook) + "."
	}
	body += " Este é o canal certo para um recorte objetivo?"
	return CommercialActionContent{
		Kind:         "MANUAL",
		Body:         body,
		Ask:          "Este é o canal certo para um recorte objetivo?",
		MailboxLabel: label,
		DoNotClaim: []string{
			"Não inventar um nome.",
			"Não tratar contato genérico como destinatário pessoal.",
		},
	}
}

func composeCall(a models.OutreachCommercialAction, company, hook, ask string, routed bool) CommercialActionContent {
	opening := "Olá, aqui é da CONFENGE."
	reason := founderCallReason(company, hook)
	callAsk := "Quem acompanha esse contrato aí?"
	doNot := []string{}
	if routed {
		opening = "Olá, aqui é da CONFENGE. Ligação para " + company + "."
		if name := givenName(a.PersonName); name != "" {
			callAsk = "Poderia me encaminhar para " + name + " ou a quem acompanha esse contrato?"
		} else {
			callAsk = "Poderia me encaminhar a quem acompanha esse contrato?"
		}
		doNot = append(doNot,
			"Não alegar que este telefone pertence diretamente a "+firstNonEmpty(a.PersonName, "a pessoa alvo")+".",
			"Este é o telefone oficial da empresa (switchboard), não o ramal pessoal.",
		)
	} else if name := givenName(a.PersonName); name != "" {
		opening = "Olá, " + name + ". Aqui é da CONFENGE."
		callAsk = diagnosticAsk(ask, "Você é quem acompanha esse contrato?")
	}
	body := strings.TrimSpace(opening + " " + reason + " " + callAsk)
	return CommercialActionContent{
		Kind:          "CALL",
		Body:          body,
		Opening:       opening,
		ReasonForCall: reason,
		Ask:           callAsk,
		MailboxLabel:  observedMailboxLabel(a.ChannelValue, a.TargetRole),
		DoNotClaim:    doNot,
	}
}

func composeWhatsAppAction(a models.OutreachCommercialAction, hook string) CommercialActionContent {
	name := givenName(a.PersonName)
	greet := "Olá"
	if name != "" {
		greet = "Olá, " + name
	}
	body := greet + ". Aqui é da CONFENGE."
	if hook != "" {
		body += " Vi " + ensureLowerStart(clipRunes(hook, 80)) + "."
	}
	body += " Posso te mandar um recorte curto?"
	return CommercialActionContent{
		Kind:         "WHATSAPP",
		Body:         body,
		Ask:          "Posso te mandar um recorte curto?",
		MailboxLabel: observedMailboxLabel(a.ChannelValue, a.TargetRole),
		DoNotClaim: []string{
			"Não enviar automaticamente.",
			"Só executar se o número publicado tiver consentimento.",
		},
	}
}

func composeSocial(a models.OutreachCommercialAction, company, hook string) CommercialActionContent {
	name := givenName(a.PersonName)
	body := "Ola"
	if name != "" {
		body = "Ola, " + name
	}
	body += ". Vi um fato publico de " + company
	if hook != "" {
		body += " (" + clipRunes(hook, 90) + ")"
	}
	body += ". Posso te mandar um recorte de uma pagina?"
	return CommercialActionContent{
		Kind: "PROFESSIONAL_SOCIAL",
		Body: body,
		Ask:  "Posso te mandar um recorte de uma pagina?",
		DoNotClaim: []string{
			"Nao automatizar scraping nem envio.",
			"Nao fingir relacao previa.",
		},
	}
}

func composeForm(a models.OutreachCommercialAction, company, hook, offer string) CommercialActionContent {
	body := "Escrevo para a equipe de " + company + "."
	if hook != "" {
		body += " Pelo que esta publico, " + hook + "."
	}
	if offer != "" {
		body += " Gostaria de enviar um recorte sobre " + offer + "."
	}
	return CommercialActionContent{
		Kind: "CONTACT_FORM",
		Body: body,
		Ask:  "Podem encaminhar ao time de contratos?",
		DoNotClaim: []string{
			"Nao submeter automaticamente.",
			"Nao inventar destinatario nominal no formulario.",
		},
	}
}

func givenName(full string) string {
	full = strings.TrimSpace(full)
	if full == "" {
		return ""
	}
	return strings.Split(full, " ")[0]
}

func clipRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "..."
}
