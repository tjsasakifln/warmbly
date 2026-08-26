package dispatch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type pgRowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

var errMailboxNotConfigured = errors.New("configured mailbox not found")

type mailboxAuthority struct {
	id                 uuid.UUID
	organizationID     uuid.UUID
	email              string
	status             string
	provider           string
	dailyCap           int
	minWaitSeconds     int
	timezone           string
	credentialsReady   bool
	workerAssigned     bool
	workerHealthy      bool
	workerLastSeenAt   *time.Time
	authState          string
	authSPF            bool
	authDKIM           bool
	authDMARC          bool
	authDMARCPolicy    string
	authCheckedAt      *time.Time
	createdAt          time.Time
	warmupStartedAt    *time.Time
	warmupDays         int
	coldRampStartedAt  *time.Time
	riskBand           string
	warmupHealth       string
	warmupHealthReason string
	blockedUntil       *time.Time
	latestErrorCode    string
}

func (s *PGStore) GetMailboxEnvelope(ctx context.Context, orgID, emailAccountID uuid.UUID, now time.Time) (MailboxEnvelope, error) {
	authority, err := queryMailboxAuthority(ctx, s.db, orgID, emailAccountID, false)
	if err != nil {
		return MailboxEnvelope{}, err
	}
	return authority.envelope(now), nil
}

func queryMailboxAuthority(ctx context.Context, q pgRowQuerier, orgID, emailAccountID uuid.UUID, lock bool) (mailboxAuthority, error) {
	query := `
		SELECT ea.id, ea.organization_id, ea.email, ea.status::text, ea.provider::text,
		       ea.campaign_limit, ea.min_wait_time, ea.timezone,
		       CASE WHEN ea.provider = 'smtp_imap' THEN EXISTS (
		           SELECT 1 FROM email_accounts_smtp_imap smtp WHERE smtp.email_account_id = ea.id
		       ) ELSE EXISTS (
		           SELECT 1 FROM email_accounts_oauth oauth WHERE oauth.email_account_id = ea.id
		       ) END AS credentials_ready,
		       ea.worker_id IS NOT NULL,
		       EXISTS (
		           SELECT 1 FROM workers w
		           WHERE w.id=ea.worker_id AND w.active
		             AND w.last_seen_at > now() - interval '10 minutes'
		       ),
		       (SELECT w.last_seen_at FROM workers w WHERE w.id=ea.worker_id),
		       ea.auth_state, ea.auth_spf, ea.auth_dkim, ea.auth_dmarc,
		       ea.auth_dmarc_policy, ea.auth_checked_at, ea.created_at,
		       ea.warmup, ea.warmup_days, ea.cold_ramp_started_at, ea.risk_band::text,
		       COALESCE(health.health_state, 'healthy'),
		       COALESCE(health.last_health_reason, ''), health.blocked_until,
		       COALESCE(last_error.error_code, '')
		FROM email_accounts ea
		LEFT JOIN LATERAL (
		    SELECT wpp.health_state, wpp.last_health_reason, wpp.blocked_until
		    FROM warmup_pool_participants wpp
		    WHERE wpp.email_account_id = ea.id
		    ORDER BY CASE wpp.health_state
		        WHEN 'blocked' THEN 5 WHEN 'quarantined' THEN 4
		        WHEN 'throttled' THEN 3 WHEN 'watch' THEN 2 ELSE 1 END DESC
		    LIMIT 1
		) health ON true
		LEFT JOIN LATERAL (
		    SELECT eae.error_code
		    FROM email_account_errors eae
		    WHERE eae.email_account_id = ea.id AND eae.resolved_at IS NULL
		    ORDER BY eae.created_at DESC
		    LIMIT 1
		) last_error ON true
		WHERE ea.organization_id = $1 AND ea.id = $2`
	if lock {
		query += " FOR SHARE OF ea"
	}
	var out mailboxAuthority
	err := q.QueryRow(ctx, query, orgID, emailAccountID).Scan(
		&out.id, &out.organizationID, &out.email, &out.status, &out.provider,
		&out.dailyCap, &out.minWaitSeconds, &out.timezone,
		&out.credentialsReady, &out.workerAssigned, &out.workerHealthy, &out.workerLastSeenAt,
		&out.authState, &out.authSPF, &out.authDKIM, &out.authDMARC,
		&out.authDMARCPolicy, &out.authCheckedAt, &out.createdAt,
		&out.warmupStartedAt, &out.warmupDays, &out.coldRampStartedAt, &out.riskBand,
		&out.warmupHealth, &out.warmupHealthReason, &out.blockedUntil,
		&out.latestErrorCode,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, errMailboxNotConfigured
	}
	return out, err
}

