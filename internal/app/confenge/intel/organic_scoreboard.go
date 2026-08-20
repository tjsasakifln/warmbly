package intel

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const organicLatencyMinN = 5

// OrganicDiscoveryAggregate is a web-cfg GSC/site snapshot. It is never
// inferred from lead volume.
type OrganicDiscoveryAggregate struct {
	OrganicSource string    `json:"organic_source,omitempty"`
	AssetFamily   string    `json:"asset_family,omitempty"`
	AssetID       string    `json:"asset_id,omitempty"`
	LandingPath   string    `json:"landing_path,omitempty"`
	IntentClass   string    `json:"intent_class,omitempty"`
	QueryClass    string    `json:"query_class,omitempty"`
	Window        string    `json:"window,omitempty"`
	Eligible      *int      `json:"eligible"`
	Appeared      *int      `json:"appeared"`
	Clicked       *int      `json:"clicked"`
	Engaged       *int      `json:"engaged"`
	Coverage      string    `json:"coverage,omitempty"`
	Freshness     string    `json:"freshness,omitempty"`
	At            time.Time `json:"at,omitempty"`
	Synthetic     bool      `json:"synthetic,omitempty"`
}

// OrganicLatency is median/p75/p90 only when n allows. Missing is UNKNOWN.
type OrganicLatency struct {
	N      int    `json:"n"`
	Median string `json:"median"`
	P75    string `json:"p75"`
	P90    string `json:"p90"`
}

// OrganicLayer is one of the ten honest layers. Discovery stays BLOCKED
// without a web-cfg aggregate.
type OrganicLayer struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Count       int    `json:"count"`
	Denominator int    `json:"denominator"`
	Conversion  string `json:"conversion,omitempty"`
	Unknown     int    `json:"unknown"`
	Observation string `json:"observation"`
}

// OrganicSlice is one source/asset/landing cohort cell.
type OrganicSlice struct {
	Key                string         `json:"key"`
	OrganicSource      string         `json:"organic_source"`
	AssetFamily        string         `json:"asset_family"`
	AssetID            string         `json:"asset_id"`
	LandingPath        string         `json:"landing_path"`
	IntentClass        string         `json:"intent_class,omitempty"`
	Attribution        string         `json:"attribution"`
	Layers             []OrganicLayer `json:"layers"`
	Won                int            `json:"won"`
	Lost               int            `json:"lost"`
	Unknown            int            `json:"unknown"`
	ContractedCents    int64          `json:"contracted_revenue_cents"`
	ReceivedCents      int64          `json:"received_revenue_cents"`
	ROAS               string         `json:"organic_roas"`
	CAC                string         `json:"organic_cac"`
	LeadToAction       OrganicLatency `json:"lead_to_first_action"`
	OpenCensored       int            `json:"open_censored"`
	DiscoveryFreshness string         `json:"discovery_freshness"`
}

// OrganicWindow is one complete or open window.
type OrganicWindow struct {
	ID       string         `json:"id"`
	Complete bool           `json:"complete"`
	Censored bool           `json:"censored"`
	From     string         `json:"from"`
	To       string         `json:"to"`
	Slices   []OrganicSlice `json:"slices"`
	BySource []OrganicSlice `json:"by_source"`
}

// OrganicScoreboard is the PII-free organic cohort placar.
type OrganicScoreboard struct {
	SchemaVersion    string          `json:"schema_version"`
	GeneratedAt      string          `json:"generated_at"`
	IncludeSynthetic bool            `json:"include_synthetic"`
	CausalProof      bool            `json:"causal_proof"`
	RealEmpty        bool            `json:"real_empty"`
	Windows          []OrganicWindow `json:"windows"`
	Sources          []string        `json:"sources"`
	Recommendation   string          `json:"recommendation"`
}

// OrganicScoreboardSources is the existing-plane input.
type OrganicScoreboardSources struct {
	Now              time.Time
	IncludeSynthetic bool
	Chains           []Chain
	Discovery        []OrganicDiscoveryAggregate
}

