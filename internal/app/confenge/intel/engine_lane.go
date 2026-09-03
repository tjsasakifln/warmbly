package intel

import (
	"sort"
	"strings"
	"time"
)

// Engine attribution across the four CONFENGE acquisition engines.
//
// This is a SEPARATE axis from RouteFamily. RouteFamily is a four-value
// commercial vocabulary (inbound / outbound / partner / customer_expansion)
// that the producer owns and that isInboundAcquisitionChain and the shipped
// outcome-feedback dimension key both depend on. The four engines do not map
// onto it: three of them are outbound and one is inbound. Overloading
// RouteFamily would re-split every existing cohort and change numbers that are
// already used for real reconciliation.
//
// So EngineLane rides alongside, out of ChainIdentity, out of MetricKey, and
// out of feedbackDimensionKey. It is readable per chain and aggregatable
// through its own projection below, which is what "do not merge the engines
// into one aggregate that hides which engine is working" actually requires.

// Engine lanes. The strings match the confenge package's own vocabulary and the
// engine_lane column, deliberately duplicated rather than imported: intel is
// below confenge in the import graph and must stay there.
const (
	EngineLaneFirstTouch  = "outbound_first_touch"
	EngineLaneIntelSeed   = "intel_seed"
	EngineLaneIntelWatch  = "intel_watch"
	EngineLaneConfengeWeb = "confenge_web"
	// EngineLaneUnattributed is the honest answer when no engine claimed the
	// chain. Never coerced into a real engine.
	EngineLaneUnattributed = ""
)

// EngineLanes is the closed attributable set, in reporting order.
var EngineLanes = []string{
	EngineLaneFirstTouch, EngineLaneIntelSeed, EngineLaneIntelWatch, EngineLaneConfengeWeb,
}

// normalizeEngineLane maps a raw value onto the closed set. Anything unknown
// becomes unattributed: a wrong attribution makes a working engine look like it
// produced someone else's result, which is worse than a missing one.
func normalizeEngineLane(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case EngineLaneFirstTouch:
		return EngineLaneFirstTouch
	case EngineLaneIntelSeed:
		return EngineLaneIntelSeed
	case EngineLaneIntelWatch:
		return EngineLaneIntelWatch
	case EngineLaneConfengeWeb:
		return EngineLaneConfengeWeb
	default:
		return EngineLaneUnattributed
	}
}

// NormalizeEngineLane is the exported form for callers outside this package.
func NormalizeEngineLane(value string) string { return normalizeEngineLane(value) }

// EngineProgressionSchemaV1 tags the per-engine progression projection.
const EngineProgressionSchemaV1 = "confenge.engine_lane_progression.v1"

// EngineStage is the observable progression every engine is measured on. The
// stages are cumulative facts, not a funnel with assumed conversion: a chain
// counts at a stage when the evidence for that stage exists.
type EngineStage string

const (
	// StageSendOrReceipt: the engine produced a touch or a receipt.
	StageSendOrReceipt EngineStage = "send_or_receipt"
	// StageReplyOrHandRaiser: a human answered or raised their hand.
	StageReplyOrHandRaiser EngineStage = "reply_or_hand_raiser"
	// StageQCO: a qualified conversation outcome.
	StageQCO EngineStage = "qco"
	// StageProposal: a proposal exists.
	StageProposal EngineStage = "proposal"
	// StageOutcome: the chain reached a terminal commercial outcome.
	StageOutcome EngineStage = "outcome"
)

// EngineStages is the progression in order.
var EngineStages = []EngineStage{
	StageSendOrReceipt, StageReplyOrHandRaiser, StageQCO, StageProposal, StageOutcome,
}

// EngineLaneRow is one engine's progression.
type EngineLaneRow struct {
	EngineLane string `json:"engine_lane"`
	Chains     int    `json:"chains"`
	// Stages carries every stage, including zeros, so "this engine produced
	// nothing at this stage" is distinguishable from "this stage is not
	// measured".
	Stages map[EngineStage]int `json:"stages"`
	// WonChains and LostChains are only ever human-confirmed. Nothing here
	// infers a win from a meeting or from inbound interest.
	WonChains  int `json:"won_chains"`
	LostChains int `json:"lost_chains"`
}

