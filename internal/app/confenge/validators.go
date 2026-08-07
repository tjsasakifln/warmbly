package confenge

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/warmbly/warmbly/internal/models"
)

// PromptVersion tags generation prompt revisions.
const PromptVersion = "confenge.draft.v1"

// DraftOutput is the structured generation result (validated before save).
type DraftOutput struct {
	Subject     string          `json:"subject"`
	BodyText    string          `json:"body_text"`
	BodyHTML    string          `json:"body_html"`
	Followups   []DraftFollowup `json:"followups"`
	FactUsed    string          `json:"fact_used"`
	EvidenceIDs []string        `json:"evidence_ids"`
	ServiceCode string          `json:"service_code"`
	Question    string          `json:"question"`
	CTA         string          `json:"cta"`
	RiskFlags   []string        `json:"risk_flags"`
}

// DraftFollowup is one cadence follow-up in the same thread.
type DraftFollowup struct {
	DelayDays   int    `json:"delay_days"`
	SubjectMode string `json:"subject_mode"`
	BodyText    string `json:"body_text"`
	BodyHTML    string `json:"body_html"`
}

// ValidationResult is deterministic pre-send / pre-approve checks.
type ValidationResult struct {
	OK       bool     `json:"ok"`
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// Banned outreach phrases (Portuguese + known AI tells). Lowercased match.
var bannedPhrases = []string{
	"dinheiro a receber",
	"crédito identificado",
	"credito identificado",
	"descobrimos um erro",
	"há irregularidade",
	"ha irregularidade",
	"vocês deixaram de receber",
	"voces deixaram de receber",
	"sua equipe não controla",
	"sua equipe nao controla",
	"falta estrutura",
	"lead quente",
	"alta chance de conversão",
	"alta chance de conversao",
	"espero que esta mensagem o encontre bem",
	"espero que esta mensagem a encontre bem",
	"espero que este e-mail o encontre bem",
	"i hope this email finds you",
	"garantimos",
	"garantia de",
	"100% de sucesso",
}

// emDash and similar are banned in outbound confenge copy.
var emDashRe = regexp.MustCompile(`[\x{2014}\x{2013}]`)

// shortURLRe catches common shorteners (tracking risk).
var shortURLRe = regexp.MustCompile(`(?i)https?://(bit\.ly|t\.co|goo\.gl|tinyurl\.com|ow\.ly)/`)

// ValidateDraft runs deterministic checks before human approval / enrollment.
func ValidateDraft(out *DraftOutput, acc *models.OutreachAccount, cand *models.OutreachContactCandidate, maxWords int) ValidationResult {
	var res ValidationResult
	res.OK = true
	if out == nil {
		res.OK = false
		res.Errors = append(res.Errors, "empty draft")
		return res
	}
	if maxWords <= 0 {
		maxWords = DefaultMaxInitialWords
	}

	email := ""
	if cand != nil {
		email = strings.TrimSpace(cand.Email)
		if !cand.CanEnroll() {
			res.OK = false
			res.Errors = append(res.Errors, "contact is not enrollable (verification, DNC, bounce, or missing email)")
		}
		if cand.DoNotContact {
			res.OK = false
			res.Errors = append(res.Errors, "contact is DO_NOT_CONTACT")
		}
		if cand.Bounced {
			res.OK = false
			res.Errors = append(res.Errors, "contact address bounced")
		}
	} else {
		res.OK = false
		res.Errors = append(res.Errors, "no contact candidate")
	}
	if email == "" {
		res.OK = false
		res.Errors = append(res.Errors, "missing recipient email")
	}

	body := strings.TrimSpace(out.BodyText)
	if body == "" {
		res.OK = false
		res.Errors = append(res.Errors, "empty body")
	}
	if strings.TrimSpace(out.Subject) == "" {
		res.OK = false
		res.Errors = append(res.Errors, "empty subject")
	}

	blob := strings.ToLower(out.Subject + "\n" + body)
	for _, fu := range out.Followups {
		blob += "\n" + strings.ToLower(fu.BodyText)
	}

	if emDashRe.MatchString(out.Subject + body) {
		res.OK = false
		res.Errors = append(res.Errors, "em dash / en dash not allowed in outreach copy")
	}
	for _, fu := range out.Followups {
		if emDashRe.MatchString(fu.BodyText) {
			res.OK = false
			res.Errors = append(res.Errors, "em dash / en dash not allowed in follow-up copy")
			break
		}
	}

	for _, p := range bannedPhrases {
		if strings.Contains(blob, p) {
			res.OK = false
			res.Errors = append(res.Errors, "banned phrase: "+p)
		}
	}

	if shortURLRe.MatchString(body) {
		res.OK = false
		res.Errors = append(res.Errors, "shortened URLs are not allowed")
	}
	if strings.Contains(strings.ToLower(out.BodyHTML), "<script") {
		res.OK = false
		res.Errors = append(res.Errors, "unsafe HTML (script) not allowed")
	}

	words := countWords(body)
	if words > maxWords {
		res.OK = false
		res.Errors = append(res.Errors, fmt.Sprintf("body exceeds %d words (%d)", maxWords, words))
	}

	if strings.TrimSpace(out.FactUsed) == "" {
		res.OK = false
		res.Errors = append(res.Errors, "fact_used is required")
	}
	if acc != nil && strings.TrimSpace(out.FactUsed) != "" {
		// Fact should be grounded in account messaging or evidence synthesis.
		grounded := strings.Contains(strings.ToLower(acc.FactToMention), strings.ToLower(out.FactUsed)) ||
			strings.Contains(strings.ToLower(out.FactUsed), firstWords(acc.FactToMention, 4)) ||
			strings.Contains(strings.ToLower(acc.MomentSummary), strings.ToLower(out.FactUsed))
		if !grounded && acc.FactToMention != "" {
			// Soft: warn if fact diverges heavily; still allow if evidence_ids present.
			if len(out.EvidenceIDs) == 0 {
				res.Warnings = append(res.Warnings, "fact_used may not match staging fact_to_mention and has no evidence_ids")
			}
		}
	}
	if strings.TrimSpace(out.FactUsed) != "" && len(out.EvidenceIDs) == 0 && acc != nil && len(acc.MomentEvidenceIDs) == 0 {
		res.Warnings = append(res.Warnings, "fact used without evidence_ids")
	}

	if acc != nil {
		if sc := strings.TrimSpace(out.ServiceCode); sc != "" && acc.ServiceCode != "" && !strings.EqualFold(sc, acc.ServiceCode) {
			res.OK = false
			res.Errors = append(res.Errors, "service_code does not match account offer")
		}
		// More than one service mention is a warning (hard multi-service check is approximate).
		if countServiceMentions(body) > 1 {
			res.OK = false
			res.Errors = append(res.Errors, "body appears to offer more than one service")
		}
	}

	qMarks := strings.Count(body, "?")
	if qMarks > 2 {
		res.Warnings = append(res.Warnings, "body has multiple question marks")
	}

	if !res.OK && len(res.Errors) == 0 {
		res.Errors = append(res.Errors, "validation failed")
	}
	return res
}

func countWords(s string) int {
	fields := strings.Fields(s)
	return len(fields)
}

func firstWords(s string, n int) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	if len(f) < n {
		n = len(f)
	}
	return strings.ToLower(strings.Join(f[:n], " "))
}

