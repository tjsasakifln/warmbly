package intel

import (
	"strings"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

// ObserveFromInbound copies durable IDs from an inbound receipt plus
// optional extra-cli / Warmbly rows. Empty fields stay empty.
func ObserveFromInbound(lead models.OutreachInboundLead, acc *models.OutreachAccount, action *models.OutreachCommercialAction, outcome *models.OutreachOutcome) ObservedFacts {
	in := ObservedFacts{
		Keys: JoinKeys{
			OrganizationID: lead.OrganizationID.String(),
			Source:         strings.TrimSpace(lead.Source),
			Query:          utmQuery(lead.UTMJSON),
			AssetID:        strings.TrimSpace(lead.AssetID),
			CTAID:          strings.TrimSpace(lead.CTAID),
			CorrelationID:  strings.TrimSpace(lead.CorrelationID),
			LeadID:         strings.TrimSpace(lead.LeadID),
			ReceiptID:      strings.TrimSpace(lead.ReceiptID),
			AccountID:      strings.TrimSpace(lead.EntityID),
			PersonID:       strings.TrimSpace(lead.PersonID),
			EventIDs:       append([]string{}, lead.Evidence...),
			RouteFamily:    strings.TrimSpace(lead.RouteFamily),
		},
		LeadCreatedAt:  lead.LeadCreatedAt,
		IngestedAt:     lead.WarmblyIngestedAt,
		EnrichmentAt:   lead.EnrichmentCompletedAt,
		FirstActionAt:  lead.FirstActionAt,
		ConversationAt: lead.ConversationAt,
		ProposalAt:     lead.ProposalAt,
		CloseAt:        lead.CloseAt,
		Label:          LabelReal,
	}
	if acc != nil {
		if in.Keys.AccountID == "" {
			in.Keys.AccountID = strings.TrimSpace(acc.SourceLeadID)
		}
		in.Keys.SourceLeadID = strings.TrimSpace(acc.SourceLeadID)
		in.Keys.TargetFitVersion = strings.TrimSpace(acc.TargetFitVersion)
		in.Keys.ActivationPolicyVersion = strings.TrimSpace(acc.ActivationPolicyVersion)
		in.Keys.TargetFitWatermark = strings.TrimSpace(acc.TargetFitSourceWatermark)
		in.Keys.TargetFitFresh = acc.TargetFitFresh
		if in.Keys.Trigger == "" {
			in.Keys.Trigger = strings.TrimSpace(acc.MomentCode)
		}
		if in.Keys.OfferID == "" {
			in.Keys.OfferID = strings.TrimSpace(acc.EntryOffer)
		}
		if len(in.Keys.EventIDs) == 0 {
			in.Keys.EventIDs = append([]string{}, acc.MomentEvidenceIDs...)
		}
		if !acc.TargetFitFresh && strings.TrimSpace(acc.TargetFitFreshnessReason) != "" {
			in.RequiresFresh = true
		}
	}
	if action != nil {
		in.Keys.ActionID = action.ID.String()
		if in.Keys.IdempotencyKey == "" {
			in.Keys.IdempotencyKey = strings.TrimSpace(action.IdempotencyKey)
		}
		if in.Keys.PersonID == "" {
			in.Keys.PersonID = strings.TrimSpace(action.PersonID)
		}
		if in.Keys.Route == "" {
			in.Keys.Route = firstNonEmpty(action.ReachabilityClass, action.RouteType, action.ActionType)
		}
		if in.Keys.OfferID == "" {
			in.Keys.OfferID = strings.TrimSpace(action.ServiceCode)
		}
		if action.StartedAt != nil {
			in.ActionOccurredAt = *action.StartedAt
		} else if !action.CreatedAt.IsZero() {
			in.ActionOccurredAt = action.CreatedAt
		}
		if in.FirstActionAt == nil && !in.ActionOccurredAt.IsZero() {
			t := in.ActionOccurredAt
			in.FirstActionAt = &t
		}
		in.Conversation = action.ConversationStarted
		in.OutcomeType = strings.TrimSpace(action.OutcomeCode)
		if action.CompletedAt != nil {
			in.OutcomeOccurredAt = *action.CompletedAt
		}
		if isWonType(action.OutcomeCode) && strings.TrimSpace(action.HumanActor) != "" {
			in.HumanConfirmed = true
		}
		if action.RequiresFresh || strings.TrimSpace(action.StaleWarning) != "" {
			in.RequiresFresh = true
		}
	}
	if outcome != nil {
		if outcome.EventID != uuid.Nil {
			in.Keys.OutboxEventID = outcome.EventID.String()
		}
		if in.Keys.IdempotencyKey == "" {
			in.Keys.IdempotencyKey = strings.TrimSpace(outcome.IdempotencyKey)
		}
		if in.Keys.OutcomeID == "" {
			in.Keys.OutcomeID = outcome.ID.String()
		}
		if in.OutcomeType == "" {
			in.OutcomeType = strings.TrimSpace(outcome.EventType)
		}
		if in.OutcomeOccurredAt.IsZero() {
			in.OutcomeOccurredAt = outcome.OccurredAt
		}
	}
	in.Qualified = in.OutcomeType == OutcomeQualifiedConversation
	in.PipelineOpen = lead.Status == models.InboundStatusOpen
	return in
}

// ObserveFromAction copies an outbound/partner/expansion action with no
// inbound receipt. Route family stays UNKNOWN unless the caller sets it.
func ObserveFromAction(action models.OutreachCommercialAction, acc *models.OutreachAccount, family string) ObservedFacts {
	in := ObservedFacts{
		Keys: JoinKeys{
			OrganizationID: action.OrganizationID.String(),
			ActionID:       action.ID.String(),
			IdempotencyKey: strings.TrimSpace(action.IdempotencyKey),
			PersonID:       strings.TrimSpace(action.PersonID),
			SourceLeadID:   strings.TrimSpace(action.SourceLeadID),
			LeadID:         strings.TrimSpace(action.SourceLeadID),
			AccountID:      action.AccountID.String(),
			OfferID:        strings.TrimSpace(action.ServiceCode),
			Route:          firstNonEmpty(action.ReachabilityClass, action.RouteType, action.ActionType),
			RouteFamily:    family,
		},
		OutcomeType:   strings.TrimSpace(action.OutcomeCode),
		Conversation:  action.ConversationStarted,
		Label:         LabelReal,
		RequiresFresh: action.RequiresFresh || strings.TrimSpace(action.StaleWarning) != "",
	}
	if !action.CreatedAt.IsZero() {
		in.ActionOccurredAt = action.CreatedAt
		in.LeadCreatedAt = action.CreatedAt
		t := action.CreatedAt
		in.FirstActionAt = &t
	}
	if action.CompletedAt != nil {
		in.OutcomeOccurredAt = *action.CompletedAt
	}
	if isWonType(action.OutcomeCode) && strings.TrimSpace(action.HumanActor) != "" {
		in.HumanConfirmed = true
	}
	if acc != nil {
		in.Keys.AccountID = firstNonEmpty(acc.SourceLeadID, in.Keys.AccountID)
		in.Keys.SourceLeadID = firstNonEmpty(acc.SourceLeadID, in.Keys.SourceLeadID)
		in.Keys.TargetFitVersion = acc.TargetFitVersion
		in.Keys.ActivationPolicyVersion = acc.ActivationPolicyVersion
		in.Keys.TargetFitWatermark = acc.TargetFitSourceWatermark
		in.Keys.TargetFitFresh = acc.TargetFitFresh
		in.Keys.Trigger = firstNonEmpty(in.Keys.Trigger, acc.MomentCode)
		in.Keys.OfferID = firstNonEmpty(in.Keys.OfferID, acc.EntryOffer)
		in.Keys.EventIDs = append([]string{}, acc.MomentEvidenceIDs...)
	}
	in.Qualified = in.OutcomeType == OutcomeQualifiedConversation
	in.PipelineOpen = !isWonType(in.OutcomeType) && !isLostType(in.OutcomeType)
	return in
}

func utmQuery(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	s := string(raw)
	for _, key := range []string{`"query"`, `"utm_campaign"`, `"campaign"`, `"utm_source"`} {
		if i := strings.Index(s, key); i >= 0 {
			rest := s[i+len(key):]
			if j := strings.Index(rest, `"`); j >= 0 {
				rest = rest[j+1:]
				if k := strings.Index(rest, `"`); k >= 0 {
					return rest[:k]
				}
			}
		}
	}
	return ""
}
