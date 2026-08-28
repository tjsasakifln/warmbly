package dispatch

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryStore is a multi-goroutine-safe in-memory Store for unit tests.
type MemoryStore struct {
	mu            sync.Mutex
	control       ControlState
	reservations  map[string]*Reservation
	byResID       map[uuid.UUID]*Reservation
	sends         map[string]time.Time
	sendMailboxes map[string]uuid.UUID
	sendTimes     []time.Time
	queue         map[string]*QueueItem
	queueByID     map[uuid.UUID]*QueueItem
	failures      []FailureRecord
	mailboxes     map[uuid.UUID]MailboxEnvelope
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		reservations:  map[string]*Reservation{},
		byResID:       map[uuid.UUID]*Reservation{},
		sends:         map[string]time.Time{},
		sendMailboxes: map[string]uuid.UUID{},
		queue:         map[string]*QueueItem{},
		queueByID:     map[uuid.UUID]*QueueItem{},
		mailboxes:     map[uuid.UUID]MailboxEnvelope{},
	}
}

// SetMailboxEnvelope configures a factual mailbox budget for tests.
func (m *MemoryStore) SetMailboxEnvelope(envelope MailboxEnvelope) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mailboxes[envelope.EmailAccountID] = envelope
}

func (m *MemoryStore) GetMailboxEnvelope(_ context.Context, orgID, emailAccountID uuid.UUID, _ time.Time) (MailboxEnvelope, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if envelope, ok := m.mailboxes[emailAccountID]; ok {
		return envelope, nil
	}
	// Existing unit tests exercise the global governor without a database. Keep
	// that fixture lane permissive; production PGStore has no such fallback.
	return MailboxEnvelope{
		EmailAccountID: emailAccountID, OrganizationID: orgID,
		DailyCap: 1000000, HourlyCap: 1000000, Ready: true,
		Timezone: "UTC", ProviderCapSource: "unknown",
	}, nil
}

func (m *MemoryStore) GetControl(ctx context.Context) (ControlState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.control, nil
}

func (m *MemoryStore) SetPaused(ctx context.Context, paused bool, reason string, by *uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.control.Paused = paused
	m.control.PauseReason = reason
	m.control.PausedBy = by
	if paused {
		now := time.Now().UTC()
		m.control.PausedAt = &now
		if by != nil && *by != uuid.Nil {
			m.control.PauseSource = "api"
		} else if m.control.PauseSource == "" {
			m.control.PauseSource = "durable_control"
		}
	} else {
		m.control.PausedAt = nil
		m.control.PauseReason = ""
		m.control.PausedBy = nil
		m.control.PauseSource = ""
	}
	return nil
}

func (m *MemoryStore) expireLocked(now time.Time) {
	for _, r := range m.reservations {
		if r.State == StateReserved && !r.LeaseUntil.After(now) {
			r.State = StateReleased
			r.LastError = "lease_expired"
		}
	}
}

func (m *MemoryStore) occupiedLocked(now time.Time, window time.Duration) ([]time.Time, time.Time) {
	cutoff := now.Add(-window)
	var times []time.Time
	var last time.Time
	for _, t := range m.sendTimes {
		if !t.Before(cutoff) && !t.After(now) {
			times = append(times, t)
			if t.After(last) {
				last = t
			}
		}
	}
	for _, r := range m.reservations {
		if r.State != StateReserved || !r.LeaseUntil.After(now) {
			continue
		}
		if !r.ReservedAt.Before(cutoff) && !r.ReservedAt.After(now) {
			times = append(times, r.ReservedAt)
			if r.ReservedAt.After(last) {
				last = r.ReservedAt
			}
		}
	}
	return times, last
}

