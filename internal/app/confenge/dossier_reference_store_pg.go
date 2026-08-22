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
	_, err := s.db.Exec(ctx, `
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
			deliverable = EXCLUDED.deliverable,
			not_deliverable_reason = EXCLUDED.not_deliverable_reason,
			updated_at = EXCLUDED.updated_at`,
		ref.ID, ref.OrganizationID, ref.AccountID, ref.CommercialActionID, ref.TouchpointID,
		ref.DossierID, ref.Schema, ref.CatalogMode, ref.DataState, ref.AsOf,
		ref.ContentHash, ref.PublicContentHash, ref.ProducerSHA, ref.ArtifactURI,
		ref.Deliverable, ref.NotDeliverableReason,
		ref.AttachedBy, ref.AttachedAt.UTC(), ref.CreatedAt.UTC(), ref.UpdatedAt.UTC(),
	)
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
