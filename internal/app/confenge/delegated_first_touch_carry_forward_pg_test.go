package confenge

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// qualifyDelegatedPGFixture stamps a valid three-year qualification on the
// account and the matching population attestation on the feed row.
func qualifyDelegatedPGFixture(t *testing.T, f *delegatedPGFixture) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	account, err := f.repo.GetAccount(f.ctx, f.orgID, f.manifest.Entries[0].AccountID)
	if err != nil || account == nil {
		t.Fatalf("account unavailable: account=%+v err=%v", account, err)
	}
	qualification := testRootQualification(account.CNPJRoot, now.AddDate(-1, 0, 0))
	applyCommercialQualificationToAccount(account, &qualification, now)
	if _, err := f.repo.UpsertAccount(f.ctx, account); err != nil {
		t.Fatal(err)
	}
	stampDelegatedPGFeed(t, f, []RootQualification{qualification})
}

// stampDelegatedPGFeed re-attests the stored feed row against its own live
// publication identity.
func stampDelegatedPGFeed(t *testing.T, f *delegatedPGFixture, roots []RootQualification) {
	t.Helper()
	state, err := f.repo.GetFeedSyncState(f.ctx, f.orgID)
	if err != nil || state == nil {
		t.Fatalf("feed state unavailable: state=%+v err=%v", state, err)
	}
	stampFeedStateWithV2(state, roots)
	if err := f.repo.UpsertFeedSyncState(f.ctx, state); err != nil {
		t.Fatal(err)
	}
}

// A feed refresh moves the run id, the snapshot, the expiry and the freshness
// hash. None of that is a commercial fact, so an APPROVED/QUEUED decision for a
// still-QUALIFIED account must survive it.
func TestDelegatedFeedRefreshKeepsQualifiedCarryForwardPostgres(t *testing.T) {
	f := newDelegatedPGFixture(t)
	qualifyDelegatedPGFixture(t, f)
	report, xerr := f.svc.ApplyDelegatedFirstTouchManifest(f.ctx, f.orgID, f.manifest, false)
	if xerr != nil || report == nil || report.Queued != 1 {
		t.Fatalf("fixture did not queue: report=%+v err=%v", report, xerr)
	}

	// The producer publishes a new run over the same membership; the account
	// row stays on the run that first emitted it.
	newRun, newSnapshot := "run-refreshed-"+uuid.NewString(), strings.Repeat("f", 64)
	state, err := f.repo.GetFeedSyncState(f.ctx, f.orgID)
	if err != nil || state == nil {
		t.Fatalf("feed state unavailable: state=%+v err=%v", state, err)
	}
	refreshedAt := time.Now().UTC().Truncate(time.Second)
	expiresAt := refreshedAt.Add(2 * time.Hour)
	state.LastRunID, state.LastSnapshotHash = newRun, newSnapshot
	state.SourceGeneratedAt, state.SourceExpiresAt = &refreshedAt, &expiresAt
	state.LastSuccessAt, state.LastAttemptAt = &refreshedAt, &refreshedAt
	state.SourceFreshnessHash = strings.Repeat("9", 64)
	account, err := f.repo.GetAccount(f.ctx, f.orgID, f.manifest.Entries[0].AccountID)
	if err != nil || account == nil {
		t.Fatalf("account unavailable: account=%+v err=%v", account, err)
	}
	qualification := testRootQualification(account.CNPJRoot, refreshedAt.AddDate(-1, 0, 0))
	stampFeedStateWithV2(state, []RootQualification{qualification})
	if err := f.repo.UpsertFeedSyncState(f.ctx, state); err != nil {
		t.Fatal(err)
	}

	retired, err := f.svc.retireStaleDelegatedFirstTouches(f.ctx, f.orgID, newRun, newSnapshot)
	if err != nil || retired != 0 {
		t.Fatalf("a feed refresh retired a still-qualified decision: retired=%d err=%v", retired, err)
	}
	var decisionState, queueState string
	if err := f.pool.QueryRow(f.ctx, `
		SELECT d.state,q.status
		FROM confenge_delegated_first_touch_decisions d
		JOIN confenge_dispatch_queue q ON q.organization_id=d.organization_id AND q.message_key=d.queue_message_key
		WHERE d.organization_id=$1 AND d.touchpoint_id=$2`, f.orgID, report.Items[0].TouchpointID).
		Scan(&decisionState, &queueState); err != nil {
		t.Fatal(err)
	}
	if decisionState != "QUEUED" || queueState != "queued" {
		t.Fatalf("qualified decision did not survive the refresh: decision=%s queue=%s", decisionState, queueState)
	}

	// Losing the commercial fact is what retires the work.
	if _, err := f.pool.Exec(f.ctx, `
		UPDATE outreach_accounts SET commercial_qualification_state='EXPIRED'
		WHERE organization_id=$1 AND id=$2`, f.orgID, f.manifest.Entries[0].AccountID); err != nil {
		t.Fatal(err)
	}
	retired, err = f.svc.retireStaleDelegatedFirstTouches(f.ctx, f.orgID, newRun, newSnapshot)
	if err != nil || retired != 1 {
		t.Fatalf("an unqualified account was not retired: retired=%d err=%v", retired, err)
	}
	if err := f.pool.QueryRow(f.ctx, `
		SELECT d.state,q.status
		FROM confenge_delegated_first_touch_decisions d
		JOIN confenge_dispatch_queue q ON q.organization_id=d.organization_id AND q.message_key=d.queue_message_key
		WHERE d.organization_id=$1 AND d.touchpoint_id=$2`, f.orgID, report.Items[0].TouchpointID).
		Scan(&decisionState, &queueState); err != nil {
		t.Fatal(err)
	}
	if decisionState != "CANCELLED" || queueState != "cancelled" {
		t.Fatalf("unqualified decision survived retirement: decision=%s queue=%s", decisionState, queueState)
	}
}

