package proposal

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCountSnapshotRevisionsAlternativesOriginAndStages(t *testing.T) {
	sentAt := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	orgID := uuid.MustParse("11111111-1111-4111-8111-000000000047")
	revisedID := uuid.MustParse("21111111-1111-4111-8111-000000000047")
	altAID := uuid.MustParse("31111111-1111-4111-8111-000000000047")
	altBID := uuid.MustParse("41111111-1111-4111-8111-000000000047")
	proposals := []Proposal{
		{OrganizationID: orgID, ProposalID: revisedID, ProposalVersion: 1, OpportunityID: "opp-revised", SentAt: &sentAt, DecisionState: StateAccepted, OriginClass: OriginDemonstratedInbound, OriginEvidence: "lead:one"},
		{OrganizationID: orgID, ProposalID: revisedID, ProposalVersion: 2, OpportunityID: "opp-revised", SentAt: &sentAt, DecisionState: StateSent, OriginClass: OriginOutboundAssisted, OriginEvidence: "campaign:one"},
		{OrganizationID: orgID, ProposalID: altAID, ProposalVersion: 1, OpportunityID: "opp-alt", ScopeID: "scope-alt", AlternativeGroupID: "group-one", CanonicalAlternative: true, SentAt: &sentAt, DecisionState: StateAccepted},
		{OrganizationID: orgID, ProposalID: altBID, ProposalVersion: 1, OpportunityID: "opp-alt", ScopeID: "scope-alt", AlternativeGroupID: "group-one", SentAt: &sentAt, DecisionState: StateSent},
	}
	snapshot := CountSnapshot(proposals)
	if snapshot.EmittedCount != 2 || snapshot.AcceptedCount != 1 {
		t.Fatalf("stages were summed or revisions/alternatives inflated: %+v", snapshot)
	}
	if snapshot.ByOriginClass[OriginDemonstratedInbound] != 0 || snapshot.ByOriginClass[OriginOutboundAssisted] != 1 || snapshot.ByOriginClass[OriginMixedOrUnknown] != 1 {
		t.Fatalf("origin classification drifted: %+v", snapshot.ByOriginClass)
	}
	if snapshot.ReceivedRevenueStatus != "UNKNOWN" || snapshot.NeedsDecision {
		t.Fatalf("revenue inferred or canonical group rejected: %+v", snapshot)
	}
}

func TestCountSnapshotRejectsAmbiguousAlternativeGroup(t *testing.T) {
	sentAt := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	orgID := uuid.MustParse("11111111-1111-4111-8111-000000000047")
	proposals := []Proposal{
		{OrganizationID: orgID, ProposalID: uuid.MustParse("51111111-1111-4111-8111-000000000047"), ProposalVersion: 1, OpportunityID: "opp-alt", AlternativeGroupID: "ambiguous", SentAt: &sentAt, DecisionState: StateSent},
		{OrganizationID: orgID, ProposalID: uuid.MustParse("61111111-1111-4111-8111-000000000047"), ProposalVersion: 1, OpportunityID: "opp-alt", AlternativeGroupID: "ambiguous", SentAt: &sentAt, DecisionState: StateSent},
	}
	snapshot := CountSnapshot(proposals)
	if !snapshot.NeedsDecision || snapshot.EmittedCount != 0 || len(snapshot.Exclusions) != 2 {
		t.Fatalf("ambiguous alternatives did not fail closed: %+v", snapshot)
	}
}
