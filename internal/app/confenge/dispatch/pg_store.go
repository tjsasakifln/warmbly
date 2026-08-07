package dispatch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PGStore struct {
	db *pgxpool.Pool
}

func NewPGStore(db *pgxpool.Pool) *PGStore {
	return &PGStore{db: db}
}

func (s *PGStore) GetControl(ctx context.Context) (ControlState, error) {
	var st ControlState
	var pausedAt *time.Time
	var pausedBy *uuid.UUID
	err := s.db.QueryRow(ctx, `
		SELECT paused, pause_reason, paused_at, paused_by
		FROM confenge_dispatch_control WHERE id = 1`).Scan(
		&st.Paused, &st.PauseReason, &pausedAt, &pausedBy,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ControlState{}, nil
	}
	if err != nil {
		return st, err
	}
	st.PausedAt = pausedAt
	st.PausedBy = pausedBy
	return st, nil
}

func (s *PGStore) SetPaused(ctx context.Context, paused bool, reason string, by *uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO confenge_dispatch_control (id, paused, pause_reason, paused_at, paused_by, updated_at)
		VALUES (1, $1, $2, CASE WHEN $1 THEN now() ELSE NULL END, $3, now())
		ON CONFLICT (id) DO UPDATE SET
			paused = EXCLUDED.paused,
			pause_reason = EXCLUDED.pause_reason,
			paused_at = EXCLUDED.paused_at,
			paused_by = EXCLUDED.paused_by,
			updated_at = now()`,
		paused, reason, by,
	)
	return err
}

func (s *PGStore) ListOccupied(ctx context.Context, now time.Time, window time.Duration) ([]time.Time, time.Time, error) {
	cutoff := now.Add(-window)
	rows, err := s.db.Query(ctx, `
		SELECT sent_at FROM confenge_dispatch_sends
		WHERE sent_at >= $1 AND sent_at <= $2
		UNION ALL
		SELECT reserved_at FROM confenge_dispatch_reservations
		WHERE state = 'reserved' AND lease_until > $2
		  AND reserved_at >= $1 AND reserved_at <= $2`,
		cutoff, now,
	)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer rows.Close()
	var times []time.Time
	var last time.Time
	for rows.Next() {
		var t time.Time
		if err := rows.Scan(&t); err != nil {
			return nil, time.Time{}, err
		}
		times = append(times, t)
		if t.After(last) {
			last = t
		}
	}
	return times, last, rows.Err()
}

func (s *PGStore) GetReservationByKey(ctx context.Context, messageKey string) (*Reservation, error) {
	var r Reservation
	var draftID *uuid.UUID
	var committedAt *time.Time
	err := s.db.QueryRow(ctx, `
		SELECT id, organization_id, channel, message_key, draft_id, state,
		       reserved_at, lease_until, committed_at
		FROM confenge_dispatch_reservations WHERE message_key = $1`, messageKey,
	).Scan(&r.ID, &r.OrganizationID, &r.Channel, &r.MessageKey, &draftID, &r.State,
		&r.ReservedAt, &r.LeaseUntil, &committedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.DraftID = draftID
	r.CommittedAt = committedAt
	return &r, nil
}

func (s *PGStore) GetSendByKey(ctx context.Context, messageKey string) (time.Time, bool, error) {
	var t time.Time
	err := s.db.QueryRow(ctx, `SELECT sent_at FROM confenge_dispatch_sends WHERE message_key = $1`, messageKey).Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return t, true, nil
}

func (s *PGStore) InsertReservation(ctx context.Context, r *Reservation) error {
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT id FROM confenge_dispatch_control WHERE id = 1 FOR UPDATE`); err != nil {
		if _, ierr := tx.Exec(ctx, `INSERT INTO confenge_dispatch_control (id) VALUES (1) ON CONFLICT DO NOTHING`); ierr != nil {
			return ierr
		}
		if _, err = tx.Exec(ctx, `SELECT id FROM confenge_dispatch_control WHERE id = 1 FOR UPDATE`); err != nil {
			return err
		}
	}

	_, _ = tx.Exec(ctx, `
		DELETE FROM confenge_dispatch_reservations
		WHERE message_key = $1 AND (
			state IN ('released', 'failed')
			OR (state = 'reserved' AND lease_until <= $2)
		)`, r.MessageKey, r.ReservedAt)

	_, err = tx.Exec(ctx, `
		INSERT INTO confenge_dispatch_reservations
			(id, organization_id, channel, message_key, draft_id, state, reserved_at, lease_until, worker_token)
		VALUES ($1,$2,$3,$4,$5,'reserved',$6,$7,$8)`,
		r.ID, r.OrganizationID, r.Channel, r.MessageKey, r.DraftID,
		r.ReservedAt, r.LeaseUntil, r.WorkerToken,
	)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PGStore) RefreshReservation(ctx context.Context, id uuid.UUID, leaseUntil time.Time, workerToken string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE confenge_dispatch_reservations
		SET lease_until = $2, worker_token = COALESCE(NULLIF($3,''), worker_token)
		WHERE id = $1 AND state = 'reserved'`, id, leaseUntil, workerToken)
	return err
}

func (s *PGStore) CommitReservation(ctx context.Context, id uuid.UUID, sentAt time.Time) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var r Reservation
	var draftID *uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id, organization_id, channel, message_key, draft_id, state
		FROM confenge_dispatch_reservations WHERE id = $1 FOR UPDATE`, id,
	).Scan(&r.ID, &r.OrganizationID, &r.Channel, &r.MessageKey, &draftID, &r.State)
	if err != nil {
		return err
	}
	r.DraftID = draftID
	if r.State == StateCommitted {
		return tx.Commit(ctx)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO confenge_dispatch_sends
			(organization_id, channel, message_key, draft_id, reservation_id, sent_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (message_key) DO NOTHING`,
		r.OrganizationID, r.Channel, r.MessageKey, r.DraftID, r.ID, sentAt,
	)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE confenge_dispatch_reservations SET state = 'committed', committed_at = $2 WHERE id = $1`, id, sentAt)
	if err != nil {
		return err
	}
	_, _ = tx.Exec(ctx, `
		UPDATE confenge_dispatch_queue SET status = 'sent', updated_at = now()
		WHERE message_key = $1 AND status IN ('queued','reserved')`, r.MessageKey)
	return tx.Commit(ctx)
}

