package confenge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
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
	if queue[0].Query != inboundUnknown {
		t.Fatalf("individual query must stay UNKNOWN, query=%q", queue[0].Query)
	}
	if queue[0].CTA != "segunda-leitura-contrato" {
		t.Fatalf("cta=%q", queue[0].CTA)
	}
	if queue[0].Origin != "CONFENGE_WEB" {
		t.Fatalf("origin=%q", queue[0].Origin)
	}
	assertNoRawQueryLeak(t, "INBOUND NOW", mustJSONBytes(queue))
	board, xerr := svc.OrganicScoreboard(context.Background(), org, false)
	if xerr != nil {
		t.Fatal(xerr)
	}
	fb, xerr := svc.OrganicFeedback(context.Background(), org, false)
	if xerr != nil {
		t.Fatal(xerr)
	}
	rep, xerr := svc.CommercialIntelReport(context.Background(), org, "2026-08", false)
	if xerr != nil {
		t.Fatal(xerr)
	}
	assertNoRawQueryLeak(t, "scoreboard", mustJSONBytes(board))
	assertNoRawQueryLeak(t, "feedback", mustJSONBytes(fb))
	assertNoRawQueryLeak(t, "report", mustJSONBytes(rep))
	chains, err := svc.intelStore().ListChains(org.String())
	if err != nil {
		t.Fatal(err)
	}
	assertNoRawQueryLeak(t, "chain", mustJSONBytes(chains))
	if alert, _ := svc.alertStore().GetOperatorAlertByLead(context.Background(), org, "webcfg-real-1"); alert != nil {
		assertNoRawQueryLeak(t, "operator alert", mustJSONBytes(alert))
	}
	fmt.Printf("INBOUND_NOW_SKIP synthetic=skipped qa=skipped internal=skipped real=%s query=%s dispatch=false\n", queue[0].LeadID, queue[0].Query)

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
		name string
		lead models.OutreachInboundLead
		want string
	}{
		{"official lead_id", models.OutreachInboundLead{LeadID: "synthetic-inbound-cfg"}, InboundSkipSynthetic},
		{"official SYNTHETIC-INBOUND lead_id", models.OutreachInboundLead{LeadID: "SYNTHETIC-INBOUND-20260815T214505Z", Source: "CONFENGE_WEB"}, InboundSkipSynthetic},
		{"source qa", models.OutreachInboundLead{LeadID: "qa-lead-1", Source: "qa"}, InboundSkipQA},
		{"label internal", models.OutreachInboundLead{LeadID: "x", RawPayload: []byte(`{"label":"internal"}`)}, InboundSkipInternal},
		{"env qa flag", models.OutreachInboundLead{LeadID: "flag-qa", RawPayload: []byte(`{"environment":"qa"}`)}, InboundSkipQA},
		{"fixture internal", models.OutreachInboundLead{LeadID: "flag-fix", RawPayload: []byte(`{"fixture":"internal"}`)}, InboundSkipInternal},
		{"explicit synthetic flag", models.OutreachInboundLead{LeadID: "flag-syn", RawPayload: []byte(`{"synthetic":true}`)}, InboundSkipSynthetic},
		{"infrastructure canary source", models.OutreachInboundLead{LeadID: "canary-src", Source: "infrastructure_canary"}, InboundSkipSynthetic},
		{"infrastructure canary label", models.OutreachInboundLead{LeadID: "canary-label", RawPayload: []byte(`{"label":"infrastructure_canary"}`)}, InboundSkipSynthetic},
		{"explicit is_synthetic", models.OutreachInboundLead{LeadID: "flag-is", RawPayload: []byte(`{"is_synthetic":"true"}`)}, InboundSkipSynthetic},
		{"real company", models.OutreachInboundLead{LeadID: "webcfg-real-1", Source: "CONFENGE_WEB", CompanyName: "Norte"}, ""},
		{"QA Engenharia", models.OutreachInboundLead{LeadID: "webcfg-obra-norte-1", Source: "CONFENGE_WEB", CompanyName: "QA Engenharia", Message: "equipe de QA na obra"}, ""},
		{"name substring qa", models.OutreachInboundLead{LeadID: "webcfg-nome-1", Source: "CONFENGE_WEB", LeadName: "Joaquim"}, ""},
		{"email substring qa", models.OutreachInboundLead{LeadID: "webcfg-email-1", Source: "CONFENGE_WEB", LeadEmail: "joaquim.qa@norte.example"}, ""},
		{"message QA real", models.OutreachInboundLead{LeadID: "webcfg-msg-1", Source: "CONFENGE_WEB", Message: "preciso da equipe de QA na obra amanha"}, ""},
		{"internal no contrato", models.OutreachInboundLead{LeadID: "webcfg-msg-int", Source: "CONFENGE_WEB", CompanyName: "Interna Engenharia", Message: "reuniao interna da obra"}, ""},
		{"official company marker", models.OutreachInboundLead{LeadID: "webcfg-real-2", Source: "CONFENGE_WEB", CompanyName: "SYNTHETIC-INBOUND"}, InboundSkipSynthetic},
		{"official email marker", models.OutreachInboundLead{LeadID: "webcfg-real-3", Source: "CONFENGE_WEB", LeadEmail: "synthetic-inbound@example.com"}, InboundSkipSynthetic},
	}
	for _, tc := range cases {
		got := InboundCommercialSkipReason(tc.lead)
		if got != tc.want {
			t.Fatalf("%s lead=%s skip=%q want=%q", tc.name, tc.lead.LeadID, got, tc.want)
		}
	}
}

