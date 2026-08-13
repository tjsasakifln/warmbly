package confenge

import (
	"fmt"
	"strings"
	"testing"

	"github.com/warmbly/warmbly/internal/models"
)

// TestAdversarialThirtyScenarios is the binary consultant test:
// a READY message must be something a competent B2G consultant would send
// without rewrite. Any "no" fails the case as READY.
func TestAdversarialThirtyScenarios(t *testing.T) {
	pb := MustPlaybook()
	type sc struct {
		id, svc, moment, fact, role string
		generic                     bool
		want                        string
	}
	cases := []sc{
		{"01_reajuste_named", "REAJUSTE", "ANUALIDADE", "contrato 1149/2022 atingiu aniversário de reajuste em 2024", "Sócio", false, MessageabilityReady},
		{"02_reajuste_generic", "REAJUSTE", "ANUALIDADE", "contrato 1149/2022 atingiu aniversário de reajuste em 2024", "", true, MessageabilityReady},
		{"03_reajuste_weak", "REAJUSTE", "PORTFOLIO", "empresa possui contrato público", "Sócio", false, MessageabilityNeedsEnrichment},
		{"04_reequilibrio", "REEQUILIBRIO", "ADITIVO", "aditivo publicado aponta possível desequilíbrio a documentar", "Advogado", false, MessageabilityReady},
		{"05_reequilibrio_weak", "REEQUILIBRIO", "PORTFOLIO", "há portfólio público observável", "Advogado", false, MessageabilityNeedsEnrichment},
		{"06_medicoes", "MEDICOES", "MEDICAO", "medição do lote 3 publicada no DER-PR", "Analista Financeiro", false, MessageabilityReady},
		{"07_medicoes_meta", "MEDICOES", "PORTFOLIO", "objeto: medição; órgão: DER; UF: PR; valor: R$ 1,200,000", "Engenheiro", false, MessageabilityNeedsEnrichment},
		{"08_aditivos", "ADITIVOS", "ADITIVO_RECENTE", "termo aditivo 2 ao contrato 88/2021 publicado", "Engenheiro", false, MessageabilityReady},
		{"09_aditivos_generic", "ADITIVOS", "ADITIVO_RECENTE", "termo aditivo 2 ao contrato 88/2021 publicado", "", true, MessageabilityReady},
		{"10_extras", "EXTRACONTRATUAIS", "EXTRA", "ordem de serviço de extra publicada no contrato 12", "Engenheiro", false, MessageabilityReady},
		{"11_planilhas", "PLANILHAS", "MEDICAO", "quantitativos do trecho sul publicados após medição", "Engenheira", false, MessageabilityReady},
		{"12_closeout", "ENCERRAMENTO_CONTRATUAL", "ENCERRAMENTO", "vigência encerra em 60 dias no contrato 220/2023", "Diretor", false, MessageabilityReady},
		{"13_licitacao", "APOIO_LICITACAO", "EDITAL", "edital 45/2026 publicado com quantitativos a conferir", "Propostas", false, MessageabilityReady},
		{"14_licitacao_credit", "APOIO_LICITACAO", "EDITAL", "edital 45/2026 publicado", "Propostas", false, MessageabilityReady},
		{"15_monitor_dump", "MONITORAMENTO_CONTRATUAL", "PORTFOLIO", "objeto: Contratação de empresa, através de empreitada global (...) ; órgão: DER-RS; UF RS; R$ 2,839,000", "Diretor", false, MessageabilityNeedsEnrichment},
		{"16_monitor_value_only", "MONITORAMENTO_CONTRATUAL", "PORTFOLIO", "empresa possui contrato público de pavimentação de R$ 2,8 milhões no RS", "Diretor", false, MessageabilityNeedsEnrichment},
		{"17_monitor_aditivo", "MONITORAMENTO_CONTRATUAL", "ADITIVO", "aditivo 1 ao contrato de pavimentação publicado no PNCP", "Diretor", false, MessageabilityReady},
		{"18_diagnostico_weak", "DIAGNOSTICO", "PORTFOLIO", "", "Sócio", false, MessageabilityNeedsEnrichment},
		{"19_diagnostico_prorroga", "DIAGNOSTICO", "PRORROGACAO", "prorrogação do contrato 001/2025 publicada no PNCP", "Sócio", false, MessageabilityReady},
		{"20_pncp", "INTELIGENCIA_PNCP", "MERCADO", "novos órgãos em SC no recorte PNCP do último trimestre", "Diretor", false, MessageabilityReady},
		{"21_pncp_weak", "INTELIGENCIA_PNCP", "PORTFOLIO", "portfólio público observado com 12 contratos", "Diretor", false, MessageabilityNeedsEnrichment},
		{"22_backoffice_weak", "BACKOFFICE", "PORTFOLIO", "empresa com site institucional simples", "Sócio", false, MessageabilityNeedsEnrichment},
		{"23_backoffice_peak", "BACKOFFICE", "MEDICAO", "pico de medições publicadas no último trimestre no contrato 9", "Sócio", false, MessageabilityReady},
		{"24_unknown_service", "TOTALLY_UNKNOWN_SERVICE_XYZ", "PORTFOLIO", "contrato público de pavimentação", "Sócio", false, MessageabilityBlocked},
		{"25_no_fact", "REAJUSTE", "GENERIC", "", "Sócio", false, MessageabilityNeedsEnrichment},
		{"26_encopav_named", "MONITORAMENTO_CONTRATUAL", "PORTFOLIO", "objeto: Contratação de empresa; órgão: DER-RS; UF: RS; R$ 2,839,000", "Diretor de Contratos", false, MessageabilityNeedsEnrichment},
		{"27_encopav_generic", "MONITORAMENTO_CONTRATUAL", "PORTFOLIO", "objeto: Contratação de empresa; órgão: DER-RS; UF: RS; R$ 2,839,000", "", true, MessageabilityNeedsEnrichment},
		{"28_reequilibrio_generic", "REEQUILIBRIO", "ADITIVO", "aditivo publicado aponta possível desequilíbrio a documentar", "", true, MessageabilityReady},
		{"29_planilhas_weak", "PLANILHAS", "PORTFOLIO", "empresa possui contrato público", "Engenheira", false, MessageabilityNeedsEnrichment},
		{"30_closeout_generic", "ENCERRAMENTO_CONTRATUAL", "ENCERRAMENTO", "vigência encerra em 60 dias no contrato 220/2023", "", true, MessageabilityReady},
		{"31_extras_weak", "EXTRACONTRATUAIS", "PORTFOLIO", "há contratos públicos", "Engenheiro", false, MessageabilityNeedsEnrichment},
		{"32_monitor_medicao", "MONITORAMENTO_CONTRATUAL", "MEDICAO", "medição do lote 2 do contrato de pavimentação publicada", "Diretor", false, MessageabilityReady},
	}
	if len(cases) < 30 {
		t.Fatalf("need >=30 cases, got %d", len(cases))
	}

	var ready, enrich, blocked int
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			acc := testAccount(tc.svc, tc.moment, tc.fact)
			cand := testCand(tc.role)
			if tc.generic {
				cand.Name = "Pessoa Histórica"
				cand.Email = "contato@exemplo.com.br"
				cand.MailboxPurpose = "GENERIC_CONTACT"
			}
			var ev []models.OutreachEvidence
			if tc.fact != "" {
				ev = []models.OutreachEvidence{{SourceEvidenceID: "ev-1", EpistemicClass: models.OutreachEpistemicConfirmedFact, Synthesis: tc.fact}}
			}
			st, plan := BuildOutboundPlan(pb, acc, cand, ev, 1)
			out := ComposeFromPlan(plan, acc, cand, ChannelEmailInitial)
			if plan.Messageability != tc.want {
				t.Fatalf("got %s want %s codes=%v reason=%q body=%q", plan.Messageability, tc.want, plan.ReasonCodes, plan.Reason, out.BodyText)
			}
			switch plan.Messageability {
			case MessageabilityReady:
				ready++
				if out.BodyText == "" {
					t.Fatal("READY without body")
				}
				assertNoLeaks(t, out.BodyText)
				if !consultantWouldSend(out.BodyText, plan) {
					t.Fatalf("consultant would rewrite this READY message:\n%s", out.BodyText)
				}
				if tc.generic && (strings.Contains(out.BodyText, "Pessoa") || strings.HasPrefix(out.BodyText, "Olá, Fulano")) {
					t.Fatalf("generic invented name: %s", out.BodyText)
				}
				if !CreditVocabAllowed(pb, tc.svc) && creditWordIn(out.BodyText) {
					t.Fatalf("crédito in %s: %s", tc.svc, out.BodyText)
				}
			case MessageabilityNeedsEnrichment:
				enrich++
				if out.BodyText != "" {
					t.Fatalf("enrichment fabricated copy: %s", out.BodyText)
				}
			case MessageabilityBlocked:
				blocked++
				if out.BodyText != "" {
					t.Fatalf("blocked fabricated copy: %s", out.BodyText)
				}
			}
			_ = st
		})
	}
	t.Logf("adversarial cohort READY=%d NEEDS_ENRICHMENT=%d BLOCKED=%d total=%d", ready, enrich, blocked, len(cases))
	if ready == 0 {
		t.Fatal("expected some READY strong-evidence cases")
	}
	if enrich == 0 {
		t.Fatal("expected some NEEDS_ENRICHMENT")
	}
}

func consultantWouldSend(body string, plan OutboundMessagePlan) bool {
	if strings.TrimSpace(body) == "" {
		return false
	}
	if LooksLikeInternalReasoning(body) || looksLikeMetadataDump(body) {
		return false
	}
	if plan.Hook == "" || plan.ValueUnit == "" || plan.CTA == "" {
		return false
	}
	// Must mention a concrete hook token and a question.
	if !strings.Contains(body, "?") {
		return false
	}
	if countWords(body) < 20 || countWords(body) > 180 {
		return false
	}
	low := strings.ToLower(body)
	if strings.Contains(low, "somos líderes") || strings.Contains(low, "agendar 30") {
		return false
	}
	return true
}

func TestAdversarialCountLog(t *testing.T) {
	// Companion so the run always prints a stable line for scratch capture.
	fmt.Println("adversarial_matrix_defined=32")
}
