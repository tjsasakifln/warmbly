package confenge

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/warmbly/warmbly/internal/models"
)

var greetingAddresseeRe = regexp.MustCompile(`(?i)(?:ol[áa]|prezad[oa]|dear)\s*,?\s+([A-Za-zÁ-ÿ]+)`)

var teamGreetingTokens = map[string]bool{
	"equipe": true, "time": true, "pessoal": true, "departamento": true,
	"setor": true, "area": true, "voce": true, "voces": true,
	"comercial": true, "licitacoes": true, "contratos": true, "compras": true,
	"financeiro": true, "financeira": true, "responsavel": true,
}

func greetingForRouteClass(class string, cand *models.OutreachContactCandidate) string {
	switch class {
	case RouteClassDirectPerson:
		if cand != nil && provenPersonName(cand) && !CandidatePersonUnknown(cand) {
			return "Olá, " + titleFirstName(firstName(cand.Name))
		}
		return "Olá"
	case RouteClassRoleOrDepartment:
		return teamGreeting(cand)
	case RouteClassGenericCompany, RouteClassPublicCompanyFreemail:
		return "Olá, equipe"
	default:
		return "Olá"
	}
}

func teamGreeting(cand *models.OutreachContactCandidate) string {
	purpose := ""
	local := ""
	if cand != nil {
		purpose = cand.MailboxPurpose
		local = emailLocal(cand.Email)
	}
	blob := foldASCII(purpose + " " + local)
	switch {
	case strings.Contains(blob, "licitac"):
		return "Olá, pessoal de licitações"
	case strings.Contains(blob, "contrato"):
		return "Olá, equipe responsável por contratos"
	case strings.Contains(blob, "compra"):
		return "Olá, equipe de compras"
	case strings.Contains(blob, "financ"):
		return "Olá, equipe financeira"
	case strings.Contains(blob, "comercial") || strings.Contains(blob, "vendas") || strings.Contains(blob, "sales"):
		return "Olá, equipe comercial"
	default:
		return "Olá, equipe"
	}
}

func routingCTA(theme string) string {
	theme = strings.TrimSpace(theme)
	if theme == "" {
		theme = "este tema"
	}
	return "Queria falar com quem acompanha " + ensureLowerStart(theme) + " por aí. Você consegue me indicar a pessoa responsável?"
}

// ComposeControlledInitial is the freeze-time composer. It never invents a
// person. GENERIC/FREEMAIL copy is institutional and may ask for a route.
func ComposeControlledInitial(acc *models.OutreachAccount, cand *models.OutreachContactCandidate, class string) (subject, body, greeting string) {
	greeting = greetingForRouteClass(class, cand)
	obs := ""
	cta := "Posso te mandar o recorte objetivo que eu conferiria?"
	theme := "contratos públicos"
	if acc != nil {
		obs = firstNonEmpty(strings.TrimSpace(acc.FactToMention), strings.TrimSpace(acc.MomentSummary))
		if strings.TrimSpace(acc.CTA) != "" {
			cta = strings.TrimSpace(acc.CTA)
			if !strings.Contains(cta, "?") {
				cta = strings.TrimRight(cta, ". ") + "?"
			}
		}
		theme = firstNonEmpty(humanizeMoment(acc.MomentCode), acc.ServiceName, theme)
	}
	obs = ApplyCopyHygiene(obs)
	switch class {
	case RouteClassGenericCompany, RouteClassPublicCompanyFreemail:
		cta = routingCTA(theme)
	}
	var b strings.Builder
	b.WriteString(greeting)
	b.WriteString(",\n\nSou da CONFENGE.\n\n")
	if obs != "" {
		b.WriteString(obs)
		if !strings.HasSuffix(strings.TrimSpace(obs), ".") {
			b.WriteByte('.')
		}
		b.WriteString("\n\n")
	}
	b.WriteString(cta)
	body = b.String()
	subject = controlledSubject(acc, obs, theme)
	return subject, body, greeting
}

func controlledSubject(acc *models.OutreachAccount, obs, theme string) string {
	raw := firstNonEmpty(trimSubjectFact(obs), theme, "Uma leitura objetiva")
	raw = ApplyCopyHygiene(raw)
	if acc != nil {
		company := firstNonEmpty(acc.NomeFantasia, acc.RazaoSocial)
		if isContratoCompanySubject(raw, company) || subjectHasLegalForm(raw) {
			raw = firstNonEmpty(theme, "Uma leitura objetiva")
		}
	}
	if subjectHasLegalForm(raw) {
		return "Uma leitura objetiva"
	}
	return strings.TrimSpace(raw)
}

func trimSubjectFact(obs string) string {
	obs = strings.TrimSpace(obs)
	if obs == "" {
		return ""
	}
	if i := strings.IndexAny(obs, ".!?"); i > 12 && i < 80 {
		return obs[:i]
	}
	if len(obs) > 80 {
		return strings.TrimSpace(obs[:80])
	}
	return obs
}

func humanizeMoment(code string) string {
	c := strings.ToUpper(strings.TrimSpace(code))
	switch c {
	case "CONTRACT_EXTENSION", "ADITIVO":
		return "aditivos e reajuste"
	case "LICITACAO", "LICITAÇÃO", "EDITAL":
		return "licitações"
	case "REAJUSTE", "REAJUSTE_14133":
		return "reajuste contratual"
	default:
		if c == "" {
			return ""
		}
		return strings.ToLower(strings.ReplaceAll(c, "_", " "))
	}
}

func looksInventedPersonGreeting(blob string) bool {
	blob = strings.ToLower(blob)
	m := greetingAddresseeRe.FindStringSubmatch(blob)
	if len(m) < 2 {
		return false
	}
	token := foldASCII(m[1])
	if token == "" || teamGreetingTokens[token] {
		return false
	}
	// Structural: a single proper-name-shaped token is invention when the
	// composer has no proven person. Team phrases start with equipe/pessoal.
	if len([]rune(token)) < 3 {
		return false
	}
	return true
}

func composerMaySeePersonName(cand *models.OutreachContactCandidate) bool {
	if cand == nil {
		return false
	}
	if CandidatePersonUnknown(cand) {
		return false
	}
	if CandidateRouteClass(cand) != RouteClassDirectPerson {
		return false
	}
	return provenPersonName(cand)
}

func hasPersonShapedToken(s string) bool {
	for _, word := range strings.Fields(s) {
		w := strings.Trim(word, ",.;:!?")
		if w == "" {
			continue
		}
		runes := []rune(w)
		if len(runes) < 3 {
			continue
		}
		if !unicode.IsUpper(runes[0]) {
			continue
		}
		if teamGreetingTokens[foldASCII(w)] {
			continue
		}
		restLower := true
		for _, r := range runes[1:] {
			if unicode.IsUpper(r) {
				restLower = false
				break
			}
		}
		if restLower && !teamGreetingTokens[foldASCII(w)] {
			return true
		}
	}
	return false
}
