package confenge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

func TestInboundShippedPathIngestToOutcome(t *testing.T) {
	svc, repo, org := inboundTestService(t)
	owner := uuid.MustParse("11111111-2222-4333-8444-555555555555")
	repo.orgOwner[org] = owner
	now := time.Date(2026, 8, 14, 15, 0, 0, 0, time.UTC)

	acc := &models.OutreachAccount{
		OrganizationID: org,
		SourceLeadID:   "extra-norte",
		CNPJ14:         "55444333000122",
		RazaoSocial:    "Construtora Norte LTDA",
		QueueState:     models.OutreachQueueNeedsContact,
		SourceSystem:   "extra-cli",
		MomentSummary:  "Aditivo contratual publicado",
		MomentCode:     "CONTRACT_MARGIN_EVENT",
		FactToMention:  "aditivo de margem no contrato municipal",
	}
	if _, err := repo.UpsertAccount(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	cand := models.OutreachContactCandidate{
		OrganizationID: org, AccountID: acc.ID, SourceContactID: "du-ana",
		PersonID: "person-ana-norte", Name: "Ana Souza", Role: "contratos",
		Email: "ana.souza@norte.example", Phone: "+5541999887766",
		VerificationStatus: models.OutreachVerifyOfficialSource,
		EmailSendReady:     true, Recommended: true,
		ReachabilityClass: models.ReachabilityR3Routed,
		RouteType:         "phone", RouteRelation: models.RouteRelRoutesToNamedPerson,
	}
	if _, err := repo.UpsertCandidate(context.Background(), &cand); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpsertEvidence(context.Background(), &models.OutreachEvidence{
		OrganizationID: org, AccountID: acc.ID, SourceEvidenceID: "CONTRACT_MARGIN_EVENT",
		EvidenceType: "CONTRACT_MARGIN_EVENT", Title: "margem contratual",
	}); err != nil {
		t.Fatal(err)
	}

	body := []byte(`{
		"lead_id":"webcfg-e2e-1",
		"receipt_id":"rcpt-e2e-1",
		"created_at":"2026-08-14T14:55:00Z",
		"source":"web-cfg",
		"route_family":"inbound",
		"asset_id":"landing-segunda-leitura",
		"cta_id":"segunda-leitura-contrato",
		"landing_url":"https://confenge.com.br/contratos/norte",
		"contract_public_id":"CTR-NORTE-88",
		"entity_public_id":"extra-norte",
		"cnpj":"55444333000122",
		"company":"Construtora Norte",
		"name":"Ana Souza",
		"email":"ana.souza@norte.example",
		"phone":"+5541999887766",
		"consent":{"granted":true,"preferred_channel":"phone"},
		"message":"Preciso de uma segunda leitura do contrato",
		"correlation_id":"corr-e2e-1"
	}`)

	ing, xerr := svc.IngestInboundLead(context.Background(), org, body, IngestOptions{Now: now})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if ing.DispatchAttempted || (ing.Action != nil && (ing.Action.Dispatchable || (ing.Action.EmailSendable && !false))) {
		t.Fatalf("auto-send leaked: %+v", ing.Action)
	}
	if ing.Action != nil && ing.Action.Dispatchable {
		t.Fatal("dispatchable")
	}
	if ing.NextAction == "" {
		t.Fatal("next_action empty")
	}
	fmt.Printf("E2E ingest lead=%s next=%s enrich=%s owner=%s action=%v auto_send=%v\n",
		ing.Lead.LeadID, ing.NextAction, ing.EnrichmentStatus, ing.Lead.Owner, ing.Action != nil, svc.cfg.AutoSendEnabled)

	queue, xerr := svc.CollectInboundNow(context.Background(), org)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if len(queue) == 0 {
		t.Fatal("INBOUND NOW empty after ingest")
	}
	item := queue[0]
	if item.LeadID != "webcfg-e2e-1" {
		t.Fatalf("queue lead %s", item.LeadID)
	}
	if item.Company == "" || item.Origin == "" || item.WhyNow == "" || item.RecommendedAction == "" || item.NextAction == "" || item.Owner == "" || item.Status == "" {
		raw, _ := json.Marshal(item)
		t.Fatalf("incomplete INBOUND NOW card: %s", raw)
	}
	if item.Latency.LeadCreatedAt == "" || item.Latency.WarmblyIngestedAt == "" {
		t.Fatalf("latency missing: %+v", item.Latency)
	}
	if item.Latency.FirstActionAt != "" || item.Latency.CloseAt != "" {
		t.Fatalf("later stamps must stay empty: %+v", item.Latency)
	}
	if item.Dispatchable || item.EmailSendable {
		t.Fatal("queue card must not be sendable")
	}
	if item.CTA == inboundUnknown || item.Asset == inboundUnknown {
		t.Fatalf("observed cta/asset rendered UNKNOWN: cta=%s asset=%s", item.CTA, item.Asset)
	}
	if item.SuggestedCopyReview != "human_review_required" {
		t.Fatalf("suggested copy must stay review-only")
	}
	fmt.Printf("INBOUND_NOW company=%s person=%s origin=%s asset=%s contract=%s why=%q action=%s channel=%s owner=%s age=%s status=%s next=%s\n",
		item.Company, item.Person, item.Origin, item.Asset, item.ContractContext, item.WhyNow,
		item.RecommendedAction, item.Channel, item.Owner, item.LeadAge, item.Status, item.NextAction)
	if card, err := json.Marshal(item); err == nil {
		fmt.Printf("INBOUND_NOW_JSON %s\n", card)
	}

	cockpit, xerr := svc.CollectContactCockpit(context.Background(), org)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if len(cockpit.InboundNow) == 0 {
		t.Fatal("cockpit missing inbound_now")
	}

	out, xerr := svc.RecordInboundOutcome(context.Background(), org, owner, "webcfg-e2e-1", OutcomeRequest{
		OutcomeCode: models.OutcomeFollowUp, Notes: "liguei, retorno sexta", Now: now.Add(20 * time.Minute),
	})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if out.Action.OutcomeCode != models.OutcomeFollowUp {
		t.Fatalf("outcome=%s", out.Action.OutcomeCode)
	}
	stored, _ := repo.GetInboundLeadByLeadID(context.Background(), org, "webcfg-e2e-1")
	if stored.FirstActionAt == nil {
		t.Fatal("first_action_at not stamped")
	}
	fmt.Printf("OUTCOME persisted=%s first_action_at=%s won_inferred=false\n", stored.Status, stored.FirstActionAt.Format(time.RFC3339))

	replay, xerr := svc.IngestInboundLead(context.Background(), org, body, IngestOptions{Now: now.Add(time.Hour)})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if !replay.Duplicate {
		t.Fatal("reprocess must be idempotent")
	}
	actions, _ := repo.ListCommercialActions(context.Background(), org, uuid.Nil, false, 20)
	inboundActions := 0
	for _, a := range actions {
		if strings.HasPrefix(a.IdempotencyKey, "inbound:webcfg-e2e-1") || a.SourceLeadID == "webcfg-e2e-1" {
			inboundActions++
		}
	}
	if inboundActions != 1 {
		t.Fatalf("reprocess duplicated inbound actions: total=%d inbound=%d", len(actions), inboundActions)
	}
	leads, _ := repo.ListInboundLeads(context.Background(), org, false, 20)
	if len(leads) != 1 {
		t.Fatalf("reprocess duplicated receipts: %d", len(leads))
	}
	again, xerr := svc.RecordInboundOutcome(context.Background(), org, owner, "webcfg-e2e-1", OutcomeRequest{
		OutcomeCode: models.OutcomeFollowUp, Now: now.Add(2 * time.Hour),
	})
	if xerr != nil {
		t.Fatal(xerr)
	}
	_ = again
	var followups int
	for _, ev := range repo.outcomes {
		if ev.EventType == OutcomeContacted || ev.SourceLeadID == "webcfg-e2e-1" && ev.EventType != OutcomeLeadImported {
			if ev.IdempotencyKey != "" {
				followups++
			}
		}
	}
	fmt.Printf("REPROCESS actions=%d auto_send=%v dispatch=%v\n", len(actions), svc.cfg.AutoSendEnabled, replay.DispatchAttempted)
	if svc.cfg.AutoSendEnabled {
		t.Fatal("CONFENGE_AUTO_SEND_ENABLED must remain false")
	}

	if err := RejectInboundQueryPII(url.Values{"phone": []string{"4199"}}); err == nil {
		t.Fatal("query PII must still be rejected after path")
	}
}

func TestInboundHTTPContractHMACAndNoSend(t *testing.T) {
	svc, _, org := inboundTestService(t)
	svc.cfg.InboundWebhookSecret = "inbound-secret-test"
	svc.cfg.InboundOrgID = org
	now := time.Date(2026, 8, 14, 16, 0, 0, 0, time.UTC)
	body := []byte(`{"lead_id":"http-1","company":"Obra Sul","phone":"41991112222","contract_public_id":"CTR-2","source":"web-cfg"}`)
	sig := SignOutcomeHMAC(svc.cfg.InboundWebhookSecret, now, body)
	if !VerifyOutcomeHMAC(svc.cfg.InboundWebhookSecret, sig, body, now, 5*time.Minute) {
		t.Fatal("HMAC verify failed on signed inbound body")
	}
	if err := RejectInboundQueryPII(url.Values{"lead_id": []string{"http-1"}}); err != nil {
		t.Fatal(err)
	}
	if err := RejectInboundQueryPII(url.Values{"email": []string{"x@y.com"}}); err == nil {
		t.Fatal("handler contract must reject query PII")
	}
	res, xerr := svc.IngestInboundLead(context.Background(), org, body, IngestOptions{Now: now})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if res.Lead.LeadID != "http-1" || res.NextAction == "" {
		t.Fatalf("http contract ingest: %+v", res)
	}
	if res.DispatchAttempted {
		t.Fatal("http path attempted send")
	}
	fmt.Printf("HTTP_CONTRACT lead_id=%s next_action=%s hmac=ok send=false\n", res.Lead.LeadID, res.NextAction)
}

func TestInboundHTTPContractConfengeWebOmittedOptionals(t *testing.T) {
	svc, _, org := inboundTestService(t)
	svc.cfg.AutoSendEnabled = false
	now := time.Date(2026, 8, 15, 16, 51, 13, 0, time.UTC)
	body := []byte(`{"lead_id":"synthetic-inbound-cfg","receipt_id":"synthetic-inbound-cfg","created_at":"2026-08-15T16:51:13Z","source":"CONFENGE_WEB"}`)
	sig := SignOutcomeHMAC("inbound-secret-test", now, body)
	if !VerifyOutcomeHMAC("inbound-secret-test", sig, body, now, 5*time.Minute) {
		t.Fatal("HMAC on CONFENGE_WEB body failed")
	}
	res, xerr := svc.IngestInboundLead(context.Background(), org, body, IngestOptions{Now: now})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if res.Lead.Source != "CONFENGE_WEB" {
		t.Fatalf("source=%s", res.Lead.Source)
	}
	if res.DispatchAttempted || (res.Action != nil && res.Action.Dispatchable) {
		t.Fatal("CONFENGE_WEB ingest dispatched")
	}
	if res.NextAction != models.InboundNextNeedsEnrichment {
		t.Fatalf("insufficient identity next=%s want NEEDS_ENRICHMENT", res.NextAction)
	}
	if res.Lead == nil || res.Lead.LeadID != "synthetic-inbound-cfg" {
		t.Fatalf("synthetic receipt must persist: %+v", res)
	}
	queue, xerr := svc.CollectInboundNow(context.Background(), org)
	if xerr != nil {
		t.Fatal(xerr)
	}
	for _, item := range queue {
		if item.LeadID == "synthetic-inbound-cfg" {
			t.Fatal("synthetic receipt leaked into commercial INBOUND NOW")
		}
	}
	fmt.Printf("CONFENGE_WEB persisted=true inbound_now_skip=synthetic lead_id=%s next=%s dispatch=false auto_send=%v\n",
		res.Lead.LeadID, res.NextAction, svc.cfg.AutoSendEnabled)
}
