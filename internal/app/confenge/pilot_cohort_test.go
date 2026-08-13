package confenge

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

type pilotFixture struct {
	service *service
	repo    *memRepo
	orgID   uuid.UUID
	userID  uuid.UUID
	now     time.Time
}

func newPilotFixture(t *testing.T) *pilotFixture {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	repo := newMemRepo()
	repo.feedSync = map[uuid.UUID]*models.OutreachFeedSyncState{}
	orgID := uuid.New()
	repo.feedSync[orgID] = &models.OutreachFeedSyncState{
		OrganizationID: orgID, LastSnapshotHash: "snapshot-real-20260812", LastSuccessAt: &now, LastStatus: "completed",
	}
	service := NewService(Config{
		Enabled: true, RequireHumanApproval: true, AutoSendEnabled: false,
		MaxInitialEmailWords: 120, DefaultDailyLimit: 200, FeedMaxAge: 24 * time.Hour,
	}, repo, nil).(*service)
	return &pilotFixture{service: service, repo: repo, orgID: orgID, userID: uuid.New(), now: now}
}

func (fixture *pilotFixture) addReadyAccount(t *testing.T, index int) uuid.UUID {
	t.Helper()
	cnpj := fmt.Sprintf("%014d", index+10000000000000)
	observedAt := fixture.now.Add(-2 * time.Hour)
	expiresAt := fixture.now.Add(7 * 24 * time.Hour)
	account := &models.OutreachAccount{
		OrganizationID: fixture.orgID, CNPJ14: cnpj, RazaoSocial: fmt.Sprintf("Construtora %02d LTDA", index),
		QueueState: models.OutreachQueueReadyToGenerate, CommercialState: "NEW",
		MomentCode: "NEW_RELEVANT_CONTRACT", MomentSummary: "Contrato público recente observado em fonte oficial.",
		MomentObservedAt: &observedAt, MomentEvidenceIDs: []string{"evidence-" + cnpj},
		ServiceCode: "gestao_monitoramento_contratual", EntryOffer: "PUBLIC_DATA_SNAPSHOT",
		FactToMention:      "Contrato público de engenharia publicado recentemente em fonte oficial.",
		QuestionToAsk:      "Posso compartilhar três pontos documentais para conferência?",
		CTA:                "Posso compartilhar três pontos documentais para conferência?",
		MessageContextHash: "context-" + cnpj,
		ActivationState:    ActivationActionableNow, ActivationReasonCodes: []string{"NEW_RELEVANT_CONTRACT"},
		ActivationExpiresAt: &expiresAt,
		TargetFitClass:      TargetFitConfirmed, TargetFitVersion: "confenge-target-fit-v1",
		TargetFitSourceWatermark: observedAt.Format(time.RFC3339), TargetFitObservedAt: &observedAt,
		TargetFitComputedAt: &observedAt, TargetFitFresh: true, TargetFitEligible: true,
		TargetFitSendTier: "A_AUTOMATIC", EmailSendReady: true,
	}
	if _, err := fixture.repo.UpsertAccount(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	// This legacy row reproduces the production bug: it is recommended and enrollable,
	// but it predates the authoritative email_send_ready decision.
	legacyDate := fixture.now.Add(-48 * time.Hour)
	legacy := models.OutreachContactCandidate{
		ID: uuid.New(), OrganizationID: fixture.orgID, AccountID: account.ID,
		SourceContactID: "legacy-" + cnpj, Email: fmt.Sprintf("contato%d@empresa%d.com.br", index, index),
		SourceURL: fmt.Sprintf("https://empresa%d.com.br/contato", index), SourceDate: &legacyDate,
		VerificationStatus: models.OutreachVerifyOfficialSource, Recommended: true,
		EmailSendReady: false, OwnershipStatus: "COMPANY_OWNED", CreatedAt: fixture.now.Add(-72 * time.Hour),
	}
	validDate := fixture.now.Add(-2 * time.Hour)
	valid := legacy
	valid.ID = uuid.New()
	valid.SourceContactID = "authoritative-" + cnpj
	valid.SourceDate = &validDate
	valid.EmailSendReady = true
	valid.RecipientCommercialSuitability = "SUITABLE"
	valid.CreatedAt = fixture.now.Add(-time.Hour)
	fixture.repo.cands[account.ID] = []models.OutreachContactCandidate{legacy, valid}
	fixture.repo.evidence[account.ID] = []models.OutreachEvidence{{
		ID: uuid.New(), OrganizationID: fixture.orgID, AccountID: account.ID,
		SourceEvidenceID: "evidence-" + cnpj, Title: "Contrato público confirmado",
		Synthesis: account.FactToMention, EpistemicClass: models.OutreachEpistemicConfirmedFact,
		Reliability: "HIGH", EvidenceDate: &observedAt,
	}}
	return account.ID
}

func TestPilotCohortPreparesValidAccountAndUsesSendReadyRecipient(t *testing.T) {
	fixture := newPilotFixture(t)
	accountID := fixture.addReadyAccount(t, 1)

	result, xerr := fixture.service.PreparePilotCohort(context.Background(), fixture.orgID, fixture.userID, []uuid.UUID{accountID}, PilotOperation{IdempotencyKey: "one", RequestID: "req-one"})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if result.Prepared != 1 || result.Blocked != 0 || result.Results[0].Status != PilotPrepared {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Results[0].ContactID == nil || *result.Results[0].ContactID != fixture.repo.cands[accountID][1].ID {
		t.Fatalf("wrong recipient selected: %+v", result.Results[0])
	}
	assertPilotAccountNeedsReview(t, fixture, accountID)
}

func TestPilotCohortMixedBatchDoesNotRollbackValidAccounts(t *testing.T) {
	fixture := newPilotFixture(t)
	validID := fixture.addReadyAccount(t, 1)
	missingID := fixture.addReadyAccount(t, 2)
	fixture.repo.cands[missingID] = nil
	ineligibleID := fixture.addReadyAccount(t, 3)
	fixture.repo.byID[ineligibleID].TargetFitEligible = false
	fixture.repo.byID[ineligibleID].TargetFitClass = TargetFitOutOfScope
	dncID := fixture.addReadyAccount(t, 4)
	fixture.repo.byID[dncID].DoNotContact = true

	result, xerr := fixture.service.PreparePilotCohort(context.Background(), fixture.orgID, fixture.userID, []uuid.UUID{validID, missingID, ineligibleID, dncID}, PilotOperation{IdempotencyKey: "mixed"})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if result.Prepared != 1 || result.Blocked != 3 || result.ContactNeeded != 1 {
		t.Fatalf("unexpected counts: %+v", result)
	}
	want := map[uuid.UUID]string{missingID: "recipient_missing", ineligibleID: "account_ineligible", dncID: "account_do_not_contact"}
	for _, item := range result.Results {
		if code, ok := want[item.AccountID]; ok && item.ReasonCode != code {
			t.Fatalf("account %s reason=%s want=%s", item.AccountID, item.ReasonCode, code)
		}
	}
	assertPilotAccountNeedsReview(t, fixture, validID)
}

func TestPilotRecipientBlockReasons(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*models.OutreachContactCandidate)
		want   string
	}{
		{name: "suppressed", mutate: func(candidate *models.OutreachContactCandidate) { candidate.Blocked = true }, want: "recipient_suppressed"},
		{name: "dnc", mutate: func(candidate *models.OutreachContactCandidate) { candidate.DoNotContact = true }, want: "recipient_opt_out"},
		{name: "hard bounce", mutate: func(candidate *models.OutreachContactCandidate) { candidate.Bounced = true }, want: "recipient_hard_bounce"},
		{name: "tainted provenance", mutate: func(candidate *models.OutreachContactCandidate) {
			candidate.Blocked = true
			candidate.BlockReason = "provenance_taint:fixture"
		}, want: "provenance_tainted"},
		{name: "fixture", mutate: func(candidate *models.OutreachContactCandidate) { candidate.Email = "fixture@example.com" }, want: "recipient_demo_or_fixture"},
		{name: "invalid address", mutate: func(candidate *models.OutreachContactCandidate) { candidate.Email = "invalid" }, want: "recipient_invalid"},
		{name: "generic policy", mutate: func(candidate *models.OutreachContactCandidate) { candidate.MailboxPurposeSendBlocked = true }, want: "generic_mailbox_not_allowed"},
		{name: "missing date", mutate: func(candidate *models.OutreachContactCandidate) { candidate.SourceDate = nil }, want: "recipient_evidence_date_missing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPilotFixture(t)
			accountID := fixture.addReadyAccount(t, 10)
			candidate := fixture.repo.cands[accountID][1]
			test.mutate(&candidate)
			fixture.repo.cands[accountID] = []models.OutreachContactCandidate{candidate}
			result, xerr := fixture.service.PreparePilotCohort(context.Background(), fixture.orgID, fixture.userID, []uuid.UUID{accountID}, PilotOperation{})
			if xerr != nil {
				t.Fatal(xerr)
			}
			if result.Results[0].ReasonCode != test.want {
				t.Fatalf("reason=%s want=%s result=%+v", result.Results[0].ReasonCode, test.want, result.Results[0])
			}
		})
	}
}

