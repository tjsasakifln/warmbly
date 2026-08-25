package proposal

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	testOrg = uuid.MustParse("11111111-1111-4111-8111-000000000047")
	testNow = time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
)

func TestProposalCreateVersionAcceptAndReplay(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store, fixedClock(testNow))
	created, err := service.Create(context.Background(), CreateCommand{
		OrganizationID: testOrg, IdempotencyKey: "fixture:cfg-diag-exp-v1:proposal:create",
		CreatedBy: "actor:synthetic-reviewer", Draft: fixtureDraft(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Proposal.ProposalVersion != 1 || created.Proposal.DecisionState != StateDraft || created.Proposal.Version != 1 {
		t.Fatalf("unexpected create result: %+v", created.Proposal)
	}
	replayed, err := service.Create(context.Background(), CreateCommand{
		OrganizationID: testOrg, IdempotencyKey: "fixture:cfg-diag-exp-v1:proposal:create",
		CreatedBy: "actor:synthetic-reviewer", Draft: fixtureDraft(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Replay || replayed.Proposal.ProposalID != created.Proposal.ProposalID {
		t.Fatalf("create did not replay: %+v", replayed)
	}

	accepted := transitionPath(t, service, created.Proposal, StateAccepted)
	if accepted.Proposal.AcceptedSnapshotHash == "" || accepted.Handoff == nil {
		t.Fatalf("accepted proposal missing frozen hash/handoff: %+v", accepted)
	}
	if accepted.Handoff.FinancialGate.State != FinancialGateUnknown || accepted.Handoff.FinancialGate.ReceivedRevenue {
		t.Fatalf("acceptance inferred finance: %+v", accepted.Handoff.FinancialGate)
	}
	acceptCommand := transitionCommand(accepted.Proposal, StateAccepted)
	acceptCommand.ExpectedRecordVersion--
	acceptCommand.OccurredAt = testNow.Add(time.Duration(acceptCommand.ExpectedRecordVersion) * time.Minute)
	acceptReplay, err := service.Transition(context.Background(), acceptCommand)
	if err != nil {
		t.Fatal(err)
	}
	if !acceptReplay.Replay || acceptReplay.Handoff.EventID != accepted.Handoff.EventID ||
		acceptReplay.Proposal.AcceptedSnapshotHash != accepted.Proposal.AcceptedSnapshotHash {
		t.Fatalf("acceptance replay diverged: %+v", acceptReplay)
	}

	_, err = service.Transition(context.Background(), TransitionCommand{
		OrganizationID: testOrg, ProposalID: accepted.Proposal.ProposalID,
		ProposalVersion: 1, ExpectedRecordVersion: accepted.Proposal.Version,
		IdempotencyKey: "fixture:accepted:mutate", Target: StateRejected,
		Actor: "actor:synthetic-reviewer", EvidenceRefs: []string{"evidence:mutation-attempt"},
	})
	if !errors.Is(err, ErrAcceptedImmutable) {
		t.Fatalf("accepted mutation err=%v", err)
	}

	revisionDraft := fixtureDraft()
	revisionDraft.Amount = 275000
	revised, err := service.Revise(context.Background(), ReviseCommand{
		OrganizationID: testOrg, ProposalID: accepted.Proposal.ProposalID,
		ProposalVersion: 1, ExpectedRecordVersion: accepted.Proposal.Version,
		IdempotencyKey: "fixture:cfg-diag-exp-v1:proposal:revise:v2",
		CreatedBy:      "actor:synthetic-reviewer", Draft: revisionDraft,
	})
	if err != nil {
		t.Fatal(err)
	}
	if revised.Proposal.ProposalVersion != 2 || revised.Proposal.DecisionState != StateDraft || revised.Proposal.AcceptedSnapshotHash != "" {
		t.Fatalf("material revision did not create clean v2: %+v", revised.Proposal)
	}
	frozen, err := store.Get(context.Background(), testOrg, accepted.Proposal.ProposalID, 1)
	if err != nil || frozen.AcceptedSnapshotHash != accepted.Proposal.AcceptedSnapshotHash || frozen.DecisionState != StateAccepted {
		t.Fatalf("accepted v1 changed after v2: frozen=%+v err=%v", frozen, err)
	}
}

func TestProposalRejectExpireAndIllegalTransition(t *testing.T) {
	for _, terminal := range []State{StateRejected, StateExpired} {
		t.Run(string(terminal), func(t *testing.T) {
			service := NewService(NewMemoryStore(), fixedClock(testNow))
			created := createFixture(t, service, "fixture:"+string(terminal)+":create")
			result := transitionPath(t, service, created, terminal)
			if result.Proposal.DecisionState != terminal || result.Proposal.DecisionAt == nil || result.Handoff != nil {
				t.Fatalf("terminal result invalid: %+v", result)
			}
		})
	}

	service := NewService(NewMemoryStore(), fixedClock(testNow))
	created := createFixture(t, service, "fixture:illegal:create")
	_, err := service.Transition(context.Background(), TransitionCommand{
		OrganizationID: testOrg, ProposalID: created.ProposalID, ProposalVersion: 1,
		ExpectedRecordVersion: created.Version, IdempotencyKey: "fixture:illegal:accept",
		Target: StateAccepted, Actor: "actor:synthetic-reviewer", EvidenceRefs: []string{"evidence:illegal"},
	})
	if !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("illegal transition err=%v", err)
	}

	sent := created
	for _, target := range []State{StatePrepared, StateApprovedToSend, StateSent} {
		result, transitionErr := service.Transition(context.Background(), transitionCommand(sent, target))
		if transitionErr != nil {
			t.Fatal(transitionErr)
		}
		sent = result.Proposal
	}
	expiredAccept := transitionCommand(sent, StateAccepted)
	expiredAccept.OccurredAt = sent.ValidUntil.Add(time.Second)
	_, err = service.Transition(context.Background(), expiredAccept)
	if !errors.Is(err, ErrProposalExpired) {
		t.Fatalf("expired acceptance err=%v", err)
	}
}

func TestProposalIdempotencyAndOptimisticConcurrency(t *testing.T) {
	service := NewService(NewMemoryStore(), fixedClock(testNow))
	created := createFixture(t, service, "fixture:idempotency:create")
	changed := fixtureDraft()
	changed.PriceVersion = "price-v2"
	_, err := service.Create(context.Background(), CreateCommand{
		OrganizationID: testOrg, IdempotencyKey: "fixture:idempotency:create",
		CreatedBy: "actor:synthetic-reviewer", Draft: changed,
	})
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("idempotency conflict err=%v", err)
	}

	_, err = service.Transition(context.Background(), TransitionCommand{
		OrganizationID: testOrg, ProposalID: created.ProposalID, ProposalVersion: 1,
		ExpectedRecordVersion: 99, IdempotencyKey: "fixture:stale:prepared",
		Target: StatePrepared, Actor: "actor:synthetic-reviewer",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale expected version err=%v", err)
	}

	outOfOrder := transitionCommand(created, StatePrepared)
	outOfOrder.IdempotencyKey = "fixture:out-of-order:prepared"
	outOfOrder.OccurredAt = created.CreatedAt.Add(-time.Second)
	_, err = service.Transition(context.Background(), outOfOrder)
	if !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("out-of-order command err=%v", err)
	}

	reclassified := fixtureDraft()
	reclassified.Amount++
	reclassified.Synthetic = false
	_, err = service.Revise(context.Background(), ReviseCommand{
		OrganizationID: testOrg, ProposalID: created.ProposalID, ProposalVersion: 1,
		ExpectedRecordVersion: created.Version, IdempotencyKey: "fixture:synthetic-reclassification",
		CreatedBy: "actor:synthetic-reviewer", Draft: reclassified,
	})
	if !errors.Is(err, ErrSyntheticMismatch) {
		t.Fatalf("synthetic reclassification err=%v", err)
	}
}

func TestProposalCommandsHonorPublishedContractBounds(t *testing.T) {
	for name, mutate := range map[string]func(*Draft){
		"long handoff identity": func(draft *Draft) { draft.AccountID = strings.Repeat("a", contractIDMaxLength+1) },
		"long evidence ref":     func(draft *Draft) { draft.EvidenceRefs = []string{strings.Repeat("e", evidenceReferenceMaxLength+1)} },
		"invalid currency":      func(draft *Draft) { draft.Currency = "br" },
	} {
		t.Run(name, func(t *testing.T) {
			draft := fixtureDraft()
			mutate(&draft)
			_, err := NewService(NewMemoryStore(), fixedClock(testNow)).Create(context.Background(), CreateCommand{
				OrganizationID: testOrg, IdempotencyKey: "fixture:invalid:" + name,
				CreatedBy: "actor:synthetic-reviewer", Draft: draft,
			})
			if err == nil {
				t.Fatal("invalid draft satisfied the published contract")
			}
		})
	}

	emptySource := ""
	err := validateFinancialGate(FinancialGate{
		SchemaVersion: FinancialGateSchema, State: FinancialGateUnknown,
		Synthetic: true, SourceEventID: &emptySource,
	}, false)
	if err == nil {
		t.Fatal("UNKNOWN financial gate accepted a non-null source_event_id")
	}
}

func TestAuthorizedSyntheticHandoffNeverClaimsRevenue(t *testing.T) {
	service := NewService(NewMemoryStore(), fixedClock(testNow))
	accepted := transitionPath(t, service, createFixture(t, service, "fixture:authorized:create"), StateAccepted)
	sourceEventID := "fixture-financial-gate-cfg-diag-exp-001"
	command := AuthorizeDeliveryCommand{
		OrganizationID: testOrg, ProposalID: accepted.Proposal.ProposalID, ProposalVersion: 1,
		IdempotencyKey: "fixture:authorized:handoff", CausationID: sourceEventID,
		OnboardingRef: "onboarding:synthetic:cfg-diag-exp-001", OccurredAt: testNow.Add(10 * time.Minute),
		FinancialGate: FinancialGate{
			SchemaVersion: FinancialGateSchema, State: FinancialGateSyntheticValid,
			Synthetic: true, SourceEventID: &sourceEventID, ReceivedRevenue: false,
			EvidenceRefs: []string{"fixture:financial-gate:synthetic-valid"},
		},
	}
	result, err := service.AuthorizeDelivery(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if result.Handoff == nil || !result.Handoff.Synthetic || result.Handoff.FinancialGate.ReceivedRevenue ||
		result.Handoff.OnboardingRef == "" || result.Handoff.AcceptedSnapshotHash != accepted.Proposal.AcceptedSnapshotHash {
		t.Fatalf("invalid authorized handoff: %+v", result.Handoff)
	}
	if len(result.Handoff.IdempotencyKey) > contractIDMaxLength {
		t.Fatalf("handoff idempotency key exceeds contract: %d", len(result.Handoff.IdempotencyKey))
	}
	replay, err := service.AuthorizeDelivery(context.Background(), command)
	if err != nil || !replay.Replay || replay.Handoff.EventID != result.Handoff.EventID {
		t.Fatalf("authorized replay diverged: replay=%+v err=%v", replay, err)
	}

	bad := command
	bad.IdempotencyKey = "fixture:authorized:received-revenue"
	bad.FinancialGate.ReceivedRevenue = true
	_, err = service.AuthorizeDelivery(context.Background(), bad)
	if err == nil {
		t.Fatal("synthetic financial gate claimed received revenue")
	}

	realSourceEventID := "financial-gate-reconciled-real-001"
	mismatched := command
	mismatched.IdempotencyKey = "fixture:authorized:synthetic-mismatch"
	mismatched.FinancialGate = FinancialGate{
		SchemaVersion: FinancialGateSchema, State: FinancialGateAuthorized,
		Synthetic: false, SourceEventID: &realSourceEventID,
		EvidenceRefs: []string{"evidence:financial-authorization"},
	}
	_, err = service.AuthorizeDelivery(context.Background(), mismatched)
	if !errors.Is(err, ErrSyntheticMismatch) {
		t.Fatalf("synthetic proposal accepted real gate err=%v", err)
	}

	outOfOrder := command
	outOfOrder.IdempotencyKey = "fixture:authorized:out-of-order"
	outOfOrder.OccurredAt = accepted.Proposal.UpdatedAt.Add(-time.Second)
	_, err = service.AuthorizeDelivery(context.Background(), outOfOrder)
	if !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("out-of-order delivery authorization err=%v", err)
	}
}

func transitionPath(t *testing.T, service *Service, start Proposal, terminal State) Result {
	t.Helper()
	current := start
	for _, state := range []State{StatePrepared, StateApprovedToSend, StateSent, StateNegotiating, terminal} {
		command := transitionCommand(current, state)
		result, err := service.Transition(context.Background(), command)
		if err != nil {
			t.Fatalf("transition %s -> %s: %v", current.DecisionState, state, err)
		}
		current = result.Proposal
		if state == terminal {
			return result
		}
	}
	t.Fatal("terminal transition not reached")
	return Result{}
}

func transitionCommand(current Proposal, target State) TransitionCommand {
	return TransitionCommand{
		OrganizationID: current.OrganizationID, ProposalID: current.ProposalID,
		ProposalVersion: current.ProposalVersion, ExpectedRecordVersion: current.Version,
		IdempotencyKey: "fixture:" + current.ProposalID.String() + ":" + string(target),
		Target:         target, Actor: "actor:synthetic-reviewer", LiteralReasonRef: "reason:synthetic:" + string(target),
		EvidenceRefs: []string{"evidence:synthetic:" + string(target)},
		OccurredAt:   testNow.Add(time.Duration(current.Version) * time.Minute),
	}
}

func createFixture(t *testing.T, service *Service, idempotencyKey string) Proposal {
	t.Helper()
	result, err := service.Create(context.Background(), CreateCommand{
		OrganizationID: testOrg, IdempotencyKey: idempotencyKey,
		CreatedBy: "actor:synthetic-reviewer", Draft: fixtureDraft(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return result.Proposal
}

func fixtureDraft() Draft {
	return SyntheticCanaryDraft()
}

func fixedClock(at time.Time) Clock {
	return func() time.Time { return at }
}
