package confenge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// InboundNowItem is one Monday-queue row. Work, not ornament.
type InboundNowItem struct {
	LeadID            string         `json:"lead_id"`
	ReceiptID         string         `json:"receipt_id"`
	Company           string         `json:"company"`
	Person            string         `json:"person,omitempty"`
	Origin            string         `json:"origin"`
	Asset             string         `json:"asset,omitempty"`
	ContractContext   string         `json:"contract_context,omitempty"`
	WhyNow            string         `json:"why_now"`
	RecommendedAction string         `json:"recommended_action"`
	Channel           string         `json:"channel,omitempty"`
	Evidence          []string       `json:"evidence,omitempty"`
	Owner             string         `json:"owner"`
	LeadAgeSeconds    int64          `json:"lead_age_seconds"`
	LeadAge           string         `json:"lead_age"`
	Status            string         `json:"status"`
	NextAction        string         `json:"next_action"`
	ActionID          string         `json:"action_id,omitempty"`
	AccountID         string         `json:"account_id,omitempty"`
	EmailSendable     bool           `json:"email_sendable"`
	Dispatchable      bool           `json:"dispatchable"`
	EnrichmentStatus  string         `json:"enrichment_status"`
	Warnings          []string       `json:"warnings,omitempty"`
	Latency           InboundLatency `json:"latency"`
}

// InboundLatency is the commercial-latency baseline. No minute SLA.
type InboundLatency struct {
	LeadCreatedAt         string `json:"lead_created_at"`
	WarmblyIngestedAt     string `json:"warmbly_ingested_at"`
	EnrichmentCompletedAt string `json:"enrichment_completed_at,omitempty"`
	OwnerAssignedAt       string `json:"owner_assigned_at,omitempty"`
	FirstActionAt         string `json:"first_action_at,omitempty"`
	ConversationAt        string `json:"conversation_at,omitempty"`
	ProposalAt            string `json:"proposal_at,omitempty"`
	CloseAt               string `json:"close_at,omitempty"`
}

