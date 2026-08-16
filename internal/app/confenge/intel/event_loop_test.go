package intel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const loopOrg = "org-inbound-learning-47"

func TestIngestNamedFixturesIdempotentReplay(t *testing.T) {
	st := NewMemoryStore()
	first := LoadNamedEventFixtures(st, loopOrg)
	if len(first) == 0 {
		t.Fatal("no fixture events ingested")
	}
	second := LoadNamedEventFixtures(st, loopOrg)
	if len(second) == 0 {
		t.Fatal("replay produced no results")
	}
	var replay int
	for _, r := range second {
		if r.Replay {
			replay++
		}
		if r.Created {
			t.Fatalf("replay created a second chain: %+v", r.Chain.Identity)
		}
	}
	if replay == 0 {
		t.Fatal("second load did not mark replay")
	}
	chains, _ := st.ListChains(loopOrg)
	ids := map[string]int{}
	for _, c := range chains {
		ids[c.Identity]++
		if MetricKeyContainsPII(c.MetricKey) {
			t.Fatalf("metric key has PII: %s", c.MetricKey)
		}
		if c.CausalProof {
			t.Fatal("causal_proof claimed")
		}
	}
	for id, n := range ids {
		if n != 1 {
			t.Fatalf("identity %s has %d chains", id, n)
		}
	}
	fmt.Printf("INGEST_REPLAY events=%d replay=%d chains=%d\n", len(second), replay, len(chains))
}

func TestMarketAnswerCompleteJoinsOneChain(t *testing.T) {
	st := NewMemoryStore()
	fx := fixtureByName(t, FixtureMarketAnswerComplete)
	var last JoinResult
	for _, ev := range fx.Events {
		last = IngestEvent(st, ev)
	}
	if last.Chain.Identity != "lead:lead-ma-complete" {
		t.Fatalf("identity=%s", last.Chain.Identity)
	}
	if !last.Chain.Qualified || last.Chain.OutcomeType != OutcomeWon || !last.Chain.HumanConfirmed {
		t.Fatalf("complete MA chain not won: %+v", last.Chain)
	}
	if !last.Chain.RevenueEvidenced || last.Chain.RevenueCents <= 0 {
		t.Fatal("revenue not evidenced on complete MA path")
	}
	chains, _ := st.ListChains(loopOrg)
	if len(chains) != 1 {
		t.Fatalf("chains=%d want 1", len(chains))
	}
	fmt.Printf("MA_COMPLETE identity=%s won=%s revenue=%d\n", last.Chain.Identity, last.Chain.OutcomeType, last.Chain.RevenueCents)
}

func TestContractAnalysisMissingOutcomeStaysUnknown(t *testing.T) {
	st := NewMemoryStore()
	fx := fixtureByName(t, FixtureContractAnalysisMissingOutcome)
	var last JoinResult
	for _, ev := range fx.Events {
		last = IngestEvent(st, ev)
	}
	if isWonType(last.Chain.OutcomeType) || isLostType(last.Chain.OutcomeType) {
		t.Fatalf("missing outcome inferred close: %s", last.Chain.OutcomeType)
	}
	if last.Chain.OutcomeType != OutcomeUnknown && last.Chain.OutcomeType != "" {
		t.Fatalf("expected UNKNOWN, got %s", last.Chain.OutcomeType)
	}
	view := Rollup([]Chain{last.Chain}, SyntheticMonth, true)
	if view.Won != 0 || view.Lost != 0 {
		t.Fatalf("CA missing outcome counted won=%d lost=%d", view.Won, view.Lost)
	}
	fmt.Printf("CA_MISSING_OUTCOME outcome=%s won=%d lost=%d\n", last.Chain.OutcomeType, view.Won, view.Lost)
}

