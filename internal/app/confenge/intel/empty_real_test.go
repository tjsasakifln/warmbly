package intel

import (
	"fmt"
	"testing"
)

func TestRealEmptyStoreNotFilledFromFixtures(t *testing.T) {
	st := NewMemoryStore()
	org := "33333333-3333-4333-8333-333333333333"
	// Fixtures exist as a function but are not loaded into this store.
	_ = SyntheticFacts(org)
	chains, _ := st.ListChains(org)
	view := Rollup(chains, SyntheticMonth, false)
	if view.ChainCount != 0 || !view.RealEmpty {
		t.Fatalf("empty store not empty: count=%d real_empty=%v", view.ChainCount, view.RealEmpty)
	}
	if view.InboundQualifiedPipeline != 0 || view.QCO != 0 || view.Conversations != 0 || view.Meetings != 0 || view.Proposals != 0 || view.Pipeline != 0 || view.Won != 0 || view.Lost != 0 {
		t.Fatalf("real rollup invented counts: %+v", view)
	}
	if view.Unknown != 0 {
		t.Fatalf("unknown count should be zero on an empty store, got %d", view.Unknown)
	}
	if view.AttributionKind != AssociationObserved || view.CausalProof {
		t.Fatal("empty view claimed causation")
	}

	// Loading synthetics must not leak into includeSynthetic=false.
	LoadSynthetic(st, org)
	realOnly := Rollup(mustList(st, org), SyntheticMonth, false)
	if realOnly.ChainCount != 0 || !realOnly.RealEmpty {
		t.Fatalf("synthetic leaked into real rollup: %+v", realOnly)
	}
	labeled := Rollup(mustList(st, org), SyntheticMonth, true)
	if labeled.ChainCount == 0 || labeled.RealEmpty {
		t.Fatal("synthetic include path empty")
	}
	if realOnly.Latency.Baseline != "insufficient_data" {
		t.Fatalf("empty real latency baseline=%q", realOnly.Latency.Baseline)
	}
	fmt.Printf("REAL_EMPTY iqp=%d qco=%d won=%d lost=%d unknown=%d real_empty=%v synthetic_count=%d latency_baseline=%s\n",
		realOnly.InboundQualifiedPipeline, realOnly.QCO, realOnly.Won, realOnly.Lost, realOnly.Unknown, realOnly.RealEmpty, labeled.ChainCount, realOnly.Latency.Baseline)
}

func mustList(st Store, org string) []Chain {
	ch, _ := st.ListChains(org)
	return ch
}
