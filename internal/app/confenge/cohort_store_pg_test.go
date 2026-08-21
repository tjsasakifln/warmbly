package confenge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/models"
)

func testPostgresDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("WARMBLY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("WARMBLY_TEST_POSTGRES_DSN is not set")
	}
	return dsn
}

func openCohortPG(t *testing.T) (*pgxpool.Pool, BoundedCohortStore) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, testPostgresDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	applyBoundedCohortSchema(t, ctx, pool)
	return pool, NewPostgresCohortStore(pool)
}

func applyBoundedCohortSchema(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	up := filepath.Join(filepath.Dir(thisFile), "..", "..", "infrastructure", "db", "migrations", "000107_confenge_bounded_cohort.up.sql")
	b, err := os.ReadFile(up)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := pool.Exec(ctx, string(b)); err != nil {
		t.Fatalf("apply cohort schema: %v", err)
	}
}

func sampleGrant(t *testing.T, max int) *BoundedCohortAuthorization {
	t.Helper()
	now := time.Now().UTC()
	auth := &BoundedCohortAuthorization{
		ID: uuid.New(), OrganizationID: uuid.New(), ActorID: uuid.New(),
		AuthorizedAt: now, RepositorySHA: "sha-live", FeedSchemaVersion: models.OutreachSchemaV1,
		CohortID: "synthetic-cohort", CohortHash: HashCohortID("synthetic-cohort", "sha-live"),
		PolicyVersion:       BoundedCohortPolicyV1,
		AllowedRouteClasses: []string{RouteClassGenericCompany, RouteClassDirectPerson},
		MaxDailyVolume:      max, RecipientSetHash: HashRecipientSet([]string{"contato@empresa.com.br"}),
		ComposerVersion: ComposerVersion, EvidenceVersion: DefaultEvidenceVersion,
		TTL: time.Hour, ExpiresAt: now.Add(time.Hour),
	}
	auth.FrozenHashValue = auth.FrozenHash()
	return auth
}

