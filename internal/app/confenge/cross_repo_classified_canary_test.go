package confenge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

func TestCrossRepoFiveClassCanaryIngest(t *testing.T) {
	t.Setenv(EnvKillSwitchPath, filepath.Join(t.TempDir(), "absent"))
	ctx := context.Background()
	raw, err := os.ReadFile(filepath.Join("testdata", "controlled_email_five_class_canary.json"))
	if err != nil {
		t.Fatal(err)
	}
	repo := newMemRepo()
	org, user := uuid.New(), uuid.New()
	svc := NewService(Config{
		Enabled: true, RequireHumanApproval: true, DefaultDailyLimit: 50,
		RepositorySHA: "sha-canary", FeedSchemaVersion: models.OutreachSchemaV1,
		EvidenceVersion: DefaultEvidenceVersion, MaxFeedPayloadBytes: DefaultMaxPayloadBytes,
	}, repo, nil).(*service)
	run, xerr := svc.ImportFromBytes(ctx, org, &user, raw, ImportOptions{IdempotencyKey: "five-class-canary"})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if run == nil || run.Status != models.OutreachImportCompleted {
		t.Fatalf("import=%+v", run)
	}
	acc, err := repo.GetAccountByCNPJ(ctx, org, "12345678000190")
	if err != nil || acc == nil {
		t.Fatalf("account: %v", err)
	}
	cands, err := repo.ListCandidates(ctx, org, acc.ID)
	if err != nil {
		t.Fatal(err)
	}
	byClass := map[string]models.OutreachContactCandidate{}
	preferred := 0
	for i := range cands {
		class := CandidateRouteClass(&cands[i])
		byClass[class] = cands[i]
		if CandidatePreferredInitial(&cands[i]) {
			preferred++
		}
	}
	for _, want := range []string{
		RouteClassDirectPerson, RouteClassRoleOrDepartment, RouteClassGenericCompany,
		RouteClassPublicCompanyFreemail, RouteClassProbabilisticOrRisky,
	} {
		if _, ok := byClass[want]; !ok {
			t.Fatalf("missing class %s in %+v", want, byClass)
		}
	}
	if preferred != 1 {
		t.Fatalf("preferred_initial count=%d want 1", preferred)
	}
	risky := byClass[RouteClassProbabilisticOrRisky]
	if CandidateControlledEligible(&risky) {
		t.Fatal("RISKY must stay outside default cohort")
	}
	generic := byClass[RouteClassGenericCompany]
	if !CandidateControlledEligible(&generic) {
		t.Fatal("generic must be controlled eligible")
	}
	if generic.PersonID != "" || generic.Name != "" {
		t.Fatalf("generic invented person id=%q name=%q", generic.PersonID, generic.Name)
	}
	gmail := byClass[RouteClassPublicCompanyFreemail]
	if CandidateRouteClass(&gmail) == RouteClassDirectPerson {
		t.Fatal("gmail must not become DIRECT_PERSON")
	}

	now := time.Now().UTC()
	mailboxes := []string{}
	for i := range cands {
		if CandidateControlledEligible(&cands[i]) {
			mailboxes = append(mailboxes, cands[i].Email)
		}
	}
	auth := &BoundedCohortAuthorization{
		ID: uuid.New(), OrganizationID: org, ActorID: user, AuthorizedAt: now,
		RepositorySHA: "sha-canary", FeedSchemaVersion: models.OutreachSchemaV1,
		CohortID: "synthetic-five-class", CohortHash: HashCohortID("synthetic-five-class", "sha-canary"),
		PolicyVersion: BoundedCohortPolicyV1,
		AllowedRouteClasses: []string{
			RouteClassDirectPerson, RouteClassRoleOrDepartment,
			RouteClassGenericCompany, RouteClassPublicCompanyFreemail,
		},
		MaxDailyVolume: 50, RecipientSetHash: HashRecipientSet(mailboxes),
		ComposerVersion: ComposerVersion, EvidenceVersion: DefaultEvidenceVersion,
		TTL: time.Hour, ExpiresAt: now.Add(time.Hour),
	}
	if strings.Contains(strings.Join(auth.AllowedRouteClasses, ","), RouteClassProbabilisticOrRisky) {
		t.Fatal("RISKY must not be in allowed classes")
	}
	if err := svc.BindBoundedCohortGrant(auth); err != nil {
		t.Fatal(err)
	}
	tp := &models.OutreachTouchpoint{
		OrganizationID: org, AccountID: acc.ID, State: models.TouchpointDrafted,
		Recipient: generic.Email, Subject: "s",
		BodyText: "Olá, equipe,\n\nSou da CONFENGE.",
		Channel:  models.OutreachChannelEmail, Purpose: models.TouchpointPurposeInitial,
	}
	if err := ApplyBoundedCohortAuthorization(tp, auth, now); err != nil {
		t.Fatal(err)
	}
	in := CohortTransportInput{
		Now: now, RepositorySHA: "sha-canary", FeedSchemaVersion: models.OutreachSchemaV1,
		CohortHash: auth.CohortHash, PolicyVersion: BoundedCohortPolicyV1,
		ComposerVersion: ComposerVersion, EvidenceVersion: DefaultEvidenceVersion,
		RecipientSetHash: auth.RecipientSetHash, RouteClass: RouteClassGenericCompany,
	}
	if err := CanTransportCohort(tp, auth, in); err != nil {
		t.Fatalf("generic cohort transport: %v", err)
	}
	in.RouteClass = RouteClassProbabilisticOrRisky
	if err := CanTransportCohort(tp, auth, in); err == nil {
		t.Fatal("RISKY must fail transport")
	}

	if dsn := os.Getenv("WARMBLY_TEST_POSTGRES_DSN"); dsn != "" {
		pool, store := openCohortPG(t)
		_ = pool
		if _, err := CreateBoundedCohortGrant(ctx, store, auth, true); err != nil {
			t.Fatal(err)
		}
		restarted := NewPostgresCohortStore(pool)
		got, err := restarted.GetGrant(ctx, auth.ID)
		if err != nil || got == nil {
			t.Fatalf("persisted authorization missing after new client: %v", err)
		}
		svc.WireCohortAuth(store)
		live, err := store.GetGrant(ctx, auth.ID)
		if err != nil {
			t.Fatal(err)
		}
		in.RouteClass = RouteClassGenericCompany
		if err := CanTransportCohort(tp, live, in); err != nil {
			t.Fatalf("PG transport revalidation: %v", err)
		}
		if err := RevokeBoundedCohortGrant(ctx, store, auth.ID, user, "canary-stop", now); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv(EnvKillSwitchPath, filepath.Join(t.TempDir(), "kill"))
	if err := EngageKillSwitch(); err != nil {
		t.Fatal(err)
	}
	if err := CanTransportCohort(tp, auth, in); err == nil {
		t.Fatal("kill switch must block canary transport")
	}
}
