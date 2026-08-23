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

// ensureClosingAsk keeps the mail ending in a real question. Portuguese
// declaratives do not become questions by swapping the final period for "?",
// so a short ask is appended instead of coercing the punctuation.
func ensureClosingAsk(cta string) string {
	cta = strings.TrimSpace(cta)
	if cta == "" || strings.Contains(cta, "?") {
		return cta
	}
	if !strings.HasSuffix(cta, ".") {
		cta += "."
	}
	return cta + " Faz sentido para você?"
}

// referralAsk keeps a departmental pitch honest when no person is proven: the
// offer still stands, but the reader is not assumed to own the subject.
const referralAsk = "Se não for com você, pode me indicar a pessoa certa?"

func appendReferralAsk(cta string) string {
	cta = strings.TrimSpace(cta)
	if cta == "" {
		return referralAsk
	}
	blob := foldASCII(cta)
	if strings.Contains(blob, "indicar a pessoa") || strings.Contains(blob, "pessoa certa") ||
		strings.Contains(blob, "encaminhar") || strings.Contains(blob, "pessoa responsavel") {
		return cta
	}
	if !strings.HasSuffix(cta, "?") && !strings.HasSuffix(cta, ".") {
		cta += "."
	}
	return cta + " " + referralAsk
}

// ComposedInitial is what the composer decided, not only what it printed. The
// founder review has to judge the observed fact and the ask on their own, and
// neither survives inside BodyText in a form that can be read back without
// re-parsing prose.
type ComposedInitial struct {
	Subject  string
	Body     string
	Greeting string
	// ObservedFact is empty whenever the composer printed no fact. FactSource
	// says which kind of empty it is, so "the feed carried nothing" is never
	// silently shown as "the composer refused what the feed carried".
	ObservedFact string
	FactSource   string
	CTA          string
	CTASource    string
	Theme        string
}

// Closed set of fact provenances. A fact the composer refused to print is a
// different founder signal from a fact that was never in the feed.
const (
	FactSourceFactToMention = "fact_to_mention"
	FactSourceMomentSummary = "moment_summary"
	FactSourceDropped       = "dropped_unusable"
	FactSourceNone          = "none"
)

// Closed set of ask provenances. A routing ask is the composer's own words for
// a mailbox with no owner; an account CTA came from the feed.
const (
	CTASourceAccount = "account_cta"
	CTASourceRouting = "routing_ask"
	CTASourceDefault = "composer_default"
)

// ComposeControlledInitial is the freeze-time composer. It never invents a
// person. GENERIC/FREEMAIL copy is institutional and may ask for a route.
func ComposeControlledInitial(acc *models.OutreachAccount, cand *models.OutreachContactCandidate, class string) (subject, body, greeting string) {
	c := ComposeControlledInitialDetailed(acc, cand, class)
	return c.Subject, c.Body, c.Greeting
}

// ComposeControlledInitialDetailed composes exactly what ComposeControlledInitial
// composes and additionally reports the inputs the copy was built from, so the
// preview can show the fact and the ask without guessing them back out of prose.
func ComposeControlledInitialDetailed(acc *models.OutreachAccount, cand *models.OutreachContactCandidate, class string) ComposedInitial {
	out := ComposedInitial{
		Greeting:   greetingForRouteClass(class, cand),
		FactSource: FactSourceNone,
		CTASource:  CTASourceDefault,
	}
	obs := ""
	cta := "Posso te mandar o recorte objetivo que eu conferiria?"
	theme := "contratos públicos"
	if acc != nil {
		if s := strings.TrimSpace(acc.FactToMention); s != "" {
			obs, out.FactSource = s, FactSourceFactToMention
		} else if s := strings.TrimSpace(acc.MomentSummary); s != "" {
			obs, out.FactSource = s, FactSourceMomentSummary
		}
		if strings.TrimSpace(acc.CTA) != "" {
			cta = ensureClosingAsk(strings.TrimSpace(acc.CTA))
			out.CTASource = CTASourceAccount
		}
		theme = firstNonEmpty(humanizeMoment(acc.MomentCode), acc.ServiceName, theme)
	}
	fedAFact := out.FactSource != FactSourceNone
	obs = ApplyCopyHygiene(obs)
	// A serialized feed record is not prose. Condense it, and drop it entirely
	// rather than pasting a database row into a cold email.
	if looksLikeMetadataDump(obs) || containsDumpLabel(obs) {
		obs = condenseMetadataFact(obs)
		if looksLikeMetadataDump(obs) || containsDumpLabel(obs) {
			obs = ""
		}
	}
	// Hygiene can also empty a fact that arrived non-empty. Reporting that as
	// "none" would tell the founder the feed carried no fact, which is false.
	if obs == "" && fedAFact {
		out.FactSource = FactSourceDropped
	}
	switch class {
	case RouteClassGenericCompany, RouteClassPublicCompanyFreemail:
		cta = routingCTA(theme)
		out.CTASource = CTASourceRouting
	case RouteClassRoleOrDepartment:
		// A department mailbox is the right door, not proof of the right person.
		if CandidatePersonUnknown(cand) {
			cta = appendReferralAsk(cta)
		}
	}
	var b strings.Builder
	b.WriteString(out.Greeting)
	b.WriteString(",\n\nSou da CONFENGE.\n\n")
	if obs != "" {
		// A fact cut at a comma used to gain a period and read "Sapucaí,.".
		obs = terminateFactSentence(obs)
		b.WriteString(obs)
		b.WriteString("\n\n")
	}
	b.WriteString(cta)
	out.ObservedFact = obs
	out.CTA = cta
	out.Theme = theme
	out.Body = b.String()
	out.Subject = controlledSubject(acc, obs, theme)
	return out
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
	if subjectHasLegalForm(raw) || containsDumpLabel(raw) {
		return firstNonEmpty(theme, "Uma leitura objetiva")
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
	// Rune-safe and on a word boundary: a byte slice can split a multi-byte
	// rune into an invalid MIME Subject header, and cuts mid-word read broken.
	return cutAtWordBoundary(obs, 64)
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
	case "PORTFOLIO_REVIEW":
		return "a carteira de contratos públicos"
	case "ADDENDUM":
		return "aditivos"
	case "GLOSA_MEDICAO":
		return "glosas de medição"
	case "REEQUILIBRIO":
		return "reequilíbrio econômico-financeiro"
	default:
		// Closed on purpose: an unmapped code must fall through to ServiceName
		// or the default theme, never reach copy as a raw enum.
		return ""
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
