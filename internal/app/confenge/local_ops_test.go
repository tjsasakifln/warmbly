package confenge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/whatsapp"
	"github.com/warmbly/warmbly/internal/models"
)

func TestBuildReadinessPanelFields(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	imp := now.Add(-5 * time.Minute)
	cfg := Config{
		Enabled: true, RequireHumanApproval: true, DefaultDailyLimit: 10,
		FeedURL:           "file://testdata/demo_3_companies.json",
		OutcomeWebhookURL: "https://example.com/hook", OutcomeWebhookSecret: "supersecret",
	}
	r := BuildReadiness(cfg, ReadinessInputs{
		EmailReady: true, WhatsAppReady: false, WhatsAppPolicyBlocked: true,
		LastImportAt: &imp, AIConfigured: false,
		Queue: &models.OutreachQueueSummary{NeedsReview: 2, ReadyToGenerate: 1, Approved: 1},
		WA:    &whatsapp.Config{Enabled: true, Provider: whatsapp.ProviderMock},
		Now:   now,
	})
	if r.Email != ReadyOK {
		t.Fatalf("email=%s", r.Email)
	}
	if r.WhatsApp != ReadyBlockedPolicy {
		t.Fatalf("whatsapp=%s want blocked_by_policy", r.WhatsApp)
	}
	if r.FeedAgeLabel != "5m" {
		t.Fatalf("feed_age=%s", r.FeedAgeLabel)
	}
	if r.OutcomeLoop != ReadyOK {
		t.Fatalf("outcome=%s", r.OutcomeLoop)
	}
	if r.AI != ReadyFallback {
		t.Fatalf("ai=%s", r.AI)
	}
	if r.GovernorCap != 10 {
		t.Fatalf("governor=%d", r.GovernorCap)
	}
	if r.QueueCount != 4 {
		t.Fatalf("queue=%d", r.QueueCount)
	}
}

func TestKillSwitchBlocksSendingAllowed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kill")
	t.Setenv(EnvKillSwitchPath, path)
	t.Setenv(EnvSendingPaused, "false")

	cfg := Config{Enabled: true, SendingPaused: false}
	if !cfg.SendingAllowed() {
		t.Fatal("expected allowed before kill file")
	}
	if err := EngageKillSwitch(); err != nil {
		t.Fatal(err)
	}
	if cfg.SendingAllowed() {
		t.Fatal("expected blocked by kill file")
	}
	if err := ReleaseKillSwitch(); err != nil {
		t.Fatal(err)
	}
	if !cfg.SendingAllowed() {
		t.Fatal("expected allowed after release")
	}

	cfg.SendingPaused = true
	if cfg.SendingAllowed() {
		t.Fatal("env pause should block")
	}
}

func TestRunPreflightFailsOnAutoSend(t *testing.T) {
	cfg := Config{
		Enabled: true, AutoSendEnabled: true, RequireHumanApproval: true,
		DefaultDailyLimit: 10,
	}
	rep := RunPreflight(cfg, PreflightDeps{})
	if rep.OK {
		t.Fatal("expected fail when auto-send on")
	}
	out := FormatPreflight(rep)
	if !strings.Contains(out, "auto_send") {
		t.Fatalf("report missing auto_send: %s", out)
	}
}

func TestRunPreflightAIOptionalWarn(t *testing.T) {
	t.Setenv("AI_PROVIDER", "")
	cfg := Config{Enabled: true, RequireHumanApproval: true, DefaultDailyLimit: 10}
	rep := RunPreflight(cfg, PreflightDeps{})
	if !rep.OK {
		t.Fatalf("should pass with warns only: fails=%d", rep.Fails)
	}
	found := false
	for _, c := range rep.Checks {
		if c.Name == "ai" && c.Severity == CheckWarn {
			found = true
		}
	}
	if !found {
		t.Fatal("expected AI soft warn")
	}
}

func TestImportDemo3CompaniesDryRunAndApply(t *testing.T) {
	repo := newMemRepo()
	svc := testSvc(repo)
	org := uuid.New()
	raw, err := os.ReadFile("testdata/demo_3_companies.json")
	if err != nil {
		t.Fatal(err)
	}
	dry, xerr := svc.ImportFromBytes(context.Background(), org, nil, raw, ImportOptions{DryRun: true})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if dry.Counts.Creates < 3 {
		t.Fatalf("dry creates=%+v", dry.Counts)
	}
	sum := FormatImportSummary(dry)
	if !strings.Contains(sum, "creates=") || !strings.Contains(sum, "blocked=") {
		t.Fatalf("summary: %s", sum)
	}
	// dry-run must not persist
	if acc, _ := repo.GetAccountByCNPJ(context.Background(), org, "11222333000181"); acc != nil {
		t.Fatal("dry-run leaked account")
	}

	run, xerr := svc.ImportFromBytes(context.Background(), org, nil, raw, ImportOptions{})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if run.Counts.Creates < 3 {
		t.Fatalf("apply creates=%+v", run.Counts)
	}
	acme, _ := repo.GetAccountByCNPJ(context.Background(), org, "11222333000181")
	if acme == nil {
		t.Fatal("missing ACME")
	}
	planalto, _ := repo.GetAccountByCNPJ(context.Background(), org, "55444333000122")
	if planalto == nil || planalto.QueueState != models.OutreachQueueNeedsContact {
		t.Fatalf("planalto=%+v", planalto)
	}
}