// The autorun reservoir must still hand back an account whose row was emitted
// by an earlier run while it stays QUALIFIED.
func TestDelegatedReservoirServesCarriedForwardAccountPostgres(t *testing.T) {
	f := newDelegatedPGFixture(t)
	qualifyDelegatedPGFixture(t, f)
	entry := f.manifest.Entries[0]
	touchpointID := uuid.New()
	if _, err := f.pool.Exec(f.ctx, `
		INSERT INTO outreach_touchpoints(id,organization_id,account_id,contact_candidate_id,ordinal,purpose,channel,state,
			source_run_id,recipient,delegated_retry_at,due_at,created_at,updated_at)
		VALUES($1,$2,$3,$4,1,'INITIAL','EMAIL','DUE',$5,$6,now()-interval '1 hour',now(),now(),now())`,
		touchpointID, f.orgID, entry.AccountID, entry.ContactCandidateID, "run-older", entry.Recipient); err != nil {
		t.Skipf("touchpoint fixture shape unavailable: %v", err)
	}
	if _, err := f.pool.Exec(f.ctx, `
		UPDATE outreach_accounts SET source_run_id='run-older',initial_backlog_reason_code=''
		WHERE organization_id=$1 AND id=$2`, f.orgID, entry.AccountID); err != nil {
		t.Fatal(err)
	}
	feed, err := f.repo.GetFeedSyncState(f.ctx, f.orgID)
	if err != nil || feed == nil {
		t.Fatalf("feed state unavailable: state=%+v err=%v", feed, err)
	}
	auth, err := f.svc.policyStore.GetActiveCampaignPolicy(f.ctx, f.orgID, f.campaignID, time.Now().UTC())
	if err != nil || auth == nil {
		t.Fatalf("policy authorization unavailable: auth=%+v err=%v", auth, err)
	}
	gotTouchpoint, gotAccount, _, err := f.svc.nextDelegatedFirstTouchCandidate(f.ctx, f.orgID, feed, auth)
	if err != nil {
		t.Fatalf("carried-forward qualified account was not selectable: %v", err)
	}
	if gotTouchpoint != touchpointID || gotAccount != entry.AccountID {
		t.Fatalf("unexpected reservoir row: touchpoint=%s account=%s", gotTouchpoint, gotAccount)
	}
}