func TestXRayCompletionIsNotPipelineOrLead(t *testing.T) {
	st := NewMemoryStore()
	fx := fixtureByName(t, FixtureXRayCompletionNoHandRaise)
	res := IngestEvent(st, fx.Events[0])
	if !res.Chain.NotALead {
		t.Fatal("X-Ray completion treated as a lead")
	}
	if res.Chain.PipelineOpen {
		t.Fatal("X-Ray completion became pipeline")
	}
	if res.Chain.Qualified {
		t.Fatal("X-Ray completion marked qualified")
	}
	view := Rollup([]Chain{res.Chain}, SyntheticMonth, true)
	if view.InboundQualifiedPipeline != 0 || view.Pipeline != 0 || view.Denominators.Leads != 0 {
		t.Fatalf("X-Ray leaked into pipeline/leads: %+v", view)
	}
	if view.B2GXRay.Completions == 0 {
		t.Fatal("X-Ray completion missing from b2g_xray slice")
	}
	fmt.Printf("XRAY_NOT_PIPELINE completions=%d iqp=%d pipe=%d\n", view.B2GXRay.Completions, view.InboundQualifiedPipeline, view.Pipeline)
}

func TestOrphanActionLandsOnExceptionQueue(t *testing.T) {
	st := NewMemoryStore()
	fx := fixtureByName(t, FixtureOrphanAction)
	res := IngestEvent(st, fx.Events[0])
	if !hasCode(res.Exceptions, ExceptionOrphan) {
		t.Fatalf("orphan action not excepted: %+v", codesOf(res.Exceptions))
	}
	if !res.Held {
		t.Fatal("orphan action not held")
	}
	xs, _ := st.ListExceptions(loopOrg)
	found := false
	for _, ex := range xs {
		if ex.Code == ExceptionOrphan {
			found = true
		}
	}
	if !found {
		t.Fatal("orphan not persisted")
	}
	fmt.Printf("ORPHAN_ACTION held=%v codes=%v\n", res.Held, codesOf(res.Exceptions))
}

func TestLateCorrectionMergesWithoutSecondChain(t *testing.T) {
	st := NewMemoryStore()
	fx := fixtureByName(t, FixtureLateCorrection)
	var last JoinResult
	for _, ev := range fx.Events {
		last = IngestEvent(st, ev)
	}
	chains, _ := st.ListChains(loopOrg)
	if len(chains) != 1 {
		t.Fatalf("late correction opened extra chains=%d", len(chains))
	}
	if last.Chain.OutcomeType != OutcomeLost || !last.Chain.HumanConfirmed {
		t.Fatalf("late correction not applied: %+v", last.Chain)
	}
	if !last.Chain.CorrectionApplied {
		t.Fatal("correction flag unset")
	}
	fmt.Printf("LATE_CORRECTION outcome=%s chains=%d\n", last.Chain.OutcomeType, len(chains))
}

func TestPipelineWithoutRevenueDoesNotCountRevenue(t *testing.T) {
	st := NewMemoryStore()
	fx := fixtureByName(t, FixturePipelineWithoutRevenue)
	var last JoinResult
	for _, ev := range fx.Events {
		last = IngestEvent(st, ev)
	}
	if !last.Chain.PipelineOpen {
		t.Fatal("pipeline event did not open pipeline")
	}
	if last.Chain.RevenueEvidenced || last.Chain.RevenueCents != 0 {
		t.Fatalf("pipeline inferred revenue: evidenced=%v cents=%d", last.Chain.RevenueEvidenced, last.Chain.RevenueCents)
	}
	view := Rollup([]Chain{last.Chain}, SyntheticMonth, true)
	if view.Pipeline == 0 {
		t.Fatal("pipeline count missing")
	}
	if view.RevenueCents != 0 || view.RevenueStatus == "evidenced" {
		t.Fatalf("pipeline counted as revenue: %+v", view)
	}
	fmt.Printf("PIPE_NO_REVENUE pipe=%d revenue=%s cents=%d\n", view.Pipeline, view.RevenueStatus, view.RevenueCents)
}

func TestLostAndUnknownStayHonest(t *testing.T) {
	st := NewMemoryStore()
	fx := fixtureByName(t, FixtureLostUnknown)
	for _, ev := range fx.Events {
		IngestEvent(st, ev)
	}
	view := Rollup(mustList(st, loopOrg), SyntheticMonth, true)
	if view.Lost != 1 {
		t.Fatalf("lost=%d want 1", view.Lost)
	}
	if view.Unknown == 0 {
		t.Fatal("UNKNOWN chain missing")
	}
	if view.Won != 0 {
		t.Fatalf("lost/unknown invented won=%d", view.Won)
	}
	fmt.Printf("LOST_UNKNOWN lost=%d unknown=%d won=%d\n", view.Lost, view.Unknown, view.Won)
}

