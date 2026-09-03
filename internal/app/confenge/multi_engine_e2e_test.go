package confenge

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/app/confenge/liveintel"
)

// The multi-engine acceptance pack.
//
// LANE 0's regression pack proves first touch's admission, queue, volume and
// cadence contracts did not move. This pack proves the additive engines cannot
// reach first touch at all: the observable result of a full first touch is
// identical whether live intelligence is absent, broken, slow or hostile.

// firstTouchOutcomeSnapshot is everything observable about one full first-touch
// pass. Two passes that agree on all of it are indistinguishable to the rest of
// the system.
type firstTouchOutcomeSnapshot struct {
	Progressed      bool
	Err             string
	ProviderCalls   int
	SentTo          string
	QueueState      string
	LedgerRecorded  bool
	SecondPassFound bool
	SecondPassCalls int
}

func runFirstTouchToTerminal(t *testing.T, resolver liveintel.Resolver) firstTouchOutcomeSnapshot {
	t.Helper()
	h := newFastLaneHarness(t, FirstTouchAccepted, nil)
	// nil means "the hook is not wired at all"; a resolver means "wired, and
	// this is how it behaves". Both must be indistinguishable downstream.
	h.svc.liveIntel = resolver
	_, key := h.enqueue(t, "alvo@exemplo.com.br")

	ctx := context.Background()
	progressed, err := h.svc.ProcessFastLaneOnce(ctx)
	snapshot := firstTouchOutcomeSnapshot{
		Progressed:    progressed,
		ProviderCalls: h.transport.attempts,
		QueueState:    h.queueStatus(t, key),
	}
	if err != nil {
		snapshot.Err = err.Error()
	}
	if len(h.transport.sentTo) > 0 {
		snapshot.SentTo = h.transport.sentTo[0]
	}
	_, recorded, _ := h.store.GetSendByKey(ctx, key)
	snapshot.LedgerRecorded = recorded

	// A second drain must find nothing: the logical first touch is gone.
	found, _ := h.svc.ProcessFastLaneOnce(ctx)
	snapshot.SecondPassFound = found
	snapshot.SecondPassCalls = h.transport.attempts
	return snapshot
}

// TEST A. OUTBOUND_FIRST_TOUCH works end to end with zero live intelligence
// present: no resolver data, no INTEL_SEED lane, no INTEL_WATCH subscriptions,
// no watch events. The engines being absent is not a degraded mode.
func TestFirstTouchWorksEndToEndWithZeroLiveIntelligence(t *testing.T) {
	// Prove the side lanes are genuinely inert in this process.
	t.Setenv(EnvIntelSeedEnabled, "")
	t.Setenv(liveintel.EnvWatchEnabled, "")
	t.Setenv(liveintel.EnvFixturePath, "")
	if IntelSeedEnabled() {
		t.Fatal("the INTEL_SEED lane is not dormant; this test proves nothing")
	}
	if liveintel.WatchEnabled() {
		t.Fatal("the INTEL_WATCH lane is not dormant; this test proves nothing")
	}
	producer, err := liveintel.NewFixtureEventProducerFromEnv()
	if err != nil || producer != nil {
		t.Fatalf("a watch event source exists: %v / %v", producer, err)
	}

	got := runFirstTouchToTerminal(t, liveintel.NoopResolver{})
	if !got.Progressed || got.Err != "" {
		t.Fatalf("first touch did not complete with no intelligence at all: %+v", got)
	}
	if got.ProviderCalls != 1 {
		t.Fatalf("expected exactly one provider attempt, got %d", got.ProviderCalls)
	}
	if got.SentTo != "alvo@exemplo.com.br" {
		t.Fatalf("unexpected recipient %q", got.SentTo)
	}
	if got.QueueState != dispatch.QueueSent {
		t.Fatalf("queue row is %q, want terminal sent", got.QueueState)
	}
	if !got.LedgerRecorded {
		t.Fatal("the accepted send was not recorded in the ledger")
	}
	if got.SecondPassFound || got.SecondPassCalls != 1 {
		t.Fatalf("the same first touch was selected again: %+v", got)
	}
}

