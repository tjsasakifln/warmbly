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
		CommercialAuthority: CommercialQualificationDecision{
			Present: true, State: CommercialQualified,
			PolicyVersion:  CommercialAuthorityPolicyV2,
			EvidenceHash:   strings.Repeat("e", 64),
			QualifiedUntil: &until,
		},
		MembershipHash:            strings.Repeat("a", 64),
		PolicyID:                  DelegatedFirstTouchPolicyV3,
		PolicyVersion:             DelegatedFirstTouchPolicyV3,
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

func TestEvaluateFirstWindowReadinessArmsWhenOnlySendWindowRemains(t *testing.T) {
	now := time.Date(2026, 8, 28, 8, 0, 0, 0, time.UTC)
	snap := readyFirstWindowSnapshot(now)
	snap.KillSwitchEngaged = false
	snap.PauseState = TransportPaused
	snap.PauseSource = TransportSourceSendWindow
	snap.Queued = 421
	got := EvaluateFirstWindowReadiness(snap)
	if got.Verdict != FirstWindowArmedForNextBusinessWindow {
		t.Fatalf("outside window after PRE-GO lift: %s blockers=%v", got.Verdict, got.Blockers)
	}
	if got.Verdict == FirstWindowGOForControlledPilot || strings.Contains(got.Verdict, "GO_FOR_CONTROLLED_EMAIL_PILOT") {
		t.Fatal("armed snapshot emitted GO_FOR_CONTROLLED_EMAIL_PILOT")
	}
}

func TestEvaluateFirstWindowReadinessStaysPreGOWhileKillSwitchEngaged(t *testing.T) {
	now := time.Date(2026, 8, 28, 8, 0, 0, 0, time.UTC)
	snap := readyFirstWindowSnapshot(now)
	snap.Queued = 421
	got := EvaluateFirstWindowReadiness(snap)
	if got.Verdict != FirstWindowReadyForGOAdjudication {
		t.Fatalf("PRE-GO kill switch still engaged: %s", got.Verdict)
	}
}

func TestEvaluateFirstWindowReadinessActiveInWindowWithoutPilotGO(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 30, 0, 0, time.UTC)
	snap := readyFirstWindowSnapshot(now)
	snap.KillSwitchEngaged = false
	snap.PauseState = TransportActive
	snap.PauseSource = TransportSourceSendWindow
	snap.Queued = 421
	got := EvaluateFirstWindowReadiness(snap)
	if got.Verdict != FirstWindowTransportActiveInWindow {
		t.Fatalf("in window after PRE-GO lift: %s blockers=%v", got.Verdict, got.Blockers)
	}
	if got.Verdict == FirstWindowGOForControlledPilot {
		t.Fatal("emitted GO_FOR_CONTROLLED_EMAIL_PILOT")
	}
}

func TestEvaluateFirstWindowReadinessDoesNotArmOnWorkerGuard(t *testing.T) {
	now := time.Date(2026, 8, 28, 8, 0, 0, 0, time.UTC)
	snap := readyFirstWindowSnapshot(now)
	snap.KillSwitchEngaged = false
	snap.PauseState = TransportPaused
	snap.PauseSource = PauseSourceWorkerGuard
	snap.Queued = 421
	got := EvaluateFirstWindowReadiness(snap)
	if got.Verdict != FirstWindowReadyForGOAdjudication {
		t.Fatalf("independent worker guard must not look armed: %s", got.Verdict)
	}
}