func TestPilotCohortFeedMissingAndStale(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*pilotFixture)
		want      string
	}{
		{name: "missing", configure: func(fixture *pilotFixture) { fixture.repo.feedSync = nil }, want: "feed_missing"},
		{name: "stale", configure: func(fixture *pilotFixture) {
			stale := fixture.now.Add(-25 * time.Hour)
			fixture.repo.feedSync[fixture.orgID].LastSuccessAt = &stale
		}, want: "feed_stale"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPilotFixture(t)
			accountID := fixture.addReadyAccount(t, 20)
			test.configure(fixture)
			result, xerr := fixture.service.PreparePilotCohort(context.Background(), fixture.orgID, fixture.userID, []uuid.UUID{accountID}, PilotOperation{})
			if xerr != nil {
				t.Fatal(xerr)
			}
			if result.Results[0].ReasonCode != test.want || result.Prepared != 0 {
				t.Fatalf("unexpected result: %+v", result)
			}
		})
	}
}

func TestPilotReadinessUsesOneFreshnessThreshold(t *testing.T) {
	now := time.Date(2026, 8, 12, 18, 0, 0, 0, time.UTC)
	freshAt := now.Add(-23 * time.Hour)
	staleAt := now.Add(-25 * time.Hour)
	cfg := Config{Enabled: true, FeedURL: "https://feed.internal/manifest.json", FeedMaxAge: 24 * time.Hour}

	fresh := BuildReadiness(cfg, ReadinessInputs{Now: now, LastImportAt: &freshAt, FeedSnapshot: "fresh-snapshot"})
	if fresh.FeedState != "fresh" || fresh.FeedLastSyncAt == nil || fresh.FeedSnapshot != "fresh-snapshot" || fresh.FeedMaxAgeSeconds != 86400 {
		t.Fatalf("unexpected fresh readiness: %+v", fresh)
	}
	stale := BuildReadiness(cfg, ReadinessInputs{Now: now, LastImportAt: &staleAt, FeedSnapshot: "stale-snapshot"})
	if stale.FeedState != "stale" {
		t.Fatalf("unexpected stale readiness: %+v", stale)
	}
	missing := BuildReadiness(cfg, ReadinessInputs{Now: now})
	if missing.FeedState != "missing" || missing.FeedAgeSeconds != nil {
		t.Fatalf("unexpected missing readiness: %+v", missing)
	}
}

