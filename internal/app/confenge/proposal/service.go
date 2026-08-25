package proposal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

var proposalNamespace = uuid.MustParse("206b7b3f-fbdf-5238-a45e-6ca74061baf2")

const (
	contractIDMaxLength        = 200
	proposalOnlyIDMaxLength    = 500
	commandIdentityMaxLength   = 500
	evidenceReferenceMaxLength = 500
)

type Clock func() time.Time

type Service struct {
	store Store
	clock Clock
}

func NewService(store Store, clock Clock) *Service {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Service{store: store, clock: clock}
}

type CreateCommand struct {
	OrganizationID uuid.UUID
	IdempotencyKey string
	CreatedBy      string
	Draft          Draft
}

type ReviseCommand struct {
	OrganizationID        uuid.UUID
	ProposalID            uuid.UUID
	ProposalVersion       int
	ExpectedRecordVersion int64
	IdempotencyKey        string
	CreatedBy             string
	Draft                 Draft
}

type TransitionCommand struct {
	OrganizationID        uuid.UUID
	ProposalID            uuid.UUID
	ProposalVersion       int
	ExpectedRecordVersion int64
	IdempotencyKey        string
	Target                State
	Actor                 string
	LiteralReasonRef      string
	EvidenceRefs          []string
	OccurredAt            time.Time
}

type AuthorizeDeliveryCommand struct {
	OrganizationID  uuid.UUID
	ProposalID      uuid.UUID
	ProposalVersion int
	IdempotencyKey  string
	CausationID     string
	OnboardingRef   string
	FinancialGate   FinancialGate
	OccurredAt      time.Time
}

func (s *Service) Create(ctx context.Context, command CreateCommand) (Result, error) {
	if s == nil || s.store == nil {
		return Result{}, fmt.Errorf("proposal store unavailable")
	}
	if err := validateCommand(command.OrganizationID, command.IdempotencyKey, command.CreatedBy); err != nil {
		return Result{}, err
	}
	if err := validateDraft(command.Draft); err != nil {
		return Result{}, err
	}
	payloadHash, err := hashCommand(command)
	if err != nil {
		return Result{}, err
	}
	if replay, ok, err := s.replay(ctx, command.OrganizationID, command.IdempotencyKey, payloadHash); err != nil || ok {
		return replay, err
	}
	now := s.clock().UTC()
	if !command.Draft.ValidUntil.After(now) {
		return Result{}, ErrProposalExpired
	}
	proposalID := stableUUID("proposal", command.OrganizationID.String(), command.IdempotencyKey)
	p := proposalFromDraft(command.OrganizationID, proposalID, 1, command.CreatedBy, command.Draft, now)
	event := proposalEvent(p, StateDraft, command.CreatedBy, now, command.Draft.EvidenceRefs, command.IdempotencyKey)
	receipt, replay, err := s.store.Apply(ctx, Mutation{
		OrganizationID: p.OrganizationID, IdempotencyKey: command.IdempotencyKey,
		PayloadHash: payloadHash, Insert: true, Proposal: p, Events: []ProposalEvent{event},
	})
	return resultFromReceipt(receipt, replay), err
}

