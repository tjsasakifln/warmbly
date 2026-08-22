package confenge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// RecordControlledEmailGOReview stores the founder's explicit decision for
// one frozen grant. READY_FOR_CONTROLLED_EMAIL_GO_REVIEW is the human
// verdict. The evaluator emits GO_FOR_CONTROLLED_EMAIL_PILOT only when every
// required live check is PASS. Auto-send stays false.
func RecordControlledEmailGOReview(
	ctx context.Context,
	store BoundedCohortStore,
	id, actor uuid.UUID,
	verdict, reason string,
	live ReleaseManifest,
	now time.Time,
) (*BoundedCohortAuthorization, error) {
	if store == nil {
		return nil, ErrCohortStoreUnavailable
	}
	if actor == uuid.Nil {
		return nil, ErrCohortHumanActor
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	verdict = strings.TrimSpace(verdict)
	switch verdict {
	case ReleaseReadyForControlledEmailReview, ReleaseNOGO, ReleaseGOForControlledEmailPilot:
	case ReleaseGO:
		return nil, fmt.Errorf("GO_FOR_CONTROLLED_PILOT is not a valid controlled-email operator verdict")
	default:
		return nil, fmt.Errorf("verdict must be %s, %s, or %s", ReleaseReadyForControlledEmailReview, ReleaseGOForControlledEmailPilot, ReleaseNOGO)
	}
	auth, err := store.GetGrant(ctx, id)
	if err != nil {
		return nil, err
	}
	if auth == nil {
		return nil, ErrCohortGrantMissing
	}
	if auth.RevokedAt != nil {
		return nil, ErrCohortGrantRevoked
	}
	if !auth.EffectiveExpiry().IsZero() && !now.Before(auth.EffectiveExpiry()) {
		return nil, ErrCohortGrantExpired
	}
	if containsRiskyClass(auth.AllowedRouteClasses) {
		return nil, fmt.Errorf("RISKY is incompatible with the default pilot review")
	}
	if auth.AutoSendEnabled || auth.GreenAutorunEnabled {
		return nil, fmt.Errorf("bounded cohort cannot enable auto-send")
	}
	if verdict == ReleaseReadyForControlledEmailReview || verdict == ReleaseGOForControlledEmailPilot {
		want := expectedReleaseFromGrant(auth)
		// Missing live evidence is a NO_GO.
		// Never synthesize release readiness from expected state.
		v := EvaluateControlledEmailRelease(want, live)
		if v.Verdict != ReleaseGOForControlledEmailPilot {
			return nil, fmt.Errorf("review refused: %s", strings.Join(v.Reasons, ","))
		}
		// Human records READY; the evaluator emits the production GO.
		if verdict == ReleaseReadyForControlledEmailReview {
			verdict = ReleaseReadyForControlledEmailReview
		}
	}
	if err := store.RecordGOReview(ctx, id, actor, verdict, reason, now); err != nil {
		return nil, err
	}
	auth.GOReviewVerdict = verdict
	auth.GOReviewActor = actor
	ts := now.UTC()
	auth.GOReviewAt = &ts
	auth.GOReviewReason = strings.TrimSpace(reason)
	return auth, nil
}

// PrepareControlledEmailGOReview loads the grant, collects live evidence, and
// compares it to the frozen expected state. It does not persist a verdict.
func PrepareControlledEmailGOReview(ctx context.Context, in LiveReleaseInput, id uuid.UUID) (*ReleaseComparison, *BoundedCohortAuthorization, error) {
	if in.Store == nil {
		return nil, nil, ErrCohortStoreUnavailable
	}
	if id == uuid.Nil {
		return nil, nil, ErrCohortGrantMissing
	}
	auth, err := in.Store.GetGrant(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if auth == nil {
		return nil, nil, ErrCohortGrantMissing
	}
	in.Auth = auth
	live := CollectLiveReleaseManifest(ctx, in)
	cmp := CompareControlledEmailRelease(expectedReleaseFromGrant(auth), live)
	cmp.AuthorizationID = auth.ID
	if auth.RevokedAt != nil {
		cmp.Verdict = ReleaseVerdict{Verdict: ReleaseNOGO, Reasons: append([]string{"authorization_revoked"}, cmp.Verdict.Reasons...)}
	} else if !auth.EffectiveExpiry().IsZero() && (in.Now.IsZero() || !in.Now.Before(auth.EffectiveExpiry())) {
		now := in.Now
		if now.IsZero() {
			now = time.Now().UTC()
		}
		if !now.Before(auth.EffectiveExpiry()) {
			cmp.Verdict = ReleaseVerdict{Verdict: ReleaseNOGO, Reasons: append([]string{"authorization_expired"}, cmp.Verdict.Reasons...)}
		}
	}
	return &cmp, auth, nil
}

func expectedReleaseFromGrant(auth *BoundedCohortAuthorization) ReleaseManifest {
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
		RepositorySHA:       auth.RepositorySHA,
		Schema:              auth.FeedSchemaVersion,
		FeedHash:            feed,
		CohortHash:          auth.CohortHash,
		RecipientSetHash:    auth.RecipientSetHash,
		PolicyVersion:       auth.PolicyVersion,
		ComposerVersion:     auth.ComposerVersion,
		AllowedRouteClasses: append([]string{}, auth.AllowedRouteClasses...),
		VolumeCap:           auth.MaxDailyVolume,
		EvidenceVersion:     auth.EvidenceVersion,
	}
}

// FormatReleaseComparison is the founder-readable GO-review preview.
func FormatReleaseComparison(cmp *ReleaseComparison) string {
	if cmp == nil {
		return "release_verdict=" + ReleaseNOGO + "\nreason=comparison_missing\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "authorization_id=%s\n", cmp.AuthorizationID)
	if cmp.Want.CohortHash != "" {
		fmt.Fprintf(&b, "cohort_hash=%s\n", cmp.Got.CohortHash)
	}
	if cmp.Want.PolicyVersion != "" || cmp.Got.PolicyVersion != "" {
		fmt.Fprintf(&b, "policy_version.expected=%s\n", cmp.Want.PolicyVersion)
	}
	if cmp.Want.VolumeCap > 0 || cmp.Got.VolumeCap > 0 {
		fmt.Fprintf(&b, "volume_cap.expected=%s\n", itoa(cmp.Want.VolumeCap))
	}
	for _, ch := range cmp.Checks {
		switch ch.Name {
		case "repository_sha":
			fmt.Fprintf(&b, "repository_sha.expected=%s\n", ch.Expected)
			fmt.Fprintf(&b, "repository_sha.live=%s\n", ch.Live)
			fmt.Fprintf(&b, "repository_sha=%s\n", ch.State.Label())
		case "auto_send", "green_autorun", "sending_paused":
			fmt.Fprintf(&b, "%s=%s\n", ch.Name, ch.Live)
		default:
			fmt.Fprintf(&b, "%s=%s\n", ch.Name, ch.State.Label())
		}
	}
	fmt.Fprintf(&b, "release_verdict=%s\n", cmp.Verdict.Verdict)
	if cmp.Verdict.Verdict == ReleaseGOForControlledEmailPilot {
		fmt.Fprintf(&b, "production_verdict=%s\n", ReleaseGOForControlledEmailPilot)
		fmt.Fprintf(&b, "repository_sha=%s\n", cmp.Got.RepositorySHA)
		fmt.Fprintf(&b, "policy_version=%s\n", cmp.Got.PolicyVersion)
		fmt.Fprintf(&b, "volume_cap=%s\n", itoa(cmp.Got.VolumeCap))
	}
	if cmp.Verdict.Verdict != ReleaseGOForControlledEmailPilot && len(cmp.Verdict.Reasons) > 0 {
		fmt.Fprintf(&b, "reason=%s\n", strings.Join(cmp.Verdict.Reasons, ","))
	}
	return b.String()
}

// RequireControlledEmailGO refuses dispatch unless the live comparison is
// GO_FOR_CONTROLLED_EMAIL_PILOT with every required check PASS.
func RequireControlledEmailGO(cmp *ReleaseComparison) error {
	if cmp == nil {
		return fmt.Errorf("live GO review missing")
	}
	if cmp.Verdict.Verdict != ReleaseGOForControlledEmailPilot {
		if len(cmp.Verdict.Reasons) > 0 {
			return fmt.Errorf("live GO refused: %s", strings.Join(cmp.Verdict.Reasons, ","))
		}
		return fmt.Errorf("live GO refused: %s", cmp.Verdict.Verdict)
	}
	for _, ch := range cmp.Checks {
		if !ch.State.IsPass() {
			return fmt.Errorf("live GO refused: %s=%s", ch.Name, ch.State.Label())
		}
	}
	return nil
}
