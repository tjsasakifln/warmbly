package intel

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// CapacityStore is the versioned 50-unit pool. MemoryStore and PGStore
// both implement it so WireIntel is operational after deploy.
type CapacityStore interface {
	HoldCapacity(orgID, leadID string, units int, now time.Time) (CapacityHold, error)
	ReleaseCapacity(orgID, holdID string) error
	FinalizeCapacity(orgID, holdID string, now time.Time) error
	GetCapacityPool(orgID string, now time.Time) CapacityPool
}

var (
	_ CapacityStore = (*MemoryStore)(nil)
	_ CapacityStore = (*PGStore)(nil)
)

// DefaultCapacityPolicy is the versioned 50-unit pool.
func DefaultCapacityPolicy() CapacitySnapshot {
	return CapacitySnapshot{
		PolicyVersion: CapacityPolicyV1,
		Limit:         CapacityLimitV1,
		Units:         1,
		Eligibility:   EligibilityUnknown,
		State:         CapacityStateNone,
	}
}

func (m *MemoryStore) HoldCapacity(orgID, leadID string, units int, now time.Time) (CapacityHold, error) {
	if m == nil {
		return CapacityHold{}, fmt.Errorf("capacity store unavailable")
	}
	if units <= 0 {
		units = 1
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.holds == nil {
		m.holds = map[string]CapacityHold{}
	}
	pool := capacityPoolLocked(m, orgID, now)
	if pool.Available < units {
		return CapacityHold{}, fmt.Errorf("no capacity: available=%d need=%d", pool.Available, units)
	}
	h := CapacityHold{
		HoldID:    uuid.NewString(),
		OrgID:     strings.TrimSpace(orgID),
		LeadID:    strings.TrimSpace(leadID),
		Units:     units,
		State:     CapacityStateHold,
		CreatedAt: now,
		ExpiresAt: now.Add(CapacityHoldTTL),
	}
	m.holds[h.HoldID] = h
	return h, nil
}

func (m *MemoryStore) ReleaseCapacity(orgID, holdID string) error {
	if m == nil {
		return fmt.Errorf("capacity store unavailable")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.holds[strings.TrimSpace(holdID)]
	if !ok {
		return nil
	}
	if org := strings.TrimSpace(orgID); org != "" && h.OrgID != org {
		return nil
	}
	h.State = CapacityStateRelease
	m.holds[h.HoldID] = h
	return nil
}

func (m *MemoryStore) FinalizeCapacity(orgID, holdID string, now time.Time) error {
	if m == nil {
		return fmt.Errorf("capacity store unavailable")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.holds[strings.TrimSpace(holdID)]
	if !ok {
		return fmt.Errorf("hold not found")
	}
	if org := strings.TrimSpace(orgID); org != "" && h.OrgID != org {
		return fmt.Errorf("hold not found")
	}
	if h.State == CapacityStateExpired || (!h.ExpiresAt.IsZero() && now.After(h.ExpiresAt) && h.State != CapacityStateFinal) {
		h.State = CapacityStateExpired
		m.holds[h.HoldID] = h
		return fmt.Errorf("hold expired")
	}
	if h.State == CapacityStateRelease {
		return fmt.Errorf("hold released")
	}
	t := now
	h.State = CapacityStateFinal
	h.FinalizedAt = &t
	m.holds[h.HoldID] = h
	return nil
}

func (m *MemoryStore) GetCapacityPool(orgID string, now time.Time) CapacityPool {
	if m == nil {
		return CapacityPool{PolicyVersion: CapacityPolicyV1, Limit: CapacityLimitV1, Available: CapacityLimitV1}
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return capacityPoolLocked(m, orgID, now)
}

func (m *MemoryStore) GetHold(holdID string) *CapacityHold {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.holds[strings.TrimSpace(holdID)]
	if !ok {
		return nil
	}
	cp := h
	return &cp
}

func capacityPoolLocked(m *MemoryStore, orgID string, now time.Time) CapacityPool {
	orgID = strings.TrimSpace(orgID)
	var holds []CapacityHold
	for _, h := range m.holds {
		if orgID != "" && h.OrgID != orgID {
			continue
		}
		holds = append(holds, h)
	}
	return capacityPoolFromHolds(holds, now)
}

func capacityPoolFromHolds(holds []CapacityHold, now time.Time) CapacityPool {
	p := CapacityPool{PolicyVersion: CapacityPolicyV1, Limit: CapacityLimitV1}
	for _, h := range holds {
		switch {
		case h.State == CapacityStateFinal:
			p.Used += h.Units
		case h.State == CapacityStateHold && !h.ExpiresAt.IsZero() && now.After(h.ExpiresAt):
			p.Expired += h.Units
		case h.State == CapacityStateHold:
			p.Held += h.Units
		case h.State == CapacityStateExpired:
			p.Expired += h.Units
		}
	}
	p.Available = p.Limit - p.Used - p.Held
	if p.Available < 0 {
		p.Available = 0
	}
	return p
}

func holdValid(c CapacitySnapshot, now time.Time) bool {
	if strings.EqualFold(c.State, CapacityStateOK) || strings.EqualFold(c.State, CapacityStateFinal) {
		return true
	}
	if !strings.EqualFold(c.State, CapacityStateHold) {
		return false
	}
	if c.HoldExpiresAt == nil {
		return false
	}
	return !now.After(*c.HoldExpiresAt)
}

func holdExpired(c CapacitySnapshot, now time.Time) bool {
	if strings.EqualFold(c.State, CapacityStateExpired) {
		return true
	}
	if c.HoldExpiresAt == nil {
		return false
	}
	if strings.EqualFold(c.State, CapacityStateHold) || strings.EqualFold(c.State, CapacityStateOK) {
		return now.After(*c.HoldExpiresAt)
	}
	return false
}
