package confenge

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// WirePolicyAuth attaches the durable campaign policy store.
func (s *service) WirePolicyAuth(store repository.ConfengePolicyRepository) {
	s.policyStore = store
}

// AuthorizeCampaignPolicy mints an auditable CAMPAIGN_POLICY_AUTHORIZATION grant.
func (s *service) AuthorizeCampaignPolicy(ctx context.Context, orgID, userID uuid.UUID, auth *models.CampaignPolicyAuthorization) (*models.CampaignPolicyAuthorization, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	if s.policyStore == nil {
		return nil, errx.New(errx.Internal, "campaign policy store not configured")
	}
	if userID == uuid.Nil {
		return nil, errx.New(errx.Unauthorized, "human actor required to authorize campaign policy")
	}
	if auth == nil || auth.CampaignID == uuid.Nil {
		return nil, errx.New(errx.BadRequest, "campaign_id required")
	}
	if auth.Channel == "" {
		auth.Channel = "EMAIL"
	}
	if strings.ToUpper(auth.Channel) != "EMAIL" {
		return nil, errx.New(errx.BadRequest, "only EMAIL channel policy is supported for go-live")
	}
	if auth.AllowedRiskClass == "" {
		auth.AllowedRiskClass = "GREEN"
	}
	if strings.ToUpper(auth.AllowedRiskClass) != "GREEN" {
		return nil, errx.New(errx.BadRequest, "allowed_risk_class must be GREEN")
	}
	if auth.EffectiveAt.IsZero() {
		auth.EffectiveAt = time.Now().UTC()
	}
	auth.AuthorizedBy = userID
	if auth.MaxRatePerHour < 1 {
		auth.MaxRatePerHour = s.cfg.RateMaxPerHour
		if auth.MaxRatePerHour < 1 {
			auth.MaxRatePerHour = 20
		}
	}
	if auth.PromptPolicyVersion == "" {
		auth.PromptPolicyVersion = PromptVersion
	}
	if auth.ValidatorVersion == "" {
		auth.ValidatorVersion = "confenge.validators.v1"
	}
	if auth.ContactPolicyVersion == "" {
		auth.ContactPolicyVersion = "confenge.contact.v1"
	}
	if _, err := s.policyStore.InsertCampaignPolicy(ctx, orgID, auth); err != nil {
		return nil, errx.New(errx.Internal, "persist campaign policy: "+err.Error())
	}
	if s.audit != nil {
		cid := auth.CampaignID
		s.audit.LogAction(ctx, orgID, userID, models.AuditActionUpdate, models.AuditEntityCampaign, &cid, "", "",
			nil, map[string]string{
				"authorization_mode": AuthorizationModeCampaignPolicy,
				"channel":            auth.Channel,
				"allowed_risk_class": auth.AllowedRiskClass,
				"sender_mailbox":     auth.SenderMailbox,
			})
	}
	return auth, nil
}

// GetActiveCampaignPolicy returns the current grant for a campaign if any.
func (s *service) GetActiveCampaignPolicy(ctx context.Context, orgID, campaignID uuid.UUID) (*models.CampaignPolicyAuthorization, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	if s.policyStore == nil {
		return nil, nil
	}
	auth, err := s.policyStore.GetActiveCampaignPolicy(ctx, orgID, campaignID, time.Now().UTC())
	if err != nil {
		return nil, errx.New(errx.Internal, "load campaign policy: "+err.Error())
	}
	return auth, nil
}

