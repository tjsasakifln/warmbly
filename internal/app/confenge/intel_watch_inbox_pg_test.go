package confenge

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/liveintel"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// Real-Postgres proofs for the durable opportunity-event inbox and the
// INTEL_SEED send ledger. They exercise the actual SQL, the actual CHECK
// constraints and the actual conflict handling, which no fake can stand in for.
//
// They skip without WARMBLY_TEST_POSTGRES_DSN, following the same pattern as
// the rest of this package's _pg_test.go files.

func inboxTestEvent(orgID uuid.UUID, id, subject string, at time.Time) models.IntelWatchInboxEvent {
	return liveintel.InboxRowFromEvent(orgID, liveintel.OpportunityEvent{
		Schema: liveintel.EventSchemaV1, EventID: id, EventType: liveintel.EventNewOpportunity,
		SubjectKey: subject, OccurredAt: at,
		Payload: map[string]string{"headline": "edital publicado", "public_url": "https://example.gov/e"},
	}, at)
}

// The envelope survives a restart because it is a row. A fresh producer over a
// fresh pool replays it.
func TestPGInboxSurvivesRestartAndReplaysUnconsumedRows(t *testing.T) {
	pool, orgID := openHandRaisePG(t)
	ctx := context.Background()
	inbox := repository.NewIntelWatchInboxRepository(pool)
	now := time.Now().UTC()

	inserted, err := inbox.AppendOpportunityEvent(ctx, inboxTestEvent(orgID, "evt-pg-1", "company:acme", now))
	if err != nil || !inserted {
		t.Fatalf("append: inserted=%v err=%v", inserted, err)
	}
	// A repeat post of the same id is a replay, not a second row, and does not
	// rewrite the stored envelope.
	again, err := inbox.AppendOpportunityEvent(ctx, inboxTestEvent(orgID, "evt-pg-1", "company:other", now))
	if err != nil || again {
		t.Fatalf("repeat append: inserted=%v err=%v", again, err)
	}
	stored, err := inbox.GetOpportunityEvent(ctx, orgID, "evt-pg-1")
	if err != nil || stored == nil {
		t.Fatalf("read back: %v", err)
	}
	if stored.SubjectKey != "company:acme" {
		t.Fatalf("stored envelope was rewritten: %+v", stored)
	}

	// A brand-new producer holds no state; the row is the state.
	events, err := liveintel.NewPostgresEventProducer(inbox).BindOrganization(orgID).Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got []liveintel.OpportunityEvent
	for event := range events {
		got = append(got, event)
	}
	if len(got) != 1 || got[0].EventID != "evt-pg-1" || got[0].OrgID != orgID {
		t.Fatalf("replay = %+v", got)
	}
	if ok, reason := got[0].Validate(); !ok {
		t.Fatalf("replayed event is invalid: %s", reason)
	}
}

// The emit lease bounds duplicate work between two producer instances, and an
// expired lease makes the row claimable again. Correctness never depended on
// the lease: nothing was consumed either way.
func TestPGInboxEmitLeaseBoundsDuplicateWorkAndExpires(t *testing.T) {
	pool, orgID := openHandRaisePG(t)
	ctx := context.Background()
	inbox := repository.NewIntelWatchInboxRepository(pool)
	now := time.Now().UTC()
	if _, err := inbox.AppendOpportunityEvent(ctx, inboxTestEvent(orgID, "evt-pg-lease", "company:acme", now)); err != nil {
		t.Fatal(err)
	}
	first, err := inbox.ClaimReplayableEvents(ctx, orgID, now, time.Hour, 2*time.Minute, 10)
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim = %d rows, err=%v", len(first), err)
	}
	second, err := inbox.ClaimReplayableEvents(ctx, orgID, now, time.Hour, 2*time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Fatalf("a second producer instance claimed %d rows inside the lease", len(second))
	}
	// Past the lease the row is offered again, which is what makes an
	// undelivered PENDING recoverable without a human.
	afterLease, err := inbox.ClaimReplayableEvents(ctx, orgID, now.Add(5*time.Minute), time.Hour, 2*time.Minute, 10)
	if err != nil || len(afterLease) != 1 {
		t.Fatalf("claim after lease expiry = %d rows, err=%v", len(afterLease), err)
	}
	// Outside the replay window the row stops being offered.
	outside, err := inbox.ClaimReplayableEvents(ctx, orgID, now.Add(48*time.Hour), time.Hour, 2*time.Minute, 10)
	if err != nil || len(outside) != 0 {
		t.Fatalf("claim outside the window = %d rows, err=%v", len(outside), err)
	}
}

// The organization is part of the row identity, so one org's producer never
// sees another org's events.
func TestPGInboxReplayIsOrganizationScoped(t *testing.T) {
	pool, orgID := openHandRaisePG(t)
	ctx := context.Background()
	inbox := repository.NewIntelWatchInboxRepository(pool)
	now := time.Now().UTC()
	if _, err := inbox.AppendOpportunityEvent(ctx, inboxTestEvent(orgID, "evt-pg-scope", "company:acme", now)); err != nil {
		t.Fatal(err)
	}
	other := uuid.New()
	rows, err := inbox.ClaimReplayableEvents(ctx, other, now, time.Hour, time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("another organization's producer saw %d rows", len(rows))
	}
}

// The INTEL_SEED ledger is the lane's own counter, and it is idempotent on the
// message key so a crash-retry cannot double-count the cap.
func TestPGIntelSeedLedgerCountsItsOwnLaneOnly(t *testing.T) {
	pool, orgID := openHandRaisePG(t)
	ctx := context.Background()
	ledger := repository.NewIntelSeedRepository(pool)
	now := time.Now().UTC()
	dayStart := now.Truncate(24 * time.Hour)

	send := models.IntelSeedSend{
		OrganizationID: orgID, MessageKey: "email:intel_seed:contact:pg:subject:company:acme",
		Recipient: "ana@example.com", SubjectKey: "company:acme", SentAt: now,
	}
	inserted, err := ledger.RecordSend(ctx, send)
	if err != nil || !inserted {
		t.Fatalf("record: inserted=%v err=%v", inserted, err)
	}
	again, err := ledger.RecordSend(ctx, send)
	if err != nil || again {
		t.Fatalf("repeat record: inserted=%v err=%v, want a no-op", again, err)
	}
	n, err := ledger.CountSendsSince(ctx, orgID, dayStart)
	if err != nil || n != 1 {
		t.Fatalf("count = %d err=%v, want 1", n, err)
	}
	sent, err := ledger.AlreadySent(ctx, orgID, send.MessageKey)
	if err != nil || !sent {
		t.Fatalf("already sent = %v err=%v", sent, err)
	}
	// Another organization's ledger is untouched.
	if n, err := ledger.CountSendsSince(ctx, uuid.New(), dayStart); err != nil || n != 0 {
		t.Fatalf("cross-org count = %d err=%v", n, err)
	}
}
