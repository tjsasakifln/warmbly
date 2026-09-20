package proposal

import (
	"sort"
	"strings"
)

const CountingSnapshotSchema = "confenge.proposal_counting.v1"

type CountingExclusion struct {
	ProposalID string `json:"proposal_id"`
	Reason     string `json:"reason"`
}

type CountingSnapshot struct {
	SchemaVersion         string              `json:"schema_version"`
	EmittedCount          int                 `json:"emitted_count"`
	AcceptedCount         int                 `json:"accepted_count"`
	ReceivedRevenueStatus string              `json:"received_revenue_status"`
	ByOriginClass         map[OriginClass]int `json:"by_origin_class"`
	NeedsDecision         bool                `json:"needs_decision"`
	Exclusions            []CountingExclusion `json:"exclusions"`
}

// CountSnapshot is a fail-closed read model. Revisions replace earlier emitted
// versions, exclusive alternatives count only their canonical member, and no
// proposal state is interpreted as received revenue.
func CountSnapshot(proposals []Proposal) CountingSnapshot {
	snapshot := CountingSnapshot{
		SchemaVersion:         CountingSnapshotSchema,
		ReceivedRevenueStatus: "UNKNOWN",
		ByOriginClass: map[OriginClass]int{
			OriginDemonstratedInbound: 0,
			OriginOutboundAssisted:    0,
			OriginMixedOrUnknown:      0,
			OriginExistingExpansion:   0,
			OriginPaidOrPartner:       0,
		},
		Exclusions: []CountingExclusion{},
	}
	latest := map[string]Proposal{}
	for _, candidate := range proposals {
		if candidate.Synthetic || candidate.SentAt == nil || candidate.SentAt.IsZero() {
			continue
		}
		key := candidate.OrganizationID.String() + "\x00" + candidate.ProposalID.String()
		current, exists := latest[key]
		if !exists || candidate.ProposalVersion > current.ProposalVersion {
			latest[key] = candidate
		}
	}
	candidates := make([]Proposal, 0, len(latest))
	for _, candidate := range latest {
		candidates = append(candidates, candidate)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].SentAt.Equal(*candidates[j].SentAt) {
			return candidates[i].SentAt.Before(*candidates[j].SentAt)
		}
		return candidates[i].ProposalID.String() < candidates[j].ProposalID.String()
	})

	alternatives := map[string][]Proposal{}
	plain := []Proposal{}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.AlternativeGroupID) == "" {
			plain = append(plain, candidate)
			continue
		}
		scope := strings.TrimSpace(candidate.ScopeID)
		if scope == "" {
			scope = strings.TrimSpace(candidate.OpportunityID)
		}
		key := candidate.OrganizationID.String() + "\x00" + candidate.OpportunityID + "\x00" + scope + "\x00" + candidate.AlternativeGroupID
		alternatives[key] = append(alternatives[key], candidate)
	}
	for _, group := range alternatives {
		canonical := []Proposal{}
		for _, candidate := range group {
			if candidate.CanonicalAlternative {
				canonical = append(canonical, candidate)
			}
		}
		if len(canonical) != 1 {
			snapshot.NeedsDecision = true
			for _, candidate := range group {
				snapshot.Exclusions = append(snapshot.Exclusions, CountingExclusion{
					ProposalID: candidate.ProposalID.String(),
					Reason:     "alternative_group_without_single_canonical",
				})
			}
			continue
		}
		plain = append(plain, canonical[0])
	}
	for _, candidate := range plain {
		snapshot.EmittedCount++
		origin := candidate.OriginClass
		if !validOriginClass(origin) || origin == "" {
			origin = OriginMixedOrUnknown
		}
		snapshot.ByOriginClass[origin]++
		if candidate.DecisionState == StateAccepted {
			snapshot.AcceptedCount++
		}
	}
	sort.Slice(snapshot.Exclusions, func(i, j int) bool {
		return snapshot.Exclusions[i].ProposalID < snapshot.Exclusions[j].ProposalID
	})
	return snapshot
}
