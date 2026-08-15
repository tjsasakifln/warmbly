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
