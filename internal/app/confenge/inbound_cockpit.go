package confenge

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// InboundNowItem is one Monday-queue row. Work, not ornament.
// Missing facts render as UNKNOWN. Email is not required.
type InboundNowItem struct {
	LeadID              string               `json:"lead_id"`
	ReceiptID           string               `json:"receipt_id"`
	Company             string               `json:"company"`
	Person              string               `json:"person"`
	Origin              string               `json:"origin"`
	Asset               string               `json:"asset"`
	Query               string               `json:"query"`
	CTA                 string               `json:"cta"`
	Trigger             string               `json:"trigger"`
	Offer               string               `json:"offer"`
	EntityID            string               `json:"entity_id"`
	PersonID            string               `json:"person_id"`
	CorrelationID       string               `json:"correlation_id"`
	ContractContext     string               `json:"contract_context"`
	WhyNow              string               `json:"why_now"`
	RecommendedAction   string               `json:"recommended_action"`
	Channel             string               `json:"channel"`
	Reachability        string               `json:"reachability"`
	Freshness           string               `json:"freshness"`
	Confidence          string               `json:"confidence"`
	Evidence            []string             `json:"evidence,omitempty"`
	Owner               string               `json:"owner"`
	LeadAgeSeconds      int64                `json:"lead_age_seconds"`
	LeadAge             string               `json:"lead_age"`
	Status              string               `json:"status"`
	NextAction          string               `json:"next_action"`
	ActionID            string               `json:"action_id,omitempty"`
	AccountID           string               `json:"account_id,omitempty"`
	EmailSendable       bool                 `json:"email_sendable"`
	Dispatchable        bool                 `json:"dispatchable"`
	EnrichmentStatus    string               `json:"enrichment_status"`
	Warnings            []string             `json:"warnings,omitempty"`
	SuggestedCopy       string               `json:"suggested_copy,omitempty"`
	SuggestedCopyRoute  string               `json:"suggested_copy_route,omitempty"`
	SuggestedCopyReview string               `json:"suggested_copy_review"`
	Latency             InboundLatency       `json:"latency"`
	AlertID             string               `json:"alert_id,omitempty"`
	AlertState          string               `json:"alert_state,omitempty"`
	AlertBand           string               `json:"alert_band,omitempty"`
	Synthetic           bool                 `json:"synthetic,omitempty"`
	AcknowledgedAt      string               `json:"acknowledged_at,omitempty"`
	AcknowledgedBy      string               `json:"acknowledged_by,omitempty"`
	ReceivedAgo         string               `json:"received_ago,omitempty"`
	AlertFailureCode    string               `json:"alert_failure_code,omitempty"`
	FirstActionType     string               `json:"first_action_type,omitempty"`
	ResolutionReason    string               `json:"resolution_reason,omitempty"`
	AlertLatency        OperatorAlertLatency `json:"alert_latency,omitempty"`
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

// CollectInboundNow projects the inbound work queue. Default excludes synthetic.
func (s *service) CollectInboundNow(ctx context.Context, orgID uuid.UUID) ([]InboundNowItem, *errx.Error) {
	return s.CollectInboundNowFiltered(ctx, orgID, false)
}

func (s *service) CollectInboundNowFiltered(ctx context.Context, orgID uuid.UUID, includeSynthetic bool) ([]InboundNowItem, *errx.Error) {
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
	alerts := map[string]models.OutreachOperatorAlert{}
	if ast := s.alertStore(); ast != nil {
		if listed, lerr := ast.ListOperatorAlerts(ctx, orgID, true, 400); lerr == nil {
			for i := range listed {
				alerts[listed[i].LeadID] = listed[i]
			}
		}
	}
	out := make([]InboundNowItem, 0, len(leads))
	for i := range leads {
		skip := InboundCommercialSkipReason(leads[i])
		if skip != "" && !includeSynthetic {
			continue
		}
		var acc *models.OutreachAccount
		var action *models.OutreachCommercialAction
		if leads[i].AccountID != nil {
			acc, _ = s.repo.GetAccount(ctx, orgID, *leads[i].AccountID)
		}
		if s.actionStore() != nil && leads[i].ActionID != nil {
			action, _ = s.actionStore().GetCommercialAction(ctx, orgID, *leads[i].ActionID)
		}
		item := ProjectInboundNowItem(leads[i], acc, action, now)
		if action != nil {
			item.RecommendedAction = firstNonEmpty(action.RecommendedAction, item.RecommendedAction)
			item.EmailSendable = false
			item.Dispatchable = false
			if action.OutcomeCode != "" && item.NextAction == "" {
				item.NextAction = action.NextActionType
			}
		}
		if skip != "" {
			item.Synthetic = true
		}
		if a, ok := alerts[leads[i].LeadID]; ok {
			attachOperatorAlert(&item, leads[i], a, now)
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri := operatorAlertUrgencyRank(firstNonEmpty(out[i].AlertBand, out[i].AlertState))
		rj := operatorAlertUrgencyRank(firstNonEmpty(out[j].AlertBand, out[j].AlertState))
		if ri != rj {
			return ri < rj
		}
		return out[i].LeadAgeSeconds > out[j].LeadAgeSeconds
	})
	return out, nil
}

func attachOperatorAlert(item *InboundNowItem, lead models.OutreachInboundLead, a models.OutreachOperatorAlert, now time.Time) {
	state := ProjectOperatorAlertState(a, now)
	item.AlertID = a.ID.String()
	item.AlertState = state
	item.AlertBand = state
	item.AcknowledgedAt = rfc3339Ptr(a.AcknowledgedAt)
	item.AcknowledgedBy = a.AcknowledgedBy
	item.AlertFailureCode = a.FailureCode
	item.FirstActionType = a.FirstActionType
	item.ResolutionReason = a.ResolutionReason
	item.AlertLatency = MeasureOperatorAlertLatency(lead, a)
	if item.Owner == inboundUnknown || item.Owner == "" {
		item.Owner = firstNonEmpty(a.Owner, item.Owner)
	}
}

func countUnacknowledgedReal(items []InboundNowItem) int {
	n := 0
	for i := range items {
		if items[i].Synthetic {
			continue
		}
		switch items[i].AlertState {
		case AlertStateAcknowledged, AlertStateActionRecorded, AlertStateResolvedNoAction:
			continue
		default:
			n++
		}
	}
	return n
}

// ProjectInboundNowItem is the shipped projector used by tests and the cockpit.
func ProjectInboundNowItem(lead models.OutreachInboundLead, acc *models.OutreachAccount, action *models.OutreachCommercialAction, now time.Time) InboundNowItem {
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
	query := inboundQueryOf(lead)
	trigger, offer, freshness := inboundAccountFacts(acc)
	reach := inboundUnknown
	if action != nil {
		reach = firstNonEmpty(action.ReachabilityClass, action.RouteType)
		if offer == inboundUnknown {
			offer = idOrUnknownLocal(action.ServiceCode)
		}
	}
	item := InboundNowItem{
		LeadID:              lead.LeadID,
		ReceiptID:           firstNonEmpty(lead.ReceiptID, lead.LeadID),
		Company:             firstNonEmpty(lead.CompanyName, lead.CNPJ14, inboundUnknown),
		Person:              firstNonEmpty(lead.PersonName, inboundUnknown),
		Origin:              firstNonEmpty(lead.Source, inboundUnknown),
		Asset:               firstNonEmpty(lead.AssetID, inboundUnknown),
		Query:               query,
		CTA:                 firstNonEmpty(lead.CTAID, inboundUnknown),
		Trigger:             trigger,
		Offer:               offer,
		EntityID:            firstNonEmpty(lead.EntityID, inboundUnknown),
		PersonID:            firstNonEmpty(lead.PersonID, inboundUnknown),
		CorrelationID:       firstNonEmpty(lead.CorrelationID, inboundUnknown),
		ContractContext:     firstNonEmpty(lead.ContractID, lead.EntityID, truncateRunes(lead.Message, 160), inboundUnknown),
		WhyNow:              firstNonEmpty(lead.WhyNow, "Lead inbound sem fato adicional."),
		RecommendedAction:   inboundRecommended(lead),
		Channel:             firstNonEmpty(lead.Channel, inboundUnknown),
		Reachability:        firstNonEmpty(reach, inboundUnknown),
		Freshness:           freshness,
		Confidence:          inboundConfidence(lead, acc),
		Evidence:            append([]string{}, lead.Evidence...),
		Owner:               firstNonEmpty(lead.Owner, models.InboundOwnerUnknown),
		LeadAgeSeconds:      int64(age.Seconds()),
		LeadAge:             formatLeadAge(age),
		Status:              firstNonEmpty(lead.Status, models.InboundStatusOpen),
		NextAction:          firstNonEmpty(lead.NextAction, models.InboundNextNeedsEnrichment),
		EmailSendable:       false,
		Dispatchable:        false,
		EnrichmentStatus:    firstNonEmpty(lead.EnrichmentStatus, models.InboundEnrichmentUnknown),
		Warnings:            append([]string{}, lead.Warnings...),
		SuggestedCopyReview: "human_review_required",
		Latency:             projectInboundLatency(lead),
		ReceivedAgo:         formatLeadAge(age),
	}
	if lead.ActionID != nil {
		item.ActionID = lead.ActionID.String()
	}
	if lead.AccountID != nil {
		item.AccountID = lead.AccountID.String()
	}
	if action != nil {
		copy := ComposeActionContent(*action)
		item.SuggestedCopy = FounderFacingCopy(copy)
		item.SuggestedCopyRoute = firstNonEmpty(copy.Kind, action.ActionType, lead.NextAction)
	} else {
		item.SuggestedCopy = inboundSuggestedWithoutAction(lead)
		item.SuggestedCopyRoute = firstNonEmpty(lead.NextAction, inboundUnknown)
	}
	return item
}

const inboundUnknown = "UNKNOWN"

func inboundQueryOf(lead models.OutreachInboundLead) string {
	if q := strings.TrimSpace(utmField(lead.UTMJSON, "query_class", "intent_class")); inboundQueryClassOK(q) {
		return q
	}
	if q := strings.TrimSpace(utmField(lead.UTMJSON, "query")); inboundQueryClassOK(q) {
		return q
	}
	return inboundUnknown
}

func utmField(raw []byte, keys ...string) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	for _, k := range keys {
		if v := strings.TrimSpace(m[k]); v != "" {
			return v
		}
	}
	return ""
}

