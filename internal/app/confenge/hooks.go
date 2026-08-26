package confenge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/models"
)

// OutcomeSink is the consumer-facing surface for commercial outcomes.
// Implemented by *service.
type OutcomeSink interface {
	Enabled() bool
	// NoteReply enqueues REPLIED when the address matches a staged candidate.
	NoteReply(ctx context.Context, orgID uuid.UUID, contactEmail string, meta map[string]any) error
	// NoteBounce enqueues BOUNCED for a failed recipient email.
	NoteBounce(ctx context.Context, orgID uuid.UUID, contactEmail, reason string) error
	// NoteDNC enqueues DO_NOT_CONTACT and marks matching candidates.
	NoteDNC(ctx context.Context, orgID uuid.UUID, contactEmail, reason string) error
}

// BounceObservation is the smallest provenance needed to distinguish a
// definitive suppression from a transient/unknown DSN without retaining the
// raw message body.
type BounceObservation struct {
	Class             string
	DeliveryUnknown   bool
	EventID           string
	MailboxID         string
	OccurredAt        time.Time
	ProviderName      string
	OriginalMessageID string
	EnhancedStatus    string
	SMTPStatus        string
	Diagnostic        string
	Reason            string
}

// StructuredBounceOutcomeSink is additive so older integrations that only
// implement OutcomeSink continue to compile. New DSN consumers must prefer it.
type StructuredBounceOutcomeSink interface {
	NoteBounceObservation(ctx context.Context, orgID uuid.UUID, contactEmail string, observation BounceObservation) error
}

type DeliveryObservation struct {
	EventID, MailboxID, ProviderName, OriginalMessageID string
	EnhancedStatus, SMTPStatus, Diagnostic              string
	OccurredAt                                          time.Time
}

type ComplaintObservation struct {
	EventID, MailboxID, ProviderName, OriginalMessageID string
	FeedbackType                                        string
	OccurredAt                                          time.Time
}

// StructuredEmailOutcomeSink receives observable post-acceptance facts.
type StructuredEmailOutcomeSink interface {
	NoteDeliveryObservation(context.Context, uuid.UUID, string, DeliveryObservation) error
	NoteComplaintObservation(context.Context, uuid.UUID, string, ComplaintObservation) error
}

// OnRecipientSuppressed implements advanced.ConfengeSuppressionHook.
func (s *service) OnRecipientSuppressed(ctx context.Context, orgID uuid.UUID, email, reason, source string, campaignID *uuid.UUID, occurredAt time.Time) error {
	if !strings.EqualFold(strings.TrimSpace(source), string(models.DeliverabilityEventUnsubscribe)) {
		return fmt.Errorf("unsupported recipient suppression source %q", source)
	}
	stableRef := "one-click:" + email
	if campaignID != nil {
		stableRef += ":" + campaignID.String()
	}
	return s.noteDNCObserved(ctx, orgID, email, reason, "one_click", stableRef, occurredAt)
}

