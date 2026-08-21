package intel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// RunFixtureReport loads named SYNTHETIC fixtures through IngestEvent
// and builds the observability report. This is the shipped CLI path.
func RunFixtureReport(orgID, month string, includeSynthetic bool) ObservabilityReport {
	st := NewMemoryStore()
	LoadNamedEventFixtures(st, orgID)
	emitLoopLearning(st, orgID)
	return BuildObservabilityReport(st, orgID, month, includeSynthetic)
}

// BuildObservabilityReport is the executive JSON payload. Pipeline
// value appears only when revenue is evidenced. causal_proof is false.
func BuildObservabilityReport(store Store, orgID, month string, includeSynthetic bool) ObservabilityReport {
	if month == "" {
		month = SyntheticMonth
	}
	rep := ObservabilityReport{
		SchemaVersion:      ReportSchemaV1,
		Month:              month,
		IncludeSynthetic:   includeSynthetic,
		Lanes:              map[string]int{},
		ExceptionCounts:    map[string]int{},
		UpstreamWrites:     []string{},
		CausalProof:        false,
		AutoSend:           false,
		EmailSideEffects:   false,
		Recommendation:     RecommendReady,
		RevenueStatus:      Unknown,
		LearningCandidates: []LearningSummary{},
		Blockers:           []string{},
	}
	if store == nil {
		rep.RealEmpty = true
		rep.Recommendation = RecommendNoGo
		rep.Blockers = append(rep.Blockers, "intel store unavailable")
		return rep
	}

	chains, _ := store.ListChains(orgID)
	view := Rollup(chains, month, includeSynthetic)
	xs, _ := store.ListExceptions(orgID)
	learn, _ := store.ListLearning(orgID)

	rep.InboundQualifiedPipeline = view.InboundQualifiedPipeline
	rep.ValidLeads = view.Denominators.Leads
	rep.QualifiedLeads = view.Denominators.Qualified
	rep.Actions = view.Denominators.Actions
	rep.Outcomes = view.Denominators.Outcomes
	rep.Meetings = view.Meetings
	rep.Proposals = view.Proposals
	rep.Pipeline = view.Pipeline
	rep.Won = view.Won
	rep.Lost = view.Lost
	rep.Unknown = view.Unknown
	rep.Denominators = view.Denominators
	rep.BySource = view.BySource
	rep.ByAsset = view.ByAsset
	rep.ByIntent = view.ByIntent
	rep.ByOffer = view.ByOffer
	rep.ByRoute = view.ByRoute
	rep.MarketAnswer = view.MarketAnswer
	rep.ContractAnalysis = view.ContractAnalysis
	rep.B2GXRay = view.B2GXRay
	rep.CustomerProof = view.CustomerProof
	rep.Latency = view.Latency
	rep.Freshness = view.Freshness
	rep.RealEmpty = view.RealEmpty
	rep.RevenueCents = view.RevenueCents
	rep.RevenueStatus = view.RevenueStatus
	if rep.RevenueStatus == "" {
		rep.RevenueStatus = Unknown
	}

	for _, f := range view.Families {
		switch f.Family {
		case FamilyInbound:
			rep.Lanes[LaneInbound] = f.InboundQualifiedPipeline
		case FamilyOutbound:
			rep.Lanes[LaneOutbound] = f.Meetings + f.Pipeline + f.QCO
		case FamilyPartner:
			rep.Lanes[LanePartner] = f.QCO + f.Lost + f.Won
		case FamilyExpansion:
			rep.Lanes[LaneExpansion] = f.Pipeline + f.Unknown + f.QCO
		}
	}
	rep.Lanes[LaneMarketAnswer] = view.MarketAnswer.Leads
	rep.Lanes[LaneContractAnalysis] = view.ContractAnalysis.Leads
	rep.Lanes[LaneB2GXRay] = view.B2GXRay.Completions + view.B2GXRay.Leads
	rep.Lanes[LaneCustomerProof] = view.CustomerProof

	attributed := 0
	for _, c := range chains {
		if !includeSynthetic && (c.Synthetic || c.Label == LabelSynthetic) {
			continue
		}
		if !inMonth(c, month) {
			continue
		}
		rep.Joins++
		if !c.NotALead {
			n := 0
			if knownID(c.Source) != "" {
				n++
			}
			if knownID(c.AssetID) != "" {
				n++
			}
			if knownID(c.Query) != "" {
				n++
			}
			if n < 3 {
				rep.Missing++
			} else {
				attributed++
			}
		}
	}
	if attributed+rep.Missing > 0 {
		rep.AttributionCompleteness = float64(attributed) / float64(attributed+rep.Missing)
	}

	for _, ex := range xs {
		if !includeSynthetic && ex.Synthetic {
			continue
		}
		rep.ExceptionCounts[ex.Code]++
		if ex.Code == ExceptionOrphan {
			rep.Orphans++
		}
		if ex.Code == ExceptionConflictingAccount || ex.Code == ExceptionImpossibleTransition {
			rep.Conflicts++
		}
	}

	seenLearn := map[string]bool{}
	for _, cand := range learn {
		if !includeSynthetic && cand.Synthetic {
			continue
		}
		if len(cand.UpstreamWrites) != 0 {
			rep.Blockers = append(rep.Blockers, "upstream_writes attempted")
			rep.Recommendation = RecommendNoGo
		}
		key := cand.Identity + "|" + cand.Recommendation
		if seenLearn[key] {
			continue
		}
		seenLearn[key] = true
		rep.LearningCandidates = append(rep.LearningCandidates, LearningSummary{
			Identity:       cand.Identity,
			Recommendation: normalizeLearningVerdict(cand.Recommendation),
			Reason:         cand.Reason,
			Target:         cand.Target,
			Synthetic:      cand.Synthetic,
		})
	}
	sort.Slice(rep.LearningCandidates, func(i, j int) bool {
		a, b := rep.LearningCandidates[i], rep.LearningCandidates[j]
		if a.Identity == b.Identity {
			if a.Recommendation == b.Recommendation {
				return a.Reason < b.Reason
			}
			return a.Recommendation < b.Recommendation
		}
		return a.Identity < b.Identity
	})

	rep.EventsConsumed = countConsumedEvents(chains, includeSynthetic)
	if !includeSynthetic && view.RealEmpty {
		rep.Recommendation = RecommendReady
		rep.Blockers = append(rep.Blockers, "real_empty: no non-synthetic inbound/action/outcome yet")
		if rep.Latency.Baseline == BaselineObserved {
			rep.Latency.Baseline = BaselineNone
		}
	}
	if view.CausalProof {
		rep.Recommendation = RecommendNoGo
		rep.Blockers = append(rep.Blockers, "causal_proof claimed")
	}
	if includeSynthetic && view.InboundQualifiedPipeline == 0 {
		rep.Recommendation = RecommendAdjust
		rep.Blockers = append(rep.Blockers, "synthetic fixture set produced no inbound qualified pipeline")
	}
	return rep
}

