package proposal

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"
)

var (
	ErrNotFound            = errors.New("proposal not found")
	ErrConflict            = errors.New("proposal optimistic concurrency conflict")
	ErrIdempotencyConflict = errors.New("idempotency key reused with different command")
	ErrAcceptedImmutable   = errors.New("accepted proposal version is immutable")
	ErrIllegalTransition   = errors.New("illegal proposal transition")
	ErrNoMaterialChange    = errors.New("revision has no material change")
	ErrProposalExpired     = errors.New("proposal validity expired")
	ErrOutOfOrder          = errors.New("proposal command occurred out of order")
	ErrSyntheticMismatch   = errors.New("synthetic classification is immutable across proposal versions")
)

type ExpectedVersion struct {
	ProposalVersion int
	RecordVersion   int64
}

type Mutation struct {
	OrganizationID  uuid.UUID
	IdempotencyKey  string
	PayloadHash     string
	Expected        *ExpectedVersion
	Insert          bool
	NoProposalWrite bool
	Proposal        Proposal
	Events          []ProposalEvent
	Handoff         *DeliveryOrderRequested
}

type CommandReceipt struct {
	OrganizationID uuid.UUID               `json:"organization_id"`
	IdempotencyKey string                  `json:"idempotency_key"`
	PayloadHash    string                  `json:"payload_hash"`
	Proposal       Proposal                `json:"proposal"`
	Events         []ProposalEvent         `json:"events"`
	Handoff        *DeliveryOrderRequested `json:"handoff,omitempty"`
}

type Store interface {
	Get(context.Context, uuid.UUID, uuid.UUID, int) (*Proposal, error)
	Latest(context.Context, uuid.UUID, uuid.UUID) (*Proposal, error)
	Receipt(context.Context, uuid.UUID, string) (*CommandReceipt, error)
	Apply(context.Context, Mutation) (CommandReceipt, bool, error)
}

type MemoryStore struct {
	mu       sync.Mutex
	rows     map[string]Proposal
	receipts map[string]CommandReceipt
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{rows: make(map[string]Proposal), receipts: make(map[string]CommandReceipt)}
}

func proposalKey(orgID, proposalID uuid.UUID, version int) string {
	return orgID.String() + "\x00" + proposalID.String() + fmt.Sprintf("\x00%d", version)
}

func receiptKey(orgID uuid.UUID, key string) string {
	return orgID.String() + "\x00" + key
}

func (m *MemoryStore) Get(_ context.Context, orgID, proposalID uuid.UUID, version int) (*Proposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.rows[proposalKey(orgID, proposalID, version)]
	if !ok {
		return nil, nil
	}
	return cloneProposal(row), nil
}

func (m *MemoryStore) Latest(_ context.Context, orgID, proposalID uuid.UUID) (*Proposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest *Proposal
	for _, row := range m.rows {
		if row.OrganizationID != orgID || row.ProposalID != proposalID {
			continue
		}
		if latest == nil || row.ProposalVersion > latest.ProposalVersion {
			latest = cloneProposal(row)
		}
	}
	return latest, nil
}

func (m *MemoryStore) Receipt(_ context.Context, orgID uuid.UUID, key string) (*CommandReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	receipt, ok := m.receipts[receiptKey(orgID, key)]
	if !ok {
		return nil, nil
	}
	cloned := cloneReceipt(receipt)
	return &cloned, nil
}

func (m *MemoryStore) Apply(_ context.Context, mutation Mutation) (CommandReceipt, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := receiptKey(mutation.OrganizationID, mutation.IdempotencyKey)
	if receipt, ok := m.receipts[key]; ok {
		if receipt.PayloadHash != mutation.PayloadHash {
			return CommandReceipt{}, false, ErrIdempotencyConflict
		}
		return cloneReceipt(receipt), true, nil
	}
	if mutation.Expected != nil {
		current, ok := m.rows[proposalKey(mutation.OrganizationID, mutation.Proposal.ProposalID, mutation.Expected.ProposalVersion)]
		if !ok || current.Version != mutation.Expected.RecordVersion {
			return CommandReceipt{}, false, ErrConflict
		}
		if !mutation.Insert && !mutation.NoProposalWrite && current.DecisionState == StateAccepted {
			return CommandReceipt{}, false, ErrAcceptedImmutable
		}
	}
	rowKey := proposalKey(mutation.OrganizationID, mutation.Proposal.ProposalID, mutation.Proposal.ProposalVersion)
	if _, exists := m.rows[rowKey]; exists && mutation.Insert {
		return CommandReceipt{}, false, ErrConflict
	}
	if !mutation.NoProposalWrite {
		m.rows[rowKey] = *cloneProposal(mutation.Proposal)
	}
	receipt := CommandReceipt{
		OrganizationID: mutation.OrganizationID, IdempotencyKey: mutation.IdempotencyKey,
		PayloadHash: mutation.PayloadHash, Proposal: mutation.Proposal,
		Events: mutation.Events, Handoff: mutation.Handoff,
	}
	m.receipts[key] = cloneReceipt(receipt)
	return receipt, false, nil
}

func cloneProposal(value Proposal) *Proposal {
	value.Credits = cloneStrings(value.Credits)
	value.Addons = cloneStrings(value.Addons)
	value.Inputs = cloneStrings(value.Inputs)
	value.Exclusions = cloneStrings(value.Exclusions)
	value.EvidenceRefs = cloneStrings(value.EvidenceRefs)
	return &value
}

func cloneReceipt(value CommandReceipt) CommandReceipt {
	value.Proposal = *cloneProposal(value.Proposal)
	value.Events = append([]ProposalEvent(nil), value.Events...)
	for index := range value.Events {
		value.Events[index].EvidenceRefs = cloneStrings(value.Events[index].EvidenceRefs)
	}
	if value.Handoff != nil {
		handoff := *value.Handoff
		handoff.EvidenceRefs = append([]string(nil), value.Handoff.EvidenceRefs...)
		handoff.FinancialGate.EvidenceRefs = append([]string(nil), value.Handoff.FinancialGate.EvidenceRefs...)
		value.Handoff = &handoff
	}
	return value
}

func cloneStrings(value []string) []string {
	if value == nil {
		return nil
	}
	return append(make([]string, 0, len(value)), value...)
}
