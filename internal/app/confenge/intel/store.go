package intel

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Store is this capability's own rows. It is not the execution ledger
// and it never writes extra-cli, web-cfg, or SmartLic.
type Store interface {
	GetChain(orgID, identity string) (*Chain, error)
	PutChain(c Chain) (Chain, bool, error)
	UpdateChain(c Chain) error
	ListChains(orgID string) ([]Chain, error)
	PutException(ex Exception) error
	GetException(orgID, id string) (*Exception, error)
	UpdateException(ex Exception) error
	ListExceptions(orgID string) ([]Exception, error)
	PutLearning(c LearningCandidate) (LearningCandidate, error)
	ListLearning(orgID string) ([]LearningCandidate, error)
}

// MemoryStore is the shipped in-process store. Tests drive Reconcile
// and Rollup through this type.
type MemoryStore struct {
	mu         sync.Mutex
	chains     map[string]Chain
	exceptions []Exception
	learning   []LearningCandidate
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{chains: map[string]Chain{}}
}

func chainKey(orgID, identity string) string {
	return strings.TrimSpace(orgID) + "\x00" + strings.TrimSpace(identity)
}

func (m *MemoryStore) GetChain(orgID, identity string) (*Chain, error) {
	if m == nil {
		return nil, nil
	}
	if strings.TrimSpace(identity) == "" {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.chains[chainKey(orgID, identity)]
	if !ok {
		return nil, nil
	}
	cp := c
	return &cp, nil
}

func (m *MemoryStore) PutChain(c Chain) (Chain, bool, error) {
	if m == nil {
		return c, false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.chains == nil {
		m.chains = map[string]Chain{}
	}
	key := chainKey(c.Keys.OrganizationID, c.Identity)
	if existing, ok := m.chains[key]; ok {
		return existing, false, nil
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	m.chains[key] = c
	return c, true, nil
}

func (m *MemoryStore) UpdateChain(c Chain) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.chains == nil {
		m.chains = map[string]Chain{}
	}
	key := chainKey(c.Keys.OrganizationID, c.Identity)
	if _, ok := m.chains[key]; !ok {
		return nil
	}
	m.chains[key] = c
	return nil
}

func (m *MemoryStore) ListChains(orgID string) ([]Chain, error) {
	if m == nil {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	orgID = strings.TrimSpace(orgID)
	out := make([]Chain, 0, len(m.chains))
	for _, c := range m.chains {
		if orgID == "" || c.Keys.OrganizationID == orgID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Identity == out[j].Identity {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].Identity < out[j].Identity
	})
	return out, nil
}

func (m *MemoryStore) PutException(ex Exception) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ex = assignExceptionID(ex)
	if ex.At.IsZero() {
		ex.At = time.Now().UTC()
	}
	for _, existing := range m.exceptions {
		if existing.ID == ex.ID {
			return nil
		}
	}
	m.exceptions = append(m.exceptions, ex)
	return nil
}

func (m *MemoryStore) GetException(orgID, id string) (*Exception, error) {
	if m == nil {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	orgID = strings.TrimSpace(orgID)
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	for i := range m.exceptions {
		ex := m.exceptions[i]
		if ex.ID != id {
			continue
		}
		if orgID != "" && ex.OrganizationID != orgID {
			continue
		}
		cp := ex
		return &cp, nil
	}
	return nil, nil
}

func (m *MemoryStore) UpdateException(ex Exception) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ex = assignExceptionID(ex)
	for i := range m.exceptions {
		if m.exceptions[i].ID != ex.ID {
			continue
		}
		if org := strings.TrimSpace(ex.OrganizationID); org != "" && m.exceptions[i].OrganizationID != org {
			continue
		}
		m.exceptions[i] = ex
		return nil
	}
	return nil
}

func (m *MemoryStore) ListExceptions(orgID string) ([]Exception, error) {
	if m == nil {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	orgID = strings.TrimSpace(orgID)
	out := make([]Exception, 0, len(m.exceptions))
	for _, ex := range m.exceptions {
		if orgID == "" || ex.OrganizationID == orgID {
			out = append(out, ex)
		}
	}
	return out, nil
}

func (m *MemoryStore) PutLearning(c LearningCandidate) (LearningCandidate, error) {
	if m == nil {
		return c, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.TrimSpace(c.ID) == "" {
		c.ID = uuid.NewString()
	}
	if c.At.IsZero() {
		c.At = time.Now().UTC()
	}
	m.learning = append(m.learning, c)
	return c, nil
}

func (m *MemoryStore) ListLearning(orgID string) ([]LearningCandidate, error) {
	if m == nil {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]LearningCandidate, len(m.learning))
	copy(out, m.learning)
	return out, nil
}
