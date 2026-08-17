package confenge

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/models"
)

func TestEnvDriftCannotReactivateForbiddenAutomation(t *testing.T) {
	t.Setenv(EnvEnabled, "true")
	t.Setenv(EnvRequireHuman, "true")
	t.Setenv(EnvAutoSend, "true")
	t.Setenv(EnvGreenAutorun, "false")
	cfg := LoadConfig()
	if err := cfg.ValidateStartup("test"); err == nil {
		t.Fatal("CONFENGE_AUTO_SEND_ENABLED=true must fail startup")
	}
	if err := cfg.ForbiddenAutomation(); err == nil || !strings.Contains(err.Error(), EnvAutoSend) {
		t.Fatalf("ForbiddenAutomation auto-send: %v", err)
	}

	t.Setenv(EnvAutoSend, "false")
	t.Setenv(EnvGreenAutorun, "true")
	cfg = LoadConfig()
	if err := cfg.ValidateStartup("dev"); err == nil {
		t.Fatal("CONFENGE_GREEN_AUTORUN_ENABLED=true must fail startup")
	}

	t.Setenv(EnvGreenAutorun, "false")
	t.Setenv(EnvRequireHuman, "false")
	cfg = LoadConfig()
	if err := cfg.ValidateStartup("production"); err == nil {
		t.Fatal("CONFENGE_REQUIRE_HUMAN_APPROVAL=false must fail startup")
	}

	t.Setenv(EnvRequireHuman, "true")
	cfg = LoadConfig()
	if err := cfg.ValidateStartup("test"); err != nil {
		t.Fatalf("safe env must start: %v", err)
	}
}

