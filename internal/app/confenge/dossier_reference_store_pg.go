package confenge

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// postgresDossierStore is the durable manifest-reference authority.
// A nil pool is fail-closed: every method returns ErrDossierStoreUnavailable.
type postgresDossierStore struct {
	db *pgxpool.Pool
}

// NewPostgresDossierStore returns the durable confenge_dossier_references store.
func NewPostgresDossierStore(db *pgxpool.Pool) DossierReferenceStore {
	return &postgresDossierStore{db: db}
}

const dossierReferenceSelect = `
	SELECT id, organization_id, account_id, commercial_action_id, touchpoint_id,
		dossier_id, schema_version, catalog_mode, data_state, as_of,
		content_hash, public_content_hash, producer_sha, artifact_uri,
		deliverable, not_deliverable_reason,
		attached_by, attached_at, delivered_at, delivered_by, delivery_note,
		created_at, updated_at `

func (s *postgresDossierStore) unavailable() error {
	if s == nil || s.db == nil {
		return ErrDossierStoreUnavailable
	}
	return nil
}

func (s *postgresDossierStore) PutDossierReference(ctx context.Context, ref *DossierReference) error {
	if err := s.unavailable(); err != nil {
		return err
	}
	if ref == nil || ref.ID == uuid.Nil {
		return ErrDossierReferenceMissing
	}
	if ref.DeliveredAt != nil || ref.DeliveredBy != nil {
		return ErrDossierDeliveryNotInferred
	}
	// RETURNING reconciles the caller's freshly minted UUID with the row that
	// actually exists: on conflict the database keeps the original id, so
	// without this the caller walks away with an id that matches no row and a
	// later MarkDelivered on it 404s.
	err := s.db.QueryRow(ctx, `
		INSERT INTO confenge_dossier_references (
			id, organization_id, account_id, commercial_action_id, touchpoint_id,
			dossier_id, schema_version, catalog_mode, data_state, as_of,
			content_hash, public_content_hash, producer_sha, artifact_uri,
			deliverable, not_deliverable_reason,
			attached_by, attached_at, created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20
		)
		ON CONFLICT (organization_id, account_id, dossier_id, content_hash) DO UPDATE SET
			commercial_action_id = EXCLUDED.commercial_action_id,
			touchpoint_id = EXCLUDED.touchpoint_id,
			as_of = EXCLUDED.as_of,
			public_content_hash = EXCLUDED.public_content_hash,
			producer_sha = EXCLUDED.producer_sha,
			artifact_uri = EXCLUDED.artifact_uri,
			schema_version = EXCLUDED.schema_version,
			-- deliverable is derived from these two, so updating it without them
			-- makes the row violate its own CHECK and the whole attach fails,
			-- leaving a stale row that still claims DATA_READY.
			catalog_mode = EXCLUDED.catalog_mode,
			data_state = EXCLUDED.data_state,
			deliverable = EXCLUDED.deliverable,
			not_deliverable_reason = EXCLUDED.not_deliverable_reason,
			updated_at = EXCLUDED.updated_at
		RETURNING id, attached_at, created_at, delivered_at, delivered_by, delivery_note`,
		ref.ID, ref.OrganizationID, ref.AccountID, ref.CommercialActionID, ref.TouchpointID,
		ref.DossierID, ref.Schema, ref.CatalogMode, ref.DataState, ref.AsOf,
		ref.ContentHash, ref.PublicContentHash, ref.ProducerSHA, ref.ArtifactURI,
		ref.Deliverable, ref.NotDeliverableReason,
		ref.AttachedBy, ref.AttachedAt.UTC(), ref.CreatedAt.UTC(), ref.UpdatedAt.UTC(),
	).Scan(&ref.ID, &ref.AttachedAt, &ref.CreatedAt, &ref.DeliveredAt, &ref.DeliveredBy, &ref.DeliveryNote)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDossierStoreUnavailable, err)
	}
	return nil
}

func (s *postgresDossierStore) GetDossierReference(ctx context.Context, orgID, id uuid.UUID) (*DossierReference, error) {
	if err := s.unavailable(); err != nil {
		return nil, err
	}
	if orgID == uuid.Nil || id == uuid.Nil {
		return nil, nil
	}
	row := s.db.QueryRow(ctx, dossierReferenceSelect+
		` FROM confenge_dossier_references WHERE organization_id=$1 AND id=$2`, orgID, id)
	return scanDossierReference(row)
}

