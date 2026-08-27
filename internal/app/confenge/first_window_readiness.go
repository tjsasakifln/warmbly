package confenge

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/errx"
)

const (
	FirstWindowReadyForGOAdjudication = "READY_FOR_GO_ADJUDICATION"
	FirstWindowBlockedPrefix          = "BLOCKED:"
	FirstWindowGOForControlledPilot   = ReleaseGOForControlledEmailPilot
)

// FirstWindowReadinessSnapshot is the closed evidence pack for a candidate
// release. Tests inject it; production fills it from live readbacks without
// sending mail.
type FirstWindowReadinessSnapshot struct {
	WarmblyReleaseSHA         string
	FeedManifestSchema        string
	SourceRunID               string
	SourceSnapshotHash        string
	SourceHealth              SourceHealthDecision
	CommercialAuthority       CommercialAuthorityDecision
	MembershipHash            string
	PolicyID                  string
	PolicyVersion             string
	AllowedRouteClasses       []string
	MailboxSet                []string
	SMTPReady                 CheckState
	IMAPReplyIngestReady      CheckState
	GlobalSendsPerHour        int
	MailboxRateCaps           []int
	MinWaitSeconds            int
	BusinessTimezone          string
	BusinessWindowStart       string
	BusinessWindowEnd         string
	PauseState                string
	PauseSource               string
	PausedBy                  *uuid.UUID
	PausedAt                  *time.Time
	KillSwitchEngaged         bool
	SuppressionCount          int
	DNCCount                  int
	BounceCount               int
	ReadyReservoir            int
	Queued                    int
	Reserved                  int
	OutcomeObservabilityReady CheckState
	ProviderMutationCount     int
	EvaluatedAt               time.Time
}

// FirstWindowReadinessReport is the only artifact this campaign may emit for
// #43. It never emits GO_FOR_CONTROLLED_EMAIL_PILOT.
type FirstWindowReadinessReport struct {
	Verdict                   string                      `json:"verdict"`
	Blockers                  []string                    `json:"blockers,omitempty"`
	WarmblyReleaseSHA         string                      `json:"warmbly_release_sha"`
	FeedManifestSchema        string                      `json:"feed_manifest_schema"`
	SourceRunID               string                      `json:"source_run_id,omitempty"`
	SourceSnapshotHash        string                      `json:"source_snapshot_hash,omitempty"`
	SourceHealth              SourceHealthDecision        `json:"source_health"`
	CommercialAuthority       CommercialAuthorityDecision `json:"commercial_authority"`
	MembershipHash            string                      `json:"membership_hash,omitempty"`
	PolicyID                  string                      `json:"policy_id"`
	PolicyVersion             string                      `json:"policy_version"`
	AllowedRouteClasses       []string                    `json:"allowed_route_classes"`
	MailboxSet                []string                    `json:"mailbox_set"`
	SMTPReady                 string                      `json:"smtp_ready"`
	IMAPReplyIngestReady      string                      `json:"imap_reply_ingest_ready"`
	GlobalSendsPerHour        int                         `json:"global_sends_per_hour"`
	MailboxRateCaps           []int                       `json:"mailbox_rate_caps"`
	MinWaitSeconds            int                         `json:"min_wait_seconds"`
	BusinessTimezone          string                      `json:"business_timezone"`
	BusinessWindowStart       string                      `json:"business_window_start"`
	BusinessWindowEnd         string                      `json:"business_window_end"`
	PauseState                string                      `json:"pause_state"`
	PauseSource               string                      `json:"pause_source,omitempty"`
	PausedBy                  *uuid.UUID                  `json:"paused_by,omitempty"`
	PausedAt                  *time.Time                  `json:"paused_at,omitempty"`
	KillSwitchEngaged         bool                        `json:"kill_switch_engaged"`
	SuppressionCount          int                         `json:"suppression_count"`
	DNCCount                  int                         `json:"dnc_count"`
	BounceCount               int                         `json:"bounce_count"`
	ReadyReservoir            int                         `json:"ready_reservoir"`
	Queued                    int                         `json:"queued"`
	Reserved                  int                         `json:"reserved"`
	OutcomeObservabilityReady string                      `json:"outcome_observability_ready"`
	ProviderMutationCount     int                         `json:"provider_mutation_count"`
	EvaluatedAt               time.Time                   `json:"evaluated_at"`
}

