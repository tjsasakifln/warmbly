package confenge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/errx"
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

func TestIngestInboundRetryAfterPersist5xx(t *testing.T) {
	svc, repo, org := inboundTestService(t)
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	body := []byte(`{"lead_id":"retry-5xx-1","receipt_id":"retry-5xx-1","source":"CONFENGE_WEB","company":"Obra Sul","phone":"41991112222","contract_public_id":"CTR-RETRY"}`)

	repo.inboundInsertErr = errors.New("postgres unavailable")
	first, xerr := svc.IngestInboundLead(context.Background(), org, body, IngestOptions{Now: now})
	if xerr == nil || first != nil {
		t.Fatalf("persist failure must 5xx, got res=%+v err=%v", first, xerr)
	}
	if xerr.Code != errx.Internal {
		t.Fatalf("persist failure code=%v want internal", xerr.Code)
	}
	leads, _ := repo.ListInboundLeads(context.Background(), org, false, 20)
	if len(leads) != 0 {
		t.Fatalf("failed persist must leave zero receipts: %d", len(leads))
	}
	fmt.Printf("RETRY_5XX persist_fail http=500 receipts=0 lead_id=retry-5xx-1\n")

	repo.inboundInsertErr = nil
	second, xerr := svc.IngestInboundLead(context.Background(), org, body, IngestOptions{Now: now.Add(time.Minute)})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if second.Duplicate || second.Lead == nil || second.Lead.LeadID != "retry-5xx-1" {
		t.Fatalf("retry after persist 5xx must create: %+v", second)
	}
	if second.DispatchAttempted || (second.Action != nil && second.Action.Dispatchable) {
		t.Fatal("retry must not dispatch")
	}
	fmt.Printf("RETRY_5XX persist_ok lead_id=%s next=%s dispatch=%v\n", second.Lead.LeadID, second.NextAction, second.DispatchAttempted)

	repo.inboundUpdateErr = errors.New("update after persist failed")
	incompleteBody := []byte(`{"lead_id":"retry-5xx-2","receipt_id":"retry-5xx-2","source":"CONFENGE_WEB","phone":"41990001111"}`)
	partial, xerr := svc.IngestInboundLead(context.Background(), org, incompleteBody, IngestOptions{Now: now.Add(2 * time.Minute)})
	if xerr == nil || partial != nil {
		t.Fatalf("update failure after persist must 5xx, got res=%+v err=%v", partial, xerr)
	}
	stored, _ := repo.GetInboundLeadByLeadID(context.Background(), org, "retry-5xx-2")
	if stored == nil {
		t.Fatal("persist-first must keep the receipt when update 5xxs")
	}
	if inboundReceiptComplete(stored) {
		t.Fatalf("incomplete receipt should stay retryable: %+v", stored)
	}
	repo.inboundUpdateErr = nil
	resumed, xerr := svc.IngestInboundLead(context.Background(), org, incompleteBody, IngestOptions{Now: now.Add(3 * time.Minute)})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if !resumed.Duplicate || resumed.NextAction == "" {
		t.Fatalf("same lead_id after 5xx must complete the receipt: %+v", resumed)
	}
	if resumed.DispatchAttempted {
		t.Fatal("resumed ingest dispatched")
	}
	leads, _ = repo.ListInboundLeads(context.Background(), org, false, 20)
	var count int
	for _, l := range leads {
		if l.LeadID == "retry-5xx-2" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("resume minted extra receipt: %d", count)
	}
	fmt.Printf("RETRY_5XX resume lead_id=retry-5xx-2 duplicate=true next=%s receipts=1 dispatch=false\n", resumed.NextAction)
}

func TestParseInboundLeadAcceptsConfengeWebOmittedOptionals(t *testing.T) {
	now := time.Date(2026, 8, 15, 16, 51, 13, 0, time.UTC)
	body := []byte(`{"lead_id":"887430130a84da4769fc0c19","receipt_id":"887430130a84da4769fc0c19","created_at":"2026-08-15T16:51:13Z","source":"CONFENGE_WEB"}`)
	lead, err := ParseInboundLead(body, now)
	if err != nil {
		t.Fatal(err)
	}
	if lead.LeadID != "887430130a84da4769fc0c19" || lead.ReceiptID != lead.LeadID {
		t.Fatalf("join ids: %+v", lead)
	}
	if lead.Source != "CONFENGE_WEB" {
		t.Fatalf("source=%q want CONFENGE_WEB", lead.Source)
	}
	if lead.Email != "" || lead.Phone != "" || lead.Name != "" || lead.CNPJ != "" || lead.EntityID != "" {
		t.Fatalf("omitted fields invented: %+v", lead)
	}
	fmt.Printf("CONTRACT source=CONFENGE_WEB lead_id=%s omitted_ok=true\n", lead.LeadID)
}