func TestPostgresGrantSurvivesNewStoreInstance(t *testing.T) {
	ctx := context.Background()
	pool, store := openCohortPG(t)
	auth := sampleGrant(t, 5)
	if err := store.PutGrant(ctx, auth); err != nil {
		t.Fatal(err)
	}
	restarted := NewPostgresCohortStore(pool)
	got, err := restarted.GetGrant(ctx, auth.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.FrozenHash() != auth.FrozenHash() {
		t.Fatalf("grant must survive new store instance: %+v", got)
	}
}

func TestPostgresRevokeThenTransportFails(t *testing.T) {
	ctx := context.Background()
	_, store := openCohortPG(t)
	auth := sampleGrant(t, 5)
	if _, err := CreateBoundedCohortGrant(ctx, store, auth, true); err != nil {
		t.Fatal(err)
	}
	if err := RevokeBoundedCohortGrant(ctx, store, auth.ID, auth.ActorID, "operator-stop", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetGrant(ctx, auth.ID)
	if err != nil || got == nil || got.RevokedAt == nil {
		t.Fatalf("revoke must persist: err=%v got=%+v", err, got)
	}
	_, rerr := store.ReserveSlot(ctx, auth.ID, "msg-after-revoke", time.Now().UTC())
	if !errors.Is(rerr, ErrCohortGrantRevoked) {
		t.Fatalf("transport after revoke must fail, got %v", rerr)
	}
	view, err := ViewBoundedCohortGrant(ctx, store, auth.ID)
	if err != nil || view.RevokeReason != "operator-stop" {
		t.Fatalf("historical revoke event must be kept: %+v %v", view, err)
	}
}

func TestPostgresExpiryFailClosed(t *testing.T) {
	ctx := context.Background()
	_, store := openCohortPG(t)
	auth := sampleGrant(t, 5)
	auth.AuthorizedAt = time.Now().UTC().Add(-2 * time.Hour)
	auth.TTL = time.Hour
	auth.ExpiresAt = auth.AuthorizedAt.Add(auth.TTL)
	if err := store.PutGrant(ctx, auth); err != nil {
		t.Fatal(err)
	}
	_, err := store.ReserveSlot(ctx, auth.ID, "msg-expired", time.Now().UTC())
	if !errors.Is(err, ErrCohortGrantExpired) {
		t.Fatalf("clock after expiry must fail closed, got %v", err)
	}
}

func TestPostgresTwoProcessLastSlot(t *testing.T) {
	ctx := context.Background()
	pool, a := openCohortPG(t)
	b := NewPostgresCohortStore(pool)
	auth := sampleGrant(t, 1)
	if err := a.PutGrant(ctx, auth); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	errA := make(chan error, 1)
	errB := make(chan error, 1)
	go func() {
		defer wg.Done()
		_, err := a.ReserveSlot(ctx, auth.ID, "worker-a", time.Now().UTC())
		errA <- err
	}()
	go func() {
		defer wg.Done()
		_, err := b.ReserveSlot(ctx, auth.ID, "worker-b", time.Now().UTC())
		errB <- err
	}()
	wg.Wait()
	ea, eb := <-errA, <-errB
	ok, fail := 0, 0
	for _, err := range []error{ea, eb} {
		if err == nil {
			ok++
		} else if errors.Is(err, ErrCohortDailyCap) {
			fail++
		} else {
			t.Fatalf("unexpected reserve error: %v / %v", ea, eb)
		}
	}
	if ok != 1 || fail != 1 {
		t.Fatalf("last slot must admit exactly one worker, ok=%d fail=%d ea=%v eb=%v", ok, fail, ea, eb)
	}
	if a.SentToday(auth.ID, time.Now().UTC().Format("2006-01-02")) != 1 {
		t.Fatalf("occupied must be 1, got %d", a.SentToday(auth.ID, time.Now().UTC().Format("2006-01-02")))
	}
}

func TestPostgresReplayDoesNotConsumeTwoSlots(t *testing.T) {
	ctx := context.Background()
	_, store := openCohortPG(t)
	auth := sampleGrant(t, 2)
	if err := store.PutGrant(ctx, auth); err != nil {
		t.Fatal(err)
	}
	key := "email:campaign:same:contact:seq"
	first, err := store.ReserveSlot(ctx, auth.ID, key, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.ReserveSlot(ctx, auth.ID, key, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if !second.Already || second.Occupied != first.Occupied || second.Occupied != 1 {
		t.Fatalf("replay must be one reservation: first=%+v second=%+v", first, second)
	}
	if err := store.CommitSlot(ctx, auth.ID, key, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	third, err := store.ReserveSlot(ctx, auth.ID, key, time.Now().UTC())
	if err != nil || !third.Already || third.State != CohortSlotSent {
		t.Fatalf("committed replay must stay sent: %+v %v", third, err)
	}
	if store.SentToday(auth.ID, time.Now().UTC().Format("2006-01-02")) != 1 {
		t.Fatal("replay must not consume a second slot")
	}
}

func TestPostgresDBUnavailableFailClosed(t *testing.T) {
	store := NewPostgresCohortStore(nil)
	_, err := store.GetGrant(context.Background(), uuid.New())
	if !errors.Is(err, ErrCohortStoreUnavailable) {
		t.Fatalf("nil pool must fail closed, got %v", err)
	}
	_, err = store.ReserveSlot(context.Background(), uuid.New(), "k", time.Now().UTC())
	if !errors.Is(err, ErrCohortStoreUnavailable) {
		t.Fatalf("reserve on nil pool must fail closed, got %v", err)
	}
}

func TestPostgresBackendAndConsumerShareGrant(t *testing.T) {
	ctx := context.Background()
	pool, backend := openCohortPG(t)
	consumer := NewPostgresCohortStore(pool)
	auth := sampleGrant(t, 3)
	if _, err := CreateBoundedCohortGrant(ctx, backend, auth, true); err != nil {
		t.Fatal(err)
	}
	got, err := consumer.GetGrant(ctx, auth.ID)
	if err != nil || got == nil {
		t.Fatalf("consumer must see backend grant: %v %+v", err, got)
	}
	if got.FrozenHash() != auth.FrozenHash() {
		t.Fatal("backend and consumer must share identical authority")
	}
}

func TestPostgresReleaseFreesSlotCommitDoesNot(t *testing.T) {
	ctx := context.Background()
	_, store := openCohortPG(t)
	auth := sampleGrant(t, 1)
	if err := store.PutGrant(ctx, auth); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReserveSlot(ctx, auth.ID, "pre-transport", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseSlot(ctx, auth.ID, "pre-transport", "smtp_connect_failed"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReserveSlot(ctx, auth.ID, "other-msg", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitSlot(ctx, auth.ID, "other-msg", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseSlot(ctx, auth.ID, "other-msg", "too-late"); err != nil {
		t.Fatal(err)
	}
	_, err := store.ReserveSlot(ctx, auth.ID, "third", time.Now().UTC())
	if !errors.Is(err, ErrCohortDailyCap) {
		t.Fatalf("committed slot must not be released, got %v", err)
	}
}

func TestOperatorCreateRequiresConfirmAndHumanActor(t *testing.T) {
	ctx := context.Background()
	_, store := openCohortPG(t)
	auth := sampleGrant(t, 5)
	if _, err := CreateBoundedCohortGrant(ctx, store, auth, false); !errors.Is(err, ErrCohortNotConfirmed) {
		t.Fatalf("create without confirm: %v", err)
	}
	auth.ActorID = uuid.Nil
	if _, err := CreateBoundedCohortGrant(ctx, store, auth, true); !errors.Is(err, ErrCohortHumanActor) {
		t.Fatalf("create without actor: %v", err)
	}
}

func TestMemoryStoreNotUsedAsPostgresConstructor(t *testing.T) {
	if NewPostgresCohortStore(nil) == nil {
		t.Fatal("constructor must return a fail-closed store")
	}
	if _, ok := NewPostgresCohortStore(nil).(*postgresCohortStore); !ok {
		t.Fatal("production constructor must be postgresCohortStore")
	}
}