func inboundAccountFacts(acc *models.OutreachAccount) (trigger, offer, freshness string) {
	if acc == nil {
		return inboundUnknown, inboundUnknown, inboundUnknown
	}
	trigger = idOrUnknownLocal(acc.MomentCode)
	offer = idOrUnknownLocal(firstNonEmpty(acc.EntryOffer, acc.ServiceName, acc.ServiceCode))
	switch {
	case acc.TargetFitFresh:
		freshness = "fresh"
	case strings.TrimSpace(acc.TargetFitFreshnessReason) != "":
		freshness = "stale"
	default:
		freshness = inboundUnknown
	}
	return trigger, offer, freshness
}

func inboundConfidence(lead models.OutreachInboundLead, acc *models.OutreachAccount) string {
	switch lead.EnrichmentStatus {
	case models.InboundEnrichmentCompleted:
		if acc != nil && (lead.PersonID != "" || lead.AccountID != nil) {
			return "observed"
		}
		return "lead_supplied"
	case models.InboundEnrichmentFailed, models.InboundEnrichmentUnavailable:
		return lead.EnrichmentStatus
	default:
		if lead.CompanyName != "" || lead.LeadPhone != "" || lead.LeadEmail != "" {
			return "lead_supplied"
		}
		return inboundUnknown
	}
}

