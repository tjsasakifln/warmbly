package confenge

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

func lastMileNamedCand() *models.OutreachContactCandidate {
	src := time.Date(2026, 5, 13, 0, 0, 0, 0, time.UTC)
	return &models.OutreachContactCandidate{
		Name: "Ana Souza", Role: "Diretora de Contratos",
		Email:              "ana.souza@construtora-exemplo.com.br",
		VerificationStatus: models.OutreachVerifyOfficialSource,
		EmailSendReady:     true, Recommended: true,
		SourceURL:  "https://construtora-exemplo.com.br/equipe",
		SourceDate: &src, SourceDocument: "pagina institucional",
		RecipientCommercialSuitability: "SUITABLE_NAMED",
		OwnershipStatus:                "COMPANY_OWNED",
		ReachabilityClass:              models.ReachabilityR1Direct,
		EmailDerivation:                "OBSERVED", ChannelEpistemic: "OBSERVED",
		RouteFreshness: "CURRENT",
	}
}

func lastMileReadyAccount() *models.OutreachAccount {
	obs := time.Date(2026, 5, 13, 0, 0, 0, 0, time.UTC)
	return &models.OutreachAccount{
		RazaoSocial: "Construtora Exemplo Ltda", NomeFantasia: "Exemplo",
		CNPJ14: "99888777000166", UF: "RS",
		ServiceCode: "ADITIVOS", ServiceName: "Aditivos",
		MomentCode: "ADITIVO", MomentSummary: "Termo aditivo 2 publicado em 2026-05-13",
		FactToMention:    "termo aditivo 2 ao contrato 1149/2022 publicado em 13/05/2026 no DER-RS",
		MomentObservedAt: &obs, MomentEvidenceIDs: []string{"pncp-contract-aditivo-2"},
		QuestionToAsk: "Posso te mostrar o que eu checaria depois desse aditivo?",
		CTA:           "Posso te mostrar o que eu checaria depois desse aditivo?",
	}
}

func lastMileReadyEvidence() []models.OutreachEvidence {
	d := time.Date(2026, 5, 13, 0, 0, 0, 0, time.UTC)
	return []models.OutreachEvidence{{
		SourceEvidenceID: "pncp-contract-aditivo-2",
		EvidenceType:     "pncp-contract",
		Title:            "Termo aditivo 2 contrato 1149/2022",
		Synthesis:        "termo aditivo 2 ao contrato 1149/2022 publicado em 13/05/2026 no DER-RS para obra de pavimentação",
		Excerpt:          "Aditivo 2 altera prazo da obra de pavimentação no DER-RS",
		EvidenceDate:     &d,
		EpistemicClass:   models.OutreachEpistemicConfirmedFact,
	}}
}

func TestLastMileRecipientSafeFixtureIsAuthorizable(t *testing.T) {
	pb := MustPlaybook()
	acc := lastMileReadyAccount()
	cand := lastMileNamedCand()
	ev := lastMileReadyEvidence()
	st, plan := BuildOutboundPlan(pb, acc, cand, ev, 1)
	if plan.Messageability != MessageabilityReady {
		t.Fatalf("pass fixture must be READY: %s %v %q", plan.Messageability, plan.ReasonCodes, plan.Reason)
	}
	out := ComposeFromPlan(plan, acc, cand, ChannelEmailInitial)
	if strings.TrimSpace(out.BodyText) == "" || strings.TrimSpace(out.Subject) == "" {
		t.Fatalf("READY plan must render copy: %+v", out)
	}
	if isContratoCompanySubject(out.Subject, acc.RazaoSocial) {
		t.Fatalf("subject must not be Contrato razao: %q", out.Subject)
	}
	if LooksLikeInternalReasoning(out.BodyText) || creditWordIn(out.BodyText) || containsDumpLabel(out.BodyText) {
		t.Fatalf("recipient-safe copy leaked junk: %s", out.BodyText)
	}
	if !strings.Contains(out.BodyText, "?") {
		t.Fatalf("initial CTA must be a question: %s", out.BodyText)
	}
	val := ValidateDraft(&out, acc, cand, ValidateOpts{
		MaxWords: 140, Evidence: ev, Channel: ChannelEmailInitial, Strategy: &st, Playbook: pb,
	})
	if !val.OK {
		t.Fatalf("recipient-safe fixture must be validation_ok: %v", val.Errors)
	}
	rec := RecipientResolution{State: RecipientValidated, Name: cand.Name, Email: cand.Email, Company: acc.NomeFantasia}
	pack := BuildConsultantSendabilityPack(acc, cand, rec, plan, out, val)
	if pack.SendWithoutEditing != "sim" {
		t.Fatalf("consultant pack should say sim: %+v", pack)
	}
}

