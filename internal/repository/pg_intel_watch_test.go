package repository

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
)

// openIntelWatchPG opens a pool against the test database and seeds one
// organization, cleaned up at the end. It skips when no DSN is configured.
func openIntelWatchPG(t *testing.T) (*pgxpool.Pool, uuid.UUID) {
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
	if _, err = pool.Exec(ctx, `INSERT INTO users (id,first_name,last_name,email) VALUES($1,'Intel','Watch',$2)`,
		userID, fmt.Sprintf("intel-watch-%s@example.test", userID)); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO organizations (id,name,slug,owner_user_id) VALUES($1,'Intel Watch',$2,$3)`,
		orgID, "intel-watch-"+orgID.String(), userID); err != nil {
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

func intelWatchFixture(orgID uuid.UUID) *models.IntelWatchSubscription {
	consentAt := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	return &models.IntelWatchSubscription{
		OrganizationID: orgID, ContactEmail: "Watcher@Example.COM",
		IntentKind: models.IntelWatchIntentDeadlineChanged, SubjectKey: "contrato-2026-0001",
		Topic: "prazos", Cadence: models.IntelWatchCadenceImmediate,
		ConsentSource: "web_form", ConsentAt: &consentAt, ConsentProvenanceOK: true,
	}
}

// Re-subscribing is idempotent on (org, contact, subject, intent) and revives a
// prior opt-out rather than inserting a twin row.
func TestIntelWatchCreateOrReactivateIsIdempotent(t *testing.T) {
	pool, orgID := openIntelWatchPG(t)
	ctx := context.Background()
	repo := NewIntelWatchRepository(pool)

	first, err := repo.CreateOrReactivateSubscription(ctx, intelWatchFixture(orgID))
	if err != nil || first == nil {
		t.Fatalf("create: %+v %v", first, err)
	}
	if first.ContactEmail != "watcher@example.com" {
		t.Fatalf("contact email was not normalized: %q", first.ContactEmail)
	}

	repeat := intelWatchFixture(orgID)
	repeat.Topic = "prazos e aditivos"
	repeat.Cadence = models.IntelWatchCadenceDaily
	second, err := repo.CreateOrReactivateSubscription(ctx, repeat)
	if err != nil || second == nil {
		t.Fatalf("re-subscribe: %+v %v", second, err)
	}
	if second.ID != first.ID {
		t.Fatalf("re-subscribe created a twin row: %s vs %s", second.ID, first.ID)
	}
	if second.Cadence != models.IntelWatchCadenceDaily || second.Topic != "prazos e aditivos" {
		t.Fatalf("re-subscribe did not update the row: %+v", second)
	}

	if stopped, err := repo.Unsubscribe(ctx, orgID, first.ID, time.Now().UTC()); err != nil || !stopped {
		t.Fatalf("unsubscribe: stopped=%v err=%v", stopped, err)
	}
	revived, err := repo.CreateOrReactivateSubscription(ctx, intelWatchFixture(orgID))
	if err != nil || revived == nil {
		t.Fatal(err)
	}
	if revived.ID != first.ID || revived.UnsubscribedAt != nil {
		t.Fatalf("re-subscribe did not revive the same row: %+v", revived)
	}
}

// The subject read path is organization-scoped and skips opted-out rows.
func TestIntelWatchLookupIsOrgScopedAndSkipsOptOuts(t *testing.T) {
	pool, orgID := openIntelWatchPG(t)
	_, otherOrg := openIntelWatchPG(t)
	ctx := context.Background()
	repo := NewIntelWatchRepository(pool)

	mine, err := repo.CreateOrReactivateSubscription(ctx, intelWatchFixture(orgID))
	if err != nil {
		t.Fatal(err)
	}
	theirs := intelWatchFixture(otherOrg)
	theirs.ContactEmail = "other@example.com"
	if _, err := repo.CreateOrReactivateSubscription(ctx, theirs); err != nil {
		t.Fatal(err)
	}

	active, err := repo.ListActiveSubscriptionsBySubject(ctx, orgID, "contrato-2026-0001")
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].ID != mine.ID {
		t.Fatalf("subject lookup leaked across organizations: %+v", active)
	}
	if got, err := repo.GetActiveSubscription(ctx, otherOrg, mine.ID); err != nil || got != nil {
		t.Fatalf("a foreign organization read the subscription: %+v %v", got, err)
	}

	if _, err := repo.Unsubscribe(ctx, orgID, mine.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if again, err := repo.Unsubscribe(ctx, orgID, mine.ID, time.Now().UTC()); err != nil || again {
		t.Fatalf("a repeat unsubscribe reported a change: again=%v err=%v", again, err)
	}
	after, err := repo.ListActiveSubscriptionsBySubject(ctx, orgID, "contrato-2026-0001")
	if err != nil || len(after) != 0 {
		t.Fatalf("an opted-out subscription still matched: %+v %v", after, err)
	}
	if got, err := repo.GetActiveSubscription(ctx, orgID, mine.ID); err != nil || got != nil {
		t.Fatalf("an opted-out subscription is still active: %+v %v", got, err)
	}
}

// A delivered watch is terminal: replaying the identical event is refused, and
// changed content is a separate, still-deliverable row.
func TestIntelWatchDeliveryReplayIsANoOp(t *testing.T) {
	pool, orgID := openIntelWatchPG(t)
	ctx := context.Background()
	repo := NewIntelWatchRepository(pool)
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

	sub, err := repo.CreateOrReactivateSubscription(ctx, intelWatchFixture(orgID))
	if err != nil {
		t.Fatal(err)
	}
	key := models.IntelWatchDeliveryKey{SubscriptionID: sub.ID, EventIdentity: "evt-1", ContentHash: "hash-a"}

	claim, err := repo.ClaimDelivery(ctx, key, now, time.Minute, 5)
	if err != nil || !claim.Granted {
		t.Fatalf("first claim: %+v err=%v", claim, err)
	}
	// A second claimant cannot take a live claim.
	held, err := repo.ClaimDelivery(ctx, key, now, time.Minute, 5)
	if err != nil || held.Granted || held.Reason != models.IntelWatchClaimHeldElsewhere {
		t.Fatalf("a live claim was stolen: %+v err=%v", held, err)
	}
	if err := repo.MarkDeliveryInFlight(ctx, key, now); err != nil {
		t.Fatal(err)
	}
	if settled, err := repo.SettleDelivery(ctx, key, models.IntelWatchDeliveryDispatched, "", now); err != nil || !settled {
		t.Fatalf("settle: settled=%v err=%v", settled, err)
	}

	replay, err := repo.ClaimDelivery(ctx, key, now.Add(time.Hour), time.Minute, 5)
	if err != nil || replay.Granted || replay.Reason != models.IntelWatchClaimAlreadyDispatched {
		t.Fatalf("a delivered watch was claimable again: %+v err=%v", replay, err)
	}
	delivered, err := repo.GetDelivery(ctx, key)
	if err != nil || delivered == nil || delivered.SentAt == nil {
		t.Fatalf("delivered row: %+v err=%v", delivered, err)
	}

	changed := models.IntelWatchDeliveryKey{SubscriptionID: sub.ID, EventIdentity: "evt-1", ContentHash: "hash-b"}
	if claim, err := repo.ClaimDelivery(ctx, changed, now, time.Minute, 5); err != nil || !claim.Granted {
		t.Fatalf("changed content must be claimable: %+v err=%v", claim, err)
	}

	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM confenge_intel_watch_dedup WHERE subscription_id=$1`, sub.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("delivery ledger holds %d rows, want 2", rows)
	}
}

