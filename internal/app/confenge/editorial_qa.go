package confenge

import (
	"regexp"
	"strings"
	"unicode"
)

// copy_qa=passed must mean one thing only: a competent B2B consultant could
// press Send on this text without rewriting a word. Every check below is
// deterministic and fail-closed. A semantic critic may add findings; it may
// never clear one, so no model opinion can turn a defect into a pass.

// EditorialQAContext is what the gate needs beyond the rendered text.
type EditorialQAContext struct {
	RouteClass      string
	RawFact         string
	SenderFirstName string
	PersonProven    bool
	PersonName      string
}

// Editorial doctrine bounds. Preferred is 45-110 words; the gate refuses only
// outside the wider band so a good short mail is not failed for being short.
const (
	editorialMinWords      = 35
	editorialMaxWords      = 150
	editorialMaxParagraphs = 5
	editorialMaxParaWords  = 70
	editorialMaxSentWords  = 40
	editorialMinSubjWords  = 2
	editorialMaxSubjWords  = 10
)

var (
	// Serialized record shapes that must never reach a recipient.
	qaEnumRe      = regexp.MustCompile(`\b[A-Z][A-Z0-9]{2,}_[A-Z0-9_]{2,}\b`)
	qaKeyValueRe  = regexp.MustCompile(`\b[a-z_]{3,}\s*=\s*\S+`)
	qaScoreRe     = regexp.MustCompile(`(?i)\b(priority_score|moment_code|route_class|snapshot|content_hash|evidence_hash|cnpj14?)\b`)
	qaFakeReplyRe = regexp.MustCompile(`(?i)^\s*(re|res|enc|fw|fwd)\s*:`)
	qaURLRe       = regexp.MustCompile(`(?i)https?://`)
	// Punctuation a person never types.
	qaBadPunctRe = regexp.MustCompile(`,\.|\.\.|,,|;\.|::|:\.|!\.|\?\.|\s+[,.;:!?]|\(\s*\)`)
	// A sentence that stops on a connective was cut, not written.
	qaDanglingTailRe = regexp.MustCompile(`(?i)\b(de|da|do|dos|das|para|por|com|em|na|no|e|ou|a|o|que|sob|sobre|entre)\s*$`)
	// Addressing the reader as the owner of a role nobody proved.
	qaAssertedRoleRe = regexp.MustCompile(`(?i)\bcomo (o |a )?(respons[aá]vel|gestor|diretor|gerente|propriet[aá]rio|s[oó]cio)\b`)
)

// syntheticPhrases are the constructions that mark machine-written outbound.
// The composer is built not to produce them; the gate refuses them anyway so a
// future template or model output cannot reintroduce the register.
var syntheticPhrases = []string{
	"espero que esteja bem",
	"espero que este e-mail o encontre",
	"venho por meio deste",
	"venho por meio desta",
	"gostaria de apresentar",
	"identificamos uma oportunidade",
	"identificamos que",
	"sinergia",
	"potencializar",
	"otimizar resultados",
	"otimizar seus resultados",
	"solucoes personalizadas",
	"solucao personalizada",
	"com base em informacoes publicas",
	"conforme informacoes publicas",
	"segue em anexo",
	"nao perca esta oportunidade",
	"oportunidade unica",
	"fico no aguardo do seu retorno",
	"agradeco desde ja",
	"sem mais para o momento",
	"somos uma empresa lider",
	"referencia no mercado",
	"parceria estrategica",
	"agregar valor",
	"alavancar",
	"proatividade",
	"sou da confenge.",
}

// roboticGreetings are openings the doctrine forbids for an institutional
// mailbox, because none of them is how a person opens a first email.
var roboticGreetings = []string{
	"ola, equipe",
	"ola equipe",
	"ola, pessoal",
	"ola pessoal",
	"ola, time",
	"prezados",
	"prezado responsavel",
	"prezada equipe",
	"a quem possa interessar",
	"caro cliente",
	"ola, comercial",
}

// subjectAdministrativePrefixes are edital openings a subject may never carry.
var subjectAdministrativePrefixes = []string{
	"contratacao publica",
	"contratacao de empresa",
	"contratacao de servicos",
	"objeto",
	"prestacao de servicos",
	"registro de precos",
	"processo administrativo",
	"pregao eletronico",
	"tomada de precos",
	"dispensa de licitacao",
	"aviso de licitacao",
}

// EditorialQA returns the reason codes that block a message. Empty means the
// message is sendable as written.
func EditorialQA(subject, body string, ctx EditorialQAContext) []string {
	var codes []string
	seen := map[string]bool{}
	add := func(c string) {
		if !seen[c] {
			seen[c] = true
			codes = append(codes, c)
		}
	}

	subject = strings.TrimSpace(subject)
	body = strings.TrimSpace(body)
	if subject == "" {
		add("subject_empty")
	}
	if body == "" {
		add("body_empty")
		return codes
	}

	checkEditorialSubject(subject, ctx, add)
	checkEditorialBody(body, ctx, add)
	checkEditorialShared(subject, body, ctx, add)
	return codes
}

