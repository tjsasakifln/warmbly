package confenge

import (
	"errors"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/warmbly/warmbly/internal/models"
)

func TestCommercialDestinationRegistryIsFiniteAndFailClosed(t *testing.T) {
	destinations := CommercialDestinations()
	if len(destinations) != 15 {
		t.Fatalf("destinations=%d want 15", len(destinations))
	}
	seen := map[string]bool{}
	active, withheld := 0, 0
	for _, destination := range destinations {
		if destination.ID == "" || seen[destination.ID] {
			t.Fatalf("empty or duplicate landing id %q", destination.ID)
		}
		seen[destination.ID] = true
		parsed, err := url.Parse(destination.CanonicalURL)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() != "confenge.com.br" || parsed.RawQuery != "" || parsed.Fragment != "" {
			t.Fatalf("unsafe canonical destination %q: %v", destination.CanonicalURL, err)
		}
		if destination.CanonicalURL == "https://confenge.com.br/" {
			t.Fatalf("generic home cannot be a commercial destination: %+v", destination)
		}
		if !reflect.DeepEqual(destination.UTMAllowlist, commercialUTMAllowlist) {
			t.Fatalf("landing %s UTM allowlist=%v", destination.ID, destination.UTMAllowlist)
		}
		if destination.PublicProofExpected == "" || destination.TerminalAction == "" {
			t.Fatalf("landing %s lacks proof or terminal action", destination.ID)
		}
		switch destination.State {
		case CommercialDestinationActive:
			active++
			if destination.Activation != CommercialActivationActive {
				t.Fatalf("active landing %s has activation=%s", destination.ID, destination.Activation)
			}
		case CommercialDestinationWithheldPendingWebRelease:
			withheld++
			if destination.Activation != CommercialActivationNotActivated {
				t.Fatalf("withheld landing %s has activation=%s", destination.ID, destination.Activation)
			}
		default:
			t.Fatalf("landing %s has unknown state=%s", destination.ID, destination.State)
		}
	}
	if active != 8 || withheld != 7 {
		t.Fatalf("active=%d withheld=%d want 8/7", active, withheld)
	}
}

func TestCurrentB2GFirstTouchesUseSpecificDestinations(t *testing.T) {
	tests := []struct {
		service string
		moment  string
		path    string
	}{
		{service: "REAJUSTE_14133", path: "/reequilibrio-obras-publicas/"},
		{service: "REEQUILIBRIO_ECONOMICO", path: "/reequilibrio-obras-publicas/"},
		{service: "glosa", path: "/medicoes-glosas-obras-publicas/"},
		{service: "ORCAMENTO_BDI", path: "/auditoria-orcamento-licitacao/"},
		{service: "APOIO_LICITACAO", path: "/bid-room-licitacoes-obras/"},
		{service: "ADITIVOS", path: "/aditivos-obras-publicas/"},
		{service: "ENCERRAMENTO", path: "/atrasos-prorrogacao-obras-publicas/"},
		{service: "MONITORAMENTO", path: "/acompanhamento-contratos-obras/"},
		{service: "DIAGNOSTICO", moment: "PORTFOLIO_REVIEW", path: "/problemas-que-resolvemos/"},
	}
	for _, tc := range tests {
		t.Run(tc.service+tc.moment, func(t *testing.T) {
			route, err := ResolveCommercialMessageRoute(tc.service, tc.moment, CommercialCampaignFirstTouchEmail)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := url.Parse(route.URL)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Path != tc.path || parsed.Path == "/" {
				t.Fatalf("service=%s moment=%s path=%s want %s", tc.service, tc.moment, parsed.Path, tc.path)
			}
			if route.ClaimKey == "" || route.VisitorSituation == "" || route.Destination.PublicProofExpected == "" {
				t.Fatalf("incomplete message match: %+v", route)
			}
		})
	}
}

func TestWithheldVerticalLandingIsNeverEmitted(t *testing.T) {
	verticals := []CommercialVertical{
		CommercialVerticalPrivateEngineering,
		CommercialVerticalProjectOffices,
		CommercialVerticalCondominiums,
		CommercialVerticalExpertiseLaw,
		CommercialVerticalValuations,
		CommercialVerticalOccupationalSafety,
		CommercialVerticalPublicEntities,
	}
	for _, vertical := range verticals {
		destination, err := CommercialDestinationForVertical(vertical)
		if err != nil {
			t.Fatal(err)
		}
		if destination.Activation != CommercialActivationNotActivated || destination.State != CommercialDestinationWithheldPendingWebRelease {
			t.Fatalf("vertical %s is not withheld: %+v", vertical, destination)
		}
		got, err := CommercialDestinationURL(destination.ID, CommercialCampaignFirstTouchEmail)
		if got != "" || !errors.Is(err, ErrCommercialDestinationWithheld) {
			t.Fatalf("withheld vertical %s emitted %q err=%v", vertical, got, err)
		}
	}
}

