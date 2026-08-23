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
	if err = restarted.QueryRow(ctx, `SELECT request_hash FROM confenge_cohort_versions WHERE organization_id=$1 AND idempotency_key='same-two-tabs'`, org).Scan(&stored); err != nil || stored != "same-payload" {
		t.Fatalf("restart receipt missing: %q %v", stored, err)
	}
	defer func() {
		_, _ = restarted.Exec(ctx, "DROP TABLE IF EXISTS confenge_cohort_go_decisions,confenge_cohort_candidate_reviews,confenge_cohort_validations,confenge_cohort_versions")
	}()
	if _, err = restarted.Exec(ctx, `INSERT INTO confenge_cohort_versions(id,organization_id,cohort_id,version,source_run_id,source_system,source_as_of,freshness_expires_at,policy_version,frozen_hash,frozen_manifest,created_by,correlation_id,idempotency_key,request_hash) VALUES($1,$2,$3,2,'fixture-run','fixture',$4,$5,'bounded-cohort-policy.v1','frozen','{}',$6,'corr','same-two-tabs','different-payload')`, uuid.New(), org, cohort, time.Now().UTC(), time.Now().UTC().Add(time.Hour), actor); err == nil {
		t.Fatal("same key with different payload must conflict")
	}
}
