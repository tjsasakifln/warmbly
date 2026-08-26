package confenge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

const (
	manifestLoadAccounts  = 10_000
	manifestLoadChunkSize = 500
)

func TestPostgresSyncFeedManifestMaterializesTenThousandAndConverges(t *testing.T) {
	if os.Getenv("WARMBLY_RUN_LOAD_TESTS") != "1" {
		t.Skip("WARMBLY_RUN_LOAD_TESTS=1 is required")
	}
	dsn := os.Getenv("WARMBLY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("WARMBLY_TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	userID, orgID := uuid.New(), uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO users (id,first_name,last_name,email) VALUES ($1,'Manifest','Load',$2)`,
		userID, "manifest-load-"+userID.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO organizations (id,name,slug,owner_user_id) VALUES ($1,'Manifest Load',$2,$3)`,
		orgID, "manifest-load-"+orgID.String(), userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cleanupCancel()
		if _, cleanupErr := pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, orgID); cleanupErr != nil {
			t.Errorf("clean load organization: %v", cleanupErr)
		}
		if _, cleanupErr := pool.Exec(cleanupCtx, `DELETE FROM users WHERE id=$1`, userID); cleanupErr != nil {
			t.Errorf("clean load user: %v", cleanupErr)
		}
	})

	repo := repository.NewOutreachRepository(pool)
	svc := NewService(Config{
		Enabled: true, AppEnv: "test", RequireHumanApproval: true,
		FeedMaxAge: 24 * time.Hour, MaxFeedPayloadBytes: 16 << 20,
	}, repo, nil).(*service)
	svc.WireHumanGate(pool)

	generatedOne := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
	manifestOne, chunksOne := writeSyntheticLoadManifest(t, t.TempDir(), "load-run-1", "load-snapshot-1", generatedOne)

	// Leave chunk 3 half-written under an abandoned durable RUNNING lease.
	staleRun := &models.OutreachImportRun{
		ID: uuid.New(), OrganizationID: orgID, SourceSystem: "extra-cli",
		SourceRunID: "load-run-1", SchemaVersion: models.OutreachSchemaV1,
		SnapshotHash: "load-snapshot-1", PayloadHash: CanonicalPayloadHash(chunksOne[3]),
		ProfileID: "load", ProfileVersion: "1", Status: models.OutreachImportRunning,
		IdempotencyKey: fmt.Sprintf("sync:%s:load-snapshot-1:3", orgID),
		StartedAt:      time.Now().UTC().Add(-10 * time.Minute), UpdatedAt: time.Now().UTC().Add(-10 * time.Minute),
	}
	if err = repo.CreateImportRun(ctx, staleRun); err != nil {
		t.Fatal(err)
	}
	var abandoned Feed
	if err = json.Unmarshal(chunksOne[3], &abandoned); err != nil {
		t.Fatal(err)
	}
	abandoned.Leads = abandoned.Leads[:manifestLoadChunkSize/2]
	partialCounts, partialErrors, _ := svc.applyFeed(ctx, orgID, staleRun, &abandoned, false, false)
	if len(partialErrors) != 0 || partialCounts.LeadsProcessed != manifestLoadChunkSize/2 {
		t.Fatalf("abandoned chunk setup failed: counts=%+v errors=%+v", partialCounts, partialErrors)
	}
	if _, err = pool.Exec(ctx, `UPDATE outreach_import_runs SET updated_at=now()-interval '10 minutes' WHERE id=$1`, staleRun.ID); err != nil {
		t.Fatal(err)
	}
	restarted := NewService(svc.cfg, repo, nil).(*service)
	restarted.WireHumanGate(pool)

	started := time.Now()
	first, xerr := restarted.SyncFeedManifest(ctx, orgID, &userID, "file://"+manifestOne)
	if xerr != nil {
		t.Fatalf("first 10k sync: result=%+v error=%v", first, xerr)
	}
	assertLoadFunnel(t, first)
	assertCurrentLoadBacklog(t, ctx, pool, orgID, "load-run-1", "load-snapshot-1", 0)
	t.Logf("10k first sync completed in %s with counts=%v", time.Since(started).Round(time.Millisecond), first.Counts)

	recovered, err := repo.GetImportRun(ctx, orgID, staleRun.ID)
	if err != nil || recovered == nil || recovered.Status != models.OutreachImportCompleted ||
		!containsString(recovered.Warnings, "resumed_stale_running_import") {
		t.Fatalf("abandoned chunk did not converge on its durable run: run=%+v error=%v", recovered, err)
	}

	replay, xerr := restarted.SyncFeedManifest(ctx, orgID, &userID, "file://"+manifestOne)
	if xerr != nil || replay.Status != "noop" || !replay.SkippedSame {
		t.Fatalf("same snapshot replay was not idempotent: result=%+v error=%v", replay, xerr)
	}
	assertCurrentLoadBacklog(t, ctx, pool, orgID, "load-run-1", "load-snapshot-1", 0)

	generatedTwo := generatedOne.Add(time.Minute)
	manifestTwo, _ := writeSyntheticLoadManifest(t, t.TempDir(), "load-run-2", "load-snapshot-2", generatedTwo)
	refreshStarted := time.Now()
	second, xerr := restarted.SyncFeedManifest(ctx, orgID, &userID, "file://"+manifestTwo)
	if xerr != nil {
		t.Fatalf("refresh 10k sync: result=%+v error=%v", second, xerr)
	}
	assertLoadFunnel(t, second)
	assertCurrentLoadBacklog(t, ctx, pool, orgID, "load-run-2", "load-snapshot-2", 9_000)
	if second.Counts["stale_initial_retired"] != 9_000 {
		t.Fatalf("stale retirement=%d want=9000", second.Counts["stale_initial_retired"])
	}
	t.Logf("10k refresh completed in %s with counts=%v", time.Since(refreshStarted).Round(time.Millisecond), second.Counts)

	replay, xerr = restarted.SyncFeedManifest(ctx, orgID, &userID, "file://"+manifestTwo)
	if xerr != nil || replay.Status != "noop" || !replay.SkippedSame {
		t.Fatalf("refreshed snapshot replay was not idempotent: result=%+v error=%v", replay, xerr)
	}
	assertCurrentLoadBacklog(t, ctx, pool, orgID, "load-run-2", "load-snapshot-2", 9_000)
}

