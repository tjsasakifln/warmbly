package confenge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
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
	if err := RequireTargetFit(acc); err != nil {
		return nil, errx.New(errx.BadRequest, err.Error())
	}
	existing, err := s.repo.ListTouchpoints(ctx, orgID, accountID, "", 50, 0)
	if err != nil {
		return nil, errx.New(errx.Internal, "list touchpoints failed")
	}
	recoveryRevision := ""
	latestRecoverableCancellation := time.Time{}
	recoverableCancelledCadence := len(existing) > 0
	for _, t := range existing {
		if t.State == models.TouchpointSent || t.State == models.TouchpointDNC {
			return nil, errx.New(errx.BadRequest, "cannot replan cadence: account has SENT or DNC touchpoints")
		}
		if IsOpen(t.State) {
			return existing, nil
		}
		if !recoverableCadenceCancellation(t.State, t.StopReason) {
			recoverableCancelledCadence = false
		}
		if t.UpdatedAt.After(latestRecoverableCancellation) {
			latestRecoverableCancellation = t.UpdatedAt
		}
	}
	if len(existing) > 0 && !recoverableCancelledCadence {
		return existing, nil
	}
	if recoverableCancelledCadence {
		revision := latestRecoverableCancellation.UTC().Format("20060102T150405.000000000Z")
		if latestRecoverableCancellation.IsZero() {
			revision = existing[len(existing)-1].ID.String()
		}
		recoveryRevision = ":recovery:" + PromptVersion + ":" + revision
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
		current := make([]models.OutreachContactCandidate, 0, len(list))
		if acc.LastImportRunID != nil {
			for i := range list {
				if list[i].LastImportRunID != nil && *list[i].LastImportRunID == *acc.LastImportRunID {
					current = append(current, list[i])
				}
			}
		} else {
			// Preserve the legacy/manual path for accounts that have never been
			// bound to the versioned feed contract.
			current = list
		}
		cand = pickRecommendedAny(current)
	}
	ch := strings.ToUpper(strings.TrimSpace(channel))
	if ch == "" {
		ch = models.OutreachChannelEmail
	}
	if ch != models.OutreachChannelEmail && ch != models.OutreachChannelWhatsApp {
		return nil, errx.New(errx.BadRequest, "channel must be EMAIL or WHATSAPP")
	}
	if ch == models.OutreachChannelEmail {
		if err := RequireEmailOutbound(acc, cand); err != nil {
			return nil, errx.New(errx.BadRequest, err.Error())
		}
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
		idem := fmt.Sprintf("tp:%s:%s:%d:%s%s", orgID, accountID, step.Ordinal, step.CadenceStep, recoveryRevision)
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
		if persisted, getErr := s.repo.GetTouchpointByIdempotency(ctx, orgID, idem); getErr == nil && persisted != nil {
			tp = persisted
		} else if err := s.repo.InsertTouchpoint(ctx, tp); err != nil {
			persisted, getErr := s.repo.GetTouchpointByIdempotency(ctx, orgID, idem)
			if getErr != nil || persisted == nil {
				return nil, errx.New(errx.Internal, "insert touchpoint: "+err.Error())
			}
			tp = persisted
		}
		id := tp.ID
		prevID = &id
		out = append(out, *tp)
	}
	_ = userID
	return out, nil
}

func recoverableCadenceCancellation(state, reason string) bool {
	if state != models.TouchpointCancelled {
		return false
	}
	reason = strings.TrimSpace(reason)
	return strings.EqualFold(reason, StopComposerStale) ||
		strings.EqualFold(reason, TargetFitReasonStale) ||
		strings.EqualFold(reason, staleReviewStopReason)
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
	pb, _ := LoadPlaybook()
	for i := range list {
		if acc, err := s.repo.GetAccount(ctx, orgID, list[i].AccountID); err == nil && acc != nil {
			list[i].Account = acc
			pos := list[i].Ordinal
			if pos <= 0 {
				pos = 1
			}
			var cand *models.OutreachContactCandidate
			if list[i].ContactCandidateID != nil {
				cand, _ = s.repo.GetCandidate(ctx, orgID, *list[i].ContactCandidateID)
				if cand != nil {
					list[i].RecipientMailboxPurpose = cand.MailboxPurpose
					list[i].RecipientGeneric = isGenericRecipient(cand)
				}
			}
			rec := RecipientResolution{Company: accName(acc)}
			if allCands, listErr := s.repo.ListCandidates(ctx, orgID, list[i].AccountID); listErr == nil {
				rec = ResolveRecipient(acc, allCands, time.Now().UTC())
				list[i].RecipientState = rec.State
				list[i].RecipientReason = rec.Reason
			}
			evidence, _ := s.repo.ListEvidence(ctx, orgID, list[i].AccountID)
			st, plan := BuildOutboundPlan(pb, acc, cand, evidence, pos)
			if list[i].FactUsed != "" {
				st.ObservedFact = list[i].FactUsed
			}
			ex := ExplainStrategy(st, list[i].Recipient)
			list[i].StrategyExplain = strategyExplainProjection(ex, "")
			promptVer := ""
			if list[i].DraftID != nil {
				if d, _ := s.repo.GetDraft(ctx, orgID, *list[i].DraftID); d != nil {
					list[i].Draft = d
					promptVer = d.PromptVersion
					if len(d.ValidationJSON) > 0 {
						var stored ValidationResult
						_ = json.Unmarshal(d.ValidationJSON, &stored)
						list[i].DoctrineAlerts = stored.DoctrineAlerts
						if stored.StrategyExplain != nil {
							list[i].StrategyExplain = strategyExplainProjection(*stored.StrategyExplain, ex.WhyThisAccount)
						}
						if stored.Recipient != nil && stored.Recipient.State != "" {
							list[i].RecipientState = stored.Recipient.State
							list[i].RecipientReason = stored.Recipient.Reason
							rec = *stored.Recipient
						}
					}
				}
			}
			synth := DraftOutput{
				Subject:     firstNonEmpty(draftSubject(list[i]), list[i].Subject),
				BodyText:    firstNonEmpty(draftBody(list[i]), list[i].BodyText),
				ServiceCode: firstNonEmpty(list[i].ServiceCode, acc.ServiceCode),
				FactUsed:    list[i].FactUsed, Channel: ChannelEmailInitial,
			}
			if d := list[i].Draft; d != nil {
				synth.RiskFlags = d.RiskFlags
				synth.EvidenceIDs = d.EvidenceIDs
			}
			val := ValidationResult{OK: false}
			if strings.TrimSpace(synth.BodyText) != "" {
				val = ValidateDraft(&synth, acc, cand, ValidateOpts{
					Evidence: evidence, Channel: ChannelEmailInitial,
					Strategy: &st, Playbook: pb, PromptVersion: promptVer,
				})
			}
			if !RecipientStateAuthorizable(rec.State) || plan.Messageability != MessageabilityReady {
				val.OK = false
			}
			if d := list[i].Draft; d != nil && d.ValidationOK != nil && !*d.ValidationOK {
				val.OK = false
			}
			pack := BuildConsultantSendabilityPack(acc, cand, rec, plan, synth, val)
			list[i].ConsultantSendability = pack.AsMap()
			projectTouchpointEditorial(&list[i], acc, cand, promptVer)
			// Live QA wins over a leftover stored true so the API cannot lie.
			if d := list[i].Draft; d != nil {
				ok := val.OK && pack.SendWithoutEditing == "sim"
				d.ValidationOK = &ok
			}
		}
	}
	return list, nil
}