// CollectInboundNow projects the inbound work queue.
func (s *service) CollectInboundNow(ctx context.Context, orgID uuid.UUID) ([]InboundNowItem, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	st := s.inboundStore()
	if st == nil {
		return []InboundNowItem{}, nil
	}
	leads, err := st.ListInboundLeads(ctx, orgID, false, 200)
	if err != nil {
		return nil, errx.New(errx.Internal, "list inbound leads: "+err.Error())
	}
	now := time.Now().UTC()
	out := make([]InboundNowItem, 0, len(leads))
	for i := range leads {
		item := ProjectInboundNowItem(leads[i], now)
		if s.actionStore() != nil && leads[i].ActionID != nil {
			if a, _ := s.actionStore().GetCommercialAction(ctx, orgID, *leads[i].ActionID); a != nil {
				item.RecommendedAction = firstNonEmpty(a.RecommendedAction, item.RecommendedAction)
				item.EmailSendable = a.EmailSendable
				item.Dispatchable = false
				if a.OutcomeCode != "" && item.NextAction == "" {
					item.NextAction = a.NextActionType
				}
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// ProjectInboundNowItem is the shipped projector used by tests and the cockpit.
func ProjectInboundNowItem(lead models.OutreachInboundLead, now time.Time) InboundNowItem {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	ageFrom := lead.LeadCreatedAt
	if ageFrom.IsZero() {
		ageFrom = lead.WarmblyIngestedAt
	}
	age := now.Sub(ageFrom)
	if age < 0 {
		age = 0
	}
	company := firstNonEmpty(lead.CompanyName, lead.CNPJ14, "UNKNOWN")
	item := InboundNowItem{
		LeadID:            lead.LeadID,
		ReceiptID:         firstNonEmpty(lead.ReceiptID, lead.LeadID),
		Company:           company,
		Person:            lead.PersonName,
		Origin:            firstNonEmpty(lead.Source, "web-cfg"),
		Asset:             lead.AssetID,
		ContractContext:   firstNonEmpty(lead.ContractID, lead.EntityID, truncateRunes(lead.Message, 160)),
		WhyNow:            firstNonEmpty(lead.WhyNow, "Lead inbound sem fato adicional."),
		RecommendedAction: inboundRecommended(lead),
		Channel:           lead.Channel,
		Evidence:          append([]string{}, lead.Evidence...),
		Owner:             firstNonEmpty(lead.Owner, models.InboundOwnerUnknown),
		LeadAgeSeconds:    int64(age.Seconds()),
		LeadAge:           formatLeadAge(age),
		Status:            firstNonEmpty(lead.Status, models.InboundStatusOpen),
		NextAction:        firstNonEmpty(lead.NextAction, models.InboundNextNeedsEnrichment),
		EmailSendable:     false,
		Dispatchable:      false,
		EnrichmentStatus:  firstNonEmpty(lead.EnrichmentStatus, models.InboundEnrichmentUnknown),
		Warnings:          append([]string{}, lead.Warnings...),
		Latency:           projectInboundLatency(lead),
	}
	if lead.ActionID != nil {
		item.ActionID = lead.ActionID.String()
	}
	if lead.AccountID != nil {
		item.AccountID = lead.AccountID.String()
	}
	return item
}

func inboundRecommended(lead models.OutreachInboundLead) string {
	if lead.SuppressReason != "" {
		return "Suprimido: " + lead.SuppressReason
	}
	switch lead.NextAction {
	case models.InboundNextCall:
		return "Ligar para o telefone observado."
	case models.InboundNextWhatsApp:
		return "WhatsApp manual no numero observado."
	case models.InboundNextSendEmail:
		return "Preparar e-mail em revisao. Nao auto-enviar."
	case models.InboundNextRoutedCall:
		return "Ligacao roteada. Sem canal direto publicado."
	case models.InboundNextManualOutreach:
		return "Abordagem manual com os fatos observados."
	case models.InboundNextSuppressed:
		return "Suprimido."
	default:
		return "Enriquecer identidade. Nao contatar no escuro."
	}
}

func projectInboundLatency(lead models.OutreachInboundLead) InboundLatency {
	return InboundLatency{
		LeadCreatedAt:         rfc3339OrEmpty(lead.LeadCreatedAt),
		WarmblyIngestedAt:     rfc3339OrEmpty(lead.WarmblyIngestedAt),
		EnrichmentCompletedAt: rfc3339Ptr(lead.EnrichmentCompletedAt),
		OwnerAssignedAt:       rfc3339Ptr(lead.OwnerAssignedAt),
		FirstActionAt:         rfc3339Ptr(lead.FirstActionAt),
		ConversationAt:        rfc3339Ptr(lead.ConversationAt),
		ProposalAt:            rfc3339Ptr(lead.ProposalAt),
		CloseAt:               rfc3339Ptr(lead.CloseAt),
	}
}

func rfc3339OrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func rfc3339Ptr(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func formatLeadAge(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// RecordInboundOutcome stamps a human-recorded commercial result on the
// inbound receipt and, when present, the linked commercial action.
func (s *service) RecordInboundOutcome(ctx context.Context, orgID, userID uuid.UUID, leadID string, req OutcomeRequest) (*OutcomeApply, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	st := s.inboundStore()
	if st == nil {
		return nil, errx.New(errx.Internal, "inbound lead store unavailable")
	}
	leadID = strings.TrimSpace(leadID)
	row, err := st.GetInboundLeadByLeadID(ctx, orgID, leadID)
	if err != nil || row == nil {
		return nil, errx.New(errx.NotFound, "inbound lead not found")
	}
	code := strings.ToUpper(strings.TrimSpace(req.OutcomeCode))
	if req.Now.IsZero() {
		req.Now = time.Now().UTC()
	}
	if row.ActionID != nil && s.actionStore() != nil {
		existing, _ := s.actionStore().GetCommercialAction(ctx, orgID, *row.ActionID)
		if existing != nil && existing.OutcomeCode == code {
			stampInboundLatency(row, code, req.Now)
			_ = st.UpdateInboundLead(ctx, row)
			return &OutcomeApply{Action: *existing}, nil
		}
	}

	var applied OutcomeApply
	if row.ActionID != nil {
		res, xerr := s.RecordCommercialOutcome(ctx, orgID, userID, *row.ActionID, req)
		if xerr != nil {
			return nil, xerr
		}
		applied = *res
	} else {
		applied = OutcomeApply{Action: models.OutreachCommercialAction{
			SourceLeadID: row.LeadID,
			OutcomeCode:  code,
			UpdatedAt:    req.Now,
		}}
	}

	stampInboundLatency(row, code, req.Now)
	if terminalInboundOutcome(code) {
		row.Status = models.InboundStatusClosed
	}
	if code == models.OutcomeDNCCode {
		row.Status = models.InboundStatusSuppressed
		row.SuppressReason = firstNonEmpty(row.SuppressReason, "DNC")
	}
	row.UpdatedAt = req.Now
	if err := st.UpdateInboundLead(ctx, row); err != nil {
		return nil, errx.New(errx.Internal, "persist inbound outcome: "+err.Error())
	}
	if row.ActionID == nil {
		s.enqueueInboundHumanOutcome(ctx, orgID, row, code)
	}
	return &applied, nil
}

func stampInboundLatency(row *models.OutreachInboundLead, code string, now time.Time) {
	switch code {
	case models.OutcomeAttempted, models.OutcomeContactedCode, models.OutcomeFollowUp,
		models.OutcomeTargetReached, models.OutcomeGatekeeperReached:
		if row.FirstActionAt == nil {
			t := now
			row.FirstActionAt = &t
		}
	}
	switch code {
	case models.OutcomeQualifiedConversation, models.OutcomeRepliedCode,
		models.OutcomeMeetingScheduled, models.OutcomeContactedCode, models.OutcomeInterested:
		if row.ConversationAt == nil {
			t := now
			row.ConversationAt = &t
		}
		if row.FirstActionAt == nil {
			t := now
			row.FirstActionAt = &t
		}
	}
	if code == models.OutcomeProposalCode && row.ProposalAt == nil {
		t := now
		row.ProposalAt = &t
	}
	if (code == models.OutcomeWonCode || code == models.OutcomeClientCode || code == models.OutcomeLostCode) && row.CloseAt == nil {
		t := now
		row.CloseAt = &t
	}
}

func terminalInboundOutcome(code string) bool {
	switch code {
	case models.OutcomeWonCode, models.OutcomeClientCode, models.OutcomeLostCode,
		models.OutcomeDNCCode, models.OutcomeNotInterested:
		return true
	}
	return false
}

func (s *service) enqueueInboundHumanOutcome(ctx context.Context, orgID uuid.UUID, row *models.OutreachInboundLead, code string) {
	eventType := mapOutcomeEventType(code)
	meta, _ := json.Marshal(map[string]any{
		"lead_id":     row.LeadID,
		"receipt_id":  row.ReceiptID,
		"outcome":     code,
		"next_action": row.NextAction,
		"source":      "inbound",
	})
	_ = s.EnqueueOutcome(ctx, orgID, models.OutreachOutcome{
		IdempotencyKey: "inbound_outcome:" + row.LeadID + ":" + code,
		SourceLeadID:   row.LeadID,
		CNPJ14:         row.CNPJ14,
		ContactEmail:   row.LeadEmail,
		EventType:      eventType,
		OccurredAt:     time.Now().UTC(),
		Payload:        meta,
	})
}
