package confenge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

func (s *service) PlanAccountCadence(ctx context.Context, orgID, userID, accountID uuid.UUID, contactID *uuid.UUID, channel string) ([]models.OutreachTouchpoint, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	acc, err := s.repo.GetAccount(ctx, orgID, accountID)
	if err != nil || acc == nil {
		return nil, errx.New(errx.NotFound, "account not found")
	}
	if acc.DoNotContact || acc.Blocked {
		return nil, errx.New(errx.BadRequest, "account is blocked or DO_NOT_CONTACT")
	}
	existing, err := s.repo.ListTouchpoints(ctx, orgID, accountID, "", 50, 0)
	if err != nil {
		return nil, errx.New(errx.Internal, "list touchpoints failed")
	}
	for _, t := range existing {
		if t.State == models.TouchpointSent || t.State == models.TouchpointDNC {
			return nil, errx.New(errx.BadRequest, "cannot replan cadence: account has SENT or DNC touchpoints")
		}
		if IsOpen(t.State) {
			return existing, nil
		}
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
			return nil, errx.New(errx.Internal, "list candidates failed")
		}
		cand = pickRecommendedAny(list)
	}
	ch := strings.ToUpper(strings.TrimSpace(channel))
	if ch == "" {
		ch = models.OutreachChannelEmail
	}
	if ch != models.OutreachChannelEmail && ch != models.OutreachChannelWhatsApp {
		return nil, errx.New(errx.BadRequest, "channel must be EMAIL or WHATSAPP")
	}
	recipient := ""
	if cand != nil {
		if ch == models.OutreachChannelWhatsApp {
			recipient = cand.PhoneE164
			if recipient == "" {
				recipient = cand.Phone
			}
		} else {
			recipient = cand.Email
		}
	}
	now := time.Now().UTC()
	policy := CadencePolicyV1()
	out := make([]models.OutreachTouchpoint, 0, len(policy))
	var prevID *uuid.UUID
	due := now
	for i, step := range policy {
		if i > 0 {
			due = due.Add(step.DelayAfterPrev)
		}
		state := models.TouchpointPlanned
		if i == 0 {
			state = models.TouchpointDue
		}
		idem := fmt.Sprintf("tp:%s:%s:%d:%s", orgID, accountID, step.Ordinal, step.CadenceStep)
		tp := &models.OutreachTouchpoint{
			OrganizationID: orgID, AccountID: accountID, Ordinal: step.Ordinal,
			CadenceStep: step.CadenceStep, Channel: ch, Purpose: step.Purpose,
			DueAt: due, State: state, Recipient: recipient, PreviousTouchpointID: prevID,
			IdempotencyKey: idem, PolicyVersion: models.CadencePolicyVersionV1,
			ServiceCode: acc.ServiceCode, FactUsed: acc.FactToMention, EvidenceIDs: acc.MomentEvidenceIDs,
		}
		if cand != nil {
			tp.ContactCandidateID = &cand.ID
		}
		if err := s.repo.InsertTouchpoint(ctx, tp); err != nil {
			return nil, errx.New(errx.Internal, "insert touchpoint: "+err.Error())
		}
		id := tp.ID
		prevID = &id
		out = append(out, *tp)
	}
	_ = userID
	return out, nil
}

func (s *service) ListReviewTouchpoints(ctx context.Context, orgID uuid.UUID, limit, offset int) ([]models.OutreachTouchpoint, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	// Promote PLANNED touches whose due_at has arrived (spaced follow-ups).
	_, _ = s.PromoteDueTouchpoints(ctx, orgID)
	list, err := s.repo.ListReviewTouchpoints(ctx, orgID, limit, offset)
	if err != nil {
		return nil, errx.New(errx.Internal, "list review touchpoints failed")
	}
	if list == nil {
		list = []models.OutreachTouchpoint{}
	}
	for i := range list {
		if acc, err := s.repo.GetAccount(ctx, orgID, list[i].AccountID); err == nil && acc != nil {
			list[i].Account = acc
		}
	}
	return list, nil
}

func (s *service) GetTouchpoint(ctx context.Context, orgID, id uuid.UUID) (*models.OutreachTouchpoint, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	tp, err := s.repo.GetTouchpoint(ctx, orgID, id)
	if err != nil {
		return nil, errx.New(errx.Internal, "load touchpoint failed")
	}
	if tp == nil {
		return nil, errx.New(errx.NotFound, "touchpoint not found")
	}
	return tp, nil
}

