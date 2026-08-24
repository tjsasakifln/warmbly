package confenge

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// A public fact is only sayable when it survives digestion into a short noun
// phrase a person would speak. Everything here reduces the raw PNCP object to
// that phrase or refuses it; nothing here launders a bad phrase into copy.

// FactRelation is what the evidence actually proves about the company and the
// public record. Copy must never claim more than the relation allows.
const (
	// FactRelationContractParty: the company is named on the public record.
	FactRelationContractParty = "CONTRACT_PARTY"
	// FactRelationRelated: the company appears related to the record, with no
	// proof it won or executed anything.
	FactRelationRelated = "RELATED"
	// FactRelationCompanyContext is the safe tier-B fallback: the imported
	// account itself proves the company context, but no specific procurement
	// object is claimed.
	FactRelationCompanyContext = "COMPANY_CONTEXT"
	// FactRelationNone: nothing recipient-facing is provable.
	FactRelationNone = "NONE"
)

// PublicFactDigest is the sayable projection of a raw public record.
type PublicFactDigest struct {
	// Phrase is a lowercase noun phrase, e.g.
	// "recuperação estrutural da ponte sobre o Rio Sapucaí".
	Phrase string
	// Subject is a short nominal head written from the phrase, never sliced
	// from the raw text at a character budget.
	Subject string
	// Contraction is the preposition that introduces Phrase in prose, agreed
	// with the head noun: "na recuperação", "no contrato", "nos serviços".
	Contraction string
	Relation    string
	// Reasons explains a refusal. Empty when the digest is usable.
	Reasons []string
}

var (
	// Segment separators in a serialized feed record: "objeto: X; órgão: Y".
	factSegmentRe = regexp.MustCompile(`\s*;\s*`)
	// Any "label:" prefix the feed uses. Copy may never carry one.
	factLabelRe = regexp.MustCompile(`(?i)^\s*(objeto|órgão|orgao|uf|cnpj|modalidade|valor|n[úu]mero|processo|instrumento|contrato|empresa|munic[íi]pio|data|situa[çc][ãa]o)\s*:\s*`)
	// A money token anywhere in the phrase means the record leaked in.
	factMoneyRe = regexp.MustCompile(`(?i)(r\$|US\$)\s*[\d.,]+`)
	// A long digit run or a three-group number is a process number or an
	// amount, never prose. A plain "12/2023" contract reference is how a
	// person actually refers to a contract, so it stays sayable.
	factNumberRe = regexp.MustCompile(`\b\d{5,}\b|\b\d+[./-]\d+[./-]\d+`)
	// A bridge is over a river, not under it; the source says otherwise often.
	factBridgePrepRe = regexp.MustCompile(`(?i)\bsob\s+(o|a)\s+(rio|c[óo]rrego|ribeir[ãa]o)\b`)
	// Pipeline telemetry describing our own ingestion, not the recipient's world.
	factTelemetryRe = regexp.MustCompile(`\b(no input|observado com|contrato\(s\)|portfolio publico observado|registros? no feed|snapshot)\b`)
	// Our own sentence coming back as a fact: a recompose reading the prose it
	// wrote last time, never a public record.
	factComposedProseRe = regexp.MustCompile(`(?i)^(vi|entrei|notei|reparei|percebi|observei|acompanhei|soube|retomo|escrevo|escrevi)\b|\bmeu nome e\b`)
	// A record separates the work from its qualifier with a dash.
	factDashQualifierRe = regexp.MustCompile(`\s+[-–—]\s+`)
)

// editalLeadIns are the procurement boilerplate openings that carry no
// information. They are stripped, longest first, until the core object is
// reached. Order matters: the longest phrasing must win.
var editalLeadIns = []string{
	"contratacao de empresa especializada para a execucao dos servicos necessarios a",
	"contratacao de empresa especializada para execucao dos servicos necessarios a",
	"contratacao de empresa especializada para a prestacao dos servicos de",
	"contratacao de empresa especializada para prestacao dos servicos de",
	"contratacao de empresa especializada para a execucao de",
	"contratacao de empresa especializada para execucao de",
	"contratacao de empresa especializada para a realizacao de",
	"contratacao de empresa especializada para realizacao de",
	"contratacao de empresa especializada em",
	"contratacao de empresa especializada para",
	"contratacao de empresa para a prestacao de servicos de",
	"contratacao de empresa para prestacao de servicos de",
	"contratacao de empresa para",
	"contratacao de pessoa juridica especializada para",
	"contratacao de pessoa juridica para",
	"contratacao de servicos tecnicos especializados de",
	"contratacao de servicos especializados de",
	"contratacao de servicos de",
	"contratacao de solucao para",
	"contratacao integrada para",
	"contratacao emergencial de",
	"contratacao de",
	"prestacao de servicos tecnicos especializados de",
	"prestacao de servicos especializados de",
	"prestacao de servicos de",
	"execucao dos servicos necessarios a",
	"execucao dos servicos de",
	"execucao de servicos de",
	"execucao indireta de",
	"registro de precos para a contratacao de",
	"registro de precos para contratacao de",
	"registro de precos para a eventual",
	"registro de precos para",
	"aquisicao de",
	"credenciamento de empresas para",
	"credenciamento de pessoas juridicas para",
	"credenciamento para",
	"servicos necessarios a",
	"o objeto do presente e a",
	"o presente objeto e a",
	"tem por objeto a",
	"tem por objeto o",
}

