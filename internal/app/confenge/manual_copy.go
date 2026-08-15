package confenge

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/warmbly/warmbly/internal/models"
)

var (
	evidenceIDTokenRe = regexp.MustCompile(`(?i)\b(?:ev|fact|evidence)[-_][a-z0-9-]{2,}\b`)
	ocrDigitLetterRe  = regexp.MustCompile(`([A-Za-zÁ-ÿ])0([A-Za-zÁ-ÿ])`)
	multiSpaceRe      = regexp.MustCompile(`\s{2,}`)
)

// observedMailboxLabel reflects the published local-part. comercial@ and
// vendas@ stay as local-part or become "caixa da empresa". Never invent
// "área de contratos".
func observedMailboxLabel(channelValue, targetRole string) string {
	local := emailLocal(channelValue)
	switch local {
	case "comercial", "vendas", "sales", "contato", "contact", "info", "atendimento",
		"contratos", "licitacao", "licitacoes", "compras":
		return local + "@"
	}
	if local != "" {
		return "caixa da empresa"
	}
	role := strings.TrimSpace(targetRole)
	if role != "" && !guessedContractsArea(role) {
		return role
	}
	return "caixa da empresa"
}

func guessedContractsArea(role string) bool {
	f := foldASCII(role)
	return strings.Contains(f, "area de contratos") || strings.Contains(f, "equipe de contratos")
}

func founderCompanyName(name string) string {
	name = stripLegalForm(strings.TrimSpace(name))
	name = strings.TrimSpace(name)
	if name == "" {
		return "a empresa"
	}
	return name
}

func founderSafeFact(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	raw = stripEvidenceIDs(raw)
	raw = scrubEvidentOCR(raw)
	raw = normalizePtBRAmount(raw)
	if looksLikeMetadataDump(raw) || containsDumpLabel(raw) {
		condensed := condenseMetadataFact(raw)
		if condensed == "" || looksLikeMetadataDump(condensed) || containsDumpLabel(condensed) {
			return ""
		}
		raw = condensed
	}
	raw = sanitizeHookProse(raw)
	raw = accentCommonPTBR(raw)
	raw = strings.TrimSpace(raw)
	if raw == "" || looksLikeMetadataDump(raw) || containsDumpLabel(raw) || creditWordIn(raw) {
		return ""
	}
	return clipRunes(raw, 110)
}

func founderCallReason(company, fact string) string {
	if fact == "" {
		return "Vi um fato público recente da " + company + "."
	}
	low := foldASCII(fact)
	if strings.HasPrefix(low, "contratacao publica") && countWords(fact) <= 8 {
		return "Vi um contrato público recente da " + company + "."
	}
	return "Vi " + ensureLowerStart(fact) + "."
}

func diagnosticAsk(ask, fallback string) string {
	ask = strings.TrimSpace(ask)
	if ask == "" {
		return fallback
	}
	low := foldASCII(ask)
	if !strings.Contains(ask, "?") || countWords(ask) > 16 {
		return fallback
	}
	if strings.Contains(low, "recorte objetivo") || strings.Contains(low, "posso enviar um recorte") ||
		strings.Contains(low, "posso te mandar o recorte do que eu conferiria") {
		return fallback
	}
	return ask
}

func stripEvidenceIDs(s string) string {
	return strings.TrimSpace(evidenceIDTokenRe.ReplaceAllString(s, " "))
}

func scrubEvidentOCR(s string) string {
	s = multiSpaceRe.ReplaceAllString(s, " ")
	for i := 0; i < 3; i++ {
		next := ocrDigitLetterRe.ReplaceAllStringFunc(s, func(m string) string {
			r := []rune(m)
			if len(r) != 3 {
				return m
			}
			r[1] = 'o'
			return string(r)
		})
		if next == s {
			break
		}
		s = next
	}
	return strings.TrimSpace(s)
}

func normalizePtBRAmount(s string) string {
	return usCurrencyRe.ReplaceAllStringFunc(s, func(m string) string {
		rest := amountPrefixRe.ReplaceAllString(m, "")
		rest = strings.TrimSpace(rest)
		if i := strings.LastIndex(rest, "."); i >= 0 && len(rest)-i <= 3 && !strings.Contains(rest[i+1:], ",") {
			rest = strings.ReplaceAll(rest[:i], ",", ".") + "," + rest[i+1:]
		} else {
			rest = strings.ReplaceAll(rest, ",", ".")
		}
		return "R$ " + rest
	})
}

var amountPrefixRe = regexp.MustCompile(`(?i)r\$\s*`)

func accentCommonPTBR(s string) string {
	fields := strings.Fields(s)
	for i, w := range fields {
		punct := ""
		ww := w
		if r := []rune(ww); len(r) > 0 {
			last := r[len(r)-1]
			if !unicode.IsLetter(last) && !unicode.IsNumber(last) {
				punct = string(last)
				ww = string(r[:len(r)-1])
			}
		}
		if repl, ok := commonUnaccentedPT[foldASCII(ww)]; ok {
			if rr := []rune(ww); len(rr) > 0 && unicode.IsUpper(rr[0]) {
				br := []rune(repl)
				br[0] = unicode.ToUpper(br[0])
				repl = string(br)
			}
			fields[i] = repl + punct
		}
	}
	return strings.Join(fields, " ")
}

var commonUnaccentedPT = map[string]string{
	"publico": "público", "publica": "pública", "nao": "não",
	"voce": "você", "voces": "vocês", "area": "área",
	"orgao": "órgão", "contratacao": "contratação",
	"pavimentacao": "pavimentação", "manutencao": "manutenção",
	"licenca": "licença", "sanitaria": "sanitária",
	"aniversario": "aniversário", "reequilibrio": "reequilíbrio",
}

func isManualRoute(actionType string) bool {
	switch actionType {
	case models.ActionDirectCall, models.ActionRoutedCall, models.ActionWhatsApp,
		models.ActionGenericEmail, models.ActionOtherManual, models.ActionRoleEmail:
		return true
	}
	return false
}
