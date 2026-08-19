package confenge

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

type operatorAlertStore interface {
	UpsertOperatorAlert(ctx context.Context, alert *models.OutreachOperatorAlert) (created bool, existing *models.OutreachOperatorAlert, err error)
	UpdateOperatorAlert(ctx context.Context, alert *models.OutreachOperatorAlert) error
	GetOperatorAlertByLead(ctx context.Context, orgID uuid.UUID, leadID string) (*models.OutreachOperatorAlert, error)
	ListOperatorAlerts(ctx context.Context, orgID uuid.UUID, includeSynthetic bool, limit int) ([]models.OutreachOperatorAlert, error)
}

func (s *service) alertStore() operatorAlertStore {
	if s == nil || s.repo == nil {
		return nil
	}
	if st, ok := s.repo.(operatorAlertStore); ok {
		return st
	}
	return nil
}

func copyChannelStates(in map[string]string) map[string]string {
	if in == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneOperatorAlert(a *models.OutreachOperatorAlert) *models.OutreachOperatorAlert {
	if a == nil {
		return nil
	}
	cp := *a
	cp.ChannelStates = copyChannelStates(a.ChannelStates)
	return &cp
}

func (s *service) ensureOperatorAlert(ctx context.Context, orgID uuid.UUID, row *models.OutreachInboundLead, now time.Time) {
	if row == nil || strings.TrimSpace(row.LeadID) == "" {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	st := s.alertStore()
	if st == nil {
		s.holdAlertStoreException(orgID, row, "operator alert store unavailable")
		return
	}
	synthetic := InboundCommercialSkipReason(*row) != ""
	existing, err := st.GetOperatorAlertByLead(ctx, orgID, row.LeadID)
	if err != nil {
		s.holdAlertStoreException(orgID, row, "load operator alert: "+err.Error())
		return
	}
	if existing != nil {
		return
	}
	alert := &models.OutreachOperatorAlert{
		ID:             uuid.New(),
		OrganizationID: orgID,
		LeadID:         row.LeadID,
		ReceiptID:      firstNonEmpty(row.ReceiptID, row.LeadID),
		EventID:        OperatorAlertEventID(row.LeadID),
		AlertType:      AlertTypeInboundOperatorAttention,
		Synthetic:      synthetic,
		CreatedAt:      now,
		Owner:          firstNonEmpty(row.Owner, models.InboundOwnerUnknown),
		Freshness:      AlertBandNew,
		State:          AlertBandNew,
		PolicyVersion:  AlertPolicyV1,
		AttemptCount:   1,
		ChannelStates:  map[string]string{},
		UpdatedAt:      now,
	}
	created, stored, err := st.UpsertOperatorAlert(ctx, alert)
	if err != nil {
		s.holdAlertStoreException(orgID, row, "persist operator alert: "+err.Error())
		return
	}
	if stored == nil {
		stored = alert
	}
	s.emitOperatorChannels(ctx, orgID, row, stored, now, created)
}

func (s *service) emitOperatorChannels(ctx context.Context, orgID uuid.UUID, row *models.OutreachInboundLead, alert *models.OutreachOperatorAlert, now time.Time, newlyCreated bool) {
	if alert == nil {
		return
	}
	st := s.alertStore()
	states := copyChannelStates(alert.ChannelStates)
	states[AlertChannelCockpit] = "ready"
	states[AlertChannelBrowser] = "client"
	_, emailReason := ResolveOperatorAlertRecipient(s.cfg, row.LeadEmail)
	if emailReason == "" {
		emailReason = AlertEmailBlockedNoTransport
	}
	if s.operatorMail != nil && emailReason == AlertEmailBlockedNoTransport {
		mail := BuildOperatorAlertEmail(now, row.Source, row.AssetID, now.Sub(row.WarmblyIngestedAt), "/app/confenge#inbound-agora")
		to := strings.TrimSpace(s.cfg.OperatorAlertEmail)
		if OperatorAlertContainsPII(mail.Subject, *row) || OperatorAlertContainsPII(mail.Body, *row) {
			emailReason = "pii_blocked"
		} else if err := s.operatorMail(to, mail.Subject, mail.Body); err != nil {
			emailReason = AlertEmailSendFailed
			alert.FailureCode = "email_transport"
		} else {
			emailReason = "sent"
		}
	}
	states[AlertChannelEmail] = emailReason
	alert.ChannelStates = states
	alert.AttemptCount++
	t := now
	if alert.FirstEmittedAt == nil {
		alert.FirstEmittedAt = &t
	}
	alert.LastEmittedAt = &t
	alert.UpdatedAt = now
	alert.State = ProjectOperatorAlertState(*alert, now)
	alert.Freshness = alert.State
	if st != nil {
		if err := st.UpdateOperatorAlert(ctx, alert); err != nil && newlyCreated {
			s.holdAlertStoreException(orgID, row, "update operator alert channels: "+err.Error())
			return
		}
	}
	if newlyCreated {
		s.emitOperatorAlertEvent(ctx, orgID, row, alert, intel.EventOperatorAlertCreated, now, "")
		s.emitOperatorAlertEvent(ctx, orgID, row, alert, intel.EventOperatorAlertEmitted, now, "")
	}
	if emailReason == AlertEmailSendFailed {
		s.emitOperatorAlertEvent(ctx, orgID, row, alert, intel.EventOperatorAlertFailed, now, alert.FailureCode)
	}
	if s.audit != nil && newlyCreated {
		eid := alert.ID
		s.audit.LogAction(ctx, orgID, uuid.Nil, models.AuditActionCreate, models.AuditEntityOutreachOperatorAlert, &eid, "", "",
			map[string]string{"action": "operator_alert_created", "lead_id": row.LeadID, "synthetic": boolText(alert.Synthetic)},
			map[string]string{"alert_type": alert.AlertType},
		)
	}
}

func boolText(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func (s *service) holdAlertStoreException(orgID uuid.UUID, row *models.OutreachInboundLead, detail string) {
	if s == nil {
		return
	}
	leadID := ""
	receipt := ""
	synthetic := false
	if row != nil {
		leadID = row.LeadID
		receipt = row.ReceiptID
		synthetic = InboundCommercialSkipReason(*row) != ""
	}
	ex := intel.Exception{
		OrganizationID: orgID.String(),
		Code:           intel.ExceptionAlertStoreFailed,
		Reason:         firstNonEmpty(detail, "operator alert store failed"),
		NextAction:     "retry alert persist; inbound receipt stays; do not contact the lead",
		LeadID:         leadID,
		ReceiptID:      receipt,
		Identity:       firstNonEmpty(leadID, receipt),
		Held:           true,
		Synthetic:      synthetic,
		At:             time.Now().UTC(),
		Owner:          intel.OwnerInboundOps,
		Lane:           intel.LaneInbound,
		Source:         "warmbly",
		Severity:       "high",
		Status:         "open",
	}
	if ex.Owner == "" {
		ex.Owner = intel.ExceptionOwner(ex.Code)
	}
	_ = s.intelStore().PutException(ex)
}

func (s *service) emitOperatorAlertEvent(ctx context.Context, orgID uuid.UUID, row *models.OutreachInboundLead, alert *models.OutreachOperatorAlert, typ string, now time.Time, actor string) {
	if row == nil || alert == nil {
		return
	}
	key := typ + ":" + alert.EventID
	if typ == intel.EventOperatorAlertEmitted && alert.AttemptCount > 1 {
		key = typ + ":" + alert.EventID + ":" + now.UTC().Format(time.RFC3339Nano)
	}
	payload, _ := json.Marshal(map[string]any{
		"alert_id":   alert.ID.String(),
		"lead_id":    row.LeadID,
		"receipt_id": row.ReceiptID,
		"state":      alert.State,
		"synthetic":  alert.Synthetic,
	})
	_ = s.EnqueueOutcome(ctx, orgID, models.OutreachOutcome{
		IdempotencyKey: key,
		SourceLeadID:   row.LeadID,
		EventType:      typ,
		OccurredAt:     now,
		Payload:        payload,
	})
	ev := intel.CommercialEvent{
		EventID:        key,
		Version:        "1",
		Schema:         intel.EventSchemaV1,
		Type:           typ,
		OccurredAt:     now,
		IngestedAt:     now,
		Timezone:       OperatorAlertDisplayTZ,
		IdempotencyKey: key,
		LeadID:         row.LeadID,
		ReceiptID:      firstNonEmpty(row.ReceiptID, row.LeadID),
		CorrelationID:  row.CorrelationID,
		Source:         row.Source,
		AssetID:        row.AssetID,
		CTAID:          row.CTAID,
		RouteFamily:    firstNonEmpty(row.RouteFamily, intel.FamilyInbound),
		OrganizationID: orgID.String(),
		Synthetic:      alert.Synthetic,
		ActorRef:       actor,
	}
	intel.IngestEvent(s.intelStore(), ev)
}

func actorRef(userID uuid.UUID, fallback string) string {
	if userID != uuid.Nil {
		return userID.String()
	}
	return strings.TrimSpace(fallback)
}

func requireAlertActor(userID uuid.UUID, fallback string) *errx.Error {
	if userID == uuid.Nil && strings.TrimSpace(fallback) == "" {
		return errx.New(errx.Unauthorized, "actor required")
	}
	return nil
}

func (s *service) AcknowledgeInboundAlert(ctx context.Context, orgID, userID uuid.UUID, leadID string, now time.Time) (*OperatorAlert, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	if xerr := requireAlertActor(userID, ""); xerr != nil {
		return nil, xerr
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	leadID = strings.TrimSpace(leadID)
	st := s.alertStore()
	if st == nil {
		return nil, errx.New(errx.Internal, "operator alert store unavailable")
	}
	alert, err := st.GetOperatorAlertByLead(ctx, orgID, leadID)
	if err != nil {
		return nil, errx.New(errx.Internal, "load operator alert: "+err.Error())
	}
	if alert == nil {
		return nil, errx.New(errx.NotFound, "operator alert not found")
	}
	if alert.AcknowledgedAt != nil && !alert.AcknowledgedAt.IsZero() {
		return cloneOperatorAlert(alert), nil
	}
	actor := actorRef(userID, "")
	t := now
	alert.AcknowledgedAt = &t
	alert.AcknowledgedBy = actor
	alert.UpdatedAt = now
	alert.State = ProjectOperatorAlertState(*alert, now)
	alert.Freshness = alert.State
	if err := st.UpdateOperatorAlert(ctx, alert); err != nil {
		return nil, errx.New(errx.Internal, "persist acknowledgement: "+err.Error())
	}
	if ist := s.inboundStore(); ist != nil {
		if row, _ := ist.GetInboundLeadByLeadID(ctx, orgID, leadID); row != nil {
			s.emitOperatorAlertEvent(ctx, orgID, row, alert, intel.EventOperatorAlertAcknowledged, now, actor)
		}
	}
	if s.audit != nil {
		eid := alert.ID
		s.audit.LogAction(ctx, orgID, userID, models.AuditActionUpdate, models.AuditEntityOutreachOperatorAlert, &eid, "", "",
			map[string]string{"action": "operator_alert_acknowledged", "lead_id": leadID},
			nil,
		)
	}
	return cloneOperatorAlert(alert), nil
}

func (s *service) ResolveInboundNoAction(ctx context.Context, orgID, userID uuid.UUID, leadID, reason string, now time.Time) (*OperatorAlert, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	if xerr := requireAlertActor(userID, ""); xerr != nil {
		return nil, xerr
	}
	if operatorAlertForbidsCommercialClose(reason) {
		return nil, errx.New(errx.BadRequest, "alert resolution cannot set WON, LOST, pipeline, or revenue")
	}
	if !ValidOperatorResolveReason(reason) {
		return nil, errx.New(errx.BadRequest, "resolution reason required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	leadID = strings.TrimSpace(leadID)
	st := s.alertStore()
	if st == nil {
		return nil, errx.New(errx.Internal, "operator alert store unavailable")
	}
	alert, err := st.GetOperatorAlertByLead(ctx, orgID, leadID)
	if err != nil {
		return nil, errx.New(errx.Internal, "load operator alert: "+err.Error())
	}
	if alert == nil {
		return nil, errx.New(errx.NotFound, "operator alert not found")
	}
	if alert.ResolvedAt != nil && !alert.ResolvedAt.IsZero() {
		return cloneOperatorAlert(alert), nil
	}
	actor := actorRef(userID, "")
	t := now
	if alert.AcknowledgedAt == nil {
		alert.AcknowledgedAt = &t
		alert.AcknowledgedBy = actor
	}
	alert.ResolvedAt = &t
	alert.ResolutionReason = strings.ToUpper(strings.TrimSpace(reason))
	alert.UpdatedAt = now
	alert.State = ProjectOperatorAlertState(*alert, now)
	alert.Freshness = alert.State
	if err := st.UpdateOperatorAlert(ctx, alert); err != nil {
		return nil, errx.New(errx.Internal, "persist resolution: "+err.Error())
	}
	if ist := s.inboundStore(); ist != nil {
		if row, _ := ist.GetInboundLeadByLeadID(ctx, orgID, leadID); row != nil {
			s.emitOperatorAlertEvent(ctx, orgID, row, alert, intel.EventInboundResolvedNoAction, now, actor)
		}
	}
	if s.audit != nil {
		eid := alert.ID
		s.audit.LogAction(ctx, orgID, userID, models.AuditActionUpdate, models.AuditEntityOutreachOperatorAlert, &eid, "", "",
			map[string]string{"action": "inbound_resolved_no_action", "lead_id": leadID, "reason": alert.ResolutionReason},
			nil,
		)
	}
	return cloneOperatorAlert(alert), nil
}

func (s *service) stampAlertFirstAction(ctx context.Context, orgID, userID uuid.UUID, row *models.OutreachInboundLead, code string, now time.Time) {
	if row == nil {
		return
	}
	st := s.alertStore()
	if st == nil {
		return
	}
	alert, err := st.GetOperatorAlertByLead(ctx, orgID, row.LeadID)
	if err != nil || alert == nil {
		return
	}
	if alert.FirstActionAt != nil && !alert.FirstActionAt.IsZero() {
		return
	}
	actor := actorRef(userID, "")
	t := now
	if alert.AcknowledgedAt == nil {
		alert.AcknowledgedAt = &t
		alert.AcknowledgedBy = actor
	}
	alert.FirstActionAt = &t
	alert.FirstActionType = strings.ToUpper(strings.TrimSpace(code))
	alert.UpdatedAt = now
	alert.State = ProjectOperatorAlertState(*alert, now)
	alert.Freshness = alert.State
	_ = st.UpdateOperatorAlert(ctx, alert)
	s.emitOperatorAlertEvent(ctx, orgID, row, alert, intel.EventFirstHumanActionRecorded, now, actor)
}