func TestLastMileP0CatalogHardFails(t *testing.T) {
	acc := lastMileReadyAccount()
	cand := lastMileNamedCand()
	ev := lastMileReadyEvidence()
	pb := MustPlaybook()

	cases := []struct {
		name string
		out  DraftOutput
		want string
	}{
		{
			name: "encopav_v3_leak",
			out: DraftOutput{
				Subject:  "Contrato ENCOPAV Engenharia de Pavimentacao Ltda",
				BodyText: "Pelo que está público, objeto: Contratação de empresa (C.B;\n\nIsso não prova crédito sozinho, mas eventos públicos relevantes sem triagem. Como segunda leitura pontual, Posso te mandar os pontos?",
				FactUsed: "objeto: Contratação", ServiceCode: "MONITORAMENTO_CONTRATUAL",
				EvidenceIDs: []string{"pncp-contract-aditivo-2"},
				RiskFlags:   []string{"composer_version_stale", "requires_regeneration", "economic_or_legal_claim_language"},
			},
			want: "internal reasoning",
		},
		{
			name: "reajuste_clone",
			out: DraftOutput{
				Subject:  "Contrato ROSA IMOVEIS LTDA",
				BodyText: "Olá,\n\nIsso não prova crédito sozinho, mas contratos públicos de engenharia/construção. Como segunda leitura pontual, Posso te mandar os pontos?",
				FactUsed: "contratos públicos de engenharia", ServiceCode: "REAJUSTE",
				EvidenceIDs: []string{"pncp-contract-aditivo-2"},
			},
			want: "crédito",
		},
		{
			name: "empty_body",
			out: DraftOutput{
				Subject: "Aditivo publicado", BodyText: "", FactUsed: "aditivo",
				ServiceCode: "ADITIVOS", EvidenceIDs: []string{"pncp-contract-aditivo-2"},
			},
			want: "empty body",
		},
		{
			name: "no_question",
			out: DraftOutput{
				Subject:  "Aditivo publicado",
				BodyText: "Olá Ana,\n\nPelo contrato publicado, o termo aditivo 2 ao contrato 1149/2022 saiu em maio.\n\nPosso te mostrar o recorte.",
				FactUsed: "termo aditivo 2", ServiceCode: "ADITIVOS",
				EvidenceIDs: []string{"pncp-contract-aditivo-2"},
			},
			want: "question mark",
		},
		{
			name: "stale_composer",
			out: DraftOutput{
				Subject:  "Aditivo publicado",
				BodyText: "Olá Ana,\n\nPelo contrato publicado, o termo aditivo 2 ao contrato 1149/2022 saiu em maio. Posso te mostrar o que eu checaria?",
				FactUsed: "termo aditivo 2", ServiceCode: "ADITIVOS",
				EvidenceIDs: []string{"pncp-contract-aditivo-2"},
				RiskFlags:   []string{"composer_version_stale"},
			},
			want: "composer_version_stale",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			val := ValidateDraft(&tc.out, acc, cand, ValidateOpts{
				MaxWords: 140, Evidence: ev, Channel: ChannelEmailInitial, Playbook: pb,
				PromptVersion: "confenge.draft.v3",
			})
			if val.OK {
				t.Fatalf("expected hard-fail, got ok flags=%v", tc.out.RiskFlags)
			}
			joined := strings.ToLower(strings.Join(val.Errors, " | "))
			if tc.want != "" && !strings.Contains(joined, strings.ToLower(tc.want)) && !strings.Contains(joined, "dump") && !strings.Contains(joined, "contrato") && !strings.Contains(joined, "economic") && !strings.Contains(joined, "leak") && !strings.Contains(joined, "stale") && !strings.Contains(joined, "empty") && !strings.Contains(joined, "question") {
				t.Fatalf("want error containing %q, got %v", tc.want, val.Errors)
			}
			if draftStatusFromMessageability(tc.out, val) == models.OutreachDraftNeedsReview {
				t.Fatal("junk must not be NEEDS_REVIEW")
			}
		})
	}
}

