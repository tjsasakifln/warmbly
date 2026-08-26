package confenge

import (
	"sort"
	"strings"

	"github.com/warmbly/warmbly/internal/models"
)

// DelegatedCorpusMessage keeps the evidence beside the rendered projection so
// corpus QA can distinguish factual repetition from cosmetic template spin.
type DelegatedCorpusMessage struct {
	ID        string
	Source    string
	Account   *models.OutreachAccount
	Candidate *models.OutreachContactCandidate
	Evidence  []models.OutreachEvidence
	Copy      delegatedRoutingCopy
}

// ComposeDelegatedCorpusMessage is the reproducible corpus entrypoint. It does
// no network or model work and has the same O(1) composer path as the worker.
func ComposeDelegatedCorpusMessage(id, source string, acc *models.OutreachAccount, cand *models.OutreachContactCandidate, evidence []models.OutreachEvidence) DelegatedCorpusMessage {
	return DelegatedCorpusMessage{
		ID: id, Source: source, Account: acc, Candidate: cand,
		Evidence: append([]models.OutreachEvidence{}, evidence...),
		Copy:     buildDelegatedRoutingCopy(acc, cand, evidence),
	}
}

type CorpusConcentration struct {
	Value             string  `json:"value"`
	Count             int     `json:"count"`
	Share             float64 `json:"share"`
	EvidenceDimension string  `json:"evidence_dimension"`
}

type CorpusLengthStats struct {
	Minimum int     `json:"minimum"`
	Average float64 `json:"average"`
	P50     int     `json:"p50"`
	P90     int     `json:"p90"`
	P95     int     `json:"p95"`
	P99     int     `json:"p99"`
	Maximum int     `json:"maximum"`
}

// DelegatedCorpusReport is intentionally descriptive for concentration. The
// hard failures below protect truth and usability; service/practice and
// route/CTA repetition are reported against their supporting dimensions rather
// than failed against an unexplained percentage.
type DelegatedCorpusReport struct {
	RulesVersion              string                `json:"rules_version"`
	Messages                  int                   `json:"messages"`
	SourceMix                 map[string]int        `json:"source_mix"`
	ExactDuplicateGroups      int                   `json:"exact_duplicate_groups"`
	ExactDuplicateMessages    int                   `json:"exact_duplicate_messages"`
	LargestExactGroup         int                   `json:"largest_exact_group"`
	NearDuplicateGroups       int                   `json:"near_duplicate_groups"`
	NearDuplicateMessages     int                   `json:"near_duplicate_messages"`
	LargestNearGroup          int                   `json:"largest_near_group"`
	NearDuplicateDefinition   string                `json:"near_duplicate_definition"`
	SubjectConcentration      []CorpusConcentration `json:"subject_concentration"`
	OpeningConcentration      []CorpusConcentration `json:"opening_concentration"`
	PracticeLineConcentration []CorpusConcentration `json:"practice_line_concentration"`
	CTAConcentration          []CorpusConcentration `json:"cta_concentration"`
	UnsupportedClaims         int                   `json:"unsupported_claims"`
	BuyerSupplierConfusions   int                   `json:"buyer_supplier_confusions"`
	GuessedPeople             int                   `json:"guessed_people"`
	InternalMetadataLeaks     int                   `json:"internal_metadata_leaks"`
	OffensiveOrManipulative   int                   `json:"offensive_or_manipulative"`
	EmptySubjectOrBody        int                   `json:"empty_subject_or_body"`
	RouteInappropriate        int                   `json:"route_inappropriate"`
	ContactExitMissing        int                   `json:"contact_exit_missing"`
	LengthWords               CorpusLengthStats     `json:"length_words"`
	Violations                map[string]int        `json:"violations,omitempty"`
}

