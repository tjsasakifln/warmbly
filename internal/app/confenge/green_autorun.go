package confenge

import (
	"context"
	"fmt"
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

func (s *service) WireCohortAuth(store BoundedCohortStore) {
	if store == nil {
		store = NewMemoryCohortStore()
	}
	s.cohortStore = store
}

func (s *service) BindBoundedCohortGrant(auth *BoundedCohortAuthorization) error {
	if s == nil || s.cohortStore == nil {
		return fmt.Errorf("bounded cohort store not wired")
	}
	if auth == nil || auth.ID == uuid.Nil {
		return fmt.Errorf("bounded cohort grant missing")
	}
	s.cohortStore.Put(auth)
	return nil
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
		auth.ValidatorVersion = ValidatorVersionV1
	}
	if auth.ContactPolicyVersion == "" {
		auth.ContactPolicyVersion = ContactPolicyVersionV1
	}
	if auth.TemplatePolicyVersion == "" && auth.AllowPolicyTemplateGREEN {
		auth.TemplatePolicyVersion = TemplatePolicyVersionV1
	}
	id, err := s.policyStore.InsertCampaignPolicy(ctx, orgID, auth)
	if err != nil {
		return nil, errx.New(errx.Internal, "persist campaign policy: "+err.Error())
	}
	auth.ID = id
	if s.audit != nil {
		cid := auth.CampaignID
		s.audit.LogAction(ctx, orgID, userID, models.AuditActionUpdate, models.AuditEntityCampaign, &cid, "", "",
			nil, map[string]string{
				"authorization_mode": AuthorizationModeCampaignPolicy,
				"channel":            auth.Channel,
				"allowed_risk_class": auth.AllowedRiskClass,
				"sender_mailbox":     auth.SenderMailbox,
				"policy_auth_id":     auth.ID.String(),
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

// RevokeCampaignPolicy revokes the active grant; unsent CAMPAIGN_POLICY messages must revalidate and block.
func (s *service) RevokeCampaignPolicy(ctx context.Context, orgID, campaignID, actorID uuid.UUID) (bool, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return false, xerr
	}
	if s.policyStore == nil {
		return false, errx.New(errx.Internal, "campaign policy store not configured")
	}
	ok, err := s.policyStore.RevokeCampaignPolicy(ctx, orgID, campaignID, actorID, time.Now().UTC())
	if err != nil {
		return false, errx.New(errx.Internal, "revoke campaign policy: "+err.Error())
	}
	return ok, nil
}

// TryGreenAutorun never approves or queues. AUTO_SEND and bulk/green autorun
// are forbidden on the CONFENGE profile even if isolated env flips the flag.
// The predicate engine remains for audit/explain only.
func (s *service) TryGreenAutorun(ctx context.Context, orgID, actorID, touchpointID uuid.UUID) (*models.OutreachTouchpoint, GreenAutorunDecision, *errx.Error) {
	_ = actorID
	dec := GreenAutorunDecision{Allow: false, Reasons: []string{"individual_approval_required"}}
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, dec, xerr
	}
	tp, err := s.repo.GetTouchpoint(ctx, orgID, touchpointID)
	if err != nil || tp == nil {
		return nil, dec, errx.New(errx.NotFound, "touchpoint not found")
	}
	return tp, dec, nil
}

// RunGreenAutorunBatch never queues. Isolated env that flips
// CONFENGE_GREEN_AUTORUN_ENABLED cannot birth send jobs.
func (s *service) RunGreenAutorunBatch(ctx context.Context, orgID, actorID uuid.UUID, limit int) (queued, skipped int, details []map[string]any, xerr *errx.Error) {
	if xerr = s.requireEnabled(); xerr != nil {
		return 0, 0, nil, xerr
	}
	_ = actorID
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	list, err := s.repo.ListReviewTouchpoints(ctx, orgID, limit, 0)
	if err != nil {
		return 0, 0, nil, errx.New(errx.Internal, "list review: "+err.Error())
	}
	details = make([]map[string]any, 0, len(list))
	for i := range list {
		_, dec, x := s.TryGreenAutorun(ctx, orgID, actorID, list[i].ID)
		row := map[string]any{
			"id":      list[i].ID.String(),
			"allow":   false,
			"reasons": dec.Reasons,
		}
		if x != nil {
			row["error"] = x.Message
		}
		details = append(details, row)
		skipped++
	}
	return 0, skipped, details, nil
}

// ImportedSendTierEligible reports whether an imported target_fit_send_tier may
// enter CAMPAIGN_POLICY. Empty/legacy tier is never promoted to A_AUTOMATIC.
func ImportedSendTierEligible(tier string) bool {
	u := strings.ToUpper(strings.TrimSpace(tier))
	return u == "A_AUTOMATIC" || u == "B_EVIDENCE_SUPPORTED"
}

// buildGreenAutorunInput constructs predicates fail-closed: critical booleans
// start false and are set true only from persisted/verified evidence.
// Never infers ownership/verification/email_send_ready from recipient syntax.
func (s *service) buildGreenAutorunInput(ctx context.Context, orgID uuid.UUID, tp *models.OutreachTouchpoint, auth *models.CampaignPolicyAuthorization) GreenAutorunInput {
	in := GreenAutorunInput{
		Channel: strings.ToUpper(strings.TrimSpace(tp.Channel)),
		// Fail-closed defaults: absence of proof = not authorized.
		EmailSendReady:            false,
		TargetFitSendTier:         "",
		OwnershipAllowed:          false,
		MailboxPurposeAllowed:     false,
		VerificationAllowed:       false,
		ContactFresh:              false,
		ContextFresh:              false,
		SingleService:             false,
		NoUnknownEvidenceIDs:      false,
		NoHypothesisAsFact:        false,
		NoClaimsToAvoidViolated:   false,
		ValidationOK:              false,
		MessageContextHashCurrent: false,
		// Fail-closed: proven only when no prior approval, or content matches approved hash.
		NoEditAfterAuthorization: false,
		CopyWithinLimits:         false,
		GovernorHealthy:          s.governor != nil,
		HasContactCandidate:      tp.ContactCandidateID != nil && *tp.ContactCandidateID != uuid.Nil,
		RiskClass:                "YELLOW", // fail-closed until draft proves GREEN
		RuntimePromptVersion:     PromptVersion,
		RuntimeValidatorVersion:  ValidatorVersionV1,
		RuntimeContactPolicy:     ContactPolicyVersionV1,
		ServiceCode:              tp.ServiceCode,
	}
	if auth != nil {
		in.RuntimeSenderMailbox = strings.TrimSpace(auth.SenderMailbox)
	}
	acc, _ := s.repo.GetAccount(ctx, orgID, tp.AccountID)
	var cand *models.OutreachContactCandidate
	if tp.ContactCandidateID != nil {
		cand, _ = s.repo.GetCandidate(ctx, orgID, *tp.ContactCandidateID)
	}

	// ── Contact candidate path only for CAMPAIGN_POLICY (no email-syntax fallback)
	if cand != nil {
		// email_send_ready must come from imported field OR explicit CanEnroll +
		// imported true. Syntax alone never grants readiness for autorun.
		in.EmailSendReady = cand.EmailSendReady && cand.CanEnroll() && strings.TrimSpace(cand.Email) != ""
		// Ownership: prefer imported ownership_status; else fall back to CanEnroll only
		// when verification is enrollable (still no syntax-only path without candidate).
		own := strings.ToUpper(strings.TrimSpace(cand.OwnershipStatus))
		switch own {
		case "COMPANY_OWNED", "HUMAN_CONFIRMED":
			in.OwnershipAllowed = !cand.Blocked && !cand.DoNotContact
		case "":
			// Legacy candidate without ownership_status: allow only full CanEnroll
			// (human review path still works; autorun still needs email_send_ready).
			in.OwnershipAllowed = cand.CanEnroll()
		default:
			in.OwnershipAllowed = false
		}
		in.VerificationAllowed = !models.OutreachUnenrollableVerification[cand.VerificationStatus]
		if cand.Bounced {
			in.Bounce = true
		}
		if cand.DoNotContact {
			in.DNC = true
		}
		if cand.MailboxPurposeSendBlocked {
			in.MailboxPurposeAllowed = false
		} else if cand.MailboxPurpose != "" {
			in.MailboxPurposeAllowed = !cand.MailboxPurposeSendBlocked && MailboxPurposeAllowed(cand.Email)
		} else {
			// No imported purpose: use local-part heuristic only as a block list, not as readiness.
			in.MailboxPurposeAllowed = MailboxPurposeAllowed(firstNonEmpty(cand.Email, tp.Recipient))
		}
	}
	// No candidate → EmailSendReady/Ownership/Verification stay false (fail-closed).

	if acc != nil {
		in.DNC = in.DNC || acc.DoNotContact
		in.Blocked = acc.Blocked
		in.ServiceCode = firstNonEmpty(tp.ServiceCode, acc.ServiceCode)
		in.SingleService = strings.TrimSpace(in.ServiceCode) != "" && !strings.Contains(in.ServiceCode, ",")
		in.FactualHookAnchored = strings.TrimSpace(firstNonEmpty(tp.FactUsed, acc.FactToMention)) != ""

		// Target fit: ONLY imported field. Never promote ACTIONABLE_NOW → A_AUTOMATIC.
		in.TargetFitSendTier = strings.ToUpper(strings.TrimSpace(acc.TargetFitSendTier))
		// Account-level email_send_ready is a weak rollup; contact-level still required.
		if !in.EmailSendReady && acc.EmailSendReady && cand != nil {
			// Do not upgrade solely from account rollup without candidate flag.
			// Contact flag is authoritative for CAMPAIGN_POLICY.
		}

		// Contact freshness: expires_at when present; if absent and activation set, treat fresh.
		if acc.ActivationExpiresAt != nil {
			in.ContactFresh = acc.ActivationExpiresAt.After(time.Now().UTC())
		} else if acc.ActivationState != "" {
			in.ContactFresh = true
		} else {
			// No activation projection: allow human review; for autorun fail-closed.
			in.ContactFresh = false
		}

		// Context freshness requires both hashes present and equal.
		if acc.MessageContextHash != "" && tp.GeneratedContextHash != "" {
			in.MessageContextHashCurrent = acc.MessageContextHash == tp.GeneratedContextHash
			in.ContextFresh = in.MessageContextHashCurrent
		}

		// Claims_to_avoid: body must not contain forbidden claims.
		in.NoClaimsToAvoidViolated = true
		if len(acc.ClaimsToAvoid) > 0 {
			bodyLower := strings.ToLower(tp.BodyText)
			for _, c := range acc.ClaimsToAvoid {
				if c != "" && strings.Contains(bodyLower, strings.ToLower(c)) {
					in.NoClaimsToAvoidViolated = false
					break
				}
			}
		}

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

		// Evidence IDs on touchpoint must be subset of account evidence when listed.
		if evid, _ := s.repo.ListEvidence(ctx, orgID, acc.ID); len(tp.EvidenceIDs) == 0 {
			// No evidence IDs claimed → no unknown IDs.
			in.NoUnknownEvidenceIDs = true
			in.NoHypothesisAsFact = true
		} else {
			known := map[string]string{}
			for i := range evid {
				known[evid[i].SourceEvidenceID] = evid[i].EpistemicClass
			}
			in.NoUnknownEvidenceIDs = true
			in.NoHypothesisAsFact = true
			for _, id := range tp.EvidenceIDs {
				ep, ok := known[id]
				if !ok {
					in.NoUnknownEvidenceIDs = false
					continue
				}
				if ep == models.OutreachEpistemicCommercialHypothesis || ep == models.OutreachEpistemicWeakInference {
					// Hypothesis may exist but must not be treated as fact in GREEN path:
					// if fact_used is empty while only hypothesis evidence, fail.
					if strings.TrimSpace(tp.FactUsed) == "" {
						in.NoHypothesisAsFact = false
					}
				}
			}
		}
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
			}
			// Generic unaudited template remains YELLOW path.
			if d.Provider == "template" && strings.Contains(d.Model, "jit") && in.RiskClass != "GREEN" {
				in.GenericUnauditedTemplate = true
			}
			if d.Provider == "template" && d.Model == TemplatePolicyVersionV1 {
				in.UsedPolicyApprovedTemplate = true
				in.GenericUnauditedTemplate = false
				in.RuntimeTemplateVersion = TemplatePolicyVersionV1
			}
			if d.PromptVersion != "" {
				in.RuntimePromptVersion = d.PromptVersion
			}
		}
	}

	// NoEditAfterAuthorization: proven only when there is no prior approval, or
	// content_hash still equals approved_content_hash (no silent material edit).
	if strings.TrimSpace(tp.ApprovedContentHash) == "" {
		// Not yet authorized: no post-authorization edit possible.
		in.NoEditAfterAuthorization = true
	} else {
		if tp.ContentHash == "" {
			RecomputeContentHash(tp)
		}
		in.NoEditAfterAuthorization = tp.ContentHash != "" && tp.ContentHash == tp.ApprovedContentHash
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

// MailboxPurposeAllowed blocks HR/recruiting/support/privacy/noreply boxes from autorun.
// This is a negative block list only; it never grants email_send_ready by itself.
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