// ProjectOrganicScoreboard keeps discovery, lead, pipeline, and revenue
// on separate layers. GSC numbers are never inferred from leads.
func ProjectOrganicScoreboard(src OrganicScoreboardSources) OrganicScoreboard {
	now := src.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	board := OrganicScoreboard{
		SchemaVersion:    OrganicScoreboardSchemaV1,
		GeneratedAt:      now.UTC().Format(time.RFC3339),
		IncludeSynthetic: src.IncludeSynthetic,
		CausalProof:      false,
		Sources:          OrganicSources(),
		Recommendation:   RecommendNeedsWebCfg,
	}
	chains := filterOrganicChains(src.Chains, src.IncludeSynthetic)
	realN := 0
	for _, c := range chains {
		if !c.Synthetic && c.Label != LabelSynthetic {
			realN++
		}
	}
	board.RealEmpty = realN == 0
	specs := organicWindowSpecs(now)
	for _, spec := range specs {
		board.Windows = append(board.Windows, projectOrganicWindow(spec, chains, src.Discovery, src.IncludeSynthetic))
	}
	switch {
	case hasDiscovery(src.Discovery, src.IncludeSynthetic) && realN > 0:
		board.Recommendation = RecommendReadyInteg
	case hasDiscovery(src.Discovery, src.IncludeSynthetic):
		board.Recommendation = RecommendNeedsReal
	default:
		board.Recommendation = RecommendNeedsWebCfg
	}
	return board
}

type organicWindowSpec struct {
	id       string
	from, to time.Time
	complete bool
	censored bool
}

func organicWindowSpecs(now time.Time) []organicWindowSpec {
	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	w7to := today
	w7from := today.AddDate(0, 0, -7)
	w28from := today.AddDate(0, 0, -28)
	w90from := today.AddDate(0, 0, -90)
	return []organicWindowSpec{
		{id: Window7dComplete, from: w7from, to: w7to, complete: true},
		{id: Window28dComplete, from: w28from, to: w7to, complete: true},
		{id: Window90d, from: w90from, to: now, complete: false},
		{id: WindowOpen, from: w90from, to: now, complete: false, censored: true},
	}
}

func filterOrganicChains(in []Chain, includeSynthetic bool) []Chain {
	var out []Chain
	for _, c := range in {
		if !includeSynthetic && (c.Synthetic || c.Label == LabelSynthetic) {
			continue
		}
		if c.NotALead && c.EventType == EventSearchObservation {
			continue
		}
		out = append(out, c)
	}
	return out
}

func hasDiscovery(rows []OrganicDiscoveryAggregate, includeSynthetic bool) bool {
	for _, r := range rows {
		if !includeSynthetic && r.Synthetic {
			continue
		}
		return true
	}
	return false
}

func projectOrganicWindow(spec organicWindowSpec, chains []Chain, discovery []OrganicDiscoveryAggregate, includeSynthetic bool) OrganicWindow {
	w := OrganicWindow{
		ID:       spec.id,
		Complete: spec.complete,
		Censored: spec.censored,
		From:     spec.from.UTC().Format(time.RFC3339),
		To:       spec.to.UTC().Format(time.RFC3339),
	}
	var inWindow []Chain
	for _, c := range chains {
		if !chainInWindow(c, spec) {
			continue
		}
		if spec.censored && organicTerminal(c) {
			continue
		}
		inWindow = append(inWindow, c)
	}
	groups := map[string][]Chain{}
	sourceGroups := map[string][]Chain{}
	for _, c := range inWindow {
		key := organicSliceKey(c)
		groups[key] = append(groups[key], c)
		src := organicSourceOf(c.Keys)
		sourceGroups[src] = append(sourceGroups[src], c)
	}
	for _, key := range sortedKeys(groups) {
		w.Slices = append(w.Slices, projectOrganicSlice(key, groups[key], discovery, spec, includeSynthetic))
	}
	for _, src := range OrganicSources() {
		cs := sourceGroups[src]
		if len(cs) == 0 && src != Unknown {
			// still emit an empty UNKNOWN-free source so slices never mix
			w.BySource = append(w.BySource, emptySourceSlice(src, spec, discovery, includeSynthetic))
			continue
		}
		if len(cs) == 0 {
			w.BySource = append(w.BySource, emptySourceSlice(src, spec, discovery, includeSynthetic))
			continue
		}
		sl := projectOrganicSlice("source:"+src, cs, discovery, spec, includeSynthetic)
		sl.OrganicSource = src
		w.BySource = append(w.BySource, sl)
	}
	return w
}

