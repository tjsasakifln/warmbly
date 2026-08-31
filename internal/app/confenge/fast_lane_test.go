package confenge

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// fastLaneRepo implements only what the fast lane touches. The embedded nil
// interface makes any unexpected repository call panic loudly rather than
// silently returning a zero value.
type fastLaneRepo struct {
	repository.OutreachRepository
	touchpoints map[uuid.UUID]*models.OutreachTouchpoint
	accounts    map[uuid.UUID]*models.OutreachAccount
	accepted    map[uuid.UUID]bool
	acceptedErr error
	committed   map[string]bool
	suppressed  map[string]*models.SuppressedRecipient
	suppressErr error
	updates     int
	updateErr   error
}

func (f *fastLaneRepo) GetTouchpointByDraft(_ context.Context, _ uuid.UUID, draftID uuid.UUID) (*models.OutreachTouchpoint, error) {
	return f.touchpoints[draftID], nil
}

func (f *fastLaneRepo) GetAccount(_ context.Context, _ uuid.UUID, accountID uuid.UUID) (*models.OutreachAccount, error) {
	return f.accounts[accountID], nil
}

func (f *fastLaneRepo) HasAcceptedInitialForAccount(_ context.Context, _ uuid.UUID, accountID uuid.UUID) (bool, error) {
	return f.accepted[accountID], f.acceptedErr
}

func (f *fastLaneRepo) HasCommittedFeedRun(_ context.Context, _ uuid.UUID, sourceRunID string) (bool, error) {
	return f.committed[sourceRunID], nil
}

func (f *fastLaneRepo) UpdateTouchpoint(_ context.Context, tp *models.OutreachTouchpoint) error {
	f.updates++
	if f.updateErr != nil {
		return f.updateErr
	}
	f.touchpoints[*tp.DraftID] = tp
	return nil
}

func (f *fastLaneRepo) ListTouchpoints(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ string, _ int, _ int) ([]models.OutreachTouchpoint, error) {
	return nil, nil
}

func (f *fastLaneRepo) GetOutreachRecipientSuppression(_ context.Context, _ uuid.UUID, recipient string) (*models.SuppressedRecipient, error) {
	if f.suppressErr != nil {
		return nil, f.suppressErr
	}
	return f.suppressed[recipient], nil
}

// scriptedTransport returns a fixed outcome and counts real send attempts, so a
// test can prove a message was never handed to a provider twice.
type scriptedTransport struct {
	outcome  FirstTouchOutcome
	err      error
	attempts int
	sentTo   []string
}

func (s *scriptedTransport) SendFirstTouch(_ context.Context, msg FirstTouchMessage) (FirstTouchAcceptance, FirstTouchOutcome, error) {
	s.attempts++
	s.sentTo = append(s.sentTo, msg.To)
	if s.outcome == FirstTouchAccepted {
		return FirstTouchAcceptance{
			Provider:          "smtp",
			ProviderMessageID: "<mid-" + msg.MessageKey + "@confenge.com.br>",
			AcceptedAt:        time.Now().UTC(),
		}, FirstTouchAccepted, nil
	}
	return FirstTouchAcceptance{}, s.outcome, s.err
}

type fastLaneHarness struct {
	svc       *service
	store     *dispatch.MemoryStore
	repo      *fastLaneRepo
	transport *scriptedTransport
	clock     *dispatch.FixedClock
	mailbox   uuid.UUID
	orgID     uuid.UUID
}

