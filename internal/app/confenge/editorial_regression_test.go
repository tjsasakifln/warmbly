package confenge

import (
	"strings"
	"testing"

	"github.com/warmbly/warmbly/internal/models"
)

// The cohort the founder opened on 2026-08-23 was frozen with
// admission_reasons saying copy_qa=passed while carrying every defect below.
// These fixtures are the contract that none of them can come back green.

// liveV3Body is the message v3 actually froze, reconstructed from the composer
// that produced it. It is the regression baseline, not a hypothetical.
const liveV3Body = "Olá, equipe,\n\nSou da CONFENGE.\n\n" +
	"contratação pública: contratação DE Empresa Especializada Para Execução " +
	"DOS Serviços Necessários DOS Serviços Necessários À Recuperação Estrutural " +
	"DA Ponte SOB O RIO Sapucaí,.\n\n" +
	"Queria falar com quem acompanha contratos públicos por aí. Você consegue me indicar a pessoa responsável?"

const liveV3Subject = "contratação pública: contratação DE Empresa Especializada Para Execução DOS"

func codeSet(codes []string) map[string]bool {
	out := make(map[string]bool, len(codes))
	for _, c := range codes {
		out[c] = true
	}
	return out
}

func requireAnyCode(t *testing.T, label string, got []string, want ...string) {
	t.Helper()
	have := codeSet(got)
	for _, w := range want {
		if have[w] {
			return
		}
	}
	t.Fatalf("%s: expected one of %v, got %v", label, want, got)
}

// TestLiveV3CohortFailsEditorialQA is the headline regression: the exact copy
// that shipped as copy_qa=passed must now be refused.
func TestLiveV3CohortFailsEditorialQA(t *testing.T) {
	codes := EditorialQA(liveV3Subject, liveV3Body, EditorialQAContext{
		RouteClass:      RouteClassGenericCompany,
		RawFact:         liveV1RawFact,
		SenderFirstName: "Tiago",
	})
	if len(codes) == 0 {
		t.Fatal("the live v3 message passed editorial QA; the gate is not a gate")
	}
	have := codeSet(codes)
	for _, must := range []string{
		"robotic_greeting",
		"robotic_self_introduction",
		"shouted_residue",
		"repeated_ngram",
		"defective_punctuation",
		"subject_administrative_object",
		"subject_label_prefix",
	} {
		if !have[must] {
			t.Errorf("expected %s in %v", must, codes)
		}
	}
}

// TestObservedDefectsEachFailIndependently pins every defect the founder named
// so a partial fix cannot let one of them back in alone.
func TestObservedDefectsEachFailIndependently(t *testing.T) {
	base := "Olá,\n\nMeu nome é Tiago, da CONFENGE. Vi uma contratação envolvendo a empresa na recuperação estrutural da ponte sobre o Rio Sapucaí.\n\nTrabalho com apoio a empresas de engenharia em contratos públicos e queria falar com quem cuida dessa frente por aí. Você consegue me indicar a pessoa certa?\n\nObrigado,\nTiago"

	cases := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "shouted mixed case from edital",
			body: strings.Replace(base, "recuperação estrutural da ponte", "Recuperação Estrutural DA Ponte", 1),
			want: []string{"shouted_residue"},
		},
		{
			name: "repeated ngram",
			body: strings.Replace(base, "na recuperação estrutural", "dos serviços necessários dos serviços necessários à recuperação estrutural", 1),
			want: []string{"repeated_ngram"},
		},
		{
			name: "broken punctuation",
			body: strings.Replace(base, "Rio Sapucaí.", "Rio Sapucaí,.", 1),
			want: []string{"defective_punctuation"},
		},
		{
			name: "team greeting",
			body: strings.Replace(base, "Olá,", "Olá, equipe,", 1),
			want: []string{"robotic_greeting"},
		},
		{
			name: "robotic self introduction",
			body: strings.Replace(base, "Meu nome é Tiago, da CONFENGE.", "Sou da CONFENGE.", 1),
			want: []string{"robotic_self_introduction", "synthetic_register"},
		},
		{
			name: "label prefix in body",
			body: strings.Replace(base, "na recuperação", "no objeto: recuperação", 1),
			want: []string{"metadata_dump"},
		},
		{
			name: "raw enum leaked",
			body: strings.Replace(base, "contratos públicos", "contratos públicos (MOMENT_CODE_PORTFOLIO_REVIEW)", 1),
			want: []string{"raw_enum"},
		},
		{
			name: "serialized record leaked",
			body: strings.Replace(base, "contratos públicos", "contratos públicos priority_score=0.82", 1),
			want: []string{"serialized_record"},
		},
		{
			name: "two calls to action",
			body: strings.Replace(base, "Obrigado,", "Podemos falar amanhã?\n\nObrigado,", 1),
			want: []string{"multiple_calls_to_action"},
		},
		{
			name: "synthetic opener",
			body: strings.Replace(base, "Meu nome é Tiago, da CONFENGE.", "Espero que esteja bem. Meu nome é Tiago, da CONFENGE.", 1),
			want: []string{"synthetic_register"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			codes := EditorialQA("Ponte sobre o Rio Sapucaí", tc.body, EditorialQAContext{
				RouteClass:      RouteClassGenericCompany,
				SenderFirstName: "Tiago",
			})
			requireAnyCode(t, tc.name, codes, tc.want...)
		})
	}
}