// TryReserveAtomic holds the store mutex for the full decision (multi-instance safe).
func (m *MemoryStore) TryReserveAtomic(ctx context.Context, in AtomicReserveInput) (AtomicReserveOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := in.Now.UTC()
	window := in.Window
	if window <= 0 {
		window = RollingWindow
	}
	out := AtomicReserveOutput{}

	m.expireLocked(now)

	if t, ok := m.sends[in.Req.MessageKey]; ok {
		_ = t
		out.Allowed = true
		out.AlreadyCommitted = true
		out.Reason = "already_sent"
		out.SentLastHour = countTimes(m.sendTimes, now.Add(-window), now)
		return out, nil
	}

	if existing := m.reservations[in.Req.MessageKey]; existing != nil &&
		existing.State == StateReserved && existing.LeaseUntil.After(now) {
		mailboxConflict := in.Req.Channel == ChannelEmail && in.Req.EmailAccountID != nil &&
			(existing.EmailAccountID == nil || *existing.EmailAccountID != *in.Req.EmailAccountID)
		taskConflict := in.Req.TaskID != nil && (existing.TaskID == nil || *existing.TaskID != *in.Req.TaskID)
		if mailboxConflict || taskConflict {
			out.Reason = "message_binding_conflict"
			out.NextSlot = existing.LeaseUntil
			return out, nil
		}
		existing.LeaseUntil = now.Add(in.LeaseTTL)
		cp := *existing
		out.Allowed = true
		out.Reservation = &cp
		out.Reason = "existing_lease"
		out.SentLastHour = countTimes(m.sendTimes, now.Add(-window), now)
		return out, nil
	}

	if in.Req.Channel == ChannelEmail && in.Mailbox != nil {
		var mailboxTimes []time.Time
		var mailboxLast time.Time
		for key, sentAt := range m.sends {
			if m.sendMailboxes[key] == in.Mailbox.EmailAccountID && !sentAt.Before(now.Add(-window)) && !sentAt.After(now) {
				mailboxTimes = append(mailboxTimes, sentAt)
				if sentAt.After(mailboxLast) {
					mailboxLast = sentAt
				}
			}
		}
		for _, reservation := range m.reservations {
			if reservation.EmailAccountID == nil || *reservation.EmailAccountID != in.Mailbox.EmailAccountID ||
				reservation.State != StateReserved || !reservation.LeaseUntil.After(now) {
				continue
			}
			observed := reservation.ReservedAt
			if reservation.AttemptedAt != nil {
				observed = *reservation.AttemptedAt
			}
			if !observed.Before(now.Add(-window)) && !observed.After(now) {
				mailboxTimes = append(mailboxTimes, observed)
				if observed.After(mailboxLast) {
					mailboxLast = observed
				}
			}
		}
		mailboxCap := in.Mailbox.HourlyCap
		if in.Req.CapOverride > 0 {
			mailboxCap = minPositive(mailboxCap, in.Req.CapOverride)
		}
		mailboxSnapshot := WindowSnapshot{
			OccupiedAt: mailboxTimes, LastOccupied: mailboxLast,
			Cap: mailboxCap, MinGap: in.Mailbox.MinGap, Window: window, Now: now,
		}
		if ok, reason, next := mailboxSnapshot.CanGrant(); !ok {
			out.Reason, out.NextSlot = "mailbox_"+reason, next
			return out, nil
		}
		usedToday := 0
		date := localDateKey(now, in.Mailbox.Timezone)
		for _, reservation := range m.reservations {
			if reservation.EmailAccountID == nil || *reservation.EmailAccountID != in.Mailbox.EmailAccountID {
				continue
			}
			reservedToday := localDateKey(reservation.ReservedAt, in.Mailbox.Timezone) == date &&
				(reservation.State == StateReserved || reservation.State == StateCommitted)
			attemptedToday := reservation.AttemptedAt != nil && localDateKey(*reservation.AttemptedAt, in.Mailbox.Timezone) == date
			if reservedToday || attemptedToday {
				usedToday++
			}
		}
		if usedToday >= in.Mailbox.DailyCap {
			out.Reason, out.NextSlot = "mailbox_daily_cap", nextLocalDay(now, in.Mailbox.Timezone)
			return out, nil
		}
	}

	occupied, last := m.occupiedLocked(now, window)
	snap := WindowSnapshot{
		OccupiedAt: occupied, LastOccupied: last,
		Cap: in.Cap, MinGap: in.MinGap, Window: window, Now: now,
	}
	out.SentLastHour = snap.OccupiedCount()
	ok, reason, next := snap.CanGrant()
	if !ok {
		out.Reason = reason
		out.NextSlot = next
		return out, nil
	}

	// Clear released/failed/expired row for this key.
	if existing := m.reservations[in.Req.MessageKey]; existing != nil {
		delete(m.byResID, existing.ID)
		delete(m.reservations, in.Req.MessageKey)
	}

	res := &Reservation{
		ID:             uuid.New(),
		OrganizationID: in.Req.OrganizationID,
		EmailAccountID: in.Req.EmailAccountID,
		TaskID:         in.Req.TaskID,
		Channel:        in.Req.Channel,
		MessageKey:     in.Req.MessageKey,
		DraftID:        in.Req.DraftID,
		State:          StateReserved,
		ReservedAt:     now,
		LeaseUntil:     now.Add(in.LeaseTTL),
		WorkerToken:    in.Req.WorkerToken,
	}
	cp := *res
	m.reservations[res.MessageKey] = &cp
	m.byResID[cp.ID] = &cp
	out.Allowed = true
	out.Reservation = res
	out.Reason = "reserved"
	out.SentLastHour = snap.OccupiedCount() + 1
	return out, nil
}