func countConsumedEvents(chains []Chain, includeSynthetic bool) int {
	n := 0
	for _, c := range chains {
		if !includeSynthetic && (c.Synthetic || c.Label == LabelSynthetic) {
			continue
		}
		n += len(c.Keys.EventIDs)
	}
	return n
}

func normalizeLearningVerdict(v string) string {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case LearningRepeat:
		return LearningRepeat
	case LearningChange:
		return LearningChange
	case LearningStop:
		return LearningStop
	case LearningNeedMore, "UNKNOWN", "":
		return LearningNeedMore
	default:
		return LearningNeedMore
	}
}

func emitLoopLearning(store Store, orgID string) {
	chains, _ := store.ListChains(orgID)
	for _, c := range chains {
		in := LearningInput{
			From:           LearningFromOutcome,
			OutcomeType:    c.OutcomeType,
			HumanConfirmed: c.HumanConfirmed,
			Keys:           c.Keys,
			Synthetic:      c.Synthetic,
		}
		switch {
		case c.NotALead:
			in.From = LearningFromOutcome
			in.Reason = "NEED_MORE_DATA"
			in.OutcomeType = OutcomeUnknown
		case c.OutcomeType == "" || c.OutcomeType == OutcomeUnknown:
			in.Reason = "NEED_MORE_DATA"
		case isLostType(c.OutcomeType) && c.HumanConfirmed:
			in.Reason = "stop"
		case isWonType(c.OutcomeType) && c.HumanConfirmed:
			in.Reason = "repeat"
		case c.CorrectionApplied:
			in.From = LearningFromCorrection
			in.Reason = "correction"
		}
		EmitLearning(store, in)
	}
}

// ReportJSON encodes the observability payload.
func ReportJSON(rep ObservabilityReport) ([]byte, error) {
	return json.MarshalIndent(rep, "", "  ")
}

