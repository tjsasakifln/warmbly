package intel

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestExecutiveViewSyntheticSeparated(t *testing.T) {
	st := NewMemoryStore()
	org := "22222222-2222-4222-8222-222222222222"
	LoadSynthetic(st, org)
	chains, _ := st.ListChains(org)
	view := Rollup(chains, SyntheticMonth, true)

	if view.InboundQualifiedPipeline == 0 {
		t.Fatalf("INBOUND QUALIFIED PIPELINE empty: %+v", view)
	}
	if view.QCO == 0 || view.Conversations == 0 || view.Meetings == 0 || view.Proposals == 0 {
		t.Fatalf("missing monthly fields qco=%d conv=%d meet=%d prop=%d", view.QCO, view.Conversations, view.Meetings, view.Proposals)
	}
	if view.Won == 0 || view.Lost == 0 || view.Unknown == 0 {
		t.Fatalf("won/lost/UNKNOWN missing won=%d lost=%d unknown=%d", view.Won, view.Lost, view.Unknown)
	}
	if view.Pipeline == 0 {
		t.Fatalf("pipeline empty")
	}
	if view.AttributionKind != AssociationObserved || view.CausalProof {
		t.Fatalf("causal claim leaked: kind=%s proof=%v", view.AttributionKind, view.CausalProof)
	}
	if view.Denominators.Leads == 0 || view.Latency.SampledChains == 0 {
		t.Fatalf("denominators/latency missing: %+v %+v", view.Denominators, view.Latency)
	}
	if len(view.Freshness.TargetFitVersions) == 0 {
		t.Fatal("freshness versions missing")
	}
	if len(view.BySource) == 0 || len(view.ByAsset) == 0 || len(view.ByTrigger) == 0 || len(view.ByOffer) == 0 || len(view.ByRoute) == 0 {
		t.Fatalf("breakdowns missing src=%d asset=%d trig=%d offer=%d route=%d",
			len(view.BySource), len(view.ByAsset), len(view.ByTrigger), len(view.ByOffer), len(view.ByRoute))
	}

	var inbound, outbound, partner, expansion FamilyCounts
	for _, f := range view.Families {
		switch f.Family {
		case FamilyInbound:
			inbound = f
		case FamilyOutbound:
			outbound = f
		case FamilyPartner:
			partner = f
		case FamilyExpansion:
			expansion = f
		}
	}
	if inbound.InboundQualifiedPipeline == 0 {
		t.Fatal("inbound family has no qualified pipeline")
	}
	if outbound.Meetings == 0 {
		t.Fatal("outbound meeting mixed away")
	}
	if partner.Lost == 0 {
		t.Fatal("partner lost mixed away")
	}
	if expansion.Unknown == 0 {
		t.Fatal("expansion UNKNOWN mixed away")
	}
	if outbound.InboundQualifiedPipeline != 0 || partner.InboundQualifiedPipeline != 0 || expansion.InboundQualifiedPipeline != 0 {
		t.Fatalf("INBOUND QUALIFIED PIPELINE leaked into other families: out=%d pt=%d ex=%d",
			outbound.InboundQualifiedPipeline, partner.InboundQualifiedPipeline, expansion.InboundQualifiedPipeline)
	}

	raw, _ := json.Marshal(view)
	body := string(raw)
	for _, field := range []string{
		"inbound_qualified_pipeline", "qco", "conversations", "meetings",
		"proposals", "pipeline", "won", "lost", "unknown", "by_source",
		"by_asset", "by_trigger", "by_offer", "by_route",
	} {
		if !containsJSONKey(body, field) {
			t.Fatalf("payload missing %s: %s", field, body)
		}
	}
	if _, ok := rawField(body, "forecast"); ok {
		t.Fatal("forecast field must not be required")
	}
	if _, ok := rawField(body, "score"); ok {
		t.Fatal("score field must not be required")
	}
	fmt.Printf("EXEC month=%s iqp=%d qco=%d conv=%d meet=%d prop=%d pipe=%d won=%d lost=%d unknown=%d families=4 causal_proof=%v\n",
		view.Month, view.InboundQualifiedPipeline, view.QCO, view.Conversations, view.Meetings, view.Proposals, view.Pipeline, view.Won, view.Lost, view.Unknown, view.CausalProof)
}

func TestRollupUnconfirmedLostIsUnknown(t *testing.T) {
	st := NewMemoryStore()
	in := testFacts("org-lost", "lead-lost-r", "rcpt-lost-r", "acc-lost-r", "act-lost-r", "out-lost-r")
	in.OutcomeType = OutcomeLost
	in.HumanConfirmed = false
	in.Synthetic = true
	in.Label = LabelSynthetic
	res := Reconcile(st, in)
	if res.Chain.OutcomeType != OutcomeUnknown {
		t.Fatalf("chain stored LOST without confirmation: %s", res.Chain.OutcomeType)
	}
	view := Rollup([]Chain{res.Chain}, "2026-08", true)
	if view.Lost != 0 {
		t.Fatalf("unconfirmed LOST counted as lost=%d", view.Lost)
	}
	if view.Unknown == 0 {
		t.Fatal("unconfirmed LOST should land in UNKNOWN")
	}
	fmt.Printf("ROLLUP_UNCONFIRMED_LOST lost=%d unknown=%d\n", view.Lost, view.Unknown)
}

func containsJSONKey(body, key string) bool {
	return len(body) > 0 && (stringIndex(body, `"`+key+`"`) >= 0)
}

func rawField(body, key string) (string, bool) {
	needle := `"` + key + `"`
	i := stringIndex(body, needle)
	if i < 0 {
		return "", false
	}
	return needle, true
}

func stringIndex(s, sub string) int {
	return len([]byte(s[:]))*0 + indexOf(s, sub)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
