package confenge

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

// carryForward ages the account onto an older feed run while leaving every
// commercial and semantic binding intact.
func carryForward(f *delegatedValidationFixture, observedAt time.Time) {
	f.account.SourceRunID = "run-older"
	f.account.ContractorRoleSourceRunID = "run-older"
	f.account.ContractorRoleObservedAt = &observedAt
	f.entry.EvidenceObservedAt = observedAt
	f.manifest.Entries = []DelegatedFirstTouchEntry{f.entry}
}

// A QUALIFIED account emitted by an earlier run is still commercially proven,
// so it must stay admissible however old that run and its observation are.
func TestDelegatedCarriedForwardQualifiedAccountStaysAdmissible(t *testing.T) {
	f := newDelegatedValidationFixture(t, RouteClassGenericCompany, "contato@empresa.example")
	carryForward(&f, time.Now().UTC().Add(-400*24*time.Hour).Truncate(time.Second))
	if blockers := f.validate(); len(blockers) != 0 {
		t.Fatalf("carried-forward qualified account blocked: %v", blockers)
	}
}

// The role observation still cannot be invented in the future.
func TestDelegatedFutureDatedRoleObservationStillBlocks(t *testing.T) {
	f := newDelegatedValidationFixture(t, RouteClassGenericCompany, "contato@empresa.example")
	carryForward(&f, time.Now().UTC().Add(48*time.Hour).Truncate(time.Second))
	if blockers := f.validate(); !delegatedTestContains(blockers, "contractor_role_evidence_stale") {
		t.Fatalf("future-dated role observation admitted: %v", blockers)
	}
}

// Losing the three-year fact still blocks a carried-forward account: the run id
// is provenance, the qualification is the gate.
func TestDelegatedCarriedForwardUnqualifiedAccountStillBlocks(t *testing.T) {
	f := newDelegatedValidationFixture(t, RouteClassGenericCompany, "contato@empresa.example")
	now := f.service.now()
	carryForward(&f, now.Add(-400*24*time.Hour).Truncate(time.Second))
	expired := testRootQualification(f.account.CNPJRoot, now.AddDate(-3, 0, -1))
	applyCommercialQualificationToAccount(f.account, &expired, now)
	if blockers := f.validate(); !delegatedTestContains(blockers, ReasonQualificationExpired) {
		t.Fatalf("expired qualification admitted on carry-forward: %v", blockers)
	}
}

// Backfill posture: an account with a valid three-year qualification must be
// admissible before the feed row carries a V2 payload, exactly as transport is.
func TestDelegatedAbsentPopulationAttestationFallsThroughToAccount(t *testing.T) {
	f := newDelegatedValidationFixture(t, RouteClassGenericCompany, "contato@empresa.example")
	f.repo.feedSync[f.orgID].CommercialAuthorityV2JSON = nil
	if blockers := f.validate(); len(blockers) != 0 {
		t.Fatalf("backfilled qualified account blocked with no population attestation: %v", blockers)
	}
	f.account.CommercialQualificationState = ""
	if blockers := f.validate(); !delegatedTestContains(blockers, ReasonQualificationMissing) {
		t.Fatalf("missing account qualification was not fail-closed: %v", blockers)
	}
}

// A present-but-unqualified population attestation still blocks admission.
func TestDelegatedRevokedPopulationAttestationBlocksAdmission(t *testing.T) {
	f := newDelegatedValidationFixture(t, RouteClassGenericCompany, "contato@empresa.example")
	feed := f.repo.feedSync[f.orgID]
	qualification := testRootQualification(f.account.CNPJRoot, f.service.now().AddDate(-1, 0, 0))
	stampFeedStateWithV2(feed, []RootQualification{qualification})
	var payload FeedCommercialAuthorityV2
	if err := json.Unmarshal(feed.CommercialAuthorityV2JSON, &payload); err != nil {
		t.Fatal(err)
	}
	payload.State = CommercialRevoked
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	feed.CommercialAuthorityV2JSON = raw
	if blockers := f.validate(); !delegatedTestContains(blockers, ReasonQualificationRevoked) {
		t.Fatalf("revoked population attestation admitted: %v", blockers)
	}
}

// Transport asks the same question as admission, so both must answer the same
// way for an account carried forward from an older run.
func TestDelegatedCarriedForwardQualifiedAccountStaysTransportable(t *testing.T) {
	f := newDelegatedValidationFixture(t, RouteClassGenericCompany, "contato@empresa.example")
	f.service.cfg.FeedSyncEnabled = true
	f.service.cfg.DelegatedFirstTouchEnabled = true
	carryForward(&f, time.Now().UTC().Add(-400*24*time.Hour).Truncate(time.Second))
	f.repo.byID[f.account.ID] = f.account
	if err := f.service.assertCommercialAuthorityForTransport(context.Background(), f.orgID, f.account); err != nil {
		t.Fatalf("carried-forward qualified account blocked at transport: %v", err)
	}
	// The same account outside the window must fail closed.
	expired := testRootQualification(f.account.CNPJRoot, f.service.now().AddDate(-3, 0, -1))
	applyCommercialQualificationToAccount(f.account, &expired, f.service.now())
	if err := f.service.assertCommercialAuthorityForTransport(context.Background(), f.orgID, f.account); err == nil {
		t.Fatal("expired qualification transported")
	}
}

