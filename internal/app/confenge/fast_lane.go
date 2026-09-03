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

// Named first-touch stop reasons. Matching rows are cancelled and never handed
// to the provider; a retry is not a second chance to send.
const (
	FastLaneFollowUpNotAuthorized = "FOLLOW_UP_NOT_AUTHORIZED"
	FastLaneRecipientOptOut       = "recipient_opt_out"
	FastLaneRecipientComplaint    = "recipient_complaint"
	FastLaneAccountReplied        = "account_already_replied"
)

// FirstTouchMessage is the exact approved payload handed to the provider.
type FirstTouchMessage struct {
	OrganizationID uuid.UUID
	EmailAccountID uuid.UUID
	MessageKey     string
	To             string
	Subject        string
	BodyText       string
	// BeforeHandoff is invoked by the transport after MAIL/RCPT succeed and
	// immediately before SMTP DATA. Success creates the durable no-resend fence.
	BeforeHandoff func(context.Context) error
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
	// Attempts is incremented by claim. A row whose prior failures already used
	// the budget must become terminal without touching the provider again.
	if item.Attempts > fastLaneMaxAttempts {
		_ = s.governor.MarkQueue(ctx, item.ID, dispatch.QueueFailed, "retries_exhausted_before_provider")
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
	if reason, ok, retryable := s.fastLaneBlock(ctx, item, tp); !ok {
		if retryable {
			_ = s.governor.DeferQueue(ctx, item.ID, s.fastLaneBackoff(item), reason)
		} else {
			_ = s.governor.MarkQueue(ctx, item.ID, dispatch.QueueCancelled, reason)
		}
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

	// Re-read every live safety fact after capacity reservation. Admission facts
	// stay committed, but a DNC/suppression/content mutation racing the claim must
	// still win before the irreversible handoff.
	liveTouchpoint, liveTouchpointErr := s.repo.GetTouchpointByDraft(ctx, item.OrganizationID, item.DraftID)
	if liveTouchpointErr != nil || liveTouchpoint == nil {
		reason := "touchpoint_safety_recheck_failed"
		_ = s.governor.Release(ctx, res.Reservation.ID, reason)
		_ = s.governor.DeferQueue(ctx, item.ID, s.fastLaneBackoff(item), reason)
		return true, nil
	}
	tp = liveTouchpoint
	if reason, ok, retryable := s.fastLaneBlock(ctx, item, tp); !ok {
		_ = s.governor.Release(ctx, res.Reservation.ID, reason)
		if retryable {
			_ = s.governor.DeferQueue(ctx, item.ID, s.fastLaneBackoff(item), reason)
		} else {
			_ = s.governor.MarkQueue(ctx, item.ID, dispatch.QueueCancelled, reason)
		}
		return true, nil
	}
	if transport := s.ResolveTransportState(ctx, nil); !transport.Active {
		reason := "transport_blocked_after_reservation"
		_ = s.governor.Release(ctx, res.Reservation.ID, reason)
		_ = s.governor.DeferQueue(ctx, item.ID, s.now().Add(fastLaneDeferFallback), reason)
		return true, nil
	}
	liveMailboxID, mailboxErr := s.fastLaneMailbox(ctx, item)
	if mailboxErr != nil || liveMailboxID != mailboxID {
		reason := "mailbox_changed_after_reservation"
		if mailboxErr != nil {
			reason = "mailbox_unavailable_after_reservation: " + mailboxErr.Error()
		}
		_ = s.governor.Release(ctx, res.Reservation.ID, reason)
		_ = s.governor.DeferQueue(ctx, item.ID, s.fastLaneBackoff(item), reason)
		return true, nil
	}

	// Feed lineage and temporal qualification were consumed at queue admission.
	// Rechecking either here makes a durable authorization disappear on a feed
	// pointer change or on the mere passage of time. Explicit live deactivation,
	// role, recipient and content safety are enforced by fastLaneBlock above.
	accepted, acceptedErr := s.repo.HasAcceptedInitialForAccount(ctx, item.OrganizationID, tp.AccountID)
	if acceptedErr != nil {
		reason := "accepted_initial_ledger_lookup_failed"
		_ = s.governor.Release(ctx, res.Reservation.ID, reason)
		_ = s.governor.DeferQueue(ctx, item.ID, s.fastLaneBackoff(item), reason)
		return true, nil
	}
	if accepted {
		reason := "accepted_initial_already_recorded"
		_ = s.governor.Release(ctx, res.Reservation.ID, reason)
		_ = s.governor.MarkQueue(ctx, item.ID, dispatch.QueueCancelled, reason)
		return true, nil
	}

	sendBudget := res.Reservation.LeaseUntil.Sub(s.now()) - fastLaneSMTPLeaseMargin
	if sendBudget <= 0 {
		_ = s.governor.Release(ctx, res.Reservation.ID, "smtp_budget_exhausted_before_handoff")
		_ = s.governor.RetryQueue(ctx, item.ID, s.fastLaneBackoff(item), "smtp_budget_exhausted_before_handoff")
		return true, nil
	}
	sendCtx, cancelSend := context.WithTimeout(ctx, sendBudget)
	defer cancelSend()
	acceptance, outcome, sendErr := s.firstTouchTransport.SendFirstTouch(sendCtx, FirstTouchMessage{
		OrganizationID: item.OrganizationID,
		EmailAccountID: mailboxID,
		MessageKey:     item.MessageKey,
		To:             tp.Recipient,
		Subject:        tp.Subject,
		BodyText:       tp.BodyText,
		BeforeHandoff: func(handoffCtx context.Context) error {
			if transport := s.ResolveTransportState(handoffCtx, nil); !transport.Active {
				return fmt.Errorf("transport blocked immediately before SMTP DATA")
			}
			liveTP, liveErr := s.repo.GetTouchpointByDraft(handoffCtx, item.OrganizationID, item.DraftID)
			if liveErr != nil {
				return fmt.Errorf("touchpoint safety lookup before SMTP DATA: %w", liveErr)
			}
			if liveTP == nil {
				return fmt.Errorf("touchpoint missing before SMTP DATA")
			}
			if TouchpointBindingHash(liveTP) != TouchpointBindingHash(tp) {
				return fmt.Errorf("approved payload changed before SMTP DATA")
			}
			if reason, ok, _ := s.fastLaneBlock(handoffCtx, item, liveTP); !ok {
				return fmt.Errorf("safety blocked before SMTP DATA: %s", reason)
			}
			lastMailboxID, mailboxErr := s.fastLaneMailbox(handoffCtx, item)
			if mailboxErr != nil {
				return fmt.Errorf("mailbox changed before SMTP DATA: %w", mailboxErr)
			}
			if lastMailboxID != mailboxID {
				return fmt.Errorf("mailbox identity changed before SMTP DATA")
			}
			terminal, terminalErr := s.repo.HasAcceptedInitialForAccount(handoffCtx, item.OrganizationID, tp.AccountID)
			if terminalErr != nil {
				return fmt.Errorf("terminal initial appeared before SMTP DATA: %w", terminalErr)
			}
			if terminal {
				return fmt.Errorf("terminal initial appeared before SMTP DATA")
			}
			return s.governor.StartHandoff(handoffCtx, res.Reservation.ID, item.ID)
		},
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
			_, _ = s.governor.FinalizeHandoff(ctx, res.Reservation.ID, "commit_failed_after_acceptance")
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
		return s.fastLaneParkUnknown(ctx, item, res.Reservation.ID, sendErr, "ambiguous_provider_result")

	case FirstTouchTransient:
		// A stop that arrived around MAIL/RCPT/DATA (DNC, suppression, opt-out,
		// complaint, reply, pause) still wins. Retrying would send after the
		// recipient asked us to stop.
		if s.fastLaneStopsAfterAttempt(ctx, item, res.Reservation.ID) {
			return true, nil
		}
		if finalized, finalizeErr := s.governor.FinalizeHandoff(ctx, res.Reservation.ID, "transient_after_handoff: "+errText(sendErr)); finalizeErr != nil {
			return true, finalizeErr
		} else if finalized {
			_ = s.governor.MarkQueue(ctx, item.ID, dispatch.QueueAttempted, "transient_after_handoff: "+errText(sendErr))
			return true, nil
		}
		_ = s.governor.Release(ctx, res.Reservation.ID, errText(sendErr))
		if item.Attempts >= fastLaneMaxAttempts {
			_ = s.governor.MarkQueue(ctx, item.ID, dispatch.QueueFailed, "retries_exhausted: "+errText(sendErr))
			return true, nil
		}
		_ = s.governor.RetryQueue(ctx, item.ID, s.fastLaneBackoff(item), errText(sendErr))
		return true, nil

	default:
		// An unrecognized provider answer is not proof the mail stayed home.
		// Park it the same way as ambiguous: never retry, never NO_RESPONSE.
		return s.fastLaneParkUnknown(ctx, item, res.Reservation.ID, sendErr, "unknown_provider_result")
	}
}

const (
	fastLaneMaxAttempts   = 5
	fastLaneDeferFallback = 5 * time.Minute
	// The socket deadline must fire while the reservation still belongs to this
	// worker. This margin leaves time for the accepted-ledger transaction.
	fastLaneSMTPLeaseMargin = 10 * time.Second
)

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// fastLaneParkUnknown parks a row whose provider result cannot be classified.
// The message may already be in flight, so it is never made sendable again.
func (s *service) fastLaneParkUnknown(ctx context.Context, item *dispatch.QueueItem, reservationID uuid.UUID, sendErr error, reasonPrefix string) (bool, error) {
	reason := reasonPrefix + ": " + errText(sendErr)
	log.Warn().Str("message_key", item.MessageKey).Err(sendErr).
		Msg("confenge fast lane got an unknown provider result; not resending")
	_, _ = s.governor.FinalizeHandoff(ctx, reservationID, reason)
	_ = s.governor.MarkQueue(ctx, item.ID, dispatch.QueueAttempted, reason)
	return true, nil
}

// fastLaneStopsAfterAttempt re-checks live stops after a provider attempt that
// did not accept. A newly arrived stop cancels (or defers, for pause) instead
// of retrying.
func (s *service) fastLaneStopsAfterAttempt(ctx context.Context, item *dispatch.QueueItem, reservationID uuid.UUID) bool {
	if transport := s.ResolveTransportState(ctx, nil); !transport.Active {
		reason := "transport_blocked_after_reservation"
		_ = s.governor.Release(ctx, reservationID, reason)
		_ = s.governor.DeferQueue(ctx, item.ID, s.now().Add(fastLaneDeferFallback), reason)
		return true
	}
	liveTP, err := s.repo.GetTouchpointByDraft(ctx, item.OrganizationID, item.DraftID)
	if err != nil || liveTP == nil {
		reason := "touchpoint_safety_recheck_failed"
		_ = s.governor.Release(ctx, reservationID, reason)
		_ = s.governor.DeferQueue(ctx, item.ID, s.fastLaneBackoff(item), reason)
		return true
	}
	if reason, ok, retryable := s.fastLaneBlock(ctx, item, liveTP); !ok {
		_ = s.governor.Release(ctx, reservationID, reason)
		if retryable {
			_ = s.governor.DeferQueue(ctx, item.ID, s.fastLaneBackoff(item), reason)
		} else {
			_ = s.governor.MarkQueue(ctx, item.ID, dispatch.QueueCancelled, reason)
		}
		return true
	}
	return false
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
func (s *service) fastLaneBlock(ctx context.Context, item *dispatch.QueueItem, tp *models.OutreachTouchpoint) (string, bool, bool) {
	// The fast lane is first-touch only. A follow-up that reached this queue is
	// never handed to the provider, even if every other gate would allow it.
	purpose := strings.ToUpper(strings.TrimSpace(tp.Purpose))
	if purpose != models.TouchpointPurposeInitial || tp.Ordinal != 1 {
		return FastLaneFollowUpNotAuthorized, false, false
	}
	// The approved content must still be the content we are about to send.
	liveHash := TouchpointBindingHash(tp)
	if tp.State != models.TouchpointQueued || liveHash == "" || tp.ContentHash != liveHash || tp.ApprovedContentHash != liveHash {
		return "approval or content binding hash changed", false, false
	}
	if tp.State == models.TouchpointSent {
		return "already_sent", false, false
	}
	recipient := strings.ToLower(strings.TrimSpace(tp.Recipient))
	if recipient == "" || !strings.Contains(recipient, "@") || strings.ContainsAny(recipient, " \t\r\n") {
		return "recipient_not_usable", false, false
	}
	if strings.TrimSpace(tp.Subject) == "" || strings.TrimSpace(tp.BodyText) == "" {
		return "approved_payload_empty", false, false
	}
	queueRecipient := strings.ToLower(strings.TrimSpace(item.RecipientRef))
	if queueRecipient == "" || queueRecipient != recipient {
		return "recipient_binding_drift", false, false
	}
	account, err := s.repo.GetAccount(ctx, item.OrganizationID, tp.AccountID)
	if err != nil || account == nil {
		return "account_safety_lookup_failed", false, true
	}
	if account.Blocked || account.DoNotContact {
		return "account_blocked_or_dnc", false, false
	}
	switch account.QueueState {
	case models.OutreachQueueBlocked, models.OutreachQueueBounced, models.OutreachQueueDoNotContact,
		models.OutreachQueueSent, models.OutreachQueueReplied, models.OutreachQueueMeeting,
		models.OutreachQueueProposal, models.OutreachQueueWon, models.OutreachQueueLost:
		return "account_terminal:" + account.QueueState, false, false
	}
	replied, replyErr := s.repo.ListTouchpoints(ctx, item.OrganizationID, tp.AccountID, models.TouchpointReplied, 1, 0)
	if replyErr != nil {
		return "reply_lookup_failed", false, true
	}
	if len(replied) > 0 {
		return FastLaneAccountReplied, false, false
	}
	qualificationState := strings.ToUpper(strings.TrimSpace(account.CommercialQualificationState))
	if account.CommercialQualificationDeactivated || qualificationState == CommercialRevoked {
		return "commercial_qualification_deactivated", false, false
	}
	role := strings.ToUpper(strings.TrimSpace(account.TargetPartyRole))
	leadCNPJ := NormalizeCNPJ14(account.CNPJ14)
	buyerCNPJ := NormalizeCNPJ14(account.BuyerCNPJ14)
	roleStatus := strings.ToUpper(strings.TrimSpace(account.ContractorRoleStatus))
	roleConflict := role == ContractorRoleConflict || role == "BUYER_CONFLICT" || role == "BUYER" ||
		roleStatus == ContractorRoleConflict || roleStatus == "PARTY_ROLE_CONFLICT"
	identityConflict := leadCNPJ != "" && buyerCNPJ == leadCNPJ
	if account.SupplierIdentityRef != "" && account.SupplierIdentityRef == account.BuyerIdentityRef {
		identityConflict = true
	}
	if roleConflict || identityConflict {
		return "target_party_role_conflict", false, false
	}
	if tp.ContactCandidateID != nil {
		candidate, candidateErr := s.repo.GetCandidate(ctx, item.OrganizationID, *tp.ContactCandidateID)
		if candidateErr != nil {
			return "candidate_safety_lookup_failed", false, true
		}
		// A missing historical candidate is provenance loss, not a new safety
		// fact. Positive live flags or a concrete binding drift still revoke.
		if candidate != nil {
			if candidate.OrganizationID != item.OrganizationID || candidate.AccountID != tp.AccountID {
				return "candidate_account_binding_drift", false, false
			}
			candidateEmail := strings.ToLower(strings.TrimSpace(candidate.Email))
			if candidateEmail != "" && candidateEmail != recipient {
				return "candidate_recipient_binding_drift", false, false
			}
			if candidate.Blocked || candidate.DoNotContact || candidate.Bounced {
				return "candidate_blocked_dnc_or_bounced", false, false
			}
			switch candidate.VerificationStatus {
			case models.OutreachVerifyInvalid, models.OutreachVerifyBounced, models.OutreachVerifyDoNotContact:
				return "candidate_terminal:" + candidate.VerificationStatus, false, false
			}
			if suppression := strings.ToUpper(strings.TrimSpace(candidate.RouteSuppression)); suppression != "" && suppression != "NONE" {
				return "candidate_route_suppressed:" + suppression, false, false
			}
		}
	}
	// Hard bounce, complaint and opt-out suppression.
	if suppressions, ok := s.repo.(interface {
		GetOutreachRecipientSuppression(context.Context, uuid.UUID, string) (*models.SuppressedRecipient, error)
	}); ok {
		suppression, err := suppressions.GetOutreachRecipientSuppression(ctx, item.OrganizationID, tp.Recipient)
		if err != nil {
			// Fail closed: an unreadable suppression list is not permission.
			return "suppression_lookup_failed", false, true
		}
		if suppression != nil {
			switch suppression.Source {
			case models.DeliverabilityEventUnsubscribe:
				return FastLaneRecipientOptOut, false, false
			case models.DeliverabilityEventComplaint:
				return FastLaneRecipientComplaint, false, false
			default:
				return "recipient_suppressed:" + string(suppression.Source), false, false
			}
		}
	} else {
		return "suppression_lookup_unavailable", false, true
	}
	return "", true, false
}

// fastLaneMailbox resolves the sending mailbox for a queued row.
func (s *service) fastLaneMailbox(ctx context.Context, item *dispatch.QueueItem) (uuid.UUID, error) {
	// Resolve the configured/live sender every time. The queue binding is an
	// admission hint, not permission to keep using a mailbox after configuration
	// changed. The resolver fails closed on a missing explicit mailbox.
	id, err := s.resolveConfengeMailbox(ctx, item.OrganizationID)
	if err != nil {
		// Unit/in-memory services have no live mailbox database. Production does,
		// and must never turn a resolver error into permission to use a stale row.
		if s.fastLaneDB == nil && item.EmailAccountID != nil && *item.EmailAccountID != uuid.Nil {
			return *item.EmailAccountID, nil
		}
		return uuid.Nil, err
	}
	if id == uuid.Nil {
		return uuid.Nil, fmt.Errorf("no active CONFENGE mailbox")
	}
	return id, nil
}