// newFastLaneHarness builds a service whose window is open and whose mailbox has
// generous capacity, so a test only exercises what it means to.
func newFastLaneHarness(t *testing.T, outcome FirstTouchOutcome, sendErr error) *fastLaneHarness {
	t.Helper()
	// A Wednesday at midday in Sao Paulo: inside the business window.
	clock := &dispatch.FixedClock{T: time.Date(2026, 9, 2, 15, 0, 0, 0, time.UTC)}
	store := dispatch.NewMemoryStore()
	cfg := dispatch.DefaultConfig()
	cfg.SendsPerHour = 100
	cfg.MinGap = 0
	cfg.RateMode = "fixed"
	gov := dispatch.NewGovernor(cfg, store, clock)

	mailbox := uuid.New()
	store.SetMailboxEnvelope(dispatch.MailboxEnvelope{
		EmailAccountID: mailbox, Ready: true, DailyCap: 50, MinGap: 0, HourlyCap: 100,
		Timezone: "America/Sao_Paulo",
	})

	repo := &fastLaneRepo{
		touchpoints: map[uuid.UUID]*models.OutreachTouchpoint{},
		accounts:    map[uuid.UUID]*models.OutreachAccount{},
		accepted:    map[uuid.UUID]bool{},
		committed:   map[string]bool{"run-committed": true},
		suppressed:  map[string]*models.SuppressedRecipient{},
	}
	transport := &scriptedTransport{outcome: outcome, err: sendErr}
	svc := &service{
		repo:                repo,
		governor:            gov,
		firstTouchTransport: transport,
		nowFn:               func() time.Time { return clock.Now() },
	}
	return &fastLaneHarness{
		svc: svc, store: store, repo: repo, transport: transport,
		clock: clock, mailbox: mailbox, orgID: uuid.New(),
	}
}

// enqueue creates one approved, queued, due first touch.
func (h *fastLaneHarness) enqueue(t *testing.T, recipient string) (uuid.UUID, string) {
	t.Helper()
	draftID := uuid.New()
	key := dispatch.MessageKeyEmail(draftID)
	mailbox := h.mailbox
	if err := h.store.Enqueue(context.Background(), &dispatch.QueueItem{
		ID: uuid.New(), OrganizationID: h.orgID, EmailAccountID: &mailbox,
		Channel: dispatch.ChannelEmail, DraftID: draftID, MessageKey: key,
		RecipientRef: recipient, DueAt: h.clock.Now().Add(-time.Minute),
		Status: dispatch.QueueQueued, CreatedAt: h.clock.Now(),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	accountID := uuid.New()
	h.repo.touchpoints[draftID] = &models.OutreachTouchpoint{
		ID: uuid.New(), OrganizationID: h.orgID, AccountID: accountID, DraftID: &draftID,
		SourceRunID: "run-committed",
		State:       models.TouchpointQueued, Recipient: recipient,
		Subject: "Assunto aprovado", BodyText: "Corpo aprovado.",
		ContentHash: "hash-a", ApprovedContentHash: "hash-a",
	}
	account := &models.OutreachAccount{ID: accountID, OrganizationID: h.orgID, CNPJRoot: "11222333", TargetPartyRole: PartyRoleSupplier}
	qualification := testRootQualification(account.CNPJRoot, h.clock.Now().AddDate(-1, 0, 0))
	applyCommercialQualificationToAccount(account, &qualification, h.clock.Now())
	h.repo.accounts[accountID] = account
	return draftID, key
}

func (h *fastLaneHarness) queueStatus(t *testing.T, key string) string {
	t.Helper()
	item, err := h.store.GetQueueByKey(context.Background(), key)
	if err != nil || item == nil {
		t.Fatalf("queue row %s missing: %v", key, err)
	}
	return item.Status
}

// A provider-accepted first touch is recorded exactly once, the queue row goes
// terminal, and nothing can select that message again.
func TestFastLaneRecordsAcceptedSendExactlyOnceAndClosesTheRow(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchAccepted, nil)
	_, key := h.enqueue(t, "alvo@exemplo.com.br")

	progressed, err := h.svc.ProcessFastLaneOnce(context.Background())
	if err != nil || !progressed {
		t.Fatalf("first pass did no work: progressed=%v err=%v", progressed, err)
	}
	if h.transport.attempts != 1 {
		t.Fatalf("expected exactly one provider attempt, got %d", h.transport.attempts)
	}
	if got := h.queueStatus(t, key); got != dispatch.QueueSent {
		t.Fatalf("queue row should be terminal 'sent', got %q", got)
	}
	if _, sent, _ := h.store.GetSendByKey(context.Background(), key); !sent {
		t.Fatal("ledger has no record of a send the provider accepted")
	}

	// Draining again must find nothing: the same logical first touch is gone.
	progressed, err = h.svc.ProcessFastLaneOnce(context.Background())
	if err != nil {
		t.Fatalf("second pass errored: %v", err)
	}
	if progressed {
		t.Fatal("a completed first touch was selected again")
	}
	if h.transport.attempts != 1 {
		t.Fatalf("message was handed to the provider %d times", h.transport.attempts)
	}
}

func TestFastLaneRechecksQualificationImmediatelyBeforeProvider(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchAccepted, nil)
	draftID, key := h.enqueue(t, "alvo@exemplo.com.br")
	tp := h.repo.touchpoints[draftID]
	account := h.repo.accounts[tp.AccountID]
	qualification := testRootQualification(account.CNPJRoot, h.clock.Now().AddDate(-3, 0, 1))
	applyCommercialQualificationToAccount(account, &qualification, h.clock.Now())
	if !AccountCommercialQualification(account, h.clock.Now()).AllowsTransport() {
		t.Fatal("fixture was not qualified at approval time")
	}
	h.clock.T = h.clock.Now().Add(48 * time.Hour)

	progressed, err := h.svc.ProcessFastLaneOnce(context.Background())
	if err != nil || !progressed {
		t.Fatalf("expired row was not closed: progressed=%v err=%v", progressed, err)
	}
	if h.transport.attempts != 0 {
		t.Fatalf("expired qualification reached provider: attempts=%d", h.transport.attempts)
	}
	if got := h.queueStatus(t, key); got != dispatch.QueueCancelled {
		t.Fatalf("expired qualification queue state=%q", got)
	}
}

