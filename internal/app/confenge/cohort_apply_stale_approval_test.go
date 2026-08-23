package confenge

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)


// cleanCohortMember carries copy that passes admission QA, so a test that means
// to exercise the approval gate is not silently short-circuited by copy QA.
func cleanCohortMember(accID, candID uuid.UUID, mailbox string) FrozenCohortMember {
	return FrozenCohortMember{
		AccountID:   accID,
		AccountRef:  "cnpj:33333333000193",
		CandidateID: candID,
		Mailbox:     mailbox,
		RouteClass:  RouteClassGenericCompany,
		Subject:     "recuperação estrutural da ponte",
		BodyText:    "Olá, equipe,\n\nSou da CONFENGE.\n\ncontratação pública: recuperação estrutural da ponte sobre o Rio Sapucaí.\n\nQueria falar com quem acompanha a carteira de contratos públicos por aí. Você consegue me indicar a pessoa responsável?",
	}
}

// An APPROVED touchpoint carries an authorization for the exact copy a human
// read. When a frozen cohort stamps different copy onto that row, the approval
// must not survive: otherwise an approval of old copy silently authorizes new
// copy. The existing drift guard only fires when the row already carries both a
// ContentHash and a BodyText, so a legacy APPROVED row with neither slipped
// through and kept its approval.
func TestApprovedTouchpointCannotCarryApprovalOntoNewCopy(t *testing.T) {
	orgID, accID, candID := uuid.New(), uuid.New(), uuid.New()
	mailbox := "contato@empresa-stale.com.br"
	approver := uuid.New()
	approvedAt := time.Now().UTC().Add(-72 * time.Hour)

	cases := []struct {
		name string
		seed func(tp *models.OutreachTouchpoint)
	}{
		{
			// The legacy hole: approved, but nothing recorded to compare against.
			name: "approved_without_recorded_content",
			seed: func(tp *models.OutreachTouchpoint) {
				tp.BodyText = ""
				tp.Subject = ""
				tp.ContentHash = ""
				tp.ApprovedContentHash = ""
			},
		},
		{
			// Approved for materially different copy.
			name: "approved_for_different_copy",
			seed: func(tp *models.OutreachTouchpoint) {
				tp.Subject = "assunto antigo"
				tp.BodyText = "Olá, equipe,\n\nSou da CONFENGE.\n\ncopy antiga aprovada.\n\nVocê consegue me indicar a pessoa responsável?"
				RecomputeContentHash(tp)
				tp.ApprovedContentHash = tp.ContentHash
			},
		},
		{
			// Approved under a superseded composer: the hash embeds the composer
			// version, so the stored approval cannot match current output.
			name: "approved_under_previous_composer",
			seed: func(tp *models.OutreachTouchpoint) {
				tp.Subject = "assunto"
				tp.BodyText = "Olá, equipe,\n\nSou da CONFENGE.\n\nfato.\n\nVocê consegue me indicar a pessoa responsável?"
				tp.ContentHash = "hash-computed-by-confenge.composer.v3"
				tp.ApprovedContentHash = "hash-computed-by-confenge.composer.v3"
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newMemRepo()
			tp := &models.OutreachTouchpoint{
				ID: uuid.New(), OrganizationID: orgID, AccountID: accID, Ordinal: 1,
				Purpose: models.TouchpointPurposeInitial, Recipient: mailbox,
				State: models.TouchpointApproved,
				ApprovedBy: &approver, ApprovedAt: &approvedAt,
				AuthorizationMode: AuthorizationModeHumanTouchpoint,
			}
			tc.seed(tp)
			seedTouchpoint(t, repo, tp)

			member := cleanCohortMember(accID, candID, mailbox)
			snap := &FrozenCohortSnapshot{Members: []FrozenCohortMember{member}}
			works, fails := planCohortApply(context.Background(), repo, orgID, snap, time.Now().UTC())

			// Either outcome is acceptable governance: refuse the member, or
			// demote it for re-review. What must never happen is that the row
			// stays APPROVED while carrying the newly stamped copy.
			for _, w := range works {
				if w.tp.State == models.TouchpointApproved {
					t.Fatalf("approval survived onto new copy: state=%s approved_hash=%q content_hash=%q",
						w.tp.State, w.tp.ApprovedContentHash, w.tp.ContentHash)
				}
				if w.tp.ApprovedContentHash != "" && w.tp.ApprovedContentHash != w.tp.ContentHash {
					t.Fatalf("stale approved_content_hash %q retained against content %q",
						w.tp.ApprovedContentHash, w.tp.ContentHash)
				}
				if w.tp.ApprovedBy != nil || w.tp.ApprovedAt != nil {
					t.Fatalf("approver identity survived a content change: by=%v at=%v", w.tp.ApprovedBy, w.tp.ApprovedAt)
				}
			}
			t.Logf("works=%d fails=%d", len(works), len(fails))
		})
	}
}

// A touchpoint approved for exactly this copy keeps its approval: the gate must
// not force the founder to re-approve identical text.
func TestApprovalSurvivesWhenContentIsIdentical(t *testing.T) {
	orgID, accID, candID := uuid.New(), uuid.New(), uuid.New()
	mailbox := "contato@empresa-igual.com.br"
	approver := uuid.New()
	approvedAt := time.Now().UTC().Add(-time.Hour)
	member := cleanCohortMember(accID, candID, mailbox)

	repo := newMemRepo()
	tp := &models.OutreachTouchpoint{
		ID: uuid.New(), OrganizationID: orgID, AccountID: accID, Ordinal: 1,
		Purpose: models.TouchpointPurposeInitial, Recipient: mailbox,
		State: models.TouchpointApproved, Subject: member.Subject, BodyText: member.BodyText,
		ApprovedBy: &approver, ApprovedAt: &approvedAt,
		AuthorizationMode: AuthorizationModeHumanTouchpoint,
	}
	RecomputeContentHash(tp)
	tp.ApprovedContentHash = tp.ContentHash
	seedTouchpoint(t, repo, tp)

	snap := &FrozenCohortSnapshot{Members: []FrozenCohortMember{member}}
	works, fails := planCohortApply(context.Background(), repo, orgID, snap, time.Now().UTC())
	if len(works) != 1 {
		t.Fatalf("identical copy must still plan: works=%d fails=%v", len(works), fails)
	}
	if works[0].tp.State != models.TouchpointApproved {
		t.Fatalf("approval for identical copy must survive, got state=%s", works[0].tp.State)
	}
}
