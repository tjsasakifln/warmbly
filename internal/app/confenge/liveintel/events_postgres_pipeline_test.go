package liveintel

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The new producer slotted into the EXISTING proven pipeline.
//
// These do not re-prove compose, dispatch, dedup or parking; those already have
// their own suites against the fixture producer. What they prove is that
// swapping the source changes none of it.
//
// C: a real-shaped event through the inbox reaches the consumer and is
//    delivered exactly once.
// D: a transient dispatch failure before the fence stays deliverable and the
//    next pass over the SAME inbox delivers it.
// E: an ambiguous outcome parks and is never auto-retried.
// F: replaying the same inbox row twice delivers once.

// seedInbox stores one real-shaped envelope the way the webhook would.
func seedInbox(t *testing.T, inbox *fakeInbox, orgID uuid.UUID, eventID, subject string) {
	t.Helper()
	row := InboxRowFromEvent(orgID, OpportunityEvent{
		Schema: EventSchemaV1, EventID: eventID, EventType: EventDeadlineChanged,
		SubjectKey: subject, OccurredAt: time.Now().UTC(),
		Payload: map[string]string{"headline": "prazo alterado", "public_url": "https://example.gov/edital"},
	}, time.Now().UTC())
	if _, err := inbox.AppendOpportunityEvent(context.Background(), row); err != nil {
		t.Fatal(err)
	}
}

// Proof C. Webhook envelope to durable row to Subscribe to the consumer's
// ledger, delivered once.
func TestInboxProducerDrivesTheExistingConsumerToDelivery(t *testing.T) {
	orgID := uuid.New()
	subject := "opportunity:pregao-2026-0042"
	store := newFakeWatchStore(watchSubscription(orgID, "ana@example.com", subject))
	dispatcher := &recordingDispatcher{}
	inbox := newFakeInbox()
	seedInbox(t, inbox, orgID, "evt-real-1", subject)

	worker := NewWatchReclaimWorker(NewConsumer(store, dispatcher), NewPostgresEventProducer(inbox), time.Minute)
	report, err := worker.ReclaimOnce(context.Background())
	if err != nil {
		t.Fatalf("reclaim pass: %v", err)
	}
	if report.EventsSeen != 1 || report.Dispatched != 1 {
		t.Fatalf("report = %+v, want one event seen and one dispatched", report)
	}
	if got := dispatcher.delivered(); len(got) != 1 || got[0] != "ana@example.com|evt-real-1" {
		t.Fatalf("delivered = %v", got)
	}
}

// Proof F. A second pass over the same inbox row delivers nothing new: the
// ledger's dedup key refuses it, exactly as it does for the fixture producer.
func TestInboxProducerReplayIsDedupedNotResent(t *testing.T) {
	orgID := uuid.New()
	subject := "company:acme-holdings"
	store := newFakeWatchStore(watchSubscription(orgID, "ana@example.com", subject))
	dispatcher := &recordingDispatcher{}
	inbox := newFakeInbox()
	seedInbox(t, inbox, orgID, "evt-real-1", subject)

	worker := NewWatchReclaimWorker(NewConsumer(store, dispatcher), NewPostgresEventProducer(inbox), time.Minute)
	if _, err := worker.ReclaimOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The emit lease is what stops a pass inside the lease from re-offering the
	// row; clearing it forces the harder case, where the event IS re-offered.
	inbox.lease = map[string]time.Time{}
	report, err := worker.ReclaimOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Dispatched != 0 || report.Deduped != 1 {
		t.Fatalf("second pass = %+v, want zero dispatched and one deduped", report)
	}
	if len(dispatcher.delivered()) != 1 {
		t.Fatalf("watcher was written to %d times", len(dispatcher.delivered()))
	}
}

// Proof D. A transient failure before the fence leaves the delivery claimable,
// and the next pass over the SAME durable inbox delivers it. This is the
// property the inbox exists for: against the old file source it held because
// the file could be re-read, and it now holds because the row is still there.
func TestInboxProducerRecoversATransientFailureOnTheNextPass(t *testing.T) {
	orgID := uuid.New()
	subject := "company:acme-holdings"
	store := newFakeWatchStore(watchSubscription(orgID, "ana@example.com", subject))
	dispatcher := &recordingDispatcher{outcomes: []dispatchStep{
		{outcome: WatchTransient, err: errors.New("451 try again"), fence: false},
	}}
	inbox := newFakeInbox()
	seedInbox(t, inbox, orgID, "evt-real-1", subject)

	worker := NewWatchReclaimWorker(NewConsumer(store, dispatcher), NewPostgresEventProducer(inbox), time.Minute)
	report, _ := worker.ReclaimOnce(context.Background())
	if report.Retryable != 1 || report.Dispatched != 0 {
		t.Fatalf("first pass = %+v, want one retryable", report)
	}
	if len(dispatcher.delivered()) != 0 {
		t.Fatalf("a transient failure was recorded as delivered")
	}
	inbox.lease = map[string]time.Time{}
	report, err := worker.ReclaimOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Dispatched != 1 {
		t.Fatalf("second pass = %+v, want the recovered delivery", report)
	}
	if len(dispatcher.delivered()) != 1 {
		t.Fatalf("delivered = %v", dispatcher.delivered())
	}
}

// Proof E. An ambiguous outcome parks and is never auto-retried, even though
// the inbox keeps re-offering the event. A duplicate watch mail is worse than
// a late one.
func TestInboxProducerAmbiguousOutcomeStaysParkedAcrossReplays(t *testing.T) {
	orgID := uuid.New()
	subject := "company:acme-holdings"
	store := newFakeWatchStore(watchSubscription(orgID, "ana@example.com", subject))
	dispatcher := &recordingDispatcher{outcomes: []dispatchStep{
		{outcome: WatchAmbiguous, err: errors.New("connection reset after DATA"), fence: true},
	}}
	inbox := newFakeInbox()
	seedInbox(t, inbox, orgID, "evt-real-1", subject)

	worker := NewWatchReclaimWorker(NewConsumer(store, dispatcher), NewPostgresEventProducer(inbox), time.Minute)
	if report, _ := worker.ReclaimOnce(context.Background()); report.Parked != 1 {
		t.Fatalf("first pass = %+v, want one parked", report)
	}
	for i := 0; i < 3; i++ {
		inbox.lease = map[string]time.Time{}
		report, err := worker.ReclaimOnce(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if report.Dispatched != 0 {
			t.Fatalf("a parked delivery was retried on replay %d: %+v", i, report)
		}
	}
	if len(dispatcher.delivered()) != 0 {
		t.Fatalf("a parked delivery was sent: %v", dispatcher.delivered())
	}
}

// Proof H (producer half). A dormant lane with no inbox produces a nil
// producer, and the reclaim worker built on it runs and does nothing rather
// than failing a boot.
func TestDormantInboxProducerRunsAndDoesNothing(t *testing.T) {
	store := newFakeWatchStore()
	worker := NewWatchReclaimWorker(NewConsumer(store, &recordingDispatcher{}), nil, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	worker.Run(ctx)
	if store.rows() != 0 {
		t.Fatalf("a dormant lane touched %d ledger rows", store.rows())
	}
}

// A store failure surfaces as a subscribe error, not as an empty pass that
// looks like "nothing was owed".
func TestInboxProducerSurfacesAStoreFailure(t *testing.T) {
	inbox := newFakeInbox()
	inbox.failNext = errors.New("connection refused")
	if _, err := NewPostgresEventProducer(inbox).Subscribe(context.Background()); err == nil {
		t.Fatal("an unreadable inbox subscribed cleanly")
	}
}
