package dispatch

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The queue used to move straight from 'queued' to 'sent' at hand-off, so the
// stored state asserted a provider acceptance nobody had observed. Production
// showed six rows in 'sent' with an empty provider-fact table and no touchpoint
// ever reaching SENT.

func TestAttemptedIsADistinctStateFromSent(t *testing.T) {
	if QueueAttempted == QueueSent {
		t.Fatal("a hand-off and a confirmed send must not be the same state")
	}
	if QueueAttempted != "attempted" {
		t.Fatalf("QueueAttempted=%q", QueueAttempted)
	}
}

func TestReEnqueueNeverRevivesAnAttemptedMessage(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	orgID := uuid.New()
	item := &QueueItem{
		OrganizationID: orgID,
		Channel:        ChannelEmail,
		DraftID:        uuid.New(),
		MessageKey:     "same-message",
		DueAt:          time.Now().UTC(),
	}
	if err := store.Enqueue(ctx, item); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateQueueStatus(ctx, item.ID, QueueAttempted, ""); err != nil {
		t.Fatal(err)
	}

	// The same message key arriving again must not become dispatchable: the
	// message is already with the transport, only its acceptance is unknown.
	again := &QueueItem{
		OrganizationID: orgID,
		Channel:        ChannelEmail,
		DraftID:        item.DraftID,
		MessageKey:     "same-message",
		DueAt:          time.Now().UTC(),
	}
	if err := store.Enqueue(ctx, again); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListQueueByStatus(ctx, QueueQueued, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("an attempted message was made dispatchable again: %+v", rows)
	}
	attempted, err := store.ListQueueByStatus(ctx, QueueAttempted, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempted) != 1 {
		t.Fatalf("attempted rows=%d want 1", len(attempted))
	}
}

func TestHandoffStartedLeaseExpiryDoesNotRequeue(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	clock := &FixedClock{T: time.Date(2026, 9, 2, 15, 0, 0, 0, time.UTC)}
	cfg := DefaultConfig()
	cfg.SendsPerHour = 100
	cfg.MinGap = 0
	cfg.RateMode = "fixed"
	cfg.WindowStart, cfg.WindowEnd, cfg.Timezone = "00:00", "23:59", "UTC"
	cfg.BusinessDaysOnly = false
	gov := NewGovernor(cfg, store, clock)
	orgID := uuid.New()
	mailbox := uuid.New()
	store.SetMailboxEnvelope(MailboxEnvelope{
		EmailAccountID: mailbox, OrganizationID: orgID, Ready: true,
		DailyCap: 50, MinGap: 0, HourlyCap: 100, Timezone: "UTC",
	})
	draftID := uuid.New()
	key := MessageKeyEmail(draftID)
	item := &QueueItem{
		ID: uuid.New(), OrganizationID: orgID, EmailAccountID: &mailbox,
		Channel: ChannelEmail, DraftID: draftID, MessageKey: key,
		RecipientRef: "alvo@exemplo.com.br", DueAt: clock.Now().Add(-time.Minute),
		Status: QueueQueued, CreatedAt: clock.Now(),
	}
	if err := store.Enqueue(ctx, item); err != nil {
		t.Fatal(err)
	}
	claimed, err := gov.ClaimNextQueued(ctx)
	if err != nil || claimed == nil {
		t.Fatalf("claim: item=%+v err=%v", claimed, err)
	}
	res, err := gov.TryReserve(ctx, ReserveRequest{
		OrganizationID: orgID, EmailAccountID: &mailbox,
		Channel: ChannelEmail, MessageKey: key, DraftID: &draftID,
	})
	if err != nil || !res.Allowed || res.Reservation == nil {
		t.Fatalf("reserve: %+v err=%v", res, err)
	}
	if err := gov.StartHandoff(ctx, res.Reservation.ID, claimed.ID); err != nil {
		t.Fatalf("start handoff: %v", err)
	}
	clock.Advance(DefaultLeaseTTL + time.Second)
	expired, err := store.ExpireStaleReservations(ctx, clock.Now())
	if err != nil || expired != 0 {
		t.Fatalf("handoff reservation expired: count=%d err=%v", expired, err)
	}
	if err := store.Enqueue(ctx, &QueueItem{
		OrganizationID: orgID, EmailAccountID: &mailbox, Channel: ChannelEmail,
		DraftID: draftID, MessageKey: key, RecipientRef: "alvo@exemplo.com.br",
		DueAt: clock.Now(),
	}); err != nil {
		t.Fatalf("re-enqueue: %v", err)
	}
	again, err := gov.ClaimNextQueued(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again != nil {
		t.Fatalf("a handoff-started row became claimable after lease expiry: %+v", again)
	}
	got, err := store.GetQueueByKey(ctx, key)
	if err != nil || got == nil {
		t.Fatalf("queue row missing: %v", err)
	}
	if got.Status != QueueAttempted {
		t.Fatalf("handoff row status=%q want attempted", got.Status)
	}
	reservation, err := store.GetReservationByKey(ctx, key)
	if err != nil || reservation == nil || reservation.AttemptedAt == nil {
		t.Fatalf("handoff fence missing: %+v err=%v", reservation, err)
	}
}

func TestListQueueByStatusOnlyReturnsTheRequestedState(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	orgID := uuid.New()
	for i, status := range []string{QueueAttempted, QueueSent, QueueCancelled, QueueAttempted} {
		item := &QueueItem{
			OrganizationID: orgID,
			Channel:        ChannelEmail,
			DraftID:        uuid.New(),
			MessageKey:     uuid.New().String(),
			DueAt:          time.Now().UTC().Add(time.Duration(i) * time.Second),
			CreatedAt:      time.Now().UTC().Add(time.Duration(i) * time.Second),
		}
		if err := store.Enqueue(ctx, item); err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateQueueStatus(ctx, item.ID, status, ""); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := store.ListQueueByStatus(ctx, QueueAttempted, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("attempted rows=%d want 2", len(rows))
	}
	for _, row := range rows {
		if row.Status != QueueAttempted {
			t.Fatalf("unexpected status %q", row.Status)
		}
	}
}
