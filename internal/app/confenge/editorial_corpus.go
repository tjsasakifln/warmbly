package confenge

import (
	"strings"
)

// A cohort is judged as a corpus, not only message by message. Presentation
// and sign-off are expected to repeat; that is ordinary boilerplate. What may
// not repeat is the part that is supposed to be about this company. Detection
// here is diagnostic: it removes the offending members rather than injecting
// synonyms, because artificial variation reads worse than repetition.

// Corpus thresholds. A cohort smaller than the floor is not a corpus yet.
const (
	corpusMinCohort         = 3
	corpusUniformOpenShare  = 0.9
	corpusDistinctiveJacard = 0.9
)

// CorpusQA returns, per account ref, the corpus-level reason codes that block
// the member. An empty map means the corpus reads as individually written.
func CorpusQA(members []FrozenCohortMember) map[string][]string {
	out := map[string][]string{}
	if len(members) < corpusMinCohort {
		return out
	}
	add := func(ref, code string) {
		for _, c := range out[ref] {
			if c == code {
				return
			}
		}
		out[ref] = append(out[ref], code)
	}

	byBody := map[string][]int{}
	bySubject := map[string][]int{}
	distinctive := make([]string, len(members))
	openings := map[string]int{}

	for i, m := range members {
		bodyKey := normalizeForCorpus(m.BodyText)
		subjKey := normalizeForCorpus(m.Subject)
		byBody[bodyKey] = append(byBody[bodyKey], i)
		bySubject[subjKey] = append(bySubject[subjKey], i)
		distinctive[i] = distinctiveContent(m.BodyText)
		openings[firstSentenceOf(distinctive[i])]++
	}

	// Byte-identical bodies are a mail merge with the merge field missing.
	for _, idxs := range byBody {
		if len(idxs) < 2 {
			continue
		}
		for _, i := range idxs {
			add(members[i].AccountRef, "corpus_identical_body")
		}
	}
	// A subject shared by more than two recipients is not about any of them.
	for _, idxs := range bySubject {
		if len(idxs) <= 2 {
			continue
		}
		for _, i := range idxs {
			add(members[i].AccountRef, "corpus_identical_subject")
		}
	}
	// If nearly every message opens on the same distinctive sentence, the
	// distinctive part is not distinctive.
	for open, n := range openings {
		if open == "" {
			continue
		}
		if float64(n)/float64(len(members)) >= corpusUniformOpenShare && n >= corpusMinCohort {
			for i, m := range members {
				if firstSentenceOf(distinctive[i]) == open {
					add(m.AccountRef, "corpus_uniform_opening")
				}
			}
		}
	}
	// Pairwise near-duplication of the company-specific content.
	for i := 0; i < len(members); i++ {
		if strings.TrimSpace(distinctive[i]) == "" {
			add(members[i].AccountRef, "corpus_no_distinctive_content")
			continue
		}
		for j := i + 1; j < len(members); j++ {
			if JaccardNgramSimilarity(distinctive[i], distinctive[j], 3) < corpusDistinctiveJacard {
				continue
			}
			add(members[i].AccountRef, "corpus_near_duplicate")
			add(members[j].AccountRef, "corpus_near_duplicate")
		}
	}
	return out
}

// corpusBoilerplateMarkers are the sentences a first email is allowed to share
// with every other first email: how the sender introduces themself and signs.
var corpusBoilerplateMarkers = []string{
	"meu nome e",
	"da confenge",
	"trabalho com",
	"obrigado",
	"ola",
}

// distinctiveContent strips the sentences that are boilerplate by design, so
// similarity is measured on what should be about this company.
func distinctiveContent(body string) string {
	var kept []string
	for _, sentence := range splitSentencesPT(body) {
		s := strings.TrimSpace(sentence)
		if s == "" {
			continue
		}
		folded := foldASCII(strings.ToLower(s))
		boiler := false
		for _, marker := range corpusBoilerplateMarkers {
			if strings.Contains(folded, marker) {
				boiler = true
				break
			}
		}
		if !boiler {
			kept = append(kept, s)
		}
	}
	return strings.TrimSpace(strings.Join(kept, " "))
}

func firstSentenceOf(s string) string {
	parts := splitSentencesPT(s)
	if len(parts) == 0 {
		return ""
	}
	return normalizeForCorpus(parts[0])
}

func normalizeForCorpus(s string) string {
	return strings.Join(strings.Fields(foldASCII(strings.ToLower(strings.TrimSpace(s)))), " ")
}
