package confenge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/models"
)

func readyFirstWindowSnapshot(now time.Time) FirstWindowReadinessSnapshot {
	until := now.Add(time.Hour)
	return FirstWindowReadinessSnapshot{
		WarmblyReleaseSHA:  strings.Repeat("a", 40),
		FeedManifestSchema: "confenge.outreach.v1",
		SourceRunID:        "run-abc",
		SourceSnapshotHash: "snap-abc",
		SourceHealth:       SourceHealthDecision{State: SourceHealthFresh},
		CommercialAuthority: CommercialAuthorityDecision{
			Present: true, State: CommercialAuthorityCurrent,
			NewAdmissionAllowed: true, ExistingBoundTouchTransportAllowed: true,
			BasisSourceRunID: "run-abc", BasisSnapshotHash: "snap-abc",
			BasisMembershipHash: strings.Repeat("a", 64), ValidUntil: &until,
		},
		MembershipHash:            strings.Repeat("a", 64),
		PolicyID:                  DelegatedFirstTouchPolicyV1,
		PolicyVersion:             DelegatedFirstTouchPolicyV1,
		AllowedRouteClasses:       []string{RouteClassDirectPerson, RouteClassGenericCompany},
		MailboxSet:                []string{"tiago.sasaki@confenge.com.br"},
		SMTPReady:                 EvidencePass,
		IMAPReplyIngestReady:      EvidencePass,
		GlobalSendsPerHour:        10,
		MailboxRateCaps:           []int{50},
		MinWaitSeconds:            600,
		BusinessTimezone:          "America/Sao_Paulo",
		BusinessWindowStart:       "09:00",
		BusinessWindowEnd:         "18:00",
		PauseState:                TransportPaused,
		PauseSource:               PauseSourceKillSwitch,
		KillSwitchEngaged:         true,
		OutcomeObservabilityReady: EvidencePass,
		ProviderMutationCount:     0,
		EvaluatedAt:               now,
	}
}

func TestEvaluateFirstWindowReadinessNeverEmitsGO(t *testing.T) {
	now := time.Date(2026, 8, 27, 16, 0, 0, 0, time.UTC)
	snap := readyFirstWindowSnapshot(now)
	first := EvaluateFirstWindowReadiness(snap)
	if first.Verdict != FirstWindowReadyForGOAdjudication {
		t.Fatalf("complete snapshot: %s blockers=%v", first.Verdict, first.Blockers)
	}
	if strings.Contains(first.Verdict, FirstWindowGOForControlledPilot) || first.Verdict == FirstWindowGOForControlledPilot {
		t.Fatal("emitted GO_FOR_CONTROLLED_EMAIL_PILOT")
	}
	required := []string{
		first.WarmblyReleaseSHA, first.FeedManifestSchema, first.SourceRunID, first.SourceSnapshotHash,
		first.MembershipHash, first.PolicyID, first.PolicyVersion, first.PauseState, first.PauseSource,
		first.BusinessTimezone, first.BusinessWindowStart, first.BusinessWindowEnd,
		first.SMTPReady, first.IMAPReplyIngestReady, first.OutcomeObservabilityReady,
	}
	for _, field := range required {
		if strings.TrimSpace(field) == "" {
			t.Fatalf("closed field empty in %+v", first)
		}
	}
	if len(first.AllowedRouteClasses) == 0 || len(first.MailboxSet) == 0 {
		t.Fatal("route/mailbox set missing")
	}
	if first.ProviderMutationCount != 0 {
		t.Fatalf("provider mutation=%d", first.ProviderMutationCount)
	}
	second := EvaluateFirstWindowReadiness(snap)
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if string(a) != string(b) {
		t.Fatal("evaluator is not deterministic")
	}

	blocked := snap
	blocked.WarmblyReleaseSHA = ""
	got := EvaluateFirstWindowReadiness(blocked)
	if !strings.HasPrefix(got.Verdict, FirstWindowBlockedPrefix) {
		t.Fatalf("missing SHA: %s", got.Verdict)
	}
	if got.Verdict == FirstWindowGOForControlledPilot {
		t.Fatal("blocked snapshot emitted GO")
	}
}

