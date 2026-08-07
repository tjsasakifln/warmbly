package confenge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/tasks"
)

func TestDefaultCadenceStepsUseApprovedDraftMergeVars(t *testing.T) {
	steps := defaultCadenceSteps()
	if len(steps) < 1 {
		t.Fatal("no steps")
	}
	if steps[0].Subject != "{{.confenge_subject}}" {
		t.Fatalf("step0 subject must merge approved draft subject, got %q", steps[0].Subject)
	}
	if steps[0].BodyPlain != "{{.confenge_body}}" {
		t.Fatalf("step0 body must merge approved draft body, got %q", steps[0].BodyPlain)
	}
	for _, s := range steps {
		if strings.Contains(s.Subject, "\u2014") || strings.Contains(s.BodyPlain, "\u2014") ||
			strings.Contains(s.Subject, "—") || strings.Contains(s.BodyPlain, "—") {
			t.Fatalf("em dash forbidden in cadence: subject=%q body=%q", s.Subject, s.BodyPlain)
		}
	}
}

func TestEnrollCustomFieldsFeedSendTemplate(t *testing.T) {
	d := &models.OutreachDraft{
		Subject:     "Assunto aprovado",
		BodyText:    "Corpo humano revisado com fato publico.",
		ServiceCode: "ADDITIVE_REVIEW",
		FactUsed:    "fato publico",
	}
	fus, _ := json.Marshal([]DraftFollowup{
		{DelayDays: 3, BodyText: "Followup um"},
		{DelayDays: 7, BodyText: "Followup dois"},
	})
	d.FollowupsJSON = fus
	acc := &models.OutreachAccount{CNPJ14: "11222333000181", ServiceCode: "ADDITIVE_REVIEW", FactToMention: "fato publico"}
	cf := EnrollCustomFields(d, acc)
	if cf["confenge_subject"] != "Assunto aprovado" || cf["confenge_body"] != "Corpo humano revisado com fato publico." {
		t.Fatalf("custom fields: %+v", cf)
	}
	if cf["confenge_followup_1"] != "Followup um" {
		t.Fatalf("followup1: %q", cf["confenge_followup_1"])
	}

	// Simulate campaign send: RenderTemplate of bootstrap sequence with contact custom fields.
	contact := models.Contact{
		FirstName:    "Ana",
		Company:      "ACME",
		CustomFields: cf,
	}
	steps := defaultCadenceSteps()
	gotSubj := tasks.RenderTemplate(steps[0].Subject, contact)
	gotBody := tasks.RenderTemplate(steps[0].BodyPlain, contact)
	if gotSubj != "Assunto aprovado" {
		t.Fatalf("rendered subject %q want approved draft", gotSubj)
	}
	if gotBody != "Corpo humano revisado com fato publico." {
		t.Fatalf("rendered body %q want approved draft", gotBody)
	}
	gotFU := tasks.RenderTemplate(steps[1].BodyPlain, contact)
	if !strings.Contains(gotFU, "Followup um") {
		t.Fatalf("follow-up render missing approved content: %q", gotFU)
	}
}

