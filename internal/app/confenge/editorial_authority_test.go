package confenge

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

// The production v3 cohort the founder opened in the Control Center. It froze
// with copy_qa=passed under its own gate, which is precisely why authority has
// to be decided from the composer stamp and not from the stored verdict.
const legacyProdComposer = "confenge.composer.v3"

func legacyMember() FrozenCohortMember {
	return FrozenCohortMember{
		AccountID: uuid.New(), CandidateID: uuid.New(),
		AccountRef: "cnpj:00507949000182", Company: "Jatobeton Engenharia",
		Mailbox: "jatobeton@jatobeton.test", RouteClass: "GENERIC_COMPANY",
		ComposerVersion: legacyProdComposer,
		Subject:         "contratação pública: contratação DE Empresa Especializada Para",
		BodyText: "Olá, equipe,\n\nSou da CONFENGE.\n\ncontratação pública: contratação DE Empresa " +
			"Especializada Para Execução DOS Serviços Necessários DOS Serviços Necessários À " +
			"Recuperação Estrutural DA Ponte SOB O RIO Sapucaí,.\n\nQueria falar com quem acompanha " +
			"a carteira de contratos públicos por aí. Você consegue me indicar a pessoa responsável?",
		AdmissionReasons: []string{"copy_qa=passed"},
	}
}

func currentMember() FrozenCohortMember {
	m := legacyMember()
	m.ComposerVersion = ComposerVersion
	return m
}

func snapshotOf(m FrozenCohortMember, composer string) FrozenCohortSnapshot {
	return FrozenCohortSnapshot{
		ComposerVersion: composer, PolicyVersion: BoundedCohortPolicyV1,
		Members: []FrozenCohortMember{m},
	}
}

func TestSupersededComposerIsNeverActionable(t *testing.T) {
	for _, stamped := range []string{legacyProdComposer, "confenge.composer.v4", "", "  "} {
		a := EvaluateCohortEditorialAuthority(stamped, BoundedCohortPolicyV1)
		if a.Actionable {
			t.Fatalf("composer %q must not be actionable", stamped)
		}
		if a.State != EditorialStateSuperseded {
			t.Fatalf("composer %q must read %s, got %s", stamped, EditorialStateSuperseded, a.State)
		}
		if len(a.ReasonCodes) == 0 || a.Notice == "" {
			t.Fatalf("composer %q must carry a reason code and a notice: %+v", stamped, a)
		}
	}
	a := EvaluateCohortEditorialAuthority(ComposerVersion, BoundedCohortPolicyV1)
	if !a.Actionable || a.State != EditorialStateCurrent || len(a.ReasonCodes) != 0 {
		t.Fatalf("current composer must stay actionable: %+v", a)
	}
	if a.Notice != "" {
		t.Fatalf("current copy must carry no legacy notice: %q", a.Notice)
	}
}

func TestSupersededPolicyIsNeverActionable(t *testing.T) {
	a := EvaluateCohortEditorialAuthority(ComposerVersion, "bounded-cohort-policy.v0")
	if a.Actionable || !slices.Contains(a.ReasonCodes, ReasonPolicySuperseded) {
		t.Fatalf("an old policy must fail closed: %+v", a)
	}
}

// A current snapshot header must not launder a member an old composer wrote.
func TestSnapshotIsOnlyCurrentWhenEveryMemberIs(t *testing.T) {
	snap := snapshotOf(currentMember(), ComposerVersion)
	if a := SnapshotEditorialAuthority(&snap); !a.Actionable {
		t.Fatalf("all-current snapshot must be actionable: %+v", a)
	}
	mixed := FrozenCohortSnapshot{
		ComposerVersion: ComposerVersion, PolicyVersion: BoundedCohortPolicyV1,
		Members: []FrozenCohortMember{currentMember(), legacyMember()},
	}
	a := SnapshotEditorialAuthority(&mixed)
	if a.Actionable || !slices.Contains(a.ReasonCodes, ReasonMemberComposerSuperseded) {
		t.Fatalf("one legacy member must make the whole snapshot history: %+v", a)
	}
	if a := SnapshotEditorialAuthority(nil); a.Actionable {
		t.Fatal("a nil snapshot must fail closed")
	}
}

