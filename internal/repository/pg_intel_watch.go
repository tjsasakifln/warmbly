package repository

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/models"
)

// IntelWatchRepository owns the INTEL_WATCH subscription and dedup tables.
// Every read and write is organization-scoped; nothing here can reach across
// organizations even when the caller supplies only a subscription id.
type IntelWatchRepository interface {
	// CreateOrReactivateSubscription is idempotent on
	// (organization, contact_email, subject_key, intent_kind): a repeat request
	// updates consent and clears any prior opt-out instead of inserting a twin.
	CreateOrReactivateSubscription(ctx context.Context, sub *models.IntelWatchSubscription) (*models.IntelWatchSubscription, error)
	GetActiveSubscription(ctx context.Context, organizationID, subscriptionID uuid.UUID) (*models.IntelWatchSubscription, error)
	ListActiveSubscriptionsBySubject(ctx context.Context, organizationID uuid.UUID, subjectKey string) ([]models.IntelWatchSubscription, error)
	// Unsubscribe reports whether this call is what stopped the subscription.
	// A repeat unsubscribe is a no-op, not an error.
	Unsubscribe(ctx context.Context, organizationID, subscriptionID uuid.UUID, at time.Time) (bool, error)

	// ClaimDelivery asks for the sole right to deliver one
	// (subscription, event identity, content hash) once. Exactly one caller can
	// hold it at a time; a terminal row never grants it again.
	ClaimDelivery(ctx context.Context, key models.IntelWatchDeliveryKey, now time.Time, lease time.Duration, maxAttempts int) (models.IntelWatchClaim, error)
	// MarkDeliveryInFlight writes the durable no-resend fence immediately before
	// the dispatcher's point of no return. It fails when the claim was lost.
	MarkDeliveryInFlight(ctx context.Context, key models.IntelWatchDeliveryKey, now time.Time) error
	// SettleDelivery writes a terminal state. Only DISPATCHED stamps sent_at.
	SettleDelivery(ctx context.Context, key models.IntelWatchDeliveryKey, state, errText string, at time.Time) (bool, error)
	// ReleaseDelivery hands a claimed-but-unfenced attempt back for retry. It
	// refuses a fenced (IN_FLIGHT) row: that one must be parked, never replayed.
	ReleaseDelivery(ctx context.Context, key models.IntelWatchDeliveryKey, errText string, at time.Time) (bool, error)
	GetDelivery(ctx context.Context, key models.IntelWatchDeliveryKey) (*models.IntelWatchDelivery, error)
	// ExpireStaleDeliveryHandoffs parks fenced attempts whose worker disappeared.
	// Reconciler work, deliberately not on the delivery path.
	ExpireStaleDeliveryHandoffs(ctx context.Context, now time.Time) (int, error)
}

type intelWatchRepository struct {
	db *pgxpool.Pool
}

func NewIntelWatchRepository(db *pgxpool.Pool) IntelWatchRepository {
	return &intelWatchRepository{db: db}
}

const intelWatchColumns = `id, organization_id, contact_email, contact_id, account_id, intent_kind,
	subject_key, topic, cadence, consent_source, consent_at, consent_provenance_ok,
	unsubscribed_at, created_at, updated_at`

func normalizeIntelWatchEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// validateIntelWatchSubscription rejects out-of-set values at the application
// boundary so a caller gets a readable error instead of a CHECK violation.
func validateIntelWatchSubscription(sub *models.IntelWatchSubscription) error {
	if sub == nil {
		return errors.New("intel watch subscription is required")
	}
	if sub.OrganizationID == uuid.Nil {
		return errors.New("intel watch subscription requires an organization")
	}
	if normalizeIntelWatchEmail(sub.ContactEmail) == "" {
		return errors.New("intel watch subscription requires a contact email")
	}
	if strings.TrimSpace(sub.SubjectKey) == "" {
		return errors.New("intel watch subscription requires a subject key")
	}
	if !slices.Contains(models.IntelWatchIntentKinds, sub.IntentKind) {
		return fmt.Errorf("unknown intel watch intent kind %q", sub.IntentKind)
	}
	if !slices.Contains(models.IntelWatchCadences, sub.Cadence) {
		return fmt.Errorf("unknown intel watch cadence %q", sub.Cadence)
	}
	return nil
}

