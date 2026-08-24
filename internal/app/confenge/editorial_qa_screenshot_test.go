package confenge

import "testing"

// The founder read these three messages side by side in the UI. v3 and v4 were
// stamped copy_qa=passed while closing with an ask that fits any company in the
// corpus; v5 is what the current composer emits and must stay sendable. These
// fixtures are the exact strings from that screen, not paraphrases.

const screenshotV3Subject = "contratação pública: contratação DE Empresa Especializada Para"

const screenshotV3Body = "Olá, equipe,\n\n" +
	"Sou da CONFENGE.\n\n" +
	"contratação pública: contratação DE Empresa Especializada Para Execução DOS " +
	"Serviços Necessários DOS Serviços Necessários À Recuperação Estrutural DA Ponte " +
	"SOB O RIO Sapucaí,.\n\n" +
	"Queria falar com quem acompanha a carteira de contratos públicos por aí. " +
	"Você consegue me indicar a pessoa responsável?"

const screenshotV4Subject = "contratação pública: empresa Especializada para Execução dos"

const screenshotV4Body = "Olá, equipe,\n\n" +
	"Sou da CONFENGE.\n\n" +
	"contratação pública: empresa Especializada para Execução dos Serviços Necessários " +
	"à Recuperação Estrutural da Ponte sob o Rio Sapucaí, Situada da Rodovia Federal.\n\n" +
	"Queria falar com quem acompanha a carteira de contratos públicos por aí. " +
	"Você consegue me indicar a pessoa responsável?"

const screenshotV5Subject = "Ponte sobre o Rio Sapucaí"

const screenshotV5Body = "Olá,\n\n" +
	"Meu nome é Tiago, da CONFENGE. Vi uma contratação envolvendo a Jatobeton " +
	"Engenharia na recuperação estrutural da ponte sobre o Rio Sapucaí.\n\n" +
	"Trabalho com apoio a empresas de engenharia em contratos públicos e queria falar " +
	"com quem cuida dessa frente por aí. Você consegue me indicar a pessoa certa?\n\n" +
	"Obrigado,\nTiago"

const screenshotRawFact = "contratação pública: contratação DE Empresa Especializada Para Execução DOS " +
	"Serviços Necessários DOS Serviços Necessários À Recuperação Estrutural DA Ponte " +
	"SOB O RIO Sapucaí, Situada da Rodovia Federal"

func requireCode(t *testing.T, label string, got []string, want string) {
	t.Helper()
	if !codeSet(got)[want] {
		t.Fatalf("%s: expected %s in %v", label, want, got)
	}
}

func refuseCode(t *testing.T, label string, got []string, unwanted string) {
	t.Helper()
	if codeSet(got)[unwanted] {
		t.Fatalf("%s: did not expect %s in %v", label, unwanted, got)
	}
}

// TestLegacyClosingsAreNonActionable pins the two shipped messages whose only
// closing was a routing request with nothing in it about this reader.
func TestLegacyClosingsAreNonActionable(t *testing.T) {
	cases := []struct {
		name    string
		subject string
		body    string
	}{
		{"v3", screenshotV3Subject, screenshotV3Body},
		{"v4", screenshotV4Subject, screenshotV4Body},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			codes := EditorialQA(tc.subject, tc.body, EditorialQAContext{
				RouteClass:      RouteClassGenericCompany,
				RawFact:         screenshotRawFact,
				SenderFirstName: "Tiago",
			})
			requireCode(t, tc.name, codes, "generic_cta")
		})
	}
}

// TestCurrentComposerScreenshotStaysClean is the load bearing case: the copy
// the composer emits today is real production output and must keep passing
// with no codes at all.
func TestCurrentComposerScreenshotStaysClean(t *testing.T) {
	codes := EditorialQA(screenshotV5Subject, screenshotV5Body, EditorialQAContext{
		RouteClass:      RouteClassGenericCompany,
		SenderFirstName: "Tiago",
		RawFact:         screenshotRawFact,
	})
	if len(codes) > 0 {
		t.Fatalf("current composer output was refused: %v\n%s", codes, screenshotV5Body)
	}
}