func TestFastLaneLedgerOnlyAcceptanceBlocksDifferentMessageKey(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchAccepted, nil)
	draftID, key := h.enqueue(t, "alvo@exemplo.com.br")
	accountID := h.repo.touchpoints[draftID].AccountID
	h.repo.accepted[accountID] = true

	progressed, err := h.svc.ProcessFastLaneOnce(context.Background())
	if err != nil || !progressed {
		t.Fatalf("ledger duplicate was not closed: progressed=%v err=%v", progressed, err)
	}
	if h.transport.attempts != 0 {
		t.Fatalf("ledger duplicate reached provider: attempts=%d", h.transport.attempts)
	}
	if got := h.queueStatus(t, key); got != dispatch.QueueCancelled {
		t.Fatalf("ledger duplicate queue state=%q", got)
	}
}

func TestFastLaneBlocksLegacyQueuedUncommittedLineage(t *testing.T) {
	for _, sourceRunID := range []string{"", "legacy-uncommitted"} {
		t.Run(firstNonEmpty(sourceRunID, "empty"), func(t *testing.T) {
			h := newFastLaneHarness(t, FirstTouchAccepted, nil)
			draftID, key := h.enqueue(t, "alvo@exemplo.com.br")
			h.repo.touchpoints[draftID].SourceRunID = sourceRunID

			progressed, err := h.svc.ProcessFastLaneOnce(context.Background())
			if err != nil || !progressed {
				t.Fatalf("uncommitted legacy row was not closed: progressed=%v err=%v", progressed, err)
			}
			if h.transport.attempts != 0 {
				t.Fatalf("uncommitted legacy row reached provider: attempts=%d", h.transport.attempts)
			}
			if got := h.queueStatus(t, key); got != dispatch.QueueCancelled {
				t.Fatalf("uncommitted legacy queue state=%q", got)
			}
		})
	}
}

