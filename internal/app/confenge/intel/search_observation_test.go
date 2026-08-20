package intel

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func searchObsBody(overrides map[string]any) []byte {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	m := map[string]any{
		"schema":         EventSchemaV1,
		"version":        OrganicDiscoveryContract,
		"type":           EventSearchObservation,
		"source":         ProducerCONFENGEWeb,
		"event_id":       "so-1",
		"receipt_id":     "so-rcpt-1",
		"organic_source": SourceOrganicSearch,
		"asset_id":       "landing-segunda-leitura",
		"landing_path":   "/guias/segunda-leitura",
		"window":         Window28dComplete,
		"eligible":       100,
		"appeared":       40,
		"clicked":        8,
		"engaged":        3,
		"coverage":       CoverageObserved,
		"freshness":      "gsc-top-rows",
		"measurement_at": now.Format(time.RFC3339),
		"record_kind":    RecordKindSynthetic,
		"synthetic":      true,
		"consent_policy": ConsentPolicyNotApplicable,
		"producer_sha":   "abc123",
	}
	for k, v := range overrides {
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	raw, _ := json.Marshal(m)
	return raw
}

func TestParseSearchObservationContractAndRejects(t *testing.T) {
	now := time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)
	obs, err := ParseSearchObservation(searchObsBody(nil), "org-so", now)
	if err != nil {
		t.Fatal(err)
	}
	if obs.Eligible == nil || *obs.Eligible != 100 || obs.QueryClass != "" {
		t.Fatalf("obs=%+v", obs)
	}
	if obs.ConsentPolicy != ConsentPolicyNotApplicable {
		t.Fatalf("consent=%s", obs.ConsentPolicy)
	}

	cases := []struct {
		name string
		body []byte
	}{
		{"missing version", searchObsBody(map[string]any{"version": nil})},
		{"unknown version", searchObsBody(map[string]any{"version": "confenge.search_observation.v0"})},
		{"missing schema", searchObsBody(map[string]any{"schema": nil})},
		{"missing event_id", searchObsBody(map[string]any{"event_id": nil, "receipt_id": "x"})},
		{"negative count", searchObsBody(map[string]any{"clicked": -1})},
		{"invalid window", searchObsBody(map[string]any{"window": "forever"})},
		{"invalid source", searchObsBody(map[string]any{"source": "google"})},
		{"query literal", searchObsBody(map[string]any{"query": "segunda leitura contrato"})},
		{"query_hash", searchObsBody(map[string]any{"query_hash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})},
		{"future measurement", searchObsBody(map[string]any{"measurement_at": now.Add(24 * time.Hour).Format(time.RFC3339)})},
	}
	for _, tc := range cases {
		_, err := ParseSearchObservation(tc.body, "org-so", now)
		if err == nil {
			t.Fatalf("%s: expected 4xx-class error", tc.name)
		}
		if _, ok := err.(EnvelopeError); !ok {
			t.Fatalf("%s: want EnvelopeError got %T %v", tc.name, err, err)
		}
	}

	nullable, err := ParseSearchObservation(searchObsBody(map[string]any{"eligible": nil, "appeared": nil, "clicked": nil, "engaged": nil, "coverage": CoverageUnknown}), "org-so", now)
	if err != nil {
		t.Fatal(err)
	}
	if nullable.Eligible != nil || nullable.Clicked != nil {
		t.Fatal("null counts must stay nil")
	}
	fmt.Printf("SEARCH_OBS_PARSE ok event=%s nullable_counts=true rejects=%d\n", obs.EventID, len(cases))
}

func TestIngestSearchObservationPersistsWithoutChainAndScoreboardProjection(t *testing.T) {
	st := NewMemoryStore()
	now := time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)
	obs, err := ParseSearchObservation(searchObsBody(map[string]any{
		"synthetic": false, "record_kind": RecordKindReal,
	}), "org-so", now)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := PersistSearchObservation(st, obs, now)
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Persisted || !rec.NotALead || rec.AcceptedVersion != OrganicDiscoveryContract {
		t.Fatalf("receipt=%+v", rec)
	}
	replay, err := PersistSearchObservation(st, obs, now)
	if err != nil || !replay.Replay || !replay.Persisted {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	chains, _ := st.ListChains("org-so")
	if len(chains) != 0 {
		t.Fatalf("search observation opened a chain: %+v", chains)
	}
	listed, err := st.ListSearchObservations("org-so", Window28dComplete)
	if err != nil || len(listed) != 1 {
		t.Fatalf("list=%d err=%v", len(listed), err)
	}

	other, err := st.GetSearchObservation("org-other", obs.EventID)
	if err != nil || other != nil {
		t.Fatal("cross-org leak")
	}

	board := ProjectOrganicScoreboard(OrganicScoreboardSources{
		Now: now, Discovery: SearchObservationsToDiscovery(listed),
	})
	if board.Recommendation != RecommendNeedsReal {
		t.Fatalf("recommendation=%s want NEEDS_REAL_EVENT", board.Recommendation)
	}
	if board.CausalProof || board.RealEmpty == false {
		t.Fatalf("board=%+v", board)
	}
	raw, _ := OrganicScoreboardJSON(board)
	if ContainsForbiddenQuery(raw) {
		t.Fatal("scoreboard leaked query")
	}

	st.SetUnavailable(true)
	_, err = PersistSearchObservation(st, SearchObservation{EventID: "x"}, now)
	if err == nil {
		t.Fatal("unavailable store must fail")
	}
	fmt.Printf("SEARCH_OBS_STORE persisted=%v replay=%v chains=%d rec=%s\n", rec.Persisted, replay.Replay, len(chains), board.Recommendation)
}

func TestIngestEventSearchObservationDoesNotCreateUnknownChain(t *testing.T) {
	st := NewMemoryStore()
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	res := IngestEvent(st, CommercialEvent{
		EventID: "so-ev-1", Schema: EventSchemaV1, Version: OrganicDiscoveryContract,
		Type: EventSearchObservation, Source: ProducerCONFENGEWeb,
		OccurredAt: now, OrganizationID: "org-so",
		OrganicSource: SourceOrganicSearch, AssetID: "asset-sl",
		LandingPath: "/guias/segunda-leitura", Window: Window7dComplete,
		Eligible: IntPtr(10), Appeared: IntPtr(4), Clicked: IntPtr(1),
		MeasurementAt: &now, Synthetic: true, RecordKind: RecordKindSynthetic,
		ConsentPolicy: ConsentPolicyAggregate,
	})
	if !res.Persisted || !res.NotALead || res.Replay {
		t.Fatalf("join=%+v", res)
	}
	chains, _ := st.ListChains("org-so")
	if len(chains) != 0 {
		t.Fatalf("chain created: %+v", chains)
	}

	bad := IngestEvent(st, CommercialEvent{
		EventID: "bad-ver", Schema: EventSchemaV1, Version: "nope",
		Type: "not_a_real_type", OccurredAt: now, OrganizationID: "org-so",
	})
	if !bad.Held || len(bad.Exceptions) == 0 {
		t.Fatalf("unknown type must hold without a silent chain: %+v", bad)
	}
	chains, _ = st.ListChains("org-so")
	if len(chains) != 0 {
		t.Fatal("unknown version persisted a chain")
	}
	fmt.Printf("SEARCH_OBS_INGEST persisted=%v not_a_lead=%v unknown_held=%v\n", res.Persisted, res.NotALead, bad.Held)
}

func TestOrganicScoreboardDiscoveryWithoutLeadAndIncludeSynthetic(t *testing.T) {
	now := time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)
	disc := []OrganicDiscoveryAggregate{{
		OrganicSource: SourceOrganicSearch, AssetID: "asset-sl",
		LandingPath: "/guias/segunda-leitura", Window: Window28dComplete,
		Eligible: IntPtr(12), Appeared: IntPtr(5), Clicked: IntPtr(1), Engaged: IntPtr(0),
		Coverage: CoverageObserved, Synthetic: true,
	}}
	empty := ProjectOrganicScoreboard(OrganicScoreboardSources{Now: now, Discovery: disc})
	if empty.Recommendation != RecommendNeedsWebCfg {
		t.Fatalf("synthetic discovery with include_synthetic=0 must not count: %s", empty.Recommendation)
	}
	with := ProjectOrganicScoreboard(OrganicScoreboardSources{Now: now, Discovery: disc, IncludeSynthetic: true})
	if with.Recommendation != RecommendNeedsReal {
		t.Fatalf("synthetic layer should show with flag: %s", with.Recommendation)
	}
	nulls := ProjectOrganicScoreboard(OrganicScoreboardSources{Now: now, Discovery: []OrganicDiscoveryAggregate{{
		OrganicSource: SourceOrganicSearch, Window: Window28dComplete, Coverage: CoverageUnknown,
	}}})
	w := windowByID(nulls.Windows, Window28dComplete)
	sl := w.BySource[indexOfSource(w.BySource, SourceOrganicSearch)]
	elig := layerByID(sl.Layers, LayerEligible)
	if elig == nil || elig.Status != TruthUnknown {
		t.Fatalf("null counts must be UNKNOWN not zero: %+v", elig)
	}
	if elig.Count != 0 {
		t.Fatalf("UNKNOWN inferred a count: %+v", elig)
	}
	absent := ProjectOrganicScoreboard(OrganicScoreboardSources{Now: now, Discovery: []OrganicDiscoveryAggregate{{
		OrganicSource: SourceOrganicSearch, Window: Window28dComplete, Coverage: CoverageAbsent,
	}}})
	w = windowByID(absent.Windows, Window28dComplete)
	sl = w.BySource[indexOfSource(w.BySource, SourceOrganicSearch)]
	elig = layerByID(sl.Layers, LayerEligible)
	if elig.Status != CoverageAbsent {
		t.Fatalf("ABSENT became %s", elig.Status)
	}
	fmt.Printf("SCOREBOARD_DISC rec_default=%s rec_syn=%s unknown=%s absent=%s\n",
		empty.Recommendation, with.Recommendation, elig.Status, elig.Status)
}

func TestOrganicFeedbackNeedMoreDataAndNoUpstreamWrites(t *testing.T) {
	st := NewMemoryStore()
	now := time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)
	exp := ExportOrganicFeedback(st, "org-empty", now, false)
	if exp.CausalProof || len(exp.UpstreamWrites) != 0 {
		t.Fatalf("feedback wrote upstream: %+v", exp)
	}
	if len(exp.Rows) == 0 || exp.Rows[0].Verdict != LearningNeedMore {
		t.Fatalf("empty feedback must be NEED_MORE_DATA: %+v", exp.Rows)
	}
	raw, _ := OrganicFeedbackJSON(exp)
	if ContainsForbiddenQuery(raw) {
		t.Fatal("feedback leaked query")
	}
	low := strings.ToLower(string(raw))
	if strings.Contains(low, "smartlic") || strings.Contains(low, "extra-cli") && strings.Contains(low, `"write"`) {
		t.Fatal("feedback mentioned an upstream write")
	}
	fmt.Printf("FEEDBACK_EMPTY verdict=%s upstream=%d causal=%v\n", exp.Rows[0].Verdict, len(exp.UpstreamWrites), exp.CausalProof)
}