func (m mailboxAuthority) envelope(now time.Time) MailboxEnvelope {
	minGap := time.Duration(m.minWaitSeconds) * time.Second
	hourly := DerivedHourlyCap(minGap)
	out := MailboxEnvelope{
		EmailAccountID:    m.id,
		OrganizationID:    m.organizationID,
		DailyCap:          effectiveMailboxCap(m.dailyCap, nil),
		HourlyCap:         hourly,
		MinGap:            minGap,
		Ready:             true,
		Timezone:          m.timezone,
		ProviderCapSource: "unknown",
	}
	switch {
	case m.status != "active":
		out.Ready, out.HealthReason = false, "mailbox_disabled"
	case !m.credentialsReady:
		out.Ready, out.HealthReason = false, "credentials_missing"
	case !m.workerAssigned:
		out.Ready, out.HealthReason = false, "worker_missing"
	case !m.workerHealthy:
		out.Ready, out.HealthReason = false, "worker_unhealthy"
	case m.authState == "unknown" || m.authCheckedAt == nil:
		out.Ready, out.HealthReason = false, "dns_auth_unknown"
	case m.authState != "passing" || !m.authSPF || !m.authDKIM || !m.authDMARC:
		out.Ready, out.HealthReason = false, "dns_auth_not_passing"
	case m.riskBand == "quarantine":
		out.Ready, out.HealthReason = false, "mailbox_quarantined"
	case (m.warmupHealth == "blocked" || m.warmupHealth == "quarantined") &&
		(m.blockedUntil == nil || m.blockedUntil.After(now)):
		out.Ready, out.HealthReason = false, "warmup_"+m.warmupHealth
	default:
		errorClass := ClassifyProviderError(m.latestErrorCode, "")
		switch errorClass {
		case "auth_failure", "provider_block", "rate_limit", "provider_interrupted":
			out.Ready, out.HealthReason = false, errorClass
		}
	}
	if out.DailyCap < 1 || out.HourlyCap < 1 || out.MinGap <= 0 {
		out.Ready, out.HealthReason = false, "mailbox_rate_bounds_invalid"
	}
	return out
}

