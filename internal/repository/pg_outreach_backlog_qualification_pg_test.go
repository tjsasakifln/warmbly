package repository

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A feed sync must never dequeue work for a company that is still commercially
// qualified. Only a member that actually lost qualification is retired.
func TestMaterializeInitialBacklogRetiresOnlyDequalifiedAccounts(t *testing.T) {
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
	defer pool.Close()

	userID, orgID := uuid.New(), uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO users (id,first_name,last_name,email) VALUES($1,'Backlog','Qualification',$2)`, userID, fmt.Sprintf("backlog-qual-%s@example.test", userID)); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO organizations (id,name,slug,owner_user_id) VALUES($1,'Backlog Qualification',$2,$3)`, orgID, "backlog-qual-"+orgID.String(), userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, orgID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id=$1`, userID)
	})

	type member struct {
		state        string
		accountID    uuid.UUID
		draftID      uuid.UUID
		touchpointID uuid.UUID
	}
	members := []*member{{state: "QUALIFIED"}, {state: "EXPIRED"}, {state: "REVOKED"}, {state: "UNKNOWN"}}
	for i, m := range members {
		m.accountID, m.draftID, m.touchpointID = uuid.New(), uuid.New(), uuid.New()
		if _, err = pool.Exec(ctx, `INSERT INTO outreach_accounts
			(id,organization_id,source_lead_id,cnpj14,razao_social,queue_state,source_system,source_run_id,commercial_qualification_state)
			VALUES($1,$2,$3,$4,'Fornecedor Backlog Ltda','APPROVED','extra-cli','run-previous',$5)`,
			m.accountID, orgID, fmt.Sprintf("backlog-qual-%d", i), fmt.Sprintf("%014d", 51000000000000+i), m.state); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO outreach_drafts
			(id,organization_id,account_id,recipient_email,subject,body_text,status)
			VALUES($1,$2,$3,$4,'Assunto','Mensagem','APPROVED')`,
			m.draftID, orgID, m.accountID, fmt.Sprintf("contato%d@fornecedor-backlog.example", i)); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO outreach_touchpoints
			(id,organization_id,account_id,ordinal,purpose,channel,state,draft_id,source_run_id,recipient,subject,body_text,content_hash)
			VALUES($1,$2,$3,1,'INITIAL','EMAIL','APPROVED',$4,'run-previous',$5,'Assunto','Mensagem','hash')`,
			m.touchpointID, orgID, m.accountID, m.draftID, fmt.Sprintf("contato%d@fornecedor-backlog.example", i)); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO confenge_dispatch_queue
			(organization_id,channel,draft_id,message_key,recipient_ref,status)
			VALUES($1,'EMAIL',$2,$3,$4,'queued')`,
			orgID, m.draftID, "backlog-qual-"+m.draftID.String(), fmt.Sprintf("contato%d@fornecedor-backlog.example", i)); err != nil {
			t.Fatal(err)
		}
	}

	repo := NewOutreachRepository(pool)
	counts, err := repo.MaterializeCurrentInitialBacklog(ctx, orgID, "run-current")
	if err != nil {
		t.Fatal(err)
	}
	if counts.StaleRetired != 3 {
		t.Fatalf("stale_retired=%d, want the three de-qualified members only", counts.StaleRetired)
	}
	for _, m := range members {
		var touchpointState, draftStatus, queueStatus string
		if err = pool.QueryRow(ctx, `SELECT state FROM outreach_touchpoints WHERE id=$1`, m.touchpointID).Scan(&touchpointState); err != nil {
			t.Fatal(err)
		}
		if err = pool.QueryRow(ctx, `SELECT status FROM outreach_drafts WHERE id=$1`, m.draftID).Scan(&draftStatus); err != nil {
			t.Fatal(err)
		}
		if err = pool.QueryRow(ctx, `SELECT status FROM confenge_dispatch_queue WHERE draft_id=$1`, m.draftID).Scan(&queueStatus); err != nil {
			t.Fatal(err)
		}
		if m.state == "QUALIFIED" {
			if touchpointState != "APPROVED" || draftStatus != "APPROVED" || queueStatus != "queued" {
				t.Fatalf("qualified member lost proven work: touchpoint=%s draft=%s queue=%s", touchpointState, draftStatus, queueStatus)
			}
			continue
		}
		if touchpointState != "CANCELLED" || draftStatus != "BLOCKED" || queueStatus != "cancelled" {
			t.Fatalf("%s member survived retirement: touchpoint=%s draft=%s queue=%s", m.state, touchpointState, draftStatus, queueStatus)
		}
	}
}