func (s *Service) Revise(ctx context.Context, command ReviseCommand) (Result, error) {
	if err := validateCommand(command.OrganizationID, command.IdempotencyKey, command.CreatedBy); err != nil {
		return Result{}, err
	}
	if err := validateDraft(command.Draft); err != nil {
		return Result{}, err
	}
	payloadHash, err := hashCommand(command)
	if err != nil {
		return Result{}, err
	}
	if replay, ok, err := s.replay(ctx, command.OrganizationID, command.IdempotencyKey, payloadHash); err != nil || ok {
		return replay, err
	}
	current, err := s.load(ctx, command.OrganizationID, command.ProposalID, command.ProposalVersion)
	if err != nil {
		return Result{}, err
	}
	if current.Version != command.ExpectedRecordVersion {
		return Result{}, ErrConflict
	}
	if current.Synthetic != command.Draft.Synthetic {
		return Result{}, ErrSyntheticMismatch
	}
	if !materiallyDifferent(*current, command.Draft) {
		return Result{}, ErrNoMaterialChange
	}
	now := s.clock().UTC()
	if !command.Draft.ValidUntil.After(now) {
		return Result{}, ErrProposalExpired
	}
	next := proposalFromDraft(current.OrganizationID, current.ProposalID, current.ProposalVersion+1, command.CreatedBy, command.Draft, now)
	event := proposalEvent(next, StateDraft, command.CreatedBy, now, command.Draft.EvidenceRefs, command.IdempotencyKey)
	receipt, replay, err := s.store.Apply(ctx, Mutation{
		OrganizationID: next.OrganizationID, IdempotencyKey: command.IdempotencyKey,
		PayloadHash: payloadHash, Expected: &ExpectedVersion{ProposalVersion: current.ProposalVersion, RecordVersion: current.Version},
		Insert: true, Proposal: next, Events: []ProposalEvent{event},
	})
	return resultFromReceipt(receipt, replay), err
}

func (s *Service) Transition(ctx context.Context, command TransitionCommand) (Result, error) {
	if err := validateCommand(command.OrganizationID, command.IdempotencyKey, command.Actor); err != nil {
		return Result{}, err
	}
	if err := validateOptionalString("literal_reason_ref", command.LiteralReasonRef, proposalOnlyIDMaxLength); err != nil {
		return Result{}, err
	}
	if err := validateStringSet("evidence_refs", command.EvidenceRefs, evidenceReferenceMaxLength, false); err != nil {
		return Result{}, err
	}
	payloadHash, err := hashCommand(command)
	if err != nil {
		return Result{}, err
	}
	if replay, ok, err := s.replay(ctx, command.OrganizationID, command.IdempotencyKey, payloadHash); err != nil || ok {
		return replay, err
	}
	current, err := s.load(ctx, command.OrganizationID, command.ProposalID, command.ProposalVersion)
	if err != nil {
		return Result{}, err
	}
	if current.Version != command.ExpectedRecordVersion {
		return Result{}, ErrConflict
	}
	if current.DecisionState == StateAccepted {
		return Result{}, ErrAcceptedImmutable
	}
	if !canTransition(current.DecisionState, command.Target) {
		return Result{}, fmt.Errorf("%w: %s -> %s", ErrIllegalTransition, current.DecisionState, command.Target)
	}
	at := command.OccurredAt.UTC()
	if at.IsZero() {
		at = s.clock().UTC()
	}
	if at.Before(current.UpdatedAt) {
		return Result{}, ErrOutOfOrder
	}
	if command.Target == StateAccepted && at.After(current.ValidUntil) {
		return Result{}, ErrProposalExpired
	}
	next := *cloneProposal(*current)
	next.DecisionState = command.Target
	next.Version++
	next.UpdatedAt = at
	next.LiteralReasonRef = strings.TrimSpace(command.LiteralReasonRef)
	next.EvidenceRefs = sortedCopy(append(next.EvidenceRefs, command.EvidenceRefs...))
	stampTransition(&next, at)
	if command.Target == StateAccepted {
		if len(sortedCopy(command.EvidenceRefs)) == 0 {
			return Result{}, fmt.Errorf("acceptance evidence_refs required")
		}
		next.AcceptedSnapshotHash, err = next.AcceptedHash()
		if err != nil {
			return Result{}, err
		}
	}
	event := proposalEvent(next, command.Target, command.Actor, at, command.EvidenceRefs, command.IdempotencyKey)
	var handoff *DeliveryOrderRequested
	if command.Target == StateAccepted {
		unknown := FinancialGate{SchemaVersion: FinancialGateSchema, State: FinancialGateUnknown, Synthetic: next.Synthetic, EvidenceRefs: []string{}}
		handoff, err = deliveryRequest(next, unknown, "", event.EventID.String(), at)
		if err != nil {
			return Result{}, err
		}
	}
	receipt, replay, err := s.store.Apply(ctx, Mutation{
		OrganizationID: next.OrganizationID, IdempotencyKey: command.IdempotencyKey,
		PayloadHash: payloadHash, Expected: &ExpectedVersion{ProposalVersion: next.ProposalVersion, RecordVersion: current.Version},
		Proposal: next, Events: []ProposalEvent{event}, Handoff: handoff,
	})
	return resultFromReceipt(receipt, replay), err
}

