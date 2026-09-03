package confenge

import (
	"context"
	"errors"
	"strings"
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
	touchpoints      map[uuid.UUID]*models.OutreachTouchpoint
	accounts         map[uuid.UUID]*models.OutreachAccount
	candidates       map[uuid.UUID]*models.OutreachContactCandidate
	accepted         map[uuid.UUID]bool
	acceptedErr      error
	committed        map[string]bool
	suppressed       map[string]*models.SuppressedRecipient
	suppressErr      error
	updates          int
	updateErr        error
	onTouchpointRead func(*models.OutreachTouchpoint)
}

func (f *fastLaneRepo) GetTouchpointByDraft(_ context.Context, _ uuid.UUID, draftID uuid.UUID) (*models.OutreachTouchpoint, error) {
	tp := f.touchpoints[draftID]
	if f.onTouchpointRead != nil {
		f.onTouchpointRead(tp)
	}
	return tp, nil
}

func (f *fastLaneRepo) GetAccount(_ context.Context, _ uuid.UUID, accountID uuid.UUID) (*models.OutreachAccount, error) {
	return f.accounts[accountID], nil
}

func (f *fastLaneRepo) GetCandidate(_ context.Context, _ uuid.UUID, candidateID uuid.UUID) (*models.OutreachContactCandidate, error) {
	return f.candidates[candidateID], nil
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

func (f *fastLaneRepo) ListTouchpoints(_ context.Context, _ uuid.UUID, accountID uuid.UUID, state string, limit, offset int) ([]models.OutreachTouchpoint, error) {
	var out []models.OutreachTouchpoint
	for _, tp := range f.touchpoints {
		if tp == nil || tp.AccountID != accountID {
			continue
		}
		if state != "" && tp.State != state {
			continue
		}
		out = append(out, *tp)
	}
	if offset < 0 {
		offset = 0
	}
	if offset >= len(out) {
		return nil, nil
	}
	out = out[offset:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fastLaneRepo) GetOutreachRecipientSuppression(_ context.Context, _ uuid.UUID, recipient string) (*models.SuppressedRecipient, error) {
	if f.suppressErr != nil {
		return nil, f.suppressErr
	}
	return f.suppressed[recipient], nil
}

func (f *fastLaneRepo) UpsertOutreachRecipientSuppression(_ context.Context, value *models.SuppressedRecipient) error {
	if value == nil {
		return errors.New("suppression required")
	}
	copyValue := *value
	f.suppressed[strings.ToLower(strings.TrimSpace(value.Email))] = &copyValue
	return nil
}

// scriptedTransport returns a fixed outcome and counts real send attempts, so a
// test can prove a message was never handed to a provider twice.
type scriptedTransport struct {
	outcome  FirstTouchOutcome
	err      error
	attempts int
	sentTo   []string
	deadline time.Time
}

func (s *scriptedTransport) SendFirstTouch(ctx context.Context, msg FirstTouchMessage) (FirstTouchAcceptance, FirstTouchOutcome, error) {
	if deadline, ok := ctx.Deadline(); ok {
		s.deadline = deadline
	}
	if s.outcome == FirstTouchAccepted || s.outcome == FirstTouchAmbiguous {
		if msg.BeforeHandoff == nil {
			return FirstTouchAcceptance{}, FirstTouchTransient, errors.New("missing handoff hook")
		}
		if err := msg.BeforeHandoff(context.Background()); err != nil {
			return FirstTouchAcceptance{}, FirstTouchTransient, err
		}
	}
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
		candidates:  map[uuid.UUID]*models.OutreachContactCandidate{},
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
	return h.enqueueWithAttempts(t, recipient, 0)
}

type commitFailStore struct {
	*dispatch.MemoryStore
	err error
}

func (s *commitFailStore) CommitReservationWithEvidence(context.Context, uuid.UUID, time.Time, dispatch.SendEvidence) error {
	return s.err
}

func TestFirstTouchMessageIDIsStableForLogicalSend(t *testing.T) {
	orgID := uuid.New()
	a := firstTouchMessageID(orgID, "email:draft:stable", "Sender@Confenge.COM.BR")
	b := firstTouchMessageID(orgID, "email:draft:stable", "sender@confenge.com.br")
	c := firstTouchMessageID(orgID, "email:draft:different", "sender@confenge.com.br")
	if a != b {
		t.Fatalf("same logical send produced different ids: %q != %q", a, b)
	}
	if a == c {
		t.Fatal("different message keys produced the same Message-ID")
	}
}

func (h *fastLaneHarness) enqueueWithAttempts(t *testing.T, recipient string, attempts int) (uuid.UUID, string) {
	t.Helper()
	draftID := uuid.New()
	key := dispatch.MessageKeyEmail(draftID)
	mailbox := h.mailbox
	if err := h.store.Enqueue(context.Background(), &dispatch.QueueItem{
		ID: uuid.New(), OrganizationID: h.orgID, EmailAccountID: &mailbox,
		Channel: dispatch.ChannelEmail, DraftID: draftID, MessageKey: key,
		RecipientRef: recipient, DueAt: h.clock.Now().Add(-time.Minute),
		Attempts: attempts, Status: dispatch.QueueQueued, CreatedAt: h.clock.Now(),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	accountID := uuid.New()
	tp := &models.OutreachTouchpoint{
		ID: uuid.New(), OrganizationID: h.orgID, AccountID: accountID, DraftID: &draftID,
		SourceRunID: "run-committed",
		State:       models.TouchpointQueued, Channel: models.OutreachChannelEmail,
		Purpose: "INITIAL", Ordinal: 1, Recipient: recipient,
		Subject: "Assunto aprovado", BodyText: "Corpo aprovado.",
	}
	tp.ContentHash = TouchpointBindingHash(tp)
	tp.ApprovedContentHash = tp.ContentHash
	h.repo.touchpoints[draftID] = tp
	account := &models.OutreachAccount{ID: accountID, OrganizationID: h.orgID, CNPJRoot: "11222333", TargetPartyRole: PartyRoleSupplier}
	qualification := testRootQualification(account.CNPJRoot, h.clock.Now().AddDate(-1, 0, 0))
	applyCommercialQualificationToAccount(account, &qualification, h.clock.Now())
	h.repo.accounts[accountID] = account
	return draftID, key
}

func (h *fastLaneHarness) claimAndReserve(t *testing.T) (*dispatch.QueueItem, *dispatch.Reservation) {
	t.Helper()
	item, err := h.svc.governor.ClaimNextQueued(context.Background())
	if err != nil || item == nil {
		t.Fatalf("claim: item=%+v err=%v", item, err)
	}
	res, err := h.svc.governor.TryReserve(context.Background(), dispatch.ReserveRequest{
		OrganizationID: item.OrganizationID, EmailAccountID: item.EmailAccountID,
		Channel: item.Channel, MessageKey: item.MessageKey, DraftID: &item.DraftID,
	})
	if err != nil || !res.Allowed || res.Reservation == nil {
		t.Fatalf("reserve: result=%+v err=%v", res, err)
	}
	return item, res.Reservation
}

func (h *fastLaneHarness) queueStatus(t *testing.T, key string) string {
	t.Helper()
	item, err := h.store.GetQueueByKey(context.Background(), key)
	if err != nil || item == nil {
		t.Fatalf("queue row %s missing: %v", key, err)
	}
	return item.Status
}

func (h *fastLaneHarness) queueLastError(t *testing.T, key string) string {
	t.Helper()
	item, err := h.store.GetQueueByKey(context.Background(), key)
	if err != nil || item == nil {
		t.Fatalf("queue row %s missing: %v", key, err)
	}
	return item.LastError
}

func (h *fastLaneHarness) assertNoSend(t *testing.T, key, wantStatus, wantReason string) {
	t.Helper()
	if h.transport.attempts != 0 {
		t.Fatalf("provider was called %d times", h.transport.attempts)
	}
	if _, sent, _ := h.store.GetSendByKey(context.Background(), key); sent {
		t.Fatal("ledger recorded a send that must not have left")
	}
	if got := h.queueStatus(t, key); got != wantStatus {
		t.Fatalf("queue status=%q want %q", got, wantStatus)
	}
	if wantReason != "" {
		if got := h.queueLastError(t, key); got != wantReason {
			t.Fatalf("queue reason=%q want %q", got, wantReason)
		}
	}
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

func TestFastLaneDoesNotRevokeAdmissionWhenQualificationTimeElapses(t *testing.T) {
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
	if h.transport.attempts != 1 {
		t.Fatalf("temporal expiry revoked durable admission: attempts=%d", h.transport.attempts)
	}
	if got := h.queueStatus(t, key); got != dispatch.QueueSent {
		t.Fatalf("admitted row should send despite temporal expiry, state=%q", got)
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

func TestFastLaneDoesNotRecheckFeedLineageAfterAdmission(t *testing.T) {
	for _, sourceRunID := range []string{"", "legacy-uncommitted"} {
		t.Run(firstNonEmpty(sourceRunID, "empty"), func(t *testing.T) {
			h := newFastLaneHarness(t, FirstTouchAccepted, nil)
			draftID, key := h.enqueue(t, "alvo@exemplo.com.br")
			h.repo.touchpoints[draftID].SourceRunID = sourceRunID

			progressed, err := h.svc.ProcessFastLaneOnce(context.Background())
			if err != nil || !progressed {
				t.Fatalf("uncommitted legacy row was not closed: progressed=%v err=%v", progressed, err)
			}
			if h.transport.attempts != 1 {
				t.Fatalf("lineage drift revoked durable admission: attempts=%d", h.transport.attempts)
			}
			if got := h.queueStatus(t, key); got != dispatch.QueueSent {
				t.Fatalf("admitted row should send after lineage drift, state=%q", got)
			}
		})
	}
}

// A producer replay after an accepted send must not reopen the terminal row.
func TestFastLaneNeverRequeuesAKeyTheLedgerAlreadyHolds(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchAccepted, nil)
	draftID, key := h.enqueue(t, "alvo@exemplo.com.br")
	if _, err := h.svc.ProcessFastLaneOnce(context.Background()); err != nil {
		t.Fatalf("initial send: %v", err)
	}

	mailbox := h.mailbox
	if err := h.store.Enqueue(context.Background(), &dispatch.QueueItem{
		OrganizationID: h.orgID, EmailAccountID: &mailbox, Channel: dispatch.ChannelEmail,
		DraftID: draftID, MessageKey: key, RecipientRef: "alvo@exemplo.com.br",
		DueAt: h.clock.Now().Add(-time.Minute), Status: dispatch.QueueQueued,
	}); err != nil {
		t.Fatalf("producer replay: %v", err)
	}

	progressed, err := h.svc.ProcessFastLaneOnce(context.Background())
	if err != nil {
		t.Fatalf("replay pass: %v", err)
	}
	if progressed {
		t.Fatal("accepted row became claimable after producer replay")
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
	if suppression := h.repo.suppressed["invalido@exemplo.com.br"]; suppression == nil || suppression.Source != models.DeliverabilityEventBounce {
		t.Fatalf("permanent rejection did not persist suppression: %+v", suppression)
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
	if got := h.queueStatus(t, key); got != dispatch.QueueQueued {
		t.Fatalf("transient lookup failure should defer durable work, got %q", got)
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

func TestFastLaneAcceptedCommitFailureParksAndNeverResends(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchAccepted, nil)
	draftID, key := h.enqueue(t, "alvo@exemplo.com.br")
	failing := &commitFailStore{MemoryStore: h.store, err: errors.New("ledger transaction unavailable")}
	cfg := dispatch.DefaultConfig()
	cfg.SendsPerHour = 100
	cfg.MinGap = 0
	cfg.RateMode = "fixed"
	h.svc.governor = dispatch.NewGovernor(cfg, failing, h.clock)

	progressed, err := h.svc.ProcessFastLaneOnce(context.Background())
	if !progressed || err == nil {
		t.Fatalf("accepted commit failure not surfaced: progressed=%v err=%v", progressed, err)
	}
	if h.transport.attempts != 1 || h.queueStatus(t, key) != dispatch.QueueAttempted {
		t.Fatalf("accepted failure was not parked: attempts=%d status=%s", h.transport.attempts, h.queueStatus(t, key))
	}
	reservation, _ := h.store.GetReservationByKey(context.Background(), key)
	if reservation == nil || reservation.AttemptedAt == nil || reservation.State != dispatch.StateFailed {
		t.Fatalf("handoff reservation not terminal: %+v", reservation)
	}
	h.clock.Advance(dispatch.DefaultLeaseTTL + time.Second)
	_, _ = h.store.ExpireStaleReservations(context.Background(), h.clock.Now())
	mailbox := h.mailbox
	_ = h.store.Enqueue(context.Background(), &dispatch.QueueItem{
		OrganizationID: h.orgID, EmailAccountID: &mailbox, Channel: dispatch.ChannelEmail,
		DraftID: draftID, MessageKey: key, RecipientRef: "alvo@exemplo.com.br", DueAt: h.clock.Now(),
	})
	progressed, err = h.svc.ProcessFastLaneOnce(context.Background())
	if err != nil || progressed || h.transport.attempts != 1 {
		t.Fatalf("parked acceptance was retried: progressed=%v attempts=%d err=%v", progressed, h.transport.attempts, err)
	}
}

func TestFastLaneSMTPDeadlineIsShorterThanReservationLease(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchAccepted, nil)
	h.enqueue(t, "alvo@exemplo.com.br")
	started := time.Now()
	if _, err := h.svc.ProcessFastLaneOnce(context.Background()); err != nil {
		t.Fatalf("process: %v", err)
	}
	if h.transport.deadline.IsZero() {
		t.Fatal("transport context had no deadline")
	}
	if budget := h.transport.deadline.Sub(started); budget <= 0 || budget >= dispatch.DefaultLeaseTTL {
		t.Fatalf("SMTP budget must be positive and shorter than lease: %s", budget)
	}
}

func TestFastLaneCrashRecoveryBeforeHandoffRequeues(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchAccepted, nil)
	_, key := h.enqueue(t, "alvo@exemplo.com.br")
	h.claimAndReserve(t) // worker crashes before the transport invokes the hook
	h.clock.Advance(dispatch.DefaultLeaseTTL + time.Second)

	progressed, err := h.svc.ProcessFastLaneOnce(context.Background())
	if err != nil || !progressed {
		t.Fatalf("pre-handoff recovery did not resume: progressed=%v err=%v", progressed, err)
	}
	if h.transport.attempts != 1 || h.queueStatus(t, key) != dispatch.QueueSent {
		t.Fatalf("pre-handoff recovery result attempts=%d status=%s", h.transport.attempts, h.queueStatus(t, key))
	}
}

func TestFastLaneCrashRecoveryAfterHandoffNeverRequeues(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchAccepted, nil)
	draftID, key := h.enqueue(t, "alvo@exemplo.com.br")
	item, reservation := h.claimAndReserve(t)
	if err := h.svc.governor.StartHandoff(context.Background(), reservation.ID, item.ID); err != nil {
		t.Fatalf("start handoff: %v", err)
	}
	h.clock.Advance(dispatch.DefaultLeaseTTL + time.Second)
	if expired, err := h.store.ExpireStaleReservations(context.Background(), h.clock.Now()); err != nil || expired != 0 {
		t.Fatalf("post-handoff reservation expired: count=%d err=%v", expired, err)
	}
	mailbox := h.mailbox
	_ = h.store.Enqueue(context.Background(), &dispatch.QueueItem{
		OrganizationID: h.orgID, EmailAccountID: &mailbox, Channel: dispatch.ChannelEmail,
		DraftID: draftID, MessageKey: key, RecipientRef: "alvo@exemplo.com.br", DueAt: h.clock.Now(),
	})
	progressed, err := h.svc.ProcessFastLaneOnce(context.Background())
	if err != nil || progressed {
		t.Fatalf("post-handoff row became work again: progressed=%v err=%v", progressed, err)
	}
	if h.transport.attempts != 0 || h.queueStatus(t, key) != dispatch.QueueAttempted {
		t.Fatalf("post-handoff recovery attempts=%d status=%s", h.transport.attempts, h.queueStatus(t, key))
	}
}

func TestFastLaneLiveAccountAndCandidateSafety(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*models.OutreachAccount, *models.OutreachContactCandidate)
	}{
		{"account_dnc", func(a *models.OutreachAccount, _ *models.OutreachContactCandidate) { a.DoNotContact = true }},
		{"account_blocked", func(a *models.OutreachAccount, _ *models.OutreachContactCandidate) { a.Blocked = true }},
		{"account_bounced", func(a *models.OutreachAccount, _ *models.OutreachContactCandidate) {
			a.QueueState = models.OutreachQueueBounced
		}},
		{"qualification_deactivated", func(a *models.OutreachAccount, _ *models.OutreachContactCandidate) {
			a.CommercialQualificationDeactivated = true
		}},
		{"buyer_role_conflict", func(a *models.OutreachAccount, _ *models.OutreachContactCandidate) {
			a.TargetPartyRole = "BUYER_CONFLICT"
		}},
		{"candidate_dnc", func(_ *models.OutreachAccount, c *models.OutreachContactCandidate) { c.DoNotContact = true }},
		{"candidate_bounced", func(_ *models.OutreachAccount, c *models.OutreachContactCandidate) { c.Bounced = true }},
		{"candidate_recipient_drift", func(_ *models.OutreachAccount, c *models.OutreachContactCandidate) { c.Email = "outro@exemplo.com.br" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newFastLaneHarness(t, FirstTouchAccepted, nil)
			draftID, key := h.enqueue(t, "alvo@exemplo.com.br")
			tp := h.repo.touchpoints[draftID]
			candidateID := uuid.New()
			tp.ContactCandidateID = &candidateID
			candidate := &models.OutreachContactCandidate{
				ID: candidateID, OrganizationID: h.orgID, AccountID: tp.AccountID,
				Email: "alvo@exemplo.com.br", VerificationStatus: models.OutreachVerifyVerified,
			}
			h.repo.candidates[candidateID] = candidate
			tt.mutate(h.repo.accounts[tp.AccountID], candidate)
			if _, err := h.svc.ProcessFastLaneOnce(context.Background()); err != nil {
				t.Fatalf("process: %v", err)
			}
			if h.transport.attempts != 0 || h.queueStatus(t, key) != dispatch.QueueCancelled {
				t.Fatalf("unsafe binding reached provider: attempts=%d status=%s", h.transport.attempts, h.queueStatus(t, key))
			}
		})
	}
}

func TestFastLaneRetryBudgetIsCheckedBeforeProvider(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchAccepted, nil)
	_, key := h.enqueueWithAttempts(t, "alvo@exemplo.com.br", fastLaneMaxAttempts)
	if _, err := h.svc.ProcessFastLaneOnce(context.Background()); err != nil {
		t.Fatalf("process: %v", err)
	}
	if h.transport.attempts != 0 || h.queueStatus(t, key) != dispatch.QueueFailed {
		t.Fatalf("exhausted row reached provider: attempts=%d status=%s", h.transport.attempts, h.queueStatus(t, key))
	}
}

func TestFastLaneFailedRowCannotBeReenqueued(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchPermanent, errors.New("550 permanent"))
	draftID, key := h.enqueue(t, "invalido@exemplo.com.br")
	if _, err := h.svc.ProcessFastLaneOnce(context.Background()); err != nil {
		t.Fatalf("process: %v", err)
	}
	mailbox := h.mailbox
	if err := h.store.Enqueue(context.Background(), &dispatch.QueueItem{
		OrganizationID: h.orgID, EmailAccountID: &mailbox, Channel: dispatch.ChannelEmail,
		DraftID: draftID, MessageKey: key, RecipientRef: "invalido@exemplo.com.br", DueAt: h.clock.Now(),
	}); err != nil {
		t.Fatalf("re-enqueue: %v", err)
	}
	if h.queueStatus(t, key) != dispatch.QueueFailed {
		t.Fatalf("failed row was reopened: %s", h.queueStatus(t, key))
	}
}

// A follow-up that reached the first-touch queue is cancelled before SMTP.
func TestFastLaneFollowUpPurposeOrdinalNotAuthorized(t *testing.T) {
	tests := []struct {
		name    string
		purpose string
		ordinal int
	}{
		{"follow_up_purpose", models.TouchpointPurposeFollowUp, 1},
		{"ordinal_two", models.TouchpointPurposeInitial, 2},
		{"follow_up_ordinal_two", models.TouchpointPurposeFollowUp, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newFastLaneHarness(t, FirstTouchAccepted, nil)
			draftID, key := h.enqueue(t, "followup@exemplo.com.br")
			tp := h.repo.touchpoints[draftID]
			tp.Purpose = tt.purpose
			tp.Ordinal = tt.ordinal
			tp.ContentHash = TouchpointBindingHash(tp)
			tp.ApprovedContentHash = tp.ContentHash

			progressed, err := h.svc.ProcessFastLaneOnce(context.Background())
			if err != nil || !progressed {
				t.Fatalf("follow-up row was not closed: progressed=%v err=%v", progressed, err)
			}
			h.assertNoSend(t, key, dispatch.QueueCancelled, FastLaneFollowUpNotAuthorized)
		})
	}
}

// Unsubscribe suppression is a first-class stop, not a DNC-flag-only check.
func TestFastLaneOptOutSuppressionBlocks(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchAccepted, nil)
	_, key := h.enqueue(t, "optout@exemplo.com.br")
	h.repo.suppressed["optout@exemplo.com.br"] = &models.SuppressedRecipient{
		OrganizationID: h.orgID, Email: "optout@exemplo.com.br",
		Source: models.DeliverabilityEventUnsubscribe, Reason: "one-click unsubscribe",
	}

	progressed, err := h.svc.ProcessFastLaneOnce(context.Background())
	if err != nil || !progressed {
		t.Fatalf("opt-out row was not closed: progressed=%v err=%v", progressed, err)
	}
	h.assertNoSend(t, key, dispatch.QueueCancelled, FastLaneRecipientOptOut)
}

