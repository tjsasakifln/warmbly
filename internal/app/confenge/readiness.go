package confenge

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/app/whatsapp"
	"github.com/warmbly/warmbly/internal/models"
)

// Channel readiness codes for the operator panel.
const (
	ReadyOK            = "ready"
	ReadyNotReady      = "not_ready"
	ReadyBlockedPolicy = "blocked_by_policy"
	ReadyNotConfigured = "not_configured"
	ReadyFallback      = "fallback_template"
)

// Readiness is the discrete operator status panel payload.
type Readiness struct {
	Email                    string     `json:"email"`
	WhatsApp                 string     `json:"whatsapp"`
	FeedConfigured           bool       `json:"feed_configured"`
	FeedAgeSeconds           *int64     `json:"feed_age_seconds,omitempty"`
	FeedAgeLabel             string     `json:"feed_age"`
	FeedState                string     `json:"feed_state"`
	FeedSnapshot             string     `json:"feed_snapshot_hash,omitempty"`
	FeedLastSyncAt           *time.Time `json:"feed_last_success_at,omitempty"`
	FeedSourceAt             *time.Time `json:"feed_source_generated_at,omitempty"`
	FeedSourceExpiresAt      *time.Time `json:"feed_source_expires_at,omitempty"`
	FeedSyncedAt             *time.Time `json:"feed_synced_at,omitempty"`
	FeedMaxAgeSeconds        int64      `json:"feed_max_age_seconds"`
	FeedAuthorityState       string     `json:"feed_authority_state"`
	TargetMembershipComplete bool       `json:"target_membership_complete"`
	TargetMembershipCount    int        `json:"target_membership_count"`
	SupplierConfirmedCount   int        `json:"supplier_confirmed_count"`
	// COMMERCIAL_AUTHORITY/2.0 population readback. This, never feed age, is
	// what decides whether the org may do outbound at all.
	CommercialQualificationState string `json:"commercial_qualification_state"`
	CommercialQualificationKnown bool   `json:"commercial_qualification_known"`
	CommercialQualifiedCount     int    `json:"commercial_qualified_count"`
	CommercialExpiredCount       int    `json:"commercial_expired_count"`
	CommercialRevokedCount       int    `json:"commercial_revoked_count"`
	CommercialUnknownCount       int    `json:"commercial_unknown_count"`
	OutcomeLoop                  string `json:"outcome_loop"`
	AI                           string `json:"ai"`
	// GovernorCap is the global rolling-hour outbound cap (email+WhatsApp).
	// Primary CONFENGE pacing control (~10/h). Not the campaign daily limit.
	GovernorCap int `json:"governor_cap"`
	// CampaignDailyLimit is the campaign-shell daily ceiling (secondary).
	CampaignDailyLimit int `json:"campaign_daily_limit"`
	// EffectiveDailyCap is min(campaign daily, hourly*window hours) when computable.
	EffectiveDailyCap int    `json:"effective_daily_cap"`
	QueueCount        int    `json:"queue_count"`
	KillSwitch        bool   `json:"kill_switch"`
	SendingAllowed    bool   `json:"sending_allowed"`
	OutreachEnabled   bool   `json:"outreach_enabled"`
	RequireHuman      bool   `json:"require_human_approval"`
	AutoSendEnabled   bool   `json:"auto_send_enabled"`
	WhatsAppEnabled   bool   `json:"whatsapp_enabled"`
	WhatsAppProvider  string `json:"whatsapp_provider,omitempty"`
	PilotCohortState  string `json:"pilot_cohort_state"`
	PilotPrepared     int    `json:"pilot_cohort_prepared"`
	PilotNeedsReview  int    `json:"pilot_cohort_needs_review"`
	PilotApproved     int    `json:"pilot_cohort_approved"`
	PilotSent         int    `json:"pilot_cohort_sent"`
	// LatestBoundedCohort is a read-only projection of the newest persisted
	// cohort grant. It is nil when no durable grant exists; absence is not an
	// authorization.
	LatestBoundedCohort *BoundedCohortReadiness `json:"latest_bounded_cohort,omitempty"`
	// Inbound is ready only when HMAC secret + dest org are set. Never implies live POST.
	Inbound                 string `json:"inbound"`
	InboundSecretConfigured bool   `json:"inbound_secret_configured"`
	InboundOrgConfigured    bool   `json:"inbound_org_configured"`
}

