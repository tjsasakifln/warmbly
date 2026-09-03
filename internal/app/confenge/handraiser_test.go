package confenge

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

func handRaiseFor(orgID, accountID uuid.UUID, signal HandRaiseSignal, engine string) HandRaise {
	candidate := uuid.New()
	return HandRaise{
		OrganizationID: orgID, AccountID: accountID, CandidateID: &candidate,
		Signal: signal, EngineLane: engine,
		OccurredAt: time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC),
		Evidence:   "respondeu pedindo proposta", PersonName: "Maria Souza",
	}
}

// TEST F. Every recognised signal converges into the SAME next-action
// abstraction, and each one keeps the engine that produced it.
func TestHandRaiseConvergenceKeepsLaneOfOriginForEverySignal(t *testing.T) {
	orgID, accountID := uuid.New(), uuid.New()
	engines := map[HandRaiseSignal]string{
		SignalPositiveReplyFirstTouch:  EngineLaneFirstTouch,
		SignalIntelSeedResponse:        EngineLaneIntelSeed,
		SignalRequestHumanReview:       EngineLaneConfengeWeb,
		SignalRequestDeepDive:          EngineLaneIntelWatch,
		SignalMeetingOrProposalRequest: EngineLaneFirstTouch,
		SignalInferredEmailReview:      EngineLaneIntelSeed,
	}
	if len(engines) != len(HandRaiseSignals) {
		t.Fatalf("the test covers %d signals but %d are declared", len(engines), len(HandRaiseSignals))
	}
	for _, signal := range HandRaiseSignals {
		engine := engines[signal]
		action := ConvergeHandRaise(handRaiseFor(orgID, accountID, signal, engine))
		if action == nil {
			t.Fatalf("signal %s did not converge", signal)
		}
		// One abstraction: every signal lands as a next action with a due time.
		if action.NextActionType == "" || action.NextActionAt == nil {
			t.Fatalf("signal %s produced no next action: %+v", signal, action)
		}
		if !action.NextActionAt.After(time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)) {
			t.Fatalf("signal %s produced a due time that is not in the future", signal)
		}
		if action.State != models.ActionStateReady || !action.Actionable {
			t.Fatalf("signal %s did not land as actionable work: %+v", signal, action)
		}
		// Attribution survives convergence.
		if action.EngineLane != engine {
			t.Fatalf("signal %s lost its lane of origin: %q want %q", signal, action.EngineLane, engine)
		}
		// The cockpit lane and the engine lane are different vocabularies and
		// must never be conflated.
		if action.Lane == action.EngineLane {
			t.Fatalf("signal %s collapsed cockpit lane into engine lane (%q)", signal, action.Lane)
		}
	}
}

// TEST F (outbound reply and inbound hand-raiser in the same abstraction).
func TestOutboundReplyAndInboundHandRaiserShareOneNextActionAbstraction(t *testing.T) {
	orgID, accountID := uuid.New(), uuid.New()
	outbound := ConvergeHandRaise(handRaiseFor(orgID, accountID,
		SignalPositiveReplyFirstTouch, EngineLaneFirstTouch))
	inbound := ConvergeHandRaise(handRaiseFor(orgID, accountID,
		SignalMeetingOrProposalRequest, EngineLaneConfengeWeb))
	if outbound == nil || inbound == nil {
		t.Fatal("one of the two signals did not converge")
	}
	// Same surface.
	for name, action := range map[string]*models.OutreachCommercialAction{"outbound": outbound, "inbound": inbound} {
		if action.NextActionType == "" || action.NextActionAt == nil {
			t.Fatalf("%s did not land in the next-action abstraction: %+v", name, action)
		}
		if !HandRaiseAwaitsHuman(action) {
			t.Fatalf("%s is not recognised as waiting on a human", name)
		}
	}
	// Distinct origins.
	if outbound.EngineLane == inbound.EngineLane {
		t.Fatalf("the two engines were merged into %q", outbound.EngineLane)
	}
	if outbound.EngineLane != EngineLaneFirstTouch || inbound.EngineLane != EngineLaneConfengeWeb {
		t.Fatalf("lane attribution drifted: outbound=%q inbound=%q", outbound.EngineLane, inbound.EngineLane)
	}
	// Distinct identities, so neither dedupes the other away.
	if outbound.IdempotencyKey == inbound.IdempotencyKey {
		t.Fatal("two engines' hand-raisers collapsed onto one idempotency key")
	}
}

