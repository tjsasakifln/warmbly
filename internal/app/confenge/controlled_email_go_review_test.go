package confenge

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/models"
)

// TestEmptyLiveReleaseManifestFailsClosed is the regression gate for the
// fail-open that copied frozen expected state into live readiness flags.
func TestEmptyLiveReleaseManifestFailsClosed(t *testing.T) {
	now := time.Now().UTC()
	store := NewMemoryCohortStore()
	auth := sampleGOReviewGrant(t, now)
	if err := store.PutGrant(context.Background(), auth); err != nil {
		t.Fatal(err)
	}
	got, err := RecordControlledEmailGOReview(context.Background(), store, auth.ID, auth.ActorID,
		ReleaseReadyForControlledEmailReview, "empty live", ReleaseManifest{}, now)
	if err == nil {
		t.Fatal("empty live ReleaseManifest must not record READY")
	}
	if !strings.Contains(err.Error(), "review refused") {
		t.Fatalf("want review refused, got %v", err)
	}
	if got != nil && got.GOReviewVerdict == ReleaseReadyForControlledEmailReview {
		t.Fatal("must not persist READY_FOR_CONTROLLED_EMAIL_GO_REVIEW")
	}
	persisted, _ := store.GetGrant(context.Background(), auth.ID)
	if persisted != nil && persisted.GOReviewVerdict == ReleaseReadyForControlledEmailReview {
		t.Fatal("store must not persist READY from empty live evidence")
	}
	if EvaluateControlledEmailRelease(expectedReleaseFromGrant(auth), ReleaseManifest{}).Verdict != ReleaseNOGO {
		t.Fatal("empty live must evaluate NO_GO")
	}
}

