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
	remaining int
	calls     atomic.Int32
}

func (s *delegatedFirstTouchProcessorStub) ProcessDelegatedFirstTouchOnce(context.Context) (bool, error) {
	s.calls.Add(1)
	if s.remaining == 0 {
		return false, nil
	}
	s.remaining--
	return true, errors.New("held item must not stop the remaining burst")
}

func TestDelegatedFirstTouchWorkerDrainsHoldsUntilQueuedOrIdle(t *testing.T) {
	processor := &delegatedFirstTouchProcessorStub{remaining: 3}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		NewDelegatedFirstTouchWorker(processor, time.Hour).Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for processor.calls.Load() < 4 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	if processor.calls.Load() != 4 {
		t.Fatalf("worker calls=%d want 4", processor.calls.Load())
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
