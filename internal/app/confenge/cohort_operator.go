package confenge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// CohortGrantSummary is the operator-facing view of a frozen grant.
type CohortGrantSummary struct {
	ID                  uuid.UUID  `json:"id"`
	OrganizationID      uuid.UUID  `json:"organization_id"`
	ActorID             uuid.UUID  `json:"actor_id"`
	AuthorizedAt        time.Time  `json:"authorized_at"`
	RepositorySHA       string     `json:"repository_sha"`
	FeedSchemaVersion   string     `json:"feed_schema_version"`
	CohortID            string     `json:"cohort_id"`
	CohortHash          string     `json:"cohort_hash"`
	PolicyVersion       string     `json:"policy_version"`
	AllowedRouteClasses []string   `json:"allowed_route_classes"`
	MaxDailyVolume      int        `json:"max_daily_volume"`
	RecipientSetHash    string     `json:"recipient_set_hash"`
	ComposerVersion     string     `json:"composer_version"`
	EvidenceVersion     string     `json:"evidence_version"`
	TTLSeconds          int64      `json:"ttl_seconds"`
	ExpiresAt           time.Time  `json:"expires_at"`
	FrozenHash          string     `json:"frozen_hash"`
	RevokedAt           *time.Time `json:"revoked_at,omitempty"`
	RevokeActor         uuid.UUID  `json:"revoke_actor,omitempty"`
	RevokeReason        string     `json:"revoke_reason,omitempty"`
}

func (a *BoundedCohortAuthorization) Summary() CohortGrantSummary {
	if a == nil {
		return CohortGrantSummary{}
	}
	return CohortGrantSummary{
		ID: a.ID, OrganizationID: a.OrganizationID, ActorID: a.ActorID,
		AuthorizedAt: a.AuthorizedAt, RepositorySHA: a.RepositorySHA,
		FeedSchemaVersion: a.FeedSchemaVersion, CohortID: a.CohortID, CohortHash: a.CohortHash,
		PolicyVersion: a.PolicyVersion, AllowedRouteClasses: append([]string{}, a.AllowedRouteClasses...),
		MaxDailyVolume: a.MaxDailyVolume, RecipientSetHash: a.RecipientSetHash,
		ComposerVersion: a.ComposerVersion, EvidenceVersion: a.EvidenceVersion,
		TTLSeconds: int64(a.TTL / time.Second), ExpiresAt: a.EffectiveExpiry(),
		FrozenHash: a.FrozenHash(), RevokedAt: a.RevokedAt, RevokeActor: a.RevokeActor,
		RevokeReason: a.RevokeReason,
	}
}

func (s CohortGrantSummary) String() string {
	rev := "active"
	if s.RevokedAt != nil {
		rev = "revoked:" + s.RevokeReason
	}
	return fmt.Sprintf(
		"id=%s org=%s actor=%s cohort=%s hash=%s sha=%s schema=%s policy=%s composer=%s evidence=%s cap=%d classes=%s expires=%s frozen=%s status=%s",
		s.ID, s.OrganizationID, s.ActorID, s.CohortID, s.CohortHash, s.RepositorySHA, s.FeedSchemaVersion,
		s.PolicyVersion, s.ComposerVersion, s.EvidenceVersion, s.MaxDailyVolume,
		strings.Join(s.AllowedRouteClasses, ","), s.ExpiresAt.UTC().Format(time.RFC3339),
		s.FrozenHash, rev,
	)
}