// BoundedCohortReadiness carries only operator-safe grant facts. It never
// includes recipient addresses or rendered message bodies.
type BoundedCohortReadiness struct {
	AuthorizationID        uuid.UUID      `json:"authorization_id"`
	CohortID               string         `json:"cohort_id"`
	CohortHash             string         `json:"cohort_hash"`
	PolicyVersion          string         `json:"policy_version"`
	AllowedRouteClasses    []string       `json:"allowed_route_classes"`
	RouteClassDistribution map[string]int `json:"route_class_distribution,omitempty"`
	AuthorizedQuantity     *int           `json:"authorized_quantity,omitempty"`
	MaxDailyVolume         int            `json:"max_daily_volume"`
	AuthorizedAt           time.Time      `json:"authorized_at"`
	ExpiresAt              time.Time      `json:"expires_at"`
	State                  string         `json:"state"`
	GOReviewVerdict        string         `json:"go_review_verdict,omitempty"`
	GOReviewAt             *time.Time     `json:"go_review_at,omitempty"`
	Sent                   *int           `json:"sent,omitempty"`
	Reserved               *int           `json:"reserved,omitempty"`
}

type latestBoundedCohortStore interface {
	LatestGrant(ctx context.Context, orgID uuid.UUID) (*BoundedCohortAuthorization, error)
	GrantDispatchCounts(ctx context.Context, authorizationID uuid.UUID) (sent int, reserved int, err error)
}

func boundedCohortReadiness(auth *BoundedCohortAuthorization, now time.Time) *BoundedCohortReadiness {
	if auth == nil {
		return nil
	}
	state := "active"
	if auth.RevokedAt != nil {
		state = "revoked"
	} else if expiry := auth.EffectiveExpiry(); !expiry.IsZero() && !now.Before(expiry) {
		state = "expired"
	}
	var quantity *int
	var routeDistribution map[string]int
	if auth.FrozenManifest != nil {
		observed := len(auth.FrozenManifest.Members)
		quantity = &observed
		routeDistribution = map[string]int{}
		for _, member := range auth.FrozenManifest.Members {
			class := strings.TrimSpace(member.RouteClass)
			if class != "" {
				routeDistribution[class]++
			}
		}
	}
	return &BoundedCohortReadiness{
		AuthorizationID: auth.ID, CohortID: auth.CohortID, CohortHash: auth.CohortHash,
		PolicyVersion: auth.PolicyVersion, AllowedRouteClasses: append([]string{}, auth.AllowedRouteClasses...),
		RouteClassDistribution: routeDistribution,
		AuthorizedQuantity:     quantity, MaxDailyVolume: auth.MaxDailyVolume,
		AuthorizedAt: auth.AuthorizedAt, ExpiresAt: auth.EffectiveExpiry(), State: state,
		GOReviewVerdict: auth.GOReviewVerdict, GOReviewAt: auth.GOReviewAt,
	}
}

// ReadinessInputs are optional live signals for BuildReadiness.
type ReadinessInputs struct {
	EmailReady               bool
	WhatsAppReady            bool
	WhatsAppPolicyBlocked    bool
	LastImportAt             *time.Time
	LastSyncAt               *time.Time
	SourceExpiresAt          *time.Time
	SourceFreshnessHash      string
	TargetMembershipComplete bool
	TargetMembershipHash     string
	TargetMembershipCount    int
	SupplierConfirmedCount   int
	// Commercial readback counts. Known is false when the readback could not run.
	CommercialQualificationKnown bool
	CommercialQualifiedCount     int
	CommercialExpiredCount       int
	CommercialRevokedCount       int
	CommercialUnknownCount       int
	FeedSnapshot                 string
	Queue                        *models.OutreachQueueSummary
	AIConfigured                 bool
	WA                           *whatsapp.Config
	Now                          time.Time
}