func firstWindowBlocked(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "unknown"
	}
	if strings.HasPrefix(reason, FirstWindowBlockedPrefix) {
		return reason
	}
	return FirstWindowBlockedPrefix + reason
}

// EvaluateFirstWindowReadiness is deterministic for a given snapshot. It never
// emits GO_FOR_CONTROLLED_EMAIL_PILOT.
func EvaluateFirstWindowReadiness(snap FirstWindowReadinessSnapshot) FirstWindowReadinessReport {
	rep := FirstWindowReadinessReport{
		WarmblyReleaseSHA:         snap.WarmblyReleaseSHA,
		FeedManifestSchema:        snap.FeedManifestSchema,
		SourceRunID:               snap.SourceRunID,
		SourceSnapshotHash:        snap.SourceSnapshotHash,
		SourceHealth:              snap.SourceHealth,
		CommercialAuthority:       snap.CommercialAuthority,
		MembershipHash:            snap.MembershipHash,
		PolicyID:                  snap.PolicyID,
		PolicyVersion:             snap.PolicyVersion,
		AllowedRouteClasses:       append([]string{}, snap.AllowedRouteClasses...),
		MailboxSet:                append([]string{}, snap.MailboxSet...),
		SMTPReady:                 snap.SMTPReady.Label(),
		IMAPReplyIngestReady:      snap.IMAPReplyIngestReady.Label(),
		GlobalSendsPerHour:        snap.GlobalSendsPerHour,
		MailboxRateCaps:           append([]int{}, snap.MailboxRateCaps...),
		MinWaitSeconds:            snap.MinWaitSeconds,
		BusinessTimezone:          snap.BusinessTimezone,
		BusinessWindowStart:       snap.BusinessWindowStart,
		BusinessWindowEnd:         snap.BusinessWindowEnd,
		PauseState:                snap.PauseState,
		PauseSource:               snap.PauseSource,
		PausedBy:                  snap.PausedBy,
		PausedAt:                  snap.PausedAt,
		KillSwitchEngaged:         snap.KillSwitchEngaged,
		SuppressionCount:          snap.SuppressionCount,
		DNCCount:                  snap.DNCCount,
		BounceCount:               snap.BounceCount,
		ReadyReservoir:            snap.ReadyReservoir,
		Queued:                    snap.Queued,
		Reserved:                  snap.Reserved,
		OutcomeObservabilityReady: snap.OutcomeObservabilityReady.Label(),
		ProviderMutationCount:     snap.ProviderMutationCount,
		EvaluatedAt:               snap.EvaluatedAt.UTC(),
	}
	var blockers []string
	add := func(ok bool, code string) {
		if !ok {
			blockers = appendUnique(blockers, code)
		}
	}
	add(strings.TrimSpace(snap.WarmblyReleaseSHA) != "", "warmbly_release_sha_missing")
	add(strings.TrimSpace(snap.FeedManifestSchema) != "", "feed_manifest_schema_missing")
	add(strings.TrimSpace(snap.SourceRunID) != "" && strings.TrimSpace(snap.SourceSnapshotHash) != "", "source_binding_missing")
	add(strings.TrimSpace(snap.MembershipHash) != "", "membership_hash_missing")
	policyID := firstNonEmpty(snap.PolicyID, snap.PolicyVersion)
	known, hold, policyReason := RecognizeFirstTouchPolicy(policyID)
	add(known && !hold, firstNonEmpty(policyReason, ReasonPolicyUnknown))
	add(len(snap.AllowedRouteClasses) > 0, "allowed_route_classes_missing")
	add(len(snap.MailboxSet) > 0, "mailbox_set_missing")
	add(snap.SMTPReady.IsPass(), "smtp_not_ready")
	add(snap.IMAPReplyIngestReady.IsPass(), "imap_reply_ingest_not_ready")
	add(snap.GlobalSendsPerHour > 0, "global_rate_cap_missing")
	add(snap.MinWaitSeconds > 0, "min_wait_time_missing")
	add(strings.TrimSpace(snap.BusinessTimezone) != "" && strings.TrimSpace(snap.BusinessWindowStart) != "" && strings.TrimSpace(snap.BusinessWindowEnd) != "", "business_window_missing")
	add(strings.TrimSpace(snap.PauseState) != "", "pause_state_missing")
	add(strings.TrimSpace(snap.PauseSource) != "", "pause_source_missing")
	add(snap.OutcomeObservabilityReady.IsPass(), "outcome_observability_not_ready")
	add(snap.ProviderMutationCount == 0, "provider_mutation_nonzero")
	if snap.CommercialAuthority.Present {
		add(snap.CommercialAuthority.State != CommercialAuthorityUnknown, "commercial_authority_unknown")
		add(snap.CommercialAuthority.State != CommercialAuthorityExpired, "commercial_authority_expired")
		add(strings.TrimSpace(snap.CommercialAuthority.BasisSourceRunID) == strings.TrimSpace(snap.SourceRunID), "commercial_authority_run_mismatch")
		add(strings.TrimSpace(snap.CommercialAuthority.BasisSnapshotHash) == strings.TrimSpace(snap.SourceSnapshotHash), "commercial_authority_snapshot_mismatch")
		add(strings.ToLower(strings.TrimSpace(snap.CommercialAuthority.BasisMembershipHash)) == strings.ToLower(strings.TrimSpace(snap.MembershipHash)), "commercial_authority_membership_mismatch")
	} else {
		add(snap.SourceHealth.State == SourceHealthFresh, "source_health_not_fresh_strict_fallback")
	}
	sort.Strings(blockers)
	if len(blockers) > 0 {
		rep.Blockers = blockers
		rep.Verdict = firstWindowBlocked(blockers[0])
		return rep
	}
	rep.Verdict = FirstWindowReadyForGOAdjudication
	return rep
}

