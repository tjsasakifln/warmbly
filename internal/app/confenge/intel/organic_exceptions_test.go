package intel

import (
	"fmt"
	"testing"
	"time"
)

func organicLeadEvent(id, source, asset string) CommercialEvent {
	return CommercialEvent{
		EventID: "ev-" + id, Version: "1", Schema: EventSchemaV1,
		Type: EventLeadReceived, OccurredAt: time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
		IngestedAt: time.Date(2026, 8, 10, 12, 1, 0, 0, time.UTC), Timezone: "UTC",
		LeadID: id, ReceiptID: "rcpt-" + id, IdempotencyKey: "idem-" + id,
		Source: source, OrganicSource: source, AssetID: asset,
		RouteFamily: FamilyInbound, OrganizationID: "org-organic-ex",
		Consent: "web-cfg-consent-ref", RecordKind: RecordKindReal,
	}
}

func TestOrganicConflictsEnterExceptionQueue(t *testing.T) {
	st := NewMemoryStore()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)

	noAsset := organicLeadEvent("lead-no-asset", SourceOrganicSearch, "")
	res := IngestEvent(st, noAsset)
	if !hasCode(res.Exceptions, ExceptionLeadWithoutAssetID) {
		t.Fatalf("lead_without_asset_id missing: %v", codesOf(res.Exceptions))
	}

	badVer := organicLeadEvent("lead-bad-ver", SourceOrganicSearch, "asset-1")
	badVer.AssetVersion = "not a version token"
	res = IngestEvent(st, badVer)
	if !hasCode(res.Exceptions, ExceptionUnknownAssetVersion) {
		t.Fatalf("unknown_asset_version missing: %v", codesOf(res.Exceptions))
	}

	first := organicLeadEvent("lead-conflict", SourceOrganicSearch, "asset-2")
	IngestEvent(st, first)
	second := first
	second.EventID = "ev-lead-conflict-2"
	second.IdempotencyKey = "idem-lead-conflict-2"
	second.OrganicSource = SourceOutbound
	second.Source = SourceOutbound
	res = IngestEvent(st, second)
	if !hasCode(res.Exceptions, ExceptionContradictorySource) {
		t.Fatalf("contradictory_source missing: %v", codesOf(res.Exceptions))
	}

	synReal := organicLeadEvent("lead-syn-real", SourceOrganicSearch, "asset-3")
	synReal.Synthetic = true
	synReal.RecordKind = RecordKindReal
	res = IngestEvent(st, synReal)
	if !hasCode(res.Exceptions, ExceptionSyntheticTreatedAsReal) {
		t.Fatalf("synthetic_treated_as_real missing: %v", codesOf(res.Exceptions))
	}

	act := organicLeadEvent("lead-nocon", SourceOrganicSearch, "asset-4")
	act.Type = EventActionApproved
	act.ActionID = "act-nocon"
	act.Consent = ""
	res = IngestEvent(st, act)
	if !hasCode(res.Exceptions, ExceptionMissingConsent) {
		t.Fatalf("missing_consent missing: %v", codesOf(res.Exceptions))
	}

	pipe := organicLeadEvent("lead-pipe", SourceOrganicSearch, "asset-5")
	pipe.Type = EventPipelineCreated
	// no action, no evidence
	pipe.ActionID = ""
	pipe.EvidenceRef = ""
	res = IngestEvent(st, pipe)
	if !hasCode(res.Exceptions, ExceptionPipelineWithoutEvidence) {
		t.Fatalf("pipeline_without_evidence missing: %v", codesOf(res.Exceptions))
	}

	rev := organicLeadEvent("lead-rev", SourceOrganicSearch, "asset-6")
	rev.Type = EventRevenueEvidenced
	rev.RevenueCents = 180000
	rev.RevenueDocumentID = ""
	res = IngestEvent(st, rev)
	if !hasCode(res.Exceptions, ExceptionRevenueWithoutFinancial) {
		t.Fatalf("revenue_without_financial_event missing: %v", codesOf(res.Exceptions))
	}

	gsc := organicLeadEvent("lead-gsc", SourceOrganicSearch, "asset-7")
	gsc.Query = "como reler contrato publico"
	res = IngestEvent(st, gsc)
	if !hasCode(res.Exceptions, ExceptionGSCQueryOnLead) {
		t.Fatalf("gsc_query_on_lead missing: %v", codesOf(res.Exceptions))
	}

	leadFirst := organicLeadEvent("lead-oo", SourceOrganicSearch, "asset-8")
	leadFirst.OccurredAt = now
	IngestEvent(st, leadFirst)
	oo := organicLeadEvent("lead-oo", SourceOrganicSearch, "asset-8")
	oo.EventID = "ev-lead-oo-won"
	oo.IdempotencyKey = "idem-lead-oo-won"
	oo.Type = EventWon
	oo.ActionID = "act-oo"
	oo.OutcomeID = "out-oo"
	oo.OccurredAt = now.Add(-48 * time.Hour)
	oo.HumanConfirmed = true
	res = IngestEvent(st, oo)
	if !hasCode(res.Exceptions, ExceptionOutOfOrder) && !hasCode(res.Exceptions, ExceptionNegativeLatency) {
		t.Fatalf("out_of_order/negative_latency missing: %v", codesOf(res.Exceptions))
	}

	dup := IngestEvent(st, first)
	if !dup.Replay {
		t.Fatal("duplicate replay not idempotent")
	}

	stored, _ := st.ListExceptions("org-organic-ex")
	need := []string{
		ExceptionLeadWithoutAssetID, ExceptionUnknownAssetVersion, ExceptionContradictorySource,
		ExceptionSyntheticTreatedAsReal, ExceptionMissingConsent, ExceptionPipelineWithoutEvidence,
		ExceptionRevenueWithoutFinancial, ExceptionGSCQueryOnLead,
	}
	seen := map[string]Exception{}
	for _, ex := range stored {
		seen[ex.Code] = ex
		if ex.Owner == "" || ex.Severity == "" || ex.Status == "" || ex.NextAction == "" {
			t.Fatalf("exception %s missing operator fields: %+v", ex.Code, ex)
		}
		if ex.RetryState == "" {
			t.Fatalf("exception %s missing retry_state", ex.Code)
		}
	}
	for _, code := range need {
		ex, ok := seen[code]
		if !ok {
			t.Fatalf("queue missing %s (have %d)", code, len(seen))
		}
		if ex.Owner != ExceptionOwner(code) {
			t.Fatalf("%s owner=%s want %s", code, ex.Owner, ExceptionOwner(code))
		}
	}
	fmt.Printf("ORGANIC_EXCEPTIONS queued=%d codes=%d\n", len(stored), len(need))
}

func TestOrganicReplayDoesNotOpenSecondChain(t *testing.T) {
	st := NewMemoryStore()
	ev := organicLeadEvent("lead-replay", SourceAIReferral, "asset-ai-1")
	ev.LandingPath = "/respostas/x"
	first := IngestEvent(st, ev)
	if !first.Created {
		t.Fatal("first should create")
	}
	outOfOrder := ev
	outOfOrder.EventID = "ev-lead-replay-late"
	outOfOrder.IdempotencyKey = "idem-lead-replay-late"
	outOfOrder.Type = EventReply
	outOfOrder.OccurredAt = ev.OccurredAt.Add(-time.Hour)
	IngestEvent(st, outOfOrder)
	again := IngestEvent(st, ev)
	if again.Created {
		t.Fatal("replay created a second chain")
	}
	chains, _ := st.ListChains("org-organic-ex")
	n := 0
	for _, c := range chains {
		if c.Identity == first.Chain.Identity {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("identity copies=%d", n)
	}
	fmt.Printf("ORGANIC_REPLAY identity=%s chains=%d replay=%v\n", first.Chain.Identity, len(chains), again.Replay)
}
