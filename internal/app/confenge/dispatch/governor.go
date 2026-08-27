package dispatch

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Governor struct {
	cfg       Config
	store     Store
	clock     Clock
	reserveMu sync.Mutex
}

func NewGovernor(cfg Config, store Store, clock Clock) *Governor {
	if clock == nil {
		clock = RealClock{}
	}
	if cfg.SendsPerHour <= 0 {
		cfg.SendsPerHour = DefaultSendsPerHour
	}
	// MinGap == 0 is valid (no gap); only negative means "unset".
	if cfg.MinGap < 0 {
		cfg.MinGap = time.Duration(DefaultMinGapSeconds) * time.Second
	}
	if cfg.Timezone == "" {
		cfg.Timezone = DefaultTimezone
	}
	if cfg.WindowStart == "" {
		cfg.WindowStart = DefaultWindowStart
	}
	if cfg.WindowEnd == "" {
		cfg.WindowEnd = DefaultWindowEnd
	}
	if cfg.LeaseTTL <= 0 {
		cfg.LeaseTTL = DefaultLeaseTTL
	}
	return &Governor{cfg: cfg, store: store, clock: clock}
}

func (g *Governor) Config() Config { return g.cfg }

func (g *Governor) SetConfig(cfg Config) {
	if cfg.SendsPerHour > 0 {
		g.cfg.SendsPerHour = cfg.SendsPerHour
	}
	if cfg.MinGap >= 0 {
		g.cfg.MinGap = cfg.MinGap
	}
	if cfg.Timezone != "" {
		g.cfg.Timezone = cfg.Timezone
	}
	if cfg.WindowStart != "" {
		g.cfg.WindowStart = cfg.WindowStart
	}
	if cfg.WindowEnd != "" {
		g.cfg.WindowEnd = cfg.WindowEnd
	}
	if cfg.LeaseTTL > 0 {
		g.cfg.LeaseTTL = cfg.LeaseTTL
	}
	g.cfg.EnvPaused = cfg.EnvPaused
	g.cfg.EnvPauseReason = cfg.EnvPauseReason
}