func firstWindowSnapshotFromLive(
	cfg Config,
	source DelegatedFirstTouchSourceReadback,
	authority CommercialAuthorityDecision,
	status dispatch.Status,
	transport TransportState,
	queued, reserved, reservoir int,
	suppression, dnc, bounce int,
	smtpReady, imapReady, outcomesReady CheckState,
	now time.Time,
) FirstWindowReadinessSnapshot {
	mailboxes := make([]string, 0, len(status.Mailboxes))
	caps := make([]int, 0, len(status.Mailboxes))
	for _, mb := range status.Mailboxes {
		if strings.TrimSpace(mb.Email) != "" {
			mailboxes = append(mailboxes, mb.Email)
		} else {
			mailboxes = append(mailboxes, mb.EmailAccountID.String())
		}
		caps = append(caps, mb.EffectiveDailyCap)
	}
	pauseSource := status.PauseSource
	if pauseSource == "" {
		pauseSource = mostRestrictivePauseSource(transport)
	}
	return FirstWindowReadinessSnapshot{
		WarmblyReleaseSHA:         cfg.RepositorySHA,
		FeedManifestSchema:        firstNonEmpty(cfg.FeedSchemaVersion, "confenge.outreach.v1"),
		SourceRunID:               source.RunID,
		SourceSnapshotHash:        source.SnapshotHash,
		SourceHealth:              ClassifySourceHealthFromReadback(source),
		CommercialAuthority:       authority,
		MembershipHash:            source.TargetMembershipHash,
		PolicyID:                  DelegatedFirstTouchPolicyV1,
		PolicyVersion:             DelegatedFirstTouchPolicyV1,
		AllowedRouteClasses:       []string{RouteClassDirectPerson, RouteClassRoleOrDepartment, RouteClassGenericCompany, RouteClassPublicCompanyFreemail},
		MailboxSet:                mailboxes,
		SMTPReady:                 smtpReady,
		IMAPReplyIngestReady:      imapReady,
		GlobalSendsPerHour:        status.Cap,
		MailboxRateCaps:           caps,
		MinWaitSeconds:            status.MinGapSeconds,
		BusinessTimezone:          firstNonEmpty(status.Timezone, dispatch.DefaultTimezone),
		BusinessWindowStart:       status.WindowStart,
		BusinessWindowEnd:         status.WindowEnd,
		PauseState:                transport.State,
		PauseSource:               pauseSource,
		KillSwitchEngaged:         FileKillSwitchActive(),
		SuppressionCount:          suppression,
		DNCCount:                  dnc,
		BounceCount:               bounce,
		ReadyReservoir:            reservoir,
		Queued:                    queued,
		Reserved:                  reserved,
		OutcomeObservabilityReady: outcomesReady,
		ProviderMutationCount:     0,
		EvaluatedAt:               now.UTC(),
	}
}

