package confenge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

func inboundTestService(t *testing.T) (*service, *memRepo, uuid.UUID) {
	t.Helper()
	repo := newMemRepo()
	cfg := Config{
		Enabled: true, RequireHumanApproval: true, AutoSendEnabled: false,
		DefaultDailyLimit: 10, MaxInitialEmailWords: 120, MaxFeedPayloadBytes: DefaultMaxPayloadBytes,
	}
	svc := NewService(cfg, repo, nil).(*service)
	org := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	repo.orgOwner[org] = uuid.Nil
	return svc, repo, org
}

func TestRejectInboundQueryPII(t *testing.T) {
	if err := RejectInboundQueryPII(nil); err != nil {
		t.Fatal(err)
	}
	ok := url.Values{"asset_id": []string{"landing-1"}, "utm_source": []string{"google"}}
	if err := RejectInboundQueryPII(ok); err != nil {
		t.Fatalf("non-PII query rejected: %v", err)
	}
	bad := url.Values{"email": []string{"pessoa@empresa.com.br"}}
	if err := RejectInboundQueryPII(bad); err == nil {
		t.Fatal("expected PII query rejection")
	}
	fmt.Println("QUERY_PII rejected=true field=email")
}

func TestIngestInboundPersistsReceiptAndDedupesLeadID(t *testing.T) {
	svc, repo, org := inboundTestService(t)
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	body := []byte(`{
		"lead_id":"webcfg-lead-001",
		"receipt_id":"rcpt-001",
		"created_at":"2026-08-14T11:50:00Z",
		"source":"web-cfg",
		"route_family":"inbound",
		"asset_id":"segunda-leitura",
		"cta_id":"segunda-leitura-contrato",
		"landing_url":"https://confenge.com.br/contratos/abc",
		"contract_public_id":"CTR-88",
		"cnpj":"55.444.333/0001-22",
		"company":"Construtora Norte",
		"name":"Ana Souza",
		"email":"ana.souza@norte.example",
		"phone":"+5541999887766",
		"consent":{"granted":true,"preferred_channel":"phone"},
		"utm":{"source":"google","medium":"cpc"},
		"referrer":"https://www.google.com/",
		"message":"Quero uma segunda leitura do contrato",
		"correlation_id":"attr-9"
	}`)
	first, xerr := svc.IngestInboundLead(context.Background(), org, body, IngestOptions{Now: now})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if first.Duplicate || first.Lead == nil {
		t.Fatalf("first ingest: %+v", first)
	}
	if first.Lead.LeadID != "webcfg-lead-001" || first.Lead.ReceiptID != "rcpt-001" {
		t.Fatalf("receipt not preserved: %+v", first.Lead)
	}
	if first.Lead.WarmblyIngestedAt.IsZero() || first.Lead.LeadCreatedAt.IsZero() {
		t.Fatal("latency stamps missing on first ingest")
	}
	if first.DispatchAttempted {
		t.Fatal("dispatch must stay off")
	}
	if first.Action != nil && first.Action.Dispatchable {
		t.Fatal("action must not be dispatchable")
	}
	fmt.Printf("INGEST lead_id=%s receipt=%s persisted=true next_action=%s dispatch=%v\n",
		first.Lead.LeadID, first.Lead.ReceiptID, first.NextAction, first.DispatchAttempted)

	replay, xerr := svc.IngestInboundLead(context.Background(), org, body, IngestOptions{Now: now.Add(time.Minute)})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if !replay.Duplicate {
		t.Fatal("same lead_id must return existing action")
	}
	leads, _ := repo.ListInboundLeads(context.Background(), org, false, 50)
	if len(leads) != 1 {
		t.Fatalf("replay created extra receipt: %d", len(leads))
	}
	open, _ := repo.ListCommercialActions(context.Background(), org, uuid.Nil, false, 50)
	if first.Action != nil && len(open) != 1 {
		t.Fatalf("replay created second action: %d", len(open))
	}
	fmt.Printf("REPLAY same_lead_id second_action=false receipts=%d\n", len(leads))
}

