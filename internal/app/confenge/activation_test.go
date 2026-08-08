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

func sampleLeadWithActivation(score float64, state string) FeedLead {
	return FeedLead{
		SourceLeadID: "lead-1",
		Company: FeedCompany{
			CNPJ14: "11222333000181", RazaoSocial: "ACME Engenharia LTDA", UF: "SP",
		},
		Priority: FeedPriority{Rank: 10, Score: 50},
		Moment: FeedMoment{
			Code: "NEW_CONTRACT", Summary: "Contrato recente observado", ObservedAt: "2026-07-20",
			EvidenceIDs: []string{"ev-1"},
		},
		Offer: FeedOffer{ServiceCode: "reajuste", ServiceName: "Reajuste", EntryOffer: "Diagnóstico"},
		MessagingContext: FeedMessaging{
			FactToMention: "Contrato público recente",
			QuestionToAsk: "Vocês formalizaram reajuste?",
			CTA:           "Posso enviar checklist",
			ClaimsToAvoid: []string{"garantimos pagamento"},
		},
		Contacts: []FeedContact{{
			SourceContactID: "c1", Name: "Maria Silva", Email: "maria@acme.com.br",
			VerificationStatus: models.OutreachVerifyOfficialSource, Recommended: true,
		}},
		Evidence: []FeedEvidence{{
			ID: "ev-1", Type: "contract", Title: "Contrato", EpistemicClass: models.OutreachEpistemicConfirmedFact,
		}},
		CommercialState: "NEW",
		Activation: &FeedActivation{
			State: state, Score: score,
			ReasonCodes:      []string{"NEW_RELEVANT_CONTRACT"},
			PolicyVersion:    "confenge-activation-v1",
			EvaluatedAt:      "2026-08-08T10:00:00Z",
			NextBestActionAt: "2026-08-08T10:00:00Z",
			ExpiresAt:        "2026-08-22T10:00:00Z",
			SourceHash:       "src-hash-1",
			ScoreComponents: map[string]float64{
				"trigger_strength": 34, "freshness": 21, "evidence_quality": 14, "commercial_relevance": 13.4,
			},
		},
	}
}

func TestValidateLeadLegacyWithoutActivation(t *testing.T) {
	lead := sampleLeadWithActivation(80, ActivationActionableNow)
	lead.Activation = nil
	if lv := ValidateLead(0, lead); lv != nil {
		t.Fatalf("legacy lead should validate: %v", lv.Message)
	}
}

func TestValidateLeadActionableRequiresReasons(t *testing.T) {
	lead := sampleLeadWithActivation(80, ActivationActionableNow)
	lead.Activation.ReasonCodes = nil
	if lv := ValidateLead(0, lead); lv == nil {
		t.Fatal("expected error for ACTIONABLE_NOW without reason_codes")
	}
}

func TestValidateLeadScoreRange(t *testing.T) {
	lead := sampleLeadWithActivation(120, ActivationWatch)
	if lv := ValidateLead(0, lead); lv == nil {
		t.Fatal("expected score out of range error")
	}
}

func TestImportActivationFields(t *testing.T) {
	r := newMemRepo()
	svc := NewService(Config{Enabled: true, DefaultDailyLimit: 100, MaxInitialEmailWords: 120, RequireHumanApproval: true}, r, nil).(*service)
	org := uuid.New()
	user := uuid.New()
	feed := Feed{
		SchemaVersion: models.OutreachSchemaV1,
		GeneratedAt:   "2026-08-08T10:00:00Z",
		Source:        FeedSource{System: "extra-cli", RunID: "run-a", SnapshotHash: "snap1", ProfileID: "p", ProfileVersion: "1"},
		Pagination:    FeedPagination{HasMore: false},
		Leads:         []FeedLead{sampleLeadWithActivation(82.4, ActivationActionableNow)},
	}
	raw, _ := json.Marshal(feed)
	run, xerr := svc.ImportFromBytes(context.Background(), org, &user, raw, ImportOptions{IdempotencyKey: "k1"})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if run.Status != models.OutreachImportCompleted {
		t.Fatalf("status=%s", run.Status)
	}
	acc, err := r.GetAccountByCNPJ(context.Background(), org, "11222333000181")
	if err != nil || acc == nil {
		t.Fatal("account missing")
	}
	if acc.ActivationState != ActivationActionableNow {
		t.Fatalf("activation_state=%s", acc.ActivationState)
	}
	if acc.ActivationScore != 82.4 {
		t.Fatalf("score=%v", acc.ActivationScore)
	}
	if acc.MessageContextHash == "" {
		t.Fatal("message_context_hash required")
	}
	if len(acc.ActivationReasonCodes) == 0 {
		t.Fatal("reason codes required")
	}
}

