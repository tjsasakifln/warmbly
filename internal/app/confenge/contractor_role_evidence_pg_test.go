package confenge

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

func TestContractorRoleEvidenceMigrationBackfillsTypedAuthority(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, testPostgresDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	actor, orgID, importID := uuid.New(), uuid.New(), uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,first_name,last_name,email) VALUES($1,'Evidence','Backfill',$2)`, actor, "evidence-"+actor.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO organizations(id,name,slug,owner_user_id) VALUES($1,'Evidence Backfill',$2,$3)`, orgID, "evidence-"+orgID.String(), actor); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, orgID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, actor)
	})
	now := time.Now().UTC().Truncate(time.Second)
	repo := repository.NewOutreachRepository(pool)
	if err = repo.CreateImportRun(ctx, &models.OutreachImportRun{
		ID: importID, OrganizationID: orgID, SourceSystem: "extra-cli", SourceRunID: "run-backfill",
		SchemaVersion: models.OutreachSchemaV1, SnapshotHash: strings.Repeat("b", 64),
		Status: models.OutreachImportCompleted, StartedAt: now, IdempotencyKey: "backfill-" + importID.String(),
	}); err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("a", 64)
	account := &models.OutreachAccount{
		OrganizationID: orgID, SourceLeadID: "lead-backfill", CNPJ14: "11222333000144", CNPJRoot: "11222333",
		RazaoSocial: "Empresa Backfill Ltda", QueueState: models.OutreachQueueNeedsReview,
		SourceSystem: "extra-cli", SourceRunID: "run-backfill", LastImportRunID: &importID,
		ContractorRoleStatus: ContractorRoleConfirmed, TargetPartyRole: "SUPPLIER",
		ContractorRolePolicyVersion: DelegatedFirstTouchEvidenceV1,
		ContractorRoleSource:        "extra-cli:v_contracts_canonical_v2", ContractorRoleSourceRunID: "run-backfill",
		ContractorRoleObservedAt: &now, ContractorRoleEvidenceHash: hash,
		ContractorRoleEvidenceReference: "extra-cli:v_contracts_canonical_v2:sha256:" + hash,
		ContractorRoleEvidenceIDs:       []string{"contract-backfill"}, SupplierCNPJ14: "11222333000144",
		SupplierIdentityRef: "cnpj:11222333000144", BuyerCNPJ14: "99888777000166",
		BuyerIdentityRef: "cnpj:99888777000166", ContractorRoleMatchMethod: "SUPPLIER_EXACT_CNPJ14",
		ContractorRoleConfidence: "HIGH", ContractorRoleReasonCodes: []string{"lead_matches_supplier", "lead_differs_from_buyer"},
	}
	if _, err = repo.UpsertAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	migration := filepath.Join(filepath.Dir(file), "..", "..", "infrastructure", "db", "migrations", "000129_confenge_contractor_role_evidence.up.sql")
	raw, err := os.ReadFile(migration)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	var evidenceType, epistemic, reliability string
	var evidenceImport uuid.UUID
	if err = pool.QueryRow(ctx, `
		SELECT evidence_type,epistemic_class,reliability,last_import_run_id
		FROM outreach_evidence
		WHERE organization_id=$1 AND account_id=$2 AND source_evidence_id='contract-backfill'`, orgID, account.ID).
		Scan(&evidenceType, &epistemic, &reliability, &evidenceImport); err != nil {
		t.Fatal(err)
	}
	if evidenceType != "CONTRACTOR_ROLE_ATTESTATION" || epistemic != models.OutreachEpistemicConfirmedFact ||
		reliability != "HIGH" || evidenceImport != importID {
		t.Fatalf("typed evidence backfill drifted: type=%s epistemic=%s reliability=%s import=%s", evidenceType, epistemic, reliability, evidenceImport)
	}
}
