package intel

import (
	"sort"
	"strings"
	"time"
)

// Rollup builds the monthly executive view from already-joined chains.
// includeSynthetic=false drops SYNTHETIC fixtures so real metrics stay
// empty/UNKNOWN when nothing live has arrived.
func Rollup(chains []Chain, month string, includeSynthetic bool) ExecutiveView {
	month = strings.TrimSpace(month)
	if month == "" {
		month = time.Now().UTC().Format("2006-01")
	}
	view := ExecutiveView{
		SchemaVersion:    SchemaV1,
		Month:            month,
		IncludeSynthetic: includeSynthetic,
		AttributionKind:  AssociationObserved,
		CausalProof:      false,
		Families: []FamilyCounts{
			{Family: FamilyInbound},
			{Family: FamilyOutbound},
			{Family: FamilyPartner},
			{Family: FamilyExpansion},
		},
	}

	familyIdx := map[string]int{
		FamilyInbound:   0,
		FamilyOutbound:  1,
		FamilyPartner:   2,
		FamilyExpansion: 3,
	}
	bySource := map[string]int{}
	byAsset := map[string]int{}
	byTrigger := map[string]int{}
	byOffer := map[string]int{}
	byRoute := map[string]int{}
	tfVers := map[string]struct{}{}
	apVers := map[string]struct{}{}
	marks := map[string]struct{}{}

	var latSum LatencyMS
	var latN int

	for _, c := range chains {
		if !includeSynthetic && (c.Synthetic || c.Label == LabelSynthetic) {
			continue
		}
		if !inMonth(c, month) {
			continue
		}
		view.ChainCount++
		incBreakdown(bySource, c.Source)
		incBreakdown(byAsset, c.AssetID)
		incBreakdown(byTrigger, c.Trigger)
		incBreakdown(byOffer, c.OfferID)
		incBreakdown(byRoute, c.Route)
		if c.Versions.TargetFit != "" {
			tfVers[c.Versions.TargetFit] = struct{}{}
		}
		if c.Versions.ActivationPolicy != "" {
			apVers[c.Versions.ActivationPolicy] = struct{}{}
		}
		if c.Versions.TargetFitWatermark != "" {
			marks[c.Versions.TargetFitWatermark] = struct{}{}
		}
		if !c.Versions.Fresh {
			view.Freshness.StaleChains++
		}
		if c.Versions.TargetFit == Unknown || c.Versions.ActivationPolicy == Unknown {
			view.Freshness.MissingVersionChains++
		}

		if strings.TrimSpace(c.LeadID) != "" && c.LeadID != Unknown {
			view.Denominators.Leads++
		}
		if strings.TrimSpace(c.ActionID) != "" && c.ActionID != Unknown {
			view.Denominators.Actions++
		}
		if c.OutcomeType != "" && c.OutcomeType != OutcomeUnknown {
			view.Denominators.Outcomes++
		}

		fi, ok := familyIdx[c.RouteFamily]
		if !ok {
			// UNKNOWN family is counted in totals only; it is not folded
			// into inbound/outbound/partner/expansion.
			fi = -1
		}

		qco := c.Qualified || c.OutcomeType == OutcomeQualifiedConversation
		if qco {
			view.QCO++
			view.Denominators.Qualified++
			if fi >= 0 {
				view.Families[fi].QCO++
			}
		}
		if c.RouteFamily == FamilyInbound && qco {
			view.InboundQualifiedPipeline++
			if fi >= 0 {
				view.Families[fi].InboundQualifiedPipeline++
			}
		}
		if c.Conversation || c.ConversationAt != nil || qco {
			view.Conversations++
			view.Denominators.Conversations++
			if fi >= 0 {
				view.Families[fi].Conversations++
			}
		}
		if c.OutcomeType == OutcomeMeeting {
			view.Meetings++
			view.Denominators.Meetings++
			if fi >= 0 {
				view.Families[fi].Meetings++
			}
		}
		if c.OutcomeType == OutcomeProposal || c.ProposalAt != nil {
			view.Proposals++
			view.Denominators.Proposals++
			if fi >= 0 {
				view.Families[fi].Proposals++
			}
		}
		if c.PipelineOpen {
			view.Pipeline++
			if fi >= 0 {
				view.Families[fi].Pipeline++
			}
		}

		switch {
		case isWonType(c.OutcomeType) && c.HumanConfirmed:
			view.Won++
			view.Denominators.Closed++
			if fi >= 0 {
				view.Families[fi].Won++
			}
		case isLostType(c.OutcomeType) && c.HumanConfirmed:
			view.Lost++
			view.Denominators.Closed++
			if fi >= 0 {
				view.Families[fi].Lost++
			}
		default:
			view.Unknown++
			if fi >= 0 {
				view.Families[fi].Unknown++
			}
		}

		if n := latencySample(c); n.SampledChains == 1 {
			latN++
			latSum.LeadToIngest += n.LeadToIngest
			latSum.IngestToEnrichment += n.IngestToEnrichment
			latSum.EnrichmentToAction += n.EnrichmentToAction
			latSum.ActionToConversation += n.ActionToConversation
			latSum.ConversationToProposal += n.ConversationToProposal
			latSum.ProposalToClose += n.ProposalToClose
		}
	}

	if latN > 0 {
		view.Latency = LatencyMS{
			LeadToIngest:           latSum.LeadToIngest / int64(latN),
			IngestToEnrichment:     latSum.IngestToEnrichment / int64(latN),
			EnrichmentToAction:     latSum.EnrichmentToAction / int64(latN),
			ActionToConversation:   latSum.ActionToConversation / int64(latN),
			ConversationToProposal: latSum.ConversationToProposal / int64(latN),
			ProposalToClose:        latSum.ProposalToClose / int64(latN),
			SampledChains:          latN,
			Baseline:               "observed",
		}
	} else {
		view.Latency.Baseline = "insufficient_data"
	}
	view.BySource = mapBreakdown(bySource)
	view.ByAsset = mapBreakdown(byAsset)
	view.ByTrigger = mapBreakdown(byTrigger)
	view.ByOffer = mapBreakdown(byOffer)
	view.ByRoute = mapBreakdown(byRoute)
	view.Freshness.TargetFitVersions = mapKeys(tfVers)
	view.Freshness.ActivationPolicyVersions = mapKeys(apVers)
	view.Freshness.Watermarks = mapKeys(marks)
	view.RealEmpty = view.ChainCount == 0
	return view
}