// TryGreenAutorun evaluates GREEN predicates and, when all pass, marks the
// touchpoint APPROVED with authorization_mode=CAMPAIGN_POLICY (no approved_by)
// and queues it for transport. YELLOW/RED never enter this path.
func (s *service) TryGreenAutorun(ctx context.Context, orgID, actorID, touchpointID uuid.UUID) (*models.OutreachTouchpoint, GreenAutorunDecision, *errx.Error) {
	dec := GreenAutorunDecision{Allow: false, Reasons: []string{"not_evaluated"}}
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, dec, xerr
	}
	if !s.cfg.GreenAutorunEnabled {
		dec = GreenAutorunDecision{Allow: false, Reasons: []string{"green_autorun_disabled"}}
		return nil, dec, nil
	}
	tp, err := s.repo.GetTouchpoint(ctx, orgID, touchpointID)
	if err != nil || tp == nil {
		return nil, dec, errx.New(errx.NotFound, "touchpoint not found")
	}
	if tp.State != models.TouchpointNeedsReview && tp.State != models.TouchpointDrafted && tp.State != models.TouchpointApproved {
		dec = GreenAutorunDecision{Allow: false, Reasons: []string{"state_" + tp.State}}
		return tp, dec, nil
	}

	campaignID := uuid.Nil
	if settings, _ := s.repo.GetOrgSettings(ctx, orgID); settings != nil && settings.CampaignID != nil {
		campaignID = *settings.CampaignID
	}
	if campaignID == uuid.Nil {
		dec = GreenAutorunDecision{Allow: false, Reasons: []string{"no_campaign_id"}}
		return tp, dec, nil
	}

	var auth *models.CampaignPolicyAuthorization
	if s.policyStore != nil {
		auth, err = s.policyStore.GetActiveCampaignPolicy(ctx, orgID, campaignID, time.Now().UTC())
		if err != nil {
			return tp, dec, errx.New(errx.Internal, "load policy: "+err.Error())
		}
	}
	in := s.buildGreenAutorunInput(ctx, orgID, tp)
	now := time.Now().UTC()
	dec = EvaluateGreenAutorun(s.cfg.GreenAutorunEnabled, auth, in, now)
	if !dec.Allow {
		return tp, dec, nil
	}

	if err := ApplyCampaignPolicyAuthorization(tp, now); err != nil {
		return tp, dec, errx.New(errx.BadRequest, err.Error())
	}
	if err := s.repo.UpdateTouchpoint(ctx, tp); err != nil {
		return tp, dec, errx.New(errx.Internal, "persist policy approval: "+err.Error())
	}
	// Queue without requiring human approved_by. actorID is used for enrollment audit only.
	queued, xerr := s.QueueTouchpoint(ctx, orgID, actorID, tp.ID)
	if xerr != nil {
		return tp, dec, xerr
	}
	return queued, dec, nil
}