// EngineLaneProgression is the whole projection: one row per engine plus one
// explicitly labelled unattributed row.
//
// It is a NEW projection and does not touch the shipped acquisition
// outcome-feedback rollup. That rollup's dimension key is unchanged, so its
// cohorts and its withheld-small-cell behaviour are byte-identical to before
// engine attribution existed.
type EngineLaneProgression struct {
	Schema      string          `json:"schema"`
	GeneratedAt time.Time       `json:"generated_at"`
	Rows        []EngineLaneRow `json:"rows"`
	// Unattributed is kept as its own row rather than being spread across the
	// engines or dropped. A hidden remainder is how an attribution report
	// starts lying.
	Unattributed EngineLaneRow `json:"unattributed"`
	TotalChains  int           `json:"total_chains"`
}

// ProjectEngineLaneProgression aggregates chains per engine without merging
// them. Every attributable engine gets a row even when it produced nothing, so
// a founder can see an engine that is not working rather than an engine that is
// simply absent from the output.
func ProjectEngineLaneProgression(chains []Chain, now time.Time) EngineLaneProgression {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	byEngine := map[string]*EngineLaneRow{}
	newRow := func(engine string) *EngineLaneRow {
		return &EngineLaneRow{EngineLane: engine, Stages: map[EngineStage]int{
			StageSendOrReceipt: 0, StageReplyOrHandRaiser: 0, StageQCO: 0,
			StageProposal: 0, StageOutcome: 0,
		}}
	}
	for _, engine := range EngineLanes {
		byEngine[engine] = newRow(engine)
	}
	unattributed := newRow(EngineLaneUnattributed)

	out := EngineLaneProgression{Schema: EngineProgressionSchemaV1, GeneratedAt: now.UTC()}
	for i := range chains {
		chain := chains[i]
		engine := normalizeEngineLane(chain.EngineLane)
		row := unattributed
		if engine != EngineLaneUnattributed {
			row = byEngine[engine]
		}
		row.Chains++
		out.TotalChains++
		for _, stage := range chainStages(chain) {
			row.Stages[stage]++
		}
		// WON and LOST are human facts in this system. Mirror that here rather
		// than deriving them from the progression.
		if isWonType(chain.OutcomeType) && chain.HumanConfirmed {
			row.WonChains++
		}
		if isLostType(chain.OutcomeType) && chain.HumanConfirmed {
			row.LostChains++
		}
	}

	for _, engine := range EngineLanes {
		out.Rows = append(out.Rows, *byEngine[engine])
	}
	sort.SliceStable(out.Rows, func(i, j int) bool {
		return engineRank(out.Rows[i].EngineLane) < engineRank(out.Rows[j].EngineLane)
	})
	out.Unattributed = *unattributed
	return out
}

func engineRank(engine string) int {
	for i, known := range EngineLanes {
		if known == engine {
			return i
		}
	}
	return len(EngineLanes)
}

// chainStages reports which progression stages one chain has evidence for. A
// stage is counted from the fact that proves it, never from a later stage
// implying an earlier one: a chain with a proposal and no recorded reply is a
// data problem worth seeing, not something to back-fill.
func chainStages(chain Chain) []EngineStage {
	var stages []EngineStage
	if chain.FirstActionAt != nil || strings.TrimSpace(chain.ReceiptID) != "" ||
		!chain.LeadCreatedAt.IsZero() {
		stages = append(stages, StageSendOrReceipt)
	}
	if chain.Conversation || chain.ConversationAt != nil ||
		strings.EqualFold(strings.TrimSpace(chain.OutcomeType), OutcomeReplied) {
		stages = append(stages, StageReplyOrHandRaiser)
	}
	if chain.Qualified || strings.EqualFold(strings.TrimSpace(chain.OutcomeType), OutcomeQualifiedConversation) {
		stages = append(stages, StageQCO)
	}
	if chain.ProposalAt != nil || strings.EqualFold(strings.TrimSpace(chain.OutcomeType), OutcomeProposal) ||
		(strings.TrimSpace(chain.ProposalID) != "" && chain.ProposalID != Unknown) {
		stages = append(stages, StageProposal)
	}
	if chain.CloseAt != nil || isWonType(chain.OutcomeType) || isLostType(chain.OutcomeType) {
		stages = append(stages, StageOutcome)
	}
	return stages
}
