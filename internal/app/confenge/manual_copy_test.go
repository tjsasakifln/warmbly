package confenge

import (
	"strings"
	"testing"

	"github.com/warmbly/warmbly/internal/models"
)

// #67 fixtures: the robotic / dump / mislabeled strings the founder was asked to copy.
const (
	issue67DumpHook = "objeto: Contratação de empresa; órgão: DER-RS; UF: RS; R$ 2,839,000"
	issue67Company  = "Encopav Engenharia LTDA"
	issue67Person   = "Carlos Silva"
	issue67Evidence = "ev-encopav-1"
)

func issue67Action(actionType, channelValue string) models.OutreachCommercialAction {
	return models.OutreachCommercialAction{
		ActionType:        actionType,
		CompanyName:       issue67Company,
		PersonName:        issue67Person,
		FactualHook:       issue67DumpHook,
		ServiceContext:    "monitoramento contratual",
		ServiceCode:       "MONITORAMENTO_CONTRATUAL",
		RecommendedAction: "Posso enviar um recorte objetivo?",
		ChannelValue:      channelValue,
		EvidenceIDs:       []string{issue67Evidence},
		WhyNow:            "contrato público recente",
	}
}

func founderText(c CommercialActionContent) string {
	return FounderFacingCopy(c)
}