func NormalizeBoundedCohortGrant(auth *BoundedCohortAuthorization, now time.Time) error {
	if auth == nil {
		return ErrCohortGrantMissing
	}
	if auth.ID == uuid.Nil {
		auth.ID = uuid.New()
	}
	if auth.ActorID == uuid.Nil {
		return ErrCohortHumanActor
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if auth.AuthorizedAt.IsZero() {
		auth.AuthorizedAt = now
	}
	if auth.PolicyVersion == "" {
		auth.PolicyVersion = BoundedCohortPolicyV1
	}
	if auth.ComposerVersion == "" {
		auth.ComposerVersion = ComposerVersion
	}
	if auth.EvidenceVersion == "" {
		auth.EvidenceVersion = DefaultEvidenceVersion
	}
	if auth.FeedSchemaVersion == "" {
		return fmt.Errorf("feed_schema_version required")
	}
	if strings.TrimSpace(auth.RepositorySHA) == "" {
		return fmt.Errorf("repository_sha required")
	}
	if strings.TrimSpace(auth.CohortID) == "" || strings.TrimSpace(auth.CohortHash) == "" {
		return fmt.Errorf("cohort id/hash required")
	}
	if strings.TrimSpace(auth.RecipientSetHash) == "" {
		return fmt.Errorf("recipient_set_hash required")
	}
	if auth.MaxDailyVolume < 1 || auth.MaxDailyVolume > 100 {
		return fmt.Errorf("max_daily_volume must be 1..100 for controlled email")
	}
	if len(auth.AllowedRouteClasses) == 0 {
		return fmt.Errorf("allowed_route_classes required")
	}
	for _, c := range auth.AllowedRouteClasses {
		if strings.EqualFold(strings.TrimSpace(c), RouteClassProbabilisticOrRisky) {
			return fmt.Errorf("RISKY is outside the default controlled-email cohort")
		}
	}
	if auth.AutoSendEnabled || auth.GreenAutorunEnabled {
		return fmt.Errorf("bounded cohort cannot enable auto-send or green autorun")
	}
	if auth.ExpiresAt.IsZero() {
		if auth.TTL <= 0 {
			auth.TTL = 24 * time.Hour
		}
		auth.ExpiresAt = auth.AuthorizedAt.Add(auth.TTL)
	} else if auth.TTL <= 0 {
		auth.TTL = auth.ExpiresAt.Sub(auth.AuthorizedAt)
		if auth.TTL < 0 {
			auth.TTL = 0
		}
	}
	auth.FrozenHashValue = auth.FrozenHash()
	return nil
}

// CreateBoundedCohortGrant persists a human-authorized frozen grant.
// confirm must be true; this campaign may auto-create only synthetic grants.
func CreateBoundedCohortGrant(ctx context.Context, store BoundedCohortStore, auth *BoundedCohortAuthorization, confirm bool) (*BoundedCohortAuthorization, error) {
	if store == nil {
		return nil, ErrCohortStoreUnavailable
	}
	if !confirm {
		return nil, ErrCohortNotConfirmed
	}
	if err := NormalizeBoundedCohortGrant(auth, time.Now().UTC()); err != nil {
		return nil, err
	}
	if err := store.PutGrant(ctx, auth); err != nil {
		return nil, err
	}
	return store.GetGrant(ctx, auth.ID)
}

func ViewBoundedCohortGrant(ctx context.Context, store BoundedCohortStore, id uuid.UUID) (*BoundedCohortAuthorization, error) {
	if store == nil {
		return nil, ErrCohortStoreUnavailable
	}
	auth, err := store.GetGrant(ctx, id)
	if err != nil {
		return nil, err
	}
	if auth == nil {
		return nil, ErrCohortGrantMissing
	}
	return auth, nil
}

func RevokeBoundedCohortGrant(ctx context.Context, store BoundedCohortStore, id, actor uuid.UUID, reason string, now time.Time) error {
	if store == nil {
		return ErrCohortStoreUnavailable
	}
	if actor == uuid.Nil {
		return ErrCohortHumanActor
	}
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("revoke reason required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return store.RevokeGrant(ctx, id, actor, reason, now)
}

func (s *service) AuthorizeBoundedCohort(ctx context.Context, orgID, actorID uuid.UUID, auth *BoundedCohortAuthorization, confirm bool) (*BoundedCohortAuthorization, error) {
	if s == nil || s.cohortStore == nil {
		return nil, ErrCohortStoreUnavailable
	}
	if auth == nil {
		return nil, ErrCohortGrantMissing
	}
	auth.OrganizationID = orgID
	auth.ActorID = actorID
	return CreateBoundedCohortGrant(ctx, s.cohortStore, auth, confirm)
}

func (s *service) GetBoundedCohortGrant(ctx context.Context, id uuid.UUID) (*BoundedCohortAuthorization, error) {
	if s == nil || s.cohortStore == nil {
		return nil, ErrCohortStoreUnavailable
	}
	return ViewBoundedCohortGrant(ctx, s.cohortStore, id)
}

func (s *service) RevokeBoundedCohort(ctx context.Context, id, actor uuid.UUID, reason string) error {
	if s == nil || s.cohortStore == nil {
		return ErrCohortStoreUnavailable
	}
	return RevokeBoundedCohortGrant(ctx, s.cohortStore, id, actor, reason, time.Now().UTC())
}