func draftSubject(tp models.OutreachTouchpoint) string {
	if tp.Draft != nil {
		return tp.Draft.Subject
	}
	return ""
}

func draftBody(tp models.OutreachTouchpoint) string {
	if tp.Draft != nil {
		return tp.Draft.BodyText
	}
	return ""
}

func strategyExplainProjection(ex StrategyExplain, fallbackWhyThisAccount string) map[string]any {
	return map[string]any{
		"why_this_account":      firstNonEmpty(ex.WhyThisAccount, fallbackWhyThisAccount),
		"why_now":               ex.WhyNow,
		"fact_used":             ex.FactUsed,
		"hypothesis":            ex.Hypothesis,
		"service":               ex.Service,
		"offer":                 ex.Offer,
		"recipient":             ex.Recipient,
		"sources":               ex.Sources,
		"touch":                 ex.Touch,
		"experiment":            ex.Experiment,
		"doctrine_version":      ex.Doctrine,
		"messageability":        ex.Messageability,
		"messageability_reason": ex.MessageabilityReason,
	}
}

func firstNonEmptyIDs(a, b []string) []string {
	if len(a) > 0 {
		return a
	}
	return b
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
	recoveryState := tp.State
	forceAIRewrite := recoveryState == models.TouchpointRejectedRewritePending
	switch tp.State {
	case models.TouchpointDue, models.TouchpointDrafted, models.TouchpointNeedsReview, models.TouchpointPlanned,
		models.TouchpointAIRewritePending, models.TouchpointEnrichmentPending,
		models.TouchpointRejectedRewritePending:
	default:
		return nil, errx.New(errx.BadRequest, "cannot generate for state "+tp.State)
	}
	if xerr := s.assertPriorReleased(ctx, orgID, tp); xerr != nil {
		return nil, xerr
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
	var targetFitRecoveryReason string
	if tp.Channel != models.OutreachChannelWhatsApp {
		var gateErr error
		targetFitRecoveryReason, gateErr = requireEmailCandidateForDraft(acc, cand)
		if gateErr != nil {
			return nil, errx.New(errx.BadRequest, gateErr.Error())
		}
	} else {
		var gateErr error
		targetFitRecoveryReason, gateErr = targetFitForDraft(acc)
		if gateErr != nil {
			return nil, errx.New(errx.BadRequest, gateErr.Error())
		}
	}
	allCands, listErr := s.repo.ListCandidates(ctx, orgID, tp.AccountID)
	if listErr != nil {
		allCands = nil
	}
	rec := ResolveRecipient(acc, allCands, time.Now().UTC())
	evidence, _ := s.repo.ListEvidence(ctx, orgID, tp.AccountID)
	priors, _ := s.repo.ListTouchpoints(ctx, orgID, tp.AccountID, models.TouchpointSent, 20, 0)
	var subject, body string
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
	// Prefer real evidence rows over opaque moment evidence ids for validators.
	evidIDs := evidenceIDsFrom(evidence, acc)
	factUsed := SanitizeText(firstNonEmpty(acc.FactToMention, tp.FactUsed), 2000)
	pb, _ := LoadPlaybook()
	pos := SequencePositionForTouch(tp.Ordinal, tp.Purpose)
	genCh := GenerationChannelForTouch(tp.Ordinal, tp.Purpose)
	st, plan := BuildOutboundPlan(pb, acc, cand, evidence, pos)
	if factUsed != "" && plan.Messageability == MessageabilityReady && plan.Hook != "" {
		st.ObservedFact = factUsed
	}
	excludeDraftID := uuid.Nil
	if tp.DraftID != nil {
		excludeDraftID = *tp.DraftID
	}
	recent := recentDraftBodiesExcept(ctx, s, orgID, tp.AccountID, tp.Channel, excludeDraftID)
	var composed DraftOutput
	providerName := "template"
	modelName := "semantic_" + tp.Purpose
	var editorialReasons []string
	aiRewriteSucceeded := false
	if plan.Messageability == MessageabilityBlocked || !RecipientStateAuthorizable(rec.State) {
		subject, body = "", ""
		factUsed = ""
	} else {
		priorSubject := ""
		if len(priors) > 0 {
			priorSubject = priors[len(priors)-1].Subject
		}
		class := CandidateRouteClass(cand)
		personName := ""
		if cand != nil {
			personName = firstName(cand.Name)
		}
		semantic, reasons := ComposeEditorialTouch(acc, cand, class, genCh, priorSubject)
		editorialReasons = append(editorialReasons, reasons...)
		if len(reasons) == 0 {
			// Missing specificity is a soft planning deficit, not a reason to
			// discard the lead. A grounded semantic tier-B brief may promote it to
			// review while the underlying enrichment remains visible in reasons.
			if plan.Messageability != MessageabilityBlocked {
				plan.Messageability = MessageabilityReady
				plan.Reason = ""
				plan.ReasonCodes = nil
				st.ObservedFact = semantic.ObservedFact
			}
			composed = DraftOutput{
				Subject: semantic.Subject, BodyText: semantic.Body,
				ServiceCode: firstNonEmpty(plan.ServiceCode, acc.ServiceCode),
				FactUsed:    semantic.ObservedFact,
				EvidenceIDs: firstNonEmptyIDs(plan.EvidenceIDs, evidIDs),
				Channel:     genCh, CTA: semantic.CTA, Question: semantic.CTA,
				Rationale: "semantic editorial brief",
				RiskFlags: []string{"semantic_editorial", "messageability_ready"},
			}
			editorialReasons = EditorialQA(composed.Subject, composed.BodyText, EditorialQAContext{
				RouteClass:      class,
				RawFact:         firstNonEmpty(acc.FactToMention, acc.MomentSummary),
				SenderFirstName: editorialSenderFirstName(),
				PersonProven:    composerMaySeePersonName(cand),
				PersonName:      personName,
			})
		}

		// A deterministic editorial miss is recoverable. When AI is configured,
		// give it one bounded rewrite attempt over the same evidence-only plan.
		// The result still has to clear every deterministic validator and remains
		// pending human approval.
		if (len(editorialReasons) > 0 || forceAIRewrite) && s.ai != nil && len(reasons) == 0 {
			touches := make([]TouchSummary, 0, len(priors))
			for _, prior := range priors {
				touches = append(touches, TouchSummary{
					Channel: prior.Channel, Direction: "OUTBOUND",
					Subject: prior.Subject, Snippet: SanitizeText(prior.BodyText, 500),
					At: prior.UpdatedAt.UTC().Format(time.RFC3339),
				})
			}
			aiOut, aiProvider, aiModel, aiErr := (&AIDraftGenerator{Provider: s.ai}).Generate(ctx, GenerateInput{
				Channel: genCh, Account: acc, Contact: cand, Evidence: evidence,
				Touches: touches, RecentBodies: recent, PriorSubject: priorSubject,
				AllowNearDupRegen: true,
			})
			if aiErr == nil {
				aiQA := EditorialQA(aiOut.Subject, aiOut.BodyText, EditorialQAContext{
					RouteClass: class, RawFact: firstNonEmpty(acc.FactToMention, acc.MomentSummary),
					SenderFirstName: editorialSenderFirstName(),
					PersonProven:    composerMaySeePersonName(cand), PersonName: personName,
				})
				if len(aiQA) == 0 {
					composed, editorialReasons = aiOut, nil
					providerName, modelName = aiProvider, aiModel
					composed.RiskFlags = appendUnique(composed.RiskFlags, "ai_editorial_rewrite")
					aiRewriteSucceeded = true
				}
			}
		}
		if forceAIRewrite && !aiRewriteSucceeded {
			editorialReasons = appendUnique(editorialReasons, "rejected_copy_requires_ai_rewrite")
		}

		if _, hit := NearDuplicate(composed.BodyText, recent); hit {
			composed.BodyText = varyTemplateHook(composed.BodyText)
			composed.RiskFlags = appendUnique(composed.RiskFlags, "near_dup_regenerated")
		}
		subject, body = composed.Subject, composed.BodyText
		factUsed = composed.FactUsed
	}
	// Classify risk with the same validators as GenerateDraft. Policy-authorized
	// deterministic templates may stay GREEN when AllowPolicyTemplateGREEN is set.
	// Follow-up/close use EMAIL_FOLLOWUP so legitimate "Re:" subjects are not first-touch fakes.
	synth := &DraftOutput{
		Subject: subject, BodyText: body, ServiceCode: firstNonEmpty(plan.ServiceCode, acc.ServiceCode),
		FactUsed: factUsed, EvidenceIDs: firstNonEmptyIDs(plan.EvidenceIDs, evidIDs), Claims: claimsFromFact(factUsed, evidIDs),
		Channel: genCh, Rationale: "touchpoint strategy compose",
		CTA: plan.CTA, Question: plan.CTA, RiskFlags: composed.RiskFlags,
	}
	val := ValidateDraft(synth, acc, cand, ValidateOpts{
		MaxWords: s.cfg.MaxInitialEmailWords, Evidence: evidence, Channel: genCh,
		Strategy: &st, Playbook: pb, RecentBodies: recent,
	})
	recipient := tp.Recipient
	if cand != nil && recipient == "" {
		recipient = cand.Email
	}
	if plan.Messageability == MessageabilityBlocked {
		val.OK = false
		if plan.Reason != "" {
			val.Errors = appendUnique(val.Errors, plan.Reason)
		}
	}
	val.Recipient = &rec
	if !RecipientStateAuthorizable(rec.State) {
		val.OK = false
		if rec.Reason != "" {
			val.Errors = appendUnique(val.Errors, rec.Reason)
		}
	}
	if targetFitRecoveryReason != "" {
		val.OK = false
		val.Errors = appendUnique(val.Errors, targetFitRecoveryReason)
	}
	risk, flags := ClassifyRisk(acc, cand, synth, val)
	for _, code := range editorialReasons {
		flags = appendUnique(flags, "editorial_"+code)
	}
	if plan.Messageability == MessageabilityBlocked {
		risk = "RED"
		flags = appendUnique(flags, "messageability_"+strings.ToLower(plan.Messageability))
		flags = appendUnique(flags, plan.ReasonCodes...)
	}
	if !RecipientStateAuthorizable(rec.State) {
		risk = "RED"
		flags = appendUnique(flags, "recipient_"+strings.ToLower(rec.State))
		flags = appendUnique(flags, rec.ReasonCodes...)
	}
	if targetFitRecoveryReason != "" {
		risk = "RED"
		flags = appendUnique(flags, strings.ToLower(targetFitRecoveryReason))
	}
	valJSON := PackValidationWithPlan(val, st, plan, recipient)
	allowTemplateGREEN := false
	if s.cfg.GreenAutorunEnabled && s.policyStore != nil {
		if settings, _ := s.repo.GetOrgSettings(ctx, orgID); settings != nil && settings.CampaignID != nil {
			if auth, _ := s.policyStore.GetActiveCampaignPolicy(ctx, orgID, *settings.CampaignID, time.Now().UTC()); auth != nil && auth.Active(time.Now().UTC()) {
				allowTemplateGREEN = auth.AllowPolicyTemplateGREEN
			}
		}
	}
	// Policy-authorized deterministic template: GREEN when validators pass and
	// ClassifyRisk did not raise RED (YELLOW product topics are allowed).
	if allowTemplateGREEN && val.OK && risk != "RED" {
		risk = "GREEN"
		modelName = "policy_approved_v1"
		flags = append(flags, "policy_approved_template")
		cleaned := flags[:0]
		for _, f := range flags {
			if f != "template_fallback" {
				cleaned = append(cleaned, f)
			}
		}
		flags = cleaned
	} else if risk == "GREEN" {
		// Unaudited template path stays human-review YELLOW unless policy grants template GREEN.
		risk = "YELLOW"
		flags = append(flags, "template_fallback")
	}
	flags = append(flags, "per_touch", "jit")
	// NEEDS_REVIEW is only for sendable copy awaiting human authorization.
	draftStatus := models.OutreachDraftNeedsReview
	switch {
	case rec.State == RecipientBlocked:
		draftStatus = models.OutreachDraftBlocked
		subject, body = "", ""
	case targetFitRecoveryReason != "":
		draftStatus = models.OutreachDraftEnrichmentPending
	case !RecipientStateAuthorizable(rec.State):
		draftStatus = models.OutreachDraftEnrichmentPending
	case plan.Messageability == MessageabilityBlocked:
		// A hard outbound gate on today's dossier is not a terminal lead
		// decision. Keep the touch recoverable so enrichment or a future
		// playbook mapping can make it reviewable.
		draftStatus = models.OutreachDraftEnrichmentPending
		subject, body = "", ""
	case forceAIRewrite && !aiRewriteSucceeded:
		draftStatus = models.OutreachDraftRejectedRewritePending
	default:
		if len(editorialReasons) > 0 || !val.OK || strings.TrimSpace(body) == "" {
			draftStatus = models.OutreachDraftAIRewritePending
		}
	}
	// One active draft per account (unique index). Reuse when present so
	// generate is not blocked by outreach_drafts_org_account_active_uidx.
	draft, _ := s.repo.GetActiveDraftForAccount(ctx, orgID, tp.AccountID)
	if draft == nil {
		draft = &models.OutreachDraft{
			OrganizationID: orgID, AccountID: tp.AccountID, ContactCandidateID: tp.ContactCandidateID,
			Channel: tp.Channel, Subject: subject, BodyText: body, ServiceCode: acc.ServiceCode,
			StrategyCode: StrategyCodeFor(st), FactUsed: factUsed, EvidenceIDs: evidIDs,
			Provider: providerName, Model: modelName, PromptVersion: PromptVersion + "+touch",
			Status: draftStatus, RiskClass: risk, RiskFlags: flags,
			ValidationJSON: valJSON,
		}
	} else {
		draft.ContactCandidateID = tp.ContactCandidateID
		draft.Channel = tp.Channel
		draft.Subject, draft.BodyText = subject, body
		draft.ServiceCode = acc.ServiceCode
		draft.StrategyCode = StrategyCodeFor(st)
		draft.FactUsed = factUsed
		draft.EvidenceIDs = evidIDs
		draft.Provider, draft.Model = providerName, modelName
		draft.PromptVersion = PromptVersion + "+touch"
		draft.Status = draftStatus
		draft.RiskClass, draft.RiskFlags = risk, flags
		draft.ValidationJSON = valJSON
		draft.ApprovedBy, draft.ApprovedAt = nil, nil
		draft.HumanEdited = false
	}
	ok := val.OK
	draft.ValidationOK = &ok
	if cand != nil {
		draft.RecipientName, draft.RecipientRole, draft.RecipientEmail = cand.Name, cand.Role, cand.Email
		draft.RecipientPhoneE164, draft.VerificationStatus = cand.PhoneE164, cand.VerificationStatus
		if isGenericRecipient(cand) {
			draft.RecipientName, draft.RecipientRole = "", ""
		}
	}
	if err := s.repo.UpsertDraft(ctx, draft); err != nil {
		return nil, errx.New(errx.Internal, "save draft: "+err.Error())
	}
	if draftStatus == models.OutreachDraftAIRewritePending || draftStatus == models.OutreachDraftEnrichmentPending {
		reason := "suboptimal_copy"
		if targetFitRecoveryReason != "" {
			reason = targetFitRecoveryReason
		} else if len(editorialReasons) > 0 {
			reason = editorialReasons[0]
		} else if len(plan.ReasonCodes) > 0 {
			reason = plan.ReasonCodes[0]
		} else if len(val.Errors) > 0 {
			reason = val.Errors[0]
		}
		did, tid := draft.ID, tp.ID
		s.recordEditorialSignal(ctx, orgID, &did, &tid, draftStatus, reason, tp.Channel)
	}
	ApplyContentMutation(tp, tp.Channel, tp.Recipient, draft.Subject, draft.BodyText)
	tp.DraftID = &draft.ID
	if draftStatus == models.OutreachDraftNeedsReview {
		tp.State = models.TouchpointNeedsReview
		tp.StopReason = ""
	} else if draftStatus == models.OutreachDraftAIRewritePending {
		tp.State = models.TouchpointAIRewritePending
	} else if draftStatus == models.OutreachDraftEnrichmentPending {
		tp.State = models.TouchpointEnrichmentPending
	} else if draftStatus == models.OutreachDraftRejectedRewritePending {
		tp.State = models.TouchpointRejectedRewritePending
	} else if draftStatus == models.OutreachDraftBlocked {
		tp.State = models.TouchpointCancelled
	} else {
		tp.State = models.TouchpointDrafted
	}
	if draftStatus != models.OutreachDraftNeedsReview {
		tp.StopReason = firstNonEmpty(targetFitRecoveryReason, rec.Reason, plan.Reason, "recipient_"+strings.ToLower(rec.State), "messageability_"+strings.ToLower(plan.Messageability))
	}
	tp.ServiceCode, tp.FactUsed, tp.EvidenceIDs = acc.ServiceCode, draft.FactUsed, draft.EvidenceIDs
	// Capture message-material context at generation for stale-approval guard.
	tp.GeneratedContextHash = acc.MessageContextHash
	// Evidence and context are part of the approval binding. They are assigned
	// after the prose mutation above, so recompute once more before exposing the
	// hash to a reviewer. Approval must not silently replace the reviewed hash.
	RecomputeContentHash(tp)
	if err := s.repo.UpdateTouchpoint(ctx, tp); err != nil {
		return nil, errx.New(errx.Internal, "update touchpoint: "+err.Error())
	}
	if draftStatus == models.OutreachDraftNeedsReview {
		_ = s.repo.SetAccountHumanFlags(ctx, orgID, tp.AccountID, acc.Blocked, acc.DoNotContact, acc.BlockReason, models.OutreachQueueNeedsReview)
	}
	pack := BuildConsultantSendabilityPack(acc, cand, rec, plan, *synth, val)
	tp.ConsultantSendability = pack.AsMap()
	if strings.TrimSpace(tp.BodyText) == "" {
		tp.GenerationError = firstNonEmpty(rec.Reason, plan.Reason, strings.Join(val.Errors, "; "), "Gerar mensagem não produziu copy sendable.")
	}
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
	if cand != nil && !isGenericRecipient(cand) {
		name = strings.TrimSpace(strings.Split(cand.Name, " ")[0])
	}
	greeting := "Olá"
	if name != "" {
		greeting = "Olá, " + name
	}
	if tp.Channel == models.OutreachChannelWhatsApp {
		body = BuildWhatsAppCopy(acc, cand)
		if len(sent) > 0 {
			body = greeting + ", retomo o contato sobre " + company + "."
		}
		return "", SanitizeText(body, 2000)
	}
	// Strategy-first compose by ordinal (1=SIGNAL … 5=CLOSE). Signature applied at enroll/send.
	pb, _ := LoadPlaybook()
	pos := SequencePositionForTouch(tp.Ordinal, tp.Purpose)
	genCh := GenerationChannelForTouch(tp.Ordinal, tp.Purpose)
	st := PlanOutreachStrategy(pb, acc, cand, evidence, pos)
	if fact != "" {
		st.ObservedFact = fact
	}
	out := ComposeFromStrategy(st, acc, cand, genCh)
	subject, body = out.Subject, out.BodyText
	_ = greeting
	_ = company
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
	origBody, origSubj, origRecipient := tp.BodyText, tp.Subject, tp.Recipient
	ch, rec, sub, bod := tp.Channel, tp.Recipient, tp.Subject, tp.BodyText
	if channel != nil && *channel != "" {
		ch = strings.ToUpper(strings.TrimSpace(*channel))
	}
	if recipient != nil {
		rec = strings.TrimSpace(*recipient)
	}
	var reboundCandidate *models.OutreachContactCandidate
	if ch == models.OutreachChannelEmail && recipient != nil {
		acc, loadErr := s.repo.GetAccount(ctx, orgID, tp.AccountID)
		if loadErr != nil || acc == nil {
			return nil, errx.New(errx.NotFound, "account not found")
		}
		candidates, loadErr := s.repo.ListCandidates(ctx, orgID, tp.AccountID)
		if loadErr != nil {
			return nil, errx.New(errx.Internal, "recipient validation failed")
		}
		resolved, block := resolvePilotRecipient(candidates, acc.LastImportRunID, time.Now().UTC())
		if block != nil || !strings.EqualFold(strings.TrimSpace(resolved.Candidate.Email), rec) {
			return nil, errx.New(errx.BadRequest, "recipient must match the single current authoritative contact")
		}
		reboundCandidate = resolved.Candidate
	}
	if subject != nil {
		sub = *subject
	}
	if body != nil {
		bod = *body
	}
	ApplyContentMutation(tp, ch, rec, sub, bod)
	if reboundCandidate != nil {
		id := reboundCandidate.ID
		tp.ContactCandidateID = &id
	}
	if err := s.repo.UpdateTouchpoint(ctx, tp); err != nil {
		return nil, errx.New(errx.Internal, "update failed")
	}
	if tp.DraftID != nil {
		if d, loadErr := s.repo.GetDraft(ctx, orgID, *tp.DraftID); loadErr != nil {
			return nil, errx.New(errx.Internal, "load draft failed")
		} else if d != nil {
			// Keep draft transport fields in lockstep with the touchpoint so
			// requireTouchTransport binding hash matches approved hash.
			d.Subject, d.BodyText = tp.Subject, tp.BodyText
			d.Channel = tp.Channel
			if tp.Channel == models.OutreachChannelWhatsApp || tp.Channel == "WHATSAPP" {
				d.RecipientPhoneE164 = tp.Recipient
			} else {
				d.RecipientEmail = tp.Recipient
				// Rebind ContactCandidateID to an enrollable email match when the
				// human edits the recipient (plan may have bound a phone-only cand).
				if reboundCandidate != nil {
					id := reboundCandidate.ID
					d.ContactCandidateID = &id
					d.RecipientName = reboundCandidate.Name
					d.RecipientRole = reboundCandidate.Role
					d.VerificationStatus = reboundCandidate.VerificationStatus
				} else if rec := strings.TrimSpace(tp.Recipient); rec != "" {
					if list, err := s.repo.ListCandidates(ctx, orgID, tp.AccountID); err == nil {
						for i := range list {
							c := &list[i]
							if strings.EqualFold(strings.TrimSpace(c.Email), rec) && c.CanEnroll() {
								id := c.ID
								d.ContactCandidateID = &id
								tp.ContactCandidateID = &id
								break
							}
						}
					}
				}
			}
			d.HumanEdited, d.Status = true, models.OutreachDraftNeedsReview
			d.ApprovedBy, d.ApprovedAt = nil, nil
			// Operator edit learning signal (accumulate only; no auto-train).
			if sig := NewOperatorEditSignal(d.ID.String(), origBody, tp.BodyText); len(sig.Codes) > 0 {
				var val ValidationResult
				if len(d.ValidationJSON) > 0 {
					_ = json.Unmarshal(d.ValidationJSON, &val)
				}
				val.OperatorEdit = &sig
				if b, err := json.Marshal(val); err == nil {
					d.ValidationJSON = b
				}
			}
			decision := DecisionEdit
			if origRecipient != tp.Recipient {
				decision = DecisionRecipientChange
			}
			if hc, herr := RecordHumanDecision(decision, d.ID.String(), userID.String(), origBody, tp.BodyText, origSubj, tp.Subject, nil); herr == nil {
				hc.RecipientBefore = origRecipient
				hc.RecipientAfter = tp.Recipient
				attachHumanCorrection(d, hc)
			}
			if err := s.repo.UpsertDraft(ctx, d); err != nil {
				return nil, errx.New(errx.Internal, "draft update failed")
			}
			if err := s.repo.UpdateTouchpoint(ctx, tp); err != nil {
				return nil, errx.New(errx.Internal, "touchpoint rebind failed")
			}
		}
	}
	_ = userID
	return tp, nil
}

func (s *service) ApproveTouchpoint(ctx context.Context, orgID, userID, id uuid.UUID, options ApprovalOptions) (*models.OutreachTouchpoint, *errx.Error) {
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
	if s.cfg.OperatorMode {
		if err := s.requirePilotMembershipForTouchpoint(ctx, orgID, tp); err != nil {
			return nil, errx.New(errx.Conflict, "pilot approval blocked: "+err.Error())
		}
	}
	if xerr := s.assertPriorReleased(ctx, orgID, tp); xerr != nil {
		return nil, xerr
	}
	if xerr := s.assertTouchpointEditorialAuthority(ctx, orgID, tp, "APPROVE"); xerr != nil {
		return nil, xerr
	}
	acc, _ := s.repo.GetAccount(ctx, orgID, tp.AccountID)
	if acc == nil {
		return nil, errx.New(errx.NotFound, "account not found")
	}
	if err := AssertMessageContextFresh(acc, tp.GeneratedContextHash); err != nil {
		ClearApproval(tp)
		tp.ContextStale = true
		tp.StopReason = "context_stale"
		_ = s.repo.UpdateTouchpoint(ctx, tp)
		return nil, errx.New(errx.Conflict, err.Error())
	}
	var cand *models.OutreachContactCandidate
	if tp.ContactCandidateID != nil {
		cand, _ = s.repo.GetCandidate(ctx, orgID, *tp.ContactCandidateID)
	}
	if tp.Channel == models.OutreachChannelWhatsApp {
		if err := RequireTargetFit(acc); err != nil {
			return nil, errx.New(errx.BadRequest, err.Error())
		}
	} else if err := RequireEmailOutbound(acc, cand); err != nil {
		return nil, errx.New(errx.BadRequest, err.Error())
	}
	if cand == nil {
		return nil, errx.New(errx.BadRequest, "approved contact candidate is missing")
	}
	if isGenericRecipient(cand) && !options.GenericRecipientAcknowledged {
		return nil, errx.New(errx.BadRequest, "generic or role mailbox requires explicit human acknowledgement")
	}
	if tp.Channel == models.OutreachChannelWhatsApp {
		phone := firstNonEmpty(cand.PhoneE164, cand.Phone)
		if strings.TrimSpace(tp.Recipient) != strings.TrimSpace(phone) {
			return nil, errx.New(errx.BadRequest, "recipient does not match the approved contact candidate")
		}
	} else if !strings.EqualFold(strings.TrimSpace(tp.Recipient), strings.TrimSpace(cand.Email)) {
		return nil, errx.New(errx.BadRequest, "recipient does not match the approved contact candidate")
	}
	if (acc.LastImportRunID != nil || cand.LastImportRunID != nil) &&
		(acc.LastImportRunID == nil || cand.LastImportRunID == nil || *acc.LastImportRunID != *cand.LastImportRunID) {
		return nil, errx.New(errx.Conflict, "recipient is not present in the current account snapshot")
	}
	if tp.DraftID == nil {
		return nil, errx.New(errx.BadRequest, "touchpoint has no review draft")
	}
	draft, draftErr := s.repo.GetDraft(ctx, orgID, *tp.DraftID)
	if draftErr != nil || draft == nil {
		return nil, errx.New(errx.BadRequest, "touchpoint review draft not found")
	}
	draftRecipient := draft.RecipientEmail
	if tp.Channel == models.OutreachChannelWhatsApp {
		draftRecipient = draft.RecipientPhoneE164
	}
	if draft.ContactCandidateID == nil || *draft.ContactCandidateID != cand.ID ||
		!strings.EqualFold(strings.TrimSpace(draftRecipient), strings.TrimSpace(tp.Recipient)) ||
		draft.Subject != tp.Subject || draft.BodyText != tp.BodyText {
		return nil, errx.New(errx.Conflict, "touchpoint and draft content are inconsistent")
	}
	pb, _ := LoadPlaybook()
	strategy := strategyFromDraft(draft)
	if blockers := StructuralApproveBlockers(acc, cand, strategy, draft, pb); len(blockers) > 0 {
		return nil, errx.New(errx.BadRequest, "approval blocked: "+strings.Join(blockers, "; "))
	}
	RecomputeContentHash(tp)
	if err := ApplyHumanApproval(tp, userID, time.Now().UTC()); err != nil {
		return nil, errx.New(errx.BadRequest, err.Error())
	}
	if hc, herr := RecordHumanDecision(DecisionApprove, draft.ID.String(), userID.String(), draft.BodyText, draft.BodyText, draft.Subject, draft.Subject, nil); herr == nil {
		attachHumanCorrection(draft, hc)
		if err := s.repo.UpsertDraft(ctx, draft); err != nil {
			return nil, errx.New(errx.Internal, "approve correction persist failed")
		}
	}
	if err := s.repo.UpdateTouchpoint(ctx, tp); err != nil {
		return nil, errx.New(errx.Internal, "approve persist failed")
	}
	if acc != nil {
		_ = s.repo.SetAccountHumanFlags(ctx, orgID, tp.AccountID, acc.Blocked, acc.DoNotContact, acc.BlockReason, models.OutreachQueueApproved)
	}
	if s.audit != nil {
		draftID := ""
		if tp.DraftID != nil {
			draftID = tp.DraftID.String()
		}
		s.audit.LogAction(ctx, orgID, userID, models.AuditActionUpdate, models.AuditEntityOutreachAccount, &tp.AccountID, "", "",
			map[string]string{"action": "touchpoint_approved", "touchpoint_id": tp.ID.String()},
			map[string]string{"draft_id": draftID, "generic_recipient_acknowledged": fmt.Sprintf("%t", options.GenericRecipientAcknowledged)},
		)
	}
	// Production wires the governor. Approval then means approval plus durable
	// scheduling, even while dispatch is paused. Unit services without a
	// governor retain the historical APPROVED state for isolated testing.
	if s.governor != nil {
		queued, qerr := s.scheduleApprovedTouchpoint(ctx, orgID, tp)
		if qerr != nil {
			return nil, qerr
		}
		return queued, nil
	}
	return tp, nil
}

// note: QueueTouchpoint + CanTransport accept CAMPAIGN_POLICY without approved_by.

func (s *service) RejectOrSkipTouchpoint(ctx context.Context, orgID, userID, id uuid.UUID, action string) (*models.OutreachTouchpoint, *errx.Error) {
	return s.RejectOrSkipTouchpointReason(ctx, orgID, userID, id, action, "")
}

// RejectOrSkipTouchpointReason records optional doctrine rejection reason for learning (one click).
func (s *service) RejectOrSkipTouchpointReason(ctx context.Context, orgID, userID, id uuid.UUID, action, reason string) (*models.OutreachTouchpoint, *errx.Error) {
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
		// Reject the copy, not the lead. The same touchpoint remains open for a
		// new semantic or AI rewrite unless a separate hard suppression is
		// recorded on the account or recipient.
		state, stop = models.TouchpointRejectedRewritePending, "REJECTED_REWRITE_PENDING"
		if r := strings.TrimSpace(reason); r != "" {
			pb, _ := LoadPlaybook()
			if ValidRejectionReason(pb, r) {
				stop = "REJECTED:" + r
			}
		}
	}
	tp.State, tp.StopReason = state, stop
	ClearApproval(tp)
	// Persist operator rejection learning signal on the draft when present.
	if tp.DraftID != nil {
		if d, _ := s.repo.GetDraft(ctx, orgID, *tp.DraftID); d != nil {
			if action == "reject" || action == "REJECTED" {
				rej := NewOperatorRejection(strings.TrimPrefix(stop, "REJECTED:"), d.ID.String(), tp.ServiceCode, "")
				if rej.Reason == "REJECTED" || rej.Reason == "" {
					rej.Reason = "other"
				}
				var val ValidationResult
				if len(d.ValidationJSON) > 0 {
					_ = json.Unmarshal(d.ValidationJSON, &val)
				}
				val.OperatorReject = &rej
				if b, err := json.Marshal(val); err == nil {
					d.ValidationJSON = b
				}
				d.Status = models.OutreachDraftRejectedRewritePending
			} else if action == "skip" || action == "SKIPPED" {
				d.Status = models.OutreachDraftSkipped
			}
			decision := DecisionSkip
			reasons := []string{}
			if action == "reject" || action == "REJECTED" {
				decision = DecisionReject
				if r := strings.TrimSpace(reason); r != "" {
					reasons = []string{r}
				}
			}
			if hc, herr := RecordHumanDecision(decision, d.ID.String(), userID.String(), d.BodyText, d.BodyText, d.Subject, d.Subject, reasons); herr == nil {
				attachHumanCorrection(d, hc)
			}
			_ = s.repo.UpsertDraft(ctx, d)
		}
	}
	if err := s.repo.UpdateTouchpoint(ctx, tp); err != nil {
		return nil, errx.New(errx.Internal, "update failed")
	}
	if state == models.TouchpointRejectedRewritePending {
		var draftID *uuid.UUID
		if tp.DraftID != nil {
			id := *tp.DraftID
			draftID = &id
		}
		tid := tp.ID
		s.recordEditorialSignal(ctx, orgID, draftID, &tid, "HUMAN_REJECTION", firstNonEmpty(reason, "other"), tp.Channel)
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
	if xerr := s.assertPriorReleased(ctx, orgID, tp); xerr != nil {
		return nil, xerr
	}
	if xerr := s.assertTouchpointEditorialAuthority(ctx, orgID, tp, "QUEUE"); xerr != nil {
		return nil, xerr
	}
	// Scheduling is allowed while transport is paused. The exact hash and
	// approval state are enforced atomically below; live DNC, policy revocation,
	// kill switch and transport gates are rechecked by the worker immediately
	// before any provider handoff.
	if tp.AuthorizationMode == AuthorizationModeCampaignPolicy {
		// Unlike a human decision, campaign authority is a live revocable grant.
		// A revoked grant cannot create new queue work.
		if err := s.assertCampaignPolicyQueueable(ctx, orgID, tp); err != nil {
			return nil, errx.New(errx.BadRequest, "queue blocked: "+err.Error())
		}
	}
	// Final dispatch gate: material context must still match generation-time hash.
	acc, aerr := s.repo.GetAccount(ctx, orgID, tp.AccountID)
	if aerr != nil || acc == nil {
		return nil, errx.New(errx.NotFound, "account not found for dispatch")
	}
	if err := AssertMessageContextFresh(acc, tp.GeneratedContextHash); err != nil {
		ClearApproval(tp)
		tp.StopReason = "context_stale"
		tp.ContextStale = true
		_ = s.repo.UpdateTouchpoint(ctx, tp)
		_ = s.repo.SetAccountHumanFlags(ctx, orgID, tp.AccountID, acc.Blocked, acc.DoNotContact, acc.BlockReason, models.OutreachQueueNeedsReview)
		return nil, errx.New(errx.Conflict, err.Error())
	}
	_ = userID
	return s.scheduleApprovedTouchpoint(ctx, orgID, tp)
}

func (s *service) scheduleApprovedTouchpoint(ctx context.Context, orgID uuid.UUID, tp *models.OutreachTouchpoint) (*models.OutreachTouchpoint, *errx.Error) {
	if s.governor == nil {
		return nil, errx.New(errx.ServiceUnavailable, "dispatch governor not wired; approval was preserved")
	}
	if tp == nil || tp.DraftID == nil {
		return nil, errx.New(errx.BadRequest, "approved touchpoint has no draft")
	}
	if tp.State == models.TouchpointQueued {
		return tp, nil
	}
	cfg := s.governor.Config()
	now := time.Now().UTC()
	earliest := now.Add(cfg.MinGap)
	if s.delegatedDB != nil {
		lockKey := delegatedFirstTouchAdvisoryKey("schedule", orgID)
		locked, err := s.repo.TryAdvisoryLock(ctx, lockKey)
		if err != nil {
			return nil, errx.New(errx.ServiceUnavailable, "queue scheduling lock unavailable")
		}
		if !locked {
			return nil, errx.New(errx.Conflict, "queue scheduling already in progress")
		}
		defer s.releaseDelegatedFirstTouchAdvisoryLock(lockKey)

		var latest *time.Time
		if err := s.delegatedDB.QueryRow(ctx, `
			SELECT max(due_at) FROM confenge_dispatch_queue
			WHERE organization_id=$1 AND status IN ('queued','reserved')`, orgID).Scan(&latest); err != nil {
			return nil, errx.New(errx.ServiceUnavailable, "queue runway readback unavailable")
		}
		if latest != nil && !latest.Before(now) {
			earliest = latest.UTC().Add(cfg.MinGap)
		}
	}
	due := dispatch.NextEligibleSlot(earliest, cfg.Timezone, cfg.WindowStart, cfg.WindowEnd, cfg.BusinessDaysOnly)
	messageKey := dispatch.MessageKeyEmail(*tp.DraftID)
	if tp.Channel == models.OutreachChannelWhatsApp {
		messageKey = dispatch.MessageKeyWhatsApp(*tp.DraftID)
	}
	queued, err := s.repo.CASScheduleTouchpoint(ctx, orgID, tp.ID, tp.ContentHash, messageKey, due)
	if err != nil {
		return nil, errx.New(errx.Internal, "schedule approved touchpoint: "+err.Error())
	}
	if queued == nil {
		latest, _ := s.repo.GetTouchpoint(ctx, orgID, tp.ID)
		if latest != nil && latest.State == models.TouchpointQueued && latest.ApprovedContentHash == latest.ContentHash {
			return latest, nil
		}
		return nil, errx.New(errx.Conflict, "touchpoint not schedulable (state or approved hash mismatch)")
	}
	return queued, nil
}

func (s *service) dispatchEmailTouch(ctx context.Context, orgID, userID uuid.UUID, tp *models.OutreachTouchpoint) *errx.Error {
	// Re-assert live policy grant immediately before enroll/SMTP (revoke-after-queue).
	if err := s.AssertTransportable(ctx, orgID, tp); err != nil {
		return errx.New(errx.Conflict, err.Error())
	}
	if tp.DraftID == nil {
		return errx.New(errx.BadRequest, "touchpoint has no draft")
	}
	d, err := s.repo.GetDraft(ctx, orgID, *tp.DraftID)
	if err != nil || d == nil {
		return errx.New(errx.NotFound, "draft not found")
	}
	d.Subject, d.BodyText = tp.Subject, tp.BodyText
	d.RecipientEmail = tp.Recipient
	d.Channel = tp.Channel
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
	// Local/dev/CI: deliver exact approved payload via SMTP when configured
	// (Mailpit). Does not regenerate copy — uses touchpoint subject/body only.
	providerID := "email-enrolled:" + d.ID.String()
	if localSMTPDeliveryEnabled() {
		from := strings.TrimSpace(os.Getenv("EMAIL_ADDRESS"))
		if err := deliverApprovedSMTP(from, tp.Recipient, tp.Subject, tp.BodyText); err != nil {
			return errx.New(errx.Internal, "approved SMTP delivery failed: "+err.Error())
		}
		providerID = "smtp-approved:" + d.ID.String()
	}
	// Enrollment only queues campaign execution. The consumer marks SENT after
	// the worker reports provider-confirmed transport success.
	tp.ProviderMessageID = providerID
	if err := s.repo.UpdateTouchpoint(ctx, tp); err != nil {
		return errx.New(errx.Internal, "persist queued enrollment failed")
	}
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

func (s *service) assertPriorReleased(ctx context.Context, orgID uuid.UUID, tp *models.OutreachTouchpoint) *errx.Error {
	if tp == nil || tp.Ordinal <= 1 {
		return nil
	}
	priors, err := s.repo.ListTouchpoints(ctx, orgID, tp.AccountID, "", 50, 0)
	if err != nil {
		return errx.New(errx.Internal, "list touchpoints failed")
	}
	if !PriorReleased(priors, tp.Ordinal) {
		return errx.New(errx.BadRequest, "prior touchpoint still awaits human decision or is not releasable")
	}
	return nil
}

// requireTouchTransport fails closed: every CONFENGE outbound must be backed by a
// touchpoint that is structurally transportable AND (for CAMPAIGN_POLICY) still
// bound to a live, non-revoked grant. Draft-only APPROVED is never enough.
// Used by EnrollDraft and WhatsApp send paths — must not use structural CanTransport alone.
func (s *service) requireTouchTransport(ctx context.Context, orgID, draftID uuid.UUID) (*models.OutreachTouchpoint, *errx.Error) {
	tp, err := s.repo.GetTouchpointByDraft(ctx, orgID, draftID)
	if err != nil {
		return nil, errx.New(errx.Internal, "load touchpoint failed")
	}
	if tp == nil {
		return nil, errx.New(errx.BadRequest, "CONFENGE transport requires an approved touchpoint; use the per-touch review queue (draft-only approval cannot send)")
	}
	// Live grant revalidation: revoke must block EnrollDraft / WhatsApp / API transport.
	if err := s.AssertTransportable(ctx, orgID, tp); err != nil {
		return nil, errx.New(errx.BadRequest, "CONFENGE transport blocked: "+err.Error())
	}
	if xerr := s.assertPriorReleased(ctx, orgID, tp); xerr != nil {
		return nil, xerr
	}
	// Bind live draft content to approved hash (ReviewDraft edit must not bypass).
	d, err := s.repo.GetDraft(ctx, orgID, draftID)
	if err != nil {
		return nil, errx.New(errx.Internal, "load draft failed")
	}
	if d != nil {
		recipient := d.RecipientEmail
		ch := d.Channel
		if ch == "" {
			ch = tp.Channel
		}
		if ch == models.OutreachChannelWhatsApp || ch == "WHATSAPP" {
			recipient = d.RecipientPhoneE164
			if recipient == "" {
				recipient = tp.Recipient
			}
		}
		live := ContentBindingHash(ch, recipient, d.Subject, d.BodyText, tp.Purpose, tp.EvidenceIDs, tp.GeneratedContextHash)
		if live != tp.ContentHash || live != tp.ApprovedContentHash {
			return nil, errx.New(errx.BadRequest, "draft content diverged from approved touchpoint; re-approve the exact send payload")
		}
	}
	return tp, nil
}

// PromoteDueTouchpoints moves PLANNED touches with due_at <= now to DUE only when
// every lower ordinal is SENT/SKIPPED/REJECTED (never while prior awaits human).
func (s *service) PromoteDueTouchpoints(ctx context.Context, orgID uuid.UUID) (int, error) {
	now := time.Now().UTC()
	// List due-eligible planned rows via repo helper, then filter by prior release.
	candidates, err := s.repo.ListDuePlannedTouchpoints(ctx, orgID, now, 200)
	if err != nil {
		return 0, err
	}
	n := 0
	// Cache account timelines to avoid N queries.
	byAccount := map[uuid.UUID][]models.OutreachTouchpoint{}
	for i := range candidates {
		c := candidates[i]
		priors, ok := byAccount[c.AccountID]
		if !ok {
			priors, err = s.repo.ListTouchpoints(ctx, orgID, c.AccountID, "", 50, 0)
			if err != nil {
				return n, err
			}
			byAccount[c.AccountID] = priors
		}
		if !PriorReleased(priors, c.Ordinal) {
			continue
		}
		c.State = models.TouchpointDue
		if err := s.repo.UpdateTouchpoint(ctx, &c); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// assertTouchpointEditorialAuthority refuses any operational step on a
// touchpoint whose copy an earlier composer wrote. Fail-closed: a touchpoint
// with no draft to identify carries no proof the current composer wrote it.
func (s *service) assertTouchpointEditorialAuthority(ctx context.Context, orgID uuid.UUID, tp *models.OutreachTouchpoint, action string) *errx.Error {
	if tp == nil {
		return errx.New(errx.NotFound, "touchpoint not found")
	}
	prompt := ""
	if tp.Draft != nil {
		prompt = tp.Draft.PromptVersion
	} else if tp.DraftID != nil {
		if d, _ := s.repo.GetDraft(ctx, orgID, *tp.DraftID); d != nil {
			prompt = d.PromptVersion
		}
	}
	if auth := EvaluateDraftEditorialAuthority(prompt); !auth.Actionable {
		return errx.New(errx.Conflict, auth.Blocker(action))
	}
	return nil
}

// projectTouchpointEditorial fills the read-only judging context the review
// screen shows next to the editable copy. Absent facts stay visible as absent
// rather than being omitted, because a blank field a reader can see is honest
// and a key that was never written is not.
func projectTouchpointEditorial(tp *models.OutreachTouchpoint, acc *models.OutreachAccount, cand *models.OutreachContactCandidate, promptVersion string) {
	auth := EvaluateDraftEditorialAuthority(promptVersion)
	tp.EditorialState = auth.State
	tp.EditorialActionable = auth.Actionable
	tp.EditorialReasonCodes = auth.ReasonCodes
	tp.EditorialNotice = auth.Notice
	tp.PromptVersion = promptVersion
	tp.ComposerVersion = ComposerVersion
	if !auth.Actionable {
		// The stamp we can prove is the prompt version; naming the current
		// composer beside superseded copy would read as if it wrote it.
		tp.ComposerVersion = ""
	}
	if cand != nil {
		tp.RouteClass = CandidateRouteClass(cand)
	}
	if acc != nil {
		if strings.TrimSpace(acc.FactToMention) != "" && strings.TrimSpace(tp.FactUsed) == acc.FactToMention {
			tp.FactSource = "fact_to_mention"
		}
		d := EvaluateTargetFit(acc)
		fit := map[string]any{
			"state":    acc.TargetFitClass,
			"eligible": d.Eligible,
			"reason":   d.Reason,
			"fresh":    acc.TargetFitFresh,
			"tier":     acc.TargetFitSendTier,
			"version":  acc.TargetFitVersion,
		}
		if acc.TargetFitObservedAt != nil {
			fit["as_of"] = acc.TargetFitObservedAt.UTC().Format(time.RFC3339)
		}
		tp.TargetFit = fit
	}
}
