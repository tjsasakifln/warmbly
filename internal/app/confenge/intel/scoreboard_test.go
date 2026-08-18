package intel

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestProjectScoreboardSevenStagesKeepImpressionLeadProposalCashDistinct(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	view := Rollup(nil, "2026-08", false)
	board := ProjectScoreboard(ScoreboardSources{
		Now: now, InboundHealthReady: true, InboundHealth: "READY",
		Executive: view,
	})
	if len(board.Stages) != 7 {
		t.Fatalf("stages=%d want 7", len(board.Stages))
	}
	wantIDs := []string{
		StageURLsLiveIndexable, StageNonBrandedImpressions, StageCTACompleted,
		StageLeadPersisted, StageQualifiedConversation, StageProposalEmitted, StageRevenueReceived,
	}
	for i, id := range wantIDs {
		if board.Stages[i].ID != id || board.Stages[i].Order != i+1 {
			t.Fatalf("stage %d = %s order=%d", i, board.Stages[i].ID, board.Stages[i].Order)
		}
		if board.Stages[i].SyntheticIncluded {
			t.Fatalf("default synthetic included on %s", id)
		}
		if board.Stages[i].Observation == "" || board.Stages[i].Owner == "" || board.Stages[i].NextAction == "" {
			t.Fatalf("stage %s missing owner/next/observation", id)
		}
	}
	if board.Stages[0].Status != TruthBlocked || board.Stages[1].Status != TruthBlocked {
		t.Fatalf("GSC/index stages must stay BLOCKED: %s %s", board.Stages[0].Status, board.Stages[1].Status)
	}
	if board.Stages[2].Status == board.Stages[6].Status && board.Stages[2].Status == TruthTrue {
		t.Fatal("CTA and receita collapsed")
	}
	if board.CausalProof {
		t.Fatal("causal_proof claimed")
	}
	if len(board.SeparateMetrics) != 4 {
		t.Fatalf("separate metrics=%d", len(board.SeparateMetrics))
	}
	ids := map[string]bool{}
	for _, m := range board.SeparateMetrics {
		ids[m.ID] = true
	}
	for _, id := range []string{MetricPipelineContracted, MetricMRR, MetricChargeCreated, MetricCashReceived} {
		if !ids[id] {
			t.Fatalf("missing separate metric %s", id)
		}
	}
	fmt.Printf("SCOREBOARD stages=7 url=%s gsc=%s cta=%s lead=%s qco=%s proposal=%s receita=%s synthetic=false\n",
		board.Stages[0].Status, board.Stages[1].Status, board.Stages[2].Status,
		board.Stages[3].Status, board.Stages[4].Status, board.Stages[5].Status, board.Stages[6].Status)
}

func TestScoreboardExcludesSyntheticCanaryFromDefault(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	st := NewMemoryStore()
	canary := CommercialEvent{
		EventID: "ev-canary-scoreboard", Version: "1", Schema: EventSchemaV1,
		Type: EventLeadReceived, OccurredAt: now, LeadID: "SYNTHETIC-INBOUND-scoreboard",
		ReceiptID: "SYNTHETIC-INBOUND-scoreboard", Source: "infrastructure_canary",
		OrganizationID: loopOrg, Synthetic: true, RouteFamily: FamilyInbound,
		Query: "segunda-leitura", AssetID: "asset-canary", CTAID: "cta-canary",
		CorrelationID: "corr-canary", Referrer: "https://search.example/ref",
	}
	res := IngestEvent(st, canary)
	if res.Chain.Identity == "" && !res.Held {
		t.Fatalf("canary did not persist: %+v", res)
	}
	realLead := canary
	realLead.EventID = "ev-real-scoreboard"
	realLead.IdempotencyKey = "idem-real-scoreboard"
	realLead.LeadID = "lead-real-scoreboard"
	realLead.ReceiptID = "rcpt-real-scoreboard"
	realLead.Source = "web-cfg"
	realLead.Synthetic = false
	IngestEvent(st, realLead)

	chains, _ := st.ListChains(loopOrg)
	syn := Rollup(chains, "2026-08", true)
	real := Rollup(chains, "2026-08", false)
	if real.Denominators.Leads == 0 {
		// real lead should count
		t.Fatalf("real lead missing from include_synthetic=0: %+v", real)
	}
	if syn.Denominators.Leads <= real.Denominators.Leads && syn.ChainCount <= real.ChainCount {
		// labeled include should see the extra synthetic chain
		t.Fatalf("include_synthetic=1 did not add the canary: syn=%+v real=%+v", syn, real)
	}
	board := ProjectScoreboard(ScoreboardSources{
		Now: now, InboundHealthReady: true, IncludeSynthetic: false,
		LeadPersistedCount: real.Denominators.Leads, CTACompletedCount: real.Denominators.Leads,
		InboundNowCount: real.Denominators.Leads, Executive: real,
	})
	if board.IncludeSynthetic {
		t.Fatal("default scoreboard included synthetic")
	}
	if board.Stages[3].Status != TruthTrue {
		t.Fatalf("real lead not TRUE: %s", board.Stages[3].Status)
	}
	if ScoreboardExcludesSynthetic(false, true) != true {
		t.Fatal("canary must be excluded from default scoreboard")
	}
	fmt.Printf("SCOREBOARD_CANARY include0_leads=%d include1_leads=%d default_synthetic=%v\n",
		real.Denominators.Leads, syn.Denominators.Leads, board.IncludeSynthetic)
}