// GO mints send authority. It is the decision that must never reach history.
func TestGOIsRefusedOnLegacyAndReachedOnCurrent(t *testing.T) {
	approved := &HumanGateReview{Decision: "APPROVE", Effective: true}
	valid := &HumanGateValidation{Status: "VALID"}

	legacy := &HumanGateCohort{
		Freshness: "FRESH", Manifest: snapshotOf(legacyMember(), legacyProdComposer),
		Candidates: []HumanGateCandidate{{
			FrozenCohortMember: legacyMember(), Validation: valid, Review: approved,
		}},
	}
	blocked := humanGateDecisionBlockers(legacy)
	if !slices.Contains(blocked, ReasonComposerSuperseded) {
		t.Fatalf("GO on legacy copy must be blocked by %s: %v", ReasonComposerSuperseded, blocked)
	}

	m := currentMember()
	current := &HumanGateCohort{
		Freshness: "FRESH", Manifest: snapshotOf(m, ComposerVersion),
		Candidates: []HumanGateCandidate{{
			FrozenCohortMember: m, Validation: valid, Review: approved,
		}},
	}
	if blocked := humanGateDecisionBlockers(current); len(blocked) != 0 {
		t.Fatalf("a valid current version must be able to reach GO: %v", blocked)
	}
}

// A recomposition is a new decision surface, never an inherited one.
func TestRecomposeMovesToCurrentComposerAndInheritsNoAuthority(t *testing.T) {
	parent := snapshotOf(legacyMember(), legacyProdComposer)
	parent.AuthorizationID = uuid.New()

	next, report, err := RecomposeManifest(&parent)
	if err != nil {
		t.Fatalf("recompose: %v", err)
	}
	if report.ComposerBefore != legacyProdComposer || report.ComposerAfter != ComposerVersion {
		t.Fatalf("report must name both composers: %+v", report)
	}
	if next.ComposerVersion != ComposerVersion {
		t.Fatalf("recomposed snapshot must carry the current composer, got %q", next.ComposerVersion)
	}
	if report.KeptMembers+report.ExcludedMembers != report.ParentMembers {
		t.Fatalf("recompose accounting must reconcile: %+v", report)
	}
	// The parent is preserved byte-identical: history is evidence.
	if parent.ComposerVersion != legacyProdComposer || parent.Members[0].BodyText != legacyMember().BodyText {
		t.Fatal("recompose must never mutate the parent snapshot")
	}
	for _, m := range next.Members {
		if m.ComposerVersion != ComposerVersion {
			t.Fatalf("kept member kept a stale composer stamp: %q", m.ComposerVersion)
		}
		if m.ContentHash == legacyMember().ContentHash && m.BodyText == legacyMember().BodyText {
			t.Fatal("recomposed copy must differ from the copy it replaces")
		}
	}
	if a := SnapshotEditorialAuthority(next); !a.Actionable {
		t.Fatalf("a recomposed snapshot must be operable again: %+v", a)
	}
}

// Superseded copy stays fully readable. Auditing history is the whole point of
// keeping it, so nothing here may blank the text.
func TestSupersededVersionStaysReadableForAudit(t *testing.T) {
	m := legacyMember()
	snap := snapshotOf(m, legacyProdComposer)
	a := SnapshotEditorialAuthority(&snap)
	if a.Actionable {
		t.Fatal("legacy must not be actionable")
	}
	if snap.Members[0].Subject == "" || snap.Members[0].BodyText == "" {
		t.Fatal("legacy copy must remain readable")
	}
	if !strings.Contains(a.Blocker("APPROVE"), legacyProdComposer) {
		t.Fatalf("the refusal must name the composer that wrote the text: %q", a.Blocker("APPROVE"))
	}
	if !strings.Contains(a.Blocker("APPROVE"), ComposerVersion) {
		t.Fatalf("the refusal must name the current composer: %q", a.Blocker("APPROVE"))
	}
}

// The blocker text must name the attempted action, so an audit trail records
// what was tried and not merely that something was refused.
func TestBlockerNamesTheAttemptedAction(t *testing.T) {
	a := EvaluateCohortEditorialAuthority(legacyProdComposer, BoundedCohortPolicyV1)
	for _, action := range []string{"APPROVE", "GO", "QUEUE", "TRANSPORT"} {
		if !strings.HasPrefix(a.Blocker(action), action+" refused") {
			t.Fatalf("blocker must name %s: %q", action, a.Blocker(action))
		}
	}
	if EvaluateCohortEditorialAuthority(ComposerVersion, BoundedCohortPolicyV1).Blocker("APPROVE") != "" {
		t.Fatal("current copy must produce no blocker")
	}
}

