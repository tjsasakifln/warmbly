package confenge

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/warmbly/warmbly/internal/models"
)

func TestInboundCommercialSkipKeepsRealInQueue(t *testing.T) {
	svc, _, org := inboundTestService(t)
	now := time.Date(2026, 8, 15, 18, 0, 0, 0, time.UTC)

	synth := []byte(`{"lead_id":"synthetic-inbound-cfg","receipt_id":"synthetic-inbound-cfg","source":"CONFENGE_WEB","company":"SYNTHETIC-INBOUND"}`)
	qa := []byte(`{"lead_id":"qa-lead-1","receipt_id":"qa-lead-1","source":"qa","company":"QA Fixture"}`)
	internal := []byte(`{"lead_id":"internal-probe","receipt_id":"internal-probe","label":"internal","company":"Ops"}`)
	realBody := []byte(`{"lead_id":"webcfg-real-1","receipt_id":"rcpt-real-1","source":"CONFENGE_WEB","company":"Construtora Norte","phone":"41991112222","cta_id":"segunda-leitura-contrato","query":"segunda leitura contrato","asset_id":"landing-norte"}`)

	for _, body := range [][]byte{synth, qa, internal, realBody} {
		if _, xerr := svc.IngestInboundLead(context.Background(), org, body, IngestOptions{Now: now}); xerr != nil {
			t.Fatal(xerr)
		}
	}

	queue, xerr := svc.CollectInboundNow(context.Background(), org)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if len(queue) != 1 || queue[0].LeadID != "webcfg-real-1" {
		t.Fatalf("commercial queue contaminated or missing real lead: %+v", queue)
	}
	if queue[0].Dispatchable || queue[0].EmailSendable {
		t.Fatal("real card must stay a human queue row")
	}
	if queue[0].Query != "segunda leitura contrato" {
		t.Fatalf("query=%q", queue[0].Query)
	}
	if queue[0].CTA != "segunda-leitura-contrato" {
		t.Fatalf("cta=%q", queue[0].CTA)
	}
	if queue[0].Origin != "CONFENGE_WEB" {
		t.Fatalf("origin=%q", queue[0].Origin)
	}
	fmt.Printf("INBOUND_NOW_SKIP synthetic=skipped qa=skipped internal=skipped real=%s dispatch=false\n", queue[0].LeadID)

	qaCo := []byte(`{"lead_id":"webcfg-obra-norte-1","receipt_id":"rcpt-obra-norte-1","source":"CONFENGE_WEB","company":"QA Engenharia","phone":"41990001111","message":"equipe de QA na obra"}`)
	if _, xerr := svc.IngestInboundLead(context.Background(), org, qaCo, IngestOptions{Now: now.Add(time.Minute)}); xerr != nil {
		t.Fatal(xerr)
	}
	queue, xerr = svc.CollectInboundNow(context.Background(), org)
	if xerr != nil {
		t.Fatal(xerr)
	}
	found := false
	for _, item := range queue {
		if item.LeadID == "webcfg-obra-norte-1" {
			found = true
		}
		if item.LeadID == "synthetic-inbound-cfg" || item.LeadID == "qa-lead-1" || item.LeadID == "internal-probe" {
			t.Fatalf("skipped receipt leaked after QA Engenharia ingest: %s", item.LeadID)
		}
	}
	if !found {
		t.Fatal("real QA Engenharia lead was dropped from commercial INBOUND NOW")
	}
	fmt.Printf("INBOUND_NOW_KEEP company=QA Engenharia lead_id=webcfg-obra-norte-1\n")
}

func TestInboundCommercialSkipReasonTokens(t *testing.T) {
	cases := []struct {
		lead models.OutreachInboundLead
		want string
	}{
		{models.OutreachInboundLead{LeadID: "synthetic-inbound-cfg"}, InboundSkipSynthetic},
		{models.OutreachInboundLead{LeadID: "qa-lead-1", Source: "qa"}, InboundSkipQA},
		{models.OutreachInboundLead{LeadID: "x", RawPayload: []byte(`{"label":"internal"}`)}, InboundSkipInternal},
		{models.OutreachInboundLead{LeadID: "webcfg-real-1", Source: "CONFENGE_WEB", CompanyName: "Norte"}, ""},
		{models.OutreachInboundLead{LeadID: "webcfg-obra-norte-1", Source: "CONFENGE_WEB", CompanyName: "QA Engenharia", Message: "equipe de QA na obra"}, ""},
		{models.OutreachInboundLead{LeadID: "webcfg-real-2", Source: "CONFENGE_WEB", CompanyName: "SYNTHETIC-INBOUND"}, InboundSkipSynthetic},
	}
	for _, tc := range cases {
		got := InboundCommercialSkipReason(tc.lead)
		if got != tc.want {
			t.Fatalf("lead=%s skip=%q want=%q", tc.lead.LeadID, got, tc.want)
		}
	}
}
