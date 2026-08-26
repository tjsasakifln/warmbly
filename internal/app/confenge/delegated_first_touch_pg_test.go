package confenge

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	infrastructuredb "github.com/warmbly/warmbly/internal/infrastructure/db"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

type delegatedPGFixture struct {
	ctx        context.Context
	pool       *pgxpool.Pool
	repo       repository.OutreachRepository
	svc        *service
	orgID      uuid.UUID
	actorID    uuid.UUID
	workerID   uuid.UUID
	campaignID uuid.UUID
	manifest   DelegatedFirstTouchManifest
}

func newDelegatedPGFixture(t *testing.T) *delegatedPGFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, testPostgresDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, table := range []string{
		"confenge_delegated_first_touch_batches", "confenge_delegated_first_touch_decisions",
		"confenge_dispatch_queue", "outreach_accounts", "outreach_feed_sync_state",
	} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass('public.' || $1) IS NOT NULL`, table).Scan(&exists); err != nil || !exists {
			t.Skipf("full migrated test schema required (%s missing)", table)
		}
	}

	f := &delegatedPGFixture{
		ctx: ctx, pool: pool, orgID: uuid.New(), actorID: uuid.New(), workerID: uuid.New(),
	}
	sender := "delegated-" + f.orgID.String() + "@example.test"
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,first_name,last_name,email) VALUES($1,'Founder','Authority',$2)`, f.actorID, sender); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organizations(id,name,slug,owner_user_id) VALUES($1,'Delegated First Touch',$2,$3)`, f.orgID, "delegated-"+f.orgID.String(), f.actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workers(id,name,ip_addr,active,worker_type,free_tier) VALUES($1,'delegated-test','127.0.0.1',true,'shared',false)`, f.workerID); err != nil {
		t.Fatal(err)
	}
	mailboxID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO email_accounts(id,user_id,organization_id,worker_id,email,name,signature_plain,signature_html,provider,status,warmup_tag,campaign_limit,min_wait_time,risk_band)
		VALUES($1,$2,$3,$4,$5,'CONFENGE','','','smtp_imap','active','delegated-test',50,600,'clean')`,
		mailboxID, f.actorID, f.orgID, f.workerID, sender); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO email_accounts_smtp_imap(email_account_id,smtp_host,smtp_port,smtp_user,smtp_password,imap_host,imap_port,imap_user,imap_password)
		VALUES($1,'smtp.invalid',587,$2,'encrypted-test','imap.invalid',993,$2,'encrypted-test')`, mailboxID, sender); err != nil {
		t.Fatal(err)
	}
	campaignID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO campaigns(id,user_id,organization_id,name,description,status,days,updated_at,created_at)
		VALUES($1,$2,$3,'CONFENGE delegated test','policy canary','paused',62,now(),now())`, campaignID, f.actorID, f.orgID); err != nil {
		t.Fatal(err)
	}
	f.campaignID = campaignID

	f.repo = repository.NewOutreachRepository(pool)
	if err := f.repo.UpsertOrgSettings(ctx, &models.OutreachOrgSettings{
		OrganizationID: f.orgID, CampaignID: &campaignID, CampaignName: "CONFENGE delegated test",
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	runID, snapshot := "run-delegated-"+uuid.NewString(), strings.Repeat("b", 64)
	importID := uuid.New()
	run := &models.OutreachImportRun{
		ID: importID, OrganizationID: f.orgID, SourceSystem: "extra-cli", SourceRunID: runID,
		SchemaVersion: models.OutreachSchemaV1, SnapshotHash: snapshot, Status: models.OutreachImportCompleted,
		StartedAt: now, FinishedAt: &now, IdempotencyKey: "import-" + importID.String(), SourceGeneratedAt: &now,
	}
	if err := f.repo.CreateImportRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.UpsertFeedSyncState(ctx, &models.OutreachFeedSyncState{
		OrganizationID: f.orgID, LastSnapshotHash: snapshot, LastRunID: runID,
		LastManifestURI: "file:///delegated-test.json", LastSuccessAt: &now, LastAttemptAt: &now,
		LastStatus: "completed", SourceGeneratedAt: &now,
	}); err != nil {
		t.Fatal(err)
	}
	evidenceHash := strings.Repeat("a", 64)
	account := &models.OutreachAccount{
		ID: uuid.New(), OrganizationID: f.orgID, SourceLeadID: "lead-delegated", CNPJ14: "11222333000144", CNPJRoot: "11222333",
		RazaoSocial: "Empresa Alfa Ltda", NomeFantasia: "Empresa Alfa", ServiceCode: "CONTRACT_MANAGEMENT",
		FactToMention: "Atuação como contratada em contrato público confirmada.", QueueState: models.OutreachQueueNeedsReview,
		SourceSystem: "extra-cli", SourceRunID: runID, LastImportRunID: &importID,
		MessageContextHash: strings.Repeat("c", 64), EmailSendReady: true,
		TargetFitClass: TargetFitConfirmed, TargetFitVersion: "target-fit.v1", TargetFitSourceWatermark: now.Format(time.RFC3339),
		TargetFitObservedAt: &now, TargetFitFresh: true, TargetFitSendTier: "A_AUTOMATIC", TargetFitEligible: true,
		ContractorRoleStatus: ContractorRoleConfirmed, TargetPartyRole: "SUPPLIER",
		ContractorRolePolicyVersion: DelegatedFirstTouchEvidenceV1, ContractorRoleSource: "extra-cli:v_contracts_canonical_v2",
		ContractorRoleSourceRunID: runID, ContractorRoleObservedAt: &now, ContractorRoleEvidenceHash: evidenceHash,
		ContractorRoleEvidenceReference: "extra-cli:v_contracts_canonical_v2:sha256:" + evidenceHash,
		ContractorRoleEvidenceIDs:       []string{"contract-1"}, SupplierCNPJ14: "11222333000144",
		SupplierIdentityRef: "cnpj:11222333000144", BuyerCNPJ14: "99888777000166",
		BuyerIdentityRef: "cnpj:99888777000166", ContractorRoleMatchMethod: "SUPPLIER_EXACT_CNPJ14",
		ContractorRoleConfidence: "HIGH", ContractorRoleReasonCodes: []string{"lead_matches_supplier", "lead_differs_from_buyer"},
	}
	if _, err := f.repo.UpsertAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	candidate := &models.OutreachContactCandidate{
		ID: uuid.New(), OrganizationID: f.orgID, AccountID: account.ID, SourceContactID: "route-delegated",
		Name: "Equipe", Role: "Atendimento", Email: "contato@empresa.example", SourceURL: "https://empresa.example/contato",
		VerificationStatus: models.OutreachVerifyOfficialSource, Confidence: "HIGH", Recommended: true,
		EmailSendReady: true, OwnershipStatus: "COMPANY_OWNED", RecipientCommercialSuitability: "SUITABLE",
		LastImportRunID: &importID, ChannelEpistemic: "OBSERVED", RouteFreshness: "FRESH",
		RouteSuppression: "NONE", EmailDerivation: "OBSERVED",
		DiscoveryJSON: []byte(`{"route_class":"GENERIC_COMPANY","controlled_email_eligible":true,"preferred_initial":true,"mailbox_company_evidence":"OBSERVED","person_unknown":true,"risk_class":"ALLOWED"}`),
	}
	if _, err := f.repo.UpsertCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.UpsertEvidence(ctx, &models.OutreachEvidence{
		ID: uuid.New(), OrganizationID: f.orgID, AccountID: account.ID, SourceEvidenceID: "contract-1",
		EvidenceType: "CONTRACT", URL: "https://pncp.gov.br/contratos/test", Synthesis: "Empresa Alfa figura como contratada.",
		EpistemicClass: models.OutreachEpistemicConfirmedFact, Reliability: "HIGH", ConsultedAt: &now, LastImportRunID: &importID,
	}); err != nil {
		t.Fatal(err)
	}

	policyStore := repository.NewConfengePolicyRepository(pool)
	auth := &models.CampaignPolicyAuthorization{
		ID: uuid.New(), CampaignID: campaignID, PromptPolicyVersion: DelegatedFirstTouchPolicyV1,
		ValidatorVersion: DelegatedFirstTouchValidatorV1, ContactPolicyVersion: DelegatedFirstTouchContactPolicyV1,
		TemplatePolicyVersion: DelegatedFirstTouchTemplateV1, SenderMailbox: sender, Channel: models.OutreachChannelEmail,
		AllowedRiskClass: "GREEN", MaxRatePerHour: 10, AllowPolicyTemplateGREEN: true,
		EffectiveAt: now.Add(-time.Minute), AuthorizedBy: f.actorID, AuthorizedByLabel: DelegatedFirstTouchAuthority,
	}
	if _, err := policyStore.InsertCampaignPolicy(ctx, f.orgID, auth); err != nil {
		t.Fatal(err)
	}

	f.svc = NewService(Config{
		Enabled: true, DelegatedFirstTouchEnabled: true, RepositorySHA: "sha-delegated-pg",
		FeedMaxAge: 24 * time.Hour, MaxInitialEmailWords: 120, OperatorOrgID: f.orgID, OperatorUserID: f.actorID,
	}, f.repo, nil).(*service)
	f.svc.WirePolicyAuth(policyStore)
	f.svc.WireDelegatedFirstTouch(pool)
	f.svc.WireOrgRisk(delegatedTestOrgRisk{})
	f.svc.WireDispatchGovernor(dispatch.NewGovernor(dispatch.Config{
		SendsPerHour: 10, MinGap: 10 * time.Minute, Timezone: "America/Sao_Paulo",
		WindowStart: "09:00", WindowEnd: "18:00", BusinessDaysOnly: true, EnvPaused: true,
		EnvPauseReason: "delegated zero-smtp canary",
	}, dispatch.NewPGStore(pool), nil))

	body := "Olá, equipe da Empresa Alfa. Meu nome é Tiago Sasaki, da CONFENGE. A Empresa Alfa aparece como contratada em contratos públicos confirmados em fonte pública. Trabalhamos com organização técnica de rotinas ligadas à administração pública. Quem é a pessoa responsável por esse tema na empresa? Você poderia indicar o contato correto ou encaminhar esta mensagem, por favor? Obrigado, Tiago Sasaki, confenge.com.br."
	entry := DelegatedFirstTouchEntry{
		IdempotencyKey: "first-touch:" + account.ID.String(), CorrelationID: "corr-delegated", AccountID: account.ID,
		ContactCandidateID: candidate.ID, CNPJ14: account.CNPJ14, SupplierCNPJ14: account.SupplierCNPJ14, BuyerCNPJ14: account.BuyerCNPJ14,
		ContractorRoleStatus: ContractorRoleConfirmed, TargetPartyRole: "SUPPLIER", ContractRoleSource: account.ContractorRoleSource,
		ContractEvidenceIDs: account.ContractorRoleEvidenceIDs, ContractEvidenceHash: evidenceHash,
		ContractEvidenceReference: account.ContractorRoleEvidenceReference, SupplierIdentityRef: account.SupplierIdentityRef,
		BuyerIdentityRef: account.BuyerIdentityRef, RoleMatchMethod: account.ContractorRoleMatchMethod,
		RoleConfidence: account.ContractorRoleConfidence, ContractRoleReasonCodes: account.ContractorRoleReasonCodes,
		EvidenceObservedAt: now, ReconciliationStatus: ReconciliationCorroborated,
		WebSources: []DelegatedWebSource{{URL: candidate.SourceURL, Kind: "OFFICIAL_COMPANY_PAGE", Supports: "COMPANY_IDENTITY_AND_MAILBOX", ObservedAt: now}},
		RouteClass: RouteClassGenericCompany, Recipient: candidate.Email, Subject: "Responsável por contratos públicos na Empresa Alfa", BodyText: body,
		EvidenceIDs: []string{"contract-1"}, QA: DelegatedFirstTouchQA{Result: "PASS", Attempts: 1, IdentityPassed: true,
			FactualPassed: true, CopyPassed: true, OperationalPassed: true, Reviewer: "agent:test"},
	}
	entry.SubjectHash, entry.BodyHash = hashText(entry.Subject), hashText(entry.BodyText)
	f.manifest = DelegatedFirstTouchManifest{
		SchemaVersion: DelegatedFirstTouchManifestV1, BatchID: "batch-" + uuid.NewString(), AgentID: "agent:test",
		PolicyVersion: DelegatedFirstTouchPolicyV1, PolicyHash: DelegatedFirstTouchPolicyHashV1,
		AuthorityReference: DelegatedFirstTouchAuthorityRef, PolicyAuthorizationID: auth.ID,
		SourceRunID: runID, SourceSnapshotHash: snapshot, EvidenceVersion: DelegatedFirstTouchEvidenceV1,
		ComposerVersion: ComposerVersion, TemplateVersion: DelegatedFirstTouchTemplateV1, PromptVersion: PromptVersion,
		GeneratedAt: now, Entries: []DelegatedFirstTouchEntry{entry},
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_, _ = pool.Exec(cleanup, `DELETE FROM organizations WHERE id=$1`, f.orgID)
		_, _ = pool.Exec(cleanup, `DELETE FROM users WHERE id=$1`, f.actorID)
		_, _ = pool.Exec(cleanup, `DELETE FROM workers WHERE id=$1`, f.workerID)
	})
	return f
}

func TestDelegatedFirstTouchApprovesQueuesAuditsAndReplaysOncePostgres(t *testing.T) {
	f := newDelegatedPGFixture(t)
	first, xerr := f.svc.ApplyDelegatedFirstTouchManifest(f.ctx, f.orgID, f.manifest, false)
	if xerr != nil || first.Queued != 1 || first.DelegatedApproved != 1 || len(first.Items) != 1 || first.Items[0].State != "QUEUED" {
		t.Fatalf("first apply did not reach QUEUED: report=%+v err=%v", first, xerr)
	}
	if processed, err := f.svc.ProcessDispatchQueueOnce(f.ctx); err != nil || processed {
		t.Fatalf("paused dispatch mutated transport: processed=%v err=%v", processed, err)
	}
	restarted := NewService(f.svc.cfg, f.repo, nil).(*service)
	restarted.WirePolicyAuth(f.svc.policyStore)
	restarted.WireDelegatedFirstTouch(f.pool)
	restarted.WireOrgRisk(delegatedTestOrgRisk{})
	restarted.WireDispatchGovernor(f.svc.governor)
	replay, xerr := restarted.ApplyDelegatedFirstTouchManifest(f.ctx, f.orgID, f.manifest, false)
	if xerr != nil || replay.Queued != 1 || !replay.Items[0].Idempotent {
		t.Fatalf("restart replay was not idempotent: report=%+v err=%v", replay, xerr)
	}
	var decisions, touchpoints, drafts, queued, sent int
	if err := f.pool.QueryRow(f.ctx, `SELECT
		(SELECT count(*) FROM confenge_delegated_first_touch_decisions WHERE organization_id=$1),
		(SELECT count(*) FROM outreach_touchpoints WHERE organization_id=$1),
		(SELECT count(*) FROM outreach_drafts WHERE organization_id=$1),
		(SELECT count(*) FROM confenge_dispatch_queue WHERE organization_id=$1 AND status='queued'),
		(SELECT count(*) FROM outreach_touchpoints WHERE organization_id=$1 AND state='SENT')`, f.orgID).
		Scan(&decisions, &touchpoints, &drafts, &queued, &sent); err != nil {
		t.Fatal(err)
	}
	if decisions != 1 || touchpoints != 1 || drafts != 1 || queued != 1 || sent != 0 {
		t.Fatalf("durable replay counts drifted: decisions=%d touchpoints=%d drafts=%d queued=%d sent=%d", decisions, touchpoints, drafts, queued, sent)
	}
	var decision, actorType, authority, state, contentHash string
	var approvedBy *uuid.UUID
	var readbackAt *time.Time
	if err := f.pool.QueryRow(f.ctx, `
		SELECT d.decision,d.approved_by_type,d.authority,d.state,d.content_hash,d.readback_at,t.approved_by
		FROM confenge_delegated_first_touch_decisions d
		JOIN outreach_touchpoints t ON t.id=d.touchpoint_id AND t.organization_id=d.organization_id
		WHERE d.organization_id=$1`, f.orgID).
		Scan(&decision, &actorType, &authority, &state, &contentHash, &readbackAt, &approvedBy); err != nil {
		t.Fatal(err)
	}
	if decision != DelegatedFirstTouchApprovalDecision || actorType != "delegated_agent" || authority != DelegatedFirstTouchAuthority ||
		state != "QUEUED" || contentHash == "" || readbackAt == nil || approvedBy != nil {
		t.Fatalf("delegated audit incomplete or forged human actor: decision=%s actor=%s authority=%s state=%s hash=%q readback=%v approved_by=%v",
			decision, actorType, authority, state, contentHash, readbackAt, approvedBy)
	}
	status, statusErr := restarted.DelegatedFirstTouchStatus(f.ctx, f.orgID, f.manifest.BatchID)
	if statusErr != nil || status == nil || !status.PolicyActive || status.QueuedReadback != 1 ||
		status.DuplicateLiveAccount != 0 || status.DuplicateLiveRoot != 0 || len(status.Items) != 1 ||
		status.Items[0].ApprovalSource != DelegatedFirstTouchApprovalDecision {
		t.Fatalf("control-center readback is incomplete: status=%+v err=%v", status, statusErr)
	}
}

func TestCampaignReadyAcceptsReadBackDelegatedQueueWithoutContact(t *testing.T) {
	f := newDelegatedPGFixture(t)
	if _, err := f.pool.Exec(f.ctx, `
		INSERT INTO sequences (id,campaign_id,organization_id,name,subject,body_plain,body_html,wait_after,position,kind)
		VALUES($1,$2,$3,'Step 1','Subject','Body','<p>Body</p>',0,0,'email')`,
		uuid.New(), f.campaignID, f.orgID); err != nil {
		t.Fatal(err)
	}
	campaignRepo := repository.NewCampaignRepostory(&infrastructuredb.DB{Pool: f.pool})
	if err := campaignRepo.ValidateCampaignReady(f.ctx, f.campaignID); err == nil || !strings.Contains(err.Error(), "at least one contact") {
		t.Fatalf("ordinary empty campaign unexpectedly ready: %v", err)
	}
	report, xerr := f.svc.ApplyDelegatedFirstTouchManifest(f.ctx, f.orgID, f.manifest, false)
	if xerr != nil || report == nil || report.Queued != 1 || len(report.Items) != 1 || report.Items[0].State != "QUEUED" {
		t.Fatalf("delegated queue unavailable: report=%+v err=%v", report, xerr)
	}
	var contacts int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM campaign_leads WHERE campaign_id=$1`, f.campaignID).Scan(&contacts); err != nil {
		t.Fatal(err)
	}
	if contacts != 0 {
		t.Fatalf("fixture already enrolled contacts: %d", contacts)
	}
	if err := campaignRepo.ValidateCampaignReady(f.ctx, f.campaignID); err != nil {
		t.Fatalf("read-back delegated rolling queue did not make campaign startable: %v", err)
	}
}