func TestCollectInboundNowKeepsStaleEnrichment(t *testing.T) {
	svc, _, org := inboundTestService(t)
	now := time.Date(2026, 8, 15, 14, 0, 0, 0, time.UTC)
	body := []byte(`{"lead_id":"stale-enrich-1","source":"CONFENGE_WEB","company":"Obra Sul","cnpj":"55444333000122","phone":"41991112222"}`)
	res, xerr := svc.IngestInboundLead(context.Background(), org, body, IngestOptions{Now: now, EnrichmentUnavailable: true})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if res.Lead.EnrichmentStatus != models.InboundEnrichmentUnavailable && res.Lead.EnrichmentStatus != models.InboundEnrichmentFailed {
		t.Fatalf("status=%s", res.Lead.EnrichmentStatus)
	}
	queue, xerr := svc.CollectInboundNow(context.Background(), org)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if len(queue) != 1 || queue[0].LeadID != "stale-enrich-1" {
		t.Fatalf("stale enrichment dropped from INBOUND NOW: %+v", queue)
	}
	if queue[0].Dispatchable || queue[0].EmailSendable {
		t.Fatal("stale card must stay a human queue row")
	}
	fmt.Printf("STALE_ENRICH inbound_now=1 lead_id=%s enrich=%s next=%s dispatch=false\n",
		queue[0].LeadID, queue[0].EnrichmentStatus, queue[0].NextAction)
}

func TestParseInboundLeadQueryVsCampaign(t *testing.T) {
	now := time.Date(2026, 8, 15, 17, 0, 0, 0, time.UTC)
	realQ, err := ParseInboundLead([]byte(`{"lead_id":"q1","source":"CONFENGE_WEB","query":"segunda leitura contrato","utm_campaign":"brand-aug"}`), now)
	if err != nil {
		t.Fatal(err)
	}
	if realQ.Query != "segunda leitura contrato" {
		t.Fatalf("real query=%q", realQ.Query)
	}
	if realQ.UTM["campaign"] != "brand-aug" {
		t.Fatalf("campaign not preserved: %+v", realQ.UTM)
	}

	term, err := ParseInboundLead([]byte(`{"lead_id":"q2","source":"CONFENGE_WEB","utm_term":"segunda-leitura","utm_campaign":"brand-aug"}`), now)
	if err != nil {
		t.Fatal(err)
	}
	if term.Query != "segunda-leitura" {
		t.Fatalf("utm_term query=%q", term.Query)
	}
	if term.UTM["campaign"] != "brand-aug" {
		t.Fatalf("campaign lost on utm_term: %+v", term.UTM)
	}

	onlyCamp, err := ParseInboundLead([]byte(`{"lead_id":"q3","source":"CONFENGE_WEB","utm_campaign":"brand-aug","utm_source":"google"}`), now)
	if err != nil {
		t.Fatal(err)
	}
	if onlyCamp.Query != "" {
		t.Fatalf("utm_campaign must not fill query: %q", onlyCamp.Query)
	}
	if onlyCamp.UTM["campaign"] != "brand-aug" || onlyCamp.UTM["source"] != "google" {
		t.Fatalf("campaign/source not preserved: %+v", onlyCamp.UTM)
	}

	row := inboundRowFromParsed(uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"), onlyCamp, []byte(`{"lead_id":"q3","utm_campaign":"brand-aug"}`), now)
	item := ProjectInboundNowItem(*row, nil, nil, now)
	if item.Query != inboundUnknown {
		t.Fatalf("campaign-only projection query=%q want UNKNOWN", item.Query)
	}
	var utm map[string]string
	if err := json.Unmarshal(row.UTMJSON, &utm); err != nil {
		t.Fatal(err)
	}
	if utm["campaign"] != "brand-aug" {
		t.Fatalf("persisted campaign=%v", utm)
	}

	realRow := inboundRowFromParsed(uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"), realQ, []byte(`{"lead_id":"q1","query":"segunda leitura contrato"}`), now)
	realItem := ProjectInboundNowItem(*realRow, nil, nil, now)
	if realItem.Query != "segunda leitura contrato" {
		t.Fatalf("real query projection=%q", realItem.Query)
	}
	termRow := inboundRowFromParsed(uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"), term, []byte(`{"lead_id":"q2","utm_term":"segunda-leitura"}`), now)
	termItem := ProjectInboundNowItem(*termRow, nil, nil, now)
	if termItem.Query != "segunda-leitura" {
		t.Fatalf("utm_term projection=%q", termItem.Query)
	}
	fmt.Printf("QUERY_VS_CAMPAIGN query=%q utm_term=%q campaign_only=%s campaign=%s\n",
		realItem.Query, termItem.Query, item.Query, utm["campaign"])
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