func TestPilotCohortThirtyAccountsGenerationAndIdempotentRetry(t *testing.T) {
	fixture := newPilotFixture(t)
	accountIDs := make([]uuid.UUID, 0, PilotCohortTarget)
	for index := 0; index < PilotCohortTarget; index++ {
		accountIDs = append(accountIDs, fixture.addReadyAccount(t, index+100))
	}
	first, xerr := fixture.service.PreparePilotCohort(context.Background(), fixture.orgID, fixture.userID, accountIDs, PilotOperation{IdempotencyKey: "thirty", RequestID: "req-thirty"})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if first.Prepared != 30 || first.Blocked != 0 || first.CohortPrepared != 30 || first.Remaining != 0 {
		t.Fatalf("unexpected first result: %+v", first)
	}
	if len(fixture.repo.drafts) != 30 || len(fixture.repo.touchpoints) != 30*len(CadencePolicyV1()) {
		t.Fatalf("drafts=%d touchpoints=%d", len(fixture.repo.drafts), len(fixture.repo.touchpoints))
	}
	second, xerr := fixture.service.PreparePilotCohort(context.Background(), fixture.orgID, fixture.userID, accountIDs, PilotOperation{IdempotencyKey: "thirty", RequestID: "req-thirty-retry"})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if second.Prepared != 30 || second.Blocked != 0 {
		t.Fatalf("unexpected retry result: %+v", second)
	}
	for _, item := range second.Results {
		if !item.Idempotent {
			t.Fatalf("retry was not idempotent: %+v", item)
		}
	}
	if len(fixture.repo.drafts) != 30 || len(fixture.repo.touchpoints) != 30*len(CadencePolicyV1()) {
		t.Fatal("retry created duplicate rows")
	}
	for _, touchpoint := range fixture.repo.touchpoints {
		if touchpoint.State == models.TouchpointSent || touchpoint.State == models.TouchpointQueued {
			t.Fatalf("pilot preparation reached transport state: %+v", touchpoint)
		}
		if touchpoint.Ordinal == 1 && (touchpoint.State != models.TouchpointNeedsReview || touchpoint.ApprovedBy != nil || touchpoint.ApprovedAt != nil) {
			t.Fatalf("initial draft bypassed human review: %+v", touchpoint)
		}
	}
}

