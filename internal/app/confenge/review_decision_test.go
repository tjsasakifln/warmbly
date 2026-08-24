package confenge

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/models"
)

func TestReviewDecisionApprovesExactHashIntoNextWindowQueue(t *testing.T) {
	repo := newMemRepo()
	svc := NewService(Config{
		Enabled: true, DefaultDailyLimit: 100, MaxInitialEmailWords: 120,
		RequireHumanApproval: true,
	}, repo, nil).(*service)
	ctx := context.Background()
	orgID, actorID := uuid.New(), uuid.New()
	feed := Feed{
		SchemaVersion: models.OutreachSchemaV1,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Source: FeedSource{
			System: "test", RunID: "review-decision", SnapshotHash: "review-snapshot",
			ProfileID: "test", ProfileVersion: "1",
		},
		Leads: []FeedLead{sampleLeadWithActivation(80, ActivationActionableNow)},
	}
	raw, _ := json.Marshal(feed)
	if _, xerr := svc.ImportFromBytes(ctx, orgID, &actorID, raw, ImportOptions{IdempotencyKey: "review-import"}); xerr != nil {
		t.Fatal(xerr)
	}
	acc, err := repo.GetAccountByCNPJ(ctx, orgID, "11222333000181")
	if err != nil || acc == nil {
		t.Fatal("imported account missing")
	}
	touchpoints, xerr := svc.PlanAccountCadence(ctx, orgID, actorID, acc.ID, nil, models.OutreachChannelEmail)
	if xerr != nil || len(touchpoints) == 0 {
		t.Fatalf("plan: %v", xerr)
	}
	tp, xerr := svc.GenerateTouchpointDraft(ctx, orgID, actorID, touchpoints[0].ID)
	if xerr != nil || tp.State != models.TouchpointNeedsReview {
		t.Fatalf("generate: state=%v err=%v", tp, xerr)
	}

	cfg := dispatch.DefaultConfig()
	cfg.Timezone, cfg.WindowStart, cfg.WindowEnd = "UTC", "09:00", "17:00"
	svc.WireDispatchGovernor(dispatch.NewGovernor(cfg, dispatch.NewMemoryStore(), nil))
	expectedHash := tp.ContentHash
	result, xerr := svc.DecideReviewTouchpoint(ctx, orgID, actorID, tp.ID, ReviewDecisionInput{
		Action: ReviewDecisionApprove, ExpectedContentHash: expectedHash,
	})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if result.Touchpoint.State != models.TouchpointQueued || result.ScheduledFor == nil {
		t.Fatalf("approval was not durably scheduled: %+v", result)
	}
	if result.Touchpoint.ApprovedContentHash != expectedHash {
		t.Fatalf("scheduled approval did not bind the exact reviewed hash: approved=%q expected=%q", result.Touchpoint.ApprovedContentHash, expectedHash)
	}

	// Exact replay is idempotent and does not require a second approval.
	replayed, xerr := svc.DecideReviewTouchpoint(ctx, orgID, actorID, tp.ID, ReviewDecisionInput{
		Action: ReviewDecisionApprove, ExpectedContentHash: expectedHash,
	})
	if xerr != nil || replayed.Touchpoint.State != models.TouchpointQueued {
		t.Fatalf("replay: result=%+v err=%v", replayed, xerr)
	}
}

func TestReviewDecisionRejectsCopyWithoutDiscardingLead(t *testing.T) {
	repo := newMemRepo()
	orgID, actorID, accountID, candidateID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	svc := NewService(Config{Enabled: true, RequireHumanApproval: true}, repo, nil).(*service)
	tp := &models.OutreachTouchpoint{
		OrganizationID: orgID, AccountID: accountID, ContactCandidateID: &candidateID,
		State: models.TouchpointNeedsReview, Channel: models.OutreachChannelEmail,
		Subject: "Assunto em revisão", BodyText: "Corpo em revisão", Recipient: "contato@example.test",
	}
	ApplyContentMutation(tp, tp.Channel, tp.Recipient, tp.Subject, tp.BodyText)
	if err := repo.InsertTouchpoint(context.Background(), tp); err != nil {
		t.Fatal(err)
	}
	result, xerr := svc.DecideReviewTouchpoint(context.Background(), orgID, actorID, tp.ID, ReviewDecisionInput{
		Action: ReviewDecisionReject, ExpectedContentHash: tp.ContentHash, Reason: "tom inadequado",
	})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if result.Touchpoint.State != models.TouchpointRejectedRewritePending {
		t.Fatalf("rejection discarded the lead: state=%s", result.Touchpoint.State)
	}
}

func TestMissingPlaybookKeepsTouchpointRecoverable(t *testing.T) {
	repo := newMemRepo()
	svc := NewService(Config{Enabled: true, RequireHumanApproval: true}, repo, nil).(*service)
	ctx := context.Background()
	orgID, actorID := uuid.New(), uuid.New()
	lead := sampleLeadWithActivation(80, ActivationActionableNow)
	lead.Offer.ServiceCode = "servico_ainda_nao_mapeado"
	lead.Offer.ServiceName = "Serviço ainda não mapeado"
	feed := Feed{
		SchemaVersion: models.OutreachSchemaV1,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Source: FeedSource{
			System: "test", RunID: "recoverable-missing-playbook", SnapshotHash: "recoverable-snapshot",
			ProfileID: "test", ProfileVersion: "1",
		},
		Leads: []FeedLead{lead},
	}
	raw, _ := json.Marshal(feed)
	if _, xerr := svc.ImportFromBytes(ctx, orgID, &actorID, raw, ImportOptions{IdempotencyKey: "recoverable-import"}); xerr != nil {
		t.Fatal(xerr)
	}
	acc, _ := repo.GetAccountByCNPJ(ctx, orgID, lead.Company.CNPJ14)
	touchpoints, xerr := svc.PlanAccountCadence(ctx, orgID, actorID, acc.ID, nil, models.OutreachChannelEmail)
	if xerr != nil || len(touchpoints) == 0 {
		t.Fatalf("plan: %v", xerr)
	}
	tp, xerr := svc.GenerateTouchpointDraft(ctx, orgID, actorID, touchpoints[0].ID)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if tp.State != models.TouchpointEnrichmentPending {
		t.Fatalf("missing playbook discarded lead: state=%s", tp.State)
	}
	if models.TouchpointTerminalStates[tp.State] {
		t.Fatalf("recoverable deficit became terminal: state=%s", tp.State)
	}
}
