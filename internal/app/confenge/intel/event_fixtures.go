package intel

import (
	"encoding/json"
	"time"
)

const (
	FixtureMarketAnswerComplete           = "market_answer_complete"
	FixtureContractAnalysisMissingOutcome = "contract_analysis_missing_outcome"
	FixtureXRayCompletionNoHandRaise      = "xray_completion_no_hand_raise"
	FixtureDuplicateReplay                = "duplicate_replay"
	FixtureOrphanAction                   = "orphan_action"
	FixtureLateCorrection                 = "late_correction"
	FixturePipelineWithoutRevenue         = "pipeline_without_revenue"
	FixtureLostUnknown                    = "lost_unknown"
	FixtureOutboundLane                   = "outbound_lane"
	FixtureNegativeDuration               = "negative_duration"
)

const FixtureProducerSHA = "synthetic-fixture-sha"

// NamedFixture is one labeled sequence consumed by IngestEvent.
type NamedFixture struct {
	Name   string
	Events []CommercialEvent
}

// NamedEventFixtures returns the required SYNTHETIC sequences. Real
// events replace these by posting the same envelope through IngestEvent.
func NamedEventFixtures(orgID string) []NamedFixture {
	base := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ts := func(days, hours int) time.Time {
		return base.AddDate(0, 0, days).Add(time.Duration(hours) * time.Hour)
	}
	ptr := func(t time.Time) *time.Time { return &t }
	org := orgID

	env := func(id, typ string, at time.Time) CommercialEvent {
		return CommercialEvent{
			EventID:        id,
			Version:        "1",
			Schema:         EventSchemaV1,
			Type:           typ,
			OccurredAt:     at,
			IngestedAt:     at.Add(time.Minute),
			Timezone:       "America/Sao_Paulo",
			OrganizationID: org,
			ProducerSHA:    FixtureProducerSHA,
			Synthetic:      true,
			Consent:        "web-cfg-consent-ref",
		}
	}

	maLead := "lead-ma-complete"
	ma := []CommercialEvent{}
	e := env("ev-ma-lead", EventLeadReceived, ts(0, 0))
	e.LeadID = maLead
	e.ReceiptID = "rcpt-ma-complete"
	e.CorrelationID = "corr-ma-complete"
	e.IdempotencyKey = "idem-ev-ma-lead"
	e.AssetFamily = AssetFamilyMarketAnswer
	e.MarketAnswerID = "ma-public-1"
	e.Source = "web-cfg"
	e.Query = "segunda-leitura-preco"
	e.IntentClass = "contract-margin"
	e.CTAID = "cta-ma-1"
	e.AssetID = "asset-ma-1"
	e.OfferID = "segunda-leitura"
	e.Route = "R3_ROUTED_TO_NAMED_PERSON"
	e.RouteFamily = FamilyInbound
	e.Referrer = "https://search.example/ref"
	e.AccountPublicID = "acc-ma-1"
	e.EntityPublicID = "ent-ma-1"
	e.Trigger = "CONTRACT_MARGIN_EVENT"
	e.PIIPointer = "pii:lead/lead-ma-complete"
	e.PublishedAt = ptr(ts(0, 0).Add(-20 * time.Minute))
	e.DetectedAt = ptr(ts(0, 0).Add(-5 * time.Minute))
	e.ActorRef = "actor:web-cfg"
	e.EvidenceRef = "evidence:ma-pack-1"
	ma = append(ma, e)

	e = env("ev-ma-validated", EventLeadValidated, ts(0, 1))
	e.LeadID, e.ReceiptID, e.CorrelationID = maLead, "rcpt-ma-complete", "corr-ma-complete"
	e.IdempotencyKey = "idem-ev-ma-validated"
	e.AssetFamily, e.MarketAnswerID = AssetFamilyMarketAnswer, "ma-public-1"
	e.Source, e.Query, e.AssetID = "web-cfg", "segunda-leitura-preco", "asset-ma-1"
	e.RouteFamily, e.OfferID, e.Route = FamilyInbound, "segunda-leitura", "R3_ROUTED_TO_NAMED_PERSON"
	e.Qualified = true
	e.PIIPointer = "pii:lead/lead-ma-complete"
	ma = append(ma, e)

	e = env("ev-ma-handoff", EventHandoffAccepted, ts(0, 2))
	e.LeadID, e.ReceiptID, e.IdempotencyKey = maLead, "rcpt-ma-complete", "idem-ev-ma-handoff"
	e.AssetFamily, e.RouteFamily = AssetFamilyMarketAnswer, FamilyInbound
	e.Source, e.AssetID = "web-cfg", "asset-ma-1"
	ma = append(ma, e)

	e = env("ev-ma-approved", EventActionApproved, ts(0, 3))
	e.LeadID, e.ActionID, e.IdempotencyKey = maLead, "act-ma-1", "idem-ev-ma-approved"
	e.AssetFamily, e.RouteFamily = AssetFamilyMarketAnswer, FamilyInbound
	e.Source, e.AssetID, e.ActorRef = "web-cfg", "asset-ma-1", "actor:operator"
	ma = append(ma, e)

	e = env("ev-ma-executed", EventActionExecuted, ts(0, 4))
	e.LeadID, e.ActionID, e.IdempotencyKey = maLead, "act-ma-1", "idem-ev-ma-executed"
	e.AssetFamily, e.RouteFamily = AssetFamilyMarketAnswer, FamilyInbound
	e.Source, e.AssetID = "web-cfg", "asset-ma-1"
	ma = append(ma, e)

	e = env("ev-ma-reply", EventReply, ts(1, 2))
	e.LeadID, e.ActionID, e.OutcomeID = maLead, "act-ma-1", "out-ma-reply"
	e.IdempotencyKey = "idem-ev-ma-reply"
	e.AssetFamily, e.RouteFamily = AssetFamilyMarketAnswer, FamilyInbound
	e.Source, e.AssetID = "web-cfg", "asset-ma-1"
	ma = append(ma, e)

	e = env("ev-ma-meeting", EventMeeting, ts(2, 0))
	e.LeadID, e.ActionID, e.OutcomeID = maLead, "act-ma-1", "out-ma-meet"
	e.IdempotencyKey = "idem-ev-ma-meeting"
	e.AssetFamily, e.RouteFamily = AssetFamilyMarketAnswer, FamilyInbound
	e.Source, e.AssetID = "web-cfg", "asset-ma-1"
	ma = append(ma, e)

	e = env("ev-ma-proposal", EventProposal, ts(5, 0))
	e.LeadID, e.ActionID, e.OutcomeID = maLead, "act-ma-1", "out-ma-prop"
	e.IdempotencyKey = "idem-ev-ma-proposal"
	e.AssetFamily, e.RouteFamily = AssetFamilyMarketAnswer, FamilyInbound
	e.Source, e.AssetID, e.OfferID = "web-cfg", "asset-ma-1", "segunda-leitura"
	ma = append(ma, e)

	e = env("ev-ma-pipeline", EventPipelineCreated, ts(5, 1))
	e.LeadID, e.ActionID, e.IdempotencyKey = maLead, "act-ma-1", "idem-ev-ma-pipeline"
	e.AssetFamily, e.RouteFamily = AssetFamilyMarketAnswer, FamilyInbound
	e.Source, e.AssetID = "web-cfg", "asset-ma-1"
	ma = append(ma, e)

	e = env("ev-ma-won", EventWon, ts(12, 0))
	e.LeadID, e.ActionID, e.OutcomeID = maLead, "act-ma-1", "out-ma-won"
	e.IdempotencyKey = "idem-ev-ma-won"
	e.AssetFamily, e.RouteFamily = AssetFamilyMarketAnswer, FamilyInbound
	e.Source, e.AssetID = "web-cfg", "asset-ma-1"
	e.HumanConfirmed = true
	e.OutcomeState = OutcomeWon
	e.EvidenceRef = "evidence:human-won"
	ma = append(ma, e)

	e = env("ev-ma-revenue", EventRevenueEvidenced, ts(13, 0))
	e.LeadID, e.ActionID, e.IdempotencyKey = maLead, "act-ma-1", "idem-ev-ma-revenue"
	e.AssetFamily, e.RouteFamily = AssetFamilyMarketAnswer, FamilyInbound
	e.Source, e.AssetID = "web-cfg", "asset-ma-1"
	e.RevenueCents = 150000
	e.RevenueDocumentID = "doc-invoice-ma-1"
	e.EvidenceRef = "evidence:invoice-ma-1"
	ma = append(ma, e)

	e = env("ev-ma-learn", EventLearningCandidate, ts(13, 1))
	e.LeadID, e.ActionID, e.OutcomeID = maLead, "act-ma-1", "out-ma-won"
	e.IdempotencyKey = "idem-ev-ma-learn"
	e.AssetFamily, e.RouteFamily = AssetFamilyMarketAnswer, FamilyInbound
	e.Source, e.AssetID, e.OfferID = "web-cfg", "asset-ma-1", "segunda-leitura"
	e.OutcomeState = OutcomeWon
	e.HumanConfirmed = true
	e.EvidenceRef = "repeat"
	ma = append(ma, e)

	caLead := "lead-ca-missing"
	ca := []CommercialEvent{}
	e = env("ev-ca-lead", EventLeadReceived, ts(1, 0))
	e.LeadID, e.ReceiptID, e.CorrelationID = caLead, "rcpt-ca-missing", "corr-ca-missing"
	e.IdempotencyKey = "idem-ev-ca-lead"
	e.AssetFamily = AssetFamilyContractAnalysis
	e.AnalysisID = "analysis-ca-1"
	e.Source, e.Query, e.AssetID = "web-cfg", "aditivo-contratual", "asset-ca-1"
	e.IntentClass, e.CTAID = "addendum", "cta-ca-1"
	e.OfferID, e.Route, e.RouteFamily = "segunda-leitura", "CALL", FamilyInbound
	e.AccountPublicID = "acc-ca-1"
	e.PIIPointer = "pii:lead/lead-ca-missing"
	e.PublishedAt = ptr(ts(1, 0).Add(-10 * time.Minute))
	e.DetectedAt = ptr(ts(1, 0).Add(-2 * time.Minute))
	ca = append(ca, e)

	e = env("ev-ca-validated", EventLeadValidated, ts(1, 1))
	e.LeadID, e.ReceiptID = caLead, "rcpt-ca-missing"
	e.IdempotencyKey = "idem-ev-ca-validated"
	e.AssetFamily, e.AnalysisID = AssetFamilyContractAnalysis, "analysis-ca-1"
	e.Source, e.AssetID, e.RouteFamily = "web-cfg", "asset-ca-1", FamilyInbound
	e.Qualified = true
	ca = append(ca, e)

	e = env("ev-ca-handoff", EventHandoffAccepted, ts(1, 2))
	e.LeadID, e.IdempotencyKey = caLead, "idem-ev-ca-handoff"
	e.AssetFamily, e.AnalysisID, e.RouteFamily = AssetFamilyContractAnalysis, "analysis-ca-1", FamilyInbound
	e.Source, e.AssetID = "web-cfg", "asset-ca-1"
	ca = append(ca, e)

	e = env("ev-ca-action", EventActionExecuted, ts(1, 4))
	e.LeadID, e.ActionID, e.IdempotencyKey = caLead, "act-ca-1", "idem-ev-ca-action"
	e.AssetFamily, e.AnalysisID, e.RouteFamily = AssetFamilyContractAnalysis, "analysis-ca-1", FamilyInbound
	e.Source, e.AssetID = "web-cfg", "asset-ca-1"
	ca = append(ca, e)

	xray := []CommercialEvent{}
	e = env("ev-xray-complete", EventXRayCompleted, ts(2, 0))
	e.IdempotencyKey = "idem-ev-xray-complete"
	e.AssetFamily = AssetFamilyB2GXRay
	e.AssetID = "asset-xray-1"
	e.Source = "web-cfg"
	e.Query = "cnpj-lookup"
	e.IntentClass = "b2g-xray"
	e.RouteFamily = FamilyInbound
	e.HandRaise = false
	e.PIIPointer = "pii:session/xray-1"
	e.PublishedAt = ptr(ts(2, 0).Add(-3 * time.Minute))
	xray = append(xray, e)

	replay := []CommercialEvent{ma[0]} // same event_id as first MA lead

	orphan := []CommercialEvent{}
	e = env("ev-orphan-action", EventActionExecuted, ts(3, 0))
	e.ActionID = "act-orphan-1"
	e.IdempotencyKey = "idem-ev-orphan-action"
	e.RouteFamily = FamilyInbound
	e.Source = "web-cfg"
	e.AssetID = "asset-orphan"
	orphan = append(orphan, e)

	late := make([]CommercialEvent, 0, len(ca)+1)
	late = append(late, ca...)
	e = env("ev-ca-late-correction", EventCorrection, ts(8, 0))
	e.LeadID, e.ActionID, e.OutcomeID = caLead, "act-ca-1", "out-ca-corr"
	e.IdempotencyKey = "idem-ev-ca-late-correction"
	e.AssetFamily, e.AnalysisID, e.RouteFamily = AssetFamilyContractAnalysis, "analysis-ca-1", FamilyInbound
	e.Source, e.AssetID = "web-cfg", "asset-ca-1"
	e.OutcomeState = OutcomeLost
	e.HumanConfirmed = true
	e.Correction = true
	e.EvidenceRef = "wrong_offer"
	late = append(late, e)

	pipeLead := "lead-pipe-norev"
	pipe := []CommercialEvent{}
	e = env("ev-pipe-lead", EventLeadReceived, ts(4, 0))
	e.LeadID, e.ReceiptID, e.CorrelationID = pipeLead, "rcpt-pipe-norev", "corr-pipe-norev"
	e.IdempotencyKey = "idem-ev-pipe-lead"
	e.AssetFamily, e.MarketAnswerID = AssetFamilyMarketAnswer, "ma-public-2"
	e.Source, e.Query, e.AssetID = "web-cfg", "diagnostico", "asset-pipe-1"
	e.IntentClass, e.OfferID, e.Route = "diagnostico", "diagnostico", "R1_DIRECT"
	e.RouteFamily = FamilyInbound
	e.PIIPointer = "pii:lead/lead-pipe-norev"
	pipe = append(pipe, e)

	e = env("ev-pipe-validated", EventLeadValidated, ts(4, 1))
	e.LeadID, e.IdempotencyKey = pipeLead, "idem-ev-pipe-validated"
	e.AssetFamily, e.RouteFamily = AssetFamilyMarketAnswer, FamilyInbound
	e.Source, e.AssetID = "web-cfg", "asset-pipe-1"
	e.Qualified = true
	pipe = append(pipe, e)

	e = env("ev-pipe-action", EventActionExecuted, ts(4, 3))
	e.LeadID, e.ActionID, e.IdempotencyKey = pipeLead, "act-pipe-1", "idem-ev-pipe-action"
	e.AssetFamily, e.RouteFamily = AssetFamilyMarketAnswer, FamilyInbound
	e.Source, e.AssetID = "web-cfg", "asset-pipe-1"
	pipe = append(pipe, e)

	e = env("ev-pipe-created", EventPipelineCreated, ts(6, 0))
	e.LeadID, e.ActionID, e.IdempotencyKey = pipeLead, "act-pipe-1", "idem-ev-pipe-created"
	e.AssetFamily, e.RouteFamily = AssetFamilyMarketAnswer, FamilyInbound
	e.Source, e.AssetID = "web-cfg", "asset-pipe-1"
	pipe = append(pipe, e)

	lostLead := "lead-lost-1"
	unkLead := "lead-unknown-1"
	lostUnk := []CommercialEvent{}
	e = env("ev-lost-lead", EventLeadReceived, ts(6, 0))
	e.LeadID, e.ReceiptID, e.IdempotencyKey = lostLead, "rcpt-lost-1", "idem-ev-lost-lead"
	e.Source, e.Query, e.AssetID = "web-cfg", "segunda-leitura", "asset-lost-1"
	e.OfferID, e.RouteFamily, e.AssetFamily = "segunda-leitura", FamilyInbound, AssetFamilyMarketAnswer
	e.PIIPointer = "pii:lead/lead-lost-1"
	lostUnk = append(lostUnk, e)

	e = env("ev-lost-action", EventActionExecuted, ts(6, 2))
	e.LeadID, e.ActionID, e.IdempotencyKey = lostLead, "act-lost-1", "idem-ev-lost-action"
	e.Source, e.AssetID, e.RouteFamily = "web-cfg", "asset-lost-1", FamilyInbound
	e.AssetFamily = AssetFamilyMarketAnswer
	lostUnk = append(lostUnk, e)

	e = env("ev-lost", EventLost, ts(9, 0))
	e.LeadID, e.ActionID, e.OutcomeID = lostLead, "act-lost-1", "out-lost-1"
	e.IdempotencyKey = "idem-ev-lost"
	e.Source, e.AssetID, e.RouteFamily = "web-cfg", "asset-lost-1", FamilyInbound
	e.AssetFamily = AssetFamilyMarketAnswer
	e.HumanConfirmed = true
	e.OutcomeState = OutcomeLost
	lostUnk = append(lostUnk, e)

	e = env("ev-unk-lead", EventLeadReceived, ts(6, 4))
	e.LeadID, e.ReceiptID, e.IdempotencyKey = unkLead, "rcpt-unk-1", "idem-ev-unk-lead"
	e.Source, e.Query, e.AssetID = "web-cfg", "diagnostico", "asset-unk-1"
	e.RouteFamily, e.AssetFamily = FamilyInbound, AssetFamilyContractAnalysis
	e.AnalysisID = "analysis-unk-1"
	e.PIIPointer = "pii:lead/lead-unknown-1"
	lostUnk = append(lostUnk, e)

	e = env("ev-unk-state", EventUnknownState, ts(7, 0))
	e.LeadID, e.IdempotencyKey = unkLead, "idem-ev-unk-state"
	e.Source, e.AssetID, e.RouteFamily = "web-cfg", "asset-unk-1", FamilyInbound
	e.AssetFamily, e.AnalysisID = AssetFamilyContractAnalysis, "analysis-unk-1"
	e.OutcomeState = OutcomeUnknown
	lostUnk = append(lostUnk, e)

	outb := []CommercialEvent{}
	e = env("ev-out-approved", EventActionApproved, ts(1, 0))
	e.ActionID, e.IdempotencyKey = "act-out-1", "idem-ev-out-approved"
	e.AccountPublicID, e.EntityPublicID = "acc-out-1", "ent-out-1"
	e.Source, e.AssetID = "extra-cli", "playbook-v1"
	e.RouteFamily, e.OfferID, e.Route = FamilyOutbound, "segunda-leitura", "EMAIL"
	e.Trigger = "CONTRACT_MARGIN_EVENT"
	outb = append(outb, e)

	e = env("ev-out-exec", EventActionExecuted, ts(1, 3))
	e.ActionID, e.IdempotencyKey = "act-out-1", "idem-ev-out-exec"
	e.Source, e.AssetID, e.RouteFamily = "extra-cli", "playbook-v1", FamilyOutbound
	outb = append(outb, e)

	e = env("ev-out-meet", EventMeeting, ts(4, 0))
	e.ActionID, e.OutcomeID, e.IdempotencyKey = "act-out-1", "out-out-1", "idem-ev-out-meet"
	e.Source, e.AssetID, e.RouteFamily = "extra-cli", "playbook-v1", FamilyOutbound
	outb = append(outb, e)

	return []NamedFixture{
		{Name: FixtureMarketAnswerComplete, Events: ma},
		{Name: FixtureContractAnalysisMissingOutcome, Events: ca},
		{Name: FixtureXRayCompletionNoHandRaise, Events: xray},
		{Name: FixtureDuplicateReplay, Events: replay},
		{Name: FixtureOrphanAction, Events: orphan},
		{Name: FixtureLateCorrection, Events: late},
		{Name: FixturePipelineWithoutRevenue, Events: pipe},
		{Name: FixtureLostUnknown, Events: lostUnk},
		{Name: FixtureOutboundLane, Events: outb},
	}
}

