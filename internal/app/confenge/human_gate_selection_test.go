package confenge

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

func TestNormalizeHumanGateSelectionIsStableAndClosedWorld(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	in := HumanGateCreateInput{
		Limit:             10,
		SelectionMode:     " recover_prior ",
		RecoverVersionIDs: []uuid.UUID{b, a, b},
	}
	if x := normalizeHumanGateSelection(&in); x != nil {
		t.Fatalf("normalize: %v", x)
	}
	if in.SelectionMode != HumanGateSelectionRecoverPrior {
		t.Fatalf("mode = %q", in.SelectionMode)
	}
	if len(in.RecoverVersionIDs) != 2 || in.RecoverVersionIDs[0].String() > in.RecoverVersionIDs[1].String() {
		t.Fatalf("recover ids were not de-duplicated and sorted: %v", in.RecoverVersionIDs)
	}
	first := humanGateSelectionRequestHash(in)
	in.RecoverVersionIDs[0], in.RecoverVersionIDs[1] = in.RecoverVersionIDs[1], in.RecoverVersionIDs[0]
	if x := normalizeHumanGateSelection(&in); x != nil {
		t.Fatalf("renormalize: %v", x)
	}
	if got := humanGateSelectionRequestHash(in); got != first {
		t.Fatalf("request hash changed with recover id order: %s != %s", got, first)
	}

	invalid := HumanGateCreateInput{SelectionMode: HumanGateSelectionNextUnclaimed, RecoverVersionIDs: []uuid.UUID{a}}
	if x := normalizeHumanGateSelection(&invalid); x == nil || x.Identifier != "recover_versions_not_allowed" {
		t.Fatalf("NEXT_UNCLAIMED with recover ids = %v", x)
	}
	invalid = HumanGateCreateInput{SelectionMode: "CALLER_OFFSET"}
	if x := normalizeHumanGateSelection(&invalid); x == nil || x.Identifier != "invalid_selection_mode" {
		t.Fatalf("caller-controlled pagination mode = %v", x)
	}
	legacy := HumanGateCreateInput{Limit: 5, SourceRunID: "run-before-rollout", SelectionMode: HumanGateSelectionLegacy}
	wantLegacy := humanGateRequestHash(struct {
		Limit int
		Run   string
	}{5, "run-before-rollout"})
	if got := humanGateSelectionRequestHash(legacy); got != wantLegacy {
		t.Fatalf("legacy request hash changed across rollout: %s != %s", got, wantLegacy)
	}
}

func TestHumanGateSupplierIdentityAndRecipientClaimsAreCanonical(t *testing.T) {
	acc := &models.OutreachAccount{CNPJ14: "12.345.678/0001-90", CNPJRoot: ""}
	if got := canonicalSupplierRoot(acc); got != "12345678" {
		t.Fatalf("supplier root = %q", got)
	}
	if humanGateRecipientHash(" Comercial@Fornecedor.Example ") != humanGateRecipientHash("comercial@fornecedor.example") {
		t.Fatal("recipient claim hash must ignore mailbox case and surrounding whitespace")
	}
	if humanGateRecipientHash("comercial@fornecedor.example") == humanGateRecipientHash("compras@fornecedor.example") {
		t.Fatal("distinct recipients must not share a claim hash")
	}
}

func TestHumanGateRecoveryReappliesOperationalSupplierGates(t *testing.T) {
	now := time.Now().UTC()
	confidence := 0.99
	acc := &models.OutreachAccount{
		CNPJ14:                   "12345678000190",
		CNPJRoot:                 "12345678",
		SourceRunID:              "supplier-run",
		QueueState:               models.OutreachQueueNeedsContact,
		ActivationState:          ActivationActionableNow,
		TargetFitEligible:        true,
		TargetFitClass:           TargetFitConfirmed,
		TargetFitConfidence:      &confidence,
		TargetFitFresh:           true,
		TargetFitVersion:         "target-fit.v1",
		TargetFitSourceWatermark: "supplier-watermark",
		TargetFitObservedAt:      &now,
		EmailSendReady:           true,
	}
	if reason := humanGateAccountOperational(acc, "supplier-run", now); reason != "" {
		t.Fatalf("operational supplier refused: %s", reason)
	}
	// Controlled institutional routes intentionally keep the named-person
	// EMAIL_SEND_READY rollup false until APPROVE performs live validation.
	acc.EmailSendReady = false
	if reason := humanGateAccountOperational(acc, "supplier-run", now); reason != "" {
		t.Fatalf("controlled-route supplier refused by named-person rollup: %s", reason)
	}
	acc.SourceRunID = "contracting-authority-run"
	if reason := humanGateAccountOperational(acc, "supplier-run", now); reason != "source_run_mismatch" {
		t.Fatalf("cross-run recovery = %q", reason)
	}
	acc.SourceRunID = "supplier-run"
	acc.TargetFitFresh = false
	if reason := humanGateAccountOperational(acc, "supplier-run", now); reason != "target_fit_not_operational" {
		t.Fatalf("stale target fit recovery = %q", reason)
	}
}

