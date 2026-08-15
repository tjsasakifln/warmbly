package confenge

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/models"
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

func TestObserveExistingJoinsOutcomeByLeadIDOnly(t *testing.T) {
	svc, repo, org := inboundTestService(t)
	now := time.Date(2026, 8, 14, 15, 0, 0, 0, time.UTC)
	sharedEmail := "shared@empresa.com"
	sharedCNPJ := "11222333000181"
	leadA := &models.OutreachInboundLead{
		OrganizationID: org, LeadID: "webcfg-join-a", ReceiptID: "rcpt-a",
		LeadCreatedAt: now, WarmblyIngestedAt: now,
		LeadEmail: sharedEmail, CNPJ14: sharedCNPJ,
		RouteFamily: "inbound", Status: models.InboundStatusOpen,
		EnrichmentStatus: models.InboundEnrichmentUnknown, Owner: models.InboundOwnerUnknown,
	}
	leadB := &models.OutreachInboundLead{
		OrganizationID: org, LeadID: "webcfg-join-b", ReceiptID: "rcpt-b",
		LeadCreatedAt: now, WarmblyIngestedAt: now,
		LeadEmail: sharedEmail, CNPJ14: sharedCNPJ,
		RouteFamily: "inbound", Status: models.InboundStatusOpen,
		EnrichmentStatus: models.InboundEnrichmentUnknown, Owner: models.InboundOwnerUnknown,
	}
	if _, _, err := repo.InsertInboundLead(context.Background(), leadA); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.InsertInboundLead(context.Background(), leadB); err != nil {
		t.Fatal(err)
	}
	if err := repo.EnqueueOutcome(context.Background(), &models.OutreachOutcome{
		OrganizationID: org, SourceLeadID: "webcfg-join-a", EventType: intel.OutcomeQualifiedConversation,
		ContactEmail: sharedEmail, CNPJ14: sharedCNPJ, OccurredAt: now, IdempotencyKey: "out-a",
		EventID: uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.EnqueueOutcome(context.Background(), &models.OutreachOutcome{
		OrganizationID: org, SourceLeadID: "webcfg-join-b", EventType: intel.OutcomeMeeting,
		ContactEmail: sharedEmail, CNPJ14: sharedCNPJ, OccurredAt: now.Add(time.Hour), IdempotencyKey: "out-b",
		EventID: uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"),
	}); err != nil {
		t.Fatal(err)
	}

	svc.observeExisting(context.Background(), org)
	chains, err := svc.intelStore().ListChains(org.String())
	if err != nil {
		t.Fatal(err)
	}
	byLead := map[string]intel.Chain{}
	for _, c := range chains {
		byLead[c.LeadID] = c
	}
	a, okA := byLead["webcfg-join-a"]
	b, okB := byLead["webcfg-join-b"]
	if !okA || !okB {
		t.Fatalf("missing chains a=%v b=%v have=%d", okA, okB, len(chains))
	}
	if a.OutcomeType == intel.OutcomeMeeting {
		t.Fatal("lead A absorbed lead B meeting via email/cnpj")
	}
	if a.OutcomeType != intel.OutcomeQualifiedConversation {
		t.Fatalf("lead A outcome=%s want QCO (old outbox filter would drop it)", a.OutcomeType)
	}
	if b.OutcomeType != intel.OutcomeMeeting {
		t.Fatalf("lead B outcome=%s want MEETING", b.OutcomeType)
	}
	fmt.Printf("OBSERVE_EXISTING a=%s b=%s email_or_rejected=true qco_visible=true\n", a.OutcomeType, b.OutcomeType)
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