// OnDeliverabilityEvent implements advanced.ConfengeDeliverabilityHook.
func (s *service) OnDeliverabilityEvent(ctx context.Context, orgID uuid.UUID, req *models.IngestDeliverabilityEventRequest, provider, idempotencyKey string, mailboxID uuid.UUID, occurredAt time.Time) error {
	if req == nil {
		return nil
	}
	originalMessageID := metaString(req.Metadata, "original_message_id")
	stableRef := firstNonEmpty(idempotencyKey, originalMessageID)
	mailboxRef := opaqueUUID(mailboxID)
	switch req.EventType {
	case models.DeliverabilityEventBounce:
		return s.NoteBounceObservation(ctx, orgID, req.RecipientEmail, BounceObservation{
			Class: "HARD", EventID: "webhook:" + stableRef, MailboxID: mailboxRef,
			OccurredAt: occurredAt, ProviderName: provider, OriginalMessageID: originalMessageID,
			EnhancedStatus: metaString(req.Metadata, "enhanced_status"), SMTPStatus: metaString(req.Metadata, "smtp_status"),
			Diagnostic: metaString(req.Metadata, "diagnostic"), Reason: req.Reason,
		})
	case models.DeliverabilityEventSoftBounce:
		return s.NoteBounceObservation(ctx, orgID, req.RecipientEmail, BounceObservation{
			Class: "SOFT", EventID: "webhook:" + stableRef, MailboxID: mailboxRef,
			OccurredAt: occurredAt, ProviderName: provider, OriginalMessageID: originalMessageID,
			EnhancedStatus: metaString(req.Metadata, "enhanced_status"), SMTPStatus: metaString(req.Metadata, "smtp_status"),
			Diagnostic: metaString(req.Metadata, "diagnostic"), Reason: req.Reason,
		})
	case models.DeliverabilityEventDelivered:
		return s.NoteDeliveryObservation(ctx, orgID, req.RecipientEmail, DeliveryObservation{
			EventID: "webhook:" + stableRef, MailboxID: mailboxRef, ProviderName: provider,
			OriginalMessageID: originalMessageID, OccurredAt: occurredAt,
			EnhancedStatus: metaString(req.Metadata, "enhanced_status"), SMTPStatus: metaString(req.Metadata, "smtp_status"),
			Diagnostic: metaString(req.Metadata, "diagnostic"),
		})
	case models.DeliverabilityEventComplaint:
		return s.NoteComplaintObservation(ctx, orgID, req.RecipientEmail, ComplaintObservation{
			EventID: "webhook:" + stableRef, MailboxID: mailboxRef, ProviderName: provider,
			OriginalMessageID: originalMessageID, FeedbackType: firstNonEmpty(metaString(req.Metadata, "feedback_type"), req.Reason),
			OccurredAt: occurredAt,
		})
	case models.DeliverabilityEventUnsubscribe:
		return s.noteDNCObserved(ctx, orgID, req.RecipientEmail, req.Reason, provider, "webhook:"+stableRef, occurredAt)
	case models.DeliverabilityEventReply:
		meta := map[string]any{
			"message_id": stableRef, "provider_name": provider, "mailbox_id": mailboxRef,
			"occurred_at": occurredAt.UTC().Format(time.RFC3339Nano), "reply_class": metaString(req.Metadata, "reply_class"),
			"subject": metaString(req.Metadata, "subject"), "body_text": metaString(req.Metadata, "body_text"),
		}
		if req.CampaignID != nil {
			meta["campaign_id"] = req.CampaignID.String()
		}
		if req.ContactID != nil {
			meta["contact_id"] = req.ContactID.String()
		}
		return s.NoteReply(ctx, orgID, req.RecipientEmail, meta)
	default:
		return nil
	}
}

func (s *service) enqueueErr(ctx context.Context, orgID uuid.UUID, ev models.OutreachOutcome) error {
	if xerr := s.EnqueueOutcome(ctx, orgID, ev); xerr != nil {
		return fmt.Errorf("%s", xerr.Message)
	}
	return nil
}