func chainInWindow(c Chain, spec organicWindowSpec) bool {
	t := c.LeadCreatedAt
	if t.IsZero() {
		t = c.CreatedAt
	}
	if t.IsZero() {
		return false
	}
	if t.Before(spec.from) {
		return false
	}
	if !t.Before(spec.to) && !t.Equal(spec.to) {
		// complete windows exclude the open end; 90d/open include now
		if spec.complete {
			return false
		}
		if t.After(spec.to) {
			return false
		}
	}
	if spec.complete && !t.Before(spec.to) {
		return false
	}
	return true
}

func organicTerminal(c Chain) bool {
	if isWonType(c.OutcomeType) || isLostType(c.OutcomeType) {
		return c.HumanConfirmed
	}
	return false
}

func organicSliceKey(c Chain) string {
	return strings.Join([]string{
		organicSourceOf(c.Keys),
		idOrUnknown(c.AssetFamily),
		idOrUnknown(c.AssetID),
		idOrUnknown(c.Keys.LandingPath),
		organicAttributionKind(c),
	}, "|")
}

func organicAttributionKind(c Chain) string {
	if knownID(c.Keys.OrganicSource) != "" && knownID(c.Keys.AssetID) != "" {
		if c.Keys.RecordKind == RecordKindReal || (!c.Synthetic && c.Label != LabelSynthetic) {
			return AttributionDirect
		}
	}
	if knownID(c.AssetID) != "" || knownID(c.Keys.LandingPath) != "" {
		return AttributionAssisted
	}
	return Unknown
}

func projectOrganicSlice(key string, chains []Chain, discovery []OrganicDiscoveryAggregate, spec organicWindowSpec, includeSynthetic bool) OrganicSlice {
	src := Unknown
	family := Unknown
	asset := Unknown
	landing := Unknown
	intent := Unknown
	attr := Unknown
	if len(chains) > 0 {
		src = organicSourceOf(chains[0].Keys)
		family = idOrUnknown(chains[0].AssetFamily)
		asset = idOrUnknown(chains[0].AssetID)
		landing = idOrUnknown(chains[0].Keys.LandingPath)
		intent = idOrUnknown(chains[0].IntentClass)
		attr = organicAttributionKind(chains[0])
	}
	sl := OrganicSlice{
		Key:                key,
		OrganicSource:      src,
		AssetFamily:        family,
		AssetID:            asset,
		LandingPath:        landing,
		IntentClass:        intent,
		Attribution:        attr,
		ROAS:               TruthBlocked,
		CAC:                TruthBlocked,
		DiscoveryFreshness: "no consumer",
	}
	disc := matchDiscovery(discovery, src, family, asset, landing, spec.id, includeSynthetic)
	if disc != nil {
		sl.DiscoveryFreshness = firstNonEmpty(disc.Freshness, "snapshot")
	}
	leads, qualified, acked, conv, meet, prop, pipe := 0, 0, 0, 0, 0, 0, 0
	won, lost, unk := 0, 0, 0
	open := 0
	var contracted, received int64
	var latencies []int64
	for _, c := range chains {
		if c.NotALead {
			continue
		}
		leads++
		if c.Qualified {
			qualified++
		}
		if c.FirstActionAt != nil {
			acked++
			if !c.LeadCreatedAt.IsZero() && !c.FirstActionAt.Before(c.LeadCreatedAt) {
				latencies = append(latencies, c.FirstActionAt.Sub(c.LeadCreatedAt).Milliseconds())
			}
		}
		if c.Conversation {
			conv++
		}
		if c.OutcomeType == OutcomeMeeting || (c.ConversationAt != nil && c.OutcomeType == OutcomeMeeting) {
			meet++
		}
		if c.OutcomeType == OutcomeProposal || c.ProposalAt != nil {
			prop++
		}
		if c.PipelineOpen {
			pipe++
		}
		switch {
		case isWonType(c.OutcomeType) && c.HumanConfirmed:
			won++
		case isLostType(c.OutcomeType) && c.HumanConfirmed:
			lost++
		default:
			unk++
		}
		if !organicTerminal(c) {
			open++
		}
		contracted += c.Commercial.Payment.ContractedCents
		switch {
		case c.RevenueEvidenced && c.RevenueCents > 0:
			received += c.RevenueCents
		case c.Commercial.Payment.ReceivedCents > 0:
			received += c.Commercial.Payment.ReceivedCents
		}
	}
	if c := countMeetings(chains); c > meet {
		meet = c
	}
	sl.Won, sl.Lost, sl.Unknown = won, lost, unk
	sl.ContractedCents = contracted
	sl.ReceivedCents = received
	sl.OpenCensored = open
	sl.LeadToAction = organicLatencyOf(latencies)
	sl.Layers = []OrganicLayer{
		discoveryLayer(LayerEligible, disc, func(d *OrganicDiscoveryAggregate) *int { return d.Eligible }, leads),
		discoveryLayer(LayerAppeared, disc, func(d *OrganicDiscoveryAggregate) *int { return d.Appeared }, leads),
		discoveryLayer(LayerClicked, disc, func(d *OrganicDiscoveryAggregate) *int { return d.Clicked }, leads),
		discoveryLayer(LayerEngaged, disc, func(d *OrganicDiscoveryAggregate) *int { return d.Engaged }, leads),
		countLayer(LayerLeadValid, leads, leads, unk),
		countLayer(LayerQualifiedLead, qualified, leads, 0),
		countLayer(LayerAcknowledged, acked, leads, 0),
		countLayer(LayerConversation, conv, maxInt(qualified, conv), 0),
		countLayer(LayerMeeting, meet, maxInt(conv, meet), 0),
		countLayer(LayerProposal, prop, maxInt(meet, prop), 0),
		countLayer(LayerQualifiedPipeline, pipe, maxInt(prop, pipe), 0),
		wonLostLayer(won, lost, unk, maxInt(pipe, won+lost+unk)),
		revenueLayer(contracted, received, pipe),
	}
	return sl
}