func TestDelegatedFirstTouchWorkerSkipsMismatchedDecisionBoundByCurrentIdempotencyKey(t *testing.T) {
	f := newDelegatedPGFixture(t)
	entry := f.manifest.Entries[0]
	firstAccount, err := f.repo.GetAccount(f.ctx, f.orgID, entry.AccountID)
	if err != nil || firstAccount == nil {
		t.Fatalf("first account unavailable: account=%+v err=%v", firstAccount, err)
	}
	firstCandidate, err := f.repo.GetCandidate(f.ctx, f.orgID, entry.ContactCandidateID)
	if err != nil || firstCandidate == nil {
		t.Fatalf("first candidate unavailable: candidate=%+v err=%v", firstCandidate, err)
	}
	firstTouchpoint, _, err := f.svc.prepareDelegatedTouchpoint(f.ctx, f.orgID, firstAccount, firstCandidate, f.manifest, entry)
	if err != nil {
		t.Fatal(err)
	}
	firstTouchpoint.DueAt = time.Now().UTC().Add(-2 * time.Hour)
	if err := f.repo.UpdateTouchpoint(f.ctx, firstTouchpoint); err != nil {
		t.Fatal(err)
	}

	// Reproduce the production drift: the durable key names the current run,
	// while a historical bad row carries an older evidence_source_run_id.
	staleManifest := f.manifest
	staleManifest.BatchID = "stale-key-" + uuid.NewString()
	staleManifest.SourceRunID = "run-stale-evidence"
	staleManifest.SourceSnapshotHash = strings.Repeat("e", 64)
	staleEntry := entry
	staleEntry.IdempotencyKey = delegatedFirstTouchIdempotencyPrefix + f.manifest.SourceRunID + ":" + firstAccount.ID.String()
	if err := f.svc.reserveDelegatedBatch(f.ctx, f.orgID, staleManifest, manifestHash(staleManifest)); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.persistDelegatedHold(f.ctx, f.orgID, staleManifest, staleEntry, []string{"historical_source_binding_drift"}); err != nil {
		t.Fatal(err)
	}

	secondAccount := *firstAccount
	secondAccount.ID = uuid.New()
	secondAccount.SourceLeadID = "lead-delegated-next"
	secondAccount.CNPJ14 = "22333444000155"
	secondAccount.CNPJRoot = "22333444"
	secondAccount.SupplierCNPJ14 = secondAccount.CNPJ14
	secondAccount.SupplierIdentityRef = "cnpj:" + secondAccount.CNPJ14
	if _, err := f.repo.UpsertAccount(f.ctx, &secondAccount); err != nil {
		t.Fatal(err)
	}
	secondCandidate := *firstCandidate
	secondCandidate.ID = uuid.New()
	secondCandidate.AccountID = secondAccount.ID
	secondCandidate.SourceContactID = "route-delegated-next"
	secondCandidate.Email = "next@empresa.example"
	if _, err := f.repo.UpsertCandidate(f.ctx, &secondCandidate); err != nil {
		t.Fatal(err)
	}
	secondEntry := entry
	secondEntry.IdempotencyKey = delegatedFirstTouchIdempotencyPrefix + f.manifest.SourceRunID + ":" + secondAccount.ID.String()
	secondEntry.AccountID = secondAccount.ID
	secondEntry.ContactCandidateID = secondCandidate.ID
	secondEntry.CNPJ14 = secondAccount.CNPJ14
	secondEntry.SupplierCNPJ14 = secondAccount.SupplierCNPJ14
	secondEntry.SupplierIdentityRef = secondAccount.SupplierIdentityRef
	secondEntry.Recipient = secondCandidate.Email
	secondTouchpoint, _, err := f.svc.prepareDelegatedTouchpoint(f.ctx, f.orgID, &secondAccount, &secondCandidate, f.manifest, secondEntry)
	if err != nil {
		t.Fatal(err)
	}
	secondTouchpoint.DueAt = time.Now().UTC().Add(-time.Hour)
	if err := f.repo.UpdateTouchpoint(f.ctx, secondTouchpoint); err != nil {
		t.Fatal(err)
	}

	touchpointID, accountID, candidateID, err := f.svc.nextDelegatedFirstTouchCandidate(f.ctx, f.orgID)
	if err != nil {
		t.Fatal(err)
	}
	if touchpointID != secondTouchpoint.ID || accountID != secondAccount.ID || candidateID != secondCandidate.ID {
		t.Fatalf("historical key mismatch blocked rolling selection: touchpoint=%s account=%s candidate=%s", touchpointID, accountID, candidateID)
	}
}

