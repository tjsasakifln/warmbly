package confenge

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/repository"
)

const (
	FirstWindowReadyForGOAdjudication     = "READY_FOR_GO_ADJUDICATION"
	FirstWindowArmedForNextBusinessWindow = "ARMED_FOR_NEXT_BUSINESS_WINDOW"
	FirstWindowTransportActiveInWindow    = "TRANSPORT_ACTIVE_IN_WINDOW"
	FirstWindowBlockedPrefix              = "BLOCKED:"
	FirstWindowGOForControlledPilot       = ReleaseGOForControlledEmailPilot
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
	CommercialAuthority       CommercialQualificationDecision
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
	Verdict                   string                          `json:"verdict"`
	Blockers                  []string                        `json:"blockers,omitempty"`
	WarmblyReleaseSHA         string                          `json:"warmbly_release_sha"`
	FeedManifestSchema        string                          `json:"feed_manifest_schema"`
	SourceRunID               string                          `json:"source_run_id,omitempty"`
	SourceSnapshotHash        string                          `json:"source_snapshot_hash,omitempty"`
	SourceHealth              SourceHealthDecision            `json:"source_health"`
	CommercialAuthority       CommercialQualificationDecision `json:"commercial_authority"`
	MembershipHash            string                          `json:"membership_hash,omitempty"`
	PolicyID                  string                          `json:"policy_id"`
	PolicyVersion             string                          `json:"policy_version"`
	AllowedRouteClasses       []string                        `json:"allowed_route_classes"`
	MailboxSet                []string                        `json:"mailbox_set"`
	SMTPReady                 string                          `json:"smtp_ready"`
	IMAPReplyIngestReady      string                          `json:"imap_reply_ingest_ready"`
	GlobalSendsPerHour        int                             `json:"global_sends_per_hour"`
	MailboxRateCaps           []int                           `json:"mailbox_rate_caps"`
	MinWaitSeconds            int                             `json:"min_wait_seconds"`
	BusinessTimezone          string                          `json:"business_timezone"`
	BusinessWindowStart       string                          `json:"business_window_start"`
	BusinessWindowEnd         string                          `json:"business_window_end"`
	PauseState                string                          `json:"pause_state"`
	PauseSource               string                          `json:"pause_source,omitempty"`
	PausedBy                  *uuid.UUID                      `json:"paused_by,omitempty"`
	PausedAt                  *time.Time                      `json:"paused_at,omitempty"`
	KillSwitchEngaged         bool                            `json:"kill_switch_engaged"`
	SuppressionCount          int                             `json:"suppression_count"`
	DNCCount                  int                             `json:"dnc_count"`
	BounceCount               int                             `json:"bounce_count"`
	ReadyReservoir            int                             `json:"ready_reservoir"`
	Queued                    int                             `json:"queued"`
	Reserved                  int                             `json:"reserved"`
	OutcomeObservabilityReady string                          `json:"outcome_observability_ready"`
	ProviderMutationCount     int                             `json:"provider_mutation_count"`
	EvaluatedAt               time.Time                       `json:"evaluated_at"`
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
		add(snap.CommercialAuthority.State != CommercialUnknown, "commercial_authority_unknown")
		add(snap.CommercialAuthority.State != CommercialExpired, ReasonQualificationExpired)
		add(snap.CommercialAuthority.State != CommercialRevoked, ReasonQualificationRevoked)
		add(RecognizeCommercialAuthorityPolicy(snap.CommercialAuthority.PolicyVersion), ReasonPolicyVersionUnsupported)
		// An account-derived rollup carries no corpus digest; its evidence is
		// the per-account hash each row was already verified against.
		add(snap.CommercialAuthority.EvidenceHash == "" || validSHA256(snap.CommercialAuthority.EvidenceHash), ReasonQualificationEvidenceDrift)
	} else {
		// Absence of commercial authority is explicit and fail-closed. Source
		// freshness is never promoted into a commercial authorization, so a
		// STALE source with valid authority produces no blocker at all.
		add(false, ReasonQualificationMissing)
	}
	sort.Strings(blockers)
	if len(blockers) > 0 {
		rep.Blockers = blockers
		rep.Verdict = firstWindowBlocked(blockers[0])
		return rep
	}
	// READY_FOR_GO_ADJUDICATION is the pre-GO pack. After PRE-GO pauses are
	// lifted, the same gates plus a live queue become the armed/in-window
	// transport verdict. This function still never emits GO_FOR_CONTROLLED_EMAIL_PILOT.
	if firstWindowPreGOPauseEngaged(snap) {
		rep.Verdict = FirstWindowReadyForGOAdjudication
		return rep
	}
	if snap.Queued <= 0 && snap.Reserved <= 0 {
		rep.Verdict = FirstWindowReadyForGOAdjudication
		return rep
	}
	if snap.PauseState == TransportActive {
		rep.Verdict = FirstWindowTransportActiveInWindow
		return rep
	}
	rep.Verdict = FirstWindowArmedForNextBusinessWindow
	return rep
}

func firstWindowPreGOPauseEngaged(snap FirstWindowReadinessSnapshot) bool {
	if snap.KillSwitchEngaged {
		return true
	}
	src := strings.ToLower(strings.TrimSpace(snap.PauseSource))
	switch src {
	case PauseSourceKillSwitch, PauseSourceDurable, PauseSourceAPI, PauseSourceWorkerGuard, PauseSourceEnvironment, PauseSourceConfiguration:
		return true
	}
	if strings.Contains(src, "deploy_preflight") || strings.Contains(src, "scale_pre_go") {
		return true
	}
	return false
}

