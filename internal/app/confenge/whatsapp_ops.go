package confenge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/whatsapp"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// WhatsAppSender is the subset of whatsapp.Service used by confenge ops.
type WhatsAppSender interface {
	Config() whatsapp.Config
	Send(ctx context.Context, state whatsapp.ContactChannelState, intent whatsapp.SendIntent, textReq *whatsapp.SendTextRequest, tmplReq *whatsapp.SendTemplateRequest) (*whatsapp.SendResultExt, error)
	ProcessInbound(ctx context.Context, state *whatsapp.ContactChannelState, ev whatsapp.ChannelEvent) (whatsapp.InboundResult, error)
	ProviderName() string
}

// WhatsAppStateStore persists channel state + messages (optional; nil = in-process only).
type WhatsAppStateStore interface {
	UpsertContactState(ctx context.Context, st *models.WhatsAppContactState) *errx.Error
	GetContactStateByPhone(ctx context.Context, orgID uuid.UUID, phoneE164 string) (*models.WhatsAppContactState, *errx.Error)
	InsertMessage(ctx context.Context, msg *models.WhatsAppMessage) (bool, *errx.Error)
	GetInstanceByName(ctx context.Context, provider, instance string) (*models.WhatsAppInstance, *errx.Error)
	InsertWebhookEvent(ctx context.Context, orgID uuid.UUID, provider, idemKey, eventType, externalMsgID, payloadHash string) (bool, *errx.Error)
}

// WireWhatsApp attaches the transport service and optional state store.
func (s *service) WireWhatsApp(sender WhatsAppSender, store WhatsAppStateStore) {
	s.wa = sender
	s.waStore = store
}

// DecideChannel runs the multichannel orchestrator for an account/candidate.
func (s *service) DecideChannel(ctx context.Context, orgID, accountID uuid.UUID, contactID *uuid.UUID) (*ChannelDecision, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	acc, err := s.repo.GetAccount(ctx, orgID, accountID)
	if err != nil || acc == nil {
		return nil, errx.New(errx.NotFound, "account not found")
	}
	var cand *models.OutreachContactCandidate
	if contactID != nil {
		cand, err = s.repo.GetCandidate(ctx, orgID, *contactID)
		if err != nil || cand == nil {
			return nil, errx.New(errx.NotFound, "contact candidate not found")
		}
	} else {
		list, err := s.repo.ListCandidates(ctx, orgID, accountID)
		if err != nil {
			return nil, errx.New(errx.Internal, "list candidates failed")
		}
		cand = pickRecommendedAny(list)
	}
	st := candidateToChannelState(orgID, cand, acc)
	emailOK := cand != nil && cand.Email != "" && cand.CanEnroll()
	phone := ""
	if cand != nil {
		phone = cand.PhoneE164
		if phone == "" {
			phone = cand.Phone
		}
	}
	waOn := s.cfg.WhatsAppEnabled && s.wa != nil && s.wa.Config().Enabled
	intent := whatsapp.SendIntent{
		Mode:                 whatsapp.ModeFreeText,
		Automated:            true,
		FeatureEnabled:       waOn,
		AutoSendEnabled:      s.wa != nil && s.wa.Config().AutoSendEnabled,
		RequireHumanApproval: s.cfg.RequireHumanApproval,
		Now:                  time.Now().UTC(),
		CrossChannelMin:      time.Duration(s.cfg.CrossChannelHours) * time.Hour,
		ServiceWindow:        24 * time.Hour,
	}
	if s.wa != nil {
		intent.ServiceWindow = s.wa.Config().ServiceWindow
		if intent.CrossChannelMin == 0 {
			intent.CrossChannelMin = s.wa.Config().CrossChannelInterval
		}
	}
	d := OrchestrateChannel(waOn, emailOK, phone, st, intent)
	return &d, nil
}