func countMeetings(chains []Chain) int {
	n := 0
	for _, c := range chains {
		if c.OutcomeType == OutcomeMeeting {
			n++
		}
	}
	return n
}

func emptySourceSlice(src string, spec organicWindowSpec, discovery []OrganicDiscoveryAggregate, includeSynthetic bool) OrganicSlice {
	sl := OrganicSlice{
		Key:                "source:" + src,
		OrganicSource:      src,
		AssetFamily:        Unknown,
		AssetID:            Unknown,
		LandingPath:        Unknown,
		Attribution:        Unknown,
		ROAS:               TruthBlocked,
		CAC:                TruthBlocked,
		DiscoveryFreshness: "no consumer",
	}
	disc := matchDiscovery(discovery, src, "", "", "", spec.id, includeSynthetic)
	if disc != nil {
		sl.DiscoveryFreshness = firstNonEmpty(disc.Freshness, "snapshot")
	}
	sl.Layers = []OrganicLayer{
		discoveryLayer(LayerEligible, disc, func(d *OrganicDiscoveryAggregate) *int { return d.Eligible }, 0),
		discoveryLayer(LayerAppeared, disc, func(d *OrganicDiscoveryAggregate) *int { return d.Appeared }, 0),
		discoveryLayer(LayerClicked, disc, func(d *OrganicDiscoveryAggregate) *int { return d.Clicked }, 0),
		discoveryLayer(LayerEngaged, disc, func(d *OrganicDiscoveryAggregate) *int { return d.Engaged }, 0),
		countLayer(LayerLeadValid, 0, 0, 0),
		countLayer(LayerQualifiedLead, 0, 0, 0),
		countLayer(LayerAcknowledged, 0, 0, 0),
		countLayer(LayerConversation, 0, 0, 0),
		countLayer(LayerMeeting, 0, 0, 0),
		countLayer(LayerProposal, 0, 0, 0),
		countLayer(LayerQualifiedPipeline, 0, 0, 0),
		wonLostLayer(0, 0, 0, 0),
		revenueLayer(0, 0, 0),
	}
	return sl
}