func checkEditorialSubject(subject string, ctx EditorialQAContext, add func(string)) {
	if subject == "" {
		return
	}
	folded := foldASCII(strings.ToLower(subject))
	words := strings.Fields(subject)
	if len(words) < editorialMinSubjWords {
		add("subject_too_short")
	}
	if len(words) > editorialMaxSubjWords {
		add("subject_too_long")
	}
	if qaFakeReplyRe.MatchString(subject) {
		add("subject_fake_reply_prefix")
	}
	for _, p := range subjectAdministrativePrefixes {
		if strings.HasPrefix(folded, p) {
			add("subject_administrative_object")
			break
		}
	}
	if strings.Contains(subject, ":") {
		add("subject_label_prefix")
	}
	if allCapsWordIn(subject) || shoutedResidueIn(subject) {
		add("subject_shouted")
	}
	if titleCasedRatio(words) > 0.6 && len(words) >= 3 {
		add("subject_artificial_title_case")
	}
	if qaBadPunctRe.MatchString(subject) {
		add("subject_defective_punctuation")
	}
	if strings.HasSuffix(strings.TrimSpace(subject), ",") || qaDanglingTailRe.MatchString(subject) {
		add("subject_truncated")
	}
	if repeatedNGramIn(subject) || repeatedWordIn(subject) {
		add("subject_repetition")
	}
	if containsDumpLabel(subject) || qaEnumRe.MatchString(subject) || qaScoreRe.MatchString(subject) {
		add("subject_metadata")
	}
	// A subject sliced out of the raw record reads as a paste, not a line
	// somebody wrote. Any long literal overlap is treated as a slice.
	if raw := foldASCII(strings.ToLower(ctx.RawFact)); raw != "" && len(folded) >= 12 {
		if strings.Contains(raw, folded) {
			add("subject_sliced_from_raw_fact")
		}
	}
}

func checkEditorialBody(body string, ctx EditorialQAContext, add func(string)) {
	folded := foldASCII(strings.ToLower(body))
	words := strings.Fields(body)
	if len(words) < editorialMinWords {
		add("body_too_short")
	}
	if len(words) > editorialMaxWords {
		add("body_too_long")
	}

	paragraphs := nonEmptyParagraphs(body)
	if len(paragraphs) > editorialMaxParagraphs {
		add("body_too_many_paragraphs")
	}
	if len(paragraphs) < 3 {
		add("body_no_reason_to_write")
	}
	for _, p := range paragraphs {
		if len(strings.Fields(p)) > editorialMaxParaWords {
			add("paragraph_too_long")
			break
		}
	}
	for _, s := range splitSentencesPT(body) {
		if len(strings.Fields(s)) > editorialMaxSentWords {
			add("sentence_too_long")
			break
		}
	}

	// Exactly one ask. Zero means nothing was requested; more than one splits
	// the reader's attention and reads as a template.
	switch strings.Count(body, "?") {
	case 0:
		add("no_call_to_action")
	case 1:
	default:
		add("multiple_calls_to_action")
	}

	firstLine := strings.TrimSpace(strings.SplitN(body, "\n", 2)[0])
	greet := foldASCII(strings.ToLower(strings.TrimRight(firstLine, " ,:!")))
	for _, bad := range roboticGreetings {
		if greet == bad || strings.HasPrefix(greet, bad) {
			add("robotic_greeting")
			break
		}
	}
	if !strings.HasPrefix(greet, "ola") && !strings.HasPrefix(greet, "bom dia") && !strings.HasPrefix(greet, "boa tarde") {
		add("unexpected_greeting")
	}
	if looksInventedPersonGreeting(firstLine) && !ctx.PersonProven {
		add("invented_person_greeting")
	}

	for _, p := range syntheticPhrases {
		if strings.Contains(folded, p) {
			add("synthetic_register")
			break
		}
	}
	if strings.Contains(folded, "sou da confenge") {
		add("robotic_self_introduction")
	}
	if ctx.SenderFirstName != "" &&
		!strings.Contains(folded, foldASCII(strings.ToLower(ctx.SenderFirstName))) {
		add("sender_not_identified")
	}

	if repeatedNGramIn(body) {
		add("repeated_ngram")
	}
	if repeatedWordIn(body) {
		add("repeated_word")
	}
	if shoutedResidueIn(body) || allCapsWordIn(body) {
		add("shouted_residue")
	}
	if qaBadPunctRe.MatchString(body) {
		add("defective_punctuation")
	}
	if containsDumpLabel(body) || looksLikeMetadataDump(body) {
		add("metadata_dump")
	}
	if qaEnumRe.MatchString(body) {
		add("raw_enum")
	}
	if qaKeyValueRe.MatchString(body) || qaScoreRe.MatchString(body) {
		add("serialized_record")
	}
	if qaURLRe.MatchString(body) {
		add("unexpected_link")
	}
	if endsMidThought(body) {
		add("incomplete_sentence")
	}
	// A role or title is never asserted unless it was proven upstream. The
	// greeting is checked by looksInventedPersonGreeting above; here we refuse
	// a named person or a title asserted anywhere else in the mail.
	if !ctx.PersonProven && ctx.PersonName != "" &&
		strings.Contains(folded, foldASCII(strings.ToLower(ctx.PersonName))) {
		add("unproven_identity")
	}
	if !ctx.PersonProven && qaAssertedRoleRe.MatchString(folded) {
		add("unproven_role")
	}
}