// AuditDelegatedFirstTouchCorpus runs in O(n log n): every duplication and
// concentration measure is map-backed, with sorting only for deterministic
// output and percentiles. It never does an all-pairs 10k comparison.
func AuditDelegatedFirstTouchCorpus(messages []DelegatedCorpusMessage) DelegatedCorpusReport {
	report := DelegatedCorpusReport{
		RulesVersion: DelegatedFirstTouchCopyRulesV1,
		Messages:     len(messages), SourceMix: map[string]int{}, Violations: map[string]int{},
		NearDuplicateDefinition: "same factual focus + service practice + route class + recipient purpose after removing company and person identity",
	}
	exact := map[string]int{}
	near := map[string]int{}
	subjects := map[string]int{}
	openings := map[string]int{}
	practices := map[string]int{}
	ctas := map[string]int{}
	lengths := make([]int, 0, len(messages))

	for i := range messages {
		message := messages[i]
		copy := message.Copy
		report.SourceMix[firstNonEmpty(strings.TrimSpace(message.Source), "unknown")]++
		exact[normalizeForCorpus(copy.Subject)+"\x00"+normalizeForCorpus(copy.Body)]++
		near[copy.SemanticSignature]++
		subjects[copy.SubjectKey]++
		openings[copy.OpeningKey]++
		practices[copy.PracticeKey]++
		ctas[copy.CTAKey]++
		lengths = append(lengths, len(strings.Fields(copy.Body)))

		expected := buildDelegatedRoutingCopy(message.Account, message.Candidate, message.Evidence)
		blob := copy.Subject + "\n" + copy.Body
		folded := foldASCII(strings.ToLower(blob))
		if strings.TrimSpace(copy.Subject) == "" || strings.TrimSpace(copy.Body) == "" {
			report.EmptySubjectOrBody++
		}
		if copy.Subject != expected.Subject || copy.Body != expected.Body || copy.FactUsed != expected.FactUsed {
			report.UnsupportedClaims++
		}
		if delegatedContainsAny(folded, "como contratante", "figura como contratante", "orgao contratante", "órgão contratante") {
			report.BuyerSupplierConfusions++
		}
		if delegatedCorpusGuessedPerson(copy, expected, message.Candidate) {
			report.GuessedPeople++
		}
		if LooksLikeInternalReasoning(blob) || looksLikeMetadataDump(blob) || containsDumpLabel(blob) ||
			qaEnumRe.MatchString(blob) || qaKeyValueRe.MatchString(blob) || qaScoreRe.MatchString(blob) {
			report.InternalMetadataLeaks++
		}
		if delegatedContainsAny(folded,
			"idiota", "incompetente", "burro", "preguicoso", "responda agora", "ultima chance", "nao perca", "voce precisa") {
			report.OffensiveOrManipulative++
		}
		if copy.RouteClass != expected.RouteClass || copy.CTAKey != expected.CTAKey || copy.CTA != expected.CTA ||
			!strings.Contains(copy.Body, expected.CTA) {
			report.RouteInappropriate++
		}
		if !strings.Contains(copy.Body, delegatedContactExit) {
			report.ContactExitMissing++
		}
	}

	report.ExactDuplicateGroups, report.ExactDuplicateMessages, report.LargestExactGroup = duplicateSummary(exact)
	report.NearDuplicateGroups, report.NearDuplicateMessages, report.LargestNearGroup = duplicateSummary(near)
	report.SubjectConcentration = topCorpusConcentrations(subjects, len(messages), "fact_focus")
	report.OpeningConcentration = topCorpusConcentrations(openings, len(messages), "fact_support")
	report.PracticeLineConcentration = topCorpusConcentrations(practices, len(messages), "service_code")
	report.CTAConcentration = topCorpusConcentrations(ctas, len(messages), "route_class+recipient_purpose")
	report.LengthWords = corpusLengthStats(lengths)

	// Exact reader-facing content may occur once. Unlike a percentage cap, this
	// has an editorial meaning at every corpus size: two recipients must never
	// receive the same complete subject and body from distinct evidence briefs.
	if report.ExactDuplicateMessages > 0 {
		report.Violations["exact_content_reused"] = report.ExactDuplicateMessages
	}
	for code, count := range map[string]int{
		"unsupported_claim":                  report.UnsupportedClaims,
		"buyer_supplier_confusion":           report.BuyerSupplierConfusions,
		"guessed_person":                     report.GuessedPeople,
		"internal_metadata_leak":             report.InternalMetadataLeaks,
		"offensive_or_manipulative_language": report.OffensiveOrManipulative,
		"subject_or_body_empty":              report.EmptySubjectOrBody,
		"route_inappropriate":                report.RouteInappropriate,
		"contact_exit_missing":               report.ContactExitMissing,
	} {
		if count > 0 {
			report.Violations[code] = count
		}
	}
	return report
}

func delegatedCorpusGuessedPerson(copy, expected delegatedRoutingCopy, cand *models.OutreachContactCandidate) bool {
	if copy.PersonUsed != expected.PersonUsed {
		return true
	}
	if expected.PersonUsed != "" {
		return !strings.HasPrefix(copy.Body, "Olá, "+expected.PersonUsed+",")
	}
	return looksInventedPersonGreeting(strings.SplitN(copy.Body, "\n", 2)[0]) ||
		(cand != nil && CandidateRouteClass(cand) != RouteClassDirectPerson && copy.PersonUsed != "")
}

func duplicateSummary(groups map[string]int) (groupCount, messageCount, largest int) {
	for key, count := range groups {
		if key == "" || count < 2 {
			continue
		}
		groupCount++
		messageCount += count
		if count > largest {
			largest = count
		}
	}
	return groupCount, messageCount, largest
}

func topCorpusConcentrations(counts map[string]int, total int, dimension string) []CorpusConcentration {
	out := make([]CorpusConcentration, 0, len(counts))
	for value, count := range counts {
		if value == "" || count == 0 {
			continue
		}
		share := 0.0
		if total > 0 {
			share = float64(count) / float64(total)
		}
		out = append(out, CorpusConcentration{Value: value, Count: count, Share: share, EvidenceDimension: dimension})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Value < out[j].Value
	})
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

func corpusLengthStats(lengths []int) CorpusLengthStats {
	if len(lengths) == 0 {
		return CorpusLengthStats{}
	}
	sort.Ints(lengths)
	total := 0
	for _, n := range lengths {
		total += n
	}
	return CorpusLengthStats{
		Minimum: lengths[0], Average: float64(total) / float64(len(lengths)),
		P50: corpusPercentile(lengths, 50), P90: corpusPercentile(lengths, 90),
		P95: corpusPercentile(lengths, 95), P99: corpusPercentile(lengths, 99),
		Maximum: lengths[len(lengths)-1],
	}
}

func corpusPercentile(sorted []int, percent int) int {
	if len(sorted) == 0 {
		return 0
	}
	index := (percent*len(sorted) + 99) / 100
	if index < 1 {
		index = 1
	}
	if index > len(sorted) {
		index = len(sorted)
	}
	return sorted[index-1]
}
