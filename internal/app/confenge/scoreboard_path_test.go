package confenge

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/models"
)

func TestInboundTruthScoreboardShippedPath(t *testing.T) {
	svc, _, org := inboundTestService(t)
	svc.cfg.InboundWebhookSecret = "inbound-truth-secret"
	svc.cfg.InboundOrgID = org
	svc.cfg.AutoSendEnabled = false
	svc.WireIntel(nil)
	now := time.Date(2026, 8, 18, 14, 0, 0, 0, time.UTC)

	body := []byte(`{
		"lead_id":"webcfg-truth-1",
		"receipt_id":"rcpt-truth-1",
		"source":"web-cfg",
		"route_family":"inbound",
		"asset_id":"landing-segunda-leitura",
		"asset_family":"contract_analysis",
		"cta_id":"segunda-leitura-contrato",
		"query":"segunda-leitura-preco",
		"referrer":"https://search.example/ref",
		"correlation_id":"corr-truth-1",
		"company":"Construtora Norte",
		"email":"ana.souza@norte.example",
		"consent":{"granted":true}
	}`)
	first, xerr := svc.IngestInboundLead(context.Background(), org, body, IngestOptions{Now: now})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if first.Duplicate || first.DispatchAttempted {
		t.Fatalf("first ingest: %+v", first)
	}
	if svc.cfg.AutoSendEnabled {
		t.Fatal("auto-send enabled")
	}
	replay, xerr := svc.IngestInboundLead(context.Background(), org, body, IngestOptions{Now: now.Add(time.Minute)})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if !replay.Duplicate || replay.DispatchAttempted {
		t.Fatalf("byte-equivalent replay: %+v", replay)
	}
	if first.Lead != nil && replay.Lead != nil && first.Lead.ReceiptID != replay.Lead.ReceiptID {
		t.Fatal("replay minted a second receipt")
	}

	canaryBody := []byte(`{
		"lead_id":"SYNTHETIC-INBOUND-truth",
		"receipt_id":"SYNTHETIC-INBOUND-truth",
		"source":"infrastructure_canary",
		"synthetic":true,
		"label":"infrastructure_canary",
		"company":"SYNTHETIC-INBOUND",
		"cta_id":"cta-canary",
		"query":"canary",
		"correlation_id":"corr-canary"
	}`)
	canary, xerr := svc.IngestInboundLead(context.Background(), org, canaryBody, IngestOptions{Now: now})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if canary.DispatchAttempted {
		t.Fatal("canary dispatch attempted")
	}
	queue, xerr := svc.CollectInboundNow(context.Background(), org)
	if xerr != nil {
		t.Fatal(xerr)
	}
	for _, item := range queue {
		if strings.Contains(strings.ToLower(item.LeadID), "synthetic") || strings.Contains(strings.ToLower(item.LeadID), "canary") {
			t.Fatalf("canary leaked into INBOUND NOW: %+v", item)
		}
	}
	if len(queue) == 0 {
		t.Fatal("real inbound missing from INBOUND NOW")
	}

	ev := intel.CommercialEvent{
		EventID: "ev-truth-1", Version: "1", Schema: intel.EventSchemaV1,
		Type: intel.EventLeadReceived, OccurredAt: now, OrganizationID: org.String(),
		LeadID: "webcfg-truth-1", ReceiptID: "rcpt-truth-1", Source: "web-cfg",
		Query: "segunda-leitura-preco", Referrer: "https://search.example/ref",
		AssetFamily: intel.AssetFamilyContractAnalysis, AssetID: "landing-segunda-leitura",
		CTAID: "segunda-leitura-contrato", CorrelationID: "corr-truth-1",
		RouteFamily: intel.FamilyInbound,
	}
	joined, xerr := svc.IngestCommercialEvent(context.Background(), org, ev)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if joined.Chain.Keys.CTAID == "" || joined.Chain.Keys.Query == "" || joined.Chain.Keys.CorrelationID == "" {
		t.Fatalf("attribution dropped: %+v", joined.Chain.Keys)
	}
	if intel.MetricKeyContainsPII(joined.Chain.MetricKey) {
		t.Fatalf("metric PII: %s", joined.Chain.MetricKey)
	}
	replayEv, xerr := svc.IngestCommercialEvent(context.Background(), org, ev)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if !replayEv.Replay || replayEv.Created {
		t.Fatalf("event replay: %+v", replayEv)
	}

	board, xerr := svc.TruthScoreboard(context.Background(), org, "2026-08", false)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if board.IncludeSynthetic || board.CausalProof || board.AutoSendEnabled || board.DispatchAttempted {
		t.Fatalf("scoreboard flags: %+v", board)
	}
	if len(board.Stages) != 7 {
		t.Fatalf("stages=%d", len(board.Stages))
	}
	if board.Stages[0].Status != intel.TruthBlocked || board.Stages[1].Status != intel.TruthBlocked {
		t.Fatalf("GSC/index invented: %s %s", board.Stages[0].Status, board.Stages[1].Status)
	}
	if board.Stages[3].Status != intel.TruthTrue {
		t.Fatalf("persisted lead not TRUE: %s", board.Stages[3].Status)
	}
	if board.Stages[6].Status == intel.TruthTrue {
		t.Fatal("receita invented")
	}

	bare := intel.HumanOutcomeEntry{LeadID: "webcfg-truth-1", Action: intel.HumanWon, OccurredAt: now}
	held, xerr := svc.RegisterHumanOutcome(context.Background(), org, bare)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if !held.Held {
		t.Fatal("bare WON accepted")
	}

	envs := svc.HumanOutcomeEnvelopes()
	if len(envs) != 4 {
		t.Fatalf("envelopes=%d", len(envs))
	}
	for _, env := range envs {
		if env.InventedIDs || env.LeadID != "" {
			t.Fatalf("invented envelope %+v", env)
		}
	}

	fmt.Printf("SHIPPED_PATH persist=%v replay=%v inbound_now=%d lead=%s receita=%s auto_send=%v dispatch=%v\n",
		!first.Duplicate, replay.Duplicate, len(queue), board.Stages[3].Status, board.Stages[6].Status,
		board.AutoSendEnabled, board.DispatchAttempted)
}

func TestInfrastructureCanarySkipToken(t *testing.T) {
	lead := models.OutreachInboundLead{
		LeadID: "canary-1", Source: "infrastructure_canary", CompanyName: "Obra Sul",
	}
	if got := InboundCommercialSkipReason(lead); got != intel.InboundSkipSynthetic {
		t.Fatalf("infrastructure_canary skip=%q", got)
	}
}
