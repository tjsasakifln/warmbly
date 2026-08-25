package confenge

import (
	"strings"
	"testing"

	"github.com/warmbly/warmbly/internal/models"
)

// These are the exact strings the founder previewed on 2026-08-24, recomposed
// from the real production manifests. Every one of them passed EditorialQA with
// zero codes, so each is pinned twice: the composer must not write it and the
// gate must refuse it even if some future composer or rewrite does.

// The real PNCP facts carried by the v3 and v4 cohorts.
const (
	realFactAquaflot = "contratação pública: licenciamento Ambiental - Mineração, Exploração Florestal, " +
		"Implantação DE Rodovias E Recuperação DE Áreas Degradadas (Renovado)"
	realFactNovacap = "contratação pública: serviços Comuns DE Engenharia, Sendo Esses A Recuperação DE " +
		"Pavimentação Asfáltica, DE Calçadas E Sinalização Viária, NO Âmbito DO Estado DO RIO DE Janeiro,"
	realRegistryNameInplenitus = "INPLENITUS PROJETOS, GERENCIAMENTO E FISCALIZ"
)

// defectiveBody wraps one broken sentence in otherwise clean copy, so a test is
// never green because the surrounding message was also wrong.
func defectiveBody(observation string) string {
	return "Olá,\n\nMeu nome é Tiago, da CONFENGE. " + observation + "\n\n" +
		"Trabalho com apoio a empresas de engenharia em contratos públicos e queria falar " +
		"com quem cuida dessa frente por aí. Você consegue me indicar a pessoa certa?\n\nObrigado,\nTiago"
}

// TestAdministrativeLabelNeverReachesProse pins defect 1: the "contratação
// pública" label survived the digest and made the sentence stutter.
func TestAdministrativeLabelNeverReachesProse(t *testing.T) {
	leaked := []string{
		"Vi uma contratação envolvendo a Aquaflot Ambiental na contratação pública licenciamento ambiental - mineração.",
		"Vi uma contratação envolvendo a Novacap Engenharia na contratação pública serviços comuns de engenharia.",
	}
	for _, obs := range leaked {
		t.Run(obs[:40], func(t *testing.T) {
			codes := EditorialQA("Licenciamento ambiental", defectiveBody(obs), EditorialQAContext{
				RouteClass:      RouteClassGenericCompany,
				RawFact:         realFactAquaflot,
				SenderFirstName: "Tiago",
			})
			requireCode(t, "leaked label", codes, "body_administrative_label")
		})
	}

	for _, tc := range []struct{ raw, phrase, subject string }{
		{realFactAquaflot, "licenciamento ambiental para mineração", "Licenciamento ambiental para mineração"},
		{realFactNovacap, "serviços comuns de engenharia", "Sobre serviços comuns de engenharia"},
	} {
		d := DigestPublicFact(tc.raw)
		if d.Phrase != tc.phrase {
			t.Errorf("phrase=%q want %q (reasons=%v)", d.Phrase, tc.phrase, d.Reasons)
		}
		if d.Subject != tc.subject {
			t.Errorf("subject=%q want %q", d.Subject, tc.subject)
		}
		if strings.Contains(foldASCII(strings.ToLower(d.Phrase)), "contratacao publica") {
			t.Errorf("digest kept the administrative label: %q", d.Phrase)
		}
	}
}

// TestRealCohortFactsComposeCleanly is the positive half: the same production
// facts must produce copy the gate accepts with no codes at all.
func TestRealCohortFactsComposeCleanly(t *testing.T) {
	cases := []struct {
		company string
		fact    string
		want    string
	}{
		{"AQUAFLOT AMBIENTAL", realFactAquaflot, "no licenciamento ambiental para mineração"},
		{"NOVACAP ENGENHARIA", realFactNovacap, "nos serviços comuns de engenharia"},
	}
	for _, tc := range cases {
		t.Run(tc.company, func(t *testing.T) {
			acc := &models.OutreachAccount{NomeFantasia: tc.company, FactToMention: tc.fact}
			out, reasons := ComposeEditorialInitial(acc, &models.OutreachContactCandidate{Email: "contato@exemplo.com.br"}, RouteClassGenericCompany)
			if len(reasons) > 0 {
				t.Fatalf("composer refused a sayable fact: %v", reasons)
			}
			if !strings.Contains(out.Body, tc.want) {
				t.Fatalf("observation did not agree with the head noun, want %q:\n%s", tc.want, out.Body)
			}
			codes := EditorialQA(out.Subject, out.Body, EditorialQAContext{
				RouteClass:      RouteClassGenericCompany,
				RawFact:         tc.fact,
				SenderFirstName: "Tiago",
			})
			if len(codes) > 0 {
				t.Fatalf("composer emitted copy its own gate refuses: %v\n%s\n%s", codes, out.Subject, out.Body)
			}
		})
	}
}