// A complaint is its own stop. It must not be classified as a bounce.
func TestFastLaneComplaintSuppressionBlocks(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchAccepted, nil)
	_, key := h.enqueue(t, "complaint@exemplo.com.br")
	h.repo.suppressed["complaint@exemplo.com.br"] = &models.SuppressedRecipient{
		OrganizationID: h.orgID, Email: "complaint@exemplo.com.br",
		Source: models.DeliverabilityEventComplaint, Reason: "spam complaint",
	}

	progressed, err := h.svc.ProcessFastLaneOnce(context.Background())
	if err != nil || !progressed {
		t.Fatalf("complaint row was not closed: progressed=%v err=%v", progressed, err)
	}
	h.assertNoSend(t, key, dispatch.QueueCancelled, FastLaneRecipientComplaint)
	if suppression := h.repo.suppressed["complaint@exemplo.com.br"]; suppression == nil || suppression.Source != models.DeliverabilityEventComplaint {
		t.Fatalf("complaint was reclassified: %+v", suppression)
	}
	if strings.Contains(h.queueLastError(t, key), string(models.DeliverabilityEventBounce)) {
		t.Fatalf("complaint reason mentioned bounce: %q", h.queueLastError(t, key))
	}
}

// A replied account cannot be handed a new first touch.
func TestFastLaneRepliedQueueStateBlocks(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchAccepted, nil)
	draftID, key := h.enqueue(t, "replied@exemplo.com.br")
	h.repo.accounts[h.repo.touchpoints[draftID].AccountID].QueueState = models.OutreachQueueReplied

	progressed, err := h.svc.ProcessFastLaneOnce(context.Background())
	if err != nil || !progressed {
		t.Fatalf("replied row was not closed: progressed=%v err=%v", progressed, err)
	}
	h.assertNoSend(t, key, dispatch.QueueCancelled, "account_terminal:"+models.OutreachQueueReplied)
}

