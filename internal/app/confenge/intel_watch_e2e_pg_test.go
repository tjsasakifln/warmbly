package confenge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/app/confenge/liveintel"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// TEST C, end to end and closed.
//
// CONFENGE_WEB monitor request -> subscription -> NEW_OPPORTUNITY event from the
// fixture producer -> INTEL_WATCH consumer -> the REAL dispatcher over the REAL
// SMTP transport against a real socket -> the REAL Postgres delivery ledger ->
// replay of the identical event delivers nothing -> unsubscribe stops the lane.
//
// Nothing in this path is a stub except the MTA on the far side of the socket,
// which is the one thing this repository cannot own.

func openWatchE2EPG(t *testing.T) (*pgxpool.Pool, uuid.UUID) {
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
	if _, err := pool.Exec(ctx, `INSERT INTO users (id,first_name,last_name,email) VALUES($1,'Watch','E2E',$2)`,
		userID, fmt.Sprintf("watch-e2e-%s@example.test", userID)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (id,name,slug,owner_user_id) VALUES($1,'Watch E2E',$2,$3)`,
		orgID, "watch-e2e-"+orgID.String(), userID); err != nil {
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

// realWatchLane assembles the production objects: the SMTP transport pointed at
// a local socket, the real dispatcher adapter, the real Postgres ledger and the
// fixture event producer.
type realWatchLane struct {
	repo     repository.IntelWatchRepository
	consumer *liveintel.Consumer
	worker   *liveintel.WatchReclaimWorker
	server   *fakeMTA
	producer *liveintel.FixtureEventProducer
}

func newRealWatchLane(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, dropAfterData bool) *realWatchLane {
	t.Helper()
	return newRealWatchLaneOn(t, pool, orgID, startFakeMTA(t, dropAfterData))
}

func newRealWatchLaneOn(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, server *fakeMTA) *realWatchLane {
	t.Helper()
	mailboxID := uuid.New()
	emails := stubEmailRepo{
		account: &models.Email{ID: mailboxID, Email: "sender@example.test", Name: "CONFENGE"},
		creds: &repository.SMTPCredentials{
			SMTPHost: "127.0.0.1", SMTPPort: server.port(),
			SMTPUser: "sender@example.test", SMTPPassword: "secret",
		},
	}
	// The SAME transport constructor the first-touch fast lane is wired with.
	dispatcher := NewIntelWatchDispatcher(NewSMTPFirstTouchTransport(emails),
		func(context.Context, uuid.UUID) (uuid.UUID, error) { return mailboxID, nil })

	repo := repository.NewIntelWatchRepository(pool)
	consumer := liveintel.NewConsumer(repo, dispatcher)
	producer, err := liveintel.NewFixtureEventProducer(
		filepath.Join("liveintel", "testdata", "intel_watch_opportunity_events.json"))
	if err != nil {
		t.Fatal(err)
	}
	producer.BindOrganization(orgID)
	return &realWatchLane{
		repo: repo, consumer: consumer, server: server, producer: producer,
		worker: liveintel.NewWatchReclaimWorker(consumer, producer, time.Minute),
	}
}

// requestMonitoring is the CONFENGE_WEB half: a person asked to be told when
// this subject changes, and the consent record says so explicitly.
func requestMonitoring(t *testing.T, repo repository.IntelWatchRepository, orgID uuid.UUID, email, subject string) models.IntelWatchSubscription {
	t.Helper()
	consentAt := time.Now().UTC().Add(-time.Minute)
	sub, err := repo.CreateOrReactivateSubscription(context.Background(), &models.IntelWatchSubscription{
		OrganizationID: orgID, ContactEmail: email,
		IntentKind: models.IntelWatchIntentNewOpportunity, SubjectKey: subject,
		Topic: "novas licitacoes", Cadence: models.IntelWatchCadenceImmediate,
		// Subscription consent, explicitly its own basis. This is NOT
		// cold-outreach admission and must never be read as such.
		ConsentSource: "confenge_web_monitor_request", ConsentAt: &consentAt,
		ConsentProvenanceOK: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return *sub
}

func TestIntelWatchClosedPathFromMonitorRequestToUnsubscribePostgres(t *testing.T) {
	t.Setenv(liveintel.EnvUnsubscribeSecret, intelWatchTestSecret)
	pool, orgID := openWatchE2EPG(t)
	lane := newRealWatchLane(t, pool, orgID, false)
	ctx := context.Background()

	subject := "contrato-2026-0001"
	sub := requestMonitoring(t, lane.repo, orgID, "watcher@example.test", subject)
	if !sub.Active() || !sub.ConsentProvenanceOK {
		t.Fatalf("the monitor request did not record usable consent: %+v", sub)
	}

	// One NEW_OPPORTUNITY event for the watched subject, from the fixture
	// producer -- the same adapter production is wired with.
	var opportunity liveintel.OpportunityEvent
	for _, event := range lane.producer.Events() {
		if event.EventType == liveintel.EventNewOpportunity && event.SubjectKey == subject {
			opportunity = event
			break
		}
	}
	if opportunity.EventID == "" {
		t.Fatal("the fixture has no NEW_OPPORTUNITY event for the watched subject")
	}

	result, err := lane.consumer.HandleEvent(ctx, opportunity)
	if err != nil {
		t.Fatalf("watch delivery failed: %v", err)
	}
	if result.Matched != 1 || result.Dispatched != 1 {
		t.Fatalf("the event did not reach exactly one watcher: %+v", result)
	}
	if !lane.server.reachedData() {
		t.Fatal("the delivery never reached SMTP DATA; nothing was actually sent")
	}

	// The ledger is terminal.
	key := models.IntelWatchDeliveryKey{
		SubscriptionID: sub.ID, EventIdentity: opportunity.EventID, ContentHash: opportunity.ContentHash(),
	}
	row, err := lane.repo.GetDelivery(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if row == nil || row.State != models.IntelWatchDeliveryDispatched || row.SentAt == nil {
		t.Fatalf("the delivery is not terminally recorded: %+v", row)
	}

	// Replay of the IDENTICAL event delivers nothing new.
	replay, err := lane.consumer.HandleEvent(ctx, opportunity)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Dispatched != 0 || replay.Deduped != 1 {
		t.Fatalf("replaying the identical event was not a no-op: %+v", replay)
	}
	after, err := lane.repo.GetDelivery(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if after.Attempts != row.Attempts || !after.SentAt.Equal(*row.SentAt) {
		t.Fatalf("replay mutated a terminal delivery: %+v -> %+v", row, after)
	}

	// A DIFFERENT event for the same subject is a different notification and
	// must be delivered, so dedup is not just "one mail per subject ever".
	var changed liveintel.OpportunityEvent
	for _, event := range lane.producer.Events() {
		if event.SubjectKey == subject && event.EventID != opportunity.EventID {
			if ok, _ := event.Validate(); ok {
				changed = event
				break
			}
		}
	}
	if changed.EventID == "" {
		t.Fatal("the fixture has no second valid event for this subject")
	}
	second, err := lane.consumer.HandleEvent(ctx, changed)
	if err != nil {
		t.Fatal(err)
	}
	if second.Dispatched != 1 {
		t.Fatalf("a genuinely new event was not delivered: %+v", second)
	}

	// The one-click opt-out link must be mintable and verifiable.
	optOut := liveintel.UnsubscribeURL(orgID, sub.ID)
	if optOut == "" {
		t.Fatal("no opt-out link could be minted for a delivered subscription")
	}
	if !liveintel.VerifyUnsubscribeToken(orgID, sub.ID, liveintel.UnsubscribeToken(orgID, sub.ID)) {
		t.Fatal("the minted opt-out token does not verify")
	}

	// Unsubscribe, then a further event reaches nobody.
	stopped, err := lane.repo.Unsubscribe(ctx, orgID, sub.ID, time.Now().UTC())
	if err != nil || !stopped {
		t.Fatalf("unsubscribe failed: stopped=%v err=%v", stopped, err)
	}
	third := opportunity
	third.EventID = "evt-after-unsubscribe"
	third.Payload = map[string]string{"title": "algo novo depois do opt-out"}
	post, err := lane.consumer.HandleEvent(ctx, third)
	if err != nil {
		t.Fatal(err)
	}
	if post.Matched != 0 || post.Dispatched != 0 {
		t.Fatalf("an unsubscribed watcher was still served: %+v", post)
	}
	// The consent record survives the opt-out: both are evidence.
	if still, err := lane.repo.GetActiveSubscription(ctx, orgID, sub.ID); err != nil || still != nil {
		t.Fatalf("an unsubscribed row is still active: %+v %v", still, err)
	}
}

// TEST D over the real transport. A genuine 4xx at the sender stage -- before
// the recipient stage and long before DATA -- must leave the notification owed,
// and the reclaim worker must deliver it on a later pass with no human
// replaying anything.
func TestIntelWatchRealSocketTransientFailureIsRecoveredAutomaticallyPostgres(t *testing.T) {
	t.Setenv(liveintel.EnvUnsubscribeSecret, intelWatchTestSecret)
	pool, orgID := openWatchE2EPG(t)
	// The fixture carries two valid events for this subject, so the first pass
	// opens two sessions. Fail both, then let the provider recover.
	server := startFakeMTAWithTransientSessions(t, 2)
	lane := newRealWatchLaneOn(t, pool, orgID, server)
	ctx := context.Background()

	subject := "contrato-2026-0001"
	sub := requestMonitoring(t, lane.repo, orgID, "transient@example.test", subject)

	first, err := lane.worker.ReclaimOnce(ctx)
	if err == nil {
		t.Fatal("a transient provider failure must be surfaced, not swallowed")
	}
	if first.Dispatched != 0 {
		t.Fatalf("a failed delivery was recorded as delivered: %+v", first)
	}
	if first.Retryable == 0 {
		t.Fatalf("the notification was not left retryable: %+v", first)
	}
	if server.reachedData() {
		t.Fatal("SMTP DATA started despite a 4xx at the sender stage")
	}
	// The ledger must show a claimable PENDING row, not a terminal one.
	pending := 0
	for _, event := range lane.producer.Events() {
		if event.SubjectKey != subject {
			continue
		}
		if ok, _ := event.Validate(); !ok {
			continue
		}
		row, rerr := lane.repo.GetDelivery(ctx, models.IntelWatchDeliveryKey{
			SubscriptionID: sub.ID, EventIdentity: event.EventID, ContentHash: event.ContentHash(),
		})
		if rerr != nil {
			t.Fatal(rerr)
		}
		if row == nil || row.State != models.IntelWatchDeliveryPending {
			t.Fatalf("event %s is not claimable PENDING: %+v", event.EventID, row)
		}
		pending++
	}
	if pending == 0 {
		t.Fatal("nothing was left pending")
	}

	// No human touches anything between the passes. The provider has recovered.
	second, err := lane.worker.ReclaimOnce(ctx)
	if err != nil {
		t.Fatalf("the recovery pass still failed: %v", err)
	}
	if second.Dispatched == 0 {
		t.Fatalf("the reclaim worker delivered nothing after recovery: %+v", second)
	}
	if !server.reachedData() {
		t.Fatal("the recovery pass never reached SMTP DATA")
	}
	// Terminal, and only once per notification.
	delivered := 0
	for _, event := range lane.producer.Events() {
		if event.SubjectKey != subject {
			continue
		}
		if ok, _ := event.Validate(); !ok {
			continue
		}
		row, rerr := lane.repo.GetDelivery(ctx, models.IntelWatchDeliveryKey{
			SubscriptionID: sub.ID, EventIdentity: event.EventID, ContentHash: event.ContentHash(),
		})
		if rerr != nil {
			t.Fatal(rerr)
		}
		if row == nil || row.State != models.IntelWatchDeliveryDispatched {
			t.Fatalf("event %s did not reach DISPATCHED: %+v", event.EventID, row)
		}
		delivered++
	}
	if delivered != pending {
		t.Fatalf("recovered %d of %d owed notifications", delivered, pending)
	}
	if server.dataAttempts() != delivered {
		t.Fatalf("the provider saw %d message bodies for %d notifications",
			server.dataAttempts(), delivered)
	}

	// A third pass changes nothing: everything is terminal.
	third, err := lane.worker.ReclaimOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if third.Dispatched != 0 || server.dataAttempts() != delivered {
		t.Fatalf("a terminal delivery was re-sent on a later pass: %+v", third)
	}
}

// TEST E over the real transport. A socket that dies at end-of-DATA must park
// the delivery, and no later pass -- including the reclaim worker, whose whole
// job is retrying -- may resend it.
func TestIntelWatchRealSocketAmbiguityIsNeverBlindlyResentPostgres(t *testing.T) {
	t.Setenv(liveintel.EnvUnsubscribeSecret, intelWatchTestSecret)
	pool, orgID := openWatchE2EPG(t)
	lane := newRealWatchLane(t, pool, orgID, true) // drop the connection after DATA
	ctx := context.Background()

	subject := "contrato-2026-0001"
	sub := requestMonitoring(t, lane.repo, orgID, "ambiguous@example.test", subject)

	first, err := lane.worker.ReclaimOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.Parked == 0 {
		t.Fatalf("a real end-of-DATA drop was not parked: %+v", first)
	}
	if first.Dispatched != 0 {
		t.Fatalf("an ambiguous delivery was recorded as delivered: %+v", first)
	}
	if !lane.server.reachedData() {
		t.Fatal("the transport never reached DATA; the test proved nothing")
	}

	// Every parked row must be AMBIGUOUS, not retryable.
	parkedKeys := 0
	for _, event := range lane.producer.Events() {
		if event.SubjectKey != subject {
			continue
		}
		if ok, _ := event.Validate(); !ok {
			continue
		}
		row, err := lane.repo.GetDelivery(ctx, models.IntelWatchDeliveryKey{
			SubscriptionID: sub.ID, EventIdentity: event.EventID, ContentHash: event.ContentHash(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if row == nil || row.State != models.IntelWatchDeliveryAmbiguous {
			t.Fatalf("event %s is not parked AMBIGUOUS: %+v", event.EventID, row)
		}
		parkedKeys++
	}
	if parkedKeys == 0 {
		t.Fatal("nothing was parked")
	}

	// The reclaim worker runs again. It must not re-attempt a parked delivery.
	attemptsBefore := lane.server.dataAttempts()
	second, err := lane.worker.ReclaimOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.Dispatched != 0 {
		t.Fatalf("the reclaim worker resent a parked delivery: %+v", second)
	}
	if lane.server.dataAttempts() != attemptsBefore {
		t.Fatalf("the provider was contacted again for a parked delivery: %d -> %d",
			attemptsBefore, lane.server.dataAttempts())
	}
}