func TestOutboundStaysOffInboundDenominators(t *testing.T) {
	st := NewMemoryStore()
	fx := fixtureByName(t, FixtureOutboundLane)
	for _, ev := range fx.Events {
		IngestEvent(st, ev)
	}
	view := Rollup(mustList(st, loopOrg), SyntheticMonth, true)
	if view.InboundQualifiedPipeline != 0 {
		t.Fatalf("outbound leaked into IQP=%d", view.InboundQualifiedPipeline)
	}
	var outbound FamilyCounts
	for _, f := range view.Families {
		if f.Family == FamilyOutbound {
			outbound = f
		}
	}
	if outbound.Meetings == 0 {
		t.Fatal("outbound meeting missing from outbound lane")
	}
	if outbound.InboundQualifiedPipeline != 0 {
		t.Fatal("outbound family carried IQP")
	}
	fmt.Printf("LANE_OUTBOUND iqp=%d out_meet=%d\n", view.InboundQualifiedPipeline, outbound.Meetings)
}

func TestNoLeadToPipelineInference(t *testing.T) {
	st := NewMemoryStore()
	ev := fixtureByName(t, FixtureMarketAnswerComplete).Events[0]
	res := IngestEvent(st, ev)
	if res.Chain.PipelineOpen {
		t.Fatal("lead_received inferred pipeline")
	}
	fmt.Printf("NO_LEAD_PIPELINE identity=%s pipe=%v\n", res.Chain.Identity, res.Chain.PipelineOpen)
}

func TestImpossibleTransitionHeld(t *testing.T) {
	st := NewMemoryStore()
	ev := CommercialEvent{
		EventID: "ev-imp-rev", Version: "1", Schema: EventSchemaV1,
		Type: EventRevenueEvidenced, OccurredAt: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
		IngestedAt: time.Date(2026, 8, 4, 12, 1, 0, 0, time.UTC), Timezone: "UTC",
		OrganizationID: loopOrg, LeadID: "lead-imp-1", ReceiptID: "rcpt-imp-1",
		IdempotencyKey: "idem-imp-rev", RouteFamily: FamilyInbound, Source: "web-cfg",
		Query: "x", AssetID: "a", RevenueCents: 10, RevenueDocumentID: "doc-x",
		Synthetic: true,
	}
	res := IngestEvent(st, ev)
	if !hasCode(res.Exceptions, ExceptionImpossibleTransition) {
		t.Fatalf("revenue without pipeline/won not held: %+v", codesOf(res.Exceptions))
	}
	if !res.Held {
		t.Fatal("impossible revenue must be held")
	}
	if res.Chain.RevenueEvidenced || res.Chain.RevenueCents != 0 {
		t.Fatalf("held revenue_evidenced wrote revenue onto the chain: evidenced=%v cents=%d", res.Chain.RevenueEvidenced, res.Chain.RevenueCents)
	}
	if isWonType(res.Chain.OutcomeType) || isLostType(res.Chain.OutcomeType) {
		t.Fatalf("held revenue invented close: %s", res.Chain.OutcomeType)
	}
	saved, _ := st.GetChain(loopOrg, res.Chain.Identity)
	if saved == nil || saved.RevenueEvidenced || saved.RevenueCents != 0 {
		t.Fatalf("store kept held revenue: %+v", saved)
	}
	view := Rollup(mustList(st, loopOrg), SyntheticMonth, true)
	if view.RevenueCents != 0 || view.RevenueStatus == "evidenced" {
		t.Fatalf("held revenue counted: status=%s cents=%d", view.RevenueStatus, view.RevenueCents)
	}
	if view.Won != 0 || view.Pipeline != 0 {
		t.Fatalf("held revenue counted won=%d pipe=%d", view.Won, view.Pipeline)
	}
	fmt.Printf("IMPOSSIBLE_TRANSITION codes=%v held=%v revenue_cents=%d evidenced=%v\n", codesOf(res.Exceptions), res.Held, res.Chain.RevenueCents, res.Chain.RevenueEvidenced)
}

