package liveintel

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

// Durable-inbox producer proofs.
//
// C (producer half): a stored envelope comes back out of Subscribe with the
//    organization the row carries, not the one the payload claimed.
// Restart survival: a fresh producer over the same store replays unconsumed
//    rows, because emission never consumed them.

// fakeInbox is an in-memory EventInbox. It reproduces the two properties the
// production table has that matter here: insertion is idempotent on
// (org, event id), and emission does not consume a row.
type fakeInbox struct {
	rows  []models.IntelWatchInboxEvent
	lease map[string]time.Time
	// failNext makes the next replay fail, so a caller can prove a producer
	// error is surfaced rather than silently emitting nothing.
	failNext error
}

func newFakeInbox() *fakeInbox {
	return &fakeInbox{lease: map[string]time.Time{}}
}

func (f *fakeInbox) key(row models.IntelWatchInboxEvent) string {
	return row.OrganizationID.String() + "/" + row.EventID
}

func (f *fakeInbox) AppendOpportunityEvent(_ context.Context, event models.IntelWatchInboxEvent) (bool, error) {
	for _, existing := range f.rows {
		if f.key(existing) == f.key(event) {
			return false, nil
		}
	}
	if event.ReceivedAt.IsZero() {
		event.ReceivedAt = time.Now().UTC()
	}
	f.rows = append(f.rows, event)
	return true, nil
}