func countTimes(times []time.Time, since, until time.Time) int {
	n := 0
	for _, t := range times {
		if !t.Before(since) && !t.After(until) {
			n++
		}
	}
	return n
}

func (m *MemoryStore) ListOccupied(ctx context.Context, now time.Time, window time.Duration) ([]time.Time, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	times, last := m.occupiedLocked(now, window)
	return times, last, nil
}

func (m *MemoryStore) GetReservationByKey(ctx context.Context, messageKey string) (*Reservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.reservations[messageKey]
	if r == nil {
		return nil, nil
	}
	cp := *r
	return &cp, nil
}

func (m *MemoryStore) GetSendByKey(ctx context.Context, messageKey string) (time.Time, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.sends[messageKey]
	return t, ok, nil
}

func (m *MemoryStore) RefreshReservation(ctx context.Context, id uuid.UUID, leaseUntil time.Time, workerToken string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.byResID[id]
	if r == nil {
		return fmt.Errorf("reservation not found")
	}
	r.LeaseUntil = leaseUntil
	return nil
}

func (m *MemoryStore) CommitReservation(ctx context.Context, id uuid.UUID, sentAt time.Time) error {
	return m.CommitReservationWithEvidence(ctx, id, sentAt, SendEvidence{})
}

// CommitReservationWithEvidence mirrors the PG behaviour; the in-memory store
// keeps no evidence columns, so the evidence is accepted and ignored.
func (m *MemoryStore) CommitReservationWithEvidence(ctx context.Context, id uuid.UUID, sentAt time.Time, _ SendEvidence) error {
	m.closeQueueForKey(id)
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.byResID[id]
	if r == nil {
		return fmt.Errorf("reservation not found")
	}
	if r.State == StateCommitted {
		current, sent := m.sends[r.MessageKey]
		if sent && sentAt.Before(current) {
			m.sends[r.MessageKey] = sentAt
			m.sendTimes = m.sendTimes[:0]
			for _, at := range m.sends {
				m.sendTimes = append(m.sendTimes, at)
			}
		}
		if r.CommittedAt == nil || sentAt.Before(*r.CommittedAt) {
			t := sentAt
			r.CommittedAt = &t
		}
		return nil
	}
	if _, ok := m.sends[r.MessageKey]; ok {
		r.State = StateCommitted
		t := sentAt
		r.CommittedAt = &t
		return nil
	}
	r.State = StateCommitted
	t := sentAt
	r.CommittedAt = &t
	m.sends[r.MessageKey] = sentAt
	if r.EmailAccountID != nil {
		m.sendMailboxes[r.MessageKey] = *r.EmailAccountID
	}
	m.sendTimes = append(m.sendTimes, sentAt)
	return nil
}

func (m *MemoryStore) ReleaseReservation(ctx context.Context, id uuid.UUID, state, errText string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.byResID[id]
	if r == nil {
		return fmt.Errorf("reservation not found")
	}
	if r.State == StateCommitted {
		return nil
	}
	if state == "" {
		state = StateReleased
	}
	r.State = state
	r.LastError = errText
	if errText != "" {
		orgID := r.OrganizationID
		m.failures = append(m.failures, FailureRecord{
			ID: uuid.New(), OrganizationID: &orgID, EmailAccountID: r.EmailAccountID,
			TaskID: r.TaskID, Channel: r.Channel, MessageKey: r.MessageKey,
			DraftID: r.DraftID, ErrorClass: "unknown", ErrorText: errText,
			OccurredAt: time.Now().UTC(),
		})
	}
	return nil
}

