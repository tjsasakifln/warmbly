package liveintel

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// These tests drive the reclaim worker against the real Postgres ledger, so
// "a transient failure is recovered automatically" is proven by the same SQL
// that runs in production rather than by an in-memory stand-in.

func openWatchPG(t *testing.T) (*pgxpool.Pool, uuid.UUID) {
	t.Helper()
	dsn := os.Getenv("WARMBLY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("WARMBLY_TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	userID, orgID := uuid.New(), uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO users (id,first_name,last_name,email) VALUES($1,'Watch','Reclaim',$2)`,
		userID, fmt.Sprintf("watch-reclaim-%s@example.test", userID)); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO organizations (id,name,slug,owner_user_id) VALUES($1,'Watch Reclaim',$2,$3)`,
		orgID, "watch-reclaim-"+orgID.String(), userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, orgID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id=$1`, userID)
		pool.Close()
	})
	return pool, orgID
}

func seedWatchSubscription(t *testing.T, repo repository.IntelWatchRepository, orgID uuid.UUID, subject string) models.IntelWatchSubscription {
	t.Helper()
	consentAt := time.Now().UTC().Add(-time.Hour)
	sub, err := repo.CreateOrReactivateSubscription(context.Background(), &models.IntelWatchSubscription{
		OrganizationID: orgID, ContactEmail: fmt.Sprintf("watcher-%s@example.test", uuid.NewString()[:8]),
		IntentKind: models.IntelWatchIntentNewOpportunity, SubjectKey: subject,
		Topic: "prazos", Cadence: models.IntelWatchCadenceImmediate,
		ConsentSource: "confenge_web_monitor_request", ConsentAt: &consentAt, ConsentProvenanceOK: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return *sub
}

// scriptedDispatcher fails the first N attempts transiently (before ever
// taking the fence, so the ledger keeps the delivery claimable) and succeeds
// after that.
type scriptedDispatcher struct {
	mu             sync.Mutex
	transientFirst int
	attempts       int
	delivered      int
	fenceTaken     int
}

func (d *scriptedDispatcher) DispatchWatchUpdate(ctx context.Context, delivery WatchDelivery) (WatchDispatchOutcome, error) {
	d.mu.Lock()
	d.attempts++
	failing := d.attempts <= d.transientFirst
	d.mu.Unlock()
	if failing {
		// Fail BEFORE the handoff fence. Nothing was written to the watcher, so
		// the delivery is legitimately still owed.
		return WatchTransient, fmt.Errorf("provider unreachable on attempt %d", d.attempts)
	}
	if delivery.BeforeHandoff != nil {
		if err := delivery.BeforeHandoff(ctx); err != nil {
			return WatchTransient, err
		}
		d.mu.Lock()
		d.fenceTaken++
		d.mu.Unlock()
	}
	d.mu.Lock()
	d.delivered++
	d.mu.Unlock()
	return WatchDelivered, nil
}

func (d *scriptedDispatcher) counts() (attempts, delivered int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.attempts, d.delivered
}

func fixtureProducer(t *testing.T, orgID uuid.UUID) *FixtureEventProducer {
	t.Helper()
	producer, err := NewFixtureEventProducer(ReferenceFixturePathWithin())
	if err != nil {
		t.Fatal(err)
	}
	return producer.BindOrganization(orgID)
}

// TEST D. A transient dispatch failure must become a delivered notification
// without anyone replaying anything by hand: the reclaim worker is the actual
// trigger, and the ledger reaches a terminal DISPATCHED state.
func TestWatchReclaimWorkerRecoversATransientFailureWithoutAHumanPostgres(t *testing.T) {
	pool, orgID := openWatchPG(t)
	repo := repository.NewIntelWatchRepository(pool)
	subscription := seedWatchSubscription(t, repo, orgID, "contrato-2026-0001")

	// Two transient failures, then success. The fixture carries two events for
	// this subject, so the first pass burns both attempts on failure.
	dispatcher := &scriptedDispatcher{transientFirst: 2}
	worker := NewWatchReclaimWorker(NewConsumer(repo, dispatcher), fixtureProducer(t, orgID), time.Minute)
	ctx := context.Background()

	first, err := worker.ReclaimOnce(ctx)
	if err == nil {
		t.Fatal("a transient dispatch failure must be surfaced, not swallowed")
	}
	if first.Dispatched != 0 || first.Retryable == 0 {
		t.Fatalf("first pass should have delivered nothing and left work retryable: %+v", first)
	}

	// No human intervention between the passes. This is the whole point.
	second, err := worker.ReclaimOnce(ctx)
	if err != nil {
		t.Fatalf("second pass still failed: %v", err)
	}
	if second.Dispatched == 0 {
		t.Fatalf("the reclaim pass delivered nothing: %+v", second)
	}
	if _, delivered := dispatcher.counts(); delivered == 0 {
		t.Fatal("the dispatcher was never re-invoked by the reclaim mechanism")
	}

	// The ledger must be terminal, not merely quiet.
	events := fixtureProducer(t, orgID).Events()
	terminal := 0
	for _, event := range events {
		if event.SubjectKey != subscription.SubjectKey {
			continue
		}
		if ok, _ := event.Validate(); !ok {
			continue
		}
		row, err := repo.GetDelivery(ctx, models.IntelWatchDeliveryKey{
			SubscriptionID: subscription.ID, EventIdentity: event.EventID, ContentHash: event.ContentHash(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if row == nil {
			t.Fatalf("event %s never reached the ledger", event.EventID)
		}
		if row.State == models.IntelWatchDeliveryDispatched {
			terminal++
		}
	}
	if terminal == 0 {
		t.Fatal("no delivery reached a terminal DISPATCHED state after reclaim")
	}
}

// TEST C (ledger half). Replaying the identical event set must deliver nothing
// new: dedup is by subscription + event identity + semantic content hash.
func TestWatchReclaimReplayOfIdenticalEventsDeliversNothingNewPostgres(t *testing.T) {
	pool, orgID := openWatchPG(t)
	repo := repository.NewIntelWatchRepository(pool)
	seedWatchSubscription(t, repo, orgID, "contrato-2026-0001")

	dispatcher := &scriptedDispatcher{}
	worker := NewWatchReclaimWorker(NewConsumer(repo, dispatcher), fixtureProducer(t, orgID), time.Minute)
	ctx := context.Background()

	first, err := worker.ReclaimOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.Dispatched == 0 {
		t.Fatalf("first pass delivered nothing: %+v", first)
	}
	attemptsAfterFirst, deliveredAfterFirst := dispatcher.counts()

	replay, err := worker.ReclaimOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Dispatched != 0 {
		t.Fatalf("replaying the identical events dispatched %d more times", replay.Dispatched)
	}
	if replay.Deduped != first.Dispatched {
		t.Fatalf("replay deduped %d, want %d", replay.Deduped, first.Dispatched)
	}
	attemptsAfterReplay, deliveredAfterReplay := dispatcher.counts()
	if attemptsAfterReplay != attemptsAfterFirst || deliveredAfterReplay != deliveredAfterFirst {
		t.Fatalf("the dispatcher was invoked again on replay: attempts %d->%d delivered %d->%d",
			attemptsAfterFirst, attemptsAfterReplay, deliveredAfterFirst, deliveredAfterReplay)
	}
}

// TEST E. An ambiguous outcome is parked and never blindly resent, including
// across a reclaim pass whose whole job is to retry things.
func TestWatchReclaimNeverResendsAnAmbiguousDeliveryPostgres(t *testing.T) {
	pool, orgID := openWatchPG(t)
	repo := repository.NewIntelWatchRepository(pool)
	subscription := seedWatchSubscription(t, repo, orgID, "contrato-2026-0002")

	ambiguous := &ambiguousDispatcher{}
	worker := NewWatchReclaimWorker(NewConsumer(repo, ambiguous), fixtureProducer(t, orgID), time.Minute)
	ctx := context.Background()

	first, err := worker.ReclaimOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.Parked == 0 {
		t.Fatalf("an ambiguous dispatch was not parked: %+v", first)
	}
	attemptsAfterFirst := ambiguous.count()

	second, err := worker.ReclaimOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.Dispatched != 0 {
		t.Fatal("a parked ambiguous delivery was resent by the reclaim worker")
	}
	if ambiguous.count() != attemptsAfterFirst {
		t.Fatalf("the dispatcher was re-invoked for a parked delivery: %d -> %d",
			attemptsAfterFirst, ambiguous.count())
	}

	for _, event := range fixtureProducer(t, orgID).Events() {
		if event.SubjectKey != subscription.SubjectKey {
			continue
		}
		if ok, _ := event.Validate(); !ok {
			continue
		}
		row, err := repo.GetDelivery(ctx, models.IntelWatchDeliveryKey{
			SubscriptionID: subscription.ID, EventIdentity: event.EventID, ContentHash: event.ContentHash(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if row == nil || row.State != models.IntelWatchDeliveryAmbiguous {
			t.Fatalf("event %s is not parked AMBIGUOUS: %+v", event.EventID, row)
		}
	}
}

type ambiguousDispatcher struct {
	mu       sync.Mutex
	attempts int
}

func (d *ambiguousDispatcher) DispatchWatchUpdate(ctx context.Context, delivery WatchDelivery) (WatchDispatchOutcome, error) {
	d.mu.Lock()
	d.attempts++
	d.mu.Unlock()
	if delivery.BeforeHandoff != nil {
		if err := delivery.BeforeHandoff(ctx); err != nil {
			return WatchTransient, err
		}
	}
	return WatchAmbiguous, fmt.Errorf("connection died at end of DATA")
}

func (d *ambiguousDispatcher) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.attempts
}

// An unsubscribed watcher stops matching, so a later event for the same
// subject reaches nobody. The consent record itself is kept.
func TestWatchReclaimStopsDeliveringAfterUnsubscribePostgres(t *testing.T) {
	pool, orgID := openWatchPG(t)
	repo := repository.NewIntelWatchRepository(pool)
	subscription := seedWatchSubscription(t, repo, orgID, "contrato-2026-0001")
	ctx := context.Background()

	stopped, err := repo.Unsubscribe(ctx, orgID, subscription.ID, time.Now().UTC())
	if err != nil || !stopped {
		t.Fatalf("unsubscribe failed: stopped=%v err=%v", stopped, err)
	}

	dispatcher := &scriptedDispatcher{}
	worker := NewWatchReclaimWorker(NewConsumer(repo, dispatcher), fixtureProducer(t, orgID), time.Minute)
	report, err := worker.ReclaimOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Matched != 0 || report.Dispatched != 0 {
		t.Fatalf("an unsubscribed watcher still matched: %+v", report)
	}
	if attempts, _ := dispatcher.counts(); attempts != 0 {
		t.Fatal("the dispatcher was invoked for an unsubscribed watcher")
	}
}

// A dead IN_FLIGHT row is swept into a reviewable AMBIGUOUS state by the same
// worker, rather than staying an invisible stall.
func TestWatchReclaimParksAbandonedHandoffsPostgres(t *testing.T) {
	pool, orgID := openWatchPG(t)
	repo := repository.NewIntelWatchRepository(pool)
	subscription := seedWatchSubscription(t, repo, orgID, "contrato-2026-0001")
	ctx := context.Background()

	key := models.IntelWatchDeliveryKey{
		SubscriptionID: subscription.ID, EventIdentity: "evt-abandoned", ContentHash: "hash-abandoned",
	}
	past := time.Now().UTC().Add(-time.Hour)
	claim, err := repo.ClaimDelivery(ctx, key, past, time.Minute, 5)
	if err != nil || !claim.Granted {
		t.Fatalf("claim failed: %+v %v", claim, err)
	}
	if err := repo.MarkDeliveryInFlight(ctx, key, past); err != nil {
		t.Fatal(err)
	}

	worker := NewWatchReclaimWorker(NewConsumer(repo, &scriptedDispatcher{}), fixtureProducer(t, orgID), time.Minute)
	report, err := worker.ReclaimOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.ParkedStaleHandoffs == 0 {
		t.Fatalf("the abandoned handoff was not swept: %+v", report)
	}
	row, err := repo.GetDelivery(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if row == nil || row.State != models.IntelWatchDeliveryAmbiguous {
		t.Fatalf("abandoned handoff is not parked AMBIGUOUS: %+v", row)
	}
}

// Adversarial: one watcher whose dispatcher hangs must not starve the events
// behind it. The per-event budget is the guard, so prove the pass completes and
// the healthy watchers are still served.
type hangingDispatcher struct {
	mu        sync.Mutex
	hangFor   time.Duration
	hangUntil int
	seen      int
	delivered int
}

func (d *hangingDispatcher) DispatchWatchUpdate(ctx context.Context, delivery WatchDelivery) (WatchDispatchOutcome, error) {
	d.mu.Lock()
	d.seen++
	hang := d.seen <= d.hangUntil
	d.mu.Unlock()
	if hang {
		// Respect the context: a well-behaved dispatcher returns when its
		// budget expires. The point of the test is that a budget EXISTS.
		select {
		case <-time.After(d.hangFor):
		case <-ctx.Done():
			return WatchTransient, ctx.Err()
		}
		return WatchTransient, fmt.Errorf("dispatcher hung")
	}
	if delivery.BeforeHandoff != nil {
		if err := delivery.BeforeHandoff(ctx); err != nil {
			return WatchTransient, err
		}
	}
	d.mu.Lock()
	d.delivered++
	d.mu.Unlock()
	return WatchDelivered, nil
}

func (d *hangingDispatcher) counts() (seen, delivered int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.seen, d.delivered
}

func TestWatchReclaimOneHangingDispatcherDoesNotStarveTheOthersPostgres(t *testing.T) {
	pool, orgID := openWatchPG(t)
	repo := repository.NewIntelWatchRepository(pool)
	// Two subjects: the fixture's first two events hit subject 0001 and its
	// third hits 0002, so a hang on the first must not lose the third.
	seedWatchSubscription(t, repo, orgID, "contrato-2026-0001")
	seedWatchSubscription(t, repo, orgID, "contrato-2026-0002")

	dispatcher := &hangingDispatcher{hangFor: time.Hour, hangUntil: 1}
	worker := NewWatchReclaimWorker(NewConsumer(repo, dispatcher), fixtureProducer(t, orgID), time.Minute)
	// A short per-event budget keeps the test fast; production uses minutes.
	worker.eventBudget = 300 * time.Millisecond

	started := time.Now()
	report, err := worker.ReclaimOnce(context.Background())
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("the hung dispatcher was not surfaced")
	}
	// The pass must not have waited for the hour-long hang.
	if elapsed > 5*time.Second {
		t.Fatalf("one hung event blocked the whole pass for %s", elapsed)
	}
	if report.EventsSeen < 3 {
		t.Fatalf("the pass stopped early: %+v", report)
	}
	if report.Dispatched == 0 {
		t.Fatalf("the healthy watchers behind the hang were starved: %+v", report)
	}
	seen, delivered := dispatcher.counts()
	if seen < 2 {
		t.Fatalf("the dispatcher was only reached %d times; later events were skipped", seen)
	}
	if delivered == 0 {
		t.Fatal("nothing was delivered after the hang")
	}
}
