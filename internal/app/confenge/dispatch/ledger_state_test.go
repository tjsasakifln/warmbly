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