// Drafts and touchpoints are stamped by prompt version, not composer version.
func TestDraftAuthorityReadsThePromptStamp(t *testing.T) {
	if a := EvaluateDraftEditorialAuthority(PromptVersion); !a.Actionable {
		t.Fatalf("current prompt must be actionable: %+v", a)
	}
	if a := EvaluateDraftEditorialAuthority(PromptVersion + "+touch"); !a.Actionable {
		t.Fatalf("a touch suffix is the same composer: %+v", a)
	}
	for _, stale := range []string{"confenge.draft.v3", "confenge.draft.v4+touch", ""} {
		if a := EvaluateDraftEditorialAuthority(stale); a.Actionable {
			t.Fatalf("prompt %q must fail closed: %+v", stale, a)
		}
	}
}

// Transport is the last gate. A grant and its copy can agree with each other
// and still both be the work of a composer this build no longer ships.
func TestTransportRefusesAGrantMintedByAnOldComposer(t *testing.T) {
	actor := uuid.New()
	authID := uuid.New()
	tp := &models.OutreachTouchpoint{
		AuthorizationMode: AuthorizationModeBoundedCohort,
		State:             models.TouchpointApproved,
		ApprovedBy:        &actor, CampaignPolicyAuthorizationID: &authID,
		ContentHash: "h", ApprovedContentHash: "h",
	}
	auth := &BoundedCohortAuthorization{
		ID: authID, ComposerVersion: legacyProdComposer, PolicyVersion: BoundedCohortPolicyV1,
	}
	err := CanTransportCohort(tp, auth, CohortTransportInput{})
	if err == nil {
		t.Fatal("transport must refuse a legacy-composer grant")
	}
	if !strings.Contains(err.Error(), ReasonComposerSuperseded) &&
		!strings.Contains(err.Error(), "does not match") &&
		!strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("unexpected refusal: %v", err)
	}
}

// A recomposition has to stay repeatable. The member keeps the record it was
// written from, so the next composer bump reads the record again instead of
// re-digesting the sentence the last composer wrote and throwing the lead away.
func TestRecomposeIsRepeatableBecauseTheRecordIsPreserved(t *testing.T) {
	t.Setenv(EnvSenderName, "Tiago Sasaki")
	const record = "objeto: recuperação estrutural da ponte sobre o Rio Sapucaí; órgão: DNIT; UF MG"

	m := legacyMember()
	m.ObservedFact = record
	m.SourceFact = ""
	parent := snapshotOf(m, legacyProdComposer)

	first, rep1, err := RecomposeManifest(&parent)
	if err != nil {
		t.Fatalf("first recompose: %v", err)
	}
	if rep1.KeptMembers != 1 {
		t.Fatalf("first recompose kept %d members, want 1: %v", rep1.KeptMembers, rep1.ByReasonCode)
	}
	if got := first.Members[0].SourceFact; got != record {
		t.Fatalf("the record was not carried forward: %q", got)
	}
	if first.Members[0].ObservedFact == record {
		t.Fatal("observed_fact must be the written sentence, not the raw record")
	}

	// Stamp it legacy again, exactly as a composer bump would leave it.
	second := *first
	second.ComposerVersion = legacyProdComposer
	second.Members = append([]FrozenCohortMember(nil), first.Members...)
	second.Members[0].ComposerVersion = legacyProdComposer

	next, rep2, err := RecomposeManifest(&second)
	if err != nil {
		t.Fatalf("second recompose: %v", err)
	}
	if rep2.KeptMembers != 1 {
		t.Fatalf("second recompose kept %d members, want 1: %v", rep2.KeptMembers, rep2.ByReasonCode)
	}
	if next.Members[0].SourceFact != record {
		t.Fatalf("the record was lost on the second pass: %q", next.Members[0].SourceFact)
	}
	if next.Members[0].Subject != first.Members[0].Subject {
		t.Fatalf("recomposing the same record twice must be stable: %q vs %q",
			first.Members[0].Subject, next.Members[0].Subject)
	}
}