func TestNegativeDurationFailsReconcile(t *testing.T) {
	st := NewMemoryStore()
	var last JoinResult
	for _, ev := range NegativeDurationFixture(loopOrg) {
		last = IngestEvent(st, ev)
	}
	if !hasCode(last.Exceptions, ExceptionNegativeLatency) && !hasCode(last.Exceptions, ExceptionOutOfOrder) {
		t.Fatalf("negative duration not failed: %+v", codesOf(last.Exceptions))
	}
	if !last.Held {
		t.Fatal("negative duration not held")
	}
	sample := latencySample(last.Chain)
	if sample.LeadToFirstAction > 0 && last.Chain.FirstActionAt != nil && last.Chain.FirstActionAt.Before(last.Chain.LeadCreatedAt) {
		t.Fatalf("negative duration coerced to %d", sample.LeadToFirstAction)
	}
	if sample.SampledChains == 1 && hasInvertedLatency(last.Chain) {
		t.Fatal("inverted latency entered the sample")
	}
	fmt.Printf("NEG_DURATION held=%v codes=%v sampled=%d\n", last.Held, codesOf(last.Exceptions), sample.SampledChains)
}

func TestPIIExcludedFromMetricsAndReport(t *testing.T) {
	st := NewMemoryStore()
	LoadNamedEventFixtures(st, loopOrg)
	rep := BuildObservabilityReport(st, loopOrg, SyntheticMonth, true)
	raw, err := ReportJSON(rep)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ToLower(string(raw))
	for _, tok := range []string{"@", "email=", "phone=", "cnpj=", "ana silva", "empresa ltda"} {
		if strings.Contains(body, tok) {
			t.Fatalf("PII token %q in report", tok)
		}
	}
	if ReportHasPII([]byte(`{"email":"a@b.com"}`)) == false {
		t.Fatal("detector missed email")
	}
	for _, c := range mustList(st, loopOrg) {
		if MetricKeyContainsPII(c.MetricKey) {
			t.Fatalf("metric key PII: %s", c.MetricKey)
		}
	}
	fmt.Printf("PII_SCAN report_bytes=%d clean=true\n", len(raw))
}

