package confenge

import (
	"testing"
)

func TestLoadPlaybookSchema(t *testing.T) {
	pb, err := LoadPlaybook()
	if err != nil {
		t.Fatalf("LoadPlaybook: %v", err)
	}
	if pb.Doctrine.OutreachDoctrineVersion != OutreachDoctrineVersion {
		t.Fatalf("version %q", pb.Doctrine.OutreachDoctrineVersion)
	}
	if OutreachDoctrineVersion != "confenge-outreach-v1" {
		t.Fatalf("unexpected doctrine version constant")
	}
	if len(pb.Offers.Offers) < 5 {
		t.Fatalf("offers too few: %d", len(pb.Offers.Offers))
	}
	if len(pb.Services.Services) < 8 {
		t.Fatalf("services too few: %d", len(pb.Services.Services))
	}
	// Cold path only LOW
	for _, o := range pb.Offers.Offers {
		if o.FulfillmentCost == "" {
			t.Fatalf("offer %s missing cost", o.Code)
		}
	}
	if pb.ChannelPolicyWhatsAppOn() {
		t.Fatal("whatsapp must be OFF in doctrine")
	}
}

// helper for channel policy check without exporting field path noise
func (pb *Playbook) ChannelPolicyWhatsAppOn() bool {
	if pb == nil {
		return false
	}
	return pb.Doctrine.ChannelPolicy.WhatsApp == "ON"
}

func TestResolveServiceAndOffer(t *testing.T) {
	pb := MustPlaybook()
	s := pb.ResolveServicePlaybook("REAJUSTE_14133")
	if s == nil || s.Code != "REAJUSTE" {
		t.Fatalf("prefix resolve: %+v", s)
	}
	s2 := pb.ResolveServicePlaybook("ADDITIVE_REVIEW")
	if s2 == nil || s2.Code != "ADITIVOS" {
		t.Fatalf("alias additive: %+v", s2)
	}
	o := pb.FindOffer("REAJUSTE_CHECK")
	if o == nil || o.FulfillmentCost != "LOW" {
		t.Fatalf("reajuste check: %+v", o)
	}
	if !pb.OfferApplicable(o, "REAJUSTE", true) {
		t.Fatal("offer should apply to REAJUSTE cold")
	}
}

func TestMapBuyerRoleNeverInfersFromGeneric(t *testing.T) {
	pb := MustPlaybook()
	if pb.MapBuyerRole("") != "UNKNOWN" {
		t.Fatal("empty")
	}
	if pb.MapBuyerRole("Engenheiro de Obras") != "ENGINEERING" {
		t.Fatal("engineering")
	}
	if pb.MapBuyerRole("Diretor Financeiro") != "DIRECTOR" && pb.MapBuyerRole("Diretor Financeiro") != "FINANCE" {
		// "Diretor" matches first; acceptable either DIRECTOR or if finance keywords win
		role := pb.MapBuyerRole("Diretor Financeiro")
		if role != "DIRECTOR" && role != "FINANCE" {
			t.Fatalf("role %s", role)
		}
	}
	if pb.MapBuyerRole("contato@empresa.com.br") == "OWNER_PARTNER" {
		t.Fatal("must not infer owner from email")
	}
}