// A reply already recorded on the account blocks even if queue_state has not
// been projected yet.
func TestFastLaneRecordedReplyBlocks(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchAccepted, nil)
	draftID, key := h.enqueue(t, "replied@exemplo.com.br")
	tp := h.repo.touchpoints[draftID]
	replyDraft := uuid.New()
	reply := *tp
	reply.ID = uuid.New()
	reply.DraftID = &replyDraft
	reply.State = models.TouchpointReplied
	h.repo.touchpoints[replyDraft] = &reply

	progressed, err := h.svc.ProcessFastLaneOnce(context.Background())
	if err != nil || !progressed {
		t.Fatalf("recorded-reply row was not closed: progressed=%v err=%v", progressed, err)
	}
	h.assertNoSend(t, key, dispatch.QueueCancelled, FastLaneAccountReplied)
}

// An unrecognized provider outcome parks as unknown/ambiguous and is never retried.
func TestFastLaneUnrecognizedOutcomeParksAttempted(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchOutcome("provider-gobbledygook"), errors.New("unclassified smtp reply"))
	_, key := h.enqueue(t, "alvo@exemplo.com.br")

	if _, err := h.svc.ProcessFastLaneOnce(context.Background()); err != nil {
		t.Fatalf("unknown pass: %v", err)
	}
	if got := h.queueStatus(t, key); got != dispatch.QueueAttempted {
		t.Fatalf("unrecognized outcome should park as 'attempted', got %q", got)
	}
	if _, sent, _ := h.store.GetSendByKey(context.Background(), key); sent {
		t.Fatal("unrecognized outcome was recorded as an accepted send")
	}
	for i := 0; i < 3; i++ {
		_, _ = h.svc.ProcessFastLaneOnce(context.Background())
	}
	if h.transport.attempts != 1 {
		t.Fatalf("unrecognized outcome was resent: %d provider attempts", h.transport.attempts)
	}
	if strings.Contains(strings.ToLower(h.queueLastError(t, key)), "no_response") {
		t.Fatalf("unrecognized outcome was classified as NO_RESPONSE: %q", h.queueLastError(t, key))
	}
}