// GenerateWhatsAppDraft creates a short WhatsApp draft for human review.
// Never auto-sends. Public phone without opt-in is blocked.
func (s *service) GenerateWhatsAppDraft(ctx context.Context, orgID, userID, accountID uuid.UUID, contactID *uuid.UUID) (*models.OutreachDraft, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	if !s.cfg.WhatsAppEnabled {
		return nil, errx.New(errx.BadRequest, "CONFENGE_WHATSAPP_ENABLED is false")
	}
	acc, err := s.repo.GetAccount(ctx, orgID, accountID)
	if err != nil || acc == nil {
		return nil, errx.New(errx.NotFound, "account not found")
	}
	if acc.DoNotContact || acc.Blocked {
		return nil, errx.New(errx.BadRequest, "account is blocked or DO_NOT_CONTACT")
	}
	var cand *models.OutreachContactCandidate
	if contactID != nil {
		cand, err = s.repo.GetCandidate(ctx, orgID, *contactID)
		if err != nil || cand == nil || cand.AccountID != accountID {
			return nil, errx.New(errx.NotFound, "contact candidate not found")
		}
	} else {
		list, err := s.repo.ListCandidates(ctx, orgID, accountID)
		if err != nil {
			return nil, errx.New(errx.Internal, "failed to list contacts")
		}
		cand = pickRecommendedAny(list)
	}
	if cand == nil {
		return nil, errx.New(errx.BadRequest, "no contact candidate")
	}
	phone := cand.PhoneE164
	if phone == "" {
		norm := whatsapp.NormalizePhone(cand.Phone, "BR")
		if norm.Valid {
			phone = norm.E164
		}
	}
	if phone == "" {
		return nil, errx.New(errx.BadRequest, "candidate has no valid phone")
	}

	st := candidateToChannelState(orgID, cand, acc)
	intent := whatsapp.SendIntent{
		Mode:                 whatsapp.ModeFreeText,
		Automated:            true,
		FeatureEnabled:       true,
		AutoSendEnabled:      false, // draft path never auto-sends
		RequireHumanApproval: true,
		Now:                  time.Now().UTC(),
		CrossChannelMin:      time.Duration(s.cfg.CrossChannelHours) * time.Hour,
	}
	// Policy: must not even generate for blocked consent (still allow MANUAL_REVIEW for opted-in).
	d := whatsapp.EvaluateEligibility(st, intent)
	if d.Eligibility == whatsapp.EligBlocked && (st.ConsentStatus == whatsapp.ConsentUnknown || st.ConsentStatus == whatsapp.ConsentNoOptIn || st.ConsentStatus == whatsapp.ConsentOptedOut || st.DoNotContact) {
		return nil, errx.New(errx.BadRequest, "whatsapp blocked by consent: "+d.Reason)
	}

	body := BuildWhatsAppCopy(acc, cand)
	draft := &models.OutreachDraft{
		OrganizationID:     orgID,
		AccountID:          accountID,
		ContactCandidateID: &cand.ID,
		Channel:            models.OutreachChannelWhatsApp,
		RecipientName:      cand.Name,
		RecipientRole:      cand.Role,
		RecipientEmail:     cand.Email,
		RecipientPhoneE164: phone,
		VerificationStatus: cand.VerificationStatus,
		Subject:            "", // WhatsApp has no subject
		BodyText:           SanitizeText(body, 2000),
		ServiceCode:        acc.ServiceCode,
		FactUsed:           SanitizeText(acc.FactToMention, 2000),
		Question:           SanitizeText(acc.QuestionToAsk, 1000),
		CTA:                SanitizeText(acc.CTA, 500),
		Provider:           "template",
		Model:              "whatsapp_short",
		PromptVersion:      PromptVersion + "+wa",
		Status:             models.OutreachDraftNeedsReview,
		RiskClass:          "YELLOW",
		RiskFlags:          []string{"whatsapp_channel", "requires_human_approval"},
	}
	ok := true
	draft.ValidationOK = &ok
	if err := s.repo.UpsertDraft(ctx, draft); err != nil {
		return nil, errx.New(errx.Internal, "failed to save whatsapp draft: "+err.Error())
	}
	if s.audit != nil {
		s.audit.LogAction(ctx, orgID, userID, models.AuditActionCreate, models.AuditEntityOutreachAccount, &accountID, "", "",
			map[string]string{"draft_id": draft.ID.String(), "channel": "WHATSAPP", "status": draft.Status},
			map[string]string{"phone": phone},
		)
	}
	return draft, nil
}

