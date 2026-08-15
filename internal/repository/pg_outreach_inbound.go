package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/warmbly/warmbly/internal/models"
)

const inboundLeadSelect = `
	SELECT id, organization_id, lead_id, COALESCE(receipt_id,''), COALESCE(identity_key,''),
		lead_created_at, warmbly_ingested_at, enrichment_completed_at, owner_assigned_at,
		first_action_at, conversation_at, proposal_at, close_at,
		COALESCE(source,''), COALESCE(route_family,''), COALESCE(asset_id,''), COALESCE(cta_id,''),
		COALESCE(landing_url,''), COALESCE(contract_public_id,''), COALESCE(entity_public_id,''),
		COALESCE(cnpj14,''), COALESCE(company_name,''), COALESCE(lead_name,''),
		COALESCE(lead_email,''), COALESCE(lead_phone,''), COALESCE(referrer,''),
		COALESCE(message,''), COALESCE(correlation_id,''),
		COALESCE(consent_json,'{}'::jsonb), COALESCE(utm_json,'{}'::jsonb), COALESCE(raw_payload,'{}'::jsonb),
		COALESCE(enrichment_status,'UNKNOWN'), COALESCE(next_action,''), COALESCE(channel,''),
		COALESCE(why_now,''), COALESCE(owner,'UNKNOWN'), COALESCE(status,'OPEN'),
		COALESCE(suppress_reason,''), COALESCE(dedupe_of_lead_id,''),
		COALESCE(person_id,''), COALESCE(person_name,''),
		COALESCE(evidence,'[]'::jsonb), COALESCE(provenance,'[]'::jsonb), COALESCE(warnings,'[]'::jsonb),
		account_id, contact_candidate_id, commercial_action_id, created_at, updated_at `

func (r *outreachRepository) InsertInboundLead(ctx context.Context, lead *models.OutreachInboundLead) (bool, *models.OutreachInboundLead, error) {
	if lead.ID == uuid.Nil {
		lead.ID = uuid.New()
	}
	now := time.Now().UTC()
	if lead.CreatedAt.IsZero() {
		lead.CreatedAt = now
	}
	if lead.UpdatedAt.IsZero() {
		lead.UpdatedAt = now
	}
	if lead.WarmblyIngestedAt.IsZero() {
		lead.WarmblyIngestedAt = now
	}
	consent := jsonOrObject(lead.ConsentJSON)
	utm := jsonOrObject(lead.UTMJSON)
	raw := jsonOrObject(lead.RawPayload)
	ev := jsonOrArray(lead.Evidence)
	prov := jsonOrArray(lead.Provenance)
	warn := jsonOrArray(lead.Warnings)
	_, err := r.db.Exec(ctx, `
		INSERT INTO outreach_inbound_leads (
			id, organization_id, lead_id, receipt_id, identity_key,
			lead_created_at, warmbly_ingested_at, enrichment_completed_at, owner_assigned_at,
			first_action_at, conversation_at, proposal_at, close_at,
			source, route_family, asset_id, cta_id, landing_url, contract_public_id, entity_public_id,
			cnpj14, company_name, lead_name, lead_email, lead_phone, referrer, message, correlation_id,
			consent_json, utm_json, raw_payload,
			enrichment_status, next_action, channel, why_now, owner, status, suppress_reason,
			dedupe_of_lead_id, person_id, person_name, evidence, provenance, warnings,
			account_id, contact_candidate_id, commercial_action_id, created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,
			$6,$7,$8,$9,
			$10,$11,$12,$13,
			$14,$15,$16,$17,$18,$19,$20,
			$21,$22,$23,$24,$25,$26,$27,$28,
			$29,$30,$31,
			$32,$33,$34,$35,$36,$37,$38,
			$39,$40,$41,$42,$43,$44,
			$45,$46,$47,$48,$49
		)`,
		lead.ID, lead.OrganizationID, lead.LeadID, lead.ReceiptID, lead.IdentityKey,
		lead.LeadCreatedAt, lead.WarmblyIngestedAt, lead.EnrichmentCompletedAt, lead.OwnerAssignedAt,
		lead.FirstActionAt, lead.ConversationAt, lead.ProposalAt, lead.CloseAt,
		lead.Source, lead.RouteFamily, lead.AssetID, lead.CTAID, lead.LandingURL, lead.ContractID, lead.EntityID,
		lead.CNPJ14, lead.CompanyName, lead.LeadName, lead.LeadEmail, lead.LeadPhone, lead.Referrer, lead.Message, lead.CorrelationID,
		consent, utm, raw,
		lead.EnrichmentStatus, lead.NextAction, lead.Channel, lead.WhyNow, lead.Owner, lead.Status, lead.SuppressReason,
		lead.DedupeOfLeadID, lead.PersonID, lead.PersonName, ev, prov, warn,
		lead.AccountID, lead.CandidateID, lead.ActionID, lead.CreatedAt, lead.UpdatedAt,
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") || strings.Contains(err.Error(), "unique") {
			existing, gerr := r.GetInboundLeadByLeadID(ctx, lead.OrganizationID, lead.LeadID)
			return false, existing, gerr
		}
		return false, nil, err
	}
	return true, lead, nil
}