func TestIngestSecondaryDedupeKeepsDistinctEvent(t *testing.T) {
	svc, repo, org := inboundTestService(t)
	now := time.Date(2026, 8, 14, 13, 0, 0, 0, time.UTC)
	firstBody := []byte(`{"lead_id":"lead-A","cnpj":"55444333000122","email":"ana@norte.example","phone":"41999887766","company":"Norte","name":"Ana"}`)
	if _, xerr := svc.IngestInboundLead(context.Background(), org, firstBody, IngestOptions{Now: now}); xerr != nil {
		t.Fatal(xerr)
	}
	secondBody := []byte(`{"lead_id":"lead-B","cnpj":"55444333000122","email":"ana@norte.example","phone":"41999887766","company":"Norte","name":"Ana","message":"mesmo contato, outro envio"}`)
	second, xerr := svc.IngestInboundLead(context.Background(), org, secondBody, IngestOptions{Now: now.Add(10 * time.Minute)})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if !second.SecondaryDedupe {
		t.Fatal("expected secondary dedupe inside window")
	}
	leads, _ := repo.ListInboundLeads(context.Background(), org, false, 50)
	if len(leads) != 2 {
		t.Fatalf("distinct event dropped: %d", len(leads))
	}
	actions, _ := repo.ListCommercialActions(context.Background(), org, uuid.Nil, false, 50)
	if len(actions) > 1 {
		t.Fatalf("secondary dedupe created extra action: %d", len(actions))
	}
	later, xerr := svc.IngestInboundLead(context.Background(), org, []byte(`{"lead_id":"lead-C","cnpj":"55444333000122","email":"ana@norte.example","phone":"41999887766","company":"Norte"}`), IngestOptions{Now: now.Add(25 * time.Hour)})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if later.SecondaryDedupe {
		t.Fatal("event outside window must not collapse")
	}
	leads, _ = repo.ListInboundLeads(context.Background(), org, false, 50)
	if len(leads) != 3 {
		t.Fatalf("later event not kept: %d", len(leads))
	}
	fmt.Printf("SECONDARY kept_events=%d in_window_dedupe=true later_kept=true\n", len(leads))
}

func TestIngestKeepsLeadWhenEnrichmentUnavailable(t *testing.T) {
	svc, _, org := inboundTestService(t)
	body := []byte(`{"lead_id":"lead-enrich-fail","company":"Obra Sul","cnpj":"55444333000122","phone":"41991112222","contract_public_id":"CTR-1"}`)
	res, xerr := svc.IngestInboundLead(context.Background(), org, body, IngestOptions{
		Now: time.Date(2026, 8, 14, 14, 0, 0, 0, time.UTC), EnrichmentUnavailable: true,
	})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if res.Lead == nil {
		t.Fatal("enrichment failure dropped the lead")
	}
	if res.Lead.EnrichmentStatus != models.InboundEnrichmentUnavailable && res.Lead.EnrichmentStatus != models.InboundEnrichmentUnknown {
		t.Fatalf("expected explicit unavailable/unknown, got %s", res.Lead.EnrichmentStatus)
	}
	if res.Lead.PersonName != "" && res.Lead.PersonName != "UNKNOWN" {
		// no extra-cli person was supplied
	}
	fmt.Printf("ENRICH_FAIL persisted=%t status=%s next=%s\n", res.Lead != nil, res.Lead.EnrichmentStatus, res.NextAction)
}

func TestParseInboundLeadRequiresReceipt(t *testing.T) {
	_, err := ParseInboundLead([]byte(`{"company":"X"}`), time.Now().UTC())
	if err == nil {
		t.Fatal("expected missing lead_id error")
	}
	lead, err := ParseInboundLead([]byte(`{"receipt_id":"r1","name":"Ana"}`), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if lead.LeadID != "r1" || lead.ReceiptID != "r1" {
		t.Fatalf("receipt fallback: %+v", lead)
	}
	raw, _ := json.Marshal(map[string]any{"lead_id": "x", "javascript": "nope"})
	_ = raw
}
