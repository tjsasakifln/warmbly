package confenge

import (
	"strings"
	"testing"
	"time"

	"github.com/warmbly/warmbly/internal/models"
)

// reviewCohort is the fixture the founder review is judged on: one departmental
// route with a fact, a company name and a mailbox purpose, plus a second
// eligible mailbox on the same account so "why this route" has something to
// compare against.
func reviewCohort(t *testing.T) *FrozenCohortSnapshot {
	t.Helper()
	unk := true
	acc := models.OutreachAccount{
		SourceLeadID: "acc-review", CNPJ14: "11111111000191",
		RazaoSocial: "CONSTRUTORA ALFA LTDA", NomeFantasia: "Construtora Alfa",
		MomentCode: "REAJUSTE_14133", MomentConfidence: "HIGH",
		FactToMention:     "Contrato 12/2023 com reajuste previsto para maio",
		CTA:               "Posso enviar o recorte?",
		TargetFitSendTier: "B_EVIDENCE_SUPPORTED", TargetFitClass: "ICP_CORE",
		TargetFitReasons:  []string{"contrato_ativo", "obra_publica"},
		TargetFitEligible: true, TargetFitFresh: true,
		PriorityTier: "A", PriorityRank: 3, PriorityConfidence: "HIGH",
	}
	snap, err := PrepareControlledCohort([]CohortAccountInput{{
		Account: acc,
		Candidates: []models.OutreachContactCandidate{
			{
				Email: "licitacoes@alfa.com.br", MailboxPurpose: "LICITACOES", OwnershipStatus: "COMPANY_OWNED",
				DiscoveryJSON: eligibleDisc(t, RouteClassRoleOrDepartment, true, controlledDiscovery{PersonUnknown: &unk}),
			},
			{
				Email: "contato@alfa.com.br", MailboxPurpose: "GENERIC_CONTACT", OwnershipStatus: "COMPANY_OWNED",
				DiscoveryJSON: eligibleDisc(t, RouteClassGenericCompany, false, controlledDiscovery{PersonUnknown: &unk}),
			},
		},
		Source: "pncp",
	}}, CohortPrepareOptions{Now: time.Now().UTC(), RepositorySHA: "sha-test", SnapshotHash: "snap-review"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Members) != 1 {
		t.Fatalf("fixture must freeze exactly one route, got %d", len(snap.Members))
	}
	return snap
}

// The founder authorizes a real cold-email cohort from this text. Counts and
// hashes cannot show the wrong company, false personalization or bad copy, so
// every judgeable dimension has to be on the page.
func TestPreviewShowsCopyRouteAndAdmissionForSampledMember(t *testing.T) {
	snap := reviewCohort(t)
	m := snap.Members[0]
	out := FormatCohortPreview(snap)

	for _, want := range []string{
		"Construtora Alfa",              // wrong company is the first thing to catch
		m.Subject,                       // subject verbatim
		"Olá",                           // greeting verbatim, without invented team identity
		"contrato 12/2023 com reajuste", // the observed fact the copy is built on
		"source=" + FactSourceFactToMention,
		// The ask follows the route: a department mailbox is asked whether the
		// frontier is theirs, not asked to forward the mail to a stranger.
		"ou devo procurar outra?",
		// The practice line is read from the lead's own service context.
		"Trabalho com reajuste contratual",
		RouteClassRoleOrDepartment,
		"mailbox_purpose=LICITACOES",
		"why_admitted:",
		"admitted_by=controlled_route_eligible",
		"feed_target_fit_tier=B_EVIDENCE_SUPPORTED class=ICP_CORE eligible=true fresh=true",
		"why_this_route:",
		// The cta field repeats the closing sentence of the body; the note keeps
		// the founder from reading it as a second CTA inside the email.
		"nao e um segundo CTA dentro do email",
		"chosen_route_class=" + RouteClassRoleOrDepartment,
		"preferred_initial=true",
		// The alternative mailbox on the same account, and why it lost.
		"not_chosen=c***@alfa.com.br",
		"reason=lower_selection_score",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("preview missing %q\n---\n%s", want, out)
		}
	}

	// A pasted database row or a raw enum in prose is usually only visible in
	// the whole message, so every sampled member renders its whole body.
	if !strings.Contains(out, "ASSUNTO: "+m.Subject) || !strings.Contains(out, "CORPO:") || !strings.Contains(out, "| Meu nome é Tiago, da CONFENGE.") {
		t.Fatalf("preview must render the whole message for every sample\n---\n%s", out)
	}

	// Reading starts with what the recipient receives; internal fields and the
	// cohort-level telemetry come after it.
	body := strings.Index(out, "ASSUNTO: ")
	meta := strings.Index(out, "metadata interna (nao faz parte do email):")
	tech := strings.Index(out, "cohort_hash=")
	if body < 0 || meta < body || tech < meta {
		t.Fatalf("order must be email, then metadata, then telemetry (body=%d meta=%d tech=%d)\n---\n%s", body, meta, tech, out)
	}
}

