package intel

import (
	"errors"
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
	PutSearchObservation(obs SearchObservation) (SearchObservation, bool, error)
	GetSearchObservation(orgID, eventID string) (*SearchObservation, error)
	ListSearchObservations(orgID, window string) ([]SearchObservation, error)
}

// MemoryStore is the shipped in-process store. Tests drive Reconcile
// and Rollup through this type.
type MemoryStore struct {
	mu           sync.Mutex
	chains       map[string]Chain
	exceptions   []Exception
	learning     []LearningCandidate
	receipts     map[string]EventReceipt
	holds        map[string]CapacityHold
	observations map[string]SearchObservation
	failPuts     bool
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		chains:       map[string]Chain{},
		receipts:     map[string]EventReceipt{},
		holds:        map[string]CapacityHold{},
		observations: map[string]SearchObservation{},
	}
}

// SetUnavailable makes PutChain/PutException fail closed (consumer down).
func (m *MemoryStore) SetUnavailable(v bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failPuts = v
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
	if m.failPuts {
		return c, false, errUnavailable("put chain")
	}
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
	if m.failPuts {
		return errUnavailable("put exception")
	}
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

func receiptKey(orgID, providerEventID string) string {
	return strings.TrimSpace(orgID) + "\x00" + strings.TrimSpace(providerEventID)
}

func (m *MemoryStore) PutEventReceipt(r EventReceipt) (EventReceipt, bool, error) {
	if m == nil {
		return r, false, errUnavailable("receipt store")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failPuts {
		return r, false, errUnavailable("put receipt")
	}
	if m.receipts == nil {
		m.receipts = map[string]EventReceipt{}
	}
	key := receiptKey(r.OrganizationID, firstNonEmpty(r.ProviderEventID, r.EventID))
	if existing, ok := m.receipts[key]; ok {
		if strings.TrimSpace(r.Identity) != "" {
			if existing.Identity != "" && existing.Identity != r.Identity {
				return r, false, errors.New("provider receipt already bound to another chain")
			}
			existing.Identity = r.Identity
			existing.EventID = firstNonEmpty(existing.EventID, r.EventID)
			m.receipts[key] = existing
		}
		return existing, false, nil
	}
	if strings.TrimSpace(r.ID) == "" {
		r.ID = uuid.NewString()
	}
	if r.At.IsZero() {
		r.At = time.Now().UTC()
	}
	r.Acked = true
	m.receipts[key] = r
	return r, true, nil
}

func (m *MemoryStore) GetEventReceipt(orgID, providerEventID string) (*EventReceipt, error) {
	if m == nil {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.receipts[receiptKey(orgID, providerEventID)]
	if !ok {
		return nil, nil
	}
	cp := r
	return &cp, nil
}

func (m *MemoryStore) MarkReceiptProcessed(orgID, providerEventID string) error {
	if m == nil {
		return errUnavailable("mark receipt")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := receiptKey(orgID, providerEventID)
	r, ok := m.receipts[key]
	if !ok {
		return errors.New("provider receipt not found")
	}
	r.Processed = true
	m.receipts[key] = r
	return nil
}

type unavailableError struct{ op string }

func (e unavailableError) Error() string { return e.op + ": commercial intelligence store unavailable" }

func errUnavailable(op string) error { return unavailableError{op: op} }

func observationKey(orgID, eventID string) string {
	return strings.TrimSpace(orgID) + "\x00" + strings.TrimSpace(eventID)
}

func (m *MemoryStore) PutSearchObservation(obs SearchObservation) (SearchObservation, bool, error) {
	if m == nil {
		return obs, false, errUnavailable("put search observation")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failPuts {
		return obs, false, errUnavailable("put search observation")
	}
	if m.observations == nil {
		m.observations = map[string]SearchObservation{}
	}
	key := observationKey(obs.OrganizationID, obs.EventID)
	if existing, ok := m.observations[key]; ok {
		existing.Replay = true
		return existing, false, nil
	}
	if obs.CreatedAt.IsZero() {
		obs.CreatedAt = time.Now().UTC()
	}
	m.observations[key] = obs
	return obs, true, nil
}

func (m *MemoryStore) GetSearchObservation(orgID, eventID string) (*SearchObservation, error) {
	if m == nil {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	obs, ok := m.observations[observationKey(orgID, eventID)]
	if !ok {
		return nil, nil
	}
	cp := obs
	return &cp, nil
}

func (m *MemoryStore) ListSearchObservations(orgID, window string) ([]SearchObservation, error) {
	if m == nil {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	orgID = strings.TrimSpace(orgID)
	window = strings.TrimSpace(window)
	out := make([]SearchObservation, 0, len(m.observations))
	for _, obs := range m.observations {
		if orgID != "" && obs.OrganizationID != orgID {
			continue
		}
		if window != "" && obs.Window != window {
			continue
		}
		out = append(out, obs)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].MeasurementAt.Equal(out[j].MeasurementAt) {
			return out[i].EventID < out[j].EventID
		}
		return out[i].MeasurementAt.Before(out[j].MeasurementAt)
	})
	return out, nil
}
