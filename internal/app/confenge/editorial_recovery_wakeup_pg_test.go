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

func TestFeedSyncWakeupOnlyAdvancesFactuallyEligibleEnrichmentPending(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, testPostgresDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	applyHumanGateSchema(t, pool)

	actor, orgID := uuid.New(), uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO users (id,first_name,last_name,email) VALUES($1,'Recovery','Wakeup',$2)`, actor, "recovery-wakeup-"+actor.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO organizations (id,name,slug,owner_user_id) VALUES($1,'Recovery Wakeup Fixture',$2,$3)`, orgID, "recovery-wakeup-"+orgID.String(), actor); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, orgID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id=$1`, actor)
	})

	repo := repository.NewOutreachRepository(pool)
	now := time.Now().UTC().Truncate(time.Millisecond)
	makeAccount := func(sourceID, cnpj string, fresh bool) *models.OutreachAccount {
		account := &models.OutreachAccount{
			OrganizationID: orgID, SourceLeadID: sourceID, CNPJ14: cnpj, CNPJRoot: cnpj[:8],
			RazaoSocial: "Recovery Wakeup Fixture Ltda", QueueState: models.OutreachQueueReadyToGenerate,
			SourceSystem: "extra-cli", SourceRunID: "recovery-wakeup-run", ActivationState: ActivationActionableNow,
			TargetFitSendTier: "A_AUTOMATIC", TargetFitClass: TargetFitConfirmed, TargetFitVersion: "target-fit.v1",
			TargetFitComputedAt: &now, TargetFitSourceWatermark: now.Format(time.RFC3339Nano), TargetFitObservedAt: &now,
			TargetFitFresh: fresh, TargetFitEligible: fresh,
		}
		if _, upsertErr := repo.UpsertAccount(ctx, account); upsertErr != nil {
			t.Fatal(upsertErr)
		}
		return account
	}
	eligible := makeAccount("recovery-wakeup-eligible", "76271049000185", true)
	stale := makeAccount("recovery-wakeup-stale", "37174657000188", false)
	unknown := true
	for _, account := range []*models.OutreachAccount{eligible, stale} {
		candidate := &models.OutreachContactCandidate{
			OrganizationID: orgID, AccountID: account.ID, SourceContactID: "route-" + account.SourceLeadID,
			Email: "contato@recovery-wakeup.example", SourceURL: "https://recovery-wakeup.example/contato",
			SourceDate: &now, VerificationStatus: models.OutreachVerifyCandidateUnverified,
			Confidence: "HIGH", Recommended: true, MailboxPurpose: "GENERIC_CONTACT",
			MailboxPurposeSendBlocked: true, OwnershipStatus: "COMPANY_OWNED",
			DiscoveryJSON: eligibleDisc(t, RouteClassGenericCompany, true, controlledDiscovery{PersonUnknown: &unknown}),
		}
		if _, upsertErr := repo.UpsertCandidate(ctx, candidate); upsertErr != nil {
			t.Fatal(upsertErr)
		}
		if _, insertErr := pool.Exec(ctx, `INSERT INTO outreach_touchpoints
			(id,organization_id,account_id,state,recipient,editorial_retry_at,editorial_reserved_until,editorial_attempts,stop_reason)
			VALUES($1,$2,$3,'ENRICHMENT_PENDING',$4,$5,$6,6,'recovery did not clear ENRICHMENT_PENDING')`,
			uuid.New(), orgID, account.ID, candidate.Email, now.Add(24*time.Hour), now.Add(time.Hour)); insertErr != nil {
			t.Fatal(insertErr)
		}
	}

	svc := NewService(Config{Enabled: true, RequireHumanApproval: true}, repo, nil).(*service)
	svc.WireHumanGate(pool)
	woken, err := svc.wakeEligibleEnrichmentRecovery(ctx, orgID)
	if err != nil {
		t.Fatal(err)
	}
	if woken != 1 {
		t.Fatalf("woken=%d want 1", woken)
	}

	var retryAt time.Time
	var reserved *time.Time
	var attempts int
	var reason string
	if err = pool.QueryRow(ctx, `SELECT editorial_retry_at,editorial_reserved_until,editorial_attempts,stop_reason FROM outreach_touchpoints WHERE account_id=$1`, eligible.ID).Scan(&retryAt, &reserved, &attempts, &reason); err != nil {
		t.Fatal(err)
	}
	if retryAt.After(time.Now().UTC().Add(time.Second)) || reserved != nil || attempts != 6 || reason != "eligibility restored by feed sync; editorial recovery due now" {
		t.Fatalf("eligible wakeup mismatch retry=%s reserved=%v attempts=%d reason=%q", retryAt, reserved, attempts, reason)
	}
	if err = pool.QueryRow(ctx, `SELECT editorial_retry_at,editorial_reserved_until FROM outreach_touchpoints WHERE account_id=$1`, stale.ID).Scan(&retryAt, &reserved); err != nil {
		t.Fatal(err)
	}
	if !retryAt.After(time.Now().UTC().Add(23*time.Hour)) || reserved == nil {
		t.Fatalf("stale blocker was incorrectly woken retry=%s reserved=%v", retryAt, reserved)
	}
}