func (s *Service) AuthorizeDelivery(ctx context.Context, command AuthorizeDeliveryCommand) (Result, error) {
	if command.OrganizationID == uuid.Nil || command.ProposalID == uuid.Nil {
		return Result{}, fmt.Errorf("organization_id, proposal_id and idempotency_key required")
	}
	if err := validateRequiredString("idempotency_key", command.IdempotencyKey, commandIdentityMaxLength); err != nil {
		return Result{}, err
	}
	if err := validateFinancialGate(command.FinancialGate, true); err != nil {
		return Result{}, err
	}
	if err := validateRequiredString("onboarding_ref", command.OnboardingRef, contractIDMaxLength); err != nil {
		return Result{}, err
	}
	if err := validateRequiredString("causation_id", command.CausationID, contractIDMaxLength); err != nil {
		return Result{}, err
	}
	payloadHash, err := hashCommand(command)
	if err != nil {
		return Result{}, err
	}
	if replay, ok, err := s.replay(ctx, command.OrganizationID, command.IdempotencyKey, payloadHash); err != nil || ok {
		return replay, err
	}
	p, err := s.load(ctx, command.OrganizationID, command.ProposalID, command.ProposalVersion)
	if err != nil {
		return Result{}, err
	}
	if p.DecisionState != StateAccepted || p.AcceptedSnapshotHash == "" {
		return Result{}, fmt.Errorf("%w: delivery requires accepted proposal", ErrIllegalTransition)
	}
	if p.Synthetic != command.FinancialGate.Synthetic {
		return Result{}, ErrSyntheticMismatch
	}
	at := command.OccurredAt.UTC()
	if at.IsZero() {
		at = s.clock().UTC()
	}
	if at.Before(p.UpdatedAt) {
		return Result{}, ErrOutOfOrder
	}
	handoff, err := deliveryRequest(*p, command.FinancialGate, command.OnboardingRef, command.CausationID, at)
	if err != nil {
		return Result{}, err
	}
	receipt, replay, err := s.store.Apply(ctx, Mutation{
		OrganizationID: p.OrganizationID, IdempotencyKey: command.IdempotencyKey,
		PayloadHash: payloadHash, Expected: &ExpectedVersion{ProposalVersion: p.ProposalVersion, RecordVersion: p.Version},
		NoProposalWrite: true, Proposal: *p, Handoff: handoff,
	})
	return resultFromReceipt(receipt, replay), err
}

func (s *Service) load(ctx context.Context, orgID, proposalID uuid.UUID, version int) (*Proposal, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("proposal store unavailable")
	}
	p, err := s.store.Get(ctx, orgID, proposalID, version)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, ErrNotFound
	}
	return p, nil
}

func (s *Service) replay(ctx context.Context, orgID uuid.UUID, key, payloadHash string) (Result, bool, error) {
	receipt, err := s.store.Receipt(ctx, orgID, key)
	if err != nil || receipt == nil {
		return Result{}, false, err
	}
	if receipt.PayloadHash != payloadHash {
		return Result{}, false, ErrIdempotencyConflict
	}
	return resultFromReceipt(*receipt, true), true, nil
}