func (g *Governor) TryReserve(ctx context.Context, req ReserveRequest) (ReserveResult, error) {
	g.reserveMu.Lock()
	defer g.reserveMu.Unlock()

	now := g.clock.Now().UTC()
	capN := g.cfg.SendsPerHour
	if req.CapOverride > 0 && (capN < 1 || req.CapOverride < capN) {
		capN = req.CapOverride
	}
	if capN < 1 {
		capN = DefaultSendsPerHour
	}
	out := ReserveResult{Cap: capN}
	effectiveGap := g.cfg.MinGap
	var mailbox *MailboxEnvelope

	if req.MessageKey == "" {
		return out, fmt.Errorf("message_key required")
	}
	if req.Channel != ChannelEmail && req.Channel != ChannelWhatsApp {
		return out, fmt.Errorf("invalid channel %q", req.Channel)
	}
	if _, committed, err := g.store.GetSendByKey(ctx, req.MessageKey); err != nil {
		return out, err
	} else if committed {
		out.Allowed = true
		out.AlreadyCommitted = true
		out.Reason = "already_sent"
		return out, nil
	}
	if req.Channel == ChannelEmail {
		mailboxStore, ok := g.store.(MailboxStore)
		if !ok {
			return out, fmt.Errorf("dispatch store cannot resolve mailbox capacity")
		}
		mailboxID := uuid.Nil
		if req.EmailAccountID != nil {
			mailboxID = *req.EmailAccountID
		}
		envelope, err := mailboxStore.GetMailboxEnvelope(ctx, req.OrganizationID, mailboxID, now)
		if errors.Is(err, errMailboxNotConfigured) {
			out.Reason = "mailbox_not_configured"
			return out, nil
		}
		if err != nil {
			return out, err
		}
		mailbox = &envelope
		if !envelope.Ready {
			out.Reason = envelope.HealthReason
			return out, nil
		}
		if envelope.MinGap > effectiveGap {
			effectiveGap = envelope.MinGap
		}
	}

	if g.cfg.EnvPaused {
		out.Reason = "env_paused"
		if g.cfg.EnvPauseReason != "" {
			out.Reason = g.cfg.EnvPauseReason
		}
		out.NextSlot = NextEligibleSlot(now.Add(effectiveGap), g.cfg.Timezone, g.cfg.WindowStart, g.cfg.WindowEnd, g.cfg.BusinessDaysOnly)
		return out, nil
	}
	ctrl, err := g.store.GetControl(ctx)
	if err != nil {
		return out, err
	}
	if ctrl.Paused {
		out.Reason = "paused"
		if ctrl.PauseReason != "" {
			out.Reason = ctrl.PauseReason
		}
		out.NextSlot = NextEligibleSlot(now.Add(effectiveGap), g.cfg.Timezone, g.cfg.WindowStart, g.cfg.WindowEnd, g.cfg.BusinessDaysOnly)
		return out, nil
	}

	inWin, werr := InSendWindowBusiness(now, g.cfg.Timezone, g.cfg.WindowStart, g.cfg.WindowEnd, g.cfg.BusinessDaysOnly)
	if werr != nil {
		return out, werr
	}
	if !inWin {
		if g.cfg.BusinessDaysOnly && !IsBusinessDay(now, g.cfg.Timezone) {
			out.Reason = "outside_business_day"
		} else {
			out.Reason = "outside_send_window"
		}
		out.NextSlot = NextWindowOpenBusiness(now, g.cfg.Timezone, g.cfg.WindowStart, g.cfg.WindowEnd, g.cfg.BusinessDaysOnly)
		return out, nil
	}

	// Full reserve decision under store serialization (multi-worker safe).
	atomic, err := g.store.TryReserveAtomic(ctx, AtomicReserveInput{
		Req: req, Now: now, Cap: capN, MinGap: g.cfg.MinGap,
		Mailbox: mailbox, LeaseTTL: g.cfg.LeaseTTL, Window: RollingWindow,
	})
	if err != nil {
		return out, err
	}
	out.Allowed = atomic.Allowed
	out.AlreadyCommitted = atomic.AlreadyCommitted
	out.Reservation = atomic.Reservation
	out.Reason = atomic.Reason
	out.NextSlot = atomic.NextSlot
	if !out.NextSlot.IsZero() {
		out.NextSlot = NextEligibleSlot(out.NextSlot, g.cfg.Timezone, g.cfg.WindowStart, g.cfg.WindowEnd, g.cfg.BusinessDaysOnly)
	}
	out.SentLastHour = atomic.SentLastHour
	return out, nil
}

func (g *Governor) Commit(ctx context.Context, reservationID uuid.UUID) error {
	return g.store.CommitReservation(ctx, reservationID, g.clock.Now().UTC())
}

// CommitByMessageKey records provider-confirmed delivery for an async transport.
func (g *Governor) CommitByMessageKey(ctx context.Context, messageKey string) error {
	if g == nil || g.store == nil || messageKey == "" {
		return fmt.Errorf("dispatch reservation key is unavailable")
	}
	reservation, err := g.store.GetReservationByKey(ctx, messageKey)
	if err != nil {
		return err
	}
	if reservation == nil {
		return fmt.Errorf("dispatch reservation not found for provider-confirmed send")
	}
	return g.store.CommitReservation(ctx, reservation.ID, g.clock.Now().UTC())
}

func (g *Governor) Release(ctx context.Context, reservationID uuid.UUID, errText string) error {
	state := StateReleased
	if errText != "" {
		state = StateFailed
	}
	return g.store.ReleaseReservation(ctx, reservationID, state, errText)
}

func (g *Governor) Enqueue(ctx context.Context, req EnqueueRequest) error {
	if req.DueAt.IsZero() {
		req.DueAt = g.clock.Now().UTC()
	}
	return g.store.Enqueue(ctx, &QueueItem{
		OrganizationID: req.OrganizationID, EmailAccountID: req.EmailAccountID,
		Channel: req.Channel, DraftID: req.DraftID,
		MessageKey: req.MessageKey, RecipientRef: req.RecipientRef,
		DueAt: req.DueAt.UTC(), Priority: req.Priority,
		Status: QueueQueued, CreatedAt: g.clock.Now().UTC(),
	})
}

