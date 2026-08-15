package confenge

import (
	"fmt"
	"testing"

	"github.com/warmbly/warmbly/internal/models"
)

func TestClassifyInboundNextActionFixtures(t *testing.T) {
	cases := []struct {
		name       string
		lead       InboundLeadV1
		facts      InboundFacts
		wantNext   string
		wantSend   bool
		wantNamed  bool
		wantPerson string
	}{
		{
			name: "contract+valid phone",
			lead: InboundLeadV1{
				LeadID: "c1", ContractID: "CTR-88", Phone: "+5541999887766",
				CTAID: "segunda-leitura-contrato", Message: "Quero uma segunda leitura do contrato",
				HighIntentHint: true, Consent: InboundConsent{PreferredChannel: "phone"},
			},
			facts:    InboundFacts{Phone: "+5541999887766", PhonePresent: true, WhyNow: "contrato CTR-88"},
			wantNext: models.InboundNextCall,
		},
		{
			name: "validated email high-intent",
			lead: InboundLeadV1{
				LeadID: "c2", Email: "ana@empresa.com", HighIntentHint: true, ContractID: "CTR-1",
			},
			facts:    InboundFacts{Email: "ana@empresa.com", EmailValidated: true, NamedHuman: true, PersonName: "Ana"},
			wantNext: models.InboundNextSendEmail,
			wantSend: false,
		},
		{
			name: "known person no direct channel",
			lead: InboundLeadV1{LeadID: "c3", Name: "Bruno Lima", CompanyName: "Obra Sul"},
			facts: InboundFacts{
				NamedHuman: true, PersonName: "Bruno Lima", PersonID: "p-bruno",
				AccountID: "acc-1",
			},
			wantNext:   models.InboundNextRoutedCall,
			wantPerson: "Bruno Lima",
		},
		{
			name:     "insufficient identity",
			lead:     InboundLeadV1{LeadID: "c4"},
			facts:    InboundFacts{Status: models.InboundEnrichmentUnknown},
			wantNext: models.InboundNextNeedsEnrichment,
		},
		{
			name:     "DNC fail-closed",
			lead:     InboundLeadV1{LeadID: "c5", Email: "x@y.com", Consent: InboundConsent{DNC: true}},
			facts:    InboundFacts{DNC: true, Email: "x@y.com"},
			wantNext: models.InboundNextSuppressed,
		},
		{
			name: "generic mailbox not named-human",
			lead: InboundLeadV1{LeadID: "c6", Email: "contato@empresa.com", CompanyName: "Empresa"},
			facts: InboundFacts{
				Email: "contato@empresa.com", GenericMailbox: true, PersonName: "Contato",
				AccountID: "acc-2",
			},
			wantNext:  models.InboundNextManualOutreach,
			wantNamed: false,
		},
		{
			name: "enrichment unavailable still classifies from lead facts",
			lead: InboundLeadV1{LeadID: "c7", Phone: "41991112222", CompanyName: "Obra Sul", HighIntentHint: true},
			facts: InboundFacts{
				Phone: "41991112222", PhonePresent: true, Status: models.InboundEnrichmentUnavailable,
			},
			wantNext: models.InboundNextCall,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyInboundNextAction(tc.lead, tc.facts)
			fmt.Printf("CLASSIFY case=%s next=%s sendable=%v dispatch=false\n", tc.name, got.NextAction, got.EmailSendable)
			if got.NextAction != tc.wantNext {
				t.Fatalf("next_action=%s want %s (%s)", got.NextAction, tc.wantNext, got.RecommendedAction)
			}
			if got.EmailSendable {
				t.Fatal("classifier must never mark email sendable")
			}
			if tc.wantSend && got.EmailSendable {
				t.Fatal("unreachable")
			}
			if got.NextAction == models.InboundNextSendEmail && got.Lane != models.LaneEmailNeedsReview {
				t.Fatalf("SEND_EMAIL must stay in review lane, got %s", got.Lane)
			}
			if tc.wantNext == models.InboundNextSuppressed && got.Status != models.InboundStatusSuppressed {
				t.Fatal("DNC must suppress")
			}
			if tc.facts.GenericMailbox && got.NextAction == models.InboundNextSendEmail {
				t.Fatal("generic mailbox must not become SEND_EMAIL")
			}
		})
	}
}

func TestEnrichInboundDoesNotInventOrPromote(t *testing.T) {
	lead := InboundLeadV1{
		LeadID: "e1", Name: "Ana Souza", Email: "ana@empresa.com", Phone: "41999999999",
		CNPJ: "55444333000122", ContractID: "CTR-88",
	}
	acc := &models.OutreachAccount{CNPJ14: "55444333000122", SourceLeadID: "extra-acc-1", MomentSummary: "aditivo publicado"}
	cands := []models.OutreachContactCandidate{{
		Name: "Fulano", Email: "contato@empresa.com", EmailSendReady: true,
		MailboxPurpose: "GENERIC", VerificationStatus: models.OutreachVerifyInstitutionalGeneric,
		PersonID: "p-extra",
	}}
	ev := []models.OutreachEvidence{{SourceEvidenceID: "CONTRACT_MARGIN_EVENT", EvidenceType: "CONTRACT_MARGIN_EVENT", Title: "margem"}}
	facts := EnrichInboundFacts(lead, acc, cands, ev, false)
	fmt.Printf("ENRICH person=%q email_validated=%v generic=%v margin=%v invented=false\n",
		facts.PersonName, facts.EmailValidated, facts.GenericMailbox, facts.ContractMargin)
	if facts.PersonName != "Ana Souza" {
		t.Fatalf("lead-supplied name replaced: %q", facts.PersonName)
	}
	if facts.EmailValidated {
		t.Fatal("generic/inferred mailbox must not become VALIDATED")
	}
	if facts.Email != "ana@empresa.com" {
		t.Fatalf("lead email replaced by inference: %q", facts.Email)
	}
	if !facts.ContractMargin {
		t.Fatal("CONTRACT_MARGIN_EVENT not consumed")
	}
	if facts.ExtraCLIAccountID != "extra-acc-1" {
		t.Fatalf("account_id not resolved: %q", facts.ExtraCLIAccountID)
	}

	down := EnrichInboundFacts(lead, nil, nil, nil, true)
	if down.Status != models.InboundEnrichmentUnavailable {
		t.Fatalf("unavailable status=%s", down.Status)
	}
	if down.PersonName != "Ana Souza" {
		t.Fatal("unavailable enrichment invented or dropped lead name")
	}
}
