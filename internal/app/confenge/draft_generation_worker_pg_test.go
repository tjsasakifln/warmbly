package confenge

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

func TestDraftGenerationWorkerLeasesControlledRouteWithoutNamedPersonRollup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, testPostgresDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	applyHumanGateSchema(t, pool)

	actor, orgID := uuid.New(), uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO users (id,first_name,last_name,email) VALUES($1,'Draft','Worker',$2)`, actor, "draft-worker-"+actor.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO organizations (id,name,slug,owner_user_id) VALUES($1,'Draft Worker Fixture',$2,$3)`, orgID, "draft-worker-"+orgID.String(), actor); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, orgID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id=$1`, actor)
	})
	staleRun, currentRun := uuid.New(), uuid.New()
	for _, run := range []struct {
		id, source, snapshot string
	}{
		{staleRun.String(), "controlled-draft-stale", "controlled-draft-stale-snapshot"},
		{currentRun.String(), "controlled-draft-current", "controlled-draft-current-snapshot"},
	} {
		if _, err = pool.Exec(ctx, `INSERT INTO outreach_import_runs
			(id,organization_id,source_run_id,snapshot_hash,status,idempotency_key)
			VALUES($1,$2,$3,$4,'completed',$5)`, run.id, orgID, run.source, run.snapshot, "controlled-draft-"+run.id); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	repo := repository.NewOutreachRepository(pool)
	account := &models.OutreachAccount{
		OrganizationID:           orgID,
		SourceLeadID:             "controlled-draft-lease",
		CNPJ14:                   "76271049000185",
		CNPJRoot:                 "76271049",
		RazaoSocial:              "Controlled Route Fixture Ltda",
		NomeFantasia:             "Controlled Route Fixture",
		QueueState:               models.OutreachQueueReadyToGenerate,
		SourceSystem:             "extra-cli",
		SourceRunID:              "controlled-draft-current",
		LastImportRunID:          &currentRun,
		ActivationState:          ActivationActionableNow,
		TargetFitSendTier:        "A_AUTOMATIC",
		TargetFitClass:           TargetFitConfirmed,
		TargetFitVersion:         "target-fit.v1",
		TargetFitComputedAt:      &now,
		TargetFitSourceWatermark: now.Format(time.RFC3339Nano),
		TargetFitObservedAt:      &now,
		TargetFitFresh:           true,
		TargetFitEligible:        true,
		EmailSendReady:           false,
	}
	if _, err = repo.UpsertAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	personUnknown := true
	candidate := &models.OutreachContactCandidate{
		OrganizationID:                 orgID,
		AccountID:                      account.ID,
		SourceContactID:                "controlled-generic-route",
		Email:                          "contato@controlled-route.example",
		SourceURL:                      "https://controlled-route.example/contato",
		SourceDate:                     &now,
		VerificationStatus:             models.OutreachVerifyCandidateUnverified,
		Confidence:                     "HIGH",
		Recommended:                    true,
		EmailSendReady:                 false,
		MailboxPurpose:                 "GENERIC_CONTACT",
		MailboxPurposeSendBlocked:      true,
		OwnershipStatus:                "COMPANY_OWNED",
		RecipientCommercialSuitability: "SUITABLE_GENERIC",
		LastImportRunID:                &currentRun,
		DiscoveryJSON: eligibleDisc(
			t,
			RouteClassGenericCompany,
			true,
			controlledDiscovery{PersonUnknown: &personUnknown},
		),
	}
	if _, err = repo.UpsertCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	staleAccount := *account
	staleAccount.ID = uuid.Nil
	staleAccount.SourceLeadID = "controlled-draft-stale-route"
	staleAccount.CNPJ14 = "76271049000347"
	staleAccount.QueueState = models.OutreachQueueReadyToGenerate
	if _, err = repo.UpsertAccount(ctx, &staleAccount); err != nil {
		t.Fatal(err)
	}
	staleCandidate := *candidate
	staleCandidate.ID = uuid.Nil
	staleCandidate.AccountID = staleAccount.ID
	staleCandidate.SourceContactID = "controlled-generic-route-stale"
	staleCandidate.Email = "stale@controlled-route.example"
	staleCandidate.LastImportRunID = &staleRun
	if _, err = repo.UpsertCandidate(ctx, &staleCandidate); err != nil {
		t.Fatal(err)
	}

	svc := NewService(Config{Enabled: true, RequireHumanApproval: true}, repo, nil).(*service)
	svc.WireHumanGate(pool)
	processed, _ := svc.ProcessDraftGenerationOnce(ctx)
	if !processed {
		t.Fatal("controlled route was not leased while account email_send_ready=false")
	}
	var attempts int
	if err = pool.QueryRow(ctx, `SELECT draft_generation_attempts FROM outreach_accounts WHERE id=$1`, account.ID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Fatalf("draft_generation_attempts=%d want 1", attempts)
	}
	if err = pool.QueryRow(ctx, `SELECT draft_generation_attempts FROM outreach_accounts WHERE id=$1`, staleAccount.ID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 {
		t.Fatalf("stale route draft_generation_attempts=%d want 0", attempts)
	}

	// The preparation workers share a per-org first-touch review ceiling. Once
	// the requested backlog exists, another otherwise eligible supplier must not
	// be leased; this is preparation capacity, never send authority.
	if _, err = pool.Exec(ctx, `UPDATE outreach_touchpoints SET state='NEEDS_REVIEW' WHERE organization_id=$1 AND account_id=$2 AND ordinal=1`, orgID, account.ID); err != nil {
		t.Fatal(err)
	}
	account2 := *account
	account2.ID = uuid.Nil
	account2.SourceLeadID = "controlled-draft-backlog-cap"
	account2.CNPJ14 = "76271049000266"
	account2.CNPJRoot = "76271049"
	account2.QueueState = models.OutreachQueueReadyToGenerate
	if _, err = repo.UpsertAccount(ctx, &account2); err != nil {
		t.Fatal(err)
	}
	candidate2 := *candidate
	candidate2.ID = uuid.Nil
	candidate2.AccountID = account2.ID
	candidate2.SourceContactID = "controlled-generic-route-backlog-cap"
	candidate2.Email = "contato-2@controlled-route.example"
	if _, err = repo.UpsertCandidate(ctx, &candidate2); err != nil {
		t.Fatal(err)
	}
	svc.cfg.DraftReviewBacklogTarget = 1
	processed, err = svc.ProcessDraftGenerationOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if processed {
		t.Fatal("review backlog ceiling leased another supplier")
	}
}
