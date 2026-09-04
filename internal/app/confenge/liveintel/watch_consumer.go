package liveintel

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/warmbly/warmbly/internal/models"
)

// The INTEL_WATCH delivery ledger.
//
// This mirrors the first-touch fast lane deliberately: claim a delivery, fence
// it immediately before the irreversible handoff, then write one terminal state
// from the outcome the dispatcher reported. A single "already sent" boolean
// cannot do that -- writing it before the send loses the notification whenever a
// dispatcher fails, and writing it after the send duplicates the notification
// whenever the process dies in between. The ledger's states separate the two.
//
// Concurrency is settled in the database, not in this process: the claim is a
// conditional upsert on the delivery's own primary key, so two workers racing
// the same event contend on one row and exactly one of them delivers.

// WatchStore is the narrow persistence surface the watch consumer needs. The
// repository satisfies it; a test fake satisfies it just as well.
type WatchStore interface {
	ListActiveSubscriptionsBySubject(ctx context.Context, organizationID uuid.UUID, subjectKey string) ([]models.IntelWatchSubscription, error)
	ClaimDelivery(ctx context.Context, key models.IntelWatchDeliveryKey, now time.Time, lease time.Duration, maxAttempts int) (models.IntelWatchClaim, error)
	MarkDeliveryInFlight(ctx context.Context, key models.IntelWatchDeliveryKey, now time.Time) error
	SettleDelivery(ctx context.Context, key models.IntelWatchDeliveryKey, state, errText string, at time.Time) (bool, error)
	ReleaseDelivery(ctx context.Context, key models.IntelWatchDeliveryKey, errText string, at time.Time) (bool, error)
}

// HandoffSweeper parks fenced attempts whose worker disappeared. Optional: a
// store that cannot do it simply never has stale handoffs swept.
type HandoffSweeper interface {
	ExpireStaleDeliveryHandoffs(ctx context.Context, now time.Time) (int, error)
}

// WatchDispatchOutcome is the closed set of things one dispatch attempt can
// mean. It is the watch-lane twin of the fast lane's FirstTouchOutcome.
type WatchDispatchOutcome string

const (
	// WatchDelivered means the watcher was written to. Terminal.
	WatchDelivered WatchDispatchOutcome = "delivered"
	// WatchPermanent means retrying this delivery cannot succeed. Terminal.
	WatchPermanent WatchDispatchOutcome = "permanent"
	// WatchTransient means the attempt failed for a reason that may pass. It is
	// retryable only when the dispatcher never reached its handoff fence.
	WatchTransient WatchDispatchOutcome = "transient"
	// WatchAmbiguous means we do not know whether the watcher was written to.
	// Parked, never auto-retried.
	WatchAmbiguous WatchDispatchOutcome = "ambiguous"
)

// WatchDelivery is one approved delivery handed to the dispatcher.
type WatchDelivery struct {
	Subscription models.IntelWatchSubscription
	Event        OpportunityEvent
	ContentHash  string
	// BeforeHandoff must be called immediately before the irreversible send.
	// It writes the durable no-resend fence and fails closed: a non-nil error
	// means the claim was lost and the dispatcher must abort without sending.
	BeforeHandoff func(context.Context) error
}

// Dispatcher is the hook a later slice wires to the composer. Nil is a
// supported state: the consumer then delivers nothing and keeps every delivery
// claimable, rather than recording sends that never happened.
type Dispatcher interface {
	DispatchWatchUpdate(ctx context.Context, delivery WatchDelivery) (WatchDispatchOutcome, error)
}

// HandleResult reports what one event did to each of its watchers, so a caller
// (and a test) can tell "nobody watching" from "already delivered" from
// "delivered" from "will be retried".
type HandleResult struct {
	Matched int
	// Dispatched was delivered on this pass.
	Dispatched int
	// Deduped was already delivered terminally: nothing changed, nothing sent.
	Deduped int
	// Parked has an unknown outcome and is never auto-retried.
	Parked int
	// Failed is a terminal failure retrying cannot fix.
	Failed int
	// Retryable failed before any handoff and is still deliverable.
	Retryable int
	// Contended is held by another worker right now; it is that worker's to
	// finish, and re-delivering the event later picks up anything it dropped.
	Contended int
	// Undelivered had no dispatcher wired to attempt it. The ledger is not
	// touched at all, so the delivery stays exactly as deliverable as it was.
	Undelivered int
	Skipped     string
}