func (f *fakeInbox) ClaimReplayableEvents(_ context.Context, orgID uuid.UUID, now time.Time, window, lease time.Duration, limit int) ([]models.IntelWatchInboxEvent, error) {
	if f.failNext != nil {
		err := f.failNext
		f.failNext = nil
		return nil, err
	}
	var out []models.IntelWatchInboxEvent
	for _, row := range f.rows {
		if orgID != uuid.Nil && row.OrganizationID != orgID {
			continue
		}
		if !row.ReceivedAt.After(now.Add(-window)) {
			continue
		}
		if held, ok := f.lease[f.key(row)]; ok && held.After(now) {
			continue
		}
		f.lease[f.key(row)] = now.Add(lease)
		out = append(out, row)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func storedEvent(orgID uuid.UUID, id, subject string, at time.Time) models.IntelWatchInboxEvent {
	return models.IntelWatchInboxEvent{
		OrganizationID: orgID, EventID: id, Schema: EventSchemaV1,
		EventType: string(EventNewOpportunity), SubjectKey: subject,
		OccurredAt: at, ReceivedAt: at, Payload: map[string]string{"headline": "edital publicado"},
	}
}

func drain(t *testing.T, producer EventProducer) []OpportunityEvent {
	t.Helper()
	ch, err := producer.Subscribe(context.Background())
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	var out []OpportunityEvent
	for event := range ch {
		out = append(out, event)
	}
	return out
}

// The channel closes when the batch is drained. The reclaim worker ranges to
// close, so a producer that kept it open would hold one pass open forever.
func TestPostgresEventProducerEmitsABatchAndCloses(t *testing.T) {
	orgID := uuid.New()
	inbox := newFakeInbox()
	now := time.Now().UTC()
	for _, id := range []string{"evt-1", "evt-2"} {
		if _, err := inbox.AppendOpportunityEvent(context.Background(), storedEvent(orgID, id, "company:acme", now)); err != nil {
			t.Fatal(err)
		}
	}
	events := drain(t, NewPostgresEventProducer(inbox))
	if len(events) != 2 {
		t.Fatalf("emitted %d events, want 2", len(events))
	}
	for _, event := range events {
		if ok, reason := event.Validate(); !ok {
			t.Fatalf("emitted event is invalid: %s", reason)
		}
		if event.OrgID != orgID {
			t.Fatalf("event org = %s, want %s", event.OrgID, orgID)
		}
	}
}

// Ingestion binds the organization the caller resolved. A payload naming a
// different organization cannot reach it.
func TestInboxRowFromEventBindsTheCallerResolvedOrganization(t *testing.T) {
	trusted, attacker := uuid.New(), uuid.New()
	row := InboxRowFromEvent(trusted, OpportunityEvent{
		Schema: EventSchemaV1, EventID: "evt-x", EventType: EventNewOpportunity,
		SubjectKey: "company:acme", OrgID: attacker,
		Payload: map[string]string{"headline": "x"},
	}, time.Now().UTC())
	if row.OrganizationID != trusted {
		t.Fatalf("stored org = %s, want the caller-resolved %s", row.OrganizationID, trusted)
	}
	if got := OpportunityEventFromInbox(row); got.OrgID != trusted {
		t.Fatalf("replayed org = %s, want %s", got.OrgID, trusted)
	}
}

// Restart survival. A brand-new producer over the same store replays the same
// events: nothing was consumed by having been emitted once.
func TestPostgresEventProducerSurvivesARestart(t *testing.T) {
	orgID := uuid.New()
	inbox := newFakeInbox()
	now := time.Now().UTC()
	if _, err := inbox.AppendOpportunityEvent(context.Background(), storedEvent(orgID, "evt-1", "company:acme", now)); err != nil {
		t.Fatal(err)
	}
	first := drain(t, NewPostgresEventProducer(inbox))
	if len(first) != 1 {
		t.Fatalf("first pass emitted %d", len(first))
	}
	// A fresh producer holds no state of its own; only the store does.
	restarted := NewPostgresEventProducer(inbox)
	// The emit lease from the first pass is still live, so an immediate second
	// pass yields nothing. That bounds duplicate work, not correctness.
	if immediate := drain(t, restarted); len(immediate) != 0 {
		t.Fatalf("a second pass inside the emit lease emitted %d events", len(immediate))
	}
	// Past the lease the same rows are offered again, which is what makes an
	// undelivered PENDING recoverable without a human.
	inbox.lease = map[string]time.Time{}
	after := drain(t, restarted)
	if len(after) != 1 || after[0].EventID != "evt-1" {
		t.Fatalf("replay after lease expiry = %+v", after)
	}
}

// A cancelled pass leaves the rows in the store. Nothing was consumed, so the
// next pass still owes them.
func TestPostgresEventProducerCancelledPassLosesNothing(t *testing.T) {
	orgID := uuid.New()
	inbox := newFakeInbox()
	now := time.Now().UTC()
	for _, id := range []string{"evt-1", "evt-2", "evt-3"} {
		if _, err := inbox.AppendOpportunityEvent(context.Background(), storedEvent(orgID, id, "company:acme", now)); err != nil {
			t.Fatal(err)
		}
	}
	producer := NewPostgresEventProducer(inbox)
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := producer.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	<-ch
	cancel()
	for range ch { //nolint:revive // draining the cancelled channel
	}
	inbox.lease = map[string]time.Time{}
	if replayed := drain(t, producer); len(replayed) != 3 {
		t.Fatalf("after a cancelled pass the store replayed %d of 3 events", len(replayed))
	}
}

// The same event id posted twice is one row, and the first envelope is kept.
func TestInboxAppendIsIdempotentOnEventID(t *testing.T) {
	orgID := uuid.New()
	inbox := newFakeInbox()
	now := time.Now().UTC()
	first, err := inbox.AppendOpportunityEvent(context.Background(), storedEvent(orgID, "evt-1", "company:acme", now))
	if err != nil || !first {
		t.Fatalf("first append: inserted=%v err=%v", first, err)
	}
	second, err := inbox.AppendOpportunityEvent(context.Background(), storedEvent(orgID, "evt-1", "company:other", now))
	if err != nil || second {
		t.Fatalf("repeat append: inserted=%v err=%v, want a replay", second, err)
	}
	events := drain(t, NewPostgresEventProducer(inbox))
	if len(events) != 1 || events[0].SubjectKey != "company:acme" {
		t.Fatalf("stored envelope was rewritten: %+v", events)
	}
}

// An unconfigured producer is nil, so the caller's dormant-lane check stays one
// nil test and the lane never becomes a boot failure.
func TestPostgresEventProducerNilWithoutAnInbox(t *testing.T) {
	if producer := NewPostgresEventProducer(nil); producer != nil {
		t.Fatal("a producer was built with no inbox")
	}
	var producer *PostgresEventProducer
	if _, err := producer.Subscribe(context.Background()); err == nil {
		t.Fatal("a nil producer subscribed without error")
	}
}

// The recognizer claims its own body and refuses the sibling schemas.
func TestOpportunityEventEnvelopeRecognizer(t *testing.T) {
	if !IsOpportunityEventEnvelope([]byte(`{"schema":"` + EventSchemaV1 + `"}`)) {
		t.Fatal("recognizer did not claim its own body")
	}
	if IsOpportunityEventEnvelope([]byte(`{"schema":"confenge.commercial_event.v1"}`)) {
		t.Fatal("recognizer claimed a commercial event body")
	}
	event, err := ParseOpportunityEvent([]byte(`{"schema":"` + EventSchemaV1 + `","event_id":"e1","event_type":"new_opportunity","subject_key":"company:acme","payload":{"a":"b"}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if event.EventType != EventNewOpportunity {
		t.Fatalf("event type = %q, want the normalized closed-set value", event.EventType)
	}
}