func (s *PGStore) MailboxCapacitySnapshot(ctx context.Context, orgID uuid.UUID, now time.Time, cfg Config) (MailboxCapacitySnapshot, error) {
	rows, err := s.db.Query(ctx, `
		SELECT ea.id
		FROM email_accounts ea
		WHERE ea.organization_id = $1
		ORDER BY lower(ea.email), ea.id`, orgID)
	if err != nil {
		return MailboxCapacitySnapshot{}, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return MailboxCapacitySnapshot{}, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return MailboxCapacitySnapshot{}, err
	}
	rows.Close()

	startToday, endToday := localDayBounds(now, cfg.Timezone)
	snapshot := MailboxCapacitySnapshot{Mailboxes: make([]MailboxCapacity, 0, len(ids))}
	for _, id := range ids {
		authority, err := queryMailboxAuthority(ctx, s.db, orgID, id, false)
		if err != nil {
			return MailboxCapacitySnapshot{}, err
		}
		envelope := authority.envelope(now)
		mailbox := MailboxCapacity{
			EmailAccountID:       authority.id,
			Email:                authority.email,
			Enabled:              authority.status == "active",
			Status:               authority.status,
			Provider:             authority.provider,
			CredentialsReady:     authority.credentialsReady,
			WorkerAssigned:       authority.workerAssigned,
			WorkerHealthy:        authority.workerHealthy,
			WorkerLastSeenAt:     authority.workerLastSeenAt,
			AuthState:            authority.authState,
			AuthSPF:              authority.authSPF,
			AuthDKIM:             authority.authDKIM,
			AuthDMARC:            authority.authDMARC,
			AuthDMARCPolicy:      authority.authDMARCPolicy,
			AuthCheckedAt:        authority.authCheckedAt,
			MailboxAgeDays:       wholeDays(now.Sub(authority.createdAt)),
			WarmupStartedAt:      authority.warmupStartedAt,
			WarmupDaysObserved:   authority.warmupDays,
			ColdRampStartedAt:    authority.coldRampStartedAt,
			ConfiguredDailyCap:   authority.dailyCap,
			ConfiguredMinWaitSec: authority.minWaitSeconds,
			DerivedHourlyCap:     DerivedHourlyCap(envelope.MinGap),
			EffectiveDailyCap:    envelope.DailyCap,
			EffectiveHourlyCap:   minPositive(envelope.HourlyCap, cfg.SendsPerHour),
			ProviderDailyCap:     envelope.ProviderDailyCap,
			ProviderHourlyCap:    envelope.ProviderHourlyCap,
			ProviderCapSource:    envelope.ProviderCapSource,
			BusinessWindow: MailboxBusinessWindow{
				Timezone: cfg.Timezone, Start: cfg.WindowStart, End: cfg.WindowEnd,
				BusinessDaysOnly: cfg.BusinessDaysOnly,
			},
			Health:       "ready",
			HealthReason: "ready",
			Unknown:      []string{"provider_daily_cap", "provider_hourly_cap"},
		}
		if authority.warmupStartedAt != nil {
			age := wholeDays(now.Sub(*authority.warmupStartedAt))
			mailbox.WarmupAgeDays = &age
		} else {
			mailbox.Unknown = append(mailbox.Unknown, "warmup_age")
		}
		if !envelope.Ready {
			mailbox.Health, mailbox.HealthReason = "blocked", envelope.HealthReason
		}
		if authority.warmupHealth == "watch" || authority.warmupHealth == "throttled" {
			signal := "warmup_" + authority.warmupHealth
			mailbox.HealthSignals = append(mailbox.HealthSignals, signal)
			if mailbox.Health == "ready" {
				mailbox.Health = "degraded"
				mailbox.HealthReason = signal
			}
		}
		if err := s.fillMailboxSignals(ctx, &mailbox, startToday, endToday, now); err != nil {
			return MailboxCapacitySnapshot{}, err
		}
		if err := s.fillMailboxErrors(ctx, &mailbox, now); err != nil {
			return MailboxCapacitySnapshot{}, err
		}
		if mailbox.Latest.AttemptAt == nil {
			mailbox.Unknown = append(mailbox.Unknown, "attempt_observations")
		}
		if mailbox.Latest.AcceptedAt == nil {
			mailbox.Unknown = append(mailbox.Unknown, "acceptance_observations")
		}
		if mailbox.Latest.ProviderRejectionAt == nil {
			mailbox.Unknown = append(mailbox.Unknown, "provider_failure_observations")
		}
		mailbox.Unknown = append(mailbox.Unknown, "delivery_evidence")
		candidate := nextRollingSlot(now, mailbox.occupiedAt, mailbox.EffectiveHourlyCap, envelope.MinGap, RollingWindow)
		if mailbox.UsedToday >= mailbox.EffectiveDailyCap {
			candidate = nextLocalDay(now, cfg.Timezone)
		}
		candidate = NextEligibleSlot(candidate, cfg.Timezone, cfg.WindowStart, cfg.WindowEnd, cfg.BusinessDaysOnly)
		if mailbox.Health != "blocked" {
			mailbox.NextEligibleSlot = &candidate
		}
		snapshot.Mailboxes = append(snapshot.Mailboxes, mailbox)
	}
	if err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM (
		    SELECT q.message_key
		    FROM confenge_dispatch_queue q
		    WHERE q.organization_id = $1 AND q.channel = 'EMAIL' AND q.status IN ('queued','reserved')
		      AND NOT EXISTS (
		          SELECT 1 FROM outreach_touchpoints tp
		          WHERE tp.organization_id=q.organization_id AND tp.draft_id=q.draft_id
		            AND tp.channel='EMAIL' AND tp.state='QUEUED'
		      )
		    UNION
		    SELECT 'touchpoint:' || tp.id::text
		    FROM outreach_touchpoints tp
		    WHERE tp.organization_id = $1 AND tp.channel = 'EMAIL' AND tp.state = 'QUEUED'
		) backlog`, orgID).Scan(&snapshot.QueuedMessages); err != nil {
		return MailboxCapacitySnapshot{}, err
	}
	return snapshot, nil
}

func (s *PGStore) fillMailboxSignals(ctx context.Context, mailbox *MailboxCapacity, startToday, endToday, now time.Time) error {
	if mailbox == nil {
		return nil
	}
	err := s.db.QueryRow(ctx, `
		SELECT
		  (SELECT max(r.attempted_at) FROM confenge_dispatch_reservations r WHERE r.email_account_id=$1),
		  (SELECT max(ds.sent_at) FROM confenge_dispatch_sends ds WHERE ds.email_account_id=$1),
		  (SELECT max(de.created_at) FROM deliverability_events de JOIN tasks t ON t.id=de.task_id WHERE t.email_account_id=$1 AND de.event_type='bounce'),
		  (SELECT max(de.created_at) FROM deliverability_events de JOIN tasks t ON t.id=de.task_id WHERE t.email_account_id=$1 AND de.event_type='complaint'),
		  (SELECT max(de.created_at) FROM deliverability_events de JOIN tasks t ON t.id=de.task_id WHERE t.email_account_id=$1 AND de.event_type='reply'),
		  (SELECT max(df.occurred_at) FROM confenge_dispatch_failures df WHERE df.email_account_id=$1 AND df.error_class <> 'unknown'),
		  COALESCE((SELECT df.error_class FROM confenge_dispatch_failures df WHERE df.email_account_id=$1 AND df.error_class <> 'unknown' ORDER BY df.occurred_at DESC LIMIT 1), ''),
		  (SELECT count(*) FROM confenge_dispatch_sends ds WHERE ds.email_account_id=$1 AND ds.sent_at >= $2),
		  (SELECT count(*) FROM confenge_dispatch_sends ds WHERE ds.email_account_id=$1 AND ds.sent_at >= $3 AND ds.sent_at < $4),
		  (SELECT count(*) FROM confenge_dispatch_sends ds WHERE ds.email_account_id=$1 AND ds.sent_at >= $5)
	`, mailbox.EmailAccountID, now.Add(-time.Hour), startToday, endToday, now.Add(-7*24*time.Hour)).Scan(
		&mailbox.Latest.AttemptAt, &mailbox.Latest.AcceptedAt, &mailbox.Latest.BounceAt,
		&mailbox.Latest.ComplaintAt, &mailbox.Latest.ReplyAt,
		&mailbox.Latest.ProviderRejectionAt, &mailbox.Latest.ProviderErrorClass,
		&mailbox.Throughput.AcceptedLastHour, &mailbox.Throughput.AcceptedToday,
		&mailbox.Throughput.AcceptedLast7d,
	)
	if err != nil {
		return err
	}
	rows, err := s.db.Query(ctx, `
		SELECT max(observed_at) AS observed_at FROM (
		    SELECT 'task:' || t.id::text AS use_key, t.completed_at AS observed_at
		    FROM tasks t
		    WHERE t.email_account_id=$1 AND t.task_type='campaign' AND t.status='completed'
		      AND t.completed_at >= $2
		    UNION ALL
		    SELECT COALESCE('task:' || ds.task_id::text, 'dispatch-send:' || ds.id::text), ds.sent_at
		    FROM confenge_dispatch_sends ds
		    WHERE ds.email_account_id=$1 AND ds.sent_at >= $2
		    UNION ALL
		    SELECT COALESCE('task:' || r.task_id::text, 'reservation:' || r.id::text), r.attempted_at
		    FROM confenge_dispatch_reservations r
		    WHERE r.email_account_id=$1 AND r.attempted_at >= $2 AND r.state <> 'committed'
		    UNION ALL
		    SELECT COALESCE('task:' || r.task_id::text, 'reservation:' || r.id::text), r.reserved_at
		    FROM confenge_dispatch_reservations r
		    WHERE r.email_account_id=$1 AND r.state='reserved' AND r.lease_until > $3
		      AND r.attempted_at IS NULL
		) observed GROUP BY use_key ORDER BY max(observed_at)`, mailbox.EmailAccountID, now.Add(-RollingWindow), now)
	if err != nil {
		return err
	}
	for rows.Next() {
		var observed time.Time
		if err := rows.Scan(&observed); err != nil {
			rows.Close()
			return err
		}
		mailbox.occupiedAt = append(mailbox.occupiedAt, observed)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	return s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM (
		    SELECT t.id::text AS use_key
		    FROM tasks t
		    WHERE t.email_account_id=$1 AND t.task_type='campaign' AND t.status='completed'
		      AND t.completed_at >= $2 AND t.completed_at < $3
		    UNION
		    SELECT COALESCE(r.task_id::text, 'reservation:' || r.id::text)
		    FROM confenge_dispatch_reservations r
		    WHERE r.email_account_id=$1 AND (
		      (r.reserved_at >= $2 AND r.reserved_at < $3 AND r.state IN ('reserved','committed'))
		      OR (r.attempted_at >= $2 AND r.attempted_at < $3)
		    )
		) used`, mailbox.EmailAccountID, startToday, endToday).Scan(&mailbox.UsedToday)
}