// NoteReply implements OutcomeSink.
func (s *service) NoteReply(ctx context.Context, orgID uuid.UUID, contactEmail string, meta map[string]any) error {
	if !s.cfg.Enabled {
		return nil
	}
	email := strings.TrimSpace(strings.ToLower(contactEmail))
	if email == "" {
		return nil
	}
	cand, acc, err := s.repo.FindCandidateByEmail(ctx, orgID, email)
	if err != nil || cand == nil || acc == nil {
		return nil // not a confenge lead
	}
	var correlatedTP *models.OutreachTouchpoint
	exactCorrelationRequested := false
	if raw := metaString(meta, "touchpoint_id"); raw != "" {
		exactCorrelationRequested = true
		if id, parseErr := uuid.Parse(raw); parseErr == nil {
			correlatedTP, _ = s.repo.GetTouchpoint(ctx, orgID, id)
		}
	}
	if correlatedTP == nil {
		campaignID, campaignErr := uuid.Parse(metaString(meta, "campaign_id"))
		contactID, contactErr := uuid.Parse(metaString(meta, "contact_id"))
		if campaignErr == nil && contactErr == nil {
			exactCorrelationRequested = true
			correlatedTP, _ = s.repo.GetTouchpointByEnrollment(ctx, orgID, campaignID, contactID)
		}
	}
	if correlatedTP != nil && correlatedTP.AccountID != acc.ID {
		// A syntactically valid identifier for another account is not evidence
		// that this reply belongs to that touchpoint or cohort.
		correlatedTP = nil
		exactCorrelationRequested = true
	}
	replyClass := ""
	if meta != nil {
		if v, ok := meta["reply_class"].(string); ok {
			replyClass = v
		}
		if v, ok := meta["classification"].(string); ok && replyClass == "" {
			replyClass = v
		}
	}
	if strings.EqualFold(strings.TrimSpace(replyClass), "OPT_OUT") {
		return s.noteDNC(ctx, orgID, email, "reply_class:opt_out", metaString(meta, "provider_name"))
	}
	observeCandidate := cand
	extra := ControlledEmailContext{ReplyClass: replyClass, ProviderName: metaString(meta, "provider_name")}
	if exactCorrelationRequested && correlatedTP == nil {
		// Preserve the reply as real while refusing to fabricate cohort/touchpoint
		// attribution when a supplied exact identifier cannot be reconciled.
		observeCandidate = nil
		extra.AccountRef = acc.ID.String()
	}
	stableRef := firstNonEmpty(metaString(meta, "message_id"), metaString(meta, "external_message_id"), metaString(meta, "original_message_id"))
	extra.StableEventRef = firstNonEmpty(stableRef, email+":"+replyClass)
	extra.MailboxID = metaString(meta, "mailbox_id")
	if err := s.observeControlledEmail(ctx, orgID, intel.EventReply, correlatedTP, observeCandidate, extra); err != nil {
		return err
	}
	var touchpointID uuid.UUID
	if correlatedTP != nil {
		touchpointID = correlatedTP.ID
	}
	actorID, _ := uuid.Parse(metaString(meta, "actor_id"))
	contactID, _ := uuid.Parse(metaString(meta, "contact_id"))
	var warmblyContactID *uuid.UUID
	if contactID != uuid.Nil {
		warmblyContactID = &contactID
	}
	occurredAt := time.Now().UTC()
	if raw := metaString(meta, "occurred_at"); raw != "" {
		if parsed, parseErr := time.Parse(time.RFC3339Nano, raw); parseErr == nil {
			occurredAt = parsed.UTC()
		}
	}
	_, xerr := s.ProcessInboundHandoff(ctx, orgID, InboundHandoff{
		Channel: models.OutreachChannelEmail, ContactEmail: email,
		Subject: metaString(meta, "subject"), BodyText: metaString(meta, "body_text"),
		PreClass: replyClass, ExternalMessageID: stableRef, OccurredAt: occurredAt,
		WarmblyContactID: warmblyContactID, ActorID: actorID, AccountID: acc.ID,
		TouchpointID: touchpointID,
	})
	if xerr != nil {
		return fmt.Errorf("%s", xerr.Message)
	}
	return nil
}

// NoteBounce implements OutcomeSink.
func (s *service) NoteBounce(ctx context.Context, orgID uuid.UUID, contactEmail, reason string) error {
	return s.NoteBounceObservation(ctx, orgID, contactEmail, BounceObservation{
		Class:  ClassifyBounceClass(reason),
		Reason: reason,
	})
}