// ListDossierReferencesForAccounts returns the newest reference per account for
// an explicit set. The org-wide paged read it replaces silently dropped any
// account whose newest reference fell outside the page, taking its
// "not deliverable" warning with it.
func (s *postgresDossierStore) ListDossierReferencesForAccounts(ctx context.Context, orgID uuid.UUID, accountIDs []uuid.UUID) ([]DossierReference, error) {
	if err := s.unavailable(); err != nil {
		return nil, err
	}
	if orgID == uuid.Nil || len(accountIDs) == 0 {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, `SELECT * FROM (`+dossierReferenceSelect+
		` FROM confenge_dossier_references
		   WHERE organization_id=$1 AND account_id = ANY($2)
		   ORDER BY account_id, attached_at DESC) t`, orgID, accountIDs)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDossierStoreUnavailable, err)
	}
	defer rows.Close()
	newest := map[uuid.UUID]DossierReference{}
	for rows.Next() {
		ref, serr := scanDossierReference(rows)
		if serr != nil {
			return nil, serr
		}
		if ref == nil {
			continue
		}
		if cur, ok := newest[ref.AccountID]; !ok || ref.AttachedAt.After(cur.AttachedAt) {
			newest[ref.AccountID] = *ref
		}
	}
	if rerr := rows.Err(); rerr != nil {
		return nil, fmt.Errorf("%w: %v", ErrDossierStoreUnavailable, rerr)
	}
	out := make([]DossierReference, 0, len(newest))
	for _, ref := range newest {
		out = append(out, ref)
	}
	return out, nil
}

func (s *postgresDossierStore) ListDossierReferences(ctx context.Context, orgID, accountID uuid.UUID, limit int) ([]DossierReference, error) {
	if err := s.unavailable(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q := dossierReferenceSelect + ` FROM confenge_dossier_references WHERE organization_id=$1`
	args := []any{orgID}
	n := 2
	if accountID != uuid.Nil {
		q += ` AND account_id=$2`
		args = append(args, accountID)
		n = 3
	}
	q += ` ORDER BY attached_at DESC LIMIT $` + strconv.Itoa(n)
	args = append(args, limit)
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDossierStoreUnavailable, err)
	}
	defer rows.Close()
	var out []DossierReference
	for rows.Next() {
		ref, serr := scanDossierReference(rows)
		if serr != nil {
			return nil, serr
		}
		if ref != nil {
			out = append(out, *ref)
		}
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("%w: %v", ErrDossierStoreUnavailable, rows.Err())
	}
	return out, nil
}

// SetDossierReferenceDelivered writes the human handoff. The WHERE clause keeps
// the guard in SQL too: a non-deliverable reference can never be marked.
func (s *postgresDossierStore) SetDossierReferenceDelivered(ctx context.Context, ref *DossierReference) error {
	if err := s.unavailable(); err != nil {
		return err
	}
	if ref == nil || ref.ID == uuid.Nil {
		return ErrDossierReferenceMissing
	}
	if !ref.Delivered() || ref.DeliveredBy == nil {
		return ErrDossierDeliveryNotInferred
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE confenge_dossier_references
		SET delivered_at = COALESCE(delivered_at, $3),
		    delivered_by = COALESCE(delivered_by, $4),
		    delivery_note = CASE WHEN delivery_note = '' THEN $5 ELSE delivery_note END,
		    updated_at = $3
		WHERE id=$1 AND organization_id=$2 AND deliverable`,
		ref.ID, ref.OrganizationID, ref.DeliveredAt.UTC(), *ref.DeliveredBy, ref.DeliveryNote,
	)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDossierStoreUnavailable, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrDossierNotDeliverable
	}
	return nil
}

func scanDossierReference(row scannableRow) (*DossierReference, error) {
	var ref DossierReference
	err := row.Scan(
		&ref.ID, &ref.OrganizationID, &ref.AccountID, &ref.CommercialActionID, &ref.TouchpointID,
		&ref.DossierID, &ref.Schema, &ref.CatalogMode, &ref.DataState, &ref.AsOf,
		&ref.ContentHash, &ref.PublicContentHash, &ref.ProducerSHA, &ref.ArtifactURI,
		&ref.Deliverable, &ref.NotDeliverableReason,
		&ref.AttachedBy, &ref.AttachedAt, &ref.DeliveredAt, &ref.DeliveredBy, &ref.DeliveryNote,
		&ref.CreatedAt, &ref.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDossierStoreUnavailable, err)
	}
	return &ref, nil
}

type scannableRow interface {
	Scan(dest ...any) error
}