func TestDelegatedFirstTouchPartialBatchFailureDoesNotBlockEligibleItemPostgres(t *testing.T) {
	f := newDelegatedPGFixture(t)
	invalid := f.manifest.Entries[0]
	invalid.IdempotencyKey = "first-touch:missing-account:" + uuid.NewString()
	invalid.CorrelationID = "corr-missing-account"
	invalid.AccountID = uuid.Nil
	invalid.ContactCandidateID = uuid.Nil
	invalid.CNPJ14 = "88777666000155"
	invalid.SupplierCNPJ14 = invalid.CNPJ14
	invalid.SupplierIdentityRef = "cnpj:" + invalid.CNPJ14
	f.manifest.Entries = append(f.manifest.Entries, invalid)

	report, xerr := f.svc.ApplyDelegatedFirstTouchManifest(f.ctx, f.orgID, f.manifest, false)
	if xerr != nil || report == nil || report.Queued != 1 || report.Held != 1 || len(report.Items) != 2 {
		t.Fatalf("partial batch did not isolate item failure: report=%+v err=%v", report, xerr)
	}
	var queued, held int
	if err := f.pool.QueryRow(f.ctx, `
		SELECT count(*) FILTER (WHERE state='QUEUED'),count(*) FILTER (WHERE state='HOLD')
		FROM confenge_delegated_first_touch_decisions WHERE organization_id=$1`, f.orgID).Scan(&queued, &held); err != nil {
		t.Fatal(err)
	}
	if queued != 1 || held != 1 {
		t.Fatalf("partial batch audit mismatch: queued=%d held=%d", queued, held)
	}
	status, statusErr := f.svc.DelegatedFirstTouchStatus(f.ctx, f.orgID, f.manifest.BatchID)
	if statusErr != nil || status == nil || len(status.Items) != 2 {
		t.Fatalf("partial batch status unavailable: status=%+v err=%v", status, statusErr)
	}
	for _, item := range status.Items {
		if item.Decision == "HOLD" && item.ApprovalSource != "POLICY_EVALUATION_HOLD" {
			t.Fatalf("HOLD was presented as an approval: %+v", item)
		}
	}
}