// Absence has to look like absence. An empty string reads as "no fact was
// needed here", which is exactly the judgement the founder must not be nudged
// into making by the renderer.
func TestPreviewRendersMissingFieldsAsExplicitAbsence(t *testing.T) {
	// This is a rendering-only fixture. Preparation correctly keeps a lead with
	// neither company nor fact out of a sendable frozen cohort and routes it to
	// enrichment instead.
	snap := &FrozenCohortSnapshot{Members: []FrozenCohortMember{{
		AccountRef: "acc-bare", Mailbox: "contato@bare.com.br", RouteClass: RouteClassGenericCompany,
		FactSource: FactSourceNone,
		AdmissionReasons: []string{
			"feed_target_fit=" + previewAbsent,
			"feed_moment=" + previewAbsent,
			"feed_priority=" + previewAbsent,
		},
	}}}
	if snap.Members[0].ObservedFact != "" || snap.Members[0].FactSource != FactSourceNone {
		t.Fatalf("fixture is supposed to carry no fact: %+v", snap.Members[0])
	}
	out := FormatCohortPreview(snap)
	for _, want := range []string{
		"company=" + previewAbsent,
		"fact=" + previewAbsent,
		"source=" + FactSourceNone,
		"mailbox_purpose=" + previewAbsent,
		"feed_target_fit=" + previewAbsent,
		"feed_moment=" + previewAbsent,
		"feed_priority=" + previewAbsent,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("absent field must render as %q\n---\n%s", want, out)
		}
	}
	// Never a blank that reads as deliberate copy, and never a stand-in that
	// could be mistaken for a real value.
	for _, forbidden := range []string{`fact=""`, "fact=\n", "fact=N/A", "fact=-", "fact=TBD", "fact=example"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("absence rendered as %q instead of a marker\n---\n%s", forbidden, out)
		}
	}
}

// The rendered preview is the text that ends up pasted into aggregated logs, so
// it stays redacted until the founder explicitly asks for the real destination.
func TestPreviewRedactsMailboxUntilExplicitlyRevealed(t *testing.T) {
	snap := reviewCohort(t)
	full := snap.Members[0].Mailbox
	if full != "licitacoes@alfa.com.br" {
		t.Fatalf("fixture mailbox drifted: %q", full)
	}
	redacted := RedactMailbox(full)

	def := FormatCohortPreview(snap)
	if strings.Contains(def, full) {
		t.Fatalf("default preview leaked the real address\n---\n%s", def)
	}
	if !strings.Contains(def, redacted) {
		t.Fatalf("default preview must show the redacted address\n---\n%s", def)
	}

	shown := FormatCohortPreviewWithOptions(snap, CohortPreviewOptions{ShowMailbox: true})
	if !strings.Contains(shown, full) {
		t.Fatalf("--show-mailbox must reveal the real destination\n---\n%s", shown)
	}
	// The alternative mailboxes are never sent to, so revealing the chosen one
	// must not widen exposure of the ones that lost.
	if strings.Contains(shown, "contato@alfa.com.br") {
		t.Fatalf("rejected routes must stay redacted even when revealing\n---\n%s", shown)
	}
}

// The sample is a judgement aid, not a per-message approval queue: it must stay
// bounded and spread across route classes rather than dumping the whole cohort.
func TestPreviewSampleIsBoundedPerRouteClass(t *testing.T) {
	unk := true
	var in []CohortAccountInput
	for i := 0; i < 6; i++ {
		ref := string(rune('a' + i))
		in = append(in, CohortAccountInput{
			Account: cohortAccount("acc-"+ref, "1111111100019"+ref, "Aditivo publicado no PNCP"),
			Candidates: []models.OutreachContactCandidate{{
				Email: "contato@" + ref + ".com.br", MailboxPurpose: "GENERIC_CONTACT", OwnershipStatus: "COMPANY_OWNED",
				DiscoveryJSON: eligibleDisc(t, RouteClassGenericCompany, false, controlledDiscovery{PersonUnknown: &unk}),
			}},
		})
	}
	snap, err := PrepareControlledCohort(in, CohortPrepareOptions{Now: time.Now().UTC(), RepositorySHA: "sha-test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Members) != 6 {
		t.Fatalf("fixture must freeze six routes, got %d", len(snap.Members))
	}
	def := FormatCohortPreview(snap)
	if n := strings.Count(def, "why_this_route:"); n != DefaultPreviewSamplesPerClass {
		t.Fatalf("default sample must stay at %d per class, got %d\n---\n%s", DefaultPreviewSamplesPerClass, n, def)
	}
	wide := FormatCohortPreviewWithOptions(snap, CohortPreviewOptions{SamplesPerClass: 4, FullBodies: 1})
	if n := strings.Count(wide, "why_this_route:"); n != 4 {
		t.Fatalf("--samples must widen the sample, got %d\n---\n%s", n, wide)
	}
	// --bodies no longer bounds anything: every sampled member is rendered whole,
	// because a founder cannot judge copy from an excerpt.
	if n := strings.Count(wide, "CORPO:"); n != 4 {
		t.Fatalf("every sampled member must render its body, got %d\n---\n%s", n, wide)
	}
}
