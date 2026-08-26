package confenge

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/scheduler"
)

const (
	delegatedFirstTouchRunwayPolicyV1 = "confenge.delegated-first-touch.runway.v1"
	delegatedFirstTouchMaxSlots       = 100000
)

// DelegatedFirstTouchRunwayMetrics is the operational reservoir and scheduled-runway readback.
type DelegatedFirstTouchRunwayMetrics struct {
	PolicyVersion         string     `json:"policy_version"`
	TargetDays            int        `json:"target_days"`
	MinReadyReservoir     int        `json:"min_ready_reservoir"`
	ReadyReservoirCount   int        `json:"ready_reservoir_count"`
	CurrentScheduledCount int        `json:"current_scheduled_count"`
	TargetScheduledCount  int        `json:"target_scheduled_count"`
	QueuedCount           int        `json:"queued_count"`
	ReservedCount         int        `json:"reserved_count"`
	RunwayHours           float64    `json:"runway_hours"`
	RunwayDays            float64    `json:"runway_days"`
	CurrentRunwayUntil    *time.Time `json:"current_runway_until,omitempty"`
	TargetRunwayUntil     *time.Time `json:"target_runway_until,omitempty"`
	FurthestDueAt         *time.Time `json:"furthest_due_at,omitempty"`
	MailboxCount          int        `json:"mailbox_count"`
	DailyCapacity         int        `json:"daily_capacity"`
	FillRate              int        `json:"fill_rate"`
	StaleRetired          int        `json:"stale_retired"`
	Held                  int        `json:"held"`
	NoCandidate           int        `json:"no_candidate"`
	CapacityBlocked       int        `json:"capacity_blocked"`
	CapacityBlocker       string     `json:"capacity_blocker,omitempty"`
}

type delegatedFirstTouchRunwayPlan struct {
	CapacityKnown         bool
	CapacityBlocker       string
	MailboxCount          int
	DailyCapacity         int
	SlotGap               time.Duration
	QueuedCount           int
	ReservedCount         int
	CurrentScheduledCount int
	TargetScheduledCount  int
	FurthestDueAt         *time.Time
	TargetRunwayUntil     time.Time
	DispatchConfig        dispatch.Config
	PlannedAt             time.Time
}

func (p delegatedFirstTouchRunwayPlan) TargetReached() bool {
	return p.CapacityKnown && p.FurthestDueAt != nil &&
		!p.FurthestDueAt.Before(p.TargetRunwayUntil) &&
		p.CurrentScheduledCount >= p.TargetScheduledCount
}

func (p delegatedFirstTouchRunwayPlan) NextDueAt() time.Time {
	base := p.PlannedAt.UTC()
	if base.IsZero() {
		base = time.Now().UTC()
	}
	if p.FurthestDueAt != nil && p.FurthestDueAt.After(base) {
		base = *p.FurthestDueAt
	}
	return nextDelegatedFirstTouchSlot(base, p.SlotGap, p.DispatchConfig)
}

func nextDelegatedFirstTouchSlot(after time.Time, gap time.Duration, cfg dispatch.Config) time.Time {
	return dispatch.NextEligibleSlot(after.UTC().Add(gap), cfg.Timezone, cfg.WindowStart, cfg.WindowEnd, cfg.BusinessDaysOnly)
}