func TestDelegatedFirstTouchQueueAuditGapCancelsBeforeTransportPostgres(t *testing.T) {
	f := newDelegatedPGFixture(t)
	report, xerr := f.svc.ApplyDelegatedFirstTouchManifest(f.ctx, f.orgID, f.manifest, false)
	if xerr != nil || report == nil || report.Queued != 1 {
		t.Fatalf("fixture did not queue: report=%+v err=%v", report, xerr)
	}
	tpID := report.Items[0].TouchpointID
	if _, err := f.pool.Exec(f.ctx, `
		UPDATE confenge_delegated_first_touch_decisions
		SET state='APPROVED',readback_at=NULL
		WHERE organization_id=$1 AND touchpoint_id=$2`, f.orgID, tpID); err != nil {
		t.Fatal(err)
	}
	tp, err := f.repo.GetTouchpoint(f.ctx, f.orgID, tpID)
	if err != nil || tp == nil || tp.State != models.TouchpointQueued {
		t.Fatalf("queued touchpoint unavailable: tp=%+v err=%v", tp, err)
	}
	if err := f.svc.AssertTransportable(f.ctx, f.orgID, tp); err == nil || !strings.Contains(err.Error(), "queue_state_drift") {
		t.Fatalf("queue/readback divergence must fail closed: %v", err)
	}
	var decisionState, touchState, queueState string
	if err := f.pool.QueryRow(f.ctx, `
		SELECT d.state,t.state,q.status
		FROM confenge_delegated_first_touch_decisions d
		JOIN outreach_touchpoints t ON t.organization_id=d.organization_id AND t.id=d.touchpoint_id
		JOIN confenge_dispatch_queue q ON q.organization_id=d.organization_id AND q.draft_id=d.draft_id
		WHERE d.organization_id=$1 AND d.touchpoint_id=$2`, f.orgID, tpID).
		Scan(&decisionState, &touchState, &queueState); err != nil {
		t.Fatal(err)
	}
	if decisionState != "CANCELLED" || touchState != models.TouchpointNeedsReview || queueState != "cancelled" {
		t.Fatalf("divergent queue was not cancelled: decision=%s touch=%s queue=%s", decisionState, touchState, queueState)
	}
}

