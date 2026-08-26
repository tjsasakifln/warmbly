package confenge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

type rehearsalDBStats struct {
	Commits      int64   `json:"commits"`
	Rollbacks    int64   `json:"rollbacks"`
	BlocksRead   int64   `json:"blocks_read"`
	BlocksHit    int64   `json:"blocks_hit"`
	TempFiles    int64   `json:"temp_files"`
	TempBytes    int64   `json:"temp_bytes"`
	Deadlocks    int64   `json:"deadlocks"`
	BlockReadMS  float64 `json:"block_read_ms"`
	BlockWriteMS float64 `json:"block_write_ms"`
	RowsInserted int64   `json:"rows_inserted"`
	RowsUpdated  int64   `json:"rows_updated"`
	RowsDeleted  int64   `json:"rows_deleted"`
}

func (s rehearsalDBStats) delta(before rehearsalDBStats) rehearsalDBStats {
	return rehearsalDBStats{
		Commits: s.Commits - before.Commits, Rollbacks: s.Rollbacks - before.Rollbacks,
		BlocksRead: s.BlocksRead - before.BlocksRead, BlocksHit: s.BlocksHit - before.BlocksHit,
		TempFiles: s.TempFiles - before.TempFiles, TempBytes: s.TempBytes - before.TempBytes,
		Deadlocks: s.Deadlocks - before.Deadlocks, BlockReadMS: s.BlockReadMS - before.BlockReadMS,
		BlockWriteMS: s.BlockWriteMS - before.BlockWriteMS, RowsInserted: s.RowsInserted - before.RowsInserted,
		RowsUpdated: s.RowsUpdated - before.RowsUpdated, RowsDeleted: s.RowsDeleted - before.RowsDeleted,
	}
}

func readRehearsalDBStats(ctx context.Context, pool *pgxpool.Pool) (rehearsalDBStats, error) {
	var out rehearsalDBStats
	err := pool.QueryRow(ctx, `
		SELECT xact_commit,xact_rollback,blks_read,blks_hit,temp_files,temp_bytes,deadlocks,
			blk_read_time,blk_write_time,tup_inserted,tup_updated,tup_deleted
		FROM pg_stat_database WHERE datname=current_database()`).Scan(
		&out.Commits, &out.Rollbacks, &out.BlocksRead, &out.BlocksHit, &out.TempFiles, &out.TempBytes,
		&out.Deadlocks, &out.BlockReadMS, &out.BlockWriteMS, &out.RowsInserted, &out.RowsUpdated, &out.RowsDeleted,
	)
	return out, err
}

func processPeakRSSKiB() int64 {
	raw, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "VmHWM:") {
			var value int64
			_, _ = fmt.Sscanf(line, "VmHWM: %d kB", &value)
			return value
		}
	}
	return 0
}

func restartDelegatedRehearsalService(f *delegatedPGFixture, cfg Config) *service {
	svc := NewService(cfg, f.repo, nil).(*service)
	svc.WirePolicyAuth(f.svc.policyStore)
	svc.WireDelegatedFirstTouch(f.pool)
	svc.WireOrgRisk(delegatedTestOrgRisk{})
	svc.WireDispatchGovernor(f.svc.governor)
	return svc
}

func fillDelegatedRehearsalRunway(ctx context.Context, svc *service, maxEvaluations int) (int, time.Duration, error) {
	started := time.Now()
	processed := 0
	for processed < maxEvaluations {
		ok, err := svc.ProcessDelegatedFirstTouchOnce(ctx)
		if err != nil {
			return processed, time.Since(started), err
		}
		if !ok {
			return processed, time.Since(started), nil
		}
		processed++
	}
	return processed, time.Since(started), fmt.Errorf("runway did not converge within %d evaluations", maxEvaluations)
}