func (s *service) lockDelegatedFirstTouchRunway(ctx context.Context, orgID uuid.UUID) (func(), bool, error) {
	conn, err := s.delegatedDB.Acquire(ctx)
	if err != nil {
		return func() {}, false, err
	}
	var locked bool
	err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, "confenge-delegated-first-touch-runway:"+orgID.String()).Scan(&locked)
	if err != nil || !locked {
		conn.Release()
		return func() {}, false, err
	}
	return func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1,0))`, "confenge-delegated-first-touch-runway:"+orgID.String())
		conn.Release()
	}, true, nil
}

func (s *service) delegatedFirstTouchRunwayPlan(
	ctx context.Context,
	orgID uuid.UUID,
	feed *models.OutreachFeedSyncState,
	auth *models.CampaignPolicyAuthorization,
	now time.Time,
) (delegatedFirstTouchRunwayPlan, error) {
	plan := delegatedFirstTouchRunwayPlan{}
	if s == nil || s.governor == nil || feed == nil || auth == nil {
		plan.CapacityBlocker = "runway_authority_unavailable"
		return plan, nil
	}
	plan.DispatchConfig = s.governor.Config()
	plan.PlannedAt = now.UTC()
	if s.cfg.DelegatedFirstTouchRunwayDays < 1 || s.cfg.DelegatedFirstTouchRunwayDays > MaxDelegatedFirstTouchRunwayDays {
		plan.CapacityBlocker = "runway_horizon_invalid"
		return plan, nil
	}
	if err := validateAuthoritativeFeedState(feed, now, s.cfg.FeedMaxAge, true); err != nil {
		plan.CapacityBlocker = "policy_or_feed_stale"
		return plan, nil
	}
	manifest := DelegatedFirstTouchManifest{PolicyAuthorizationID: auth.ID}
	if len(validateDelegatedPolicy(auth, manifest, now)) > 0 || len(s.validateDelegatedFounderBinding(orgID, auth)) > 0 {
		plan.CapacityBlocker = "policy_or_feed_stale"
		return plan, nil
	}
	if err := s.delegatedFirstTouchQueueSnapshot(ctx, orgID, feed, auth.ID, &plan); err != nil {
		return plan, err
	}
	if blockers := s.validateDelegatedTransportAuthority(ctx, orgID, auth); len(blockers) > 0 {
		plan.CapacityBlocker = blockers[0]
		return plan, nil
	}
	if s.orgRisk == nil || s.orgRisk.SendingSuspended(ctx, orgID) {
		plan.CapacityBlocker = "suppression_or_risk_blocked"
		return plan, nil
	}
	status, err := s.governor.Status(ctx, &orgID)
	if err != nil {
		return plan, err
	}
	var selectedMailbox *dispatch.MailboxCapacity
	for i := range status.Mailboxes {
		if strings.EqualFold(status.Mailboxes[i].Email, auth.SenderMailbox) {
			selectedMailbox = &status.Mailboxes[i]
			break
		}
	}
	if selectedMailbox == nil {
		plan.CapacityBlocker = "authorized_mailbox_capacity_unknown"
		return plan, nil
	}
	if selectedMailbox.Health == "blocked" || selectedMailbox.EffectiveDailyCap < 1 || selectedMailbox.EffectiveHourlyCap < 1 {
		plan.CapacityBlocker = firstNonEmpty(selectedMailbox.HealthReason, "authorized_mailbox_capacity_blocked")
		return plan, nil
	}

	dailyCapacity, mailboxCount, blocker, err := s.delegatedFirstTouchDailyCapacity(ctx, orgID, auth, now, plan.DispatchConfig)
	if err != nil {
		return plan, err
	}
	plan.DailyCapacity, plan.MailboxCount, plan.CapacityBlocker = dailyCapacity, mailboxCount, blocker
	if blocker != "" || dailyCapacity < 1 {
		return plan, nil
	}
	window, ok := delegatedSendWindowDuration(plan.DispatchConfig.WindowStart, plan.DispatchConfig.WindowEnd)
	if !ok {
		plan.CapacityBlocker = "business_window_unknown"
		return plan, nil
	}
	plan.SlotGap = time.Duration(math.Ceil(float64(window) / float64(dailyCapacity)))
	if plan.SlotGap < plan.DispatchConfig.MinGap {
		plan.SlotGap = plan.DispatchConfig.MinGap
	}
	plan.TargetRunwayUntil = dispatch.NextEligibleSlot(
		now.AddDate(0, 0, s.cfg.DelegatedFirstTouchRunwayDays),
		plan.DispatchConfig.Timezone,
		plan.DispatchConfig.WindowStart,
		plan.DispatchConfig.WindowEnd,
		plan.DispatchConfig.BusinessDaysOnly,
	)
	for due := nextDelegatedFirstTouchSlot(now, plan.SlotGap, plan.DispatchConfig); ; due = nextDelegatedFirstTouchSlot(due, plan.SlotGap, plan.DispatchConfig) {
		plan.TargetScheduledCount++
		if plan.TargetScheduledCount > delegatedFirstTouchMaxSlots {
			plan.CapacityBlocker = "runway_target_exceeds_safe_slot_bound"
			return plan, nil
		}
		if !due.Before(plan.TargetRunwayUntil) {
			break
		}
	}
	if plan.TargetScheduledCount < 1 {
		plan.CapacityBlocker = "runway_target_has_no_slots"
		return plan, nil
	}
	plan.CapacityKnown = true
	return plan, nil
}

func (s *service) delegatedFirstTouchDailyCapacity(
	ctx context.Context,
	orgID uuid.UUID,
	auth *models.CampaignPolicyAuthorization,
	now time.Time,
	cfg dispatch.Config,
) (int, int, string, error) {
	window, ok := delegatedSendWindowDuration(cfg.WindowStart, cfg.WindowEnd)
	if !ok || cfg.SendsPerHour < 1 || cfg.MinGap <= 0 || auth.MaxRatePerHour < 1 {
		return 0, 0, "business_capacity_unknown", nil
	}
	rows, err := s.delegatedDB.Query(ctx, `
		SELECT c.status::text,c.daily_limit,c.max_new_leads_per_day,
		       c.ramp_enabled,c.ramp_start,c.ramp_increment,c.ramp_ceiling,c.ramp_level,
		       ea.id,ea.user_id::text,ea.email,ea.status::text,ea.risk_band::text,
		       ea.worker_id IS NOT NULL,
		       CASE WHEN ea.provider='smtp_imap' THEN EXISTS (
		         SELECT 1 FROM email_accounts_smtp_imap smtp WHERE smtp.email_account_id=ea.id
		       ) ELSE EXISTS (
		         SELECT 1 FROM email_accounts_oauth oauth WHERE oauth.email_account_id=ea.id
		       ) END,
		       ea.campaign_limit,ea.min_wait_time,ea.warmup,ea.cold_ramp_started_at
		FROM campaigns c
		JOIN email_accounts ea ON ea.user_id=c.user_id AND ea.organization_id=c.organization_id
		WHERE c.organization_id=$1 AND c.id=$2
		  AND lower(ea.email)=lower($3)
		  AND (
		    (NOT EXISTS (SELECT 1 FROM campaign_senders cs WHERE cs.campaign_id=c.id AND cs.enabled)
		      AND NOT EXISTS (SELECT 1 FROM campaign_email_tags cet WHERE cet.campaign_id=c.id))
		    OR EXISTS (SELECT 1 FROM campaign_senders cs WHERE cs.campaign_id=c.id AND cs.email_account_id=ea.id AND cs.enabled)
		    OR EXISTS (
		        SELECT 1 FROM campaign_email_tags cet
		        JOIN email_tags et ON et.tag_id=cet.tag_id
		        WHERE cet.campaign_id=c.id AND et.email_id=ea.id
		      )
		  )
		ORDER BY ea.id`, orgID, auth.CampaignID, auth.SenderMailbox)
	if err != nil {
		return 0, 0, "", err
	}
	defer rows.Close()

	total, mailboxCount := 0, 0
	campaignDaily, maxNewLeads := 0, 0
	for rows.Next() {
		var campaignStatus, mailboxStatus, riskBand string
		var rampEnabled, workerAssigned, credentialsPresent bool
		var rampStart, rampIncrement, rampCeiling, rampLevel int
		var account models.Email
		if err := rows.Scan(
			&campaignStatus, &campaignDaily, &maxNewLeads,
			&rampEnabled, &rampStart, &rampIncrement, &rampCeiling, &rampLevel,
			&account.ID, &account.UserID, &account.Email, &mailboxStatus, &riskBand,
			&workerAssigned, &credentialsPresent, &account.CampaignLimit, &account.MinWaitTime,
			&account.Warmup, &account.ColdRampStartedAt,
		); err != nil {
			return 0, 0, "", err
		}
		if campaignStatus != "active" && campaignStatus != "draft" && campaignStatus != "paused" {
			return 0, mailboxCount, "canonical_campaign_not_schedulable", nil
		}
		if mailboxStatus != "active" || !workerAssigned || !credentialsPresent {
			return 0, mailboxCount, "mailbox_unavailable", nil
		}
		if riskBand == "quarantine" {
			return 0, mailboxCount, "suppression_or_risk_blocked", nil
		}
		if account.CampaignLimit < 1 || account.MinWaitTime < 1 {
			return 0, mailboxCount, "mailbox_capacity_unknown", nil
		}
		mailboxCount++
		mailboxCap := scheduler.ColdReadinessCeiling(account, now, 0, 0)
		if rampEnabled {
			mailboxCap = minInt(mailboxCap, scheduler.CampaignRampCeiling(true, rampStart, rampIncrement, rampCeiling, rampLevel))
		}
		mailboxCap = minInt(mailboxCap, account.CampaignLimit)
		mailboxCap = minInt(mailboxCap, maxInt(1, int(window/(time.Duration(account.MinWaitTime)*time.Second))))
		if riskBand == "risky" {
			mailboxCap = maxInt(1, mailboxCap/2)
		}
		total += mailboxCap
	}
	if err := rows.Err(); err != nil {
		return 0, 0, "", err
	}
	if mailboxCount == 0 || campaignDaily < 1 {
		return 0, mailboxCount, "mailbox_capacity_unknown", nil
	}
	total = minInt(total, campaignDaily)
	if maxNewLeads > 0 {
		total = minInt(total, maxNewLeads)
	}
	windowHours := maxInt(1, int(window/time.Hour))
	total = minInt(total, auth.MaxRatePerHour*windowHours)
	total = minInt(total, cfg.SendsPerHour*windowHours)
	total = minInt(total, maxInt(1, int(window/cfg.MinGap)))
	if total < 1 {
		return 0, mailboxCount, "mailbox_capacity_unknown", nil
	}
	return total, mailboxCount, "", nil
}

func delegatedSendWindowDuration(start, end string) (time.Duration, bool) {
	sh, sm, okStart := parseHHMM(start)
	eh, em, okEnd := parseHHMM(end)
	if !okStart || !okEnd {
		return 0, false
	}
	minutes := (eh*60 + em) - (sh*60 + sm)
	if minutes <= 0 {
		return 0, false
	}
	return time.Duration(minutes) * time.Minute, true
}

func (s *service) delegatedFirstTouchQueueSnapshot(
	ctx context.Context,
	orgID uuid.UUID,
	feed *models.OutreachFeedSyncState,
	policyAuthorizationID uuid.UUID,
	plan *delegatedFirstTouchRunwayPlan,
) error {
	if plan == nil {
		return nil
	}
	var furthest *time.Time
	err := s.delegatedDB.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE q.status='queued')::int,
		       count(*) FILTER (WHERE q.status='reserved')::int,
		       max(q.due_at)
		FROM confenge_delegated_first_touch_decisions d
		JOIN confenge_dispatch_queue q
		  ON q.organization_id=d.organization_id AND q.draft_id=d.draft_id AND q.message_key=d.queue_message_key
		WHERE d.organization_id=$1 AND d.state='QUEUED' AND q.status IN ('queued','reserved')
		  AND d.evidence_source_run_id=$2 AND d.source_snapshot_hash=$3
		  AND d.runtime_release_sha=$4 AND d.policy_authorization_id=$5`,
		orgID, feed.LastRunID, feed.LastSnapshotHash, s.cfg.RepositorySHA, policyAuthorizationID).
		Scan(&plan.QueuedCount, &plan.ReservedCount, &furthest)
	if err != nil {
		return err
	}
	plan.CurrentScheduledCount = plan.QueuedCount + plan.ReservedCount
	plan.FurthestDueAt = furthest
	return nil
}

