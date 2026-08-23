package confenge

import (
	"strings"
	"testing"
)

// The live v1 body read:
//
//	"contratação pública: contratação DE Empresa Especializada Para Execução
//	 DOS Serviços Necessários DOS Serviços Necessários À Recuperação
//	 Estrutural DA Ponte SOB O RIO Sapucaí,."
//
// Three separate defects, all owned by this repository's outreach projection.
func TestLiveV1FactProjectionHasNoCopyDefects(t *testing.T) {
	got := condenseMetadataFact(liveV1RawFact)
	got = ApplyCopyHygiene(got)
	if got == "" {
		t.Fatal("the projection must still produce a usable fact")
	}

	if ng, ok := hasRepeatedNGram(got, 3); ok {
		t.Errorf("repeated 3-gram %q survived the projection: %q", ng, got)
	}
	if ng, ok := hasRepeatedNGram(got, 1); ok && ng == "contratação" {
		t.Errorf("the label repeats the object's own first word: %q", got)
	}
	for _, bad := range []string{",.", ",,", " ,", "..", ";."} {
		if strings.Contains(got, bad) {
			t.Errorf("defective punctuation %q in projection: %q", bad, got)
		}
	}
	// Short Portuguese function words must not survive as shouted residue of an
	// all-caps edital. "DOS Serviços" is the tell the founder actually saw.
	for _, w := range strings.Fields(got) {
		trimmed := strings.Trim(w, ".,;:!?()")
		if trimmed == "" {
			continue
		}
		upper := strings.ToUpper(trimmed)
		lower := strings.ToLower(trimmed)
		if trimmed == upper && upper != lower && len([]rune(trimmed)) <= 3 {
			t.Errorf("all-caps residue %q in projection: %q", trimmed, got)
		}
	}
	t.Logf("projection = %q", got)
}

// Whatever the projection produces must be terminated as a sentence by the
// composer without ever creating ",." — the exact defect in two live messages.
func TestComposerTerminatesFactWithoutDoublePunctuation(t *testing.T) {
	for _, fact := range []string{
		"contratação pública: recuperação estrutural da ponte sobre o Rio Sapucaí,",
		"contratação pública: pavimentação asfáltica em vias urbanas;",
		"contratação pública: reforma da escola municipal",
		"contratação pública: obra concluída.",
	} {
		got := terminateFactSentence(fact)
		if strings.HasSuffix(got, ",.") || strings.HasSuffix(got, ";.") || strings.HasSuffix(got, "..") {
			t.Errorf("terminated %q into %q", fact, got)
		}
		if !strings.HasSuffix(got, ".") {
			t.Errorf("fact %q must end as a sentence, got %q", fact, got)
		}
	}
}

// Defence in depth: even if a future composer change reintroduces a defect, the
// admission gate must refuse the member instead of freezing it as eligible.
// The live v1 froze with admission_reasons carrying "copy_qa=passed".
func TestCopyQARefusesDefectiveCopy(t *testing.T) {
	cases := map[string]string{
		"repeated_ngram":        "Olá, equipe,\n\nSou da CONFENGE.\n\ncontratação pública: execução dos serviços necessários dos serviços necessários à recuperação.\n\nQueria falar com quem acompanha a carteira.",
		"defective_punctuation": "Olá, equipe,\n\nSou da CONFENGE.\n\ncontratação pública: recuperação estrutural da ponte,.\n\nQueria falar com quem acompanha a carteira.",
		"shouted_residue":       "Olá, equipe,\n\nSou da CONFENGE.\n\ncontratação pública: execução DOS Serviços Necessários DA Ponte.\n\nQueria falar com quem acompanha a carteira.",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			errs := ValidateCopyForRouteClass(RouteClassGenericCompany, body, "contratação pública", nil)
			if len(errs) == 0 {
				t.Fatalf("copy QA passed defective copy (%s): %q", name, body)
			}
			t.Logf("%s -> %v", name, errs)
		})
	}
}

// Clean copy must still pass; a gate that refuses everything blocks the pilot.
func TestCopyQAAcceptsCleanCopy(t *testing.T) {
	body := "Olá, equipe,\n\nSou da CONFENGE.\n\ncontratação pública: recuperação estrutural da ponte sobre o Rio Sapucaí.\n\nQueria falar com quem acompanha a carteira de contratos públicos por aí. Você consegue me indicar a pessoa responsável?"
	if errs := ValidateCopyForRouteClass(RouteClassGenericCompany, body, "recuperação estrutural da ponte", nil); len(errs) > 0 {
		t.Fatalf("clean copy must pass, got %v", errs)
	}
}