// SendApprovedWhatsApp sends an APPROVED WhatsApp draft via the provider after policy gate.
func (s *service) SendApprovedWhatsApp(ctx context.Context, orgID, userID, draftID uuid.UUID) (*models.OutreachDraft, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	if s.wa == nil || !s.cfg.WhatsAppEnabled {
		return nil, errx.New(errx.ServiceUnavailable, "whatsapp transport not configured")
	}
	d, err := s.repo.GetDraft(ctx, orgID, draftID)
	if err != nil || d == nil {
		return nil, errx.New(errx.NotFound, "draft not found")
	}
	// Explicit WHATSAPP channel only — never send an EMAIL draft even if a phone is set.
	if d.Channel != models.OutreachChannelWhatsApp && d.Channel != "WHATSAPP" {
		return nil, errx.New(errx.BadRequest, "draft is not a WhatsApp draft")
	}
	if d.Status != models.OutreachDraftApproved {
		return nil, errx.New(errx.BadRequest, "draft must be APPROVED before WhatsApp send")
	}
	// Fail-closed: same per-touch invariant as email (draft-only APPROVED is not enough).
	if _, xerr := s.requireTouchTransport(ctx, orgID, draftID); xerr != nil {
		return nil, xerr
	}
	acc, err := s.repo.GetAccount(ctx, orgID, d.AccountID)
	if err != nil || acc == nil {
		return nil, errx.New(errx.NotFound, "account not found")
	}
	var cand *models.OutreachContactCandidate
	if d.ContactCandidateID != nil {
		cand, _ = s.repo.GetCandidate(ctx, orgID, *d.ContactCandidateID)
	}
	st := candidateToChannelState(orgID, cand, acc)
	if d.RecipientPhoneE164 != "" {
		st.PhoneE164 = d.RecipientPhoneE164
	}
	// Load live service window from store when present.
	if s.waStore != nil && st.PhoneE164 != "" {
		if live, xerr := s.waStore.GetContactStateByPhone(ctx, orgID, st.PhoneE164); xerr == nil && live != nil {
			st.LastInboundAt = live.LastInboundAt
			st.ServiceWindowUntil = live.ServiceWindowUntil
			st.OptOutAt = live.OptOutAt
			if live.DoNotContact {
				st.DoNotContact = true
			}
			if live.ConsentStatus == whatsapp.ConsentOptedOut || live.ConsentStatus == whatsapp.ConsentDoNotContact {
				st.ConsentStatus = live.ConsentStatus
			}
			if live.LastEmailOutboundAt != nil {
				st.LastEmailOutboundAt = live.LastEmailOutboundAt
			}
			if live.LastWhatsAppOutboundAt != nil {
				st.LastWhatsAppOutboundAt = live.LastWhatsAppOutboundAt
			}
		}
	}

	intent := whatsapp.SendIntent{
		Mode:                 whatsapp.ModeFreeText,
		Automated:            false, // human approved send
		FeatureEnabled:       true,
		AutoSendEnabled:      true, // human already approved; allow transport
		RequireHumanApproval: false,
		Now:                  time.Now().UTC(),
		CrossChannelMin:      time.Duration(s.cfg.CrossChannelHours) * time.Hour,
		ServiceWindow:        s.wa.Config().ServiceWindow,
	}
	// Outside service window without template: block free text.
	elig := whatsapp.EvaluateEligibility(st, intent)
	if !elig.Allowed {
		return nil, errx.New(errx.BadRequest, "whatsapp send blocked: "+elig.Reason)
	}

	instance := s.wa.Config().EvolutionInstance
	res, err := s.wa.Send(ctx, st, intent, &whatsapp.SendTextRequest{
		Instance:       instance,
		ToE164:         st.PhoneE164,
		Body:           d.BodyText,
		IdempotencyKey: "wa-draft:" + draftID.String(),
		OrganizationID: orgID,
	}, nil)
	if err != nil {
		return nil, errx.New(errx.Internal, "whatsapp send failed: "+err.Error())
	}
	if res == nil || res.Skipped {
		reason := "skipped"
		if res != nil {
			reason = res.Decision.Reason
		}
		return nil, errx.New(errx.BadRequest, "whatsapp send skipped: "+reason)
	}

	now := time.Now().UTC()
	d.Status = models.OutreachDraftSent
	if err := s.repo.UpsertDraft(ctx, d); err != nil {
		return nil, errx.New(errx.Internal, "failed to mark draft sent")
	}
	_ = s.repo.SetAccountHumanFlags(ctx, orgID, d.AccountID, acc.Blocked, acc.DoNotContact, acc.BlockReason, models.OutreachQueueSent)

	// Persist outbound message + state.
	if s.waStore != nil {
		msg := &models.WhatsAppMessage{
			OrganizationID:    orgID,
			ThreadKey:         st.PhoneE164,
			Direction:         "outbound",
			Channel:           whatsapp.ChannelWhatsApp,
			Provider:          s.wa.ProviderName(),
			ProviderMessageID: "",
			IdempotencyKey:    "wa-draft-send:" + draftID.String(),
			BodyText:          d.BodyText,
			Status:            "sent",
			DraftID:           &d.ID,
			CampaignID:        d.CampaignID,
			ReviewedBy:        &userID,
			ApprovedAt:        d.ApprovedAt,
			SentAt:            &now,
			OccurredAt:        now,
		}
		if res.Result != nil {
			msg.ProviderMessageID = res.Result.ProviderMessageID
		}
		if cand != nil && cand.WarmblyContactID != nil {
			msg.ContactID = cand.WarmblyContactID
		}
		_, _ = s.waStore.InsertMessage(ctx, msg)

		persist := models.WhatsAppContactState{
			OrganizationID:         orgID,
			PhoneE164:              st.PhoneE164,
			PhoneRaw:               st.PhoneE164,
			PhoneValid:             true,
			ConsentStatus:          st.ConsentStatus,
			ConsentSource:          st.ConsentSource,
			ConsentAt:              st.ConsentAt,
			ConsentProvenanceOK:    st.ConsentProvenanceOK,
			LastWhatsAppOutboundAt: &now,
			ContactID:              nil,
		}
		if cand != nil && cand.WarmblyContactID != nil {
			persist.ContactID = cand.WarmblyContactID
		}
		_ = s.waStore.UpsertContactState(ctx, &persist)
	}

	_ = s.EnqueueOutcome(ctx, orgID, models.OutreachOutcome{
		IdempotencyKey: fmt.Sprintf("wa-sent:%s:%s", orgID, draftID),
		SourceLeadID:   acc.SourceLeadID,
		CNPJ14:         acc.CNPJ14,
		ContactEmail:   d.RecipientEmail,
		EventType:      OutcomeContacted,
		OccurredAt:     now,
		Payload:        mustJSON(map[string]any{"channel": "WHATSAPP", "draft_id": draftID.String()}),
	})

	if s.audit != nil {
		s.audit.LogAction(ctx, orgID, userID, models.AuditActionUpdate, models.AuditEntityOutreachAccount, &d.AccountID, "", "",
			map[string]string{"draft_id": d.ID.String(), "status": d.Status, "channel": "WHATSAPP"},
			nil,
		)
	}
	return d, nil
}

