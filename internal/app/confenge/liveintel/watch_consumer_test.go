package liveintel

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

func watchSubscription(orgID uuid.UUID, email, subject string) models.IntelWatchSubscription {
	return models.IntelWatchSubscription{
		ID: uuid.New(), OrganizationID: orgID, ContactEmail: email,
		IntentKind: models.IntelWatchIntentOpportunityChanged, SubjectKey: subject,
		Cadence: models.IntelWatchCadenceImmediate, ConsentProvenanceOK: true,
	}
}

func watchEvent(orgID uuid.UUID, subject, eventID string, payload map[string]string) OpportunityEvent {
	return OpportunityEvent{
		Schema: EventSchemaV1, EventID: eventID, EventType: EventOpportunityChanged,
		SubjectKey: subject, OrgID: orgID,
		OccurredAt: time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC),
		Payload:    payload,
	}
}

func deliveryKey(sub models.IntelWatchSubscription, event OpportunityEvent) models.IntelWatchDeliveryKey {
	return models.IntelWatchDeliveryKey{
		SubscriptionID: sub.ID, EventIdentity: event.EventID, ContentHash: event.ContentHash(),
	}
}

// Fake producer emits an event, the consumer resolves the subscription, claims
// the delivery, dispatches once and commits. Replaying the identical event is a
// no-op: only a DISPATCHED row means "nothing changed => nothing sent".
func TestWatchConsumerReplayNeverDuplicates(t *testing.T) {
	ctx := context.Background()
	orgID, subject := uuid.New(), "contrato-2026-0001"
	sub := watchSubscription(orgID, "watcher@example.com", subject)
	store := newFakeWatchStore(sub)
	dispatcher := &recordingDispatcher{}
	consumer := NewConsumer(store, dispatcher)

	event := watchEvent(orgID, subject, "evt-1", map[string]string{"deadline": "2026-10-01"})
	producer := &FakeProducer{events: []OpportunityEvent{event, event, event}}
	stream, err := producer.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	results := []HandleResult{}
	for emitted := range stream {
		result, err := consumer.HandleEvent(ctx, emitted)
		if err != nil {
			t.Fatalf("handle: %v", err)
		}
		results = append(results, result)
	}
	if len(results) != 3 {
		t.Fatalf("producer delivered %d events", len(results))
	}
	if results[0].Dispatched != 1 || results[0].Deduped != 0 {
		t.Fatalf("first delivery=%+v", results[0])
	}
	for i, replay := range results[1:] {
		if replay.Dispatched != 0 || replay.Deduped != 1 {
			t.Fatalf("replay %d duplicated a watch send: %+v", i+1, replay)
		}
	}
	if len(dispatcher.delivered()) != 1 {
		t.Fatalf("watcher was written to %d times: %v", len(dispatcher.delivered()), dispatcher.delivered())
	}
	if got := store.state(deliveryKey(sub, event)); got != models.IntelWatchDeliveryDispatched {
		t.Fatalf("ledger state=%q want DISPATCHED", got)
	}
}

