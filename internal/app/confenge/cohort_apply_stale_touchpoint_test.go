package confenge

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

func staleCohortMember(t *testing.T, accID, candID uuid.UUID, mailbox string) FrozenCohortMember {
	t.Helper()
	return FrozenCohortMember{
		AccountID:   accID,
		AccountRef:  "cnpj:33333333000193",
		CandidateID: candID,
		Mailbox:     mailbox,
		RouteClass:  RouteClassGenericCompany,
		Subject:     "objeto: contrato publicado",
		BodyText:    "Olá, equipe,\n\nSou da CONFENGE.\n\nobjeto: contrato publicado.\n\nVocê consegue me indicar a pessoa responsável?",
	}
}

// A CANCELLED row left by an older composer version must not block a fresh
// frozen cohort: it is superseded by a new initial touch, not reused.
func TestStaleCancelledTouchpointIsSupersededNotReused(t *testing.T) {
	accID, candID := uuid.New(), uuid.New()
	orgID := uuid.New()
	mailbox := "contato@empresa3.com.br"

	repo := newMemRepo()
	seedTouchpoint(t, repo, &models.OutreachTouchpoint{
		ID: uuid.New(), OrganizationID: orgID, AccountID: accID, Ordinal: 1,
		Purpose: models.TouchpointPurposeInitial, Recipient: mailbox,
		State: models.TouchpointCancelled, StopReason: "composer_version_stale",
	})

	tp, create, err := locateOrBuildTouchpoint(context.Background(), repo, orgID, staleCohortMember(t, accID, candID, mailbox), time.Now().UTC())
	if err != nil {
		t.Fatalf("stale cancelled row must not block: %v", err)
	}
	if !create {
		t.Fatal("expected a fresh touchpoint, got reuse of the cancelled row")
	}
	if tp.State != models.TouchpointNeedsReview {
		t.Fatalf("state = %s, want NEEDS_REVIEW", tp.State)
	}
}

// An already-dispatched route must still block, so a frozen cohort cannot
// produce a second send to the same mailbox.
func TestSentTouchpointBlocksReauthorization(t *testing.T) {
	accID, candID := uuid.New(), uuid.New()
	orgID := uuid.New()
	mailbox := "contato@empresa3.com.br"

	for _, st := range []string{models.TouchpointSent, models.TouchpointQueued} {
		repo := newMemRepo()
		seedTouchpoint(t, repo, &models.OutreachTouchpoint{
			ID: uuid.New(), OrganizationID: orgID, AccountID: accID, Ordinal: 1,
			Purpose: models.TouchpointPurposeInitial, Recipient: mailbox, State: st,
		})
		_, _, err := locateOrBuildTouchpoint(context.Background(), repo, orgID, staleCohortMember(t, accID, candID, mailbox), time.Now().UTC())
		if err == nil || !strings.Contains(err.Error(), "route_already_dispatched") {
			t.Fatalf("state %s: err = %v, want route_already_dispatched", st, err)
		}
	}
}

// A recipient-level stop still blocks regardless of stop-reason text.
func TestSuppressedTouchpointBlocksReauthorization(t *testing.T) {
	accID, candID := uuid.New(), uuid.New()
	orgID := uuid.New()
	mailbox := "contato@empresa3.com.br"

	for _, st := range []string{models.TouchpointDNC, models.TouchpointBounced, models.TouchpointRejected} {
		repo := newMemRepo()
		seedTouchpoint(t, repo, &models.OutreachTouchpoint{
			ID: uuid.New(), OrganizationID: orgID, AccountID: accID, Ordinal: 1,
			Purpose: models.TouchpointPurposeInitial, Recipient: mailbox, State: st,
		})
		_, _, err := locateOrBuildTouchpoint(context.Background(), repo, orgID, staleCohortMember(t, accID, candID, mailbox), time.Now().UTC())
		if err == nil || !strings.Contains(err.Error(), "route_suppressed") {
			t.Fatalf("state %s: err = %v, want route_suppressed", st, err)
		}
	}
}

// A live pre-send draft is still reused rather than duplicated.
func TestPreSendTouchpointIsReused(t *testing.T) {
	accID, candID := uuid.New(), uuid.New()
	orgID := uuid.New()
	mailbox := "contato@empresa3.com.br"
	want := uuid.New()

	repo := newMemRepo()
	seedTouchpoint(t, repo, &models.OutreachTouchpoint{
		ID: want, OrganizationID: orgID, AccountID: accID, Ordinal: 1,
		Purpose: models.TouchpointPurposeInitial, Recipient: mailbox,
		State: models.TouchpointDrafted,
	})
	tp, create, err := locateOrBuildTouchpoint(context.Background(), repo, orgID, staleCohortMember(t, accID, candID, mailbox), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if create || tp.ID != want {
		t.Fatalf("expected reuse of %s, got create=%v id=%s", want, create, tp.ID)
	}
}

func seedTouchpoint(t *testing.T, repo *memRepo, tp *models.OutreachTouchpoint) {
	t.Helper()
	if err := repo.InsertTouchpoint(context.Background(), tp); err != nil {
		t.Fatalf("seed touchpoint: %v", err)
	}
}