func TestDelegatedFirstTouchMaterialDriftInvalidatesApprovalPostgres(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *delegatedPGFixture, *models.OutreachTouchpoint)
	}{
		{
			name: "recipient",
			mutate: func(t *testing.T, f *delegatedPGFixture, tp *models.OutreachTouchpoint) {
				tp.Recipient = "outro@empresa.example"
				RecomputeContentHash(tp)
				tp.ApprovedContentHash = tp.ContentHash
				if err := f.repo.UpdateTouchpoint(f.ctx, tp); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "content",
			mutate: func(t *testing.T, f *delegatedPGFixture, tp *models.OutreachTouchpoint) {
				tp.BodyText += " Texto inserido depois da aprovação."
				RecomputeContentHash(tp)
				tp.ApprovedContentHash = tp.ContentHash
				if err := f.repo.UpdateTouchpoint(f.ctx, tp); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "policy_version",
			mutate: func(t *testing.T, f *delegatedPGFixture, tp *models.OutreachTouchpoint) {
				if _, err := f.pool.Exec(f.ctx, `
					UPDATE confenge_delegated_first_touch_decisions
					SET policy_hash=$3
					WHERE organization_id=$1 AND touchpoint_id=$2`, f.orgID, tp.ID, strings.Repeat("f", 64)); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newDelegatedPGFixture(t)
			report, xerr := f.svc.ApplyDelegatedFirstTouchManifest(f.ctx, f.orgID, f.manifest, false)
			if xerr != nil || report == nil || report.Queued != 1 {
				t.Fatalf("fixture did not queue: report=%+v err=%v", report, xerr)
			}
			tp, err := f.repo.GetTouchpoint(f.ctx, f.orgID, report.Items[0].TouchpointID)
			if err != nil || tp == nil || tp.State != models.TouchpointQueued {
				t.Fatalf("queued touchpoint unavailable: tp=%+v err=%v", tp, err)
			}
			tc.mutate(t, f, tp)
			if err := f.svc.AssertTransportable(f.ctx, f.orgID, tp); err == nil {
				t.Fatal("material drift remained transportable")
			}
			var decisionState, touchState, queueState string
			if err := f.pool.QueryRow(f.ctx, `
				SELECT d.state,t.state,q.status
				FROM confenge_delegated_first_touch_decisions d
				JOIN outreach_touchpoints t ON t.organization_id=d.organization_id AND t.id=d.touchpoint_id
				JOIN confenge_dispatch_queue q ON q.organization_id=d.organization_id AND q.draft_id=d.draft_id
				WHERE d.organization_id=$1 AND d.touchpoint_id=$2`, f.orgID, tp.ID).
				Scan(&decisionState, &touchState, &queueState); err != nil {
				t.Fatal(err)
			}
			if decisionState != "CANCELLED" || touchState != models.TouchpointNeedsReview || queueState != "cancelled" {
				t.Fatalf("drift did not revoke delegated approval: decision=%s touch=%s queue=%s", decisionState, touchState, queueState)
			}
		})
	}
}
