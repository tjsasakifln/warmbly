package repository

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/warmbly/warmbly/internal/models"
)

const operatorAlertSelect = `
	SELECT id, organization_id, lead_id, COALESCE(receipt_id,''), event_id, alert_type, synthetic,
		created_at, first_emitted_at, last_emitted_at, COALESCE(channel_states,'{}'::jsonb),
		acknowledged_at, COALESCE(acknowledged_by,''), first_action_at, COALESCE(first_action_type,''),
		resolved_at, COALESCE(resolution_reason,''), attempt_count, next_attempt_at,
		COALESCE(failure_code,''), COALESCE(owner,'UNKNOWN'), COALESCE(freshness,''),
		COALESCE(state,'NEW'), COALESCE(policy_version,'v1'), updated_at `

func (r *outreachRepository) UpsertOperatorAlert(ctx context.Context, alert *models.OutreachOperatorAlert) (bool, *models.OutreachOperatorAlert, error) {
	if alert.ID == uuid.Nil {
		alert.ID = uuid.New()
	}
	now := time.Now().UTC()
	if alert.CreatedAt.IsZero() {
		alert.CreatedAt = now
	}
	if alert.UpdatedAt.IsZero() {
		alert.UpdatedAt = now
	}
	if strings.TrimSpace(alert.AlertType) == "" {
		alert.AlertType = models.OperatorAlertTypeInboundAttention
	}
	if strings.TrimSpace(alert.EventID) == "" {
		alert.EventID = models.OperatorAlertTypeInboundAttention + ":" + alert.LeadID
	}
	if strings.TrimSpace(alert.PolicyVersion) == "" {
		alert.PolicyVersion = models.OperatorAlertPolicyV1
	}
	if strings.TrimSpace(alert.State) == "" {
		alert.State = models.OperatorAlertBandNew
	}
	states, _ := json.Marshal(alert.ChannelStates)
	if len(states) == 0 {
		states = []byte("{}")
	}
	tag, err := r.db.Exec(ctx, `
		INSERT INTO outreach_operator_alerts (
			id, organization_id, lead_id, receipt_id, event_id, alert_type, synthetic,
			created_at, first_emitted_at, last_emitted_at, channel_states,
			acknowledged_at, acknowledged_by, first_action_at, first_action_type,
			resolved_at, resolution_reason, attempt_count, next_attempt_at,
			failure_code, owner, freshness, state, policy_version, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,
			$8,$9,$10,$11::jsonb,
			$12,$13,$14,$15,
			$16,$17,$18,$19,
			$20,$21,$22,$23,$24,$25
		)
		ON CONFLICT (organization_id, event_id) DO NOTHING`,
		alert.ID, alert.OrganizationID, alert.LeadID, alert.ReceiptID, alert.EventID, alert.AlertType, alert.Synthetic,
		alert.CreatedAt, alert.FirstEmittedAt, alert.LastEmittedAt, states,
		alert.AcknowledgedAt, alert.AcknowledgedBy, alert.FirstActionAt, alert.FirstActionType,
		alert.ResolvedAt, alert.ResolutionReason, alert.AttemptCount, alert.NextAttemptAt,
		alert.FailureCode, alert.Owner, alert.Freshness, alert.State, alert.PolicyVersion, alert.UpdatedAt,
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") || strings.Contains(err.Error(), "unique") {
			existing, gerr := r.GetOperatorAlertByLead(ctx, alert.OrganizationID, alert.LeadID)
			return false, existing, gerr
		}
		return false, nil, err
	}
	if tag.RowsAffected() == 0 {
		existing, gerr := r.GetOperatorAlertByLead(ctx, alert.OrganizationID, alert.LeadID)
		return false, existing, gerr
	}
	return true, alert, nil
}

func (r *outreachRepository) UpdateOperatorAlert(ctx context.Context, alert *models.OutreachOperatorAlert) error {
	alert.UpdatedAt = time.Now().UTC()
	states, _ := json.Marshal(alert.ChannelStates)
	if len(states) == 0 {
		states = []byte("{}")
	}
	ct, err := r.db.Exec(ctx, `
		UPDATE outreach_operator_alerts SET
			receipt_id=$3, first_emitted_at=$4, last_emitted_at=$5, channel_states=$6::jsonb,
			acknowledged_at=$7, acknowledged_by=$8, first_action_at=$9, first_action_type=$10,
			resolved_at=$11, resolution_reason=$12, attempt_count=$13, next_attempt_at=$14,
			failure_code=$15, owner=$16, freshness=$17, state=$18, policy_version=$19, updated_at=$20,
			synthetic=$21
		WHERE id=$1 AND organization_id=$2`,
		alert.ID, alert.OrganizationID, alert.ReceiptID, alert.FirstEmittedAt, alert.LastEmittedAt, states,
		alert.AcknowledgedAt, alert.AcknowledgedBy, alert.FirstActionAt, alert.FirstActionType,
		alert.ResolvedAt, alert.ResolutionReason, alert.AttemptCount, alert.NextAttemptAt,
		alert.FailureCode, alert.Owner, alert.Freshness, alert.State, alert.PolicyVersion, alert.UpdatedAt,
		alert.Synthetic,
	)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (r *outreachRepository) GetOperatorAlertByLead(ctx context.Context, orgID uuid.UUID, leadID string) (*models.OutreachOperatorAlert, error) {
	row := r.db.QueryRow(ctx, operatorAlertSelect+`
		FROM outreach_operator_alerts WHERE organization_id=$1 AND lead_id=$2 AND alert_type=$3`,
		orgID, leadID, models.OperatorAlertTypeInboundAttention)
	return scanOperatorAlert(row)
}

func (r *outreachRepository) ListOperatorAlerts(ctx context.Context, orgID uuid.UUID, includeSynthetic bool, limit int) ([]models.OutreachOperatorAlert, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q := operatorAlertSelect + ` FROM outreach_operator_alerts WHERE organization_id=$1`
	args := []any{orgID}
	if !includeSynthetic {
		q += ` AND synthetic=false`
	}
	q += ` ORDER BY created_at ASC LIMIT $2`
	args = append(args, limit)
	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.OutreachOperatorAlert
	for rows.Next() {
		a, err := scanOperatorAlert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

type operatorAlertScanner interface {
	Scan(dest ...any) error
}

func scanOperatorAlert(row operatorAlertScanner) (*models.OutreachOperatorAlert, error) {
	var a models.OutreachOperatorAlert
	var states []byte
	err := row.Scan(
		&a.ID, &a.OrganizationID, &a.LeadID, &a.ReceiptID, &a.EventID, &a.AlertType, &a.Synthetic,
		&a.CreatedAt, &a.FirstEmittedAt, &a.LastEmittedAt, &states,
		&a.AcknowledgedAt, &a.AcknowledgedBy, &a.FirstActionAt, &a.FirstActionType,
		&a.ResolvedAt, &a.ResolutionReason, &a.AttemptCount, &a.NextAttemptAt,
		&a.FailureCode, &a.Owner, &a.Freshness, &a.State, &a.PolicyVersion, &a.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if len(states) > 0 {
		_ = json.Unmarshal(states, &a.ChannelStates)
	}
	if a.ChannelStates == nil {
		a.ChannelStates = map[string]string{}
	}
	return &a, nil
}