func (s *PGStore) fillMailboxErrors(ctx context.Context, mailbox *MailboxCapacity, now time.Time) error {
	type fact struct {
		class string
		key   string
		at    time.Time
	}
	var facts []fact
	rows, err := s.db.Query(ctx, `
		SELECT COALESCE(task_id::text, 'account-error:' || id::text), error_code, message, created_at
		FROM email_account_errors
		WHERE email_account_id=$1 AND resolved_at IS NULL
		ORDER BY created_at DESC`, mailbox.EmailAccountID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key, code, message string
		var at time.Time
		if err := rows.Scan(&key, &code, &message, &at); err != nil {
			rows.Close()
			return err
		}
		facts = append(facts, fact{class: ClassifyProviderError(code, message), key: key, at: at})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = s.db.Query(ctx, `
		SELECT COALESCE(task_id::text, 'failure:' || id::text), error_class, occurred_at
		FROM confenge_dispatch_failures
		WHERE email_account_id=$1 AND occurred_at >= $2
		ORDER BY occurred_at DESC`, mailbox.EmailAccountID, now.Add(-24*time.Hour))
	if err != nil {
		return err
	}
	for rows.Next() {
		var item fact
		if err := rows.Scan(&item.key, &item.class, &item.at); err != nil {
			rows.Close()
			return err
		}
		facts = append(facts, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	counts := map[string]int{}
	latest := map[string]time.Time{}
	seen := map[string]bool{}
	for _, item := range facts {
		factKey := item.class + "|" + item.key
		if seen[factKey] {
			if item.at.After(latest[item.class]) {
				latest[item.class] = item.at
			}
			continue
		}
		seen[factKey] = true
		counts[item.class]++
		if item.at.After(latest[item.class]) {
			latest[item.class] = item.at
		}
	}
	mailbox.errorCounts = counts
	mailbox.errorLatest = latest
	blocking := ""
	for _, class := range []string{"auth_failure", "provider_block", "rate_limit", "provider_interrupted"} {
		if counts[class] > 0 {
			blocking = class
			mailbox.HealthSignals = append(mailbox.HealthSignals, class)
			break
		}
	}
	if blocking != "" {
		mailbox.Health, mailbox.HealthReason = "blocked", blocking
		mailbox.NextEligibleSlot = nil
	}
	for _, class := range []string{"provider_4xx", "provider_5xx"} {
		if counts[class] > 0 {
			mailbox.HealthSignals = append(mailbox.HealthSignals, class)
			if mailbox.Health == "ready" {
				mailbox.Health, mailbox.HealthReason = "degraded", class
			}
		}
	}
	if mailbox.Latest.BounceAt != nil {
		mailbox.HealthSignals = append(mailbox.HealthSignals, "hard_bounce_observed")
		if mailbox.Health == "ready" {
			mailbox.Health, mailbox.HealthReason = "degraded", "hard_bounce_observed"
		}
	}
	if mailbox.Latest.ComplaintAt != nil {
		mailbox.HealthSignals = append(mailbox.HealthSignals, "complaint_observed")
		if mailbox.Health == "ready" {
			mailbox.Health, mailbox.HealthReason = "degraded", "complaint_observed"
		}
	}
	return nil
}

func listMailboxOccupiedTx(ctx context.Context, tx pgx.Tx, emailAccountID uuid.UUID, now time.Time, window time.Duration) ([]time.Time, time.Time, error) {
	rows, err := tx.Query(ctx, `
		SELECT max(observed_at) AS observed_at FROM (
		    SELECT 'task:' || t.id::text AS use_key, t.completed_at AS observed_at
		    FROM tasks t
		    WHERE t.email_account_id=$1 AND t.task_type='campaign' AND t.status='completed'
		      AND t.completed_at >= $2 AND t.completed_at <= $3
		    UNION ALL
		    SELECT COALESCE('task:' || ds.task_id::text, 'dispatch-send:' || ds.id::text), ds.sent_at
		    FROM confenge_dispatch_sends ds
		    WHERE ds.email_account_id=$1 AND ds.sent_at >= $2 AND ds.sent_at <= $3
		    UNION ALL
		    SELECT COALESCE('task:' || r.task_id::text, 'reservation:' || r.id::text), r.attempted_at
		    FROM confenge_dispatch_reservations r
		    WHERE r.email_account_id=$1 AND r.attempted_at >= $2 AND r.attempted_at <= $3
		      AND r.state <> 'committed'
		    UNION ALL
		    SELECT COALESCE('task:' || r.task_id::text, 'reservation:' || r.id::text), r.reserved_at
		    FROM confenge_dispatch_reservations r
		    WHERE r.email_account_id=$1 AND r.state='reserved' AND r.lease_until > $3
		      AND r.attempted_at IS NULL AND r.reserved_at >= $2 AND r.reserved_at <= $3
		) observed GROUP BY use_key ORDER BY max(observed_at)`, emailAccountID, now.Add(-window), now)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer rows.Close()
	var values []time.Time
	var last time.Time
	for rows.Next() {
		var value time.Time
		if err := rows.Scan(&value); err != nil {
			return nil, time.Time{}, err
		}
		values = append(values, value)
		if value.After(last) {
			last = value
		}
	}
	return values, last, rows.Err()
}

func countMailboxDailyUseTx(ctx context.Context, tx pgx.Tx, emailAccountID uuid.UUID, start, end time.Time) (int, error) {
	var used int
	err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM (
		    SELECT t.id::text AS use_key
		    FROM tasks t
		    WHERE t.email_account_id=$1 AND t.task_type='campaign' AND t.status='completed'
		      AND t.completed_at >= $2 AND t.completed_at < $3
		    UNION
		    SELECT COALESCE(r.task_id::text, 'reservation:' || r.id::text)
		    FROM confenge_dispatch_reservations r
		    WHERE r.email_account_id=$1 AND (
		      (r.reserved_at >= $2 AND r.reserved_at < $3 AND r.state IN ('reserved','committed'))
		      OR (r.attempted_at >= $2 AND r.attempted_at < $3)
		    )
		) used`, emailAccountID, start, end).Scan(&used)
	return used, err
}

func (s *PGStore) MarkAttempt(ctx context.Context, messageKey string, attemptedAt time.Time) error {
	if attemptedAt.IsZero() {
		attemptedAt = time.Now().UTC()
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE confenge_dispatch_reservations
		SET attempted_at = CASE
		    WHEN attempted_at IS NULL OR attempted_at < $2 THEN $2 ELSE attempted_at END
		WHERE message_key=$1`, messageKey, attemptedAt.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("dispatch reservation not found for provider attempt")
	}
	return nil
}

func (s *PGStore) RecordProviderFailure(ctx context.Context, taskID uuid.UUID, errorCode, errorText string, occurredAt time.Time) error {
	if taskID == uuid.Nil {
		return nil
	}
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var reservationID, orgID uuid.UUID
	var emailAccountID, draftID *uuid.UUID
	var channel, messageKey string
	err = tx.QueryRow(ctx, `
		SELECT id, organization_id, email_account_id, draft_id, channel, message_key
		FROM confenge_dispatch_reservations
		WHERE task_id=$1
		ORDER BY reserved_at DESC
		LIMIT 1
		FOR UPDATE`, taskID).Scan(&reservationID, &orgID, &emailAccountID, &draftID, &channel, &messageKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	errorClass := ClassifyProviderError(errorCode, errorText)
	_, err = tx.Exec(ctx, `
		INSERT INTO confenge_dispatch_failures
			(organization_id, email_account_id, task_id, channel, message_key, draft_id,
			 error_code, error_class, error_text, occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (task_id, error_code) WHERE task_id IS NOT NULL AND error_code <> ''
		DO UPDATE SET error_class=EXCLUDED.error_class, error_text=EXCLUDED.error_text,
		              occurred_at=GREATEST(confenge_dispatch_failures.occurred_at, EXCLUDED.occurred_at)`,
		orgID, emailAccountID, taskID, channel, messageKey, draftID,
		errorCode, errorClass, errorText, occurredAt.UTC())
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE confenge_dispatch_reservations
		SET state='failed', last_error=$2, attempted_at=COALESCE(attempted_at,$3)
		WHERE id=$1 AND state='reserved'`, reservationID, errorText, occurredAt.UTC())
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func localDayBounds(now time.Time, timezone string) (time.Time, time.Time) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	local := now.In(loc)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	return start.UTC(), start.AddDate(0, 0, 1).UTC()
}

func wholeDays(duration time.Duration) int {
	if duration <= 0 {
		return 0
	}
	return int(duration / (24 * time.Hour))
}

func healthAlert(mailbox *MailboxCapacity, code, severity, reason string, count int, at time.Time) CapacityAlert {
	id := mailbox.EmailAccountID
	alert := CapacityAlert{Code: code, Severity: severity, EmailAccountID: &id, Count: count, Reason: reason}
	if !at.IsZero() {
		value := at.UTC()
		alert.OccurredAt = &value
	}
	return alert
}

func mailboxAlerts(mailbox *MailboxCapacity) []CapacityAlert {
	if mailbox == nil {
		return nil
	}
	var alerts []CapacityAlert
	switch mailbox.HealthReason {
	case "auth_failure", "dns_auth_unknown", "dns_auth_not_passing", "credentials_missing":
		alerts = append(alerts, healthAlert(mailbox, AlertAuthFailure, "critical", mailbox.HealthReason, 1, timeOrZero(mailbox.AuthCheckedAt)))
	case "provider_interrupted", "worker_missing":
		alerts = append(alerts, healthAlert(mailbox, AlertProviderInterrupted, "critical", mailbox.HealthReason, 1, timeOrZero(mailbox.Latest.ProviderRejectionAt)))
	case "rate_limit":
		alerts = append(alerts, healthAlert(mailbox, AlertRateLimit, "critical", mailbox.HealthReason, 1, timeOrZero(mailbox.Latest.ProviderRejectionAt)))
	case "provider_block":
		alerts = append(alerts, healthAlert(mailbox, AlertProviderInterrupted, "critical", mailbox.HealthReason, 1, timeOrZero(mailbox.Latest.ProviderRejectionAt)))
	case "mailbox_disabled":
		alerts = append(alerts, healthAlert(mailbox, AlertMailboxDisabled, "critical", mailbox.HealthReason, 1, time.Time{}))
	}
	if mailbox.errorCounts["auth_failure"] > 0 && mailbox.HealthReason != "auth_failure" {
		alerts = append(alerts, healthAlert(mailbox, AlertAuthFailure, "critical", "provider authentication failure observed", mailbox.errorCounts["auth_failure"], mailbox.errorLatest["auth_failure"]))
	}
	if mailbox.errorCounts["provider_interrupted"] > 0 && mailbox.HealthReason != "provider_interrupted" {
		alerts = append(alerts, healthAlert(mailbox, AlertProviderInterrupted, "critical", "provider interruption observed", mailbox.errorCounts["provider_interrupted"], mailbox.errorLatest["provider_interrupted"]))
	}
	if mailbox.errorCounts["provider_block"] > 0 && mailbox.HealthReason != "provider_block" {
		alerts = append(alerts, healthAlert(mailbox, AlertProviderInterrupted, "critical", "provider block observed", mailbox.errorCounts["provider_block"], mailbox.errorLatest["provider_block"]))
	}
	if mailbox.errorCounts["rate_limit"] > 0 && mailbox.HealthReason != "rate_limit" {
		alerts = append(alerts, healthAlert(mailbox, AlertRateLimit, "critical", "provider rate-limit rejection observed", mailbox.errorCounts["rate_limit"], mailbox.errorLatest["rate_limit"]))
	}
	if mailbox.Latest.ComplaintAt != nil {
		alerts = append(alerts, healthAlert(mailbox, AlertComplaint, "critical", "complaint_observed", 1, *mailbox.Latest.ComplaintAt))
	}
	if mailbox.errorCounts["provider_4xx"] >= 2 {
		alerts = append(alerts, healthAlert(mailbox, AlertRepeated4xx, "warning", "repeated factual provider 4xx responses", mailbox.errorCounts["provider_4xx"], mailbox.errorLatest["provider_4xx"]))
	}
	if mailbox.errorCounts["provider_5xx"] >= 2 {
		alerts = append(alerts, healthAlert(mailbox, AlertRepeated5xx, "warning", "repeated factual provider 5xx responses", mailbox.errorCounts["provider_5xx"], mailbox.errorLatest["provider_5xx"]))
	}
	return alerts
}

func timeOrZero(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

func normalizeHealthSignals(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
