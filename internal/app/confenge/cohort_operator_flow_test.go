package confenge

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/models"
)

func cohortBool(v bool) *bool { return &v }

func eligibleDisc(t *testing.T, class string, preferred bool, extra controlledDiscovery) []byte {
	t.Helper()
	el := true
	extra.RouteClass = class
	extra.ControlledEmailEligible = &el
	if preferred {
		p := true
		extra.PreferredInitial = &p
	}
	return discoveryJSON(t, extra)
}

func cohortAccount(ref, cnpj, fact string) models.OutreachAccount {
	return models.OutreachAccount{
		SourceLeadID: ref, CNPJ14: cnpj, RazaoSocial: "EMPRESA " + ref + " LTDA",
		NomeFantasia: ref, MomentCode: "REAJUSTE_14133", MomentSummary: "Aditivo publicado",
		FactToMention: fact, ServiceCode: "REAJUSTE_14133", CTA: "Posso enviar o recorte?",
	}
}

func TestPreparePreviewFreezeDerivesHashesAndReconciles(t *testing.T) {
	el := true
	unk := true
	accounts := []CohortAccountInput{
		{Account: cohortAccount("acc-direct", "11111111000191", "Aditivo no PNCP"), Candidates: []models.OutreachContactCandidate{{
			Email: "ana.souza@empresa1.com.br", Name: "ANA SOUZA", Role: "Gerente", EmailSendReady: true,
			OwnershipStatus: "COMPANY_OWNED", MailboxPurpose: "PERSONAL_WORK",
			DiscoveryJSON: eligibleDisc(t, RouteClassDirectPerson, true, controlledDiscovery{PersonUnknown: cohortBool(false), EmailValidated: &el}),
		}}, Source: "pncp"},
		{Account: cohortAccount("acc-role", "22222222000192", "Edital publicado"), Candidates: []models.OutreachContactCandidate{{
			Email: "comercial@empresa2.com.br", MailboxPurpose: "COMERCIAL", OwnershipStatus: "COMPANY_OWNED",
			DiscoveryJSON: eligibleDisc(t, RouteClassRoleOrDepartment, true, controlledDiscovery{PersonUnknown: &unk}),
		}}, Source: "pncp"},
		{Account: cohortAccount("acc-generic", "33333333000193", "Contrato vigente"), Candidates: []models.OutreachContactCandidate{{
			Email: "contato@empresa3.com.br", MailboxPurpose: "GENERIC_CONTACT", OwnershipStatus: "COMPANY_OWNED",
			DiscoveryJSON: eligibleDisc(t, RouteClassGenericCompany, true, controlledDiscovery{PersonUnknown: &unk}),
		}}, Source: "portal"},
		{Account: cohortAccount("acc-gmail", "44444444000194", "Site da empresa"), Candidates: []models.OutreachContactCandidate{{
			Email: "empresa4@gmail.com", OwnershipStatus: "COMPANY_OWNED",
			DiscoveryJSON: eligibleDisc(t, RouteClassPublicCompanyFreemail, true, controlledDiscovery{PersonUnknown: &unk, MailboxCompanyEvidence: "OBSERVED"}),
		}}, Source: "site"},
		{Account: cohortAccount("acc-risky", "55555555000195", "Inferido"), Candidates: []models.OutreachContactCandidate{{
			Email: "joao.silva@empresa5.com.br", EmailDerivation: "INFERRED",
			DiscoveryJSON: discoveryJSON(t, controlledDiscovery{RouteClass: RouteClassProbabilisticOrRisky, RiskClass: "RISKY"}),
		}}, Source: "inferred"},
	}
	snap, err := PrepareControlledCohort(accounts, CohortPrepareOptions{
		Now: time.Now().UTC(), RepositorySHA: "sha-test", SnapshotHash: "snap-1", FeedIdentity: "run-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if snap.CohortHash == "" || snap.RecipientSetHash == "" || snap.CohortID == "" {
		t.Fatal("hashes must be derived; founder must not supply them")
	}
	if snap.Preview.AccountsConsidered != 5 || snap.Preview.AccountsEligible != 4 || snap.Preview.RiskyExcluded != 1 {
		t.Fatalf("preview %+v", snap.Preview)
	}
	if !snap.Preview.Reconciled {
		t.Fatalf("unreconciled: %s", snap.Preview.ReconcileError)
	}
	if snap.RealEmailSent || snap.AutoSendEnabled {
		t.Fatal("prepare must not send or auto-send")
	}
	for _, m := range snap.Members {
		if canonicalPilotEmail(m.Mailbox) == "" {
			t.Fatal("frozen member mailbox must be non-empty")
		}
	}
	if snap.RecipientSetHash == HashRecipientSet(nil) {
		t.Fatal("recipient set hash must not be the empty-set digest")
	}
	if snap.Preview.ByRouteClass[RouteClassDirectPerson] != 1 || snap.Preview.ByRouteClass[RouteClassRoleOrDepartment] != 1 ||
		snap.Preview.ByRouteClass[RouteClassGenericCompany] != 1 || snap.Preview.ByRouteClass[RouteClassPublicCompanyFreemail] != 1 {
		t.Fatalf("classes %+v", snap.Preview.ByRouteClass)
	}
	got := HashFrozenMembership(snap.Members)
	if got != snap.CohortHash {
		t.Fatal("cohort hash must match membership")
	}
}

func TestSelectInitialRoutePreferredAndDirectPersonWin(t *testing.T) {
	unk := true
	role := models.OutreachContactCandidate{
		Email: "comercial@empresa.com.br", MailboxPurpose: "COMERCIAL", OwnershipStatus: "COMPANY_OWNED",
		DiscoveryJSON: eligibleDisc(t, RouteClassRoleOrDepartment, true, controlledDiscovery{PersonUnknown: &unk}),
	}
	person := models.OutreachContactCandidate{
		Email: "ana.souza@empresa.com.br", Name: "ANA SOUZA", Role: "Gerente", EmailSendReady: true,
		OwnershipStatus: "COMPANY_OWNED",
		DiscoveryJSON:   eligibleDisc(t, RouteClassDirectPerson, false, controlledDiscovery{EmailValidated: cohortBool(true)}),
	}
	got, reason := SelectInitialRoute([]models.OutreachContactCandidate{role, person}, nil, time.Now().UTC())
	if got == nil || reason != "" {
		t.Fatalf("select: %v %s", got, reason)
	}
	if CandidateRouteClass(got) != RouteClassDirectPerson {
		t.Fatalf("DIRECT_PERSON should win when proven, got %s", CandidateRouteClass(got))
	}
	generic := models.OutreachContactCandidate{
		Email: "contato@empresa.com.br", MailboxPurpose: "GENERIC_CONTACT", OwnershipStatus: "COMPANY_OWNED",
		DiscoveryJSON: eligibleDisc(t, RouteClassGenericCompany, true, controlledDiscovery{PersonUnknown: &unk}),
	}
	got, _ = SelectInitialRoute([]models.OutreachContactCandidate{role, generic}, nil, time.Now().UTC())
	if CandidateRouteClass(got) != RouteClassRoleOrDepartment {
		t.Fatalf("preferred ROLE should win without DIRECT_PERSON, got %s", CandidateRouteClass(got))
	}
}

func TestPrepareExcludesBounceOptOutSuppressionStaleDuplicateRisky(t *testing.T) {
	unk := true
	stale := time.Now().UTC().Add(-20 * 30 * 24 * time.Hour)
	accounts := []CohortAccountInput{
		{Account: cohortAccount("bounce", "10000000000100", "fato"), Candidates: []models.OutreachContactCandidate{{
			Email: "contato@bounce.com.br", Bounced: true, MailboxPurpose: "GENERIC_CONTACT",
			DiscoveryJSON: eligibleDisc(t, RouteClassGenericCompany, true, controlledDiscovery{PersonUnknown: &unk}),
		}}},
		{Account: cohortAccount("optout", "10000000000101", "fato"), Candidates: []models.OutreachContactCandidate{{
			Email: "contato@optout.com.br", DoNotContact: true, MailboxPurpose: "GENERIC_CONTACT",
			DiscoveryJSON: eligibleDisc(t, RouteClassGenericCompany, true, controlledDiscovery{PersonUnknown: &unk}),
		}}},
		{Account: cohortAccount("supp", "10000000000102", "fato"), Candidates: []models.OutreachContactCandidate{{
			Email: "contato@supp.com.br", Blocked: true, MailboxPurpose: "GENERIC_CONTACT",
			DiscoveryJSON: eligibleDisc(t, RouteClassGenericCompany, true, controlledDiscovery{PersonUnknown: &unk}),
		}}},
		{Account: cohortAccount("stale", "10000000000103", "fato"), Candidates: []models.OutreachContactCandidate{{
			Email: "contato@stale.com.br", MailboxPurpose: "GENERIC_CONTACT", SourceDate: &stale, RouteFreshness: "STALE",
			DiscoveryJSON: eligibleDisc(t, RouteClassGenericCompany, true, controlledDiscovery{PersonUnknown: &unk}),
		}}},
		{Account: cohortAccount("ok-a", "10000000000104", "fato"), Candidates: []models.OutreachContactCandidate{{
			Email: "contato@dup.com.br", MailboxPurpose: "GENERIC_CONTACT", OwnershipStatus: "COMPANY_OWNED",
			DiscoveryJSON: eligibleDisc(t, RouteClassGenericCompany, true, controlledDiscovery{PersonUnknown: &unk}),
		}}},
		{Account: cohortAccount("ok-b", "10000000000105", "fato"), Candidates: []models.OutreachContactCandidate{{
			Email: "contato@dup.com.br", MailboxPurpose: "GENERIC_CONTACT", OwnershipStatus: "COMPANY_OWNED",
			DiscoveryJSON: eligibleDisc(t, RouteClassGenericCompany, true, controlledDiscovery{PersonUnknown: &unk}),
		}}},
		{Account: cohortAccount("dnc-acc", "10000000000106", "fato"), Candidates: []models.OutreachContactCandidate{{
			Email: "contato@dncacc.com.br", MailboxPurpose: "GENERIC_CONTACT",
			DiscoveryJSON: eligibleDisc(t, RouteClassGenericCompany, true, controlledDiscovery{PersonUnknown: &unk}),
		}}},
	}
	accounts[6].Account.DoNotContact = true
	snap, err := PrepareControlledCohort(accounts, CohortPrepareOptions{Now: time.Now().UTC(), RepositorySHA: "sha"})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Preview.RecipientsFinal != 1 {
		t.Fatalf("only first duplicate should survive, got %+v", snap.Preview)
	}
	if snap.Preview.HardBounce < 1 || snap.Preview.OptOut < 1 || snap.Preview.Suppressed < 1 || snap.Preview.Stale < 1 || snap.Preview.Duplicates < 1 {
		t.Fatalf("exclusion counts %+v", snap.Preview)
	}
}

func TestTwoMailboxesSameCompanyPickPreferredNotShotgun(t *testing.T) {
	unk := true
	acc := cohortAccount("two-box", "66666666000196", "fato")
	cands := []models.OutreachContactCandidate{
		{Email: "contato@empresa.com.br", MailboxPurpose: "GENERIC_CONTACT", OwnershipStatus: "COMPANY_OWNED",
			DiscoveryJSON: eligibleDisc(t, RouteClassGenericCompany, false, controlledDiscovery{PersonUnknown: &unk})},
		{Email: "comercial@empresa.com.br", MailboxPurpose: "COMERCIAL", OwnershipStatus: "COMPANY_OWNED",
			DiscoveryJSON: eligibleDisc(t, RouteClassRoleOrDepartment, true, controlledDiscovery{PersonUnknown: &unk})},
	}
	snap, err := PrepareControlledCohort([]CohortAccountInput{{Account: acc, Candidates: cands, Source: "pncp"}}, CohortPrepareOptions{Now: time.Now().UTC(), RepositorySHA: "sha"})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Preview.RecipientsFinal != 1 {
		t.Fatalf("one route per account, got %d", snap.Preview.RecipientsFinal)
	}
	if snap.Members[0].RouteClass != RouteClassRoleOrDepartment {
		t.Fatalf("preferred ROLE should win, got %s", snap.Members[0].RouteClass)
	}
}

func TestPersonUnknownDoesNotExcludeAndDoesNotInventName(t *testing.T) {
	unk := true
	acc := cohortAccount("unknown-person", "77777777000197", "Aditivo publicado")
	cand := models.OutreachContactCandidate{
		Email: "contato@empresa.com.br", MailboxPurpose: "GENERIC_CONTACT", OwnershipStatus: "COMPANY_OWNED",
		DiscoveryJSON: eligibleDisc(t, RouteClassGenericCompany, true, controlledDiscovery{PersonUnknown: &unk}),
	}
	snap, err := PrepareControlledCohort([]CohortAccountInput{{Account: acc, Candidates: []models.OutreachContactCandidate{cand}}}, CohortPrepareOptions{Now: time.Now().UTC(), RepositorySHA: "sha"})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Preview.RecipientsFinal != 1 {
		t.Fatal("missing nominal person must not exclude a control-eligible route")
	}
	m := snap.Members[0]
	if !m.PersonUnknown {
		t.Fatal("person_unknown")
	}
	if looksInventedPersonGreeting(strings.ToLower(m.BodyText)) {
		t.Fatalf("invented greeting in %q", m.BodyText)
	}
	if qa := ValidateCopyForRouteClass(m.RouteClass, m.BodyText, m.Subject, &cand); len(qa) > 0 {
		t.Fatalf("qa %v body %q", qa, m.BodyText)
	}
	if !composerMaySeePersonName(&models.OutreachContactCandidate{
		Name: "ANA SOUZA", Email: "ana@empresa.com.br", EmailSendReady: true,
		DiscoveryJSON: eligibleDisc(t, RouteClassDirectPerson, true, controlledDiscovery{}),
	}) {
		t.Fatal("DIRECT_PERSON with proven name may see the name")
	}
	if composerMaySeePersonName(&cand) {
		t.Fatal("person_unknown must not expose a person variable")
	}
}

func TestGenericNeverValidatedAndGmailNotCorporate(t *testing.T) {
	unk := true
	cand := &models.OutreachContactCandidate{
		Email: "contato@empresa.com.br", MailboxPurpose: "GENERIC_CONTACT",
		DiscoveryJSON: eligibleDisc(t, RouteClassGenericCompany, true, controlledDiscovery{PersonUnknown: &unk}),
	}
	acc := &models.OutreachAccount{CNPJ14: "1", RazaoSocial: "EMPRESA"}
	res := ResolveRecipient(acc, []models.OutreachContactCandidate{*cand}, time.Now().UTC())
	if res.State != RecipientControlledEligible {
		t.Fatalf("state=%s", res.State)
	}
	if res.State == RecipientValidated {
		t.Fatal("GENERIC must never become VALIDATED")
	}
	gmail := &models.OutreachContactCandidate{Email: "empresa@gmail.com"}
	errs := ValidateCopyForRouteClass(RouteClassPublicCompanyFreemail, "Seu domínio corporativo gmail.com", "x", gmail)
	found := false
	for _, e := range errs {
		if e == "gmail_is_not_corporate_domain" {
			found = true
		}
	}
	if !found {
		t.Fatalf("gmail corporate claim: %v", errs)
	}
	if !looksInventedPersonGreeting("olá, maria") {
		t.Fatal("structural invented greeting")
	}
	if looksInventedPersonGreeting("olá, equipe comercial") {
		t.Fatal("team greeting is not invention")
	}
	if !hasPersonShapedToken("Maria") {
		t.Fatal("Maria is person-shaped")
	}
}

func TestAuthorizeFrozenCohortFailClosedAndIdempotentMembership(t *testing.T) {
	unk := true
	accID := uuid.New()
	candID := uuid.New()
	acc := cohortAccount("apply-ok", "88888888000198", "Aditivo publicado")
	acc.ID = accID
	cand := models.OutreachContactCandidate{
		ID: candID, AccountID: accID, Email: "contato@apply.com.br", MailboxPurpose: "GENERIC_CONTACT",
		OwnershipStatus: "COMPANY_OWNED",
		DiscoveryJSON:   eligibleDisc(t, RouteClassGenericCompany, true, controlledDiscovery{PersonUnknown: &unk}),
	}
	snap, err := PrepareControlledCohort([]CohortAccountInput{{Account: acc, Candidates: []models.OutreachContactCandidate{cand}}}, CohortPrepareOptions{Now: time.Now().UTC(), RepositorySHA: "sha-apply"})
	if err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	store := NewMemoryCohortStore()
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := AuthorizeFrozenCohort(ctx, store, nil, uuid.New(), actor, snap, false, now); err == nil {
		t.Fatal("authorize without --confirm must not persist")
	}
	res, err := AuthorizeFrozenCohort(ctx, store, nil, uuid.New(), actor, snap, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.AuthorizedTouchpoints != 1 || res.FailedAuthorization != 0 || res.RealEmailSent {
		t.Fatalf("%+v", res)
	}
	got, err := store.GetGrant(ctx, res.AuthorizationID)
	if err != nil || got == nil || got.FrozenManifest == nil || len(got.FrozenManifest.Members) != 1 {
		t.Fatalf("membership not reconstructable: %+v %v", got, err)
	}
	if got.FrozenManifest.Members[0].Mailbox != "contato@apply.com.br" {
		t.Fatal("exact mailbox missing")
	}
	if got.FrozenManifest.Members[0].AccountID != accID || got.FrozenManifest.Members[0].TouchpointID == uuid.Nil {
		t.Fatalf("operational correlation missing after authorize: %+v", got.FrozenManifest.Members[0])
	}
	if got.CohortHash != snap.CohortHash || HashFrozenCohort(got.FrozenManifest) != snap.CohortHash {
		t.Fatal("operational correlation must not change reviewed cohort hash")
	}
	if state, reason := ProveObservabilityWiring(got); state != EvidencePass {
		t.Fatalf("authorized cohort observability=%s reason=%s", state, reason)
	}

	bad := *snap
	bad.Members = append(append([]FrozenCohortMember{}, snap.Members...), FrozenCohortMember{
		AccountRef: "ghost", Mailbox: "ghost@x.com", RouteClass: RouteClassGenericCompany,
		Subject: "x", BodyText: "Olá, Ana, você é o gerente.", ContentHash: "drift",
	})
	if _, err := AuthorizeFrozenCohort(ctx, NewMemoryCohortStore(), nil, uuid.New(), actor, &bad, true, now); err == nil {
		t.Fatal("partial cohort with copy failure must fail closed")
	}
}

func TestGOReviewIsNotLiveGOAndRevocationBlocks(t *testing.T) {
	now := time.Now().UTC()
	store := NewMemoryCohortStore()
	auth := &BoundedCohortAuthorization{
		ID: uuid.New(), ActorID: uuid.New(), AuthorizedAt: now, RepositorySHA: "sha",
		FeedSchemaVersion: models.OutreachSchemaV1, CohortID: "c", CohortHash: "h",
		PolicyVersion: BoundedCohortPolicyV1, AllowedRouteClasses: []string{RouteClassGenericCompany},
		MaxDailyVolume: 10, RecipientSetHash: HashRecipientSet([]string{"contato@x.com"}),
		ComposerVersion: ComposerVersion, EvidenceVersion: DefaultEvidenceVersion,
		TTL: time.Hour, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.PutGrant(context.Background(), auth); err != nil {
		t.Fatal(err)
	}
	_, err := RecordControlledEmailGOReview(context.Background(), store, auth.ID, auth.ActorID,
		ReleaseGOForControlledEmailPilot, "no", ReleaseManifest{}, now)
	if err == nil {
		t.Fatal("empty evidence must refuse live GO")
	}
	empty, err := RecordControlledEmailGOReview(context.Background(), store, auth.ID, auth.ActorID,
		ReleaseReadyForControlledEmailReview, "founder review", ReleaseManifest{}, now)
	if err == nil || (empty != nil && empty.GOReviewVerdict == ReleaseReadyForControlledEmailReview) {
		t.Fatal("empty live manifest must not record READY")
	}
	live := matchingControlledEmailLive(auth)
	got, err := RecordControlledEmailGOReview(context.Background(), store, auth.ID, auth.ActorID,
		ReleaseReadyForControlledEmailReview, "founder review", live, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.GOReviewVerdict != ReleaseReadyForControlledEmailReview {
		t.Fatalf("verdict=%s", got.GOReviewVerdict)
	}
	if EvaluateControlledEmailRelease(expectedReleaseFromGrant(auth), live).Verdict != ReleaseGOForControlledEmailPilot {
		t.Fatal("complete live evidence must evaluate to GO_FOR_CONTROLLED_EMAIL_PILOT")
	}
	_ = store.RevokeGrant(context.Background(), auth.ID, auth.ActorID, "stop", now)
	rev, _ := store.GetGrant(context.Background(), auth.ID)
	in := CohortTransportInput{Now: now, RepositorySHA: "sha", FeedSchemaVersion: models.OutreachSchemaV1,
		CohortHash: "h", PolicyVersion: BoundedCohortPolicyV1, ComposerVersion: ComposerVersion,
		EvidenceVersion: DefaultEvidenceVersion, RecipientSetHash: auth.RecipientSetHash,
		RouteClass: RouteClassGenericCompany}
	if reasons := ValidateBoundedCohortAuthorization(rev, in); !containsStr(reasons, "authorization_revoked") {
		t.Fatalf("revoke must block: %v", reasons)
	}
}

func TestObservePathPopulatesRouteClassWithoutHandBuiltEvent(t *testing.T) {
	allowConfengeSendingForTest(t)
	ctx := context.Background()
	org, accID, candID := uuid.New(), uuid.New(), uuid.New()
	repo := newMemRepoWithSettings()
	now := time.Now().UTC()
	acc := &models.OutreachAccount{
		ID: accID, OrganizationID: org, CNPJ14: "12345678000190",
		EmailSendReady: false, TargetFitClass: TargetFitConfirmed, TargetFitVersion: "v1",
		TargetFitSourceWatermark: "wm", TargetFitObservedAt: &now, TargetFitFresh: true, TargetFitEligible: true,
		MessageContextHash: "ctx", SourceRunID: "run",
	}
	importID := uuid.New()
	acc.LastImportRunID = &importID
	_, _ = repo.UpsertAccount(ctx, acc)
	eligible := true
	cand := &models.OutreachContactCandidate{
		ID: candID, OrganizationID: org, AccountID: accID,
		Email: "contato@empresa.com.br", OwnershipStatus: "COMPANY_OWNED",
		MailboxPurpose: "GENERIC_CONTACT",
		DiscoveryJSON: discoveryJSON(t, controlledDiscovery{
			RouteClass: RouteClassGenericCompany, ControlledEmailEligible: &eligible, PersonUnknown: cohortBool(true),
		}),
		LastImportRunID: &importID, CreatedAt: now.Add(-time.Hour),
	}
	_, _ = repo.UpsertCandidate(ctx, cand)
	auth := &BoundedCohortAuthorization{
		ID: uuid.New(), ActorID: uuid.New(), AuthorizedAt: now.Add(-time.Minute), MaxDailyVolume: 5,
		AllowedRouteClasses: []string{RouteClassGenericCompany}, CohortID: "cohort-obs",
		RepositorySHA: "sha", FeedSchemaVersion: models.OutreachSchemaV1, CohortHash: "h",
		PolicyVersion: BoundedCohortPolicyV1, RecipientSetHash: HashRecipientSet([]string{cand.Email}),
		ComposerVersion: ComposerVersion, EvidenceVersion: "ev1", ExpiresAt: now.Add(time.Hour),
	}
	tp := &models.OutreachTouchpoint{
		ID: uuid.New(), OrganizationID: org, AccountID: accID, ContactCandidateID: &candID,
		State: models.TouchpointApproved, Recipient: cand.Email, Subject: "s",
		BodyText: "Olá, equipe,\n\nSou da CONFENGE.", Channel: models.OutreachChannelEmail,
		Purpose: models.TouchpointPurposeInitial, GeneratedContextHash: "ctx",
	}
	if err := ApplyBoundedCohortAuthorization(tp, auth, now); err != nil {
		t.Fatal(err)
	}
	if err := repo.InsertTouchpoint(ctx, tp); err != nil {
		t.Fatal(err)
	}
	svc := NewService(Config{
		Enabled: true, RepositorySHA: "sha", FeedSchemaVersion: models.OutreachSchemaV1,
		EvidenceVersion: "ev1", RequireHumanApproval: true, DefaultDailyLimit: 50,
	}, repo, nil).(*service)
	if err := svc.BindBoundedCohortGrant(auth); err != nil {
		t.Fatal(err)
	}
	svc.observeControlledEmail(ctx, org, intel.EventEmailAttempted, tp, cand, ControlledEmailContext{
		OccurredAt: now, ProviderName: "smtp",
	})
	if err := svc.transitionCompletedTouchpoint(ctx, org, tp, now, "prov-1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.observeControlledEmail(ctx, org, intel.EventProviderAccepted, tp, cand, ControlledEmailContext{
		OccurredAt: now, ProviderName: "smtp", StableEventRef: "prov-1",
	}); err != nil {
		t.Fatal(err)
	}
	_ = repo.UpdateTouchpoint(ctx, tp)
	if err := svc.NoteReply(ctx, org, cand.Email, map[string]any{"reply_class": "POSITIVE"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.NoteBounce(ctx, org, cand.Email, "user unknown"); err != nil {
		t.Fatal(err)
	}
	events := svc.ObservedControlledEmailEvents()
	if len(events) < 4 {
		t.Fatalf("real observe path must emit attempted/accepted/reply/bounce, got %d", len(events))
	}
	foundAttempted, foundAccepted, foundReply, foundBounce := false, false, false, false
	for _, ev := range events {
		if ev.Synthetic {
			t.Fatalf("real controlled-email event mislabeled synthetic: %+v", ev)
		}
		if ev.Type == intel.EventNoReply {
			t.Fatal("no-reply must not be inferred")
		}
		switch ev.Type {
		case intel.EventEmailAttempted:
			foundAttempted = true
		case intel.EventProviderAccepted:
			foundAccepted = true
		case intel.EventReply:
			foundReply = true
		case intel.EventHardBounce, intel.EventSoftBounce:
			foundBounce = true
		default:
			continue
		}
		if ev.EmailRouteClass != RouteClassGenericCompany {
			t.Fatalf("%s missing route class: %+v", ev.Type, ev)
		}
		if ev.CohortID != auth.CohortID {
			t.Fatalf("%s missing cohort_id: %+v", ev.Type, ev)
		}
		if ev.PolicyVersion != auth.PolicyVersion {
			t.Fatalf("%s missing policy_version: %+v", ev.Type, ev)
		}
		if ev.AccountPublicID == "" && ev.EntityPublicID == "" && ev.CorrelationID == "" {
			t.Fatalf("%s missing touchpoint/account correlation: %+v", ev.Type, ev)
		}
	}
	if !foundAttempted || !foundAccepted || !foundReply || !foundBounce {
		raw, _ := json.Marshal(events)
		t.Fatalf("attempted/accepted/reply/bounce missing from real path: %s", raw)
	}
	rep := intel.BuildControlledEmailExecutiveReport(events)
	if len(rep.Rows) == 0 {
		t.Fatal("executive report empty")
	}
	text := intel.FormatControlledEmailReport(rep)
	if !strings.Contains(text, "route_class") || !strings.Contains(text, RouteClassGenericCompany) {
		t.Fatalf("report %s", text)
	}
	noReply := intel.CommercialEvent{Type: "no_reply", EmailRouteClass: RouteClassGenericCompany, CohortID: "cohort-obs"}
	if !intel.NonReplyDoesNotInvalidateMailbox(noReply) {
		t.Fatal("no-reply stays UNKNOWN")
	}
}

func TestFirstCohortDryRunFortyAccountsNoRealSend(t *testing.T) {
	unk := true
	el := true
	var accounts []CohortAccountInput
	add := func(ref, cnpj, email, class string, preferred bool, mutate func(*models.OutreachContactCandidate)) {
		d := controlledDiscovery{PersonUnknown: &unk}
		if class == RouteClassDirectPerson {
			d.PersonUnknown = cohortBool(false)
			d.EmailValidated = &el
		}
		if class == RouteClassPublicCompanyFreemail {
			d.MailboxCompanyEvidence = "OBSERVED"
		}
		c := models.OutreachContactCandidate{
			Email: email, OwnershipStatus: "COMPANY_OWNED",
			DiscoveryJSON: eligibleDisc(t, class, preferred, d),
		}
		if class == RouteClassDirectPerson {
			c.Name = "ANA SOUZA"
			c.Role = "Gerente"
			c.EmailSendReady = true
			c.MailboxPurpose = "PERSONAL_WORK"
		}
		if class == RouteClassRoleOrDepartment {
			c.MailboxPurpose = "COMERCIAL"
		}
		if class == RouteClassGenericCompany {
			c.MailboxPurpose = "GENERIC_CONTACT"
		}
		if mutate != nil {
			mutate(&c)
		}
		accounts = append(accounts, CohortAccountInput{Account: cohortAccount(ref, cnpj, "Aditivo publicado no portal"), Candidates: []models.OutreachContactCandidate{c}, Source: "pncp"})
	}
	for i := 0; i < 10; i++ {
		add("d-"+itoa(i), "110000000000"+itoa(10+i), "pessoa"+itoa(i)+"@d"+itoa(i)+".com.br", RouteClassDirectPerson, true, nil)
	}
	for i := 0; i < 10; i++ {
		add("r-"+itoa(i), "120000000000"+itoa(10+i), "comercial@r"+itoa(i)+".com.br", RouteClassRoleOrDepartment, true, nil)
	}
	for i := 0; i < 10; i++ {
		add("g-"+itoa(i), "130000000000"+itoa(10+i), "contato@g"+itoa(i)+".com.br", RouteClassGenericCompany, true, nil)
	}
	for i := 0; i < 8; i++ {
		add("f-"+itoa(i), "140000000000"+itoa(10+i), "empresa"+itoa(i)+"@gmail.com", RouteClassPublicCompanyFreemail, true, nil)
	}
	add("risky-1", "15000000000151", "inferido@x.com.br", RouteClassProbabilisticOrRisky, false, func(c *models.OutreachContactCandidate) {
		c.EmailDerivation = "INFERRED"
		c.DiscoveryJSON = discoveryJSON(t, controlledDiscovery{RouteClass: RouteClassProbabilisticOrRisky})
	})
	add("bounce-1", "15000000000152", "bounce@x.com.br", RouteClassGenericCompany, true, func(c *models.OutreachContactCandidate) { c.Bounced = true })
	add("opt-1", "15000000000153", "opt@x.com.br", RouteClassGenericCompany, true, func(c *models.OutreachContactCandidate) { c.DoNotContact = true })
	add("shotgun-a", "15000000000154", "shared@dup.com.br", RouteClassGenericCompany, true, nil)
	add("shotgun-b", "15000000000155", "shared@dup.com.br", RouteClassGenericCompany, true, nil)

	snap, err := PrepareControlledCohort(accounts, CohortPrepareOptions{
		Now: time.Now().UTC(), RepositorySHA: "sha-dry", Limit: 50, SnapshotHash: "dry-run-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Preview.AccountsConsidered != len(accounts) {
		t.Fatalf("considered=%d", snap.Preview.AccountsConsidered)
	}
	if snap.Preview.RecipientsFinal < 30 || snap.Preview.RecipientsFinal > 50 {
		t.Fatalf("first cohort size %d", snap.Preview.RecipientsFinal)
	}
	if snap.Preview.ByRouteClass[RouteClassDirectPerson] < 1 || snap.Preview.ByRouteClass[RouteClassRoleOrDepartment] < 1 ||
		snap.Preview.ByRouteClass[RouteClassGenericCompany] < 1 || snap.Preview.ByRouteClass[RouteClassPublicCompanyFreemail] < 1 {
		t.Fatalf("need all four default classes: %+v", snap.Preview.ByRouteClass)
	}
	if snap.Preview.RiskyExcluded < 1 || snap.Preview.HardBounce < 1 || snap.Preview.OptOut < 1 || snap.Preview.Duplicates < 1 {
		t.Fatalf("exclusions %+v", snap.Preview)
	}
	if !snap.Preview.Reconciled {
		t.Fatal(snap.Preview.ReconcileError)
	}
	actor := uuid.New()
	res, err := AuthorizeFrozenCohort(context.Background(), NewMemoryCohortStore(), nil, uuid.New(), actor, snap, true, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if res.AuthorizedTouchpoints != snap.Preview.RecipientsFinal || res.FailedAuthorization != 0 || res.RealEmailSent {
		t.Fatalf("authorize %+v", res)
	}
	want := ReleaseManifest{
		RepositorySHA: "sha-dry", Schema: models.OutreachSchemaV1, FeedHash: "dry-run-1",
		CohortHash: snap.CohortHash, RecipientSetHash: snap.RecipientSetHash,
		PolicyVersion: BoundedCohortPolicyV1, ComposerVersion: ComposerVersion,
		AllowedRouteClasses: snap.AllowedRouteClasses, VolumeCap: snap.MaxDailyVolume,
		SMTPReady: EvidencePass, ReplyIngestReady: EvidencePass, ObservabilityReady: EvidencePass,
		DispatchWiring: EvidencePass, SenderProviderConfig: EvidencePass, TTLValid: EvidencePass,
		SuppressionClear: EvidencePass, DBCohortAuthority: EvidencePass, EvidenceVersion: DefaultEvidenceVersion,
		KillSwitchOperational: EvidencePass, SendingPausedState: EvidencePass,
		AutoSendState: EvidencePass, GreenAutorunState: EvidencePass,
	}
	v := EvaluateControlledEmailRelease(want, want)
	if v.Verdict != ReleaseGOForControlledEmailPilot {
		t.Fatalf("complete PASS set must be live GO, got %s %v", v.Verdict, v.Reasons)
	}
	if res.RealEmailSent || res.AutoSendEnabled {
		t.Fatal("authorize/dry-run must not send or enable auto-send")
	}
}

func TestPostFreezeAndContentDriftFailClosed(t *testing.T) {
	now := time.Now().UTC()
	auth := &BoundedCohortAuthorization{
		ID: uuid.New(), ActorID: uuid.New(), AuthorizedAt: now, MaxDailyVolume: 5,
		AllowedRouteClasses: []string{RouteClassGenericCompany},
		RepositorySHA:       "sha", FeedSchemaVersion: models.OutreachSchemaV1, CohortHash: "h",
		PolicyVersion: BoundedCohortPolicyV1, RecipientSetHash: HashRecipientSet([]string{"contato@x.com"}),
		ComposerVersion: ComposerVersion, EvidenceVersion: "ev1", TTL: time.Hour, ExpiresAt: now.Add(time.Hour),
	}
	in := CohortTransportInput{
		Now: now, RepositorySHA: "sha", FeedSchemaVersion: models.OutreachSchemaV1, CohortHash: "h",
		PolicyVersion: BoundedCohortPolicyV1, ComposerVersion: ComposerVersion, EvidenceVersion: "ev1",
		RecipientSetHash: auth.RecipientSetHash, RouteClass: RouteClassGenericCompany, PostFreezeRecipient: true,
	}
	if reasons := ValidateBoundedCohortAuthorization(auth, in); !containsStr(reasons, "post_freeze_recipient") {
		t.Fatalf("post-freeze: %v", reasons)
	}
	in.PostFreezeRecipient = false
	in.ComposerVersion = "other"
	if reasons := ValidateBoundedCohortAuthorization(auth, in); !containsStr(reasons, "copy_drift") {
		t.Fatalf("content/composer drift: %v", reasons)
	}
}

func extraCLIStampContact(email, class, sourceURL, suppression string) FeedContact {
	eligible, unk, chain := true, true, false
	preferred := class == RouteClassGenericCompany
	return FeedContact{
		Email: email, SourceURL: sourceURL, OwnershipStatus: "COMPANY_OWNED",
		MailboxPurpose: "UNKNOWN", VerificationStatus: models.OutreachVerifyOfficialSource,
		RouteClass: class, ControlledEmailEligible: &eligible, PreferredInitial: &preferred,
		PersonUnknown: &unk, MailboxCompanyEvidence: "OBSERVED", RouteSuppression: suppression,
		RouteFreshness: "FRESH", ProvenanceChainValid: &chain, RiskClass: "ALLOWED",
	}
}

func extraCLIStampLead(ref, cnpj, email, class, sourceURL, suppression string) FeedLead {
	return FeedLead{
		SourceLeadID: ref,
		Company:      FeedCompany{CNPJ14: cnpj, RazaoSocial: "EMPRESA " + ref + " LTDA"},
		Moment:       FeedMoment{Code: "CONTRACT_EXTENSION", Summary: "Aditivo publicado"},
		MessagingContext: FeedMessaging{
			FactToMention: "Aditivo publicado no portal oficial", CTA: "Posso enviar o recorte?",
		},
		Contacts: []FeedContact{extraCLIStampContact(email, class, sourceURL, suppression)},
	}
}

func TestPrepareFromExtraCLINoneSuppressionKeepsControlledEligible(t *testing.T) {
	feed := &Feed{
		SchemaVersion: models.OutreachSchemaV1,
		Source:        FeedSource{System: "extra-cli", RunID: "run-none", SnapshotHash: "snap-none"},
		Leads: []FeedLead{
			extraCLIStampLead("lead-generic", "11111111000191", "contato@empresa-extra.com.br", RouteClassGenericCompany, "https://empresa-extra.com.br/contato", "NONE"),
			extraCLIStampLead("lead-gmail", "22222222000192", "empresa@gmail.com", RouteClassPublicCompanyFreemail, "https://empresa-gmail.com.br/contato", "NONE"),
		},
	}
	snap, err := PrepareControlledCohortFromFeed(feed, CohortPrepareOptions{
		Now: time.Now().UTC(), Limit: 50, MaxDailyVolume: 10, TTL: 24 * time.Hour, RepositorySHA: "sha-none",
	})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Preview.AccountsEligible != 2 || snap.Preview.RecipientsFinal != 2 {
		t.Fatalf("extra-cli NONE + invalid chain must freeze stamped routes, preview=%+v exclusions=%+v", snap.Preview, snap.Exclusions)
	}
	if snap.RecipientSetHash == HashRecipientSet(nil) {
		t.Fatal("recipient set must not be the empty digest")
	}
	for _, m := range snap.Members {
		if canonicalPilotEmail(m.Mailbox) == "" {
			t.Fatal("frozen mailbox empty")
		}
		if m.RouteClass != RouteClassGenericCompany && m.RouteClass != RouteClassPublicCompanyFreemail {
			t.Fatalf("unexpected class %s", m.RouteClass)
		}
	}
}

func TestPrepareFromExtraCLIActiveSuppressionStillWins(t *testing.T) {
	feed := &Feed{
		SchemaVersion: models.OutreachSchemaV1,
		Source:        FeedSource{System: "extra-cli", RunID: "run-supp", SnapshotHash: "snap-supp"},
		Leads: []FeedLead{
			extraCLIStampLead("lead-dnc", "33333333000193", "contato@empresa-dnc.com.br", RouteClassGenericCompany, "https://empresa-dnc.com.br/contato", "SUPPRESSED"),
		},
	}
	snap, err := PrepareControlledCohortFromFeed(feed, CohortPrepareOptions{
		Now: time.Now().UTC(), RepositorySHA: "sha-supp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Preview.AccountsEligible != 0 {
		t.Fatalf("active suppression must win, preview=%+v", snap.Preview)
	}
	if snap.Preview.OptOut < 1 && snap.Preview.ByExclusionReason["recipient_opt_out"] < 1 {
		t.Fatalf("want recipient_opt_out, got %+v", snap.Preview.ByExclusionReason)
	}
}

func TestPrepareFromExtraCLIFixtureTaintStillExcluded(t *testing.T) {
	lead := extraCLIStampLead("lead-fix", "55555555000195", "contato@empresa-fix.com.br", RouteClassGenericCompany, "https://empresa-fix.com.br/contato", "NONE")
	derived := true
	lead.Contacts[0].DerivedFromFixture = &derived
	feed := &Feed{
		SchemaVersion: models.OutreachSchemaV1,
		Source:        FeedSource{System: "extra-cli", RunID: "run-fix", SnapshotHash: "snap-fix"},
		Leads:         []FeedLead{lead},
	}
	snap, err := PrepareControlledCohortFromFeed(feed, CohortPrepareOptions{
		Now: time.Now().UTC(), RepositorySHA: "sha-fix",
	})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Preview.AccountsEligible != 0 {
		t.Fatalf("fixture taint must exclude, preview=%+v", snap.Preview)
	}
}

func TestPrepareFromFiveClassCanaryPicksOneNonemptyRoute(t *testing.T) {
	raw, err := os.ReadFile("testdata/controlled_email_five_class_canary.json")
	if err != nil {
		t.Fatal(err)
	}
	feed, err := ParseFeed(raw)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := PrepareControlledCohortFromFeed(feed, CohortPrepareOptions{
		Now: time.Now().UTC(), Limit: 50, MaxDailyVolume: 10, TTL: 24 * time.Hour, RepositorySHA: "sha-canary",
	})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Preview.RecipientsFinal != 1 {
		t.Fatalf("one account, one route, got eligible=%d reasons=%+v members=%d", snap.Preview.AccountsEligible, snap.Preview.ByExclusionReason, len(snap.Members))
	}
	if canonicalPilotEmail(snap.Members[0].Mailbox) == "" {
		t.Fatal("canary frozen mailbox empty")
	}
	if snap.Members[0].RouteClass == RouteClassProbabilisticOrRisky {
		t.Fatal("RISKY must not freeze")
	}
}

func TestLatestBoundedCohortReadinessIsTruthfulAndRedacted(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	orgID := uuid.New()
	store := NewMemoryCohortStore()
	auth := &BoundedCohortAuthorization{
		ID: uuid.New(), OrganizationID: orgID, ActorID: uuid.New(), AuthorizedAt: now,
		ExpiresAt: now.Add(6 * time.Hour), CohortID: "cohort-real-10", CohortHash: "cohort-hash",
		PolicyVersion: BoundedCohortPolicyV1, AllowedRouteClasses: []string{RouteClassGenericCompany},
		MaxDailyVolume: 10, FrozenManifest: &FrozenCohortSnapshot{Members: []FrozenCohortMember{
			{RouteClass: RouteClassGenericCompany}, {RouteClass: RouteClassGenericCompany},
		}},
	}
	if err := store.PutGrant(context.Background(), auth); err != nil {
		t.Fatal(err)
	}
	latestStore, ok := store.(latestBoundedCohortStore)
	if !ok {
		t.Fatal("bounded store does not expose latest read")
	}
	latest, err := latestStore.LatestGrant(context.Background(), orgID)
	if err != nil {
		t.Fatal(err)
	}
	view := boundedCohortReadiness(latest, now)
	if view == nil || view.CohortID != auth.CohortID || view.AuthorizedQuantity == nil || *view.AuthorizedQuantity != 2 || view.MaxDailyVolume != 10 {
		t.Fatalf("unexpected readiness: %+v", view)
	}
	if view.State != "active" {
		t.Fatalf("state=%s", view.State)
	}
	if view.RouteClassDistribution[RouteClassGenericCompany] != 2 {
		t.Fatalf("route distribution lost: %+v", view.RouteClassDistribution)
	}
	legacy := *latest
	legacy.FrozenManifest = nil
	legacyView := boundedCohortReadiness(&legacy, now)
	if legacyView.AuthorizedQuantity != nil || legacyView.RouteClassDistribution != nil {
		t.Fatalf("legacy grant without membership must preserve UNKNOWN, got %+v", legacyView)
	}
	sent, reserved, err := latestStore.GrantDispatchCounts(context.Background(), auth.ID)
	if err != nil || sent != 0 || reserved != 0 {
		t.Fatalf("dispatch counts: sent=%d reserved=%d err=%v", sent, reserved, err)
	}
	expired := boundedCohortReadiness(latest, now.Add(7*time.Hour))
	if expired == nil || expired.State != "expired" {
		t.Fatalf("expired state lost: %+v", expired)
	}
}