func matchDiscovery(rows []OrganicDiscoveryAggregate, src, family, asset, landing, window string, includeSynthetic bool) *OrganicDiscoveryAggregate {
	for i := range rows {
		r := rows[i]
		if !includeSynthetic && r.Synthetic {
			continue
		}
		if window != "" && r.Window != "" && r.Window != window {
			continue
		}
		if src != "" && r.OrganicSource != "" && NormalizeOrganicSource(r.OrganicSource) != src {
			continue
		}
		if asset != "" && asset != Unknown && r.AssetID != "" && r.AssetID != asset {
			continue
		}
		if landing != "" && landing != Unknown && r.LandingPath != "" && r.LandingPath != landing {
			continue
		}
		if family != "" && family != Unknown && r.AssetFamily != "" && r.AssetFamily != family {
			continue
		}
		return &rows[i]
	}
	return nil
}

func discoveryLayer(id string, disc *OrganicDiscoveryAggregate, pick func(*OrganicDiscoveryAggregate) *int, leadHint int) OrganicLayer {
	ly := OrganicLayer{
		ID:          id,
		Status:      TruthBlocked,
		Observation: "ELIGIBLE/APPEARED/CLICKED/ENGAGED are not inferred from lead volume. Future ingest is " + OrganicDiscoveryContract + ".",
	}
	_ = leadHint
	if disc == nil {
		return ly
	}
	switch strings.ToUpper(strings.TrimSpace(disc.Coverage)) {
	case CoverageAbsent:
		ly.Status = CoverageAbsent
		ly.Observation = "web-cfg aggregate ABSENT"
		return ly
	case CoverageBlocked:
		ly.Status = TruthBlocked
		ly.Observation = "web-cfg aggregate BLOCKED"
		return ly
	case CoverageUnknown:
		ly.Status = TruthUnknown
		ly.Observation = "web-cfg aggregate UNKNOWN"
		return ly
	}
	n := pick(disc)
	if n == nil {
		ly.Status = TruthUnknown
		ly.Observation = "web-cfg aggregate " + OrganicDiscoveryContract + " count UNKNOWN"
		return ly
	}
	ly.Count = *n
	ly.Denominator = *n
	ly.Status = TruthTrue
	ly.Observation = "web-cfg aggregate " + OrganicDiscoveryContract
	return ly
}

func countLayer(id string, n, den, unk int) OrganicLayer {
	st := TruthFalse
	if n > 0 {
		st = TruthTrue
	} else if den == 0 {
		st = TruthUnknown
	}
	return OrganicLayer{
		ID:          id,
		Status:      st,
		Count:       n,
		Denominator: den,
		Conversion:  conversionText(n, den),
		Unknown:     unk,
	}
}

func wonLostLayer(won, lost, unk, den int) OrganicLayer {
	return OrganicLayer{
		ID:          LayerWonLostUnknown,
		Status:      TruthUnknown,
		Count:       won + lost,
		Denominator: den,
		Unknown:     unk,
		Observation: "WON/LOST require human confirmation. UNKNOWN stays visible.",
		Conversion:  conversionText(won, den),
	}
}

func revenueLayer(contracted, received int64, pipe int) OrganicLayer {
	st := TruthUnknown
	if received > 0 {
		st = TruthTrue
	} else if contracted > 0 || pipe > 0 {
		st = TruthFalse
	}
	return OrganicLayer{
		ID:          LayerRevenue,
		Status:      st,
		Count:       int(received),
		Denominator: int(contracted),
		Observation: "CONTRACTED_REVENUE and RECEIVED_REVENUE stay distinct. No organic ROAS/CAC without complete cost and observed received revenue.",
	}
}

func conversionText(n, den int) string {
	if den <= 0 {
		return Unknown
	}
	return fmt.Sprintf("%.3f", float64(n)/float64(den))
}

func organicLatencyOf(samples []int64) OrganicLatency {
	out := OrganicLatency{N: len(samples), Median: Unknown, P75: Unknown, P90: Unknown}
	if len(samples) < organicLatencyMinN {
		return out
	}
	cp := append([]int64(nil), samples...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	out.Median = time.Duration(percentile(cp, 50) * int64(time.Millisecond)).String()
	out.P75 = time.Duration(percentile(cp, 75) * int64(time.Millisecond)).String()
	out.P90 = time.Duration(percentile(cp, 90) * int64(time.Millisecond)).String()
	return out
}

func percentile(sorted []int64, p int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 100 {
		return sorted[len(sorted)-1]
	}
	idx := (p * (len(sorted) - 1)) / 100
	return sorted[idx]
}

func sortedKeys(m map[string][]Chain) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
