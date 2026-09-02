package confenge

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/liveintel"
)

// The INTEL_WATCH dispatcher.
//
// This is an adapter, not a transport. It reuses the exact FirstTouchTransport
// the first-touch fast lane sends through, so there is one SMTP implementation
// in this system and one place where a provider fact is observed. What it does
// NOT reuse is the first-touch ledger: no reservation, no dispatch queue row,
// no send-governor commit. INTEL_WATCH mail is subscription mail the recipient
// asked for, so it neither consumes nor is bounded by the cold-outreach send
// budget, and a watch send can never mark a first touch as delivered.

// MessageKeyIntelWatch is the INTEL_WATCH idempotency identity. It is
// deliberately namespaced away from MessageKeyCampaignEmail
// ("email:campaign:...") and MessageKeyIntelSeed ("email:intel_seed:...") so a
// watch key can never be mistaken for, or collide with, a first touch.
func MessageKeyIntelWatch(subscriptionID uuid.UUID, eventIdentity, contentHash string) string {
	return fmt.Sprintf("email:intel_watch:sub:%s:event:%s:content:%s",
		subscriptionID, strings.TrimSpace(eventIdentity), strings.TrimSpace(contentHash))
}

// IntelWatchMailboxResolver answers which mailbox an organization's watch mail
// leaves from. The service's own CONFENGE mailbox resolver satisfies it.
type IntelWatchMailboxResolver func(ctx context.Context, organizationID uuid.UUID) (uuid.UUID, error)

// IntelWatchDispatcher implements liveintel.Dispatcher over the first-touch
// transport.
type IntelWatchDispatcher struct {
	transport FirstTouchTransport
	mailbox   IntelWatchMailboxResolver
	// compose is injectable so a test can force a composition failure without
	// reaching into the copy rules.
	compose func(liveintel.WatchDelivery) (liveintel.WatchMessage, error)
}

// NewIntelWatchDispatcher builds the adapter. A nil transport or resolver is a
// supported state: every dispatch then reports a transient failure, which the
// ledger treats as still-deliverable rather than as a delivered notification.
func NewIntelWatchDispatcher(transport FirstTouchTransport, mailbox IntelWatchMailboxResolver) *IntelWatchDispatcher {
	return &IntelWatchDispatcher{
		transport: transport,
		mailbox:   mailbox,
		compose:   liveintel.ComposeWatchMessage,
	}
}

// DispatchWatchUpdate sends one watcher notification and reports what the
// provider did, in the ledger's own vocabulary.
func (d *IntelWatchDispatcher) DispatchWatchUpdate(ctx context.Context, delivery liveintel.WatchDelivery) (liveintel.WatchDispatchOutcome, error) {
	if d == nil || d.transport == nil {
		return liveintel.WatchTransient, fmt.Errorf("intel watch transport not wired")
	}
	if d.mailbox == nil {
		return liveintel.WatchTransient, fmt.Errorf("intel watch mailbox resolver not wired")
	}
	subscription := delivery.Subscription
	// An opted-out subscription must never be written to, even if a stale claim
	// reached this far. The ledger cannot know this; the subscription row does.
	if !subscription.Active() {
		return liveintel.WatchPermanent, fmt.Errorf("subscription is unsubscribed")
	}
	recipient := strings.ToLower(strings.TrimSpace(subscription.ContactEmail))
	if recipient == "" || !strings.Contains(recipient, "@") || strings.ContainsAny(recipient, " \t\r\n") {
		return liveintel.WatchPermanent, fmt.Errorf("subscription has no usable recipient")
	}

	mailboxID, err := d.mailbox(ctx, subscription.OrganizationID)
	if err != nil {
		// No mailbox today may well mean a mailbox tomorrow. Retryable.
		return liveintel.WatchTransient, fmt.Errorf("intel watch mailbox: %w", err)
	}
	if mailboxID == uuid.Nil {
		return liveintel.WatchTransient, fmt.Errorf("intel watch mailbox unresolved")
	}

	composer := d.compose
	if composer == nil {
		composer = liveintel.ComposeWatchMessage
	}
	message, err := composer(delivery)
	if err != nil {
		// A message this deployment cannot compose (no opt-out secret, malformed
		// event) will not become composable by retrying the same input.
		return liveintel.WatchPermanent, fmt.Errorf("intel watch compose: %w", err)
	}

	_, outcome, sendErr := d.transport.SendFirstTouch(ctx, FirstTouchMessage{
		OrganizationID: subscription.OrganizationID,
		EmailAccountID: mailboxID,
		MessageKey:     MessageKeyIntelWatch(subscription.ID, delivery.Event.EventID, delivery.ContentHash),
		To:             recipient,
		Subject:        message.Subject,
		BodyText:       message.BodyText,
		// The ledger's fence is the transport's fence. Passing it straight
		// through is what makes an end-of-DATA failure ambiguous rather than
		// retryable: the row is already IN_FLIGHT when the socket dies.
		BeforeHandoff: delivery.BeforeHandoff,
	})
	return watchOutcomeFor(outcome), sendErr
}

// watchOutcomeFor is the whole mapping between the two lanes' outcome sets.
// They are separate types on purpose (a watch delivery is not a first touch),
// but they classify the same four provider realities.
func watchOutcomeFor(outcome FirstTouchOutcome) liveintel.WatchDispatchOutcome {
	switch outcome {
	case FirstTouchAccepted:
		return liveintel.WatchDelivered
	case FirstTouchPermanent:
		return liveintel.WatchPermanent
	case FirstTouchAmbiguous:
		return liveintel.WatchAmbiguous
	case FirstTouchTransient:
		return liveintel.WatchTransient
	default:
		// An unrecognised transport answer is not proof the mail stayed home.
		// Ambiguous parks it; transient would risk a duplicate.
		return liveintel.WatchAmbiguous
	}
}