// NoteBounceObservation records every attributable DSN but mutates suppression
// state only for a machine-proven HARD bounce.
func (s *service) NoteBounceObservation(ctx context.Context, orgID uuid.UUID, contactEmail string, observation BounceObservation) error {
	if !s.cfg.Enabled {
		return nil
	}
	email := strings.TrimSpace(strings.ToLower(contactEmail))
	if email == "" {
		return nil
	}
	cand, acc, err := s.repo.FindCandidateByEmail(ctx, orgID, email)
	if err != nil {
		return err
	}
	bounceClass := normalizeBounceClass(observation.Class)
	definitiveHard := bounceClass == "HARD"
	if cand != nil && definitiveHard {
		cand.Bounced = true
		cand.VerificationStatus = models.OutreachVerifyBounced
		_, _ = s.repo.UpsertCandidate(ctx, cand)
	}
	cnpj, lead := "", ""
	if acc != nil {
		cnpj, lead = acc.CNPJ14, acc.SourceLeadID
	}
	phone := ""
	if cand != nil {
		phone = cand.PhoneE164
		if phone == "" {
			phone = cand.Phone
		}
	}
	if definitiveHard {
		if suppressions, ok := s.repo.(interface {
			UpsertOutreachRecipientSuppression(context.Context, *models.SuppressedRecipient) error
			CancelSuppressedOutreachRecipient(context.Context, uuid.UUID, string, string, string) (int, error)
		}); ok {
			if err := suppressions.UpsertOutreachRecipientSuppression(ctx, &models.SuppressedRecipient{
				OrganizationID: orgID, Email: email, Reason: firstNonEmpty(observation.Reason, "hard bounce"),
				Source: models.DeliverabilityEventBounce, Metadata: map[string]interface{}{
					"bounce_class": bounceClass, "provider": observation.ProviderName,
					"enhanced_status": observation.EnhancedStatus,
				},
			}); err != nil {
				return err
			}
			if _, err := suppressions.CancelSuppressedOutreachRecipient(ctx, orgID, email, models.TouchpointBounced, "hard_bounce"); err != nil {
				return err
			}
		}
		s.cancelQueuedForRecipient(ctx, orgID, email, phone, "bounce")
	}
	typ := intel.EventUnknownState
	if observation.DeliveryUnknown {
		typ = intel.EventDeliveryUnknown
	} else if bounceClass == "HARD" {
		typ = "hard_bounce"
	} else if bounceClass == "SOFT" {
		typ = "soft_bounce"
	}
	var correlatedTP *models.OutreachTouchpoint
	if finder, ok := s.repo.(interface {
		GetTouchpointByProviderMessageID(context.Context, uuid.UUID, string) (*models.OutreachTouchpoint, error)
	}); ok && strings.TrimSpace(observation.OriginalMessageID) != "" {
		correlatedTP, _ = finder.GetTouchpointByProviderMessageID(ctx, orgID, observation.OriginalMessageID)
	}
	if err := s.observeControlledEmail(ctx, orgID, typ, correlatedTP, cand, ControlledEmailContext{
		OccurredAt: observation.OccurredAt, StableEventRef: firstNonEmpty(observation.EventID, observation.OriginalMessageID, email+":"+bounceClass),
		MailboxID:   observation.MailboxID,
		BounceClass: bounceClass, ProviderName: observation.ProviderName, SMTPStatus: observation.SMTPStatus,
		EnhancedStatus: observation.EnhancedStatus, Diagnostic: observation.Diagnostic,
	}); err != nil {
		return err
	}
	if !definitiveHard {
		return nil
	}
	idemRef := firstNonEmpty(strings.TrimSpace(observation.OriginalMessageID), email)
	return s.enqueueErr(ctx, orgID, models.OutreachOutcome{
		IdempotencyKey: fmt.Sprintf("bounced:%s:%s:%s", orgID, bounceClass, idemRef),
		SourceLeadID:   lead,
		CNPJ14:         cnpj,
		ContactEmail:   email,
		EventType:      OutcomeBounced,
		OccurredAt:     firstNonZeroTime(nil, observation.OccurredAt),
		Payload: mustJSON(map[string]any{
			"reason": observation.Reason, "bounce_class": bounceClass,
			"original_message_id": observation.OriginalMessageID,
			"enhanced_status":     observation.EnhancedStatus,
			"smtp_status":         observation.SMTPStatus,
			"diagnostic":          observation.Diagnostic,
		}),
	})
}

