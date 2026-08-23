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
	_, _ = s.repo.CancelOpenTouchpoints(ctx, orgID, acc.ID, models.TouchpointReplied, "REPLY")
	payload, _ := jsonMarshalMap(meta)
	phone := ""
	if cand != nil {
		phone = cand.PhoneE164
		if phone == "" {
			phone = cand.Phone
		}
	}
	s.cancelQueuedForRecipient(ctx, orgID, email, phone, "reply")
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
	s.observeControlledEmail(ctx, orgID, "reply", correlatedTP, observeCandidate, extra)
	return s.enqueueErr(ctx, orgID, models.OutreachOutcome{
		IdempotencyKey: fmt.Sprintf("replied:%s:%s:%d", orgID, email, time.Now().UTC().Truncate(time.Minute).Unix()),
		SourceLeadID:   acc.SourceLeadID,
		CNPJ14:         acc.CNPJ14,
		ContactEmail:   email,
		EventType:      OutcomeReplied,
		OccurredAt:     time.Now().UTC(),
		Payload:        payload,
	})
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
	if acc != nil && definitiveHard {
		_ = s.repo.SetAccountHumanFlags(ctx, orgID, acc.ID, acc.Blocked, acc.DoNotContact, "bounce", models.OutreachQueueBounced)
		_, _ = s.repo.CancelOpenTouchpoints(ctx, orgID, acc.ID, models.TouchpointBounced, "BOUNCE")
	}
	phone := ""
	if cand != nil {
		phone = cand.PhoneE164
		if phone == "" {
			phone = cand.Phone
		}
	}
	if definitiveHard {
		s.cancelQueuedForRecipient(ctx, orgID, email, phone, "bounce")
	}
	typ := intel.EventUnknownState
	if bounceClass == "HARD" {
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
	s.observeControlledEmail(ctx, orgID, typ, correlatedTP, cand, ControlledEmailContext{
		BounceClass: bounceClass, ProviderName: observation.ProviderName, SMTPStatus: observation.SMTPStatus,
		EnhancedStatus: observation.EnhancedStatus, Diagnostic: observation.Diagnostic,
	})
	idemRef := firstNonEmpty(strings.TrimSpace(observation.OriginalMessageID), email)
	return s.enqueueErr(ctx, orgID, models.OutreachOutcome{
		IdempotencyKey: fmt.Sprintf("bounced:%s:%s:%s", orgID, bounceClass, idemRef),
		SourceLeadID:   lead,
		CNPJ14:         cnpj,
		ContactEmail:   email,
		EventType:      OutcomeBounced,
		OccurredAt:     time.Now().UTC(),
		Payload: mustJSON(map[string]any{
			"reason": observation.Reason, "bounce_class": bounceClass,
			"original_message_id": observation.OriginalMessageID,
			"enhanced_status":     observation.EnhancedStatus,
			"smtp_status":         observation.SMTPStatus,
			"diagnostic":          observation.Diagnostic,
		}),
	})
}

// NoteDNC implements OutcomeSink.
func (s *service) NoteDNC(ctx context.Context, orgID uuid.UUID, contactEmail, reason string) error {
	return s.noteDNC(ctx, orgID, contactEmail, reason, "")
}

func (s *service) noteDNC(ctx context.Context, orgID uuid.UUID, contactEmail, reason, provider string) error {
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
		_ = s.repo.SetAccountHumanFlags(ctx, orgID, acc.ID, true, true, reason, models.OutreachQueueDoNotContact)
		_, _ = s.repo.CancelOpenTouchpoints(ctx, orgID, acc.ID, models.TouchpointDNC, "DNC")
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
	s.observeControlledEmail(ctx, orgID, "opt_out", nil, cand, ControlledEmailContext{ReplyClass: "OPT_OUT", ProviderName: provider})
	return s.enqueueErr(ctx, orgID, models.OutreachOutcome{
		IdempotencyKey: fmt.Sprintf("dnc:%s:%s", orgID, email),
		SourceLeadID:   lead,
		CNPJ14:         cnpj,
		ContactEmail:   email,
		EventType:      OutcomeDoNotContact,
		OccurredAt:     time.Now().UTC(),
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