// BuildReadiness aggregates operator-facing readiness without side effects.
func BuildReadiness(cfg Config, in ReadinessInputs) Readiness {
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	waCfg := whatsapp.LoadConfig()
	if in.WA != nil {
		waCfg = *in.WA
	}

	dcfg := dispatch.LoadConfig()
	hourly := dcfg.SendsPerHour
	if hourly <= 0 {
		hourly = dispatch.DefaultSendsPerHour
	}
	windowHours := SendWindowHours(dcfg.WindowStart, dcfg.WindowEnd)
	if windowHours <= 0 {
		windowHours = 9 // 09:00–18:00 default
	}
	daily := cfg.DefaultDailyLimit
	if daily < 1 {
		daily = DefaultCampaignDailyLimit
	}
	hourlyDay := hourly * windowHours
	effective := daily
	if hourlyDay > 0 && hourlyDay < effective {
		effective = hourlyDay
	}

	r := Readiness{
		OutreachEnabled:         cfg.Enabled,
		RequireHuman:            cfg.RequireHumanApproval,
		AutoSendEnabled:         cfg.AutoSendEnabled,
		GovernorCap:             hourly,
		CampaignDailyLimit:      daily,
		EffectiveDailyCap:       effective,
		FeedConfigured:          strings.TrimSpace(cfg.FeedURL) != "" || strings.TrimSpace(cfg.ManifestURL) != "",
		KillSwitch:              !cfg.SendingAllowed(),
		SendingAllowed:          cfg.SendingAllowed(),
		WhatsAppEnabled:         cfg.WhatsAppEnabled || waCfg.Enabled,
		WhatsAppProvider:        waCfg.Provider,
		InboundSecretConfigured: strings.TrimSpace(cfg.InboundWebhookSecret) != "",
		InboundOrgConfigured:    cfg.InboundOrgID != uuid.Nil || cfg.OperatorOrgID != uuid.Nil,
	}
	probe := EvaluateInboundReceive(cfg)
	switch {
	case probe.Status == InboundReceiveReady:
		r.Inbound = ReadyOK
	case cfg.AutoSendEnabled:
		r.Inbound = ReadyBlockedPolicy
	default:
		r.Inbound = ReadyNotConfigured
	}
	maxAge := cfg.FeedMaxAge
	if maxAge <= 0 {
		maxAge = 24 * time.Hour
	}
	r.FeedMaxAgeSeconds = int64(maxAge.Seconds())

	if in.EmailReady {
		r.Email = ReadyOK
	} else {
		r.Email = ReadyNotReady
	}

	switch {
	case !r.WhatsAppEnabled:
		r.WhatsApp = ReadyNotConfigured
	case in.WhatsAppPolicyBlocked:
		r.WhatsApp = ReadyBlockedPolicy
	case in.WhatsAppReady:
		r.WhatsApp = ReadyOK
	default:
		r.WhatsApp = ReadyNotReady
	}

	if cfg.OutcomeWebhookURL != "" && cfg.OutcomeWebhookSecret != "" {
		r.OutcomeLoop = ReadyOK
	} else {
		r.OutcomeLoop = ReadyNotConfigured
	}

	if in.AIConfigured {
		r.AI = ReadyOK
	} else {
		r.AI = ReadyFallback
	}

	if in.LastImportAt != nil {
		age := int64(now.Sub(in.LastImportAt.UTC()).Seconds())
		if age < 0 {
			age = 0
		}
		r.FeedAgeSeconds = &age
		r.FeedAgeLabel = formatAge(age)
		r.FeedLastSyncAt = in.LastImportAt
		r.FeedSourceAt = in.LastImportAt
		r.FeedSyncedAt = in.LastSyncAt
		r.FeedSnapshot = in.FeedSnapshot
		r.FeedSourceExpiresAt = in.SourceExpiresAt
		r.TargetMembershipComplete = in.TargetMembershipComplete
		r.TargetMembershipCount = in.TargetMembershipCount
		r.SupplierConfirmedCount = in.SupplierConfirmedCount
		if cfg.DelegatedFirstTouchEnabled && (in.SourceExpiresAt == nil || !validSHA256(in.SourceFreshnessHash) ||
			!in.TargetMembershipComplete || !validSHA256(in.TargetMembershipHash) || in.TargetMembershipCount < 1) {
			r.FeedState = "missing"
			r.FeedAuthorityState = "missing"
		} else if cfg.DelegatedFirstTouchEnabled && !now.Before(in.SourceExpiresAt.UTC()) {
			r.FeedState = "stale"
			r.FeedAuthorityState = "expired"
		} else if time.Duration(age)*time.Second > maxAge {
			r.FeedState = "stale"
			r.FeedAuthorityState = "stale"
		} else {
			r.FeedState = "fresh"
			if cfg.DelegatedFirstTouchEnabled {
				r.FeedAuthorityState = "fresh"
			} else {
				r.FeedAuthorityState = "not_required"
			}
		}
	} else {
		r.FeedAgeLabel = "unknown"
		r.FeedState = "missing"
		r.FeedAuthorityState = "missing"
	}

	r.CommercialQualificationKnown = in.CommercialQualificationKnown
	r.CommercialQualifiedCount = in.CommercialQualifiedCount
	r.CommercialExpiredCount = in.CommercialExpiredCount
	r.CommercialRevokedCount = in.CommercialRevokedCount
	r.CommercialUnknownCount = in.CommercialUnknownCount
	r.CommercialQualificationState = rollupCommercialQualification(in)

	if in.Queue != nil {
		r.QueueCount = in.Queue.NeedsReview + in.Queue.ReadyToGenerate + in.Queue.Approved
	}
	return r
}