func countServiceMentions(body string) int {
	// Conservative: count known multi-offer patterns rather than NLP.
	patterns := []string{"além disso oferecemos", "alem disso oferecemos", "também fazemos", "tambem fazemos", "nossos serviços incluem", "nossos servicos incluem"}
	n := 0
	low := strings.ToLower(body)
	for _, p := range patterns {
		if strings.Contains(low, p) {
			n++
		}
	}
	return n
}

// ClassifyRisk returns GREEN/YELLOW/RED send-risk (not lead value).
func ClassifyRisk(acc *models.OutreachAccount, cand *models.OutreachContactCandidate, out *DraftOutput, val ValidationResult) (class string, flags []string) {
	flags = []string{}
	class = "GREEN"

	raise := func(to string, flag string) {
		flags = append(flags, flag)
		if rank(to) > rank(class) {
			class = to
		}
	}

	if !val.OK {
		raise("RED", "validation_failed")
	}
	if cand != nil {
		switch cand.VerificationStatus {
		case models.OutreachVerifyOfficialSource, models.OutreachVerifyMultipleSources, models.OutreachVerifyPublicDocumentRecent:
			// ok
		case models.OutreachVerifyInstitutionalGeneric:
			raise("YELLOW", "institutional_generic_recipient")
		case models.OutreachVerifyPublicPossiblyStale:
			raise("YELLOW", "possibly_stale_contact")
		default:
			raise("RED", "weak_or_blocked_verification")
		}
		role := strings.ToLower(cand.Role)
		for _, k := range []string{"ceo", "cfo", "presidente", "sócio", "socio", "diretor geral"} {
			if strings.Contains(role, k) {
				raise("RED", "senior_executive_recipient")
				break
			}
		}
	}
	if acc != nil {
		code := strings.ToUpper(acc.MomentCode + " " + acc.ServiceCode + " " + acc.MomentSummary)
		for _, k := range []string{"CREDIT", "CRÉDITO", "CREDITO", "REEQUILIB", "SANCTION", "SANÇÃO", "SANCAO", "LITIG", "CONSÓRCIO", "CONSORCIO", "CONSORTIUM"} {
			if strings.Contains(code, k) {
				raise("RED", "sensitive_moment_or_service")
				break
			}
		}
		for _, k := range []string{"ADDITIVE", "ADITIVO", "EXTENSION", "PRORROG", "REAJUSTE"} {
			if strings.Contains(code, k) {
				raise("YELLOW", "contract_sensitive_topic")
				break
			}
		}
		if acc.FactToMention == "" {
			raise("YELLOW", "missing_public_fact")
		}
	}
	if out != nil {
		blob := strings.ToLower(out.BodyText + " " + out.Subject)
		for _, k := range []string{"crédito", "credito", "reequilíbrio", "reequilibrio", "litígio", "litigio", "sanção", "sancao"} {
			if strings.Contains(blob, k) {
				raise("RED", "economic_or_legal_claim_language")
				break
			}
		}
	}
	if class == "GREEN" && len(flags) == 0 {
		flags = []string{"low_send_risk"}
	}
	return class, flags
}