func (s *service) delegatedFirstTouchReadyReservoirCount(
	ctx context.Context,
	orgID uuid.UUID,
	feed *models.OutreachFeedSyncState,
	auth *models.CampaignPolicyAuthorization,
) (int, error) {
	if feed == nil || auth == nil {
		return 0, nil
	}
	var count int
	err := s.delegatedDB.QueryRow(ctx, `
		SELECT count(*)::int
		FROM outreach_touchpoints t
		JOIN outreach_accounts a ON a.organization_id=t.organization_id AND a.id=t.account_id
		JOIN outreach_feed_sync_state feed ON feed.organization_id=t.organization_id
		WHERE t.organization_id=$1
		  AND t.ordinal=1 AND t.purpose='INITIAL' AND t.channel='EMAIL'
		  AND t.state IN ('DUE','NEEDS_REVIEW') AND t.contact_candidate_id IS NOT NULL
		  AND feed.last_status='completed' AND a.source_run_id=feed.last_run_id
		  AND t.source_run_id=feed.last_run_id AND a.initial_backlog_reason_code=''
		  AND a.last_import_run_id IS NOT NULL AND EXISTS (
		    SELECT 1 FROM outreach_contact_candidates c
		    WHERE c.organization_id=t.organization_id AND c.id=t.contact_candidate_id
		      AND c.last_import_run_id=a.last_import_run_id
		  )
		  AND NOT EXISTS (
		    SELECT 1 FROM confenge_delegated_first_touch_decisions d
		    WHERE d.organization_id=t.organization_id AND d.account_id=t.account_id
		      AND (d.state='SENT' OR (d.state<>'CANCELLED'
		        AND d.evidence_source_run_id=$2 AND d.source_snapshot_hash=$3
		        AND d.runtime_release_sha=$4 AND d.policy_authorization_id=$5))
		  )`, orgID, feed.LastRunID, feed.LastSnapshotHash, s.cfg.RepositorySHA, auth.ID).Scan(&count)
	return count, err
}