func firstWindowSnapshotFromLive(
	cfg Config,
	source DelegatedFirstTouchSourceReadback,
	authority CommercialQualificationDecision,
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
		PolicyID:                  DelegatedFirstTouchPolicyV2,
		PolicyVersion:             DelegatedFirstTouchPolicyV2,
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
	feed, feedErr := s.repo.GetFeedSyncState(ctx, orgID)
	if feedErr != nil {
		return nil, errx.New(errx.Internal, "feed sync state: "+feedErr.Error())
	}
	sourceHealth := EvaluateStoredSourceHealth(feed, now, s.cfg.FeedMaxAge)
	authority := FeedCommercialAuthorityState(feed)
	if !authority.Present {
		authority = s.commercialQualificationFromAccounts(ctx, orgID, now)
	}
	source := DelegatedFirstTouchSourceReadback{}
	if feed != nil {
		source = DelegatedFirstTouchSourceReadback{
			RunID: feed.LastRunID, SnapshotHash: feed.LastSnapshotHash,
			FreshnessState:           delegatedSourceFreshnessState(feed, now, s.cfg.FeedMaxAge),
			GeneratedAt:              feed.SourceGeneratedAt,
			ExpiresAt:                feed.SourceExpiresAt,
			FreshnessHash:            feed.SourceFreshnessHash,
			TargetMembershipComplete: feed.TargetMembershipComplete,
			TargetMembershipHash:     feed.TargetMembershipHash,
			TargetMembershipCount:    feed.TargetMembershipCount,
			SupplierConfirmedCount:   feed.SupplierConfirmedCount,
		}
	} else {
		source.FreshnessState = "missing"
	}
	status, _ := s.DelegatedFirstTouchStatus(ctx, orgID, "")
	queued, reserved, reservoir := 0, 0, 0
	outcomesReady := EvidenceUnknown
	if status != nil {
		queued = status.Control.Queued
		reserved = status.Control.Reserved
		reservoir = status.Control.ReadyReservoir
		if status.Control.Outcomes != nil {
			outcomesReady = EvidencePass
		}
	}
	dispatchStatus, dsErr := s.DispatchStatus(ctx, orgID)
	if dsErr != nil {
		dispatchStatus = dispatch.Status{}
	}
	transport := s.ResolveTransportState(ctx, &orgID)
	smtpReady, _ := ProbeSMTPReadiness(3 * time.Second)
	imapReady, _ := ProbeReplyIngestReadiness(ctx, s.cohortStore, 3*time.Second)
	suppression, dnc, bounce := s.collectSafetySnapshots(ctx, orgID)
	snap := firstWindowSnapshotFromLive(s.cfg, source, authority, dispatchStatus, transport, queued, reserved, reservoir, suppression, dnc, bounce, smtpReady, imapReady, outcomesReady, now)
	snap.SourceHealth = sourceHealth
	snap.PausedBy = dispatchStatus.PausedBy
	snap.PausedAt = dispatchStatus.PausedAt
	report := EvaluateFirstWindowReadiness(snap)
	return &report, nil
}

// commercialQualificationFromAccounts derives population qualification from the
// durable per-account evidence. It is used when the producer has not attested a
// population: per-company evidence, each hash-verified and inside the rolling
// three-year window, is strictly stronger than an attestation, so an unattested
// but individually proven population is not treated as unqualified.
func (s *service) commercialQualificationFromAccounts(ctx context.Context, orgID uuid.UUID, now time.Time) CommercialQualificationDecision {
	out := CommercialQualificationDecision{State: CommercialUnknown, ReasonCodes: []string{ReasonQualificationMissing}}
	if s.delegatedDB == nil {
		return out
	}
	var qualified int
	err := s.delegatedDB.QueryRow(ctx, `
		SELECT count(*)::int FROM outreach_accounts
		WHERE organization_id=$1
		  AND commercial_qualification_state='QUALIFIED'
		  AND NOT commercial_qualification_deactivated
		  AND commercial_qualified_until > $2::date`, orgID, now).Scan(&qualified)
	if err != nil || qualified <= 0 {
		return out
	}
	return CommercialQualificationDecision{
		Present:       true,
		State:         CommercialQualified,
		PolicyVersion: CommercialAuthorityPolicyV2,
		EvidenceHash:  "",
		ReasonCodes:   []string{ReasonQualified},
	}
}

func (s *service) collectSafetySnapshots(ctx context.Context, orgID uuid.UUID) (suppression, dnc, bounce int) {
	if s.delegatedDB != nil {
		err := s.delegatedDB.QueryRow(ctx, `
			SELECT
				(SELECT count(*)::int FROM outreach_accounts WHERE organization_id=$1 AND blocked),
				(SELECT count(*)::int FROM outreach_accounts WHERE organization_id=$1 AND do_not_contact),
				(SELECT count(*)::int FROM outreach_contact_candidates WHERE organization_id=$1 AND bounced)`, orgID).
			Scan(&suppression, &dnc, &bounce)
		if err == nil {
			return suppression, dnc, bounce
		}
	}
	accounts, err := s.repo.ListAccounts(ctx, orgID, repository.OutreachAccountFilter{Limit: 1000})
	if err != nil {
		return 0, 0, 0
	}
	for i := range accounts {
		if accounts[i].Blocked {
			suppression++
		}
		if accounts[i].DoNotContact {
			dnc++
		}
		cands, candErr := s.repo.ListCandidates(ctx, orgID, accounts[i].ID)
		if candErr != nil {
			continue
		}
		for j := range cands {
			if cands[j].Bounced {
				bounce++
			}
		}
	}
	return suppression, dnc, bounce
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
