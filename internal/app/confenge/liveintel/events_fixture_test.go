package liveintel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func fixturePath() string { return filepath.Join("testdata", "intel_watch_opportunity_events.json") }

// The checked-in fixture must actually load, and must be marked synthetic so
// nobody mistakes a rehearsal file for captured production facts.
func TestFixtureProducerLoadsTheCheckedInEventFile(t *testing.T) {
	producer, err := NewFixtureEventProducer(fixturePath())
	if err != nil {
		t.Fatal(err)
	}
	events := producer.Events()
	if len(events) == 0 {
		t.Fatal("the fixture produced no events")
	}
	valid := 0
	for _, event := range events {
		if ok, _ := event.Validate(); ok {
			valid++
		}
	}
	if valid == 0 {
		t.Fatal("the fixture contains no valid events")
	}
	if valid == len(events) {
		t.Fatal("the fixture should also carry a malformed event so the discard path is exercised")
	}
}

func TestFixtureFileIsMarkedSynthetic(t *testing.T) {
	raw, err := os.ReadFile(fixturePath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"synthetic": true`) {
		t.Fatal("the event fixture is not marked synthetic")
	}
	if !strings.Contains(string(raw), FixtureSchemaV1) {
		t.Fatalf("the event fixture is not tagged %s", FixtureSchemaV1)
	}
}

// A file producer is finite: Subscribe must close its channel so a consumer
// that ranges over it terminates instead of waiting on an upstream that will
// never speak again.
func TestFixtureProducerSubscribeIsFiniteAndReplayable(t *testing.T) {
	producer, err := NewFixtureEventProducer(fixturePath())
	if err != nil {
		t.Fatal(err)
	}
	drain := func() []string {
		channel, err := producer.Subscribe(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for event := range channel {
			ids = append(ids, event.EventID)
		}
		return ids
	}
	first, second := drain(), drain()
	if len(first) == 0 {
		t.Fatal("Subscribe emitted nothing")
	}
	if len(first) != len(second) {
		t.Fatalf("replay emitted a different number of events: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("replay order drifted at %d: %q vs %q", i, first[i], second[i])
		}
	}
}

// Binding an organization must rewrite every event, so one authored fixture
// can be replayed into whichever organization is under test.
func TestFixtureProducerBindsTheOrganization(t *testing.T) {
	producer, err := NewFixtureEventProducer(fixturePath())
	if err != nil {
		t.Fatal(err)
	}
	orgID := uuid.New()
	for _, event := range producer.BindOrganization(orgID).Events() {
		if event.OrgID != orgID {
			t.Fatalf("event %s kept org %s", event.EventID, event.OrgID)
		}
	}
}

// A file with the wrong schema tag is refused outright: an unversioned event
// source is not a source we are willing to act on.
func TestFixtureProducerRefusesAnUntaggedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "untagged.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":"nope","source":{"system":"x"},"events":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFixtureEventProducer(path); err == nil {
		t.Fatal("an untagged event file was accepted")
	}
}

// An unset path is a dormant lane, never a startup failure.
func TestFixtureProducerFromEnvIsDormantWhenUnset(t *testing.T) {
	t.Setenv(EnvFixturePath, "")
	producer, err := NewFixtureEventProducerFromEnv()
	if err != nil || producer != nil {
		t.Fatalf("unset fixture path produced %v / %v", producer, err)
	}
}