func (g *Governor) CancelQueued(ctx context.Context, messageKey, reason string) error {
	return g.store.CancelQueue(ctx, messageKey, reason)
}

func (g *Governor) Pause(ctx context.Context, reason string, by *uuid.UUID) error {
	if reason == "" {
		reason = "manual_pause"
	}
	return g.store.SetPaused(ctx, true, reason, by)
}

func (g *Governor) Resume(ctx context.Context, by *uuid.UUID) error {
	return g.store.SetPaused(ctx, false, "", by)
}

func (g *Governor) Status(ctx context.Context, orgID *uuid.UUID) (Status, error) {
	now := g.clock.Now().UTC()
	st := Status{
		Cap: g.cfg.SendsPerHour, MinGapSeconds: int(g.cfg.MinGap / time.Second),
		Timezone: g.cfg.Timezone, WindowStart: g.cfg.WindowStart, WindowEnd: g.cfg.WindowEnd,
		CapacitySource: "global_legacy",
	}
	_, _ = g.store.ExpireStaleReservations(ctx, now)
	sent, err := g.store.CountSendsSince(ctx, now.Add(-RollingWindow))
	if err != nil {
		return st, err
	}
	st.SentLastHour = sent
	leases, err := g.store.CountActiveLeases(ctx, now)
	if err != nil {
		return st, err
	}
	st.ActiveLeases = leases
	queued, err := g.store.CountQueued(ctx, orgID)
	if err != nil {
		return st, err
	}
	st.QueuedApproved = queued
	ctrl, err := g.store.GetControl(ctx)
	if err != nil {
		return st, err
	}
	st.Paused = ctrl.Paused || g.cfg.EnvPaused
	st.PauseReason = ctrl.PauseReason
	if g.cfg.EnvPaused {
		st.Paused = true
		if g.cfg.EnvPauseReason != "" {
			st.PauseReason = g.cfg.EnvPauseReason
		} else if st.PauseReason == "" {
			st.PauseReason = "env_paused"
		}
	}
	if g.cfg.EnvPaused {
		st.PauseSource = "environment"
	} else if ctrl.Paused {
		st.PauseSource = "durable_control"
	}
	inWin, _ := InSendWindowBusiness(now, g.cfg.Timezone, g.cfg.WindowStart, g.cfg.WindowEnd, g.cfg.BusinessDaysOnly)
	st.InSendWindow = inWin
	occupied, last, err := g.store.ListOccupied(ctx, now, RollingWindow)
	if err != nil {
		return st, err
	}
	if st.Paused {
		// A pause gap is plain arithmetic and lands wherever it lands, so on a
		// Sunday it used to advertise a Sunday slot beside in_send_window=false.
		// Normalizing it keeps the two fields telling the founder one story.
		t := NextEligibleSlot(now.Add(g.cfg.MinGap), g.cfg.Timezone, g.cfg.WindowStart, g.cfg.WindowEnd, g.cfg.BusinessDaysOnly)
		st.NextSlotAt = &t
	} else if !inWin {
		t := NextWindowOpenBusiness(now, g.cfg.Timezone, g.cfg.WindowStart, g.cfg.WindowEnd, g.cfg.BusinessDaysOnly)
		st.NextSlotAt = &t
	} else {
		snap := WindowSnapshot{
			OccupiedAt: occupied, LastOccupied: last,
			Cap: g.cfg.SendsPerHour, MinGap: g.cfg.MinGap, Window: RollingWindow, Now: now,
		}
		ok, _, next := snap.CanGrant()
		if !ok {
			// A rolling-cap expiry or min-gap can cross the window close or a
			// Friday evening; it is a lower bound on the slot, not the slot.
			next = NextEligibleSlot(next, g.cfg.Timezone, g.cfg.WindowStart, g.cfg.WindowEnd, g.cfg.BusinessDaysOnly)
			st.NextSlotAt = &next
		}
	}
	fails, err := g.store.ListRecentFailures(ctx, DefaultMaxRecentFails)
	if err != nil {
		return st, err
	}
	st.RecentFailures = fails
	if orgID != nil {
		if capacityStore, ok := g.store.(MailboxStore); ok {
			snapshot, err := capacityStore.MailboxCapacitySnapshot(ctx, *orgID, now, g.cfg)
			if err != nil {
				return st, err
			}
			st.CapacitySource = "mailbox_envelopes"
			st.Mailboxes = snapshot.Mailboxes
			st.QueuedApproved = snapshot.QueuedMessages
			st.Forecast, st.NextSlotAt = buildCapacityForecast(now, g.cfg, st.Mailboxes, occupied, snapshot.QueuedMessages, st.Paused)
			for i := range st.Mailboxes {
				st.Mailboxes[i].HealthSignals = normalizeHealthSignals(st.Mailboxes[i].HealthSignals)
				if st.Paused {
					st.Mailboxes[i].PauseSource = st.PauseSource
					st.Mailboxes[i].NextEligibleSlot = nil
				}
				st.Alerts = append(st.Alerts, mailboxAlerts(&st.Mailboxes[i])...)
			}
			if snapshot.QueuedMessages > 0 && st.Forecast.SlotsNext24h == 0 {
				st.Alerts = append(st.Alerts, CapacityAlert{
					Code: AlertQueueRunoff, Severity: "critical", Count: snapshot.QueuedMessages,
					Reason: "approved queue has no effective mailbox capacity in the next 24 hours",
				})
			}
		}
	}
	return st, nil
}