func scanIntelWatchSubscription(row pgx.Row) (*models.IntelWatchSubscription, error) {
	var sub models.IntelWatchSubscription
	err := row.Scan(&sub.ID, &sub.OrganizationID, &sub.ContactEmail, &sub.ContactID, &sub.AccountID,
		&sub.IntentKind, &sub.SubjectKey, &sub.Topic, &sub.Cadence, &sub.ConsentSource,
		&sub.ConsentAt, &sub.ConsentProvenanceOK, &sub.UnsubscribedAt, &sub.CreatedAt, &sub.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &sub, nil
}

func (r *intelWatchRepository) CreateOrReactivateSubscription(ctx context.Context, sub *models.IntelWatchSubscription) (*models.IntelWatchSubscription, error) {
	if err := validateIntelWatchSubscription(sub); err != nil {
		return nil, err
	}
	if sub.Cadence == "" {
		sub.Cadence = models.IntelWatchCadenceImmediate
	}
	query := `
		INSERT INTO confenge_intel_watch_subscriptions
			(organization_id, contact_email, contact_id, account_id, intent_kind, subject_key,
			 topic, cadence, consent_source, consent_at, consent_provenance_ok)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (organization_id, contact_email, subject_key, intent_kind) DO UPDATE SET
			contact_id = COALESCE(EXCLUDED.contact_id, confenge_intel_watch_subscriptions.contact_id),
			account_id = COALESCE(EXCLUDED.account_id, confenge_intel_watch_subscriptions.account_id),
			topic = EXCLUDED.topic,
			cadence = EXCLUDED.cadence,
			consent_source = EXCLUDED.consent_source,
			consent_at = COALESCE(EXCLUDED.consent_at, confenge_intel_watch_subscriptions.consent_at),
			consent_provenance_ok = EXCLUDED.consent_provenance_ok,
			unsubscribed_at = NULL,
			updated_at = now()
		RETURNING ` + intelWatchColumns
	return scanIntelWatchSubscription(r.db.QueryRow(ctx, query,
		sub.OrganizationID, normalizeIntelWatchEmail(sub.ContactEmail), sub.ContactID, sub.AccountID,
		sub.IntentKind, strings.TrimSpace(sub.SubjectKey), sub.Topic, sub.Cadence,
		sub.ConsentSource, sub.ConsentAt, sub.ConsentProvenanceOK))
}

func (r *intelWatchRepository) GetActiveSubscription(ctx context.Context, organizationID, subscriptionID uuid.UUID) (*models.IntelWatchSubscription, error) {
	query := `SELECT ` + intelWatchColumns + `
		FROM confenge_intel_watch_subscriptions
		WHERE organization_id = $1 AND id = $2 AND unsubscribed_at IS NULL`
	return scanIntelWatchSubscription(r.db.QueryRow(ctx, query, organizationID, subscriptionID))
}

func (r *intelWatchRepository) ListActiveSubscriptionsBySubject(ctx context.Context, organizationID uuid.UUID, subjectKey string) ([]models.IntelWatchSubscription, error) {
	subject := strings.TrimSpace(subjectKey)
	if organizationID == uuid.Nil || subject == "" {
		return nil, nil
	}
	query := `SELECT ` + intelWatchColumns + `
		FROM confenge_intel_watch_subscriptions
		WHERE organization_id = $1 AND subject_key = $2 AND unsubscribed_at IS NULL
		ORDER BY created_at ASC`
	rows, err := r.db.Query(ctx, query, organizationID, subject)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.IntelWatchSubscription
	for rows.Next() {
		sub, err := scanIntelWatchSubscription(rows)
		if err != nil {
			return nil, err
		}
		if sub != nil {
			out = append(out, *sub)
		}
	}
	return out, rows.Err()
}

func (r *intelWatchRepository) Unsubscribe(ctx context.Context, organizationID, subscriptionID uuid.UUID, at time.Time) (bool, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	tag, err := r.db.Exec(ctx, `
		UPDATE confenge_intel_watch_subscriptions
		SET unsubscribed_at = $3, updated_at = now()
		WHERE organization_id = $1 AND id = $2 AND unsubscribed_at IS NULL`,
		organizationID, subscriptionID, at.UTC())
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// normalizeDeliveryKey trims and validates a delivery identity. Every ledger
// method calls it before touching the pool, so a bad key is a readable error
// rather than a constraint violation (and a nil-pool repository stays usable in
// the argument-validation tests).
func normalizeDeliveryKey(key models.IntelWatchDeliveryKey) (models.IntelWatchDeliveryKey, error) {
	key.EventIdentity = strings.TrimSpace(key.EventIdentity)
	key.ContentHash = strings.TrimSpace(key.ContentHash)
	if key.SubscriptionID == uuid.Nil {
		return key, errors.New("intel watch delivery requires a subscription")
	}
	if key.EventIdentity == "" || key.ContentHash == "" {
		return key, errors.New("intel watch delivery requires an event identity and a content hash")
	}
	return key, nil
}

func (r *intelWatchRepository) ClaimDelivery(ctx context.Context, key models.IntelWatchDeliveryKey, now time.Time, lease time.Duration, maxAttempts int) (models.IntelWatchClaim, error) {
	out := models.IntelWatchClaim{}
	key, err := normalizeDeliveryKey(key)
	if err != nil {
		return out, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if lease <= 0 {
		return out, errors.New("intel watch delivery claim requires a positive lease")
	}
	if maxAttempts < 1 {
		return out, errors.New("intel watch delivery claim requires a positive attempt budget")
	}
	// The conditional upsert IS the concurrency guard. A first claimant inserts
	// with a live lease; anyone racing it conflicts on the primary key and fails
	// the WHERE, so exactly one worker holds the delivery at a time. A terminal
	// row never matches, so a replay can never re-send a delivered watch.
	var state string
	var attempts int
	err = r.db.QueryRow(ctx, `
		INSERT INTO confenge_intel_watch_dedup
			(subscription_id, event_identity, content_hash, state, attempts, claimed_at, lease_until, created_at, updated_at)
		VALUES ($1,$2,$3,'PENDING',1,$4,$5,$4,$4)
		ON CONFLICT (subscription_id, event_identity, content_hash) DO UPDATE SET
			state = 'PENDING',
			attempts = confenge_intel_watch_dedup.attempts + 1,
			claimed_at = $4,
			lease_until = $5,
			last_error = '',
			updated_at = $4
		WHERE confenge_intel_watch_dedup.state = 'PENDING'
		  AND (confenge_intel_watch_dedup.lease_until IS NULL OR confenge_intel_watch_dedup.lease_until <= $4)
		  AND confenge_intel_watch_dedup.attempts < $6
		RETURNING state, attempts`,
		key.SubscriptionID, key.EventIdentity, key.ContentHash, now, now.Add(lease), maxAttempts).
		Scan(&state, &attempts)
	if err == nil {
		return models.IntelWatchClaim{Granted: true, State: state, Attempts: attempts}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	// The claim was refused. Read the row back to say why; the answer is
	// advisory only, since we already know we may not deliver.
	existing, err := r.GetDelivery(ctx, key)
	if err != nil {
		return out, err
	}
	if existing == nil {
		return out, errors.New("intel watch delivery claim was refused with no ledger row")
	}
	out.State = existing.State
	out.Attempts = existing.Attempts
	out.Reason = deliveryRefusalReason(existing, now, maxAttempts)
	return out, nil
}

func deliveryRefusalReason(existing *models.IntelWatchDelivery, now time.Time, maxAttempts int) string {
	switch existing.State {
	case models.IntelWatchDeliveryDispatched:
		return models.IntelWatchClaimAlreadyDispatched
	case models.IntelWatchDeliveryFailed:
		return models.IntelWatchClaimTerminalFailed
	case models.IntelWatchDeliveryAmbiguous:
		return models.IntelWatchClaimParkedAmbiguous
	case models.IntelWatchDeliveryInFlight:
		// A live lease means another worker is mid-handoff right now; an expired
		// one means it never came back and the delivery is parked. Either way
		// the claim is refused, so this only decides how it is reported.
		if existing.LeaseUntil != nil && existing.LeaseUntil.After(now) {
			return models.IntelWatchClaimHeldElsewhere
		}
		return models.IntelWatchClaimParkedAmbiguous
	}
	if existing.Attempts >= maxAttempts {
		return models.IntelWatchClaimAttemptsExhausted
	}
	return models.IntelWatchClaimHeldElsewhere
}

func (r *intelWatchRepository) MarkDeliveryInFlight(ctx context.Context, key models.IntelWatchDeliveryKey, now time.Time) error {
	key, err := normalizeDeliveryKey(key)
	if err != nil {
		return err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tag, err := r.db.Exec(ctx, `
		UPDATE confenge_intel_watch_dedup
		SET state = 'IN_FLIGHT', updated_at = $4
		WHERE subscription_id = $1 AND event_identity = $2 AND content_hash = $3
		  AND state = 'PENDING'`,
		key.SubscriptionID, key.EventIdentity, key.ContentHash, now.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		// Fail closed: without the fence the dispatcher must not hand over.
		return errors.New("intel watch delivery claim was lost before handoff")
	}
	return nil
}

func (r *intelWatchRepository) SettleDelivery(ctx context.Context, key models.IntelWatchDeliveryKey, state, errText string, at time.Time) (bool, error) {
	key, err := normalizeDeliveryKey(key)
	if err != nil {
		return false, err
	}
	if !models.IntelWatchDeliveryTerminal(state) {
		return false, fmt.Errorf("intel watch delivery cannot settle in state %q", state)
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	tag, err := r.db.Exec(ctx, `
		UPDATE confenge_intel_watch_dedup
		SET state = $4,
		    last_error = $5,
		    sent_at = CASE WHEN $4 = 'DISPATCHED' THEN $6 ELSE sent_at END,
		    lease_until = NULL,
		    updated_at = $6
		WHERE subscription_id = $1 AND event_identity = $2 AND content_hash = $3
		  AND state IN ('PENDING','IN_FLIGHT')`,
		key.SubscriptionID, key.EventIdentity, key.ContentHash, state, errText, at.UTC())
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (r *intelWatchRepository) ReleaseDelivery(ctx context.Context, key models.IntelWatchDeliveryKey, errText string, at time.Time) (bool, error) {
	key, err := normalizeDeliveryKey(key)
	if err != nil {
		return false, err
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	// Only an unfenced attempt may go back on the shelf. IN_FLIGHT is excluded
	// on purpose: releasing it would let a possibly-delivered watch be re-sent.
	tag, err := r.db.Exec(ctx, `
		UPDATE confenge_intel_watch_dedup
		SET lease_until = NULL, last_error = $4, updated_at = $5
		WHERE subscription_id = $1 AND event_identity = $2 AND content_hash = $3
		  AND state = 'PENDING'`,
		key.SubscriptionID, key.EventIdentity, key.ContentHash, errText, at.UTC())
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (r *intelWatchRepository) GetDelivery(ctx context.Context, key models.IntelWatchDeliveryKey) (*models.IntelWatchDelivery, error) {
	key, err := normalizeDeliveryKey(key)
	if err != nil {
		return nil, err
	}
	var out models.IntelWatchDelivery
	err = r.db.QueryRow(ctx, `
		SELECT subscription_id, event_identity, content_hash, state, attempts,
		       claimed_at, lease_until, sent_at, last_error, created_at, updated_at
		FROM confenge_intel_watch_dedup
		WHERE subscription_id = $1 AND event_identity = $2 AND content_hash = $3`,
		key.SubscriptionID, key.EventIdentity, key.ContentHash).
		Scan(&out.SubscriptionID, &out.EventIdentity, &out.ContentHash, &out.State, &out.Attempts,
			&out.ClaimedAt, &out.LeaseUntil, &out.SentAt, &out.LastError, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

func (r *intelWatchRepository) ExpireStaleDeliveryHandoffs(ctx context.Context, now time.Time) (int, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tag, err := r.db.Exec(ctx, `
		UPDATE confenge_intel_watch_dedup
		SET state = 'AMBIGUOUS', last_error = 'handoff_lease_expired', lease_until = NULL, updated_at = $1
		WHERE state = 'IN_FLIGHT' AND lease_until IS NOT NULL AND lease_until <= $1`, now.UTC())
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}