// HandleWhatsAppInbound is the production path for Evolution-normalized inbound events.
// It stops follow-ups, records CRM/outcomes, and applies sticky opt-out.
func (s *service) HandleWhatsAppInbound(ctx context.Context, orgID uuid.UUID, ev whatsapp.ChannelEvent) (whatsapp.InboundResult, error) {
	empty := whatsapp.InboundResult{Event: ev}
	if s == nil || !s.cfg.Enabled {
		return empty, nil
	}
	if s.wa == nil {
		return empty, fmt.Errorf("whatsapp service not wired")
	}

	phone := ev.FromE164
	if phone == "" {
		phone = ev.ToE164
	}
	st := whatsapp.ContactChannelState{
		OrganizationID: orgID,
		PhoneE164:      phone,
		ConsentStatus:  whatsapp.ConsentUnknown,
	}
	// Resolve candidate by phone or e164.
	var cand *models.OutreachContactCandidate
	var acc *models.OutreachAccount
	if phone != "" {
		c, a, err := s.repo.FindCandidateByPhone(ctx, orgID, phone)
		if err == nil {
			cand, acc = c, a
		}
	}
	if cand != nil {
		st = candidateToChannelState(orgID, cand, acc)
	}

	// Overlay persisted channel state.
	if s.waStore != nil && phone != "" {
		if live, xerr := s.waStore.GetContactStateByPhone(ctx, orgID, phone); xerr == nil && live != nil {
			if live.ConsentStatus != "" {
				st.ConsentStatus = live.ConsentStatus
			}
			st.ConsentProvenanceOK = live.ConsentProvenanceOK
			st.OptOutAt = live.OptOutAt
			st.DoNotContact = live.DoNotContact || st.DoNotContact
			st.LastInboundAt = live.LastInboundAt
			st.ServiceWindowUntil = live.ServiceWindowUntil
			if live.ContactID != nil {
				st.ContactID = *live.ContactID
			}
		}
	}

	res, err := s.wa.ProcessInbound(ctx, &st, ev)
	if err != nil {
		return res, err
	}
	if res.Duplicate || res.Ignored {
		return res, nil
	}

	// Persist message + state.
	if s.waStore != nil && ev.EventType == whatsapp.EventMessageReceived {
		msg := &models.WhatsAppMessage{
			OrganizationID:    orgID,
			ThreadKey:         phone,
			Direction:         "inbound",
			Channel:           whatsapp.ChannelWhatsApp,
			Provider:          whatsapp.ProviderEvolution,
			ProviderMessageID: ev.ExternalMessageID,
			IdempotencyKey:    ev.IdempotencyKey(),
			BodyText:          ev.Content.Text,
			Status:            "received",
			OccurredAt:        ev.OccurredAt,
		}
		if cand != nil && cand.WarmblyContactID != nil {
			msg.ContactID = cand.WarmblyContactID
		}
		_, _ = s.waStore.InsertMessage(ctx, msg)

		persist := models.WhatsAppContactState{
			OrganizationID:      orgID,
			PhoneE164:           st.PhoneE164,
			PhoneValid:          true,
			ConsentStatus:       st.ConsentStatus,
			ConsentSource:       st.ConsentSource,
			ConsentAt:           st.ConsentAt,
			ConsentProvenanceOK: st.ConsentProvenanceOK,
			LastInboundAt:       st.LastInboundAt,
			ServiceWindowUntil:  st.ServiceWindowUntil,
			OptOutAt:            st.OptOutAt,
			DoNotContact:        st.DoNotContact,
		}
		if cand != nil && cand.WarmblyContactID != nil {
			persist.ContactID = cand.WarmblyContactID
		}
		_ = s.waStore.UpsertContactState(ctx, &persist)
	}

	// Stop follow-ups / mark replied on confenge account + drafts.
	if res.StopSequences && acc != nil {
		term, stop := models.TouchpointReplied, "REPLY"
		if res.OptOut.Matched && res.OptOut.Confident {
			term, stop = models.TouchpointDNC, "DNC"
		}
		_, _ = s.repo.CancelOpenTouchpoints(ctx, orgID, acc.ID, term, stop)
		_ = s.repo.SetAccountHumanFlags(ctx, orgID, acc.ID, acc.Blocked, acc.DoNotContact || st.DoNotContact, acc.BlockReason, models.OutreachQueueReplied)
		// Mark WhatsApp drafts replied when possible.
		if drafts, err := s.repo.ListDrafts(ctx, orgID, models.OutreachDraftEnrolled, 50, 0); err == nil {
			for i := range drafts {
				dd := drafts[i]
				if dd.AccountID == acc.ID && (dd.Channel == models.OutreachChannelWhatsApp || dd.Channel == "WHATSAPP" || dd.RecipientPhoneE164 != "") {
					dd.Status = models.OutreachDraftReplied
					_ = s.repo.UpsertDraft(ctx, &dd)
				}
			}
		}
		for _, stt := range []string{models.OutreachDraftSent, models.OutreachDraftApproved} {
			if drafts, err := s.repo.ListDrafts(ctx, orgID, stt, 50, 0); err == nil {
				for i := range drafts {
					dd := drafts[i]
					if dd.AccountID == acc.ID {
						dd.Status = models.OutreachDraftReplied
						_ = s.repo.UpsertDraft(ctx, &dd)
					}
				}
			}
		}
	}

	// Outcomes + DNC.
	email := ""
	if cand != nil {
		email = cand.Email
	}
	if res.OptOut.Matched && res.OptOut.Confident {
		if email != "" {
			_ = s.NoteDNC(ctx, orgID, email, "whatsapp_opt_out:"+res.OptOut.Phrase)
		} else if acc != nil {
			_ = s.EnqueueOutcome(ctx, orgID, models.OutreachOutcome{
				IdempotencyKey: fmt.Sprintf("wa-dnc:%s:%s", orgID, phone),
				SourceLeadID:   acc.SourceLeadID,
				CNPJ14:         acc.CNPJ14,
				EventType:      OutcomeDoNotContact,
				OccurredAt:     ev.OccurredAt,
				Payload:        mustJSON(map[string]any{"channel": "WHATSAPP", "phrase": res.OptOut.Phrase, "phone": phone}),
			})
			_ = s.repo.SetAccountHumanFlags(ctx, orgID, acc.ID, true, true, "whatsapp_opt_out", models.OutreachQueueDoNotContact)
		}
		if cand != nil {
			cand.DoNotContact = true
			cand.WhatsAppConsentStatus = whatsapp.ConsentOptedOut
			now := ev.OccurredAt
			cand.WhatsAppConsentAt = &now
			_, _ = s.repo.UpsertCandidate(ctx, cand)
		}
	} else if res.StopSequences && email != "" {
		_ = s.NoteReply(ctx, orgID, email, map[string]any{
			"channel": "WHATSAPP",
			"text":    truncateRunes(ev.Content.Text, 500),
		})
		// Feed CRM classification path (same taxonomy as email).
		_ = s.HandleClassifiedReply(ctx, orgID, uuid.Nil, email, "unknown", nil)
	} else if res.StopSequences && acc != nil {
		_ = s.EnqueueOutcome(ctx, orgID, models.OutreachOutcome{
			IdempotencyKey: fmt.Sprintf("wa-replied:%s:%s:%s", orgID, phone, ev.ExternalMessageID),
			SourceLeadID:   acc.SourceLeadID,
			CNPJ14:         acc.CNPJ14,
			EventType:      OutcomeReplied,
			OccurredAt:     ev.OccurredAt,
			Payload:        mustJSON(map[string]any{"channel": "WHATSAPP", "phone": phone}),
		})
	}

	return res, nil
}