func TestDemoReimportPreservesDNC(t *testing.T) {
	repo := newMemRepo()
	svc := testSvc(repo)
	org := uuid.New()
	raw, err := os.ReadFile("testdata/demo_3_companies.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, xerr := svc.ImportFromBytes(context.Background(), org, nil, raw, ImportOptions{}); xerr != nil {
		t.Fatal(xerr)
	}
	acc, _ := repo.GetAccountByCNPJ(context.Background(), org, "11222333000181")
	if acc == nil {
		t.Fatal("missing")
	}
	acc.DoNotContact = true
	acc.QueueState = models.OutreachQueueDoNotContact
	if _, err := repo.UpsertAccount(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	if _, xerr := svc.ImportFromBytes(context.Background(), org, nil, raw, ImportOptions{}); xerr != nil {
		t.Fatal(xerr)
	}
	again, _ := repo.GetAccountByCNPJ(context.Background(), org, "11222333000181")
	if again == nil || !again.DoNotContact {
		t.Fatal("DNC must not be reactivated on reimport")
	}
}

func TestOfflineGenerateApproveWithTemplate(t *testing.T) {
	// Real generate/approve path with fixture + template AI fallback (no network).
	var outcomes []models.OutreachOutcome
	rf := &memRepoOutcome{
		memRepoFull: *newMemRepoWithSettings(),
		outcomes:    &outcomes,
	}
	rf.memRepo = newMemRepo()
	rf.settings = map[uuid.UUID]*models.OutreachOrgSettings{}
	rf.drafts = map[uuid.UUID]*models.OutreachDraft{}

	cfg := Config{
		Enabled: true, DefaultDailyLimit: 10, MaxInitialEmailWords: 120,
		RequireHumanApproval: true, MaxFeedPayloadBytes: DefaultMaxPayloadBytes,
	}
	svc := NewService(cfg, rf, nil).(*service)
	svc.WireExecution(&mockCampaigns{}, &mockContacts{})

	org := uuid.New()
	user := uuid.New()
	raw, err := os.ReadFile("testdata/demo_3_companies.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, xerr := svc.ImportFromBytes(context.Background(), org, &user, raw, ImportOptions{}); xerr != nil {
		t.Fatal(xerr)
	}
	acc, _ := rf.GetAccountByCNPJ(context.Background(), org, "11222333000181")
	if acc == nil {
		t.Fatal("acme missing")
	}
	draft, xerr := svc.GenerateDraft(context.Background(), org, user, acc.ID, nil)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if draft.Status != models.OutreachDraftNeedsReview {
		t.Fatalf("status=%s", draft.Status)
	}
	// AI must not auto-approve
	if draft.Status == models.OutreachDraftApproved {
		t.Fatal("AI/template must not approve")
	}
	approved, xerr := svc.ReviewDraft(context.Background(), org, user, draft.ID, "approve", nil)
	if xerr != nil {
		// template may fail validation; force-edit with safe copy then approve
		subj := "Sobre a prorrogacao do contrato 001/2025"
		body := "Ola Ana,\n\nVi a prorrogacao do contrato 001/2025 publicada no PNCP em julho/2026. Faz sentido conversarmos sobre o controle de aditivos desta obra?\n\nPosso enviar um checklist de 1 pagina?\n\nAbraço"
		edit := &DraftEdit{Subject: &subj, BodyText: &body}
		approved, xerr = svc.ReviewDraft(context.Background(), org, user, draft.ID, "edit", edit)
		if xerr != nil {
			t.Fatal(xerr)
		}
		approved, xerr = svc.ReviewDraft(context.Background(), org, user, draft.ID, "approve", nil)
		if xerr != nil {
			t.Fatal(xerr)
		}
	}
	if approved.Status != models.OutreachDraftApproved {
		t.Fatalf("approve status=%s", approved.Status)
	}
	// kill switch blocks enroll
	t.Setenv(EnvKillSwitchPath, filepath.Join(t.TempDir(), "k"))
	if err := EngageKillSwitch(); err != nil {
		t.Fatal(err)
	}
	if _, xerr := svc.EnrollDraft(context.Background(), org, user, approved.ID); xerr == nil {
		t.Fatal("enroll should fail under kill switch")
	}
	_ = ReleaseKillSwitch()
}

func TestRedactSecret(t *testing.T) {
	if RedactSecret("") != "" {
		t.Fatal("empty")
	}
	if RedactSecret("ab") != "****" {
		t.Fatal("short")
	}
	out := RedactSecret("supersecret")
	if strings.Contains(out, "supersecret") || !strings.Contains(out, "*") {
		t.Fatalf("redact=%s", out)
	}
}
