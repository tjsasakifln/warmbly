package confenge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// postgresCohortStore is the production BOUNDED_COHORT_AUTHORIZATION authority.
type postgresCohortStore struct {
	db *pgxpool.Pool
}

// NewPostgresCohortStore returns the durable grant + daily-slot store.
// A nil pool is fail-closed: every method returns ErrCohortStoreUnavailable.
func NewPostgresCohortStore(db *pgxpool.Pool) BoundedCohortStore {
	return &postgresCohortStore{db: db}
}

func (s *postgresCohortStore) unavailable() error {
	if s == nil || s.db == nil {
		return ErrCohortStoreUnavailable
	}
	return nil
}

func (s *postgresCohortStore) Put(auth *BoundedCohortAuthorization) {
	_ = s.PutGrant(context.Background(), auth)
}

func (s *postgresCohortStore) Get(id uuid.UUID) *BoundedCohortAuthorization {
	auth, err := s.GetGrant(context.Background(), id)
	if err != nil {
		return nil
	}
	return auth
}

func (s *postgresCohortStore) SentToday(id uuid.UUID, day string) int {
	n, err := s.occupiedToday(context.Background(), nil, id, day)
	if err != nil {
		return 0
	}
	return n
}

func (s *postgresCohortStore) RecordSent(id uuid.UUID, day string) {
	now := time.Now().UTC()
	if day != "" {
		if parsed, err := time.Parse("2006-01-02", day); err == nil {
			now = parsed.UTC()
		}
	}
	key := fmt.Sprintf("legacy-record:%s:%d", id, time.Now().UTC().UnixNano())
	_, _ = s.ReserveSlot(context.Background(), id, key, now)
	_ = s.CommitSlot(context.Background(), id, key, now)
}

func (s *postgresCohortStore) PutGrant(ctx context.Context, auth *BoundedCohortAuthorization) error {
	if err := s.unavailable(); err != nil {
		return err
	}
	if auth == nil || auth.ID == uuid.Nil {
		return ErrCohortGrantMissing
	}
	if auth.ActorID == uuid.Nil {
		return ErrCohortHumanActor
	}
	if strings.TrimSpace(auth.RepositorySHA) == "" || strings.TrimSpace(auth.FrozenHash()) == "" {
		return fmt.Errorf("bounded cohort grant is incomplete")
	}
	if auth.AuthorizedAt.IsZero() {
		auth.AuthorizedAt = time.Now().UTC()
	}
	if auth.ExpiresAt.IsZero() {
		auth.ExpiresAt = auth.EffectiveExpiry()
	}
	if auth.ExpiresAt.IsZero() {
		return fmt.Errorf("bounded cohort grant requires expiry")
	}
	if auth.MaxDailyVolume < 1 {
		return fmt.Errorf("daily cap missing")
	}
	if auth.PolicyVersion == "" {
		auth.PolicyVersion = BoundedCohortPolicyV1
	}
	frozen := auth.FrozenHash()
	auth.FrozenHashValue = frozen
	ttlSec := int64(auth.TTL / time.Second)
	now := time.Now().UTC()
	_, err := s.db.Exec(ctx, `
		INSERT INTO confenge_bounded_cohort_authorizations (
			id, organization_id, actor_id, authorized_at,
			repository_sha, feed_schema_version, cohort_id, cohort_hash,
			policy_version, allowed_route_classes, max_daily_volume,
			recipient_set_hash, composer_version, evidence_version,
			ttl_seconds, expires_at, frozen_hash,
			revoked_at, revoke_actor, revoke_reason, created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,
			$18,$19,$20,$21,$21
		)
		ON CONFLICT (id) DO UPDATE SET
			updated_at = EXCLUDED.updated_at`,
		auth.ID, auth.OrganizationID, auth.ActorID, auth.AuthorizedAt.UTC(),
		strings.TrimSpace(auth.RepositorySHA), strings.TrimSpace(auth.FeedSchemaVersion),
		strings.TrimSpace(auth.CohortID), strings.TrimSpace(auth.CohortHash),
		strings.TrimSpace(auth.PolicyVersion), auth.AllowedRouteClasses, auth.MaxDailyVolume,
		strings.TrimSpace(auth.RecipientSetHash), strings.TrimSpace(auth.ComposerVersion),
		strings.TrimSpace(auth.EvidenceVersion), ttlSec, auth.ExpiresAt.UTC(), frozen,
		auth.RevokedAt, nilUUID(auth.RevokeActor), strings.TrimSpace(auth.RevokeReason), now,
	)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCohortStoreUnavailable, err)
	}
	return nil
}

func nilUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func (s *postgresCohortStore) GetGrant(ctx context.Context, id uuid.UUID) (*BoundedCohortAuthorization, error) {
	if err := s.unavailable(); err != nil {
		return nil, err
	}
	if id == uuid.Nil {
		return nil, nil
	}
	row := s.db.QueryRow(ctx, `
		SELECT id, organization_id, actor_id, authorized_at,
			repository_sha, feed_schema_version, cohort_id, cohort_hash,
			policy_version, allowed_route_classes, max_daily_volume,
			recipient_set_hash, composer_version, evidence_version,
			ttl_seconds, expires_at, frozen_hash,
			revoked_at, revoke_actor, revoke_reason
		FROM confenge_bounded_cohort_authorizations WHERE id=$1`, id)
	return scanCohortGrant(row)
}

func scanCohortGrant(row pgx.Row) (*BoundedCohortAuthorization, error) {
	var a BoundedCohortAuthorization
	var ttlSec int64
	var revokeActor *uuid.UUID
	err := row.Scan(
		&a.ID, &a.OrganizationID, &a.ActorID, &a.AuthorizedAt,
		&a.RepositorySHA, &a.FeedSchemaVersion, &a.CohortID, &a.CohortHash,
		&a.PolicyVersion, &a.AllowedRouteClasses, &a.MaxDailyVolume,
		&a.RecipientSetHash, &a.ComposerVersion, &a.EvidenceVersion,
		&ttlSec, &a.ExpiresAt, &a.FrozenHashValue,
		&a.RevokedAt, &revokeActor, &a.RevokeReason,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCohortStoreUnavailable, err)
	}
	a.TTL = time.Duration(ttlSec) * time.Second
	if revokeActor != nil {
		a.RevokeActor = *revokeActor
	}
	return &a, nil
}