func proposalFromDraft(orgID, proposalID uuid.UUID, proposalVersion int, actor string, draft Draft, now time.Time) Proposal {
	return Proposal{
		SchemaVersion: ProposalSchemaVersion, ProposalID: proposalID, ProposalVersion: proposalVersion,
		OrganizationID: orgID, AccountID: strings.TrimSpace(draft.AccountID), ClientRef: strings.TrimSpace(draft.ClientRef),
		OpportunityID: strings.TrimSpace(draft.OpportunityID), QCOID: strings.TrimSpace(draft.QCOID), DealID: strings.TrimSpace(draft.DealID),
		SourceLeadID: strings.TrimSpace(draft.SourceLeadID), CorrelationID: strings.TrimSpace(draft.CorrelationID),
		OfferID: strings.TrimSpace(draft.OfferID), OfferVersion: strings.TrimSpace(draft.OfferVersion),
		DeliverableID: strings.TrimSpace(draft.DeliverableID), DeliverableVersion: strings.TrimSpace(draft.DeliverableVersion),
		ScopeVersion: strings.TrimSpace(draft.ScopeVersion), PriceVersion: strings.TrimSpace(draft.PriceVersion),
		TermsVersion: strings.TrimSpace(draft.TermsVersion), Amount: draft.Amount, Currency: strings.ToUpper(strings.TrimSpace(draft.Currency)),
		Credits: sortedCopy(draft.Credits), Addons: sortedCopy(draft.Addons), Inputs: sortedCopy(draft.Inputs),
		Exclusions: sortedCopy(draft.Exclusions), Deadline: draft.Deadline.UTC(), ValidUntil: draft.ValidUntil.UTC(),
		DecisionState: StateDraft, EvidenceRefs: sortedCopy(draft.EvidenceRefs), CreatedBy: strings.TrimSpace(actor),
		Synthetic: draft.Synthetic, Version: 1, CreatedAt: now, UpdatedAt: now,
	}
}

func validateCommand(orgID uuid.UUID, idempotencyKey, actor string) error {
	if orgID == uuid.Nil {
		return fmt.Errorf("organization_id, idempotency_key and actor required")
	}
	if err := validateRequiredString("idempotency_key", idempotencyKey, commandIdentityMaxLength); err != nil {
		return err
	}
	return validateRequiredString("actor", actor, proposalOnlyIDMaxLength)
}

func validateDraft(draft Draft) error {
	required := map[string]string{
		"account_id": draft.AccountID, "client_ref": draft.ClientRef, "opportunity_id": draft.OpportunityID,
		"qco_id": draft.QCOID, "correlation_id": draft.CorrelationID, "offer_id": draft.OfferID,
		"offer_version": draft.OfferVersion, "deliverable_id": draft.DeliverableID,
		"deliverable_version": draft.DeliverableVersion, "scope_version": draft.ScopeVersion,
		"price_version": draft.PriceVersion, "terms_version": draft.TermsVersion, "currency": draft.Currency,
	}
	for field, value := range required {
		if err := validateRequiredString(field, value, contractIDMaxLength); err != nil {
			return err
		}
	}
	if err := validateOptionalString("deal_id", draft.DealID, proposalOnlyIDMaxLength); err != nil {
		return err
	}
	if err := validateOptionalString("source_lead_id", draft.SourceLeadID, proposalOnlyIDMaxLength); err != nil {
		return err
	}
	currency := strings.ToUpper(strings.TrimSpace(draft.Currency))
	if len(currency) != 3 || currency[0] < 'A' || currency[0] > 'Z' ||
		currency[1] < 'A' || currency[1] > 'Z' || currency[2] < 'A' || currency[2] > 'Z' {
		return fmt.Errorf("currency must be a three-letter code")
	}
	if draft.Amount < 0 || draft.Deadline.IsZero() || draft.ValidUntil.IsZero() {
		return fmt.Errorf("non-negative amount, deadline and valid_until required")
	}
	for field, values := range map[string][]string{
		"credits": draft.Credits, "addons": draft.Addons, "inputs": draft.Inputs,
		"exclusions": draft.Exclusions, "evidence_refs": draft.EvidenceRefs,
	} {
		if err := validateStringSet(field, values, evidenceReferenceMaxLength, field == "inputs"); err != nil {
			return err
		}
	}
	return nil
}