func (m *MemoryStore) ExpireStaleReservations(ctx context.Context, now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, r := range m.reservations {
		if r.State == StateReserved && !r.LeaseUntil.After(now) {
			r.State = StateReleased
			r.LastError = "lease_expired"
			n++
		}
	}
	return n, nil
}

func (m *MemoryStore) Enqueue(ctx context.Context, item *QueueItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing := m.queue[item.MessageKey]; existing != nil {
		// 'attempted' is terminal for enqueue: the message is already with the
		// transport and its acceptance is merely unobserved. Re-queueing it would
		// dispatch the same message a second time.
		if existing.Status == QueueAttempted || existing.Status == QueueSent || existing.Status == QueueCancelled {
			return nil
		}
		existing.DueAt = item.DueAt
		existing.Priority = item.Priority
		existing.RecipientRef = item.RecipientRef
		if item.EmailAccountID != nil {
			existing.EmailAccountID = item.EmailAccountID
		}
		existing.Status = QueueQueued
		return nil
	}
	if item.ID == uuid.Nil {
		item.ID = uuid.New()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	if item.Status == "" {
		item.Status = QueueQueued
	}
	cp := *item
	m.queue[item.MessageKey] = &cp
	m.queueByID[cp.ID] = &cp
	return nil
}

func (m *MemoryStore) CancelQueue(ctx context.Context, messageKey, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := m.queue[messageKey]
	if q == nil {
		return nil
	}
	if q.Status == QueueSent {
		return nil
	}
	q.Status = QueueCancelled
	q.CancelReason = reason
	return nil
}

func (m *MemoryStore) CancelQueueByRecipient(ctx context.Context, orgID uuid.UUID, recipientRef, reason string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if recipientRef == "" {
		return 0, nil
	}
	n := 0
	for _, q := range m.queue {
		if q.OrganizationID != orgID || q.Status != QueueQueued {
			continue
		}
		if q.RecipientRef == recipientRef {
			q.Status = QueueCancelled
			q.CancelReason = reason
			n++
		}
	}
	return n, nil
}

func (m *MemoryStore) CountQueued(ctx context.Context, orgID *uuid.UUID) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, q := range m.queue {
		if q.Status != QueueQueued {
			continue
		}
		if orgID != nil && q.OrganizationID != *orgID {
			continue
		}
		n++
	}
	return n, nil
}

