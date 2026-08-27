package confenge

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
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
