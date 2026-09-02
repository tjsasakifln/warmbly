package confenge

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/app/confenge/liveintel"
	"github.com/warmbly/warmbly/internal/models"
)

func allowConfengeSendingForTest(t *testing.T) {
	t.Helper()
	t.Setenv(EnvKillSwitchPath, filepath.Join(t.TempDir(), "not-engaged"))
}

func TestGateCampaignEmailKillSwitchBlocksStaleEnrollment(t *testing.T) {
	t.Setenv(EnvKillSwitchPath, filepath.Join(t.TempDir(), "engaged"))
	if err := EngageKillSwitch(); err != nil {
		t.Fatal(err)
	}
	svc := &service{cfg: Config{Enabled: true}}
	result := svc.GateCampaignEmail(context.Background(), uuid.New(), DefaultCampaignName, "lead@example.com", uuid.New(), uuid.New(), uuid.New())
	if result.Kind != GateDeferred || result.Reason != ReasonSendingOff {
		t.Fatalf("kill switch must defer before transport: %+v", result)
	}
}

func TestPermanentSuppressOnlyHardBlock(t *testing.T) {
	cases := []struct {
		kind GateKind
		want bool
	}{
		{GateProceed, false},
		{GateDeferred, false},
		{GateAlready, false},
		{GateHardBlock, true},
		{GateTransient, false},
		{GateBypass, false},
	}
	for _, tc := range cases {
		r := CampaignGateResult{Kind: tc.kind}
		if got := r.PermanentSuppress(); got != tc.want {
			t.Fatalf("kind=%d PermanentSuppress=%v want %v", tc.kind, got, tc.want)
		}
	}
}

func TestGateCampaignEmailHardBlockDNC(t *testing.T) {
	allowConfengeSendingForTest(t)
	org := uuid.New()
	accID := uuid.New()
	candID := uuid.New()
	repo := newMemRepoWithSettings()
	acc := &models.OutreachAccount{
		ID: accID, OrganizationID: org, CNPJ14: "12345678000199",
		DoNotContact: false, Blocked: false,
	}
	_, _ = repo.UpsertAccount(context.Background(), acc)
	cand := &models.OutreachContactCandidate{
		ID: candID, OrganizationID: org, AccountID: accID,
		Email: "dnc@example.com", DoNotContact: true,
	}
	_, _ = repo.UpsertCandidate(context.Background(), cand)

	svc := &service{cfg: Config{Enabled: true}, repo: repo}
	clock := &dispatch.FixedClock{T: time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)}
	cfg := dispatch.DefaultConfig()
	cfg.WindowStart, cfg.WindowEnd, cfg.Timezone, cfg.MinGap = "00:00", "23:59", "UTC", 0
	cfg.BusinessDaysOnly = false
	svc.governor = dispatch.NewGovernor(cfg, dispatch.NewMemoryStore(), clock)

	r := svc.GateCampaignEmail(context.Background(), org, DefaultCampaignName, "dnc@example.com",
		uuid.New(), uuid.New(), uuid.New())
	if r.Kind != GateHardBlock || r.Reason != ReasonDNCOrBounce {
		t.Fatalf("got kind=%d reason=%s want HardBlock dnc_or_bounce", r.Kind, r.Reason)
	}
	if r.Err != nil {
		t.Fatalf("HardBlock must not set Err: %v", r.Err)
	}
	if !r.PermanentSuppress() {
		t.Fatal("HardBlock must PermanentSuppress")
	}
}

func TestGateCampaignEmailHardBlockAccountDNC(t *testing.T) {
	allowConfengeSendingForTest(t)
	org := uuid.New()
	accID := uuid.New()
	repo := newMemRepoWithSettings()
	acc := &models.OutreachAccount{
		ID: accID, OrganizationID: org, CNPJ14: "12345678000188",
		DoNotContact: true, Blocked: true,
	}
	_, _ = repo.UpsertAccount(context.Background(), acc)
	cand := &models.OutreachContactCandidate{
		ID: uuid.New(), OrganizationID: org, AccountID: accID,
		Email: "ok@example.com", DoNotContact: false,
	}
	_, _ = repo.UpsertCandidate(context.Background(), cand)

	svc := &service{cfg: Config{Enabled: true}, repo: repo}
	clock := &dispatch.FixedClock{T: time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)}
	cfg := dispatch.DefaultConfig()
	cfg.WindowStart, cfg.WindowEnd, cfg.Timezone, cfg.MinGap = "00:00", "23:59", "UTC", 0
	cfg.BusinessDaysOnly = false
	svc.governor = dispatch.NewGovernor(cfg, dispatch.NewMemoryStore(), clock)

	r := svc.GateCampaignEmail(context.Background(), org, DefaultCampaignName, "ok@example.com",
		uuid.New(), uuid.New(), uuid.New())
	if r.Kind != GateHardBlock || r.Reason != ReasonAccountDNC {
		t.Fatalf("got kind=%d reason=%s", r.Kind, r.Reason)
	}
	if r.PermanentSuppress() != true {
		t.Fatal("account DNC must permanent suppress")
	}
}