// Same event identity, different content, is a real change and is delivered.
// Same content under a new event identity is also delivered: the producer
// asserted a new event, and the composite key deliberately includes both.
func TestWatchConsumerDeliversRealChanges(t *testing.T) {
	ctx := context.Background()
	orgID, subject := uuid.New(), "contrato-2026-0002"
	store := newFakeWatchStore(watchSubscription(orgID, "watcher@example.com", subject))
	dispatcher := &recordingDispatcher{}
	consumer := NewConsumer(store, dispatcher)

	first := watchEvent(orgID, subject, "evt-1", map[string]string{"deadline": "2026-10-01"})
	changed := watchEvent(orgID, subject, "evt-1", map[string]string{"deadline": "2026-11-15"})
	reIdentified := watchEvent(orgID, subject, "evt-2", map[string]string{"deadline": "2026-11-15"})

	for _, event := range []OpportunityEvent{first, changed, reIdentified} {
		result, err := consumer.HandleEvent(ctx, event)
		if err != nil {
			t.Fatal(err)
		}
		if result.Dispatched != 1 {
			t.Fatalf("event %s payload %v was not delivered: %+v", event.EventID, event.Payload, result)
		}
	}
	if len(dispatcher.delivered()) != 3 {
		t.Fatalf("expected three deliveries, got %v", dispatcher.delivered())
	}
	// Payload key order must not change the semantic hash.
	if first.ContentHash() == changed.ContentHash() {
		t.Fatal("a changed deadline produced the same content hash")
	}
	shuffled := watchEvent(orgID, subject, "evt-3", map[string]string{"b": "2", "a": "1"})
	reordered := watchEvent(orgID, subject, "evt-3", map[string]string{"a": "1", "b": "2"})
	if shuffled.ContentHash() != reordered.ContentHash() {
		t.Fatal("payload key order changed the semantic hash")
	}
}