func TestEvaluateFirstWindowReadinessUnknownPolicyHolds(t *testing.T) {
	now := time.Date(2026, 8, 27, 16, 0, 0, 0, time.UTC)
	snap := readyFirstWindowSnapshot(now)
	snap.PolicyID = "CFG-FIRST-TOUCH-ROUTING-v1-beta"
	snap.PolicyVersion = snap.PolicyID
	got := EvaluateFirstWindowReadiness(snap)
	if !strings.HasPrefix(got.Verdict, FirstWindowBlockedPrefix) {
		t.Fatalf("%s", got.Verdict)
	}
}

func TestCollectFirstWindowReadinessIndependentAuthorityOnStaleSource(t *testing.T) {
	now := time.Date(2026, 8, 27, 16, 0, 0, 0, time.UTC)
	orgID := uuid.New()
	repo := newMemRepo()
	generatedAt := now.Add(-48 * time.Hour)
	expiresAt := now.Add(2 * time.Hour)
	payload := testAuthorityPayload(CommercialAuthorityCurrent, now)
	svc := NewService(Config{
		Enabled: true, RepositorySHA: strings.Repeat("a", 40),
		FeedSchemaVersion: "confenge.outreach.v1", FeedMaxAge: 24 * time.Hour,
	}, repo, nil).(*service)
	svc.nowFn = func() time.Time { return now }
	svc.WireDispatchGovernor(dispatch.NewGovernor(dispatch.Config{
		SendsPerHour: 10, MinGap: 10 * time.Minute, Timezone: "America/Sao_Paulo",
		WindowStart: "09:00", WindowEnd: "18:00", EnvPaused: true, EnvPauseReason: "zero-smtp",
	}, dispatch.NewMemoryStore(), &dispatch.FixedClock{T: now}))
	if err := repo.UpsertFeedSyncState(context.Background(), &models.OutreachFeedSyncState{
		OrganizationID: orgID, LastSnapshotHash: "snap-abc", LastRunID: "run-abc",
		LastSuccessAt: &now, LastStatus: "completed",
		SourceGeneratedAt: &generatedAt, SourceExpiresAt: &expiresAt,
		TargetMembershipComplete: true, TargetMembershipHash: strings.Repeat("a", 64),
		TargetMembershipCount: 3, CommercialAuthorityJSON: marshalCommercialAuthority(&payload),
	}); err != nil {
		t.Fatal(err)
	}
	blocked := &models.OutreachAccount{ID: uuid.New(), OrganizationID: orgID, CNPJ14: "11111111000191", Blocked: true}
	dnc := &models.OutreachAccount{ID: uuid.New(), OrganizationID: orgID, CNPJ14: "22222222000191", DoNotContact: true}
	bounceAcc := &models.OutreachAccount{ID: uuid.New(), OrganizationID: orgID, CNPJ14: "33333333000191"}
	for _, acc := range []*models.OutreachAccount{blocked, dnc, bounceAcc} {
		if _, err := repo.UpsertAccount(context.Background(), acc); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.UpsertCandidate(context.Background(), &models.OutreachContactCandidate{
		OrganizationID: orgID, AccountID: bounceAcc.ID, Email: "bounce@example.test", Bounced: true,
	}); err != nil {
		t.Fatal(err)
	}

	report, xerr := svc.CollectFirstWindowReadiness(context.Background(), orgID)
	if xerr != nil || report == nil {
		t.Fatalf("collect: %+v err=%v", report, xerr)
	}
	if report.Verdict == FirstWindowGOForControlledPilot || strings.Contains(report.Verdict, FirstWindowGOForControlledPilot) {
		t.Fatal("emitted GO_FOR_CONTROLLED_EMAIL_PILOT")
	}
	if report.SourceHealth.State != SourceHealthStale {
		t.Fatalf("source=%s commercial=%+v", report.SourceHealth.State, report.CommercialAuthority)
	}
	if !report.CommercialAuthority.Present || report.CommercialAuthority.State != CommercialAuthorityCurrent {
		t.Fatalf("commercial masked by source: %+v", report.CommercialAuthority)
	}
	if !report.CommercialAuthority.ExistingBoundTouchTransportAllowed {
		t.Fatal("stale source collapsed existing-bound transport")
	}
	if report.PauseState == "" || report.PauseSource == "" {
		t.Fatalf("transport missing: pause=%s source=%s", report.PauseState, report.PauseSource)
	}
	if report.SuppressionCount != 1 || report.DNCCount != 1 || report.BounceCount != 1 {
		t.Fatalf("safety snapshot not from stored state: suppression=%d dnc=%d bounce=%d", report.SuppressionCount, report.DNCCount, report.BounceCount)
	}
}