func TestInboundCommercialSkipKeepsFreeTextQANameEmail(t *testing.T) {
	svc, _, org := inboundTestService(t)
	now := time.Date(2026, 8, 15, 18, 20, 0, 0, time.UTC)
	bodies := [][]byte{
		[]byte(`{"lead_id":"webcfg-nome-1","receipt_id":"rcpt-nome-1","source":"CONFENGE_WEB","name":"Joaquim","company":"Norte","phone":"41990002222"}`),
		[]byte(`{"lead_id":"webcfg-email-1","receipt_id":"rcpt-email-1","source":"CONFENGE_WEB","email":"joaquim.qa@norte.example","company":"Norte","phone":"41990003333"}`),
		[]byte(`{"lead_id":"SYNTHETIC-INBOUND-20260815T182000Z","receipt_id":"SYNTHETIC-INBOUND-20260815T182000Z","source":"CONFENGE_WEB","company":"SYNTHETIC-INBOUND","email":"synthetic-inbound@example.com","message":"SYNTHETIC-INBOUND do not contact"}`),
		[]byte(`{"lead_id":"flag-synth-1","receipt_id":"flag-synth-1","source":"CONFENGE_WEB","company":"Norte","synthetic":true}`),
	}
	for _, body := range bodies {
		if _, xerr := svc.IngestInboundLead(context.Background(), org, body, IngestOptions{Now: now}); xerr != nil {
			t.Fatal(xerr)
		}
	}
	queue, xerr := svc.CollectInboundNow(context.Background(), org)
	if xerr != nil {
		t.Fatal(xerr)
	}
	got := map[string]bool{}
	for _, item := range queue {
		got[item.LeadID] = true
		if item.Dispatchable || item.EmailSendable {
			t.Fatalf("queue row must stay human-only: %s", item.LeadID)
		}
	}
	if !got["webcfg-nome-1"] || !got["webcfg-email-1"] {
		t.Fatalf("real name/email qa substring dropped: %+v", queue)
	}
	if got["SYNTHETIC-INBOUND-20260815T182000Z"] || got["flag-synth-1"] {
		t.Fatalf("official synthetic leaked into INBOUND NOW: %+v", queue)
	}
	view, xerr := svc.CommercialExecutiveView(context.Background(), org, "2026-08", false)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if view.Denominators.Leads < 2 {
		t.Fatalf("include_synthetic=0 lost real leads: %+v", view)
	}
	fmt.Printf("INBOUND_NOW_KEEP name=Joaquim email=joaquim.qa@norte.example official=skipped include_synthetic=0 leads=%d\n", view.Denominators.Leads)
}

func assertNoRawQueryLeak(t *testing.T, surface string, raw []byte) {
	t.Helper()
	if intel.ContainsForbiddenQuery(raw) || strings.Contains(strings.ToLower(string(raw)), "segunda leitura contrato") {
		t.Fatalf("%s leaked raw query/GSCQuery/query_hash: %s", surface, string(raw))
	}
}

func mustJSONBytes(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