func TestPilotCohortIncompleteCopyContextBlocksWithoutDraft(t *testing.T) {
	fixture := newPilotFixture(t)
	accountID := fixture.addReadyAccount(t, 40)
	fixture.repo.byID[accountID].ServiceCode = ""
	fixture.repo.byID[accountID].FactToMention = ""
	fixture.repo.evidence[accountID] = nil
	result, xerr := fixture.service.PreparePilotCohort(context.Background(), fixture.orgID, fixture.userID, []uuid.UUID{accountID}, PilotOperation{})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if result.Results[0].ReasonCode != "incomplete_copy_context" || len(fixture.repo.drafts) != 0 {
		t.Fatalf("unexpected result: %+v drafts=%d", result.Results[0], len(fixture.repo.drafts))
	}
}

func TestPilotEditInvalidatesApproval(t *testing.T) {
	fixture := newPilotFixture(t)
	accountID := fixture.addReadyAccount(t, 50)
	result, xerr := fixture.service.PreparePilotCohort(context.Background(), fixture.orgID, fixture.userID, []uuid.UUID{accountID}, PilotOperation{})
	if xerr != nil || result.Results[0].TouchpointID == nil {
		t.Fatalf("prepare failed: %+v %v", result, xerr)
	}
	touchpointID := *result.Results[0].TouchpointID
	approved, xerr := fixture.service.ApproveTouchpoint(context.Background(), fixture.orgID, fixture.userID, touchpointID)
	if xerr != nil || approved.ApprovedBy == nil {
		t.Fatalf("approve failed: %+v %v", approved, xerr)
	}
	editedBody := approved.BodyText + "\n\nRevisão humana."
	edited, xerr := fixture.service.EditTouchpoint(context.Background(), fixture.orgID, fixture.userID, touchpointID, nil, &editedBody, nil, nil)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if edited.ApprovedBy != nil || edited.ApprovedAt != nil || edited.ApprovedContentHash != "" || edited.State != models.TouchpointNeedsReview {
		t.Fatalf("edit did not invalidate approval: %+v", edited)
	}
}

func assertPilotAccountNeedsReview(t *testing.T, fixture *pilotFixture, accountID uuid.UUID) {
	t.Helper()
	touchpoints, err := fixture.repo.ListTouchpoints(context.Background(), fixture.orgID, accountID, "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	first := firstPilotTouchpoint(touchpoints)
	if first == nil || first.State != models.TouchpointNeedsReview || first.DraftID == nil {
		t.Fatalf("first touchpoint not in needs_review: %+v", first)
	}
	draft, err := fixture.repo.GetDraft(context.Background(), fixture.orgID, *first.DraftID)
	if err != nil || draft == nil || draft.Status != models.OutreachDraftNeedsReview {
		t.Fatalf("draft not in needs_review: %+v err=%v", draft, err)
	}
	if first.ApprovedBy != nil || first.ApprovedAt != nil {
		t.Fatalf("draft was auto-approved: %+v", first)
	}
}