// NegativeDurationFixture is one overlapping pair. It must fail
// reconcile and must not produce a coerced positive duration.
func NegativeDurationFixture(orgID string) []CommercialEvent {
	base := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	lead := CommercialEvent{
		EventID:        "ev-neg-lead",
		Version:        "1",
		Schema:         EventSchemaV1,
		Type:           EventLeadReceived,
		OccurredAt:     base,
		IngestedAt:     base.Add(time.Minute),
		Timezone:       "UTC",
		OrganizationID: orgID,
		LeadID:         "lead-neg-1",
		ReceiptID:      "rcpt-neg-1",
		IdempotencyKey: "idem-ev-neg-lead",
		Source:         "web-cfg",
		Query:          "diagnostico",
		AssetID:        "asset-neg-1",
		RouteFamily:    FamilyInbound,
		AssetFamily:    AssetFamilyMarketAnswer,
		ProducerSHA:    FixtureProducerSHA,
		Synthetic:      true,
	}
	action := lead
	action.EventID = "ev-neg-action"
	action.Type = EventActionExecuted
	action.ActionID = "act-neg-1"
	action.IdempotencyKey = "idem-ev-neg-action"
	action.OccurredAt = base.Add(-2 * time.Hour)
	action.IngestedAt = base.Add(-2*time.Hour + time.Minute)
	return []CommercialEvent{lead, action}
}

// LoadNamedEventFixtures ingests every required sequence through IngestEvent.
func LoadNamedEventFixtures(store Store, orgID string) []JoinResult {
	var out []JoinResult
	for _, fx := range NamedEventFixtures(orgID) {
		for _, ev := range fx.Events {
			if ev.OrganizationID == "" {
				ev.OrganizationID = orgID
			}
			out = append(out, IngestEvent(store, ev))
		}
	}
	return out
}

// FixtureJSON returns the named sequences as JSON objects for schema tests.
func FixtureJSON(orgID string) ([]byte, error) {
	type file struct {
		Schema   string         `json:"schema"`
		Fixtures []NamedFixture `json:"fixtures"`
	}
	return json.Marshal(file{Schema: EventSchemaV1, Fixtures: NamedEventFixtures(orgID)})
}
