package liveintel

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
)

// The INTEL_WATCH reclaim worker.
//
// A transient dispatch failure must become a delivered notification without a
// human replaying anything. The ledger already makes that safe: a released or
// lease-expired PENDING row is claimable again, and a DISPATCHED row never is.
// What was missing is something that actually comes back. This is it, built on
// the same NewXWorker(...).Run(ctx) ticker shape the fast lane, the editorial
// recovery worker and the draft generation worker already use.
//
// It re-drives the producer's events rather than reconstructing messages from
// the ledger, because the ledger deliberately stores delivery identity, not
// content. Re-driving is safe precisely because dedup is by
// (subscription, event identity, semantic content hash): everything already
// delivered is a no-op, and only the rows that never made it are attempted.

// WatchReclaimReport is what one pass did. It exists so an operator (and a
// test) can tell "nothing was owed" from "something was recovered".
type WatchReclaimReport struct {
	// ParkedStaleHandoffs is fenced attempts whose worker disappeared. They are
	// made legible, never retried.
	ParkedStaleHandoffs int
	EventsSeen          int
	EventsSkipped       int
	Matched             int
	Dispatched          int
	Deduped             int
	Parked              int
	Failed              int
	Retryable           int
	Contended           int
	Undelivered         int
}

func (r *WatchReclaimReport) absorb(result HandleResult) {
	r.Matched += result.Matched
	r.Dispatched += result.Dispatched
	r.Deduped += result.Deduped
	r.Parked += result.Parked
	r.Failed += result.Failed
	r.Retryable += result.Retryable
	r.Contended += result.Contended
	r.Undelivered += result.Undelivered
	if result.Skipped != "" {
		r.EventsSkipped++
	}
}

const (
	// defaultWatchReclaimInterval spaces reclaim passes. Recovery is measured in
	// minutes, not seconds: a watcher would rather be told late than twice.
	defaultWatchReclaimInterval = 5 * time.Minute
	// defaultWatchEventBudget bounds ONE event's fan-out. Without it a single
	// unreachable provider would hold the pass open and starve every event
	// behind it -- head-of-line blocking dressed up as a retry.
	defaultWatchEventBudget = 2 * time.Minute
)

// WatchReclaimWorker drives recurring reclaim passes.
type WatchReclaimWorker struct {
	consumer    *Consumer
	producer    EventProducer
	interval    time.Duration
	eventBudget time.Duration
}

// NewWatchReclaimWorker builds the worker. A nil consumer or producer yields a
// worker that runs and does nothing, so a dormant lane cannot crash a boot.
func NewWatchReclaimWorker(consumer *Consumer, producer EventProducer, interval time.Duration) *WatchReclaimWorker {
	if interval <= 0 {
		interval = defaultWatchReclaimInterval
	}
	return &WatchReclaimWorker{
		consumer: consumer, producer: producer,
		interval: interval, eventBudget: defaultWatchEventBudget,
	}
}

// Run performs a pass immediately and then on every tick.
func (w *WatchReclaimWorker) Run(ctx context.Context) {
	if w == nil || w.consumer == nil || w.producer == nil {
		return
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		report, err := w.ReclaimOnce(ctx)
		if err != nil {
			// A failing pass is normal operation, not a reason to stop coming
			// back: the next tick re-attempts exactly what is still owed.
			log.Warn().Err(err).Msg("confenge intel watch: reclaim pass finished with problems")
		}
		if report.Dispatched > 0 || report.ParkedStaleHandoffs > 0 {
			log.Info().Int("dispatched", report.Dispatched).Int("parked_stale", report.ParkedStaleHandoffs).
				Int("still_retryable", report.Retryable).Msg("confenge intel watch: reclaim pass recovered deliveries")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// ReclaimOnce parks abandoned handoffs and then re-drives every event the
// producer can still replay. Errors are aggregated, never fatal: one event's
// bad dispatcher must not stop the events behind it from being served.
func (w *WatchReclaimWorker) ReclaimOnce(ctx context.Context) (WatchReclaimReport, error) {
	report := WatchReclaimReport{}
	if w == nil || w.consumer == nil {
		return report, fmt.Errorf("intel watch reclaim worker has no consumer")
	}
	var problems []error
	// Park first. A fenced row whose worker vanished is unclaimable either way;
	// sweeping it turns an invisible stall into a reviewable AMBIGUOUS row.
	if parked, err := w.consumer.ExpireStaleHandoffs(ctx); err != nil {
		problems = append(problems, fmt.Errorf("intel watch stale handoff sweep: %w", err))
	} else {
		report.ParkedStaleHandoffs = parked
	}
	if w.producer == nil {
		return report, errors.Join(problems...)
	}

	events, err := w.producer.Subscribe(ctx)
	if err != nil {
		problems = append(problems, fmt.Errorf("intel watch event subscribe: %w", err))
		return report, errors.Join(problems...)
	}
	for {
		select {
		case <-ctx.Done():
			return report, errors.Join(append(problems, ctx.Err())...)
		case event, open := <-events:
			if !open {
				return report, errors.Join(problems...)
			}
			report.EventsSeen++
			result, handleErr := w.handleBounded(ctx, event)
			report.absorb(result)
			if handleErr != nil {
				problems = append(problems, fmt.Errorf("event %s: %w", event.EventID, handleErr))
			}
		}
	}
}

// handleBounded gives one event its own deadline so a stuck dispatcher costs
// that event's pass and nothing else's.
func (w *WatchReclaimWorker) handleBounded(ctx context.Context, event OpportunityEvent) (HandleResult, error) {
	budget := w.eventBudget
	if budget <= 0 {
		return w.consumer.HandleEvent(ctx, event)
	}
	eventCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	return w.consumer.HandleEvent(eventCtx, event)
}
