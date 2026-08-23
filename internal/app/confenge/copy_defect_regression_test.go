package confenge

import "strings"

// liveV1RawFact is the fact_to_mention that produced cohort v1 in production on
// 2026-08-23. It is stored verbatim as the raw provenance and must stay that
// way; only the outreach projection may normalize it. The doubled
// "DOS SERVIÇOS NECESSÁRIOS" is genuinely in the PNCP object text, so the feed
// is faithful and the collapse belongs here, not upstream.
const liveV1RawFact = "objeto: CONTRATAÇÃO DE EMPRESA ESPECIALIZADA PARA EXECUÇÃO DOS SERVIÇOS NECESSÁRIOS DOS SERVIÇOS NECESSÁRIOS À RECUPERAÇÃO ESTRUTURAL DA PONTE SOB O RIO SAPUCAÍ, SITUADA DA RODOVIA FEDERAL; órgão: SUPERINTENDÊNCIA REGIONAL DO DNIT - MG; UF MG; R$ 8,763,672"

func hasRepeatedNGram(s string, n int) (string, bool) {
	words := strings.Fields(strings.ToLower(s))
	for i := 0; i+2*n <= len(words); i++ {
		a := strings.Join(words[i:i+n], " ")
		b := strings.Join(words[i+n:i+2*n], " ")
		if a == b {
			return a, true
		}
	}
	return "", false
}
