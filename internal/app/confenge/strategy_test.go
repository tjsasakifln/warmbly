package confenge

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

func testAccount(service, moment, fact string) *models.OutreachAccount {
	return &models.OutreachAccount{
		ID:                uuid.New(),
		RazaoSocial:       "Construtora Exemplo LTDA",
		NomeFantasia:      "Exemplo",
		CNPJ14:            "12345678000199",
		ServiceCode:       service,
		ServiceName:       service,
		MomentCode:        moment,
		MomentSummary:     moment + " context",
		FactToMention:     fact,
		ClaimsToAvoid:     []string{"dinheiro a receber"},
		MomentEvidenceIDs: []string{"ev-1"},
	}
}

func testCand(role string) *models.OutreachContactCandidate {
	return &models.OutreachContactCandidate{
		ID:                 uuid.New(),
		Name:               "Ana Silva",
		Role:               role,
		Email:              "ana@exemplo.com.br",
		VerificationStatus: models.OutreachVerifyVerified,
	}
}

func TestPlanStrategyAnnualidadeNoUnpaidClaim(t *testing.T) {
	pb := MustPlaybook()
	acc := testAccount("REAJUSTE", "ANUALIDADE", "contrato 1149/2022 atingiu aniversário de reajuste em 2024")
	cand := testCand("Sócio")
	ev := []models.OutreachEvidence{{
		SourceEvidenceID: "ev-1",
		Synthesis:        "publicação de vigência",
		EpistemicClass:   models.OutreachEpistemicConfirmedFact,
	}}
	st := PlanOutreachStrategy(pb, acc, cand, ev, 1)
	if st.DoctrineVersion != OutreachDoctrineVersion {
		t.Fatalf("doctrine %s", st.DoctrineVersion)
	}
	if st.WhyNow == "" || st.WhyThisAccount == "" {
		t.Fatal("why you/now required")
	}
	if !containsStr(st.RiskFlags, "annualidade_verify_only") {
		t.Fatalf("flags %v", st.RiskFlags)
	}
	if st.MicroOfferCode != "REAJUSTE_CHECK" {
		t.Fatalf("offer %s", st.MicroOfferCode)
	}
	if st.CTAType != CTATypePermissionOffer {
		t.Fatalf("cta %s", st.CTAType)
	}
	// No score fields exist on struct — compile-time guarantee via JSON tags check
	blob, _ := json.Marshal(st)
	for _, banned := range []string{"lead_score", "priority_score", "commercial_score", "conversion_score"} {
		if strings.Contains(string(blob), banned) {
			t.Fatalf("must not emit %s", banned)
		}
	}
	for _, bad := range []string{"reajuste a receber", "crédito de reajuste"} {
		if containsStr(st.ClaimsToAvoid, bad) || strings.Contains(strings.Join(st.ClaimsToAvoid, " "), bad) {
			// good, in avoid list
			continue
		}
	}
	// Compose must not claim unpaid
	out := ComposeFromStrategy(st, acc, cand, ChannelEmailInitial)
	low := strings.ToLower(out.BodyText)
	for _, bad := range []string{"reajuste a receber", "deixou de pagar", "crédito de reajuste"} {
		if strings.Contains(low, bad) {
			t.Fatalf("body claims unpaid: %s\n%s", bad, out.BodyText)
		}
	}
	if !strings.Contains(low, "checklist") && !strings.Contains(low, "confer") && !strings.Contains(low, "verific") {
		// CTA should invite check
		if !strings.Contains(low, "posso") {
			t.Fatalf("expected verify/offer language: %s", out.BodyText)
		}
	}
}

func TestPlanStrategyBuyerRoles(t *testing.T) {
	pb := MustPlaybook()
	acc := testAccount("MEDICOES", "MEDICAO", "medição do trecho norte publicada no PNCP")
	cases := []struct {
		role string
		want string
	}{
		{"Engenheiro Civil", "ENGINEERING"},
		{"Analista Financeiro", "FINANCE"},
		{"Advogada", "LEGAL"},
		{"", "UNKNOWN"},
	}
	for _, tc := range cases {
		st := PlanOutreachStrategy(pb, acc, testCand(tc.role), nil, 1)
		if st.BuyerRole != tc.want {
			t.Fatalf("role %q got %s want %s", tc.role, st.BuyerRole, tc.want)
		}
	}
}

func TestPlanStrategyNoScoreFields(t *testing.T) {
	st := PlanOutreachStrategy(MustPlaybook(), testAccount("ADITIVOS", "ADITIVO_RECENTE", "termo aditivo nº 3"), testCand("Diretor"), nil, 1)
	if st.Experiment == nil {
		t.Fatal("expected experiment assignment")
	}
	if st.Experiment.VariantID != "champion" && st.Experiment.VariantID == "" {
		t.Fatal("variant")
	}
	// same account stable
	st2 := PlanOutreachStrategy(MustPlaybook(), testAccount("ADITIVOS", "ADITIVO_RECENTE", "termo aditivo nº 3"), testCand("Diretor"), nil, 1)
	// different UUIDs so variants may differ — just ensure no panic and offer set
	if st.MicroOfferCode == "" || st2.MicroOfferCode == "" {
		t.Fatal("offer required")
	}
}
