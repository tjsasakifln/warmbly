package dispatch

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Store interface {
	GetControl(ctx context.Context) (ControlState, error)
	SetPaused(ctx context.Context, paused bool, reason string, by *uuid.UUID) error
	ListOccupied(ctx context.Context, now time.Time, window time.Duration) (times []time.Time, last time.Time, err error)
	GetReservationByKey(ctx context.Context, messageKey string) (*Reservation, error)
	GetSendByKey(ctx context.Context, messageKey string) (sentAt time.Time, ok bool, err error)
	InsertReservation(ctx context.Context, r *Reservation) error
	RefreshReservation(ctx context.Context, id uuid.UUID, leaseUntil time.Time, workerToken string) error
	CommitReservation(ctx context.Context, id uuid.UUID, sentAt time.Time) error
	ReleaseReservation(ctx context.Context, id uuid.UUID, state, errText string) error
	ExpireStaleReservations(ctx context.Context, now time.Time) (int, error)
	Enqueue(ctx context.Context, item *QueueItem) error
	CancelQueue(ctx context.Context, messageKey, reason string) error
	CountQueued(ctx context.Context, orgID *uuid.UUID) (int, error)
	DequeueNext(ctx context.Context, now time.Time) (*QueueItem, error)
	UpdateQueueStatus(ctx context.Context, id uuid.UUID, status, errText string) error
	RecordFailure(ctx context.Context, f FailureRecord) error
	ListRecentFailures(ctx context.Context, limit int) ([]FailureRecord, error)
	CountActiveLeases(ctx context.Context, now time.Time) (int, error)
	CountSendsSince(ctx context.Context, since time.Time) (int, error)
}
