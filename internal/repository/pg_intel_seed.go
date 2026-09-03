package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/models"
)

// IntelSeedRepository owns the INTEL_SEED send ledger, the lane's own daily-cap
// counter. Nothing here reads or writes first-touch dispatch state: the cap is
// additional to the shared admission gates, never carved out of them.
type IntelSeedRepository interface {
	CountSendsSince(ctx context.Context, orgID uuid.UUID, since time.Time) (int, error)
	// RecordSend inserts the no-resend record. It reports whether this call is
	// what created it, so a racing worker learns the touch is not its to send.
	RecordSend(ctx context.Context, send models.IntelSeedSend) (bool, error)
	AlreadySent(ctx context.Context, orgID uuid.UUID, messageKey string) (bool, error)
	// SeededCandidateIDs is the loop's skip set: contacts this lane already
	// wrote to since the given time.
	SeededCandidateIDs(ctx context.Context, orgID uuid.UUID, since time.Time) (map[uuid.UUID]bool, error)
}

type intelSeedRepository struct {
	db *pgxpool.Pool
}

func NewIntelSeedRepository(db *pgxpool.Pool) IntelSeedRepository {
	return &intelSeedRepository{db: db}
}

func (r *intelSeedRepository) CountSendsSince(ctx context.Context, orgID uuid.UUID, since time.Time) (int, error) {
	if orgID == uuid.Nil {
		return 0, errors.New("intel seed send count requires an organization")
	}
	var n int
	err := r.db.QueryRow(ctx, `
		SELECT count(*)::int FROM confenge_intel_seed_sends
		WHERE organization_id = $1 AND sent_at >= $2`, orgID, since.UTC()).Scan(&n)
	return n, err
}

func (r *intelSeedRepository) RecordSend(ctx context.Context, send models.IntelSeedSend) (bool, error) {
	key := strings.TrimSpace(send.MessageKey)
	recipient := strings.ToLower(strings.TrimSpace(send.Recipient))
	if send.OrganizationID == uuid.Nil || key == "" || recipient == "" {
		return false, errors.New("intel seed send requires an organization, a message key and a recipient")
	}
	if send.SentAt.IsZero() {
		send.SentAt = time.Now().UTC()
	}
	tag, err := r.db.Exec(ctx, `
		INSERT INTO confenge_intel_seed_sends
			(organization_id, message_key, candidate_id, account_id, recipient, subject_key, sent_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (organization_id, message_key) DO NOTHING`,
		send.OrganizationID, key, nullableUUID(send.CandidateID), nullableUUID(send.AccountID),
		recipient, strings.TrimSpace(send.SubjectKey), send.SentAt.UTC())
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (r *intelSeedRepository) AlreadySent(ctx context.Context, orgID uuid.UUID, messageKey string) (bool, error) {
	key := strings.TrimSpace(messageKey)
	if orgID == uuid.Nil || key == "" {
		return false, nil
	}
	var exists bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM confenge_intel_seed_sends
			WHERE organization_id = $1 AND message_key = $2)`, orgID, key).Scan(&exists)
	return exists, err
}

func (r *intelSeedRepository) SeededCandidateIDs(ctx context.Context, orgID uuid.UUID, since time.Time) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	if orgID == uuid.Nil {
		return out, nil
	}
	rows, err := r.db.Query(ctx, `
		SELECT candidate_id FROM confenge_intel_seed_sends
		WHERE organization_id = $1 AND sent_at >= $2 AND candidate_id IS NOT NULL`,
		orgID, since.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