func TestControlledEmailGOReviewAdversarialFailClosed(t *testing.T) {
	now := time.Now().UTC()
	mustNOGO := func(t *testing.T, live ReleaseManifest, wantReason string) {
		t.Helper()
		store := NewMemoryCohortStore()
		auth := sampleGOReviewGrant(t, now)
		if err := store.PutGrant(context.Background(), auth); err != nil {
			t.Fatal(err)
		}
		got, err := RecordControlledEmailGOReview(context.Background(), store, auth.ID, auth.ActorID,
			ReleaseReadyForControlledEmailReview, "probe", live, now)
		if err == nil || (got != nil && got.GOReviewVerdict == ReleaseReadyForControlledEmailReview) {
			t.Fatalf("expected NO_GO, recorded %+v err=%v", got, err)
		}
		v := EvaluateControlledEmailRelease(expectedReleaseFromGrant(auth), live)
		if v.Verdict != ReleaseNOGO {
			t.Fatalf("verdict=%s reasons=%v", v.Verdict, v.Reasons)
		}
		if v.Verdict == ReleaseGOForControlledEmailPilot {
			t.Fatal("must never emit GO_FOR_CONTROLLED_EMAIL_PILOT")
		}
		if wantReason != "" && !containsStr(v.Reasons, wantReason) {
			t.Fatalf("want reason %s in %v", wantReason, v.Reasons)
		}
	}

	t.Run("1_empty_manifest", func(t *testing.T) {
		mustNOGO(t, ReleaseManifest{}, "repository_sha_missing")
	})
	t.Run("2_missing_repository_sha", func(t *testing.T) {
		live := matchingControlledEmailLive(sampleGOReviewGrant(t, now))
		live.RepositorySHA = ""
		mustNOGO(t, live, "repository_sha_missing")
	})
	t.Run("3_missing_smtp", func(t *testing.T) {
		live := matchingControlledEmailLive(sampleGOReviewGrant(t, now))
		live.SMTPReady = EvidenceUnknown
		mustNOGO(t, live, "smtp_readiness_not_proven")
	})
	t.Run("4_missing_observability", func(t *testing.T) {
		live := matchingControlledEmailLive(sampleGOReviewGrant(t, now))
		live.ObservabilityReady = EvidenceUnknown
		mustNOGO(t, live, "observability_not_proven")
	})
	t.Run("5_missing_db_authority", func(t *testing.T) {
		live := matchingControlledEmailLive(sampleGOReviewGrant(t, now))
		live.DBCohortAuthority = EvidenceUnknown
		mustNOGO(t, live, "db_cohort_authority_not_proven")
	})
	t.Run("6_unknown_suppression", func(t *testing.T) {
		live := matchingControlledEmailLive(sampleGOReviewGrant(t, now))
		live.SuppressionClear = EvidenceUnknown
		mustNOGO(t, live, "suppression_not_proven")
	})
	t.Run("7_expired_grant", func(t *testing.T) {
		store := NewMemoryCohortStore()
		auth := sampleGOReviewGrant(t, now)
		auth.ExpiresAt = now.Add(-time.Minute)
		auth.TTL = time.Minute
		if err := store.PutGrant(context.Background(), auth); err != nil {
			t.Fatal(err)
		}
		_, err := RecordControlledEmailGOReview(context.Background(), store, auth.ID, auth.ActorID,
			ReleaseReadyForControlledEmailReview, "expired", matchingControlledEmailLive(auth), now)
		if err == nil {
			t.Fatal("expired grant must be NO_GO")
		}
		if err != ErrCohortGrantExpired && !strings.Contains(err.Error(), "expired") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("8_revoked_grant", func(t *testing.T) {
		store := NewMemoryCohortStore()
		auth := sampleGOReviewGrant(t, now)
		if err := store.PutGrant(context.Background(), auth); err != nil {
			t.Fatal(err)
		}
		if err := store.RevokeGrant(context.Background(), auth.ID, auth.ActorID, "stop", now); err != nil {
			t.Fatal(err)
		}
		_, err := RecordControlledEmailGOReview(context.Background(), store, auth.ID, auth.ActorID,
			ReleaseReadyForControlledEmailReview, "revoked", matchingControlledEmailLive(auth), now)
		if err == nil {
			t.Fatal("revoked grant must be NO_GO")
		}
		if err != ErrCohortGrantRevoked {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("9_sha_drift", func(t *testing.T) {
		live := matchingControlledEmailLive(sampleGOReviewGrant(t, now))
		live.RepositorySHA = "other"
		mustNOGO(t, live, "sha_drift")
	})
	t.Run("10_feed_drift", func(t *testing.T) {
		live := matchingControlledEmailLive(sampleGOReviewGrant(t, now))
		live.FeedHash = "other-feed"
		mustNOGO(t, live, "feed_drift")
	})
	t.Run("11_cohort_hash_drift", func(t *testing.T) {
		live := matchingControlledEmailLive(sampleGOReviewGrant(t, now))
		live.CohortHash = "other-cohort"
		mustNOGO(t, live, "cohort_hash_drift")
	})
	t.Run("12_recipient_set_drift", func(t *testing.T) {
		live := matchingControlledEmailLive(sampleGOReviewGrant(t, now))
		live.RecipientSetHash = HashRecipientSet([]string{"other@x.com"})
		mustNOGO(t, live, "recipient_set_drift")
	})
	t.Run("13_policy_drift", func(t *testing.T) {
		live := matchingControlledEmailLive(sampleGOReviewGrant(t, now))
		live.PolicyVersion = "other-policy"
		mustNOGO(t, live, "policy_drift")
	})
	t.Run("14_composer_drift", func(t *testing.T) {
		live := matchingControlledEmailLive(sampleGOReviewGrant(t, now))
		live.ComposerVersion = "other-composer"
		mustNOGO(t, live, "composer_drift")
	})
	t.Run("15_evidence_version_drift", func(t *testing.T) {
		live := matchingControlledEmailLive(sampleGOReviewGrant(t, now))
		live.EvidenceVersion = "other-evidence"
		mustNOGO(t, live, "evidence_drift")
	})
	t.Run("16_risky_class", func(t *testing.T) {
		store := NewMemoryCohortStore()
		auth := sampleGOReviewGrant(t, now)
		auth.AllowedRouteClasses = []string{RouteClassProbabilisticOrRisky}
		if err := store.PutGrant(context.Background(), auth); err != nil {
			t.Fatal(err)
		}
		_, err := RecordControlledEmailGOReview(context.Background(), store, auth.ID, auth.ActorID,
			ReleaseReadyForControlledEmailReview, "risky", matchingControlledEmailLive(auth), now)
		if err == nil {
			t.Fatal("RISKY must be NO_GO")
		}
	})
	t.Run("17_auto_send_true", func(t *testing.T) {
		live := matchingControlledEmailLive(sampleGOReviewGrant(t, now))
		live.AutoSend = true
		live.AutoSendState = EvidenceFail
		mustNOGO(t, live, "auto_send_enabled")
	})
	t.Run("18_green_autorun_true", func(t *testing.T) {
		live := matchingControlledEmailLive(sampleGOReviewGrant(t, now))
		live.GreenAutorun = true
		live.GreenAutorunState = EvidenceFail
		mustNOGO(t, live, "green_autorun_enabled")
	})
	t.Run("19_kill_switch_engaged", func(t *testing.T) {
		live := matchingControlledEmailLive(sampleGOReviewGrant(t, now))
		live.SendingPaused = true
		live.SendingPausedState = EvidenceFail
		mustNOGO(t, live, "sending_paused")
	})
	t.Run("20_all_checks_matching_ready", func(t *testing.T) {
		store := NewMemoryCohortStore()
		auth := sampleGOReviewGrant(t, now)
		if err := store.PutGrant(context.Background(), auth); err != nil {
			t.Fatal(err)
		}
		live := matchingControlledEmailLive(auth)
		got, err := RecordControlledEmailGOReview(context.Background(), store, auth.ID, auth.ActorID,
			ReleaseReadyForControlledEmailReview, "founder", live, now)
		if err != nil {
			t.Fatal(err)
		}
		if got.GOReviewVerdict != ReleaseReadyForControlledEmailReview {
			t.Fatalf("verdict=%s", got.GOReviewVerdict)
		}
		if got.GOReviewVerdict == ReleaseGOForControlledEmailPilot || got.GOReviewVerdict == ReleaseGO {
			t.Fatal("must never emit GO_FOR_CONTROLLED_EMAIL_PILOT")
		}
		v := EvaluateControlledEmailRelease(expectedReleaseFromGrant(auth), live)
		if v.Verdict != ReleaseReadyForControlledEmailReview {
			t.Fatalf("eval %s %v", v.Verdict, v.Reasons)
		}
		if v.Verdict == ReleaseGOForControlledEmailPilot {
			t.Fatal("evaluator must never emit live GO")
		}
	})
	t.Run("21_explicit_human_nogo_without_fake_readiness", func(t *testing.T) {
		store := NewMemoryCohortStore()
		auth := sampleGOReviewGrant(t, now)
		if err := store.PutGrant(context.Background(), auth); err != nil {
			t.Fatal(err)
		}
		got, err := RecordControlledEmailGOReview(context.Background(), store, auth.ID, auth.ActorID,
			ReleaseNOGO, "operator no-go", ReleaseManifest{}, now)
		if err != nil {
			t.Fatal(err)
		}
		if got.GOReviewVerdict != ReleaseNOGO {
			t.Fatalf("verdict=%s", got.GOReviewVerdict)
		}
	})
}

func TestLiveCollectorUnknownBlocksReady(t *testing.T) {
	t.Setenv("SMTP_HOST", "")
	t.Setenv(EnvRepositorySHA, "")
	t.Setenv(EnvKillSwitchPath, filepath.Join(t.TempDir(), "absent-kill"))
	t.Setenv(EnvAutoSend, "false")
	t.Setenv(EnvGreenAutorun, "false")
	now := time.Now().UTC()
	store := NewMemoryCohortStore()
	auth := sampleGOReviewGrant(t, now)
	if err := store.PutGrant(context.Background(), auth); err != nil {
		t.Fatal(err)
	}
	cfg := Config{RepositorySHA: "", AutoSendEnabled: false, GreenAutorunEnabled: false}
	live := CollectLiveReleaseManifest(context.Background(), LiveReleaseInput{
		Now: now, Config: &cfg, Store: store, Auth: auth,
	})
	if live.SMTPReady.IsPass() {
		t.Fatal("SMTP must stay UNKNOWN when host is unproven")
	}
	if live.DBCohortAuthority.IsPass() {
		t.Fatal("memory store must not prove PostgreSQL authority")
	}
	if live.SuppressionClear.IsPass() {
		t.Fatal("suppression without repo must stay UNKNOWN")
	}
	if live.SMTPReady.Label() != "UNKNOWN" {
		t.Fatalf("smtp=%s", live.SMTPReady.Label())
	}
	v := EvaluateControlledEmailRelease(expectedReleaseFromGrant(auth), live)
	if v.Verdict != ReleaseNOGO {
		t.Fatalf("unproven collector must NO_GO: %s %v", v.Verdict, v.Reasons)
	}
	if !containsStr(v.Reasons, "smtp_readiness_not_proven") && !containsStr(v.Reasons, "db_cohort_authority_not_proven") {
		t.Fatalf("reasons=%v", v.Reasons)
	}
}

func TestLiveCollectorSMTPDialPassAndJSONBoolUnknown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	t.Setenv("SMTP_HOST", "127.0.0.1")
	t.Setenv("SMTP_PORT", port)
	st, reason := ProbeSMTPReadiness(time.Second)
	if st != EvidencePass {
		t.Fatalf("dial proof: %s %s", st.Label(), reason)
	}

	var got ReleaseManifest
	if err := json.Unmarshal([]byte(`{"smtp_ready":true,"observability_ready":false}`), &got); err != nil {
		t.Fatal(err)
	}
	if got.SMTPReady.IsPass() || got.ObservabilityReady.IsPass() {
		t.Fatal("JSON booleans must not become PASS")
	}
}

func TestPostFreezeSuppressionFailsClosedWithoutHashRewrite(t *testing.T) {
	now := time.Now().UTC()
	auth := sampleGOReviewGrant(t, now)
	frozenHash := auth.CohortHash
	repo := newMemRepo()
	member := auth.FrozenManifest.Members[0]
	cand := &models.OutreachContactCandidate{
		ID: member.CandidateID, OrganizationID: auth.OrganizationID, AccountID: member.AccountID,
		Email: member.Mailbox, Bounced: true, DiscoveryJSON: discoveryJSON(t, controlledDiscovery{RouteClass: member.RouteClass}),
	}
	_, _ = repo.UpsertCandidate(context.Background(), cand)
	st, reason := probeSuppressionClear(context.Background(), repo, auth, now)
	if st != EvidenceFail || reason != "cohort_member_suppressed_after_freeze" {
		t.Fatalf("st=%s reason=%s", st.Label(), reason)
	}
	if HashFrozenMembership(auth.FrozenManifest.Members) != frozenHash {
		t.Fatal("post-freeze suppression must not rewrite the frozen hash")
	}
	live := matchingControlledEmailLive(auth)
	live.SuppressionClear = st
	live.SuppressionReason = reason
	v := EvaluateControlledEmailRelease(expectedReleaseFromGrant(auth), live)
	if v.Verdict != ReleaseNOGO || !containsStr(v.Reasons, "cohort_member_suppressed_after_freeze") {
		t.Fatalf("verdict=%s reasons=%v", v.Verdict, v.Reasons)
	}
}

func TestKillSwitchEngagedCollectorIsNOGO(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kill")
	t.Setenv(EnvKillSwitchPath, path)
	if err := os.WriteFile(path, []byte("paused\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	op, paused, pausedState := probeKillSwitch(Config{})
	if op != EvidencePass || !paused || pausedState != EvidenceFail {
		t.Fatalf("operational=%s paused=%v state=%s", op.Label(), paused, pausedState.Label())
	}
	auth := sampleGOReviewGrant(t, time.Now().UTC())
	live := matchingControlledEmailLive(auth)
	live.KillSwitchOperational = op
	live.SendingPaused = paused
	live.SendingPausedState = pausedState
	v := EvaluateControlledEmailRelease(expectedReleaseFromGrant(auth), live)
	if v.Verdict != ReleaseNOGO || !containsStr(v.Reasons, "sending_paused") {
		t.Fatalf("engaged kill switch must be NO_GO: %s %v", v.Verdict, v.Reasons)
	}
}

func TestCLIReviewFormatterPreviewNotPersisted(t *testing.T) {
	now := time.Now().UTC()
	auth := sampleGOReviewGrant(t, now)
	store := NewMemoryCohortStore()
	if err := store.PutGrant(context.Background(), auth); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		RepositorySHA: auth.RepositorySHA, FeedSchemaVersion: auth.FeedSchemaVersion,
		EvidenceVersion: auth.EvidenceVersion,
	}
	cmp, _, err := PrepareControlledEmailGOReview(context.Background(), LiveReleaseInput{
		Now: now, Config: &cfg, Store: store, Auth: auth,
	}, auth.ID)
	if err != nil {
		t.Fatal(err)
	}
	text := FormatReleaseComparison(cmp)
	for _, want := range []string{
		"authorization_id=" + auth.ID.String(),
		"repository_sha.expected=",
		"repository_sha.live=",
		"smtp_ready=",
		"observability_ready=",
		"db_cohort_authority=",
		"suppression_clear=",
		"ttl_valid=",
		"kill_switch_available=",
		"auto_send=false",
		"green_autorun=false",
		"release_verdict=",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
	if strings.Contains(text, ReleaseGOForControlledEmailPilot) {
		t.Fatal("formatter must not emit live GO")
	}
	persisted, _ := store.GetGrant(context.Background(), auth.ID)
	if persisted.GOReviewVerdict != "" {
		t.Fatal("prepare must not persist a verdict")
	}

	ready := matchingControlledEmailLive(auth)
	cmpReady := CompareControlledEmailRelease(expectedReleaseFromGrant(auth), ready)
	cmpReady.AuthorizationID = auth.ID
	readyText := FormatReleaseComparison(&cmpReady)
	if !strings.Contains(readyText, "release_verdict="+ReleaseReadyForControlledEmailReview) {
		t.Fatalf("ready preview:\n%s", readyText)
	}
	if !strings.Contains(readyText, "repository_sha=PASS") || !strings.Contains(readyText, "smtp_ready=PASS") {
		t.Fatalf("ready checks:\n%s", readyText)
	}
}

func TestObservabilityWiringUsesSnapshotNotHandBuiltEvent(t *testing.T) {
	auth := sampleGOReviewGrant(t, time.Now().UTC())
	st, reason := ProveObservabilityWiring(auth)
	if st != EvidencePass {
		t.Fatalf("wiring %s %s", st.Label(), reason)
	}
	ev := intel.CommercialEvent{Type: intel.EventEmailAttempted}
	if ev.CohortID != "" || ev.EmailRouteClass != "" {
		t.Fatal("probe must start from an empty event")
	}
}

func matchingControlledEmailLive(auth *BoundedCohortAuthorization) ReleaseManifest {
	if auth == nil {
		return ReleaseManifest{}
	}
	feed := ""
	if auth.FrozenManifest != nil {
		feed = firstNonEmpty(auth.FrozenManifest.SnapshotHash, auth.FrozenManifest.FeedIdentity)
	}
	if feed == "" {
		feed = firstNonEmpty(auth.RecipientSetHash, auth.FeedSchemaVersion)
	}
	return ReleaseManifest{
		RepositorySHA:         auth.RepositorySHA,
		Schema:                auth.FeedSchemaVersion,
		FeedHash:              feed,
		CohortHash:            auth.CohortHash,
		RecipientSetHash:      auth.RecipientSetHash,
		PolicyVersion:         auth.PolicyVersion,
		ComposerVersion:       auth.ComposerVersion,
		AllowedRouteClasses:   append([]string{}, auth.AllowedRouteClasses...),
		VolumeCap:             auth.MaxDailyVolume,
		EvidenceVersion:       auth.EvidenceVersion,
		SMTPReady:             EvidencePass,
		ObservabilityReady:    EvidencePass,
		TTLValid:              EvidencePass,
		SuppressionClear:      EvidencePass,
		DBCohortAuthority:     EvidencePass,
		KillSwitchOperational: EvidencePass,
		SendingPausedState:    EvidencePass,
		AutoSendState:         EvidencePass,
		GreenAutorunState:     EvidencePass,
	}
}

func sampleGOReviewGrant(t *testing.T, now time.Time) *BoundedCohortAuthorization {
	t.Helper()
	mailbox := "contato@empresa.com.br"
	auth := &BoundedCohortAuthorization{
		ID: uuid.New(), OrganizationID: uuid.New(), ActorID: uuid.New(),
		AuthorizedAt: now, RepositorySHA: "sha-live",
		FeedSchemaVersion: models.OutreachSchemaV1, CohortID: "cohort-review-1",
		PolicyVersion: BoundedCohortPolicyV1, AllowedRouteClasses: []string{RouteClassGenericCompany},
		MaxDailyVolume: 10, ComposerVersion: ComposerVersion, EvidenceVersion: DefaultEvidenceVersion,
		TTL: time.Hour, ExpiresAt: now.Add(time.Hour),
	}
	auth.FrozenManifest = &FrozenCohortSnapshot{
		SnapshotHash: "feed-1", FeedIdentity: "feed-1",
		Members: []FrozenCohortMember{{
			AccountID: uuid.New(), AccountRef: "acc-1", CandidateID: uuid.New(),
			Mailbox: mailbox, RouteClass: RouteClassGenericCompany, TouchpointID: uuid.New(),
		}},
	}
	auth.CohortHash = HashFrozenMembership(auth.FrozenManifest.Members)
	auth.RecipientSetHash = HashRecipientSet([]string{mailbox})
	auth.FrozenHashValue = auth.FrozenHash()
	return auth
}
