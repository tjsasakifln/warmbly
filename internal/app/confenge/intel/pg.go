package intel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore persists this capability's own tables. It does not write
// extra-cli, web-cfg, or SmartLic, and it is not the execution ledger.
type PGStore struct {
	db    *pgxpool.Pool
	orgID string
}

func NewPGStore(db *pgxpool.Pool, orgID string) *PGStore {
	return &PGStore{db: db, orgID: strings.TrimSpace(orgID)}
}

func (s *PGStore) GetChain(orgID, identity string) (*Chain, error) {
	if s == nil || s.db == nil || strings.TrimSpace(identity) == "" {
		return nil, nil
	}
	org := firstNonEmpty(orgID, s.orgID)
	var raw []byte
	err := s.db.QueryRow(context.Background(), `
		SELECT payload FROM outreach_intel_chains
		WHERE organization_id = $1::uuid AND identity = $2`, org, identity).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c Chain
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *PGStore) PutChain(c Chain) (Chain, bool, error) {
	if s == nil || s.db == nil {
		return c, false, errors.New("intel pg store unavailable")
	}
	org := firstNonEmpty(c.Keys.OrganizationID, s.orgID)
	if existing, err := s.GetChain(org, c.Identity); err != nil {
		return c, false, err
	} else if existing != nil {
		return *existing, false, nil
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return c, false, err
	}
	_, err = s.db.Exec(context.Background(), `
		INSERT INTO outreach_intel_chains (
			id, organization_id, identity, metric_key, route_family, label, synthetic, payload, created_at
		) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8::jsonb, $9)
		ON CONFLICT (organization_id, identity) DO NOTHING`,
		uuid.New(), org, c.Identity, c.MetricKey, c.RouteFamily, c.Label, c.Synthetic, raw, c.CreatedAt,
	)
	if err != nil {
		return c, false, err
	}
	if existing, gerr := s.GetChain(org, c.Identity); gerr == nil && existing != nil {
		return *existing, existing.MetricKey == c.MetricKey && existing.CreatedAt.Equal(c.CreatedAt), nil
	}
	return c, true, nil
}

func (s *PGStore) UpdateChain(c Chain) error {
	if s == nil || s.db == nil {
		return errors.New("intel pg store unavailable")
	}
	org := firstNonEmpty(c.Keys.OrganizationID, s.orgID)
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(context.Background(), `
		UPDATE outreach_intel_chains
		SET metric_key = $3, route_family = $4, payload = $5::jsonb
		WHERE organization_id = $1::uuid AND identity = $2`,
		org, c.Identity, c.MetricKey, c.RouteFamily, raw,
	)
	return err
}

func (s *PGStore) ListChains(orgID string) ([]Chain, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	org := firstNonEmpty(orgID, s.orgID)
	rows, err := s.db.Query(context.Background(), `
		SELECT payload FROM outreach_intel_chains WHERE organization_id = $1::uuid ORDER BY identity`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chain
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var c Chain
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *PGStore) PutException(ex Exception) error {
	if s == nil || s.db == nil {
		return errors.New("intel pg store unavailable")
	}
	ex = assignExceptionID(ex)
	if ex.At.IsZero() {
		ex.At = time.Now().UTC()
	}
	org := firstNonEmpty(ex.OrganizationID, s.orgID)
	raw, err := json.Marshal(ex)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(context.Background(), `
		INSERT INTO outreach_intel_exceptions (
			id, organization_id, code, identity, metric_key, held, payload, created_at
		) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7::jsonb, $8)
		ON CONFLICT (id) DO NOTHING`,
		ex.ID, org, ex.Code, ex.Identity, ex.MetricKey, ex.Held, raw, ex.At,
	)
	return err
}

func (s *PGStore) GetException(orgID, id string) (*Exception, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("intel pg store unavailable")
	}
	org := firstNonEmpty(orgID, s.orgID)
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	var raw []byte
	err := s.db.QueryRow(context.Background(), `
		SELECT payload FROM outreach_intel_exceptions
		WHERE organization_id = $1::uuid AND id = $2::uuid`, org, id).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ex Exception
	if err := json.Unmarshal(raw, &ex); err != nil {
		return nil, err
	}
	return &ex, nil
}

func (s *PGStore) UpdateException(ex Exception) error {
	if s == nil || s.db == nil {
		return errors.New("intel pg store unavailable")
	}
	ex = assignExceptionID(ex)
	org := firstNonEmpty(ex.OrganizationID, s.orgID)
	raw, err := json.Marshal(ex)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(context.Background(), `
		UPDATE outreach_intel_exceptions
		SET held = $3, payload = $4::jsonb
		WHERE organization_id = $1::uuid AND id = $2::uuid`,
		org, ex.ID, ex.Held, raw,
	)
	return err
}