const (
	// watchDeliveryLease bounds how long one worker may hold a delivery before
	// another may take it over. A PENDING lease expiring is safe (nothing was
	// handed over); an IN_FLIGHT lease expiring parks the row instead.
	watchDeliveryLease = 2 * time.Minute
	// watchMaxAttempts bounds transient retries so a permanently sick
	// dispatcher cannot re-attempt one watcher forever.
	watchMaxAttempts = 5
)

// Consumer turns opportunity events into watch deliveries, at most once per
// (subscription, event identity, content).
type Consumer struct {
	store       WatchStore
	dispatcher  Dispatcher
	now         func() time.Time
	lease       time.Duration
	maxAttempts int
}

func NewConsumer(store WatchStore, dispatcher Dispatcher) *Consumer {
	return &Consumer{
		store: store, dispatcher: dispatcher,
		now:         func() time.Time { return time.Now().UTC() },
		lease:       watchDeliveryLease,
		maxAttempts: watchMaxAttempts,
	}
}

// ExpireStaleHandoffs parks fenced deliveries whose worker went away. It is
// reconciler work and is deliberately NOT called from the delivery path: a
// stuck IN_FLIGHT row is already unclaimable, so sweeping it only makes the
// parked state legible to an operator.
func (c *Consumer) ExpireStaleHandoffs(ctx context.Context) (int, error) {
	if c == nil || c.store == nil {
		return 0, fmt.Errorf("intel watch consumer has no store")
	}
	sweeper, ok := c.store.(HandoffSweeper)
	if !ok {
		return 0, nil
	}
	return sweeper.ExpireStaleDeliveryHandoffs(ctx, c.now())
}

// HandleEvent resolves the watchers of the event's subject and delivers to each
// one whose delivery is not already terminal. Every watcher is attempted
// independently: one failing dispatch neither loses that notification nor stops
// the other watchers of the same subject from being served.
func (c *Consumer) HandleEvent(ctx context.Context, event OpportunityEvent) (HandleResult, error) {
	if c == nil || c.store == nil {
		return HandleResult{}, fmt.Errorf("intel watch consumer has no store")
	}
	if _, reason := AdmitOfficialOpportunityEvent(event); reason != "" {
		// A malformed, stale, rejected or unofficial event is discarded, not
		// retried: no producer replay can make it well-formed.
		log.Warn().Str("event_id", event.EventID).Str("reason", reason).
			Msg("confenge intel watch: event rejected")
		return HandleResult{Skipped: reason}, nil
	}
	subscriptions, err := c.store.ListActiveSubscriptionsBySubject(ctx, event.OrgID, event.SubjectKey)
	if err != nil {
		return HandleResult{}, fmt.Errorf("intel watch subscription lookup: %w", err)
	}
	contentHash := event.ContentHash()
	result := HandleResult{}
	var problems []error
	for i := range subscriptions {
		subscription := subscriptions[i]
		if !subscription.Active() {
			continue
		}
		result.Matched++
		if err := c.deliverOne(ctx, subscription, event, contentHash, &result); err != nil {
			// Aggregate rather than abort: the next watcher of this subject has
			// nothing to do with this one's dispatcher.
			problems = append(problems, fmt.Errorf("subscription %s: %w", subscription.ID, err))
		}
	}
	return result, errors.Join(problems...)
}