// TEST G. Live intelligence unavailable in every shape it can be unavailable
// in. First touch must behave IDENTICALLY to the no-intelligence baseline, not
// merely "still work".
func TestFirstTouchIsIdenticalUnderEveryLiveIntelligenceFailure(t *testing.T) {
	baseline := runFirstTouchToTerminal(t, liveintel.NoopResolver{})
	if !baseline.Progressed || baseline.ProviderCalls != 1 {
		t.Fatalf("the baseline run is not a valid comparison point: %+v", baseline)
	}

	failures := map[string]liveintel.Resolver{
		"hook not wired at all": nil,
		"lookup error": liveintel.LookupFunc(func(context.Context, uuid.UUID, uuid.UUID) (*liveintel.LiveIntelligenceV1, error) {
			return nil, errors.New("intel source unavailable")
		}),
		"context deadline": liveintel.LookupFunc(func(context.Context, uuid.UUID, uuid.UUID) (*liveintel.LiveIntelligenceV1, error) {
			return nil, context.DeadlineExceeded
		}),
		"malformed payload": liveintel.LookupFunc(func(context.Context, uuid.UUID, uuid.UUID) (*liveintel.LiveIntelligenceV1, error) {
			return &liveintel.LiveIntelligenceV1{Schema: "NOT_A_SCHEMA/0.0"}, nil
		}),
		"unattested payload": liveintel.LookupFunc(func(context.Context, uuid.UUID, uuid.UUID) (*liveintel.LiveIntelligenceV1, error) {
			intel := validE2EIntel()
			intel.Attestation = ""
			return intel, nil
		}),
		"panicking resolver":   e2ePanicResolver{},
		"resolver over budget": e2eSlowResolver{delay: liveintel.LookupBudget * 3},
		"resolver returning junk": liveintel.LookupFunc(func(context.Context, uuid.UUID, uuid.UUID) (*liveintel.LiveIntelligenceV1, error) {
			return nil, nil
		}),
	}
	for name, resolver := range failures {
		got := runFirstTouchToTerminal(t, resolver)
		if got != baseline {
			t.Fatalf("%q changed first-touch behaviour:\n got %+v\nwant %+v", name, got, baseline)
		}
	}

	// And a WORKING resolver must be equally invisible to the send path: the
	// intelligence is attached to the gate decision, never consumed here.
	working := runFirstTouchToTerminal(t, liveintel.LookupFunc(
		func(context.Context, uuid.UUID, uuid.UUID) (*liveintel.LiveIntelligenceV1, error) {
			return validE2EIntel(), nil
		}))
	if working != baseline {
		t.Fatalf("a working resolver changed first-touch behaviour:\n got %+v\nwant %+v", working, baseline)
	}
}

// A resolver that hangs past its budget must not extend the first touch's send
// deadline: the caller holds a live dispatch reservation while it runs.
func TestSlowLiveIntelligenceDoesNotStretchTheFirstTouchSendBudget(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchAccepted, nil)
	h.svc.liveIntel = e2eSlowResolver{delay: liveintel.LookupBudget * 4}
	_, _ = h.enqueue(t, "alvo@exemplo.com.br")

	started := time.Now()
	if progressed, err := h.svc.ProcessFastLaneOnce(context.Background()); err != nil || !progressed {
		t.Fatalf("first touch did not complete: progressed=%v err=%v", progressed, err)
	}
	// The gate's lookup is bounded; the send path itself never consults intel,
	// so the whole pass must not pay four budgets' worth of waiting.
	if elapsed := time.Since(started); elapsed > liveintel.LookupBudget*3 {
		t.Fatalf("a slow resolver stretched the send path by %s", elapsed)
	}
	if h.transport.attempts != 1 {
		t.Fatalf("provider attempts=%d, want 1", h.transport.attempts)
	}
}

func validE2EIntel() *liveintel.LiveIntelligenceV1 {
	return &liveintel.LiveIntelligenceV1{
		Schema: liveintel.SchemaLiveIntelligenceV1, SubjectKey: "contrato-2026-0001",
		Kind: liveintel.KindOpportunity, Headline: "aditivo publicado",
		PublicURL:  "https://exemplo.gov.br/contratos/2026-0001",
		ObservedAt: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC), Attestation: "sig-abc",
	}
}

type e2ePanicResolver struct{}

func (e2ePanicResolver) Resolve(context.Context, uuid.UUID, uuid.UUID) (*liveintel.LiveIntelligenceV1, bool) {
	panic("intel resolver exploded inside the send path")
}

type e2eSlowResolver struct{ delay time.Duration }

func (r e2eSlowResolver) Resolve(ctx context.Context, _, _ uuid.UUID) (*liveintel.LiveIntelligenceV1, bool) {
	select {
	case <-time.After(r.delay):
		return validE2EIntel(), true
	case <-ctx.Done():
		return nil, false
	}
}