func TestGreenAutorunAndBatchCreateZeroSendJobsWhenEnvFlipped(t *testing.T) {
	ctx := context.Background()
	repo := newMemRepo()
	org, user, accID := uuid.New(), uuid.New(), uuid.New()
	camp := uuid.New()
	_ = repo.UpsertOrgSettings(ctx, &models.OutreachOrgSettings{OrganizationID: org, CampaignID: &camp})
	acc := &models.OutreachAccount{
		ID: accID, OrganizationID: org, CNPJ14: "12345678000199",
		RazaoSocial: "Engenharia Alpha LTDA", ServiceCode: "REAJUSTE",
		FactToMention:   "prorrogação do contrato 001/2025 no PNCP",
		ActivationState: "ACTIONABLE_NOW", TargetFitSendTier: "A_AUTOMATIC",
		EmailSendReady: true, MessageContextHash: "ctx-hash-1",
		QueueState: models.OutreachQueueNeedsReview,
	}
	_, _ = repo.UpsertAccount(ctx, acc)
	cand := &models.OutreachContactCandidate{
		ID: uuid.New(), OrganizationID: org, AccountID: accID,
		Name: "Ana Silva", Email: "ana.silva@alphaeng.com.br",
		VerificationStatus: models.OutreachVerifyOfficialSource, Confidence: "HIGH",
		Recommended: true, EmailSendReady: true, OwnershipStatus: "COMPANY_OWNED",
	}
	_, _ = repo.UpsertCandidate(ctx, cand)
	tp := &models.OutreachTouchpoint{
		OrganizationID: org, AccountID: accID, ContactCandidateID: &cand.ID,
		Ordinal: 1, Channel: models.OutreachChannelEmail, Purpose: models.TouchpointPurposeInitial,
		State: models.TouchpointNeedsReview, Recipient: cand.Email,
		Subject: "Sobre Alpha Eng", BodyText: "Olá Ana,\n\nNotei a prorrogação.\n\nFaz sentido?\n\n" + SignaturePlain(),
		ServiceCode: "REAJUSTE", FactUsed: acc.FactToMention, GeneratedContextHash: "ctx-hash-1",
		IdempotencyKey: "drift-autorun-1",
	}
	RecomputeContentHash(tp)
	_ = repo.InsertTouchpoint(ctx, tp)
	ok := true
	draft := &models.OutreachDraft{
		OrganizationID: org, AccountID: accID, ContactCandidateID: &cand.ID,
		Channel: models.OutreachChannelEmail, Subject: tp.Subject, BodyText: tp.BodyText,
		RecipientEmail: cand.Email, ServiceCode: "REAJUSTE", FactUsed: acc.FactToMention,
		Provider: "template", Model: TemplatePolicyVersionV1, Status: models.OutreachDraftNeedsReview,
		RiskClass: "GREEN", ValidationOK: &ok,
	}
	_ = repo.UpsertDraft(ctx, draft)
	tp.DraftID = &draft.ID
	_ = repo.UpdateTouchpoint(ctx, tp)

	svc := NewService(Config{
		Enabled: true, RequireHumanApproval: true, GreenAutorunEnabled: true,
		MaxInitialEmailWords: 120, AppEnv: "test",
	}, repo, nil).(*service)
	store := newMemPolicyStore()
	svc.WirePolicyAuth(store)
	if _, xerr := svc.AuthorizeCampaignPolicy(ctx, org, user, &models.CampaignPolicyAuthorization{
		CampaignID: camp, Channel: "EMAIL", AllowedRiskClass: "GREEN",
		SenderMailbox: "tiago.sasaki@confenge.com.br", AllowPolicyTemplateGREEN: true,
		EffectiveAt: time.Now().UTC().Add(-time.Minute),
	}); xerr != nil {
		t.Fatal(xerr)
	}

	out, dec, xerr := svc.TryGreenAutorun(ctx, org, user, tp.ID)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if dec.Allow || !containsString(dec.Reasons, "individual_approval_required") {
		t.Fatalf("autorun must refuse: %+v", dec)
	}
	if out != nil && out.State == models.TouchpointQueued {
		t.Fatal("TryGreenAutorun must not queue")
	}

	queued, skipped, details, xerr := svc.RunGreenAutorunBatch(ctx, org, user, 10)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if queued != 0 {
		t.Fatalf("batch queued=%d details=%v", queued, details)
	}
	if skipped == 0 && len(details) == 0 {
		t.Fatal("batch must visit review items and skip them")
	}
	stored, _ := repo.GetTouchpoint(ctx, org, tp.ID)
	if stored.State == models.TouchpointQueued || stored.ApprovedBy != nil {
		t.Fatalf("batch must not mint approval/queue: %+v", stored)
	}
}

