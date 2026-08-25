package proposal

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var SyntheticCanaryOrganizationID = uuid.MustParse("11111111-1111-4111-8111-000000000047")

var SyntheticCanaryTime = time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)

type SyntheticCanaryResult struct {
	Proposal Proposal               `json:"proposal"`
	Handoff  DeliveryOrderRequested `json:"handoff"`
}

func RunSyntheticCanary(ctx context.Context) (SyntheticCanaryResult, error) {
	service := NewService(NewMemoryStore(), func() time.Time { return SyntheticCanaryTime })
	result, err := service.Create(ctx, CreateCommand{
		OrganizationID: SyntheticCanaryOrganizationID,
		IdempotencyKey: "fixture:cfg-diag-exp-v1:proposal:create",
		CreatedBy:      "actor:synthetic-reviewer",
		Draft:          SyntheticCanaryDraft(),
	})
	if err != nil {
		return SyntheticCanaryResult{}, err
	}
	for index, target := range []State{StatePrepared, StateApprovedToSend, StateSent, StateAccepted} {
		result, err = service.Transition(ctx, TransitionCommand{
			OrganizationID: result.Proposal.OrganizationID,
			ProposalID:     result.Proposal.ProposalID, ProposalVersion: result.Proposal.ProposalVersion,
			ExpectedRecordVersion: result.Proposal.Version,
			IdempotencyKey:        "fixture:cfg-diag-exp-v1:proposal:" + string(target),
			Target:                target, Actor: "actor:synthetic-reviewer",
			LiteralReasonRef: "reason:synthetic:" + string(target),
			EvidenceRefs:     []string{"evidence:synthetic:" + string(target)},
			OccurredAt:       SyntheticCanaryTime.Add(time.Duration(index+1) * time.Minute),
		})
		if err != nil {
			return SyntheticCanaryResult{}, err
		}
	}
	sourceEventID := "fixture-financial-gate-cfg-diag-exp-001"
	authorized, err := service.AuthorizeDelivery(ctx, AuthorizeDeliveryCommand{
		OrganizationID: result.Proposal.OrganizationID,
		ProposalID:     result.Proposal.ProposalID, ProposalVersion: result.Proposal.ProposalVersion,
		IdempotencyKey: "fixture:cfg-diag-exp-v1:delivery:SYNTHETIC_VALID",
		CausationID:    sourceEventID, OnboardingRef: "onboarding:synthetic:cfg-diag-exp-001",
		OccurredAt: SyntheticCanaryTime.Add(5 * time.Minute),
		FinancialGate: FinancialGate{
			SchemaVersion: FinancialGateSchema, State: FinancialGateSyntheticValid,
			Synthetic: true, SourceEventID: &sourceEventID, ReceivedRevenue: false,
			EvidenceRefs: []string{"fixture:financial-gate:synthetic-valid"},
		},
	})
	if err != nil {
		return SyntheticCanaryResult{}, err
	}
	if authorized.Handoff == nil {
		return SyntheticCanaryResult{}, fmt.Errorf("synthetic canary produced no delivery handoff")
	}
	return SyntheticCanaryResult{Proposal: authorized.Proposal, Handoff: *authorized.Handoff}, nil
}

func SyntheticCanaryDraft() Draft {
	return Draft{
		AccountID: "account-synthetic-cfg-diag-exp-001", ClientRef: "client:redacted:cfg-diag-exp-001",
		OpportunityID: "opportunity-synthetic-cfg-diag-exp-001", QCOID: "qco-synthetic-cfg-diag-exp-001",
		DealID: "deal-synthetic-cfg-diag-exp-001", SourceLeadID: "lead-synthetic-cfg-diag-exp-001",
		CorrelationID: "corr-synthetic-cfg-diag-exp-001", OfferID: "CFG-DIAG-EXP-v1", OfferVersion: "v1",
		DeliverableID: "CFG-DIAG-EXP-v1", DeliverableVersion: "v1", ScopeVersion: "CFG-SCOPE-DIAG-EXP-v1",
		PriceVersion: "CFG-OFFER-CATALOG-v1", TermsVersion: "CFG-TERMS-B2B-2026-08-17-v1", Amount: 250000,
		Currency: "BRL", Credits: []string{}, Addons: []string{},
		Inputs:     []string{"company_context_ref", "expansion_hypothesis_ref"},
		Exclusions: []string{"real_customer_data", "provider_charge"},
		Deadline:   SyntheticCanaryTime.Add(48 * time.Hour), ValidUntil: SyntheticCanaryTime.Add(7 * 24 * time.Hour),
		EvidenceRefs: []string{"fixture:cfg-diag-exp-v1:proposal"}, Synthetic: true,
	}
}