func TestEnrollSetsCustomFieldsMatchingApprovedDraft(t *testing.T) {
	r := newMemRepoWithSettings()
	svc := NewService(Config{Enabled: true, DefaultDailyLimit: 10, MaxInitialEmailWords: 120, RequireHumanApproval: true}, r, nil).(*service)
	contacts := &mockContacts{}
	camps := &mockCampaigns{}
	svc.WireExecution(camps, contacts)
	org := uuid.New()
	accID := uuid.New()
	acc := &models.OutreachAccount{
		ID: accID, OrganizationID: org, CNPJ14: "11222333000181",
		RazaoSocial: "ACME", NomeFantasia: "ACME", QueueState: models.OutreachQueueApproved,
		FactToMention: "prorrogacao do contrato", ServiceCode: "ADDITIVE_REVIEW", SourceLeadID: "L1",
	}
	_, _ = r.UpsertAccount(context.Background(), acc)
	candID := uuid.New()
	cand := &models.OutreachContactCandidate{
		ID: candID, OrganizationID: org, AccountID: accID, Name: "Ana Silva",
		Email: "ana@example.com", VerificationStatus: models.OutreachVerifyOfficialSource, Recommended: true,
	}
	_, _ = r.UpsertCandidate(context.Background(), cand)
	approvedBody := "Ola Ana,\n\nNotei a prorrogacao do contrato. Faz sentido conversarmos?\n\nPosso enviar checklist?"
	approvedSubj := "Sobre ACME"
	fus, _ := json.Marshal([]DraftFollowup{{DelayDays: 3, BodyText: "Pergunta de encaminhamento"}})
	d := &models.OutreachDraft{
		ID: uuid.New(), OrganizationID: org, AccountID: accID, ContactCandidateID: &candID,
		Subject: approvedSubj, BodyText: approvedBody,
		FactUsed: "prorrogacao do contrato", ServiceCode: "ADDITIVE_REVIEW",
		Status: models.OutreachDraftApproved, RiskClass: "GREEN",
		RecipientEmail: "ana@example.com", VerificationStatus: models.OutreachVerifyOfficialSource,
		FollowupsJSON: fus,
	}
	ok := true
	d.ValidationOK = &ok
	_ = r.UpsertDraft(context.Background(), d)

	// Capture CreateCampaign sequences
	var createdSteps []models.CreateSequenceInput
	camps.captureSteps = &createdSteps

	out, xerr := svc.EnrollDraft(context.Background(), org, uuid.New(), d.ID)
	if xerr != nil {
		t.Fatal(xerr.Message)
	}
	if out.Status != models.OutreachDraftEnrolled {
		t.Fatalf("status %s", out.Status)
	}
	if len(contacts.added) != 1 {
		t.Fatal("expected contact")
	}
	cf := contacts.added[0].CustomFields
	if cf["confenge_subject"] != approvedSubj || cf["confenge_body"] != approvedBody {
		t.Fatalf("enroll custom fields must equal approved draft: %+v", cf)
	}
	if cf["confenge_followup_1"] != "Pergunta de encaminhamento" {
		t.Fatalf("followup field: %+v", cf)
	}
	// Bootstrap steps (from first Create) must use merge vars for step 0
	if len(createdSteps) > 0 {
		if createdSteps[0].BodyPlain != "{{.confenge_body}}" {
			t.Fatalf("campaign sequence body must be merge var, got %q", createdSteps[0].BodyPlain)
		}
	}
	// End-to-end render proof
	contact := models.Contact{CustomFields: cf, Company: "ACME", FirstName: "Ana"}
	steps := defaultCadenceSteps()
	if tasks.RenderTemplate(steps[0].BodyPlain, contact) != approvedBody {
		t.Fatal("send path would not use approved body")
	}
}

func TestNoteDNCCallsPlatformSuppress(t *testing.T) {
	r := newMemRepoWithSettings()
	svc := NewService(Config{Enabled: true, DefaultDailyLimit: 10, MaxInitialEmailWords: 120}, r, nil).(*service)
	var suppressed []string
	svc.WireSuppress(SuppressFromAdvanced{Fn: func(ctx context.Context, orgID uuid.UUID, email, reason string) error {
		suppressed = append(suppressed, email)
		return nil
	}})
	org := uuid.New()
	accID := uuid.New()
	_, _ = r.UpsertAccount(context.Background(), &models.OutreachAccount{
		ID: accID, OrganizationID: org, CNPJ14: "11222333000181", RazaoSocial: "X",
	})
	_, _ = r.UpsertCandidate(context.Background(), &models.OutreachContactCandidate{
		ID: uuid.New(), OrganizationID: org, AccountID: accID, Email: "dnc@example.com",
		VerificationStatus: models.OutreachVerifyOfficialSource,
	})
	if err := svc.NoteDNC(context.Background(), org, "dnc@example.com", "reply unsub"); err != nil {
		t.Fatal(err)
	}
	if len(suppressed) != 1 || suppressed[0] != "dnc@example.com" {
		t.Fatalf("expected platform suppress, got %v", suppressed)
	}
}