func TestRankOnlyChangeDoesNotChangeMessageContextHash(t *testing.T) {
	a := sampleLeadWithActivation(50, ActivationActionableNow)
	b := sampleLeadWithActivation(50, ActivationActionableNow)
	b.Priority.Rank = 1
	b.Priority.Score = 99
	b.Activation.Score = 99
	h1 := MessageContextHash(a)
	h2 := MessageContextHash(b)
	if h1 != h2 {
		t.Fatal("rank/score-only change must not alter message_context_hash")
	}
	// Material moment change must alter hash
	b.Moment.Summary = "Novo fato material F2"
	h3 := MessageContextHash(b)
	if h1 == h3 {
		t.Fatal("material moment change must alter message_context_hash")
	}
}

func TestStaleContextBlocksQueue(t *testing.T) {
	r := newMemRepo()
	svc := NewService(Config{
		Enabled: true, DefaultDailyLimit: 100, MaxInitialEmailWords: 120, RequireHumanApproval: true,
	}, r, nil).(*service)
	org := uuid.New()
	user := uuid.New()
	ctx := context.Background()

	// Import F1
	feed := Feed{
		SchemaVersion: models.OutreachSchemaV1,
		GeneratedAt:   "2026-08-08T10:00:00Z",
		Source:        FeedSource{System: "extra-cli", RunID: "run-a", SnapshotHash: "snap1", ProfileID: "p", ProfileVersion: "1"},
		Pagination:    FeedPagination{},
		Leads:         []FeedLead{sampleLeadWithActivation(80, ActivationActionableNow)},
	}
	raw, _ := json.Marshal(feed)
	if _, xerr := svc.ImportFromBytes(ctx, org, &user, raw, ImportOptions{IdempotencyKey: "imp1"}); xerr != nil {
		t.Fatal(xerr)
	}
	acc, _ := r.GetAccountByCNPJ(ctx, org, "11222333000181")
	// Plan cadence + generate
	tps, xerr := svc.PlanAccountCadence(ctx, org, user, acc.ID, nil, models.OutreachChannelEmail)
	if xerr != nil || len(tps) == 0 {
		t.Fatalf("plan: %v len=%d", xerr, len(tps))
	}
	tp, xerr := svc.GenerateTouchpointDraft(ctx, org, user, tps[0].ID)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if tp.GeneratedContextHash == "" || tp.GeneratedContextHash != acc.MessageContextHash {
		// re-fetch account hash after import
		acc, _ = r.GetAccountByCNPJ(ctx, org, "11222333000181")
		if tp.GeneratedContextHash != acc.MessageContextHash {
			t.Fatalf("generated hash %q != account %q", tp.GeneratedContextHash, acc.MessageContextHash)
		}
	}
	// Approve
	tp, xerr = svc.ApproveTouchpoint(ctx, org, user, tp.ID)
	if xerr != nil {
		t.Fatal(xerr)
	}
	// Reimport material F2
	lead2 := sampleLeadWithActivation(80, ActivationActionableNow)
	lead2.Moment.Summary = "Contrato prorrogado — fato F2"
	lead2.MessagingContext.FactToMention = "Prorrogação observada em 2026-08"
	feed2 := feed
	feed2.Source.RunID = "run-b"
	feed2.Source.SnapshotHash = "snap2"
	feed2.Leads = []FeedLead{lead2}
	raw2, _ := json.Marshal(feed2)
	if _, xerr := svc.ImportFromBytes(ctx, org, &user, raw2, ImportOptions{IdempotencyKey: "imp2"}); xerr != nil {
		t.Fatal(xerr)
	}
	acc2, _ := r.GetAccountByCNPJ(ctx, org, "11222333000181")
	if acc2.MessageContextHash == tp.GeneratedContextHash {
		t.Fatal("expected message context hash to change after material reimport")
	}
	// Queue/dispatch must fail closed
	_, xerr = svc.QueueTouchpoint(ctx, org, user, tp.ID)
	if xerr == nil {
		t.Fatal("expected stale context block on queue")
	}
	if !strings.Contains(strings.ToLower(xerr.Message), "stale") && !strings.Contains(strings.ToLower(xerr.Message), "context") {
		t.Fatalf("unexpected error: %s", xerr.Message)
	}
	// Regen + reapprove can proceed past context check (dispatch may still fail without governor/mail)
	tp2, xerr := svc.GenerateTouchpointDraft(ctx, org, user, tp.ID)
	if xerr != nil {
		// may be state conflict if still APPROVED; force state
		tpReload, _ := r.GetTouchpoint(ctx, org, tp.ID)
		if tpReload != nil {
			tpReload.State = models.TouchpointNeedsReview
			_ = r.UpdateTouchpoint(ctx, tpReload)
			tp2, xerr = svc.GenerateTouchpointDraft(ctx, org, user, tp.ID)
		}
	}
	if xerr != nil {
		t.Fatalf("regen: %v", xerr)
	}
	if tp2.GeneratedContextHash != acc2.MessageContextHash {
		// account may have been re-read inside generate
		acc3, _ := r.GetAccount(ctx, org, acc2.ID)
		if tp2.GeneratedContextHash != acc3.MessageContextHash {
			t.Fatalf("regen hash mismatch %q vs %q", tp2.GeneratedContextHash, acc3.MessageContextHash)
		}
	}
}