func (s *service) ListAccountTouchpoints(ctx context.Context, orgID, accountID uuid.UUID) ([]models.OutreachTouchpoint, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	list, err := s.repo.ListTouchpoints(ctx, orgID, accountID, "", 100, 0)
	if err != nil {
		return nil, errx.New(errx.Internal, "list timeline failed")
	}
	if list == nil {
		list = []models.OutreachTouchpoint{}
	}
	return list, nil
}

func (s *service) GenerateTouchpointDraft(ctx context.Context, orgID, userID, touchpointID uuid.UUID) (*models.OutreachTouchpoint, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	tp, err := s.repo.GetTouchpoint(ctx, orgID, touchpointID)
	if err != nil || tp == nil {
		return nil, errx.New(errx.NotFound, "touchpoint not found")
	}
	switch tp.State {
	case models.TouchpointDue, models.TouchpointDrafted, models.TouchpointNeedsReview, models.TouchpointPlanned:
	default:
		return nil, errx.New(errx.BadRequest, "cannot generate for state "+tp.State)
	}
	if tp.Ordinal > 1 {
		priors, _ := s.repo.ListTouchpoints(ctx, orgID, tp.AccountID, "", 50, 0)
		for _, p := range priors {
			if p.Ordinal < tp.Ordinal && IsOpen(p.State) && p.State != models.TouchpointQueued {
				return nil, errx.New(errx.BadRequest, "prior touchpoint still awaits human decision")
			}
		}
	}
	acc, err := s.repo.GetAccount(ctx, orgID, tp.AccountID)
	if err != nil || acc == nil {
		return nil, errx.New(errx.NotFound, "account not found")
	}
	if acc.DoNotContact || acc.Blocked {
		return nil, errx.New(errx.BadRequest, "account blocked or DNC")
	}
	var cand *models.OutreachContactCandidate
	if tp.ContactCandidateID != nil {
		cand, _ = s.repo.GetCandidate(ctx, orgID, *tp.ContactCandidateID)
	}
	evidence, _ := s.repo.ListEvidence(ctx, orgID, tp.AccountID)
	priors, _ := s.repo.ListTouchpoints(ctx, orgID, tp.AccountID, models.TouchpointSent, 20, 0)
	subject, body := jitCompose(tp, acc, cand, evidence, priors)
	if tp.Channel == "" {
		tp.Channel = models.OutreachChannelEmail
	}
	if tp.Recipient == "" && cand != nil {
		if tp.Channel == models.OutreachChannelWhatsApp {
			tp.Recipient = cand.PhoneE164
			if tp.Recipient == "" {
				tp.Recipient = cand.Phone
			}
		} else {
			tp.Recipient = cand.Email
		}
	}
	draft := &models.OutreachDraft{
		OrganizationID: orgID, AccountID: tp.AccountID, ContactCandidateID: tp.ContactCandidateID,
		Channel: tp.Channel, Subject: subject, BodyText: body, ServiceCode: acc.ServiceCode,
		FactUsed: SanitizeText(acc.FactToMention, 2000), EvidenceIDs: acc.MomentEvidenceIDs,
		Provider: "template", Model: "jit_" + tp.Purpose, PromptVersion: PromptVersion + "+touch",
		Status: models.OutreachDraftNeedsReview, RiskClass: "YELLOW", RiskFlags: []string{"per_touch_approval", "jit"},
	}
	if cand != nil {
		draft.RecipientName, draft.RecipientRole, draft.RecipientEmail = cand.Name, cand.Role, cand.Email
		draft.RecipientPhoneE164, draft.VerificationStatus = cand.PhoneE164, cand.VerificationStatus
	}
	if err := s.repo.UpsertDraft(ctx, draft); err != nil {
		return nil, errx.New(errx.Internal, "save draft: "+err.Error())
	}
	ApplyContentMutation(tp, tp.Channel, tp.Recipient, draft.Subject, draft.BodyText)
	tp.DraftID = &draft.ID
	tp.State = models.TouchpointNeedsReview
	tp.ServiceCode, tp.FactUsed, tp.EvidenceIDs = acc.ServiceCode, draft.FactUsed, draft.EvidenceIDs
	if err := s.repo.UpdateTouchpoint(ctx, tp); err != nil {
		return nil, errx.New(errx.Internal, "update touchpoint: "+err.Error())
	}
	_ = s.repo.SetAccountHumanFlags(ctx, orgID, tp.AccountID, acc.Blocked, acc.DoNotContact, acc.BlockReason, models.OutreachQueueNeedsReview)
	_ = userID
	return tp, nil
}