func (r *outreachRepository) UpdateInboundLead(ctx context.Context, lead *models.OutreachInboundLead) error {
	lead.UpdatedAt = time.Now().UTC()
	ev := jsonOrArray(lead.Evidence)
	prov := jsonOrArray(lead.Provenance)
	warn := jsonOrArray(lead.Warnings)
	ct, err := r.db.Exec(ctx, `
		UPDATE outreach_inbound_leads SET
			receipt_id=$3, identity_key=$4,
			enrichment_completed_at=$5, owner_assigned_at=$6,
			first_action_at=$7, conversation_at=$8, proposal_at=$9, close_at=$10,
			enrichment_status=$11, next_action=$12, channel=$13, why_now=$14,
			owner=$15, status=$16, suppress_reason=$17, dedupe_of_lead_id=$18,
			person_id=$19, person_name=$20, evidence=$21, provenance=$22, warnings=$23,
			account_id=$24, contact_candidate_id=$25, commercial_action_id=$26, updated_at=$27
		WHERE id=$1 AND organization_id=$2`,
		lead.ID, lead.OrganizationID, lead.ReceiptID, lead.IdentityKey,
		lead.EnrichmentCompletedAt, lead.OwnerAssignedAt,
		lead.FirstActionAt, lead.ConversationAt, lead.ProposalAt, lead.CloseAt,
		lead.EnrichmentStatus, lead.NextAction, lead.Channel, lead.WhyNow,
		lead.Owner, lead.Status, lead.SuppressReason, lead.DedupeOfLeadID,
		lead.PersonID, lead.PersonName, ev, prov, warn,
		lead.AccountID, lead.CandidateID, lead.ActionID, lead.UpdatedAt,
	)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (r *outreachRepository) GetInboundLeadByLeadID(ctx context.Context, orgID uuid.UUID, leadID string) (*models.OutreachInboundLead, error) {
	row := r.db.QueryRow(ctx, inboundLeadSelect+`
		FROM outreach_inbound_leads WHERE organization_id=$1 AND lead_id=$2`, orgID, leadID)
	return scanInboundLead(row)
}

func (r *outreachRepository) ListInboundLeads(ctx context.Context, orgID uuid.UUID, openOnly bool, limit int) ([]models.OutreachInboundLead, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q := inboundLeadSelect + ` FROM outreach_inbound_leads WHERE organization_id=$1`
	args := []any{orgID}
	if openOnly {
		q += ` AND status='OPEN'`
	}
	q += ` ORDER BY warmbly_ingested_at DESC LIMIT $2`
	args = append(args, limit)
	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.OutreachInboundLead
	for rows.Next() {
		lead, err := scanInboundLead(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *lead)
	}
	return out, rows.Err()
}

func (r *outreachRepository) FindRecentInboundByIdentity(ctx context.Context, orgID uuid.UUID, identityKey string, since time.Time, excludeLeadID string) (*models.OutreachInboundLead, error) {
	if strings.TrimSpace(identityKey) == "" {
		return nil, nil
	}
	row := r.db.QueryRow(ctx, inboundLeadSelect+`
		FROM outreach_inbound_leads
		WHERE organization_id=$1 AND identity_key=$2 AND warmbly_ingested_at>=$3 AND lead_id<>$4
		ORDER BY warmbly_ingested_at DESC LIMIT 1`, orgID, identityKey, since, excludeLeadID)
	lead, err := scanInboundLead(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return lead, err
}

type inboundScanner interface {
	Scan(dest ...any) error
}

func scanInboundLead(row inboundScanner) (*models.OutreachInboundLead, error) {
	var l models.OutreachInboundLead
	var ev, prov, warn []byte
	err := row.Scan(
		&l.ID, &l.OrganizationID, &l.LeadID, &l.ReceiptID, &l.IdentityKey,
		&l.LeadCreatedAt, &l.WarmblyIngestedAt, &l.EnrichmentCompletedAt, &l.OwnerAssignedAt,
		&l.FirstActionAt, &l.ConversationAt, &l.ProposalAt, &l.CloseAt,
		&l.Source, &l.RouteFamily, &l.AssetID, &l.CTAID,
		&l.LandingURL, &l.ContractID, &l.EntityID,
		&l.CNPJ14, &l.CompanyName, &l.LeadName,
		&l.LeadEmail, &l.LeadPhone, &l.Referrer,
		&l.Message, &l.CorrelationID,
		&l.ConsentJSON, &l.UTMJSON, &l.RawPayload,
		&l.EnrichmentStatus, &l.NextAction, &l.Channel,
		&l.WhyNow, &l.Owner, &l.Status,
		&l.SuppressReason, &l.DedupeOfLeadID,
		&l.PersonID, &l.PersonName,
		&ev, &prov, &warn,
		&l.AccountID, &l.CandidateID, &l.ActionID, &l.CreatedAt, &l.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(ev, &l.Evidence)
	_ = json.Unmarshal(prov, &l.Provenance)
	_ = json.Unmarshal(warn, &l.Warnings)
	return &l, nil
}

func jsonOrObject(b []byte) []byte {
	if len(b) == 0 {
		return []byte("{}")
	}
	return b
}

func jsonOrArray(vals []string) []byte {
	if vals == nil {
		return []byte("[]")
	}
	b, err := json.Marshal(vals)
	if err != nil || b == nil {
		return []byte("[]")
	}
	return b
}
