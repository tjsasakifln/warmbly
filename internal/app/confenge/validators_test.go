package confenge

import (
	"testing"

	"github.com/warmbly/warmbly/internal/models"
)

func TestValidateDraftRejectsEmDash(t *testing.T) {
	acc := &models.OutreachAccount{ServiceCode: "ADDITIVE_REVIEW", FactToMention: "contrato prorrogado"}
	cand := &models.OutreachContactCandidate{
		Email: "a@example.com", VerificationStatus: models.OutreachVerifyOfficialSource,
	}
	out := &DraftOutput{
		Subject: "Oi", BodyText: "Texto com travessão — aqui", FactUsed: "contrato prorrogado",
		ServiceCode: "ADDITIVE_REVIEW", EvidenceIDs: []string{"e1"},
	}
	res := ValidateDraft(out, acc, cand, 120)
	if res.OK {
		t.Fatal("em dash must fail")
	}
}

func TestValidateDraftRejectsBannedPhrase(t *testing.T) {
	acc := &models.OutreachAccount{ServiceCode: "X", FactToMention: "fato"}
	cand := &models.OutreachContactCandidate{
		Email: "a@example.com", VerificationStatus: models.OutreachVerifyOfficialSource,
	}
	out := &DraftOutput{
		Subject: "Oi", BodyText: "Identificamos dinheiro a receber na conta.", FactUsed: "fato",
		ServiceCode: "X", EvidenceIDs: []string{"e1"},
	}
	res := ValidateDraft(out, acc, cand, 120)
	if res.OK {
		t.Fatal("banned phrase must fail")
	}
}

func TestValidateDraftRejectsUnverified(t *testing.T) {
	acc := &models.OutreachAccount{ServiceCode: "X", FactToMention: "fato publico"}
	cand := &models.OutreachContactCandidate{
		Email: "a@example.com", VerificationStatus: models.OutreachVerifyCandidateUnverified,
	}
	out := &DraftOutput{
		Subject: "Oi", BodyText: "Mensagem curta com fato publico. Faz sentido?", FactUsed: "fato publico",
		ServiceCode: "X", EvidenceIDs: []string{"e1"},
	}
	res := ValidateDraft(out, acc, cand, 120)
	if res.OK {
		t.Fatal("unverified must not enroll")
	}
}

func TestValidateDraftAcceptsClean(t *testing.T) {
	acc := &models.OutreachAccount{ServiceCode: "ADDITIVE_REVIEW", FactToMention: "prorrogacao do contrato 001"}
	cand := &models.OutreachContactCandidate{
		Email: "a@example.com", VerificationStatus: models.OutreachVerifyOfficialSource,
	}
	out := &DraftOutput{
		Subject:     "Sobre a prorrogacao",
		BodyText:    "Ola Ana,\n\nNotei a prorrogacao do contrato 001. Faz sentido conversarmos sobre aditivos?\n\nPosso enviar um checklist?",
		FactUsed:    "prorrogacao do contrato 001",
		ServiceCode: "ADDITIVE_REVIEW",
		EvidenceIDs: []string{"e1"},
		Question:    "Faz sentido conversarmos?",
		CTA:         "Posso enviar um checklist?",
	}
	res := ValidateDraft(out, acc, cand, 120)
	if !res.OK {
		t.Fatalf("expected ok, got %v", res.Errors)
	}
}

func TestClassifyRiskRedForCredit(t *testing.T) {
	acc := &models.OutreachAccount{MomentCode: "CREDIT_CLAIM", FactToMention: "x"}
	cand := &models.OutreachContactCandidate{
		Email: "a@example.com", VerificationStatus: models.OutreachVerifyOfficialSource,
	}
	class, flags := ClassifyRisk(acc, cand, &DraftOutput{BodyText: "ola"}, ValidationResult{OK: true})
	if class != "RED" {
		t.Fatalf("want RED got %s flags=%v", class, flags)
	}
}

func TestClassifyRiskGreenOfficial(t *testing.T) {
	acc := &models.OutreachAccount{MomentCode: "WARM_INTRO", FactToMention: "publicacao no portal", ServiceCode: "DIAGNOSTIC"}
	cand := &models.OutreachContactCandidate{
		Email: "a@example.com", VerificationStatus: models.OutreachVerifyOfficialSource, Role: "Analista",
	}
	class, _ := ClassifyRisk(acc, cand, &DraftOutput{BodyText: "ola", Subject: "oi"}, ValidationResult{OK: true})
	if class != "GREEN" {
		t.Fatalf("want GREEN got %s", class)
	}
}

func TestTemplateDraftNoEmDash(t *testing.T) {
	acc := &models.OutreachAccount{
		RazaoSocial: "ACME", FactToMention: "contrato X", ServiceName: "revisao",
		QuestionToAsk: "Faz sentido?", CTA: "Posso enviar?",
	}
	cand := &models.OutreachContactCandidate{Name: "Ana Silva", Email: "a@example.com", VerificationStatus: models.OutreachVerifyOfficialSource}
	out := TemplateDraft(acc, cand)
	if emDashRe.MatchString(out.BodyText + out.Subject) {
		t.Fatal("template must not contain em dash")
	}
	if out.BodyText == "" || out.Subject == "" {
		t.Fatal("template empty")
	}
}

func TestParseDraftJSONStripsFence(t *testing.T) {
	raw := "```json\n{\"subject\":\"Oi\",\"body_text\":\"Corpo\",\"fact_used\":\"f\",\"service_code\":\"S\",\"evidence_ids\":[\"1\"],\"followups\":[],\"question\":\"?\",\"cta\":\"c\",\"risk_flags\":[]}\n```"
	out, err := parseDraftJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if out.Subject != "Oi" {
		t.Fatalf("got %q", out.Subject)
	}
}
