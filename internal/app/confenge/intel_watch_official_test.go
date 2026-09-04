package confenge

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/liveintel"
	"github.com/warmbly/warmbly/internal/models"
)

type memOpportunityInbox struct {
	mu   sync.Mutex
	rows map[string]models.IntelWatchInboxEvent
}

func (m *memOpportunityInbox) AppendOpportunityEvent(_ context.Context, event models.IntelWatchInboxEvent) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows == nil {
		m.rows = map[string]models.IntelWatchInboxEvent{}
	}
	key := event.OrganizationID.String() + "|" + event.EventID
	if _, exists := m.rows[key]; exists {
		return false, nil
	}
	m.rows[key] = event
	return true, nil
}

func (m *memOpportunityInbox) ClaimReplayableEvents(_ context.Context, orgID uuid.UUID, _ time.Time, _, _ time.Duration, _ int) ([]models.IntelWatchInboxEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []models.IntelWatchInboxEvent
	for _, row := range m.rows {
		if orgID != uuid.Nil && row.OrganizationID != orgID {
			continue
		}
		out = append(out, row)
	}
	return out, nil
}

func officialWatchService(t *testing.T) (*service, *memOpportunityInbox, uuid.UUID) {
	t.Helper()
	svc, orgID := newEmptyWebIntentService(t)
	inbox := &memOpportunityInbox{}
	svc.WireIntelWatchInbox(inbox)
	return svc, inbox, orgID
}

func TestIngestOfficialEnvelopeStoresOnceAndReplayDoesNotRewrite(t *testing.T) {
	svc, inbox, orgID := officialWatchService(t)
	event := liveintel.OpportunityEvent{
		Schema: liveintel.EventSchemaV1, EventID: "evt-official-1",
		EventType: liveintel.EventNewOpportunity, SubjectKey: "opportunity:pregao-1",
		OccurredAt: time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC),
		Payload:    map[string]string{"headline": "publicado"},
	}
	event.SourceRunID = "run-official-1"
	event.Provenance = "extra-cli"
	event.PublicDecision = liveintel.PublicDecisionPublicSafe
	event.Freshness = liveintel.FreshnessFresh
	event.ClaimedContentHash = event.ContentHash()

	first, xerr := svc.IngestOpportunityEvent(context.Background(), orgID, event, time.Now().UTC())
	if xerr != nil || first == nil || first.Replay {
		t.Fatalf("first ingest: %+v err=%v", first, xerr)
	}
	for i := 0; i < 100; i++ {
		again, xerr := svc.IngestOpportunityEvent(context.Background(), orgID, event, time.Now().UTC())
		if xerr != nil || again == nil || !again.Replay {
			t.Fatalf("replay %d: %+v err=%v", i, again, xerr)
		}
	}
	if len(inbox.rows) != 1 {
		t.Fatalf("inbox rows = %d want 1", len(inbox.rows))
	}
}

func TestIngestOfficialRejectsStaleHashMismatchAndBundle(t *testing.T) {
	svc, inbox, orgID := officialWatchService(t)
	event := liveintel.OpportunityEvent{
		Schema: liveintel.EventSchemaV1, EventID: "evt-bad",
		EventType: liveintel.EventNewOpportunity, SubjectKey: "company:acme",
		OccurredAt: time.Now().UTC(), Payload: map[string]string{"headline": "x"},
	}
	event.Freshness = liveintel.FreshnessStale
	if _, xerr := svc.IngestOpportunityEvent(context.Background(), orgID, event, time.Now().UTC()); xerr == nil {
		t.Fatal("stale event was stored")
	}
	event.Freshness = liveintel.FreshnessFresh
	event.ClaimedContentHash = "deadbeef"
	if _, xerr := svc.IngestOpportunityEvent(context.Background(), orgID, event, time.Now().UTC()); xerr == nil {
		t.Fatal("hash mismatch was stored")
	}
	event.ClaimedContentHash = ""
	event.PublicDecision = liveintel.PublicDecisionNotPublicSafe
	if _, xerr := svc.IngestOpportunityEvent(context.Background(), orgID, event, time.Now().UTC()); xerr == nil {
		t.Fatal("not_public_safe was stored")
	}
	if len(inbox.rows) != 0 {
		t.Fatalf("rejected events created inbox rows: %d", len(inbox.rows))
	}
	if !liveintel.IsOfficialLiveIntelligenceBundle([]byte(`{"schema":"CONFENGE_LIVE_INTELLIGENCE/1.0"}`)) {
		t.Fatal("bundle recognizer")
	}
}
