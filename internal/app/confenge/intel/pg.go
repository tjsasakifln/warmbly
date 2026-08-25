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
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return c, false, err
	}
	ctx := context.Background()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return c, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		INSERT INTO outreach_intel_chains (
			id, organization_id, identity, metric_key, route_family, label, synthetic, payload, created_at
		) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8::jsonb, $9)
		ON CONFLICT (organization_id, identity) DO NOTHING`,
		uuid.New(), org, c.Identity, c.MetricKey, c.RouteFamily, c.Label, c.Synthetic, raw, c.CreatedAt,
	)
	if err != nil {
		return c, false, err
	}
	if tag.RowsAffected() == 0 {
		var existingRaw []byte
		if err := tx.QueryRow(ctx, `
			SELECT payload FROM outreach_intel_chains
			WHERE organization_id = $1::uuid AND identity = $2`, org, c.Identity).Scan(&existingRaw); err != nil {
			return c, false, err
		}
		var existing Chain
		if err := json.Unmarshal(existingRaw, &existing); err != nil {
			return c, false, err
		}
		if err := syncIdentityLinksTx(ctx, tx, org, existing); err != nil {
			return c, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return c, false, err
		}
		return existing, false, nil
	}
	if err := syncIdentityLinksTx(ctx, tx, org, c); err != nil {
		return c, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return c, false, err
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
	ctx := context.Background()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		UPDATE outreach_intel_chains
		SET metric_key = $3, route_family = $4, payload = $5::jsonb
		WHERE organization_id = $1::uuid AND identity = $2`,
		org, c.Identity, c.MetricKey, c.RouteFamily, raw,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("canonical chain not found")
	}
	if err := syncIdentityLinksTx(ctx, tx, org, c); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func syncIdentityLinksTx(ctx context.Context, tx pgx.Tx, org string, c Chain) error {
	identity := CanonicalIdentityOf(c)
	if identity.CorrelationID == Unknown {
		return nil
	}
	type identityLink struct {
		kind string
		id   string
	}
	links := []identityLink{
		{kind: "account", id: identity.AccountID},
		{kind: "opportunity", id: identity.OpportunityID},
		{kind: "offer", id: identity.OfferID},
		{kind: "proposal", id: identity.ProposalID},
		{kind: "charge", id: identity.ChargeID},
		{kind: "payment", id: identity.PaymentID},
	}
	for _, receipt := range c.Commercial.Timeline {
		links = append(links,
			identityLink{kind: "charge", id: canonicalID(receipt.ChargeID)},
			identityLink{kind: "payment", id: canonicalID(receipt.PaymentID)},
		)
	}
	for _, link := range links {
		if link.id == Unknown {
			continue
		}
		if globallyUniqueIdentityKind(link.kind) {
			var bound string
			err := tx.QueryRow(ctx, `
				SELECT correlation_id
				FROM outreach_intel_identity_links
				WHERE organization_id = $1::uuid AND entity_kind = $2 AND entity_id = $3`,
				org, link.kind, link.id).Scan(&bound)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if err == nil && bound != identity.CorrelationID {
				return fmt.Errorf("canonical %s id is already bound to another correlation", link.kind)
			}
		}
		if singletonIdentityKind(link.kind) {
			var boundID string
			err := tx.QueryRow(ctx, `
				SELECT entity_id
				FROM outreach_intel_identity_links
				WHERE organization_id = $1::uuid AND correlation_id = $2 AND entity_kind = $3`,
				org, identity.CorrelationID, link.kind).Scan(&boundID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if err == nil && boundID != link.id {
				return fmt.Errorf("canonical correlation already has another %s id", link.kind)
			}
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO outreach_intel_identity_links (
				organization_id, correlation_id, entity_kind, entity_id
			) VALUES ($1::uuid, $2, $3, $4)
			ON CONFLICT (organization_id, correlation_id, entity_kind, entity_id) DO NOTHING`,
			org, identity.CorrelationID, link.kind, link.id)
		if err != nil {
			return err
		}
	}
	return nil
}

func globallyUniqueIdentityKind(kind string) bool {
	switch kind {
	case "opportunity", "proposal", "charge", "payment":
		return true
	default:
		return false
	}
}

func singletonIdentityKind(kind string) bool {
	switch kind {
	case "account", "opportunity", "offer", "proposal":
		return true
	default:
		return false
	}
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
		if strings.TrimSpace(r.Identity) != "" {
			if existing.Identity != "" && existing.Identity != r.Identity {
				return r, false, errors.New("provider receipt already bound to another chain")
			}
			existing.Identity = r.Identity
			existing.EventID = firstNonEmpty(existing.EventID, r.EventID)
			org := firstNonEmpty(r.OrganizationID, s.orgID)
			tag, err := s.db.Exec(context.Background(), `
				UPDATE outreach_intel_event_receipts
				SET identity = $3,
					event_id = $4,
					payload = jsonb_set(
						jsonb_set(payload, '{identity}', to_jsonb($3::text), true),
						'{event_id}', to_jsonb($4::text), true
					)
				WHERE organization_id = $1::uuid AND provider_event_id = $2
					AND (identity = '' OR identity = $3)`,
				org, r.ProviderEventID, existing.Identity, existing.EventID)
			if err != nil {
				return r, false, err
			}
			if tag.RowsAffected() == 0 {
				return r, false, errors.New("provider receipt already bound to another chain")
			}
		}
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
	tag, err := s.db.Exec(context.Background(), `
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
	if tag.RowsAffected() == 0 {
		return s.PutEventReceipt(r)
	}
	return r, true, nil
}

func (s *PGStore) GetEventReceipt(orgID, providerEventID string) (*EventReceipt, error) {
	if s == nil || s.db == nil || strings.TrimSpace(providerEventID) == "" {
		return nil, nil
	}
	org := firstNonEmpty(orgID, s.orgID)
	var raw []byte
	var acked, processed bool
	err := s.db.QueryRow(context.Background(), `
		SELECT payload, acked, processed FROM outreach_intel_event_receipts
		WHERE organization_id = $1::uuid AND provider_event_id = $2`, org, providerEventID).Scan(&raw, &acked, &processed)
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
	r.Acked = acked
	r.Processed = processed
	return &r, nil
}

func (s *PGStore) MarkReceiptProcessed(orgID, providerEventID string) error {
	if s == nil || s.db == nil {
		return errors.New("intel pg store unavailable")
	}
	org := firstNonEmpty(orgID, s.orgID)
	tag, err := s.db.Exec(context.Background(), `
		UPDATE outreach_intel_event_receipts
		SET processed = true,
			payload = jsonb_set(payload, '{processed}', 'true'::jsonb, true)
		WHERE organization_id = $1::uuid AND provider_event_id = $2`, org, providerEventID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("provider receipt not found")
	}
	return nil
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

func (s *PGStore) PutSearchObservation(obs SearchObservation) (SearchObservation, bool, error) {
	if s == nil || s.db == nil {
		return obs, false, errors.New("intel pg store unavailable")
	}
	org := firstNonEmpty(obs.OrganizationID, s.orgID)
	if existing, err := s.GetSearchObservation(org, obs.EventID); err != nil {
		return obs, false, err
	} else if existing != nil {
		existing.Replay = true
		return *existing, false, nil
	}
	if obs.CreatedAt.IsZero() {
		obs.CreatedAt = time.Now().UTC()
	}
	raw, err := json.Marshal(obs)
	if err != nil {
		return obs, false, err
	}
	_, err = s.db.Exec(context.Background(), `
		INSERT INTO outreach_intel_search_observations (
			id, organization_id, event_id, receipt_id, payload_hash, "window",
			organic_source, asset_family, asset_id, landing_path, intent_class, query_class,
			eligible, appeared, clicked, engaged, coverage, freshness,
			producer_source, producer_sha, synthetic, record_kind, consent_policy,
			measurement_at, replay, out_of_order, payload, created_at
		) VALUES (
			$1::uuid, $2::uuid, $3, $4, $5, $6,
			$7, $8, $9, $10, $11, $12,
			$13, $14, $15, $16, $17, $18,
			$19, $20, $21, $22, $23,
			$24, $25, $26, $27::jsonb, $28
		)
		ON CONFLICT (organization_id, event_id) DO NOTHING`,
		uuid.New(), org, obs.EventID, obs.ReceiptID, obs.PayloadHash, obs.Window,
		obs.OrganicSource, obs.AssetFamily, obs.AssetID, obs.LandingPath, obs.IntentClass, obs.QueryClass,
		obs.Eligible, obs.Appeared, obs.Clicked, obs.Engaged, firstNonEmpty(obs.Coverage, CoverageUnknown), obs.Freshness,
		obs.Source, obs.ProducerSHA, obs.Synthetic, obs.RecordKind, firstNonEmpty(obs.ConsentPolicy, ConsentPolicyNotApplicable),
		obs.MeasurementAt, obs.Replay, obs.OutOfOrder, raw, obs.CreatedAt,
	)
	if err != nil {
		return obs, false, err
	}
	if existing, gerr := s.GetSearchObservation(org, obs.EventID); gerr == nil && existing != nil {
		return *existing, existing.PayloadHash == obs.PayloadHash && existing.CreatedAt.Equal(obs.CreatedAt), nil
	}
	return obs, true, nil
}

func (s *PGStore) GetSearchObservation(orgID, eventID string) (*SearchObservation, error) {
	if s == nil || s.db == nil || strings.TrimSpace(eventID) == "" {
		return nil, nil
	}
	org := firstNonEmpty(orgID, s.orgID)
	var raw []byte
	err := s.db.QueryRow(context.Background(), `
		SELECT payload FROM outreach_intel_search_observations
		WHERE organization_id = $1::uuid AND event_id = $2`, org, strings.TrimSpace(eventID)).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var obs SearchObservation
	if err := json.Unmarshal(raw, &obs); err != nil {
		return nil, err
	}
	return &obs, nil
}

func (s *PGStore) ListSearchObservations(orgID, window string) ([]SearchObservation, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	org := firstNonEmpty(orgID, s.orgID)
	window = strings.TrimSpace(window)
	var rows pgx.Rows
	var err error
	if window == "" {
		rows, err = s.db.Query(context.Background(), `
			SELECT payload FROM outreach_intel_search_observations
			WHERE organization_id = $1::uuid
			ORDER BY measurement_at, event_id`, org)
	} else {
		rows, err = s.db.Query(context.Background(), `
			SELECT payload FROM outreach_intel_search_observations
			WHERE organization_id = $1::uuid AND "window" = $2
			ORDER BY measurement_at, event_id`, org, window)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchObservation
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var obs SearchObservation
		if err := json.Unmarshal(raw, &obs); err != nil {
			return nil, err
		}
		out = append(out, obs)
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
