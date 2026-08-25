package proposal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PGStore struct {
	db *pgxpool.Pool
}

func NewPGStore(db *pgxpool.Pool) *PGStore {
	return &PGStore{db: db}
}

func (s *PGStore) Get(ctx context.Context, orgID, proposalID uuid.UUID, version int) (*Proposal, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("proposal pg store unavailable")
	}
	return getProposal(ctx, s.db, orgID, proposalID, version)
}

func (s *PGStore) Latest(ctx context.Context, orgID, proposalID uuid.UUID) (*Proposal, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("proposal pg store unavailable")
	}
	var payload []byte
	err := s.db.QueryRow(ctx, `
		SELECT payload
		FROM confenge_proposals
		WHERE organization_id = $1 AND proposal_id = $2
		ORDER BY proposal_version DESC
		LIMIT 1`, orgID, proposalID).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p Proposal
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *PGStore) Receipt(ctx context.Context, orgID uuid.UUID, key string) (*CommandReceipt, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("proposal pg store unavailable")
	}
	receipt, ok, err := getReceipt(ctx, s.db, orgID, key)
	if err != nil || !ok {
		return nil, err
	}
	return &receipt, nil
}

func (s *PGStore) Apply(ctx context.Context, mutation Mutation) (CommandReceipt, bool, error) {
	if s == nil || s.db == nil {
		return CommandReceipt{}, false, fmt.Errorf("proposal pg store unavailable")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return CommandReceipt{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if receipt, ok, rerr := getReceipt(ctx, tx, mutation.OrganizationID, mutation.IdempotencyKey); rerr != nil {
		return CommandReceipt{}, false, rerr
	} else if ok {
		if receipt.PayloadHash != mutation.PayloadHash {
			return CommandReceipt{}, false, ErrIdempotencyConflict
		}
		return receipt, true, nil
	}

	if mutation.Expected != nil {
		var recordVersion int64
		var state State
		err = tx.QueryRow(ctx, `
			SELECT record_version, decision_state
			FROM confenge_proposals
			WHERE organization_id = $1 AND proposal_id = $2 AND proposal_version = $3
			FOR UPDATE`, mutation.OrganizationID, mutation.Proposal.ProposalID, mutation.Expected.ProposalVersion).
			Scan(&recordVersion, &state)
		if errors.Is(err, pgx.ErrNoRows) {
			return CommandReceipt{}, false, ErrConflict
		}
		if err != nil {
			return CommandReceipt{}, false, err
		}
		if recordVersion != mutation.Expected.RecordVersion {
			return CommandReceipt{}, false, ErrConflict
		}
		if !mutation.Insert && !mutation.NoProposalWrite && state == StateAccepted {
			return CommandReceipt{}, false, ErrAcceptedImmutable
		}
	}

	payload, err := json.Marshal(mutation.Proposal)
	if err != nil {
		return CommandReceipt{}, false, err
	}
	if mutation.NoProposalWrite {
		err = nil
	} else if mutation.Insert {
		err = insertProposal(ctx, tx, mutation.Proposal, payload)
	} else {
		err = updateProposal(ctx, tx, mutation.Proposal, payload, mutation.Expected)
	}
	if err != nil {
		return CommandReceipt{}, false, err
	}
	if err := insertEvents(ctx, tx, mutation); err != nil {
		return CommandReceipt{}, false, err
	}
	receipt := CommandReceipt{
		OrganizationID: mutation.OrganizationID, IdempotencyKey: mutation.IdempotencyKey,
		PayloadHash: mutation.PayloadHash, Proposal: mutation.Proposal,
		Events: mutation.Events, Handoff: mutation.Handoff,
	}
	receiptPayload, err := json.Marshal(receipt)
	if err != nil {
		return CommandReceipt{}, false, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO confenge_proposal_command_receipts (
			organization_id, idempotency_key, payload_hash, proposal_id, proposal_version, payload
		) VALUES ($1, $2, $3, $4, $5, $6)`, mutation.OrganizationID, mutation.IdempotencyKey,
		mutation.PayloadHash, mutation.Proposal.ProposalID, mutation.Proposal.ProposalVersion, receiptPayload)
	if err != nil {
		return CommandReceipt{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CommandReceipt{}, false, err
	}
	return receipt, false, nil
}

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func getProposal(ctx context.Context, q queryer, orgID, proposalID uuid.UUID, version int) (*Proposal, error) {
	var payload []byte
	err := q.QueryRow(ctx, `
		SELECT payload FROM confenge_proposals
		WHERE organization_id = $1 AND proposal_id = $2 AND proposal_version = $3`, orgID, proposalID, version).
		Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p Proposal
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func getReceipt(ctx context.Context, q queryer, orgID uuid.UUID, key string) (CommandReceipt, bool, error) {
	var payload []byte
	err := q.QueryRow(ctx, `
		SELECT payload FROM confenge_proposal_command_receipts
		WHERE organization_id = $1 AND idempotency_key = $2`, orgID, key).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return CommandReceipt{}, false, nil
	}
	if err != nil {
		return CommandReceipt{}, false, err
	}
	var receipt CommandReceipt
	if err := json.Unmarshal(payload, &receipt); err != nil {
		return CommandReceipt{}, false, err
	}
	return receipt, true, nil
}

func insertProposal(ctx context.Context, tx pgx.Tx, p Proposal, payload []byte) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO confenge_proposals (
			organization_id, proposal_id, proposal_version, account_id, client_ref,
			opportunity_id, qco_id, deal_id, correlation_id, offer_id, offer_version,
			deliverable_id, deliverable_version, scope_version, price_version, terms_version,
			decision_state, accepted_snapshot_hash, synthetic, record_version, valid_until,
			payload, created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24
		)`, p.OrganizationID, p.ProposalID, p.ProposalVersion, p.AccountID, p.ClientRef,
		p.OpportunityID, p.QCOID, nullableText(p.DealID), p.CorrelationID, p.OfferID, p.OfferVersion,
		p.DeliverableID, p.DeliverableVersion, p.ScopeVersion, p.PriceVersion, p.TermsVersion,
		p.DecisionState, nullableText(p.AcceptedSnapshotHash), p.Synthetic, p.Version, p.ValidUntil,
		payload, p.CreatedAt, p.UpdatedAt)
	return err
}

func updateProposal(ctx context.Context, tx pgx.Tx, p Proposal, payload []byte, expected *ExpectedVersion) error {
	if expected == nil {
		return ErrConflict
	}
	tag, err := tx.Exec(ctx, `
		UPDATE confenge_proposals
		SET decision_state=$5, accepted_snapshot_hash=$6, record_version=$7, payload=$8, updated_at=$9
		WHERE organization_id=$1 AND proposal_id=$2 AND proposal_version=$3 AND record_version=$4`,
		p.OrganizationID, p.ProposalID, p.ProposalVersion, expected.RecordVersion,
		p.DecisionState, nullableText(p.AcceptedSnapshotHash), p.Version, payload, p.UpdatedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func insertEvents(ctx context.Context, tx pgx.Tx, mutation Mutation) error {
	for _, event := range mutation.Events {
		payload, err := json.Marshal(event)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO confenge_proposal_events (
				event_id, organization_id, proposal_id, proposal_version, event_type,
				idempotency_key, occurred_at, payload
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, event.EventID, event.OrganizationID,
			event.ProposalID, event.ProposalVersion, event.EventType, mutation.IdempotencyKey,
			event.OccurredAt, payload)
		if err != nil {
			return err
		}
	}
	if mutation.Handoff == nil {
		return nil
	}
	payload, err := json.Marshal(mutation.Handoff)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO confenge_proposal_events (
			event_id, organization_id, proposal_id, proposal_version, event_type,
			idempotency_key, occurred_at, payload
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, mutation.Handoff.EventID, mutation.Handoff.OrganizationID,
		mutation.Handoff.ProposalID, mutation.Handoff.ProposalVersion, DeliveryRequestSchema,
		mutation.Handoff.IdempotencyKey, mutation.Handoff.OccurredAt, payload)
	return err
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}