func (s *PGStore) ListExceptions(orgID string) ([]Exception, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	org := firstNonEmpty(orgID, s.orgID)
	rows, err := s.db.Query(context.Background(), `
		SELECT payload FROM outreach_intel_exceptions WHERE organization_id = $1::uuid ORDER BY created_at`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Exception
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var ex Exception
		if err := json.Unmarshal(raw, &ex); err != nil {
			return nil, err
		}
		out = append(out, ex)
	}
	return out, rows.Err()
}

func (s *PGStore) PutLearning(c LearningCandidate) (LearningCandidate, error) {
	if s == nil || s.db == nil {
		return c, errors.New("intel pg store unavailable")
	}
	if strings.TrimSpace(c.ID) == "" {
		c.ID = uuid.NewString()
	}
	if c.At.IsZero() {
		c.At = time.Now().UTC()
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return c, err
	}
	org := firstNonEmpty(c.OrganizationID, s.orgID)
	_, err = s.db.Exec(context.Background(), `
		INSERT INTO outreach_intel_learning_candidates (
			id, organization_id, target, source, status, identity, payload, created_at
		) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7::jsonb, $8)`,
		c.ID, org, c.Target, c.Source, c.Status, c.Identity, raw, c.At,
	)
	return c, err
}

func (s *PGStore) ListLearning(orgID string) ([]LearningCandidate, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	org := firstNonEmpty(orgID, s.orgID)
	rows, err := s.db.Query(context.Background(), `
		SELECT payload FROM outreach_intel_learning_candidates WHERE organization_id = $1::uuid ORDER BY created_at`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LearningCandidate
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var c LearningCandidate
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *PGStore) PutEventReceipt(r EventReceipt) (EventReceipt, bool, error) {
	if s == nil || s.db == nil {
		return r, false, errors.New("intel pg store unavailable")
	}
	if existing, err := s.GetEventReceipt(r.OrganizationID, r.ProviderEventID); err != nil {
		return r, false, err
	} else if existing != nil {
		return *existing, false, nil
	}
	if strings.TrimSpace(r.ID) == "" {
		r.ID = uuid.NewString()
	}
	if r.At.IsZero() {
		r.At = time.Now().UTC()
	}
	r.Acked = true
	raw, err := json.Marshal(r)
	if err != nil {
		return r, false, err
	}
	org := firstNonEmpty(r.OrganizationID, s.orgID)
	_, err = s.db.Exec(context.Background(), `
		INSERT INTO outreach_intel_event_receipts (
			id, organization_id, provider_event_id, external_reference, event_id, identity,
			type, raw_type, raw_status, acked, processed, synthetic, payload, created_at
		) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13::jsonb, $14)
		ON CONFLICT (organization_id, provider_event_id) DO NOTHING`,
		r.ID, org, r.ProviderEventID, r.ExternalRef, r.EventID, r.Identity,
		r.Type, r.RawType, r.RawStatus, r.Acked, r.Processed, r.Synthetic, raw, r.At,
	)
	if err != nil {
		return r, false, err
	}
	if existing, gerr := s.GetEventReceipt(org, r.ProviderEventID); gerr == nil && existing != nil {
		return *existing, existing.ID == r.ID, nil
	}
	return r, true, nil
}

func (s *PGStore) GetEventReceipt(orgID, providerEventID string) (*EventReceipt, error) {
	if s == nil || s.db == nil || strings.TrimSpace(providerEventID) == "" {
		return nil, nil
	}
	org := firstNonEmpty(orgID, s.orgID)
	var raw []byte
	err := s.db.QueryRow(context.Background(), `
		SELECT payload FROM outreach_intel_event_receipts
		WHERE organization_id = $1::uuid AND provider_event_id = $2`, org, providerEventID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r EventReceipt
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *PGStore) MarkReceiptProcessed(orgID, providerEventID string) {
	if s == nil || s.db == nil {
		return
	}
	org := firstNonEmpty(orgID, s.orgID)
	_, _ = s.db.Exec(context.Background(), `
		UPDATE outreach_intel_event_receipts SET processed = true
		WHERE organization_id = $1::uuid AND provider_event_id = $2`, org, providerEventID)
}

func (s *PGStore) HoldCapacity(orgID, leadID string, units int, now time.Time) (CapacityHold, error) {
	if s == nil || s.db == nil {
		return CapacityHold{}, errors.New("intel pg store unavailable")
	}
	if units <= 0 {
		units = 1
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	org := firstNonEmpty(orgID, s.orgID)
	tx, err := s.db.Begin(context.Background())
	if err != nil {
		return CapacityHold{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(context.Background(), `SELECT pg_advisory_xact_lock(hashtext($1))`, org); err != nil {
		return CapacityHold{}, err
	}
	holds, err := s.listHoldsTx(tx, org)
	if err != nil {
		return CapacityHold{}, err
	}
	pool := capacityPoolFromHolds(holds, now)
	if pool.Available < units {
		return CapacityHold{}, fmt.Errorf("no capacity: available=%d need=%d", pool.Available, units)
	}
	h := CapacityHold{
		HoldID:    uuid.NewString(),
		OrgID:     org,
		LeadID:    strings.TrimSpace(leadID),
		Units:     units,
		State:     CapacityStateHold,
		CreatedAt: now,
		ExpiresAt: now.Add(CapacityHoldTTL),
	}
	raw, err := json.Marshal(h)
	if err != nil {
		return CapacityHold{}, err
	}
	_, err = tx.Exec(context.Background(), `
		INSERT INTO outreach_intel_capacity_holds (
			hold_id, organization_id, lead_id, units, state, created_at, expires_at, payload
		) VALUES ($1, $2::uuid, $3, $4, $5, $6, $7, $8::jsonb)`,
		h.HoldID, org, h.LeadID, h.Units, h.State, h.CreatedAt, h.ExpiresAt, raw,
	)
	if err != nil {
		return CapacityHold{}, err
	}
	if err := tx.Commit(context.Background()); err != nil {
		return CapacityHold{}, err
	}
	return h, nil
}

func (s *PGStore) ReleaseCapacity(orgID, holdID string) error {
	if s == nil || s.db == nil {
		return errors.New("intel pg store unavailable")
	}
	org := firstNonEmpty(orgID, s.orgID)
	h, err := s.GetHold(holdID)
	if err != nil || h == nil {
		return err
	}
	if org != "" && h.OrgID != org {
		return nil
	}
	h.State = CapacityStateRelease
	return s.updateHold(*h)
}

func (s *PGStore) FinalizeCapacity(orgID, holdID string, now time.Time) error {
	if s == nil || s.db == nil {
		return errors.New("intel pg store unavailable")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	org := firstNonEmpty(orgID, s.orgID)
	h, err := s.GetHold(holdID)
	if err != nil {
		return err
	}
	if h == nil || (org != "" && h.OrgID != org) {
		return fmt.Errorf("hold not found")
	}
	if h.State == CapacityStateExpired || (!h.ExpiresAt.IsZero() && now.After(h.ExpiresAt) && h.State != CapacityStateFinal) {
		h.State = CapacityStateExpired
		_ = s.updateHold(*h)
		return fmt.Errorf("hold expired")
	}
	if h.State == CapacityStateRelease {
		return fmt.Errorf("hold released")
	}
	t := now
	h.State = CapacityStateFinal
	h.FinalizedAt = &t
	return s.updateHold(*h)
}

func (s *PGStore) GetCapacityPool(orgID string, now time.Time) CapacityPool {
	if s == nil || s.db == nil {
		return CapacityPool{PolicyVersion: CapacityPolicyV1, Limit: CapacityLimitV1, Available: CapacityLimitV1}
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	org := firstNonEmpty(orgID, s.orgID)
	holds, err := s.listHolds(org)
	if err != nil {
		return CapacityPool{PolicyVersion: CapacityPolicyV1, Limit: CapacityLimitV1, Available: CapacityLimitV1}
	}
	return capacityPoolFromHolds(holds, now)
}

func (s *PGStore) GetHold(holdID string) (*CapacityHold, error) {
	if s == nil || s.db == nil || strings.TrimSpace(holdID) == "" {
		return nil, nil
	}
	var raw []byte
	err := s.db.QueryRow(context.Background(), `
		SELECT payload FROM outreach_intel_capacity_holds WHERE hold_id = $1`, strings.TrimSpace(holdID)).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var h CapacityHold
	if err := json.Unmarshal(raw, &h); err != nil {
		return nil, err
	}
	return &h, nil
}

func (s *PGStore) updateHold(h CapacityHold) error {
	raw, err := json.Marshal(h)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(context.Background(), `
		UPDATE outreach_intel_capacity_holds
		SET state = $2, finalized_at = $3, payload = $4::jsonb
		WHERE hold_id = $1`, h.HoldID, h.State, h.FinalizedAt, raw)
	return err
}

func (s *PGStore) listHolds(orgID string) ([]CapacityHold, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("intel pg store unavailable")
	}
	rows, err := s.db.Query(context.Background(), `
		SELECT payload FROM outreach_intel_capacity_holds WHERE organization_id = $1::uuid`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CapacityHold
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var h CapacityHold
		if err := json.Unmarshal(raw, &h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *PGStore) listHoldsTx(tx pgx.Tx, orgID string) ([]CapacityHold, error) {
	rows, err := tx.Query(context.Background(), `
		SELECT payload FROM outreach_intel_capacity_holds WHERE organization_id = $1::uuid`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CapacityHold
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var h CapacityHold
		if err := json.Unmarshal(raw, &h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
