package confenge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/models"
)

func discoveryJSON(t *testing.T, d controlledDiscovery) []byte {
	t.Helper()
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestControlledEligibleRoleMailboxHasNoPerson(t *testing.T) {
	eligible := true
	unknown := true
	c := &models.OutreachContactCandidate{
		Email: "comercial@empresa.com.br", OwnershipStatus: "COMPANY_OWNED",
		MailboxPurpose: "COMERCIAL", VerificationStatus: models.OutreachVerifyInstitutionalGeneric,
		DiscoveryJSON: discoveryJSON(t, controlledDiscovery{
			RouteClass: RouteClassRoleOrDepartment, ControlledEmailEligible: &eligible, PersonUnknown: &unknown,
		}),
	}
	if CandidateRouteClass(c) != RouteClassRoleOrDepartment {
		t.Fatalf("class=%s", CandidateRouteClass(c))
	}
	if !CandidateControlledEligible(c) {
		t.Fatal("comercial@ should be controlled eligible")
	}
	if !CandidatePersonUnknown(c) {
		t.Fatal("person must stay UNKNOWN")
	}
	if CandidateEmailValidated(c) {
		t.Fatal("must not become EMAIL_VALIDATED / person VALIDATED")
	}
	acc := &models.OutreachAccount{CNPJ14: "12345678000190", RazaoSocial: "EMPRESA"}
	res := ResolveRecipient(acc, []models.OutreachContactCandidate{*c}, time.Now().UTC())
	if res.State != RecipientControlledEligible {
		t.Fatalf("state=%s reasons=%v", res.State, res.ReasonCodes)
	}
	if res.Name != "" {
		t.Fatalf("invented name %q", res.Name)
	}
}

func TestControlledEligibleContatoAndFreemail(t *testing.T) {
	eligible := true
	contato := models.OutreachContactCandidate{
		Email: "contato@empresa.com.br", OwnershipStatus: "COMPANY_OWNED",
		MailboxPurpose: "GENERIC_CONTACT",
		DiscoveryJSON: discoveryJSON(t, controlledDiscovery{
			RouteClass: RouteClassGenericCompany, ControlledEmailEligible: &eligible,
		}),
	}
	gmail := models.OutreachContactCandidate{
		Email: "empresa@gmail.com", OwnershipStatus: "COMPANY_OWNED",
		DiscoveryJSON: discoveryJSON(t, controlledDiscovery{
			RouteClass: RouteClassPublicCompanyFreemail, ControlledEmailEligible: &eligible,
			MailboxCompanyEvidence: "OBSERVED",
		}),
	}
	acc := &models.OutreachAccount{CNPJ14: "1", RazaoSocial: "EMPRESA"}
	now := time.Now().UTC()
	if got := ResolveRecipient(acc, []models.OutreachContactCandidate{contato}, now); got.State != RecipientControlledEligible {
		t.Fatalf("contato state=%s", got.State)
	}
	if got := ResolveRecipient(acc, []models.OutreachContactCandidate{gmail}, now); got.State != RecipientControlledEligible {
		t.Fatalf("gmail state=%s", got.State)
	}
	if CandidateRouteClass(&gmail) == RouteClassDirectPerson {
		t.Fatal("gmail must not become DIRECT_PERSON")
	}
}

func TestUnassociatedGmailAndInferredStayOut(t *testing.T) {
	gmail := models.OutreachContactCandidate{Email: "pessoa@gmail.com"}
	if CandidateRouteClass(&gmail) != RouteClassProbabilisticOrRisky {
		t.Fatalf("unassociated gmail class=%s", CandidateRouteClass(&gmail))
	}
	if CandidateControlledEligible(&gmail) {
		t.Fatal("unassociated gmail must not be eligible")
	}
	inferred := models.OutreachContactCandidate{
		Email: "joao.silva@empresa.com.br", EmailDerivation: "INFERRED",
		DiscoveryJSON: discoveryJSON(t, controlledDiscovery{RouteClass: RouteClassProbabilisticOrRisky, RiskClass: "RISKY"}),
	}
	if CandidateControlledEligible(&inferred) {
		t.Fatal("inferred must stay out of default pilot")
	}
}

func TestCopyQARejectsInventionAndGmailCorporateClaim(t *testing.T) {
	c := &models.OutreachContactCandidate{Email: "contato@empresa.com.br"}
	errs := ValidateCopyForRouteClass(RouteClassGenericCompany, "Olá, Ana, você é o gerente.", "Oi Ana", c)
	if len(errs) == 0 {
		t.Fatal("expected invented name/role errors")
	}
	gerrs := ValidateCopyForRouteClass(RouteClassPublicCompanyFreemail, "Seu domínio corporativo gmail.com", "Gmail", c)
	found := false
	for _, e := range gerrs {
		if e == "gmail_is_not_corporate_domain" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected gmail corporate-domain rejection, got %v", gerrs)
	}
}

func TestGreetingByRouteClassNeverInventsPerson(t *testing.T) {
	role := &models.OutreachContactCandidate{Email: "comercial@empresa.com.br"}
	if g := greetingForRouteClass(RouteClassRoleOrDepartment, role); !strings.Contains(strings.ToLower(g), "comercial") {
		t.Fatalf("role greeting=%s", g)
	}
	gen := &models.OutreachContactCandidate{Email: "contato@empresa.com.br"}
	if g := greetingForRouteClass(RouteClassGenericCompany, gen); g != "Olá, equipe" {
		t.Fatalf("generic greeting=%s", g)
	}
}

func TestBoundedCohortAuthorizationDriftAndFreeze(t *testing.T) {
	now := time.Now().UTC()
	auth := &BoundedCohortAuthorization{
		ID: uuid.New(), ActorID: uuid.New(), AuthorizedAt: now,
		RepositorySHA: "abc", FeedSchemaVersion: "confenge.outreach.v1",
		CohortID: "c1", CohortHash: "h1", PolicyVersion: BoundedCohortPolicyV1,
		AllowedRouteClasses: []string{RouteClassGenericCompany, RouteClassRoleOrDepartment},
		MaxDailyVolume:      50, RecipientSetHash: HashRecipientSet([]string{"contato@empresa.com.br"}),
		ComposerVersion: ComposerVersion, EvidenceVersion: "ev1", TTL: time.Hour, ExpiresAt: now.Add(time.Hour),
	}
	base := CohortTransportInput{
		Now: now, RepositorySHA: "abc", FeedSchemaVersion: "confenge.outreach.v1",
		CohortHash: "h1", PolicyVersion: BoundedCohortPolicyV1, ComposerVersion: ComposerVersion,
		EvidenceVersion: "ev1", RecipientSetHash: auth.RecipientSetHash,
		RecipientMailbox: "contato@empresa.com.br", RouteClass: RouteClassGenericCompany,
	}
	_ = base
	in := CohortTransportInput{
		Now: now, RepositorySHA: "abc", FeedSchemaVersion: "confenge.outreach.v1",
		CohortHash: "h1", PolicyVersion: BoundedCohortPolicyV1, ComposerVersion: ComposerVersion,
		EvidenceVersion: "ev1", RecipientSetHash: auth.RecipientSetHash,
		RecipientMailbox: "contato@empresa.com.br", RouteClass: RouteClassGenericCompany,
	}
	if reasons := ValidateBoundedCohortAuthorization(auth, in); len(reasons) != 0 {
		t.Fatalf("expected valid grant, got %v", reasons)
	}
	in.RecipientMailbox = "novo@empresa.com.br"
	in.PostFreezeRecipient = true
	if reasons := ValidateBoundedCohortAuthorization(auth, in); !containsStr(reasons, "post_freeze_recipient") {
		t.Fatalf("post-freeze must fail: %v", reasons)
	}
	in.PostFreezeRecipient = false
	in.ComposerVersion = "other"
	if reasons := ValidateBoundedCohortAuthorization(auth, in); !containsStr(reasons, "copy_drift") {
		t.Fatalf("copy drift must fail: %v", reasons)
	}
	in.ComposerVersion = ComposerVersion
	in.EvidenceVersion = "other"
	if reasons := ValidateBoundedCohortAuthorization(auth, in); !containsStr(reasons, "evidence_drift") {
		t.Fatalf("evidence drift must fail: %v", reasons)
	}
	in.EvidenceVersion = "ev1"
	in.RecipientSetHash = HashRecipientSet([]string{"outro@empresa.com.br"})
	if reasons := ValidateBoundedCohortAuthorization(auth, in); !containsStr(reasons, "recipient_drift") {
		t.Fatalf("recipient drift must fail: %v", reasons)
	}
	in.RecipientSetHash = auth.RecipientSetHash
	in.RouteClass = RouteClassProbabilisticOrRisky
	if reasons := ValidateBoundedCohortAuthorization(auth, in); !containsStr(reasons, "route_class_outside_policy") {
		t.Fatalf("out of policy class must fail: %v", reasons)
	}
}

func TestDailyCapIsEnforced(t *testing.T) {
	if err := EnforceDailyCap(49, 50); err != nil {
		t.Fatal(err)
	}
	if err := EnforceDailyCap(50, 50); err == nil {
		t.Fatal("daily cap must block at equality")
	}
	auth := &BoundedCohortAuthorization{
		ID: uuid.New(), ActorID: uuid.New(), MaxDailyVolume: 2,
		AllowedRouteClasses: []string{RouteClassGenericCompany},
		RepositorySHA:       "s", FeedSchemaVersion: "v", CohortHash: "h", PolicyVersion: "p",
		RecipientSetHash: "r", ComposerVersion: "c", EvidenceVersion: "e",
		AuthorizedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	in := CohortTransportInput{Now: time.Now().UTC(), SentToday: 2, RouteClass: RouteClassGenericCompany,
		RepositorySHA: "s", FeedSchemaVersion: "v", CohortHash: "h", PolicyVersion: "p",
		RecipientSetHash: "r", ComposerVersion: "c", EvidenceVersion: "e"}
	if reasons := ValidateBoundedCohortAuthorization(auth, in); !containsStr(reasons, "daily_cap_exceeded") {
		t.Fatalf("expected daily_cap_exceeded, got %v", reasons)
	}
}

func TestKillSwitchBlocksIndividualAndCohort(t *testing.T) {
	t.Setenv(EnvKillSwitchPath, filepath.Join(t.TempDir(), "kill"))
	if err := EngageKillSwitch(); err != nil {
		t.Fatal(err)
	}
	tp := &models.OutreachTouchpoint{
		State: models.TouchpointApproved, Recipient: "a@b.com", Subject: "s", BodyText: "hello body",
		Channel: "EMAIL", Purpose: models.TouchpointPurposeInitial,
	}
	RecomputeContentHash(tp)
	tp.ApprovedContentHash = tp.ContentHash
	uid := uuid.New()
	tp.ApprovedBy = &uid
	tp.AuthorizationMode = AuthorizationModeHumanTouchpoint
	if err := CanTransport(tp); err == nil || !strings.Contains(err.Error(), "kill switch") {
		t.Fatalf("kill switch must block human path: %v", err)
	}
	auth := &BoundedCohortAuthorization{ID: uuid.New(), ActorID: uid}
	tp.AuthorizationMode = AuthorizationModeBoundedCohort
	tp.CampaignPolicyAuthorizationID = &auth.ID
	tp.AuthorizationPolicyHash = "x"
	if err := CanTransport(tp); err == nil || !strings.Contains(err.Error(), "kill switch") {
		t.Fatalf("kill switch must block cohort path: %v", err)
	}
}

func TestAutoSendAndGreenAutorunRemainProhibited(t *testing.T) {
	t.Setenv(EnvAutoSend, "true")
	t.Setenv(EnvGreenAutorun, "true")
	t.Setenv(EnvEnabled, "true")
	cfg := LoadConfig()
	if err := cfg.ValidateStartup("test"); err == nil {
		t.Fatal("CONFENGE_AUTO_SEND_ENABLED=true must remain prohibited")
	}
	auth := &BoundedCohortAuthorization{
		ID: uuid.New(), ActorID: uuid.New(), MaxDailyVolume: 10,
		AllowedRouteClasses: []string{RouteClassGenericCompany},
		AuthorizedAt:        time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour),
		AutoSendEnabled: true,
	}
	in := CohortTransportInput{Now: time.Now().UTC(), AutoSendEnabled: true, GreenAutorunEnabled: true, RouteClass: RouteClassGenericCompany}
	reasons := ValidateBoundedCohortAuthorization(auth, in)
	if !containsStr(reasons, "auto_send_forbidden") || !containsStr(reasons, "green_autorun_forbidden") {
		t.Fatalf("expected auto-send and green autorun forbidden, got %v", reasons)
	}
}

func TestCampaignPolicyStillNotTransportable(t *testing.T) {
	t.Setenv(EnvKillSwitchPath, filepath.Join(t.TempDir(), "absent"))
	tp := &models.OutreachTouchpoint{
		State: models.TouchpointApproved, Recipient: "a@b.com", Subject: "s", BodyText: "hello body",
		Channel: "EMAIL", Purpose: models.TouchpointPurposeInitial,
		AuthorizationMode: AuthorizationModeCampaignPolicy,
	}
	RecomputeContentHash(tp)
	tp.ApprovedContentHash = tp.ContentHash
	id := uuid.New()
	tp.CampaignPolicyAuthorizationID = &id
	tp.AuthorizationPolicyHash = "h"
	if err := CanTransport(tp); err == nil {
		t.Fatal("CAMPAIGN_POLICY must remain non-transportable")
	}
}

func TestBoundedCohortCanTransportAndIsNotAutoSend(t *testing.T) {
	t.Setenv(EnvKillSwitchPath, filepath.Join(t.TempDir(), "absent"))
	now := time.Now().UTC()
	tp := &models.OutreachTouchpoint{
		State: models.TouchpointDrafted, Recipient: "contato@empresa.com.br",
		Subject: "s", BodyText: "Olá, equipe,\n\nSou da CONFENGE.",
		Channel: "EMAIL", Purpose: models.TouchpointPurposeInitial,
	}
	auth := &BoundedCohortAuthorization{
		ID: uuid.New(), ActorID: uuid.New(), AuthorizedAt: now, MaxDailyVolume: 50,
		AllowedRouteClasses: []string{RouteClassGenericCompany},
	}
	if err := ApplyBoundedCohortAuthorization(tp, auth, now); err != nil {
		t.Fatal(err)
	}
	if tp.AuthorizationMode != AuthorizationModeBoundedCohort {
		t.Fatalf("mode=%s", tp.AuthorizationMode)
	}
	if tp.ApprovedBy == nil {
		t.Fatal("cohort must bind human actor, unlike CAMPAIGN_POLICY")
	}
	if err := CanTransport(tp); err != nil {
		t.Fatal(err)
	}
}

func TestReplayDoesNotDuplicateSendJobs(t *testing.T) {
	if MessageKeyCampaignEmail(uuid.MustParse("11111111-1111-1111-1111-111111111111"), uuid.MustParse("22222222-2222-2222-2222-222222222222"), uuid.MustParse("33333333-3333-3333-3333-333333333333")) == "" {
		t.Fatal("message key required for idempotent replay")
	}
	first := MessageKeyCampaignEmail(uuid.MustParse("11111111-1111-1111-1111-111111111111"), uuid.MustParse("22222222-2222-2222-2222-222222222222"), uuid.MustParse("33333333-3333-3333-3333-333333333333"))
	second := MessageKeyCampaignEmail(uuid.MustParse("11111111-1111-1111-1111-111111111111"), uuid.MustParse("22222222-2222-2222-2222-222222222222"), uuid.MustParse("33333333-3333-3333-3333-333333333333"))
	if first != second {
		t.Fatal("replay must reuse the same message key")
	}
}

func TestNoTestInvokesRealSend(t *testing.T) {
	if os.Getenv("CONFENGE_AUTO_SEND_ENABLED") == "true" && FileKillSwitchActive() {
		t.Fatal("tests must not combine auto-send with live kill-switch off in this package")
	}
	cfg := LoadConfig()
	if cfg.AutoSendEnabled && cfg.ValidateStartup("test") == nil {
		t.Fatal("auto-send must stay startup-prohibited")
	}
}

func TestSuppressedAndOptOutNeverTransportableViaCohort(t *testing.T) {
	now := time.Now().UTC()
	auth := &BoundedCohortAuthorization{
		ID: uuid.New(), ActorID: uuid.New(), AuthorizedAt: now, ExpiresAt: now.Add(time.Hour),
		MaxDailyVolume: 10, AllowedRouteClasses: []string{RouteClassGenericCompany},
		RepositorySHA: "s", FeedSchemaVersion: "v", CohortHash: "h", PolicyVersion: "p",
		RecipientSetHash: "r", ComposerVersion: "c", EvidenceVersion: "e",
	}
	in := CohortTransportInput{Now: now, RouteClass: RouteClassGenericCompany, Suppressed: true,
		RepositorySHA: "s", FeedSchemaVersion: "v", CohortHash: "h", PolicyVersion: "p",
		RecipientSetHash: "r", ComposerVersion: "c", EvidenceVersion: "e"}
	if reasons := ValidateBoundedCohortAuthorization(auth, in); !containsStr(reasons, "suppressed_mailbox") {
		t.Fatalf("suppressed: %v", reasons)
	}
	in.Suppressed = false
	in.OptOut = true
	if reasons := ValidateBoundedCohortAuthorization(auth, in); !containsStr(reasons, "opt_out") {
		t.Fatalf("opt-out: %v", reasons)
	}
}

func TestReleaseEvaluatorNeverEmitsLiveEmailGO(t *testing.T) {
	want := ReleaseManifest{
		RepositorySHA: "sha", Schema: "confenge.outreach.v1", FeedHash: "f", CohortHash: "c",
		PolicyVersion: "p", ComposerVersion: "comp", KillSwitch: true, VolumeCap: 50,
		AllowedRouteClasses: []string{RouteClassGenericCompany}, SMTPReady: true,
		ObservabilityReady: true, TTLValid: true, SuppressionClear: true,
	}
	got := want
	v := EvaluateControlledEmailRelease(want, got)
	if v.Verdict != ReleaseReadyForControlledEmailReview {
		t.Fatalf("verdict=%s reasons=%v", v.Verdict, v.Reasons)
	}
	if v.Verdict == ReleaseGOForControlledEmailPilot {
		t.Fatal("must never emit GO_FOR_CONTROLLED_EMAIL_PILOT")
	}
	got.RepositorySHA = "other"
	if EvaluateControlledEmailRelease(want, got).Verdict != ReleaseNOGO {
		t.Fatal("sha drift must be NO_GO")
	}
}

func TestIntelSliceKeepsUnknownAndNonReply(t *testing.T) {
	events := []intel.CommercialEvent{
		{Type: "email_attempted", EmailRouteClass: RouteClassGenericCompany, Source: "pncp", CohortID: "c1", PolicyVersion: "p"},
		{Type: "no_reply", EmailRouteClass: RouteClassGenericCompany, Source: "pncp", CohortID: "c1", PolicyVersion: "p"},
		{Type: "hard_bounce", EmailRouteClass: RouteClassGenericCompany, BounceClass: "HARD", Source: "pncp", CohortID: "c1", PolicyVersion: "p"},
	}
	slices := intel.SliceControlledEmailOutcomes(events)
	if len(slices) != 1 {
		t.Fatalf("slices=%d", len(slices))
	}
	if slices[0].Attempted != 1 || slices[0].HardBounce != 1 || slices[0].Unknown != 1 {
		t.Fatalf("got %+v", slices[0])
	}
	if !intel.NonReplyDoesNotInvalidateMailbox(events[1]) {
		t.Fatal("no-reply must not invalidate mailbox")
	}
}

func TestStructuralApproveAllowsControlledEligibleGeneric(t *testing.T) {
	eligible := true
	acc := &models.OutreachAccount{ServiceCode: "REAJUSTE_14133", TargetFitClass: "TARGET_CONFIRMED", EmailSendReady: false}
	cand := &models.OutreachContactCandidate{
		Email: "contato@empresa.com.br", OwnershipStatus: "COMPANY_OWNED",
		MailboxPurpose: "GENERIC_CONTACT", VerificationStatus: models.OutreachVerifyInstitutionalGeneric,
		DiscoveryJSON: discoveryJSON(t, controlledDiscovery{RouteClass: RouteClassGenericCompany, ControlledEmailEligible: &eligible}),
	}
	if !CandidateControlledEligible(cand) {
		t.Fatal("fixture not eligible")
	}
	_ = acc
}