func rank(c string) int {
	switch c {
	case "RED":
		return 3
	case "YELLOW":
		return 2
	default:
		return 1
	}
}

// TemplateDraft builds a deterministic safe draft when AI is unavailable.
// Never marked as AI-generated approved output by callers.
func TemplateDraft(acc *models.OutreachAccount, cand *models.OutreachContactCandidate) DraftOutput {
	name := ""
	role := ""
	email := ""
	if cand != nil {
		name = strings.TrimSpace(cand.Name)
		role = strings.TrimSpace(cand.Role)
		email = strings.TrimSpace(cand.Email)
	}
	company := ""
	if acc != nil {
		company = firstNonEmpty(acc.NomeFantasia, acc.RazaoSocial)
	}
	greeting := "Olá"
	if name != "" && cand != nil && cand.VerificationStatus != models.OutreachVerifyInstitutionalGeneric {
		greeting = "Olá, " + firstName(name)
	} else if cand != nil && cand.VerificationStatus == models.OutreachVerifyInstitutionalGeneric {
		greeting = "Olá, equipe"
	}

	fact := ""
	question := "Faz sentido conversarmos brevemente?"
	cta := "Posso enviar um checklist de uma página?"
	service := ""
	if acc != nil {
		fact = acc.FactToMention
		if acc.QuestionToAsk != "" {
			question = acc.QuestionToAsk
		}
		if acc.CTA != "" {
			cta = acc.CTA
		}
		service = acc.ServiceName
		if service == "" {
			service = acc.ServiceCode
		}
	}
	if fact == "" {
		fact = "acompanhei publicações recentes relacionadas à " + company
	}

	body := greeting + ",\n\n"
	body += "Sou da CONFENGE. Notei que " + fact + ".\n\n"
	if service != "" {
		body += "Ajudamos times de engenharia com " + strings.ToLower(service) + ", sempre a partir de fatos públicos e sem pressa.\n\n"
	}
	body += question + " " + cta + "\n\n"
	body += "Abraço,\nTiago Sasaki\nCONFENGE"

	// Strip any accidental em dashes from inputs.
	body = emDashRe.ReplaceAllString(body, ",")
	subj := "Sobre " + company
	if utf8.RuneCountInString(subj) > 80 {
		subj = "Conversa rápida"
	}

	_ = email
	_ = role
	return DraftOutput{
		Subject:     subj,
		BodyText:    body,
		BodyHTML:    "",
		Followups:   defaultFollowups(question),
		FactUsed:    fact,
		EvidenceIDs: nil,
		ServiceCode: func() string {
			if acc != nil {
				return acc.ServiceCode
			}
			return ""
		}(),
		Question:  question,
		CTA:       cta,
		RiskFlags: []string{"template_fallback"},
	}
}

func defaultFollowups(question string) []DraftFollowup {
	return []DraftFollowup{
		{DelayDays: 3, SubjectMode: "same_thread", BodyText: "Reenvio com uma pergunta mais objetiva: " + question},
		{DelayDays: 7, SubjectMode: "same_thread", BodyText: "Se não for com você, pode me indicar a pessoa certa de contratos ou engenharia?"},
		{DelayDays: 14, SubjectMode: "same_thread", BodyText: "Encerro por aqui para não ocupar sua caixa. Se fizer sentido no futuro, é só responder este fio."},
	}
}

func firstName(full string) string {
	parts := strings.Fields(full)
	if len(parts) == 0 {
		return full
	}
	return parts[0]
}

// CanBatchApprove reports whether a draft may be approved in bulk.
func CanBatchApprove(d *models.OutreachDraft, cand *models.OutreachContactCandidate) bool {
	if d == nil || cand == nil {
		return false
	}
	if d.Status != models.OutreachDraftNeedsReview && d.Status != models.OutreachDraftApproved {
		return false
	}
	if d.RiskClass != "GREEN" {
		return false
	}
	if !cand.CanEnroll() {
		return false
	}
	// validation must be ok
	if d.ValidationOK != nil && !*d.ValidationOK {
		return false
	}
	return true
}