func (s *PGStore) ReleaseReservation(ctx context.Context, id uuid.UUID, state, errText string) error {
	if state == "" {
		state = StateReleased
	}
	_, err := s.db.Exec(ctx, `
		UPDATE confenge_dispatch_reservations SET state = $2, last_error = $3
		WHERE id = $1 AND state = 'reserved'`, id, state, errText)
	return err
}

func (s *PGStore) ExpireStaleReservations(ctx context.Context, now time.Time) (int, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE confenge_dispatch_reservations
		SET state = 'released', last_error = 'lease_expired'
		WHERE state = 'reserved' AND lease_until <= $1`, now)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (s *PGStore) Enqueue(ctx context.Context, item *QueueItem) error {
	if item.ID == uuid.Nil {
		item.ID = uuid.New()
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO confenge_dispatch_queue
			(id, organization_id, channel, draft_id, message_key, due_at, priority, status, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'queued', now(), now())
		ON CONFLICT (message_key) DO UPDATE SET
			due_at = EXCLUDED.due_at,
			priority = EXCLUDED.priority,
			status = CASE
				WHEN confenge_dispatch_queue.status IN ('sent','cancelled') THEN confenge_dispatch_queue.status
				ELSE 'queued'
			END,
			updated_at = now()`,
		item.ID, item.OrganizationID, item.Channel, item.DraftID, item.MessageKey,
		item.DueAt, item.Priority,
	)
	return err
}

func (s *PGStore) CancelQueue(ctx context.Context, messageKey, reason string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE confenge_dispatch_queue
		SET status = 'cancelled', cancel_reason = $2, updated_at = now()
		WHERE message_key = $1 AND status IN ('queued','reserved','failed')`, messageKey, reason)
	return err
}

func (s *PGStore) CountQueued(ctx context.Context, orgID *uuid.UUID) (int, error) {
	var n int
	var err error
	if orgID != nil {
		err = s.db.QueryRow(ctx, `SELECT count(*) FROM confenge_dispatch_queue WHERE status = 'queued' AND organization_id = $1`, *orgID).Scan(&n)
	} else {
		err = s.db.QueryRow(ctx, `SELECT count(*) FROM confenge_dispatch_queue WHERE status = 'queued'`).Scan(&n)
	}
	return n, err
}

func (s *PGStore) DequeueNext(ctx context.Context, now time.Time) (*QueueItem, error) {
	var q QueueItem
	err := s.db.QueryRow(ctx, `
		SELECT id, organization_id, channel, draft_id, message_key, due_at, priority, status,
		       cancel_reason, last_error, created_at
		FROM confenge_dispatch_queue
		WHERE status = 'queued' AND due_at <= $1
		ORDER BY due_at ASC, priority DESC, created_at ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED`, now,
	).Scan(&q.ID, &q.OrganizationID, &q.Channel, &q.DraftID, &q.MessageKey, &q.DueAt,
		&q.Priority, &q.Status, &q.CancelReason, &q.LastError, &q.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &q, nil
}

func (s *PGStore) UpdateQueueStatus(ctx context.Context, id uuid.UUID, status, errText string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE confenge_dispatch_queue
		SET status = $2, last_error = CASE WHEN $3 <> '' THEN $3 ELSE last_error END, updated_at = now()
		WHERE id = $1`, id, status, errText)
	return err
}

func (s *PGStore) RecordFailure(ctx context.Context, f FailureRecord) error {
	if f.ID == uuid.Nil {
		f.ID = uuid.New()
	}
	if f.OccurredAt.IsZero() {
		f.OccurredAt = time.Now().UTC()
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO confenge_dispatch_failures
			(id, organization_id, channel, message_key, draft_id, error_text, occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		f.ID, f.OrganizationID, f.Channel, f.MessageKey, f.DraftID, f.ErrorText, f.OccurredAt,
	)
	return err
}

func (s *PGStore) ListRecentFailures(ctx context.Context, limit int) ([]FailureRecord, error) {
	if limit < 1 {
		limit = DefaultMaxRecentFails
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, organization_id, channel, message_key, draft_id, error_text, occurred_at
		FROM confenge_dispatch_failures ORDER BY occurred_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FailureRecord
	for rows.Next() {
		var f FailureRecord
		if err := rows.Scan(&f.ID, &f.OrganizationID, &f.Channel, &f.MessageKey, &f.DraftID, &f.ErrorText, &f.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *PGStore) CountActiveLeases(ctx context.Context, now time.Time) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM confenge_dispatch_reservations WHERE state = 'reserved' AND lease_until > $1`, now).Scan(&n)
	return n, err
}

func (s *PGStore) CountSendsSince(ctx context.Context, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM confenge_dispatch_sends WHERE sent_at >= $1`, since).Scan(&n)
	return n, err
}

func (s *PGStore) EnsureControl(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO confenge_dispatch_control (id, paused, pause_reason)
		VALUES (1, false, '') ON CONFLICT (id) DO NOTHING`)
	if err != nil {
		return fmt.Errorf("ensure confenge_dispatch_control: %w", err)
	}
	return nil
}
