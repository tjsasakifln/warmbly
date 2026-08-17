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
	byTerms := map[string]int{}
	byCTA := map[string]int{}
	comm := CommercialCounts{}
	exCount := 0
	manual := 0
	byIntent := map[string]int{}
	ma := emptyAssetSlice(AssetFamilyMarketAnswer)
	ca := emptyAssetSlice(AssetFamilyContractAnalysis)
	xr := emptyAssetSlice(AssetFamilyB2GXRay)
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
		incBreakdown(byTerms, c.Commercial.Offer.TermsVersion)
		incBreakdown(byCTA, firstNonEmpty(c.CTAID, c.Keys.CTAID))
		incBreakdown(byIntent, firstNonEmpty(c.IntentClass, c.Keys.IntentClass))
		addCommercialCounts(&comm, c)
		if c.Held {
			exCount++
		}
		manual += c.Commercial.ManualTouches
		switch normalizeAssetFamily(firstNonEmpty(c.AssetFamily, c.Keys.AssetFamily)) {
		case AssetFamilyMarketAnswer:
			addAssetSlice(&ma, c)
		case AssetFamilyContractAnalysis:
			addAssetSlice(&ca, c)
		case AssetFamilyB2GXRay:
			addAssetSlice(&xr, c)
		}
		if c.Keys.CustomerProofLane && !c.NotALead {
			view.CustomerProof++
		}
		if !c.Held && c.RevenueEvidenced && c.RevenueCents > 0 {
			view.RevenueCents += c.RevenueCents
			view.RevenueStatus = "evidenced"
		}
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

		if !c.NotALead && strings.TrimSpace(c.LeadID) != "" && c.LeadID != Unknown {
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
		if c.RouteFamily == FamilyInbound && qco && !c.NotALead && !c.Held {
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
		if !c.Held && c.PipelineOpen {
			view.Pipeline++
			if fi >= 0 {
				view.Families[fi].Pipeline++
			}
		}

		switch {
		case !c.Held && isWonType(c.OutcomeType) && c.HumanConfirmed:
			view.Won++
			view.Denominators.Closed++
			if fi >= 0 {
				view.Families[fi].Won++
			}
		case !c.Held && isLostType(c.OutcomeType) && c.HumanConfirmed:
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
			latSum.PublishedToDetected += n.PublishedToDetected
			latSum.DetectedToLead += n.DetectedToLead
			latSum.LeadToIngest += n.LeadToIngest
			latSum.LeadToFirstAction += n.LeadToFirstAction
			latSum.IngestToEnrichment += n.IngestToEnrichment
			latSum.EnrichmentToAction += n.EnrichmentToAction
			latSum.ActionToConversation += n.ActionToConversation
			latSum.ConversationToProposal += n.ConversationToProposal
			latSum.ProposalToClose += n.ProposalToClose
			latSum.LeadToPayment += n.LeadToPayment
			latSum.PaymentToOnboarding += n.PaymentToOnboarding
			latSum.OnboardingToActivation += n.OnboardingToActivation
			latSum.CensoredCycles += n.CensoredCycles
		} else {
			latSum.CensoredCycles++
		}
	}

	if latN > 0 {
		view.Latency = LatencyMS{
			PublishedToDetected:    latSum.PublishedToDetected / int64(latN),
			DetectedToLead:         latSum.DetectedToLead / int64(latN),
			LeadToIngest:           latSum.LeadToIngest / int64(latN),
			LeadToFirstAction:      latSum.LeadToFirstAction / int64(latN),
			IngestToEnrichment:     latSum.IngestToEnrichment / int64(latN),
			EnrichmentToAction:     latSum.EnrichmentToAction / int64(latN),
			ActionToConversation:   latSum.ActionToConversation / int64(latN),
			ConversationToProposal: latSum.ConversationToProposal / int64(latN),
			ProposalToClose:        latSum.ProposalToClose / int64(latN),
			LeadToPayment:          latSum.LeadToPayment / int64(latN),
			PaymentToOnboarding:    latSum.PaymentToOnboarding / int64(latN),
			OnboardingToActivation: latSum.OnboardingToActivation / int64(latN),
			SampledChains:          latN,
			CensoredCycles:         latSum.CensoredCycles,
			Baseline:               latencyBaseline(chains, includeSynthetic),
		}
	} else {
		view.Latency.Baseline = BaselineNone
		view.Latency.CensoredCycles = latSum.CensoredCycles
	}
	if view.RevenueStatus == "" {
		view.RevenueStatus = Unknown
	}
	view.BySource = mapBreakdown(bySource)
	view.ByAsset = mapBreakdown(byAsset)
	view.ByTrigger = mapBreakdown(byTrigger)
	view.ByOffer = mapBreakdown(byOffer)
	view.ByRoute = mapBreakdown(byRoute)
	view.ByIntent = mapBreakdown(byIntent)
	view.ByTerms = mapBreakdown(byTerms)
	view.ByCTA = mapBreakdown(byCTA)
	comm.ExceptionCount = exCount
	comm.ManualTouches = manual
	view.Commercial = comm
	view.MarketAnswer = ma
	view.ContractAnalysis = ca
	view.B2GXRay = xr
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
	if hasInvertedLatency(c) {
		return LatencyMS{}
	}
	span := func(a, b time.Time) (int64, bool) {
		if a.IsZero() || b.IsZero() {
			return 0, true
		}
		if b.Before(a) {
			return 0, true
		}
		return b.Sub(a).Milliseconds(), false
	}
	var firstAction, enrich, conv, prop, closeT, pub, det time.Time
	if c.FirstActionAt != nil {
		firstAction = *c.FirstActionAt
	}
	if c.EnrichmentAt != nil {
		enrich = *c.EnrichmentAt
	}
	if c.ConversationAt != nil {
		conv = *c.ConversationAt
	}
	if c.ProposalAt != nil {
		prop = *c.ProposalAt
	}
	if c.CloseAt != nil {
		closeT = *c.CloseAt
	}
	if c.PublishedAt != nil {
		pub = *c.PublishedAt
	}
	if c.DetectedAt != nil {
		det = *c.DetectedAt
	}
	var censored int
	take := func(a, b time.Time) int64 {
		v, miss := span(a, b)
		if miss {
			censored++
		}
		return v
	}
	var pay, onboard, activate time.Time
	if c.Commercial.Payment.ReceivedAt != nil {
		pay = *c.Commercial.Payment.ReceivedAt
	} else if c.Commercial.Payment.ConfirmedAt != nil {
		pay = *c.Commercial.Payment.ConfirmedAt
	}
	if c.Commercial.Delivery.OnboardingStartedAt != nil {
		onboard = *c.Commercial.Delivery.OnboardingStartedAt
	}
	if c.Commercial.Delivery.ServiceActivatedAt != nil {
		activate = *c.Commercial.Delivery.ServiceActivatedAt
	}
	return LatencyMS{
		PublishedToDetected:    take(pub, det),
		DetectedToLead:         take(det, c.LeadCreatedAt),
		LeadToIngest:           take(c.LeadCreatedAt, c.IngestedAt),
		LeadToFirstAction:      take(c.LeadCreatedAt, firstAction),
		IngestToEnrichment:     take(c.IngestedAt, enrich),
		EnrichmentToAction:     take(enrich, firstAction),
		ActionToConversation:   take(firstAction, conv),
		ConversationToProposal: take(conv, prop),
		ProposalToClose:        take(prop, closeT),
		LeadToPayment:          take(firstNonZeroTime(c.LeadCreatedAt, c.IngestedAt), pay),
		PaymentToOnboarding:    take(pay, onboard),
		OnboardingToActivation: take(onboard, activate),
		SampledChains:          1,
		CensoredCycles:         censored,
	}
}