// TestSubjectDefectsFail covers the subject line on its own, because a clean
// body behind an edital subject is still an edital in the inbox.
func TestSubjectDefectsFail(t *testing.T) {
	cases := []struct {
		name    string
		subject string
		want    []string
	}{
		{"administrative object", "Contratação de empresa especializada para execução", []string{"subject_administrative_object"}},
		{"label prefix", "contratação pública: ponte", []string{"subject_label_prefix"}},
		{"shouted", "PONTE SOBRE O RIO SAPUCAI", []string{"subject_shouted"}},
		{"artificial title case", "Recuperação Estrutural Da Ponte Sobre", []string{"subject_artificial_title_case"}},
		{"truncated on preposition", "Recuperação estrutural da ponte sobre o", []string{"subject_truncated", "subject_too_long"}},
		{"trailing comma", "Ponte sobre o Rio Sapucaí,", []string{"subject_truncated"}},
		{"fake reply prefix", "RE: ponte sobre o rio", []string{"subject_fake_reply_prefix"}},
		{"broken punctuation", "Ponte sobre o Rio Sapucaí ,.", []string{"subject_defective_punctuation"}},
		{"metadata", "route_class=GENERIC_COMPANY", []string{"subject_metadata"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			codes := EditorialQA(tc.subject, cleanEditorialBody(), EditorialQAContext{
				RouteClass:      RouteClassGenericCompany,
				SenderFirstName: "Tiago",
			})
			requireAnyCode(t, tc.name, codes, tc.want...)
		})
	}
}

// TestSubjectSlicedFromRawFactFails pins the rule that a subject is written,
// never obtained with fact[:N].
func TestSubjectSlicedFromRawFactFails(t *testing.T) {
	raw := "recuperação estrutural da ponte sobre o Rio Sapucaí situada na rodovia federal"
	codes := EditorialQA("recuperação estrutural da ponte", cleanEditorialBody(), EditorialQAContext{
		RouteClass:      RouteClassGenericCompany,
		RawFact:         raw,
		SenderFirstName: "Tiago",
	})
	requireAnyCode(t, "sliced subject", codes, "subject_sliced_from_raw_fact")
}

// TestRawPNCPPastedIntoBodyFails pins that the record may be projected, never
// pasted, no matter how tidy the surrounding prose is.
func TestRawPNCPPastedIntoBodyFails(t *testing.T) {
	pasted := "Olá,\n\nMeu nome é Tiago, da CONFENGE. Vi o seguinte: " +
		"contratação de empresa especializada para execução dos serviços necessários à recuperação estrutural da ponte sob o rio sapucaí situada da rodovia federal.\n\n" +
		"Trabalho com apoio a empresas de engenharia em contratos públicos e queria falar com quem cuida dessa frente por aí. Você consegue me indicar a pessoa certa?\n\nObrigado,\nTiago"
	codes := EditorialQA("Ponte sobre o Rio Sapucaí", pasted, EditorialQAContext{
		RouteClass:      RouteClassGenericCompany,
		RawFact:         "objeto: CONTRATAÇÃO DE EMPRESA ESPECIALIZADA PARA EXECUÇÃO DOS SERVIÇOS NECESSÁRIOS À RECUPERAÇÃO ESTRUTURAL DA PONTE SOB O RIO SAPUCAÍ SITUADA DA RODOVIA FEDERAL",
		SenderFirstName: "Tiago",
	})
	requireAnyCode(t, "pasted record", codes, "raw_fact_pasted")
}

// cleanEditorialBody is a body that must always pass, so a subject test is
// never green merely because the body was also broken.
func cleanEditorialBody() string {
	return "Olá,\n\nMeu nome é Tiago, da CONFENGE. Vi uma contratação envolvendo a empresa na recuperação estrutural da ponte sobre o Rio Sapucaí.\n\n" +
		"Trabalho com apoio a empresas de engenharia em contratos públicos e queria falar com quem cuida dessa frente por aí. Você consegue me indicar a pessoa certa?\n\nObrigado,\nTiago"
}