func TestSchemaCompatibleFixtureJSON(t *testing.T) {
	raw, err := FixtureJSON(loopOrg)
	if err != nil {
		t.Fatal(err)
	}
	var wrap struct {
		Schema   string         `json:"schema"`
		Fixtures []NamedFixture `json:"fixtures"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		t.Fatal(err)
	}
	if wrap.Schema != EventSchemaV1 {
		t.Fatalf("schema=%s", wrap.Schema)
	}
	if len(wrap.Fixtures) < 9 {
		t.Fatalf("fixtures=%d want >=9", len(wrap.Fixtures))
	}
	st := NewMemoryStore()
	for _, fx := range wrap.Fixtures {
		for _, ev := range fx.Events {
			enc, _ := json.Marshal(ev)
			parsed, err := ParseCommercialEvent(enc)
			if err != nil {
				t.Fatalf("parse %s %s: %v", fx.Name, ev.EventID, err)
			}
			IngestEvent(st, parsed)
		}
	}
	fmt.Printf("SCHEMA_FIXTURES n=%d schema=%s\n", len(wrap.Fixtures), wrap.Schema)
}

func TestNoAutoSendOrEmailSideEffects(t *testing.T) {
	rep := RunFixtureReport(loopOrg, SyntheticMonth, true)
	if rep.AutoSend || rep.EmailSideEffects {
		t.Fatalf("report claimed send side effects: %+v", rep)
	}
	if len(rep.UpstreamWrites) != 0 {
		t.Fatalf("upstream writes: %v", rep.UpstreamWrites)
	}
	fmt.Printf("NO_SEND auto_send=%v email=%v upstream=%d\n", rep.AutoSend, rep.EmailSideEffects, len(rep.UpstreamWrites))
}

func TestLearningVerdictsAreExact(t *testing.T) {
	st := NewMemoryStore()
	LoadNamedEventFixtures(st, loopOrg)
	emitLoopLearning(st, loopOrg)
	cands, _ := st.ListLearning(loopOrg)
	if len(cands) == 0 {
		t.Fatal("no learning candidates")
	}
	allowed := map[string]bool{
		LearningRepeat: true, LearningChange: true, LearningStop: true, LearningNeedMore: true,
	}
	for _, c := range cands {
		if !allowed[normalizeLearningVerdict(c.Recommendation)] {
			t.Fatalf("verdict %q not in REPEAT|CHANGE|STOP|NEED_MORE_DATA", c.Recommendation)
		}
		if c.CausalProof {
			t.Fatal("learning claimed causal proof")
		}
		if len(c.UpstreamWrites) != 0 {
			t.Fatalf("learning wrote upstream: %v", c.UpstreamWrites)
		}
	}
	fmt.Printf("LEARNING_VERDICTS n=%d\n", len(cands))
}

func TestIncludeSyntheticZeroOnFixtureStore(t *testing.T) {
	st := NewMemoryStore()
	LoadNamedEventFixtures(st, loopOrg)
	rep := BuildObservabilityReport(st, loopOrg, SyntheticMonth, false)
	if !rep.RealEmpty {
		t.Fatalf("real report not empty: %+v", rep)
	}
	if rep.InboundQualifiedPipeline != 0 || rep.ValidLeads != 0 || rep.Won != 0 {
		t.Fatalf("synthetics leaked: iqp=%d leads=%d won=%d", rep.InboundQualifiedPipeline, rep.ValidLeads, rep.Won)
	}
	if rep.Latency.Baseline == BaselineObserved {
		t.Fatal("empty real store claimed BASELINE_OBSERVED")
	}
	fmt.Printf("REAL_EMPTY_FIXTURES iqp=%d baseline=%s real_empty=%v\n", rep.InboundQualifiedPipeline, rep.Latency.Baseline, rep.RealEmpty)
}

func TestRunFixtureReportTwiceStable(t *testing.T) {
	a := RunFixtureReport(loopOrg, SyntheticMonth, true)
	b := RunFixtureReport(loopOrg, SyntheticMonth, true)
	if a.InboundQualifiedPipeline == 0 {
		t.Fatal("IQP missing from fixture report")
	}
	if a.InboundQualifiedPipeline != b.InboundQualifiedPipeline || a.Won != b.Won || a.Lost != b.Lost {
		t.Fatalf("unstable report a=%+v b=%+v", a, b)
	}
	if a.Latency.Baseline != BaselineSynthetic || b.Latency.Baseline != BaselineSynthetic {
		t.Fatalf("baseline=%s/%s", a.Latency.Baseline, b.Latency.Baseline)
	}
	if a.CausalProof || len(a.UpstreamWrites) != 0 {
		t.Fatal("causal or upstream leak")
	}
	if a.Lanes[LaneOutbound] == 0 || a.Lanes[LaneMarketAnswer] == 0 {
		t.Fatalf("lanes missing: %+v", a.Lanes)
	}
	if a.ExceptionCounts[ExceptionOrphan] == 0 {
		t.Fatal("orphan fixture missing from exception counts")
	}
	if a.Recommendation != RecommendReady && a.Recommendation != RecommendAdjust && a.Recommendation != RecommendNoGo {
		t.Fatalf("recommendation=%s", a.Recommendation)
	}
	found := false
	for _, c := range a.LearningCandidates {
		switch c.Recommendation {
		case LearningRepeat, LearningChange, LearningStop, LearningNeedMore:
			found = true
		default:
			t.Fatalf("bad verdict %s", c.Recommendation)
		}
	}
	if !found {
		t.Fatal("no learning candidates")
	}
	raw, _ := ReportJSON(a)
	md := ReportMarkdown(a)
	if !strings.Contains(md, "INBOUND QUALIFIED PIPELINE") {
		t.Fatal("markdown missing IQP")
	}
	if ReportHasPII(raw) {
		t.Fatal("report JSON flagged as PII")
	}
	fmt.Printf("REPORT_STABLE iqp=%d won=%d lost=%d unknown=%d baseline=%s rec=%s\n",
		a.InboundQualifiedPipeline, a.Won, a.Lost, a.Unknown, a.Latency.Baseline, a.Recommendation)
}

func TestOrderingOutOfOrderHeld(t *testing.T) {
	st := NewMemoryStore()
	lead := fixtureByName(t, FixtureMarketAnswerComplete).Events[0]
	out := lead
	out.EventID = "ev-ooo-outcome"
	out.Type = EventWon
	out.ActionID = "act-ooo"
	out.OutcomeID = "out-ooo"
	out.IdempotencyKey = "idem-ooo-won"
	out.HumanConfirmed = true
	out.OutcomeState = OutcomeWon
	out.OccurredAt = lead.OccurredAt.Add(-3 * time.Hour)
	res := IngestEvent(st, lead)
	if !res.Created && res.Chain.Identity == "" {
		t.Fatal("lead should join")
	}
	ooo := IngestEvent(st, out)
	if !hasCode(ooo.Exceptions, ExceptionOutOfOrder) && !hasCode(ooo.Exceptions, ExceptionNegativeLatency) {
		t.Fatalf("out-of-order not held: %+v", codesOf(ooo.Exceptions))
	}
	if !ooo.Held {
		t.Fatal("out-of-order won must be held")
	}
	if ooo.Chain.OutcomeType == OutcomeWon || (isWonType(ooo.Chain.OutcomeType) && ooo.Chain.HumanConfirmed) {
		t.Fatalf("held out-of-order WON landed on the chain: %s confirmed=%v", ooo.Chain.OutcomeType, ooo.Chain.HumanConfirmed)
	}
	saved, _ := st.GetChain(loopOrg, ooo.Chain.Identity)
	if saved == nil || saved.OutcomeType == OutcomeWon || saved.HumanConfirmed {
		t.Fatalf("store kept held WON: %+v", saved)
	}
	view := Rollup(mustList(st, loopOrg), SyntheticMonth, true)
	if view.Won != 0 {
		t.Fatalf("held out-of-order WON counted as won=%d", view.Won)
	}
	fmt.Printf("OUT_OF_ORDER codes=%v held=%v outcome=%s won=%d\n", codesOf(ooo.Exceptions), ooo.Held, ooo.Chain.OutcomeType, view.Won)
}

func TestOmittedClocksStayCensored(t *testing.T) {
	ev := fixtureByName(t, FixturePipelineWithoutRevenue).Events[0]
	ev.PublishedAt = nil
	ev.DetectedAt = nil
	facts := EventToFacts(ev)
	if facts.PublishedAt != nil || facts.DetectedAt != nil {
		t.Fatalf("EventToFacts invented clocks published=%v detected=%v", facts.PublishedAt, facts.DetectedAt)
	}
	st := NewMemoryStore()
	res := IngestEvent(st, ev)
	if res.Chain.PublishedAt != nil || res.Chain.DetectedAt != nil {
		t.Fatalf("ingest invented clocks published=%v detected=%v", res.Chain.PublishedAt, res.Chain.DetectedAt)
	}
	sample := latencySample(res.Chain)
	if sample.PublishedToDetected != 0 || sample.DetectedToLead != 0 {
		t.Fatalf("omitted clocks measured as durations pub=%d det=%d", sample.PublishedToDetected, sample.DetectedToLead)
	}
	if sample.CensoredCycles < 2 {
		t.Fatalf("omitted clocks were not censored: %+v", sample)
	}
	fmt.Printf("OMITTED_CLOCKS published=%v detected=%v censored=%d pub_ms=%d det_ms=%d\n",
		res.Chain.PublishedAt, res.Chain.DetectedAt, sample.CensoredCycles, sample.PublishedToDetected, sample.DetectedToLead)
}

func TestAssetAttributionSlices(t *testing.T) {
	rep := RunFixtureReport(loopOrg, SyntheticMonth, true)
	if rep.MarketAnswer.Leads == 0 {
		t.Fatal("market_answer slice empty")
	}
	if rep.ContractAnalysis.Leads == 0 {
		t.Fatal("contract_analysis slice empty")
	}
	if rep.B2GXRay.Completions == 0 {
		t.Fatal("b2g_xray completions empty")
	}
	fmt.Printf("SLICES ma=%d ca=%d xray=%d\n", rep.MarketAnswer.Leads, rep.ContractAnalysis.Leads, rep.B2GXRay.Completions)
}

func fixtureByName(t *testing.T, name string) NamedFixture {
	t.Helper()
	for _, fx := range NamedEventFixtures(loopOrg) {
		if fx.Name == name {
			return fx
		}
	}
	t.Fatalf("missing fixture %s", name)
	return NamedFixture{}
}

func TestWriteContractFixtures(t *testing.T) {
	raw, err := FixtureJSON(loopOrg)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join("testdata", "inbound_learning")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.v1.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