func TestIssue67RegressionsManualCopy(t *testing.T) {
	t.Run("call_not_unaccented_dump_or_pitch", func(t *testing.T) {
		got := ComposeActionContent(issue67Action(models.ActionDirectCall, "+5551999990000"))
		text := founderText(got)
		t.Logf("CALL copy: %q", text)
		assertConsultancyCall(t, text, got)
		assertNotEmailTemplate(t, "CALL", text)
		assertNoDumpOrIDs(t, text)
		assertAccentedPTBR(t, text)
	})

	t.Run("routed_call_asks_reception_not_personal_line", func(t *testing.T) {
		a := issue67Action(models.ActionRoutedCall, "+555133330000")
		a.RouteRelation = models.RouteRelRoutesToNamedPerson
		a.TargetRole = "recepção"
		got := ComposeActionContent(a)
		text := founderText(got)
		t.Logf("ROUTED_CALL copy: %q", text)
		assertConsultancyCall(t, text, got)
		assertNotEmailTemplate(t, "ROUTED_CALL", text)
		assertNoDumpOrIDs(t, text)
		assertAccentedPTBR(t, text)
		low := foldASCII(text)
		if !strings.Contains(low, "encaminh") && !strings.Contains(low, "passar") && !strings.Contains(low, "quem acompanha") {
			t.Errorf("ROUTED_CALL must ask to be routed, got %q", text)
		}
		if strings.Contains(low, "ola, carlos") {
			t.Errorf("ROUTED_CALL must not greet the switchboard as a personal line: %q", text)
		}
	})

	t.Run("whatsapp_short_not_email_dump", func(t *testing.T) {
		got := ComposeActionContent(issue67Action(models.ActionWhatsApp, "+5551999990000"))
		text := founderText(got)
		t.Logf("WHATSAPP copy: %q", text)
		assertWhatsAppConsultancy(t, text)
		assertNotEmailTemplate(t, "WHATSAPP", text)
		assertNoDumpOrIDs(t, text)
		assertAccentedPTBR(t, text)
	})

	t.Run("generic_mailbox_no_invented_function", func(t *testing.T) {
		a := issue67Action(models.ActionGenericEmail, "contato@encopav.com.br")
		a.PersonName = ""
		a.TargetRole = ""
		got := ComposeActionContent(a)
		text := founderText(got)
		t.Logf("GENERIC copy: %q", text)
		assertNoDumpOrIDs(t, text)
		assertAccentedPTBR(t, text)
		assertNotEmailTemplate(t, "GENERIC", text)
		low := foldASCII(text)
		if strings.Contains(low, "carlos") {
			t.Errorf("generic mailbox must not address a named person: %q", text)
		}
		if strings.Contains(low, "area de contratos") || strings.Contains(low, "equipe de contratos") {
			t.Errorf("generic mailbox must not invent a function: %q", text)
		}
	})

	t.Run("comercial_at_not_area_de_contratos", func(t *testing.T) {
		item := ManualQueueItem{
			Company:           issue67Company,
			FactualHook:       issue67DumpHook,
			Channel:           "email",
			ChannelValue:      "comercial@encopav.com.br",
			RecommendedAction: "Decidir manualmente se a caixa funcional deve ser usada.",
		}
		cls := ContactClass{Tier: ContactTierC, Lane: LaneRoleMailboxException, Channel: "email"}
		text := manualSuggestedText(cls, item)
		t.Logf("TIER C comercial@ copy: %q", text)
		low := foldASCII(text)
		if strings.Contains(low, "area de contratos") {
			t.Errorf("comercial@ must not be labeled área de contratos: %q", text)
		}
		if !strings.Contains(low, "comercial@") && !strings.Contains(low, "caixa da empresa") {
			t.Errorf("comercial@ label must stay local-part or caixa da empresa, got %q", text)
		}
		assertNoDumpOrIDs(t, text)
		assertAccentedPTBR(t, text)
	})

	t.Run("vendas_at_not_area_de_contratos", func(t *testing.T) {
		item := ManualQueueItem{
			Company:      issue67Company,
			FactualHook:  issue67DumpHook,
			Channel:      "email",
			ChannelValue: "vendas@encopav.com.br",
		}
		text := manualSuggestedText(ContactClass{Tier: ContactTierC}, item)
		t.Logf("TIER C vendas@ copy: %q", text)
		low := foldASCII(text)
		if strings.Contains(low, "area de contratos") {
			t.Errorf("vendas@ must not be labeled área de contratos: %q", text)
		}
		if !strings.Contains(low, "vendas@") && !strings.Contains(low, "caixa da empresa") {
			t.Errorf("vendas@ label must stay local-part or caixa da empresa, got %q", text)
		}
	})

	t.Run("whatsapp_composer_not_email_paste", func(t *testing.T) {
		acc := &models.OutreachAccount{
			NomeFantasia:  issue67Company,
			FactToMention: issue67DumpHook,
			EntryOffer:    "monitoramento contratual",
			QuestionToAsk: "Posso te mandar o recorte do que eu conferiria neste contrato publicado?",
		}
		cand := &models.OutreachContactCandidate{Name: issue67Person}
		text := BuildWhatsAppCopy(acc, cand)
		t.Logf("BuildWhatsAppCopy: %q", text)
		assertWhatsAppConsultancy(t, text)
		assertNotEmailTemplate(t, "BuildWhatsAppCopy", text)
		assertNoDumpOrIDs(t, text)
	})

	t.Run("four_routes_do_not_share_email_template", func(t *testing.T) {
		email := ComposeActionContent(issue67Action(models.ActionDirectEmail, "carlos@encopav.com.br"))
		call := founderText(ComposeActionContent(issue67Action(models.ActionDirectCall, "+5551")))
		routed := founderText(ComposeActionContent(issue67Action(models.ActionRoutedCall, "+5551")))
		wa := founderText(ComposeActionContent(issue67Action(models.ActionWhatsApp, "+5551")))
		generic := founderText(ComposeActionContent(func() models.OutreachCommercialAction {
			a := issue67Action(models.ActionGenericEmail, "contato@encopav.com.br")
			a.PersonName = ""
			return a
		}()))
		if email.Body == "" {
			t.Fatal("email fixture empty")
		}
		for name, body := range map[string]string{"CALL": call, "ROUTED_CALL": routed, "WHATSAPP": wa, "GENERIC": generic} {
			if body == email.Body {
				t.Errorf("%s reused the email body", name)
			}
			assertNotEmailTemplate(t, name, body)
		}
	})
}

