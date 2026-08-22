package confenge

import (
	"strings"
	"testing"

	"github.com/warmbly/warmbly/internal/models"
)

func roleCandidate(t *testing.T, mailbox, purpose string) *models.OutreachContactCandidate {
	t.Helper()
	unk := true
	return &models.OutreachContactCandidate{
		Email:           mailbox,
		MailboxPurpose:  purpose,
		OwnershipStatus: "COMPANY_OWNED",
		DiscoveryJSON:   eligibleDisc(t, RouteClassRoleOrDepartment, true, controlledDiscovery{PersonUnknown: &unk}),
	}
}

// A department mailbox proves the door, not the person. When no person is
// proven the initial touch must still ask to be routed, instead of assuming
// the reader owns the subject.
func TestRoleRouteWithUnknownPersonAsksForReferral(t *testing.T) {
	acc := cohortAccount("acc-role", "22222222000192", "Edital publicado")
	acc.CTA = "Disponibilizo apoio de pico em análise de edital/proposta sob NDA?"
	cand := roleCandidate(t, "comercial@empresa.com.br", "COMERCIAL")

	_, body, greeting := ComposeControlledInitial(&acc, cand, RouteClassRoleOrDepartment)

	if !strings.Contains(body, referralAsk) {
		t.Fatalf("role body must carry the referral ask, got:\n%s", body)
	}
	if !strings.Contains(body, "apoio de pico") {
		t.Fatalf("referral ask must not replace the offer, got:\n%s", body)
	}
	if !strings.HasPrefix(greeting, "Olá, equipe") {
		t.Fatalf("greeting = %q, want a team greeting", greeting)
	}
}

// The ask is appended once, never stacked on copy that already routes.
func TestReferralAskIsNotDuplicated(t *testing.T) {
	acc := cohortAccount("acc-role2", "22222222000193", "Contrato vigente")
	acc.CTA = "Pode me indicar a pessoa certa de contratos?"
	cand := roleCandidate(t, "licitacoes@empresa.com.br", "LICITACOES")

	_, body, _ := ComposeControlledInitial(&acc, cand, RouteClassRoleOrDepartment)

	if n := strings.Count(body, "indicar a pessoa"); n != 1 {
		t.Fatalf("referral ask appears %d times, want 1:\n%s", n, body)
	}
}

// Generic and freemail routes keep their existing routing CTA.
func TestGenericRouteStillUsesRoutingCTA(t *testing.T) {
	unk := true
	acc := cohortAccount("acc-generic", "33333333000193", "Contrato vigente")
	cand := &models.OutreachContactCandidate{
		Email:           "contato@empresa.com.br",
		MailboxPurpose:  "GENERIC_CONTACT",
		OwnershipStatus: "COMPANY_OWNED",
		DiscoveryJSON:   eligibleDisc(t, RouteClassGenericCompany, true, controlledDiscovery{PersonUnknown: &unk}),
	}
	_, body, _ := ComposeControlledInitial(&acc, cand, RouteClassGenericCompany)
	if !strings.Contains(body, "me indicar a pessoa responsável") {
		t.Fatalf("generic body lost its routing CTA:\n%s", body)
	}
}
