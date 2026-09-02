package liveintel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// The fixture-backed opportunity-event producer.
//
// The real upstream (extra-cli) is an out-of-repo tool that is not reachable
// from this process, so the production EventProducer is a file. That is a
// deliberate, testable choice rather than a placeholder: the consumer's whole
// contract is "given these events, deliver each one at most once", and a
// versioned file replays exactly as well as a broker does. Swapping in a live
// producer later means implementing one interface, changing nothing downstream.

// FixtureSchemaV1 tags the event-fixture envelope, matching the repo's other
// schema-tagged fixtures.
const FixtureSchemaV1 = "CONFENGE_OPPORTUNITY_EVENT_FIXTURE/1.0"

// EnvFixturePath points the producer at its event file. Unset means the
// producer has nothing to emit, which is a working dormant state, not an error.
const EnvFixturePath = "CONFENGE_INTEL_WATCH_EVENTS_FILE"

// EventFixture is the on-disk envelope. Synthetic marks a file that must never
// be mistaken for observed production facts, mirroring the convention in
// testdata/controlled_email_five_class_canary.json.
type EventFixture struct {
	SchemaVersion string             `json:"schema_version"`
	GeneratedAt   time.Time          `json:"generated_at"`
	Synthetic     bool               `json:"synthetic"`
	Source        EventFixtureSource `json:"source"`
	Events        []OpportunityEvent `json:"events"`
}

// EventFixtureSource records where the events claim to come from, so an
// operator reading the file can tell a rehearsal from a captured replay.
type EventFixtureSource struct {
	System string `json:"system"`
	RunID  string `json:"run_id"`
}

// Validate rejects a file that cannot be trusted as an event source. It does
// not judge the individual events: a malformed event is the consumer's to
// discard, and one bad row must not silence the whole file.
func (f *EventFixture) Validate() (bool, string) {
	if f == nil {
		return false, "fixture_nil"
	}
	if strings.TrimSpace(f.SchemaVersion) != FixtureSchemaV1 {
		return false, "fixture_schema_mismatch"
	}
	if strings.TrimSpace(f.Source.System) == "" {
		return false, "fixture_source_system_missing"
	}
	return true, ""
}

// FixtureEventProducer replays a versioned event file. It is safe to Subscribe
// to more than once: each subscription gets its own channel over the same
// immutable event set, so a re-drive after a transient failure sees exactly the
// events it saw before. That replayability is what lets the reclaim worker
// re-deliver without an event-payload column in the ledger.
type FixtureEventProducer struct {
	mu     sync.RWMutex
	events []OpportunityEvent
	source EventFixtureSource
	// orgOverride rewrites every event's organization. A fixture is authored
	// once and replayed into whichever organization is being exercised.
	orgOverride uuid.UUID
}

// NewFixtureEventProducer loads and validates an event file.
func NewFixtureEventProducer(path string) (*FixtureEventProducer, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("intel watch event fixture: %w", err)
	}
	var fixture EventFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		return nil, fmt.Errorf("intel watch event fixture %s: %w", path, err)
	}
	if ok, reason := fixture.Validate(); !ok {
		return nil, fmt.Errorf("intel watch event fixture %s rejected: %s", path, reason)
	}
	// A stable order makes a replay byte-for-byte reproducible, which is what
	// makes "the same event twice does nothing" a testable claim.
	events := append([]OpportunityEvent(nil), fixture.Events...)
	sort.SliceStable(events, func(i, j int) bool { return events[i].EventID < events[j].EventID })
	return &FixtureEventProducer{events: events, source: fixture.Source}, nil
}

// NewFixtureEventProducerFromEnv builds the producer from EnvFixturePath. It
// returns (nil, nil) when the variable is unset: no configured source is a
// dormant lane, never a startup failure.
func NewFixtureEventProducerFromEnv() (*FixtureEventProducer, error) {
	path := strings.TrimSpace(os.Getenv(EnvFixturePath))
	if path == "" {
		return nil, nil
	}
	return NewFixtureEventProducer(path)
}

// BindOrganization replays the fixture into one organization. Without it a
// fixture's authored org id is used as written.
func (p *FixtureEventProducer) BindOrganization(orgID uuid.UUID) *FixtureEventProducer {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.orgOverride = orgID
	return p
}

// Events returns the replayable event set, already bound to the organization.
func (p *FixtureEventProducer) Events() []OpportunityEvent {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]OpportunityEvent, 0, len(p.events))
	for _, event := range p.events {
		if p.orgOverride != uuid.Nil {
			event.OrgID = p.orgOverride
		}
		out = append(out, event)
	}
	return out
}

// Subscribe emits the whole event set and closes. Closing is the point: a file
// producer is finite, and a consumer that ranges over the channel terminates
// instead of waiting forever for an upstream that will never speak again.
func (p *FixtureEventProducer) Subscribe(ctx context.Context) (<-chan OpportunityEvent, error) {
	if p == nil {
		return nil, fmt.Errorf("intel watch event producer is not configured")
	}
	events := p.Events()
	out := make(chan OpportunityEvent)
	go func() {
		defer close(out)
		for _, event := range events {
			select {
			case <-ctx.Done():
				log.Warn().Str("run_id", p.source.RunID).
					Msg("confenge intel watch: event replay cancelled before the file was drained")
				return
			case out <- event:
			}
		}
	}()
	return out, nil
}
