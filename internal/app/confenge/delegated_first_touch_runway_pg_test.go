package confenge

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/models"
)

func delegatedRunwayAuthority(t *testing.T, f *delegatedPGFixture) (*models.OutreachFeedSyncState, *models.CampaignPolicyAuthorization) {
	t.Helper()
	feed, err := f.repo.GetFeedSyncState(f.ctx, f.orgID)
	if err != nil || feed == nil {
		t.Fatalf("feed unavailable: feed=%+v err=%v", feed, err)
	}
	auth, err := f.svc.policyStore.GetCampaignPolicyByID(f.ctx, f.orgID, f.manifest.PolicyAuthorizationID)
	if err != nil || auth == nil {
		t.Fatalf("policy unavailable: auth=%+v err=%v", auth, err)
	}
	return feed, auth
}

func insertDelegatedAlternateCandidate(t *testing.T, f *delegatedPGFixture, mutate func(*models.OutreachContactCandidate)) *models.OutreachContactCandidate {
	t.Helper()
	base, err := f.repo.GetCandidate(f.ctx, f.orgID, f.manifest.Entries[0].ContactCandidateID)
	if err != nil || base == nil {
		t.Fatalf("base candidate unavailable: candidate=%+v err=%v", base, err)
	}
	alternate := *base
	alternate.ID = uuid.New()
	alternate.SourceContactID = "alternate-" + alternate.ID.String()
	alternate.Email = "alternate-" + alternate.ID.String() + "@empresa.example"
	alternate.SourceURL = "https://empresa.example/alternate/" + alternate.ID.String()
	if mutate != nil {
		mutate(&alternate)
	}
	if _, err := f.repo.UpsertCandidate(f.ctx, &alternate); err != nil {
		t.Fatal(err)
	}
	return &alternate
}

func enableDelegatedRunwayFixture(t *testing.T, f *delegatedPGFixture, days, dailyLimit int) {
	t.Helper()
	t.Setenv(EnvKillSwitchPath, filepath.Join(t.TempDir(), "not-engaged"))
	if _, err := f.pool.Exec(f.ctx, `UPDATE campaigns SET status='active',daily_limit=$2 WHERE id=$1`, f.campaignID, dailyLimit); err != nil {
		t.Fatal(err)
	}
	f.svc.cfg.DelegatedFirstTouchAutorunEnabled = true
	f.svc.cfg.DelegatedFirstTouchRunwayDays = days
	f.svc.cfg.DraftReviewBacklogTarget = 1000
	f.svc.cfg.SendingPaused = false
	f.svc.WireDispatchGovernor(dispatch.NewGovernor(dispatch.Config{
		SendsPerHour: 10, MinGap: 10 * time.Minute, Timezone: "America/Sao_Paulo",
		WindowStart: "09:00", WindowEnd: "18:00", BusinessDaysOnly: true,
		EnvPaused: true, EnvPauseReason: "delegated runway no-SMTP test",
	}, dispatch.NewPGStore(f.pool), nil))
}

