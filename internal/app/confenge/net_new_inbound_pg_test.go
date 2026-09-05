package confenge

import (
	"context"
	"testing"
	"time"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

func TestPGNetNewInboundHandraiserPersistReadback(t *testing.T) {
	dsn := postgresTestDSN()
	if dsn == "" {
		t.Skip("WARMBLY_TEST_POSTGRES_DSN/PRIMARY_DB is not set")
	}
	pool, orgID := openInboundOnlyPG(t)
	t.Cleanup(pool.Close)
	repo := repository.NewOutreachRepository(pool)
	svc := NewService(Config{Enabled: true, RequireHumanApproval: true, AutoSendEnabled: false}, repo, nil).(*service)
	svc.rev02Pin = rev02TestOnlyPin()
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	body := marshalNetNew(t, validNetNewMap("nnhr-pg-1"))
	res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), orgID, body, now)
	if xerr != nil {
		t.Fatalf("pg ingest: %v", xerr)
	}
	if res.Outcome != NetNewInboundOutcomeAccepted || res.ActionID == nil {
		t.Fatalf("pg ingest: %+v", res)
	}
	rb, xerr := svc.ReadbackNetNewInboundHandraiser(context.Background(), orgID, "nnhr-pg-1")
	if xerr != nil {
		t.Fatal(xerr)
	}
	if rb.Receipt != res.Receipt || rb.Outcome != NetNewInboundOutcomeAccepted {
		t.Fatalf("pg readback: %+v", rb)
	}
	acc, err := repo.GetAccount(context.Background(), orgID, *res.AccountID)
	if err != nil || acc == nil {
		t.Fatalf("account: %v", err)
	}
	if !models.AccountIsInboundOnly(acc) || FirstTouchEligibleAccount(acc) {
		t.Fatal("pg net-new is outbound-eligible")
	}
	replay, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), orgID, body, now.Add(time.Minute))
	if xerr != nil {
		t.Fatal(xerr)
	}
	if !replay.Replay || *replay.ActionID != *res.ActionID {
		t.Fatalf("pg replay: %+v", replay)
	}
}