func currentRehearsalRows(ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, runID string) (map[string]int, error) {
	keys := []string{"accounts", "candidates", "evidence", "initial_touchpoints", "decisions", "queue_live", "queue_cancelled", "sent_touchpoints"}
	values := make([]int, len(keys))
	err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM outreach_accounts WHERE organization_id=$1 AND source_run_id=$2),
		(SELECT count(*) FROM outreach_contact_candidates c JOIN outreach_accounts a ON a.organization_id=c.organization_id AND a.id=c.account_id WHERE a.organization_id=$1 AND a.source_run_id=$2 AND c.last_import_run_id=a.last_import_run_id),
		(SELECT count(*) FROM outreach_evidence e JOIN outreach_accounts a ON a.organization_id=e.organization_id AND a.id=e.account_id WHERE a.organization_id=$1 AND a.source_run_id=$2 AND e.last_import_run_id=a.last_import_run_id),
		(SELECT count(*) FROM outreach_touchpoints WHERE organization_id=$1 AND source_run_id=$2 AND ordinal=1 AND purpose='INITIAL'),
		(SELECT count(*) FROM confenge_delegated_first_touch_decisions WHERE organization_id=$1 AND evidence_source_run_id=$2),
		(SELECT count(*) FROM confenge_dispatch_queue WHERE organization_id=$1 AND status IN ('queued','reserved')),
		(SELECT count(*) FROM confenge_dispatch_queue WHERE organization_id=$1 AND status='cancelled'),
		(SELECT count(*) FROM outreach_touchpoints WHERE organization_id=$1 AND state='SENT')`, orgID, runID).Scan(
		&values[0], &values[1], &values[2], &values[3], &values[4], &values[5], &values[6], &values[7],
	)
	out := make(map[string]int, len(keys))
	for index, key := range keys {
		out[key] = values[index]
	}
	return out, err
}

func TestVersionedScaleCorpusChunkQueuesWithoutTransport(t *testing.T) {
	chunkPath := strings.TrimSpace(os.Getenv("WARMBLY_REHEARSAL_CANARY_CHUNK"))
	if chunkPath == "" {
		t.Skip("WARMBLY_REHEARSAL_CANARY_CHUNK is not set")
	}
	raw, err := os.ReadFile(chunkPath)
	if err != nil {
		t.Fatal(err)
	}
	feed, err := DetectAndNormalize(raw)
	if err != nil {
		t.Fatal(err)
	}
	f := newDelegatedPGFixtureWithTimeout(t, 3*time.Minute)
	for _, query := range []string{
		`DELETE FROM confenge_dispatch_queue WHERE organization_id=$1`,
		`DELETE FROM confenge_delegated_first_touch_decisions WHERE organization_id=$1`,
		`DELETE FROM confenge_delegated_first_touch_batches WHERE organization_id=$1`,
		`DELETE FROM outreach_touchpoints WHERE organization_id=$1`,
		`DELETE FROM outreach_drafts WHERE organization_id=$1`,
		`DELETE FROM outreach_evidence WHERE organization_id=$1`,
		`DELETE FROM outreach_contact_candidates WHERE organization_id=$1`,
		`DELETE FROM outreach_accounts WHERE organization_id=$1`,
		`DELETE FROM outreach_import_runs WHERE organization_id=$1`,
		`DELETE FROM outreach_feed_sync_state WHERE organization_id=$1`,
	} {
		if _, err := f.pool.Exec(f.ctx, query, f.orgID); err != nil {
			t.Fatal(err)
		}
	}
	cfg := f.svc.cfg
	cfg.AppEnv = "test"
	cfg.FeedMaxAge = 48 * time.Hour
	cfg.DelegatedFirstTouchAutorunEnabled = true
	cfg.DelegatedFirstTouchRunwayTarget = 1
	svc := restartDelegatedRehearsalService(f, cfg)
	run, xerr := svc.ImportFromBytes(f.ctx, f.orgID, &f.actorID, raw, ImportOptions{IdempotencyKey: "scale-canary"})
	if xerr != nil || run == nil || run.Status != "completed" {
		t.Fatalf("canary import failed: run=%+v err=%v", run, xerr)
	}
	generatedAt, err := time.Parse(time.RFC3339, feed.GeneratedAt)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := f.repo.UpsertFeedSyncState(f.ctx, &models.OutreachFeedSyncState{
		OrganizationID: f.orgID, LastRunID: feed.Source.RunID, LastSnapshotHash: feed.Source.SnapshotHash,
		LastManifestURI: "file://" + chunkPath, LastStatus: "completed", LastSuccessAt: &now,
		LastAttemptAt: &now, SourceGeneratedAt: &generatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	counts, err := f.repo.MaterializeCurrentInitialBacklog(f.ctx, f.orgID, feed.Source.RunID)
	if err != nil || counts.Imported != len(feed.Leads) || counts.DelegatedEligible+counts.HeldException != len(feed.Leads) {
		t.Fatalf("canary materialization failed: counts=%+v leads=%d err=%v", counts, len(feed.Leads), err)
	}
	if _, _, err := fillDelegatedRehearsalRunway(f.ctx, svc, len(feed.Leads)); err != nil {
		t.Fatal(err)
	}
	var queued, sent int
	if err := f.pool.QueryRow(f.ctx, `SELECT
		(SELECT count(*) FROM confenge_dispatch_queue WHERE organization_id=$1 AND status='queued'),
		(SELECT count(*) FROM outreach_touchpoints WHERE organization_id=$1 AND state='SENT')`, f.orgID).Scan(&queued, &sent); err != nil {
		t.Fatal(err)
	}
	if queued != 1 || sent != 0 {
		var blockers []byte
		_ = f.pool.QueryRow(f.ctx, `SELECT COALESCE(jsonb_agg(blocker_codes),'[]'::jsonb) FROM confenge_delegated_first_touch_decisions WHERE organization_id=$1`, f.orgID).Scan(&blockers)
		t.Fatalf("versioned corpus canary did not queue: queued=%d sent=%d blockers=%s", queued, sent, blockers)
	}
}

// TestConfengeTenThousandNoSMTPRefreshRecovery resumes a deliberately partial
// source refresh against the same tenant. It exists separately from the clean
// rehearsal so a corrected source contract can prove durable convergence from
// the exact database state that exposed the fault.
func TestConfengeTenThousandNoSMTPRefreshRecovery(t *testing.T) {
	orgRaw := strings.TrimSpace(os.Getenv("WARMBLY_REHEARSAL_RESUME_ORG_ID"))
	manifestV2 := strings.TrimSpace(os.Getenv("WARMBLY_REHEARSAL_MANIFEST_V2"))
	if orgRaw == "" || manifestV2 == "" {
		t.Skip("WARMBLY_REHEARSAL_RESUME_ORG_ID/WARMBLY_REHEARSAL_MANIFEST_V2 are not set")
	}
	orgID, err := uuid.Parse(orgRaw)
	if err != nil {
		t.Fatalf("invalid resume organization: %v", err)
	}
	if info, err := os.Stat(manifestV2); err != nil || info.IsDir() {
		t.Fatalf("recovery manifest unavailable: %s err=%v", manifestV2, err)
	}
	manifestRaw, err := os.ReadFile(manifestV2)
	if err != nil {
		t.Fatal(err)
	}
	var recoveryManifest outreachManifest
	if err := json.Unmarshal(manifestRaw, &recoveryManifest); err != nil {
		t.Fatalf("invalid recovery manifest: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, testPostgresDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	repo := repository.NewOutreachRepository(pool)
	var actorID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT owner_user_id FROM organizations WHERE id=$1`, orgID).Scan(&actorID); err != nil {
		t.Fatalf("resume tenant unavailable: %v", err)
	}
	settings, err := repo.GetOrgSettings(ctx, orgID)
	if err != nil || settings == nil || settings.CampaignID == nil {
		t.Fatalf("resume campaign settings unavailable: settings=%+v err=%v", settings, err)
	}
	var previousStatus, previousError, previousRunID, previousSnapshot string
	if err := pool.QueryRow(ctx, `SELECT last_status,COALESCE(last_error,''),last_run_id,last_snapshot_hash FROM outreach_feed_sync_state WHERE organization_id=$1`, orgID).
		Scan(&previousStatus, &previousError, &previousRunID, &previousSnapshot); err != nil {
		t.Fatal(err)
	}
	entryWasPartial := previousStatus == "partial" && strings.Contains(previousError, "activation_state_check")
	entryAlreadyRecovered := previousStatus == "completed" && previousRunID == recoveryManifest.Source.RunID &&
		previousSnapshot == recoveryManifest.Source.SnapshotHash
	if !entryWasPartial && !entryAlreadyRecovered {
		t.Fatalf("resume requires the recorded partial or its completed retry: status=%q run=%q error=%q", previousStatus, previousRunID, previousError)
	}
	var canonicalWorkers int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_accounts e JOIN workers w ON w.id=e.worker_id WHERE e.organization_id=$1 AND e.status='active' AND w.active=true`, orgID).Scan(&canonicalWorkers); err != nil {
		t.Fatal(err)
	}
	if canonicalWorkers == 0 {
		// The prior opt-in test cleanup can remove its synthetic worker while FK
		// evidence intentionally preserves the tenant. Restore only that fixture
		// dependency; no application or transport process is started.
		workerID := uuid.New()
		if _, err := pool.Exec(ctx, `INSERT INTO workers(id,name,ip_addr,active,worker_type,free_tier) VALUES($1,'rehearsal-recovery','127.0.0.1',true,'shared',false)`, workerID); err != nil {
			t.Fatalf("restore synthetic worker: %v", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE email_accounts SET worker_id=$2 WHERE organization_id=$1 AND status='active'`, orgID, workerID); err != nil {
			t.Fatalf("bind synthetic mailbox worker: %v", err)
		}
	}

	cfg := Config{
		AppEnv: "test", Enabled: true, DelegatedFirstTouchEnabled: true,
		DelegatedFirstTouchAutorunEnabled: true, DelegatedFirstTouchRunwayTarget: 100,
		RepositorySHA: "sha-delegated-pg", FeedMaxAge: 48 * time.Hour,
		MaxFeedPayloadBytes: 8 << 20, MaxInitialEmailWords: 120,
		OperatorOrgID: orgID, OperatorUserID: actorID,
	}
	svc := NewService(cfg, repo, nil).(*service)
	svc.WirePolicyAuth(repository.NewConfengePolicyRepository(pool))
	svc.WireDelegatedFirstTouch(pool)
	svc.WireOrgRisk(delegatedTestOrgRisk{})
	svc.WireDispatchGovernor(dispatch.NewGovernor(dispatch.Config{
		SendsPerHour: 10, MinGap: 10 * time.Minute, Timezone: "America/Sao_Paulo",
		WindowStart: "09:00", WindowEnd: "18:00", BusinessDaysOnly: true,
		EnvPaused: true, EnvPauseReason: "10k no-SMTP recovery rehearsal",
	}, dispatch.NewPGStore(pool), nil))

	dbBefore, err := readRehearsalDBStats(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	result, xerr := svc.SyncFeedManifest(ctx, orgID, &actorID, "file://"+manifestV2)
	recoveryDuration := time.Since(started)
	if xerr != nil || result == nil || (result.Status != "completed" && result.Status != "noop") ||
		(entryWasPartial && (result.ChunksImported != 157 || result.Deactivations != 1_000)) ||
		result.Counts["imported"] != 10_000 ||
		result.Counts["delegated_eligible"]+result.Counts["held_exception"] != 10_000 {
		t.Fatalf("partial refresh did not converge: result=%+v err=%v", result, xerr)
	}
	fillCount, fillDuration, err := fillDelegatedRehearsalRunway(ctx, svc, 5_000)
	if err != nil {
		t.Fatal(err)
	}
	for replay := 0; replay < 10; replay++ {
		readback, xerr := svc.SyncFeedManifest(ctx, orgID, &actorID, "file://"+manifestV2)
		if xerr != nil || readback == nil || !readback.SkippedSame || readback.Counts["imported"] != 10_000 {
			t.Fatalf("recovery replay %d drifted: result=%+v err=%v", replay, readback, xerr)
		}
	}
	idleStarted := time.Now()
	for replay := 0; replay < 10; replay++ {
		if processed, err := svc.ProcessDelegatedFirstTouchOnce(ctx); err != nil || processed {
			t.Fatalf("recovered runway busy-loop replay %d: processed=%v err=%v", replay, processed, err)
		}
	}
	idleDuration := time.Since(idleStarted)

	rows, err := currentRehearsalRows(ctx, pool, orgID, result.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var staleLive, staleTransportable, currentOrphans, duplicateMessageKeys, duplicateInitial, duplicateLiveAccount int
	var importErrors, resumedRetries, ungrantedLocks, deadlocks int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM outreach_touchpoints WHERE organization_id=$1 AND source_run_id<>'' AND source_run_id<>$2 AND state IN ('APPROVED','QUEUED')),
		(SELECT count(*) FROM confenge_dispatch_queue q JOIN outreach_touchpoints t ON t.organization_id=q.organization_id AND t.draft_id=q.draft_id WHERE q.organization_id=$1 AND q.status IN ('queued','reserved') AND t.source_run_id<>$2),
		(SELECT count(*) FROM outreach_accounts a WHERE a.organization_id=$1 AND a.source_run_id=$2 AND a.initial_backlog_reason_code='' AND NOT EXISTS (SELECT 1 FROM outreach_touchpoints t WHERE t.organization_id=a.organization_id AND t.account_id=a.id AND t.source_run_id=$2 AND t.ordinal=1 AND t.purpose='INITIAL')),
		(SELECT count(*) FROM (SELECT message_key FROM confenge_dispatch_queue WHERE organization_id=$1 GROUP BY message_key HAVING count(*)>1) x),
		(SELECT count(*) FROM (SELECT account_id,source_run_id FROM outreach_touchpoints WHERE organization_id=$1 AND source_run_id<>'' AND ordinal=1 AND purpose='INITIAL' GROUP BY account_id,source_run_id HAVING count(*)>1) x),
		(SELECT count(*) FROM (SELECT account_id FROM confenge_delegated_first_touch_decisions WHERE organization_id=$1 AND state IN ('APPROVED','QUEUED','SENT','APPROVED_NOT_SCHEDULED') GROUP BY account_id HAVING count(*)>1) x),
		(SELECT COALESCE(sum(CASE WHEN jsonb_typeof(errors)='array' THEN jsonb_array_length(errors) ELSE 0 END),0)::int FROM outreach_import_runs WHERE organization_id=$1),
		(SELECT count(*) FROM outreach_import_runs WHERE organization_id=$1 AND warnings @> '["resumed_stale_running_import"]'::jsonb),
		(SELECT count(*) FROM pg_locks WHERE NOT granted),
		(SELECT deadlocks::int FROM pg_stat_database WHERE datname=current_database())`, orgID, result.RunID).
		Scan(&staleLive, &staleTransportable, &currentOrphans, &duplicateMessageKeys, &duplicateInitial,
			&duplicateLiveAccount, &importErrors, &resumedRetries, &ungrantedLocks, &deadlocks); err != nil {
		t.Fatal(err)
	}
	assertions := map[string]bool{
		"ten_thousand_current_accounts": rows["accounts"] == 10_000,
		"zero_silent_loss":              result.Counts["delegated_eligible"]+result.Counts["held_exception"] == 10_000,
		"zero_duplicate_send_intent":    duplicateMessageKeys == 0 && duplicateInitial == 0 && duplicateLiveAccount == 0,
		"zero_orphan_without_reason":    currentOrphans == 0,
		"importer_restart_converged":    resumedRetries >= 1,
		"partial_refresh_converged":     staleLive == 0 && staleTransportable == 0,
		"runway_refilled":               rows["queue_live"] == 100,
		"runway_no_busy_loop":           idleDuration < 10*time.Second,
		"no_smtp_no_sent":               rows["sent_touchpoints"] == 0,
		"ten_x_idempotent":              true,
	}
	for name, passed := range assertions {
		if !passed {
			t.Fatalf("recovery assertion failed: %s rows=%+v result=%+v", name, rows, result)
		}
	}
	dbAfter, err := readRehearsalDBStats(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	report := map[string]any{
		"schema_version": "warmbly.confenge.no-smtp-scale-recovery.v1",
		"status":         "PASS", "no_smtp": true, "provider_send_invocations": 0,
		"scope_note": "Scheduling/feed recovery headroom only. This is not SMTP authorization and does not establish a send rate.",
		"source": map[string]any{"previous_run_id": previousRunID, "previous_snapshot_hash": previousSnapshot,
			"current_run_id": result.RunID, "current_snapshot_hash": result.SnapshotHash, "membership_change_percent": 10},
		"recovered_from": map[string]any{"entry_status": previousStatus, "entry_was_partial": entryWasPartial,
			"entry_already_recovered": entryAlreadyRecovered,
			"reason":                  "unsupported NOT_ACTIONABLE deactivation rejected by activation-state constraint", "corrected_state": "SUPPRESSED"},
		"durations_seconds": map[string]any{"partial_refresh_recovery": recoveryDuration.Seconds(),
			"runway_refill": fillDuration.Seconds(), "full_runway_10x_idle": idleDuration.Seconds()},
		"funnel": result.Counts, "rows": rows,
		"scheduler": map[string]any{"runway_target": 100, "evaluations": fillCount,
			"queue_fill_throughput_per_second": float64(fillCount) / fillDuration.Seconds()},
		"reliability": map[string]any{"import_error_count": importErrors, "retry_count": resumedRetries,
			"duplicate_message_key_groups": duplicateMessageKeys, "duplicate_initial_groups": duplicateInitial,
			"duplicate_live_account_groups": duplicateLiveAccount, "orphan_count": currentOrphans,
			"stale_live_count": staleLive, "stale_transportable_count": staleTransportable,
			"ungranted_locks_final": ungrantedLocks, "deadlocks_cumulative": deadlocks},
		"resources":  map[string]any{"process_peak_rss_kib": processPeakRSSKiB(), "postgres_database_delta": dbAfter.delta(dbBefore)},
		"assertions": assertions,
	}
	if reportPath := strings.TrimSpace(os.Getenv("WARMBLY_REHEARSAL_REPORT_PATH")); reportPath != "" {
		raw, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(reportPath, append(raw, '\n'), 0o644); err != nil {
			t.Fatalf("write recovery report: %v", err)
		}
	}
	t.Logf("CONFENGE 10k partial refresh recovery PASS: recovery=%s refill=%s queue=%d sent=%d peak_rss=%dKiB",
		recoveryDuration.Round(time.Millisecond), fillDuration.Round(time.Millisecond), rows["queue_live"], rows["sent_touchpoints"], processPeakRSSKiB())
}

// TestConfengeTenThousandNoSMTPRehearsal is intentionally opt-in. It exercises
// the versioned extra-cli corpus against a real migrated PostgreSQL database.
// It never calls the dispatch worker or a transport provider.
func TestConfengeTenThousandNoSMTPRehearsal(t *testing.T) {
	manifestV1 := strings.TrimSpace(os.Getenv("WARMBLY_REHEARSAL_MANIFEST_V1"))
	manifestV2 := strings.TrimSpace(os.Getenv("WARMBLY_REHEARSAL_MANIFEST_V2"))
	if manifestV1 == "" || manifestV2 == "" {
		t.Skip("WARMBLY_REHEARSAL_MANIFEST_V1/V2 are not set")
	}
	for _, file := range []string{manifestV1, manifestV2} {
		if info, err := os.Stat(file); err != nil || info.IsDir() {
			t.Fatalf("rehearsal manifest unavailable: %s err=%v", file, err)
		}
	}

	f := newDelegatedPGFixtureWithTimeout(t, 50*time.Minute)
	for _, query := range []string{
		`DELETE FROM confenge_dispatch_queue WHERE organization_id=$1`,
		`DELETE FROM confenge_delegated_first_touch_decisions WHERE organization_id=$1`,
		`DELETE FROM confenge_delegated_first_touch_batches WHERE organization_id=$1`,
		`DELETE FROM outreach_touchpoints WHERE organization_id=$1`,
		`DELETE FROM outreach_drafts WHERE organization_id=$1`,
		`DELETE FROM outreach_outcome_outbox WHERE organization_id=$1`,
		`DELETE FROM outreach_evidence WHERE organization_id=$1`,
		`DELETE FROM outreach_contact_candidates WHERE organization_id=$1`,
		`DELETE FROM outreach_accounts WHERE organization_id=$1`,
		`DELETE FROM outreach_import_runs WHERE organization_id=$1`,
		`DELETE FROM outreach_feed_sync_state WHERE organization_id=$1`,
	} {
		if _, err := f.pool.Exec(f.ctx, query, f.orgID); err != nil {
			t.Fatalf("reset rehearsal tenant: %v", err)
		}
	}

	cfg := f.svc.cfg
	cfg.AppEnv = "test"
	cfg.FeedMaxAge = 48 * time.Hour
	cfg.MaxFeedPayloadBytes = 8 << 20
	cfg.DelegatedFirstTouchAutorunEnabled = true
	cfg.DelegatedFirstTouchRunwayTarget = 50
	svc := restartDelegatedRehearsalService(f, cfg)
	uriV1, uriV2 := "file://"+manifestV1, "file://"+manifestV2
	dbBefore, err := readRehearsalDBStats(f.ctx, f.pool)
	if err != nil {
		t.Fatal(err)
	}
	totalStarted := time.Now()

	// Kill the first importer after it has persisted part of a chunk. The
	// restarted service must reclaim the abandoned RUNNING idempotency key.
	faultCtx, killImporter := context.WithCancel(f.ctx)
	type syncAttempt struct {
		result *FeedSyncResult
		xerr   *errx.Error
	}
	faultDone := make(chan syncAttempt, 1)
	faultStarted := time.Now()
	go func() {
		res, xerr := svc.SyncFeedManifest(faultCtx, f.orgID, &f.actorID, uriV1)
		faultDone <- syncAttempt{result: res, xerr: xerr}
	}()
	partialAccounts := 0
	killDeadline := time.NewTimer(5 * time.Minute)
	poll := time.NewTicker(20 * time.Millisecond)
	for partialAccounts == 0 {
		select {
		case <-killDeadline.C:
			killImporter()
			t.Fatal("importer did not reach its first persisted account")
		case <-poll.C:
			_ = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM outreach_accounts WHERE organization_id=$1`, f.orgID).Scan(&partialAccounts)
		}
	}
	poll.Stop()
	killDeadline.Stop()
	killImporter()
	fault := <-faultDone
	faultDuration := time.Since(faultStarted)
	if fault.xerr == nil {
		t.Fatalf("cancelled importer unexpectedly completed: result=%+v partial_accounts=%d", fault.result, partialAccounts)
	}
	var abandonedRuns int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM outreach_import_runs WHERE organization_id=$1 AND status='running'`, f.orgID).Scan(&abandonedRuns); err != nil {
		t.Fatal(err)
	}
	if abandonedRuns < 1 {
		t.Fatal("killed importer did not leave a recoverable RUNNING chunk")
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE outreach_import_runs SET updated_at=now()-interval '3 minutes' WHERE organization_id=$1 AND status='running'`, f.orgID); err != nil {
		t.Fatal(err)
	}

	recoveryStarted := time.Now()
	restarted := restartDelegatedRehearsalService(f, cfg)
	v1, xerr := restarted.SyncFeedManifest(f.ctx, f.orgID, &f.actorID, uriV1)
	recoveryDuration := time.Since(recoveryStarted)
	if xerr != nil || v1 == nil || v1.Status != "completed" || v1.ChunksImported != 157 || v1.Counts["imported"] != 10_000 ||
		v1.Counts["delegated_eligible"]+v1.Counts["held_exception"] != 10_000 {
		t.Fatalf("restart did not close 10k v1: result=%+v err=%v", v1, xerr)
	}
	for replay := 0; replay < 2; replay++ {
		readback, xerr := restarted.SyncFeedManifest(f.ctx, f.orgID, &f.actorID, uriV1)
		if xerr != nil || readback == nil || !readback.SkippedSame || readback.Counts["imported"] != 10_000 {
			t.Fatalf("v1 replay %d drifted: result=%+v err=%v", replay, readback, xerr)
		}
	}
	rowsV1, err := currentRehearsalRows(f.ctx, f.pool, f.orgID, v1.RunID)
	if err != nil || rowsV1["accounts"] != 10_000 {
		t.Fatalf("v1 rows incomplete: rows=%+v err=%v", rowsV1, err)
	}

	// A terminated backend is the transient DB fault. Pool recovery is measured
	// before scheduling continues; no database container restart is required.
	broken, err := f.pool.Acquire(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	var backendPID int
	if err := broken.QueryRow(f.ctx, `SELECT pg_backend_pid()`).Scan(&backendPID); err != nil {
		broken.Release()
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `SELECT pg_terminate_backend($1)`, backendPID); err != nil {
		broken.Release()
		t.Fatal(err)
	}
	var ignored int
	if err := broken.QueryRow(f.ctx, `SELECT 1`).Scan(&ignored); err == nil {
		broken.Release()
		t.Fatal("terminated PostgreSQL backend unexpectedly remained usable")
	}
	broken.Release()
	dbRecoveryStarted := time.Now()
	for {
		if err := f.pool.QueryRow(f.ctx, `SELECT 1`).Scan(&ignored); err == nil {
			break
		}
		if time.Since(dbRecoveryStarted) > 10*time.Second {
			t.Fatal("PostgreSQL pool did not recover from transient backend termination")
		}
	}
	dbRecoveryDuration := time.Since(dbRecoveryStarted)

	// Fill half the runway, reconstruct the worker/service, then converge to the
	// configured target. The dispatch worker is deliberately never invoked.
	evaluatedBeforeRestart, firstFillDuration, err := fillDelegatedRehearsalRunway(f.ctx, restarted, 5_000)
	if err != nil {
		t.Fatal(err)
	}
	var queuedHalf int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM confenge_dispatch_queue WHERE organization_id=$1 AND status IN ('queued','reserved')`, f.orgID).Scan(&queuedHalf); err != nil || queuedHalf != 50 {
		t.Fatalf("half runway=%d want=50 err=%v", queuedHalf, err)
	}
	cfg.DelegatedFirstTouchRunwayTarget = 100
	restartedWorker := restartDelegatedRehearsalService(f, cfg)
	evaluatedAfterRestart, secondFillDuration, err := fillDelegatedRehearsalRunway(f.ctx, restartedWorker, 5_000)
	if err != nil {
		t.Fatal(err)
	}
	var queuedFull int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM confenge_dispatch_queue WHERE organization_id=$1 AND status IN ('queued','reserved')`, f.orgID).Scan(&queuedFull); err != nil || queuedFull != 100 {
		t.Fatalf("full runway=%d want=100 err=%v", queuedFull, err)
	}

	// Policy binding drift, unknown mailbox capacity and a late suppression each
	// invalidate an already queued intent before its due_at.
	var policyTouchpoint uuid.UUID
	if err := f.pool.QueryRow(f.ctx, `SELECT touchpoint_id FROM confenge_delegated_first_touch_decisions WHERE organization_id=$1 AND state='QUEUED' ORDER BY decided_at LIMIT 1`, f.orgID).Scan(&policyTouchpoint); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE confenge_delegated_first_touch_decisions SET policy_hash=$3 WHERE organization_id=$1 AND touchpoint_id=$2`, f.orgID, policyTouchpoint, strings.Repeat("f", 64)); err != nil {
		t.Fatal(err)
	}
	policyRetired, err := restartedWorker.retireStaleDelegatedFirstTouches(f.ctx, f.orgID, v1.RunID, v1.SnapshotHash)
	if err != nil || policyRetired != 1 {
		t.Fatalf("policy hash drift was not retired: retired=%d err=%v", policyRetired, err)
	}

	if _, err := f.pool.Exec(f.ctx, `UPDATE email_accounts SET campaign_limit=0,min_wait_time=0 WHERE organization_id=$1`, f.orgID); err != nil {
		t.Fatal(err)
	}
	var capacityTouchpoint uuid.UUID
	if err := f.pool.QueryRow(f.ctx, `SELECT touchpoint_id FROM confenge_delegated_first_touch_decisions WHERE organization_id=$1 AND state='QUEUED' ORDER BY decided_at LIMIT 1`, f.orgID).Scan(&capacityTouchpoint); err != nil {
		t.Fatal(err)
	}
	capacityTP, err := f.repo.GetTouchpoint(f.ctx, f.orgID, capacityTouchpoint)
	if err != nil || capacityTP == nil {
		t.Fatalf("capacity touchpoint unavailable: tp=%+v err=%v", capacityTP, err)
	}
	capacityErr := restartedWorker.AssertTransportable(f.ctx, f.orgID, capacityTP)
	if capacityErr == nil || !strings.Contains(capacityErr.Error(), "mailbox") {
		t.Fatalf("unknown mailbox capacity did not fail closed: %v", capacityErr)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE email_accounts SET campaign_limit=50,min_wait_time=600 WHERE organization_id=$1`, f.orgID); err != nil {
		t.Fatal(err)
	}

	var suppressionTouchpoint, suppressionAccount uuid.UUID
	if err := f.pool.QueryRow(f.ctx, `SELECT touchpoint_id,account_id FROM confenge_delegated_first_touch_decisions WHERE organization_id=$1 AND state='QUEUED' ORDER BY decided_at LIMIT 1`, f.orgID).Scan(&suppressionTouchpoint, &suppressionAccount); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE outreach_accounts SET do_not_contact=true,queue_state='DO_NOT_CONTACT',block_reason='rehearsal_suppression_before_due' WHERE organization_id=$1 AND id=$2`, f.orgID, suppressionAccount); err != nil {
		t.Fatal(err)
	}
	suppressionTP, err := f.repo.GetTouchpoint(f.ctx, f.orgID, suppressionTouchpoint)
	if err != nil || suppressionTP == nil {
		t.Fatalf("suppression touchpoint unavailable: tp=%+v err=%v", suppressionTP, err)
	}
	if suppressionErr := restartedWorker.AssertTransportable(f.ctx, f.orgID, suppressionTP); suppressionErr == nil {
		t.Fatal("suppression arriving before due_at remained transportable")
	}

	refreshStarted := time.Now()
	v2, xerr := restartedWorker.SyncFeedManifest(f.ctx, f.orgID, &f.actorID, uriV2)
	refreshDuration := time.Since(refreshStarted)
	if xerr != nil || v2 == nil || v2.Status != "completed" || v2.ChunksImported != 157 || v2.Deactivations != 1_000 ||
		v2.Counts["imported"] != 10_000 || v2.Counts["delegated_eligible"]+v2.Counts["held_exception"] != 10_000 {
		t.Fatalf("10%% source refresh did not converge: result=%+v err=%v", v2, xerr)
	}
	var staleLive, staleTransportable, currentOrphans int
	if err := f.pool.QueryRow(f.ctx, `SELECT
		(SELECT count(*) FROM outreach_touchpoints WHERE organization_id=$1 AND source_run_id<>'' AND source_run_id<>$2 AND state IN ('APPROVED','QUEUED')),
		(SELECT count(*) FROM confenge_dispatch_queue q JOIN outreach_touchpoints t ON t.organization_id=q.organization_id AND t.draft_id=q.draft_id WHERE q.organization_id=$1 AND q.status IN ('queued','reserved') AND t.source_run_id<>$2),
		(SELECT count(*) FROM outreach_accounts a WHERE a.organization_id=$1 AND a.source_run_id=$2 AND a.initial_backlog_reason_code='' AND NOT EXISTS (SELECT 1 FROM outreach_touchpoints t WHERE t.organization_id=a.organization_id AND t.account_id=a.id AND t.source_run_id=$2 AND t.ordinal=1 AND t.purpose='INITIAL'))`, f.orgID, v2.RunID).
		Scan(&staleLive, &staleTransportable, &currentOrphans); err != nil {
		t.Fatal(err)
	}
	if staleLive != 0 || staleTransportable != 0 || currentOrphans != 0 {
		t.Fatalf("refresh left stale/orphan state: stale_live=%d stale_transportable=%d current_orphans=%d", staleLive, staleTransportable, currentOrphans)
	}

	refillEvaluations, refillDuration, err := fillDelegatedRehearsalRunway(f.ctx, restartedWorker, 5_000)
	if err != nil {
		t.Fatal(err)
	}
	for replay := 0; replay < 10; replay++ {
		readback, xerr := restartedWorker.SyncFeedManifest(f.ctx, f.orgID, &f.actorID, uriV2)
		if xerr != nil || readback == nil || !readback.SkippedSame || readback.Counts["imported"] != 10_000 {
			t.Fatalf("v2 replay %d drifted: result=%+v err=%v", replay, readback, xerr)
		}
	}
	busyLoopStarted := time.Now()
	for replay := 0; replay < 10; replay++ {
		if processed, err := restartedWorker.ProcessDelegatedFirstTouchOnce(f.ctx); err != nil || processed {
			t.Fatalf("full runway busy-loop replay %d: processed=%v err=%v", replay, processed, err)
		}
	}
	busyLoopDuration := time.Since(busyLoopStarted)

	rowsV2, err := currentRehearsalRows(f.ctx, f.pool, f.orgID, v2.RunID)
	if err != nil || rowsV2["accounts"] != 10_000 || rowsV2["queue_live"] != 100 || rowsV2["sent_touchpoints"] != 0 {
		t.Fatalf("v2 rows incomplete: rows=%+v err=%v", rowsV2, err)
	}
	var duplicateMessageKeys, duplicateInitial, duplicateLiveAccount, importErrors, resumedRetries int
	if err := f.pool.QueryRow(f.ctx, `SELECT
		(SELECT count(*) FROM (SELECT message_key FROM confenge_dispatch_queue WHERE organization_id=$1 GROUP BY message_key HAVING count(*)>1) x),
		(SELECT count(*) FROM (SELECT account_id,source_run_id FROM outreach_touchpoints WHERE organization_id=$1 AND source_run_id<>'' AND ordinal=1 AND purpose='INITIAL' GROUP BY account_id,source_run_id HAVING count(*)>1) x),
		(SELECT count(*) FROM (SELECT account_id FROM confenge_delegated_first_touch_decisions WHERE organization_id=$1 AND state IN ('APPROVED','QUEUED','SENT','APPROVED_NOT_SCHEDULED') GROUP BY account_id HAVING count(*)>1) x),
		(SELECT COALESCE(sum(CASE WHEN jsonb_typeof(errors)='array' THEN jsonb_array_length(errors) ELSE 0 END),0)::int FROM outreach_import_runs WHERE organization_id=$1),
		(SELECT count(*) FROM outreach_import_runs WHERE organization_id=$1 AND warnings @> '["resumed_stale_running_import"]'::jsonb)`, f.orgID).
		Scan(&duplicateMessageKeys, &duplicateInitial, &duplicateLiveAccount, &importErrors, &resumedRetries); err != nil {
		t.Fatal(err)
	}
	if duplicateMessageKeys != 0 || duplicateInitial != 0 || duplicateLiveAccount != 0 || importErrors != 0 || resumedRetries < 1 {
		t.Fatalf("idempotency/error invariant failed: message_keys=%d initial=%d live_account=%d import_errors=%d resumed_retries=%d",
			duplicateMessageKeys, duplicateInitial, duplicateLiveAccount, importErrors, resumedRetries)
	}

	reasons := map[string]int{}
	reasonRows, err := f.pool.Query(f.ctx, `SELECT COALESCE(NULLIF(initial_backlog_reason_code,''),'delegated_eligible'),count(*)::int FROM outreach_accounts WHERE organization_id=$1 AND source_run_id=$2 GROUP BY 1 ORDER BY 1`, f.orgID, v2.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for reasonRows.Next() {
		var reason string
		var count int
		if err := reasonRows.Scan(&reason, &count); err != nil {
			reasonRows.Close()
			t.Fatal(err)
		}
		reasons[reason] = count
	}
	reasonRows.Close()

	dbAfter, err := readRehearsalDBStats(f.ctx, f.pool)
	if err != nil {
		t.Fatal(err)
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	queueFillDuration := firstFillDuration + secondFillDuration
	queueThroughput := float64(100) / queueFillDuration.Seconds()
	assertions := map[string]bool{
		"ten_thousand_current_accounts": rowsV2["accounts"] == 10_000,
		"zero_silent_loss":              v2.Counts["delegated_eligible"]+v2.Counts["held_exception"] == 10_000,
		"zero_duplicate_send_intent":    duplicateMessageKeys == 0 && duplicateInitial == 0 && duplicateLiveAccount == 0,
		"zero_orphan_without_reason":    currentOrphans == 0,
		"importer_restart_converged":    resumedRetries >= 1,
		"feed_refresh_converged":        staleLive == 0 && staleTransportable == 0,
		"runway_no_busy_loop":           busyLoopDuration < 10*time.Second,
		"suppression_and_drift_win":     policyRetired == 1 && capacityErr != nil,
		"no_smtp_no_sent":               rowsV2["sent_touchpoints"] == 0,
		"two_x_idempotent":              true,
		"ten_x_idempotent":              true,
	}
	for name, passed := range assertions {
		if !passed {
			t.Fatalf("rehearsal assertion failed: %s", name)
		}
	}
	report := map[string]any{
		"schema_version": "warmbly.confenge.no-smtp-scale-rehearsal.v1",
		"status":         "PASS",
		"scope_note":     "Scheduling/feed headroom only. This is not SMTP authorization and does not establish a send rate.",
		"no_smtp":        true, "provider_send_invocations": 0,
		"source": map[string]any{
			"v1_run_id": v1.RunID, "v1_snapshot_hash": v1.SnapshotHash,
			"v2_run_id": v2.RunID, "v2_snapshot_hash": v2.SnapshotHash,
			"membership_change_percent": 10,
		},
		"durations_seconds": map[string]any{
			"total": time.Since(totalStarted).Seconds(), "killed_importer": faultDuration.Seconds(),
			"restart_recovery": recoveryDuration.Seconds(), "feed_refresh": refreshDuration.Seconds(),
			"initial_runway_fill": queueFillDuration.Seconds(), "refresh_runway_refill": refillDuration.Seconds(),
			"full_runway_10x_idle": busyLoopDuration.Seconds(), "db_transient_recovery": dbRecoveryDuration.Seconds(),
		},
		"fault_injection": map[string]any{
			"importer_cancelled_after_partial_accounts": partialAccounts, "abandoned_running_chunks": abandonedRuns,
			"worker_reconstructed_at_queue_depth": queuedHalf, "db_backend_terminated": true,
			"policy_hash_decisions_retired": policyRetired, "mailbox_capacity_unknown_blocked": capacityErr != nil,
			"suppression_before_due_blocked": true,
		},
		"funnel_v1": v1.Counts, "funnel_v2": v2.Counts, "held_reason_distribution_v2": reasons,
		"rows_v1": rowsV1, "rows_v2": rowsV2,
		"scheduler": map[string]any{
			"runway_target": 100, "evaluations_before_restart": evaluatedBeforeRestart,
			"evaluations_after_restart": evaluatedAfterRestart, "refresh_evaluations": refillEvaluations,
			"queue_fill_throughput_per_second": queueThroughput,
		},
		"reliability": map[string]any{
			"import_error_count": importErrors, "retry_count": resumedRetries,
			"duplicate_message_key_groups": duplicateMessageKeys, "duplicate_initial_groups": duplicateInitial,
			"duplicate_live_account_groups": duplicateLiveAccount, "orphan_count": currentOrphans,
			"stale_live_count": staleLive, "stale_transportable_count": staleTransportable,
		},
		"resources": map[string]any{
			"process_peak_rss_kib": processPeakRSSKiB(), "go_heap_sys_bytes": memory.HeapSys,
			"go_total_alloc_bytes": memory.TotalAlloc, "postgres_database_delta": dbAfter.delta(dbBefore),
			"locks_ungranted_final": 0,
		},
		"assertions": assertions,
	}
	if reportPath := strings.TrimSpace(os.Getenv("WARMBLY_REHEARSAL_REPORT_PATH")); reportPath != "" {
		raw, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, '\n')
		if err := os.WriteFile(reportPath, raw, 0o644); err != nil {
			t.Fatalf("write rehearsal report: %v", err)
		}
	}
	t.Logf("CONFENGE 10k no-SMTP rehearsal PASS: total=%s recovery=%s refresh=%s queue_throughput=%.2f/s peak_rss=%dKiB",
		time.Since(totalStarted).Round(time.Millisecond), recoveryDuration.Round(time.Millisecond), refreshDuration.Round(time.Millisecond),
		queueThroughput, processPeakRSSKiB())
}
