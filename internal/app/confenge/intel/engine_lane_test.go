package intel

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

func engineChain(engine string, mutate func(*Chain)) Chain {
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	chain := Chain{
		SchemaVersion: SchemaV1, EngineLane: engine,
		LeadCreatedAt: at, FirstActionAt: &at,
		OutcomeType: OutcomeContacted,
	}
	if mutate != nil {
		mutate(&chain)
	}
	return chain
}

// The four engines must stay separately visible. An aggregate that merges them
// hides which engine is actually producing revenue.
func TestEngineProgressionNeverMergesTheFourEngines(t *testing.T) {
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	chains := []Chain{
		engineChain(EngineLaneFirstTouch, func(c *Chain) {
			c.Conversation = true
			c.Qualified = true
			c.OutcomeType = OutcomeQualifiedConversation
		}),
		engineChain(EngineLaneIntelSeed, func(c *Chain) { c.Conversation = true }),
		engineChain(EngineLaneIntelWatch, nil),
		engineChain(EngineLaneConfengeWeb, func(c *Chain) {
			c.ProposalAt = &at
			c.OutcomeType = OutcomeProposal
		}),
	}
	got := ProjectEngineLaneProgression(chains, at)

	if got.Schema != EngineProgressionSchemaV1 {
		t.Fatalf("projection is untagged: %q", got.Schema)
	}
	if got.TotalChains != 4 {
		t.Fatalf("projected %d chains, want 4", got.TotalChains)
	}
	if len(got.Rows) != len(EngineLanes) {
		t.Fatalf("projected %d engine rows, want %d", len(got.Rows), len(EngineLanes))
	}
	byEngine := map[string]EngineLaneRow{}
	for _, row := range got.Rows {
		byEngine[row.EngineLane] = row
	}
	for _, engine := range EngineLanes {
		row, ok := byEngine[engine]
		if !ok {
			t.Fatalf("engine %q is missing from the projection", engine)
		}
		if row.Chains != 1 {
			t.Fatalf("engine %q has %d chains, want 1", engine, row.Chains)
		}
		// Every stage key is present even at zero, so "produced nothing here"
		// is distinguishable from "not measured".
		for _, stage := range EngineStages {
			if _, present := row.Stages[stage]; !present {
				t.Fatalf("engine %q is missing stage %q", engine, stage)
			}
		}
	}
	// The progression is per engine, not pooled.
	if byEngine[EngineLaneFirstTouch].Stages[StageQCO] != 1 {
		t.Fatalf("first touch QCO not attributed: %+v", byEngine[EngineLaneFirstTouch].Stages)
	}
	if byEngine[EngineLaneIntelSeed].Stages[StageQCO] != 0 {
		t.Fatal("first touch's QCO leaked into INTEL_SEED")
	}
	if byEngine[EngineLaneConfengeWeb].Stages[StageProposal] != 1 {
		t.Fatalf("confenge web proposal not attributed: %+v", byEngine[EngineLaneConfengeWeb].Stages)
	}
	if byEngine[EngineLaneIntelWatch].Stages[StageReplyOrHandRaiser] != 0 {
		t.Fatal("INTEL_WATCH was credited with a reply it did not produce")
	}
}

// The whole observable progression must be attributable, stage by stage.
func TestEngineProgressionCoversTheFullObservableProgression(t *testing.T) {
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	full := engineChain(EngineLaneFirstTouch, func(c *Chain) {
		c.Conversation = true
		c.ConversationAt = &at
		c.Qualified = true
		c.ProposalAt = &at
		c.CloseAt = &at
		c.OutcomeType = OutcomeWon
		c.HumanConfirmed = true
	})
	got := ProjectEngineLaneProgression([]Chain{full}, at)
	row := got.Rows[0]
	for _, stage := range EngineStages {
		if row.Stages[stage] != 1 {
			t.Fatalf("stage %q was not observed: %+v", stage, row.Stages)
		}
	}
	if row.WonChains != 1 {
		t.Fatalf("a human-confirmed win was not attributed: %+v", row)
	}
}

// A win is a human fact. The projection must not infer one from a proposal or
// from a terminal timestamp.
func TestEngineProgressionNeverInfersAWinWithoutAHuman(t *testing.T) {
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	unconfirmed := engineChain(EngineLaneIntelSeed, func(c *Chain) {
		c.CloseAt = &at
		c.OutcomeType = OutcomeWon
		c.HumanConfirmed = false
	})
	got := ProjectEngineLaneProgression([]Chain{unconfirmed}, at)
	for _, row := range got.Rows {
		if row.WonChains != 0 {
			t.Fatalf("engine %q was credited with an unconfirmed win", row.EngineLane)
		}
	}
	// The terminal stage is still observed: the timestamp is a real fact.
	if got.Rows[1].Stages[StageOutcome] != 1 {
		t.Fatalf("the terminal outcome stage was dropped: %+v", got.Rows[1].Stages)
	}
}

