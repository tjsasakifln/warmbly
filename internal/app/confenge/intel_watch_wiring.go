package confenge

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/app/confenge/liveintel"
)

// INTEL_WATCH is a side lane, wired alongside first touch and never into it.
//
// It borrows exactly two things from the first-touch machinery: the SMTP
// transport (so there is one send implementation) and the mailbox resolution
// rules (so both lanes agree on which sender an organization owns). It borrows
// nothing else -- no dispatch governor, no queue, no reservation, no cap. A
// watch lane that is dormant, misconfigured or failing changes nothing about
// how first touch behaves.

// WireIntelWatch installs the side lane's own database handle so mailbox
// resolution works whether or not the fast lane is enabled.
func (s *service) WireIntelWatch(pool *pgxpool.Pool) {
	if s == nil {
		return
	}
	s.intelWatchDB = pool
}

// ResolveOutboundMailbox answers which mailbox this organization's CONFENGE
// mail leaves from, using the same rules the fast lane uses. It is exported so
// a side lane can ask without reaching into first-touch internals.
func (s *service) ResolveOutboundMailbox(ctx context.Context, orgID uuid.UUID) (uuid.UUID, error) {
	if s == nil {
		return uuid.Nil, errIntelWatchNoService
	}
	pool := s.fastLaneDB
	if pool == nil {
		pool = s.intelWatchDB
	}
	return resolveConfengeMailboxIn(ctx, pool, orgID)
}

// NewIntelWatchReclaimWorker builds the whole side lane: the dispatcher over
// the shared transport, the consumer over the delivery ledger, and the
// recurring pass that makes a transient failure recoverable without a human.
//
// It returns nil when the lane cannot run, so the caller's `go worker.Run(ctx)`
// is a no-op rather than a boot failure. A dormant INTEL_WATCH must never be
// able to stop first touch from starting.
func (s *service) NewIntelWatchReclaimWorker(store liveintel.WatchStore, transport FirstTouchTransport, producer liveintel.EventProducer, interval time.Duration) *liveintel.WatchReclaimWorker {
	if s == nil || store == nil || transport == nil || producer == nil {
		return nil
	}
	dispatcher := NewIntelWatchDispatcher(transport, s.ResolveOutboundMailbox)
	return liveintel.NewWatchReclaimWorker(liveintel.NewConsumer(store, dispatcher), producer, interval)
}

type intelWatchError string

func (e intelWatchError) Error() string { return string(e) }

const errIntelWatchNoService = intelWatchError("confenge service is not available")