func TestManualCopyAdversarialRoutes(t *testing.T) {
	cases := []struct {
		name  string
		a     models.OutreachCommercialAction
		item  *ManualQueueItem
		cls   ContactClass
		check func(t *testing.T, text string, c CommercialActionContent)
	}{
		{
			name: "telefone_geral",
			a: models.OutreachCommercialAction{
				ActionType: models.ActionRoutedCall, CompanyName: "Metrovia",
				FactualHook:  "aditivo 2 ao contrato 88/2021 publicado em julho/2026",
				ChannelValue: "+555133331100", RouteRelation: models.RouteRelRoutesToNamedPerson,
			},
			check: func(t *testing.T, text string, c CommercialActionContent) {
				assertConsultancyCall(t, text, c)
				low := foldASCII(text)
				if strings.Contains(low, "seu telefone") || strings.Contains(low, "seu ramal") {
					t.Errorf("general phone treated as personal: %q", text)
				}
				if !strings.Contains(low, "encaminh") && !strings.Contains(low, "passar") && !strings.Contains(low, "quem acompanha") {
					t.Errorf("general phone must ask reception to route: %q", text)
				}
			},
		},
		{
			name: "recepcao",
			a: models.OutreachCommercialAction{
				ActionType: models.ActionRoutedCall, CompanyName: "Atlas Obras",
				PersonName: "Marina Costa", TargetRole: "recepção",
				FactualHook:  "contrato 1149/2022 atingiu aniversário de reajuste",
				ChannelValue: "+555132221100", RouteRelation: models.RouteRelRoutesToNamedPerson,
			},
			check: func(t *testing.T, text string, c CommercialActionContent) {
				assertConsultancyCall(t, text, c)
				if strings.HasPrefix(foldASCII(text), "ola, marina") {
					t.Errorf("reception must not be greeted as the named target: %q", text)
				}
			},
		},
		{
			name: "numero_setorial",
			a: models.OutreachCommercialAction{
				ActionType: models.ActionDirectCall, CompanyName: "Construtora Delta",
				PersonName: "Paulo Mendes", ObservedRole: "compras",
				FactualHook:  "edital de manutenção predial publicado na prefeitura",
				ChannelValue: "+555134445566", RouteRelation: models.RouteRelBelongsToNamedPerson,
			},
			check: func(t *testing.T, text string, c CommercialActionContent) {
				assertConsultancyCall(t, text, c)
				assertNoLegalEconomicClaim(t, text)
			},
		},
		{
			name: "whatsapp_nao_comprovado",
			a: models.OutreachCommercialAction{
				ActionType: models.ActionWhatsApp, CompanyName: "Encopav Engenharia",
				PersonName: "Carlos Silva", FactualHook: "aditivo publicado no PNCP",
				ChannelValue: "+5551999888777",
				Warnings:     []string{"WhatsApp sem consentimento comprovado"},
			},
			check: func(t *testing.T, text string, c CommercialActionContent) {
				assertWhatsAppConsultancy(t, text)
				joined := foldASCII(text + " " + strings.Join(c.DoNotClaim, " "))
				if !strings.Contains(joined, "consent") && !strings.Contains(joined, "nao enviar automaticamente") && !strings.Contains(joined, "não enviar automaticamente") {
					t.Errorf("unproven WhatsApp must not be presented as sendable: copy=%q donot=%v", text, c.DoNotClaim)
				}
				if strings.Contains(foldASCII(text), "ja combinamos") || strings.Contains(foldASCII(text), "conforme conversamos") {
					t.Errorf("unproven WhatsApp invented prior consent: %q", text)
				}
			},
		},
		{
			name: "caixa_generica",
			a: models.OutreachCommercialAction{
				ActionType: models.ActionGenericEmail, CompanyName: "Atlas Obras S.A.",
				ChannelValue: "contato@atlasobras.com.br",
				FactualHook:  "contrato 1149/2022 atingiu aniversário de reajuste em 2024",
			},
			check: func(t *testing.T, text string, _ CommercialActionContent) {
				low := foldASCII(text)
				if strings.Contains(low, "ola, ") && strings.ContainsAny(text, "AÁBCDEFGHIJKLMNOPQRSTUVWXYZ") {
					// named greeting is the failure mode
				}
				if strings.Contains(low, "diretor") || strings.Contains(low, "area de contratos") {
					t.Errorf("generic box invented identity/function: %q", text)
				}
				assertNotEmailTemplate(t, "caixa_generica", text)
				assertAccentedPTBR(t, text)
			},
		},
		{
			name: "empresa_fora_de_icp",
			a: models.OutreachCommercialAction{
				ActionType: models.ActionDirectCall, CompanyName: "Padaria Central LTDA",
				FactualHook:    "licença sanitária renovada na prefeitura",
				ServiceCode:    "MONITORAMENTO_CONTRATUAL",
				ServiceContext: "monitoramento contratual de obras públicas",
				Warnings:       []string{"empresa fora do ICP"},
			},
			check: func(t *testing.T, text string, _ CommercialActionContent) {
				low := foldASCII(text)
				if strings.Contains(low, "monitoramento contratual") || strings.Contains(low, "cobertura nacional") {
					t.Errorf("out-of-ICP must not invent coverage: %q", text)
				}
				if !strings.Contains(text, "?") {
					t.Errorf("out-of-ICP should stay a question, got %q", text)
				}
				assertNoLegalEconomicClaim(t, text)
			},
		},
		{
			name: "hook_service_mismatch",
			a: models.OutreachCommercialAction{
				ActionType: models.ActionWhatsApp, CompanyName: "Hospital São Lucas",
				FactualHook:    "edital de merenda escolar publicado",
				ServiceCode:    "REEQUILIBRIO",
				ServiceContext: "reequilíbrio econômico de contrato de engenharia",
			},
			check: func(t *testing.T, text string, _ CommercialActionContent) {
				low := foldASCII(text)
				if strings.Contains(low, "reequilibrio") || strings.Contains(low, "reequilíbrio") {
					t.Errorf("mismatch must not push the unmatched offer: %q", text)
				}
				if strings.Count(text, "?") < 1 {
					t.Errorf("mismatch should ask, not pitch: %q", text)
				}
				assertWhatsAppConsultancy(t, text)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := ComposeActionContent(tc.a)
			text := founderText(c)
			t.Logf("%s copy: %q", tc.name, text)
			if strings.TrimSpace(text) == "" {
				t.Fatal("empty founder copy")
			}
			assertNoDumpOrIDs(t, text)
			if tc.check != nil {
				tc.check(t, text, c)
			}
		})
	}
}