func inboundSuggestedWithoutAction(lead models.OutreachInboundLead) string {
	switch lead.NextAction {
	case models.InboundNextCall:
		return "Ligar no numero observado. Confirmar quem acompanha o contrato. Nao inventar identidade."
	case models.InboundNextWhatsApp:
		return "WhatsApp manual no numero observado. Sujeito a revisao. O sistema nao envia."
	case models.InboundNextRoutedCall:
		return "Ligacao roteada. Pedir a quem acompanha o contrato. Sem canal direto publicado."
	case models.InboundNextSendEmail:
		return "Preparar e-mail em revisao humana. Nao auto-enviar."
	case models.InboundNextSuppressed:
		return "Nao contatar."
	case models.InboundNextManualOutreach:
		return "Abordagem manual com os fatos observados. Sem auto-envio."
	default:
		return "Enriquecer identidade. Nao contatar no escuro."
	}
}

func idOrUnknownLocal(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return inboundUnknown
	}
	return v
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
	if xerr := requireAlertActor(userID, req.Actor); xerr != nil {
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
	s.emitInboundLearning(ctx, orgID, row, code)
	s.stampAlertFirstAction(ctx, orgID, userID, row, code, req.Now)
	return &applied, nil
}

func (s *service) emitInboundLearning(ctx context.Context, orgID uuid.UUID, row *models.OutreachInboundLead, code string) {
	_ = ctx
	if s.intel == nil && s.intelStore() == nil {
		return
	}
	in := intel.LearningInput{
		From:           intel.LearningFromOutcome,
		OutcomeType:    code,
		HumanConfirmed: true,
		Keys: intel.JoinKeys{
			OrganizationID: orgID.String(),
			LeadID:         row.LeadID,
			ReceiptID:      row.ReceiptID,
			AssetID:        row.AssetID,
			CTAID:          row.CTAID,
			CorrelationID:  row.CorrelationID,
			Source:         row.Source,
			PersonID:       row.PersonID,
			RouteFamily:    firstNonEmpty(row.RouteFamily, intel.FamilyInbound),
		},
		Synthetic: InboundCommercialSkipReason(*row) != "",
	}
	if row.ActionID != nil {
		in.Keys.ActionID = row.ActionID.String()
	}
	if row.AccountID != nil {
		in.Keys.AccountID = row.AccountID.String()
	}
	intel.EmitLearning(s.intelStore(), in)
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
