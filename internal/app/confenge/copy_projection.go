package confenge

import (
	"strings"
	"unicode"
)

// This file owns the outreach *projection* of a raw source fact. The raw fact
// and its provenance are never modified: PNCP object text is frequently shouted,
// truncated and self-repeating, and that is faithful data. What must not reach a
// recipient is the shouting, the repetition and the broken terminal punctuation.

// ptFunctionWords are Portuguese articles, prepositions and contractions. In an
// all-caps edital they carry no emphasis, so they are lowercased rather than
// title-cased: "PONTE SOB O RIO" must read "Ponte sob o Rio", not "Ponte Sob O Rio".
var ptFunctionWords = map[string]bool{
	"a": true, "as": true, "o": true, "os": true, "e": true, "ou": true,
	"de": true, "do": true, "da": true, "dos": true, "das": true,
	"em": true, "no": true, "na": true, "nos": true, "nas": true, "num": true, "numa": true,
	"ao": true, "aos": true, "as_": true, "à": true, "às": true, "àa": true,
	"por": true, "pelo": true, "pela": true, "pelos": true, "pelas": true,
	"para": true, "com": true, "sem": true, "sob": true, "sobre": true,
	"entre": true, "ate": true, "até": true, "que": true, "se": true, "ser": true,
}

// knownAcronyms stay uppercase when the rest of a shouted line is de-shouted.
// Anything absent from this set and longer than two letters is treated as an
// ordinary shouted word, which is the safe direction: a mis-title-cased acronym
// is readable, a shouted sentence is not.
var knownAcronyms = map[string]bool{
	"pncp": true, "cnpj": true, "cpf": true, "uf": true, "crea": true, "cau": true,
	"art": true, "rrt": true, "dnit": true, "dner": true, "der": true, "ans": true,
	"epp": true, "mei": true, "sa": true, "ltda": true, "eireli": true,
	"cbuq": true, "epi": true, "abnt": true, "nbr": true, "iss": true, "inss": true,
	"srp": true, "ata": true, "tce": true, "tcu": true, "cgu": true, "ufmg": true,
}

// shoutedRatio reports the share of alphabetic words written entirely in caps.
func shoutedRatio(s string) float64 {
	words, shouted := 0, 0
	for _, w := range strings.Fields(s) {
		t := strings.Trim(w, ".,;:!?()[]\"'")
		letters, upper := 0, 0
		for _, r := range t {
			if unicode.IsLetter(r) {
				letters++
				if unicode.IsUpper(r) {
					upper++
				}
			}
		}
		if letters < 2 {
			continue
		}
		words++
		if upper == letters {
			shouted++
		}
	}
	if words == 0 {
		return 0
	}
	return float64(shouted) / float64(words)
}

// deshoutEditalProse converts an all-caps edital line into ordinary prose. It
// runs only when the line really is shouted, so a normal sentence that happens
// to contain an acronym is left alone.
func deshoutEditalProse(s string) string {
	if shoutedRatio(s) < 0.5 {
		return s
	}
	fields := strings.Fields(s)
	for i, w := range fields {
		prefix, core, suffix := splitAffixes(w)
		if core == "" {
			continue
		}
		letters, upper, digits := 0, 0, 0
		for _, r := range core {
			if unicode.IsLetter(r) {
				letters++
				if unicode.IsUpper(r) {
					upper++
				}
			}
			if unicode.IsDigit(r) {
				digits++
			}
		}
		if letters == 0 || upper != letters || digits > 0 {
			continue
		}
		key := foldASCII(strings.ToLower(core))
		switch {
		case ptFunctionWords[key] && i > 0:
			// Never lowercase the first word of the projection.
			fields[i] = prefix + strings.ToLower(core) + suffix
		case knownAcronyms[key], letters <= 2:
			// Acronyms and two-letter tokens (state codes) keep their case.
			continue
		default:
			fields[i] = prefix + titleWordPT(core) + suffix
		}
	}
	return strings.Join(fields, " ")
}

// splitAffixes separates leading/trailing punctuation from a token so casing
// rules operate on letters only and never eat a comma.
func splitAffixes(w string) (prefix, core, suffix string) {
	runes := []rune(w)
	i := 0
	for i < len(runes) && !unicode.IsLetter(runes[i]) && !unicode.IsDigit(runes[i]) {
		i++
	}
	j := len(runes)
	for j > i && !unicode.IsLetter(runes[j-1]) && !unicode.IsDigit(runes[j-1]) {
		j--
	}
	return string(runes[:i]), string(runes[i:j]), string(runes[j:])
}

// collapseRepeatedNGrams removes an n-gram immediately repeated verbatim. PNCP
// object text does this often ("DOS SERVIÇOS NECESSÁRIOS DOS SERVIÇOS
// NECESSÁRIOS"); repeating it to a recipient reads as a broken mail merge.
// Comparison folds case and accents so a de-shouted copy is still caught.
func collapseRepeatedNGrams(s string) string {
	for n := 6; n >= 2; n-- {
		for {
			collapsed, changed := collapseOnce(s, n)
			s = collapsed
			if !changed {
				break
			}
		}
	}
	return s
}