func jitCompose(tp *models.OutreachTouchpoint, acc *models.OutreachAccount, cand *models.OutreachContactCandidate, evidence []models.OutreachEvidence, sent []models.OutreachTouchpoint) (subject, body string) {
	company := firstNonEmpty(acc.NomeFantasia, acc.RazaoSocial)
	fact := strings.TrimSpace(acc.FactToMention)
	if fact == "" && len(evidence) > 0 {
		fact = firstNonEmpty(evidence[0].Synthesis, evidence[0].Excerpt)
	}
	name := ""
	if cand != nil {
		name = strings.TrimSpace(strings.Split(cand.Name, " ")[0])
	}
	greeting := "Ola"
	if name != "" {
		greeting = "Ola " + name
	}
	if tp.Channel == models.OutreachChannelWhatsApp {
		body = BuildWhatsAppCopy(acc, cand)
		if len(sent) > 0 {
			body = greeting + ", retomo o contato sobre " + company + "."
		}
		return "", SanitizeText(body, 2000)
	}
	switch tp.Purpose {
	case models.TouchpointPurposeFollowUp:
		subject = "Re: " + company
		body = greeting + ",\n\nRetomo com uma pergunta objetiva: quem na equipe acompanha " + firstNonEmpty(acc.ServiceName, "este tema") + "?\n\n"
		if fact != "" {
			body += "Contexto: " + fact + "\n\n"
		}
		body += "Se fizer sentido, respondo em poucos minutos.\n\nAbraco,\nCONFENGE"
	case models.TouchpointPurposeClose:
		subject = "Re: " + company
		body = greeting + ",\n\nEncerro por aqui para nao ocupar sua caixa. Se fizer sentido no futuro, e so responder este fio.\n\nAbraco."
	default:
		out := TemplateDraft(acc, cand)
		subject, body = out.Subject, out.BodyText
	}
	return SanitizeText(subject, 200), SanitizeText(body, 8000)
}

func (s *service) EditTouchpoint(ctx context.Context, orgID, userID, id uuid.UUID, subject, body, recipient, channel *string) (*models.OutreachTouchpoint, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	tp, err := s.repo.GetTouchpoint(ctx, orgID, id)
	if err != nil || tp == nil {
		return nil, errx.New(errx.NotFound, "touchpoint not found")
	}
	if models.TouchpointTerminalStates[tp.State] || tp.State == models.TouchpointQueued || tp.State == models.TouchpointSent {
		return nil, errx.New(errx.BadRequest, "cannot edit touchpoint in state "+tp.State)
	}
	ch, rec, sub, bod := tp.Channel, tp.Recipient, tp.Subject, tp.BodyText
	if channel != nil && *channel != "" {
		ch = strings.ToUpper(strings.TrimSpace(*channel))
	}
	if recipient != nil {
		rec = strings.TrimSpace(*recipient)
	}
	if subject != nil {
		sub = *subject
	}
	if body != nil {
		bod = *body
	}
	ApplyContentMutation(tp, ch, rec, sub, bod)
	if err := s.repo.UpdateTouchpoint(ctx, tp); err != nil {
		return nil, errx.New(errx.Internal, "update failed")
	}
	if tp.DraftID != nil {
		if d, _ := s.repo.GetDraft(ctx, orgID, *tp.DraftID); d != nil {
			d.Subject, d.BodyText = tp.Subject, tp.BodyText
			d.HumanEdited, d.Status = true, models.OutreachDraftNeedsReview
			d.ApprovedBy, d.ApprovedAt = nil, nil
			_ = s.repo.UpsertDraft(ctx, d)
		}
	}
	_ = userID
	return tp, nil
}

func (s *service) ApproveTouchpoint(ctx context.Context, orgID, userID, id uuid.UUID) (*models.OutreachTouchpoint, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	if userID == uuid.Nil {
		return nil, errx.New(errx.Unauthorized, "human user required to approve")
	}
	tp, err := s.repo.GetTouchpoint(ctx, orgID, id)
	if err != nil || tp == nil {
		return nil, errx.New(errx.NotFound, "touchpoint not found")
	}
	acc, _ := s.repo.GetAccount(ctx, orgID, tp.AccountID)
	if acc != nil && (acc.DoNotContact || acc.Blocked) {
		return nil, errx.New(errx.BadRequest, "account blocked or DNC")
	}
	RecomputeContentHash(tp)
	if err := ApplyHumanApproval(tp, userID, time.Now().UTC()); err != nil {
		return nil, errx.New(errx.BadRequest, err.Error())
	}
	if err := s.repo.UpdateTouchpoint(ctx, tp); err != nil {
		return nil, errx.New(errx.Internal, "approve persist failed")
	}
	if acc != nil {
		_ = s.repo.SetAccountHumanFlags(ctx, orgID, tp.AccountID, acc.Blocked, acc.DoNotContact, acc.BlockReason, models.OutreachQueueApproved)
	}
	return tp, nil
}