func validateRequiredString(field, value string, maxLength int) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s required", field)
	}
	if utf8.RuneCountInString(value) > maxLength {
		return fmt.Errorf("%s exceeds %d characters", field, maxLength)
	}
	return nil
}

func validateOptionalString(field, value string, maxLength int) error {
	if value = strings.TrimSpace(value); value == "" {
		return nil
	}
	if utf8.RuneCountInString(value) > maxLength {
		return fmt.Errorf("%s exceeds %d characters", field, maxLength)
	}
	return nil
}

func validateStringSet(field string, values []string, maxLength int, required bool) error {
	normalized := sortedCopy(values)
	if required && len(normalized) == 0 {
		return fmt.Errorf("%s required", field)
	}
	for _, value := range normalized {
		if utf8.RuneCountInString(value) > maxLength {
			return fmt.Errorf("%s entry exceeds %d characters", field, maxLength)
		}
	}
	return nil
}

func materiallyDifferent(current Proposal, draft Draft) bool {
	material := proposalFromDraft(current.OrganizationID, current.ProposalID, current.ProposalVersion, current.CreatedBy, draft, current.CreatedAt)
	current.PreparedAt, current.ApprovedAt, current.SentAt, current.DecisionAt = nil, nil, nil, nil
	current.LiteralReasonRef, current.AcceptedSnapshotHash = "", ""
	current.DecisionState, current.Version, current.UpdatedAt = StateDraft, 1, current.CreatedAt
	current.EvidenceRefs, current.CreatedBy, current.Synthetic = material.EvidenceRefs, material.CreatedBy, material.Synthetic
	return !reflect.DeepEqual(current, material)
}

func canTransition(from, to State) bool {
	allowed := map[State]map[State]bool{
		StateDraft: {StatePrepared: true}, StatePrepared: {StateApprovedToSend: true},
		StateApprovedToSend: {StateSent: true},
		StateSent:           {StateNegotiating: true, StateAccepted: true, StateRejected: true, StateExpired: true, StateUnknown: true},
		StateNegotiating:    {StateAccepted: true, StateRejected: true, StateExpired: true, StateUnknown: true},
	}
	return allowed[from][to]
}

func stampTransition(p *Proposal, at time.Time) {
	switch p.DecisionState {
	case StatePrepared:
		p.PreparedAt = &at
	case StateApprovedToSend:
		p.ApprovedAt = &at
	case StateSent:
		p.SentAt = &at
	case StateAccepted, StateRejected, StateExpired, StateUnknown:
		p.DecisionAt = &at
	}
}

func proposalEvent(p Proposal, state State, actor string, at time.Time, evidence []string, idempotencyKey string) ProposalEvent {
	return ProposalEvent{
		SchemaVersion: ProposalEventSchemaVersion,
		EventID:       stableUUID("proposal-event", p.OrganizationID.String(), p.ProposalID.String(), fmt.Sprint(p.ProposalVersion), idempotencyKey),
		EventType:     "proposal." + strings.ToLower(string(state)), OrganizationID: p.OrganizationID,
		ProposalID: p.ProposalID, ProposalVersion: p.ProposalVersion, State: state,
		Actor: strings.TrimSpace(actor), OccurredAt: at, EvidenceRefs: sortedCopy(evidence),
	}
}