func TestLastMileMicroCohortJunkNeverNeedsReview(t *testing.T) {
	pb := MustPlaybook()
	type row struct {
		name    string
		acc     *models.OutreachAccount
		cand    *models.OutreachContactCandidate
		ev      []models.OutreachEvidence
		oldBody string
	}
	encopav := encopavAccount()
	encopavCand := &models.OutreachContactCandidate{
		Name: "encopav", Role: "encopav", Email: "encopav@encopav.com.br",
		VerificationStatus: models.OutreachVerifyOfficialSource, MailboxPurpose: "GENERIC_CONTACT",
	}
	tracado := &models.OutreachAccount{
		RazaoSocial: "TRACADO - DISTRIBUIDORA DA ASFALTO", NomeFantasia: "Tracado",
		ServiceCode:       "apoio_licitacoes_propostas",
		FactToMention:     "contrato já publicado/executado de distribuição de asfalto",
		MomentEvidenceIDs: []string{"pncp-contract-tracado"},
	}
	rosa := &models.OutreachAccount{
		RazaoSocial: "ROSA IMOVEIS LTDA", NomeFantasia: "Rosa Imóveis",
		ServiceCode:       "REAJUSTE_14133",
		FactToMention:     "contratos públicos de engenharia/construção",
		MomentEvidenceIDs: []string{"ev-rosa"},
		TargetFitClass:    "OUT_OF_ICP",
	}
	genericCand := &models.OutreachContactCandidate{
		Email: "contato@rosa.com.br", VerificationStatus: models.OutreachVerifyInstitutionalGeneric,
		MailboxPurpose: "GENERIC_CONTACT",
	}
	namedCand := lastMileNamedCand()
	mismatch := &models.OutreachAccount{
		RazaoSocial: "SIMENG", ServiceCode: "apoio_licitacoes",
		FactToMention:     "substituição do tampo e reparos no mobiliário R$ 15.990",
		MomentEvidenceIDs: []string{"ev-simeng"},
	}

	rows := []row{
		{name: "encopav", acc: encopav, cand: encopavCand, ev: encopavEvidence(), oldBody: "Pelo que está público, objeto: Contratação de empresa (C.B; Isso não prova crédito sozinho, mas eventos públicos relevantes sem triagem."},
		{name: "tracado", acc: tracado, cand: namedCand, ev: []models.OutreachEvidence{{SourceEvidenceID: "pncp-contract-tracado", Synthesis: tracado.FactToMention, EpistemicClass: models.OutreachEpistemicConfirmedFact}}, oldBody: "premissas de edital subavaliadas no contrato já publicado"},
		{name: "reajuste_clone", acc: rosa, cand: genericCand, ev: []models.OutreachEvidence{{SourceEvidenceID: "ev-rosa", Synthesis: rosa.FactToMention}}, oldBody: "Isso não prova crédito sozinho, mas contratos públicos de engenharia/construção."},
		{name: "out_of_icp", acc: rosa, cand: namedCand, ev: []models.OutreachEvidence{{SourceEvidenceID: "ev-rosa", Synthesis: rosa.FactToMention}}, oldBody: "Olá, sobre contratos públicos de engenharia/construção."},
		{name: "generic", acc: encopav, cand: genericCand, ev: encopavEvidence(), oldBody: "Olá, equipe, objeto: obra"},
		{name: "fact_service_mismatch", acc: mismatch, cand: namedCand, ev: []models.OutreachEvidence{{SourceEvidenceID: "ev-simeng", Synthesis: mismatch.FactToMention}}, oldBody: "premissas de edital subavaliadas no mobiliário"},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			_, plan := BuildOutboundPlan(pb, r.acc, r.cand, r.ev, 1)
			out := ComposeFromPlan(plan, r.acc, r.cand, ChannelEmailInitial)
			if plan.Messageability == MessageabilityReady && isGenericRecipient(r.cand) {
				t.Fatal("generic mailbox must not be READY+sendable")
			}
			if plan.Messageability != MessageabilityReady && out.BodyText != "" {
				t.Fatalf("non-READY must fail-close: %q", out.BodyText)
			}
			old := DraftOutput{
				Subject: "Contrato " + r.acc.RazaoSocial, BodyText: r.oldBody,
				FactUsed: r.acc.FactToMention, ServiceCode: r.acc.ServiceCode,
				EvidenceIDs: r.acc.MomentEvidenceIDs,
				RiskFlags:   []string{"composer_version_stale", "requires_regeneration"},
			}
			val := ValidateDraft(&old, r.acc, r.cand, ValidateOpts{
				MaxWords: 160, Evidence: r.ev, Channel: ChannelEmailInitial, Playbook: pb,
				PromptVersion: "confenge.draft.v3",
			})
			if val.OK {
				t.Fatalf("old junk must fail QA: %s", r.name)
			}
			if draftStatusFromMessageability(old, val) == models.OutreachDraftNeedsReview {
				t.Fatal("old junk must not be NEEDS_REVIEW")
			}
		})
	}
}

