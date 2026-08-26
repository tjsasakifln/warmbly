package confenge

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

func seedDelegatedRunwayAccount(t *testing.T, f *delegatedPGFixture, ordinal int) {
	t.Helper()
	baseEntry := f.manifest.Entries[0]
	account, err := f.repo.GetAccount(f.ctx, f.orgID, baseEntry.AccountID)
	if err != nil || account == nil {
		t.Fatalf("base runway account unavailable: account=%+v err=%v", account, err)
	}
	candidate, err := f.repo.GetCandidate(f.ctx, f.orgID, baseEntry.ContactCandidateID)
	if err != nil || candidate == nil {
		t.Fatalf("base runway candidate unavailable: candidate=%+v err=%v", candidate, err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if ordinal > 0 {
		account.ID = uuid.New()
		account.SourceLeadID = fmt.Sprintf("lead-runway-%d", ordinal)
		account.CNPJ14 = fmt.Sprintf("%08d000144", 20_000_000+ordinal)
		account.CNPJRoot = account.CNPJ14[:8]
		account.SupplierCNPJ14 = account.CNPJ14
		account.SupplierIdentityRef = "cnpj:" + account.CNPJ14
		account.ContractorRoleEvidenceIDs = []string{fmt.Sprintf("contract-runway-%d", ordinal)}
		if _, err := f.repo.UpsertAccount(f.ctx, account); err != nil {
			t.Fatal(err)
		}
		candidate.ID = uuid.New()
		candidate.AccountID = account.ID
		candidate.SourceContactID = fmt.Sprintf("route-runway-%d", ordinal)
		candidate.Email = fmt.Sprintf("route-%d@company-%d.example", ordinal, ordinal)
		candidate.SourceURL = fmt.Sprintf("https://company-%d.example/contact", ordinal)
		candidate.SourceDate = &now
		if _, err := f.repo.UpsertCandidate(f.ctx, candidate); err != nil {
			t.Fatal(err)
		}
		if _, err := f.repo.UpsertEvidence(f.ctx, &models.OutreachEvidence{
			ID: uuid.New(), OrganizationID: f.orgID, AccountID: account.ID,
			SourceEvidenceID: account.ContractorRoleEvidenceIDs[0], EvidenceType: "CONTRACT",
			URL:       fmt.Sprintf("https://pncp.gov.br/contratos/runway-%d", ordinal),
			Synthesis: "Synthetic runway supplier evidence", EpistemicClass: models.OutreachEpistemicConfirmedFact,
			Reliability: "HIGH", ConsultedAt: &now, LastImportRunID: account.LastImportRunID,
		}); err != nil {
			t.Fatal(err)
		}
	} else {
		candidate.SourceDate = &now
		if _, err := f.repo.UpsertCandidate(f.ctx, candidate); err != nil {
			t.Fatal(err)
		}
	}

	entry := baseEntry
	entry.IdempotencyKey = fmt.Sprintf("seed-runway:%d:%s", ordinal, account.ID)
	entry.CorrelationID = fmt.Sprintf("seed-runway:%d", ordinal)
	entry.AccountID = account.ID
	entry.ContactCandidateID = candidate.ID
	entry.CNPJ14 = account.CNPJ14
	entry.SupplierCNPJ14 = account.SupplierCNPJ14
	entry.SupplierIdentityRef = account.SupplierIdentityRef
	entry.ContractEvidenceIDs = append([]string{}, account.ContractorRoleEvidenceIDs...)
	entry.EvidenceIDs = append([]string{}, account.ContractorRoleEvidenceIDs...)
	entry.Recipient = candidate.Email
	entry.WebSources = []DelegatedWebSource{{
		URL: candidate.SourceURL, Kind: "OFFICIAL_COMPANY_PAGE",
		Supports: "COMPANY_IDENTITY_AND_MAILBOX", ObservedAt: now,
	}}
	entry.SubjectHash, entry.BodyHash = hashText(entry.Subject), hashText(entry.BodyText)
	if _, _, err := f.svc.prepareDelegatedTouchpoint(f.ctx, f.orgID, account, candidate, f.manifest, entry); err != nil {
		t.Fatalf("seed runway touchpoint %d: %v", ordinal, err)
	}
}

func TestDelegatedFirstTouchWorkerFillsBoundedSpacedRunwayAndRestarts(t *testing.T) {
	f := newDelegatedPGFixture(t)
	const target = 3
	for index := 0; index < target+1; index++ {
		seedDelegatedRunwayAccount(t, f, index)
	}
	f.svc.cfg.DelegatedFirstTouchAutorunEnabled = true
	f.svc.cfg.DelegatedFirstTouchRunwayTarget = target

	for index := 0; index < target; index++ {
		processed, err := f.svc.ProcessDelegatedFirstTouchOnce(f.ctx)
		if err != nil || !processed {
			t.Fatalf("runway fill %d: processed=%v err=%v", index, processed, err)
		}
	}
	if processed, err := f.svc.ProcessDelegatedFirstTouchOnce(f.ctx); err != nil || processed {
		t.Fatalf("worker exceeded runway: processed=%v err=%v", processed, err)
	}

	rows, err := f.pool.Query(f.ctx, `
		SELECT due_at FROM confenge_dispatch_queue
		WHERE organization_id=$1 AND status IN ('queued','reserved') ORDER BY due_at`, f.orgID)
	if err != nil {
		t.Fatal(err)
	}
	var due []time.Time
	for rows.Next() {
		var value time.Time
		if err := rows.Scan(&value); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		due = append(due, value)
	}
	rows.Close()
	if len(due) != target {
		t.Fatalf("queued runway=%d want %d", len(due), target)
	}
	for index := 1; index < len(due); index++ {
		if gap := due[index].Sub(due[index-1]); gap < 10*time.Minute {
			t.Fatalf("runway gap[%d]=%s want >=10m; due=%v", index, gap, due)
		}
	}

	restarted := NewService(f.svc.cfg, f.repo, nil).(*service)
	restarted.WirePolicyAuth(f.svc.policyStore)
	restarted.WireDelegatedFirstTouch(f.pool)
	restarted.WireOrgRisk(delegatedTestOrgRisk{})
	restarted.WireDispatchGovernor(f.svc.governor)
	for replay := 0; replay < 10; replay++ {
		if processed, err := restarted.ProcessDelegatedFirstTouchOnce(f.ctx); err != nil || processed {
			t.Fatalf("restart replay %d changed a full runway: processed=%v err=%v", replay, processed, err)
		}
	}

	if _, err := f.pool.Exec(f.ctx, `
		UPDATE confenge_dispatch_queue SET status='sent'
		WHERE id=(SELECT id FROM confenge_dispatch_queue WHERE organization_id=$1 AND status='queued' ORDER BY due_at LIMIT 1)`,
		f.orgID); err != nil {
		t.Fatal(err)
	}
	if processed, err := restarted.ProcessDelegatedFirstTouchOnce(f.ctx); err != nil || !processed {
		t.Fatalf("restart did not refill runway: processed=%v err=%v", processed, err)
	}

	var live, duplicateKeys, sentTouches int
	if err := f.pool.QueryRow(f.ctx, `SELECT
		(SELECT count(*) FROM confenge_dispatch_queue WHERE organization_id=$1 AND status IN ('queued','reserved')),
		(SELECT count(*) FROM (SELECT message_key FROM confenge_dispatch_queue WHERE organization_id=$1 GROUP BY message_key HAVING count(*)>1) duplicates),
		(SELECT count(*) FROM outreach_touchpoints WHERE organization_id=$1 AND state='SENT')`, f.orgID).
		Scan(&live, &duplicateKeys, &sentTouches); err != nil {
		t.Fatal(err)
	}
	if live != target || duplicateKeys != 0 || sentTouches != 0 {
		t.Fatalf("runway convergence drift: live=%d duplicates=%d sent_touchpoints=%d", live, duplicateKeys, sentTouches)
	}
}

func TestCurrentRunInitialBacklogRefreshRetiresAndReplenishesPostgres(t *testing.T) {
	f := newDelegatedPGFixture(t)
	f.svc.cfg.DelegatedFirstTouchAutorunEnabled = true
	f.svc.cfg.DelegatedFirstTouchRunwayTarget = 1

	first, err := f.repo.MaterializeCurrentInitialBacklog(f.ctx, f.orgID, f.manifest.SourceRunID)
	if err != nil || first.Imported != 1 || first.InitialPrepared != 1 || first.DelegatedEligible != 1 || first.HeldException != 0 {
		t.Fatalf("first materialization did not close the funnel: counts=%+v err=%v", first, err)
	}
	if processed, err := f.svc.ProcessDelegatedFirstTouchOnce(f.ctx); err != nil || !processed {
		t.Fatalf("first source run did not fill runway: processed=%v err=%v", processed, err)
	}

	account, err := f.repo.GetAccount(f.ctx, f.orgID, f.manifest.Entries[0].AccountID)
	if err != nil || account == nil {
		t.Fatalf("current account unavailable: account=%+v err=%v", account, err)
	}
	candidate, err := f.repo.GetCandidate(f.ctx, f.orgID, f.manifest.Entries[0].ContactCandidateID)
	if err != nil || candidate == nil {
		t.Fatalf("current candidate unavailable: candidate=%+v err=%v", candidate, err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	newRunID := "run-refresh-" + uuid.NewString()
	newSnapshot := strings.Repeat("d", 64)
	newImportID := uuid.New()
	if err := f.repo.CreateImportRun(f.ctx, &models.OutreachImportRun{
		ID: newImportID, OrganizationID: f.orgID, SourceSystem: "extra-cli", SourceRunID: newRunID,
		SchemaVersion: models.OutreachSchemaV1, SnapshotHash: newSnapshot, Status: models.OutreachImportCompleted,
		StartedAt: now, FinishedAt: &now, IdempotencyKey: "refresh-" + newImportID.String(), SourceGeneratedAt: &now,
	}); err != nil {
		t.Fatal(err)
	}
	account.SourceRunID = newRunID
	account.LastImportRunID = &newImportID
	account.ContractorRoleSourceRunID = newRunID
	account.ContractorRoleObservedAt = &now
	if _, err := f.repo.UpsertAccount(f.ctx, account); err != nil {
		t.Fatal(err)
	}
	candidate.LastImportRunID = &newImportID
	candidate.SourceDate = &now
	if _, err := f.repo.UpsertCandidate(f.ctx, candidate); err != nil {
		t.Fatal(err)
	}
	evidence, err := f.repo.ListEvidence(f.ctx, f.orgID, account.ID)
	if err != nil || len(evidence) == 0 {
		t.Fatalf("current evidence unavailable: evidence=%+v err=%v", evidence, err)
	}
	for index := range evidence {
		evidence[index].LastImportRunID = &newImportID
		evidence[index].ConsultedAt = &now
		if _, err := f.repo.UpsertEvidence(f.ctx, &evidence[index]); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.repo.UpsertFeedSyncState(f.ctx, &models.OutreachFeedSyncState{
		OrganizationID: f.orgID, LastSnapshotHash: newSnapshot, LastRunID: newRunID,
		LastManifestURI: "file:///refresh-test.json", LastSuccessAt: &now, LastAttemptAt: &now,
		LastStatus: "completed", SourceGeneratedAt: &now,
	}); err != nil {
		t.Fatal(err)
	}

	refresh, err := f.repo.MaterializeCurrentInitialBacklog(f.ctx, f.orgID, newRunID)
	if err != nil || refresh.StaleRetired != 1 || refresh.Imported != 1 || refresh.DelegatedEligible != 1 {
		t.Fatalf("refresh did not retire and replenish: counts=%+v err=%v", refresh, err)
	}
	for replay := 0; replay < 10; replay++ {
		counts, err := f.repo.MaterializeCurrentInitialBacklog(f.ctx, f.orgID, newRunID)
		if err != nil || counts.StaleRetired != 0 || counts.Imported != 1 || counts.DelegatedEligible != 1 {
			t.Fatalf("materialization replay %d drifted: counts=%+v err=%v", replay, counts, err)
		}
	}
	if processed, err := f.svc.ProcessDelegatedFirstTouchOnce(f.ctx); err != nil || !processed {
		t.Fatalf("refreshed source run did not refill runway: processed=%v err=%v", processed, err)
	}

	var oldCancelled, currentLive, duplicateRunInitial, duplicateMessageKeys, sent int
	var currentStates, decisionStates string
	if err := f.pool.QueryRow(f.ctx, `SELECT
		(SELECT count(*) FROM outreach_touchpoints WHERE organization_id=$1 AND source_run_id<>$2 AND state='CANCELLED' AND stop_reason='source_run_superseded'),
		(SELECT count(*) FROM outreach_touchpoints WHERE organization_id=$1 AND source_run_id=$2 AND state='QUEUED'),
		(SELECT count(*) FROM (SELECT account_id,source_run_id FROM outreach_touchpoints WHERE organization_id=$1 AND source_run_id<>'' AND ordinal=1 GROUP BY account_id,source_run_id HAVING count(*)>1) duplicate_initial),
		(SELECT count(*) FROM (SELECT message_key FROM confenge_dispatch_queue WHERE organization_id=$1 GROUP BY message_key HAVING count(*)>1) duplicate_keys),
		(SELECT count(*) FROM outreach_touchpoints WHERE organization_id=$1 AND state='SENT'),
		COALESCE((SELECT string_agg(state || ':' || COALESCE(stop_reason,''), ',') FROM outreach_touchpoints WHERE organization_id=$1 AND source_run_id=$2),''),
		COALESCE((SELECT string_agg(state || ':' || blocker_codes::text, ',') FROM confenge_delegated_first_touch_decisions WHERE organization_id=$1 AND evidence_source_run_id=$2),'')`, f.orgID, newRunID).
		Scan(&oldCancelled, &currentLive, &duplicateRunInitial, &duplicateMessageKeys, &sent, &currentStates, &decisionStates); err != nil {
		t.Fatal(err)
	}
	if oldCancelled != 1 || currentLive != 1 || duplicateRunInitial != 0 || duplicateMessageKeys != 0 || sent != 0 {
		t.Fatalf("refresh convergence drift: old_cancelled=%d current_live=%d duplicate_initial=%d duplicate_message_keys=%d sent=%d current_states=%s decisions=%s",
			oldCancelled, currentLive, duplicateRunInitial, duplicateMessageKeys, sent, currentStates, decisionStates)
	}
}
