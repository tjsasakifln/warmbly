package confenge

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	doubleDotRe      = regexp.MustCompile(`\.{2,}`)
	orphanCommaRe    = regexp.MustCompile(`\s+,`)
	ocrLoneSRe       = regexp.MustCompile(`(?i)\b([A-Za-zÁ-ÿ]{2,})\s+S\s+([A-Za-zÁ-ÿ]{3,})\s+S\b`)
	usAmountBareRe   = regexp.MustCompile(`(?i)(?:r\$\s*)?\b\d{1,3}(?:,\d{3}){1,4}(?:\.\d+)?\b`)
	legalFormTokenRe = regexp.MustCompile(`(?i)(?:^|[\s,])(ltda\.?|eireli(?:-epp)?|s/?a\.?|s\.a\.|eireli-epp)(?:\b|$)`)
	legalSuffixMERe  = regexp.MustCompile(`([A-ZÁ-Ÿ0-9.]{2,})(?:\s+|-)(ME|EPP|MEI)\b`)
	recuperacaoRe    = regexp.MustCompile(`(?i)\s*[-,]?\s*em recupera[cç][aã]o judicial\b`)
	gluedDeRe        = regexp.MustCompile(`(?i)\bde(engenharia|obras|servicos|serviços|pavimentacao|pavimentação)\b`)
	midTokenBreakRe  = regexp.MustCompile(`([A-Za-zÁ-ÿ]{3,})\n([A-Za-zÁ-ÿ]{3,})`)
)

// ApplyCopyHygiene only normalizes currency, OCR, CAPS, and legal vocative.
// It must not invent facts or rewrite the compose path.
func ApplyCopyHygiene(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	s = midTokenBreakRe.ReplaceAllString(s, "$1 $2")
	s = strings.ReplaceAll(s, "\n", " ")
	s = scrubEvidentOCR(s)
	s = gluedDeRe.ReplaceAllStringFunc(s, func(m string) string {
		low := strings.ToLower(m)
		return "de " + strings.TrimPrefix(low, "de")
	})
	s = ocrLoneSRe.ReplaceAllString(s, "$1 $2")
	s = normalizePtBRAmount(s)
	s = normalizeBareUSAmount(s)
	// De-shout before collapseEditalCaps, not after: collapseEditalCaps only
	// touches wholly-uppercase words of four letters or more, and once it has
	// title-cased those, the remaining "DOS"/"DA"/"SOB" are a minority and no
	// longer look like a shouted line. Reading the still-shouted original is
	// what lets the short prepositions and proper nouns be fixed at all.
	s = deshoutEditalProse(s)
	s = collapseEditalCaps(s)
	// Source object text repeats itself verbatim often enough that a recipient
	// would read it as a broken merge. Collapse after casing so the comparison
	// sees one consistent form.
	s = collapseRepeatedNGrams(s)
	s = stripLegalVocative(s)
	s = orphanCommaRe.ReplaceAllString(s, ",")
	s = doubleDotRe.ReplaceAllString(s, ".")
	s = multiSpaceRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func normalizeBareUSAmount(s string) string {
	return usAmountBareRe.ReplaceAllStringFunc(s, func(m string) string {
		if strings.Contains(strings.ToLower(m), "r$") {
			return normalizePtBRAmount(m)
		}
		if !strings.Contains(m, ",") {
			return m
		}
		return normalizePtBRAmount("R$ " + strings.TrimSpace(m))
	})
}

func collapseEditalCaps(s string) string {
	fields := strings.Fields(s)
	for i, w := range fields {
		letters := 0
		upper := 0
		for _, r := range w {
			if unicode.IsLetter(r) {
				letters++
				if unicode.IsUpper(r) {
					upper++
				}
			}
		}
		if letters < 4 || upper < letters {
			continue
		}
		keep := foldASCII(strings.Trim(w, ".,;:"))
		switch keep {
		case "pncp", "nda", "confenge", "cnpj", "utm":
			continue
		}
		fields[i] = titleWordPT(w)
	}
	return strings.Join(fields, " ")
}

func titleWordPT(w string) string {
	runes := []rune(w)
	out := make([]rune, 0, len(runes))
	first := true
	for _, r := range runes {
		if unicode.IsLetter(r) {
			if first {
				out = append(out, unicode.ToUpper(r))
				first = false
				continue
			}
			out = append(out, unicode.ToLower(r))
			continue
		}
		out = append(out, r)
		if r == '-' {
			first = true
		}
	}
	return string(out)
}

func stripLegalVocative(s string) string {
	s = recuperacaoRe.ReplaceAllString(s, "")
	s = legalFormTokenRe.ReplaceAllString(s, " ")
	s = legalSuffixMERe.ReplaceAllString(s, "$1")
	s = strings.ReplaceAll(s, "  ", " ")
	return strings.TrimSpace(s)
}

func ensureEmailSender(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return body
	}
	low := foldASCII(body)
	if strings.Contains(low, "tiago") || strings.Contains(low, "confenge") {
		return body
	}
	return body + "\n\nTiago Sasaki\nCONFENGE"
}