// Contact freshness is proven when the decision is minted; the runway then
// ships days later and must not re-age the same evidence against now.
func TestDelegatedContactEvidenceWindowIsAnchoredToTheDecision(t *testing.T) {
	decidedAt := time.Now().UTC().Add(-40 * 24 * time.Hour)
	observedAt := decidedAt.Add(-29 * 24 * time.Hour)
	source := DelegatedWebSource{URL: "https://empresa.example/contato", Kind: "PUBLIC_COMPANY_SOURCE", Supports: "COMPANY_MAILBOX", ObservedAt: observedAt}
	if !delegatedWebSourceAllowed(source, decidedAt) {
		t.Fatal("evidence fresh at the decision instant was rejected")
	}
	if delegatedWebSourceAllowed(source, time.Now().UTC()) {
		t.Fatal("a moving now must not be the reference at admission time either")
	}
	if !delegatedWebSourceTransportable(source, time.Now().UTC()) {
		t.Fatal("an approval well inside the runway ceiling was cancelled")
	}
	stale := source
	stale.ObservedAt = time.Now().UTC().Add(-(delegatedContactEvidenceCeiling + 24*time.Hour))
	if delegatedWebSourceTransportable(stale, time.Now().UTC()) {
		t.Fatal("a decision parked past the absolute ceiling was still transportable")
	}
	future := source
	future.ObservedAt = time.Now().UTC().Add(time.Hour)
	if delegatedWebSourceTransportable(future, time.Now().UTC()) {
		t.Fatal("future-dated contact evidence was transportable")
	}
}

// The runway horizon must fit inside the contact-evidence ceiling, otherwise a
// legal approval is cancelled before its own due date.
func TestDelegatedContactEvidenceCeilingCoversTheRunway(t *testing.T) {
	if delegatedContactEvidenceCeiling <= time.Duration(MaxDelegatedFirstTouchRunwayDays)*24*time.Hour {
		t.Fatalf("ceiling %s does not cover a %d day runway", delegatedContactEvidenceCeiling, MaxDelegatedFirstTouchRunwayDays)
	}
}

func TestDelegatedEvidenceRowsCurrentUsesTheDecisionInstant(t *testing.T) {
	importID := uuid.New()
	decidedAt := time.Now().UTC().Add(-40 * 24 * time.Hour)
	consultedAt := decidedAt.Add(-10 * 24 * time.Hour)
	rows := []models.OutreachEvidence{{
		ID: uuid.New(), SourceEvidenceID: "contract-1", EpistemicClass: models.OutreachEpistemicConfirmedFact,
		ConsultedAt: &consultedAt, LastImportRunID: &importID,
	}}
	now := time.Now().UTC()
	if !delegatedEvidenceRowsCurrent(rows, []string{"contract-1"}, &importID, decidedAt, now) {
		t.Fatal("evidence current at the decision instant was treated as drifted")
	}
	if delegatedEvidenceRowsCurrent(rows, []string{"contract-1"}, &importID, now, now) {
		t.Fatal("admission must still evaluate the window against now")
	}
	old := now.Add(-(delegatedContactEvidenceCeiling + 24*time.Hour))
	rows[0].ConsultedAt = &old
	if delegatedEvidenceRowsCurrent(rows, []string{"contract-1"}, &importID, old, now) {
		t.Fatal("evidence past the absolute ceiling was accepted")
	}
}

// Acquisition provenance must not appear in the pre-SMTP or admission gates.
func TestDelegatedFirstTouchGatesCarryNoRunIDRevocation(t *testing.T) {
	b, err := os.ReadFile("delegated_first_touch.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, revocation := range []string{
		"acc.SourceRunID != manifest.SourceRunID",
		"acc.SourceRunID != got.SourceRunID",
		"acc.ContractorRoleSourceRunID != manifest.SourceRunID",
		"acc.ContractorRoleSourceRunID != got.SourceRunID",
		"feedState.LastRunID != got.SourceRunID",
		"got.SourceExpiresAt.Equal(feedState.SourceExpiresAt",
		// The attestation revision is import provenance for the same reason the
		// run id and snapshot hash are: a republish over the same members moves
		// the hash and count without revoking anything. Revocation is carried
		// per account by the three-year qualification.
		"got.TargetMembershipHash != feedState.TargetMembershipHash",
		"got.TargetMembershipCount != feedState.TargetMembershipCount",
	} {
		if strings.Contains(s, revocation) {
			t.Fatalf("%s revokes a bound decision on acquisition provenance", revocation)
		}
	}
	for _, binding := range []string{
		"acc.ContractorRoleEvidenceHash != got.EvidenceHash",
		"got.ContentHash != tp.ContentHash",
		"AccountCommercialQualification(acc, now); !qual.AllowsTransport()",
	} {
		if !strings.Contains(s, binding) {
			t.Fatalf("%s is a content/binding integrity term and must stay", binding)
		}
	}
}

// The autorun reservoir must agree with pg_outreach_backlog and must never drop
// an account silently for a run-id mismatch.
func TestDelegatedReservoirSelectsCarriedForwardQualifiedAccounts(t *testing.T) {
	for _, file := range []string{"delegated_first_touch_worker.go", "delegated_first_touch_runway.go"} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if !strings.Contains(s, "confenge_commercially_qualified") ||
			!strings.Contains(s, "outreach_feed_committed_runs") {
			t.Fatalf("%s: dynamic qualification or committed lineage is missing", file)
		}
		if strings.Contains(s, "c.last_import_run_id=a.last_import_run_id") {
			t.Fatalf("%s: run-id equality drops the recipient with no evidence it became invalid", file)
		}
		for _, safety := range []string{"NOT c.blocked", "NOT c.do_not_contact", "NOT c.bounced", "upper(c.route_suppression) IN ('','NONE')"} {
			if !strings.Contains(s, safety) {
				t.Fatalf("%s: recipient safety predicate %s was weakened", file, safety)
			}
		}
	}
}
