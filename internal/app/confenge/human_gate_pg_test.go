package confenge

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/warmbly/warmbly/internal/errx"
)

func applyHumanGateSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "infrastructure", "db", "migrations", "000116_confenge_human_gate.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(context.Background(), string(raw)); err != nil {
		t.Fatal(err)
	}
}

func TestHumanGatePostgresIdempotencySurvivesConcurrencyAndRestart(t *testing.T) {
	dsn := os.Getenv("WARMBLY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("WARMBLY_TEST_POSTGRES_DSN is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	applyHumanGateSchema(t, pool)
	org, cohort, actor := uuid.New(), uuid.New(), uuid.New()
	insert := func(id uuid.UUID, key, hash string) error {
		_, e := pool.Exec(ctx, `INSERT INTO confenge_cohort_versions(id,organization_id,cohort_id,version,source_run_id,source_system,source_as_of,freshness_expires_at,policy_version,frozen_hash,frozen_manifest,created_by,correlation_id,idempotency_key,request_hash) VALUES($1,$2,$3,1,'fixture-run','fixture',$4,$5,'bounded-cohort-policy.v1','frozen','{}',$6,'corr',$7,$8)`, id, org, cohort, time.Now().UTC(), time.Now().UTC().Add(time.Hour), actor, key, hash)
		return e
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- insert(uuid.New(), "same-two-tabs", "same-payload") }()
	}
	wg.Wait()
	close(errs)
	success, conflict := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else {
			conflict++
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("two tabs: success=%d conflict=%d", success, conflict)
	}
	pool.Close()
	restarted, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	var stored string
	var versionID uuid.UUID
	if err = restarted.QueryRow(ctx, `SELECT id,request_hash FROM confenge_cohort_versions WHERE organization_id=$1 AND idempotency_key='same-two-tabs'`, org).Scan(&versionID, &stored); err != nil || stored != "same-payload" {
		t.Fatalf("restart receipt missing: %q %v", stored, err)
	}
	if _, err = restarted.Exec(ctx, `INSERT INTO confenge_cohort_versions(id,organization_id,cohort_id,version,source_run_id,source_system,source_as_of,freshness_expires_at,policy_version,frozen_hash,frozen_manifest,created_by,correlation_id,idempotency_key,request_hash) VALUES($1,$2,$3,2,'fixture-run','fixture',$4,$5,'bounded-cohort-policy.v1','frozen','{}',$6,'corr','same-two-tabs','different-payload')`, uuid.New(), org, cohort, time.Now().UTC(), time.Now().UTC().Add(time.Hour), actor); err == nil {
		t.Fatal("same key with different payload must conflict")
	}

	assertTwoTabs := func(name string, insert func(uuid.UUID) error) {
		t.Helper()
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); errs <- insert(uuid.New()) }()
		}
		wg.Wait()
		close(errs)
		ok, duplicate := 0, 0
		for e := range errs {
			if e == nil {
				ok++
			} else {
				duplicate++
			}
		}
		if ok != 1 || duplicate != 1 {
			t.Fatalf("%s two tabs: success=%d conflict=%d", name, ok, duplicate)
		}
	}

	now := time.Now().UTC()
	candidateID := uuid.New()
	assertTwoTabs("validation", func(id uuid.UUID) error {
		_, e := restarted.Exec(ctx, `INSERT INTO confenge_cohort_validations(id,organization_id,cohort_version_id,candidate_id,status,reason,provider,method,evidence_hash,checked_at,expires_at,actor_id,correlation_id,receipt,idempotency_key,request_hash) VALUES($1,$2,$3,$4,'VALID','fixture','fixture','syntax-mx','evidence',$5,$6,$7,'corr',$8,'validation-two-tabs','validation-payload')`, id, org, versionID, candidateID, now, now.Add(time.Hour), actor, humanGateReceipt("validation", id))
		return e
	})
	var validationID uuid.UUID
	if err = restarted.QueryRow(ctx, `SELECT id FROM confenge_cohort_validations WHERE organization_id=$1 AND idempotency_key='validation-two-tabs'`, org).Scan(&validationID); err != nil {
		t.Fatal(err)
	}
	assertTwoTabs("review", func(id uuid.UUID) error {
		_, e := restarted.Exec(ctx, `INSERT INTO confenge_cohort_candidate_reviews(id,organization_id,cohort_version_id,candidate_id,decision,reason,recipient_hash,content_hash,policy_version,evidence_hash,validation_id,validation_expires_at,actor_id,correlation_id,receipt,idempotency_key,request_hash) VALUES($1,$2,$3,$4,'APPROVE','fixture','recipient','content','bounded-cohort-policy.v1','evidence',$5,$6,$7,'corr',$8,'review-two-tabs','review-payload')`, id, org, versionID, candidateID, validationID, now.Add(time.Hour), actor, humanGateReceipt("review", id))
		return e
	})
	assertTwoTabs("decision", func(id uuid.UUID) error {
		_, e := restarted.Exec(ctx, `INSERT INTO confenge_cohort_go_decisions(id,organization_id,cohort_version_id,decision,reason,readiness_hash,actor_id,correlation_id,receipt,idempotency_key,request_hash) VALUES($1,$2,$3,'NO_GO','fixture','readiness',$4,'corr',$5,'decision-two-tabs','decision-payload')`, id, org, versionID, actor, humanGateReceipt("decision", id))
		return e
	})

	// A second process/pool must see every receipt; this is the crash/restart
	// guarantee the browser relies on after an ambiguous timeout.
	restarted.Close()
	afterCrash, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer afterCrash.Close()
	defer func() {
		_, _ = afterCrash.Exec(ctx, "DROP TABLE IF EXISTS confenge_cohort_go_decisions,confenge_cohort_candidate_reviews,confenge_cohort_validations,confenge_cohort_versions")
	}()
	for table, key := range map[string]string{
		"confenge_cohort_versions":          "same-two-tabs",
		"confenge_cohort_validations":       "validation-two-tabs",
		"confenge_cohort_candidate_reviews": "review-two-tabs",
		"confenge_cohort_go_decisions":      "decision-two-tabs",
	} {
		var count int
		query := "SELECT count(*) FROM " + table + " WHERE organization_id=$1 AND idempotency_key=$2"
		if err = afterCrash.QueryRow(ctx, query, org, key).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s restart receipt count=%d err=%v", table, count, err)
		}
	}
}

func TestHumanGatePostgresIntentLockSerializesTwoTabs(t *testing.T) {
	dsn := os.Getenv("WARMBLY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("WARMBLY_TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	svc := &service{humanGateDB: pool}
	org := uuid.New()
	unlockFirst, xerr := svc.lockHumanGateIntent(ctx, org, "same-go-intent")
	if xerr != nil {
		t.Fatal(xerr)
	}
	acquired := make(chan func(), 1)
	errs := make(chan *errx.Error, 1)
	go func() {
		unlock, lockErr := svc.lockHumanGateIntent(ctx, org, "same-go-intent")
		if lockErr != nil {
			errs <- lockErr
			return
		}
		acquired <- unlock
	}()
	select {
	case unlock := <-acquired:
		unlock()
		unlockFirst()
		t.Fatal("second tab acquired the same intent before the first released it")
	case lockErr := <-errs:
		unlockFirst()
		t.Fatal(lockErr)
	case <-time.After(100 * time.Millisecond):
		// Expected: the second caller is serialized behind the first.
	}
	unlockFirst()
	select {
	case unlock := <-acquired:
		unlock()
	case lockErr := <-errs:
		t.Fatal(lockErr)
	case <-ctx.Done():
		t.Fatal("second tab did not acquire after release")
	}
}