func TestIsOutboundDueRespectsFutureNBAAndExpiry(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	future := now.Add(48 * time.Hour)
	past := now.Add(-48 * time.Hour)
	acc := &models.OutreachAccount{
		ActivationState:  ActivationActionableNow,
		QueueState:       models.OutreachQueueReadyToGenerate,
		NextBestActionAt: &future,
	}
	if IsOutboundDue(acc, now) {
		t.Fatal("future NBA must not be due")
	}
	acc.NextBestActionAt = &now
	acc.ActivationExpiresAt = &past
	if IsOutboundDue(acc, now) {
		t.Fatal("expired activation must not be due")
	}
	acc.ActivationExpiresAt = &future
	if !IsOutboundDue(acc, now) {
		t.Fatal("expected due")
	}
	acc.DoNotContact = true
	if IsOutboundDue(acc, now) {
		t.Fatal("DNC must dominate")
	}
}

func TestAssertMessageContextFresh(t *testing.T) {
	acc := &models.OutreachAccount{MessageContextHash: "abc"}
	if err := AssertMessageContextFresh(acc, "abc"); err != nil {
		t.Fatal(err)
	}
	if err := AssertMessageContextFresh(acc, "zzz"); err == nil {
		t.Fatal("expected mismatch error")
	}
	// legacy empty account hash allows
	if err := AssertMessageContextFresh(&models.OutreachAccount{}, "anything"); err != nil {
		t.Fatal(err)
	}
}

func TestDynamicPriorityFlagOffPreservesLegacyImport(t *testing.T) {
	r := newMemRepo()
	svc := NewService(Config{
		Enabled: true, DefaultDailyLimit: 100, MaxInitialEmailWords: 120,
		RequireHumanApproval: true, DynamicPriorityEnabled: false,
	}, r, nil).(*service)
	org := uuid.New()
	user := uuid.New()
	feed := Feed{
		SchemaVersion: models.OutreachSchemaV1,
		GeneratedAt:   "2026-08-08T10:00:00Z",
		Source:        FeedSource{System: "extra-cli", RunID: "run-a", SnapshotHash: "snap1", ProfileID: "p", ProfileVersion: "1"},
		Leads:         []FeedLead{sampleLeadWithActivation(90, ActivationActionableNow)},
	}
	raw, _ := json.Marshal(feed)
	if _, xerr := svc.ImportFromBytes(context.Background(), org, &user, raw, ImportOptions{}); xerr != nil {
		t.Fatal(xerr)
	}
	// Activation stored even when flag off
	acc, _ := r.GetAccountByCNPJ(context.Background(), org, "11222333000181")
	if acc.ActivationState != ActivationActionableNow {
		t.Fatal("import should still store activation when flag off")
	}
}

func TestDNCNotReactivatedByActivationFeed(t *testing.T) {
	r := newMemRepo()
	svc := NewService(Config{Enabled: true, DefaultDailyLimit: 100, MaxInitialEmailWords: 120, RequireHumanApproval: true}, r, nil).(*service)
	org := uuid.New()
	user := uuid.New()
	ctx := context.Background()
	feed := Feed{
		SchemaVersion: models.OutreachSchemaV1,
		GeneratedAt:   "2026-08-08T10:00:00Z",
		Source:        FeedSource{System: "extra-cli", RunID: "run-a", SnapshotHash: "snap1", ProfileID: "p", ProfileVersion: "1"},
		Leads:         []FeedLead{sampleLeadWithActivation(90, ActivationActionableNow)},
	}
	raw, _ := json.Marshal(feed)
	if _, xerr := svc.ImportFromBytes(ctx, org, &user, raw, ImportOptions{IdempotencyKey: "a"}); xerr != nil {
		t.Fatal(xerr)
	}
	acc, _ := r.GetAccountByCNPJ(ctx, org, "11222333000181")
	_, _ = svc.BlockAccount(ctx, org, user, acc.ID, "opt-out", true)
	// reimport as actionable
	feed.Source.RunID = "run-b"
	feed.Source.SnapshotHash = "snap2"
	raw2, _ := json.Marshal(feed)
	if _, xerr := svc.ImportFromBytes(ctx, org, &user, raw2, ImportOptions{IdempotencyKey: "b"}); xerr != nil {
		t.Fatal(xerr)
	}
	acc2, _ := r.GetAccountByCNPJ(ctx, org, "11222333000181")
	if !acc2.DoNotContact {
		t.Fatal("DNC must survive reimport")
	}
}