// ClaimNextQueued picks the next fair due item and marks it reserved under the lock.
func (m *MemoryStore) ClaimNextQueued(ctx context.Context, now time.Time) (*QueueItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var candidates []*QueueItem
	for _, q := range m.queue {
		if q.Status != QueueQueued || q.DueAt.After(now) {
			continue
		}
		candidates = append(candidates, q)
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if !a.DueAt.Equal(b.DueAt) {
			return a.DueAt.Before(b.DueAt)
		}
		if a.Priority != b.Priority {
			return a.Priority > b.Priority
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	chosen := candidates[0]
	chosen.Status = QueueReserved
	chosen.Attempts++
	cp := *chosen
	return &cp, nil
}

func (m *MemoryStore) UpdateQueueStatus(ctx context.Context, id uuid.UUID, status, errText string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := m.queueByID[id]
	if q == nil {
		return fmt.Errorf("queue item not found")
	}
	q.Status = status
	if errText != "" {
		q.LastError = errText
	}
	return nil
}

// GetQueueByKey returns a copy of the queue row for a message key. It exists
// for inspection and tests; the Store interface does not expose it.
// closeQueueForKey mirrors the PG commit transaction, which closes the queue
// row sharing the reservation's message key.
func (m *MemoryStore) closeQueueForKey(reservationID uuid.UUID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.byResID[reservationID]
	if r == nil {
		return
	}
	q := m.queue[r.MessageKey]
	if q == nil {
		return
	}
	switch q.Status {
	case QueueQueued, QueueReserved, QueueAttempted:
		q.Status = QueueSent
	}
}

func (m *MemoryStore) GetQueueByKey(_ context.Context, messageKey string) (*QueueItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := m.queue[messageKey]
	if q == nil {
		return nil, nil
	}
	cp := *q
	return &cp, nil
}

func (m *MemoryStore) ListQueueByStatus(_ context.Context, status string, limit int) ([]QueueItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var out []QueueItem
	for _, q := range m.queue {
		if q != nil && q.Status == status {
			out = append(out, *q)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryStore) DeferQueue(ctx context.Context, id uuid.UUID, dueAt time.Time, reason string) error {
	m.mu.Lock()
	q := m.queueByID[id]
	if q != nil && q.Attempts > 0 {
		q.Attempts--
	}
	m.mu.Unlock()
	return m.RetryQueue(ctx, id, dueAt, reason)
}

func (m *MemoryStore) RetryQueue(ctx context.Context, id uuid.UUID, dueAt time.Time, errText string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := m.queueByID[id]
	if q == nil {
		return fmt.Errorf("queue item not found")
	}
	q.Status = QueueQueued
	q.DueAt = dueAt.UTC()
	q.LastError = errText
	return nil
}

func (m *MemoryStore) RecordFailure(ctx context.Context, f FailureRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if f.ID == uuid.Nil {
		f.ID = uuid.New()
	}
	if f.OccurredAt.IsZero() {
		f.OccurredAt = time.Now().UTC()
	}
	m.failures = append(m.failures, f)
	return nil
}

func (m *MemoryStore) ListRecentFailures(ctx context.Context, limit int) ([]FailureRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit < 1 {
		limit = DefaultMaxRecentFails
	}
	out := make([]FailureRecord, len(m.failures))
	copy(out, m.failures)
	sort.Slice(out, func(i, j int) bool { return out[i].OccurredAt.After(out[j].OccurredAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryStore) CountActiveLeases(ctx context.Context, now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, r := range m.reservations {
		if r.State == StateReserved && r.LeaseUntil.After(now) {
			n++
		}
	}
	return n, nil
}

func (m *MemoryStore) CountSendsSince(ctx context.Context, since time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, t := range m.sendTimes {
		if !t.Before(since) {
			n++
		}
	}
	return n, nil
}

func (m *MemoryStore) MarkAttempt(_ context.Context, messageKey string, attemptedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	reservation := m.reservations[messageKey]
	if reservation == nil {
		return fmt.Errorf("dispatch reservation not found for provider attempt")
	}
	value := attemptedAt.UTC()
	reservation.AttemptedAt = &value
	return nil
}

func (m *MemoryStore) RecordProviderFailure(_ context.Context, taskID uuid.UUID, errorCode, errorText string, occurredAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, reservation := range m.reservations {
		if reservation.TaskID == nil || *reservation.TaskID != taskID {
			continue
		}
		reservation.State = StateFailed
		reservation.LastError = errorText
		at := occurredAt.UTC()
		reservation.AttemptedAt = &at
		orgID := reservation.OrganizationID
		m.failures = append(m.failures, FailureRecord{
			ID: uuid.New(), OrganizationID: &orgID, EmailAccountID: reservation.EmailAccountID,
			TaskID: &taskID, Channel: reservation.Channel, MessageKey: reservation.MessageKey,
			DraftID: reservation.DraftID, ErrorCode: errorCode,
			ErrorClass: ClassifyProviderError(errorCode, errorText), ErrorText: errorText, OccurredAt: at,
		})
		return nil
	}
	return nil
}

func (m *MemoryStore) MailboxCapacitySnapshot(_ context.Context, orgID uuid.UUID, now time.Time, cfg Config) (MailboxCapacitySnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := MailboxCapacitySnapshot{}
	for _, envelope := range m.mailboxes {
		if envelope.OrganizationID != uuid.Nil && envelope.OrganizationID != orgID {
			continue
		}
		mailbox := MailboxCapacity{
			EmailAccountID: envelope.EmailAccountID, Enabled: envelope.Ready,
			Status: "active", CredentialsReady: true, WorkerAssigned: true,
			AuthState: "passing", AuthSPF: true, AuthDKIM: true, AuthDMARC: true,
			ConfiguredDailyCap:   envelope.DailyCap,
			ConfiguredMinWaitSec: int(envelope.MinGap / time.Second),
			DerivedHourlyCap:     envelope.HourlyCap,
			EffectiveDailyCap:    envelope.DailyCap,
			EffectiveHourlyCap:   minPositive(envelope.HourlyCap, cfg.SendsPerHour),
			ProviderCapSource:    "unknown",
			BusinessWindow: MailboxBusinessWindow{
				Timezone: cfg.Timezone, Start: cfg.WindowStart, End: cfg.WindowEnd,
				BusinessDaysOnly: cfg.BusinessDaysOnly,
			},
			Health: "ready", HealthReason: "ready",
			Unknown: []string{"provider_daily_cap", "provider_hourly_cap", "warmup_age"},
		}
		if !envelope.Ready {
			mailbox.Health, mailbox.HealthReason = "blocked", envelope.HealthReason
		}
		day := localDateKey(now, envelope.Timezone)
		for key, sentAt := range m.sends {
			if m.sendMailboxes[key] != envelope.EmailAccountID {
				continue
			}
			if mailbox.Latest.AcceptedAt == nil || sentAt.After(*mailbox.Latest.AcceptedAt) {
				value := sentAt
				mailbox.Latest.AcceptedAt = &value
			}
			if !sentAt.Before(now.Add(-time.Hour)) {
				mailbox.Throughput.AcceptedLastHour++
				mailbox.occupiedAt = append(mailbox.occupiedAt, sentAt)
			}
			if localDateKey(sentAt, envelope.Timezone) == day {
				mailbox.Throughput.AcceptedToday++
			}
			if !sentAt.Before(now.Add(-7 * 24 * time.Hour)) {
				mailbox.Throughput.AcceptedLast7d++
			}
		}
		for _, reservation := range m.reservations {
			if reservation.EmailAccountID == nil || *reservation.EmailAccountID != envelope.EmailAccountID {
				continue
			}
			if reservation.AttemptedAt != nil {
				if mailbox.Latest.AttemptAt == nil || reservation.AttemptedAt.After(*mailbox.Latest.AttemptAt) {
					value := *reservation.AttemptedAt
					mailbox.Latest.AttemptAt = &value
				}
				if !reservation.AttemptedAt.Before(now.Add(-time.Hour)) && reservation.State != StateCommitted {
					mailbox.occupiedAt = append(mailbox.occupiedAt, *reservation.AttemptedAt)
				}
			}
			reservedToday := localDateKey(reservation.ReservedAt, envelope.Timezone) == day &&
				(reservation.State == StateReserved || reservation.State == StateCommitted)
			attemptedToday := reservation.AttemptedAt != nil && localDateKey(*reservation.AttemptedAt, envelope.Timezone) == day
			if reservedToday || attemptedToday {
				mailbox.UsedToday++
			}
			if reservation.State == StateReserved && reservation.AttemptedAt == nil && reservation.LeaseUntil.After(now) &&
				!reservation.ReservedAt.Before(now.Add(-time.Hour)) {
				mailbox.occupiedAt = append(mailbox.occupiedAt, reservation.ReservedAt)
			}
		}
		for _, failure := range m.failures {
			if failure.EmailAccountID == nil || *failure.EmailAccountID != envelope.EmailAccountID {
				continue
			}
			if failure.ErrorClass == "" || failure.ErrorClass == "unknown" {
				continue
			}
			if mailbox.Latest.ProviderRejectionAt == nil || failure.OccurredAt.After(*mailbox.Latest.ProviderRejectionAt) {
				value := failure.OccurredAt
				mailbox.Latest.ProviderRejectionAt = &value
				mailbox.Latest.ProviderErrorClass = failure.ErrorClass
			}
		}
		candidate := nextRollingSlot(now, mailbox.occupiedAt, mailbox.EffectiveHourlyCap, envelope.MinGap, RollingWindow)
		if mailbox.UsedToday >= mailbox.EffectiveDailyCap {
			candidate = nextLocalDay(now, envelope.Timezone)
		}
		candidate = NextEligibleSlot(candidate, cfg.Timezone, cfg.WindowStart, cfg.WindowEnd, cfg.BusinessDaysOnly)
		if envelope.Ready {
			mailbox.NextEligibleSlot = &candidate
		}
		out.Mailboxes = append(out.Mailboxes, mailbox)
	}
	for _, item := range m.queue {
		if item.OrganizationID == orgID && item.Channel == ChannelEmail &&
			(item.Status == QueueQueued || item.Status == QueueReserved) {
			out.QueuedMessages++
		}
	}
	return out, nil
}
