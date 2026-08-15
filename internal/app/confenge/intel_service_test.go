package confenge

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
)

func TestCommercialExecutiveViewEmptyReal(t *testing.T) {
	svc, _, org := inboundTestService(t)
	view, xerr := svc.CommercialExecutiveView(context.Background(), org, intel.SyntheticMonth, false)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if view.InboundQualifiedPipeline != 0 || view.QCO != 0 || view.Won != 0 || !view.RealEmpty {
		t.Fatalf("empty real view invented data: %+v", view)
	}
	fmt.Printf("SERVICE_REAL_EMPTY iqp=%d qco=%d real_empty=%v\n", view.InboundQualifiedPipeline, view.QCO, view.RealEmpty)
}

func TestCommercialIntelJoinAndLearningViaService(t *testing.T) {
	svc, _, org := inboundTestService(t)
	facts := intel.SyntheticFacts(org.String())[1]
	res, xerr := svc.ReconcileCommercialIntel(context.Background(), org, facts)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if !res.Created {
		t.Fatal("first service join should create")
	}
	again, xerr := svc.ReconcileCommercialIntel(context.Background(), org, facts)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if again.Created || !again.Replay {
		t.Fatal("service replay must return the first chain")
	}
	view, xerr := svc.CommercialExecutiveView(context.Background(), org, intel.SyntheticMonth, true)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if view.QCO == 0 {
		t.Fatalf("synthetic QCO missing: %+v", view)
	}
	cand, xerr := svc.RecordIntelLearning(context.Background(), org, intel.LearningInput{
		From: intel.LearningFromCorrection, CorrectionCodes: []string{"wrong_service"},
		Keys: facts.Keys, Synthetic: true,
	})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if cand.Target != intel.TargetOffer || len(cand.UpstreamWrites) != 0 {
		t.Fatalf("learning via service: %+v", cand)
	}
	fmt.Printf("SERVICE_JOIN identity=%s replay=%v qco=%d learning_target=%s\n",
		res.Chain.Identity, again.Replay, view.QCO, cand.Target)
}

func TestObserveFromInboundKeepsIDsOnly(t *testing.T) {
	svc, repo, org := inboundTestService(t)
	now := parseFlexibleTime("2026-08-14T14:55:00Z")
	body := []byte(`{
		"lead_id":"webcfg-obs-1","receipt_id":"rcpt-obs-1","created_at":"2026-08-14T14:55:00Z",
		"source":"web-cfg","route_family":"inbound","asset_id":"landing-segunda-leitura",
		"cta_id":"segunda-leitura-contrato","entity_public_id":"extra-obs",
		"company":"Construtora Norte","name":"Ana Souza","email":"ana.souza@norte.example",
		"phone":"+5541999887766","correlation_id":"corr-obs-1",
		"utm":{"source":"google","campaign":"segunda-leitura"}
	}`)
	ing, xerr := svc.IngestInboundLead(context.Background(), org, body, IngestOptions{Now: now})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if ing.Lead == nil {
		t.Fatal("ingest dropped lead")
	}
	facts := intel.ObserveFromInbound(*ing.Lead, nil, ing.Action, nil)
	if facts.Keys.LeadID != "webcfg-obs-1" || facts.Keys.ReceiptID != "rcpt-obs-1" {
		t.Fatalf("inbound IDs not copied: %+v", facts.Keys)
	}
	if facts.Keys.AssetID != "landing-segunda-leitura" || facts.Keys.CorrelationID != "corr-obs-1" {
		t.Fatalf("web-cfg attribution not copied: %+v", facts.Keys)
	}
	key := intel.MetricKey(facts.Keys)
	if intel.MetricKeyContainsPII(key) {
		t.Fatalf("observe metric key has PII: %s", key)
	}
	raw, _ := json.Marshal(facts.Keys)
	if containsAny(string(raw), "ana.souza@norte.example", "+5541999887766", "Ana Souza") {
		t.Fatalf("PII entered join keys: %s", raw)
	}
	fmt.Printf("OBSERVE lead=%s receipt=%s asset=%s metric_pii=false\n",
		facts.Keys.LeadID, facts.Keys.ReceiptID, facts.Keys.AssetID)
	_ = repo
}

func containsAny(s string, parts ...string) bool {
	for _, p := range parts {
		if len(p) > 0 && indexOfStr(s, p) >= 0 {
			return true
		}
	}
	return false
}
func indexOfStr(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
