package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/warmbly/warmbly/internal/models"
)

func (r *outreachRepository) EnqueueOutcome(ctx context.Context, ev *models.OutreachOutcome) error {
	if ev.ID == uuid.Nil {
		ev.ID = uuid.New()
	}
	if ev.EventID == uuid.Nil {
		ev.EventID = uuid.New()
	}
	now := time.Now().UTC()
	ev.CreatedAt = now
	ev.UpdatedAt = now
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = now
	}
	if ev.NextAttemptAt.IsZero() {
		ev.NextAttemptAt = now
	}
	payload := ev.Payload
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO outreach_outcome_outbox (
			id, organization_id, event_id, idempotency_key, source_lead_id, cnpj14,
			contact_email, event_type, payload, occurred_at, attempts, next_attempt_at,
			created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,0,$11,$12,$12)`,
		ev.ID, ev.OrganizationID, ev.EventID, ev.IdempotencyKey, ev.SourceLeadID, ev.CNPJ14,
		ev.ContactEmail, ev.EventType, payload, ev.OccurredAt, ev.NextAttemptAt, now,
	)
	return err
}

func (r *outreachRepository) ListPendingOutcomes(ctx context.Context, limit int) ([]models.OutreachOutcome, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := r.db.Query(ctx, `
		SELECT id, organization_id, event_id, idempotency_key, COALESCE(source_lead_id,''), COALESCE(cnpj14,''),
			COALESCE(contact_email,''), event_type, payload, occurred_at, attempts, next_attempt_at,
			delivered_at, COALESCE(last_error,''), dead_letter, created_at, updated_at
		FROM outreach_outcome_outbox
		WHERE delivered_at IS NULL AND dead_letter = false AND next_attempt_at <= now()
		ORDER BY next_attempt_at ASC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.OutreachOutcome
	for rows.Next() {
		var ev models.OutreachOutcome
		if err := rows.Scan(
			&ev.ID, &ev.OrganizationID, &ev.EventID, &ev.IdempotencyKey, &ev.SourceLeadID, &ev.CNPJ14,
			&ev.ContactEmail, &ev.EventType, &ev.Payload, &ev.OccurredAt, &ev.Attempts, &ev.NextAttemptAt,
			&ev.DeliveredAt, &ev.LastError, &ev.DeadLetter, &ev.CreatedAt, &ev.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (r *outreachRepository) MarkOutcomeDelivered(ctx context.Context, orgID, id uuid.UUID) error {
	ct, err := r.db.Exec(ctx, `
		UPDATE outreach_outcome_outbox SET delivered_at=now(), updated_at=now(), last_error=''
		WHERE organization_id=$1 AND id=$2`, orgID, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (r *outreachRepository) MarkOutcomeAttempt(ctx context.Context, orgID, id uuid.UUID, attempts int, next time.Time, lastErr string, dead bool) error {
	_, err := r.db.Exec(ctx, `
		UPDATE outreach_outcome_outbox SET
			attempts=$3, next_attempt_at=$4, last_error=$5, dead_letter=$6, updated_at=now()
		WHERE organization_id=$1 AND id=$2`,
		orgID, id, attempts, next, lastErr, dead,
	)
	return err
}

func (r *outreachRepository) GetOutcomeByIdempotency(ctx context.Context, orgID uuid.UUID, key string) (*models.OutreachOutcome, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, organization_id, event_id, idempotency_key, COALESCE(source_lead_id,''), COALESCE(cnpj14,''),
			COALESCE(contact_email,''), event_type, payload, occurred_at, attempts, next_attempt_at,
			delivered_at, COALESCE(last_error,''), dead_letter, created_at, updated_at
		FROM outreach_outcome_outbox WHERE organization_id=$1 AND idempotency_key=$2`, orgID, key)
	var ev models.OutreachOutcome
	err := row.Scan(
		&ev.ID, &ev.OrganizationID, &ev.EventID, &ev.IdempotencyKey, &ev.SourceLeadID, &ev.CNPJ14,
		&ev.ContactEmail, &ev.EventType, &ev.Payload, &ev.OccurredAt, &ev.Attempts, &ev.NextAttemptAt,
		&ev.DeliveredAt, &ev.LastError, &ev.DeadLetter, &ev.CreatedAt, &ev.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ev, nil
}