// TestCleanEditorialBodyPasses proves the gate is not simply refusing
// everything, which would be an equally broken gate.
func TestCleanEditorialBodyPasses(t *testing.T) {
	codes := EditorialQA("Ponte sobre o Rio Sapucaí", cleanEditorialBody(), EditorialQAContext{
		RouteClass:      RouteClassGenericCompany,
		SenderFirstName: "Tiago",
	})
	if len(codes) > 0 {
		t.Fatalf("clean editorial copy was refused: %v", codes)
	}
}

// TestFactDigestProducesSayablePhrase pins the projection the founder asked
// for: the shouted, self-repeating record becomes one plain phrase.
func TestFactDigestProducesSayablePhrase(t *testing.T) {
	d := DigestPublicFact(liveV1RawFact)
	if d.Phrase != "recuperação estrutural da ponte sobre o Rio Sapucaí" {
		t.Fatalf("phrase=%q", d.Phrase)
	}
	if d.Subject != "Ponte sobre o Rio Sapucaí" {
		t.Fatalf("subject=%q", d.Subject)
	}
	if _, repeated := hasRepeatedNGram(d.Phrase, 3); repeated {
		t.Fatal("digested phrase still repeats itself")
	}
	if strings.Contains(d.Phrase, "SOB O RIO") {
		t.Fatal("digested phrase kept the shouted, wrong preposition")
	}
}

// TestFactDigestRefusesUnsayableFacts pins that a fact with no safe
// recipient-facing reading is dropped rather than forced into copy.
func TestFactDigestRefusesUnsayableFacts(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"internal telemetry", "Portfólio público observado com 8 contrato(s) no input."},
		{"empty", ""},
		{"amount only", "objeto: R$ 8.763.672,00"},
		{"process number", "objeto: processo 12345/2026-88"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if d := DigestPublicFact(tc.raw); d.Phrase != "" {
				t.Fatalf("expected refusal, got phrase=%q", d.Phrase)
			}
		})
	}
}

// TestComposerUsesGroundedCompanyFallback pins recovery over discard: an
// unsayable record is dropped, but the imported company context can still
// support a short routing message without inventing a procurement event.
func TestComposerUsesGroundedCompanyFallback(t *testing.T) {
	acc := &models.OutreachAccount{
		NomeFantasia:  "Construtora Exemplo",
		FactToMention: "Portfólio público observado com 8 contrato(s) no input.",
	}
	out, reasons := ComposeEditorialInitial(acc, &models.OutreachContactCandidate{Email: "contato@exemplo.com.br"}, RouteClassGenericCompany)
	if len(reasons) != 0 {
		t.Fatalf("composer discarded a recoverable company: %v", reasons)
	}
	if !strings.Contains(out.Body, "atuação da Construtora Exemplo") {
		t.Fatalf("fallback did not use grounded company context:\n%s", out.Body)
	}
	if strings.Contains(out.Body, "8 contrato") || strings.Contains(out.Body, "no input") {
		t.Fatalf("fallback leaked the unsayable fact:\n%s", out.Body)
	}
}

// TestComposerOutputPassesItsOwnGate is the end-to-end contract: whatever the
// composer emits must already be sendable without a human rewriting a word.
func TestComposerOutputPassesItsOwnGate(t *testing.T) {
	acc := &models.OutreachAccount{
		NomeFantasia:  "Construtora Exemplo",
		FactToMention: liveV1RawFact,
	}
	for _, class := range []string{
		RouteClassGenericCompany,
		RouteClassPublicCompanyFreemail,
		RouteClassRoleOrDepartment,
	} {
		t.Run(class, func(t *testing.T) {
			out, reasons := ComposeEditorialInitial(acc, &models.OutreachContactCandidate{Email: "contato@exemplo.com.br"}, class)
			if len(reasons) > 0 {
				t.Fatalf("composer refused a sayable fact: %v", reasons)
			}
			codes := EditorialQA(out.Subject, out.Body, EditorialQAContext{
				RouteClass:      class,
				RawFact:         liveV1RawFact,
				SenderFirstName: "Tiago",
			})
			if len(codes) > 0 {
				t.Fatalf("composer emitted copy its own gate refuses: %v\n%s", codes, out.Body)
			}
			if n := len(strings.Fields(out.Body)); n < 45 || n > 110 {
				t.Errorf("body is %d words, doctrine prefers 45-110:\n%s", n, out.Body)
			}
		})
	}
}