func deliveryRequest(p Proposal, gate FinancialGate, onboardingRef, causationID string, at time.Time) (*DeliveryOrderRequested, error) {
	if err := validateFinancialGate(gate, false); err != nil {
		return nil, err
	}
	source := "none"
	if gate.SourceEventID != nil {
		source = strings.TrimSpace(*gate.SourceEventID)
	}
	idempotencyKey := stableDigest("delivery-order", p.ProposalID.String(), fmt.Sprint(p.ProposalVersion),
		p.AcceptedSnapshotHash, p.DeliverableID, p.DeliverableVersion, string(gate.State), source)
	return &DeliveryOrderRequested{
		EventID: stableUUID("delivery-order-request", idempotencyKey), SchemaVersion: DeliveryRequestSchema,
		Synthetic: gate.Synthetic || p.Synthetic, CorrelationID: p.CorrelationID, CausationID: causationID,
		IdempotencyKey: idempotencyKey, OrganizationID: p.OrganizationID, AccountID: p.AccountID,
		ClientRef: p.ClientRef, OpportunityID: p.OpportunityID, QCOID: p.QCOID,
		ProposalID: p.ProposalID, ProposalVersion: p.ProposalVersion, AcceptedSnapshotHash: p.AcceptedSnapshotHash,
		OfferID: p.OfferID, OfferVersion: p.OfferVersion, DeliverableID: p.DeliverableID,
		DeliverableVersion: p.DeliverableVersion, ScopeVersion: p.ScopeVersion,
		PriceVersion: p.PriceVersion, TermsVersion: p.TermsVersion, FinancialGate: gate,
		OnboardingRef: strings.TrimSpace(onboardingRef), OccurredAt: at,
		EvidenceRefs: sortedCopy(append(p.EvidenceRefs, gate.EvidenceRefs...)),
	}, nil
}

func validateFinancialGate(gate FinancialGate, authorizedOnly bool) error {
	if gate.SchemaVersion != FinancialGateSchema {
		return fmt.Errorf("financial_gate.schema_version must be %s", FinancialGateSchema)
	}
	if gate.ReceivedRevenue {
		return fmt.Errorf("financial gate cannot claim received revenue")
	}
	source := ""
	if gate.SourceEventID != nil {
		source = strings.TrimSpace(*gate.SourceEventID)
	}
	switch gate.State {
	case FinancialGateUnknown:
		if authorizedOnly {
			return fmt.Errorf("UNKNOWN financial gate fails closed")
		}
		if gate.SourceEventID != nil {
			return fmt.Errorf("UNKNOWN financial gate requires null source_event_id")
		}
	case FinancialGateSyntheticValid:
		if !gate.Synthetic || source == "" || len(sortedCopy(gate.EvidenceRefs)) == 0 {
			return fmt.Errorf("SYNTHETIC_VALID requires synthetic=true, source_event_id and evidence_refs")
		}
	case FinancialGateAuthorized:
		if gate.Synthetic || source == "" || len(sortedCopy(gate.EvidenceRefs)) == 0 {
			return fmt.Errorf("AUTHORIZED requires synthetic=false, source_event_id and evidence_refs")
		}
	default:
		return fmt.Errorf("unsupported financial gate state %q", gate.State)
	}
	if source != "" {
		if err := validateRequiredString("financial_gate.source_event_id", source, contractIDMaxLength); err != nil {
			return err
		}
	}
	if err := validateStringSet("financial_gate.evidence_refs", gate.EvidenceRefs, evidenceReferenceMaxLength, false); err != nil {
		return err
	}
	return nil
}

func stableUUID(parts ...string) uuid.UUID {
	return uuid.NewSHA1(proposalNamespace, []byte(strings.Join(parts, "\x00")))
}

func stableDigest(prefix string, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return prefix + ":sha256:" + hex.EncodeToString(sum[:])
}

func hashCommand(command any) (string, error) {
	raw, err := json.Marshal(command)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func resultFromReceipt(receipt CommandReceipt, replay bool) Result {
	return Result{Proposal: receipt.Proposal, Events: receipt.Events, Handoff: receipt.Handoff, Replay: replay}
}

func IsConflict(err error) bool {
	return errors.Is(err, ErrConflict) || errors.Is(err, ErrIdempotencyConflict)
}