// TestGenericSubjectIsRefused pins defect 2: two members of one cohort both
// shipped "Contrato público", a subject that names no work at all.
func TestGenericSubjectIsRefused(t *testing.T) {
	for _, subject := range []string{
		"Contrato público",
		"Contratos públicos",
		"Licitação pública",
		"Processo administrativo",
		"Objeto do contrato",
	} {
		t.Run(subject, func(t *testing.T) {
			codes := EditorialQA(subject, cleanEditorialBody(), EditorialQAContext{
				RouteClass:      RouteClassGenericCompany,
				RawFact:         liveV1RawFact,
				SenderFirstName: "Tiago",
			})
			requireCode(t, subject, codes, "subject_generic_boilerplate")
		})
	}
	// A subject naming the work is what the same check must let through.
	for _, subject := range []string{
		"Ponte sobre o Rio Sapucaí",
		"Licenciamento ambiental para mineração",
		"Contratos públicos de engenharia",
		"Reequilíbrio do contrato público",
	} {
		t.Run("allowed/"+subject, func(t *testing.T) {
			if !subjectNamesSpecificWork(subject) {
				t.Fatalf("a subject naming real work was treated as boilerplate: %q", subject)
			}
		})
	}
	// The composer must not fall back to the generic head either.
	for _, raw := range []string{realFactAquaflot, realFactNovacap} {
		if d := DigestPublicFact(raw); d.Subject == "Contrato público" {
			t.Fatalf("composer wrote the generic subject for %q", raw)
		}
	}
}

// TestTruncatedRegistryNameNeverReachesProse pins defect 3: a registry name cut
// at its width limit was spoken as if it were the company's name.
func TestTruncatedRegistryNameNeverReachesProse(t *testing.T) {
	leaked := "Entrei em contato por causa da atuação da Inplenitus Projetos, Gerenciamento E Fiscaliz em contratos públicos de engenharia."
	codes := EditorialQA("Contratos públicos de engenharia", defectiveBody(leaked), EditorialQAContext{
		RouteClass:      RouteClassGenericCompany,
		RawFact:         realFactNovacap,
		SenderFirstName: "Tiago",
	})
	requireCode(t, "truncated registry name", codes, "truncated_word")

	acc := &models.OutreachAccount{RazaoSocial: realRegistryNameInplenitus}
	if got := editorialCompanyName(acc); got != "Inplenitus" {
		t.Fatalf("company name=%q, want the short trading name", got)
	}
	out, reasons := ComposeEditorialInitial(acc, &models.OutreachContactCandidate{Email: "contato@exemplo.com.br"}, RouteClassGenericCompany)
	if len(reasons) > 0 {
		t.Fatalf("composer refused a recoverable company: %v", reasons)
	}
	if strings.Contains(out.Body, "Fiscaliz") || strings.Contains(out.Body, "Gerenciamento E") {
		t.Fatalf("truncated registry name reached prose:\n%s", out.Body)
	}
}

// TestTruncatedWordDetection keeps the truncation check honest in both
// directions: ordinary Portuguese words are not cut words.
func TestTruncatedWordDetection(t *testing.T) {
	for _, s := range []string{"Fiscaliz", "Gerenciament", "caracterizaç", "Engenh"} {
		if !truncatedWordIn(s) {
			t.Errorf("%q was not detected as truncated", s)
		}
	}
	for _, s := range []string{
		"Inplenitus", "Aquaflot Ambiental", "Novacap Engenharia", "Jatobeton Engenharia",
		"recuperação estrutural da ponte sobre o Rio Sapucaí",
		"licenciamento ambiental para mineração",
		"Você consegue me indicar a pessoa certa?",
		"quem gerencia essa frente por aí",
	} {
		if truncatedWordIn(s) {
			t.Errorf("%q was wrongly treated as truncated", s)
		}
	}
}

// TestComposedProseIsNotEvidence pins the recompose loop: the sentence this
// composer wrote last time is our own output, never a public record, so it can
// neither be re-digested into a fact nor laundered into the company fallback.
func TestComposedProseIsNotEvidence(t *testing.T) {
	prior := "Vi uma contratação envolvendo a Jatobeton Engenharia na recuperação estrutural da ponte sobre o Rio Sapucaí."
	d := DigestPublicFact(prior)
	if d.Phrase != "" {
		t.Fatalf("our own sentence was digested as a fact: %q", d.Phrase)
	}
	if !codeSet(d.Reasons)["fact_is_composed_prose"] {
		t.Fatalf("reasons=%v", d.Reasons)
	}
	acc := &models.OutreachAccount{NomeFantasia: "Jatobeton Engenharia", FactToMention: prior}
	_, reasons := ComposeEditorialInitial(acc, &models.OutreachContactCandidate{Email: "contato@exemplo.com.br"}, RouteClassGenericCompany)
	if !codeSet(reasons)["fact_is_composed_prose"] {
		t.Fatalf("composer recomposed its own prose instead of refusing: %v", reasons)
	}
}