func inMonth(c Chain, month string) bool {
	ts := c.LeadCreatedAt
	if ts.IsZero() {
		ts = c.IngestedAt
	}
	if ts.IsZero() {
		ts = c.CreatedAt
	}
	if ts.IsZero() {
		return true
	}
	return ts.UTC().Format("2006-01") == month
}

func incBreakdown(m map[string]int, key string) {
	key = idOrUnknown(key)
	m[key]++
}

func mapBreakdown(m map[string]int) []Breakdown {
	out := make([]Breakdown, 0, len(m))
	for k, n := range m {
		out = append(out, Breakdown{Key: k, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Key < out[j].Key
		}
		return out[i].Count > out[j].Count
	})
	return out
}

func mapKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func latencySample(c Chain) LatencyMS {
	ms := func(a, b time.Time) int64 {
		if a.IsZero() || b.IsZero() || b.Before(a) {
			return 0
		}
		return b.Sub(a).Milliseconds()
	}
	var firstAction time.Time
	if c.FirstActionAt != nil {
		firstAction = *c.FirstActionAt
	}
	var enrich time.Time
	if c.EnrichmentAt != nil {
		enrich = *c.EnrichmentAt
	}
	var conv time.Time
	if c.ConversationAt != nil {
		conv = *c.ConversationAt
	}
	var prop time.Time
	if c.ProposalAt != nil {
		prop = *c.ProposalAt
	}
	var closeT time.Time
	if c.CloseAt != nil {
		closeT = *c.CloseAt
	}
	return LatencyMS{
		LeadToIngest:           ms(c.LeadCreatedAt, c.IngestedAt),
		IngestToEnrichment:     ms(c.IngestedAt, enrich),
		EnrichmentToAction:     ms(enrich, firstAction),
		ActionToConversation:   ms(firstAction, conv),
		ConversationToProposal: ms(conv, prop),
		ProposalToClose:        ms(prop, closeT),
		SampledChains:          1,
	}
}
