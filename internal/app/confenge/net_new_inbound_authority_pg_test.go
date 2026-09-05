package confenge

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// netNewAuthorityPGService builds a service WITHOUT any test-only pin override,
// so admission runs against RuntimeInboundAuthorityPin (the published
// Governance authority). Anything accepted here was accepted by production
// authority, not by a fixture.
func netNewAuthorityPGService(t *testing.T, pool *pgxpool.Pool) *service {
	t.Helper()
	repo := repository.NewOutreachRepository(pool)
	svc := NewService(Config{Enabled: true, RequireHumanApproval: true, AutoSendEnabled: false}, repo, nil).(*service)
	if svc.authorityPin.Pinned() {
		t.Fatal("service must not carry a pin override; production authority is under test")
	}
	if got := svc.activeAuthorityPin(); got.ContentHash != GovernanceInboundPolicyHash || got.TestOnly {
		t.Fatalf("active pin is not the published authority: %+v", got)
	}
	return svc
}

func netNewOrgRowCounts(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID) (leads, accounts, actions, alerts int) {
	t.Helper()
	ctx := context.Background()
	q := func(sql string) int {
		var n int
		if err := pool.QueryRow(ctx, sql, orgID).Scan(&n); err != nil {
			t.Fatalf("count (%s): %v", sql, err)
		}
		return n
	}
	return q(`SELECT count(*) FROM outreach_inbound_leads WHERE organization_id=$1`),
		q(`SELECT count(*) FROM outreach_accounts WHERE organization_id=$1`),
		q(`SELECT count(*) FROM outreach_commercial_actions WHERE organization_id=$1`),
		q(`SELECT count(*) FROM outreach_operator_alerts WHERE organization_id=$1`)
}

func netNewPGCleanup(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID) {
	t.Helper()
	t.Cleanup(func() {
		ctx, done := context.WithTimeout(context.Background(), 20*time.Second)
		defer done()
		for _, tbl := range []string{
			"outreach_operator_alerts", "outreach_inbound_leads",
			"outreach_commercial_actions", "outreach_contact_candidates", "outreach_accounts",
		} {
			_, _ = pool.Exec(ctx, `DELETE FROM `+tbl+` WHERE organization_id=$1`, orgID)
		}
	})
}