func candidateToChannelState(orgID uuid.UUID, cand *models.OutreachContactCandidate, acc *models.OutreachAccount) whatsapp.ContactChannelState {
	st := whatsapp.ContactChannelState{
		OrganizationID: orgID,
		ConsentStatus:  whatsapp.ConsentUnknown,
	}
	if acc != nil && acc.DoNotContact {
		st.DoNotContact = true
		st.ConsentStatus = whatsapp.ConsentDoNotContact
	}
	if cand == nil {
		return st
	}
	if cand.WarmblyContactID != nil {
		st.ContactID = *cand.WarmblyContactID
	}
	st.PhoneE164 = cand.PhoneE164
	if st.PhoneE164 == "" && cand.Phone != "" {
		n := whatsapp.NormalizePhone(cand.Phone, "BR")
		if n.Valid {
			st.PhoneE164 = n.E164
		}
	}
	st.PhoneSource = cand.PhoneSource
	st.PhoneSourceURL = cand.PhoneSourceURL
	if cand.WhatsAppConsentStatus != "" {
		st.ConsentStatus = cand.WhatsAppConsentStatus
	}
	st.ConsentSource = cand.WhatsAppConsentSource
	st.ConsentAt = cand.WhatsAppConsentAt
	st.ConsentProvenanceOK = cand.WhatsAppConsentProvenanceOK
	if cand.DoNotContact {
		st.DoNotContact = true
		st.ConsentStatus = whatsapp.ConsentDoNotContact
	}
	return st
}

// pickRecommendedAny prefers enrollable email contacts but falls back to any non-DNC with phone.
func pickRecommendedAny(list []models.OutreachContactCandidate) *models.OutreachContactCandidate {
	if c := pickRecommended(list); c != nil {
		return c
	}
	var fallback *models.OutreachContactCandidate
	for i := range list {
		c := &list[i]
		if c.DoNotContact || c.Bounced || c.Blocked {
			continue
		}
		if c.Phone == "" && c.PhoneE164 == "" {
			continue
		}
		if c.Recommended {
			return c
		}
		if fallback == nil {
			fallback = c
		}
	}
	return fallback
}

// Ensure compile-time use of strings in this package for truncate helpers.
var _ = strings.TrimSpace
