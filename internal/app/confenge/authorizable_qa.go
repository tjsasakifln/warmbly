package confenge

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/warmbly/warmbly/internal/models"
)

// ApplyAuthorizableHardQA is the #70 catalog: validation_ok means only human
// authorization remains. These fails never enter NEEDS_REVIEW.
func ApplyAuthorizableHardQA(res *ValidationResult, out *DraftOutput, acc *models.OutreachAccount, cand *models.OutreachContactCandidate, opts ValidateOpts, channel, body string) {
	if res == nil || out == nil {
		return
	}
	subj := strings.TrimSpace(out.Subject)
	blob := subj + "\n" + body
	fail := func(msg string) {
		res.OK = false
		res.Errors = appendUnique(res.Errors, msg)
	}

	if LooksLikeInternalReasoning(blob) {
		fail("internal reasoning leaked into copy")
	}
	if rationaleLeaked(out.Rationale, body) || strategyExplainLeaked(opts.Strategy, blob) {
		fail("internal rationale leaked into body_text")
	}
	for _, p := range cataloguedLeakPhrases {
		if p != "" && containsFolded(blob, p) {
			fail("catalogued operator phrase leaked: " + p)
		}
	}
	if looksLikeMetadataDump(blob) || containsDumpLabel(blob) {
		fail("metadata dump in copy")
	}
	if looksMidTokenTruncation(subj) || looksMidTokenTruncation(body) {
		fail("copy truncates mid-word or mid-phrase")
	}
	if isContratoCompanySubject(subj, accountCompany(acc)) {
		fail("subject is Contrato {razao social}")
	}
	if subjectHasLegalForm(subj) {
		fail("subject includes legal form or recuperacao judicial")
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(subj)), "objeto:") {
		fail("subject starts with objeto:")
	}
	if creditWordIn(blob) && !CreditVocabAllowed(opts.Playbook, firstNonEmpty(out.ServiceCode, accService(acc))) {
		fail("economic_or_legal_claim_language")
	}
	for _, f := range out.RiskFlags {
		switch f {
		case FlagComposerStale:
			fail("composer_version_stale")
		case FlagRequiresRegen:
			fail("requires_regeneration")
		}
	}
	if !CurrentComposerPrompt(firstNonEmpty(opts.PromptVersion, PromptVersion)) && strings.TrimSpace(body) != "" {
		fail("composer_version_stale")
	}
	if outOfICPEngineeringLanguage(acc, blob) {
		fail("out-of-icp engineering language")
	}
	if cand != nil && !opts.SkipEmailRecipient && !IsWhatsAppChannel(channel) {
		if models.OutreachUnenrollableVerification[cand.VerificationStatus] || cand.VerificationStatus == models.OutreachVerifyInstitutionalGeneric {
			fail("recipient fails the email lane gate")
		}
		if isGenericRecipient(cand) || isRoleMailbox(cand) {
			fail("generic or role mailbox is not send-NEEDS_REVIEW")
		}
	}
}

func rationaleLeaked(rationale, body string) bool {
	r := strings.TrimSpace(rationale)
	if r == "" || strings.TrimSpace(body) == "" {
		return false
	}
	if utf8.RuneCountInString(r) > 24 {
		r = string([]rune(r)[:24])
	}
	return r != "" && strings.Contains(body, r)
}

func strategyExplainLeaked(st *OutreachStrategy, blob string) bool {
	if st == nil {
		return false
	}
	for _, s := range []string{st.ProblemHypothesis, st.ImplicationHypothesis, st.CommercialReframe} {
		s = strings.TrimSpace(s)
		if utf8.RuneCountInString(s) < 16 {
			continue
		}
		sample := s
		if utf8.RuneCountInString(sample) > 28 {
			sample = string([]rune(sample)[:28])
		}
		if sample != "" && strings.Contains(blob, sample) {
			return true
		}
	}
	return false
}

func containsDumpLabel(s string) bool {
	low := foldASCII(s)
	for _, lab := range []string{"objeto:", "orgao:", "órgão:", "uf:", "cnpj:"} {
		if strings.Contains(low, foldASCII(lab)) {
			return true
		}
	}
	return false
}