func (g *Governor) MarkAttempt(ctx context.Context, messageKey string, attemptedAt time.Time) error {
	store, ok := g.store.(MailboxStore)
	if !ok {
		return fmt.Errorf("dispatch store cannot record mailbox attempt")
	}
	return store.MarkAttempt(ctx, messageKey, attemptedAt)
}

func (g *Governor) RecordProviderFailure(ctx context.Context, taskID uuid.UUID, errorCode, errorText string, occurredAt time.Time) error {
	store, ok := g.store.(MailboxStore)
	if !ok {
		return fmt.Errorf("dispatch store cannot record provider failure")
	}
	return store.RecordProviderFailure(ctx, taskID, errorCode, errorText, occurredAt)
}

func (g *Governor) RecordFailure(ctx context.Context, orgID uuid.UUID, channel, messageKey string, draftID *uuid.UUID, errText string) error {
	oid := orgID
	return g.store.RecordFailure(ctx, FailureRecord{
		OrganizationID: &oid, Channel: channel, MessageKey: messageKey,
		DraftID: draftID, ErrorText: errText, OccurredAt: g.clock.Now().UTC(),
	})
}

// ClaimNextQueued transactionally claims the next fair due item (status -> reserved).
func (g *Governor) ClaimNextQueued(ctx context.Context) (*QueueItem, error) {
	return g.store.ClaimNextQueued(ctx, g.clock.Now().UTC())
}

// CancelByRecipient cancels queued items for a contact email/phone (DNC/opt-out/bounce).
func (g *Governor) CancelByRecipient(ctx context.Context, orgID uuid.UUID, recipientRef, reason string) (int, error) {
	return g.store.CancelQueueByRecipient(ctx, orgID, recipientRef, reason)
}

// ListQueueByStatus exposes queue rows awaiting reconciliation.
func (g *Governor) ListQueueByStatus(ctx context.Context, status string, limit int) ([]QueueItem, error) {
	return g.store.ListQueueByStatus(ctx, status, limit)
}

func (g *Governor) MarkQueue(ctx context.Context, id uuid.UUID, status, errText string) error {
	return g.store.UpdateQueueStatus(ctx, id, status, errText)
}

func (g *Governor) RetryQueue(ctx context.Context, id uuid.UUID, dueAt time.Time, errText string) error {
	return g.store.RetryQueue(ctx, id, dueAt.UTC(), errText)
}

func (g *Governor) countSince(ctx context.Context, since time.Time) int {
	n, _ := g.store.CountSendsSince(ctx, since)
	return n
}