// RunGreenAutorunBatch generates (if needed) then tries policy autoqueue for up
// to limit EMAIL touchpoints that are due for review. Ready-to-generate accounts
// without open touchpoints are planned+generated first so GREEN stock can form.
func (s *service) RunGreenAutorunBatch(ctx context.Context, orgID, actorID uuid.UUID, limit int) (queued, skipped int, details []map[string]any, xerr *errx.Error) {
	if xerr = s.requireEnabled(); xerr != nil {
		return 0, 0, nil, xerr
	}
	if !s.cfg.GreenAutorunEnabled {
		return 0, 0, nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	// Seed GREEN-eligible drafts: plan READY accounts and promote due/planned
	// ordinal-1 touches into NEEDS_REVIEW with policy-approved templates.
	seeded := 0
	if ready, err := s.repo.ListAccounts(ctx, orgID, repository.OutreachAccountFilter{
		QueueState: models.OutreachQueueReadyToGenerate, Limit: limit * 2,
	}); err == nil {
		for i := range ready {
			if seeded >= limit {
				break
			}
			acc := ready[i]
			if acc.DoNotContact || acc.Blocked {
				continue
			}
			if acc.ActivationState != "" && acc.ActivationState != "ACTIONABLE_NOW" {
				continue
			}
			existing, _ := s.repo.ListTouchpoints(ctx, orgID, acc.ID, "", 20, 0)
			var due *models.OutreachTouchpoint
			for j := range existing {
				t := existing[j]
				if t.Ordinal == 1 && (t.State == models.TouchpointDue || t.State == models.TouchpointPlanned ||
					t.State == models.TouchpointNeedsReview || t.State == models.TouchpointDrafted) {
					due = &existing[j]
					break
				}
			}
			if due == nil {
				var contactID *uuid.UUID
				if cands, _ := s.repo.ListCandidates(ctx, orgID, acc.ID); len(cands) > 0 {
					for j := range cands {
						if cands[j].CanEnroll() && strings.Contains(cands[j].Email, "@") && MailboxPurposeAllowed(cands[j].Email) {
							id := cands[j].ID
							contactID = &id
							break
						}
					}
				}
				if contactID == nil {
					continue
				}
				tps, px := s.PlanAccountCadence(ctx, orgID, actorID, acc.ID, contactID, models.OutreachChannelEmail)
				if px != nil || len(tps) == 0 {
					continue
				}
				due = &tps[0]
			}
			if due.State == models.TouchpointPlanned {
				due.State = models.TouchpointDue
				_ = s.repo.UpdateTouchpoint(ctx, due)
			}
			if gen, gx := s.GenerateTouchpointDraft(ctx, orgID, actorID, due.ID); gx == nil && gen != nil {
				seeded++
			}
		}
	}

	list, err := s.repo.ListReviewTouchpoints(ctx, orgID, limit, 0)
	if err != nil {
		return 0, 0, nil, errx.New(errx.Internal, "list review: "+err.Error())
	}
	details = make([]map[string]any, 0, len(list))
	for i := range list {
		tp := list[i]
		if strings.ToUpper(tp.Channel) != "EMAIL" {
			skipped++
			details = append(details, map[string]any{"id": tp.ID.String(), "allow": false, "reasons": []string{"channel_not_email"}})
			continue
		}
		// Re-generate stale YELLOW jit drafts under active policy so they can become policy GREEN.
		if tp.DraftID != nil {
			if d, _ := s.repo.GetDraft(ctx, orgID, *tp.DraftID); d != nil && d.RiskClass != "GREEN" {
				_, _ = s.GenerateTouchpointDraft(ctx, orgID, actorID, tp.ID)
			}
		} else if tp.State == models.TouchpointDue || tp.State == models.TouchpointNeedsReview {
			_, _ = s.GenerateTouchpointDraft(ctx, orgID, actorID, tp.ID)
		}
		out, dec, x := s.TryGreenAutorun(ctx, orgID, actorID, tp.ID)
		row := map[string]any{
			"id":                 tp.ID.String(),
			"allow":              dec.Allow,
			"reasons":            dec.Reasons,
			"authorization_mode": dec.AuthorizationMode,
		}
		if out != nil {
			row["state"] = out.State
			row["authorization_mode_stored"] = out.AuthorizationMode
			row["approved_by"] = out.ApprovedBy
		}
		if x != nil {
			row["error"] = x.Message
		}
		details = append(details, row)
		if x != nil || !dec.Allow {
			skipped++
			continue
		}
		queued++
	}
	return queued, skipped, details, nil
}

func (s *service) buildGreenAutorunInput(ctx context.Context, orgID uuid.UUID, tp *models.OutreachTouchpoint) GreenAutorunInput {
	in := GreenAutorunInput{
		Channel:                   strings.ToUpper(strings.TrimSpace(tp.Channel)),
		ContactFresh:              true,
		ContextFresh:              true,
		SingleService:             true,
		NoUnknownEvidenceIDs:      true,
		NoHypothesisAsFact:        true,
		NoClaimsToAvoidViolated:   true,
		ValidationOK:              true,
		MessageContextHashCurrent: true,
		NoEditAfterAuthorization:  true,
		CopyWithinLimits:          true,
		GovernorHealthy:           s.governor != nil || s.cfg.AppEnv == "dev" || s.cfg.AppEnv == "test" || s.cfg.AppEnv == "",
		InSendWindow:              true, // queue eligibility; final send still hits governor window
		ProviderHealthy:           true,
		ServiceCode:               tp.ServiceCode,
		RiskClass:                 "YELLOW", // fail-closed until draft proves GREEN
	}
	acc, _ := s.repo.GetAccount(ctx, orgID, tp.AccountID)
	var cand *models.OutreachContactCandidate
	if tp.ContactCandidateID != nil {
		cand, _ = s.repo.GetCandidate(ctx, orgID, *tp.ContactCandidateID)
	}
	if acc != nil {
		in.DNC = acc.DoNotContact
		in.Blocked = acc.Blocked
		in.ServiceCode = firstNonEmpty(tp.ServiceCode, acc.ServiceCode)
		in.SingleService = strings.TrimSpace(in.ServiceCode) != "" && !strings.Contains(in.ServiceCode, ",")
		in.FactualHookAnchored = strings.TrimSpace(firstNonEmpty(tp.FactUsed, acc.FactToMention)) != ""
		if acc.MessageContextHash != "" && tp.GeneratedContextHash != "" {
			in.MessageContextHashCurrent = acc.MessageContextHash == tp.GeneratedContextHash
			in.ContextFresh = in.MessageContextHashCurrent
		}
		// Target fit: ACTIONABLE_NOW with activation => A_AUTOMATIC; else scan reason codes.
		in.TargetFitSendTier = deriveTargetFitTier(acc)
		if acc.ActivationExpiresAt != nil && acc.ActivationExpiresAt.Before(time.Now().UTC()) {
			in.ContactFresh = false
		}
		// Claims_to_avoid: body must not contain forbidden claims literally.
		if len(acc.ClaimsToAvoid) > 0 {
			bodyLower := strings.ToLower(tp.BodyText)
			for _, c := range acc.ClaimsToAvoid {
				if c != "" && strings.Contains(bodyLower, strings.ToLower(c)) {
					in.NoClaimsToAvoidViolated = false
					break
				}
			}
		}
		// Replied / bounce via commercial state.
		switch strings.ToUpper(acc.CommercialState) {
		case "REPLIED", "MEETING", "PROPOSAL", "WON":
			in.Replied = true
		case "BOUNCED":
			in.Bounce = true
		}
		if acc.QueueState == models.OutreachQueueBounced {
			in.Bounce = true
		}
		if acc.QueueState == models.OutreachQueueReplied {
			in.Replied = true
		}
	}
	if cand != nil {
		in.OwnershipAllowed = cand.CanEnroll() || (!cand.Blocked && !cand.DoNotContact && cand.Email != "")
		in.VerificationAllowed = !models.OutreachUnenrollableVerification[cand.VerificationStatus]
		if cand.Bounced {
			in.Bounce = true
		}
		if cand.DoNotContact {
			in.DNC = true
		}
		in.EmailSendReady = cand.CanEnroll() && strings.TrimSpace(cand.Email) != ""
	} else {
		// Recipient on TP may still be a valid email path for enrollable mint-free cases.
		in.EmailSendReady = strings.Contains(tp.Recipient, "@")
		in.OwnershipAllowed = in.EmailSendReady
		in.VerificationAllowed = in.EmailSendReady
	}
	in.MailboxPurposeAllowed = MailboxPurposeAllowed(tp.Recipient)
	if !in.MailboxPurposeAllowed {
		in.EmailSendReady = false
	}
	// Draft risk class is authoritative for GREEN vs YELLOW/RED.
	if tp.DraftID != nil {
		if d, _ := s.repo.GetDraft(ctx, orgID, *tp.DraftID); d != nil {
			in.RiskClass = strings.ToUpper(strings.TrimSpace(d.RiskClass))
			if in.RiskClass == "" {
				in.RiskClass = "YELLOW"
			}
			if d.ValidationOK != nil {
				in.ValidationOK = *d.ValidationOK
			} else {
				in.ValidationOK = in.RiskClass == "GREEN"
			}
			// Generic unaudited template remains YELLOW path.
			if d.Provider == "template" && strings.Contains(d.Model, "jit") && in.RiskClass != "GREEN" {
				in.GenericUnauditedTemplate = true
			}
			// Policy-approved deterministic template may set UsedPolicyApprovedTemplate.
			if d.Provider == "template" && d.Model == "policy_approved_v1" {
				in.UsedPolicyApprovedTemplate = true
				in.GenericUnauditedTemplate = false
			}
		}
	}
	// Prefer feed/import activation: ACTIONABLE_NOW is send-tier A when reason codes omit explicit A/B.
	if in.TargetFitSendTier == "RESEARCH_ONLY" && acc != nil && acc.ActivationState == "ACTIONABLE_NOW" {
		in.TargetFitSendTier = "A_AUTOMATIC"
	}
	// Word limits for initial email.
	words := countWords(tp.BodyText)
	maxW := s.cfg.MaxInitialEmailWords
	if maxW < 1 {
		maxW = DefaultMaxInitialWords
	}
	in.CopyWithinLimits = words > 0 && words <= maxW+40 // small margin for signature
	if strings.TrimSpace(in.ServiceCode) == "" {
		in.SingleService = false
	}
	return in
}

func deriveTargetFitTier(acc *models.OutreachAccount) string {
	if acc == nil {
		return "RESEARCH_ONLY"
	}
	for _, c := range acc.ActivationReasonCodes {
		u := strings.ToUpper(strings.TrimSpace(c))
		if u == "A_AUTOMATIC" || strings.Contains(u, "A_AUTOMATIC") {
			return "A_AUTOMATIC"
		}
		if u == "B_EVIDENCE_SUPPORTED" || strings.Contains(u, "B_EVIDENCE") {
			return "B_EVIDENCE_SUPPORTED"
		}
		if u == "OUT_OF_SCOPE" || strings.Contains(u, "OUT_OF_SCOPE") {
			return "OUT_OF_SCOPE"
		}
	}
	if acc.ActivationState == "ACTIONABLE_NOW" {
		return "A_AUTOMATIC"
	}
	return "RESEARCH_ONLY"
}

// MailboxPurposeAllowed blocks HR/recruiting/support/privacy/noreply boxes from autorun.
func MailboxPurposeAllowed(email string) bool {
	local := strings.ToLower(strings.TrimSpace(email))
	if i := strings.Index(local, "@"); i > 0 {
		local = local[:i]
	}
	if local == "" {
		return false
	}
	blocked := []string{
		"vagas", "rh", "curriculo", "currículos", "carreiras", "jobs", "recruit", "recrutamento",
		"suporte", "sac", "support", "helpdesk",
		"privacidade", "dpo", "lgpd", "privacy",
		"noreply", "no-reply", "donotreply", "bounce", "mailer-daemon",
	}
	for _, b := range blocked {
		if local == b || strings.HasPrefix(local, b+".") || strings.HasPrefix(local, b+"-") || strings.HasPrefix(local, b+"_") {
			return false
		}
	}
	return true
}