func TestAssertTransportableCancelsQueuedSendSuppressedMinutesBeforeDue(t *testing.T) {
	allowConfengeSendingForTest(t)
	ctx := context.Background()
	repo := newMemRepoWithSettings()
	orgID, accountID, candidateID := uuid.New(), uuid.New(), uuid.New()
	_, _ = repo.UpsertAccount(ctx, &models.OutreachAccount{ID: accountID, OrganizationID: orgID, CNPJ14: "12345678000176"})
	_, _ = repo.UpsertCandidate(ctx, &models.OutreachContactCandidate{
		ID: candidateID, OrganizationID: orgID, AccountID: accountID, Email: "exact@example.com",
	})
	otherCandidateID := uuid.New()
	_, _ = repo.UpsertCandidate(ctx, &models.OutreachContactCandidate{
		ID: otherCandidateID, OrganizationID: orgID, AccountID: accountID, Email: "other@example.com",
	})
	campaignID, contactID := bindTransportableEnrollment(t, repo.memRepo, orgID, accountID, candidateID, "exact@example.com")
	touchpoint, _ := repo.GetTouchpointByEnrollment(ctx, orgID, campaignID, contactID)
	dueAt := time.Now().UTC().Add(5 * time.Minute)
	touchpoint.DueAt = dueAt
	if err := repo.UpdateTouchpoint(ctx, touchpoint); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertOutreachRecipientSuppression(ctx, &models.SuppressedRecipient{
		OrganizationID: orgID, Email: "exact@example.com", Reason: "one-click unsubscribe",
		Source: models.DeliverabilityEventUnsubscribe, CreatedAt: dueAt.Add(-2 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	svc := &service{cfg: Config{Enabled: true}, repo: repo}
	if err := svc.AssertTransportable(ctx, orgID, touchpoint); err == nil {
		t.Fatal("suppression inserted before due_at must cancel transport")
	}
	cancelled, _ := repo.GetTouchpoint(ctx, orgID, touchpoint.ID)
	if cancelled.State != models.TouchpointDNC {
		t.Fatalf("touchpoint state=%s want %s", cancelled.State, models.TouchpointDNC)
	}
	account, _ := repo.GetAccount(ctx, orgID, accountID)
	if account.Blocked || account.DoNotContact {
		t.Fatal("exact recipient suppression must not invalidate the company account")
	}
	other, _ := repo.GetCandidate(ctx, orgID, otherCandidateID)
	if other.Blocked || other.Bounced || other.DoNotContact {
		t.Fatal("exact recipient suppression must preserve another mailbox at the same company")
	}
}

func TestObserveCampaignEmailAttemptIncludesNonCohortControlledTouch(t *testing.T) {
	ctx := context.Background()
	repo := newMemRepoWithSettings()
	orgID, accountID, candidateID := uuid.New(), uuid.New(), uuid.New()
	_, _ = repo.UpsertAccount(ctx, &models.OutreachAccount{
		ID: accountID, OrganizationID: orgID, CNPJ14: "12345678000148",
		SourceSystem: "extra-cli", SourceRunID: "source-run-47",
	})
	_, _ = repo.UpsertCandidate(ctx, &models.OutreachContactCandidate{
		ID: candidateID, OrganizationID: orgID, AccountID: accountID, Email: "attempt@example.com",
	})
	campaignID, contactID := bindTransportableEnrollment(t, repo.memRepo, orgID, accountID, candidateID, "attempt@example.com")
	account, _ := repo.GetAccount(ctx, orgID, accountID)
	account.SourceRunID = "source-run-47"
	_, _ = repo.UpsertAccount(ctx, account)
	taskID, mailboxID, sequenceID := uuid.New(), uuid.New(), uuid.New()
	attemptedAt := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	store := dispatch.NewMemoryStore()
	store.SetMailboxEnvelope(dispatch.MailboxEnvelope{
		EmailAccountID: mailboxID, OrganizationID: orgID, DailyCap: 50, HourlyCap: 20,
		Ready: true, Timezone: "UTC",
	})
	cfg := dispatch.DefaultConfig()
	cfg.SendsPerHour, cfg.MinGap = 100, 0
	cfg.WindowStart, cfg.WindowEnd, cfg.Timezone = "00:00", "23:59", "UTC"
	cfg.BusinessDaysOnly = false
	governor := dispatch.NewGovernor(cfg, store, &dispatch.FixedClock{T: attemptedAt})
	messageKey := MessageKeyCampaignEmail(campaignID, contactID, sequenceID)
	reserved, err := governor.TryReserve(ctx, dispatch.ReserveRequest{
		OrganizationID: orgID, EmailAccountID: &mailboxID, TaskID: &taskID,
		Channel: dispatch.ChannelEmail, MessageKey: messageKey,
	})
	if err != nil || !reserved.Allowed {
		t.Fatalf("reserve provider attempt: result=%+v err=%v", reserved, err)
	}
	svc := &service{cfg: Config{Enabled: true}, repo: repo, governor: governor}
	if err := svc.ObserveCampaignEmailAttempt(ctx, orgID, campaignID, contactID, sequenceID, taskID, mailboxID, "smtp", attemptedAt); err != nil {
		t.Fatal(err)
	}
	events := svc.ObservedControlledEmailEvents()
	if len(events) != 1 || events[0].Type != "email_attempted" {
		t.Fatalf("attempt projection=%+v", events)
	}
	if events[0].SourceRunID != "source-run-47" || events[0].MailboxID != mailboxID.String() {
		t.Fatalf("operational dimensions missing: %+v", events[0])
	}
}

type failOnceCommitStore struct {
	*dispatch.MemoryStore
	fail bool
}

func (s *failOnceCommitStore) CommitReservation(ctx context.Context, id uuid.UUID, sentAt time.Time) error {
	if s.fail {
		s.fail = false
		return errors.New("injected post-provider persistence failure")
	}
	return s.MemoryStore.CommitReservation(ctx, id, sentAt)
}

func TestCompleteCampaignEmailRecoversProviderAcceptedPersistenceFailure(t *testing.T) {
	allowConfengeSendingForTest(t)
	ctx := context.Background()
	repo := newMemRepoWithSettings()
	orgID, accountID, candidateID := uuid.New(), uuid.New(), uuid.New()
	_, _ = repo.UpsertAccount(ctx, &models.OutreachAccount{
		ID: accountID, OrganizationID: orgID, CNPJ14: "12345678000147",
	})
	_, _ = repo.UpsertCandidate(ctx, &models.OutreachContactCandidate{
		ID: candidateID, OrganizationID: orgID, AccountID: accountID, Email: "accepted@example.com",
	})
	campaignID, contactID := bindTransportableEnrollment(t, repo.memRepo, orgID, accountID, candidateID, "accepted@example.com")
	touchpoint, _ := repo.GetTouchpointByEnrollment(ctx, orgID, campaignID, contactID)

	acceptedAt := time.Date(2026, 8, 28, 17, 1, 58, 0, time.UTC)
	clock := &dispatch.FixedClock{T: acceptedAt.Add(30 * time.Minute)}
	mailboxID, sequenceID, taskID := uuid.New(), uuid.New(), uuid.New()
	baseStore := dispatch.NewMemoryStore()
	baseStore.SetMailboxEnvelope(dispatch.MailboxEnvelope{
		EmailAccountID: mailboxID, OrganizationID: orgID, DailyCap: 50, HourlyCap: 20,
		Ready: true, Timezone: "UTC",
	})
	store := &failOnceCommitStore{MemoryStore: baseStore, fail: true}
	cfg := dispatch.DefaultConfig()
	cfg.SendsPerHour, cfg.MinGap = 100, 0
	cfg.WindowStart, cfg.WindowEnd, cfg.Timezone = "00:00", "23:59", "UTC"
	cfg.BusinessDaysOnly = false
	governor := dispatch.NewGovernor(cfg, store, clock)
	messageKey := MessageKeyCampaignEmail(campaignID, contactID, sequenceID)
	queueID := uuid.New()
	if err := baseStore.Enqueue(ctx, &dispatch.QueueItem{
		ID: queueID, OrganizationID: orgID, EmailAccountID: &mailboxID,
		Channel: dispatch.ChannelEmail, DraftID: *touchpoint.DraftID,
		MessageKey: dispatch.MessageKeyEmail(*touchpoint.DraftID), RecipientRef: touchpoint.Recipient,
		Status: dispatch.QueueAttempted, CreatedAt: acceptedAt.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("record delegated hand-off: %v", err)
	}
	reserved, err := governor.TryReserve(ctx, dispatch.ReserveRequest{
		OrganizationID: orgID, EmailAccountID: &mailboxID, TaskID: &taskID,
		Channel: dispatch.ChannelEmail, MessageKey: messageKey, DraftID: touchpoint.DraftID,
	})
	if err != nil || !reserved.Allowed {
		t.Fatalf("reserve accepted send: result=%+v err=%v", reserved, err)
	}

	svc := &service{cfg: Config{Enabled: true}, repo: repo, governor: governor}
	providerMessageID := "<provider-accepted@confenge.com.br>"
	err = svc.CompleteCampaignEmail(ctx, orgID, campaignID, contactID, sequenceID, taskID, mailboxID, providerMessageID, "smtp", acceptedAt)
	if err == nil {
		t.Fatal("the injected local commit failure must surface")
	}
	if _, committed, getErr := baseStore.GetSendByKey(ctx, messageKey); getErr != nil || committed {
		t.Fatalf("failed local commit must not claim a durable send: committed=%v err=%v", committed, getErr)
	}
	partiallyCompleted, _ := repo.GetTouchpoint(ctx, orgID, touchpoint.ID)
	if partiallyCompleted.State != models.TouchpointSent || partiallyCompleted.ProviderMessageID != providerMessageID {
		t.Fatalf("provider fact must remain replayable after the later commit fails: %+v", partiallyCompleted)
	}

	if err := svc.CompleteCampaignEmail(ctx, orgID, campaignID, contactID, sequenceID, taskID, mailboxID, providerMessageID, "smtp", acceptedAt); err != nil {
		t.Fatalf("reconcile provider acceptance: %v", err)
	}
	sentAt, committed, err := baseStore.GetSendByKey(ctx, messageKey)
	if err != nil || !committed {
		t.Fatalf("reconciliation must commit the provider fact once: committed=%v err=%v", committed, err)
	}
	if !sentAt.Equal(acceptedAt) {
		t.Fatalf("dispatch ledger sent_at=%s want provider acceptance %s", sentAt, acceptedAt)
	}
	if reconciled, err := svc.ReconcileAttemptedDispatches(ctx); err != nil || reconciled != 1 {
		t.Fatalf("reconcile delegated hand-off: count=%d err=%v", reconciled, err)
	}
	queue, err := baseStore.ListQueueByStatus(ctx, dispatch.QueueSent, 10)
	if err != nil || len(queue) != 1 || queue[0].ID != queueID {
		t.Fatalf("delegated queue must close without sharing the campaign reservation key: queue=%+v err=%v", queue, err)
	}
	if err := svc.CompleteCampaignEmail(ctx, orgID, campaignID, contactID, sequenceID, taskID, mailboxID, providerMessageID, "smtp", acceptedAt); err != nil {
		t.Fatalf("idempotent reconciliation replay: %v", err)
	}
	if replayedAt, _, _ := baseStore.GetSendByKey(ctx, messageKey); !replayedAt.Equal(acceptedAt) {
		t.Fatalf("replay changed durable provider time to %s", replayedAt)
	}
}

func TestGateCampaignEmailTransientOnGovernorError(t *testing.T) {
	allowConfengeSendingForTest(t)
	// errStore fails TryReserveAtomic (simulates DB blip).
	store := &errStore{MemoryStore: dispatch.NewMemoryStore(), failReserve: true}
	clock := &dispatch.FixedClock{T: time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)}
	cfg := dispatch.DefaultConfig()
	cfg.WindowStart, cfg.WindowEnd, cfg.Timezone, cfg.MinGap = "00:00", "23:59", "UTC", 0
	cfg.BusinessDaysOnly = false
	gov := dispatch.NewGovernor(cfg, store, clock)

	repo := newMemRepoWithSettings()
	org := uuid.New()
	accID := uuid.New()
	_, _ = repo.UpsertAccount(context.Background(), &models.OutreachAccount{
		ID: accID, OrganizationID: org, CNPJ14: "12345678000177",
	})
	candidateID := uuid.New()
	_, _ = repo.UpsertCandidate(context.Background(), &models.OutreachContactCandidate{ID: candidateID, OrganizationID: org, AccountID: accID, Email: "lead@example.com"})
	campaignID, contactID := bindTransportableEnrollment(t, repo.memRepo, org, accID, candidateID, "lead@example.com")

	svc := &service{cfg: Config{Enabled: true}, repo: repo, governor: gov}
	r := svc.GateCampaignEmail(context.Background(), org, DefaultCampaignName, "lead@example.com",
		campaignID, contactID, uuid.New())
	if r.Kind != GateTransient {
		t.Fatalf("governor error must be Transient, got kind=%d reason=%s err=%v", r.Kind, r.Reason, r.Err)
	}
	if r.PermanentSuppress() {
		t.Fatal("Transient must never PermanentSuppress")
	}
	if r.Err == nil {
		t.Fatal("Transient should carry Err")
	}
}

func TestGateCampaignEmailDeferredCap(t *testing.T) {
	allowConfengeSendingForTest(t)
	store := dispatch.NewMemoryStore()
	clock := &dispatch.FixedClock{T: time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)}
	cfg := dispatch.DefaultConfig()
	cfg.WindowStart, cfg.WindowEnd, cfg.Timezone, cfg.MinGap = "00:00", "23:59", "UTC", 0
	cfg.BusinessDaysOnly = false
	cfg.SendsPerHour = 1
	gov := dispatch.NewGovernor(cfg, store, clock)

	repo := newMemRepoWithSettings()
	org := uuid.New()
	accID := uuid.New()
	_, _ = repo.UpsertAccount(context.Background(), &models.OutreachAccount{
		ID: accID, OrganizationID: org, CNPJ14: "12345678000166",
	})
	candidateID := uuid.New()
	_, _ = repo.UpsertCandidate(context.Background(), &models.OutreachContactCandidate{ID: candidateID, OrganizationID: org, AccountID: accID, Email: "lead@example.com"})
	campaignID, contactID := bindTransportableEnrollment(t, repo.memRepo, org, accID, candidateID, "lead@example.com")
	svc := &service{cfg: Config{Enabled: true}, repo: repo, governor: gov}

	// First send consumes the only slot.
	r1 := svc.GateCampaignEmail(context.Background(), org, DefaultCampaignName, "lead@example.com",
		campaignID, contactID, uuid.New())
	if r1.Kind != GateProceed {
		t.Fatalf("first: kind=%d", r1.Kind)
	}
	_ = svc.governor.Commit(context.Background(), r1.ReservationID)

	// Second is deferred (cap), not hard-block.
	r2 := svc.GateCampaignEmail(context.Background(), org, DefaultCampaignName, "lead@example.com",
		campaignID, contactID, uuid.New())
	if r2.Kind != GateDeferred {
		t.Fatalf("second: kind=%d reason=%s want Deferred", r2.Kind, r2.Reason)
	}
	if r2.PermanentSuppress() {
		t.Fatal("Deferred must not permanent suppress")
	}
}

func bindTransportableEnrollment(t *testing.T, repo *memRepo, orgID, accountID, candidateID uuid.UUID, email string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	account, _ := repo.GetAccount(ctx, orgID, accountID)
	contact, _ := repo.GetCandidate(ctx, orgID, candidateID)
	if account == nil || contact == nil {
		t.Fatal("account and candidate are required")
	}
	importID := uuid.New()
	now := time.Now().UTC()
	account.LastImportRunID = &importID
	account.SourceRunID = "current-run"
	account.MessageContextHash = "context-" + accountID.String()
	account.TargetFitClass = TargetFitConfirmed
	account.TargetFitVersion = "v1"
	account.TargetFitSourceWatermark = "watermark"
	account.TargetFitObservedAt = &now
	account.TargetFitFresh = true
	account.TargetFitEligible = true
	account.EmailSendReady = true
	_, _ = repo.UpsertAccount(ctx, account)
	contact.LastImportRunID = &importID
	contact.EmailSendReady = true
	contact.OwnershipStatus = "COMPANY_OWNED"
	contact.VerificationStatus = models.OutreachVerifyOfficialSource
	_, _ = repo.UpsertCandidate(ctx, contact)
	campaignID, enrollmentContactID := uuid.New(), uuid.New()
	draft := &models.OutreachDraft{
		OrganizationID: orgID, AccountID: accountID, ContactCandidateID: &candidateID,
		RecipientEmail: email, CampaignID: &campaignID, EnrollmentContactID: &enrollmentContactID,
		Status: models.OutreachDraftEnrolled,
	}
	if err := repo.UpsertDraft(ctx, draft); err != nil {
		t.Fatal(err)
	}
	approver := uuid.New()
	touchpoint := &models.OutreachTouchpoint{
		OrganizationID: orgID, AccountID: accountID, ContactCandidateID: &candidateID,
		DraftID: &draft.ID, Ordinal: 1, Channel: models.OutreachChannelEmail,
		State: models.TouchpointQueued, Recipient: email, Subject: "Approved subject",
		BodyText:   "Approved body with enough detail",
		ApprovedBy: &approver, AuthorizationMode: AuthorizationModeHumanTouchpoint,
		GeneratedContextHash: account.MessageContextHash,
	}
	RecomputeContentHash(touchpoint)
	touchpoint.ApprovedContentHash = touchpoint.ContentHash
	if err := repo.InsertTouchpoint(ctx, touchpoint); err != nil {
		t.Fatal(err)
	}
	return campaignID, enrollmentContactID
}

func TestGateCampaignEmailBlocksPartialAuthoritativeFeed(t *testing.T) {
	allowConfengeSendingForTest(t)
	repo := newMemRepoWithSettings()
	orgID, accountID, candidateID := uuid.New(), uuid.New(), uuid.New()
	_, _ = repo.UpsertAccount(context.Background(), &models.OutreachAccount{ID: accountID, OrganizationID: orgID, CNPJ14: "12345678000155"})
	_, _ = repo.UpsertCandidate(context.Background(), &models.OutreachContactCandidate{ID: candidateID, OrganizationID: orgID, AccountID: accountID, Email: "lead@example.com"})
	campaignID, contactID := bindTransportableEnrollment(t, repo.memRepo, orgID, accountID, candidateID, "lead@example.com")
	now := time.Now().UTC()
	repo.feedSync = map[uuid.UUID]*models.OutreachFeedSyncState{orgID: {
		OrganizationID: orgID, LastStatus: "partial", LastRunID: "current-run",
		LastSnapshotHash: "snapshot", SourceGeneratedAt: &now,
	}}
	dispatchCfg := dispatch.DefaultConfig()
	dispatchCfg.WindowStart, dispatchCfg.WindowEnd, dispatchCfg.Timezone, dispatchCfg.BusinessDaysOnly = "00:00", "23:59", "UTC", false
	svc := &service{
		cfg: Config{Enabled: true, FeedSyncEnabled: true}, repo: repo,
		governor: dispatch.NewGovernor(dispatchCfg, dispatch.NewMemoryStore(), nil),
	}
	result := svc.GateCampaignEmail(context.Background(), orgID, DefaultCampaignName, "lead@example.com", campaignID, contactID, uuid.New())
	if result.Kind != GateCommercialBlock || result.Reason != "authoritative_feed_invalid" {
		t.Fatalf("partial authoritative feed must block transport: %+v", result)
	}
}

func TestGateCampaignEmailBypassNonConfenge(t *testing.T) {
	svc := &service{cfg: Config{Enabled: true}}
	r := svc.GateCampaignEmail(context.Background(), uuid.New(), "Regular campaign", "a@b.com",
		uuid.New(), uuid.New(), uuid.New())
	if r.Kind != GateBypass {
		t.Fatalf("kind=%d", r.Kind)
	}
	if r.PermanentSuppress() {
		t.Fatal("Bypass must not suppress")
	}
}

func TestGateCampaignEmailRenamedConfiguredCampaignCannotBypass(t *testing.T) {
	allowConfengeSendingForTest(t)
	orgID, campaignID := uuid.New(), uuid.New()
	repo := newMemRepoWithSettings()
	if err := repo.UpsertOrgSettings(context.Background(), &models.OutreachOrgSettings{OrganizationID: orgID, CampaignID: &campaignID}); err != nil {
		t.Fatal(err)
	}
	svc := &service{cfg: Config{Enabled: true}, repo: repo}
	result := svc.GateCampaignEmail(context.Background(), orgID, "Renamed campaign", "lead@example.com", campaignID, uuid.New(), uuid.New())
	if result.Kind == GateBypass || result.Kind == GateProceed {
		t.Fatalf("configured CONFENGE campaign must fail closed without an exact approved touchpoint: %+v", result)
	}
}

// TestGateCampaignEmailConfengeNilGovernorFailClosed: CONFENGE + nil governor => zero send.
func TestGateCampaignEmailConfengeNilGovernorFailClosed(t *testing.T) {
	allowConfengeSendingForTest(t)
	svc := &service{cfg: Config{Enabled: true}, governor: nil}
	r := svc.GateCampaignEmail(context.Background(), uuid.New(), DefaultCampaignName, "lead@example.com",
		uuid.New(), uuid.New(), uuid.New())
	if r.Kind != GateTransient {
		t.Fatalf("CONFENGE+nil governor must be Transient (fail-closed), got kind=%d reason=%s", r.Kind, r.Reason)
	}
	if r.Reason != ReasonNoGovernor {
		t.Fatalf("reason want %s got %s", ReasonNoGovernor, r.Reason)
	}
	if r.PermanentSuppress() {
		t.Fatal("must not permanent suppress")
	}
	// Non-CONFENGE still bypasses with nil governor.
	r2 := svc.GateCampaignEmail(context.Background(), uuid.New(), "Other campaign", "a@b.com",
		uuid.New(), uuid.New(), uuid.New())
	if r2.Kind != GateBypass {
		t.Fatalf("non-CONFENGE+nil want Bypass, got %d", r2.Kind)
	}
}

// TestGateCampaignEmailConfengeDisabledFailClosed: enabled=false still fail-closed for CONFENGE name.
func TestGateCampaignEmailConfengeDisabledFailClosed(t *testing.T) {
	allowConfengeSendingForTest(t)
	svc := &service{cfg: Config{Enabled: false}, governor: nil}
	r := svc.GateCampaignEmail(context.Background(), uuid.New(), "CONFENGE cold", "x@y.com",
		uuid.New(), uuid.New(), uuid.New())
	if r.Kind != GateTransient {
		t.Fatalf("got kind=%d want Transient", r.Kind)
	}
}

// gateIntelResolver scripts the optional live-intelligence lookup and records
// whether the gate consulted it at all.
type gateIntelResolver struct {
	payload *liveintel.LiveIntelligenceV1
	ok      bool
	panics  bool
	calls   int
}

func (r *gateIntelResolver) Resolve(context.Context, uuid.UUID, uuid.UUID) (*liveintel.LiveIntelligenceV1, bool) {
	r.calls++
	if r.panics {
		panic("intel resolver exploded")
	}
	return r.payload, r.ok
}

func validGateIntel() *liveintel.LiveIntelligenceV1 {
	return &liveintel.LiveIntelligenceV1{
		Schema:      liveintel.SchemaLiveIntelligenceV1,
		SubjectKey:  "contrato-2026-0001",
		Kind:        liveintel.KindOpportunity,
		PublicURL:   "https://pncp.gov.br/app/contratos/2026-0001",
		ObservedAt:  time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC),
		Attestation: "attestation-signature",
	}
}

// newProceedingGateService builds the smallest service whose gate reaches
// GateProceed, so an intel assertion measures only the hook.
func newProceedingGateService(t *testing.T) (*service, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	allowConfengeSendingForTest(t)
	ctx := context.Background()
	repo := newMemRepoWithSettings()
	orgID, accountID, candidateID := uuid.New(), uuid.New(), uuid.New()
	_, _ = repo.UpsertAccount(ctx, &models.OutreachAccount{
		ID: accountID, OrganizationID: orgID, CNPJ14: "12345678000122",
	})
	_, _ = repo.UpsertCandidate(ctx, &models.OutreachContactCandidate{
		ID: candidateID, OrganizationID: orgID, AccountID: accountID, Email: "intel@example.com",
	})
	campaignID, contactID := bindTransportableEnrollment(t, repo.memRepo, orgID, accountID, candidateID, "intel@example.com")
	cfg := dispatch.DefaultConfig()
	cfg.SendsPerHour, cfg.MinGap, cfg.RateMode = 100, 0, "fixed"
	cfg.WindowStart, cfg.WindowEnd, cfg.Timezone = "00:00", "23:59", "UTC"
	cfg.BusinessDaysOnly = false
	governor := dispatch.NewGovernor(cfg, dispatch.NewMemoryStore(),
		&dispatch.FixedClock{T: time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)})
	svc := &service{cfg: Config{Enabled: true}, repo: repo, governor: governor}
	return svc, orgID, campaignID, contactID
}

