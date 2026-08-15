package intel

import (
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

func TestObserveFromInboundRejectsEmailCNPJOutcome(t *testing.T) {
	lead := models.OutreachInboundLead{
		LeadID: "webcfg-a", ReceiptID: "rcpt-a", EntityID: "acc-a",
		LeadEmail: "shared@empresa.com", CNPJ14: "11222333000181",
	}
	foreign := &models.OutreachOutcome{
		ID:           uuid.MustParse("55555555-5555-4555-8555-555555555555"),
		SourceLeadID: "webcfg-b", EventType: OutcomeMeeting,
		ContactEmail: "shared@empresa.com", CNPJ14: "11222333000181",
	}
	facts := ObserveFromInbound(lead, nil, nil, foreign)
	if facts.Keys.OutcomeID != "" {
		t.Fatalf("foreign meeting attached via email/cnpj: %s", facts.Keys.OutcomeID)
	}
	if facts.OutcomeType == OutcomeMeeting {
		t.Fatal("foreign MEETING leaked onto the lead")
	}

	own := &models.OutreachOutcome{
		ID:           uuid.MustParse("66666666-6666-4666-8666-666666666666"),
		SourceLeadID: "webcfg-a", EventType: OutcomeWon,
	}
	ownFacts := ObserveFromInbound(lead, nil, nil, own)
	if ownFacts.Keys.OutcomeID != own.ID.String() {
		t.Fatalf("same lead_id outcome not joined: %s", ownFacts.Keys.OutcomeID)
	}
	fmt.Printf("OBSERVE_JOIN own_outcome=%s foreign_rejected=true\n", ownFacts.Keys.OutcomeID)
}

func TestObserveFromInboundMarksOfficialSyntheticPreflight(t *testing.T) {
	lead := models.OutreachInboundLead{
		LeadID: "SYNTHETIC-INBOUND-20260815T214505Z", ReceiptID: "SYNTHETIC-INBOUND-20260815T214505Z",
		Source: "CONFENGE_WEB", CompanyName: "SYNTHETIC-INBOUND",
		LeadEmail:  "synthetic-inbound@example.com",
		RawPayload: []byte(`{"lead_id":"SYNTHETIC-INBOUND-20260815T214505Z","company":"SYNTHETIC-INBOUND","email":"synthetic-inbound@example.com","message":"SYNTHETIC-INBOUND do not contact"}`),
		Status:     models.InboundStatusOpen,
	}
	facts := ObserveFromInbound(lead, nil, nil, nil)
	if !facts.Synthetic || facts.Label != LabelSynthetic {
		t.Fatalf("official preflight POST must not be REAL: synthetic=%v label=%s", facts.Synthetic, facts.Label)
	}
	real := ObserveFromInbound(models.OutreachInboundLead{
		LeadID: "webcfg-real-1", ReceiptID: "rcpt-real-1", Source: "CONFENGE_WEB", CompanyName: "Construtora Norte",
	}, nil, nil, nil)
	if real.Synthetic || real.Label == LabelSynthetic {
		t.Fatalf("real lead marked synthetic: %+v", real)
	}
	fmt.Printf("OBSERVE_SYNTHETIC official_preflight=SYNTHETIC real_label=%s\n", real.Label)
}

func TestUtmQueryNeverReadsCampaign(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"real query", `{"query":"segunda leitura contrato","campaign":"brand"}`, "segunda leitura contrato"},
		{"search_query", `{"search_query":"leitura contrato"}`, "leitura contrato"},
		{"q", `{"q":"obra norte"}`, "obra norte"},
		{"utm_term", `{"utm_term":"segunda-leitura","utm_campaign":"brand-aug"}`, "segunda-leitura"},
		{"term", `{"term":"contrato","campaign":"ignored"}`, "contrato"},
		{"campaign only", `{"utm_campaign":"brand-aug","campaign":"brand-aug","utm_source":"google"}`, ""},
		{"empty", `{}`, ""},
	}
	for _, tc := range cases {
		got := utmQuery([]byte(tc.raw))
		if got != tc.want {
			t.Fatalf("%s utmQuery=%q want=%q", tc.name, got, tc.want)
		}
	}
	lead := models.OutreachInboundLead{
		LeadID: "webcfg-utm-1", ReceiptID: "rcpt-utm-1", Source: "CONFENGE_WEB",
		UTMJSON: []byte(`{"utm_campaign":"brand-aug","campaign":"brand-aug"}`),
	}
	facts := ObserveFromInbound(lead, nil, nil, nil)
	if facts.Keys.Query != "" {
		t.Fatalf("campaign-only Observe query=%q want empty", facts.Keys.Query)
	}
	fmt.Printf("UTM_QUERY campaign_only=empty query_keys=query,search_query,q,utm_term,term\n")
}