func checkEditorialShared(subject, body string, ctx EditorialQAContext, add func(string)) {
	raw := strings.TrimSpace(ctx.RawFact)
	if raw == "" {
		return
	}
	foldedBody := foldASCII(strings.ToLower(body))
	foldedRaw := foldASCII(strings.ToLower(raw))

	// The email must be a projection of the record, not a copy of it.
	if longestSharedRun(foldedBody, foldedRaw) >= 12 {
		add("raw_fact_pasted")
	}
	if len(strings.Fields(raw)) > 25 {
		for _, p := range nonEmptyParagraphs(body) {
			if len(strings.Fields(p)) > 25 && strings.Contains(foldedRaw, foldASCII(strings.ToLower(p))) {
				add("raw_fact_literal_too_long")
				break
			}
		}
	}
	// Repeating the subject verbatim at the top of the body is mail-merge tell.
	// Naming the same fact in the subject and in the observation is how a
	// person writes. The defect is a paragraph that only restates the subject.
	subjFolded := foldASCII(strings.ToLower(strings.TrimSpace(subject)))
	if subjFolded != "" {
		for _, p := range nonEmptyParagraphs(body) {
			if strings.TrimRight(foldASCII(strings.ToLower(p)), " .!?") == subjFolded {
				add("subject_restated_in_lead")
				break
			}
		}
	}
}

// MergeCriticFindings adds a semantic critic's codes to a deterministic
// result. It can only ever add, so a critic cannot clear a hard failure.
func MergeCriticFindings(deterministic, critic []string) []string {
	out := append([]string{}, deterministic...)
	seen := map[string]bool{}
	for _, c := range out {
		seen[c] = true
	}
	for _, c := range critic {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}

func nonEmptyParagraphs(body string) []string {
	var out []string
	for _, p := range strings.Split(body, "\n\n") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func splitSentencesPT(body string) []string {
	var out []string
	cur := strings.Builder{}
	for _, r := range body {
		cur.WriteRune(r)
		if r == '.' || r == '!' || r == '?' {
			if s := strings.TrimSpace(cur.String()); s != "" {
				out = append(out, s)
			}
			cur.Reset()
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}

func repeatedWordIn(s string) bool {
	prev := ""
	for _, w := range strings.Fields(s) {
		k := foldASCII(strings.ToLower(strings.Trim(w, ".,;:!?()[]\"'")))
		if k == "" {
			continue
		}
		if k == prev && len([]rune(k)) > 2 {
			return true
		}
		prev = k
	}
	return false
}

func titleCasedRatio(words []string) float64 {
	if len(words) == 0 {
		return 0
	}
	n := 0
	for _, w := range words {
		runes := []rune(strings.Trim(w, ".,;:!?"))
		if len(runes) < 2 || !unicode.IsUpper(runes[0]) {
			continue
		}
		lowerRest := true
		for _, r := range runes[1:] {
			if unicode.IsUpper(r) {
				lowerRest = false
				break
			}
		}
		if lowerRest {
			n++
		}
	}
	return float64(n) / float64(len(words))
}

// endsMidThought reports a body whose last sentence never closed.
func endsMidThought(body string) bool {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return true
	}
	lines := strings.Split(trimmed, "\n")
	last := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			last = s
			break
		}
	}
	if last == "" {
		return true
	}
	// A sign-off line is a name, not a sentence, so it needs no terminator.
	if len(strings.Fields(last)) <= 3 && !qaDanglingTailRe.MatchString(last) {
		return false
	}
	return qaDanglingTailRe.MatchString(last) || strings.HasSuffix(last, ",")
}

// longestSharedRun returns the longest run of words present verbatim in both
// texts, which is how a pasted record is told from a written sentence.
func longestSharedRun(a, b string) int {
	aw := strings.Fields(a)
	best := 0
	for i := range aw {
		for n := best + 1; i+n <= len(aw); n++ {
			if !strings.Contains(b, strings.Join(aw[i:i+n], " ")) {
				break
			}
			if n > best {
				best = n
			}
		}
	}
	return best
}