func (s *service) CollectFirstWindowReadiness(ctx context.Context, orgID uuid.UUID) (*FirstWindowReadinessReport, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	now := s.now()
	status, xerr := s.DelegatedFirstTouchStatus(ctx, orgID, "")
	if xerr != nil {
		return nil, xerr
	}
	dispatchStatus, dsErr := s.DispatchStatus(ctx, orgID)
	if dsErr != nil {
		dispatchStatus = dispatch.Status{}
	}
	transport := s.ResolveTransportState(ctx, &orgID)
	smtpReady, _ := ProbeSMTPReadiness(3 * time.Second)
	imapReady, _ := ProbeReplyIngestReadiness(ctx, s.cohortStore, 3*time.Second)
	outcomesReady := EvidenceUnknown
	if status != nil && status.Control.Outcomes != nil {
		outcomesReady = EvidencePass
	}
	authority := CommercialAuthorityDecision{State: CommercialAuthorityAbsent}
	if status != nil {
		authority = CommercialAuthorityDecision{
			Present:                            status.Control.Commercial.Present,
			State:                              firstNonEmpty(status.Control.Commercial.State, CommercialAuthorityAbsent),
			NewAdmissionAllowed:                status.Control.Commercial.NewAdmissionAllowed,
			ExistingBoundTouchTransportAllowed: status.Control.Commercial.ExistingBoundTouchTransportAllowed,
			BasisSourceRunID:                   status.Control.Commercial.BasisSourceRunID,
			BasisSnapshotHash:                  status.Control.Commercial.BasisSnapshotHash,
			BasisMembershipHash:                status.Control.Commercial.BasisMembershipHash,
			ValidUntil:                         status.Control.Commercial.ValidUntil,
			ReasonCodes:                        status.Control.Commercial.ReasonCodes,
		}
	}
	source := DelegatedFirstTouchSourceReadback{}
	queued, reserved, reservoir := 0, 0, 0
	if status != nil {
		source = status.Control.Source
		queued = status.Control.Queued
		reserved = status.Control.Reserved
		reservoir = status.Control.ReadyReservoir
	}
	snap := firstWindowSnapshotFromLive(s.cfg, source, authority, dispatchStatus, transport, queued, reserved, reservoir, 0, 0, 0, smtpReady, imapReady, outcomesReady, now)
	snap.PausedBy = dispatchStatus.PausedBy
	snap.PausedAt = dispatchStatus.PausedAt
	report := EvaluateFirstWindowReadiness(snap)
	return &report, nil
}

func ClassifySourceHealthFromReadback(src DelegatedFirstTouchSourceReadback) SourceHealthDecision {
	state := strings.ToUpper(strings.TrimSpace(src.FreshnessState))
	switch state {
	case SourceHealthFresh:
		state = SourceHealthFresh
	case SourceHealthDegraded:
		state = SourceHealthDegraded
	case SourceHealthStale, "EXPIRED", "INVALID":
		state = SourceHealthStale
	case "", "MISSING":
		state = SourceHealthMissing
	default:
		state = SourceHealthStale
	}
	return SourceHealthDecision{State: state, AsOf: src.GeneratedAt, ExpiresAt: src.ExpiresAt}
}