func TestCollectFirstWindowReadinessIndependentAuthorityOnStaleSource(t *testing.T) {
	now := time.Date(2026, 8, 27, 16, 0, 0, 0, time.UTC)
	orgID := uuid.New()
	repo := newMemRepo()
	generatedAt := now.Add(-48 * time.Hour)
	expiresAt := now.Add(2 * time.Hour)
	qualification := testRootQualification("11222333", now.AddDate(-1, 0, 0))
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
		TargetMembershipCount: 3,
		CommercialAuthorityV2JSON: testCommercialAuthorityV2JSON(
			"run-abc", "snap-abc", strings.Repeat("a", 64),
			strings.Repeat("s", 64), strings.Repeat("p", 64),
			[]RootQualification{qualification}),
		PublicationSemanticHash:   strings.Repeat("s", 64),
		ProducerIdentity:          strings.Repeat("p", 64),
		QualificationEvidenceHash: HashQualificationCorpus([]RootQualification{qualification}),
		QualifiedRootCount:        1,
		QualificationWindowYears:  QualificationWindowYears,
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
	if !report.CommercialAuthority.Present || report.CommercialAuthority.State != CommercialQualified {
		t.Fatalf("commercial masked by source: %+v", report.CommercialAuthority)
	}
	if !report.CommercialAuthority.AllowsTransport() {
		t.Fatal("stale source collapsed existing-bound transport")
	}
	if report.PauseState == "" || report.PauseSource == "" {
		t.Fatalf("transport missing: pause=%s source=%s", report.PauseState, report.PauseSource)
	}
	if report.SuppressionCount != 1 || report.DNCCount != 1 || report.BounceCount != 1 {
		t.Fatalf("safety snapshot not from stored state: suppression=%d dnc=%d bounce=%d", report.SuppressionCount, report.DNCCount, report.BounceCount)
	}
}

// A population every one of whose members carries verified three-year evidence
// is qualified even when the producer has published no population attestation.
// Per-company evidence is stronger than an attestation, not weaker.
func TestReadinessAcceptsAccountDerivedQualificationWithoutFeedAttestation(t *testing.T) {
	now := time.Date(2026, 8, 28, 16, 0, 0, 0, time.UTC)
	snap := readyFirstWindowSnapshot(now)
	snap.SourceHealth = SourceHealthDecision{State: SourceHealthStale}
	snap.CommercialAuthority = CommercialQualificationDecision{
		Present:       true,
		State:         CommercialQualified,
		PolicyVersion: CommercialAuthorityPolicyV2,
		// Account-derived rollups carry no corpus digest.
		EvidenceHash: "",
	}
	report := EvaluateFirstWindowReadiness(snap)
	for _, blocker := range report.Blockers {
		if strings.Contains(blocker, "fresh") || strings.Contains(blocker, "stale") ||
			blocker == ReasonQualificationEvidenceDrift || blocker == ReasonQualificationMissing {
			t.Fatalf("account-derived qualification blocked: %v", report.Blockers)
		}
	}
	if strings.HasPrefix(report.Verdict, "BLOCKED") {
		t.Fatalf("verdict %s blockers=%v", report.Verdict, report.Blockers)
	}
}

// An ACTIVE transport has nothing paused, so it has no pause source. Demanding
// one blocked exactly the state the cutover is trying to reach.
func TestReadinessActiveTransportNeedsNoPauseSource(t *testing.T) {
	now := time.Date(2026, 8, 28, 16, 0, 0, 0, time.UTC)
	snap := readyFirstWindowSnapshot(now)
	snap.PauseState = TransportActive
	snap.PauseSource = ""
	snap.KillSwitchEngaged = false
	snap.Queued = 3
	report := EvaluateFirstWindowReadiness(snap)
	if containsStr(report.Blockers, "pause_source_missing") {
		t.Fatalf("an unpaused transport was blocked for having no pause source: %v", report.Blockers)
	}
	if report.Verdict != FirstWindowTransportActiveInWindow {
		t.Fatalf("verdict=%s blockers=%v", report.Verdict, report.Blockers)
	}
	// Still fail closed when actually paused with an unreadable source.
	snap.PauseState = TransportPaused
	snap.PauseSource = ""
	if !containsStr(EvaluateFirstWindowReadiness(snap).Blockers, "pause_source_missing") {
		t.Fatal("a paused transport with no readable source stopped failing closed")
	}
}
