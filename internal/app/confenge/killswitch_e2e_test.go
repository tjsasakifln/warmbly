package confenge

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/models"
)

// TestKillSwitchE2EBlocksEnrollQueueAndFinalGate drives the shipped pause path
// twice. Approval scheduling remains durable while transport stays blocked.
func TestKillSwitchE2EBlocksEnrollQueueAndFinalGate(t *testing.T) {
	for i := 1; i <= 2; i++ {
		if err := runKillSwitchE2E(t, i); err != nil {
			t.Fatalf("kill-switch e2e run %d: %v", i, err)
		}
	}
}

func runKillSwitchE2E(t *testing.T, run int) error {
	t.Helper()
	t.Setenv(EnvKillSwitchPath, filepath.Join(t.TempDir(), "kill-e2e"))
	if err := EngageKillSwitch(); err != nil {
		return err
	}
	if !FileKillSwitchActive() {
		t.Fatal("kill switch file must be engaged")
	}

	ctx := context.Background()
	repo := newMemRepoWithSettings()
	org, user, accID := uuid.New(), uuid.New(), uuid.New()
	svc := NewService(Config{
		Enabled: true, RequireHumanApproval: true, DefaultDailyLimit: 10,
		MaxInitialEmailWords: 120, AutoSendEnabled: false, GreenAutorunEnabled: false,
	}, repo, nil).(*service)
	contacts := &mockContacts{}
	svc.WireExecution(&mockCampaigns{}, contacts)
	clock := &dispatch.FixedClock{T: time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)}
	cfg := dispatch.DefaultConfig()
	cfg.WindowStart, cfg.WindowEnd, cfg.Timezone, cfg.MinGap = "00:00", "23:59", "UTC", 0
	cfg.BusinessDaysOnly = false
	svc.WireDispatchGovernor(dispatch.NewGovernor(cfg, dispatch.NewMemoryStore(), clock))

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
		// Current composer: this test is about the kill switch, not about copy age.
		PromptVersion: PromptVersion,
	}
	ok := true
	d.ValidationOK = &ok
	_ = repo.UpsertDraft(ctx, d)
	tp := &models.OutreachTouchpoint{
		OrganizationID: org, AccountID: accID, ContactCandidateID: &cand.ID,
		Ordinal: 1, Channel: models.OutreachChannelEmail, Purpose: models.TouchpointPurposeInitial,
		State: models.TouchpointNeedsReview, Recipient: cand.Email,
		Subject: d.Subject, BodyText: d.BodyText, DraftID: &d.ID,
		IdempotencyKey: "kill-e2e-" + uuid.NewString(),
	}
	if err := ApplyHumanApproval(tp, user, time.Now().UTC()); err != nil {
		return err
	}
	if err := repo.InsertTouchpoint(ctx, tp); err != nil {
		return err
	}

	if _, xerr := svc.QueueTouchpoint(ctx, org, user, tp.ID); xerr != nil {
		t.Fatalf("run %d: approval must remain schedulable while transport is paused: %v", run, xerr)
	}
	stored, _ := repo.GetTouchpoint(ctx, org, tp.ID)
	if stored.State != models.TouchpointQueued {
		t.Fatalf("run %d: approval must enter durable queue, state=%s", run, stored.State)
	}

	if _, xerr := svc.EnrollDraft(ctx, org, user, d.ID); xerr == nil {
		t.Fatalf("run %d: enroll must refuse while paused", run)
	} else if !containsFold(xerr.Message, "paused") && !containsFold(xerr.Message, "kill") {
		t.Fatalf("run %d: enroll reason %q", run, xerr.Message)
	}
	if len(contacts.added) != 0 {
		t.Fatalf("run %d: enroll created send job: %+v", run, contacts.added)
	}

	gate := svc.GateCampaignEmail(ctx, org, DefaultCampaignName, cand.Email, uuid.New(), uuid.New(), uuid.New())
	if gate.Kind != GateDeferred || gate.Reason != ReasonSendingOff {
		t.Fatalf("run %d: final gate %+v", run, gate)
	}
	_ = run
	return nil
}