func (s *service) NoteDeliveryObservation(ctx context.Context, orgID uuid.UUID, contactEmail string, observation DeliveryObservation) error {
	if !s.cfg.Enabled {
		return nil
	}
	var cand *models.OutreachContactCandidate
	if email := strings.TrimSpace(strings.ToLower(contactEmail)); email != "" {
		cand, _, _ = s.repo.FindCandidateByEmail(ctx, orgID, email)
	}
	var touchpoint *models.OutreachTouchpoint
	if finder, ok := s.repo.(interface {
		GetTouchpointByProviderMessageID(context.Context, uuid.UUID, string) (*models.OutreachTouchpoint, error)
	}); ok {
		var err error
		touchpoint, err = finder.GetTouchpointByProviderMessageID(ctx, orgID, observation.OriginalMessageID)
		if err != nil {
			return err
		}
	}
	if touchpoint == nil && cand == nil {
		return nil
	}
	return s.observeControlledEmail(ctx, orgID, intel.EventDelivered, touchpoint, cand, ControlledEmailContext{
		OccurredAt: observation.OccurredAt, ProviderName: observation.ProviderName,
		MailboxID: observation.MailboxID, StableEventRef: firstNonEmpty(observation.EventID, observation.OriginalMessageID),
		SMTPStatus: observation.SMTPStatus, EnhancedStatus: observation.EnhancedStatus, Diagnostic: observation.Diagnostic,
	})
}

func (s *service) NoteComplaintObservation(ctx context.Context, orgID uuid.UUID, contactEmail string, observation ComplaintObservation) error {
	if !s.cfg.Enabled {
		return nil
	}
	email := strings.TrimSpace(strings.ToLower(contactEmail))
	if email == "" {
		return nil
	}
	cand, _, err := s.repo.FindCandidateByEmail(ctx, orgID, email)
	if err != nil {
		return err
	}
	if suppressions, ok := s.repo.(interface {
		UpsertOutreachRecipientSuppression(context.Context, *models.SuppressedRecipient) error
		CancelSuppressedOutreachRecipient(context.Context, uuid.UUID, string, string, string) (int, error)
	}); ok {
		if err := suppressions.UpsertOutreachRecipientSuppression(ctx, &models.SuppressedRecipient{
			OrganizationID: orgID, Email: email, Reason: firstNonEmpty(observation.FeedbackType, "provider complaint"),
			Source: models.DeliverabilityEventComplaint,
		}); err != nil {
			return err
		}
		if _, err := suppressions.CancelSuppressedOutreachRecipient(ctx, orgID, email, models.TouchpointCancelled, "spam_complaint"); err != nil {
			return err
		}
	}
	var touchpoint *models.OutreachTouchpoint
	if finder, ok := s.repo.(interface {
		GetTouchpointByProviderMessageID(context.Context, uuid.UUID, string) (*models.OutreachTouchpoint, error)
	}); ok {
		touchpoint, err = finder.GetTouchpointByProviderMessageID(ctx, orgID, observation.OriginalMessageID)
		if err != nil {
			return err
		}
	}
	return s.observeControlledEmail(ctx, orgID, intel.EventSpamComplaint, touchpoint, cand, ControlledEmailContext{
		OccurredAt: observation.OccurredAt, ProviderName: observation.ProviderName,
		MailboxID: observation.MailboxID, StableEventRef: firstNonEmpty(observation.EventID, observation.OriginalMessageID, email),
	})
}

// NoteDNC implements OutcomeSink.
func (s *service) NoteDNC(ctx context.Context, orgID uuid.UUID, contactEmail, reason string) error {
	return s.noteDNC(ctx, orgID, contactEmail, reason, "")
}

