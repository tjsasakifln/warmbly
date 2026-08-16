package intel

import (
	"strings"
	"time"
)

// SyntheticMonth is the fixture window used by tests and the labeled demo.
const SyntheticMonth = "2026-08"

// SyntheticFacts returns clearly labeled SYNTHETIC observed records.
// Real rollups must call Rollup(..., includeSynthetic=false).
func SyntheticFacts(orgID string) []ObservedFacts {
	base := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ts := func(days, hours int) time.Time { return base.AddDate(0, 0, days).Add(time.Duration(hours) * time.Hour) }
	ptr := func(t time.Time) *time.Time { return &t }

	return []ObservedFacts{
		{
			Keys: JoinKeys{
				OrganizationID: orgID, Source: "web-cfg", Query: "segunda-leitura",
				AssetID: "landing-segunda-leitura", CTAID: "segunda-leitura-contrato",
				CorrelationID: "corr-syn-in-1", LeadID: "webcfg-syn-in-1", ReceiptID: "rcpt-syn-in-1",
				AccountID: "extra-acc-norte", SourceLeadID: "extra-acc-norte", PersonID: "person-ana",
				EventIDs: []string{"ev-contract-margin"}, TargetFitVersion: "tf-v1",
				ActivationPolicyVersion: "ap-v1", TargetFitWatermark: "wm-2026-08-01",
				TargetFitFresh: true, ActionID: "act-syn-in-1", OutcomeID: "out-syn-in-1",
				OutboxEventID: "evt-syn-in-1", IdempotencyKey: "idem-syn-in-1",
				RouteFamily: FamilyInbound, Trigger: "CONTRACT_MARGIN_EVENT",
				OfferID: "segunda-leitura", Route: "R3_ROUTED_TO_NAMED_PERSON",
			},
			LeadCreatedAt: ts(0, 0), IngestedAt: ts(0, 1), EnrichmentAt: ptr(ts(0, 2)),
			FirstActionAt: ptr(ts(0, 4)), ConversationAt: ptr(ts(1, 2)),
			ProposalAt: ptr(ts(5, 0)), CloseAt: ptr(ts(12, 0)),
			ActionOccurredAt: ts(0, 4), OutcomeOccurredAt: ts(12, 0),
			OutcomeType: OutcomeWon, HumanConfirmed: true, Qualified: true,
			Conversation: true, PipelineOpen: false, Synthetic: true, Label: LabelSynthetic,
		},
		{
			Keys: JoinKeys{
				OrganizationID: orgID, Source: "web-cfg", Query: "diagnostico",
				AssetID: "landing-diagnostico", CTAID: "cta-diag",
				CorrelationID: "corr-syn-in-2", LeadID: "webcfg-syn-in-2", ReceiptID: "rcpt-syn-in-2",
				AccountID: "extra-acc-sul", SourceLeadID: "extra-acc-sul", PersonID: "person-bruno",
				EventIDs: []string{"ev-public-notice"}, TargetFitVersion: "tf-v1",
				ActivationPolicyVersion: "ap-v1", TargetFitWatermark: "wm-2026-08-01",
				TargetFitFresh: true, ActionID: "act-syn-in-2", OutcomeID: "out-syn-in-2",
				OutboxEventID: "evt-syn-in-2", IdempotencyKey: "idem-syn-in-2",
				RouteFamily: FamilyInbound, Trigger: "PUBLIC_NOTICE",
				OfferID: "diagnostico", Route: "R1_DIRECT",
			},
			LeadCreatedAt: ts(2, 0), IngestedAt: ts(2, 1), EnrichmentAt: ptr(ts(2, 2)),
			FirstActionAt: ptr(ts(2, 5)), ConversationAt: ptr(ts(3, 1)),
			ActionOccurredAt: ts(2, 5), OutcomeOccurredAt: ts(3, 1),
			OutcomeType: OutcomeQualifiedConversation, Qualified: true,
			Conversation: true, PipelineOpen: true, Synthetic: true, Label: LabelSynthetic,
		},
		{
			Keys: JoinKeys{
				OrganizationID: orgID, Source: "web-cfg", Query: "segunda-leitura",
				AssetID: "landing-segunda-leitura", LeadID: "webcfg-syn-in-3", ReceiptID: "rcpt-syn-in-3",
				AccountID: "extra-acc-oeste", SourceLeadID: "extra-acc-oeste",
				EventIDs: []string{"ev-addendum"}, TargetFitVersion: "tf-v1",
				ActivationPolicyVersion: "ap-v1", TargetFitFresh: true,
				ActionID: "act-syn-in-3", OutcomeID: "out-syn-in-3",
				IdempotencyKey: "idem-syn-in-3", RouteFamily: FamilyInbound,
				Trigger: "ADDENDUM", OfferID: "segunda-leitura", Route: "CALL",
			},
			LeadCreatedAt: ts(3, 0), IngestedAt: ts(3, 1), FirstActionAt: ptr(ts(3, 6)),
			ProposalAt: ptr(ts(8, 0)), ActionOccurredAt: ts(3, 6), OutcomeOccurredAt: ts(8, 0),
			OutcomeType: OutcomeProposal, Qualified: true, Conversation: true,
			PipelineOpen: true, Synthetic: true, Label: LabelSynthetic,
		},
		{
			Keys: JoinKeys{
				OrganizationID: orgID, Source: "extra-cli", Query: "outbound-pilot",
				AssetID: "playbook-v1", AccountID: "extra-acc-leste", SourceLeadID: "extra-acc-leste",
				PersonID: "person-carla", EventIDs: []string{"ev-moment-leste"},
				TargetFitVersion: "tf-v1", ActivationPolicyVersion: "ap-v1",
				TargetFitFresh: true, ActionID: "act-syn-out-1", OutcomeID: "out-syn-out-1",
				IdempotencyKey: "idem-syn-out-1", RouteFamily: FamilyOutbound,
				Trigger: "CONTRACT_MARGIN_EVENT", OfferID: "segunda-leitura", Route: "EMAIL",
			},
			LeadCreatedAt: ts(1, 0), IngestedAt: ts(1, 0), FirstActionAt: ptr(ts(1, 3)),
			ConversationAt: ptr(ts(4, 0)), ActionOccurredAt: ts(1, 3), OutcomeOccurredAt: ts(4, 0),
			OutcomeType: OutcomeMeeting, Qualified: true, Conversation: true,
			PipelineOpen: true, Synthetic: true, Label: LabelSynthetic,
		},
		{
			Keys: JoinKeys{
				OrganizationID: orgID, Source: "partner", Query: "indicacao",
				AssetID: "partner-kit", AccountID: "extra-acc-partner", SourceLeadID: "extra-acc-partner",
				PersonID: "person-diego", TargetFitVersion: "tf-v1",
				ActivationPolicyVersion: "ap-v1", TargetFitFresh: true,
				ActionID: "act-syn-pt-1", OutcomeID: "out-syn-pt-1",
				IdempotencyKey: "idem-syn-pt-1", RouteFamily: FamilyPartner,
				Trigger: "PARTNER_REFERRAL", OfferID: "diagnostico", Route: "ROUTED_CALL",
			},
			LeadCreatedAt: ts(6, 0), IngestedAt: ts(6, 0), FirstActionAt: ptr(ts(6, 2)),
			ActionOccurredAt: ts(6, 2), OutcomeOccurredAt: ts(10, 0),
			OutcomeType: OutcomeLost, HumanConfirmed: true, PipelineOpen: false,
			Synthetic: true, Label: LabelSynthetic,
		},
		{
			Keys: JoinKeys{
				OrganizationID: orgID, Source: "expansion", Query: "renewal",
				AssetID: "client-review", AccountID: "extra-acc-client", SourceLeadID: "extra-acc-client",
				PersonID: "person-eva", TargetFitVersion: "tf-v1",
				ActivationPolicyVersion: "ap-v1", TargetFitFresh: true,
				ActionID: "act-syn-ex-1", OutcomeID: "out-syn-ex-1",
				IdempotencyKey: "idem-syn-ex-1", RouteFamily: FamilyExpansion,
				Trigger: "RENEWAL_WINDOW", OfferID: "annualidade", Route: "CALL",
			},
			LeadCreatedAt: ts(7, 0), IngestedAt: ts(7, 0), FirstActionAt: ptr(ts(7, 2)),
			ActionOccurredAt: ts(7, 2), OutcomeType: OutcomeUnknown,
			PipelineOpen: true, Synthetic: true, Label: LabelSynthetic,
		},
	}
}

