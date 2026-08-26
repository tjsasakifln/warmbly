package confenge

import (
	"strings"

	"github.com/warmbly/warmbly/internal/models"
)

// DelegatedFirstTouchCopyRulesV1 names the editorial contract used both by the
// runway worker and by corpus QA. Concentration is interpreted through these
// factual dimensions, never through a target percentage of cosmetic variety.
const DelegatedFirstTouchCopyRulesV1 = "confenge.first-touch-copy-rules.v1"

const delegatedContactExit = "Se preferir não receber meus contatos, é só me avisar."

type delegatedRoutingCopy struct {
	Subject             string
	Body                string
	Opening             string
	OpeningKey          string
	Practice            string
	PracticeKey         string
	CTA                 string
	CTAKey              string
	SubjectKey          string
	FactUsed            string
	FactKey             string
	FactEvidenceIDs     []string
	RouteClass          string
	RecipientPurposeKey string
	PersonUsed          string
	SemanticSignature   string
}

type delegatedFactProjection struct {
	Phrase      string
	Subject     string
	Key         string
	EvidenceIDs []string
}

// composeDelegatedRoutingCopy keeps the legacy two-string boundary used by the
// worker while the typed projection remains available to QA and corpus tests.
func composeDelegatedRoutingCopy(acc *models.OutreachAccount, cand *models.OutreachContactCandidate, evidence []models.OutreachEvidence) (string, string) {
	copy := buildDelegatedRoutingCopy(acc, cand, evidence)
	return copy.Subject, copy.Body
}

func buildDelegatedRoutingCopy(acc *models.OutreachAccount, cand *models.OutreachContactCandidate, evidence []models.OutreachEvidence) delegatedRoutingCopy {
	if acc == nil || cand == nil {
		return delegatedRoutingCopy{}
	}
	company := editorialCompanyName(acc)
	if company == "" {
		return delegatedRoutingCopy{}
	}

	routeClass := CandidateRouteClass(cand)
	purposeKey, purposeLabel := delegatedRecipientPurpose(cand)
	person := ""
	greeting := "Olá"
	if routeClass == RouteClassDirectPerson && composerMaySeePersonName(cand) {
		person = titleFirstName(firstName(cand.Name))
		if person != "" {
			greeting += ", " + person
		}
	}

	fact := delegatedSupportedFact(acc, evidence)
	opening := "Vi em registro público que a " + company + " aparece como contratada no setor público."
	openingKey := "SUPPLIER_ROLE"
	subject := company + " no setor público"
	subjectKey := normalizeForCorpus(subject)
	factUsed := "Atuação como contratada no setor público confirmada."
	factKey := "SUPPLIER_ROLE"
	if fact.Phrase != "" {
		opening = "Vi a " + company + " como contratada em " + fact.Phrase + "."
		openingKey = "CONTRACT_FACT"
		subject = delegatedFactSubject(company, fact.Subject)
		subjectKey = normalizeForCorpus(subject)
		factUsed = "Atuação como contratada em " + fact.Phrase + "."
		factKey = fact.Key
	}

	practice := practiceForAccount(acc)
	cta, ctaKey := delegatedCTA(routeClass, purposeKey, purposeLabel)
	if cta == "" {
		return delegatedRoutingCopy{}
	}
	body := greeting + ",\n\n" +
		"Sou Tiago Sasaki, da CONFENGE. " + opening + "\n\n" +
		practice + ". " + cta + "\n\n" +
		delegatedContactExit + "\n\n" +
		"Obrigado,\nTiago Sasaki\nCONFENGE\ntiago.sasaki@confenge.com.br"

	semantic := strings.Join([]string{
		DelegatedFirstTouchCopyRulesV1,
		factKey,
		normalizeForCorpus(practice),
		routeClass,
		purposeKey,
	}, "|")
	return delegatedRoutingCopy{
		Subject: subject, Body: body,
		Opening: opening, OpeningKey: openingKey,
		Practice: practice, PracticeKey: normalizeForCorpus(practice),
		CTA: cta, CTAKey: ctaKey,
		SubjectKey: subjectKey,
		FactUsed:   factUsed, FactKey: factKey, FactEvidenceIDs: append([]string{}, fact.EvidenceIDs...),
		RouteClass: routeClass, RecipientPurposeKey: purposeKey, PersonUsed: person,
		SemanticSignature: hashText(semantic),
	}
}

func delegatedFactSubject(company, factSubject string) string {
	detail := strings.TrimSpace(factSubject)
	detail = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(detail, "Sobre "), "sobre "))
	candidate := strings.TrimSpace(company + " e " + detail)
	if detail == "" || len([]rune(candidate)) > 100 {
		return company + " no setor público"
	}
	return candidate
}

func delegatedRecipientPurpose(cand *models.OutreachContactCandidate) (string, string) {
	if cand == nil {
		return "UNKNOWN", ""
	}
	purpose := strings.ToUpper(strings.TrimSpace(cand.MailboxPurpose))
	role := foldASCII(strings.ToLower(strings.TrimSpace(cand.Role)))
	switch {
	case strings.Contains(purpose, "LICIT") || strings.Contains(role, "licit"):
		return "LICITACOES", "licitações"
	case strings.Contains(purpose, "CONTRAT") || strings.Contains(role, "contrat"):
		return "CONTRATOS", "contratos"
	case strings.Contains(purpose, "COMERCIAL") || strings.Contains(role, "comercial"):
		return "COMERCIAL", "comercial"
	case purpose == "ROLE_MAILBOX":
		return "ROLE_MAILBOX", ""
	case purpose == "PERSONAL_WORK":
		return "PERSONAL_WORK", ""
	case purpose == "GENERIC_CONTACT" || purpose == "GENERIC":
		return "GENERIC_CONTACT", ""
	case purpose != "":
		return purpose, ""
	default:
		return "UNKNOWN", ""
	}
}

