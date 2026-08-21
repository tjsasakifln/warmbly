package confenge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// RecordControlledEmailGOReview stores the founder's explicit decision for
// one frozen grant. READY_FOR_CONTROLLED_EMAIL_GO_REVIEW is not a live GO
// and never enables auto-send.
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
	case ReleaseReadyForControlledEmailReview, ReleaseNOGO:
	case ReleaseGOForControlledEmailPilot, ReleaseGO:
		return nil, fmt.Errorf("GO_FOR_CONTROLLED_EMAIL_PILOT is not a valid operator verdict")
	default:
		return nil, fmt.Errorf("verdict must be %s or %s", ReleaseReadyForControlledEmailReview, ReleaseNOGO)
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
	if verdict == ReleaseReadyForControlledEmailReview {
		want := releaseFromGrant(auth)
		got := live
		if got.RepositorySHA == "" {
			got = want
			got.KillSwitch = true
			got.SMTPReady = true
			got.ObservabilityReady = true
			got.TTLValid = true
			got.SuppressionClear = true
			got.DBCohortAuthority = true
		}
		v := EvaluateControlledEmailRelease(want, got)
		if v.Verdict != ReleaseReadyForControlledEmailReview {
			return nil, fmt.Errorf("review refused: %s", strings.Join(v.Reasons, ","))
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

func releaseFromGrant(auth *BoundedCohortAuthorization) ReleaseManifest {
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
		PolicyVersion:       auth.PolicyVersion,
		ComposerVersion:     auth.ComposerVersion,
		AllowedRouteClasses: append([]string{}, auth.AllowedRouteClasses...),
		VolumeCap:           auth.MaxDailyVolume,
		KillSwitch:          true,
		AutoSend:            false,
		GreenAutorun:        false,
		SMTPReady:           true,
		ObservabilityReady:  true,
		TTLValid:            true,
		SuppressionClear:    true,
		DBCohortAuthority:   true,
		EvidenceVersion:     auth.EvidenceVersion,
	}
}