// rollupCommercialQualification answers the population-level question the
// operator panel gates on. Feed, crawler and snapshot age are never inputs.
func rollupCommercialQualification(in ReadinessInputs) string {
	switch {
	case !in.CommercialQualificationKnown:
		return CommercialUnknown
	case in.CommercialQualifiedCount > 0:
		return CommercialQualified
	case in.CommercialExpiredCount > 0:
		return CommercialExpired
	case in.CommercialRevokedCount > 0:
		return CommercialRevoked
	default:
		return CommercialUnknown
	}
}

func formatAge(seconds int64) string {
	if seconds < 60 {
		return "just now"
	}
	if seconds < 3600 {
		return formatInt(seconds/60) + "m"
	}
	if seconds < 86400 {
		return formatInt(seconds/3600) + "h"
	}
	return formatInt(seconds/86400) + "d"
}

func formatInt(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// CollectReadiness loads org signals and builds Readiness for the API.
func (s *service) CollectReadiness(ctx context.Context, orgID uuid.UUID, emailReady bool) Readiness {
	in := ReadinessInputs{
		EmailReady:   emailReady,
		AIConfigured: s.ai != nil,
	}
	if s.cfg.WhatsAppEnabled && s.wa != nil {
		in.WhatsAppReady = true
	}
	state, stateErr := s.repo.GetFeedSyncState(ctx, orgID)
	if stateErr == nil && state != nil {
		if state.LastStatus == "completed" && state.SourceGeneratedAt != nil && state.LastSnapshotHash != "" && state.LastRunID != "" {
			in.LastImportAt = state.SourceGeneratedAt
			in.LastSyncAt = state.LastSuccessAt
			in.FeedSnapshot = state.LastSnapshotHash
			in.SourceExpiresAt = state.SourceExpiresAt
			in.SourceFreshnessHash = state.SourceFreshnessHash
			in.TargetMembershipComplete = state.TargetMembershipComplete
			in.TargetMembershipHash = state.TargetMembershipHash
			in.TargetMembershipCount = state.TargetMembershipCount
			in.SupplierConfirmedCount = state.SupplierConfirmedCount
		}
	} else if stateErr == nil && state == nil {
		runs, err := s.repo.ListImportRuns(ctx, orgID, 1)
		if err == nil && len(runs) > 0 && runs[0].Status == models.OutreachImportCompleted {
			in.LastImportAt = runs[0].SourceGeneratedAt
			in.LastSyncAt = runs[0].FinishedAt
			in.FeedSnapshot = runs[0].SnapshotHash
		}
	}
	if sum, err := s.repo.CountByQueueState(ctx, orgID); err == nil {
		in.Queue = sum
	}
	s.loadCommercialQualification(ctx, orgID, &in, time.Now().UTC())
	readiness := BuildReadiness(s.cfg, in)
	if store, ok := s.cohortStore.(latestBoundedCohortStore); ok {
		if auth, grantErr := store.LatestGrant(ctx, orgID); grantErr == nil {
			readiness.LatestBoundedCohort = boundedCohortReadiness(auth, time.Now().UTC())
			if readiness.LatestBoundedCohort != nil {
				if sent, reserved, countErr := store.GrantDispatchCounts(ctx, auth.ID); countErr == nil {
					readiness.LatestBoundedCohort.Sent = &sent
					readiness.LatestBoundedCohort.Reserved = &reserved
				}
			}
		}
	}
	readiness.PilotCohortState = "unavailable"
	cohortID := uuid.NewSHA1(orgID, []byte("confenge-pilot-v1")).String()
	memberships, err := s.repo.ListPilotMemberships(ctx, orgID, cohortID)
	if err != nil {
		return readiness
	}
	readiness.PilotCohortState = "ready"
	readiness.PilotPrepared = len(memberships)
	for i := range memberships {
		touchpoint, touchpointErr := s.repo.GetTouchpoint(ctx, orgID, memberships[i].TouchpointID)
		if touchpointErr != nil || touchpoint == nil {
			readiness.PilotCohortState = "unavailable"
			readiness.PilotNeedsReview = 0
			readiness.PilotApproved = 0
			readiness.PilotSent = 0
			return readiness
		}
		switch touchpoint.State {
		case models.TouchpointNeedsReview:
			readiness.PilotNeedsReview++
		case models.TouchpointApproved, models.TouchpointQueued:
			readiness.PilotApproved++
		case models.TouchpointSent, models.TouchpointReplied:
			readiness.PilotSent++
		}
	}
	return readiness
}

// loadCommercialQualification reads the durable COMMERCIAL_AUTHORITY/2.0
// columns. It leaves Known false on any failure so the panel never reports an
// unverified population as qualified.
func (s *service) loadCommercialQualification(ctx context.Context, orgID uuid.UUID, in *ReadinessInputs, now time.Time) {
	if s == nil || in == nil || s.humanGateDB == nil {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var qualified, expired, revoked, unknown int
	err := s.humanGateDB.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE commercial_qualification_deactivated = false
				AND commercial_qualification_state = 'QUALIFIED'
				AND commercial_qualified_until IS NOT NULL
				AND commercial_qualified_until > $2::date)::int,
			count(*) FILTER (WHERE commercial_qualification_deactivated = false
				AND (commercial_qualification_state = 'EXPIRED'
					OR (commercial_qualification_state = 'QUALIFIED'
						AND (commercial_qualified_until IS NULL OR commercial_qualified_until <= $2::date))))::int,
			count(*) FILTER (WHERE commercial_qualification_deactivated = true
				OR commercial_qualification_state = 'REVOKED')::int,
			count(*) FILTER (WHERE commercial_qualification_deactivated = false
				AND commercial_qualification_state = 'UNKNOWN')::int
		FROM outreach_accounts WHERE organization_id = $1`,
		orgID, now).Scan(&qualified, &expired, &revoked, &unknown)
	if err != nil {
		return
	}
	in.CommercialQualificationKnown = true
	in.CommercialQualifiedCount = qualified
	in.CommercialExpiredCount = expired
	in.CommercialRevokedCount = revoked
	in.CommercialUnknownCount = unknown
}

// RedactSecret returns a non-empty secret marker without leaking the value.
func RedactSecret(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return "****"
	}
	return s[:2] + strings.Repeat("*", len(s)-4) + s[len(s)-2:]
}

// AIProviderName reports the configured AI_PROVIDER env (empty = template fallback).
func AIProviderName() string {
	return strings.TrimSpace(os.Getenv("AI_PROVIDER"))
}