func TestManualCopyHygieneOnRoutes(t *testing.T) {
	a := models.OutreachCommercialAction{
		ActionType:  models.ActionDirectCall,
		CompanyName: "Construtora Alfa LTDA - EM RECUPERACAO JUDICIAL",
		FactualHook: "C0ntrato  publico  de  pavimentacao  R$ 1,200,000",
		EvidenceIDs: []string{"fact-99", "ev-abc"},
	}
	text := founderText(ComposeActionContent(a))
	t.Logf("hygiene copy: %q", text)
	low := foldASCII(text)
	if strings.Contains(low, "ltda") || strings.Contains(low, "recuperacao judicial") || strings.Contains(low, "recuperação judicial") {
		t.Errorf("razão social suffixes leaked into copy: %q", text)
	}
	if strings.Contains(text, "R$ 1,200,000") {
		t.Errorf("US-style amount leaked: %q", text)
	}
	if strings.Contains(text, "C0ntrato") {
		t.Errorf("evident OCR leaked: %q", text)
	}
	if strings.Contains(text, "fact-99") || strings.Contains(text, "ev-abc") {
		t.Errorf("evidence ids leaked into copy: %q", text)
	}
	assertAccentedPTBR(t, text)
}

func TestManualSuggestedTextRoutesDoNotReuseEmail(t *testing.T) {
	hook := issue67DumpHook
	phone := manualSuggestedText(ContactClass{Tier: ContactTierB, Channel: "phone"}, ManualQueueItem{
		Company: issue67Company, Person: issue67Person, Channel: "phone", FactualHook: hook,
	})
	wa := manualSuggestedText(ContactClass{Tier: ContactTierB, Channel: "whatsapp"}, ManualQueueItem{
		Company: issue67Company, Person: issue67Person, Channel: "whatsapp", FactualHook: hook,
	})
	generic := manualSuggestedText(ContactClass{Tier: ContactTierD}, ManualQueueItem{
		Company: issue67Company, Channel: "email", ChannelValue: "contato@encopav.com.br", FactualHook: hook,
	})
	t.Logf("manual phone=%q wa=%q generic=%q", phone, wa, generic)
	assertConsultancyCall(t, phone, CommercialActionContent{Kind: "CALL", Body: phone, Opening: phone, Ask: phone})
	assertWhatsAppConsultancy(t, wa)
	assertNotEmailTemplate(t, "manual-generic", generic)
	if strings.Contains(foldASCII(generic), "carlos") {
		t.Errorf("generic suggested text addressed a person: %q", generic)
	}
}