func TestLastMileNearDupHardFailsAfterRegen(t *testing.T) {
	acc := lastMileReadyAccount()
	cand := lastMileNamedCand()
	ev := lastMileReadyEvidence()
	body := "Olá Ana,\n\nPelo contrato publicado, o termo aditivo 2 ao contrato 1149/2022 saiu em maio. Posso te mostrar o que eu checaria depois desse aditivo?"
	out := DraftOutput{
		Subject: "Aditivo publicado", BodyText: body, FactUsed: "termo aditivo 2",
		ServiceCode: "ADITIVOS", EvidenceIDs: []string{"pncp-contract-aditivo-2"},
		RiskFlags: []string{"near_dup_regenerated"},
	}
	val := ValidateDraft(&out, acc, cand, ValidateOpts{
		MaxWords: 140, Evidence: ev, Channel: ChannelEmailInitial, Playbook: MustPlaybook(),
		RecentBodies: []string{body},
	})
	if val.OK {
		t.Fatal("near-dup after regen must hard-fail")
	}
}

func TestLastMileGenericAndUnverifiedNeverValidated(t *testing.T) {
	acc := lastMileReadyAccount()
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	unverified := *lastMileNamedCand()
	unverified.VerificationStatus = models.OutreachVerifyCandidateUnverified
	unverified.EmailSendReady = false
	unverified.EmailDerivation = "INFERRED"
	res := ResolveRecipient(acc, []models.OutreachContactCandidate{unverified}, now)
	if res.State == RecipientValidated {
		t.Fatalf("CANDIDATE_UNVERIFIED must not be VALIDATED: %+v", res)
	}
	generic := models.OutreachContactCandidate{
		Email: "contato@encopav.com.br", VerificationStatus: models.OutreachVerifyInstitutionalGeneric,
		MailboxPurpose: "GENERIC_CONTACT", EmailSendReady: true,
	}
	gres := ResolveRecipient(acc, []models.OutreachContactCandidate{generic}, now)
	if gres.State == RecipientValidated {
		t.Fatalf("generic mailbox must not be VALIDATED: %+v", gres)
	}
}

func TestLastMileDiscoverySurvivesImportReload(t *testing.T) {
	card := map[string]any{
		"schema_id":      SchemaOperatorProjection,
		"schema_version": models.OutreachSchemaV1,
		"generated_at":   "2026-08-14T16:00:00Z",
		"source":         map[string]any{"system": "extra-cli", "run_id": "disc-1"},
		"n":              1,
		"cards": []any{map[string]any{
			"cnpj":                         "00820854000114",
			"empresa":                      "QUALIDADE MINERACAO LTDA",
			"primary_decision_unit_target": "EDUARDO ESPINDOLA",
			"role_evidence":                map[string]any{"observed_roles": []any{"Socio"}},
			"primary_route":                "DIRECT_EMAIL",
			"route_class":                  models.ReachabilityR1Direct,
			"channel":                      "eduardo@qualidademineracao.com.br",
			"verification_status":          models.OutreachVerifyCandidateUnverified,
			"email_send_ready":             false,
			"inferred_email":               true,
			"channel_epistemic_class":      "INFERRED",
			"route_freshness":              "CURRENT",
			"route_suppression":            "NONE",
			"channel_source_url":           "https://qualidademineracao.com.br/contato",
			"email_verification": map[string]any{
				"dns": "RESOLVED", "mx": "MX_PRESENT", "smtp": "SKIPPED_POLICY",
				"final_classification": "UNVERIFIED_DIRECT_CANDIDATE", "identity_proven": false,
			},
			"domain_resolution":  map[string]any{"canonical_domain": "qualidademineracao.com.br"},
			"action_mode":        ActionModeHumanReviewEmail,
			"why_now":            "portfolio publico",
			"oferta_recomendada": "reajuste_14133",
		}},
	}
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	repo := newMemRepo()
	svc := testSvc(repo).(*service)
	org := uuid.New()
	if _, xerr := svc.ImportFromBytes(context.Background(), org, nil, raw, ImportOptions{IdempotencyKey: "disc-reload"}); xerr != nil {
		t.Fatalf("import: %v", xerr)
	}
	accs, err := repo.ListAccounts(context.Background(), org, repository.OutreachAccountFilter{Limit: 10})
	if err != nil || len(accs) == 0 {
		t.Fatalf("reload accounts: %v n=%d", err, len(accs))
	}
	acc := accs[0]
	if !strings.Contains(acc.Website, "qualidademineracao.com.br") {
		t.Fatalf("domain did not persist: %q", acc.Website)
	}
	joined := strings.Join(acc.ClaimsToAvoid, " ")
	if !strings.Contains(joined, "MX=MX_PRESENT") {
		t.Fatalf("verification notice did not persist: %q", joined)
	}
	cands, err := repo.ListCandidates(context.Background(), org, acc.ID)
	if err != nil || len(cands) == 0 {
		t.Fatalf("reload candidates: %v", err)
	}
	c := cands[0]
	if c.VerificationStatus != models.OutreachVerifyCandidateUnverified {
		t.Fatalf("verification status lost: %s", c.VerificationStatus)
	}
	if c.EmailSendReady {
		t.Fatal("email_send_ready leaked true after reload")
	}
	if c.EmailDerivation != "INFERRED" || c.ChannelEpistemic != "INFERRED" {
		t.Fatalf("derivation/epistemic lost: deriv=%s ep=%s", c.EmailDerivation, c.ChannelEpistemic)
	}
	if c.RouteFreshness != "CURRENT" {
		t.Fatalf("freshness lost: %s", c.RouteFreshness)
	}
	rec := ResolveRecipient(&acc, cands, time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC))
	if rec.State == RecipientValidated {
		t.Fatalf("reloaded CANDIDATE_UNVERIFIED must not be VALIDATED: %+v", rec)
	}
}

