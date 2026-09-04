package confenge

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

func openInboundOnlyPG(t *testing.T) (*pgxpool.Pool, uuid.UUID) {
	t.Helper()
	dsn := postgresTestDSN()
	if dsn == "" {
		t.Skip("WARMBLY_TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("inbound-only postgres unavailable: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("inbound-only postgres ping failed: %v", err)
	}
	ensureInboundOnlyColumn(t, pool)
	_, orgID := openHandRaisePGSeed(t, pool)
	return pool, orgID
}

func openHandRaisePGSeed(t *testing.T, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	userID, orgID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id,first_name,last_name,email) VALUES($1,'Inbound','Only',$2)`,
		userID, "inbound-only-"+userID.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (id,name,slug,owner_user_id) VALUES($1,'Inbound Only',$2,$3)`,
		orgID, "inbound-only-"+orgID.String(), userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, orgID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id=$1`, userID)
	})
	return userID, orgID
}

func ensureInboundOnlyColumn(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name='outreach_accounts' AND column_name='inbound_only')`).Scan(&exists); err != nil {
		t.Fatalf("inbound_only column probe: %v", err)
	}
	if exists {
		return
	}
	up, err := os.ReadFile(inboundOnlyMigrationPath("up"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("apply 000145 up: %v", err)
	}
}

func inboundOnlyMigrationPath(kind string) string {
	return filepath.Join("..", "..", "infrastructure", "db", "migrations", "000145_confenge_inbound_only."+kind+".sql")
}

func TestPGNetNewAdmitCreatesUniqueInboundOnlyNotOutbound(t *testing.T) {
	pool, orgID := openInboundOnlyPG(t)
	t.Cleanup(pool.Close)
	repo := repository.NewOutreachRepository(pool)
	svc := NewService(Config{Enabled: true, RequireHumanApproval: true}, repo, nil).(*service)
	svc.WireIntelWatchSubscriptions(&fakeWatchSubs{})

	env := validWebIntent(intel.WebIntentRequestHumanReview)
	env.ContactEmail = "pg-net-new@example.com"
	res, xerr := svc.IngestWebIntent(context.Background(), orgID, env, time.Now().UTC())
	if xerr != nil {
		t.Fatalf("pg admit dropped: %v", xerr)
	}
	if res.ActionID == nil || !res.InboundOnly || res.OutboundEligible {
		t.Fatalf("pg admit: %+v", res)
	}
	acc, err := repo.GetAccount(context.Background(), orgID, *res.AccountID)
	if err != nil || acc == nil {
		t.Fatalf("account: %v", err)
	}
	if !models.AccountIsInboundOnly(acc) {
		t.Fatalf("source_system=%q inbound_only=%v", acc.SourceSystem, acc.InboundOnly)
	}
	if acc.TargetFitEligible || acc.EmailSendReady {
		t.Fatal("inbound_only row is outbound-eligible")
	}
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM outreach_accounts WHERE organization_id=$1 AND inbound_only`, orgID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("unique inbound-only identity failed: %d rows", n)
	}
	_, err = pool.Exec(context.Background(),
		`UPDATE outreach_accounts SET target_fit_eligible=true WHERE organization_id=$1 AND inbound_only`, orgID)
	if err == nil {
		t.Fatal("inbound_only CHECK allowed target_fit_eligible=true")
	}
}

func TestPGInboundOnlyReplayAndRaceConverge(t *testing.T) {
	pool, orgID := openInboundOnlyPG(t)
	t.Cleanup(pool.Close)
	repo := repository.NewOutreachRepository(pool)
	svc := NewService(Config{Enabled: true, RequireHumanApproval: true}, repo, nil).(*service)
	svc.WireIntelWatchSubscriptions(&fakeWatchSubs{})
	env := validWebIntent(intel.WebIntentRequestDeepDive)
	env.ContactEmail = "pg-race@example.com"
	now := time.Now().UTC()

	var first *WebIntentResult
	var mu sync.Mutex
	var wg sync.WaitGroup
	errCh := make(chan string, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, xerr := svc.IngestWebIntent(context.Background(), orgID, env, now)
			if xerr != nil {
				errCh <- xerr.Error()
				return
			}
			if res.ActionID == nil {
				errCh <- "dropped"
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if first == nil {
				first = res
				return
			}
			if *res.ActionID != *first.ActionID || *res.AccountID != *first.AccountID {
				errCh <- "duplicate identity"
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Fatalf("race: %s", msg)
	}
	if first == nil {
		t.Fatal("no admission survived the race")
	}
	var accounts, actions int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM outreach_accounts WHERE organization_id=$1 AND inbound_only`, orgID).Scan(&accounts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM outreach_commercial_actions WHERE organization_id=$1`, orgID).Scan(&actions); err != nil {
		t.Fatal(err)
	}
	if accounts != 1 || actions != 1 {
		t.Fatalf("race produced accounts=%d actions=%d", accounts, actions)
	}
}

func TestPGInboundOnlyMigrationForwardBackwardForward(t *testing.T) {
	dsn := postgresTestDSN()
	if dsn == "" {
		t.Skip("WARMBLY_TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("inbound-only postgres unavailable: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("inbound-only postgres ping failed: %v", err)
	}
	up, err := os.ReadFile(inboundOnlyMigrationPath("up"))
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile(inboundOnlyMigrationPath("down"))
	if err != nil {
		t.Fatal(err)
	}
	ensureInboundOnlyColumn(t, pool)
	for i, sql := range []string{string(down), string(up), string(down), string(up)} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("migration step %d: %v", i, err)
		}
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name='outreach_accounts' AND column_name='inbound_only')`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("inbound_only column missing after forward-backward-forward")
	}
	var checkExists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM pg_constraint WHERE conname='outreach_accounts_inbound_only_not_outbound')`).Scan(&checkExists); err != nil {
		t.Fatal(err)
	}
	if !checkExists {
		t.Fatal("inbound_only not-outbound CHECK missing after remigration")
	}
}