// A producer that publishes no population attestation has revoked nothing. The
// durable per-account three-year evidence is the authority, so approved work
// for a still-QUALIFIED account must survive an unattested feed rather than be
// mass-cancelled as an advanced binding.
func TestDelegatedUnattestedPopulationKeepsQualifiedApprovalsPostgres(t *testing.T) {
	f := newDelegatedPGFixture(t)
	qualifyDelegatedPGFixture(t, f)
	report, xerr := f.svc.ApplyDelegatedFirstTouchManifest(f.ctx, f.orgID, f.manifest, false)
	if xerr != nil || report == nil || report.Queued != 1 {
		t.Fatalf("fixture did not queue: report=%+v err=%v", report, xerr)
	}

	// Drop only the population attestation. Every per-account commercial fact
	// and the structural attestation columns stay exactly as they were.
	if _, err := f.pool.Exec(f.ctx, `
		UPDATE outreach_feed_sync_state SET commercial_authority_v2=NULL
		WHERE organization_id=$1`, f.orgID); err != nil {
		t.Fatal(err)
	}
	state, err := f.repo.GetFeedSyncState(f.ctx, f.orgID)
	if err != nil || state == nil {
		t.Fatalf("feed state unavailable: state=%+v err=%v", state, err)
	}
	if authority := FeedCommercialAuthorityState(state); authority.Present {
		t.Fatalf("fixture still carries a population attestation: %+v", authority)
	}

	retired, err := f.svc.retireStaleDelegatedFirstTouches(f.ctx, f.orgID, state.LastRunID, state.LastSnapshotHash)
	if err != nil || retired != 0 {
		t.Fatalf("an unattested population retired a qualified decision: retired=%d err=%v", retired, err)
	}
	var decisionState, queueState string
	if err := f.pool.QueryRow(f.ctx, `
		SELECT d.state,q.status
		FROM confenge_delegated_first_touch_decisions d
		JOIN confenge_dispatch_queue q ON q.organization_id=d.organization_id AND q.message_key=d.queue_message_key
		WHERE d.organization_id=$1 AND d.touchpoint_id=$2`, f.orgID, report.Items[0].TouchpointID).
		Scan(&decisionState, &queueState); err != nil {
		t.Fatal(err)
	}
	if decisionState != "QUEUED" || queueState != "queued" {
		t.Fatalf("qualified decision did not survive an unattested population: decision=%s queue=%s", decisionState, queueState)
	}

	// Losing the per-account commercial fact still retires the work, so the
	// fallback is an authority substitution and not a bypass.
	if _, err := f.pool.Exec(f.ctx, `
		UPDATE outreach_accounts SET commercial_qualification_state='EXPIRED'
		WHERE organization_id=$1 AND id=$2`, f.orgID, f.manifest.Entries[0].AccountID); err != nil {
		t.Fatal(err)
	}
	retired, err = f.svc.retireStaleDelegatedFirstTouches(f.ctx, f.orgID, state.LastRunID, state.LastSnapshotHash)
	if err != nil || retired != 1 {
		t.Fatalf("an unqualified account survived an unattested population: retired=%d err=%v", retired, err)
	}
}