func TestHumanGatePostgresCreatesOneHundredDisjointSupplierMessages(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, testPostgresDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	applyHumanGateSchema(t, pool)

	actor, orgID := uuid.New(), uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO users (id,first_name,last_name,email) VALUES($1,'Selection','Fixture',$2)`, actor, "selection-"+actor.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO organizations (id,name,slug,owner_user_id) VALUES($1,'Selection Fixture',$2,$3)`, orgID, "selection-"+orgID.String(), actor); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, orgID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id=$1`, actor)
	})

	now := time.Now().UTC().Truncate(time.Millisecond)
	runID := "supplier-selection-" + uuid.NewString()
	repo := repository.NewOutreachRepository(pool)
	finished := now
	run := &models.OutreachImportRun{
		OrganizationID:    orgID,
		SourceSystem:      "extra-cli",
		SourceRunID:       runID,
		SchemaVersion:     models.OutreachSchemaV1,
		SnapshotHash:      "supplier-snapshot-" + uuid.NewString(),
		RepoSHA:           "extra-supplier-sha",
		ProfileID:         "confenge",
		ProfileVersion:    "supplier-contract.v1",
		Status:            models.OutreachImportCompleted,
		FinishedAt:        &finished,
		SourceGeneratedAt: &finished,
	}
	if err = repo.CreateImportRun(ctx, run); err != nil {
		t.Fatal(err)
	}

	unknown := true
	confidence := 0.99
	for i := 0; i < 120; i++ {
		root := fmt.Sprintf("%08d", 70000000+i)
		acc := &models.OutreachAccount{
			OrganizationID:           orgID,
			SourceLeadID:             fmt.Sprintf("supplier-%03d", i),
			CNPJ14:                   root + "000100",
			CNPJRoot:                 root,
			RazaoSocial:              fmt.Sprintf("Fornecedor Fixture %03d Ltda", i),
			NomeFantasia:             fmt.Sprintf("Fornecedor %03d", i),
			PriorityRank:             i + 1,
			PriorityScore:            float64(120 - i),
			MomentCode:               "CONTRACT_EXTENSION",
			MomentSummary:            "Prorrogação contratual publicada",
			MomentObservedAt:         &now,
			ServiceCode:              "ADDITIVE_REVIEW",
			ServiceName:              "Revisão de aditivos",
			FactToMention:            "Prorrogação contratual publicada no portal oficial",
			QuestionToAsk:            "Faz sentido revisar os controles?",
			CTA:                      "Posso enviar um checklist?",
			CommercialState:          "NEW",
			QueueState:               models.OutreachQueueNeedsContact,
			SourceSystem:             "extra-cli",
			SourceRunID:              runID,
			ActivationState:          ActivationActionableNow,
			ActivationScore:          100 - float64(i)/10,
			ActivationPolicyVersion:  "activation.v1",
			ActivationEvaluatedAt:    &now,
			ActivationExpiresAt:      timePtr(now.Add(12 * time.Hour)),
			TargetFitSendTier:        "A_AUTOMATIC",
			TargetFitClass:           TargetFitConfirmed,
			TargetFitConfidence:      &confidence,
			TargetFitVersion:         "target-fit.v1",
			TargetFitComputedAt:      &now,
			TargetFitSourceWatermark: "supplier-watermark",
			TargetFitObservedAt:      &now,
			TargetFitFresh:           true,
			TargetFitEligible:        true,
			EmailSendReady:           false,
		}
		if _, err = repo.UpsertAccount(ctx, acc); err != nil {
			t.Fatalf("upsert supplier %d: %v", i, err)
		}
		cand := &models.OutreachContactCandidate{
			OrganizationID:                 orgID,
			AccountID:                      acc.ID,
			SourceContactID:                fmt.Sprintf("supplier-contact-%03d", i),
			Email:                          fmt.Sprintf("contato@fornecedor-%03d.com.br", i),
			SourceURL:                      fmt.Sprintf("https://fornecedor-%03d.com.br/contato", i),
			SourceDate:                     &now,
			VerificationStatus:             models.OutreachVerifyCandidateUnverified,
			Confidence:                     "HIGH",
			Recommended:                    true,
			EmailSendReady:                 false,
			MailboxPurpose:                 "GENERIC_CONTACT",
			MailboxPurposeSendBlocked:      true,
			OwnershipStatus:                "COMPANY_OWNED",
			RecipientCommercialSuitability: "SUITABLE",
			DiscoveryJSON:                  eligibleDisc(t, RouteClassGenericCompany, true, controlledDiscovery{PersonUnknown: &unknown}),
		}
		// Reproduce a route imported before the controlled-email contract. Its
		// strict lane was exhausted and the repository persisted a recoverable
		// provenance block. The current authoritative stamp must clear that
		// non-terminal block without weakening DNC/bounce/manual suppression.
		cand.Blocked = true
		cand.BlockReason = "provenance_chain_invalid"
		if _, err = repo.UpsertCandidate(ctx, cand); err != nil {
			t.Fatalf("upsert legacy recipient %d: %v", i, err)
		}
		cand.Blocked = false
		if _, err = repo.UpsertCandidate(ctx, cand); err != nil {
			t.Fatalf("recover controlled recipient %d: %v", i, err)
		}
		stored, getErr := repo.GetCandidate(ctx, orgID, cand.ID)
		if getErr != nil || stored == nil || stored.Blocked {
			t.Fatalf("controlled recipient %d stayed blocked: candidate=%+v err=%v", i, stored, getErr)
		}
		if i == 119 {
			cand.Blocked = true
			cand.BlockReason = "manual_suppression"
			if _, err = repo.UpsertCandidate(ctx, cand); err != nil {
				t.Fatalf("upsert manual suppression: %v", err)
			}
			cand.Blocked = false
			cand.BlockReason = ""
			if _, err = repo.UpsertCandidate(ctx, cand); err != nil {
				t.Fatalf("replay after manual suppression: %v", err)
			}
			stored, getErr = repo.GetCandidate(ctx, orgID, cand.ID)
			if getErr != nil || stored == nil || !stored.Blocked || stored.BlockReason != "manual_suppression" {
				t.Fatalf("manual suppression was softened: candidate=%+v err=%v", stored, getErr)
			}
		}
	}

	svc := NewService(Config{Enabled: true, RepositorySHA: "warmbly-selection-sha", FeedMaxAge: 24 * time.Hour}, repo, nil).(*service)
	svc.WireHumanGate(pool)
	create := func(key string) (*HumanGateCohort, *errx.Error) {
		return svc.CreateHumanGateCohort(ctx, orgID, actor, HumanGateCreateInput{
			Limit:          10,
			SelectionMode:  HumanGateSelectionNextUnclaimed,
			IdempotencyKey: key,
			CorrelationID:  "corr-" + key,
		})
	}

	results := make(chan *HumanGateCohort, 2)
	errs := make(chan *errx.Error, 2)
	var wg sync.WaitGroup
	for _, key := range []string{"selection-concurrent-a", "selection-concurrent-b"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			cohort, x := create(key)
			results <- cohort
			errs <- x
		}(key)
	}
	wg.Wait()
	close(results)
	close(errs)
	var cohorts []*HumanGateCohort
	for x := range errs {
		if x != nil {
			t.Fatalf("concurrent create: %v", x)
		}
	}
	for cohort := range results {
		cohorts = append(cohorts, cohort)
	}
	for i := 2; i < 10; i++ {
		cohort, x := create(fmt.Sprintf("selection-sequential-%02d", i))
		if x != nil {
			t.Fatalf("sequential create %d: %v", i, x)
		}
		cohorts = append(cohorts, cohort)
	}

	roots, recipients := map[string]bool{}, map[string]bool{}
	for _, cohort := range cohorts {
		if cohort == nil || len(cohort.Manifest.Members) != 10 {
			t.Fatalf("cohort has %d members, want 10", len(cohort.Manifest.Members))
		}
		for _, member := range cohort.Manifest.Members {
			acc, getErr := repo.GetAccount(ctx, orgID, member.AccountID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			root := canonicalSupplierRoot(acc)
			if roots[root] {
				t.Fatalf("supplier root repeated: %s", root)
			}
			if recipients[member.Mailbox] {
				t.Fatalf("recipient repeated: %s", member.Mailbox)
			}
			roots[root], recipients[member.Mailbox] = true, true
		}
	}
	if len(roots) != 100 || len(recipients) != 100 {
		t.Fatalf("unique suppliers=%d recipients=%d, want 100/100", len(roots), len(recipients))
	}
	var claims int
	if err = pool.QueryRow(ctx, `SELECT count(*)::int FROM confenge_cohort_selection_claims WHERE organization_id=$1 AND source_run_id=$2`, orgID, runID).Scan(&claims); err != nil || claims != 100 {
		t.Fatalf("claims=%d error=%v", claims, err)
	}
	replayed, x := create("selection-concurrent-a")
	if x != nil || (replayed.ID != cohorts[0].ID && replayed.ID != cohorts[1].ID) {
		t.Fatalf("idempotent replay cohort=%v error=%v", replayed, x)
	}
}

func timePtr(value time.Time) *time.Time { return &value }