func prepareDelegatedRunwayCandidates(t *testing.T, f *delegatedPGFixture, total int) {
	t.Helper()
	if total < 1 {
		return
	}
	baseEntry := f.manifest.Entries[0]
	baseAccount, err := f.repo.GetAccount(f.ctx, f.orgID, baseEntry.AccountID)
	if err != nil || baseAccount == nil {
		t.Fatalf("base account unavailable: %+v %v", baseAccount, err)
	}
	baseCandidate, err := f.repo.GetCandidate(f.ctx, f.orgID, baseEntry.ContactCandidateID)
	if err != nil || baseCandidate == nil {
		t.Fatalf("base candidate unavailable: %+v %v", baseCandidate, err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	baseCandidate.SourceDate = &now
	if _, err := f.repo.UpsertCandidate(f.ctx, baseCandidate); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.prepareDelegatedTouchpoint(f.ctx, f.orgID, baseAccount, baseCandidate, f.manifest, baseEntry); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < total; i++ {
		account := *baseAccount
		account.ID = uuid.New()
		account.SourceLeadID = fmt.Sprintf("runway-lead-%04d", i)
		account.CNPJ14 = fmt.Sprintf("%08d000100", 20000000+i)
		account.CNPJRoot = account.CNPJ14[:8]
		account.RazaoSocial = "Empresa Runway " + strings.Repeat("A", i) + " Ltda"
		account.NomeFantasia = "Empresa Runway " + strings.Repeat("A", i)
		account.SupplierCNPJ14 = account.CNPJ14
		account.SupplierIdentityRef = "cnpj:" + account.CNPJ14
		account.ContractorRoleEvidenceIDs = []string{fmt.Sprintf("contract-runway-%04d", i)}
		account.ContractorRoleObservedAt = &now
		if _, err := f.repo.UpsertAccount(f.ctx, &account); err != nil {
			t.Fatal(err)
		}

		candidate := *baseCandidate
		candidate.ID = uuid.New()
		candidate.AccountID = account.ID
		candidate.SourceContactID = fmt.Sprintf("route-runway-%04d", i)
		candidate.Email = fmt.Sprintf("contato-%04d@empresa.example", i)
		candidate.SourceURL = fmt.Sprintf("https://empresa-%04d.example/contato", i)
		candidate.SourceDate = &now
		if _, err := f.repo.UpsertCandidate(f.ctx, &candidate); err != nil {
			t.Fatal(err)
		}
		evidence := models.OutreachEvidence{
			ID: uuid.New(), OrganizationID: f.orgID, AccountID: account.ID,
			SourceEvidenceID: account.ContractorRoleEvidenceIDs[0], EvidenceType: "CONTRACT",
			URL: "https://pncp.gov.br/contratos/runway", Synthesis: account.NomeFantasia + " figura como contratada.",
			EpistemicClass: models.OutreachEpistemicConfirmedFact, Reliability: "HIGH",
			ConsultedAt: &now, LastImportRunID: account.LastImportRunID,
		}
		if _, err := f.repo.UpsertEvidence(f.ctx, &evidence); err != nil {
			t.Fatal(err)
		}
		copy := buildDelegatedRoutingCopy(&account, &candidate, []models.OutreachEvidence{evidence})
		entry := delegatedEntryFromCurrentState(&account, &candidate, uuid.New(), copy)
		entry.IdempotencyKey = "prepared-runway:" + account.ID.String()
		entry.CorrelationID = "prepared-runway:" + account.ID.String()
		entry.Recipient = candidate.Email
		entry.RouteClass = CandidateRouteClass(&candidate)
		entry.SubjectHash, entry.BodyHash = hashText(entry.Subject), hashText(entry.BodyText)
		if _, _, err := f.svc.prepareDelegatedTouchpoint(f.ctx, f.orgID, &account, &candidate, f.manifest, entry); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDelegatedFirstTouchRunwayFillsThirtyDaysPostgres(t *testing.T) {
	f := newDelegatedPGFixture(t)
	enableDelegatedRunwayFixture(t, f, 30, 1)
	prepareDelegatedRunwayCandidates(t, f, 40)

	processed := 0
	for ; processed < 100; processed++ {
		ok, err := f.svc.ProcessDelegatedFirstTouchOnce(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
	}
	if processed < 2 || processed >= 100 {
		var states string
		_ = f.pool.QueryRow(f.ctx, `SELECT COALESCE(string_agg(state || ':' || blocker_codes::text,','),'') FROM confenge_delegated_first_touch_decisions WHERE organization_id=$1`, f.orgID).Scan(&states)
		feed, _ := f.repo.GetFeedSyncState(f.ctx, f.orgID)
		auth, _ := f.svc.policyStore.GetCampaignPolicyByID(f.ctx, f.orgID, f.manifest.PolicyAuthorizationID)
		plan, _ := f.svc.delegatedFirstTouchRunwayPlan(f.ctx, f.orgID, feed, auth, time.Now().UTC())
		t.Fatalf("runway fill processed=%d, want finite multi-item fill; plan=%+v states=%s", processed, plan, states)
	}
	metrics := f.svc.delegatedFirstTouchRunwayMetrics(f.ctx, f.orgID)
	if metrics.CapacityBlocked != 0 || metrics.QueuedCount < 2 || metrics.ReservedCount != 0 {
		var blockers string
		_ = f.pool.QueryRow(f.ctx, `SELECT COALESCE(string_agg(blocker_codes::text,','),'') FROM confenge_delegated_first_touch_decisions WHERE organization_id=$1 AND state='HOLD'`, f.orgID).Scan(&blockers)
		t.Fatalf("unexpected runway metrics: %+v blockers=%s", metrics, blockers)
	}
	if metrics.FurthestDueAt == nil || metrics.TargetRunwayUntil == nil || metrics.FurthestDueAt.Before(*metrics.TargetRunwayUntil) {
		var states string
		_ = f.pool.QueryRow(f.ctx, `SELECT COALESCE(string_agg(state || ':' || blocker_codes::text,','),'') FROM confenge_delegated_first_touch_decisions WHERE organization_id=$1`, f.orgID).Scan(&states)
		t.Fatalf("runway did not reach 30-day target: %+v states=%s", metrics, states)
	}
	if metrics.CurrentScheduledCount < metrics.TargetScheduledCount || metrics.CurrentScheduledCount > metrics.TargetScheduledCount+1 {
		t.Fatalf("scheduled=%d target=%d", metrics.CurrentScheduledCount, metrics.TargetScheduledCount)
	}
	var queued, distinctDue int
	if err := f.pool.QueryRow(f.ctx, `
		SELECT count(*)::int,count(DISTINCT due_at)::int
		FROM confenge_dispatch_queue
		WHERE organization_id=$1 AND status IN ('queued','reserved')`, f.orgID).Scan(&queued, &distinctDue); err != nil {
		t.Fatal(err)
	}
	if queued != distinctDue || queued < 2 {
		t.Fatalf("queue does not hold distinct future slots: queued=%d distinct_due=%d", queued, distinctDue)
	}

	restarted := NewService(f.svc.cfg, f.repo, nil).(*service)
	restarted.WirePolicyAuth(f.svc.policyStore)
	restarted.WireDelegatedFirstTouch(f.pool)
	restarted.WireOrgRisk(delegatedTestOrgRisk{})
	restarted.WireDispatchGovernor(f.svc.governor)
	if ok, err := restarted.ProcessDelegatedFirstTouchOnce(f.ctx); err != nil || ok {
		t.Fatalf("restart changed a full runway: processed=%v err=%v", ok, err)
	}
	var afterRestart int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*)::int FROM confenge_dispatch_queue WHERE organization_id=$1 AND status IN ('queued','reserved')`, f.orgID).Scan(&afterRestart); err != nil {
		t.Fatal(err)
	}
	if afterRestart != queued {
		t.Fatalf("restart duplicated queue: before=%d after=%d", queued, afterRestart)
	}
}

func TestDelegatedFirstTouchReadyReservoirCountsOneThousandPostgres(t *testing.T) {
	f := newDelegatedPGFixture(t)
	enableDelegatedRunwayFixture(t, f, 30, 50)
	prepareDelegatedRunwayCandidates(t, f, 1)
	baseAccountID := f.manifest.Entries[0].AccountID
	baseCandidateID := f.manifest.Entries[0].ContactCandidateID
	baseTouchpointID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(DelegatedFirstTouchPolicyV1+"\x00touchpoint\x00"+f.orgID.String()+"\x00"+f.manifest.SourceRunID+"\x00"+baseAccountID.String()))
	if _, err := f.pool.Exec(f.ctx, `
		WITH new_accounts AS (
		  INSERT INTO outreach_accounts
		  SELECT (jsonb_populate_record(NULL::outreach_accounts,
		    to_jsonb(a) || jsonb_build_object(
		      'id',gen_random_uuid(),'source_lead_id','reservoir-' || g::text,
		      'cnpj14',lpad((40000000000000+g)::text,14,'0'),
		      'cnpj_root',left((40000000000000+g)::text,8),
		      'supplier_cnpj14',lpad((40000000000000+g)::text,14,'0'),
		      'supplier_identity_ref','cnpj:' || lpad((40000000000000+g)::text,14,'0'),
		      'moment_evidence_ids','[]'::jsonb,
		      'claims_to_avoid','[]'::jsonb,
		      'contracts_json','[]'::jsonb,
		      'raw_snapshot','{}'::jsonb,
		      'activation_reason_codes','[]'::jsonb,
		      'score_components','{}'::jsonb,
		      'target_fit_reasons','[]'::jsonb,
		      'target_fit_evidence_ids','[]'::jsonb,
		      'contractor_role_evidence_ids','[]'::jsonb,
		      'contractor_role_reason_codes','[]'::jsonb,
		      'created_at',now(),'updated_at',now()
		    ))).*
		  FROM outreach_accounts a CROSS JOIN generate_series(1,999) g
		  WHERE a.organization_id=$1 AND a.id=$2
		  RETURNING id,source_lead_id
		), new_candidates AS (
		  INSERT INTO outreach_contact_candidates
		  SELECT (jsonb_populate_record(NULL::outreach_contact_candidates,
		    to_jsonb(c) || jsonb_build_object(
		      'id',gen_random_uuid(),'account_id',a.id,'source_contact_id',a.source_lead_id,
		      'email',a.source_lead_id || '@empresa.example',
		      'source_url','https://' || a.source_lead_id || '.empresa.example/contato',
		      'created_at',now(),'updated_at',now()
		    ))).*
		  FROM new_accounts a CROSS JOIN outreach_contact_candidates c
		  WHERE c.organization_id=$1 AND c.id=$3
		  RETURNING id,account_id,source_contact_id
		)
		INSERT INTO outreach_touchpoints
		SELECT (jsonb_populate_record(NULL::outreach_touchpoints,
		  to_jsonb(t) || jsonb_build_object(
		    'id',gen_random_uuid(),'account_id',c.account_id,'contact_candidate_id',c.id,
		    'draft_id',NULL,'idempotency_key','reservoir-touch:' || c.source_contact_id,
		    'created_at',now(),'updated_at',now()
		  ))).*
		FROM new_candidates c CROSS JOIN outreach_touchpoints t
		WHERE t.organization_id=$1 AND t.id=$4`, f.orgID, baseAccountID, baseCandidateID, baseTouchpointID); err != nil {
		t.Fatal(err)
	}
	feed, err := f.repo.GetFeedSyncState(f.ctx, f.orgID)
	if err != nil || feed == nil {
		t.Fatalf("feed unavailable: %+v %v", feed, err)
	}
	auth, err := f.svc.policyStore.GetCampaignPolicyByID(f.ctx, f.orgID, f.manifest.PolicyAuthorizationID)
	if err != nil || auth == nil {
		t.Fatalf("policy unavailable: %+v %v", auth, err)
	}
	ready, err := f.svc.delegatedFirstTouchReadyReservoirCount(f.ctx, f.orgID, feed, auth)
	if err != nil || ready != 1000 {
		t.Fatalf("ready reservoir=%d want 1000 err=%v", ready, err)
	}
	metrics := f.svc.delegatedFirstTouchRunwayMetrics(f.ctx, f.orgID)
	if metrics.ReadyReservoirCount != 1000 || metrics.MinReadyReservoir != 1000 {
		t.Fatalf("reservoir metrics mismatch: %+v", metrics)
	}
	var queued int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*)::int FROM confenge_dispatch_queue WHERE organization_id=$1`, f.orgID).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 0 {
		t.Fatalf("prepared reservoir caused mass scheduling: queued=%d", queued)
	}
}

func TestDelegatedFirstTouchRunwayUsesOnlyAuthorizedMailboxPostgres(t *testing.T) {
	f := newDelegatedPGFixture(t)
	enableDelegatedRunwayFixture(t, f, 30, 50)
	feed, _ := f.repo.GetFeedSyncState(f.ctx, f.orgID)
	auth, _ := f.svc.policyStore.GetCampaignPolicyByID(f.ctx, f.orgID, f.manifest.PolicyAuthorizationID)
	one, err := f.svc.delegatedFirstTouchRunwayPlan(f.ctx, f.orgID, feed, auth, time.Now().UTC())
	if err != nil || !one.CapacityKnown || one.MailboxCount != 1 || one.DailyCapacity != 10 {
		t.Fatalf("one mailbox plan: %+v err=%v", one, err)
	}
	mailboxID := uuid.New()
	mailbox := "runway-second-" + f.orgID.String() + "@example.test"
	if _, err := f.pool.Exec(f.ctx, `
		INSERT INTO email_accounts(id,user_id,organization_id,worker_id,email,name,signature_plain,signature_html,provider,status,warmup_tag,campaign_limit,min_wait_time,risk_band,
			auth_state,auth_spf,auth_dkim,auth_dmarc,auth_checked_at)
		VALUES($1,$2,$3,$4,$5,'Runway 2','','','smtp_imap','active','runway-test',50,600,'clean','passing',true,true,true,now())`,
		mailboxID, f.actorID, f.orgID, f.workerID, mailbox); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `
		INSERT INTO email_accounts_smtp_imap(email_account_id,smtp_host,smtp_port,smtp_user,smtp_password,imap_host,imap_port,imap_user,imap_password)
		VALUES($1,'smtp.invalid',587,$2,'encrypted-test','imap.invalid',993,$2,'encrypted-test')`, mailboxID, mailbox); err != nil {
		t.Fatal(err)
	}
	two, err := f.svc.delegatedFirstTouchRunwayPlan(f.ctx, f.orgID, feed, auth, time.Now().UTC())
	if err != nil || !two.CapacityKnown || two.MailboxCount != 1 || two.DailyCapacity != 10 {
		t.Fatalf("two mailbox plan: %+v err=%v", two, err)
	}
}

func TestDelegatedFirstTouchReservedQueueRaceKeepsReadbackPostgres(t *testing.T) {
	f := newDelegatedPGFixture(t)
	report, xerr := f.svc.ApplyDelegatedFirstTouchManifest(f.ctx, f.orgID, f.manifest, false)
	if xerr != nil || report == nil || report.Queued != 1 {
		t.Fatalf("queue failed: %+v %v", report, xerr)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE confenge_dispatch_queue SET status='reserved',reserved_until=now()+interval '5 minutes' WHERE organization_id=$1`, f.orgID); err != nil {
		t.Fatal(err)
	}
	tp, err := f.repo.GetTouchpoint(f.ctx, f.orgID, report.Items[0].TouchpointID)
	if err != nil || tp == nil {
		t.Fatalf("touchpoint unavailable: %+v %v", tp, err)
	}
	if _, _, ok := f.svc.delegatedQueueReadback(f.ctx, f.orgID, tp); !ok {
		t.Fatal("reserved queue lost canonical readback")
	}
	if repaired, err := f.svc.ReconcileDelegatedFirstTouchLedger(f.ctx, f.orgID); err != nil || repaired != 0 {
		t.Fatalf("reserve race falsely cancelled ledger: repaired=%d err=%v", repaired, err)
	}
}

func TestDelegatedFirstTouchManifestReplayTenTimesPostgres(t *testing.T) {
	f := newDelegatedPGFixture(t)
	for i := 0; i < 10; i++ {
		report, xerr := f.svc.ApplyDelegatedFirstTouchManifest(f.ctx, f.orgID, f.manifest, false)
		if xerr != nil || report == nil || report.Queued != 1 || (i > 0 && !report.Items[0].Idempotent) {
			t.Fatalf("replay %d: report=%+v err=%v", i+1, report, xerr)
		}
	}
	var decisions, queue int
	if err := f.pool.QueryRow(f.ctx, `SELECT
		(SELECT count(*) FROM confenge_delegated_first_touch_decisions WHERE organization_id=$1),
		(SELECT count(*) FROM confenge_dispatch_queue WHERE organization_id=$1)`, f.orgID).Scan(&decisions, &queue); err != nil {
		t.Fatal(err)
	}
	if decisions != 1 || queue != 1 {
		t.Fatalf("10x replay duplicated state: decisions=%d queue=%d", decisions, queue)
	}
}

func TestDelegatedFirstTouchRunwayRefreshesBindingsAndStopsOnMailboxPausePostgres(t *testing.T) {
	f := newDelegatedPGFixture(t)
	enableDelegatedRunwayFixture(t, f, 1, 1)
	prepareDelegatedRunwayCandidates(t, f, 1)
	if ok, err := f.svc.ProcessDelegatedFirstTouchOnce(f.ctx); err != nil || !ok {
		t.Fatalf("initial runway fill: processed=%v err=%v", ok, err)
	}

	entry := f.manifest.Entries[0]
	account, err := f.repo.GetAccount(f.ctx, f.orgID, entry.AccountID)
	if err != nil || account == nil {
		t.Fatalf("account unavailable: %+v %v", account, err)
	}
	candidate, err := f.repo.GetCandidate(f.ctx, f.orgID, entry.ContactCandidateID)
	if err != nil || candidate == nil {
		t.Fatalf("candidate unavailable: %+v %v", candidate, err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	newRunID, newSnapshot, newImportID := "run-refresh-"+uuid.NewString(), hashText("snapshot-"+uuid.NewString()), uuid.New()
	if err := f.repo.CreateImportRun(f.ctx, &models.OutreachImportRun{
		ID: newImportID, OrganizationID: f.orgID, SourceSystem: "extra-cli", SourceRunID: newRunID,
		SchemaVersion: models.OutreachSchemaV1, SnapshotHash: newSnapshot, Status: models.OutreachImportCompleted,
		StartedAt: now, FinishedAt: &now, IdempotencyKey: "import-" + newImportID.String(), SourceGeneratedAt: &now,
	}); err != nil {
		t.Fatal(err)
	}
	account.SourceRunID, account.ContractorRoleSourceRunID, account.LastImportRunID = newRunID, newRunID, &newImportID
	account.ContractorRoleObservedAt = &now
	if _, err := f.repo.UpsertAccount(f.ctx, account); err != nil {
		t.Fatal(err)
	}
	candidate.LastImportRunID, candidate.SourceDate = &newImportID, &now
	if _, err := f.repo.UpsertCandidate(f.ctx, candidate); err != nil {
		t.Fatal(err)
	}
	evidence, err := f.repo.ListEvidence(f.ctx, f.orgID, account.ID)
	if err != nil || len(evidence) == 0 {
		t.Fatalf("evidence unavailable: %v %v", evidence, err)
	}
	for i := range evidence {
		evidence[i].LastImportRunID, evidence[i].ConsultedAt = &newImportID, &now
		if _, err := f.repo.UpsertEvidence(f.ctx, &evidence[i]); err != nil {
			t.Fatal(err)
		}
	}
	sourceExpiresAt := now.Add(time.Hour)
	if err := f.repo.UpsertFeedSyncState(f.ctx, &models.OutreachFeedSyncState{
		OrganizationID: f.orgID, LastSnapshotHash: newSnapshot, LastRunID: newRunID,
		LastManifestURI: "file:///runway-refresh.json", LastSuccessAt: &now, LastAttemptAt: &now,
		LastStatus: "completed", SourceGeneratedAt: &now, SourceExpiresAt: &sourceExpiresAt,
		SourceFreshnessHash: hashText("freshness-" + newRunID), TargetMembershipComplete: true,
		TargetMembershipHash: hashText("membership-" + newRunID), TargetMembershipCount: 1, SupplierConfirmedCount: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.CommitFeedRun(f.ctx, f.orgID, newRunID, newSnapshot, now); err != nil {
		t.Fatal(err)
	}
	backlog, err := f.repo.MaterializeCurrentInitialBacklog(f.ctx, f.orgID, newRunID)
	if err != nil || backlog.InitialPrepared != 1 || backlog.StaleRetired != 1 {
		t.Fatalf("feed refresh materialization: counts=%+v err=%v", backlog, err)
	}
	if ok, err := f.svc.ProcessDelegatedFirstTouchOnce(f.ctx); err != nil || !ok {
		t.Fatalf("feed refresh replenishment: processed=%v err=%v", ok, err)
	}
	assertDelegatedBindingCounts(t, f, 1, 1)

	f.svc.cfg.RepositorySHA = "sha-delegated-runway-refresh"
	if ok, err := f.svc.ProcessDelegatedFirstTouchOnce(f.ctx); err != nil || !ok {
		t.Fatalf("runtime refresh replenishment: processed=%v err=%v", ok, err)
	}
	assertDelegatedBindingCounts(t, f, 2, 1)

	oldAuth, err := f.svc.policyStore.GetActiveCampaignPolicy(f.ctx, f.orgID, f.campaignID, now)
	if err != nil || oldAuth == nil {
		t.Fatalf("active policy unavailable: %+v %v", oldAuth, err)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE confenge_campaign_policy_authorizations SET revoked_at=now() WHERE organization_id=$1 AND id=$2`, f.orgID, oldAuth.ID); err != nil {
		t.Fatal(err)
	}
	newAuth := *oldAuth
	newAuth.ID, newAuth.RevokedAt, newAuth.EffectiveAt = uuid.New(), nil, time.Now().UTC().Add(-time.Second)
	if _, err := f.svc.policyStore.InsertCampaignPolicy(f.ctx, f.orgID, &newAuth); err != nil {
		t.Fatal(err)
	}
	if ok, err := f.svc.ProcessDelegatedFirstTouchOnce(f.ctx); err != nil || !ok {
		t.Fatalf("policy refresh replenishment: processed=%v err=%v", ok, err)
	}
	assertDelegatedBindingCounts(t, f, 3, 1)

	var touchpointID uuid.UUID
	if err := f.pool.QueryRow(f.ctx, `
		SELECT touchpoint_id FROM confenge_delegated_first_touch_decisions
		WHERE organization_id=$1 AND state='QUEUED'`, f.orgID).Scan(&touchpointID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE email_accounts SET status='inactive' WHERE organization_id=$1`, f.orgID); err != nil {
		t.Fatal(err)
	}
	if ok, err := f.svc.ProcessDelegatedFirstTouchOnce(f.ctx); err != nil || ok {
		t.Fatalf("paused mailbox allowed runway fill: processed=%v err=%v", ok, err)
	}
	tp, err := f.repo.GetTouchpoint(f.ctx, f.orgID, touchpointID)
	if err != nil || tp == nil {
		t.Fatalf("touchpoint unavailable: %+v %v", tp, err)
	}
	if err := f.svc.AssertTransportable(f.ctx, f.orgID, tp); err == nil {
		t.Fatal("mailbox pause remained transportable")
	}
	assertDelegatedBindingCounts(t, f, 4, 0)
}

func assertDelegatedBindingCounts(t *testing.T, f *delegatedPGFixture, cancelled, queued int) {
	t.Helper()
	var gotCancelled, gotQueued int
	if err := f.pool.QueryRow(f.ctx, `SELECT
		count(*) FILTER (WHERE state='CANCELLED')::int,
		count(*) FILTER (WHERE state='QUEUED')::int
		FROM confenge_delegated_first_touch_decisions WHERE organization_id=$1`, f.orgID).Scan(&gotCancelled, &gotQueued); err != nil {
		t.Fatal(err)
	}
	if gotCancelled != cancelled || gotQueued != queued {
		var states string
		_ = f.pool.QueryRow(f.ctx, `SELECT COALESCE(string_agg(state || ':' || blocker_codes::text,','),'') FROM confenge_delegated_first_touch_decisions WHERE organization_id=$1`, f.orgID).Scan(&states)
		t.Fatalf("binding counts cancelled=%d/%d queued=%d/%d states=%s", gotCancelled, cancelled, gotQueued, queued, states)
	}
}

func TestDelegatedFirstTouchRunwayNeverSelectsFollowupsPostgres(t *testing.T) {
	f := newDelegatedPGFixture(t)
	enableDelegatedRunwayFixture(t, f, 30, 50)
	prepareDelegatedRunwayCandidates(t, f, 1)
	baseID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(DelegatedFirstTouchPolicyV1+"\x00touchpoint\x00"+f.orgID.String()+"\x00"+f.manifest.SourceRunID+"\x00"+f.manifest.Entries[0].AccountID.String()))
	base, err := f.repo.GetTouchpoint(f.ctx, f.orgID, baseID)
	if err != nil || base == nil {
		t.Fatalf("base first touch unavailable: %+v %v", base, err)
	}
	followup := *base
	followup.ID, followup.Ordinal, followup.Purpose = uuid.New(), 2, models.TouchpointPurposeFollowUp
	followup.DueAt, followup.IdempotencyKey = time.Now().UTC().Add(-24*time.Hour), "runway-followup:"+uuid.NewString()
	if err := f.repo.InsertTouchpoint(f.ctx, &followup); err != nil {
		t.Fatal(err)
	}
	feed, _ := f.repo.GetFeedSyncState(f.ctx, f.orgID)
	auth, _ := f.svc.policyStore.GetCampaignPolicyByID(f.ctx, f.orgID, f.manifest.PolicyAuthorizationID)
	touchpointID, _, _, err := f.svc.nextDelegatedFirstTouchCandidate(f.ctx, f.orgID, feed, auth)
	if err != nil {
		t.Fatal(err)
	}
	if touchpointID != base.ID {
		t.Fatalf("runway selected non-first-touch row: got=%s first=%s followup=%s", touchpointID, base.ID, followup.ID)
	}
}

func TestDelegatedFirstTouchRunwayRebindsOnlyToCurrentControlledCandidatePostgres(t *testing.T) {
	f := newDelegatedPGFixture(t)
	qualifyDelegatedPGFixture(t, f)
	prepareDelegatedRunwayCandidates(t, f, 1)

	// Institutional controlled routes intentionally do not inherit the strict
	// named-person EMAIL_SEND_READY bit.
	alternate := insertDelegatedAlternateCandidate(t, f, func(c *models.OutreachContactCandidate) {
		c.EmailSendReady = false
	})
	if _, err := f.pool.Exec(f.ctx, `
		UPDATE outreach_contact_candidates SET blocked=true
		WHERE organization_id=$1 AND id=$2`, f.orgID, f.manifest.Entries[0].ContactCandidateID); err != nil {
		t.Fatal(err)
	}
	feed, auth := delegatedRunwayAuthority(t, f)
	touchpointID, _, candidateID, err := f.svc.nextDelegatedFirstTouchCandidate(f.ctx, f.orgID, feed, auth)
	if err != nil {
		t.Fatal(err)
	}
	if candidateID != alternate.ID {
		t.Fatalf("selected candidate=%s want current controlled alternate=%s", candidateID, alternate.ID)
	}
	reserved, err := f.repo.GetTouchpoint(f.ctx, f.orgID, touchpointID)
	if err != nil || reserved == nil || reserved.ContactCandidateID == nil {
		t.Fatalf("reserved touchpoint unavailable: touchpoint=%+v err=%v", reserved, err)
	}
	if *reserved.ContactCandidateID != f.manifest.Entries[0].ContactCandidateID {
		t.Fatalf("reservation rebound recipient before prepare: got=%s want old=%s", *reserved.ContactCandidateID, f.manifest.Entries[0].ContactCandidateID)
	}
}

func TestDelegatedFirstTouchRunwayRejectsHistoricalAndUnsafeAlternatesPostgres(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*models.OutreachContactCandidate)
	}{
		{"historical_import", func(c *models.OutreachContactCandidate) { id := uuid.New(); c.LastImportRunID = &id }},
		{"not_controlled", func(c *models.OutreachContactCandidate) {
			c.DiscoveryJSON = []byte(`{"route_class":"GENERIC_COMPANY","preferred_initial":true}`)
		}},
		{"company_evidence_missing", func(c *models.OutreachContactCandidate) {
			c.DiscoveryJSON = []byte(`{"route_class":"GENERIC_COMPANY","controlled_email_eligible":true,"preferred_initial":true}`)
		}},
		{"inferred", func(c *models.OutreachContactCandidate) { c.EmailDerivation = "INFERRED" }},
		{"suppressed", func(c *models.OutreachContactCandidate) { c.RouteSuppression = "DNC" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newDelegatedPGFixture(t)
			qualifyDelegatedPGFixture(t, f)
			prepareDelegatedRunwayCandidates(t, f, 1)
			if _, err := f.pool.Exec(f.ctx, `
				UPDATE outreach_contact_candidates SET blocked=true
				WHERE organization_id=$1 AND id=$2`, f.orgID, f.manifest.Entries[0].ContactCandidateID); err != nil {
				t.Fatal(err)
			}
			insertDelegatedAlternateCandidate(t, f, tc.mutate)
			feed, auth := delegatedRunwayAuthority(t, f)
			if _, _, _, err := f.svc.nextDelegatedFirstTouchCandidate(f.ctx, f.orgID, feed, auth); !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("unsafe alternate became selectable: %v", err)
			}
			ready, err := f.svc.delegatedFirstTouchReadyReservoirCount(f.ctx, f.orgID, feed, auth)
			if err != nil || ready != 0 {
				t.Fatalf("reservoir drifted from selector: ready=%d err=%v", ready, err)
			}
		})
	}
}

func TestDelegatedFirstTouchRunwayHoldConsumesCandidateNotAccountPostgres(t *testing.T) {
	f := newDelegatedPGFixture(t)
	qualifyDelegatedPGFixture(t, f)
	prepareDelegatedRunwayCandidates(t, f, 1)
	alternate := insertDelegatedAlternateCandidate(t, f, nil)

	manifest := f.manifest
	entry := manifest.Entries[0]
	entry.IdempotencyKey = "old-candidate-hold-" + uuid.NewString()
	if err := f.svc.persistDelegatedHold(f.ctx, f.orgID, manifest, entry, []string{"recipient_not_controlled_eligible"}); err != nil {
		t.Fatal(err)
	}
	feed, auth := delegatedRunwayAuthority(t, f)
	_, _, candidateID, err := f.svc.nextDelegatedFirstTouchCandidate(f.ctx, f.orgID, feed, auth)
	if err != nil {
		t.Fatal(err)
	}
	if candidateID != alternate.ID {
		t.Fatalf("old candidate HOLD blocked safe alternate: got=%s want=%s", candidateID, alternate.ID)
	}
}
