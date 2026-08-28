package confenge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
)

type delegatedFirstTouchProcessorStub struct {
	calls atomic.Int32
}

type delegatedFirstTouchAlwaysProcesses struct{ calls atomic.Int32 }

func (s *delegatedFirstTouchAlwaysProcesses) ProcessDelegatedFirstTouchOnce(context.Context) (bool, error) {
	s.calls.Add(1)
	return true, nil
}

func (s *delegatedFirstTouchProcessorStub) ProcessDelegatedFirstTouchOnce(context.Context) (bool, error) {
	s.calls.Add(1)
	return false, errors.New("capacity or policy blocker")
}

func TestDelegatedFirstTouchWorkerSleepsWhenProcessorReportsBlocker(t *testing.T) {
	processor := &delegatedFirstTouchProcessorStub{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		NewDelegatedFirstTouchWorker(processor, time.Hour).Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for processor.calls.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(10 * time.Millisecond)
	cancel()
	<-done
	if processor.calls.Load() != 1 {
		t.Fatalf("worker calls=%d want one attempt before sleep", processor.calls.Load())
	}
}

func TestDelegatedFirstTouchWorkerSleepsAfterBoundedBurst(t *testing.T) {
	processor := &delegatedFirstTouchAlwaysProcesses{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		NewDelegatedFirstTouchWorker(processor, time.Hour).Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for processor.calls.Load() < delegatedFirstTouchMaxBurst && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(10 * time.Millisecond)
	cancel()
	<-done
	if processor.calls.Load() != delegatedFirstTouchMaxBurst {
		t.Fatalf("worker calls=%d want bounded burst=%d", processor.calls.Load(), delegatedFirstTouchMaxBurst)
	}
}

func TestComposeDelegatedRoutingCopyVariesOnlyWithEvidence(t *testing.T) {
	acc := &models.OutreachAccount{ID: uuid.MustParse("21982eb9-878a-4c67-8a80-25ff6ca4d1f7"), NomeFantasia: "Empresa Alfa", ServiceCode: "MONITORAMENTO_CONTRATUAL"}
	cand := &models.OutreachContactCandidate{
		Name: "Contato", Role: "Atendimento", MailboxPurpose: "GENERIC_CONTACT",
		DiscoveryJSON: []byte(`{"route_class":"GENERIC_COMPANY","person_unknown":true}`),
	}
	subject, first := composeDelegatedRoutingCopy(acc, cand, nil)
	if subject == "" || first == "" || !strings.Contains(first, "Empresa Alfa") {
		t.Fatalf("incomplete first copy: subject=%q body=%q", subject, first)
	}
	secondSubject, second := composeDelegatedRoutingCopy(acc, cand, nil)
	if secondSubject != subject || second != first {
		t.Fatal("identical evidence produced cosmetic variation")
	}
	if !strings.Contains(first, delegatedContactExit) {
		t.Fatal("copy omitted the natural contact exit")
	}
}

func TestComposeDelegatedRoutingCopyUsesOnlyBoundFactsAndRouteIdentity(t *testing.T) {
	factID := "fact-pavimentacao"
	acc := &models.OutreachAccount{
		ID: uuid.New(), NomeFantasia: "Empresa Alfa", ServiceCode: "auditoria_orcamento_bdi",
		FactToMention:     "Contratação de empresa para pavimentação asfáltica na Avenida Ipê",
		MomentEvidenceIDs: []string{factID}, ContractorRoleEvidenceIDs: []string{"role-alfa"},
	}
	evidence := []models.OutreachEvidence{{
		SourceEvidenceID: factID, EpistemicClass: models.OutreachEpistemicConfirmedFact,
		Synthesis: "Contratação de empresa para pavimentação asfáltica na Avenida Ipê",
	}}
	tests := []struct {
		name, route, purpose, person, wantCTA string
		wantName                              bool
	}{
		{"direct", RouteClassDirectPerson, "PERSONAL_WORK", "Ana Souza", "Essa frente passa por você?", true},
		{"direct_without_identity", RouteClassDirectPerson, "PERSONAL_WORK", "Contato", "Essa frente passa por você?", false},
		{"role", RouteClassRoleOrDepartment, "LICITACOES", "Equipe", "Essa frente fica com a área de licitações ou devo procurar outra?", false},
		{"generic", RouteClassGenericCompany, "GENERIC_CONTACT", "Contato", "Você consegue encaminhar esta mensagem a quem cuida dessa frente?", false},
		{"freemail", RouteClassPublicCompanyFreemail, "GENERIC_CONTACT", "Contato", "Você consegue encaminhar esta mensagem a quem cuida dessa frente?", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			personUnknown := tc.route != RouteClassDirectPerson
			cand := &models.OutreachContactCandidate{
				Name: tc.person, Role: "Atendimento", MailboxPurpose: tc.purpose,
				Email: "rota@empresa.example", VerificationStatus: models.OutreachVerifyOfficialSource,
				DiscoveryJSON: []byte(fmt.Sprintf(`{"route_class":%q,"person_unknown":%t}`, tc.route, personUnknown)),
			}
			copy := buildDelegatedRoutingCopy(acc, cand, evidence)
			if !strings.Contains(normalizeForCorpus(copy.Body), "pavimentacao asfaltica na avenida ipe") || len(copy.FactEvidenceIDs) != 1 {
				t.Fatalf("supported fact was not projected: %+v", copy)
			}
			if copy.CTA != tc.wantCTA {
				t.Fatalf("cta=%q want %q", copy.CTA, tc.wantCTA)
			}
			hasName := strings.HasPrefix(copy.Body, "Olá, Ana,")
			if hasName != tc.wantName {
				t.Fatalf("identity use=%v want %v body=%q", hasName, tc.wantName, copy.Body)
			}
		})
	}

	unsupported := buildDelegatedRoutingCopy(acc, testsCandidate(RouteClassGenericCompany), []models.OutreachEvidence{{
		SourceEvidenceID: factID, EpistemicClass: models.OutreachEpistemicStrongInference,
		Synthesis: "Possível pavimentação asfáltica na Avenida Ipê",
	}})
	if len(unsupported.FactEvidenceIDs) != 0 || !strings.Contains(unsupported.Body, "aparece como contratada no setor público") {
		t.Fatalf("unsupported detail did not fall back to simple copy: %+v", unsupported)
	}
}

func testsCandidate(route string) *models.OutreachContactCandidate {
	return &models.OutreachContactCandidate{
		Name: "Contato", Role: "Atendimento", Email: "contato@empresa.example", MailboxPurpose: "GENERIC_CONTACT",
		VerificationStatus: models.OutreachVerifyOfficialSource,
		DiscoveryJSON:      []byte(fmt.Sprintf(`{"route_class":%q,"person_unknown":true}`, route)),
	}
}

func TestDelegatedFirstTouchAutorunRequiresNarrowGateAndOperatorBinding(t *testing.T) {
	cfg := Config{Enabled: true, RequireHumanApproval: true, DelegatedFirstTouchAutorunEnabled: true,
		DefaultDailyLimit: 50, MaxInitialEmailWords: 120}
	if err := cfg.ValidateStartup("test"); err == nil || !strings.Contains(err.Error(), EnvDelegatedFirstTouch) {
		t.Fatalf("autorun without delegated gate: %v", err)
	}
	cfg.DelegatedFirstTouchEnabled = true
	if err := cfg.ValidateStartup("test"); err == nil || !strings.Contains(err.Error(), "operator") {
		t.Fatalf("autorun without operator binding: %v", err)
	}
	cfg.OperatorUserID, cfg.OperatorOrgID = uuid.New(), uuid.New()
	if err := cfg.ValidateStartup("test"); err == nil || !strings.Contains(err.Error(), EnvDelegatedFirstTouchRunwayDays) {
		t.Fatalf("autorun without capacity runway: %v", err)
	}
	cfg.DelegatedFirstTouchRunwayDays = 30
	if err := cfg.ValidateStartup("test"); err != nil {
		t.Fatalf("valid delegated autorun config: %v", err)
	}
}

func TestDelegatedEntryUsesSourceObservationDateNotImportTimestamp(t *testing.T) {
	sourceDate := time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)
	importedAt := time.Date(2026, 8, 25, 20, 52, 0, 0, time.UTC)
	acc := &models.OutreachAccount{
		ID: uuid.New(), CNPJ14: "12345678000190", SourceRunID: "run-current",
	}
	cand := &models.OutreachContactCandidate{
		ID: uuid.New(), AccountID: acc.ID, SourceURL: "https://empresa.example/contato",
		SourceDate: &sourceDate, UpdatedAt: importedAt,
	}

	entry := delegatedEntryFromCurrentState(acc, cand, uuid.New(), delegatedRoutingCopy{Subject: "Assunto", Body: "Corpo"})
	if got := entry.WebSources[0].ObservedAt; !got.Equal(sourceDate) {
		t.Fatalf("web observation=%s want source date %s; import timestamp must not refresh evidence", got, sourceDate)
	}

	cand.SourceDate = nil
	entry = delegatedEntryFromCurrentState(acc, cand, uuid.New(), delegatedRoutingCopy{Subject: "Assunto", Body: "Corpo"})
	if got := entry.WebSources[0].ObservedAt; !got.IsZero() {
		t.Fatalf("missing source date must fail closed, got observation %s", got)
	}
}

