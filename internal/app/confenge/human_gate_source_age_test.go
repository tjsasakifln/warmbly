package confenge

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// Cohort preparation is commercial work. It may not refuse because the crawler
// went quiet, only because the run itself cannot be proven.
func TestHumanGateCreateNeverRefusesOnCrawlerAge(t *testing.T) {
	raw, err := os.ReadFile("human_gate.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	create := body[strings.Index(body, "func (s *service) CreateHumanGateCohort("):]
	if idx := strings.Index(create, "\nfunc (s *service) ReproduceHumanGateCohort("); idx > 0 {
		create = create[:idx]
	}
	if strings.Contains(create, `"source_stale"`) || strings.Contains(create, "now.Before(asOf.Add(maxAge))") {
		t.Fatal("CreateHumanGateCohort must not gate a proven population on crawler age")
	}
	if !strings.Contains(create, "ValidateHistoricalSourceFreshness(freshness)") {
		t.Fatal("the synthesized attestation must be proven FRESH at publication, not FRESH now")
	}
	if !strings.Contains(create, "assertCohortMembersCommerciallyQualified") {
		t.Fatal("membership must still be proven per member with COMMERCIAL_AUTHORITY/2.0")
	}
}

func TestHumanGatePostgresPreparesSeventyTwoHoursAfterLastCrawl(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, testPostgresDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	applyHumanGateSchema(t, pool)

	actor, orgID := uuid.New(), uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO users (id,first_name,last_name,email) VALUES($1,'Quiet','Crawler',$2)`, actor, "quiet-crawler-"+actor.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO organizations (id,name,slug,owner_user_id) VALUES($1,'Quiet Crawler Fixture',$2,$3)`, orgID, "quiet-crawler-"+orgID.String(), actor); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, orgID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id=$1`, actor)
	})

	now := time.Now().UTC().Truncate(time.Millisecond)
	crawledAt := now.Add(-72 * time.Hour)
	runID := "quiet-crawler-" + uuid.NewString()
	repo := repository.NewOutreachRepository(pool)
	run := &models.OutreachImportRun{
		OrganizationID: orgID, SourceSystem: "extra-cli", SourceRunID: runID,
		SchemaVersion: models.OutreachSchemaV1, SnapshotHash: "quiet-snapshot-" + uuid.NewString(),
		RepoSHA: "extra-quiet-sha", ProfileID: "confenge", ProfileVersion: "supplier-contract.v1",
		Status: models.OutreachImportCompleted, FinishedAt: &crawledAt, SourceGeneratedAt: &crawledAt,
	}
	if err = repo.CreateImportRun(ctx, run); err != nil {
		t.Fatal(err)
	}

	unknown := true
	confidence := 0.99
	for i := 0; i < 3; i++ {
		root := fmt.Sprintf("%08d", 61000000+i)
		acc := &models.OutreachAccount{
			OrganizationID: orgID, SourceLeadID: fmt.Sprintf("quiet-supplier-%03d", i),
			CNPJ14: root + "000100", CNPJRoot: root,
			RazaoSocial: fmt.Sprintf("Fornecedor Silencioso %03d Ltda", i), NomeFantasia: fmt.Sprintf("Fornecedor %03d", i),
			PriorityRank: i + 1, PriorityScore: float64(10 - i),
			MomentCode: "CONTRACT_EXTENSION", MomentSummary: "Prorrogação contratual publicada", MomentObservedAt: &crawledAt,
			ServiceCode: "REAJUSTE_14133", ServiceName: "Revisão de aditivos",
			FactToMention: "Prorrogação contratual publicada no portal oficial",
			QuestionToAsk: "Faz sentido revisar os controles?", CTA: "Posso enviar um checklist?",
			CommercialState: "NEW", QueueState: models.OutreachQueueNeedsContact,
			SourceSystem: "extra-cli", SourceRunID: runID,
			ActivationState: ActivationActionableNow, ActivationScore: float64(100 - i),
			ActivationPolicyVersion: "activation.v1", ActivationEvaluatedAt: &now,
			ActivationExpiresAt: timePtr(now.Add(12 * time.Hour)),
			TargetFitSendTier:   "A_AUTOMATIC", TargetFitClass: TargetFitConfirmed,
			TargetFitConfidence: &confidence, TargetFitVersion: "target-fit.v1", TargetFitComputedAt: &now,
			TargetFitSourceWatermark: "supplier-watermark", TargetFitObservedAt: &now,
			TargetFitFresh: true, TargetFitEligible: true,
		}
		if _, err = repo.UpsertAccount(ctx, acc); err != nil {
			t.Fatal(err)
		}
		cand := &models.OutreachContactCandidate{
			OrganizationID: orgID, AccountID: acc.ID,
			SourceContactID: fmt.Sprintf("quiet-contact-%03d", i),
			Email:           fmt.Sprintf("contato@fornecedor-silencioso-%03d.com.br", i),
			SourceURL:       fmt.Sprintf("https://fornecedor-silencioso-%03d.com.br/contato", i),
			SourceDate:      &now, VerificationStatus: models.OutreachVerifyCandidateUnverified,
			Confidence: "HIGH", Recommended: true, MailboxPurpose: "GENERIC_CONTACT",
			OwnershipStatus: "COMPANY_OWNED", RecipientCommercialSuitability: "SUITABLE",
			DiscoveryJSON: eligibleDisc(t, RouteClassGenericCompany, true, controlledDiscovery{PersonUnknown: &unknown}),
		}
		if _, err = repo.UpsertCandidate(ctx, cand); err != nil {
			t.Fatal(err)
		}
	}

	svc := NewService(Config{Enabled: true, RepositorySHA: "quiet-crawler-sha", FeedMaxAge: 24 * time.Hour}, repo, nil).(*service)
	svc.WireHumanGate(pool)
	cohort, x := svc.CreateHumanGateCohort(ctx, orgID, actor, HumanGateCreateInput{
		Limit: 2, SelectionMode: HumanGateSelectionNextUnclaimed,
		IdempotencyKey: "quiet-crawler-" + uuid.NewString(), CorrelationID: "corr-quiet-crawler",
	})
	if x != nil {
		t.Fatalf("a 72h-old crawl refused founder cohort preparation: %v", x)
	}
	if cohort == nil || len(cohort.Manifest.Members) != 2 {
		t.Fatalf("cohort members = %v", cohort)
	}
}
