package confenge

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

// Freeze-time QA must reject what the send-time hard QA rejects. When they
// disagree a cohort freezes clean and then fails for every member at dispatch.
func TestFreezeQARejectsWhatSendQARejects(t *testing.T) {
	dump := "objeto: Obra de pavimentação asfáltica.; órgão: Prefeitura Municipal DE Brochier; UF RS; R$ 1.398.000."
	body := "Olá, equipe,\n\nSou da CONFENGE.\n\n" + dump + "\n\nVocê consegue me indicar a pessoa responsável?"

	errs := ValidateCopyForRouteClass(RouteClassGenericCompany, body, "objeto: Obra de pavimentação asfáltica", nil)
	if len(errs) == 0 {
		t.Fatal("freeze QA accepted a serialized feed record that send-time QA rejects")
	}
	joined := strings.Join(errs, ",")
	for _, want := range []string{"metadata_dump", "subject_dump_label"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("errs = %q, want %s", joined, want)
		}
	}
}

// The composer must not ship a serialized record as prose.
func TestComposerDropsMetadataDumpFromBody(t *testing.T) {
	unk := true
	acc := cohortAccount("acc-dump", "33333333000193", "objeto: Obra de pavimentação asfáltica.; órgão: Prefeitura Municipal DE Brochier; UF RS; R$ 1.398.000.")
	acc.MomentCode = "PORTFOLIO_REVIEW"
	cand := &models.OutreachContactCandidate{
		Email:           "contato@empresa.com.br",
		MailboxPurpose:  "GENERIC_CONTACT",
		OwnershipStatus: "COMPANY_OWNED",
		DiscoveryJSON:   eligibleDisc(t, RouteClassGenericCompany, true, controlledDiscovery{PersonUnknown: &unk}),
	}

	subject, body, _ := ComposeControlledInitial(&acc, cand, RouteClassGenericCompany)

	if containsDumpLabel(body) || looksLikeMetadataDump(body) {
		t.Fatalf("body still carries a record dump:\n%s", body)
	}
	if containsDumpLabel(subject) {
		t.Fatalf("subject still carries a dump label: %q", subject)
	}
	if strings.Contains(strings.ToLower(body), "portfolio review") {
		t.Fatalf("English moment enum leaked into pt-BR copy:\n%s", body)
	}
	if errs := ValidateCopyForRouteClass(RouteClassGenericCompany, body, subject, cand); len(errs) != 0 {
		t.Fatalf("composed copy fails its own freeze QA: %v\n%s", errs, body)
	}
}

// An unmapped moment code must never reach copy as a lowercased enum.
func TestUnmappedMomentCodeDoesNotReachCopy(t *testing.T) {
	if got := humanizeMoment("SOME_NEW_UNMAPPED_CODE"); got != "" {
		t.Fatalf("humanizeMoment = %q, want empty so it falls through", got)
	}
	if got := humanizeMoment("PORTFOLIO_REVIEW"); got == "portfolio review" || got == "" {
		t.Fatalf("PORTFOLIO_REVIEW = %q, want a pt-BR label", got)
	}
}

// A declarative CTA must not be turned into a pseudo-question by punctuation.
func TestDeclarativeCTAGetsARealQuestion(t *testing.T) {
	got := ensureClosingAsk("Disponibilizo apoio de pico em análise de edital/proposta sob NDA.")
	if strings.HasSuffix(got, "NDA?") {
		t.Fatalf("declarative was coerced into a question: %q", got)
	}
	if !strings.HasSuffix(got, "?") {
		t.Fatalf("copy must still end in an ask: %q", got)
	}
}

// A classified institutional route enrolls without nominal verification, but
// suppression and provenance guards still reject.
func TestControlledRouteEnrollableWithoutNominalVerification(t *testing.T) {
	unk := true
	base := func() *models.OutreachContactCandidate {
		return &models.OutreachContactCandidate{
			ID:                 uuid.New(),
			Email:              "contato@empresa.com.br",
			VerificationStatus: models.OutreachVerifyCandidateUnverified,
			MailboxPurpose:     "GENERIC_CONTACT",
			OwnershipStatus:    "COMPANY_OWNED",
			DiscoveryJSON:      eligibleDisc(t, RouteClassGenericCompany, true, controlledDiscovery{PersonUnknown: &unk}),
		}
	}

	c := base()
	if c.CanEnroll() {
		t.Fatal("CANDIDATE_UNVERIFIED must still fail the legacy nominal rule")
	}
	if !CandidateEnrollable(c) {
		t.Fatal("a classified institutional route must be enrollable")
	}

	sup := base()
	sup.DoNotContact = true
	if CandidateEnrollable(sup) {
		t.Fatal("suppression must still block a controlled route")
	}

	taint := base()
	taint.BlockReason = "provenance_taint:fixture"
	if CandidateEnrollable(taint) {
		t.Fatal("provenance taint must still block a controlled route")
	}

	plain := base()
	plain.DiscoveryJSON = nil
	if CandidateEnrollable(plain) {
		t.Fatal("without a classified route the strict verification rule must hold")
	}
}