// An unattributed chain is counted in its own row, never spread across the
// engines and never dropped. A hidden remainder is how attribution starts lying.
func TestEngineProgressionKeepsUnattributedChainsVisible(t *testing.T) {
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	chains := []Chain{
		engineChain(EngineLaneUnattributed, nil),
		engineChain("something-else-entirely", nil),
		engineChain(EngineLaneFirstTouch, nil),
	}
	got := ProjectEngineLaneProgression(chains, at)
	if got.Unattributed.Chains != 2 {
		t.Fatalf("unattributed row has %d chains, want 2", got.Unattributed.Chains)
	}
	if got.TotalChains != 3 {
		t.Fatalf("total is %d, want 3", got.TotalChains)
	}
	total := got.Unattributed.Chains
	for _, row := range got.Rows {
		total += row.Chains
	}
	if total != got.TotalChains {
		t.Fatalf("rows plus unattributed = %d but total = %d; chains went missing", total, got.TotalChains)
	}
}

// An unknown engine value must never be coerced into a real engine.
func TestNormalizeEngineLaneRefusesToGuess(t *testing.T) {
	for _, raw := range []string{"", " ", "outbound", "inbound", "seed", "intel-watch", "web"} {
		if got := normalizeEngineLane(raw); got != EngineLaneUnattributed {
			t.Fatalf("%q normalized to %q, want unattributed", raw, got)
		}
	}
	for _, engine := range EngineLanes {
		if got := normalizeEngineLane(strings.ToUpper(engine)); got != engine {
			t.Fatalf("%q normalized to %q", engine, got)
		}
	}
}

// The non-regression guarantee for item 6: engine attribution must not enter
// the chain identity or the metric key, because either would re-split every
// existing chain and move numbers already used for real reconciliation.
func TestEngineLaneIsNotPartOfChainIdentityOrMetricKey(t *testing.T) {
	base := JoinKeys{
		OrganizationID: uuid.NewString(), Source: "confenge-web",
		LeadID: "lead-1", ReceiptID: "receipt-1", RouteFamily: FamilyInbound,
	}
	withEngine := base
	withEngine.EngineLane = EngineLaneConfengeWeb
	other := base
	other.EngineLane = EngineLaneIntelSeed

	if ChainIdentity(base) != ChainIdentity(withEngine) {
		t.Fatal("adding an engine changed the chain identity; every existing chain would re-split")
	}
	if ChainIdentity(withEngine) != ChainIdentity(other) {
		t.Fatal("two engines produced two identities for the same commercial chain")
	}
	if MetricKey(base) != MetricKey(withEngine) {
		t.Fatal("adding an engine changed the metric key; existing metrics would move")
	}
}

// An inbound lead is CONFENGE_WEB's own engine, and observing one must not
// disturb the route family the shipped inbound predicate depends on.
func TestObserveFromInboundAttributesConfengeWebWithoutTouchingRouteFamily(t *testing.T) {
	lead := models.OutreachInboundLead{
		OrganizationID: uuid.New(), Source: "confenge-web",
		LeadID: "lead-1", ReceiptID: "receipt-1", RouteFamily: FamilyInbound,
		LeadCreatedAt: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
	}
	facts := ObserveFromInbound(lead, nil, nil, nil)
	if facts.Keys.EngineLane != EngineLaneConfengeWeb {
		t.Fatalf("an inbound lead was attributed to %q", facts.Keys.EngineLane)
	}
	if facts.Keys.RouteFamily != FamilyInbound {
		t.Fatalf("engine attribution disturbed the route family: %q", facts.Keys.RouteFamily)
	}
}

// An outbound action carries whatever engine stamped it, and an unstamped
// action stays unattributed rather than inheriting the route family's sense.
func TestObserveFromActionCarriesTheStampedEngineOnly(t *testing.T) {
	stamped := models.OutreachCommercialAction{
		OrganizationID: uuid.New(), AccountID: uuid.New(), ID: uuid.New(),
		ActionType: models.ActionOtherManual, EngineLane: "intel_seed",
	}
	if got := ObserveFromAction(stamped, nil, FamilyOutbound); got.Keys.EngineLane != EngineLaneIntelSeed {
		t.Fatalf("a stamped action was attributed to %q", got.Keys.EngineLane)
	}
	unstamped := stamped
	unstamped.EngineLane = ""
	got := ObserveFromAction(unstamped, nil, FamilyOutbound)
	if got.Keys.EngineLane != EngineLaneUnattributed {
		t.Fatalf("an unstamped action was attributed to %q", got.Keys.EngineLane)
	}
	if got.Keys.RouteFamily != FamilyOutbound {
		t.Fatalf("engine attribution disturbed the route family: %q", got.Keys.RouteFamily)
	}
}
