package confenge

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// inboundLeadStore is implemented by the Postgres outreach repo and memRepo.
type inboundLeadStore interface {
	InsertInboundLead(ctx context.Context, lead *models.OutreachInboundLead) (created bool, existing *models.OutreachInboundLead, err error)
	UpdateInboundLead(ctx context.Context, lead *models.OutreachInboundLead) error
	GetInboundLeadByLeadID(ctx context.Context, orgID uuid.UUID, leadID string) (*models.OutreachInboundLead, error)
	ListInboundLeads(ctx context.Context, orgID uuid.UUID, openOnly bool, limit int) ([]models.OutreachInboundLead, error)
	FindRecentInboundByIdentity(ctx context.Context, orgID uuid.UUID, identityKey string, since time.Time, excludeLeadID string) (*models.OutreachInboundLead, error)
}

func (s *service) inboundStore() inboundLeadStore {
	if st, ok := s.repo.(inboundLeadStore); ok {
		return st
	}
	return nil
}

// IngestInboundLead is the shipped receive path. The receipt is persisted
// before enrichment. Replaying the same lead_id never creates a second action.
func (s *service) IngestInboundLead(ctx context.Context, orgID uuid.UUID, raw []byte, opts IngestOptions) (*InboundIngestResult, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	if xerr := RejectInboundQueryPII(opts.Query); xerr != nil {
		return nil, xerr
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	parsed, xerr := ParseInboundLead(raw, now)
	if xerr != nil {
		return nil, xerr
	}
	st := s.inboundStore()
	if st == nil {
		return nil, errx.New(errx.Internal, "inbound lead store unavailable")
	}

	row := inboundRowFromParsed(orgID, parsed, raw, now)
	created, existing, err := st.InsertInboundLead(ctx, row)
	if err != nil {
		return nil, errx.New(errx.Internal, "persist inbound receipt: "+err.Error())
	}
	if !created && existing != nil {
		res := &InboundIngestResult{
			Lead:              existing,
			Duplicate:         true,
			EnrichmentStatus:  existing.EnrichmentStatus,
			NextAction:        existing.NextAction,
			DispatchAttempted: false,
		}
		if existing.ActionID != nil && s.actionStore() != nil {
			if a, _ := s.actionStore().GetCommercialAction(ctx, orgID, *existing.ActionID); a != nil {
				res.Action = a
			}
		}
		return res, nil
	}

	res := &InboundIngestResult{Lead: row, DispatchAttempted: false}
	facts, class, enrichErr := s.enrichAndClassifyInbound(ctx, orgID, parsed, opts)
	if enrichErr != nil {
		row.EnrichmentStatus = models.InboundEnrichmentFailed
		row.Warnings = appendUnique(row.Warnings, "enrichment_failed: "+enrichErr.Error())
		row.NextAction = models.InboundNextNeedsEnrichment
		row.WhyNow = defaultInboundWhy(parsed)
		row.UpdatedAt = now
		_ = st.UpdateInboundLead(ctx, row)
		res.Lead = row
		res.EnrichmentStatus = row.EnrichmentStatus
		res.NextAction = row.NextAction
		s.enqueueInboundImported(ctx, orgID, row)
		return res, nil
	}

	applyClassificationToLead(row, parsed, facts, class, now)
	s.assignInboundOwner(ctx, orgID, row, now)

	if peer := s.secondaryDedupe(ctx, orgID, row, now); peer != nil {
		row.DedupeOfLeadID = peer.LeadID
		if peer.ActionID != nil {
			row.ActionID = peer.ActionID
		}
		if peer.AccountID != nil && row.AccountID == nil {
			row.AccountID = peer.AccountID
		}
		row.UpdatedAt = now
		_ = st.UpdateInboundLead(ctx, row)
		res.Lead = row
		res.SecondaryDedupe = true
		res.EnrichmentStatus = row.EnrichmentStatus
		res.NextAction = row.NextAction
		if row.ActionID != nil && s.actionStore() != nil {
			if a, _ := s.actionStore().GetCommercialAction(ctx, orgID, *row.ActionID); a != nil {
				res.Action = a
			}
		}
		s.enqueueInboundImported(ctx, orgID, row)
		return res, nil
	}

	if class.Status != models.InboundStatusSuppressed && !opts.SkipCommercialAction {
		acc, cand := s.ensureInboundAccount(ctx, orgID, parsed, facts)
		if acc != nil {
			id := acc.ID
			row.AccountID = &id
		}
		if cand != nil {
			id := cand.ID
			row.CandidateID = &id
		}
		if acc != nil && s.actionStore() != nil {
			action := buildInboundAction(orgID, acc, cand, parsed, facts, class, now)
			if err := s.actionStore().UpsertCommercialAction(ctx, &action); err == nil {
				id := action.ID
				row.ActionID = &id
				res.Action = &action
			}
		}
	}

	row.UpdatedAt = now
	if err := st.UpdateInboundLead(ctx, row); err != nil {
		return nil, errx.New(errx.Internal, "update inbound lead: "+err.Error())
	}
	res.Lead = row
	res.EnrichmentStatus = row.EnrichmentStatus
	res.NextAction = row.NextAction
	s.enqueueInboundImported(ctx, orgID, row)
	if s.audit != nil {
		eid := row.ID
		s.audit.LogAction(ctx, orgID, uuid.Nil, models.AuditActionCreate, models.AuditEntityOutreachInboundLead, &eid, "", "",
			map[string]string{"action": "inbound_ingest", "lead_id": row.LeadID, "next_action": row.NextAction},
			map[string]string{"source": row.Source},
		)
	}
	return res, nil
}

func (s *service) enrichAndClassifyInbound(ctx context.Context, orgID uuid.UUID, lead InboundLeadV1, opts IngestOptions) (InboundFacts, InboundClassification, error) {
	var acc *models.OutreachAccount
	var cands []models.OutreachContactCandidate
	var ev []models.OutreachEvidence
	if !opts.EnrichmentUnavailable {
		acc = s.lookupInboundAccount(ctx, orgID, lead)
		if acc != nil {
			cands, _ = s.repo.ListCandidates(ctx, orgID, acc.ID)
			ev, _ = s.repo.ListEvidence(ctx, orgID, acc.ID)
		}
		if acc == nil && lead.Email != "" {
			if c, a, err := s.repo.FindCandidateByEmail(ctx, orgID, lead.Email); err == nil && a != nil {
				acc = a
				if c != nil {
					cands = []models.OutreachContactCandidate{*c}
				}
				ev, _ = s.repo.ListEvidence(ctx, orgID, acc.ID)
			}
		}
		if acc == nil && lead.Phone != "" {
			if c, a, err := s.repo.FindCandidateByPhone(ctx, orgID, lead.Phone); err == nil && a != nil {
				acc = a
				if c != nil {
					cands = []models.OutreachContactCandidate{*c}
				}
			}
		}
	}
	facts := EnrichInboundFacts(lead, acc, cands, ev, opts.EnrichmentUnavailable)
	class := ClassifyInboundNextAction(lead, facts)
	return facts, class, nil
}

func (s *service) lookupInboundAccount(ctx context.Context, orgID uuid.UUID, lead InboundLeadV1) *models.OutreachAccount {
	if cnpj := NormalizeCNPJ14(lead.CNPJ); cnpj != "" {
		if acc, err := s.repo.GetAccountByCNPJ(ctx, orgID, cnpj); err == nil && acc != nil {
			return acc
		}
	}
	if lead.EntityID != "" {
		if accs, err := s.repo.ListAccounts(ctx, orgID, repository.OutreachAccountFilter{Limit: 500}); err == nil {
			for i := range accs {
				if accs[i].SourceLeadID == lead.EntityID {
					return &accs[i]
				}
			}
		}
	}
	return nil
}

func (s *service) secondaryDedupe(ctx context.Context, orgID uuid.UUID, row *models.OutreachInboundLead, now time.Time) *models.OutreachInboundLead {
	st := s.inboundStore()
	if st == nil || row.IdentityKey == "" {
		return nil
	}
	since := now.Add(-inboundDedupeWindow)
	peer, err := st.FindRecentInboundByIdentity(ctx, orgID, row.IdentityKey, since, row.LeadID)
	if err != nil || peer == nil {
		return nil
	}
	if peer.ActionID == nil && peer.Status == models.InboundStatusSuppressed {
		return nil
	}
	return peer
}

func (s *service) assignInboundOwner(ctx context.Context, orgID uuid.UUID, row *models.OutreachInboundLead, now time.Time) {
	row.Owner = models.InboundOwnerUnknown
	if owner, err := s.repo.GetOrgOwnerUserID(ctx, orgID); err == nil && owner != uuid.Nil {
		row.Owner = owner.String()
		t := now
		row.OwnerAssignedAt = &t
	}
}

func (s *service) ensureInboundAccount(ctx context.Context, orgID uuid.UUID, lead InboundLeadV1, facts InboundFacts) (*models.OutreachAccount, *models.OutreachContactCandidate) {
	acc := s.lookupInboundAccount(ctx, orgID, lead)
	if acc == nil && lead.CNPJ != "" {
		acc = &models.OutreachAccount{
			OrganizationID: orgID,
			SourceLeadID:   lead.LeadID,
			CNPJ14:         lead.CNPJ,
			RazaoSocial:    lead.CompanyName,
			NomeFantasia:   lead.CompanyName,
			QueueState:     models.OutreachQueueNeedsContact,
			SourceSystem:   "web-cfg",
			MomentSummary:  defaultInboundWhy(lead),
			FactToMention:  "",
			CTA:            lead.CTAID,
		}
		if _, err := s.repo.UpsertAccount(ctx, acc); err != nil {
			return nil, nil
		}
	}
	if acc == nil {
		return nil, nil
	}
	cands, _ := s.repo.ListCandidates(ctx, orgID, acc.ID)
	if existing := pickInboundCandidate(lead, cands); existing != nil && (lead.Email == "" || strings.EqualFold(existing.Email, lead.Email) || lead.Name == "") {
		return acc, existing
	}
	if lead.Name == "" && lead.Email == "" && lead.Phone == "" {
		return acc, pickInboundCandidate(lead, cands)
	}
	cand := &models.OutreachContactCandidate{
		OrganizationID:     orgID,
		AccountID:          acc.ID,
		SourceContactID:    "web-cfg:" + lead.LeadID,
		Name:               lead.Name,
		Email:              lead.Email,
		Phone:              lead.Phone,
		VerificationStatus: models.OutreachVerifyCandidateUnverified,
		Confidence:         "LEAD_SUPPLIED",
		Recommended:        true,
		PersonID:           facts.PersonID,
	}
	if _, err := s.repo.UpsertCandidate(ctx, cand); err != nil {
		return acc, pickInboundCandidate(lead, cands)
	}
	return acc, cand
}

func inboundRowFromParsed(orgID uuid.UUID, lead InboundLeadV1, raw []byte, now time.Time) *models.OutreachInboundLead {
	consent, _ := json.Marshal(lead.Consent)
	if len(consent) == 0 {
		consent = []byte("{}")
	}
	utm, _ := json.Marshal(lead.UTM)
	if len(utm) == 0 {
		utm = []byte("{}")
	}
	payload := raw
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	return &models.OutreachInboundLead{
		ID:                uuid.New(),
		OrganizationID:    orgID,
		LeadID:            lead.LeadID,
		ReceiptID:         firstNonEmpty(lead.ReceiptID, lead.LeadID),
		IdentityKey:       inboundIdentityKey(lead.CNPJ, lead.Email, lead.Phone),
		LeadCreatedAt:     lead.CreatedAt,
		WarmblyIngestedAt: now,
		Source:            firstNonEmpty(lead.Source, "web-cfg"),
		RouteFamily:       lead.RouteFamily,
		AssetID:           lead.AssetID,
		CTAID:             lead.CTAID,
		LandingURL:        lead.LandingURL,
		ContractID:        lead.ContractID,
		EntityID:          lead.EntityID,
		CNPJ14:            lead.CNPJ,
		CompanyName:       lead.CompanyName,
		LeadName:          lead.Name,
		LeadEmail:         lead.Email,
		LeadPhone:         lead.Phone,
		Referrer:          lead.Referrer,
		Message:           lead.Message,
		CorrelationID:     lead.CorrelationID,
		ConsentJSON:       consent,
		UTMJSON:           utm,
		RawPayload:        payload,
		EnrichmentStatus:  models.InboundEnrichmentUnknown,
		Owner:             models.InboundOwnerUnknown,
		Status:            models.InboundStatusOpen,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func applyClassificationToLead(row *models.OutreachInboundLead, lead InboundLeadV1, facts InboundFacts, class InboundClassification, now time.Time) {
	row.EnrichmentStatus = facts.Status
	if facts.Status == models.InboundEnrichmentCompleted || facts.Status == models.InboundEnrichmentUnavailable || facts.Status == models.InboundEnrichmentFailed {
		t := now
		row.EnrichmentCompletedAt = &t
	}
	row.NextAction = class.NextAction
	row.Channel = class.Channel
	row.WhyNow = class.WhyNow
	row.Status = class.Status
	row.SuppressReason = class.SuppressReason
	row.PersonID = facts.PersonID
	row.PersonName = firstNonEmpty(facts.PersonName, lead.Name)
	if facts.GenericMailbox && !leadSuppliedName(lead, row.PersonName) && !facts.NamedHuman {
		row.PersonName = ""
	}
	row.Evidence = append([]string{}, facts.Evidence...)
	row.Provenance = append([]string{}, facts.Provenance...)
	row.Warnings = append([]string{}, facts.Warnings...)
	row.Warnings = append(row.Warnings, class.Warnings...)
	if facts.AccountID != "" {
		if id, err := uuid.Parse(facts.AccountID); err == nil {
			row.AccountID = &id
		}
	}
}

func buildInboundAction(orgID uuid.UUID, acc *models.OutreachAccount, cand *models.OutreachContactCandidate, lead InboundLeadV1, facts InboundFacts, class InboundClassification, now time.Time) models.OutreachCommercialAction {
	a := models.OutreachCommercialAction{
		OrganizationID:    orgID,
		AccountID:         acc.ID,
		SourceLeadID:      lead.LeadID,
		CompanyName:       firstNonEmpty(lead.CompanyName, accName(acc)),
		PersonName:        facts.PersonName,
		PersonID:          facts.PersonID,
		ObservedRole:      facts.Role,
		TargetRole:        facts.Role,
		ActionType:        firstNonEmpty(class.ActionType, models.ActionOtherManual),
		ReachabilityClass: facts.ReachabilityClass,
		MappingVersion:    models.ReachabilityMappingVersionV1,
		RouteType:         firstNonEmpty(facts.RouteType, class.Channel),
		RouteRelation:     facts.RouteRelation,
		ChannelValue:      firstNonEmpty(facts.ChannelValue, lead.Phone, lead.Email),
		ChannelDisplay:    firstNonEmpty(facts.ChannelDisplay, class.Channel),
		WhyNow:            class.WhyNow,
		FactualHook:       firstNonEmpty(facts.WhyNow, class.WhyNow),
		RecommendedAction: class.RecommendedAction,
		EvidenceIDs:       append([]string{}, facts.Evidence...),
		Warnings:          append([]string{}, class.Warnings...),
		State:             models.ActionStateReady,
		Lane:              firstNonEmpty(class.Lane, models.LaneInboundNow),
		Actionable:        class.Actionable,
		EmailSendable:     false,
		Dispatchable:      false,
		IdempotencyKey:    inboundActionIdempotency(lead.LeadID),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if cand != nil && cand.ID != uuid.Nil {
		id := cand.ID
		a.CandidateID = &id
	}
	a.RouteFingerprint = routeFingerprint(a.ActionType, a.RouteType, a.RouteRelation, a.ChannelValue, a.PersonName)
	a.ID = DeterministicActionID(orgID, acc.ID, a.ActionType, a.IdempotencyKey)
	return a
}

func (s *service) enqueueInboundImported(ctx context.Context, orgID uuid.UUID, row *models.OutreachInboundLead) {
	meta, _ := json.Marshal(map[string]any{
		"lead_id":           row.LeadID,
		"receipt_id":        row.ReceiptID,
		"next_action":       row.NextAction,
		"enrichment_status": row.EnrichmentStatus,
		"source":            row.Source,
		"correlation_id":    row.CorrelationID,
		"secondary_dedupe":  row.DedupeOfLeadID != "",
	})
	_ = s.EnqueueOutcome(ctx, orgID, models.OutreachOutcome{
		IdempotencyKey: "inbound_imported:" + row.LeadID,
		SourceLeadID:   row.LeadID,
		CNPJ14:         row.CNPJ14,
		ContactEmail:   row.LeadEmail,
		EventType:      OutcomeLeadImported,
		OccurredAt:     row.WarmblyIngestedAt,
		Payload:        meta,
	})
}