// deliverOne takes a single watcher's delivery from claim to terminal state. A
// returned error means the event is worth re-delivering; the ledger decides
// what a re-delivery is actually allowed to do.
func (c *Consumer) deliverOne(ctx context.Context, subscription models.IntelWatchSubscription, event OpportunityEvent, contentHash string, result *HandleResult) error {
	if c.dispatcher == nil {
		// No transport exists yet, so there is nothing to attempt. Do not even
		// touch the ledger: claiming here would spend an attempt from the retry
		// budget on every pass and eventually exhaust a delivery nobody ever
		// tried, turning "no composer yet" into a permanently lost notification.
		result.Undelivered++
		return nil
	}
	key := models.IntelWatchDeliveryKey{
		SubscriptionID: subscription.ID,
		EventIdentity:  event.EventID,
		ContentHash:    contentHash,
	}
	claim, err := c.store.ClaimDelivery(ctx, key, c.now(), c.lease, c.maxAttempts)
	if err != nil {
		return fmt.Errorf("intel watch delivery claim: %w", err)
	}
	if !claim.Granted {
		switch claim.Reason {
		case models.IntelWatchClaimAlreadyDispatched:
			result.Deduped++
		case models.IntelWatchClaimParkedAmbiguous:
			result.Parked++
		case models.IntelWatchClaimTerminalFailed:
			result.Failed++
		case models.IntelWatchClaimAttemptsExhausted:
			// The attempt budget is spent. Make that terminal and visible
			// instead of leaving a row that silently never delivers again.
			if _, settleErr := c.store.SettleDelivery(ctx, key, models.IntelWatchDeliveryFailed,
				models.IntelWatchClaimAttemptsExhausted, c.now()); settleErr != nil {
				return fmt.Errorf("intel watch delivery exhaustion: %w", settleErr)
			}
			result.Failed++
		default:
			result.Contended++
		}
		return nil
	}

	fenced := false
	outcome, dispatchErr := c.dispatcher.DispatchWatchUpdate(ctx, WatchDelivery{
		Subscription: subscription,
		Event:        event,
		ContentHash:  contentHash,
		BeforeHandoff: func(handoffCtx context.Context) error {
			if err := c.store.MarkDeliveryInFlight(handoffCtx, key, c.now()); err != nil {
				return err
			}
			fenced = true
			return nil
		},
	})

	switch outcome {
	case WatchDelivered:
		if !fenced {
			// The message is out, so committing is still right, but a
			// dispatcher that skipped the fence leaves a crash window we cannot
			// close. Say so loudly rather than hiding it.
			log.Warn().Str("subscription_id", subscription.ID.String()).Str("event_id", event.EventID).
				Msg("confenge intel watch: dispatcher delivered without taking the handoff fence")
		}
		if _, err := c.store.SettleDelivery(ctx, key, models.IntelWatchDeliveryDispatched, "", c.now()); err != nil {
			// The watch mail is gone and we could not record it. Never re-send
			// from here: report it so the caller can surface the ledger gap.
			log.Error().Err(err).Str("subscription_id", subscription.ID.String()).
				Msg("confenge intel watch: could not record a delivered watch update")
			return fmt.Errorf("intel watch delivery commit: %w", err)
		}
		result.Dispatched++
		return nil

	case WatchPermanent:
		if _, err := c.store.SettleDelivery(ctx, key, models.IntelWatchDeliveryFailed, errText(dispatchErr), c.now()); err != nil {
			return fmt.Errorf("intel watch delivery settle: %w", err)
		}
		result.Failed++
		return nil

	case WatchTransient:
		if fenced {
			// It failed after the point of no return, so we cannot prove the
			// watcher was not written to. Park it.
			return c.park(ctx, key, subscription, event, "transient_after_handoff: "+errText(dispatchErr), result)
		}
		if _, err := c.store.ReleaseDelivery(ctx, key, errText(dispatchErr), c.now()); err != nil {
			return fmt.Errorf("intel watch delivery release: %w", err)
		}
		result.Retryable++
		// Surface it: this notification has NOT been delivered and the caller
		// should re-deliver the event.
		return fmt.Errorf("intel watch dispatch: %w", firstError(dispatchErr, errDispatchTransient))

	case WatchAmbiguous:
		return c.park(ctx, key, subscription, event, "ambiguous_dispatch_result: "+errText(dispatchErr), result)

	default:
		// A dispatcher that reports nothing recognisable is treated as
		// ambiguous. We cannot prove the watcher was not written to, and a
		// duplicate watch mail is worse than a delayed one.
		return c.park(ctx, key, subscription, event,
			fmt.Sprintf("unrecognised_dispatch_outcome:%q %s", string(outcome), errText(dispatchErr)), result)
	}
}

func (c *Consumer) park(ctx context.Context, key models.IntelWatchDeliveryKey, subscription models.IntelWatchSubscription, event OpportunityEvent, reason string, result *HandleResult) error {
	log.Warn().Str("subscription_id", subscription.ID.String()).Str("event_id", event.EventID).
		Str("reason", reason).Msg("confenge intel watch: delivery parked, not resending")
	if _, err := c.store.SettleDelivery(ctx, key, models.IntelWatchDeliveryAmbiguous, reason, c.now()); err != nil {
		return fmt.Errorf("intel watch delivery park: %w", err)
	}
	result.Parked++
	return nil
}

var errDispatchTransient = errors.New("dispatcher reported a transient failure")

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
