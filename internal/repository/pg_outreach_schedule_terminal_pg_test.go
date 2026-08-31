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

func TestCASScheduleTouchpointNeverReopensTerminalInitialQueue(t *testing.T) {
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
	if _, err = pool.Exec(ctx, `INSERT INTO users (id,first_name,last_name,email) VALUES($1,'Terminal','Schedule',$2)`, userID, fmt.Sprintf("terminal-schedule-%s@example.test", userID)); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO organizations (id,name,slug,owner_user_id) VALUES($1,'Terminal Schedule',$2,$3)`, orgID, "terminal-schedule-"+orgID.String(), userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, orgID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id=$1`, userID)
	})

	for _, terminalStatus := range []string{"attempted", "failed", "sent"} {
		t.Run(terminalStatus, func(t *testing.T) {
			accountID, oldDraftID, newDraftID, touchpointID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			oldRecipient := terminalStatus + "-old@fornecedor.example"
			newRecipient := terminalStatus + "-new@fornecedor.example"
			messageKey := "email:first-touch:account:" + accountID.String()
			if _, err = pool.Exec(ctx, `INSERT INTO outreach_accounts
				(id,organization_id,source_lead_id,cnpj14,razao_social,queue_state,source_system)
				VALUES($1,$2,$3,$4,'Fornecedor Terminal Ltda','APPROVED','extra-cli')`,
				accountID, orgID, "terminal-"+terminalStatus, fmt.Sprintf("%014d", 52000000000000+len(terminalStatus))); err != nil {
				t.Fatal(err)
			}
			for _, draft := range []struct {
				id        uuid.UUID
				recipient string
			}{{oldDraftID, oldRecipient}, {newDraftID, newRecipient}} {
				if _, err = pool.Exec(ctx, `INSERT INTO outreach_drafts
					(id,organization_id,account_id,recipient_email,subject,body_text,status)
					VALUES($1,$2,$3,$4,'Assunto','Mensagem','APPROVED')`,
					draft.id, orgID, accountID, draft.recipient); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = pool.Exec(ctx, `INSERT INTO outreach_touchpoints
				(id,organization_id,account_id,ordinal,purpose,channel,state,draft_id,recipient,subject,body_text,
				 content_hash,approved_content_hash,approved_by,approved_at)
				VALUES($1,$2,$3,1,'INITIAL','EMAIL','APPROVED',$4,$5,'Assunto','Mensagem','hash','hash',$6,now())`,
				touchpointID, orgID, accountID, newDraftID, newRecipient, userID); err != nil {
				t.Fatal(err)
			}
			if _, err = pool.Exec(ctx, `INSERT INTO confenge_dispatch_queue
				(organization_id,channel,draft_id,message_key,recipient_ref,status,last_error)
				VALUES($1,'EMAIL',$2,$3,$4,$5,'terminal evidence')`,
				orgID, oldDraftID, messageKey, oldRecipient, terminalStatus); err != nil {
				t.Fatal(err)
			}

			repo := NewOutreachRepository(pool)
			queued, scheduleErr := repo.CASScheduleTouchpoint(ctx, orgID, touchpointID, "hash", messageKey, time.Now().UTC().Add(time.Hour))
			if scheduleErr != nil {
				t.Fatal(scheduleErr)
			}
			if queued != nil {
				t.Fatalf("terminal queue was reported scheduled: %+v", queued)
			}
			var gotStatus, gotRecipient, touchState string
			var gotDraft uuid.UUID
			if err = pool.QueryRow(ctx, `SELECT status,draft_id,recipient_ref FROM confenge_dispatch_queue WHERE message_key=$1`, messageKey).
				Scan(&gotStatus, &gotDraft, &gotRecipient); err != nil {
				t.Fatal(err)
			}
			if err = pool.QueryRow(ctx, `SELECT state FROM outreach_touchpoints WHERE id=$1`, touchpointID).Scan(&touchState); err != nil {
				t.Fatal(err)
			}
			if gotStatus != terminalStatus || gotDraft != oldDraftID || gotRecipient != oldRecipient || touchState != "APPROVED" {
				t.Fatalf("terminal binding changed: status=%s draft=%s recipient=%s touchpoint=%s", gotStatus, gotDraft, gotRecipient, touchState)
			}
		})
	}
}
