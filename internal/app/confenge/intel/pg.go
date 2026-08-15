package intel

import (
	"context"
	"encoding/json"
	"errors"
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
		SELECT payload FROM outreach_intel_chains WHERE organization_id = $1::uuid`, org)
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
	if strings.TrimSpace(ex.ID) == "" {
		ex.ID = uuid.NewString()
	}
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
		) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7::jsonb, $8)`,
		ex.ID, org, ex.Code, ex.Identity, ex.MetricKey, ex.Held, raw, ex.At,
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
