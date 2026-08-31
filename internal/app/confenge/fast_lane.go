package confenge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/models"
)

// The CONFENGE first-touch fast lane.
//
// One loop owns transport: claim a due row, apply the gates that protect a
// recipient or a mailbox, send, record what the provider said, close the row.
// Everything else -- touchpoint projections, delegated decisions, campaign
// progress -- is reconciled after the fact and can never turn a message the
// provider already accepted back into work.
//
// The legacy path spread one first touch across four processes (backend enrol,
// scheduler, worker SMTP, consumer projection) and seventeen state writes, so
// any one of them failing stranded the send. Here the process that observes the
// acceptance is the process that commits it.

// FirstTouchOutcome is the closed set of things a provider attempt can mean.
type FirstTouchOutcome string

const (
	// FirstTouchAccepted means the provider took responsibility for the message.
	FirstTouchAccepted FirstTouchOutcome = "accepted"
	// FirstTouchPermanent means retrying this recipient cannot succeed.
	FirstTouchPermanent FirstTouchOutcome = "permanent"
	// FirstTouchTransient means the attempt failed for a reason that may pass.
	FirstTouchTransient FirstTouchOutcome = "transient"
	// FirstTouchAmbiguous means we do not know whether the message left. It is
	// never retried: a duplicate cold email is worse than a delayed one.
	FirstTouchAmbiguous FirstTouchOutcome = "ambiguous"
)

// FirstTouchMessage is the exact approved payload handed to the provider.
type FirstTouchMessage struct {
	OrganizationID uuid.UUID
	EmailAccountID uuid.UUID
	MessageKey     string
	To             string
	Subject        string
	BodyText       string
}

// FirstTouchAcceptance is what the provider reported back.
type FirstTouchAcceptance struct {
	Provider          string
	ProviderMessageID string
	AcceptedAt        time.Time
}

// FirstTouchTransport sends one approved message synchronously. Synchronous is
// the point: the caller commits the ledger from the returned acceptance, so a
// provider fact cannot be lost between processes.
type FirstTouchTransport interface {
	SendFirstTouch(ctx context.Context, msg FirstTouchMessage) (FirstTouchAcceptance, FirstTouchOutcome, error)
}

// FastLaneWorker drives the loop.
type FastLaneWorker struct {
	service  Service
	interval time.Duration
}

func NewFastLaneWorker(service Service, interval time.Duration) *FastLaneWorker {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &FastLaneWorker{service: service, interval: interval}
}

