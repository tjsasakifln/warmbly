package confenge

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

type schedulingPGFixture struct {
	t           *testing.T
	ctx         context.Context
	pool        *pgxpool.Pool
	svc         *service
	org         uuid.UUID
	actor       uuid.UUID
	versionID   uuid.UUID
	candidateID uuid.UUID
}

func newSchedulingPGFixture(t *testing.T) *schedulingPGFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, testPostgresDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	// This test exercises real outreach rows and the real dispatch queue. A
	// migrated application schema is therefore part of its contract.
	for _, table := range []string{"outreach_accounts", "confenge_cohort_candidate_dispatches", "confenge_dispatch_queue"} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass('public.' || $1) IS NOT NULL`, table).Scan(&exists); err != nil || !exists {
			t.Skipf("full migrated test schema required (%s missing)", table)
		}
	}

	f := &schedulingPGFixture{t: t, ctx: ctx, pool: pool, org: uuid.New(), actor: uuid.New(), versionID: uuid.New()}
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,first_name,last_name,email) VALUES($1,'Human','Reviewer',$2)`, f.actor, "human-gate-"+f.actor.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organizations(id,name,slug,owner_user_id) VALUES($1,'Human Gate Scheduling',$2,$3)`, f.org, "human-gate-"+f.org.String(), f.actor); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_, _ = pool.Exec(cleanup, `DELETE FROM organizations WHERE id=$1`, f.org)
		_, _ = pool.Exec(cleanup, `DELETE FROM users WHERE id=$1`, f.actor)
	})

	repo := repository.NewOutreachRepository(pool)
	now := time.Now().UTC()
	runID := "run-scheduling-" + uuid.NewString()
	acc := cohortAccount("scheduling-account", "37174657000188", "Contrato vigente publicado")
	acc.ID = uuid.New()
	acc.OrganizationID = f.org
	acc.SourceSystem = "extra-cli"
	acc.SourceRunID = runID
	acc.QueueState = models.OutreachQueueReadyToGenerate
	acc.MessageContextHash = "ctx-" + uuid.NewString()
	acc.TargetFitClass = "TARGET_CONFIRMED"
	acc.TargetFitSendTier = "A_AUTOMATIC"
	acc.TargetFitFresh = true
	acc.TargetFitEligible = true
	acc.EmailSendReady = true
	if _, err := repo.UpsertAccount(ctx, &acc); err != nil {
		t.Fatal(err)
	}
	unknown := true
	candidate := models.OutreachContactCandidate{
		ID: f.candidateID, OrganizationID: f.org, AccountID: acc.ID,
		SourceContactID: "candidate-scheduling", Email: "contato@empresa-scheduling.invalid",
		EmailSendReady: true, MailboxPurpose: "GENERIC_CONTACT", OwnershipStatus: "COMPANY_OWNED",
		RecipientCommercialSuitability: "SUITABLE", Recommended: true,
		VerificationStatus: "INSTITUTIONAL_GENERIC", Confidence: "HIGH",
		DiscoveryJSON: eligibleDisc(t, RouteClassGenericCompany, true, controlledDiscovery{PersonUnknown: &unknown}),
	}
	candidate.ID = uuid.New()
	f.candidateID = candidate.ID
	if _, err := repo.UpsertCandidate(ctx, &candidate); err != nil {
		t.Fatal(err)
	}

	inputs, err := AccountsFromOrgForRun(ctx, repo, f.org, "extra-cli", runID)
	if err != nil {
		t.Fatal(err)
	}
	fresh := &FeedSourceFreshness{
		ContractVersion: AuthoritativeFreshnessContractV1, Status: "FRESH", RunID: runID,
		AsOf: now.Add(-time.Hour).Format(time.RFC3339Nano), ExpiresAt: now.Add(12 * time.Hour).Format(time.RFC3339Nano),
	}
	snap, err := PrepareControlledCohort(inputs, CohortPrepareOptions{
		Now: now, Limit: 5, MaxDailyVolume: 5, TTL: DefaultCohortTTL,
		RepositorySHA: "sha-scheduling", FeedSchemaVersion: models.OutreachSchemaV1,
		FeedIdentity: runID, PolicyVersion: BoundedCohortPolicyV1,
		EvidenceVersion: DefaultEvidenceVersion, Source: "extra-cli",
		AuthoritativeSourceFreshness: fresh, RequireAuthoritativeFreshness: true,
	})
	if err != nil || len(snap.Members) != 1 {
		t.Fatalf("prepare snapshot: members=%d err=%v", len(snap.Members), err)
	}
	f.candidateID = snap.Members[0].CandidateID
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO confenge_cohort_versions
		(id,organization_id,cohort_id,version,source_run_id,source_system,source_as_of,freshness_expires_at,policy_version,frozen_hash,frozen_manifest,created_by,correlation_id,idempotency_key,request_hash)
		VALUES($1,$2,$3,1,$4,'extra-cli',$5,$6,$7,$8,$9,$10,'corr-scheduling',$11,'fixture')`,
		f.versionID, f.org, uuid.New(), runID, now.Add(-time.Hour), now.Add(12*time.Hour),
		BoundedCohortPolicyV1, snap.CohortHash, raw, f.actor, "version-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	validationID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO confenge_cohort_validations
		(id,organization_id,cohort_version_id,candidate_id,status,reason,provider,method,evidence_hash,checked_at,expires_at,actor_id,correlation_id,receipt,idempotency_key,request_hash)
		VALUES($1,$2,$3,$4,'VALID','fixture','fixture','fixture','validation-evidence',$5,$6,$7,'corr',$8,$9,'fixture')`,
		validationID, f.org, f.versionID, f.candidateID, now, now.Add(6*time.Hour), f.actor,
		humanGateReceipt("validation", validationID), "validation-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}

	f.svc = NewService(Config{Enabled: true, AppEnv: "test", FeedMaxAge: 24 * time.Hour, RequireHumanApproval: true}, repo, nil).(*service)
	f.svc.WireHumanGate(pool)
	f.svc.cohortStore = NewPostgresCohortStore(pool)
	f.svc.WireDispatchGovernor(dispatch.NewGovernor(dispatch.Config{
		SendsPerHour: 10, MinGap: time.Minute, Timezone: "America/Sao_Paulo",
		WindowStart: "09:00", WindowEnd: "18:00", BusinessDaysOnly: true,
	}, dispatch.NewPGStore(pool), nil))
	return f
}

func (f *schedulingPGFixture) review(decision, key string) *HumanGateCohort {
	f.t.Helper()
	v, xerr := f.svc.ReviewHumanGateCandidate(f.ctx, f.org, f.actor, f.versionID, f.candidateID, HumanGateReviewInput{
		Decision: decision, Reason: "decisão explícita do revisor", Acknowledged: decision == "APPROVE",
		IdempotencyKey: key, CorrelationID: "corr-" + key,
	})
	if xerr != nil {
		f.t.Fatalf("%s %s: %v", decision, key, xerr)
	}
	return v
}

func (f *schedulingPGFixture) counts() [4]int {
	f.t.Helper()
	var reviews, mappings, touchpoints, queued int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM confenge_cohort_candidate_reviews WHERE organization_id=$1`, f.org).Scan(&reviews); err != nil {
		f.t.Fatal(err)
	}
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM confenge_cohort_candidate_dispatches WHERE organization_id=$1`, f.org).Scan(&mappings); err != nil {
		f.t.Fatal(err)
	}
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM outreach_touchpoints WHERE organization_id=$1`, f.org).Scan(&touchpoints); err != nil {
		f.t.Fatal(err)
	}
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM confenge_dispatch_queue WHERE organization_id=$1 AND status='queued'`, f.org).Scan(&queued); err != nil {
		f.t.Fatal(err)
	}
	return [4]int{reviews, mappings, touchpoints, queued}
}

func TestHumanGateApproveSchedulesOnceAndReconcileIsIdempotentPostgres(t *testing.T) {
	f := newSchedulingPGFixture(t)
	killPath := t.TempDir() + "/kill-switch"
	if err := os.WriteFile(killPath, []byte("paused\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvKillSwitchPath, killPath)
	first := f.review("APPROVE", "approve-first")
	var candidate *HumanGateCandidate
	for i := range first.Candidates {
		if first.Candidates[i].CandidateID == f.candidateID {
			candidate = &first.Candidates[i]
			break
		}
	}
	if candidate == nil || candidate.Scheduling == nil || candidate.Scheduling.State != models.TouchpointQueued ||
		!candidate.Scheduling.AutoSend || candidate.Scheduling.DueAt.IsZero() {
		t.Fatalf("APPROVE did not confirm QUEUED auto_send=true with due_at: %#v", candidate)
	}
	if got := f.counts(); got != [4]int{1, 1, 1, 1} {
		t.Fatalf("after first approve counts=%v", got)
	}
	if processed, err := f.svc.ProcessDispatchQueueOnce(f.ctx); err != nil || processed {
		t.Fatalf("engaged kill switch must leave approved work queued: processed=%v err=%v", processed, err)
	}
	// Same key models a double-click/ambiguous HTTP retry; a distinct key
	// models a deliberate second approval and a second browser tab.
	f.review("APPROVE", "approve-first")
	f.review("APPROVE", "approve-second-tab")
	if got := f.counts(); got != [4]int{2, 1, 1, 1} {
		t.Fatalf("duplicate approvals must still be one message: %v", got)
	}

	firstRun, xerr := f.svc.ReconcileApprovedHumanGateCandidates(f.ctx, f.org, f.actor)
	if xerr != nil || firstRun.ApprovalRecords != 2 || firstRun.UniqueApprovedCandidates != 1 ||
		firstRun.LatestApprovedBindings != 1 || firstRun.AlreadyScheduled != 1 || firstRun.Scheduled != 0 || firstRun.Failed != 0 {
		t.Fatalf("first reconcile: report=%+v err=%v", firstRun, xerr)
	}
	secondRun, xerr := f.svc.ReconcileApprovedHumanGateCandidates(f.ctx, f.org, f.actor)
	if xerr != nil || secondRun.AlreadyScheduled != 1 || secondRun.Scheduled != 0 || secondRun.Failed != 0 {
		t.Fatalf("second reconcile: report=%+v err=%v", secondRun, xerr)
	}
	if got := f.counts(); got != [4]int{2, 1, 1, 1} {
		t.Fatalf("reconcile rerun duplicated durable state: %v", got)
	}
}

func TestHumanGateHoldCancelsQueuedMessageAndLaterApproveGetsNewQueueKeyPostgres(t *testing.T) {
	f := newSchedulingPGFixture(t)
	f.review("APPROVE", "approve-before-hold")
	f.review("HOLD", "hold-after-approve")
	var tpState, queueState string
	var invalidated *time.Time
	if err := f.pool.QueryRow(f.ctx, `SELECT t.state,q.status,d.invalidated_at
		FROM confenge_cohort_candidate_dispatches d
		JOIN outreach_touchpoints t ON t.id=d.touchpoint_id
		JOIN confenge_dispatch_queue q ON q.draft_id=d.draft_id
		WHERE d.organization_id=$1`, f.org).Scan(&tpState, &queueState, &invalidated); err != nil {
		t.Fatal(err)
	}
	if tpState != models.TouchpointNeedsReview || queueState != dispatch.QueueCancelled || invalidated == nil {
		t.Fatalf("HOLD must cancel unsent queue work: touchpoint=%s queue=%s invalidated=%v", tpState, queueState, invalidated)
	}
	f.review("APPROVE", "approve-after-hold")
	var queued, cancelled, distinctKeys int
	if err := f.pool.QueryRow(f.ctx, `SELECT
		count(*) FILTER (WHERE status='queued'), count(*) FILTER (WHERE status='cancelled'), count(DISTINCT message_key)
		FROM confenge_dispatch_queue WHERE organization_id=$1`, f.org).Scan(&queued, &cancelled, &distinctKeys); err != nil {
		t.Fatal(err)
	}
	if queued != 1 || cancelled != 1 || distinctKeys != 2 {
		t.Fatalf("reapproval must create one fresh queue key: queued=%d cancelled=%d keys=%d", queued, cancelled, distinctKeys)
	}
}

func TestHumanGateDuplicateCohortsShareOneOutboundMessagePostgres(t *testing.T) {
	f := newSchedulingPGFixture(t)
	first := f.review("APPROVE", "approve-original-cohort")
	firstScheduling := first.Candidates[0].Scheduling
	if firstScheduling == nil {
		t.Fatal("first cohort has no scheduling projection")
	}
	tp, err := f.svc.repo.GetTouchpoint(f.ctx, f.org, firstScheduling.TouchpointID)
	if err != nil || tp == nil {
		t.Fatalf("read scheduled touchpoint: %v", err)
	}
	t.Setenv(EnvKillSwitchPath, t.TempDir()+"/not-engaged")
	if err := f.svc.AssertTransportable(f.ctx, f.org, tp); err != nil {
		t.Fatalf("individual human-gate authority must use its frozen controlled route, not global target-fit: %v", err)
	}

	duplicateVersion := uuid.New()
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO confenge_cohort_versions
		(id,organization_id,cohort_id,version,source_run_id,source_system,source_as_of,freshness_expires_at,policy_version,frozen_hash,frozen_manifest,created_by,correlation_id,idempotency_key,request_hash)
		SELECT $1,organization_id,$2,version,source_run_id,source_system,source_as_of,freshness_expires_at,policy_version,frozen_hash,frozen_manifest,created_by,'corr-duplicate',$3,'duplicate'
		FROM confenge_cohort_versions WHERE id=$4 AND organization_id=$5`,
		duplicateVersion, uuid.New(), "duplicate-version-"+uuid.NewString(), f.versionID, f.org); err != nil {
		t.Fatal(err)
	}
	validationID := uuid.New()
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO confenge_cohort_validations
		(id,organization_id,cohort_version_id,candidate_id,status,reason,provider,method,evidence_hash,checked_at,expires_at,actor_id,correlation_id,receipt,idempotency_key,request_hash)
		SELECT $1,organization_id,$2,candidate_id,'VALID','duplicate fixture','fixture','fixture',evidence_hash,now(),now()+interval '6 hours',actor_id,'corr-duplicate',$3,$4,'duplicate'
		FROM confenge_cohort_validations WHERE organization_id=$5 AND cohort_version_id=$6 AND candidate_id=$7
		ORDER BY created_at DESC,id DESC LIMIT 1`, validationID, duplicateVersion,
		humanGateReceipt("validation", validationID), "duplicate-validation-"+uuid.NewString(), f.org, f.versionID, f.candidateID); err != nil {
		t.Fatal(err)
	}
	second, xerr := f.svc.ReviewHumanGateCandidate(f.ctx, f.org, f.actor, duplicateVersion, f.candidateID, HumanGateReviewInput{
		Decision: "APPROVE", Reason: "mesma mensagem em coorte duplicada", Acknowledged: true,
		IdempotencyKey: "approve-duplicate-cohort", CorrelationID: "corr-duplicate",
	})
	if xerr != nil {
		t.Fatal(xerr)
	}
	secondScheduling := second.Candidates[0].Scheduling
	if secondScheduling == nil || secondScheduling.TouchpointID != firstScheduling.TouchpointID {
		t.Fatalf("duplicate cohort did not converge: first=%+v second=%+v", firstScheduling, secondScheduling)
	}
	var mappings, touchpoints, queued int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM confenge_cohort_candidate_dispatches WHERE organization_id=$1`, f.org).Scan(&mappings); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM outreach_touchpoints WHERE organization_id=$1`, f.org).Scan(&touchpoints); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM confenge_dispatch_queue WHERE organization_id=$1 AND status='queued'`, f.org).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if mappings != 2 || touchpoints != 1 || queued != 1 {
		t.Fatalf("duplicate cohort created outbound duplicate: mappings=%d touchpoints=%d queued=%d", mappings, touchpoints, queued)
	}
	report, xerr := f.svc.ReconcileApprovedHumanGateCandidates(f.ctx, f.org, f.actor)
	if xerr != nil || report.ApprovalRecords != 2 || report.LatestApprovedBindings != 2 ||
		report.UniqueApprovedCandidates != 1 || report.AlreadyScheduled != 2 || report.Failed != 0 {
		t.Fatalf("duplicate cohort reconcile report=%+v err=%v", report, xerr)
	}
}

