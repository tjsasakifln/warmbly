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

// A lagging last_import_run_id is acquisition provenance: it must not retire a
// first touch in review. Only actual recipient invalidity may.
func TestRetireStaleReviewBacklogKeepsLaggingButValidRecipients(t *testing.T) {
	runStaleReviewBacklogFixture(t, false)
}

func TestRetireStaleReviewBacklogRequeuesOnlyAgainstCurrentRoute(t *testing.T) {
	runStaleReviewBacklogFixture(t, true)
}

func runStaleReviewBacklogFixture(t *testing.T, invalidateRecipient bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, testPostgresDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	applyHumanGateSchema(t, pool)

	actor, orgID := uuid.New(), uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO users (id,first_name,last_name,email) VALUES($1,'Stale','Review',$2)`, actor, "stale-review-"+actor.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO organizations (id,name,slug,owner_user_id) VALUES($1,'Stale Review Fixture',$2,$3)`, orgID, "stale-review-"+orgID.String(), actor); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, orgID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id=$1`, actor)
	})

	oldRun, currentRun := uuid.New(), uuid.New()
	for _, run := range []struct {
		id, source, snapshot string
	}{
		{oldRun.String(), "stale-review-old", "stale-review-old-snapshot"},
		{currentRun.String(), "stale-review-current", "stale-review-current-snapshot"},
	} {
		if _, err = pool.Exec(ctx, `INSERT INTO outreach_import_runs
			(id,organization_id,source_run_id,snapshot_hash,status,idempotency_key)
			VALUES($1,$2,$3,$4,'completed',$5)`, run.id, orgID, run.source, run.snapshot, "stale-review-"+run.id); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	repo := repository.NewOutreachRepository(pool)
	account := &models.OutreachAccount{
		OrganizationID: orgID, SourceLeadID: "stale-review-supplier", CNPJ14: "76271049000185", CNPJRoot: "76271049",
		RazaoSocial: "Stale Review Supplier Ltda", QueueState: models.OutreachQueueNeedsReview,
		SourceSystem: "extra-cli", SourceRunID: "stale-review-current", LastImportRunID: &currentRun,
		ActivationState: ActivationActionableNow, TargetFitClass: TargetFitConfirmed,
		TargetFitVersion: "target-fit.v1", TargetFitComputedAt: &now,
		TargetFitSourceWatermark: now.Format(time.RFC3339Nano), TargetFitObservedAt: &now,
		TargetFitFresh: true, TargetFitEligible: true,
	}
	if _, err = repo.UpsertAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	unknown := true
	makeCandidate := func(sourceID, email string, importID uuid.UUID) *models.OutreachContactCandidate {
		return &models.OutreachContactCandidate{
			OrganizationID: orgID, AccountID: account.ID, SourceContactID: sourceID,
			Email: email, SourceURL: "https://stale-review.example/contato", SourceDate: &now,
			VerificationStatus: models.OutreachVerifyOfficialSource, Confidence: "HIGH", Recommended: true,
			OwnershipStatus: "COMPANY_OWNED", ChannelEpistemic: "OBSERVED", RouteFreshness: "FRESH", RouteSuppression: "NONE",
			LastImportRunID: &importID,
			DiscoveryJSON:   eligibleDisc(t, RouteClassGenericCompany, true, controlledDiscovery{PersonUnknown: &unknown}),
		}
	}
	oldCandidate := makeCandidate("stale-review-old-route", "old@stale-review.example", oldRun)
	currentCandidate := makeCandidate("stale-review-current-route", "current@stale-review.example", currentRun)
	if invalidateRecipient {
		oldCandidate.Bounced = true
	}
	if _, err = repo.UpsertCandidate(ctx, oldCandidate); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.UpsertCandidate(ctx, currentCandidate); err != nil {
		t.Fatal(err)
	}

	draftID, touchpointID, followupID := uuid.New(), uuid.New(), uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO outreach_drafts
		(id,organization_id,account_id,contact_candidate_id,recipient_email,subject,body_text,status)
		VALUES($1,$2,$3,$4,$5,'Assunto','Mensagem','NEEDS_REVIEW')`,
		draftID, orgID, account.ID, oldCandidate.ID, oldCandidate.Email); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO outreach_touchpoints
		(id,organization_id,account_id,contact_candidate_id,ordinal,state,draft_id,recipient,subject,body_text,content_hash)
		VALUES($1,$2,$3,$4,1,'NEEDS_REVIEW',$5,$6,'Assunto','Mensagem','hash')`,
		touchpointID, orgID, account.ID, oldCandidate.ID, draftID, oldCandidate.Email); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO outreach_touchpoints
		(id,organization_id,account_id,contact_candidate_id,ordinal,state,recipient)
		VALUES($1,$2,$3,$4,2,'PLANNED',$5)`, followupID, orgID, account.ID, oldCandidate.ID, oldCandidate.Email); err != nil {
		t.Fatal(err)
	}

	svc := NewService(Config{Enabled: true, RequireHumanApproval: true}, repo, nil).(*service)
	svc.WireHumanGate(pool)
	retired, requeued, err := svc.retireStaleReviewBacklog(ctx, orgID)
	if err != nil {
		t.Fatal(err)
	}
	if !invalidateRecipient {
		if retired != 0 || requeued != 0 {
			t.Fatalf("a lagging import run retired proven review work: retired=%d requeued=%d", retired, requeued)
		}
		var keptState, keptDraft string
		if err = pool.QueryRow(ctx, `SELECT state FROM outreach_touchpoints WHERE id=$1`, touchpointID).Scan(&keptState); err != nil {
			t.Fatal(err)
		}
		if err = pool.QueryRow(ctx, `SELECT status FROM outreach_drafts WHERE id=$1`, draftID).Scan(&keptDraft); err != nil {
			t.Fatal(err)
		}
		if keptState != models.TouchpointNeedsReview || keptDraft != "NEEDS_REVIEW" {
			t.Fatalf("touchpoint=%s draft=%s must stay in review", keptState, keptDraft)
		}
		return
	}
	if retired != 1 || requeued != 1 {
		t.Fatalf("retired=%d requeued=%d want 1/1", retired, requeued)
	}
	var touchpointState, stopReason, draftStatus, queueState string
	if err = pool.QueryRow(ctx, `SELECT state,stop_reason FROM outreach_touchpoints WHERE id=$1`, touchpointID).Scan(&touchpointState, &stopReason); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT status FROM outreach_drafts WHERE id=$1`, draftID).Scan(&draftStatus); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT queue_state FROM outreach_accounts WHERE id=$1`, account.ID).Scan(&queueState); err != nil {
		t.Fatal(err)
	}
	if touchpointState != models.TouchpointCancelled || stopReason != staleReviewStopReason ||
		draftStatus != models.OutreachDraftBlocked || queueState != models.OutreachQueueReadyToGenerate {
		t.Fatalf("touchpoint=%s reason=%s draft=%s account=%s", touchpointState, stopReason, draftStatus, queueState)
	}
	if err = pool.QueryRow(ctx, `SELECT state FROM outreach_touchpoints WHERE id=$1`, followupID).Scan(&touchpointState); err != nil {
		t.Fatal(err)
	}
	if touchpointState != models.TouchpointCancelled {
		t.Fatalf("stale follow-up state=%s", touchpointState)
	}
	retired, requeued, err = svc.retireStaleReviewBacklog(ctx, orgID)
	if err != nil || retired != 0 || requeued != 0 {
		t.Fatalf("replay retired=%d requeued=%d err=%v", retired, requeued, err)
	}
	replanned, xerr := svc.PlanAccountCadence(ctx, orgID, actor, account.ID, nil, models.OutreachChannelEmail)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if len(replanned) == 0 || replanned[0].ContactCandidateID == nil || *replanned[0].ContactCandidateID != currentCandidate.ID || replanned[0].Recipient != currentCandidate.Email {
		t.Fatalf("cadence did not rebind to current candidate: %+v", replanned)
	}
}