func (w *FastLaneWorker) Run(ctx context.Context) {
	if w == nil || w.service == nil {
		return
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		// Drain while work remains and the gates keep allowing it, so a backlog
		// is limited by the send cap rather than by the tick interval.
		for i := 0; i < fastLaneMaxPerTick; i++ {
			progressed, err := w.service.ProcessFastLaneOnce(ctx)
			if err != nil || !progressed {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// fastLaneMaxPerTick bounds one tick. The real rate limit is the governor's
// cap and min-gap; this only stops a single tick monopolising the process.
const fastLaneMaxPerTick = 20

// ProcessFastLaneOnce claims at most one due row and takes it to a terminal
// state. It returns true when it did work, so the caller can drain.
func (s *service) ProcessFastLaneOnce(ctx context.Context) (bool, error) {
	if s.governor == nil || s.firstTouchTransport == nil {
		return false, nil
	}
	// Kill switch, durable pause and business window, resolved once and shared
	// with the status projection so the dispatcher and the dashboard cannot
	// disagree about whether transport is live.
	if transport := s.ResolveTransportState(ctx, nil); !transport.Active {
		return false, nil
	}

	item, err := s.governor.ClaimNextQueued(ctx)
	if err != nil || item == nil {
		return false, err
	}
	if item.Channel != dispatch.ChannelEmail {
		// The fast lane is the email first-touch path only. Leave anything else
		// for the legacy dispatcher rather than guessing at its transport.
		_ = s.governor.RetryQueue(ctx, item.ID, s.now().Add(time.Minute), "not_email_channel")
		return true, nil
	}

	// The ledger is the only authority on whether this already went out. A row
	// that survived a crash between provider acceptance and queue close is
	// closed here instead of being sent twice.
	if _, sent, sendErr := s.governor.SendRecorded(ctx, item.MessageKey); sendErr == nil && sent {
		_ = s.governor.MarkQueue(ctx, item.ID, dispatch.QueueSent, "")
		return true, nil
	}

	tp, err := s.repo.GetTouchpointByDraft(ctx, item.OrganizationID, item.DraftID)
	if err != nil {
		return true, err
	}
	if tp == nil {
		_ = s.governor.MarkQueue(ctx, item.ID, dispatch.QueueFailed, "approved touchpoint not found")
		return true, nil
	}

	// Essential pre-send gates. Anything that only describes where the work came
	// from -- release SHA, snapshot identity, membership revision, copy versions
	// -- is deliberately absent: it was already authoritative when this row was
	// approved into the queue, and re-deciding it at transport time is what
	// cancelled thousands of approved rows on every deploy.
	if reason, ok := s.fastLaneBlock(ctx, item, tp); !ok {
		_ = s.governor.MarkQueue(ctx, item.ID, dispatch.QueueCancelled, reason)
		return true, nil
	}

	mailboxID, err := s.fastLaneMailbox(ctx, item)
	if err != nil {
		_ = s.governor.RetryQueue(ctx, item.ID, s.fastLaneBackoff(item), "mailbox_unresolved: "+err.Error())
		return true, nil
	}

	// Cap, min-gap, mailbox envelope, window and pause, decided atomically under
	// the store lock. Reserved under the queue's own message key, so the
	// reservation, the ledger and the queue row all share one identity.
	res, err := s.governor.TryReserve(ctx, dispatch.ReserveRequest{
		OrganizationID: item.OrganizationID,
		EmailAccountID: &mailboxID,
		Channel:        dispatch.ChannelEmail,
		MessageKey:     item.MessageKey,
		DraftID:        &item.DraftID,
	})
	if err != nil {
		_ = s.governor.RetryQueue(ctx, item.ID, s.fastLaneBackoff(item), "reserve_failed: "+err.Error())
		return true, err
	}
	if res.AlreadyCommitted {
		_ = s.governor.MarkQueue(ctx, item.ID, dispatch.QueueSent, "")
		return true, nil
	}
	if !res.Allowed || res.Reservation == nil {
		// A cap or window deferral is not a failure. Put the row back at the
		// next moment it could legitimately send, without consuming an attempt.
		next := res.NextSlot
		if next.IsZero() {
			next = s.now().Add(fastLaneDeferFallback)
		}
		_ = s.governor.DeferQueue(ctx, item.ID, next, res.Reason)
		return true, nil
	}

	// Qualification is time-dependent. Re-read it after the reservation and at
	// the final boundary before the provider, so an approval that expired while
	// queued cannot leave the system.
	committedRun, lineageErr := s.repo.HasCommittedFeedRun(ctx, item.OrganizationID, tp.SourceRunID)
	if lineageErr != nil || !committedRun {
		reason := "feed_lineage_unavailable"
		if lineageErr == nil {
			reason = "feed_lineage_uncommitted"
		}
		_ = s.governor.Release(ctx, res.Reservation.ID, reason)
		_ = s.governor.MarkQueue(ctx, item.ID, dispatch.QueueCancelled, reason)
		return true, nil
	}
	account, accountErr := s.repo.GetAccount(ctx, item.OrganizationID, tp.AccountID)
	qualification := AccountCommercialQualification(account, s.now())
	if accountErr != nil || !qualification.AllowsTransport() {
		reason := "commercial_qualification_unavailable"
		if accountErr == nil {
			reason = firstNonEmpty(firstHold(qualification.ReasonCodes), ReasonQualificationMissing)
		}
		_ = s.governor.Release(ctx, res.Reservation.ID, reason)
		_ = s.governor.MarkQueue(ctx, item.ID, dispatch.QueueCancelled, reason)
		return true, nil
	}
	accepted, acceptedErr := s.repo.HasAcceptedInitialForAccount(ctx, item.OrganizationID, tp.AccountID)
	if acceptedErr != nil || accepted {
		reason := "accepted_initial_ledger_lookup_failed"
		if acceptedErr == nil {
			reason = "accepted_initial_already_recorded"
		}
		_ = s.governor.Release(ctx, res.Reservation.ID, reason)
		_ = s.governor.MarkQueue(ctx, item.ID, dispatch.QueueCancelled, reason)
		return true, nil
	}

	acceptance, outcome, sendErr := s.firstTouchTransport.SendFirstTouch(ctx, FirstTouchMessage{
		OrganizationID: item.OrganizationID,
		EmailAccountID: mailboxID,
		MessageKey:     item.MessageKey,
		To:             tp.Recipient,
		Subject:        tp.Subject,
		BodyText:       tp.BodyText,
	})

	switch outcome {
	case FirstTouchAccepted:
		if acceptance.AcceptedAt.IsZero() {
			acceptance.AcceptedAt = s.now()
		}
		// THE authoritative write. One transaction records the send, commits the
		// reservation and closes the queue row. Nothing after this point can
		// make the message eligible again.
		if err := s.governor.CommitFirstTouch(ctx, res.Reservation.ID, acceptance.AcceptedAt, dispatch.SendEvidence{
			Recipient:         strings.ToLower(strings.TrimSpace(tp.Recipient)),
			Provider:          acceptance.Provider,
			ProviderMessageID: acceptance.ProviderMessageID,
			QueueID:           &item.ID,
		}); err != nil {
			// The mail is gone and we could not record it. Park the row as
			// attempted so reconciliation owns it; never re-send from here.
			log.Error().Err(err).Str("message_key", item.MessageKey).
				Msg("confenge fast lane could not record an accepted send")
			_ = s.governor.MarkQueue(ctx, item.ID, dispatch.QueueAttempted, "commit_failed_after_acceptance")
			return true, err
		}
		// Compatibility only. These keep legacy readers honest; they are allowed
		// to fail and converge later, and none of them gates the send.
		s.reconcileFastLaneCompat(ctx, item, tp, acceptance)
		return true, nil

	case FirstTouchPermanent:
		_ = s.governor.Release(ctx, res.Reservation.ID, errText(sendErr))
		s.fastLaneSuppress(ctx, item, tp, sendErr)
		_ = s.governor.MarkQueue(ctx, item.ID, dispatch.QueueFailed, errText(sendErr))
		return true, nil

	case FirstTouchAmbiguous:
		// We do not know what happened. Releasing the reservation would let the
		// row be claimed again, so it stays parked for human/reconciler review.
		log.Warn().Str("message_key", item.MessageKey).Err(sendErr).
			Msg("confenge fast lane got an ambiguous provider result; not resending")
		_ = s.governor.MarkQueue(ctx, item.ID, dispatch.QueueAttempted, "ambiguous_provider_result: "+errText(sendErr))
		return true, nil

	default: // FirstTouchTransient
		_ = s.governor.Release(ctx, res.Reservation.ID, errText(sendErr))
		if item.Attempts >= fastLaneMaxAttempts {
			_ = s.governor.MarkQueue(ctx, item.ID, dispatch.QueueFailed, "retries_exhausted: "+errText(sendErr))
			return true, nil
		}
		_ = s.governor.RetryQueue(ctx, item.ID, s.fastLaneBackoff(item), errText(sendErr))
		return true, nil
	}
}

const (
	fastLaneMaxAttempts   = 5
	fastLaneDeferFallback = 5 * time.Minute
)

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// fastLaneBackoff spaces transient retries without ever scheduling outside the
// business window; the window gate re-checks on the next claim regardless.
func (s *service) fastLaneBackoff(item *dispatch.QueueItem) time.Time {
	attempts := item.Attempts
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 5 {
		attempts = 5
	}
	return s.now().Add(time.Duration(1<<(attempts-1)) * 5 * time.Minute)
}

// fastLaneBlock holds the gates that protect a recipient or a mailbox. It
// returns false with a reason when the row must not be sent at all.
func (s *service) fastLaneBlock(ctx context.Context, item *dispatch.QueueItem, tp *models.OutreachTouchpoint) (string, bool) {
	// The approved content must still be the content we are about to send.
	if tp.State != models.TouchpointQueued || tp.ContentHash == "" || tp.ApprovedContentHash != tp.ContentHash {
		return "approval or content hash changed", false
	}
	if tp.State == models.TouchpointSent {
		return "already_sent", false
	}
	recipient := strings.ToLower(strings.TrimSpace(tp.Recipient))
	if recipient == "" || !strings.Contains(recipient, "@") || strings.ContainsAny(recipient, " \t\r\n") {
		return "recipient_not_usable", false
	}
	if strings.TrimSpace(tp.Subject) == "" || strings.TrimSpace(tp.BodyText) == "" {
		return "approved_payload_empty", false
	}
	// Hard bounce, complaint and opt-out suppression.
	if suppressions, ok := s.repo.(interface {
		GetOutreachRecipientSuppression(context.Context, uuid.UUID, string) (*models.SuppressedRecipient, error)
	}); ok {
		suppression, err := suppressions.GetOutreachRecipientSuppression(ctx, item.OrganizationID, tp.Recipient)
		if err != nil {
			// Fail closed: an unreadable suppression list is not permission.
			return "suppression_lookup_failed", false
		}
		if suppression != nil {
			return "recipient_suppressed:" + string(suppression.Source), false
		}
	}
	return "", true
}

// fastLaneMailbox resolves the sending mailbox for a queued row.
func (s *service) fastLaneMailbox(ctx context.Context, item *dispatch.QueueItem) (uuid.UUID, error) {
	if item.EmailAccountID != nil && *item.EmailAccountID != uuid.Nil {
		return *item.EmailAccountID, nil
	}
	id, err := s.resolveConfengeMailbox(ctx, item.OrganizationID)
	if err != nil {
		return uuid.Nil, err
	}
	if id == uuid.Nil {
		return uuid.Nil, fmt.Errorf("no active CONFENGE mailbox")
	}
	return id, nil
}