func isContratoCompanySubject(subj, company string) bool {
	low := strings.TrimSpace(foldASCII(subj))
	if !strings.HasPrefix(low, "contrato ") && low != "contrato" && low != "contrato publico" {
		return false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(low, "contrato"))
	rest = strings.TrimSpace(rest)
	if rest == "" || rest == "publico" {
		return true
	}
	if company != "" {
		c := foldASCII(company)
		if strings.Contains(rest, c) || strings.Contains(c, rest) {
			return true
		}
	}
	return subjectHasLegalForm(subj)
}

func subjectHasLegalForm(subj string) bool {
	t := foldASCII(subj)
	for _, p := range []string{" ltda", " eireli", " s/a", " s.a.", "recuperacao judicial", "em recuperacao"} {
		if strings.Contains(t, p) {
			return true
		}
	}
	return false
}

func looksMidTokenTruncation(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for _, cut := range cataloguedTruncations {
		if strings.Contains(s, cut) {
			return true
		}
	}
	// Last token ends with a hanging semicolon after a short fragment.
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return false
	}
	last := strings.TrimSpace(fields[len(fields)-1])
	if strings.HasSuffix(last, ";") {
		tok := strings.TrimSuffix(last, ";")
		tok = strings.Trim(tok, ".,)")
		if tok != "" && utf8.RuneCountInString(tok) <= 4 && !isLikelyAcronym(tok) {
			return true
		}
	}
	runes := []rune(last)
	if len(runes) >= 6 && !strings.HasSuffix(last, ".") && !strings.HasSuffix(last, "?") {
		// Incomplete Portuguese ending: ...aç / ...ç / ...ment
		low := foldASCII(last)
		if strings.HasSuffix(low, "c") && strings.Contains(low, "caracteriz") {
			return true
		}
	}
	return false
}

func isLikelyAcronym(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if unicode.IsLetter(r) && !unicode.IsUpper(r) {
			return false
		}
	}
	return true
}

func outOfICPEngineeringLanguage(acc *models.OutreachAccount, blob string) bool {
	if acc == nil || !looksOutsideConstructionICP(acc) {
		return false
	}
	t := foldASCII(blob)
	for _, p := range []string{
		"contratos publicos de engenharia",
		"contratos de engenharia/construcao",
		"contratos de engenharia",
		"obra de engenharia",
		"construcao civil",
	} {
		if strings.Contains(t, p) {
			return true
		}
	}
	return false
}

func looksOutsideConstructionICP(acc *models.OutreachAccount) bool {
	if acc == nil {
		return false
	}
	name := foldASCII(acc.RazaoSocial + " " + acc.NomeFantasia + " " + acc.FactToMention)
	for _, p := range []string{"imoveis", "imobili", "farmac", "comercio", "oficina", "dealer", "clinica", "hotel"} {
		if strings.Contains(name, p) {
			return true
		}
	}
	if acc.TargetFitSendTier == "SUPPRESSED" || acc.TargetFitClass == "OUT_OF_ICP" {
		return true
	}
	return false
}

func accountCompany(acc *models.OutreachAccount) string {
	if acc == nil {
		return ""
	}
	return firstNonEmpty(acc.NomeFantasia, acc.RazaoSocial)
}

func containsFolded(hay, needle string) bool {
	return needle != "" && strings.Contains(foldASCII(hay), foldASCII(needle))
}

var cataloguedLeakPhrases = []string{
	"eventos publicos relevantes sem triagem",
	"eventos públicos relevantes sem triagem",
	"como segunda leitura / validacao independente, sem presumir falta de capacidade interna",
	"como segunda leitura / validação independente, sem presumir falta de capacidade interna",
	"sem presumir falta de capacidade interna",
	"premissas de edital subavaliadas",
	"criterio de medicao ambiguo em trecho critico",
	"critério de medição ambíguo em trecho crítico",
	"indice aplicavel nao formalizado no prazo esperado",
	"índice aplicável não formalizado no prazo esperado",
	"isso nao prova credito sozinho",
	"isso não prova crédito sozinho",
	"como segunda leitura pontual",
	"segunda leitura pontual",
}

var cataloguedTruncations = []string{
	"(C.B;", "Parl;", "por po;", "as fa;", "PR-466, I;",
	"CAIADO FR;", "na zona u;", "do M;", "complementa;",
	"caracterizaç", "PLUVIAI;", "Conego Rodolf;",
}