// An unsubscribed watcher stops matching, so a later event about the same
// subject reaches nobody.
func TestWatchConsumerSkipsUnsubscribedWatcher(t *testing.T) {
	ctx := context.Background()
	orgID, subject := uuid.New(), "contrato-2026-0003"
	sub := watchSubscription(orgID, "leaving@example.com", subject)
	store := newFakeWatchStore(sub)
	dispatcher := &recordingDispatcher{}
	consumer := NewConsumer(store, dispatcher)

	if result, err := consumer.HandleEvent(ctx, watchEvent(orgID, subject, "evt-1", map[string]string{"status": "aberto"})); err != nil || result.Dispatched != 1 {
		t.Fatalf("first delivery result=%+v err=%v", result, err)
	}
	store.unsubscribe(sub.ID, time.Date(2026, 9, 2, 13, 0, 0, 0, time.UTC))

	result, err := consumer.HandleEvent(ctx, watchEvent(orgID, subject, "evt-2", map[string]string{"status": "prorrogado"}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Matched != 0 || result.Dispatched != 0 {
		t.Fatalf("an unsubscribed watcher still matched: %+v", result)
	}
	if len(dispatcher.delivered()) != 1 {
		t.Fatalf("unsubscribed watcher received mail: %v", dispatcher.delivered())
	}
}

// A different organization's watcher never sees the event.
func TestWatchConsumerDoesNotLeakAcrossOrganizations(t *testing.T) {
	ctx := context.Background()
	mine, theirs := uuid.New(), uuid.New()
	subject := "contrato-2026-0004"
	store := newFakeWatchStore(
		watchSubscription(mine, "mine@example.com", subject),
		watchSubscription(theirs, "theirs@example.com", subject),
	)
	dispatcher := &recordingDispatcher{}
	consumer := NewConsumer(store, dispatcher)

	result, err := consumer.HandleEvent(ctx, watchEvent(mine, subject, "evt-1", map[string]string{"status": "aberto"}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Matched != 1 || result.Dispatched != 1 {
		t.Fatalf("cross-organization match: %+v", result)
	}
	if got := dispatcher.delivered(); len(got) != 1 || got[0] != "mine@example.com|evt-1" {
		t.Fatalf("delivered to the wrong organization: %v", got)
	}
}

// A malformed event is discarded without touching the ledger.
func TestWatchConsumerDiscardsMalformedEvents(t *testing.T) {
	ctx := context.Background()
	orgID, subject := uuid.New(), "contrato-2026-0005"
	store := newFakeWatchStore(watchSubscription(orgID, "watcher@example.com", subject))
	consumer := NewConsumer(store, &recordingDispatcher{})

	valid := watchEvent(orgID, subject, "evt-1", map[string]string{"status": "aberto"})
	cases := map[string]func(*OpportunityEvent){
		ReasonEventSchemaMismatch: func(e *OpportunityEvent) { e.Schema = "other/1.0" },
		ReasonEventIDMissing:      func(e *OpportunityEvent) { e.EventID = " " },
		ReasonEventTypeUnknown:    func(e *OpportunityEvent) { e.EventType = "GUESS" },
		ReasonEventOrgMissing:     func(e *OpportunityEvent) { e.OrgID = uuid.Nil },
		ReasonEventSubjectMissing: func(e *OpportunityEvent) { e.SubjectKey = "" },
		ReasonEventPayloadEmpty:   func(e *OpportunityEvent) { e.Payload = nil },
	}
	for reason, mutate := range cases {
		event := valid
		mutate(&event)
		result, err := consumer.HandleEvent(ctx, event)
		if err != nil {
			t.Fatalf("%s: a malformed event must not error: %v", reason, err)
		}
		if result.Skipped != reason {
			t.Fatalf("skipped=%q want %q", result.Skipped, reason)
		}
	}
	if store.claims != 0 {
		t.Fatalf("malformed events claimed %d deliveries", store.claims)
	}
	if store.rows() != 0 {
		t.Fatalf("malformed events wrote %d ledger rows", store.rows())
	}
}

// With no dispatcher wired there is no transport, so nothing may be recorded as
// delivered. The claim is handed straight back and the notification survives
// until a real dispatcher exists.
func TestWatchConsumerWithoutADispatcherKeepsDeliveriesDeliverable(t *testing.T) {
	ctx := context.Background()
	orgID, subject := uuid.New(), "contrato-2026-0006"
	sub := watchSubscription(orgID, "watcher@example.com", subject)
	store := newFakeWatchStore(sub)
	consumer := NewConsumer(store, nil)

	event := watchEvent(orgID, subject, "evt-1", map[string]string{"status": "aberto"})
	// Well past the retry budget: a delivery nobody could attempt must never
	// spend attempts and exhaust itself into a terminal failure.
	for i := 0; i < watchMaxAttempts+2; i++ {
		result, err := consumer.HandleEvent(ctx, event)
		if err != nil || result.Matched != 1 || result.Dispatched != 0 || result.Undelivered != 1 ||
			result.Deduped != 0 || result.Failed != 0 {
			t.Fatalf("pass %d=%+v err=%v", i+1, result, err)
		}
	}
	if got := store.state(deliveryKey(sub, event)); got != "" && got != models.IntelWatchDeliveryPending {
		t.Fatalf("an unattempted delivery settled as %q", got)
	}
	if store.claims != 0 {
		t.Fatalf("a delivery with no transport spent %d claims", store.claims)
	}

	// Wiring the composer later must still deliver this notification.
	dispatcher := &recordingDispatcher{}
	wired := NewConsumer(store, dispatcher)
	second, err := wired.HandleEvent(ctx, event)
	if err != nil || second.Dispatched != 1 {
		t.Fatalf("a notification was lost while no dispatcher existed: %+v err=%v", second, err)
	}
}

// A transient failure before any handoff must NOT be deduped away: the watcher
// was never written to, so re-delivering the same event still delivers it.
func TestWatchConsumerTransientFailureIsRetriedNotLost(t *testing.T) {
	ctx := context.Background()
	orgID, subject := uuid.New(), "contrato-2026-0007"
	sub := watchSubscription(orgID, "watcher@example.com", subject)
	store := newFakeWatchStore(sub)
	dispatcher := &recordingDispatcher{outcomes: []dispatchStep{
		{outcome: WatchTransient, err: errDispatcherDown, fence: false},
	}}
	consumer := NewConsumer(store, dispatcher)
	event := watchEvent(orgID, subject, "evt-1", map[string]string{"status": "aberto"})

	first, err := consumer.HandleEvent(ctx, event)
	if err == nil {
		t.Fatal("an undelivered notification must surface as an error")
	}
	if first.Retryable != 1 || first.Dispatched != 0 || first.Deduped != 0 {
		t.Fatalf("first=%+v", first)
	}
	if got := store.state(deliveryKey(sub, event)); got != models.IntelWatchDeliveryPending {
		t.Fatalf("a failed pre-handoff attempt settled as %q", got)
	}

	second, err := consumer.HandleEvent(ctx, event)
	if err != nil || second.Dispatched != 1 {
		t.Fatalf("the retry did not deliver: %+v err=%v", second, err)
	}
	if len(dispatcher.delivered()) != 1 {
		t.Fatalf("retry duplicated the notification: %v", dispatcher.delivered())
	}
	if got := store.state(deliveryKey(sub, event)); got != models.IntelWatchDeliveryDispatched {
		t.Fatalf("ledger state=%q want DISPATCHED", got)
	}
}

// An ambiguous outcome is parked, never re-dispatched. A duplicate watch mail
// is worse than a delayed one.
func TestWatchConsumerAmbiguousOutcomeIsParkedNotResent(t *testing.T) {
	ctx := context.Background()
	orgID, subject := uuid.New(), "contrato-2026-0008"
	sub := watchSubscription(orgID, "watcher@example.com", subject)
	store := newFakeWatchStore(sub)
	dispatcher := &recordingDispatcher{outcomes: []dispatchStep{
		{outcome: WatchAmbiguous, err: errDispatcherDown, fence: true},
	}}
	consumer := NewConsumer(store, dispatcher)
	event := watchEvent(orgID, subject, "evt-1", map[string]string{"status": "aberto"})

	first, err := consumer.HandleEvent(ctx, event)
	if err != nil {
		t.Fatalf("parking is not an error: %v", err)
	}
	if first.Parked != 1 || first.Dispatched != 0 || first.Retryable != 0 {
		t.Fatalf("first=%+v", first)
	}
	if got := store.state(deliveryKey(sub, event)); got != models.IntelWatchDeliveryAmbiguous {
		t.Fatalf("ledger state=%q want AMBIGUOUS", got)
	}

	replay, err := consumer.HandleEvent(ctx, event)
	if err != nil || replay.Parked != 1 || replay.Dispatched != 0 {
		t.Fatalf("a parked delivery was retried: %+v err=%v", replay, err)
	}
	if dispatcher.attempts != 1 {
		t.Fatalf("the dispatcher was called %d times for a parked delivery", dispatcher.attempts)
	}
}

// A transient failure AFTER the handoff fence cannot be proven undelivered, so
// it parks instead of retrying.
func TestWatchConsumerTransientAfterHandoffIsParked(t *testing.T) {
	ctx := context.Background()
	orgID, subject := uuid.New(), "contrato-2026-0009"
	sub := watchSubscription(orgID, "watcher@example.com", subject)
	store := newFakeWatchStore(sub)
	dispatcher := &recordingDispatcher{outcomes: []dispatchStep{
		{outcome: WatchTransient, err: errDispatcherDown, fence: true},
	}}
	consumer := NewConsumer(store, dispatcher)
	event := watchEvent(orgID, subject, "evt-1", map[string]string{"status": "aberto"})

	result, err := consumer.HandleEvent(ctx, event)
	if err != nil {
		t.Fatalf("parking is not an error: %v", err)
	}
	if result.Parked != 1 || result.Retryable != 0 {
		t.Fatalf("a fenced transient failure was treated as retryable: %+v", result)
	}
	if got := store.state(deliveryKey(sub, event)); got != models.IntelWatchDeliveryAmbiguous {
		t.Fatalf("ledger state=%q want AMBIGUOUS", got)
	}
}

// A permanent failure is terminal and is never retried.
func TestWatchConsumerPermanentFailureIsTerminal(t *testing.T) {
	ctx := context.Background()
	orgID, subject := uuid.New(), "contrato-2026-0010"
	sub := watchSubscription(orgID, "watcher@example.com", subject)
	store := newFakeWatchStore(sub)
	dispatcher := &recordingDispatcher{outcomes: []dispatchStep{
		{outcome: WatchPermanent, err: errDispatcherDown, fence: false},
	}}
	consumer := NewConsumer(store, dispatcher)
	event := watchEvent(orgID, subject, "evt-1", map[string]string{"status": "aberto"})

	if result, err := consumer.HandleEvent(ctx, event); err != nil || result.Failed != 1 {
		t.Fatalf("first=%+v err=%v", result, err)
	}
	if got := store.state(deliveryKey(sub, event)); got != models.IntelWatchDeliveryFailed {
		t.Fatalf("ledger state=%q want FAILED", got)
	}
	replay, err := consumer.HandleEvent(ctx, event)
	if err != nil || replay.Failed != 1 || replay.Dispatched != 0 {
		t.Fatalf("a terminal failure was retried: %+v err=%v", replay, err)
	}
	if dispatcher.attempts != 1 {
		t.Fatalf("the dispatcher was called %d times", dispatcher.attempts)
	}
}

// Transient failures are bounded. Once the attempt budget is spent the delivery
// becomes terminally FAILED instead of a row that silently never delivers.
func TestWatchConsumerTransientRetriesAreBounded(t *testing.T) {
	ctx := context.Background()
	orgID, subject := uuid.New(), "contrato-2026-0011"
	sub := watchSubscription(orgID, "watcher@example.com", subject)
	store := newFakeWatchStore(sub)
	steps := make([]dispatchStep, watchMaxAttempts)
	for i := range steps {
		steps[i] = dispatchStep{outcome: WatchTransient, err: errDispatcherDown}
	}
	dispatcher := &recordingDispatcher{outcomes: steps}
	consumer := NewConsumer(store, dispatcher)
	event := watchEvent(orgID, subject, "evt-1", map[string]string{"status": "aberto"})

	for i := 0; i < watchMaxAttempts; i++ {
		if _, err := consumer.HandleEvent(ctx, event); err == nil {
			t.Fatalf("attempt %d should have surfaced a failure", i+1)
		}
	}
	result, err := consumer.HandleEvent(ctx, event)
	if err != nil {
		t.Fatalf("exhaustion is terminal, not an error: %v", err)
	}
	if result.Failed != 1 {
		t.Fatalf("an exhausted delivery was not made terminal: %+v", result)
	}
	if got := store.state(deliveryKey(sub, event)); got != models.IntelWatchDeliveryFailed {
		t.Fatalf("ledger state=%q want FAILED", got)
	}
	if dispatcher.attempts != watchMaxAttempts {
		t.Fatalf("the dispatcher was called %d times, budget is %d", dispatcher.attempts, watchMaxAttempts)
	}
}

// Two workers racing the identical (subscription, event identity, content hash)
// must produce one delivery. The ledger row, not an in-process lock, is the
// arbiter: exactly one claim is granted.
func TestWatchConsumerConcurrentDeliveriesDispatchOnce(t *testing.T) {
	ctx := context.Background()
	orgID, subject := uuid.New(), "contrato-2026-0012"
	sub := watchSubscription(orgID, "watcher@example.com", subject)
	store := newFakeWatchStore(sub)
	dispatcher := &recordingDispatcher{}
	event := watchEvent(orgID, subject, "evt-1", map[string]string{"status": "aberto"})

	const racers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	dispatched, refused := 0, 0
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Every racer is its own consumer, as two processes would be.
			consumer := NewConsumer(store, dispatcher)
			<-start
			result, _ := consumer.HandleEvent(ctx, event)
			mu.Lock()
			dispatched += result.Dispatched
			refused += result.Contended + result.Deduped + result.Parked + result.Failed
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()

	if dispatched != 1 {
		t.Fatalf("%d racers produced %d deliveries", racers, dispatched)
	}
	if len(dispatcher.delivered()) != 1 {
		t.Fatalf("the watcher was written to %d times: %v", len(dispatcher.delivered()), dispatcher.delivered())
	}
	if refused != racers-1 {
		t.Fatalf("racers=%d dispatched=%d refused=%d", racers, dispatched, refused)
	}
	if dispatcher.attempts != 1 {
		t.Fatalf("%d racers reached the dispatcher %d times", racers, dispatcher.attempts)
	}
	if got := store.state(deliveryKey(sub, event)); got != models.IntelWatchDeliveryDispatched {
		t.Fatalf("ledger state=%q want DISPATCHED", got)
	}
}

// One watcher's dispatch failure must not stop the other watchers of the same
// subject in the same batch from being served.
func TestWatchConsumerOneFailingSubscriberDoesNotBlockTheOthers(t *testing.T) {
	ctx := context.Background()
	orgID, subject := uuid.New(), "contrato-2026-0013"
	first := watchSubscription(orgID, "first@example.com", subject)
	broken := watchSubscription(orgID, "broken@example.com", subject)
	last := watchSubscription(orgID, "last@example.com", subject)
	store := newFakeWatchStore(first, broken, last)
	dispatcher := &recordingDispatcher{perEmail: map[string]dispatchStep{
		"broken@example.com": {outcome: WatchTransient, err: errDispatcherDown},
	}}
	consumer := NewConsumer(store, dispatcher)
	event := watchEvent(orgID, subject, "evt-1", map[string]string{"status": "aberto"})

	result, err := consumer.HandleEvent(ctx, event)
	if err == nil {
		t.Fatal("the failing watcher must surface")
	}
	if result.Matched != 3 || result.Dispatched != 2 || result.Retryable != 1 {
		t.Fatalf("head-of-line blocking: %+v", result)
	}
	delivered := dispatcher.delivered()
	if len(delivered) != 2 {
		t.Fatalf("healthy watchers were not served: %v", delivered)
	}
	// The failing watcher is still deliverable; re-delivering the event serves
	// it without writing to the two that already received it.
	dispatcher.perEmail = nil
	retry, err := consumer.HandleEvent(ctx, event)
	if err != nil || retry.Dispatched != 1 || retry.Deduped != 2 {
		t.Fatalf("retry=%+v err=%v", retry, err)
	}
	if len(dispatcher.delivered()) != 3 {
		t.Fatalf("the recovered watcher was not served exactly once: %v", dispatcher.delivered())
	}
}

// A worker that dies between taking the fence and settling leaves an IN_FLIGHT
// row. That row is never re-claimed; the reconciler turns it into an explicit
// parked state, and a replay still refuses to re-dispatch it.
func TestWatchConsumerAbandonedFenceIsParkedNeverResent(t *testing.T) {
	ctx := context.Background()
	orgID, subject := uuid.New(), "contrato-2026-0014"
	sub := watchSubscription(orgID, "watcher@example.com", subject)
	store := newFakeWatchStore(sub)
	consumer := NewConsumer(store, &recordingDispatcher{})
	event := watchEvent(orgID, subject, "evt-1", map[string]string{"status": "aberto"})
	key := deliveryKey(sub, event)

	// Simulate the crash: claim, fence, then vanish.
	claim, err := store.ClaimDelivery(ctx, key, time.Now().UTC(), time.Minute, watchMaxAttempts)
	if err != nil || !claim.Granted {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	if err := store.MarkDeliveryInFlight(ctx, key, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	// While the lease is live the row belongs to the (now dead) worker.
	held, err := consumer.HandleEvent(ctx, event)
	if err != nil || held.Dispatched != 0 || held.Contended != 1 {
		t.Fatalf("a fenced delivery was re-dispatched: %+v err=%v", held, err)
	}

	// The lease lapses. A fenced row is still never re-claimed: it parks.
	store.expireLease(key, time.Now().UTC().Add(-time.Minute))
	replay, err := consumer.HandleEvent(ctx, event)
	if err != nil || replay.Parked != 1 || replay.Dispatched != 0 {
		t.Fatalf("an abandoned fenced delivery was re-dispatched: %+v err=%v", replay, err)
	}
	swept, err := consumer.ExpireStaleHandoffs(ctx)
	if err != nil || swept != 1 {
		t.Fatalf("swept=%d err=%v", swept, err)
	}
	if got := store.state(key); got != models.IntelWatchDeliveryAmbiguous {
		t.Fatalf("ledger state=%q want AMBIGUOUS", got)
	}
	after, err := consumer.HandleEvent(ctx, event)
	if err != nil || after.Parked != 1 || after.Dispatched != 0 {
		t.Fatalf("a swept delivery was re-dispatched: %+v err=%v", after, err)
	}
}

// An abandoned PENDING claim (the worker died BEFORE the fence, so nothing was
// handed over) is safely reclaimed once its lease lapses.
func TestWatchConsumerAbandonedPreHandoffClaimIsReclaimed(t *testing.T) {
	ctx := context.Background()
	orgID, subject := uuid.New(), "contrato-2026-0015"
	sub := watchSubscription(orgID, "watcher@example.com", subject)
	store := newFakeWatchStore(sub)
	dispatcher := &recordingDispatcher{}
	consumer := NewConsumer(store, dispatcher)
	event := watchEvent(orgID, subject, "evt-1", map[string]string{"status": "aberto"})
	key := deliveryKey(sub, event)

	claim, err := store.ClaimDelivery(ctx, key, time.Now().UTC(), time.Minute, watchMaxAttempts)
	if err != nil || !claim.Granted {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	held, err := consumer.HandleEvent(ctx, event)
	if err != nil || held.Contended != 1 || held.Dispatched != 0 {
		t.Fatalf("a live claim was stolen: %+v err=%v", held, err)
	}
	store.expireLease(key, time.Now().UTC().Add(-time.Minute))

	taken, err := consumer.HandleEvent(ctx, event)
	if err != nil || taken.Dispatched != 1 {
		t.Fatalf("an abandoned pre-handoff claim was not recovered: %+v err=%v", taken, err)
	}
	if len(dispatcher.delivered()) != 1 {
		t.Fatalf("recovery duplicated the notification: %v", dispatcher.delivered())
	}
}

// A store failure on the subscription lookup is a retryable error, not a
// silently empty batch.
func TestWatchConsumerSurfacesStoreFailures(t *testing.T) {
	ctx := context.Background()
	orgID, subject := uuid.New(), "contrato-2026-0016"
	store := newFakeWatchStore(watchSubscription(orgID, "watcher@example.com", subject))
	store.listErr = errDispatcherDown
	consumer := NewConsumer(store, &recordingDispatcher{})
	if _, err := consumer.HandleEvent(ctx, watchEvent(orgID, subject, "evt-1", map[string]string{"a": "b"})); err == nil {
		t.Fatal("a failing subscription lookup must surface")
	}

	claiming := newFakeWatchStore(watchSubscription(orgID, "watcher@example.com", subject))
	claiming.claimErr = errDispatcherDown
	if _, err := NewConsumer(claiming, &recordingDispatcher{}).HandleEvent(ctx,
		watchEvent(orgID, subject, "evt-1", map[string]string{"a": "b"})); err == nil {
		t.Fatal("a failing claim must surface")
	}
}

// A consumer with no store is a wiring error, not a silent no-op.
func TestWatchConsumerRequiresAStore(t *testing.T) {
	consumer := NewConsumer(nil, nil)
	if _, err := consumer.HandleEvent(context.Background(), OpportunityEvent{}); err == nil {
		t.Fatal("a consumer without a store must refuse")
	}
	if _, err := consumer.ExpireStaleHandoffs(context.Background()); err == nil {
		t.Fatal("sweeping without a store must refuse")
	}
}