func assertConsultancyCall(t *testing.T, text string, c CommercialActionContent) {
	t.Helper()
	if !strings.Contains(text, "CONFENGE") {
		t.Errorf("CALL must identify CONFENGE: %q", text)
	}
	if !strings.Contains(text, "?") {
		t.Errorf("CALL must ask a routing/diagnostic question: %q", text)
	}
	if countWords(text) > 70 {
		t.Errorf("CALL is a long pitch (%d words): %q", countWords(text), text)
	}
	assertNoLegalEconomicClaim(t, text)
	_ = c
}

func assertWhatsAppConsultancy(t *testing.T, text string) {
	t.Helper()
	if countWords(text) > 40 {
		t.Errorf("WhatsApp too long (%d words, cap well under %d): %q", countWords(text), MaxWhatsAppWords, text)
	}
	if strings.Count(text, "?") != 1 {
		t.Errorf("WhatsApp must have exactly one question, got %d: %q", strings.Count(text, "?"), text)
	}
	if strings.Contains(foldASCII(text), "urgente") || strings.Contains(foldASCII(text), "ultima chance") {
		t.Errorf("WhatsApp has artificial pressure: %q", text)
	}
}

func assertNotEmailTemplate(t *testing.T, route, text string) {
	t.Helper()
	low := foldASCII(text)
	// Email shape: Olá + hook paragraph + email CTA.
	emailCTA := strings.Contains(low, "posso te mandar o recorte do que eu conferiria") ||
		strings.Contains(low, "posso enviar um recorte objetivo para a equipe")
	emailHook := strings.Contains(low, "pelo que esta publico") || strings.Contains(low, "pelo que está público")
	if strings.HasPrefix(strings.TrimSpace(low), "ola") && emailHook && emailCTA {
		t.Errorf("%s reused the email template shape: %q", route, text)
	}
	if emailHook && strings.Contains(low, "escrevo para") && emailCTA {
		t.Errorf("%s reused the role-email template shape: %q", route, text)
	}
}

func assertNoDumpOrIDs(t *testing.T, text string) {
	t.Helper()
	low := foldASCII(text)
	for _, lab := range []string{"objeto:", "orgao:", "órgão:", "uf:", "cnpj:"} {
		if strings.Contains(low, foldASCII(lab)) {
			t.Errorf("dump label %q in copy: %q", lab, text)
		}
	}
	if strings.Contains(text, "R$ 2,839,000") || strings.Contains(text, "R$ 1,200,000") {
		t.Errorf("raw US-style amount dump in copy: %q", text)
	}
	if strings.Contains(text, issue67Evidence) || strings.Contains(low, "ev-encopav") {
		t.Errorf("evidence id leaked into copy: %q", text)
	}
}

func assertAccentedPTBR(t *testing.T, text string) {
	t.Helper()
	low := text
	// Robotic unaccented templates from the #67 review.
	for _, bad := range []string{"Ola ", "Ola.", "Ola,", "esta publico", "nao abordar", "nao e o", "aqui e da"} {
		if strings.Contains(low, bad) {
			t.Errorf("unaccented robotic fragment %q in copy: %q", bad, text)
		}
	}
}

func assertNoLegalEconomicClaim(t *testing.T, text string) {
	t.Helper()
	low := foldASCII(text)
	for _, p := range []string{"problema juridico", "problema econômico", "problema economico", "em recuperacao judicial", "sem credito", "sem crédito", "risco de credito"} {
		if strings.Contains(low, foldASCII(p)) {
			t.Errorf("unproven legal/economic claim %q in copy: %q", p, text)
		}
	}
	if creditWordIn(text) {
		t.Errorf("credit language in manual copy: %q", text)
	}
}

func TestFounderFacingCopyJoinsCallFields(t *testing.T) {
	c := CommercialActionContent{
		Kind: "CALL", Opening: "Olá, aqui é da CONFENGE.",
		ReasonForCall: "Vi um contrato público da empresa.",
		Ask:           "Quem acompanha esse contrato?",
	}
	got := FounderFacingCopy(c)
	if !strings.Contains(got, "CONFENGE") || !strings.Contains(got, "?") {
		t.Fatalf("join failed: %q", got)
	}
	if strings.Contains(got, "ev-") {
		t.Fatalf("ids in join: %q", got)
	}
}