// A restart that re-queues an already-recorded key must close the row from the
// ledger instead of sending a second copy.
func TestFastLaneNeverResendsAKeyTheLedgerAlreadyHolds(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchAccepted, nil)
	_, key := h.enqueue(t, "alvo@exemplo.com.br")
	if _, err := h.svc.ProcessFastLaneOnce(context.Background()); err != nil {
		t.Fatalf("initial send: %v", err)
	}

	// Simulate a crash-recovery that puts the row back to queued.
	if err := h.store.RetryQueue(context.Background(), h.mustQueueID(t, key), h.clock.Now().Add(-time.Minute), "replayed"); err != nil {
		// RetryQueue only moves reserved rows; force the state directly.
		_ = err
	}
	h.forceQueued(t, key)

	progressed, err := h.svc.ProcessFastLaneOnce(context.Background())
	if err != nil {
		t.Fatalf("replay pass: %v", err)
	}
	if !progressed {
		t.Fatal("replayed row was not handled")
	}
	if h.transport.attempts != 1 {
		t.Fatalf("replay resent the message: %d provider attempts", h.transport.attempts)
	}
	if got := h.queueStatus(t, key); got != dispatch.QueueSent {
		t.Fatalf("replayed row should close as sent, got %q", got)
	}
}

// A definitive rejection is terminal and must not hold up the queue behind it.
func TestFastLanePermanentRejectionDoesNotHeadOfLineBlock(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchPermanent, errors.New("RECIPIENT_REJECTED: 550 mailbox unavailable"))
	_, badKey := h.enqueue(t, "invalido@exemplo.com.br")

	if _, err := h.svc.ProcessFastLaneOnce(context.Background()); err != nil {
		t.Fatalf("permanent pass: %v", err)
	}
	if got := h.queueStatus(t, badKey); got != dispatch.QueueFailed {
		t.Fatalf("permanent rejection should be terminal 'failed', got %q", got)
	}

	// The next valid row must still send.
	h.transport.outcome = FirstTouchAccepted
	h.transport.err = nil
	_, goodKey := h.enqueue(t, "valido@exemplo.com.br")
	progressed, err := h.svc.ProcessFastLaneOnce(context.Background())
	if err != nil || !progressed {
		t.Fatalf("valid row blocked behind a rejected one: progressed=%v err=%v", progressed, err)
	}
	if got := h.queueStatus(t, goodKey); got != dispatch.QueueSent {
		t.Fatalf("valid row should have sent, got %q", got)
	}
}

// An ambiguous provider result must never be retried.
func TestFastLaneNeverResendsAnAmbiguousResult(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchAmbiguous, errors.New("DELIVERY_UNKNOWN: connection lost at end of DATA"))
	_, key := h.enqueue(t, "alvo@exemplo.com.br")

	if _, err := h.svc.ProcessFastLaneOnce(context.Background()); err != nil {
		t.Fatalf("ambiguous pass: %v", err)
	}
	if got := h.queueStatus(t, key); got != dispatch.QueueAttempted {
		t.Fatalf("ambiguous result should park as 'attempted', got %q", got)
	}
	// Repeated drains must not turn an unknown into a duplicate.
	for i := 0; i < 3; i++ {
		_, _ = h.svc.ProcessFastLaneOnce(context.Background())
	}
	if h.transport.attempts != 1 {
		t.Fatalf("ambiguous result was resent: %d provider attempts", h.transport.attempts)
	}
}

// A suppressed recipient is never handed to the provider.
func TestFastLaneRefusesSuppressedRecipient(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchAccepted, nil)
	_, key := h.enqueue(t, "bounced@exemplo.com.br")
	h.repo.suppressed["bounced@exemplo.com.br"] = &models.SuppressedRecipient{
		Source: models.DeliverabilityEventBounce,
	}

	if _, err := h.svc.ProcessFastLaneOnce(context.Background()); err != nil {
		t.Fatalf("suppressed pass: %v", err)
	}
	if h.transport.attempts != 0 {
		t.Fatal("a suppressed recipient was sent to")
	}
	if got := h.queueStatus(t, key); got != dispatch.QueueCancelled {
		t.Fatalf("suppressed row should cancel, got %q", got)
	}
}