func TestDelegatedEntryUsesOfficialRegistryWhenSourceURLEmpty(t *testing.T) {
	sourceDate := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	acc := &models.OutreachAccount{ID: uuid.New(), CNPJ14: "12345678000190", SourceRunID: "run-current"}
	cand := &models.OutreachContactCandidate{
		ID: uuid.New(), AccountID: acc.ID, SourceURL: "",
		SourceDate: &sourceDate, VerificationStatus: models.OutreachVerifyOfficialSource,
		ChannelEpistemic: "OBSERVED", OwnershipStatus: "COMPANY_OWNED", RouteFreshness: "FRESH",
		DiscoveryJSON: []byte(`{"source":"company_registry","source_type":"company_registry","route_class":"GENERIC_COMPANY","controlled_email_eligible":true,"preferred_initial":true}`),
	}
	entry := delegatedEntryFromCurrentState(acc, cand, uuid.New(), delegatedRoutingCopy{Subject: "Assunto", Body: "Corpo"})
	if len(entry.WebSources) != 1 || entry.WebSources[0].Kind != DelegatedWebSourceKindOfficialRegistry || entry.WebSources[0].URL != "" {
		t.Fatalf("registry entry must not invent a URL: %+v", entry.WebSources)
	}
	now := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	if !delegatedWebSourceAllowed(entry.WebSources[0], now) {
		t.Fatal("official registry source must be allowed without HTTP URL")
	}
	if !candidateSourceCorroborated(cand, entry.WebSources) {
		t.Fatal("official registry mailbox association must corroborate without inventing a URL")
	}
}

func TestDelegatedWebSourceAllowedKeepsHTTPGateForPublicPages(t *testing.T) {
	now := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	observed := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	if delegatedWebSourceAllowed(DelegatedWebSource{URL: "", Kind: "PUBLIC_COMPANY_SOURCE", Supports: "COMPANY_MAILBOX", ObservedAt: observed}, now) {
		t.Fatal("public company source without URL must stay invalid")
	}
	if delegatedWebSourceAllowed(DelegatedWebSource{URL: "https://empresa.example/contato", Kind: DelegatedWebSourceKindOfficialRegistry, Supports: "COMPANY_MAILBOX", ObservedAt: observed}, now) {
		t.Fatal("registry kind must not carry an invented URL")
	}
	if !delegatedWebSourceAllowed(DelegatedWebSource{URL: "https://empresa.example/contato", Kind: "PUBLIC_COMPANY_SOURCE", Supports: "COMPANY_MAILBOX", ObservedAt: observed}, now) {
		t.Fatal("http(s) public company source must remain allowed")
	}
}