func assertLoadFunnel(t *testing.T, result *FeedSyncResult) {
	t.Helper()
	if result == nil || result.Status != "completed" || result.ChunksImported != 20 {
		t.Fatalf("incomplete manifest result: %+v", result)
	}
	want := map[string]int{
		"imported":                    10_000,
		"supplier_confirmed":          9_000,
		"candidate_attributed":        9_000,
		"initial_touchpoint_prepared": 9_000,
		"delegated_eligible":          8_000,
		"held_exception":              2_000,
	}
	for stage, count := range want {
		if result.Counts[stage] != count {
			t.Fatalf("stage %s=%d want=%d; all=%v", stage, result.Counts[stage], count, result.Counts)
		}
	}
}

func assertCurrentLoadBacklog(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, runID, snapshot string, stale int) {
	t.Helper()
	var accounts, currentInitial, duplicateInitial, reviewInitial, staleInitial, drafts, dispatch, commercialActions int
	var chunkRuns, chunkLeads, invalidChunkRuns int
	var stateRun, stateStatus string
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outreach_accounts WHERE organization_id=$1 AND source_run_id=$2`, orgID, runID).Scan(&accounts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outreach_touchpoints t
		JOIN outreach_accounts a ON a.organization_id=t.organization_id AND a.id=t.account_id
		JOIN outreach_contact_candidates c ON c.organization_id=t.organization_id AND c.id=t.contact_candidate_id
		WHERE t.organization_id=$1 AND t.source_run_id=$2 AND t.ordinal=1 AND t.purpose='INITIAL'
		  AND t.channel='EMAIL' AND t.state='DUE' AND a.source_run_id=$2
		  AND c.last_import_run_id=a.last_import_run_id
		  AND c.discovery_json->>'preferred_initial'='true'`, orgID, runID).Scan(&currentInitial); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT account_id,source_run_id FROM outreach_touchpoints
			WHERE organization_id=$1 AND ordinal=1 AND purpose='INITIAL' AND channel='EMAIL' AND source_run_id<>''
			GROUP BY account_id,source_run_id HAVING count(*)<>1
		) duplicates`, orgID).Scan(&duplicateInitial); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outreach_touchpoints WHERE organization_id=$1 AND ordinal=1 AND purpose='INITIAL' AND state='NEEDS_REVIEW'`, orgID).Scan(&reviewInitial); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outreach_touchpoints WHERE organization_id=$1 AND source_run_id<>$2 AND ordinal=1 AND purpose='INITIAL' AND channel='EMAIL' AND state='CANCELLED' AND stop_reason='source_run_superseded'`, orgID, runID).Scan(&staleInitial); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outreach_drafts WHERE organization_id=$1`, orgID).Scan(&drafts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM confenge_dispatch_queue WHERE organization_id=$1`, orgID).Scan(&dispatch); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outreach_commercial_actions WHERE organization_id=$1`, orgID).Scan(&commercialActions); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*),COALESCE(sum((counts->>'leads_processed')::int),0),
			count(*) FILTER (WHERE status<>'completed' OR snapshot_hash<>$3 OR length(payload_hash)<>64)
		FROM outreach_import_runs WHERE organization_id=$1 AND source_run_id=$2`, orgID, runID, snapshot).
		Scan(&chunkRuns, &chunkLeads, &invalidChunkRuns); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT last_run_id,last_status FROM outreach_feed_sync_state WHERE organization_id=$1`, orgID).
		Scan(&stateRun, &stateStatus); err != nil {
		t.Fatal(err)
	}
	if accounts != 10_000 || currentInitial != 9_000 || duplicateInitial != 0 || reviewInitial != 0 ||
		staleInitial != stale || drafts != 0 || dispatch != 0 || commercialActions != 0 || chunkRuns != 20 || chunkLeads != 10_000 ||
		invalidChunkRuns != 0 || stateRun != runID || stateStatus != "completed" {
		t.Fatalf("backlog mismatch: accounts=%d current=%d duplicates=%d review=%d stale=%d drafts=%d dispatch=%d commercial_actions=%d chunks=%d chunk_leads=%d invalid_chunks=%d state=%s/%s",
			accounts, currentInitial, duplicateInitial, reviewInitial, staleInitial, drafts, dispatch, commercialActions,
			chunkRuns, chunkLeads, invalidChunkRuns, stateRun, stateStatus)
	}

	rows, err := pool.Query(ctx, `
		SELECT initial_backlog_reason_code,count(*) FROM outreach_accounts
		WHERE organization_id=$1 AND source_run_id=$2
		GROUP BY initial_backlog_reason_code`, orgID, runID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	reasons := map[string]int{}
	for rows.Next() {
		var reason string
		var count int
		if err = rows.Scan(&reason, &count); err != nil {
			t.Fatal(err)
		}
		reasons[reason] = count
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if reasons[""] != 8_000 || reasons["supplier_role_unknown"] != 1_000 ||
		reasons["preferred_current_recipient_missing"] != 1_000 || len(reasons) != 3 {
		t.Fatalf("held reason distribution=%v", reasons)
	}
}

func writeSyntheticLoadManifest(t *testing.T, dir, runID, snapshot string, generatedAt time.Time) (string, [][]byte) {
	t.Helper()
	chunks := make([]map[string]any, 0, manifestLoadAccounts/manifestLoadChunkSize)
	raws := make([][]byte, 0, manifestLoadAccounts/manifestLoadChunkSize)
	for start := 0; start < manifestLoadAccounts; start += manifestLoadChunkSize {
		index := start / manifestLoadChunkSize
		leads := make([]FeedLead, 0, manifestLoadChunkSize)
		for i := start; i < start+manifestLoadChunkSize; i++ {
			leads = append(leads, syntheticLoadLead(i, runID, generatedAt))
		}
		feed := Feed{
			SchemaVersion: models.OutreachSchemaV1, GeneratedAt: generatedAt.Format(time.RFC3339),
			Source:     FeedSource{System: "extra-cli", RunID: runID, SnapshotHash: snapshot, ProfileID: "load", ProfileVersion: "1"},
			Pagination: FeedPagination{HasMore: start+manifestLoadChunkSize < manifestLoadAccounts}, Leads: leads,
		}
		raw, err := json.Marshal(feed)
		if err != nil {
			t.Fatal(err)
		}
		filename := fmt.Sprintf("chunk_%04d.json", index)
		if err = os.WriteFile(filepath.Join(dir, filename), raw, 0o600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		chunks = append(chunks, map[string]any{
			"file": filename, "chunk_index": index, "content_hash": hex.EncodeToString(sum[:]),
			"lead_count": manifestLoadChunkSize, "has_more": feed.Pagination.HasMore,
		})
		raws = append(raws, raw)
	}
	manifest := map[string]any{
		"schema_version": "confenge.outreach.manifest.v1", "generated_at": generatedAt.Format(time.RFC3339),
		"source": map[string]any{
			"system": "extra-cli", "run_id": runID, "snapshot_hash": snapshot,
			"profile_id": "load", "profile_version": "1",
		},
		"lead_count": manifestLoadAccounts, "chunk_count": len(chunks), "chunks": chunks,
		"deactivations": []any{}, "deactivation_count": 0,
	}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	if err = os.WriteFile(manifestPath, manifestRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	return manifestPath, raws
}

func syntheticLoadLead(index int, runID string, generatedAt time.Time) FeedLead {
	ready, notBlocked, notFixture, preferred := true, false, false, true
	cnpj := fmt.Sprintf("%014d", 10_000_000_000_000+index)
	evidenceID := fmt.Sprintf("role-evidence-%05d", index)
	evidenceHash := fmt.Sprintf("%064x", index+1)
	lead := FeedLead{
		SourceLeadID: fmt.Sprintf("load-lead-%05d", index),
		Company: FeedCompany{
			CNPJ14: cnpj, CNPJRoot: cnpj[:8], RazaoSocial: fmt.Sprintf("Empresa Carga %05d Ltda", index),
			NomeFantasia: fmt.Sprintf("Empresa %05d", index), UF: "SP", Website: fmt.Sprintf("https://empresa%05d.com.br", index),
		},
		Priority: FeedPriority{Rank: index + 1, Score: 80, Tier: "HIGH", Confidence: "HIGH"},
		Moment: FeedMoment{
			Code: "CONTRACT_EXTENSION", Summary: "Prorrogação contratual publicada",
			ObservedAt: generatedAt.Format("2006-01-02"), Confidence: "HIGH", EvidenceIDs: []string{evidenceID},
		},
		Offer: FeedOffer{ServiceCode: "ADDITIVE_REVIEW", ServiceName: "Revisão de aditivos", EntryOffer: "Leitura técnica", Rationale: "Prorrogação recente"},
		MessagingContext: FeedMessaging{
			FactToMention: "Prorrogação contratual publicada em fonte oficial",
			QuestionToAsk: "Faz sentido revisar os controles?", CTA: "Posso enviar um checklist?",
		},
		TargetFitClass: TargetFitConfirmed, TargetFitVersion: "load-fit-v1",
		TargetFitComputedAt: generatedAt.Format(time.RFC3339), TargetFitSourceWatermark: generatedAt.Format(time.RFC3339),
		TargetFitFresh: &ready, TargetFitSendTier: "A_AUTOMATIC", EmailSendReady: &ready,
		CommercialState: "NEW",
		ContractorRole: FeedContractorRole{
			Status: ContractorRoleConfirmed, TargetPartyRole: "SUPPLIER", PolicyVersion: DelegatedFirstTouchEvidenceV1,
			Source: "extra-cli:v_contracts_canonical_v2", SourceRunID: runID, ObservedAt: generatedAt.Format(time.RFC3339),
			EvidenceHash: evidenceHash, EvidenceReference: "extra-cli:v_contracts_canonical_v2:sha256:" + evidenceHash,
			EvidenceIDs: []string{evidenceID}, ReasonCodes: []string{"lead_matches_supplier", "lead_differs_from_buyer"},
			SupplierCNPJ14: cnpj, SupplierIdentityRef: "cnpj:" + cnpj,
			BuyerCNPJ14: "99000000000001", BuyerIdentityRef: "cnpj:99000000000001",
			RoleMatchMethod: "SUPPLIER_EXACT_CNPJ14", Confidence: "HIGH",
		},
	}
	if index < 9_000 {
		email := fmt.Sprintf("contato@empresa%05d.com.br", index)
		lead.Contacts = []FeedContact{{
			SourceContactID: fmt.Sprintf("load-contact-%05d", index), Name: "Equipe Comercial", Role: "Comercial", Email: email,
			SourceURL: fmt.Sprintf("https://empresa%05d.com.br/contato", index), SourceDate: generatedAt.Format("2006-01-02"),
			VerificationStatus: models.OutreachVerifyOfficialSource, Confidence: "HIGH", Recommended: true,
			EmailSendReady: &ready, MailboxPurpose: "COMERCIAL", MailboxPurposeSendBlocked: &notBlocked,
			OwnershipStatus: "COMPANY_OWNED", RecipientCommercialSuitability: "SUITABLE",
			ProvenanceChainValid: &ready, DerivedFromFixture: &notFixture, RootSourceType: "OFFICIAL_COMPANY_WEBSITE",
			RouteClass: RouteClassRoleOrDepartment, ControlledEmailEligible: &ready, PreferredInitial: &preferred,
			MailboxCompanyEvidence: "OBSERVED", MailboxDepartmentEvidence: "OBSERVED",
			ChannelEpistemic: "OBSERVED", RouteFreshness: "FRESH", RouteSuppression: "NONE", RiskClass: "ALLOWED",
		}}
	}
	if index >= 8_000 && index < 9_000 {
		lead.ContractorRole = FeedContractorRole{
			Status: ContractorRoleUnknown, TargetPartyRole: ContractorRoleUnknown, PolicyVersion: DelegatedFirstTouchEvidenceV1,
			Source: "extra-cli:v_contracts_canonical_v2", SourceRunID: runID, ObservedAt: generatedAt.Format(time.RFC3339),
			EvidenceHash: evidenceHash, EvidenceReference: "extra-cli:v_contracts_canonical_v2:sha256:" + evidenceHash,
			ReasonCodes: []string{"supplier_role_not_confirmed"},
		}
	}
	return lead
}