// A claim released before any handoff stays deliverable; a fenced one never is,
// and the reconciler turns an abandoned fence into an explicit parked state.
func TestIntelWatchDeliveryLedgerStateMachine(t *testing.T) {
	pool, orgID := openIntelWatchPG(t)
	ctx := context.Background()
	repo := NewIntelWatchRepository(pool)
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

	sub, err := repo.CreateOrReactivateSubscription(ctx, intelWatchFixture(orgID))
	if err != nil {
		t.Fatal(err)
	}

	// Released before the fence: still deliverable.
	retry := models.IntelWatchDeliveryKey{SubscriptionID: sub.ID, EventIdentity: "evt-retry", ContentHash: "hash"}
	if claim, err := repo.ClaimDelivery(ctx, retry, now, time.Minute, 5); err != nil || !claim.Granted {
		t.Fatalf("claim: %+v err=%v", claim, err)
	}
	if released, err := repo.ReleaseDelivery(ctx, retry, "composer unavailable", now); err != nil || !released {
		t.Fatalf("release: released=%v err=%v", released, err)
	}
	again, err := repo.ClaimDelivery(ctx, retry, now, time.Minute, 5)
	if err != nil || !again.Granted || again.Attempts != 2 {
		t.Fatalf("a released delivery was not retryable: %+v err=%v", again, err)
	}

	// Fenced then abandoned: never re-claimed, swept into AMBIGUOUS.
	fenced := models.IntelWatchDeliveryKey{SubscriptionID: sub.ID, EventIdentity: "evt-fenced", ContentHash: "hash"}
	if claim, err := repo.ClaimDelivery(ctx, fenced, now, time.Minute, 5); err != nil || !claim.Granted {
		t.Fatalf("claim: %+v err=%v", claim, err)
	}
	if err := repo.MarkDeliveryInFlight(ctx, fenced, now); err != nil {
		t.Fatal(err)
	}
	if released, err := repo.ReleaseDelivery(ctx, fenced, "late", now); err != nil || released {
		t.Fatalf("a fenced delivery must not be releasable: released=%v err=%v", released, err)
	}
	stale := now.Add(2 * time.Minute)
	blocked, err := repo.ClaimDelivery(ctx, fenced, stale, time.Minute, 5)
	if err != nil || blocked.Granted || blocked.Reason != models.IntelWatchClaimParkedAmbiguous {
		t.Fatalf("an abandoned fence was re-claimed: %+v err=%v", blocked, err)
	}
	swept, err := repo.ExpireStaleDeliveryHandoffs(ctx, stale)
	if err != nil || swept != 1 {
		t.Fatalf("sweep: swept=%d err=%v", swept, err)
	}
	parked, err := repo.GetDelivery(ctx, fenced)
	if err != nil || parked == nil || parked.State != models.IntelWatchDeliveryAmbiguous {
		t.Fatalf("parked row: %+v err=%v", parked, err)
	}

	// The attempt budget is enforced by the claim itself.
	bounded := models.IntelWatchDeliveryKey{SubscriptionID: sub.ID, EventIdentity: "evt-bounded", ContentHash: "hash"}
	for i := 0; i < 2; i++ {
		claim, err := repo.ClaimDelivery(ctx, bounded, now, time.Minute, 2)
		if err != nil || !claim.Granted {
			t.Fatalf("attempt %d: %+v err=%v", i+1, claim, err)
		}
		if _, err := repo.ReleaseDelivery(ctx, bounded, "transient", now); err != nil {
			t.Fatal(err)
		}
	}
	exhausted, err := repo.ClaimDelivery(ctx, bounded, now, time.Minute, 2)
	if err != nil || exhausted.Granted || exhausted.Reason != models.IntelWatchClaimAttemptsExhausted {
		t.Fatalf("the attempt budget was not enforced: %+v err=%v", exhausted, err)
	}
}