// Live intelligence is attached to an already-granted GateProceed and can never
// change the outcome: absent, failing, panicking or valid all proceed.
func TestGateCampaignEmailLiveIntelNeverChangesTheDecision(t *testing.T) {
	cases := []struct {
		name       string
		resolver   liveintel.Resolver
		wantIntel  bool
		wantCalled bool
	}{
		{name: "not wired", resolver: nil},
		{name: "noop", resolver: liveintel.NoopResolver{}},
		{name: "absent", resolver: &gateIntelResolver{}, wantCalled: true},
		{name: "lookup error", resolver: liveintel.LookupFunc(func(context.Context, uuid.UUID, uuid.UUID) (*liveintel.LiveIntelligenceV1, error) {
			return nil, errors.New("intel source unavailable")
		})},
		{name: "malformed payload", resolver: &gateIntelResolver{payload: &liveintel.LiveIntelligenceV1{}, ok: true}, wantCalled: true},
		{name: "panicking", resolver: &gateIntelResolver{panics: true}, wantCalled: true},
		{name: "valid", resolver: &gateIntelResolver{payload: validGateIntel(), ok: true}, wantIntel: true, wantCalled: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, orgID, campaignID, contactID := newProceedingGateService(t)
			svc.liveIntel = tc.resolver
			result := svc.GateCampaignEmail(context.Background(), orgID, DefaultCampaignName,
				"intel@example.com", campaignID, contactID, uuid.New())
			if result.Kind != GateProceed {
				t.Fatalf("intel changed the decision: %+v", result)
			}
			if (result.OptionalIntel != nil) != tc.wantIntel {
				t.Fatalf("OptionalIntel=%+v want present=%v", result.OptionalIntel, tc.wantIntel)
			}
			if scripted, ok := tc.resolver.(*gateIntelResolver); ok && (scripted.calls > 0) != tc.wantCalled {
				t.Fatalf("resolver calls=%d want called=%v", scripted.calls, tc.wantCalled)
			}
		})
	}
}

