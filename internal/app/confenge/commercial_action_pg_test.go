package confenge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// Import writes reachability onto candidates; CollectToday re-plans from
// ListCandidates (the real PG scan). R2 must stay inferred, never VALIDATED.
func TestPostgresR2InferredEmailSurvivesCandidateScan(t *testing.T) {
	dsn := os.Getenv("WARMBLY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("WARMBLY_TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	userID, orgID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, first_name, last_name, email) VALUES ($1,'R2','Scan',$2)`, userID, "r2-scan-"+userID.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (id, name, slug, owner_user_id) VALUES ($1,'R2 Scan',$2,$3)`, orgID, "r2-scan-"+orgID.String(), userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, orgID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id=$1`, userID)
	})

	raw, err := os.ReadFile(filepath.Join("testdata", "reachability_r0_r5_v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	repo := repository.NewOutreachRepository(pool)
	svc := NewService(Config{Enabled: true, RequireHumanApproval: true, MaxFeedPayloadBytes: DefaultMaxPayloadBytes}, repo, nil).(*service)
	if _, xerr := svc.ImportFromBytes(ctx, orgID, &userID, raw, ImportOptions{IdempotencyKey: "r2-pg-scan"}); xerr != nil {
		t.Fatal(xerr)
	}
	acc, err := repo.GetAccountByCNPJ(ctx, orgID, "22222000000182")
	if err != nil || acc == nil {
		t.Fatalf("r2 account: %v", err)
	}
	cands, err := repo.ListCandidates(ctx, orgID, acc.ID)
	if err != nil || len(cands) == 0 {
		t.Fatalf("list candidates: n=%d err=%v", len(cands), err)
	}
	if cands[0].ReachabilityClass != "R2_HIGH_CONFIDENCE_DIRECT" {
		t.Fatalf("PG scan lost R2 class: %+v", cands[0])
	}
	planned := PlanCommercialAction(PlanInput{
		Account: acc, Candidate: &cands[0], Candidates: cands, Now: time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC),
	})
	if planned.Action.ActionType != models.ActionInferredEmailReview || planned.Action.Lane != models.LaneHumanReviewEmail {
		t.Fatalf("replan after scan: %+v", planned.Action)
	}
	if planned.Action.EmailSendable || planned.Action.Dispatchable || planned.RecipientState == RecipientValidated {
		t.Fatalf("R2 after PG reload must not be VALIDATED/sendable: %+v rec=%s", planned.Action, planned.RecipientState)
	}
}

func postgresTestDSN() string {
	if dsn := os.Getenv("WARMBLY_TEST_POSTGRES_DSN"); dsn != "" {
		return dsn
	}
	return strings.TrimSpace(os.Getenv("PRIMARY_DB"))
}

func TestPostgresQualidadePersonIDSurvivesReload(t *testing.T) {
	dsn := postgresTestDSN()
	if dsn == "" {
		t.Skip("WARMBLY_TEST_POSTGRES_DSN/PRIMARY_DB is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE outreach_contact_candidates ADD COLUMN IF NOT EXISTS person_id text NOT NULL DEFAULT ''`); err != nil {
		t.Fatalf("candidate person_id column: %v", err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE outreach_commercial_actions ADD COLUMN IF NOT EXISTS person_id text NOT NULL DEFAULT ''`); err != nil {
		t.Fatalf("action person_id column: %v", err)
	}
	userID, orgID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, first_name, last_name, email) VALUES ($1,'Qualidade','Person',$2)`, userID, "qualidade-"+userID.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (id, name, slug, owner_user_id) VALUES ($1,'Qualidade Person',$2,$3)`, orgID, "qualidade-person-"+orgID.String(), userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, orgID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id=$1`, userID)
	})

	raw, err := os.ReadFile(filepath.Join("testdata", "track_a_operator_projection_v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	repo := repository.NewOutreachRepository(pool)
	svc := NewService(Config{Enabled: true, RequireHumanApproval: true, MaxFeedPayloadBytes: DefaultMaxPayloadBytes}, repo, nil).(*service)
	if _, xerr := svc.ImportFromBytes(ctx, orgID, &userID, raw, ImportOptions{IdempotencyKey: "qualidade-person-pg"}); xerr != nil {
		t.Fatal(xerr)
	}
	acc, err := repo.GetAccountByCNPJ(ctx, orgID, "00820854000114")
	if err != nil || acc == nil {
		t.Fatalf("qualidade account: %v", err)
	}
	cands, err := repo.ListCandidates(ctx, orgID, acc.ID)
	if err != nil || len(cands) == 0 {
		t.Fatalf("list candidates: n=%d err=%v", len(cands), err)
	}
	const wantPerson = "17adca65031d71b21ebaac4c"
	const wantCandidate = "9f4676aa79d0cd5f5fedd7a7"
	if cands[0].PersonID != wantPerson {
		t.Fatalf("PG reload person_id=%q want extra-cli %q (source_contact_id=%q)", cands[0].PersonID, wantPerson, cands[0].SourceContactID)
	}
	if cands[0].SourceContactID != wantCandidate {
		t.Fatalf("PG reload source_contact_id=%q want candidate_id %q", cands[0].SourceContactID, wantCandidate)
	}
	if cands[0].PersonID == cands[0].SourceContactID {
		t.Fatal("PG stored person_id as source_contact_id")
	}
	planned := PlanCommercialAction(PlanInput{
		Account: acc, Candidate: &cands[0], Candidates: cands,
		Now: time.Date(2026, 8, 14, 16, 0, 0, 0, time.UTC),
	})
	if planned.Action.PersonID != wantPerson {
		t.Fatalf("replan after PG scan dropped person_id: %q", planned.Action.PersonID)
	}
	if planned.Action.EmailSendable || planned.Action.Dispatchable {
		t.Fatalf("routed call after PG reload must not send: %+v", planned.Action)
	}
	svc.persistPlannedAction(ctx, planned)
	st := svc.actionStore()
	if st == nil {
		t.Fatal("commercial action store unavailable")
	}
	open, err := st.ListCommercialActions(ctx, orgID, acc.ID, true, 20)
	if err != nil || len(open) == 0 {
		t.Fatalf("list actions: n=%d err=%v", len(open), err)
	}
	if open[0].PersonID != wantPerson {
		t.Fatalf("persisted action person_id=%q want %q", open[0].PersonID, wantPerson)
	}
	card := AssembleActionCard(open[0])
	if card.PersonID != wantPerson {
		t.Fatalf("card person_id=%q want %q", card.PersonID, wantPerson)
	}
}