// Out-of-set and incomplete values are refused at the application boundary,
// with a readable error rather than a CHECK violation.
func TestIntelWatchRejectsOutOfSetValues(t *testing.T) {
	repo := NewIntelWatchRepository(nil)
	ctx := context.Background()
	orgID := uuid.New()

	cases := map[string]*models.IntelWatchSubscription{
		"nil":              nil,
		"no organization":  {ContactEmail: "a@b.com", IntentKind: models.IntelWatchIntentNewOpportunity, SubjectKey: "s", Cadence: models.IntelWatchCadenceDaily},
		"no contact":       {OrganizationID: orgID, IntentKind: models.IntelWatchIntentNewOpportunity, SubjectKey: "s", Cadence: models.IntelWatchCadenceDaily},
		"no subject":       {OrganizationID: orgID, ContactEmail: "a@b.com", IntentKind: models.IntelWatchIntentNewOpportunity, Cadence: models.IntelWatchCadenceDaily},
		"unknown intent":   {OrganizationID: orgID, ContactEmail: "a@b.com", IntentKind: "GUESS", SubjectKey: "s", Cadence: models.IntelWatchCadenceDaily},
		"unknown cadence":  {OrganizationID: orgID, ContactEmail: "a@b.com", IntentKind: models.IntelWatchIntentNewOpportunity, SubjectKey: "s", Cadence: "hourly"},
		"no cadence given": {OrganizationID: orgID, ContactEmail: "a@b.com", IntentKind: models.IntelWatchIntentNewOpportunity, SubjectKey: "s"},
	}
	for name, sub := range cases {
		if _, err := repo.CreateOrReactivateSubscription(ctx, sub); err == nil {
			t.Fatalf("%s: was accepted", name)
		}
	}

	// Every ledger method validates its key before touching the pool, so a bad
	// key is a readable error rather than a nil-pool panic.
	now := time.Now().UTC()
	for name, key := range map[string]models.IntelWatchDeliveryKey{
		"no membership": {EventIdentity: "evt", ContentHash: "hash"},
		"no identity":   {SubscriptionID: uuid.New(), ContentHash: "hash"},
		"no hash":       {SubscriptionID: uuid.New(), EventIdentity: "evt"},
	} {
		if _, err := repo.ClaimDelivery(ctx, key, now, time.Minute, 5); err == nil {
			t.Fatalf("%s: claim was accepted", name)
		}
		if err := repo.MarkDeliveryInFlight(ctx, key, now); err == nil {
			t.Fatalf("%s: fence was accepted", name)
		}
		if _, err := repo.SettleDelivery(ctx, key, models.IntelWatchDeliveryDispatched, "", now); err == nil {
			t.Fatalf("%s: settle was accepted", name)
		}
		if _, err := repo.ReleaseDelivery(ctx, key, "", now); err == nil {
			t.Fatalf("%s: release was accepted", name)
		}
		if _, err := repo.GetDelivery(ctx, key); err == nil {
			t.Fatalf("%s: read was accepted", name)
		}
	}

	valid := models.IntelWatchDeliveryKey{SubscriptionID: uuid.New(), EventIdentity: "evt", ContentHash: "hash"}
	if _, err := repo.ClaimDelivery(ctx, valid, now, 0, 5); err == nil {
		t.Fatal("a claim without a lease was accepted")
	}
	if _, err := repo.ClaimDelivery(ctx, valid, now, time.Minute, 0); err == nil {
		t.Fatal("a claim without an attempt budget was accepted")
	}
	// Only a terminal state may settle a delivery.
	for _, state := range []string{models.IntelWatchDeliveryPending, models.IntelWatchDeliveryInFlight, "GUESS"} {
		if _, err := repo.SettleDelivery(ctx, valid, state, "", now); err == nil {
			t.Fatalf("settling in %q was accepted", state)
		}
	}
}

