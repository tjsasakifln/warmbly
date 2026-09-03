package liveintel

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

// The durable-inbox opportunity-event producer.
//
// This is the production EventProducer. It resolves the replayability boundary
// the fixture producer documented: the real upstream (extra-cli) posts once
// over HTTP and cannot be asked to post again, so Warmbly persists the envelope
// at ingestion and replays from its own table. Restart survival and
// replay-after-transient-failure are then structural, not a property of the
// caller.
//
// It deliberately reproduces the fixture's semantics rather than inventing new
// ones, because the reclaim worker was built against them: Subscribe emits a
// bounded batch and CLOSES the channel. The worker ranges to close, so a
// producer that opened a live tail would hold one reclaim pass open forever.
//
// Rows are never consumed by being emitted. Marking a row consumed at hand-off
// would lose the event whenever the consumer failed straight afterwards, which
// is exactly the failure the inbox exists to prevent. Re-emission inside the
// replay window is free: the delivery ledger refuses a duplicate by primary
// key. The emit lease bounds duplicate WORK between two producer instances; it
// is not what makes delivery safe.

// EventInbox is the durable envelope store the production producer reads. The
// repository satisfies it; a test fake satisfies it just as well.
type EventInbox interface {
	AppendOpportunityEvent(ctx context.Context, event models.IntelWatchInboxEvent) (bool, error)
	ClaimReplayableEvents(ctx context.Context, orgID uuid.UUID, now time.Time, window, lease time.Duration, limit int) ([]models.IntelWatchInboxEvent, error)
}

const (
	// DefaultInboxReplayWindow bounds how far back a pass replays. Past it an
	// event is either delivered or terminally settled in the ledger, so
	// re-offering it only costs work.
	DefaultInboxReplayWindow = 24 * time.Hour
	// defaultInboxEmitLease keeps two producer instances off the same rows for
	// one pass. It matches the consumer's own delivery lease.
	defaultInboxEmitLease = 2 * time.Minute
	// defaultInboxBatchLimit bounds one pass. The events it does not reach stay
	// in the window and are offered again on the next tick.
	defaultInboxBatchLimit = 500
)

// PostgresEventProducer replays the durable inbox.
type PostgresEventProducer struct {
	mu     sync.RWMutex
	inbox  EventInbox
	window time.Duration
	lease  time.Duration
	limit  int
	// orgFilter restricts a pass to one organization. uuid.Nil means every
	// organization, which is the normal production shape: the org is
	// authoritative on the row, having come from Warmbly's own webhook auth.
	orgFilter uuid.UUID
	now       func() time.Time
}

// NewPostgresEventProducer builds the production producer. It returns nil when
// no inbox is available, so a caller's dormant-lane check stays one nil test.
func NewPostgresEventProducer(inbox EventInbox) *PostgresEventProducer {
	if inbox == nil {
		return nil
	}
	return &PostgresEventProducer{
		inbox: inbox, window: DefaultInboxReplayWindow,
		lease: defaultInboxEmitLease, limit: defaultInboxBatchLimit,
		now: func() time.Time { return time.Now().UTC() },
	}
}

// BindOrganization restricts replay to one organization.
func (p *PostgresEventProducer) BindOrganization(orgID uuid.UUID) *PostgresEventProducer {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.orgFilter = orgID
	return p
}

// WithReplayWindow overrides how far back a pass replays.
func (p *PostgresEventProducer) WithReplayWindow(window time.Duration) *PostgresEventProducer {
	if p == nil || window <= 0 {
		return p
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.window = window
	return p
}

// Subscribe emits the events currently replayable and closes.
//
// Closing is the contract, not a shortcut: the reclaim worker ranges to close,
// so one Subscribe is one pass. Anything this pass does not deliver is still in
// the inbox for the next one.
func (p *PostgresEventProducer) Subscribe(ctx context.Context) (<-chan OpportunityEvent, error) {
	if p == nil || p.inbox == nil {
		return nil, fmt.Errorf("intel watch event inbox is not configured")
	}
	p.mu.RLock()
	inbox, window, lease, limit, orgFilter, now := p.inbox, p.window, p.lease, p.limit, p.orgFilter, p.now()
	p.mu.RUnlock()

	rows, err := inbox.ClaimReplayableEvents(ctx, orgFilter, now, window, lease, limit)
	if err != nil {
		return nil, fmt.Errorf("intel watch event inbox replay: %w", err)
	}
	out := make(chan OpportunityEvent)
	go func() {
		defer close(out)
		for _, row := range rows {
			select {
			case <-ctx.Done():
				return
			case out <- OpportunityEventFromInbox(row):
			}
		}
	}()
	return out, nil
}

// OpportunityEventFromInbox rebuilds the event from its stored row. The
// organization comes from the row's own column, never from the payload: the
// column is what Warmbly's webhook auth resolved, and the payload is whatever
// an external caller chose to write.
func OpportunityEventFromInbox(row models.IntelWatchInboxEvent) OpportunityEvent {
	return OpportunityEvent{
		Schema:     EventSchemaV1,
		EventID:    strings.TrimSpace(row.EventID),
		EventType:  EventType(strings.TrimSpace(row.EventType)),
		SubjectKey: strings.TrimSpace(row.SubjectKey),
		OrgID:      row.OrganizationID,
		OccurredAt: row.OccurredAt.UTC(),
		Payload:    row.Payload,
	}
}

// InboxRowFromEvent turns a validated event into the row to persist. orgID is
// the caller-resolved organization and always wins over anything the event
// carries.
func InboxRowFromEvent(orgID uuid.UUID, event OpportunityEvent, receivedAt time.Time) models.IntelWatchInboxEvent {
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	}
	occurred := event.OccurredAt.UTC()
	if occurred.IsZero() {
		occurred = receivedAt.UTC()
	}
	return models.IntelWatchInboxEvent{
		OrganizationID: orgID,
		EventID:        strings.TrimSpace(event.EventID),
		Schema:         EventSchemaV1,
		EventType:      string(event.EventType),
		SubjectKey:     strings.TrimSpace(event.SubjectKey),
		OccurredAt:     occurred,
		Payload:        event.Payload,
		ReceivedAt:     receivedAt.UTC(),
	}
}