// Intelligence is looked up only on the proceed path, so it can never turn a
// block into a send and is never consulted for a decision that is not proceed.
func TestGateCampaignEmailLiveIntelIsNotConsultedOnBlockedPaths(t *testing.T) {
	allowConfengeSendingForTest(t)
	ctx := context.Background()
	org := uuid.New()
	accID, candID := uuid.New(), uuid.New()
	repo := newMemRepoWithSettings()
	_, _ = repo.UpsertAccount(ctx, &models.OutreachAccount{
		ID: accID, OrganizationID: org, CNPJ14: "12345678000111", DoNotContact: true, Blocked: true,
	})
	_, _ = repo.UpsertCandidate(ctx, &models.OutreachContactCandidate{
		ID: candID, OrganizationID: org, AccountID: accID, Email: "blocked@example.com",
	})
	cfg := dispatch.DefaultConfig()
	cfg.WindowStart, cfg.WindowEnd, cfg.Timezone, cfg.MinGap = "00:00", "23:59", "UTC", 0
	cfg.BusinessDaysOnly = false
	resolver := &gateIntelResolver{payload: validGateIntel(), ok: true}

	svc := &service{
		cfg: Config{Enabled: true}, repo: repo, liveIntel: resolver,
		governor: dispatch.NewGovernor(cfg, dispatch.NewMemoryStore(),
			&dispatch.FixedClock{T: time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)}),
	}
	hard := svc.GateCampaignEmail(ctx, org, DefaultCampaignName, "blocked@example.com",
		uuid.New(), uuid.New(), uuid.New())
	if hard.Kind != GateHardBlock {
		t.Fatalf("account DNC must stay a hard block: %+v", hard)
	}
	if hard.OptionalIntel != nil {
		t.Fatalf("a blocked result carried intelligence: %+v", hard.OptionalIntel)
	}

	// Fail-closed transient (no governor) and legacy bypass are equally untouched.
	bare := &service{cfg: Config{Enabled: true}, liveIntel: resolver}
	if r := bare.GateCampaignEmail(ctx, org, DefaultCampaignName, "x@y.com", uuid.Nil, uuid.Nil, uuid.New()); r.Kind != GateTransient || r.OptionalIntel != nil {
		t.Fatalf("nil governor result changed: %+v", r)
	}
	if r := bare.GateCampaignEmail(ctx, org, "Regular campaign", "x@y.com", uuid.Nil, uuid.Nil, uuid.New()); r.Kind != GateBypass || r.OptionalIntel != nil {
		t.Fatalf("non-CONFENGE bypass changed: %+v", r)
	}
	if resolver.calls != 0 {
		t.Fatalf("intelligence was consulted %d times on non-proceed paths", resolver.calls)
	}
}

// errStore embeds MemoryStore and can fail TryReserveAtomic.
type errStore struct {
	*dispatch.MemoryStore
	failReserve bool
}

func (e *errStore) TryReserveAtomic(ctx context.Context, in dispatch.AtomicReserveInput) (dispatch.AtomicReserveOutput, error) {
	if e.failReserve {
		return dispatch.AtomicReserveOutput{}, errors.New("simulated store failure")
	}
	return e.MemoryStore.TryReserveAtomic(ctx, in)
}