func delegatedCTA(routeClass, purposeKey, purposeLabel string) (string, string) {
	switch routeClass {
	case RouteClassDirectPerson:
		return "Essa frente passa por você?", "DIRECT_OWNER_CHECK"
	case RouteClassRoleOrDepartment:
		if purposeLabel != "" {
			return "Essa frente fica com a área de " + purposeLabel + " ou devo procurar outra?", "ROLE_PURPOSE_CHECK:" + purposeKey
		}
		return "Essa frente fica com a sua área ou devo procurar outra?", "ROLE_AREA_CHECK"
	case RouteClassGenericCompany, RouteClassPublicCompanyFreemail:
		return "Você consegue encaminhar esta mensagem a quem cuida dessa frente?", "GENERIC_FORWARD"
	default:
		return "", ""
	}
}

// delegatedSupportedFact uses only a fact already projected by extra-cli and
// bound to a current CONFIRMED_FACT row. Raw contracts are deliberately not
// parsed here: buyer/supplier interpretation remains upstream authority.
func delegatedSupportedFact(acc *models.OutreachAccount, evidence []models.OutreachEvidence) delegatedFactProjection {
	if acc == nil {
		return delegatedFactProjection{}
	}
	raw := strings.TrimSpace(acc.FactToMention)
	low := foldASCII(strings.ToLower(raw))
	if raw == "" || delegatedDigitPattern.MatchString(raw) || delegatedContainsAny(low,
		"atuacao como contratada", "empresa contratada", "fornecedora", "portfolio publico observado",
		"sem prova", "hipotese", "pode haver", "lead", "score") {
		return delegatedFactProjection{}
	}
	if looksLikeMetadataDump(raw) || containsDumpLabel(raw) || qaEnumRe.MatchString(raw) ||
		qaKeyValueRe.MatchString(raw) || qaScoreRe.MatchString(raw) {
		return delegatedFactProjection{}
	}
	digest := DigestPublicFact(raw)
	if digest.Phrase == "" || digest.Relation == FactRelationCompanyContext || len(strings.Fields(digest.Phrase)) > 16 {
		return delegatedFactProjection{}
	}
	if len(factPhraseDefects(digest.Phrase)) > 0 || delegatedContainsAny(foldASCII(strings.ToLower(digest.Phrase)),
		"credito", "reequilibrio", "irregular", "inadimpl", "litig", "sancao", "conden") {
		return delegatedFactProjection{}
	}

	candidateIDs := append([]string{}, acc.MomentEvidenceIDs...)
	candidateIDs = append(candidateIDs, acc.ContractorRoleEvidenceIDs...)
	ids := delegatedEvidenceSupportingFact(raw, digest.Phrase, candidateIDs, evidence)
	if len(ids) == 0 {
		return delegatedFactProjection{}
	}
	return delegatedFactProjection{
		Phrase: digest.Phrase, Subject: digest.Subject,
		Key: "FACT:" + normalizeForCorpus(digest.Phrase), EvidenceIDs: ids,
	}
}

func delegatedEvidenceSupportingFact(raw, phrase string, candidateIDs []string, evidence []models.OutreachEvidence) []string {
	wanted := map[string]bool{}
	for _, id := range candidateIDs {
		if id = strings.TrimSpace(id); id != "" {
			wanted[id] = true
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	var supported []string
	for i := range evidence {
		e := evidence[i]
		id := strings.TrimSpace(e.SourceEvidenceID)
		if !wanted[id] || !strings.EqualFold(strings.TrimSpace(e.EpistemicClass), models.OutreachEpistemicConfirmedFact) {
			continue
		}
		blob := strings.Join([]string{e.Title, e.Excerpt, e.Synthesis}, " ")
		if delegatedEvidenceTextSupports(blob, raw, phrase) {
			supported = appendUnique(supported, id)
		}
	}
	return supported
}

var delegatedFactStopWords = map[string]bool{
	"para": true, "pela": true, "pelo": true, "como": true, "empresa": true,
	"contratacao": true, "contratada": true, "servico": true, "servicos": true,
	"obra": true, "obras": true, "publico": true, "publica": true, "publicos": true,
}

func delegatedEvidenceTextSupports(blob, raw, phrase string) bool {
	blob = normalizeForCorpus(blob)
	if blob == "" {
		return false
	}
	if normalizedRaw := normalizeForCorpus(raw); len([]rune(normalizedRaw)) >= 12 && strings.Contains(blob, normalizedRaw) {
		return true
	}
	hits := 0
	seen := map[string]bool{}
	for _, word := range strings.Fields(normalizeForCorpus(phrase)) {
		word = strings.Trim(word, ".,;:!?()[]\"'")
		if len([]rune(word)) < 5 || delegatedFactStopWords[word] || seen[word] {
			continue
		}
		seen[word] = true
		if strings.Contains(" "+blob+" ", " "+word+" ") {
			hits++
		}
	}
	return hits >= 2
}