// ReportMarkdown is the executive text form. No vanity aggregate.
func ReportMarkdown(rep ObservabilityReport) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# Inbound learning report (%s)\n\n", rep.Month)
	fmt.Fprintf(&b, "- INBOUND QUALIFIED PIPELINE: %d\n", rep.InboundQualifiedPipeline)
	fmt.Fprintf(&b, "- valid leads: %d\n", rep.ValidLeads)
	fmt.Fprintf(&b, "- qualified leads: %d\n", rep.QualifiedLeads)
	fmt.Fprintf(&b, "- actions: %d\n", rep.Actions)
	fmt.Fprintf(&b, "- outcomes: %d\n", rep.Outcomes)
	fmt.Fprintf(&b, "- meetings: %d\n", rep.Meetings)
	fmt.Fprintf(&b, "- proposals: %d\n", rep.Proposals)
	fmt.Fprintf(&b, "- pipeline: %d\n", rep.Pipeline)
	fmt.Fprintf(&b, "- won/lost/UNKNOWN: %d/%d/%d\n", rep.Won, rep.Lost, rep.Unknown)
	fmt.Fprintf(&b, "- missing attribution: %d\n", rep.Missing)
	fmt.Fprintf(&b, "- revenue_status: %s\n", rep.RevenueStatus)
	if rep.RevenueStatus == "evidenced" {
		fmt.Fprintf(&b, "- evidenced revenue cents: %d\n", rep.RevenueCents)
	}
	fmt.Fprintf(&b, "- latency baseline: %s\n", rep.Latency.Baseline)
	fmt.Fprintf(&b, "- censored cycles: %d\n", rep.Latency.CensoredCycles)
	fmt.Fprintf(&b, "- events consumed: %d\n", rep.EventsConsumed)
	fmt.Fprintf(&b, "- joins: %d\n", rep.Joins)
	fmt.Fprintf(&b, "- orphans/conflicts: %d/%d\n", rep.Orphans, rep.Conflicts)
	fmt.Fprintf(&b, "- causal_proof: %v\n", rep.CausalProof)
	fmt.Fprintf(&b, "- upstream_writes: %d\n", len(rep.UpstreamWrites))
	fmt.Fprintf(&b, "- recommendation: %s\n", rep.Recommendation)
	fmt.Fprintf(&b, "\n## Lanes\n\n")
	for _, k := range []string{LaneInbound, LaneOutbound, LanePartner, LaneExpansion, LaneCustomerProof, LaneMarketAnswer, LaneContractAnalysis, LaneB2GXRay} {
		fmt.Fprintf(&b, "- %s: %d\n", k, rep.Lanes[k])
	}
	fmt.Fprintf(&b, "\n## Exceptions\n\n")
	if len(rep.ExceptionCounts) == 0 {
		fmt.Fprintf(&b, "- none\n")
	} else {
		codes := make([]string, 0, len(rep.ExceptionCounts))
		for k := range rep.ExceptionCounts {
			codes = append(codes, k)
		}
		sort.Strings(codes)
		for _, k := range codes {
			fmt.Fprintf(&b, "- %s: %d\n", k, rep.ExceptionCounts[k])
		}
	}
	fmt.Fprintf(&b, "\n## Learning candidates\n\n")
	for _, c := range rep.LearningCandidates {
		fmt.Fprintf(&b, "- %s %s (%s)\n", c.Recommendation, c.Identity, c.Reason)
	}
	if len(rep.Blockers) > 0 {
		fmt.Fprintf(&b, "\n## Blockers\n\n")
		for _, bl := range rep.Blockers {
			fmt.Fprintf(&b, "- %s\n", bl)
		}
	}
	if len(rep.ControlledEmail) > 0 {
		fmt.Fprintf(&b, "\n## Controlled email\n\n")
		fmt.Fprintf(&b, "%s", FormatControlledEmailReport(ControlledEmailExecutiveReport{Rows: rep.ControlledEmail}))
	}
	fmt.Fprintf(&b, "\nThis is not a CRM and not a forecast.\n")
	return b.String()
}

// ReportHasPII is a defensive scan of encoded report values. Spaces in
// JSON pretty-print are not PII.
func ReportHasPII(raw []byte) bool {
	low := strings.ToLower(string(raw))
	for _, tok := range []string{"@", "email=", "phone=", "nome=", "cnpj=", "tel:", "company="} {
		if strings.Contains(low, tok) {
			return true
		}
	}
	return false
}