func (s *service) RejectOrSkipTouchpoint(ctx context.Context, orgID, userID, id uuid.UUID, action string) (*models.OutreachTouchpoint, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	tp, err := s.repo.GetTouchpoint(ctx, orgID, id)
	if err != nil || tp == nil {
		return nil, errx.New(errx.NotFound, "touchpoint not found")
	}
	if models.TouchpointTerminalStates[tp.State] {
		return tp, nil
	}
	state, stop := TerminalStopReason(action)
	if action == "skip" || action == "SKIPPED" {
		state, stop = models.TouchpointSkipped, "SKIPPED"
	} else if action == "reject" || action == "REJECTED" {
		state, stop = models.TouchpointRejected, "REJECTED"
	}
	tp.State, tp.StopReason = state, stop
	ClearApproval(tp)
	if err := s.repo.UpdateTouchpoint(ctx, tp); err != nil {
		return nil, errx.New(errx.Internal, "update failed")
	}
	if state == models.TouchpointSkipped {
		_ = s.releaseNextTouch(ctx, orgID, tp)
	}
	_ = userID
	return tp, nil
}

func (s *service) releaseNextTouch(ctx context.Context, orgID uuid.UUID, prior *models.OutreachTouchpoint) error {
	if !NextMayRelease(prior) {
		return nil
	}
	list, err := s.repo.ListTouchpoints(ctx, orgID, prior.AccountID, models.TouchpointPlanned, 20, 0)
	if err != nil {
		return err
	}
	for i := range list {
		if list[i].Ordinal == prior.Ordinal+1 {
			next := list[i]
			if next.DueAt.After(time.Now().UTC()) {
				return nil
			}
			next.State = models.TouchpointDue
			return s.repo.UpdateTouchpoint(ctx, &next)
		}
	}
	return nil
}

func (s *service) QueueTouchpoint(ctx context.Context, orgID, userID, id uuid.UUID) (*models.OutreachTouchpoint, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	tp, err := s.repo.GetTouchpoint(ctx, orgID, id)
	if err != nil || tp == nil {
		return nil, errx.New(errx.NotFound, "touchpoint not found")
	}
	if err := CanTransport(tp); err != nil {
		return nil, errx.New(errx.BadRequest, "send blocked: "+err.Error())
	}
	queued, err := s.repo.CASQueueTouchpoint(ctx, orgID, id, tp.ContentHash)
	if err != nil {
		return nil, errx.New(errx.Internal, "queue cas failed: "+err.Error())
	}
	if queued == nil {
		tp2, _ := s.repo.GetTouchpoint(ctx, orgID, id)
		if tp2 != nil && tp2.State == models.TouchpointQueued {
			return tp2, nil
		}
		return nil, errx.New(errx.Conflict, "touchpoint not queueable (state or hash mismatch)")
	}
	tp = queued
	var dispatchErr *errx.Error
	if tp.Channel == models.OutreachChannelWhatsApp {
		dispatchErr = s.dispatchWhatsAppTouch(ctx, orgID, userID, tp)
	} else {
		dispatchErr = s.dispatchEmailTouch(ctx, orgID, userID, tp)
	}
	if dispatchErr != nil {
		tp.State = models.TouchpointFailed
		tp.StopReason = dispatchErr.Message
		_ = s.repo.UpdateTouchpoint(ctx, tp)
		return nil, dispatchErr
	}
	return tp, nil
}

func (s *service) dispatchEmailTouch(ctx context.Context, orgID, userID uuid.UUID, tp *models.OutreachTouchpoint) *errx.Error {
	if tp.DraftID == nil {
		return errx.New(errx.BadRequest, "touchpoint has no draft")
	}
	d, err := s.repo.GetDraft(ctx, orgID, *tp.DraftID)
	if err != nil || d == nil {
		return errx.New(errx.NotFound, "draft not found")
	}
	d.Subject, d.BodyText = tp.Subject, tp.BodyText
	d.Status = models.OutreachDraftApproved
	d.ApprovedBy, d.ApprovedAt = tp.ApprovedBy, tp.ApprovedAt
	if err := s.repo.UpsertDraft(ctx, d); err != nil {
		return errx.New(errx.Internal, "sync draft failed")
	}
	enrolled, xerr := s.EnrollDraft(ctx, orgID, userID, d.ID)
	if xerr != nil {
		if enrolled == nil || enrolled.Status != models.OutreachDraftEnrolled {
			return xerr
		}
	}
	now := time.Now().UTC()
	if err := TransitionToSent(tp, now, "email-enrolled:"+d.ID.String()); err != nil {
		tp.State = models.TouchpointSent
		tp.SentAt = &now
		tp.ProviderMessageID = "email-enrolled:" + d.ID.String()
	}
	if err := s.repo.UpdateTouchpoint(ctx, tp); err != nil {
		return errx.New(errx.Internal, "mark sent failed")
	}
	_ = s.releaseNextTouch(ctx, orgID, tp)
	return nil
}