func TestCommercialDestinationURLHasFiniteAttributionAndNoPII(t *testing.T) {
	route, err := ResolveCommercialMessageRoute("REAJUSTE", "", CommercialCampaignFirstTouchEmail)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(route.URL)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"utm_source": "warmbly", "utm_medium": "email",
		"utm_campaign": "first_touch_contracts", "utm_content": landingB2GRebalancing,
	}
	if len(parsed.Query()) != len(want) {
		t.Fatalf("query=%v", parsed.Query())
	}
	for key, value := range want {
		if parsed.Query().Get(key) != value {
			t.Fatalf("%s=%q want %q", key, parsed.Query().Get(key), value)
		}
	}
	for _, forbidden := range []string{"nome", "email", "telefone", "cnpj", "contrato", "processo", "mensagem"} {
		if strings.Contains(strings.ToLower(route.URL), forbidden+"=") {
			t.Fatalf("PII/personalization key %s leaked in %s", forbidden, route.URL)
		}
	}

	destination := route.Destination
	for _, unsafe := range []string{
		route.URL + "&email=lead@example.com",
		strings.Replace(route.URL, "first_touch_contracts", "11999999999", 1),
		strings.Replace(route.URL, "warmbly", "cliente-12345678000199", 1),
		strings.Replace(route.URL, "confenge.com.br", "confenge.com.br:444", 1),
	} {
		if err := ValidateCommercialDestinationURL(unsafe, destination); !errors.Is(err, ErrCommercialDestinationUnsafe) {
			t.Fatalf("unsafe URL accepted: %s err=%v", unsafe, err)
		}
	}
}

func TestFirstTouchMessageMatchPreservesLegacyHumanCopy(t *testing.T) {
	account := &models.OutreachAccount{RazaoSocial: "Empresa Alfa", ServiceCode: "REAJUSTE"}
	candidate := &models.OutreachContactCandidate{Email: "contato@empresa.example", MailboxPurpose: "GENERIC_CONTACT"}
	legacy := buildDelegatedRoutingCopyV1(account, candidate, nil)
	current := buildDelegatedRoutingCopy(account, candidate, nil)
	if legacy.Subject != current.Subject || legacy.Opening != current.Opening || legacy.Practice != current.Practice || legacy.CTA != current.CTA {
		t.Fatalf("human copy changed beyond destination line: legacy=%+v current=%+v", legacy, current)
	}
	line := "\nVeja como tratamos esse tipo de situação: " + current.DestinationURL
	if current.DestinationURL == "" || strings.Count(current.Body, current.DestinationURL) != 1 {
		t.Fatalf("current copy does not carry exactly one destination: %q", current.Body)
	}
	if strings.Replace(current.Body, line, "", 1) != legacy.Body {
		t.Fatalf("V2 did not preserve V1 body around the governed destination\nlegacy=%q\ncurrent=%q", legacy.Body, current.Body)
	}
	if current.LandingID != landingB2GRebalancing || current.ClaimKey != "REAJUSTE_OU_REEQUILIBRIO" {
		t.Fatalf("message match=%+v", current)
	}
	entry := DelegatedFirstTouchEntry{
		Subject: current.Subject, BodyText: current.Body, CopyRulesVersion: DelegatedFirstTouchCopyRulesCurrent,
		FactUsed: current.FactUsed, Practice: current.Practice, CTA: current.CTA,
		ClaimKey: current.ClaimKey, LandingID: current.LandingID, DestinationURL: current.DestinationURL,
		SubjectHash: hashText(current.Subject), BodyHash: hashText(current.Body),
		QA: DelegatedFirstTouchQA{Result: "PASS", Attempts: 1, IdentityPassed: true, FactualPassed: true, CopyPassed: true, OperationalPassed: true, Reviewer: DelegatedFirstTouchValidatorV1},
	}
	if blockers := validateDelegatedCopy(entry, account, candidate); len(blockers) != 0 {
		t.Fatalf("governed first touch failed copy quality: %v", blockers)
	}
}

func TestCommercialRoutingIsPureAndReachesNoSendPath(t *testing.T) {
	forbidden := []string{
		"Dispatch", "SendApproved", "QueueTouchpoint", "GreenAutorun",
		"AutoSendEnabled", "ReserveSlot", "CommitSlot", "RecordSent",
		"EnqueueOutcome", "KillSwitch",
	}
	for _, name := range []string{"commercial_destination.go", "delegated_first_touch_copy.go"} {
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, token := range forbidden {
			if strings.Contains(string(body), token) {
				t.Fatalf("%s must not reference send path symbol %q", name, token)
			}
		}
	}

	account := &models.OutreachAccount{RazaoSocial: "Empresa Alfa", ServiceCode: "MEDICOES", CNPJ14: "12345678000199"}
	candidate := &models.OutreachContactCandidate{Email: "pessoa@empresa.example", Phone: "+55 11 99999-9999", MailboxPurpose: "GENERIC_CONTACT"}
	wantAccount, wantCandidate := *account, *candidate
	for range 100 {
		copy := buildDelegatedRoutingCopy(account, candidate, nil)
		if copy.DestinationURL == "" || strings.Contains(copy.DestinationURL, account.CNPJ14) || strings.Contains(copy.DestinationURL, candidate.Email) || strings.Contains(copy.DestinationURL, candidate.Phone) {
			t.Fatalf("unsafe or missing route %q", copy.DestinationURL)
		}
	}
	if !reflect.DeepEqual(*account, wantAccount) || !reflect.DeepEqual(*candidate, wantCandidate) {
		t.Fatal("routing mutated account or candidate")
	}
}
