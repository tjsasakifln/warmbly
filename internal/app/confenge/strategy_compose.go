package confenge

import (
	"encoding/json"
	"strings"

	"github.com/warmbly/warmbly/internal/models"
)

// ComposeFromStrategy builds constrained copy from OutreachStrategy (no freestyle).
// Used as template path and as the deterministic scaffold when AI is unavailable.
func ComposeFromStrategy(st OutreachStrategy, acc *models.OutreachAccount, cand *models.OutreachContactCandidate, channel string) DraftOutput {
	if channel == "" {
		channel = ChannelEmailInitial
	}
	name := ""
	if cand != nil {
		name = strings.TrimSpace(cand.Name)
	}
	greeting := "Olá"
	if name != "" && cand != nil && !isGenericRecipient(cand) {
		greeting = "Olá, " + firstName(name)
	} else if isGenericRecipient(cand) {
		greeting = "Olá, equipe"
	}

	fact := strings.TrimSpace(st.ObservedFact)
	company := ""
	if acc != nil {
		company = firstNonEmpty(acc.NomeFantasia, acc.RazaoSocial)
	}

	// Fail closed commercially when no safe hook: diagnostic path, not institutional pitch or invented facts.
	if containsStr(st.RiskFlags, "no_safe_factual_hook") || fact == "" {
		body := greeting + ",\n\n"
		body += "Escrevo da CONFENGE sobre " + firstNonEmpty(company, "sua empresa") + ".\n\n"
		body += "Sem um fato público específico o bastante neste fio, o caminho diagnóstico costuma começar "
		body += "por um checklist de aditivo ou reajuste a partir do que já está publicado, sem inventar contexto.\n\n"
		if st.CTASuggested != "" {
			body += st.CTASuggested
		} else {
			body += "Posso te mandar o checklist diagnóstico do que eu conferiria primeiro?"
		}
		return DraftOutput{
			Channel: channel, Subject: trimSubject(firstNonEmpty(company, "Contrato público")),
			BodyText:    emDashRe.ReplaceAllString(body, ","),
			FactUsed:    "insufficient_public_fact",
			EvidenceIDs: st.EvidenceIDs, ServiceCode: st.ServiceCode,
			Question: st.CTASuggested, CTA: st.CTASuggested,
			RiskFlags: append([]string{"needs_review", "no_safe_factual_hook", "strategy_compose"}, st.RiskFlags...),
			Rationale: "strategy-compose: no safe factual hook; diagnostic fail-closed (no institutional pitch)",
		}
	}

	cta := st.CTASuggested
	if cta == "" {
		cta = "Posso te mandar os pontos que eu conferiria?"
	}
	// Challenger experiment variant: softer interest CTA
	if st.Experiment != nil && st.Experiment.VariantID == "challenger" && st.Experiment.Dimension == ExpDimCTA {
		cta = "Faz sentido eu te enviar o recorte que encontrei?"
	}

	var body string
	switch st.SequencePosition {
	case 2:
		body = greeting + ",\n\n"
		body += "Outro ângulo sobre " + fact + ": "
		if st.ImplicationHypothesis != "" {
			body += st.ImplicationHypothesis
		} else {
			body += "o que muda na prática é a documentação que passa a importar agora."
		}
		body += "\n\n" + cta
		// Follow-ups stay in-thread when channel is EMAIL_FOLLOWUP (subject set below).
	case 3:
		body = greeting + ",\n\n"
		body += "Se for útil, um mini-checklist objetivo a partir de " + fact + ":\n"
		body += "1) confirmar o trecho público relevante;\n"
		body += "2) separar fato de hipótese;\n"
		body += "3) reunir memórias antes de qualquer pedido formal.\n\n"
		body += cta
	case 4:
		body = greeting + ",\n\n"
		body += "Sobre " + fact + ", quem na equipe acompanha este tema no dia a dia?\n\n"
		body += "Se não for você, um encaminhamento curto já ajuda."
	case 5:
		body = greeting + ",\n\n"
		body += "Parece que não é prioridade agora; encerro por aqui para não ocupar sua caixa.\n\n"
		body += "Se fizer sentido no futuro, é só responder este fio."
	default:
		// Touch 1 SIGNAL: fact → hypothesis → insight → micro-offer
		body = greeting + ",\n\n"
		// Public-source phrasing when natural
		body += "Pelo que está público, " + ensureLowerStart(fact) + ".\n\n"
		if st.ProblemHypothesis != "" {
			body += "Isso não prova crédito sozinho, mas " + ensureLowerStart(st.ProblemHypothesis) + ".\n\n"
		} else if st.CommercialReframe != "" {
			body += truncateRunesOffer(st.CommercialReframe, 180) + "\n\n"
		}
		// Role-light: no mini-CV
		if st.BuyerRole == "LEGAL" {
			body += "No plano técnico-documental (complementar ao jurídico), "
		} else if st.AccountArchetype == "robust" {
			body += "Como segunda leitura pontual, "
		}
		body += cta
	}

	body = emDashRe.ReplaceAllString(body, ",")
	subj := strategySubject(st, company, fact)
	if channel == ChannelEmailFollowup || st.SequencePosition >= 2 {
		// Same-thread convention for follow-ups (tests + operator UX).
		if company != "" {
			subj = trimSubject("Re: " + company)
		} else {
			subj = "Re: conversa anterior"
		}
	}

	return DraftOutput{
		Channel: channel, Subject: subj, BodyText: body,
		FactUsed: fact, EvidenceIDs: st.EvidenceIDs,
		Claims:      claimsFromFact(fact, st.EvidenceIDs),
		ServiceCode: st.ServiceCode, Question: cta, CTA: cta,
		RiskFlags: append([]string{"strategy_compose"}, st.RiskFlags...),
		Rationale: "composed from OutreachStrategy " + st.DoctrineVersion + " offer=" + st.MicroOfferCode,
	}
}