// A pause that arrives after capacity reservation still prevents the send.
func TestFastLanePauseAfterReservationDoesNotSend(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchAccepted, nil)
	_, key := h.enqueue(t, "alvo@exemplo.com.br")
	reads := 0
	h.repo.onTouchpointRead = func(*models.OutreachTouchpoint) {
		reads++
		if reads == 2 {
			if err := h.svc.governor.Pause(context.Background(), "ops_hold", nil); err != nil {
				t.Fatalf("pause: %v", err)
			}
		}
	}

	progressed, err := h.svc.ProcessFastLaneOnce(context.Background())
	if err != nil || !progressed {
		t.Fatalf("paused row was not closed: progressed=%v err=%v", progressed, err)
	}
	if reads < 2 {
		t.Fatal("pause hook never ran after reservation")
	}
	h.assertNoSend(t, key, dispatch.QueueQueued, "transport_blocked_after_reservation")
}

// A stop that appears after claim still refuses SMTP DATA.
func TestFastLaneStopBetweenQueueAndProviderRefusesData(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchAccepted, nil)
	_, key := h.enqueue(t, "alvo@exemplo.com.br")
	reads := 0
	h.repo.onTouchpointRead = func(tp *models.OutreachTouchpoint) {
		reads++
		if reads >= 3 && tp != nil {
			h.repo.suppressed[tp.Recipient] = &models.SuppressedRecipient{
				OrganizationID: h.orgID, Email: tp.Recipient,
				Source: models.DeliverabilityEventUnsubscribe, Reason: "opt-out after claim",
			}
		}
	}

	progressed, err := h.svc.ProcessFastLaneOnce(context.Background())
	if err != nil || !progressed {
		t.Fatalf("late-stop row was not closed: progressed=%v err=%v", progressed, err)
	}
	if reads < 3 {
		t.Fatal("BeforeHandoff never re-read the touchpoint")
	}
	h.assertNoSend(t, key, dispatch.QueueCancelled, FastLaneRecipientOptOut)
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
