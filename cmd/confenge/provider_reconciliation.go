package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/app/confenge"
	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

const providerAcceptedRecoveryConfirmation = "PROVIDER_ACCEPTED_LOCAL_COMPLETION_FAILED"

type providerAcceptedRecoveryTarget struct {
	OrganizationID    uuid.UUID
	CampaignID        uuid.UUID
	ContactID         uuid.UUID
	SequenceID        uuid.UUID
	MailboxID         uuid.UUID
	CampaignName      string
	TaskStatus        string
	MailboxProvider   string
	ReservationID     uuid.UUID
	ReservationState  string
	ReservationKey    string
	ReservationAt     time.Time
	QueueID           uuid.UUID
	QueueKey          string
	QueueStatus       string
	ExistingSendID    *uuid.UUID
	ExistingSendAt    *time.Time
	ProgressSentAt    *time.Time
	TouchpointAccount uuid.UUID
}

func cmdReconcileProviderAccepted(args []string) int {
	maybeLoadDotEnv()
	fs := flag.NewFlagSet("reconcile-provider-accepted", flag.ContinueOnError)
	taskRaw := fs.String("task-id", "", "original campaign task UUID")
	touchpointRaw := fs.String("touchpoint-id", "", "original outreach touchpoint UUID")
	recipient := fs.String("recipient", "", "exact original recipient")
	providerMessageID := fs.String("provider-message-id", "", "provider-confirmed Message-ID")
	acceptedRaw := fs.String("accepted-at", "", "provider acceptance time in RFC3339")
	actorRaw := fs.String("actor", "", "organization member performing reconciliation")
	dryRun := fs.Bool("dry-run", false, "validate and report without writing")
	confirm := fs.String("confirm", "", "required recovery confirmation")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if (*dryRun && strings.TrimSpace(*confirm) != "") || (!*dryRun && *confirm != providerAcceptedRecoveryConfirmation) {
		fmt.Fprintf(os.Stderr, "choose --dry-run or --confirm %s\n", providerAcceptedRecoveryConfirmation)
		return 2
	}

	taskID, taskErr := uuid.Parse(strings.TrimSpace(*taskRaw))
	touchpointID, touchpointErr := uuid.Parse(strings.TrimSpace(*touchpointRaw))
	actorID, actorErr := uuid.Parse(strings.TrimSpace(*actorRaw))
	acceptedAt, acceptedErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(*acceptedRaw))
	recipientValue := strings.TrimSpace(strings.ToLower(*recipient))
	providerIDValue := strings.TrimSpace(*providerMessageID)
	if taskErr != nil || touchpointErr != nil || actorErr != nil || acceptedErr != nil || recipientValue == "" || providerIDValue == "" {
		fmt.Fprintln(os.Stderr, "valid --task-id, --touchpoint-id, --recipient, --provider-message-id, --accepted-at, and --actor are required")
		return 2
	}
	if strings.ContainsAny(providerIDValue, "\r\n\t") {
		fmt.Fprintln(os.Stderr, "provider message id contains invalid whitespace")
		return 2
	}
	acceptedAt = acceptedAt.UTC()
	if acceptedAt.After(time.Now().UTC().Add(5 * time.Minute)) {
		fmt.Fprintln(os.Stderr, "provider acceptance time is in the future")
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, primaryDSN())
	if err != nil {
		fmt.Fprintf(os.Stderr, "provider reconciliation db: %v\n", err)
		return 1
	}
	defer pool.Close()

	target, touchpoint, err := loadProviderAcceptedRecoveryTarget(ctx, pool, taskID, touchpointID, recipientValue, providerIDValue, acceptedAt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "provider reconciliation refused: %v\n", err)
		return 1
	}
	var actorMember bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM organization_members
			WHERE organization_id=$1 AND user_id=$2 AND accepted_at IS NOT NULL
		)`, target.OrganizationID, actorID).Scan(&actorMember); err != nil || !actorMember {
		fmt.Fprintln(os.Stderr, "provider reconciliation refused: actor is not an accepted organization member")
		return 1
	}

	status := "WOULD_RECONCILE"
	if target.ExistingSendID != nil && touchpoint.State == models.TouchpointSent &&
		normalizeProviderMessageID(touchpoint.ProviderMessageID) == normalizeProviderMessageID(providerIDValue) {
		status = "ALREADY_RECONCILED"
	}
	if *dryRun {
		printJSON(providerAcceptedRecoveryOutput(status, taskID, touchpointID, recipientValue, providerIDValue, acceptedAt, target, touchpoint))
		return 0
	}

	cfg := confenge.LoadConfig()
	if !cfg.Enabled {
		fmt.Fprintln(os.Stderr, "provider reconciliation refused: CONFENGE outreach is disabled")
		return 1
	}
	repo := repository.NewOutreachRepository(pool)
	svc := confenge.NewService(cfg, repo, nil)
	svc.WireDispatch(pool)
	svc.WireCohortAuth(confenge.NewPostgresCohortStore(pool))
	svc.WireDelegatedFirstTouch(pool)
	svc.WireIntel(pool)
	if err := svc.CompleteCampaignEmail(
		ctx,
		target.OrganizationID,
		target.CampaignID,
		target.ContactID,
		target.SequenceID,
		taskID,
		target.MailboxID,
		providerIDValue,
		target.MailboxProvider,
		acceptedAt,
	); err != nil {
		_ = writeProviderRecoveryAudit(ctx, pool, actorID, taskID, touchpointID, recipientValue, providerIDValue, acceptedAt, target, "FAILED", err.Error())
		fmt.Fprintf(os.Stderr, "provider reconciliation failed: %v\n", err)
		return 1
	}
	if _, err := svc.ReconcileAttemptedDispatches(ctx); err != nil {
		_ = writeProviderRecoveryAudit(ctx, pool, actorID, taskID, touchpointID, recipientValue, providerIDValue, acceptedAt, target, "PARTIAL", err.Error())
		fmt.Fprintf(os.Stderr, "provider reconciliation dispatch queue: %v\n", err)
		return 1
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO campaign_contact_progress (campaign_id, contact_id, sequence_id, sent_at)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (campaign_id, contact_id, sequence_id)
		DO UPDATE SET sent_at=EXCLUDED.sent_at`,
		target.CampaignID, target.ContactID, target.SequenceID, acceptedAt); err != nil {
		_ = writeProviderRecoveryAudit(ctx, pool, actorID, taskID, touchpointID, recipientValue, providerIDValue, acceptedAt, target, "PARTIAL", err.Error())
		fmt.Fprintf(os.Stderr, "provider reconciliation campaign progress: %v\n", err)
		return 1
	}
	target, touchpoint, err = loadProviderAcceptedRecoveryTarget(ctx, pool, taskID, touchpointID, recipientValue, providerIDValue, acceptedAt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "provider reconciliation readback: %v\n", err)
		return 1
	}
	if target.QueueStatus != dispatch.QueueSent {
		governor := dispatch.NewGovernor(dispatch.LoadConfig(), dispatch.NewPGStore(pool), nil)
		if err := governor.MarkQueue(ctx, target.QueueID, dispatch.QueueSent, ""); err != nil {
			_ = writeProviderRecoveryAudit(ctx, pool, actorID, taskID, touchpointID, recipientValue, providerIDValue, acceptedAt, target, "PARTIAL", err.Error())
			fmt.Fprintf(os.Stderr, "provider reconciliation exact dispatch queue: %v\n", err)
			return 1
		}
		target, touchpoint, err = loadProviderAcceptedRecoveryTarget(ctx, pool, taskID, touchpointID, recipientValue, providerIDValue, acceptedAt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "provider reconciliation final readback: %v\n", err)
			return 1
		}
	}
	if target.QueueStatus != dispatch.QueueSent || target.ExistingSendID == nil || touchpoint.State != models.TouchpointSent ||
		normalizeProviderMessageID(touchpoint.ProviderMessageID) != normalizeProviderMessageID(providerIDValue) {
		err := fmt.Errorf("durable recovery readback is incomplete")
		_ = writeProviderRecoveryAudit(ctx, pool, actorID, taskID, touchpointID, recipientValue, providerIDValue, acceptedAt, target, "PARTIAL", err.Error())
		fmt.Fprintf(os.Stderr, "provider reconciliation readback: %v\n", err)
		return 1
	}
	if err := writeProviderRecoveryAudit(ctx, pool, actorID, taskID, touchpointID, recipientValue, providerIDValue, acceptedAt, target, "COMPLETED", ""); err != nil {
		fmt.Fprintf(os.Stderr, "provider reconciliation audit: %v\n", err)
		return 1
	}
	printJSON(providerAcceptedRecoveryOutput("RECONCILED", taskID, touchpointID, recipientValue, providerIDValue, acceptedAt, target, touchpoint))
	return 0
}