// factLeadIns is every boilerplate opening a phrase may lose, longest first, so
// a full lead-in always wins over the shorter administrative label inside it.
var factLeadIns = buildFactLeadIns()

// buildFactLeadIns derives the bare openings a feed also publishes without the
// "contratação de" head, e.g. "empresa especializada para execução de".
func buildFactLeadIns() []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(editalLeadIns)*2)
	add := func(v string) {
		if v = strings.TrimSpace(v); v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	for _, lead := range editalLeadIns {
		add(lead)
		for _, head := range []string{"contratacao de empresa", "contratacao de pessoa juridica"} {
			if strings.HasPrefix(lead, head) {
				add(strings.TrimPrefix(lead, "contratacao de "))
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// factAdministrativeLabels is the subject gate's list of administrative
// openings, longest first, reused so a label never has two definitions.
var factAdministrativeLabels = sortedByLengthDesc(subjectAdministrativePrefixes)

func sortedByLengthDesc(in []string) []string {
	out := append([]string(nil), in...)
	sort.SliceStable(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// factStopPhrases mark the point where the object stops describing the work
// and starts describing paperwork. Everything from there on is dropped.
var factStopPhrases = []string{
	" conforme condicoes",
	" conforme especificacoes",
	" conforme quantidades",
	" conforme termo de referencia",
	" conforme projeto basico",
	" conforme edital",
	" conforme anexo",
	" de acordo com as especificacoes",
	" de acordo com o termo",
	" na forma do edital",
	" nos termos do edital",
	" tudo conforme",
	" com fornecimento de",
	" pelo periodo de",
	" pelo prazo de",
	" por um periodo de",
	" a serem executados",
	" a ser executado",
	" em atendimento a",
	" para atender as necessidades",
	" para atender a demanda",
	" visando atender",
	" localizada na",
	" localizado na",
	" situada da",
	" situada na",
	" situado na",
	" situada no",
	" situado no",
}

// prepositionsPT stay lowercase inside a phrase.
var prepositionsPT = map[string]bool{
	"a": true, "as": true, "ao": true, "aos": true, "à": true, "às": true,
	"da": true, "das": true, "de": true, "do": true, "dos": true,
	"e": true, "em": true, "na": true, "nas": true, "no": true, "nos": true,
	"o": true, "os": true, "para": true, "por": true, "sob": true,
	"sobre": true, "um": true, "uma": true, "com": true, "entre": true,
}

// properNounCues are tokens after which the next words name something real and
// keep their capitals: "rio Sapucaí", "rodovia Fernão Dias".
var properNounCues = map[string]bool{
	"rio": true, "ponte": true, "rodovia": true, "avenida": true, "rua": true,
	"praca": true, "bairro": true, "distrito": true, "municipio": true,
	"escola": true, "hospital": true, "terminal": true, "aeroporto": true,
	"estadio": true, "barragem": true, "represa": true, "corrego": true,
}

// DigestPublicFact turns a raw feed fact into a sayable phrase, or refuses it.
// Refusal is the safe outcome: a lead with no sayable fact is enriched later,
// never padded with filler.
func DigestPublicFact(raw string) PublicFactDigest {
	out := PublicFactDigest{Relation: FactRelationNone}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		out.Reasons = []string{"fact_absent"}
		return out
	}
	core := extractObjectSegment(raw)
	if core == "" {
		out.Reasons = []string{"fact_no_object_segment"}
		return out
	}
	core = collapseRepeatedNGrams(core)
	core = stripAdministrativeLabel(core)
	core = stripEditalLeadIn(core)
	core = truncateAtStopPhrase(core)
	core = resolveDashQualifier(core)
	core = strings.Trim(core, " \t.,;:-–—")
	core = multiSpaceRe.ReplaceAllString(core, " ")
	if core == "" {
		out.Reasons = []string{"fact_only_boilerplate"}
		return out
	}
	phrase := recasePhrasePT(core)
	phrase = factBridgePrepRe.ReplaceAllString(phrase, "sobre $1 $2")
	phrase = collapseRepeatedNGrams(phrase)
	phrase = strings.Trim(multiSpaceRe.ReplaceAllString(phrase, " "), " \t.,;:-–—")
	if reasons := factPhraseDefects(phrase); len(reasons) > 0 {
		out.Reasons = reasons
		return out
	}
	subject := subjectFromPhrase(phrase, raw)
	if subject == "" {
		out.Reasons = []string{"fact_no_subject_head"}
		return out
	}
	out.Phrase = phrase
	out.Subject = subject
	out.Contraction = contractionForHead(phrase)
	out.Relation = FactRelationRelated
	return out
}

// extractObjectSegment keeps only the segment that describes the work. A feed
// record is "objeto: X; órgão: Y; UF MG; R$ 1,00" and only X may be spoken.
func extractObjectSegment(raw string) string {
	segments := factSegmentRe.Split(raw, -1)
	labelled := ""
	firstUnlabelled := ""
	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		if factLabelRe.MatchString(seg) {
			folded := foldASCII(strings.ToLower(seg))
			if !strings.HasPrefix(folded, "objeto") {
				// A labelled non-object segment is pure metadata.
				continue
			}
			body := strings.TrimSpace(factLabelRe.ReplaceAllString(seg, ""))
			if len(body) > len(labelled) {
				labelled = body
			}
			continue
		}
		if firstUnlabelled == "" {
			firstUnlabelled = seg
		}
	}
	if labelled != "" {
		return labelled
	}
	return firstUnlabelled
}

func stripEditalLeadIn(s string) string {
	for changed := true; changed; {
		changed = false
		orig, folded := runeFold(s)
		for _, lead := range factLeadIns {
			n := len([]rune(lead))
			if n > len(folded) || string(folded[:n]) != lead {
				continue
			}
			trimmed := strings.TrimSpace(string(orig[n:]))
			// Refuse a strip that leaves nothing to say.
			if len(strings.Fields(trimmed)) < 2 {
				continue
			}
			s = trimmed
			changed = true
			break
		}
	}
	return strings.TrimSpace(stripLeadingPrepositions(s))
}

// stripAdministrativeLabel drops a labelled procurement opening such as
// "contratação pública: X", which otherwise survives into prose and makes the
// sentence stutter. The separator is required so an object that merely starts
// with one of those words is never cut mid sentence.
func stripAdministrativeLabel(s string) string {
	for changed := true; changed; {
		changed = false
		orig, folded := runeFold(s)
		for _, label := range factAdministrativeLabels {
			n := len([]rune(label))
			if n >= len(folded) || string(folded[:n]) != label {
				continue
			}
			rest := []rune(strings.TrimLeft(string(orig[n:]), " \t"))
			if len(rest) == 0 || !strings.ContainsRune(":-–—", rest[0]) {
				continue
			}
			trimmed := strings.TrimSpace(strings.TrimLeft(string(rest), ":-–— \t"))
			if len(strings.Fields(trimmed)) < 2 {
				continue
			}
			s = trimmed
			changed = true
			break
		}
	}
	return strings.TrimSpace(s)
}

// resolveDashQualifier rewrites the record's "work - qualifier" shorthand as
// prose: the head alone when it already says something, otherwise bound with
// "para", which is how a person reads that dash out loud.
func resolveDashQualifier(s string) string {
	loc := factDashQualifierRe.FindStringIndex(s)
	if loc == nil {
		return s
	}
	head := strings.TrimSpace(s[:loc[0]])
	tail := strings.TrimSpace(s[loc[1]:])
	if head == "" || tail == "" {
		return s
	}
	if len(strings.Fields(head)) >= 3 {
		return head
	}
	return head + " para " + tail
}

// stripLeadingPrepositions drops the connective left behind by a lead-in cut,
// so "de engenharia civil" reads as "engenharia civil".
func stripLeadingPrepositions(s string) string {
	for {
		fields := strings.Fields(s)
		if len(fields) < 3 {
			return strings.TrimSpace(s)
		}
		if !prepositionsPT[foldASCII(strings.ToLower(fields[0]))] {
			return strings.TrimSpace(s)
		}
		s = strings.Join(fields[1:], " ")
	}
}

// runeFold returns the original runes and an accent-folded, lowercased view of
// the same length, so an index found in one is valid in the other.
func runeFold(s string) ([]rune, []rune) {
	orig := []rune(s)
	folded := []rune(foldASCII(strings.ToLower(s)))
	if len(folded) != len(orig) {
		// Fail safe: never slice with a drifting index.
		return orig, orig
	}
	return orig, folded
}

func truncateAtStopPhrase(s string) string {
	orig, folded := runeFold(s)
	cut := -1
	for _, stop := range factStopPhrases {
		if i := runeIndex(folded, stop); i > 0 && (cut < 0 || i < cut) {
			cut = i
		}
	}
	if cut > 0 {
		orig = orig[:cut]
	}
	out := string(orig)
	// A trailing comma clause is administrative more often than not; keep the
	// head when the head already says something on its own.
	if i := strings.Index(out, ","); i > 0 {
		if head := strings.TrimSpace(out[:i]); len(strings.Fields(head)) >= 4 {
			out = head
		}
	}
	return strings.TrimSpace(out)
}

func runeIndex(hay []rune, needle string) int {
	n := []rune(needle)
	if len(n) == 0 || len(n) > len(hay) {
		return -1
	}
	for i := 0; i+len(n) <= len(hay); i++ {
		if string(hay[i:i+len(n)]) == needle {
			return i
		}
	}
	return -1
}

// recasePhrasePT rewrites shouted edital text as ordinary prose: everything
// lowercase except tokens that name something real.
func recasePhrasePT(s string) string {
	fields := strings.Fields(s)
	out := make([]string, 0, len(fields))
	prevCue := false
	for _, w := range fields {
		core := strings.Trim(w, ".,;:!?()[]\"'")
		key := foldASCII(strings.ToLower(core))
		switch {
		case key == "":
			out = append(out, w)
		case knownAcronymUpper(key):
			out = append(out, strings.ToUpper(core))
		case prevCue && !prepositionsPT[key]:
			out = append(out, titleWordPT(strings.ToLower(core)))
		default:
			out = append(out, strings.ToLower(core))
		}
		prevCue = properNounCues[key] || (prevCue && prepositionsPT[key])
	}
	return strings.Join(out, " ")
}

func knownAcronymUpper(key string) bool {
	switch key {
	case "pncp", "cnpj", "crea", "cau", "art", "rrt", "dnit", "dner", "der",
		"cbuq", "abnt", "nbr", "epi", "srp", "tce", "tcu", "cgu", "br",
		"confenge", "cpf", "ltda", "epp", "mei", "sa", "uf", "ne", "se", "df":
		return true
	}
	return false
}

// factPhraseDefects refuses a phrase that would still read as a record.
func factPhraseDefects(phrase string) []string {
	var reasons []string
	words := strings.Fields(phrase)
	switch {
	case len(words) < 3:
		reasons = append(reasons, "fact_too_short_to_say")
	case len(words) > 16:
		reasons = append(reasons, "fact_too_long_to_say")
	}
	if factMoneyRe.MatchString(phrase) {
		reasons = append(reasons, "fact_carries_amount")
	}
	if factNumberRe.MatchString(phrase) {
		reasons = append(reasons, "fact_carries_record_number")
	}
	if factLabelRe.MatchString(phrase) || containsDumpLabel(phrase) {
		reasons = append(reasons, "fact_carries_label")
	}
	if factTelemetryRe.MatchString(foldASCII(strings.ToLower(phrase))) {
		reasons = append(reasons, "fact_is_internal_telemetry")
	}
	if factComposedProseRe.MatchString(foldASCII(strings.ToLower(phrase))) {
		reasons = append(reasons, "fact_is_composed_prose")
	}
	if strings.ContainsAny(phrase, "()[]{}") {
		reasons = append(reasons, "fact_carries_record_punctuation")
	}
	if shoutedResidueIn(phrase) || allCapsWordIn(phrase) {
		reasons = append(reasons, "fact_shouted_residue")
	}
	if repeatedNGramIn(phrase) {
		reasons = append(reasons, "fact_repeated_ngram")
	}
	return reasons
}

// allCapsWordIn catches a shouted content word the residue check ignores,
// because a single surviving "PONTE" is enough to look like a paste.
func allCapsWordIn(s string) bool {
	for _, w := range strings.Fields(s) {
		core := strings.Trim(w, ".,;:!?()[]\"'")
		key := foldASCII(strings.ToLower(core))
		if knownAcronymUpper(key) {
			continue
		}
		letters, upper := 0, 0
		for _, r := range core {
			if unicode.IsLetter(r) {
				letters++
				if unicode.IsUpper(r) {
					upper++
				}
			}
		}
		if letters >= 2 && upper == letters {
			return true
		}
	}
	return false
}

// subjectFromPhrase writes a short nominal head. It selects whole words from
// the phrase's noun group; it never cuts at a character budget. An empty
// return refuses the fact, which is better than a subject naming no work.
func subjectFromPhrase(phrase, raw string) string {
	words := strings.Fields(phrase)
	if len(words) < 2 {
		return ""
	}
	folded := foldASCII(strings.ToLower(phrase))
	foldedRaw := foldASCII(strings.ToLower(strings.TrimSpace(raw)))
	// Event subjects are written from the meaning of the fact, not copied from
	// its first N words. Besides reading better, this keeps the subject stable
	// when upstream changes administrative wording around the same event.
	if s := eventSubjectFor(folded); s != "" && subjectNamesSpecificWork(s) {
		return s
	}
	// Prefer the group that names something real, so "recuperação estrutural
	// da ponte sobre o Rio Sapucaí" yields "Ponte sobre o Rio Sapucaí".
	if i := indexOfProperCue(words); i >= 0 {
		tail := words[i:]
		// A two-word place name alone ("Avenida Brasil") is vaguer than the
		// work itself; only prefer the place group when it is descriptive.
		if len(tail) >= 3 && len(tail) <= 6 {
			if s := writtenSubject(tail, foldedRaw); s != "" {
				return s
			}
		}
	}
	limit := len(words)
	if limit > 5 {
		limit = 5
	}
	head := words[:limit]
	for len(head) > 2 && prepositionsPT[foldASCII(strings.ToLower(head[len(head)-1]))] {
		head = head[:len(head)-1]
	}
	for len(head) > 2 && prepositionsPT[foldASCII(strings.ToLower(head[0]))] {
		head = head[1:]
	}
	if len(head) < 2 || prepositionsPT[foldASCII(strings.ToLower(head[0]))] {
		return ""
	}
	return writtenSubject(head, foldedRaw)
}

// eventSubjectFor names the procurement event the phrase is about.
func eventSubjectFor(folded string) string {
	switch {
	case strings.Contains(folded, "reequilibr"):
		return "Reequilíbrio do contrato público"
	case strings.Contains(folded, "reajust"):
		return "Reajuste do contrato público"
	case strings.Contains(folded, "prorrog"):
		return "Prorrogação do contrato público"
	case strings.Contains(folded, "aditivo"):
		return "Aditivo do contrato público"
	}
	return ""
}

// writtenSubject turns a word group into a subject line. It refuses a group
// that names no work, and prefixes "Sobre" only when the plain head would read
// as a literal slice of the record.
func writtenSubject(words []string, foldedRaw string) string {
	if len(words) < 2 {
		return ""
	}
	plain := strings.Join(words, " ")
	if !subjectNamesSpecificWork(plain) {
		return ""
	}
	folded := foldASCII(strings.ToLower(plain))
	if foldedRaw == "" || !strings.Contains(foldedRaw, folded) {
		return upperFirstRune(plain)
	}
	if strings.HasPrefix(folded, "sobre ") {
		return ""
	}
	return "Sobre " + strings.ToLower(plain)
}

func indexOfProperCue(words []string) int {
	for i, w := range words {
		if properNounCues[foldASCII(strings.ToLower(strings.Trim(w, ".,;:")))] {
			return i
		}
	}
	return -1
}

func upperFirstRune(s string) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) == 0 {
		return ""
	}
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// contractionForHead agrees "em + article" with the phrase's head noun. Slot
// filling without agreement is what produces "na contrato".
func contractionForHead(phrase string) string {
	fields := strings.Fields(phrase)
	if len(fields) == 0 {
		return "na"
	}
	head := foldASCII(strings.ToLower(strings.Trim(fields[0], ".,;:")))
	plural := strings.HasSuffix(head, "s") && len(head) > 3
	singular := strings.TrimSuffix(head, "s")
	feminine := false
	switch {
	case strings.HasSuffix(singular, "cao"), strings.HasSuffix(singular, "sao"),
		strings.HasSuffix(singular, "dade"), strings.HasSuffix(singular, "agem"),
		strings.HasSuffix(singular, "ura"), strings.HasSuffix(singular, "eza"),
		strings.HasSuffix(singular, "ncia"), strings.HasSuffix(singular, "orma"):
		feminine = true
	case strings.HasSuffix(singular, "mento"), strings.HasSuffix(singular, "o"),
		strings.HasSuffix(singular, "or"), strings.HasSuffix(singular, "il"):
		feminine = false
	default:
		feminine = strings.HasSuffix(singular, "a")
	}
	switch {
	case feminine && plural:
		return "nas"
	case feminine:
		return "na"
	case plural:
		return "nos"
	default:
		return "no"
	}
}