func (s *service) dispatchWhatsAppTouch(ctx context.Context, orgID, userID uuid.UUID, tp *models.OutreachTouchpoint) *errx.Error {
	if tp.DraftID == nil {
		return errx.New(errx.BadRequest, "touchpoint has no draft")
	}
	d, err := s.repo.GetDraft(ctx, orgID, *tp.DraftID)
	if err != nil || d == nil {
		return errx.New(errx.NotFound, "draft not found")
	}
	d.BodyText, d.RecipientPhoneE164 = tp.BodyText, tp.Recipient
	d.Status = models.OutreachDraftApproved
	d.ApprovedBy, d.ApprovedAt = tp.ApprovedBy, tp.ApprovedAt
	d.Channel = models.OutreachChannelWhatsApp
	if err := s.repo.UpsertDraft(ctx, d); err != nil {
		return errx.New(errx.Internal, "sync draft failed")
	}
	sent, xerr := s.SendApprovedWhatsApp(ctx, orgID, userID, d.ID)
	if xerr != nil {
		return xerr
	}
	now := time.Now().UTC()
	tp.State = models.TouchpointSent
	tp.SentAt = &now
	if sent != nil {
		tp.ProviderMessageID = "wa-draft:" + sent.ID.String()
	}
	if err := s.repo.UpdateTouchpoint(ctx, tp); err != nil {
		return errx.New(errx.Internal, "mark sent failed")
	}
	_ = s.releaseNextTouch(ctx, orgID, tp)
	return nil
}

func (s *service) CancelAccountTouchpoints(ctx context.Context, orgID, userID, accountID uuid.UUID, reason string) (int, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return 0, xerr
	}
	state, stop := TerminalStopReason(reason)
	n, err := s.repo.CancelOpenTouchpoints(ctx, orgID, accountID, state, stop)
	if err != nil {
		return 0, errx.New(errx.Internal, "cancel failed: "+err.Error())
	}
	if acc, _ := s.repo.GetAccount(ctx, orgID, accountID); acc != nil {
		qs, dnc, blocked := models.OutreachQueueBlocked, acc.DoNotContact, acc.Blocked
		if state == models.TouchpointDNC {
			qs, dnc, blocked = models.OutreachQueueDoNotContact, true, true
		} else if state == models.TouchpointReplied {
			qs = models.OutreachQueueReplied
		} else if state == models.TouchpointBounced {
			qs = models.OutreachQueueBounced
		}
		_ = s.repo.SetAccountHumanFlags(ctx, orgID, accountID, blocked, dnc, stop, qs)
	}
	_ = userID
	return n, nil
}

// AssertTransportAllowed is the shared gate for email and WhatsApp transport.
func AssertTransportAllowed(tp *models.OutreachTouchpoint) *errx.Error {
	if err := CanTransport(tp); err != nil {
		return errx.New(errx.BadRequest, "CONFENGE transport blocked: "+err.Error())
	}
	return nil
}

// requireTouchTransport fails closed: every CONFENGE outbound must be backed by a
// touchpoint whose approved_content_hash matches content_hash and has human approved_by.
// Draft-only APPROVED status is never enough.
func (s *service) requireTouchTransport(ctx context.Context, orgID, draftID uuid.UUID) (*models.OutreachTouchpoint, *errx.Error) {
	tp, err := s.repo.GetTouchpointByDraft(ctx, orgID, draftID)
	if err != nil {
		return nil, errx.New(errx.Internal, "load touchpoint failed")
	}
	if tp == nil {
		return nil, errx.New(errx.BadRequest, "CONFENGE transport requires an approved touchpoint; use the per-touch review queue (draft-only approval cannot send)")
	}
	if xerr := AssertTransportAllowed(tp); xerr != nil {
		return nil, xerr
	}
	return tp, nil
}

// PromoteDueTouchpoints moves PLANNED touches with due_at <= now to DUE so they
// enter the human review queue after prior SENT/SKIP release.
func (s *service) PromoteDueTouchpoints(ctx context.Context, orgID uuid.UUID) (int, error) {
	n, err := s.repo.PromoteDuePlannedTouchpoints(ctx, orgID, time.Now().UTC())
	return n, err
}