func loadProviderAcceptedRecoveryTarget(ctx context.Context, pool *pgxpool.Pool, taskID, touchpointID uuid.UUID, recipient, providerMessageID string, acceptedAt time.Time) (providerAcceptedRecoveryTarget, *models.OutreachTouchpoint, error) {
	target := providerAcceptedRecoveryTarget{}
	var mailboxProvider *string
	err := pool.QueryRow(ctx, `
		SELECT c.organization_id, ct.campaign_id, ct.contact_id, ct.sequence_id,
		       t.email_account_id, c.name, t.status::text, e.provider::text
		FROM tasks t
		JOIN campaign_tasks ct ON ct.task_id=t.id
		JOIN campaigns c ON c.id=ct.campaign_id
		LEFT JOIN email_accounts e ON e.id=t.email_account_id
		WHERE t.id=$1`, taskID).Scan(
		&target.OrganizationID, &target.CampaignID, &target.ContactID, &target.SequenceID,
		&target.MailboxID, &target.CampaignName, &target.TaskStatus, &mailboxProvider,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return target, nil, fmt.Errorf("campaign task not found")
	}
	if err != nil {
		return target, nil, err
	}
	target.MailboxProvider = providerAcceptedMailboxProvider(mailboxProvider)
	if !confenge.IsConfengeCampaign(target.CampaignName) {
		return target, nil, fmt.Errorf("task is not bound to the canonical CONFENGE campaign")
	}
	if target.TaskStatus != "completed" {
		return target, nil, fmt.Errorf("task status is %s, expected completed", target.TaskStatus)
	}

	repo := repository.NewOutreachRepository(pool)
	touchpoint, err := repo.GetTouchpointByEnrollment(ctx, target.OrganizationID, target.CampaignID, target.ContactID)
	if err != nil || touchpoint == nil {
		return target, nil, fmt.Errorf("enrolled touchpoint not found")
	}
	if touchpoint.ID != touchpointID {
		return target, nil, fmt.Errorf("touchpoint does not match the task enrollment")
	}
	if !strings.EqualFold(strings.TrimSpace(touchpoint.Recipient), recipient) {
		return target, nil, fmt.Errorf("recipient does not match the enrolled touchpoint")
	}
	if touchpoint.State != models.TouchpointQueued && touchpoint.State != models.TouchpointApproved && touchpoint.State != models.TouchpointSent {
		return target, nil, fmt.Errorf("touchpoint state %s is not recoverable by this command", touchpoint.State)
	}
	if touchpoint.State == models.TouchpointSent && normalizeProviderMessageID(touchpoint.ProviderMessageID) != normalizeProviderMessageID(providerMessageID) {
		return target, nil, fmt.Errorf("touchpoint has a different provider message id")
	}
	target.TouchpointAccount = touchpoint.AccountID

	var reservationCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM confenge_dispatch_reservations WHERE task_id=$1`, taskID).Scan(&reservationCount); err != nil {
		return target, nil, err
	}
	if reservationCount != 1 {
		return target, nil, fmt.Errorf("expected one task reservation, found %d", reservationCount)
	}
	var reservationTaskID, reservationMailboxID uuid.UUID
	err = pool.QueryRow(ctx, `
		SELECT id, state, message_key, task_id, email_account_id, reserved_at
		FROM confenge_dispatch_reservations WHERE task_id=$1`, taskID).Scan(
		&target.ReservationID, &target.ReservationState, &target.ReservationKey,
		&reservationTaskID, &reservationMailboxID, &target.ReservationAt,
	)
	if err != nil {
		return target, nil, err
	}
	expectedKey := confenge.MessageKeyCampaignEmail(target.CampaignID, target.ContactID, target.SequenceID)
	if reservationTaskID != taskID || reservationMailboxID != target.MailboxID || target.ReservationKey != expectedKey {
		return target, nil, fmt.Errorf("reservation binding does not match the campaign task")
	}
	if acceptedAt.Before(target.ReservationAt.Add(-time.Minute)) {
		return target, nil, fmt.Errorf("provider acceptance predates the reservation")
	}
	if touchpoint.DraftID == nil {
		return target, nil, fmt.Errorf("touchpoint has no draft binding")
	}
	var queueCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM confenge_dispatch_queue
		WHERE organization_id=$1 AND draft_id=$2`, target.OrganizationID, *touchpoint.DraftID).Scan(&queueCount); err != nil {
		return target, nil, err
	}
	if queueCount != 1 {
		return target, nil, fmt.Errorf("expected one delegated dispatch queue row, found %d", queueCount)
	}
	if err := pool.QueryRow(ctx, `
		SELECT id, message_key, status FROM confenge_dispatch_queue
		WHERE organization_id=$1 AND draft_id=$2`, target.OrganizationID, *touchpoint.DraftID).Scan(
		&target.QueueID, &target.QueueKey, &target.QueueStatus,
	); err != nil {
		return target, nil, fmt.Errorf("dispatch queue binding: %w", err)
	}
	if target.QueueStatus != dispatch.QueueAttempted && target.QueueStatus != dispatch.QueueReserved && target.QueueStatus != dispatch.QueueSent {
		return target, nil, fmt.Errorf("queue status %s is not an accepted hand-off", target.QueueStatus)
	}

	var conflictingTouchpoint uuid.UUID
	err = pool.QueryRow(ctx, `
		SELECT id FROM outreach_touchpoints
		WHERE id<>$1 AND lower(trim(both '<>' from provider_message_id))=lower($2)
		LIMIT 1`, touchpointID, normalizeProviderMessageID(providerMessageID)).Scan(&conflictingTouchpoint)
	if err == nil {
		return target, nil, fmt.Errorf("provider message id is already bound to another touchpoint")
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return target, nil, err
	}

	var existingTaskID *uuid.UUID
	var sendID uuid.UUID
	var sendAt time.Time
	err = pool.QueryRow(ctx, `SELECT id, task_id, sent_at FROM confenge_dispatch_sends WHERE message_key=$1`, expectedKey).Scan(&sendID, &existingTaskID, &sendAt)
	if err == nil {
		if existingTaskID == nil || *existingTaskID != taskID {
			return target, nil, fmt.Errorf("durable send is bound to a different task")
		}
		target.ExistingSendID = &sendID
		target.ExistingSendAt = &sendAt
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return target, nil, err
	}
	_ = pool.QueryRow(ctx, `
		SELECT sent_at FROM campaign_contact_progress
		WHERE campaign_id=$1 AND contact_id=$2 AND sequence_id=$3`,
		target.CampaignID, target.ContactID, target.SequenceID).Scan(&target.ProgressSentAt)
	return target, touchpoint, nil
}