func TestScoreboardWonLostReceitaWithoutEvidenceStayUnknownOrBlocked(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	st := NewMemoryStore()
	won := CommercialEvent{
		EventID: "ev-won-no-evidence", Version: "1", Type: EventWon, OccurredAt: now,
		LeadID: "lead-won-bare", OrganizationID: loopOrg, HumanConfirmed: false,
		RouteFamily: FamilyInbound, ActionID: "act-won-bare",
	}
	res := IngestEvent(st, won)
	if !hasCode(res.Exceptions, ExceptionUnconfirmedWon) {
		t.Fatalf("unconfirmed WON silent: %+v", codesOf(res.Exceptions))
	}
	if res.Exceptions[0].Owner == "" || res.Exceptions[0].NextAction == "" {
		t.Fatalf("exception missing owner/next: %+v", res.Exceptions[0])
	}
	view := Rollup([]Chain{res.Chain}, "2026-08", false)
	if view.Won != 0 {
		t.Fatalf("unconfirmed WON counted: %d", view.Won)
	}
	board := ProjectScoreboard(ScoreboardSources{
		Now: now, InboundHealthReady: true, LeadPersistedCount: 1, Executive: view,
	})
	if board.Stages[6].Status == TruthTrue {
		t.Fatal("receita TRUE without a document")
	}
	fmt.Printf("SCOREBOARD_UNCONFIRMED won_rollup=%d receita=%s owner=%s\n",
		view.Won, board.Stages[6].Status, res.Exceptions[0].Owner)
}

func TestIngestUnknownOutOfOrderRetryUnavailableHaveOwnerReasonNext(t *testing.T) {
	now := time.Date(2026, 8, 18, 15, 0, 0, 0, time.UTC)
	st := NewMemoryStore()

	unk := IngestEvent(st, CommercialEvent{
		EventID: "ev-unknown-type", Version: "1", Type: "totally_unknown_event",
		OccurredAt: now, OrganizationID: loopOrg, LeadID: "lead-unk-evt",
	})
	if !hasCode(unk.Exceptions, ExceptionUnknownProviderEvent) && !unk.Held && len(unk.Exceptions) == 0 {
		t.Fatalf("unknown event silent drop: %+v", unk)
	}
	for _, ex := range unk.Exceptions {
		if ex.Owner == "" || ex.Reason == "" || ex.NextAction == "" {
			t.Fatalf("unknown exception incomplete: %+v", ex)
		}
	}

	first := IngestEvent(st, CommercialEvent{
		EventID: "ev-order-action", Version: "1", Type: EventActionExecuted,
		OccurredAt: now, OrganizationID: loopOrg, LeadID: "lead-order",
		ActionID: "act-order", RouteFamily: FamilyInbound,
	})
	if first.Chain.Identity == "" {
		t.Fatalf("action did not persist: %+v", first)
	}
	ooo := IngestEvent(st, CommercialEvent{
		EventID: "ev-order-outcome", Version: "1", Type: EventMeeting,
		OccurredAt: now.Add(-2 * time.Hour), OrganizationID: loopOrg,
		LeadID: "lead-order", ActionID: "act-order", OutcomeID: "out-order",
		RouteFamily: FamilyInbound,
	})
	if !hasCode(ooo.Exceptions, ExceptionOutOfOrder) {
		t.Fatalf("out-of-order silent: %+v", codesOf(ooo.Exceptions))
	}
	if !ooo.Held {
		t.Fatal("out-of-order not held")
	}

	retry := IngestEvent(st, CommercialEvent{
		EventID: "ev-order-action", Version: "1", Type: EventActionExecuted,
		OccurredAt: now, OrganizationID: loopOrg, LeadID: "lead-order",
		ActionID: "act-order", RouteFamily: FamilyInbound,
	})
	if !retry.Replay {
		t.Fatal("retry did not replay")
	}
	if retry.Created {
		t.Fatal("retry created a second chain")
	}

	down := Reconcile(nil, ObservedFacts{
		Keys: JoinKeys{LeadID: "lead-down", OrganizationID: loopOrg},
	})
	if !hasCode(down.Exceptions, ExceptionUnavailable) {
		t.Fatalf("unavailable silent: %+v", down)
	}
	if down.Exceptions[0].Owner == "" || down.Exceptions[0].NextAction == "" {
		t.Fatalf("unavailable incomplete: %+v", down.Exceptions[0])
	}
	fmt.Printf("EXCEPTION_CASES unknown=%v ooo=%v retry=%v unavailable=%v\n",
		codesOf(unk.Exceptions), codesOf(ooo.Exceptions), retry.Replay, codesOf(down.Exceptions))
}

func TestScoreboardJSONRoundTripHasRequiredFields(t *testing.T) {
	board := ProjectScoreboard(ScoreboardSources{
		Now:                time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC),
		InboundHealthReady: false, Executive: Rollup(nil, "2026-08", false),
	})
	raw, err := json.Marshal(board)
	if err != nil {
		t.Fatal(err)
	}
	var again Scoreboard
	if err := json.Unmarshal(raw, &again); err != nil {
		t.Fatal(err)
	}
	if again.SchemaVersion != ScoreboardSchemaV1 || len(again.Stages) != 7 {
		t.Fatalf("roundtrip %+v", again)
	}
	if again.ProductionPath != "BLOCKED" || again.HumanBlocker == "" {
		t.Fatalf("blocked path missing: %+v", again)
	}
}
