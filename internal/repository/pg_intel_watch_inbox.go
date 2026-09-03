package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/models"
)

// IntelWatchInboxRepository owns the durable opportunity-event inbox.
//
// The inbox is append-only inside a bounded replay window. Nothing here marks a
// row consumed: a consumer that fails right after an event is handed to it must
// still find that event on the next pass, and the delivery ledger already
// refuses the duplicate that re-emission would otherwise cause.
type IntelWatchInboxRepository interface {
	// AppendOpportunityEvent stores one envelope. It reports whether this call
	// is what created the row; a repeat post of the same event id is a replay,
	// not an error and not a second row.
	AppendOpportunityEvent(ctx context.Context, event models.IntelWatchInboxEvent) (bool, error)
	// ClaimReplayableEvents returns the events still inside the replay window
	// whose emit lease is free, taking that lease. orgID uuid.Nil means every
	// organization. The lease bounds duplicate work between producer instances;
	// it is not what makes delivery safe.
	ClaimReplayableEvents(ctx context.Context, orgID uuid.UUID, now time.Time, window, lease time.Duration, limit int) ([]models.IntelWatchInboxEvent, error)
	// GetOpportunityEvent reads one stored envelope back.
	GetOpportunityEvent(ctx context.Context, orgID uuid.UUID, eventID string) (*models.IntelWatchInboxEvent, error)
}

type intelWatchInboxRepository struct {
	db *pgxpool.Pool
}

func NewIntelWatchInboxRepository(db *pgxpool.Pool) IntelWatchInboxRepository {
	return &intelWatchInboxRepository{db: db}
}

func validateInboxEvent(event models.IntelWatchInboxEvent) error {
	if event.OrganizationID == uuid.Nil {
		return errors.New("intel watch inbox event requires an organization")
	}
	if strings.TrimSpace(event.EventID) == "" {
		return errors.New("intel watch inbox event requires an event id")
	}
	if strings.TrimSpace(event.SubjectKey) == "" {
		return errors.New("intel watch inbox event requires a subject key")
	}
	if len(event.Payload) == 0 {
		return errors.New("intel watch inbox event requires a payload")
	}
	return nil
}

func (r *intelWatchInboxRepository) AppendOpportunityEvent(ctx context.Context, event models.IntelWatchInboxEvent) (bool, error) {
	if err := validateInboxEvent(event); err != nil {
		return false, err
	}
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return false, err
	}
	if event.ReceivedAt.IsZero() {
		event.ReceivedAt = time.Now().UTC()
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = event.ReceivedAt
	}
	// DO NOTHING rather than DO UPDATE: the stored envelope is the evidence of
	// what the upstream actually said, and a later post of the same id must not
	// be able to rewrite it.
	tag, err := r.db.Exec(ctx, `
		INSERT INTO confenge_intel_watch_inbox
			(organization_id, event_id, event_schema, event_type, subject_key, occurred_at, payload, received_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (organization_id, event_id) DO NOTHING`,
		event.OrganizationID, strings.TrimSpace(event.EventID), strings.TrimSpace(event.Schema),
		strings.TrimSpace(event.EventType), strings.TrimSpace(event.SubjectKey),
		event.OccurredAt.UTC(), payload, event.ReceivedAt.UTC())
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

const intelWatchInboxColumns = `organization_id, event_id, event_schema, event_type, subject_key,
	occurred_at, payload, received_at, emitted_count, last_emitted_at`

func scanInboxEvent(scan func(dest ...any) error) (models.IntelWatchInboxEvent, error) {
	var out models.IntelWatchInboxEvent
	var payload []byte
	if err := scan(&out.OrganizationID, &out.EventID, &out.Schema, &out.EventType, &out.SubjectKey,
		&out.OccurredAt, &payload, &out.ReceivedAt, &out.EmittedCount, &out.LastEmittedAt); err != nil {
		return out, err
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &out.Payload); err != nil {
			return out, err
		}
	}
	return out, nil
}

func (r *intelWatchInboxRepository) ClaimReplayableEvents(ctx context.Context, orgID uuid.UUID, now time.Time, window, lease time.Duration, limit int) ([]models.IntelWatchInboxEvent, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if window <= 0 {
		return nil, errors.New("intel watch inbox replay requires a positive window")
	}
	if lease <= 0 {
		return nil, errors.New("intel watch inbox replay requires a positive lease")
	}
	if limit <= 0 {
		limit = 500
	}
	// One statement so the lease is taken atomically with the read. FOR UPDATE
	// SKIP LOCKED keeps two producer instances from queueing on each other.
	rows, err := r.db.Query(ctx, `
		UPDATE confenge_intel_watch_inbox AS inbox
		SET emit_lease_until = $3,
		    emitted_count = inbox.emitted_count + 1,
		    last_emitted_at = $2
		WHERE (inbox.organization_id, inbox.event_id) IN (
			SELECT candidate.organization_id, candidate.event_id
			FROM confenge_intel_watch_inbox AS candidate
			WHERE ($1::uuid IS NULL OR candidate.organization_id = $1)
			  AND candidate.received_at > $4
			  AND (candidate.emit_lease_until IS NULL OR candidate.emit_lease_until <= $2)
			ORDER BY candidate.received_at ASC
			LIMIT $5
			FOR UPDATE SKIP LOCKED
		)
		RETURNING `+intelWatchInboxColumns,
		nullableOrgID(orgID), now, now.Add(lease), now.Add(-window), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.IntelWatchInboxEvent
	for rows.Next() {
		event, err := scanInboxEvent(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

func (r *intelWatchInboxRepository) GetOpportunityEvent(ctx context.Context, orgID uuid.UUID, eventID string) (*models.IntelWatchInboxEvent, error) {
	eventID = strings.TrimSpace(eventID)
	if orgID == uuid.Nil || eventID == "" {
		return nil, nil
	}
	row := r.db.QueryRow(ctx, `SELECT `+intelWatchInboxColumns+`
		FROM confenge_intel_watch_inbox WHERE organization_id = $1 AND event_id = $2`, orgID, eventID)
	event, err := scanInboxEvent(row.Scan)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &event, nil
}

// nullableOrgID turns the "every organization" sentinel into SQL NULL.
func nullableOrgID(orgID uuid.UUID) *uuid.UUID {
	if orgID == uuid.Nil {
		return nil
	}
	return &orgID
}