// The three lanes' idempotency namespaces must be mutually exclusive across a
// wide sample, not just for one hand-picked triple. A collision would let one
// lane's "already sent" answer another lane's question.
func TestEveryLaneMessageKeyNamespaceStaysDisjoint(t *testing.T) {
	seen := map[string]string{}
	claim := func(lane, key string) {
		if prior, ok := seen[key]; ok {
			t.Fatalf("key %q minted by both %s and %s", key, prior, lane)
		}
		seen[key] = lane
	}
	for i := 0; i < 100; i++ {
		campaign, contact, seq, sub := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		claim("first_touch", MessageKeyCampaignEmail(campaign, contact, seq))
		claim("intel_seed", MessageKeyIntelSeed(contact, fmt.Sprintf("subject-%d", i)))
		claim("intel_watch", MessageKeyIntelWatch(sub, fmt.Sprintf("evt-%d", i), fmt.Sprintf("hash-%d", i)))
	}
	prefixes := map[string]string{
		"first_touch": "email:campaign:", "intel_seed": "email:intel_seed:", "intel_watch": "email:intel_watch:",
	}
	for key, lane := range seen {
		want := prefixes[lane]
		if len(key) < len(want) || key[:len(want)] != want {
			t.Fatalf("%s minted %q outside its namespace %q", lane, key, want)
		}
		// No other lane's namespace may be a prefix of this key.
		for otherLane, otherPrefix := range prefixes {
			if otherLane == lane {
				continue
			}
			if len(key) >= len(otherPrefix) && key[:len(otherPrefix)] == otherPrefix {
				t.Fatalf("%s minted %q inside %s's namespace", lane, key, otherLane)
			}
		}
	}
}

// Cold-outreach consent and subscription consent are legally and product
// distinct. Neither may substitute for the other in either direction.
func TestColdOutreachAndSubscriptionConsentNeverSubstituteForEachOther(t *testing.T) {
	t.Setenv(EnvIntelSeedEnabled, "true")
	t.Setenv(liveintel.EnvUnsubscribeSecret, intelWatchTestSecret)

	// An INTEL_WATCH subscriber's explicit consent must not admit a contact
	// that cold-outreach admission refuses.
	f := newIntelSeedFixture(t)
	f.withIntel(seedIntel(f.org, f.acc.ID))
	f.cand.DoNotContact = true
	if _, err := f.repo.UpsertCandidate(context.Background(), f.cand); err != nil {
		t.Fatal(err)
	}
	decision := f.svc.GateIntelSeed(context.Background(), f.org, f.cand.ID)
	if decision.Kind != IntelSeedBlocked {
		t.Fatalf("a DNC contact was admitted to a cold lane: kind=%d reason=%q", decision.Kind, decision.Reason)
	}

	// Conversely, passing cold-outreach admission must not by itself create a
	// watch subscription: the watch dispatcher refuses an inactive one, and
	// nothing in the seed lane writes a subscription row.
	seedDecision := newIntelSeedFixture(t)
	seedDecision.withIntel(seedIntel(seedDecision.org, seedDecision.acc.ID))
	admitted := seedDecision.svc.GateIntelSeed(context.Background(), seedDecision.org, seedDecision.cand.ID)
	if admitted.Kind != IntelSeedProceed {
		t.Fatalf("the control case was not admitted: kind=%d reason=%q", admitted.Kind, admitted.Reason)
	}
	// The composed seed message carries the watched subject so a human can opt
	// in by replying. It must not itself be a subscription.
	if admitted.Message.SubjectKey == "" {
		t.Fatal("the seed message lost the subject a recipient could subscribe to")
	}
	stopped := time.Now().UTC()
	transport := &stubWatchTransport{outcome: FirstTouchAccepted}
	unsubscribed := watchTestDelivery(t, func(context.Context) error { return nil })
	unsubscribed.Subscription.UnsubscribedAt = &stopped
	outcome, err := watchDispatcherFor(transport).DispatchWatchUpdate(context.Background(), unsubscribed)
	if outcome != liveintel.WatchPermanent || err == nil {
		t.Fatalf("cold-outreach eligibility leaked into watch consent: %q / %v", outcome, err)
	}
	if transport.calls != 0 {
		t.Fatal("an unsubscribed watcher was written to")
	}
}