func TestHumanGateValidationDriftCancelsBeforeTransportPostgres(t *testing.T) {
	f := newSchedulingPGFixture(t)
	f.review("APPROVE", "approve-before-drift")
	if _, err := f.pool.Exec(f.ctx, `UPDATE confenge_cohort_validations SET status='UNKNOWN' WHERE organization_id=$1`, f.org); err != nil {
		t.Fatal(err)
	}
	var touchpointID uuid.UUID
	if err := f.pool.QueryRow(f.ctx, `SELECT touchpoint_id FROM confenge_cohort_candidate_dispatches WHERE organization_id=$1`, f.org).Scan(&touchpointID); err != nil {
		t.Fatal(err)
	}
	workerNow := time.Date(2026, 8, 25, 13, 0, 0, 0, time.UTC)
	if _, err := f.pool.Exec(f.ctx, `UPDATE confenge_dispatch_queue SET due_at='2000-01-01T00:00:00Z' WHERE organization_id=$1`, f.org); err != nil {
		t.Fatal(err)
	}
	pgStore := dispatch.NewPGStore(f.pool)
	if err := pgStore.EnsureControl(f.ctx); err != nil {
		t.Fatal(err)
	}
	f.svc.WireDispatchGovernor(dispatch.NewGovernor(dispatch.Config{
		SendsPerHour: 10, MinGap: time.Minute, Timezone: "UTC",
		WindowStart: "00:00", WindowEnd: "23:59",
	}, pgStore, &dispatch.FixedClock{T: workerNow}))
	t.Setenv(EnvKillSwitchPath, t.TempDir()+"/not-engaged")
	processed, err := f.svc.ProcessDispatchQueueOnce(f.ctx)
	if err != nil || !processed {
		t.Fatalf("drifted queue item was not examined: processed=%v err=%v", processed, err)
	}
	var state, queueState string
	if err := f.pool.QueryRow(f.ctx, `SELECT t.state,q.status FROM outreach_touchpoints t
		JOIN confenge_dispatch_queue q ON q.draft_id=t.draft_id WHERE t.id=$1`, touchpointID).Scan(&state, &queueState); err != nil {
		t.Fatal(err)
	}
	if state != models.TouchpointNeedsReview || queueState != dispatch.QueueCancelled {
		t.Fatalf("drift survived cancellation: touchpoint=%s queue=%s", state, queueState)
	}
}

func TestHumanGateSchedulingMigrationKeepsGlobalAutoSendProhibited(t *testing.T) {
	raw, err := os.ReadFile("../../infrastructure/db/migrations/000121_confenge_human_gate_scheduling.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, required := range []string{"auto_send boolean NOT NULL DEFAULT true", "CHECK (auto_send = true)", "HUMAN_GATE_APPROVAL"} {
		if !containsLiteral(sql, required) {
			t.Fatalf("migration missing %q", required)
		}
	}
	if LoadConfig().AutoSendEnabled {
		t.Fatal("per-message auto_send=true must not enable CONFENGE_AUTO_SEND_ENABLED")
	}
}

func containsLiteral(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