func (s *service) noteDNC(ctx context.Context, orgID uuid.UUID, contactEmail, reason, provider string) error {
	return s.noteDNCObserved(ctx, orgID, contactEmail, reason, provider, "", time.Time{})
}

func (s *service) noteDNCObserved(ctx context.Context, orgID uuid.UUID, contactEmail, reason, provider, stableRef string, occurredAt time.Time) error {
	if !s.cfg.Enabled {
		return nil
	}
	email := strings.TrimSpace(strings.ToLower(contactEmail))
	if email == "" {
		return nil
	}
	cand, acc, err := s.repo.FindCandidateByEmail(ctx, orgID, email)
	if err != nil {
		return err
	}
	if cand != nil {
		cand.DoNotContact = true
		cand.VerificationStatus = models.OutreachVerifyDoNotContact
		_, _ = s.repo.UpsertCandidate(ctx, cand)
	}
	cnpj, lead := "", ""
	if acc != nil {
		cnpj, lead = acc.CNPJ14, acc.SourceLeadID
	}
	if suppressions, ok := s.repo.(interface {
		UpsertOutreachRecipientSuppression(context.Context, *models.SuppressedRecipient) error
		CancelSuppressedOutreachRecipient(context.Context, uuid.UUID, string, string, string) (int, error)
	}); ok {
		metadata := map[string]interface{}{}
		if !occurredAt.IsZero() {
			metadata["observed_at"] = occurredAt.UTC().Format(time.RFC3339Nano)
		}
		if stableRef != "" {
			metadata["event_ref"] = stableRef
		}
		if err := suppressions.UpsertOutreachRecipientSuppression(ctx, &models.SuppressedRecipient{
			OrganizationID: orgID, Email: email, Reason: firstNonEmpty(reason, "recipient opt-out"),
			Source: models.DeliverabilityEventUnsubscribe, Metadata: metadata,
		}); err != nil {
			return err
		}
		if _, err := suppressions.CancelSuppressedOutreachRecipient(ctx, orgID, email, models.TouchpointDNC, "opt_out"); err != nil {
			return err
		}
	}
	// Dominant block: drop queued email AND WhatsApp outbound for this recipient.
	phone := ""
	if cand != nil {
		phone = cand.PhoneE164
		if phone == "" {
			phone = cand.Phone
		}
	}
	s.cancelQueuedForRecipient(ctx, orgID, email, phone, "DO_NOT_CONTACT")
	if err := s.observeControlledEmail(ctx, orgID, "opt_out", nil, cand, ControlledEmailContext{
		OccurredAt: occurredAt, ReplyClass: "OPT_OUT", ProviderName: provider,
		StableEventRef: firstNonEmpty(stableRef, "opt-out:"+email),
	}); err != nil {
		return err
	}
	return s.enqueueErr(ctx, orgID, models.OutreachOutcome{
		IdempotencyKey: fmt.Sprintf("dnc:%s:%s", orgID, email),
		SourceLeadID:   lead,
		CNPJ14:         cnpj,
		ContactEmail:   email,
		EventType:      OutcomeDoNotContact,
		OccurredAt:     firstNonZeroTime(nil, occurredAt),
		Payload:        mustJSON(map[string]any{"reason": reason}),
	})
}

func jsonMarshalMap(m map[string]any) ([]byte, error) {
	if m == nil {
		return []byte("{}"), nil
	}
	return marshalJSON(m)
}

// OnClassifiedReply implements advanced.ConfengeReplyHook with subject/body for commercial lexicon.
func (s *service) OnClassifiedReply(ctx context.Context, orgID uuid.UUID, contactEmail, replyClass string, contactID *uuid.UUID, subject, bodyText string, actorID uuid.UUID) error {
	if xerr := s.HandleClassifiedReplyFull(ctx, orgID, actorID, contactEmail, replyClass, contactID, subject, bodyText, nil); xerr != nil {
		return fmt.Errorf("%s", xerr.Message)
	}
	return nil
}