// TestGenericRouteAsksToBeRouted pins that an institutional mailbox is treated
// as a door, never as proof the reader owns the subject.
func TestGenericRouteAsksToBeRouted(t *testing.T) {
	acc := &models.OutreachAccount{NomeFantasia: "Construtora Exemplo", FactToMention: liveV1RawFact}
	out, reasons := ComposeEditorialInitial(acc, &models.OutreachContactCandidate{Email: "contato@exemplo.com.br"}, RouteClassGenericCompany)
	if len(reasons) > 0 {
		t.Fatalf("unexpected refusal: %v", reasons)
	}
	folded := foldASCII(strings.ToLower(out.Body))
	if !strings.Contains(folded, "indicar a pessoa certa") {
		t.Fatalf("generic route did not ask to be routed:\n%s", out.Body)
	}
	if strings.Contains(folded, "ola, equipe") {
		t.Fatal("generic route reintroduced the team greeting")
	}
}

// TestSenderIdentityFailsClosed pins that an unresolvable human sender stops
// composition rather than producing an anonymous or invented signature.
func TestSenderIdentityFailsClosed(t *testing.T) {
	t.Setenv(EnvSenderName, "equipe")
	if _, err := ResolveSenderIdentity(); err == nil {
		t.Fatal("a role word was accepted as a human sender name")
	}
	t.Setenv(EnvSenderName, "Mariana Alves")
	id, err := ResolveSenderIdentity()
	if err != nil {
		t.Fatalf("configured sender rejected: %v", err)
	}
	if id.FirstName != "Mariana" {
		t.Fatalf("first name=%q", id.FirstName)
	}
}

// TestCorpusQADetectsMailMerge pins the cohort-level view: individually clean
// messages that are the same message must not all survive.
func TestCorpusQADetectsMailMerge(t *testing.T) {
	body := cleanEditorialBody()
	members := []FrozenCohortMember{
		{AccountRef: "a", Subject: "Ponte sobre o Rio Sapucaí", BodyText: body},
		{AccountRef: "b", Subject: "Ponte sobre o Rio Sapucaí", BodyText: body},
		{AccountRef: "c", Subject: "Ponte sobre o Rio Sapucaí", BodyText: body},
	}
	findings := CorpusQA(members)
	if len(findings) == 0 {
		t.Fatal("three identical bodies passed corpus QA")
	}
	for _, ref := range []string{"a", "b", "c"} {
		if len(findings[ref]) == 0 {
			t.Errorf("member %s not flagged: %v", ref, findings)
		}
	}
}

// TestCorpusQAAllowsSharedBoilerplate pins that presentation and sign-off are
// allowed to repeat, so the corpus check does not force fake variety.
func TestCorpusQAAllowsSharedBoilerplate(t *testing.T) {
	mk := func(ref, fact, subject string) FrozenCohortMember {
		return FrozenCohortMember{
			AccountRef: ref,
			Subject:    subject,
			BodyText: "Olá,\n\nMeu nome é Tiago, da CONFENGE. Vi uma contratação envolvendo a empresa na " + fact + ".\n\n" +
				"Trabalho com apoio a empresas de engenharia em contratos públicos e queria falar com quem cuida dessa frente por aí. Você consegue me indicar a pessoa certa?\n\nObrigado,\nTiago",
		}
	}
	members := []FrozenCohortMember{
		mk("a", "recuperação estrutural da ponte sobre o Rio Sapucaí", "Ponte sobre o Rio Sapucaí"),
		mk("b", "pavimentação asfáltica em CBUQ na avenida Brasil", "Pavimentação asfáltica em CBUQ"),
		mk("c", "reforma da escola municipal do centro", "Reforma da escola municipal"),
	}
	if findings := CorpusQA(members); len(findings) > 0 {
		t.Fatalf("distinct facts were flagged as a mail merge: %v", findings)
	}
}

// TestMergeCriticCannotClearDeterministicFailure pins the authority rule: a
// semantic critic may add findings and may never remove one.
func TestMergeCriticCannotClearDeterministicFailure(t *testing.T) {
	merged := MergeCriticFindings([]string{"shouted_residue"}, nil)
	if len(merged) != 1 || merged[0] != "shouted_residue" {
		t.Fatalf("critic silence dropped a deterministic failure: %v", merged)
	}
	merged = MergeCriticFindings([]string{"shouted_residue"}, []string{"reads_as_ai", "shouted_residue"})
	if len(merged) != 2 {
		t.Fatalf("expected additive merge, got %v", merged)
	}
}