func (s *service) delegatedFirstTouchRunwayMetrics(ctx context.Context, orgID uuid.UUID) DelegatedFirstTouchRunwayMetrics {
	if s == nil {
		return DelegatedFirstTouchRunwayMetrics{
			PolicyVersion:   delegatedFirstTouchRunwayPolicyV1,
			CapacityBlocked: 1, CapacityBlocker: "runway_authority_unavailable",
		}
	}
	now := time.Now().UTC()
	out := DelegatedFirstTouchRunwayMetrics{
		PolicyVersion:     delegatedFirstTouchRunwayPolicyV1,
		TargetDays:        s.cfg.DelegatedFirstTouchRunwayDays,
		MinReadyReservoir: s.draftReviewBacklogTarget(),
	}
	if s.delegatedDB == nil || s.repo == nil || s.policyStore == nil {
		out.CapacityBlocked, out.CapacityBlocker = 1, "runway_authority_unavailable"
		return out
	}
	feed, feedErr := s.repo.GetFeedSyncState(ctx, orgID)
	settings, settingsErr := s.repo.GetOrgSettings(ctx, orgID)
	if feedErr != nil || settingsErr != nil || feed == nil || settings == nil || settings.CampaignID == nil {
		out.CapacityBlocked, out.CapacityBlocker = 1, "policy_or_feed_stale"
		return out
	}
	auth, authErr := s.policyStore.GetActiveCampaignPolicy(ctx, orgID, *settings.CampaignID, now)
	if authErr != nil || auth == nil {
		out.CapacityBlocked, out.CapacityBlocker = 1, "policy_or_feed_stale"
		return out
	}
	plan, planErr := s.delegatedFirstTouchRunwayPlan(ctx, orgID, feed, auth, now)
	if planErr != nil {
		out.CapacityBlocked, out.CapacityBlocker = 1, "capacity_read_failed"
		return out
	}
	out.CurrentScheduledCount = plan.CurrentScheduledCount
	out.TargetScheduledCount = plan.TargetScheduledCount
	out.QueuedCount, out.ReservedCount = plan.QueuedCount, plan.ReservedCount
	out.FurthestDueAt, out.CurrentRunwayUntil = plan.FurthestDueAt, plan.FurthestDueAt
	out.MailboxCount, out.DailyCapacity = plan.MailboxCount, plan.DailyCapacity
	if !plan.TargetRunwayUntil.IsZero() {
		target := plan.TargetRunwayUntil
		out.TargetRunwayUntil = &target
	}
	if plan.FurthestDueAt != nil && plan.FurthestDueAt.After(now) {
		out.RunwayHours = plan.FurthestDueAt.Sub(now).Hours()
		out.RunwayDays = out.RunwayHours / 24
	}
	if !plan.CapacityKnown {
		out.CapacityBlocked, out.CapacityBlocker = 1, plan.CapacityBlocker
	}
	if ready, err := s.delegatedFirstTouchReadyReservoirCount(ctx, orgID, feed, auth); err == nil {
		out.ReadyReservoirCount = ready
		if ready == 0 {
			out.NoCandidate = 1
		}
	}
	_ = s.delegatedDB.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE state='HOLD')::int,
		       count(*) FILTER (WHERE state='CANCELLED' AND blocker_codes ? $2)::int,
		       count(*) FILTER (WHERE readback_at >= now()-interval '1 hour')::int
		FROM confenge_delegated_first_touch_decisions
		WHERE organization_id=$1`, orgID, delegatedBindingRefreshReason).
		Scan(&out.Held, &out.StaleRetired, &out.FillRate)
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