// TestPGGovernanceAuthorityAdmits100ReplaysAsOneIntent is the Phase B proof.
//
// It replays the identical NET_NEW_INBOUND_HANDRAISER request 100 times
// against a REAL PostgreSQL instance, with admission decided by the published
// Governance authority (no test-only pin), and asserts on persisted ROW COUNTS
// rather than on Go return values: HTTP 2xx is not acceptance and a Go struct
// is not a row.
func TestPGGovernanceAuthorityAdmits100ReplaysAsOneIntent(t *testing.T) {
	if postgresTestDSN() == "" {
		t.Skip("WARMBLY_TEST_POSTGRES_DSN/PRIMARY_DB is not set")
	}
	pool, orgID := openInboundOnlyPG(t)
	t.Cleanup(pool.Close)
	netNewPGCleanup(t, pool, orgID)

	svc := netNewAuthorityPGService(t, pool)
	logicalID := "nnhr-authority-100x-" + uuid.NewString()[:8]
	body := marshalNetNew(t, governanceNetNewMap(logicalID))
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

	if l, a, c, _ := netNewOrgRowCounts(t, pool, orgID); l+a+c != 0 {
		t.Fatalf("org not clean before replay: leads=%d accounts=%d actions=%d", l, a, c)
	}

	var first *NetNewInboundResult
	for i := 0; i < 100; i++ {
		res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), orgID, body, now.Add(time.Duration(i)*time.Second))
		if xerr != nil {
			t.Fatalf("replay %d failed: %v", i, xerr)
		}
		if res.Outcome != NetNewInboundOutcomeAccepted {
			t.Fatalf("replay %d not ACCEPTED by published authority: outcome=%s reason=%s", i, res.Outcome, res.Reason)
		}
		if res.ActionID == nil || res.AccountID == nil {
			t.Fatalf("replay %d dropped the intent: %+v", i, res)
		}
		if first == nil {
			first = res
			if res.Replay {
				t.Fatal("first ingest reported as a replay")
			}
			continue
		}
		if !res.Replay {
			t.Fatalf("replay %d was not marked as a replay", i)
		}
		if *res.ActionID != *first.ActionID {
			t.Fatalf("replay %d created a second commercial action: %s vs %s", i, res.ActionID, first.ActionID)
		}
		if *res.AccountID != *first.AccountID {
			t.Fatalf("replay %d created a second account", i)
		}
		if res.Receipt != first.Receipt {
			t.Fatalf("replay %d changed the receipt", i)
		}
	}

	// The proof: persisted row counts, org-scoped (openHandRaisePGSeed mints a
	// fresh org per test, so a stray row cannot hide behind a filter).
	leads, accounts, actions, alerts := netNewOrgRowCounts(t, pool, orgID)
	if leads != 1 {
		t.Fatalf("100 replays persisted %d receipts, want 1", leads)
	}
	if accounts != 1 {
		t.Fatalf("100 replays persisted %d accounts, want 1", accounts)
	}
	if actions != 1 {
		t.Fatalf("100 replays persisted %d commercial actions, want 1", actions)
	}
	if alerts > 1 {
		t.Fatalf("100 replays persisted %d operator alerts, want <=1", alerts)
	}

	// The persisted receipt must attest the authority it was admitted against.
	var prov []string
	if err := pool.QueryRow(context.Background(),
		`SELECT ARRAY(SELECT jsonb_array_elements_text(provenance)) FROM outreach_inbound_leads WHERE organization_id=$1 AND lead_id=$2`,
		orgID, logicalID).Scan(&prov); err != nil {
		t.Fatalf("read provenance: %v", err)
	}
	gotHash := provenanceValue(prov, netNewProvHash)
	if gotHash != GovernanceInboundPolicyHash {
		t.Fatalf("receipt attests hash=%s, want the admitted Governance policy_hash %s", gotHash, GovernanceInboundPolicyHash)
	}
	if outcome := provenanceValue(prov, netNewProvOutcome); outcome != NetNewInboundOutcomeAccepted {
		t.Fatalf("persisted outcome=%s", outcome)
	}
	// The admission digest must be persisted, and must match the material that
	// was admitted. Without it the idempotency-conflict guard silently
	// degrades to "trust the stored decision", so assert it explicitly: the
	// guard has to be tamper-evident, not best-effort.
	env, perr := ParseNetNewInboundEnvelope(body)
	if perr != nil {
		t.Fatal(perr)
	}
	storedDigest := provenanceValue(prov, netNewProvDigest)
	if storedDigest == "" {
		t.Fatal("receipt has no admission_digest; the replay-conflict guard is inert")
	}
	if storedDigest != NetNewAdmissionDigest(env) {
		t.Fatalf("admission_digest=%s want %s", storedDigest, NetNewAdmissionDigest(env))
	}

	// Inbound-only must hold at the DB level, not only in the returned struct.
	acc, err := repository.NewOutreachRepository(pool).GetAccount(context.Background(), orgID, *first.AccountID)
	if err != nil || acc == nil {
		t.Fatalf("account: %v", err)
	}
	if !models.AccountIsInboundOnly(acc) || FirstTouchEligibleAccount(acc) {
		t.Fatal("net-new inbound account is outbound-eligible")
	}
	var inboundOnly bool
	if err := pool.QueryRow(context.Background(),
		`SELECT inbound_only FROM outreach_accounts WHERE organization_id=$1 AND id=$2`, orgID, acc.ID).Scan(&inboundOnly); err != nil {
		t.Fatalf("read inbound_only: %v", err)
	}
	if !inboundOnly {
		t.Fatal("generated column inbound_only is false for a net-new inbound account")
	}

	// Outbound-eligibility must be impossible at the DB level, not merely
	// unset by the application. Postgres must refuse the write outright.
	if _, err := pool.Exec(context.Background(),
		`UPDATE outreach_accounts SET email_send_ready=true WHERE organization_id=$1 AND id=$2`, orgID, acc.ID); err == nil {
		t.Fatal("DB allowed an inbound-only account to become outbound-eligible (email_send_ready)")
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE outreach_accounts SET target_fit_eligible=true WHERE organization_id=$1 AND id=$2`, orgID, acc.ID); err == nil {
		t.Fatal("DB allowed an inbound-only account to become outbound-eligible (target_fit_eligible)")
	}

	t.Logf("REPLAY_100_LOSS=0 REPLAY_100_DUPLICATES=0 receipts=%d accounts=%d actions=%d alerts=%d authority=%s source_sha=%s",
		leads, accounts, actions, alerts, GovernanceInboundPolicyHash, GovernanceInboundSourceSHA)
}

// TestPGGovernanceAuthorityConcurrentReplayIsOneIntent hits the same logical
// key from many goroutines at once. Sequential replay never exercises the DB
// uniqueness constraints; PersistHandRaise is check-then-act, so only
// outreach_commercial_actions_org_idempotency_uidx makes this safe.
func TestPGGovernanceAuthorityConcurrentReplayIsOneIntent(t *testing.T) {
	if postgresTestDSN() == "" {
		t.Skip("WARMBLY_TEST_POSTGRES_DSN/PRIMARY_DB is not set")
	}
	pool, orgID := openInboundOnlyPG(t)
	t.Cleanup(pool.Close)
	netNewPGCleanup(t, pool, orgID)

	svc := netNewAuthorityPGService(t, pool)
	logicalID := "nnhr-authority-conc-" + uuid.NewString()[:8]
	body := marshalNetNew(t, governanceNetNewMap(logicalID))
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

	const workers = 24
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), orgID, body, now)
			if xerr != nil {
				errs <- fmt.Errorf("worker %d: %v", i, xerr)
				return
			}
			if res.Outcome != NetNewInboundOutcomeAccepted && res.Outcome != NetNewInboundOutcomeUnknown {
				errs <- fmt.Errorf("worker %d: outcome=%s reason=%s", i, res.Outcome, res.Reason)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	// A racing loser may leave the receipt short of a commercial action; a
	// later replay must finish it. This is the fail-closed direction: retry,
	// never a duplicate.
	final, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), orgID, body, now.Add(time.Minute))
	if xerr != nil {
		t.Fatalf("settling replay: %v", xerr)
	}
	if final.Outcome != NetNewInboundOutcomeAccepted || final.ActionID == nil {
		t.Fatalf("settling replay did not converge: %+v", final)
	}

	leads, accounts, actions, _ := netNewOrgRowCounts(t, pool, orgID)
	if leads != 1 || accounts != 1 || actions != 1 {
		t.Fatalf("%d concurrent replays produced leads=%d accounts=%d actions=%d, want 1/1/1",
			workers, leads, accounts, actions)
	}
	t.Logf("CONCURRENT_REPLAY_%d receipts=%d accounts=%d actions=%d", workers, leads, accounts, actions)
}

// TestPGDifferentPayloadUnderSameLogicalIDDoesNotInheritAcceptance is the
// adversarial idempotency probe. A SECOND, materially different payload sent
// under the same logical_id must not be handed the first payload's ACCEPTED
// decision. Material that production authority would reject must never receive
// an acceptance receipt just because a prior request reused the key.
func TestPGDifferentPayloadUnderSameLogicalIDDoesNotInheritAcceptance(t *testing.T) {
	if postgresTestDSN() == "" {
		t.Skip("WARMBLY_TEST_POSTGRES_DSN/PRIMARY_DB is not set")
	}
	pool, orgID := openInboundOnlyPG(t)
	t.Cleanup(pool.Close)
	netNewPGCleanup(t, pool, orgID)

	svc := netNewAuthorityPGService(t, pool)
	logicalID := "nnhr-authority-conflict-" + uuid.NewString()[:8]
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

	good, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), orgID,
		marshalNetNew(t, governanceNetNewMap(logicalID)), now)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if good.Outcome != NetNewInboundOutcomeAccepted {
		t.Fatalf("baseline not accepted: %+v", good)
	}

	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"unpinned_content_hash", func(m map[string]any) {
			m["content_hash"] = "sha256:" + NetNewInboundPinnedHash
			m["schema_hash"] = "sha256:" + NetNewInboundPinnedHash
			m["policy_hash"] = "sha256:" + NetNewInboundPinnedHash
		}},
		{"conflict_decline", func(m map[string]any) {
			m["conflict"] = map[string]any{"status": "DECLINE", "ref": "conflict:declined"}
		}},
		{"consent_withdrawn", func(m map[string]any) {
			m["consent"] = map[string]any{"granted": false}
		}},
		{"foreign_contract_id", func(m map[string]any) {
			m["contract_id"] = "SOME_OTHER_AUTHORITY"
			m["policy_id"] = "SOME_OTHER_AUTHORITY"
			m["schema"] = "SOME_OTHER_AUTHORITY/9.9.9"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := governanceNetNewMap(logicalID)
			tc.mutate(m)
			res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), orgID, marshalNetNew(t, m), now.Add(time.Minute))
			if xerr != nil {
				t.Fatalf("mutated ingest: %v", xerr)
			}
			if res.Outcome == NetNewInboundOutcomeAccepted {
				t.Fatalf("ADMISSION BYPASS: rejectable payload (%s) inherited ACCEPTED under a reused logical_id: %+v",
					tc.name, res)
			}
			if res.Reason != NetNewInboundReasonKeyConflict {
				t.Fatalf("%s: reason=%s want %s", tc.name, res.Reason, NetNewInboundReasonKeyConflict)
			}
			if res.MeetcfgHandoff {
				t.Fatalf("%s: conflicted payload was cleared for meetcfg handoff", tc.name)
			}

			// The stored receipt must be untouched: the original decision
			// stands, and the colliding payload creates nothing.
			rb, rerr := svc.ReadbackNetNewInboundHandraiser(context.Background(), orgID, logicalID)
			if rerr != nil {
				t.Fatalf("%s: readback: %v", tc.name, rerr)
			}
			if rb.Outcome != NetNewInboundOutcomeAccepted || rb.Receipt != good.Receipt {
				t.Fatalf("%s: stored receipt was mutated by a conflicting payload: %+v", tc.name, rb)
			}
			if leads, accounts, actions, _ := netNewOrgRowCounts(t, pool, orgID); leads != 1 || accounts != 1 || actions != 1 {
				t.Fatalf("%s: conflicting payload wrote rows: leads=%d accounts=%d actions=%d",
					tc.name, leads, accounts, actions)
			}
		})
	}

	// A byte-different but logically identical re-send (different JSON key
	// order, cosmetic-only field change) must still replay, not conflict.
	same := governanceNetNewMap(logicalID)
	same["why_now"] = "requested a technical readiness assessment from the public form"
	same["correlation_id"] = "corr-" + logicalID
	res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), orgID, marshalNetNew(t, same), now.Add(2*time.Minute))
	if xerr != nil {
		t.Fatal(xerr)
	}
	if res.Outcome != NetNewInboundOutcomeAccepted || !res.Replay {
		t.Fatalf("logically identical re-send did not replay: %+v", res)
	}
}

// TestPGConflictGuardHoldsOnIncompleteReceipt closes the path this branch was
// built for: a receipt persisted but left INCOMPLETE because the downstream
// commercial write failed, which a later replay is meant to finish. A mutated
// payload must not be the thing that finishes it -- otherwise an attacker's
// material completes a receipt under the original request's identity.
func TestPGConflictGuardHoldsOnIncompleteReceipt(t *testing.T) {
	if postgresTestDSN() == "" {
		t.Skip("WARMBLY_TEST_POSTGRES_DSN/PRIMARY_DB is not set")
	}
	pool, orgID := openInboundOnlyPG(t)
	t.Cleanup(pool.Close)
	netNewPGCleanup(t, pool, orgID)

	svc := netNewAuthorityPGService(t, pool)
	logicalID := "nnhr-authority-incomplete-" + uuid.NewString()[:8]
	body := marshalNetNew(t, governanceNetNewMap(logicalID))
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

	// Force the receipt to persist while the downstream effect fails.
	svc.netNewAfterPersist = func(*models.OutreachInboundLead) error {
		return errors.New("downstream unavailable")
	}
	first, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), orgID, body, now)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if first.Outcome == NetNewInboundOutcomeAccepted {
		t.Fatalf("expected a non-accepted, retryable receipt: %+v", first)
	}
	if leads, _, actions, _ := netNewOrgRowCounts(t, pool, orgID); leads != 1 || actions != 0 {
		t.Fatalf("incomplete state not as expected: leads=%d actions=%d", leads, actions)
	}
	svc.netNewAfterPersist = nil

	// A mutated payload must NOT be allowed to complete the incomplete receipt.
	mutated := governanceNetNewMap(logicalID)
	mutated["conflict"] = map[string]any{"status": "DECLINE", "ref": "conflict:declined"}
	res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), orgID, marshalNetNew(t, mutated), now.Add(time.Minute))
	if xerr != nil {
		t.Fatal(xerr)
	}
	if res.Outcome == NetNewInboundOutcomeAccepted {
		t.Fatalf("ADMISSION BYPASS: mutated payload completed an incomplete receipt: %+v", res)
	}
	if res.Reason != NetNewInboundReasonKeyConflict {
		t.Fatalf("incomplete-row conflict reason=%s want %s", res.Reason, NetNewInboundReasonKeyConflict)
	}
	if _, _, actions, _ := netNewOrgRowCounts(t, pool, orgID); actions != 0 {
		t.Fatalf("mutated payload filed %d commercial actions against an incomplete receipt", actions)
	}

	// The ORIGINAL material must still be able to finish the receipt: the
	// guard blocks conflicting material, it does not break legitimate retry.
	settled, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), orgID, body, now.Add(2*time.Minute))
	if xerr != nil {
		t.Fatal(xerr)
	}
	if settled.Outcome != NetNewInboundOutcomeAccepted || settled.ActionID == nil {
		t.Fatalf("legitimate retry could not finish the receipt: %+v", settled)
	}
	leads, accounts, actions, _ := netNewOrgRowCounts(t, pool, orgID)
	if leads != 1 || accounts != 1 || actions != 1 {
		t.Fatalf("after settle: leads=%d accounts=%d actions=%d, want 1/1/1", leads, accounts, actions)
	}
	t.Logf("INCOMPLETE_RECEIPT_GUARD conflict_blocked=1 legit_retry_completed=1 leads=%d accounts=%d actions=%d",
		leads, accounts, actions)
}
