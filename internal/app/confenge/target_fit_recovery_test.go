package confenge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

func targetFitRecoveryFixture(t *testing.T) (*service, *memRepo, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
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
			System: "test", RunID: "target-fit-recovery", SnapshotHash: "target-fit-recovery-snapshot",
			ProfileID: "test", ProfileVersion: "1",
		},
		Leads: []FeedLead{sampleLeadWithActivation(80, ActivationActionableNow)},
	}
	raw, _ := json.Marshal(feed)
	if _, xerr := svc.ImportFromBytes(ctx, orgID, &actorID, raw, ImportOptions{IdempotencyKey: "target-fit-recovery"}); xerr != nil {
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
	return svc, repo, orgID, actorID, touchpoints[0].ID
}

func TestStaleTargetFitKeepsDraftAndTouchpointRecoverable(t *testing.T) {
	svc, repo, orgID, actorID, touchpointID := targetFitRecoveryFixture(t)
	tp, _ := repo.GetTouchpoint(context.Background(), orgID, touchpointID)
	acc := repo.byID[tp.AccountID]
	acc.TargetFitFresh = false
	acc.TargetFitEligible = false
	acc.TargetFitSuppressionReason = TargetFitReasonStale
	acc.EmailSendReady = false
	acc.QueueState = models.OutreachQueueTargetFitSuppressed

	generated, xerr := svc.GenerateTouchpointDraft(context.Background(), orgID, actorID, touchpointID)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if generated.State != models.TouchpointEnrichmentPending {
		t.Fatalf("stale target fit discarded the touchpoint: state=%s", generated.State)
	}
	if strings.TrimSpace(generated.BodyText) == "" || generated.DraftID == nil {
		t.Fatal("recoverable target fit must retain useful draft copy")
	}
	if generated.ApprovedBy != nil || generated.ApprovedContentHash != "" {
		t.Fatal("recoverable target fit must not retain approval")
	}
	draft, _ := repo.GetDraft(context.Background(), orgID, *generated.DraftID)
	if draft == nil || draft.Status != models.OutreachDraftEnrichmentPending || !strings.Contains(string(draft.ValidationJSON), TargetFitReasonStale) {
		t.Fatalf("draft did not record stale-fit recovery: %+v", draft)
	}
	if _, approveErr := svc.ApproveTouchpoint(context.Background(), orgID, actorID, generated.ID, ApprovalOptions{}); approveErr == nil {
		t.Fatal("stale target fit must remain fail-closed for approval")
	}
}

func TestCurrentTargetFitOutRemainsHardGenerationGate(t *testing.T) {
	svc, repo, orgID, actorID, touchpointID := targetFitRecoveryFixture(t)
	tp, _ := repo.GetTouchpoint(context.Background(), orgID, touchpointID)
	acc := repo.byID[tp.AccountID]
	acc.TargetFitClass = TargetFitOutOfScope
	acc.TargetFitFresh = true
	acc.TargetFitEligible = false
	acc.TargetFitSuppressionReason = TargetFitReasonOut

	if _, xerr := svc.GenerateTouchpointDraft(context.Background(), orgID, actorID, touchpointID); xerr == nil || !strings.Contains(xerr.Message, TargetFitReasonOut) {
		t.Fatalf("current target-fit OUT must stay blocked, got %v", xerr)
	}
}

func TestEnrichmentPendingReturnsToReviewWhenTargetFitRecovers(t *testing.T) {
	svc, repo, orgID, actorID, touchpointID := targetFitRecoveryFixture(t)
	tp, _ := repo.GetTouchpoint(context.Background(), orgID, touchpointID)
	acc := repo.byID[tp.AccountID]
	acc.TargetFitFresh = false
	acc.TargetFitEligible = false
	acc.TargetFitSuppressionReason = TargetFitReasonStale
	acc.EmailSendReady = false
	acc.QueueState = models.OutreachQueueTargetFitSuppressed

	pending, xerr := svc.GenerateTouchpointDraft(context.Background(), orgID, actorID, touchpointID)
	if xerr != nil || pending.State != models.TouchpointEnrichmentPending {
		t.Fatalf("create recoverable draft: state=%v err=%v", pending, xerr)
	}
	acc.TargetFitFresh = true
	acc.TargetFitEligible = true
	acc.TargetFitSuppressionReason = ""
	acc.EmailSendReady = true
	acc.QueueState = models.OutreachQueueReadyToGenerate

	recovered, xerr := svc.GenerateTouchpointDraft(context.Background(), orgID, actorID, touchpointID)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if recovered.State != models.TouchpointNeedsReview {
		t.Fatalf("recovered touchpoint must reach human review, got %s", recovered.State)
	}
	if recovered.StopReason != "" {
		t.Fatalf("obsolete enrichment blocker was not cleared: %q", recovered.StopReason)
	}
	if recovered.ApprovedBy != nil || recovered.ApprovedContentHash != "" {
		t.Fatal("recovery must not approve the touchpoint")
	}
}