func TestStaleApprovalAndEvidenceVersionsRefuseTransport(t *testing.T) {
	tp := sampleTouch(models.OutreachChannelEmail, "ana@empresa.com.br", "Sobre ACME", "Corpo aprovado com fato.")
	tp.EvidenceIDs = []string{"ev-1"}
	tp.GeneratedContextHash = "ctx-v1"
	human := uuid.New()
	if err := ApplyHumanApproval(tp, human, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := CanTransport(tp); err != nil {
		t.Fatalf("fresh individual approval must transport: %v", err)
	}

	tp.EvidenceIDs = []string{"ev-2"}
	if err := CanTransport(tp); err == nil {
		t.Fatal("evidence version drift must refuse transport")
	}
	tp.EvidenceIDs = []string{"ev-1"}
	tp.GeneratedContextHash = "ctx-v2"
	if err := CanTransport(tp); err == nil {
		t.Fatal("context/evidence version drift must refuse transport")
	}
	tp.GeneratedContextHash = "ctx-v1"
	tp.Recipient = "outro@empresa.com.br"
	if err := CanTransport(tp); err == nil {
		t.Fatal("recipient drift must refuse transport")
	}
	tp.Recipient = "ana@empresa.com.br"
	tp.BodyText = "corpo editado depois da aprovação"
	if err := CanTransport(tp); err == nil {
		t.Fatal("content drift must refuse transport")
	}
}

func TestPolicyOnlyAndMissingApprovalCannotBirthSendJob(t *testing.T) {
	allowConfengeSendingForTest(t)
	ctx := context.Background()
	repo := newMemRepoWithSettings()
	org, user, accID := uuid.New(), uuid.New(), uuid.New()
	svc := NewService(Config{Enabled: true, RequireHumanApproval: true, DefaultDailyLimit: 10, MaxInitialEmailWords: 120}, repo, nil).(*service)
	contacts := &mockContacts{}
	svc.WireExecution(&mockCampaigns{}, contacts)

	acc := &models.OutreachAccount{
		ID: accID, OrganizationID: org, CNPJ14: "11222333000181",
		RazaoSocial: "ACME", NomeFantasia: "ACME", QueueState: models.OutreachQueueApproved,
		FactToMention: "prorrogacao do contrato", ServiceCode: "ADDITIVE_REVIEW",
	}
	_, _ = repo.UpsertAccount(ctx, acc)
	cand := &models.OutreachContactCandidate{
		ID: uuid.New(), OrganizationID: org, AccountID: accID, Name: "Ana Silva",
		Email: "ana@example.com", VerificationStatus: models.OutreachVerifyOfficialSource, Recommended: true,
	}
	_, _ = repo.UpsertCandidate(ctx, cand)
	d := &models.OutreachDraft{
		ID: uuid.New(), OrganizationID: org, AccountID: accID, ContactCandidateID: &cand.ID,
		Subject: "Sobre ACME", BodyText: "Ola Ana,\n\nNotei a prorrogacao do contrato. Faz sentido conversarmos?\n\nPosso enviar checklist?",
		FactUsed: "prorrogacao do contrato", ServiceCode: "ADDITIVE_REVIEW",
		Status: models.OutreachDraftApproved, RiskClass: "GREEN", RecipientEmail: cand.Email,
		VerificationStatus: models.OutreachVerifyOfficialSource,
	}
	ok := true
	d.ValidationOK = &ok
	_ = repo.UpsertDraft(ctx, d)

	missing := &models.OutreachTouchpoint{
		OrganizationID: org, AccountID: accID, ContactCandidateID: &cand.ID,
		Ordinal: 1, Channel: models.OutreachChannelEmail, Purpose: models.TouchpointPurposeInitial,
		State: models.TouchpointNeedsReview, Recipient: cand.Email,
		Subject: d.Subject, BodyText: d.BodyText, DraftID: &d.ID, IdempotencyKey: "bypass-missing",
	}
	RecomputeContentHash(missing)
	_ = repo.InsertTouchpoint(ctx, missing)
	if _, xerr := svc.QueueTouchpoint(ctx, org, user, missing.ID); xerr == nil {
		t.Fatal("missing approval must not queue")
	}
	if _, xerr := svc.EnrollDraft(ctx, org, user, d.ID); xerr == nil {
		t.Fatal("missing approval must not enroll")
	}
	if len(contacts.added) != 0 {
		t.Fatalf("no contact/send job on missing approval: %+v", contacts.added)
	}

	store := newMemPolicyStore()
	svc.WirePolicyAuth(store)
	auth, xerr := svc.AuthorizeCampaignPolicy(ctx, org, user, &models.CampaignPolicyAuthorization{
		CampaignID: uuid.New(), Channel: "EMAIL", AllowedRiskClass: "GREEN",
		EffectiveAt: time.Now().UTC().Add(-time.Minute), SenderMailbox: "tiago.sasaki@confenge.com.br",
	})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if err := ApplyCampaignPolicyAuthorization(missing, auth, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	_ = repo.UpdateTouchpoint(ctx, missing)
	if CanTransport(missing) == nil {
		t.Fatal("policy-only must not transport")
	}
	if _, xerr := svc.QueueTouchpoint(ctx, org, user, missing.ID); xerr == nil {
		t.Fatal("policy-only must not queue")
	}
	if _, xerr := svc.EnrollDraft(ctx, org, user, d.ID); xerr == nil {
		t.Fatal("policy-only must not enroll")
	}
	if len(contacts.added) != 0 {
		t.Fatal("policy-only must create zero send jobs")
	}
}

func TestAutoSendAndGreenAutorunConfigRefuseQueueAndEnroll(t *testing.T) {
	allowConfengeSendingForTest(t)
	ctx := context.Background()
	repo := newMemRepoWithSettings()
	org, user, accID := uuid.New(), uuid.New(), uuid.New()
	svc := NewService(Config{
		Enabled: true, RequireHumanApproval: true, AutoSendEnabled: true,
		DefaultDailyLimit: 10, MaxInitialEmailWords: 120,
	}, repo, nil).(*service)
	contacts := &mockContacts{}
	svc.WireExecution(&mockCampaigns{}, contacts)
	acc := &models.OutreachAccount{
		ID: accID, OrganizationID: org, CNPJ14: "11222333000181",
		RazaoSocial: "ACME", NomeFantasia: "ACME", QueueState: models.OutreachQueueApproved,
		FactToMention: "prorrogacao do contrato", ServiceCode: "ADDITIVE_REVIEW",
	}
	_, _ = repo.UpsertAccount(ctx, acc)
	cand := &models.OutreachContactCandidate{
		ID: uuid.New(), OrganizationID: org, AccountID: accID, Name: "Ana Silva",
		Email: "ana@example.com", VerificationStatus: models.OutreachVerifyOfficialSource, Recommended: true,
	}
	_, _ = repo.UpsertCandidate(ctx, cand)
	d := &models.OutreachDraft{
		ID: uuid.New(), OrganizationID: org, AccountID: accID, ContactCandidateID: &cand.ID,
		Subject: "Sobre ACME", BodyText: "Ola Ana,\n\nNotei a prorrogacao do contrato. Faz sentido conversarmos?\n\nPosso enviar checklist?",
		FactUsed: "prorrogacao do contrato", ServiceCode: "ADDITIVE_REVIEW",
		Status: models.OutreachDraftApproved, RiskClass: "GREEN", RecipientEmail: cand.Email,
	}
	ok := true
	d.ValidationOK = &ok
	_ = repo.UpsertDraft(ctx, d)
	tp := &models.OutreachTouchpoint{
		OrganizationID: org, AccountID: accID, ContactCandidateID: &cand.ID,
		Ordinal: 1, Channel: models.OutreachChannelEmail, Purpose: models.TouchpointPurposeInitial,
		State: models.TouchpointNeedsReview, Recipient: cand.Email,
		Subject: d.Subject, BodyText: d.BodyText, DraftID: &d.ID, IdempotencyKey: "autosend-refuse",
	}
	if err := ApplyHumanApproval(tp, user, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	_ = repo.InsertTouchpoint(ctx, tp)
	if _, xerr := svc.QueueTouchpoint(ctx, org, user, tp.ID); xerr == nil {
		t.Fatal("auto-send config must not queue")
	}
	if _, xerr := svc.EnrollDraft(ctx, org, user, d.ID); xerr == nil {
		t.Fatal("auto-send config must not enroll")
	}
	if len(contacts.added) != 0 {
		t.Fatal("auto-send config must create zero send jobs")
	}

	svc.cfg.AutoSendEnabled = false
	svc.cfg.GreenAutorunEnabled = true
	if _, xerr := svc.QueueTouchpoint(ctx, org, user, tp.ID); xerr == nil {
		t.Fatal("green-autorun config must not queue")
	}
	if _, xerr := svc.EnrollDraft(ctx, org, user, d.ID); xerr == nil {
		t.Fatal("green-autorun config must not enroll")
	}
}

func TestDuplicateGateDoesNotMintSecondSend(t *testing.T) {
	allowConfengeSendingForTest(t)
	repo := newMemRepoWithSettings()
	org, accID := uuid.New(), uuid.New()
	_, _ = repo.UpsertAccount(context.Background(), &models.OutreachAccount{
		ID: accID, OrganizationID: org, CNPJ14: "12345678000166",
	})
	candidateID := uuid.New()
	_, _ = repo.UpsertCandidate(context.Background(), &models.OutreachContactCandidate{
		ID: candidateID, OrganizationID: org, AccountID: accID, Email: "lead@example.com",
	})
	campaignID, contactID := bindTransportableEnrollment(t, repo.memRepo, org, accID, candidateID, "lead@example.com")
	clock := &dispatch.FixedClock{T: time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)}
	cfg := dispatch.DefaultConfig()
	cfg.WindowStart, cfg.WindowEnd, cfg.Timezone, cfg.MinGap = "00:00", "23:59", "UTC", 0
	cfg.BusinessDaysOnly = false
	cfg.SendsPerHour = 10
	svc := &service{
		cfg:  Config{Enabled: true, RequireHumanApproval: true},
		repo: repo, governor: dispatch.NewGovernor(cfg, dispatch.NewMemoryStore(), clock),
	}
	seq := uuid.New()
	first := svc.GateCampaignEmail(context.Background(), org, DefaultCampaignName, "lead@example.com", campaignID, contactID, seq)
	if first.Kind != GateProceed {
		t.Fatalf("first gate: %+v", first)
	}
	if err := svc.governor.Commit(context.Background(), first.ReservationID); err != nil {
		t.Fatal(err)
	}
	replay := svc.GateCampaignEmail(context.Background(), org, DefaultCampaignName, "lead@example.com", campaignID, contactID, seq)
	if replay.Kind != GateAlready {
		t.Fatalf("replay must be GateAlready, got %+v", replay)
	}
}

func TestPauseDispatchAuditsKillSwitch(t *testing.T) {
	allowConfengeSendingForTest(t)
	t.Setenv(EnvKillSwitchPath, filepath.Join(t.TempDir(), "kill"))
	repo := newMemRepo()
	audit := &recordingAudit{}
	svc := NewService(Config{Enabled: true, RequireHumanApproval: true, DefaultDailyLimit: 10, MaxInitialEmailWords: 120}, repo, audit).(*service)
	clock := &dispatch.FixedClock{T: time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)}
	cfg := dispatch.DefaultConfig()
	cfg.WindowStart, cfg.WindowEnd, cfg.Timezone = "00:00", "23:59", "UTC"
	svc.WireDispatchGovernor(dispatch.NewGovernor(cfg, dispatch.NewMemoryStore(), clock))
	org, user := uuid.New(), uuid.New()
	if xerr := svc.PauseDispatch(context.Background(), org, user, "safety-invariant"); xerr != nil {
		t.Fatal(xerr)
	}
	if svc.cfg.SendingAllowed() {
		t.Fatal("pause must engage kill switch")
	}
	if !audit.saw("dispatch_pause") {
		t.Fatalf("pause must be audited: %+v", audit.actions)
	}
	if xerr := svc.ResumeDispatch(context.Background(), org, user); xerr != nil {
		t.Fatal(xerr)
	}
	if !audit.saw("dispatch_resume") {
		t.Fatal("resume must be audited")
	}
}

type recordingAudit struct {
	actions []string
}

func (r *recordingAudit) LogAction(_ context.Context, _, _ uuid.UUID, _ models.AuditAction, _ models.AuditEntityType, _ *uuid.UUID, _, _ string, changes, metadata map[string]string) {
	if changes != nil {
		if a := changes["action"]; a != "" {
			r.actions = append(r.actions, a)
		}
	}
	if metadata != nil {
		if a := metadata["action"]; a != "" {
			r.actions = append(r.actions, a)
		}
	}
}

func (r *recordingAudit) saw(action string) bool {
	for _, a := range r.actions {
		if a == action {
			return true
		}
	}
	return false
}