func (s *postgresCohortStore) RevokeGrant(ctx context.Context, id, actor uuid.UUID, reason string, now time.Time) error {
	if err := s.unavailable(); err != nil {
		return err
	}
	if id == uuid.Nil {
		return ErrCohortGrantMissing
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE confenge_bounded_cohort_authorizations
		SET revoked_at = COALESCE(revoked_at, $2),
		    revoke_actor = COALESCE(revoke_actor, $3),
		    revoke_reason = CASE WHEN revoke_reason = '' THEN $4 ELSE revoke_reason END,
		    updated_at = $2
		WHERE id=$1`,
		id, now.UTC(), nilUUID(actor), strings.TrimSpace(reason),
	)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCohortStoreUnavailable, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrCohortGrantMissing
	}
	return nil
}

func (s *postgresCohortStore) occupiedToday(ctx context.Context, tx pgx.Tx, id uuid.UUID, day string) (int, error) {
	q := `
		SELECT count(*) FROM confenge_bounded_cohort_reservations
		WHERE authorization_id=$1 AND day=$2::date AND state IN ('reserved','sent')`
	var n int
	var err error
	if tx != nil {
		err = tx.QueryRow(ctx, q, id, day).Scan(&n)
	} else {
		if uerr := s.unavailable(); uerr != nil {
			return 0, uerr
		}
		err = s.db.QueryRow(ctx, q, id, day).Scan(&n)
	}
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrCohortStoreUnavailable, err)
	}
	return n, nil
}

func (s *postgresCohortStore) ReserveSlot(ctx context.Context, id uuid.UUID, messageKey string, now time.Time) (CohortReserveResult, error) {
	out := CohortReserveResult{}
	if err := s.unavailable(); err != nil {
		return out, err
	}
	if id == uuid.Nil || strings.TrimSpace(messageKey) == "" {
		return out, ErrCohortGrantMissing
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	day := now.Format("2006-01-02")

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return out, fmt.Errorf("%w: %v", ErrCohortStoreUnavailable, err)
	}
	defer tx.Rollback(ctx)

	var auth BoundedCohortAuthorization
	var ttlSec int64
	var revokeActor *uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id, organization_id, actor_id, authorized_at,
			repository_sha, feed_schema_version, cohort_id, cohort_hash,
			policy_version, allowed_route_classes, max_daily_volume,
			recipient_set_hash, composer_version, evidence_version,
			ttl_seconds, expires_at, frozen_hash,
			revoked_at, revoke_actor, revoke_reason
		FROM confenge_bounded_cohort_authorizations
		WHERE id=$1 FOR UPDATE`, id,
	).Scan(
		&auth.ID, &auth.OrganizationID, &auth.ActorID, &auth.AuthorizedAt,
		&auth.RepositorySHA, &auth.FeedSchemaVersion, &auth.CohortID, &auth.CohortHash,
		&auth.PolicyVersion, &auth.AllowedRouteClasses, &auth.MaxDailyVolume,
		&auth.RecipientSetHash, &auth.ComposerVersion, &auth.EvidenceVersion,
		&ttlSec, &auth.ExpiresAt, &auth.FrozenHashValue,
		&auth.RevokedAt, &revokeActor, &auth.RevokeReason,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrCohortGrantMissing
	}
	if err != nil {
		return out, fmt.Errorf("%w: %v", ErrCohortStoreUnavailable, err)
	}
	if auth.RevokedAt != nil && !auth.RevokedAt.After(now) {
		return out, ErrCohortGrantRevoked
	}
	if !auth.ExpiresAt.IsZero() && !now.Before(auth.ExpiresAt.UTC()) {
		return out, ErrCohortGrantExpired
	}

	var existingState string
	err = tx.QueryRow(ctx, `
		SELECT state FROM confenge_bounded_cohort_reservations
		WHERE authorization_id=$1 AND message_key=$2
		FOR UPDATE`, id, messageKey,
	).Scan(&existingState)
	if err == nil {
		if existingState == CohortSlotReserved || existingState == CohortSlotSent {
			n, oerr := s.occupiedToday(ctx, tx, id, day)
			if oerr != nil {
				return out, oerr
			}
			out.State = existingState
			out.Already = true
			out.Occupied = n
			if err := tx.Commit(ctx); err != nil {
				return out, fmt.Errorf("%w: %v", ErrCohortStoreUnavailable, err)
			}
			return out, nil
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return out, fmt.Errorf("%w: %v", ErrCohortStoreUnavailable, err)
	}

	occupied, err := s.occupiedToday(ctx, tx, id, day)
	if err != nil {
		return out, err
	}
	if occupied >= auth.MaxDailyVolume {
		return out, ErrCohortDailyCap
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO confenge_bounded_cohort_reservations
			(authorization_id, message_key, day, state, reserved_at)
		VALUES ($1,$2,$3::date,'reserved',$4)
		ON CONFLICT (authorization_id, message_key) DO UPDATE SET
			state = 'reserved',
			day = EXCLUDED.day,
			reserved_at = EXCLUDED.reserved_at,
			released_at = NULL,
			release_reason = '',
			committed_at = NULL
		WHERE confenge_bounded_cohort_reservations.state = 'released'`,
		id, messageKey, day, now,
	)
	if err != nil {
		return out, fmt.Errorf("%w: %v", ErrCohortStoreUnavailable, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return out, fmt.Errorf("%w: %v", ErrCohortStoreUnavailable, err)
	}
	out.State = CohortSlotReserved
	out.Occupied = occupied + 1
	return out, nil
}

func (s *postgresCohortStore) CommitSlot(ctx context.Context, id uuid.UUID, messageKey string, now time.Time) error {
	if err := s.unavailable(); err != nil {
		return err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE confenge_bounded_cohort_reservations
		SET state='sent', committed_at=$3
		WHERE authorization_id=$1 AND message_key=$2 AND state IN ('reserved','sent')`,
		id, messageKey, now.UTC(),
	)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCohortStoreUnavailable, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrCohortSlotNotHeld
	}
	return nil
}

func (s *postgresCohortStore) ReleaseSlot(ctx context.Context, id uuid.UUID, messageKey, reason string) error {
	if err := s.unavailable(); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx, `
		UPDATE confenge_bounded_cohort_reservations
		SET state='released', released_at=now(), release_reason=$3
		WHERE authorization_id=$1 AND message_key=$2 AND state='reserved'`,
		id, messageKey, strings.TrimSpace(reason),
	)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCohortStoreUnavailable, err)
	}
	return nil
}

func (s *postgresCohortStore) ReleaseSlotByLease(ctx context.Context, leaseToken, reason string) error {
	if err := s.unavailable(); err != nil {
		return err
	}
	if strings.TrimSpace(leaseToken) == "" {
		return nil
	}
	_, err := s.db.Exec(ctx, `
		UPDATE confenge_bounded_cohort_reservations
		SET state='released', released_at=now(), release_reason=$2
		WHERE lease_token=$1 AND state='reserved'`,
		leaseToken, strings.TrimSpace(reason),
	)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCohortStoreUnavailable, err)
	}
	return nil
}

func (s *postgresCohortStore) BindLeaseToken(ctx context.Context, id uuid.UUID, messageKey, leaseToken string) error {
	if err := s.unavailable(); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx, `
		UPDATE confenge_bounded_cohort_reservations
		SET lease_token=$3
		WHERE authorization_id=$1 AND message_key=$2 AND state='reserved'`,
		id, messageKey, leaseToken,
	)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCohortStoreUnavailable, err)
	}
	return nil
}