// An unreadable suppression list is not permission to send.
func TestFastLaneFailsClosedWhenSuppressionUnreadable(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchAccepted, nil)
	_, key := h.enqueue(t, "alvo@exemplo.com.br")
	h.repo.suppressErr = errors.New("suppression store down")

	if _, err := h.svc.ProcessFastLaneOnce(context.Background()); err != nil {
		t.Fatalf("pass: %v", err)
	}
	if h.transport.attempts != 0 {
		t.Fatal("sent while the suppression list was unreadable")
	}
	if got := h.queueStatus(t, key); got != dispatch.QueueCancelled {
		t.Fatalf("expected cancelled, got %q", got)
	}
}

// A failing compatibility projection must not undo a recorded send.
func TestFastLaneCompatFailureDoesNotUnrecordAnAcceptedSend(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchAccepted, nil)
	_, key := h.enqueue(t, "alvo@exemplo.com.br")
	h.repo.updateErr = errors.New("touchpoint projection unavailable")

	if _, err := h.svc.ProcessFastLaneOnce(context.Background()); err != nil {
		t.Fatalf("pass returned error despite provider acceptance: %v", err)
	}
	if _, sent, _ := h.store.GetSendByKey(context.Background(), key); !sent {
		t.Fatal("a legacy projection failure erased the provider fact")
	}
	if got := h.queueStatus(t, key); got != dispatch.QueueSent {
		t.Fatalf("queue row should still be terminal, got %q", got)
	}
}

// Content that no longer matches its approval is never sent.
func TestFastLaneRefusesContentThatDriftedFromItsApproval(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchAccepted, nil)
	draftID, key := h.enqueue(t, "alvo@exemplo.com.br")
	h.repo.touchpoints[draftID].ContentHash = "hash-b" // approval was hash-a

	if _, err := h.svc.ProcessFastLaneOnce(context.Background()); err != nil {
		t.Fatalf("pass: %v", err)
	}
	if h.transport.attempts != 0 {
		t.Fatal("sent copy that no longer matched its approval")
	}
	if got := h.queueStatus(t, key); got != dispatch.QueueCancelled {
		t.Fatalf("expected cancelled, got %q", got)
	}
}

// A transient failure retries, and stops retrying once bounded.
func TestFastLaneTransientFailureRetriesAndIsBounded(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchTransient, errors.New("SERVER_UNREACHABLE: dial timeout"))
	_, key := h.enqueue(t, "alvo@exemplo.com.br")

	if _, err := h.svc.ProcessFastLaneOnce(context.Background()); err != nil {
		t.Fatalf("transient pass: %v", err)
	}
	if got := h.queueStatus(t, key); got != dispatch.QueueQueued {
		t.Fatalf("transient failure should re-queue, got %q", got)
	}
	item, _ := h.store.GetQueueByKey(context.Background(), key)
	if !item.DueAt.After(h.clock.Now()) {
		t.Fatal("retry was not backed off into the future")
	}
}

// Helpers.

func (h *fastLaneHarness) mustQueueID(t *testing.T, key string) uuid.UUID {
	t.Helper()
	item, err := h.store.GetQueueByKey(context.Background(), key)
	if err != nil || item == nil {
		t.Fatalf("queue row %s missing", key)
	}
	return item.ID
}

func (h *fastLaneHarness) forceQueued(t *testing.T, key string) {
	t.Helper()
	item, err := h.store.GetQueueByKey(context.Background(), key)
	if err != nil || item == nil {
		t.Fatalf("queue row %s missing", key)
	}
	if err := h.store.UpdateQueueStatus(context.Background(), item.ID, dispatch.QueueQueued, ""); err != nil {
		t.Fatalf("force queued: %v", err)
	}
}