func providerAcceptedMailboxProvider(provider *string) string {
	if provider == nil || strings.TrimSpace(*provider) == "" {
		return "smtp"
	}
	return strings.TrimSpace(*provider)
}

func writeProviderRecoveryAudit(ctx context.Context, pool *pgxpool.Pool, actorID, taskID, touchpointID uuid.UUID, recipient, providerMessageID string, acceptedAt time.Time, target providerAcceptedRecoveryTarget, outcome, failure string) error {
	return repository.NewAuditRepository(pool).Log(ctx, &models.CreateAuditLog{
		OrgID: target.OrganizationID, UserID: actorID, Action: models.AuditActionUpdate,
		EntityType: models.AuditEntityOutreachAccount, EntityID: &target.TouchpointAccount,
		Changes: map[string]string{
			"action":        "provider_acceptance_reconciled",
			"touchpoint_id": touchpointID.String(),
			"outcome":       outcome,
		},
		Metadata: map[string]string{
			"task_id": taskID.String(), "recipient": recipient,
			"provider_message_id": providerMessageID, "provider": target.MailboxProvider,
			"accepted_at": acceptedAt.Format(time.RFC3339Nano), "reservation_id": target.ReservationID.String(),
			"reason": "provider_accepted_local_completion_failed", "transport_invoked": "false", "failure": failure,
		},
	})
}

func providerAcceptedRecoveryOutput(status string, taskID, touchpointID uuid.UUID, recipient, providerMessageID string, acceptedAt time.Time, target providerAcceptedRecoveryTarget, touchpoint *models.OutreachTouchpoint) map[string]any {
	return map[string]any{
		"status": status, "transport_invoked": false,
		"task_id": taskID, "task_status": target.TaskStatus,
		"touchpoint_id": touchpointID, "touchpoint_state": touchpoint.State,
		"recipient": recipient, "provider": target.MailboxProvider,
		"provider_message_id": providerMessageID, "accepted_at": acceptedAt,
		"reservation_id": target.ReservationID, "reservation_state": target.ReservationState,
		"queue_id": target.QueueID, "queue_key": target.QueueKey, "queue_status": target.QueueStatus,
		"local_send_id": target.ExistingSendID,
		"local_send_at": target.ExistingSendAt, "campaign_progress_sent_at": target.ProgressSentAt,
	}
}

func normalizeProviderMessageID(value string) string {
	return strings.Trim(strings.TrimSpace(value), "<>")
}