func firstNonZeroTime(vs ...time.Time) time.Time {
	for _, t := range vs {
		if !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}

func addCommercialCounts(comm *CommercialCounts, c Chain) {
	st := c.Commercial
	if st.Offer.OfferID != "" || hasTimelineType(st, EventOfferViewed) || hasTimelineType(st, EventOfferSelected) {
		comm.OfferViewed++
	}
	if st.Capacity.Eligibility != "" && st.Capacity.Eligibility != EligibilityUnknown {
		comm.Eligibility++
	}
	switch strings.ToLower(st.Capacity.State) {
	case CapacityStateOK:
		comm.CapacityApproved++
	case CapacityStateHold:
		comm.CapacityHeld++
	case CapacityStateWait:
		comm.CapacityWaitlisted++
	case CapacityStateReject:
		comm.CapacityRejected++
	case CapacityStateExpired:
		comm.CapacityExpired++
	case CapacityStateFinal:
		comm.CapacityUsed++
	}
	if hasTimelineType(st, EventTermsAccepted) {
		comm.TermsAccepted++
	}
	if hasTimelineType(st, EventCheckoutCreated) {
		comm.CheckoutCreated++
	}
	switch strings.ToLower(st.Payment.CanonicalStatus) {
	case PaymentStatusCreated:
		comm.PaymentCreated++
	case PaymentStatusPending:
		comm.PaymentPending++
	case PaymentStatusConfirmed:
		comm.PaymentConfirmed++
	case PaymentStatusReceived:
		comm.PaymentReceived++
	case PaymentStatusOverdue:
		comm.PaymentOverdue++
	case PaymentStatusRefunded:
		comm.PaymentRefunded++
	}
	if st.Delivery.OnboardingStartedAt != nil {
		comm.Onboarding++
	}
	if st.Delivery.ServiceActivatedAt != nil && st.Delivery.ServiceEndedAt == nil {
		comm.ServiceActive++
	}
	if hasTimelineType(st, EventRenewalDue) || hasTimelineType(st, EventRenewed) {
		comm.Renewal++
	}
	if st.Subscription.CanceledAt != nil || st.Subscription.CanonicalStatus == EventSubscriptionCanceled {
		comm.Canceled++
	}
	if c.RouteFamily == FamilyExpansion {
		comm.Expansion++
	}
	comm.ContractedCents += st.Payment.ContractedCents
	comm.MRRCents += st.Payment.MRRCents
	comm.ReceivedCents += st.Payment.ReceivedCents
	if c.Qualified && !c.Held && st.Payment.CanonicalStatus != PaymentStatusReceived {
		comm.QualifiedPipeline++
	}
}

func hasInvertedLatency(c Chain) bool {
	times := []time.Time{}
	add := func(t time.Time) {
		if !t.IsZero() {
			times = append(times, t)
		}
	}
	if c.PublishedAt != nil {
		add(*c.PublishedAt)
	}
	if c.DetectedAt != nil {
		add(*c.DetectedAt)
	}
	add(c.LeadCreatedAt)
	if c.FirstActionAt != nil {
		add(*c.FirstActionAt)
	}
	if c.ConversationAt != nil {
		add(*c.ConversationAt)
	}
	if c.ProposalAt != nil {
		add(*c.ProposalAt)
	}
	if c.CloseAt != nil {
		add(*c.CloseAt)
	}
	for i := 1; i < len(times); i++ {
		if times[i].Before(times[i-1]) {
			return true
		}
	}
	return false
}

func latencyBaseline(chains []Chain, includeSynthetic bool) string {
	sawReal, sawSyn := false, false
	for _, c := range chains {
		if !includeSynthetic && (c.Synthetic || c.Label == LabelSynthetic) {
			continue
		}
		if c.Synthetic || c.Label == LabelSynthetic {
			sawSyn = true
		} else {
			sawReal = true
		}
	}
	switch {
	case sawReal:
		return BaselineObserved
	case sawSyn:
		return BaselineSynthetic
	default:
		return BaselineNone
	}
}

func emptyAssetSlice(family string) AssetSlice {
	return AssetSlice{AssetFamily: family, RevenueStatus: Unknown}
}

func addAssetSlice(s *AssetSlice, c Chain) {
	if s == nil {
		return
	}
	if c.NotALead {
		s.Completions++
		return
	}
	if knownID(c.LeadID) != "" || knownID(c.ReceiptID) != "" {
		s.Leads++
	}
	if knownID(c.ActionID) != "" {
		s.Actions++
	}
	if c.Qualified || c.OutcomeType == OutcomeQualifiedConversation {
		s.Qualified++
	}
	if c.OutcomeType == OutcomeMeeting {
		s.Meetings++
	}
	if c.OutcomeType == OutcomeProposal || c.ProposalAt != nil {
		s.Proposals++
	}
	if !c.Held && c.PipelineOpen {
		s.Pipeline++
	}
	switch {
	case !c.Held && isWonType(c.OutcomeType) && c.HumanConfirmed:
		s.Won++
	case !c.Held && isLostType(c.OutcomeType) && c.HumanConfirmed:
		s.Lost++
	default:
		s.Unknown++
	}
	if !c.Held && c.RevenueEvidenced && c.RevenueCents > 0 {
		s.RevenueCents += c.RevenueCents
		s.RevenueStatus = "evidenced"
	}
	if knownID(c.Source) == "" || knownID(c.AssetID) == "" {
		s.MissingAttribution++
	}
}