func strategySubject(st OutreachStrategy, company, fact string) string {
	// Short, natural, context-linked; no fake Re, no urgency
	if containsStr(st.RiskFlags, "annualidade_verify_only") {
		return "Reajuste contratual"
	}
	if st.ActivationTrigger != "" {
		switch strings.ToUpper(st.ActivationTrigger) {
		case "ADITIVO_RECENTE", "ADITIVO":
			return trimSubject("Aditivo " + firstNonEmpty(company, ""))
		case "MEDICAO", "MEDICOES":
			return trimSubject("Medições " + firstNonEmpty(company, ""))
		case "ENCERRAMENTO":
			return "Encerramento contratual"
		}
	}
	if fact != "" && hasConcreteToken(fact) {
		return trimSubject(firstWords(fact, 4))
	}
	if company != "" {
		return trimSubject("Contrato " + company)
	}
	return "Contrato público"
}

func ensureLowerStart(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	r := []rune(s)
	// Avoid double-capital awkwardness mid-sentence; keep if looks like proper noun acronym
	if len(r) > 0 && r[0] >= 'A' && r[0] <= 'Z' {
		// keep as-is for names; for long sentences lower first if second is lower
		if len(r) > 1 && r[1] >= 'a' && r[1] <= 'z' {
			r[0] = rune(strings.ToLower(string(r[0]))[0])
		}
	}
	return string(r)
}

// draftSystemPromptDoctrine extends the system prompt with doctrine constraints.
func draftSystemPromptDoctrine(channel string, st OutreachStrategy) string {
	base := draftSystemPrompt(channel)
	base += "\n\nDOUTRINA " + OutreachDoctrineVersion + ":\n"
	base += "- Gere copy SOMENTE após a estratégia JSON; não invente fatos.\n"
	base += "- hypothesis != fact; use linguagem hipotética quando indicado.\n"
	base += "- Primeiro email: micro-oferta / permissão; NÃO peça reunião por padrão.\n"
	base += "- Sem pitch institucional, sem lista de serviços, sem mini-CV.\n"
	base += "- KNOW A LOT / SAY LITTLE; prefira 'pelo contrato publicado' a 'estamos monitorando'.\n"
	base += "- CTA alinhado a: " + st.CTAType + " / offer " + st.MicroOfferCode + "\n"
	base += "- claims_to_avoid são proibições absolutas.\n"
	if containsStr(st.RiskFlags, "annualidade_verify_only") {
		base += "- ANUALIDADE: nunca diga que há reajuste a receber; só verificação/checklist.\n"
	}
	if containsStr(st.RiskFlags, "generic_recipient") {
		base += "- DESTINATÁRIO GENÉRICO: trate como equipe; nunca use nome, cargo ou saudação pessoal.\n"
	}
	return base
}

// draftUserPromptWithStrategy includes strategy object before dossier.
func draftUserPromptWithStrategy(in GenerateInput, st OutreachStrategy) string {
	// Reuse dossier builder then prepend strategy.
	dossier := draftUserPrompt(in)
	b, _ := json.MarshalIndent(st, "", "  ")
	return "ESTRATÉGIA COMERCIAL (obrigatória; a copy deve segui-la):\n" + string(b) + "\n\n" + dossier
}

// ApplyStrategyToGenerateInput is a marker that generation was strategy-first.
func StrategyCodeFor(st OutreachStrategy) string {
	parts := []string{st.DoctrineVersion, st.MicroOfferCode, st.CTAType}
	if st.SequenceTouchName != "" {
		parts = append(parts, st.SequenceTouchName)
	}
	return strings.Join(parts, "/")
}

// PackValidationWithStrategy marshals validation including strategy explain for UI.
func PackValidationWithStrategy(val ValidationResult, st OutreachStrategy, recipient string) []byte {
	val.DoctrineVersion = st.DoctrineVersion
	val.Strategy = &st
	ex := ExplainStrategy(st, recipient)
	val.StrategyExplain = &ex
	b, _ := json.Marshal(val)
	return b
}