// LoadSynthetic joins every labeled fixture into store. Idempotent.
func LoadSynthetic(store Store, orgID string) []JoinResult {
	var out []JoinResult
	for _, f := range SyntheticFacts(orgID) {
		out = append(out, Reconcile(store, f))
	}
	return out
}

// LoadOperatorQueue loads labeled SYNTHETIC chains plus a representative
// exception set with stable IDs and ages. CLI --fixture uses this path.
func LoadOperatorQueue(store Store, orgID string) {
	if strings.TrimSpace(orgID) == "" {
		orgID = OperatorQueueOrgID
	}
	LoadSynthetic(store, orgID)
	for _, ex := range OperatorQueueFixtures(orgID) {
		_ = store.PutException(ex)
	}
}

// OperatorQueueFixtures is the labeled operator-queue sample. Not live.
func OperatorQueueFixtures(orgID string) []Exception {
	now := OperatorQueueNow
	item := func(code, reason, next, lane, source, identity, lead, action, outcome string, held bool, age time.Duration, extra ObservedFacts) Exception {
		in := extra
		in.Keys.OrganizationID = orgID
		in.Keys.RouteFamily = lane
		in.Keys.Source = source
		in.Synthetic = true
		in.Label = LabelSynthetic
		ex := Exception{
			ID:             StableExceptionID(orgID, code, identity, lead, action, outcome),
			OrganizationID: orgID,
			Code:           code,
			Reason:         reason,
			NextAction:     next,
			Identity:       identity,
			ActionID:       action,
			OutcomeID:      outcome,
			LeadID:         lead,
			Held:           held,
			Synthetic:      true,
			At:             now.Add(-age),
			Lane:           lane,
			Source:         source,
		}
		enrichException(&ex, in)
		ex.At = now.Add(-age)
		return ex
	}
	return []Exception{
		item(ExceptionOrphan, "outcome without action", "hold on exception queue until action arrives",
			FamilyInbound, "web-cfg", "", "", "", "out-orphan-1", true, 2*time.Hour, ObservedFacts{}),
		item(ExceptionConflictingAccount, "incoming account_id disagrees with the first chain",
			"keep the first extra-cli account; do not overwrite", FamilyInbound, "web-cfg",
			"lead:webcfg-syn-in-1", "webcfg-syn-in-1", "act-syn-in-1", "out-syn-in-1", false, 26*time.Hour, ObservedFacts{}),
		item(ExceptionOutOfOrder, "outcome timestamp precedes action or inbound; order is not invented",
			"hold; do not reorder into a fake chain", FamilyInbound, "web-cfg",
			"lead:oo-1", "lead-oo-1", "act-oo-1", "out-oo-1", true, 8*24*time.Hour, ObservedFacts{}),
		item(ExceptionUnconfirmedWon, "WON cannot be inferred",
			"require human or document confirmation; keep UNKNOWN", FamilyInbound, "web-cfg",
			"lead:won-1", "lead-won-1", "act-won-1", "out-won-1", true, 3*time.Hour, ObservedFacts{}),
		item(ExceptionUnconfirmedLost, "LOST cannot be inferred",
			"require human or document confirmation; keep UNKNOWN", FamilyOutbound, "extra-cli",
			"action:act-lost-1", "", "act-lost-1", "out-lost-1", true, 5*time.Hour, ObservedFacts{}),
		item(ExceptionMissingVersion, "no extra-cli version on the observed path",
			"leave version UNKNOWN", FamilyPartner, "partner",
			"lead:mv-1", "lead-mv-1", "act-mv-1", "", false, 30*time.Minute, ObservedFacts{}),
		item(ExceptionStaleAttribution, "attribution or target-fit freshness is stale",
			"keep the observed IDs; do not invent a newer source/asset", FamilyExpansion, "expansion",
			"lead:stale-1", "lead-stale-1", "act-stale-1", "", false, 4*24*time.Hour, ObservedFacts{}),
		item(ExceptionDuplicate, "same join IDs already have a chain",
			"return the first chain; do not insert a second", FamilyInbound, "web-cfg",
			"lead:webcfg-syn-in-2", "webcfg-syn-in-2", "act-syn-in-2", "out-syn-in-2", false, 10*time.Minute, ObservedFacts{}),
	}
}
