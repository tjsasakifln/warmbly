package dispatch

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// AtomicReserveInput is the full reserve decision payload (evaluated under store lock).
type AtomicReserveInput struct {
	Req      ReserveRequest
	Now      time.Time
	Cap      int
	MinGap   time.Duration
	Mailbox  *MailboxEnvelope
	LeaseTTL time.Duration
	Window   time.Duration
}

// AtomicReserveOutput is the result of a serialized reserve decision.
type AtomicReserveOutput struct {
	Allowed          bool
	AlreadyCommitted bool
	Reservation      *Reservation
	Reason           string
	NextSlot         time.Time
	SentLastHour     int
}

// Store is the durable multi-worker-safe backend for the governor.
type Store interface {
	GetControl(ctx context.Context) (ControlState, error)
	SetPaused(ctx context.Context, paused bool, reason string, by *uuid.UUID) error

	// TryReserveAtomic expires stale leases, checks idempotency, re-counts
	// occupied slots, and inserts a reservation under a single global lock /
	// transaction so concurrent workers cannot oversubscribe the cap.
	TryReserveAtomic(ctx context.Context, in AtomicReserveInput) (AtomicReserveOutput, error)

	GetReservationByKey(ctx context.Context, messageKey string) (*Reservation, error)
	GetSendByKey(ctx context.Context, messageKey string) (sentAt time.Time, ok bool, err error)

	RefreshReservation(ctx context.Context, id uuid.UUID, leaseUntil time.Time, workerToken string) error
	// StartHandoff durably fences a message immediately before the transport's
	// irreversible DATA phase. The reservation attempt timestamp and terminal
	// queue state are changed atomically: once this succeeds, lease recovery may
	// never make the message sendable again without provider evidence.
	StartHandoff(ctx context.Context, reservationID, queueID uuid.UUID, attemptedAt time.Time) error
	// FinalizeHandoff closes an attempted reservation without making it
	// retryable. It returns false when DATA was never fenced and ordinary
	// pre-handoff release/retry semantics still apply.
	FinalizeHandoff(ctx context.Context, reservationID uuid.UUID, state, errText string) (bool, error)
	CommitReservation(ctx context.Context, id uuid.UUID, sentAt time.Time) error
	// CommitReservationWithEvidence commits the send and records what the
	// provider reported in the same transaction, so an accepted message can
	// never be recorded without the evidence that proves it.
	CommitReservationWithEvidence(ctx context.Context, id uuid.UUID, sentAt time.Time, ev SendEvidence) error
	ReleaseReservation(ctx context.Context, id uuid.UUID, state, errText string) error
	ExpireStaleReservations(ctx context.Context, now time.Time) (int, error)

	// ListOccupied is for status/observability (not the reserve hot path).
	ListOccupied(ctx context.Context, now time.Time, window time.Duration) (times []time.Time, last time.Time, err error)

	Enqueue(ctx context.Context, item *QueueItem) error
	CancelQueue(ctx context.Context, messageKey, reason string) error
	// CancelQueueByRecipient cancels queued items for a recipient (email/phone) before reserve.
	CancelQueueByRecipient(ctx context.Context, orgID uuid.UUID, recipientRef, reason string) (int, error)
	CountQueued(ctx context.Context, orgID *uuid.UUID) (int, error)
	// ClaimNextQueued transactionally selects the next fair due item and marks it reserved.
	ClaimNextQueued(ctx context.Context, now time.Time) (*QueueItem, error)
	UpdateQueueStatus(ctx context.Context, id uuid.UUID, status, errText string) error
	// ListQueueByStatus enumerates rows awaiting reconciliation, oldest first.
	ListQueueByStatus(ctx context.Context, status string, limit int) ([]QueueItem, error)
	RetryQueue(ctx context.Context, id uuid.UUID, dueAt time.Time, errText string) error
	// DeferQueue returns a claimed row to the queue at its next legitimate slot
	// without consuming a retry attempt. A cap, min-gap or window deferral is
	// scheduling, not failure, and must not exhaust the row.
	DeferQueue(ctx context.Context, id uuid.UUID, dueAt time.Time, reason string) error

	RecordFailure(ctx context.Context, f FailureRecord) error
	ListRecentFailures(ctx context.Context, limit int) ([]FailureRecord, error)

	CountActiveLeases(ctx context.Context, now time.Time) (int, error)
	CountSendsSince(ctx context.Context, since time.Time) (int, error)
}

// MailboxStore resolves real email envelopes; WhatsApp keeps the shared ceiling only.
type MailboxStore interface {
	GetMailboxEnvelope(ctx context.Context, orgID, emailAccountID uuid.UUID, now time.Time) (MailboxEnvelope, error)
	MailboxCapacitySnapshot(ctx context.Context, orgID uuid.UUID, now time.Time, cfg Config) (MailboxCapacitySnapshot, error)
	MarkAttempt(ctx context.Context, messageKey string, attemptedAt time.Time) error
	RecordProviderFailure(ctx context.Context, taskID uuid.UUID, errorCode, errorText string, occurredAt time.Time) error
}

type MailboxCapacitySnapshot struct {
	Mailboxes      []MailboxCapacity
	QueuedMessages int
}
