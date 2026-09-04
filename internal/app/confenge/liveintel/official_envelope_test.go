package liveintel

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

func officialEvent(orgID uuid.UUID, subject, eventID string) OpportunityEvent {
	event := OpportunityEvent{
		Schema: EventSchemaV1, EventID: eventID, EventType: EventNewOpportunity,
		SubjectKey: subject, OrgID: orgID,
		OccurredAt: time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC),
		Payload:    map[string]string{"headline": "edital publicado", "public_url": "https://example.gov/e"},
	}
	event.SourceRunID = "run-official-1"
	event.Provenance = "extra-cli:CONFENGE_LIVE_INTELLIGENCE/1.0"
	event.AsOf = event.OccurredAt.Format(time.RFC3339)
	event.ClaimedContentHash = event.ContentHash()
	event.PublicDecision = PublicDecisionPublicSafe
	event.Freshness = FreshnessFresh
	event.SchemaVersion = EventSchemaV1
	return event
}

func TestAdmitOfficialOpportunityEventAcceptsProducerBinding(t *testing.T) {
	orgID := uuid.New()
	event := OpportunityEvent{
		Schema: EventSchemaV1, EventID: "evt-binding", EventType: EventDeadlineChanged,
		SubjectKey: "opportunity:pregao-1", OrgID: orgID,
		OccurredAt: time.Now().UTC(),
		Payload:    map[string]string{"prazo": "2026-10-01"},
	}
	got, reason := AdmitOfficialOpportunityEvent(event)
	if reason != "" {
		t.Fatalf("current extra-cli webhook binding was rejected: %s", reason)
	}
	if got.EventID != event.EventID {
		t.Fatalf("event_id mutated")
	}
}

func TestAdmitOfficialOpportunityEventFailClosed(t *testing.T) {
	orgID := uuid.New()
	base := officialEvent(orgID, "company:acme", "evt-1")
	cases := []struct {
		name   string
		mutate func(*OpportunityEvent)
		reason string
	}{
		{"stale", func(e *OpportunityEvent) { e.Freshness = FreshnessStale }, ReasonEventStale},
		{"not_public_safe", func(e *OpportunityEvent) { e.PublicDecision = PublicDecisionNotPublicSafe }, ReasonEventNotPublicSafe},
		{"rejected", func(e *OpportunityEvent) { e.Status = "rejected" }, ReasonEventRejected},
		{"unknown_status", func(e *OpportunityEvent) { e.Status = "UNKNOWN" }, ReasonEventUnknownStatus},
		{"hash_mismatch", func(e *OpportunityEvent) { e.ClaimedContentHash = "deadbeef" }, ReasonEventHashMismatch},
		{"schema_drift", func(e *OpportunityEvent) { e.SchemaVersion = "CONFENGE_OPPORTUNITY_EVENT/0.9" }, ReasonEventSchemaDrift},
		{"freshness_invalid", func(e *OpportunityEvent) { e.Freshness = "MAYBE" }, ReasonEventFreshnessInvalid},
		{"schema_mismatch", func(e *OpportunityEvent) { e.Schema = OfficialLiveIntelligenceSchema }, ReasonEventSchemaMismatch},
		{"empty_payload", func(e *OpportunityEvent) { e.Payload = map[string]string{} }, ReasonEventPayloadEmpty},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event := base
			event.Payload = map[string]string{"headline": "edital publicado", "public_url": "https://example.gov/e"}
			event.ClaimedContentHash = event.ContentHash()
			tc.mutate(&event)
			if _, reason := AdmitOfficialOpportunityEvent(event); reason != tc.reason {
				t.Fatalf("reason = %q want %q", reason, tc.reason)
			}
		})
	}
}

func TestOfficialStaleOrInvalidCreatesZeroWatchActions(t *testing.T) {
	orgID := uuid.New()
	subject := "opportunity:pregao-1"
	store := newFakeWatchStore(watchSubscription(orgID, "ana@example.com", subject))
	dispatcher := &recordingDispatcher{}
	consumer := NewConsumer(store, dispatcher)

	stale := officialEvent(orgID, subject, "evt-stale")
	stale.Freshness = FreshnessStale
	admitted, reason := AdmitOfficialOpportunityEvent(stale)
	if reason != ReasonEventStale {
		t.Fatalf("reason = %q", reason)
	}
	if admitted.EventID != "" {
		t.Fatal("stale event was admitted")
	}
	result, err := consumer.HandleEvent(context.Background(), stale)
	if err != nil {
		t.Fatal(err)
	}
	if result.Skipped == "" || result.Dispatched != 0 || result.Matched != 0 {
		t.Fatalf("stale event created work: %+v", result)
	}
	if len(dispatcher.delivered()) != 0 {
		t.Fatalf("dispatcher ran: %v", dispatcher.delivered())
	}
}

func TestOfficialValidEventUpdatesExactlyOneWatchAndReplay100IsOne(t *testing.T) {
	orgID := uuid.New()
	subject := "company:acme-holdings"
	sub := watchSubscription(orgID, "ana@example.com", subject)
	store := newFakeWatchStore(sub)
	dispatcher := &recordingDispatcher{}
	consumer := NewConsumer(store, dispatcher)

	event, reason := AdmitOfficialOpportunityEvent(officialEvent(orgID, subject, "evt-live-1"))
	if reason != "" {
		t.Fatal(reason)
	}
	first, err := consumer.HandleEvent(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	if first.Matched != 1 || first.Dispatched != 1 {
		t.Fatalf("first = %+v", first)
	}
	for i := 0; i < 100; i++ {
		replay, err := consumer.HandleEvent(context.Background(), event)
		if err != nil {
			t.Fatalf("replay %d: %v", i, err)
		}
		if replay.Dispatched != 0 || replay.Deduped != 1 {
			t.Fatalf("replay %d duplicated: %+v", i, replay)
		}
	}
	if got := dispatcher.delivered(); len(got) != 1 {
		t.Fatalf("deliveries = %v", got)
	}
	key := models.IntelWatchDeliveryKey{SubscriptionID: sub.ID, EventIdentity: event.EventID, ContentHash: event.ContentHash()}
	if store.state(key) != models.IntelWatchDeliveryDispatched {
		t.Fatalf("ledger state = %s", store.state(key))
	}
}

func TestOfficialLiveIntelligenceBundleIsNotAnOpportunityEvent(t *testing.T) {
	raw := []byte(`{"schema":"CONFENGE_LIVE_INTELLIGENCE/1.0","source_run_id":"snap-1"}`)
	if !IsOfficialLiveIntelligenceBundle(raw) {
		t.Fatal("bundle not recognized")
	}
	if IsOpportunityEventEnvelope(raw) {
		t.Fatal("bundle was treated as an opportunity event")
	}
}
