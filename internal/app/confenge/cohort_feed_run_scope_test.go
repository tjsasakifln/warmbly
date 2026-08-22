package confenge

import (
	"testing"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

// A PG-bound freeze must still carry feed identity, otherwise the live GO
// review reports feed_identity_missing and the cohort can never reach
// GO_FOR_CONTROLLED_EMAIL_PILOT.
func TestOrgBoundCohortWithoutFeedIdentityFailsReleaseComparison(t *testing.T) {
	auth := &BoundedCohortAuthorization{
		ID:                  uuid.New(),
		RepositorySHA:       "sha-1",
		FeedSchemaVersion:   "confenge.outreach.v1",
		PolicyVersion:       BoundedCohortPolicyV1,
		ComposerVersion:     ComposerVersion,
		EvidenceVersion:     DefaultEvidenceVersion,
		RecipientSetHash:    "recipients-1",
		CohortHash:          "cohort-1",
		MaxDailyVolume:      50,
		AllowedRouteClasses: []string{RouteClassDirectPerson, RouteClassRoleOrDepartment, RouteClassGenericCompany, RouteClassPublicCompanyFreemail},
		FrozenManifest: &FrozenCohortSnapshot{
			Members: []FrozenCohortMember{{Mailbox: "a@example.com", RouteClass: RouteClassGenericCompany}},
		},
	}

	want := expectedReleaseFromGrant(auth)
	got := want
	// Live collection derives FeedHash only from the snapshot's identity fields.
	got.FeedHash = ""

	cmp := CompareControlledEmailRelease(want, got)
	var found bool
	for _, ch := range cmp.Checks {
		if ch.Name == "feed_hash" {
			found = true
			if ch.State.IsPass() {
				t.Fatalf("feed_hash must not PASS when live identity is empty")
			}
			if ch.Reason != "feed_identity_missing" {
				t.Fatalf("reason = %q, want feed_identity_missing", ch.Reason)
			}
		}
	}
	if !found {
		t.Fatal("feed_hash check missing from comparison")
	}
	if cmp.Verdict.Verdict == ReleaseGOForControlledEmailPilot {
		t.Fatal("missing feed identity must not reach GO_FOR_CONTROLLED_EMAIL_PILOT")
	}
}

// Freezing from Postgres scoped to a feed run keeps the real account/candidate
// ids AND the feed identity, so the same snapshot can pass review and dispatch.
func TestPrepareFromOrgAccountsCarriesFeedIdentityAndDBIdentifiers(t *testing.T) {
	unk := true
	accID, candID := uuid.New(), uuid.New()
	acc := cohortAccount("acc-generic", "33333333000193", "Contrato vigente")
	acc.ID = accID
	acc.SourceRunID = "run-fresh-1"
	accounts := []CohortAccountInput{{
		Account: acc,
		Candidates: []models.OutreachContactCandidate{{
			ID:              candID,
			AccountID:       accID,
			Email:           "contato@empresa3.com.br",
			MailboxPurpose:  "GENERIC_CONTACT",
			OwnershipStatus: "COMPANY_OWNED",
			DiscoveryJSON:   eligibleDisc(t, RouteClassGenericCompany, true, controlledDiscovery{PersonUnknown: &unk}),
		}},
		Source: "extra-cli",
	}}

	snap, err := PrepareControlledCohort(accounts, CohortPrepareOptions{
		RepositorySHA:  "sha-1",
		FeedIdentity:   "run-fresh-1",
		Limit:          50,
		MaxDailyVolume: 50,
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if snap.FeedIdentity != "run-fresh-1" {
		t.Fatalf("feed identity = %q, want run-fresh-1", snap.FeedIdentity)
	}
	if len(snap.Members) != 1 {
		t.Fatalf("members = %d, want 1", len(snap.Members))
	}
	m := snap.Members[0]
	if m.AccountID != accID {
		t.Fatalf("account id = %s, want %s", m.AccountID, accID)
	}
	if m.CandidateID != candID {
		t.Fatalf("candidate id = %s, want %s", m.CandidateID, candID)
	}
}