func collapseOnce(s string, n int) (string, bool) {
	words := strings.Fields(s)
	if len(words) < 2*n {
		return s, false
	}
	norm := make([]string, len(words))
	for i, w := range words {
		norm[i] = foldASCII(strings.ToLower(strings.Trim(w, ".,;:!?()[]\"'")))
	}
	for i := 0; i+2*n <= len(words); i++ {
		match := true
		for k := 0; k < n; k++ {
			if norm[i+k] == "" || norm[i+k] != norm[i+n+k] {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		// Keep the first occurrence, drop the second, and keep whatever
		// punctuation the dropped run carried at its end.
		tail := words[i+2*n-1]
		_, _, suffix := splitAffixes(tail)
		out := append([]string{}, words[:i+n]...)
		if suffix != "" {
			last := len(out) - 1
			_, core, existing := splitAffixes(out[last])
			pre, _, _ := splitAffixes(out[last])
			if existing == "" {
				out[last] = pre + core + suffix
			}
		}
		out = append(out, words[i+2*n:]...)
		return strings.Join(out, " "), true
	}
	return s, false
}

// terminateFactSentence ends a projected fact as one sentence. The composer used
// to append "." unconditionally, so a fact truncated at a comma became ",." —
// which reached two live messages in cohort v1.
func terminateFactSentence(fact string) string {
	fact = strings.TrimSpace(fact)
	if fact == "" {
		return ""
	}
	fact = strings.TrimRight(fact, " \t,;:-–—")
	if fact == "" {
		return ""
	}
	if strings.HasSuffix(fact, ".") || strings.HasSuffix(fact, "!") || strings.HasSuffix(fact, "?") {
		return fact
	}
	return fact + "."
}

// dropRedundantLeadingLabel removes a label whose first content word repeats the
// label's own head noun. condenseMetadataFact prefixes "contratação pública: "
// and the PNCP object then opens with "CONTRATAÇÃO DE EMPRESA...", which read
// "contratação pública: contratação DE Empresa" in production.
func dropRedundantLeadingLabel(label, body string) string {
	label = strings.TrimSpace(label)
	body = strings.TrimSpace(body)
	if label == "" {
		return body
	}
	if body == "" {
		return label
	}
	head := foldASCII(strings.ToLower(strings.Fields(label)[0]))
	first := strings.Fields(body)
	if len(first) > 0 && foldASCII(strings.ToLower(strings.Trim(first[0], ".,;:"))) == head {
		rest := first[1:]
		// Dropping the head noun strands the preposition that governed it:
		// "contratação DE EMPRESA" would read "contratação pública: de Empresa".
		// Dropping that too yields "contratação pública: Empresa ...".
		if len(rest) > 1 {
			lead := foldASCII(strings.ToLower(strings.Trim(rest[0], ".,;:")))
			switch lead {
			case "de", "da", "do", "dos", "das", "para", "em":
				rest = rest[1:]
			}
		}
		body = ensureLowerStart(strings.TrimSpace(strings.Join(rest, " ")))
	}
	return strings.TrimSpace(label + ": " + body)
}

// defectivePunctuation lists sequences that only ever result from mechanical
// assembly, never from prose a human would write.
var defectivePunctuation = []string{",.", ",,", ";.", "..", " ,", " ;", ":.", "!.", "?."}

// copyProjectionDefects returns machine-readable reason codes for copy that a
// recipient would read as broken. It inspects the composed subject and body,
// so it catches a defect regardless of which upstream stage introduced it.
func copyProjectionDefects(subject, body string) []string {
	var codes []string
	seen := map[string]bool{}
	add := func(c string) {
		if !seen[c] {
			seen[c] = true
			codes = append(codes, c)
		}
	}
	for _, text := range []string{subject, body} {
		t := strings.TrimSpace(text)
		if t == "" {
			continue
		}
		for _, bad := range defectivePunctuation {
			if strings.Contains(t, bad) {
				add("defective_punctuation")
				break
			}
		}
		if repeatedNGramIn(t) {
			add("repeated_ngram")
		}
		if shoutedResidueIn(t) {
			add("shouted_residue")
		}
	}
	return codes
}

// repeatedNGramIn reports a verbatim immediately-repeated run of two or more
// words. Comparison folds case and accents, matching the collapse rule.
func repeatedNGramIn(s string) bool {
	words := strings.Fields(s)
	norm := make([]string, 0, len(words))
	for _, w := range words {
		t := foldASCII(strings.ToLower(strings.Trim(w, ".,;:!?()[]\"'")))
		if t != "" {
			norm = append(norm, t)
		}
	}
	for n := 2; n <= 6; n++ {
		for i := 0; i+2*n <= len(norm); i++ {
			match := true
			for k := 0; k < n; k++ {
				if norm[i+k] != norm[i+n+k] {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
	}
	return false
}

// shoutedResidueIn reports a short all-caps Portuguese function word left over
// from an edital, e.g. "execução DOS Serviços". A genuine acronym is exempt,
// and a wholly shouted line is a different defect handled by the dump checks.
func shoutedResidueIn(s string) bool {
	for _, w := range strings.Fields(s) {
		_, core, _ := splitAffixes(w)
		if core == "" {
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
		if letters < 2 || upper != letters {
			continue
		}
		key := foldASCII(strings.ToLower(core))
		if knownAcronyms[key] {
			continue
		}
		if ptFunctionWords[key] {
			return true
		}
	}
	return false
}
