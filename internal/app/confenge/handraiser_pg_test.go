package confenge

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

func openHandRaisePG(t *testing.T) (*pgxpool.Pool, uuid.UUID) {
	t.Helper()
	dsn := os.Getenv("WARMBLY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("WARMBLY_TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	userID, orgID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id,first_name,last_name,email) VALUES($1,'Hand','Raise',$2)`,
		userID, fmt.Sprintf("hand-raise-%s@example.test", userID)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (id,name,slug,owner_user_id) VALUES($1,'Hand Raise',$2,$3)`,
		orgID, "hand-raise-"+orgID.String(), userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, orgID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id=$1`, userID)
		pool.Close()
	})
	return pool, orgID
}

func seedHandRaiseAccount(t *testing.T, repo repository.OutreachRepository, orgID uuid.UUID, cnpj string) uuid.UUID {
	t.Helper()
	acc := &models.OutreachAccount{
		ID: uuid.New(), OrganizationID: orgID, CNPJ14: cnpj, RazaoSocial: "EMPRESA " + cnpj,
		QueueState: models.OutreachQueueNeedsContact,
	}
	if _, err := repo.UpsertAccount(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetAccountByCNPJ(context.Background(), orgID, cnpj)
	if err != nil || stored == nil {
		t.Fatalf("seed account %s: %v", cnpj, err)
	}
	return stored.ID
}

// Lane attribution has to survive the database, not just the struct. This is
// the round trip that proves engine_lane is actually persisted and read back.
func TestEngineLaneSurvivesPostgresRoundTrip(t *testing.T) {
	pool, orgID := openHandRaisePG(t)
	repo := repository.NewOutreachRepository(pool)
	ctx := context.Background()
	svc := NewService(Config{Enabled: true}, repo, nil).(*service)
	store := svc.actionStore()
	if store == nil {
		t.Fatal("commercial action store unavailable")
	}

	written := map[string]uuid.UUID{}
	for i, engine := range EngineLanes {
		accountID := seedHandRaiseAccount(t, repo, orgID, fmt.Sprintf("1234567800%04d", i))
		action := ConvergeHandRaise(HandRaise{
			OrganizationID: orgID, AccountID: accountID,
			Signal: SignalPositiveReplyFirstTouch, EngineLane: engine,
			OccurredAt: time.Now().UTC(), PersonName: "Maria Souza",
		})
		if action == nil {
			t.Fatalf("engine %s did not converge", engine)
		}
		if err := store.UpsertCommercialAction(ctx, action); err != nil {
			t.Fatal(err)
		}
		written[engine] = action.ID
	}

	for engine, id := range written {
		got, err := store.GetCommercialAction(ctx, orgID, id)
		if err != nil {
			t.Fatal(err)
		}
		if got == nil {
			t.Fatalf("action for engine %s vanished", engine)
		}
		if got.EngineLane != engine {
			t.Fatalf("engine lane did not survive Postgres: got %q want %q", got.EngineLane, engine)
		}
		// The cockpit lane is a separate column and must be untouched by this.
		if got.Lane == "" || got.Lane == got.EngineLane {
			t.Fatalf("cockpit lane was corrupted by engine attribution: %q", got.Lane)
		}
		if got.NextActionType == "" || got.NextActionAt == nil {
			t.Fatalf("the next action did not survive Postgres: %+v", got)
		}
	}
}

// A row written without engine attribution reads back as unattributed, not as
// some default engine. This is what keeps pre-existing rows honest.
func TestUnattributedActionReadsBackUnattributedFromPostgres(t *testing.T) {
	pool, orgID := openHandRaisePG(t)
	repo := repository.NewOutreachRepository(pool)
	ctx := context.Background()
	svc := NewService(Config{Enabled: true}, repo, nil).(*service)
	store := svc.actionStore()
	if store == nil {
		t.Fatal("commercial action store unavailable")
	}
	accountID := seedHandRaiseAccount(t, repo, orgID, "99999999000199")

	legacy := &models.OutreachCommercialAction{
		OrganizationID: orgID, AccountID: accountID,
		ActionType: models.ActionOtherManual, State: models.ActionStateReady,
		Lane: models.LaneInboundNow, IdempotencyKey: "legacy-no-engine",
	}
	if err := store.UpsertCommercialAction(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetCommercialAction(ctx, orgID, legacy.ID)
	if err != nil || got == nil {
		t.Fatalf("legacy action: %v", err)
	}
	if got.EngineLane != EngineLaneUnattributed {
		t.Fatalf("a row written with no engine read back as %q", got.EngineLane)
	}
	if NormalizeEngineLane(got.EngineLane) != EngineLaneUnattributed {
		t.Fatal("an unattributed row normalized into an engine")
	}
}

// The Founder Interrupt Budget must work against the real store, including
// finding the hand-raiser that has no due date.
func TestInterruptBudgetProjectsFromPostgres(t *testing.T) {
	pool, orgID := openHandRaisePG(t)
	repo := repository.NewOutreachRepository(pool)
	ctx := context.Background()
	svc := NewService(Config{Enabled: true}, repo, nil).(*service)
	store := svc.actionStore()
	if store == nil {
		t.Fatal("commercial action store unavailable")
	}

	uncommitted := ConvergeHandRaise(HandRaise{
		OrganizationID: orgID, AccountID: seedHandRaiseAccount(t, repo, orgID, "11111111000191"),
		Signal: SignalIntelSeedResponse, EngineLane: EngineLaneIntelSeed,
		OccurredAt: time.Now().UTC().Add(-48 * time.Hour),
	})
	uncommitted.NextActionType, uncommitted.NextActionAt = "", nil
	if err := store.UpsertCommercialAction(ctx, uncommitted); err != nil {
		t.Fatal(err)
	}
	meeting := ConvergeHandRaise(HandRaise{
		OrganizationID: orgID, AccountID: seedHandRaiseAccount(t, repo, orgID, "22222222000192"),
		Signal: SignalMeetingOrProposalRequest, EngineLane: EngineLaneConfengeWeb,
		OccurredAt: time.Now().UTC(),
	})
	if err := store.UpsertCommercialAction(ctx, meeting); err != nil {
		t.Fatal(err)
	}

	budget, xerr := svc.FounderInterruptBudget(ctx, orgID, 50)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if budget.Total < 2 {
		t.Fatalf("the projection missed rows: %+v", budget)
	}
	if budget.Items[0].Bucket != BucketNoNextAction {
		t.Fatalf("the uncommitted hand-raiser was not surfaced first: %q", budget.Items[0].Bucket)
	}
	if budget.Items[0].EngineLane != EngineLaneIntelSeed {
		t.Fatalf("lane attribution was lost through Postgres: %q", budget.Items[0].EngineLane)
	}
	if budget.ByEngine[EngineLaneConfengeWeb] != 1 || budget.ByEngine[EngineLaneIntelSeed] != 1 {
		t.Fatalf("engines were merged in the projection: %+v", budget.ByEngine)
	}
}