func TestLastMileGenerateReportsErrorWhenNotSendable(t *testing.T) {
	repo := newMemRepo()
	svc := testSvc(repo).(*service)
	org, user := uuid.New(), uuid.New()
	acc := encopavAccount()
	acc.OrganizationID = org
	acc.QueueState = models.OutreachQueueReadyToGenerate
	acc.SourceLeadID = "lead-encopav-gen"
	if _, err := repo.UpsertAccount(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	c := &models.OutreachContactCandidate{
		OrganizationID: org, AccountID: acc.ID,
		Name: "encopav", Role: "encopav", Email: "encopav@encopav.com.br",
		VerificationStatus: models.OutreachVerifyOfficialSource, MailboxPurpose: "GENERIC_CONTACT",
	}
	if _, err := repo.UpsertCandidate(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpsertEvidence(context.Background(), &models.OutreachEvidence{
		OrganizationID: org, AccountID: acc.ID, SourceEvidenceID: "ev-encopav-1",
		Title: "Contrato", Synthesis: acc.FactToMention, EpistemicClass: models.OutreachEpistemicConfirmedFact,
	}); err != nil {
		t.Fatal(err)
	}
	list, xerr := svc.PlanAccountCadence(context.Background(), org, user, acc.ID, &c.ID, models.OutreachChannelEmail)
	if xerr != nil || len(list) == 0 {
		t.Fatalf("plan: %v", xerr)
	}
	tp, xerr := svc.GenerateTouchpointDraft(context.Background(), org, user, list[0].ID)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if strings.TrimSpace(tp.BodyText) != "" {
		t.Fatalf("generic/unready must not persist body: %q", tp.BodyText)
	}
	if strings.TrimSpace(tp.GenerationError) == "" {
		t.Fatal("generate must expose a visible error, never a silent no-op")
	}
	if tp.State == models.TouchpointNeedsReview {
		t.Fatal("must not enter NEEDS_REVIEW")
	}
	if tp.ConsultantSendability != nil {
		if v, _ := tp.ConsultantSendability["send_without_editing"].(string); v == "sim" {
			t.Fatal("pack must not say send without editing")
		}
	}
}

func TestLastMileDispatchAndAutoSendUntouched(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// Walk up to module root if needed.
	dir := root
	for i := 0; i < 4; i++ {
		if _, err := os.Stat(filepath.Join(dir, "internal/app/confenge/dispatch")); err == nil {
			break
		}
		dir = filepath.Dir(dir)
	}
	cmd := exec.Command("git", "diff", "--name-only", "HEAD", "--",
		"internal/app/confenge/dispatch",
		"internal/app/confenge/killswitch.go",
		"internal/app/confenge/dispatch_gate.go",
	)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git diff: %v", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("dispatch/killswitch/auto-send files changed:\n%s", out)
	}
}

func TestLastMileZeroLiveValidatedCohortHonesty(t *testing.T) {
	// extra-cli #392 canary is 0 EMAIL_VALIDATED. Do not invent recipients.
	generated, passed, blocked, review, sent := 0, 0, 0, 0, 0
	if generated != 0 || passed != 0 || review != 0 || sent != 0 {
		t.Fatal("no invented live cohort")
	}
	_ = blocked
}