// The conditional upsert IS the concurrency guard. Several connections racing
// the identical delivery must produce exactly one granted claim, so two
// consumers can never both write to the same watcher about the same change.
func TestIntelWatchDeliveryClaimIsExclusiveUnderConcurrency(t *testing.T) {
	pool, orgID := openIntelWatchPG(t)
	ctx := context.Background()
	repo := NewIntelWatchRepository(pool)
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

	sub, err := repo.CreateOrReactivateSubscription(ctx, intelWatchFixture(orgID))
	if err != nil {
		t.Fatal(err)
	}
	key := models.IntelWatchDeliveryKey{SubscriptionID: sub.ID, EventIdentity: "evt-race", ContentHash: "hash"}

	const racers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	granted := 0
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claim, err := repo.ClaimDelivery(ctx, key, now, time.Minute, racers+1)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				t.Errorf("claim: %v", err)
				return
			}
			if claim.Granted {
				granted++
			}
		}()
	}
	close(start)
	wg.Wait()
	if granted != 1 {
		t.Fatalf("%d racers were granted %d claims on one delivery", racers, granted)
	}

	var rows, attempts int
	if err := pool.QueryRow(ctx,
		`SELECT count(*), max(attempts) FROM confenge_intel_watch_dedup WHERE subscription_id=$1 AND event_identity=$2`,
		sub.ID, key.EventIdentity).Scan(&rows, &attempts); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || attempts != 1 {
		t.Fatalf("the race left %d rows with %d attempts", rows, attempts)
	}
}