// The same signal from the same person through two engines is two facts about
// two engines, never one duplicate.
func TestHandRaiseIdempotencyKeepsEnginesApart(t *testing.T) {
	orgID, accountID := uuid.New(), uuid.New()
	candidate := uuid.New()
	base := HandRaise{
		OrganizationID: orgID, AccountID: accountID, CandidateID: &candidate,
		Signal: SignalPositiveReplyFirstTouch,
	}
	first := base
	first.EngineLane = EngineLaneFirstTouch
	seed := base
	seed.EngineLane = EngineLaneIntelSeed

	if HandRaiseIdempotencyKey(first) == HandRaiseIdempotencyKey(seed) {
		t.Fatal("two engines produced the same hand-raiser identity")
	}
	// The same engine and signal for the same person is stable, so a repeat
	// converges once instead of piling up.
	if HandRaiseIdempotencyKey(first) != HandRaiseIdempotencyKey(first) {
		t.Fatal("the hand-raiser identity is not stable")
	}
}

// An unknown engine value becomes unattributed rather than being coerced into
// a real engine: a wrong attribution makes a working engine look like it
// produced someone else's result.
func TestUnknownEngineBecomesUnattributedNotADefault(t *testing.T) {
	for _, raw := range []string{"", "  ", "sales", "OUTBOUND", "intel-seed", "confenge web"} {
		if got := NormalizeEngineLane(raw); got != EngineLaneUnattributed {
			t.Fatalf("%q normalized to %q, want unattributed", raw, got)
		}
	}
	for _, engine := range EngineLanes {
		if got := NormalizeEngineLane(engine); got != engine {
			t.Fatalf("%q normalized to %q", engine, got)
		}
		// Case and padding are tolerated; meaning is not invented.
		if got := NormalizeEngineLane("  " + engine + "  "); got != engine {
			t.Fatalf("padded %q normalized to %q", engine, got)
		}
	}
}

// An unrecognised signal must stay visible as unhandled rather than being
// quietly filed under some default action.
func TestUnknownSignalDoesNotConverge(t *testing.T) {
	orgID, accountID := uuid.New(), uuid.New()
	if action := ConvergeHandRaise(handRaiseFor(orgID, accountID, HandRaiseSignal("SOMETHING_NEW"), EngineLaneFirstTouch)); action != nil {
		t.Fatalf("an unknown signal was converged into %+v", action)
	}
	// A signal with no identity cannot be filed either.
	orphan := handRaiseFor(uuid.Nil, accountID, SignalRequestDeepDive, EngineLaneIntelWatch)
	if action := ConvergeHandRaise(orphan); action != nil {
		t.Fatal("a hand-raiser with no organization was converged")
	}
}

// ---------------------------------------------------------------------------
// Founder Interrupt Budget
// ---------------------------------------------------------------------------

func interruptFixture(t *testing.T) (*service, uuid.UUID, *memRepoFull) {
	t.Helper()
	repo := newMemRepoWithSettings()
	svc := &service{cfg: Config{Enabled: true}, repo: repo}
	return svc, uuid.New(), repo
}

func storeAction(t *testing.T, repo *memRepoFull, action *models.OutreachCommercialAction) {
	t.Helper()
	if err := repo.UpsertCommercialAction(context.Background(), action); err != nil {
		t.Fatal(err)
	}
}

// The projection must surface a hand-raiser that nobody has committed to,
// without a human first knowing to look for it. That row has no due date, so
// every due-date-ordered view is blind to it.
func TestInterruptBudgetSurfacesAHandRaiserWithNoNextAction(t *testing.T) {
	svc, orgID, repo := interruptFixture(t)
	accountID := uuid.New()

	orphan := ConvergeHandRaise(handRaiseFor(orgID, accountID, SignalPositiveReplyFirstTouch, EngineLaneFirstTouch))
	// Nobody committed to it: the next action was cleared but the row stands.
	orphan.NextActionType = ""
	orphan.NextActionAt = nil
	orphan.CreatedAt = time.Now().UTC().Add(-72 * time.Hour)
	storeAction(t, repo, orphan)

	budget, xerr := svc.FounderInterruptBudget(context.Background(), orgID, 50)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if budget.Total != 1 {
		t.Fatalf("the projection found %d interruptions, want 1: %+v", budget.Total, budget.Items)
	}
	item := budget.Items[0]
	if item.Bucket != BucketNoNextAction {
		t.Fatalf("the uncommitted hand-raiser landed in %q", item.Bucket)
	}
	if item.EngineLane != EngineLaneFirstTouch {
		t.Fatalf("the projection lost the lane of origin: %q", item.EngineLane)
	}
	if item.Overdue {
		t.Fatal("a row with no due time was reported overdue rather than uncommitted")
	}
	if item.WaitingSeconds < int64((71 * time.Hour).Seconds()) {
		t.Fatalf("waiting time was not projected: %ds", item.WaitingSeconds)
	}
	if budget.ByBucket[BucketNoNextAction] != 1 {
		t.Fatalf("bucket counts are wrong: %+v", budget.ByBucket)
	}
}