// TestPersonalizationWithoutFactFails pins the fail closed rule: a message may
// not claim it saw something when no record was supplied.
func TestPersonalizationWithoutFactFails(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "vi uma",
			body: "Olá,\n\nMeu nome é Tiago, da CONFENGE. Vi uma contratação envolvendo a Jatobeton Engenharia na recuperação estrutural da ponte sobre o Rio Sapucaí.\n\n" +
				"Trabalho com apoio a empresas de engenharia em contratos públicos e queria falar com quem cuida dessa frente por aí. Você consegue me indicar a pessoa certa?\n\nObrigado,\nTiago",
		},
		{
			name: "vi que",
			body: "Olá,\n\nMeu nome é Tiago, da CONFENGE. Vi que a Jatobeton Engenharia assumiu a recuperação estrutural da ponte sobre o Rio Sapucaí.\n\n" +
				"Trabalho com apoio a empresas de engenharia em contratos públicos e queria falar com quem cuida dessa frente por aí. Você consegue me indicar a pessoa certa?\n\nObrigado,\nTiago",
		},
		{
			name: "notei que",
			body: "Olá,\n\nMeu nome é Tiago, da CONFENGE. Notei que a Jatobeton Engenharia atua na recuperação estrutural da ponte sobre o Rio Sapucaí.\n\n" +
				"Trabalho com apoio a empresas de engenharia em contratos públicos e queria falar com quem cuida dessa frente por aí. Você consegue me indicar a pessoa certa?\n\nObrigado,\nTiago",
		},
		{
			name: "acompanhei",
			body: "Olá,\n\nMeu nome é Tiago, da CONFENGE. Acompanhei a recuperação estrutural da ponte sobre o Rio Sapucaí pela Jatobeton Engenharia.\n\n" +
				"Trabalho com apoio a empresas de engenharia em contratos públicos e queria falar com quem cuida dessa frente por aí. Você consegue me indicar a pessoa certa?\n\nObrigado,\nTiago",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			codes := EditorialQA(screenshotV5Subject, tc.body, EditorialQAContext{
				RouteClass:      RouteClassGenericCompany,
				SenderFirstName: "Tiago",
			})
			requireCode(t, tc.name, codes, "personalization_without_fact")

			with := EditorialQA(screenshotV5Subject, tc.body, EditorialQAContext{
				RouteClass:      RouteClassGenericCompany,
				SenderFirstName: "Tiago",
				RawFact:         screenshotRawFact,
			})
			refuseCode(t, tc.name+" with fact", with, "personalization_without_fact")
		})
	}
}

// TestRoutingAskNeedsAnAnchor pins that asking to be routed is allowed and only
// the bare, context free version of that ask is refused.
func TestRoutingAskNeedsAnAnchor(t *testing.T) {
	head := "Olá,\n\nMeu nome é Tiago, da CONFENGE. Vi uma contratação envolvendo a Jatobeton Engenharia na recuperação estrutural da ponte sobre o Rio Sapucaí.\n\n"
	tail := "\n\nObrigado,\nTiago"

	cases := []struct {
		name string
		ask  string
		want bool
	}{
		{
			name: "bare routing ask",
			ask:  "Você consegue me indicar a pessoa responsável?",
			want: true,
		},
		{
			name: "bare ask with a wish clause in front",
			ask:  "Queria falar com quem acompanha a carteira de contratos públicos por aí. Você consegue me indicar a pessoa responsável?",
			want: true,
		},
		{
			name: "ask anchored on the practice line",
			ask:  "Trabalho com apoio a empresas de engenharia em contratos públicos e queria falar com quem cuida dessa frente por aí. Você consegue me indicar a pessoa certa?",
			want: false,
		},
		{
			name: "ask anchored on the observed fact",
			ask:  "Queria entender como vocês tocam a recuperação estrutural dessa ponte. Você consegue me indicar a pessoa certa?",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			codes := EditorialQA(screenshotV5Subject, head+tc.ask+tail, EditorialQAContext{
				RouteClass:      RouteClassGenericCompany,
				RawFact:         screenshotRawFact,
				SenderFirstName: "Tiago",
			})
			if tc.want {
				requireCode(t, tc.name, codes, "generic_cta")
				return
			}
			refuseCode(t, tc.name, codes, "generic_cta")
		})
	}
}
