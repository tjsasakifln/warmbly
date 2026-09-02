package models

import (
	"time"

	"github.com/google/uuid"
)

// INTEL_WATCH intent kinds. Closed set, mirrored by a CHECK constraint on
// confenge_intel_watch_subscriptions.intent_kind.
const (
	IntelWatchIntentNewOpportunity     = "NEW_OPPORTUNITY"
	IntelWatchIntentOpportunityChanged = "OPPORTUNITY_CHANGED"
	IntelWatchIntentDeadlineChanged    = "DEADLINE_CHANGED"
	IntelWatchIntentFitBecameRelevant  = "FIT_BECAME_RELEVANT"
)

// INTEL_WATCH delivery cadences. Closed set, mirrored by a CHECK constraint.
const (
	IntelWatchCadenceImmediate = "immediate"
	IntelWatchCadenceDaily     = "daily"
	IntelWatchCadenceWeekly    = "weekly"
)

// IntelWatchIntentKinds is the closed intent set in declaration order.
var IntelWatchIntentKinds = []string{
	IntelWatchIntentNewOpportunity,
	IntelWatchIntentOpportunityChanged,
	IntelWatchIntentDeadlineChanged,
	IntelWatchIntentFitBecameRelevant,
}

// IntelWatchCadences is the closed cadence set in declaration order.
var IntelWatchCadences = []string{
	IntelWatchCadenceImmediate,
	IntelWatchCadenceDaily,
	IntelWatchCadenceWeekly,
}

// IntelWatchSubscription is one contact's standing request to hear about one
// subject. An unsubscribed row is kept, not deleted: the consent record and the
// opt-out are both evidence.
type IntelWatchSubscription struct {
	ID                  uuid.UUID  `json:"id"`
	OrganizationID      uuid.UUID  `json:"organization_id"`
	ContactEmail        string     `json:"contact_email"`
	ContactID           *uuid.UUID `json:"contact_id,omitempty"`
	AccountID           *uuid.UUID `json:"account_id,omitempty"`
	IntentKind          string     `json:"intent_kind"`
	SubjectKey          string     `json:"subject_key"`
	Topic               string     `json:"topic"`
	Cadence             string     `json:"cadence"`
	ConsentSource       string     `json:"consent_source"`
	ConsentAt           *time.Time `json:"consent_at,omitempty"`
	ConsentProvenanceOK bool       `json:"consent_provenance_ok"`
	UnsubscribedAt      *time.Time `json:"unsubscribed_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// Active reports whether the subscription may still receive watch mail.
func (s *IntelWatchSubscription) Active() bool {
	return s != nil && s.UnsubscribedAt == nil
}

// INTEL_WATCH delivery-ledger states. A single boolean "already sent" row cannot
// tell a delivery that was never attempted apart from one the watcher actually
// received, so the ledger carries the same reserve -> in-flight -> terminal shape
// the first-touch dispatch governor uses.
const (
	// IntelWatchDeliveryPending is a claimed attempt that has not yet reached the
	// dispatcher's point of no return. Its lease expiring makes it claimable
	// again: nothing was handed over, so a retry cannot duplicate.
	IntelWatchDeliveryPending = "PENDING"
	// IntelWatchDeliveryInFlight is the durable no-resend fence. The dispatcher
	// is past the point where it could still abort, so this row is never
	// re-claimed; an expired lease parks it as AMBIGUOUS instead.
	IntelWatchDeliveryInFlight = "IN_FLIGHT"
	// IntelWatchDeliveryDispatched is terminal success: the watcher was written
	// to. Only this state means "nothing changed => nothing sent" on replay.
	IntelWatchDeliveryDispatched = "DISPATCHED"
	// IntelWatchDeliveryFailed is a terminal failure that retrying cannot fix.
	IntelWatchDeliveryFailed = "FAILED"
	// IntelWatchDeliveryAmbiguous is an unknown outcome. It is parked for review
	// and never auto-retried: a duplicate watch mail is worse than a late one.
	IntelWatchDeliveryAmbiguous = "AMBIGUOUS"
)

// IntelWatchDeliveryStates is the closed set, mirrored by a CHECK constraint on
// confenge_intel_watch_dedup.state.
var IntelWatchDeliveryStates = []string{
	IntelWatchDeliveryPending,
	IntelWatchDeliveryInFlight,
	IntelWatchDeliveryDispatched,
	IntelWatchDeliveryFailed,
	IntelWatchDeliveryAmbiguous,
}

// IntelWatchDeliveryTerminal reports whether a state can never be attempted
// again by any worker.
func IntelWatchDeliveryTerminal(state string) bool {
	switch state {
	case IntelWatchDeliveryDispatched, IntelWatchDeliveryFailed, IntelWatchDeliveryAmbiguous:
		return true
	}
	return false
}

// IntelWatchDeliveryKey is the three-part delivery identity: subscription,
// producer-assigned event id, semantic content hash. It is the ledger's primary
// key, so concurrency is resolved by the database rather than in the process.
type IntelWatchDeliveryKey struct {
	SubscriptionID uuid.UUID `json:"subscription_id"`
	EventIdentity  string    `json:"event_identity"`
	ContentHash    string    `json:"content_hash"`
}

// IntelWatchDelivery is one row of the delivery ledger.
type IntelWatchDelivery struct {
	IntelWatchDeliveryKey
	State      string     `json:"state"`
	Attempts   int        `json:"attempts"`
	ClaimedAt  *time.Time `json:"claimed_at,omitempty"`
	LeaseUntil *time.Time `json:"lease_until,omitempty"`
	SentAt     *time.Time `json:"sent_at,omitempty"`
	LastError  string     `json:"last_error"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// Reasons a delivery claim was refused. Granted claims carry an empty reason.
const (
	IntelWatchClaimAlreadyDispatched = "already_dispatched"
	IntelWatchClaimTerminalFailed    = "terminal_failed"
	IntelWatchClaimParkedAmbiguous   = "parked_ambiguous"
	IntelWatchClaimHeldElsewhere     = "lease_held_elsewhere"
	IntelWatchClaimAttemptsExhausted = "attempts_exhausted"
)

// IntelWatchClaim is the outcome of asking for the right to deliver once.
// Granted is true for exactly one caller at a time, enforced by the ledger row.
type IntelWatchClaim struct {
	Granted  bool   `json:"granted"`
	State    string `json:"state"`
	Attempts int    `json:"attempts"`
	Reason   string `json:"reason"`
}