// All four required categories are surfaced, and each keeps its engine so no
// aggregate can hide which engine is actually working.
func TestInterruptBudgetSurfacesEveryCategoryWithItsEngine(t *testing.T) {
	svc, orgID, repo := interruptFixture(t)

	uncommitted := ConvergeHandRaise(handRaiseFor(orgID, uuid.New(), SignalPositiveReplyFirstTouch, EngineLaneFirstTouch))
	uncommitted.NextActionType, uncommitted.NextActionAt = "", nil
	storeAction(t, repo, uncommitted)

	storeAction(t, repo, ConvergeHandRaise(handRaiseFor(orgID, uuid.New(),
		SignalMeetingOrProposalRequest, EngineLaneConfengeWeb)))
	storeAction(t, repo, ConvergeHandRaise(handRaiseFor(orgID, uuid.New(),
		SignalIntelSeedResponse, EngineLaneIntelSeed)))
	storeAction(t, repo, ConvergeHandRaise(handRaiseFor(orgID, uuid.New(),
		SignalRequestHumanReview, EngineLaneIntelWatch)))

	budget, xerr := svc.FounderInterruptBudget(context.Background(), orgID, 50)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if budget.Total != 4 {
		t.Fatalf("projected %d interruptions, want 4: %+v", budget.Total, budget.Items)
	}
	for _, bucket := range InterruptBuckets {
		if budget.ByBucket[bucket] != 1 {
			t.Fatalf("bucket %q has %d, want 1: %+v", bucket, budget.ByBucket[bucket], budget.ByBucket)
		}
	}
	// Every engine is separately visible; nothing is merged.
	for _, engine := range EngineLanes {
		if budget.ByEngine[engine] != 1 {
			t.Fatalf("engine %q has %d, want 1: %+v", engine, budget.ByEngine[engine], budget.ByEngine)
		}
	}
	if budget.Unattributed != 0 {
		t.Fatalf("attributed rows leaked into unattributed: %d", budget.Unattributed)
	}
	// Most silently dangerous first.
	if budget.Items[0].Bucket != BucketNoNextAction {
		t.Fatalf("the uncommitted hand-raiser was not surfaced first: %q", budget.Items[0].Bucket)
	}
}

// An unattributed row is counted honestly rather than being assigned to an
// engine that did not produce it.
func TestInterruptBudgetNeverInventsAnEngineForAnUnattributedRow(t *testing.T) {
	svc, orgID, repo := interruptFixture(t)
	legacy := ConvergeHandRaise(handRaiseFor(orgID, uuid.New(), SignalRequestDeepDive, EngineLaneIntelWatch))
	legacy.EngineLane = "" // a row that predates engine attribution
	storeAction(t, repo, legacy)

	budget, xerr := svc.FounderInterruptBudget(context.Background(), orgID, 50)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if budget.Unattributed != 1 {
		t.Fatalf("the unattributed row was not counted as such: %+v", budget)
	}
	for _, engine := range EngineLanes {
		if budget.ByEngine[engine] != 0 {
			t.Fatalf("engine %q absorbed an unattributed row", engine)
		}
	}
}

// A finished action is not an interruption.
func TestInterruptBudgetIgnoresCompletedWork(t *testing.T) {
	svc, orgID, repo := interruptFixture(t)
	for _, state := range []string{models.ActionStateCompleted, models.ActionStateSkipped, models.ActionStateFailed} {
		done := ConvergeHandRaise(handRaiseFor(orgID, uuid.New(), SignalMeetingOrProposalRequest, EngineLaneFirstTouch))
		done.State = state
		storeAction(t, repo, done)
	}
	budget, xerr := svc.FounderInterruptBudget(context.Background(), orgID, 50)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if budget.Total != 0 {
		t.Fatalf("finished work was reported as an interruption: %+v", budget.Items)
	}
}
