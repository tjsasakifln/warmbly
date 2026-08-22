package confenge

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// DossierAttachInput binds one confenge-dossier/1.0 manifest to an account.
// ManifestJSON is the raw manifest.json: passing dossier.json is refused.
type DossierAttachInput struct {
	AccountID          uuid.UUID
	CommercialActionID *uuid.UUID
	TouchpointID       *uuid.UUID
	ManifestJSON       []byte
	ArtifactURI        string
}

// WireDossierReferences installs the durable manifest-reference store.
func (s *service) WireDossierReferences(store DossierReferenceStore) {
	if s == nil || store == nil {
		return
	}
	s.dossierStore = store
}

// AttachDossierReference records that a dossier exists for an account. It is
// card metadata: it never queues, approves, or dispatches anything.
func (s *service) AttachDossierReference(ctx context.Context, orgID, actorID uuid.UUID, in DossierAttachInput) (*DossierReference, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	if s == nil || s.dossierStore == nil {
		return nil, errx.New(errx.Internal, ErrDossierStoreUnavailable.Error())
	}
	if actorID == uuid.Nil {
		return nil, errx.New(errx.BadRequest, ErrDossierHumanActor.Error())
	}
	manifest, err := ParseDossierManifest(in.ManifestJSON)
	if err != nil {
		if errors.Is(err, ErrDossierPrivateBody) {
			return nil, errx.New(errx.Unprocessable, err.Error())
		}
		return nil, errx.New(errx.BadRequest, err.Error())
	}
	acc, aerr := s.repo.GetAccount(ctx, orgID, in.AccountID)
	if aerr != nil || acc == nil {
		return nil, errx.New(errx.NotFound, "account not found")
	}
	ref := &DossierReference{
		OrganizationID:     orgID,
		AccountID:          in.AccountID,
		CommercialActionID: in.CommercialActionID,
		TouchpointID:       in.TouchpointID,
		DossierID:          manifest.DossierID,
		Schema:             manifest.Schema,
		CatalogMode:        manifest.CatalogMode,
		DataState:          manifest.DataState,
		AsOf:               manifest.AsOf,
		ContentHash:        manifest.ContentHash,
		PublicContentHash:  manifest.PublicContentHash,
		ProducerSHA:        manifest.ProducerSHA,
		ArtifactURI:        in.ArtifactURI,
		AttachedBy:         actorID,
	}
	if nerr := NormalizeDossierReference(ref, time.Now().UTC()); nerr != nil {
		return nil, errx.New(errx.BadRequest, nerr.Error())
	}
	if perr := s.dossierStore.PutDossierReference(ctx, ref); perr != nil {
		return nil, errx.New(errx.Internal, "persist dossier reference: "+perr.Error())
	}
	s.auditDossier(ctx, orgID, actorID, ref, "dossier_reference_attach")
	return ref, nil
}

// ListDossierReferences returns the manifest references for an org, optionally
// narrowed to one account. Pass uuid.Nil for the whole org.
func (s *service) ListDossierReferences(ctx context.Context, orgID, accountID uuid.UUID) ([]DossierReference, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	if s == nil || s.dossierStore == nil {
		return nil, errx.New(errx.Internal, ErrDossierStoreUnavailable.Error())
	}
	refs, err := s.dossierStore.ListDossierReferences(ctx, orgID, accountID, 200)
	if err != nil {
		return nil, errx.New(errx.Internal, "list dossier references: "+err.Error())
	}
	return refs, nil
}

// MarkDossierReferenceDelivered records the human handoff of the dossier.
// Nothing else in Warmbly may set it: delivery is never inferred.
func (s *service) MarkDossierReferenceDelivered(ctx context.Context, orgID, actorID, refID uuid.UUID, note string) (*DossierReference, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	if s == nil || s.dossierStore == nil {
		return nil, errx.New(errx.Internal, ErrDossierStoreUnavailable.Error())
	}
	if actorID == uuid.Nil {
		return nil, errx.New(errx.BadRequest, ErrDossierHumanActor.Error())
	}
	ref, err := s.dossierStore.GetDossierReference(ctx, orgID, refID)
	if err != nil {
		return nil, errx.New(errx.Internal, "load dossier reference: "+err.Error())
	}
	if ref == nil {
		return nil, errx.New(errx.NotFound, ErrDossierReferenceMissing.Error())
	}
	if merr := MarkDossierDelivered(ref, actorID, time.Now().UTC(), note); merr != nil {
		if errors.Is(merr, ErrDossierNotDeliverable) {
			return nil, errx.New(errx.Conflict, merr.Error())
		}
		return nil, errx.New(errx.BadRequest, merr.Error())
	}
	if serr := s.dossierStore.SetDossierReferenceDelivered(ctx, ref); serr != nil {
		return nil, errx.New(errx.Internal, "persist dossier delivery: "+serr.Error())
	}
	s.auditDossier(ctx, orgID, actorID, ref, "dossier_reference_delivered")
	return ref, nil
}

// auditDossier reuses the outreach_account entity so the realtime spine already
// invalidates the confenge queries without a new frontend mapping.
func (s *service) auditDossier(ctx context.Context, orgID, actorID uuid.UUID, ref *DossierReference, action string) {
	if s == nil || s.audit == nil || ref == nil {
		return
	}
	accountID := ref.AccountID
	delivered := "false"
	if ref.Delivered() {
		delivered = "true"
	}
	deliverable := "false"
	if ref.Deliverable {
		deliverable = "true"
	}
	s.audit.LogAction(ctx, orgID, actorID, models.AuditActionUpdate, models.AuditEntityOutreachAccount, &accountID, "", "",
		map[string]string{
			"action":       action,
			"dossier_id":   ref.DossierID,
			"data_state":   ref.DataState,
			"catalog_mode": ref.CatalogMode,
			"deliverable":  deliverable,
			"delivered":    delivered,
		},
		map[string]string{
			"content_hash":  ref.ContentHash,
			"as_of":         ref.AsOf,
			"reference_id":  ref.ID.String(),
			"delivery_note": ref.DeliveryNote,
		},
	)
}

// decorateDossierBadges stamps the manifest badge onto the operator cards.
func (s *service) decorateDossierBadges(ctx context.Context, orgID uuid.UUID, view *TodayView) {
	if s == nil || s.dossierStore == nil || view == nil {
		return
	}
	refs, err := s.dossierStore.ListDossierReferences(ctx, orgID, uuid.Nil, 500)
	if err != nil || len(refs) == 0 {
		return
	}
	ApplyDossierBadges(view, refs)
}