// A deploy changes the runtime release sha of the process, not the commercial
// fact, the policy binding or the copy. Comparing it retired every approved and
// queued first touch on every deploy: production accumulated 7141 cancelled
// qualified decisions across six release cohorts and never sent a single mail,
// because the queue was destroyed faster than the send cadence could drain it.
func TestDelegatedRedeployKeepsQueuedWorkPostgres(t *testing.T) {
	f := newDelegatedPGFixture(t)
	qualifyDelegatedPGFixture(t, f)
	report, xerr := f.svc.ApplyDelegatedFirstTouchManifest(f.ctx, f.orgID, f.manifest, false)
	if xerr != nil || report == nil || report.Queued != 1 {
		t.Fatalf("fixture did not queue: report=%+v err=%v", report, xerr)
	}
	feed, err := f.repo.GetFeedSyncState(f.ctx, f.orgID)
	if err != nil || feed == nil {
		t.Fatalf("feed state unavailable: state=%+v err=%v", feed, err)
	}

	// The next release ships. Nothing about this account changed.
	f.svc.cfg.RepositorySHA = "sha-after-redeploy-" + uuid.NewString()

	retired, err := f.svc.retireStaleDelegatedFirstTouches(f.ctx, f.orgID, feed.LastRunID, feed.LastSnapshotHash)
	if err != nil || retired != 0 {
		t.Fatalf("a redeploy retired still-qualified queued work: retired=%d err=%v", retired, err)
	}
	var decisionState, queueState string
	if err := f.pool.QueryRow(f.ctx, `
		SELECT d.state,q.status
		FROM confenge_delegated_first_touch_decisions d
		JOIN confenge_dispatch_queue q ON q.organization_id=d.organization_id AND q.message_key=d.queue_message_key
		WHERE d.organization_id=$1 AND d.touchpoint_id=$2`, f.orgID, report.Items[0].TouchpointID).
		Scan(&decisionState, &queueState); err != nil {
		t.Fatal(err)
	}
	if decisionState != "QUEUED" || queueState != "queued" {
		t.Fatalf("queued work did not survive the redeploy: decision=%s queue=%s", decisionState, queueState)
	}
}

// The population attestation can be republished over the same members with a
// new revision. That is an import event, not a revocation, and it must not
// cancel work already proven against the per-account three-year fact.
func TestDelegatedMembershipRepublishKeepsQueuedWorkPostgres(t *testing.T) {
	f := newDelegatedPGFixture(t)
	qualifyDelegatedPGFixture(t, f)
	report, xerr := f.svc.ApplyDelegatedFirstTouchManifest(f.ctx, f.orgID, f.manifest, false)
	if xerr != nil || report == nil || report.Queued != 1 {
		t.Fatalf("fixture did not queue: report=%+v err=%v", report, xerr)
	}
	state, err := f.repo.GetFeedSyncState(f.ctx, f.orgID)
	if err != nil || state == nil {
		t.Fatalf("feed state unavailable: state=%+v err=%v", state, err)
	}
	account, err := f.repo.GetAccount(f.ctx, f.orgID, f.manifest.Entries[0].AccountID)
	if err != nil || account == nil {
		t.Fatalf("account unavailable: account=%+v err=%v", account, err)
	}
	// The producer republishes with one more member in the population.
	state.TargetMembershipHash = strings.Repeat("c", 64)
	state.TargetMembershipCount += 1
	qualification := testRootQualification(account.CNPJRoot, time.Now().UTC().AddDate(-1, 0, 0))
	stampFeedStateWithV2(state, []RootQualification{qualification})
	if err := f.repo.UpsertFeedSyncState(f.ctx, state); err != nil {
		t.Fatal(err)
	}

	retired, err := f.svc.retireStaleDelegatedFirstTouches(f.ctx, f.orgID, state.LastRunID, state.LastSnapshotHash)
	if err != nil || retired != 0 {
		t.Fatalf("a membership republish retired still-qualified queued work: retired=%d err=%v", retired, err)
	}
	var queueState string
	if err := f.pool.QueryRow(f.ctx, `
		SELECT q.status
		FROM confenge_delegated_first_touch_decisions d
		JOIN confenge_dispatch_queue q ON q.organization_id=d.organization_id AND q.message_key=d.queue_message_key
		WHERE d.organization_id=$1 AND d.touchpoint_id=$2`, f.orgID, report.Items[0].TouchpointID).
		Scan(&queueState); err != nil {
		t.Fatal(err)
	}
	if queueState != "queued" {
		t.Fatalf("queued work did not survive the republish: queue=%s", queueState)
	}
}
