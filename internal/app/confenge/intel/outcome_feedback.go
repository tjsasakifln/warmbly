package intel

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	OutcomeFeedbackSchemaV1  = "confenge.acquisition_outcome_feedback.v1"
	OutcomeFeedbackMinCellN  = 5
	FeedbackStatusObserved   = "OBSERVED"
	FeedbackStatusPartial    = "PARTIAL"
	FeedbackStatusUnknown    = "UNKNOWN"
	FeedbackStatusUnjoinable = "UNJOINABLE"
	FeedbackStatusWithheld   = "WITHHELD_SMALL_CELL"
	FeedbackJoinJoined       = "JOINED"
	FeedbackJoinPartial      = "PARTIAL"
	FeedbackJoinUnjoinable   = "UNJOINABLE"
	FeedbackCohortDirect     = "DIRECT"
	FeedbackCohortRollup     = "SMALL_CELL_ROLLUP"
	FeedbackCohortWithheld   = "WITHHELD_SMALL_CELL"
	RouteAttributionPartial  = "PARTIAL"
)

// OutcomeFeedbackPeriod is a half-open acquisition cohort period [from,to).
// Projection canonicalizes both bounds to complete UTC calendar months.
type OutcomeFeedbackPeriod struct {
	From time.Time
	To   time.Time
}

// ParseOutcomeFeedbackPeriod accepts inclusive UTC calendar months (YYYY-MM).
func ParseOutcomeFeedbackPeriod(fromRaw, toRaw string, now time.Time) (OutcomeFeedbackPeriod, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	toMonth, err := parseFeedbackMonth(toRaw)
	if err != nil {
		return OutcomeFeedbackPeriod{}, fmt.Errorf("invalid to: %w", err)
	}
	if toMonth.IsZero() {
		toMonth = monthStart(now)
	}
	fromMonth, err := parseFeedbackMonth(fromRaw)
	if err != nil {
		return OutcomeFeedbackPeriod{}, fmt.Errorf("invalid from: %w", err)
	}
	if fromMonth.IsZero() {
		fromMonth = toMonth.AddDate(0, -2, 0)
	}
	if fromMonth.After(toMonth) {
		return OutcomeFeedbackPeriod{}, fmt.Errorf("from must be before to")
	}
	return OutcomeFeedbackPeriod{From: fromMonth, To: toMonth.AddDate(0, 1, 0)}, nil
}

func parseFeedbackMonth(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse("2006-01", raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("use YYYY-MM")
	}
	return t.UTC(), nil
}

func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func canonicalOutcomeFeedbackPeriod(period OutcomeFeedbackPeriod, now time.Time) OutcomeFeedbackPeriod {
	if period.From.IsZero() || period.To.IsZero() {
		fallback, _ := ParseOutcomeFeedbackPeriod("", "", now)
		return fallback
	}
	from := monthStart(period.From)
	to := period.To.UTC()
	toStart := monthStart(to)
	if !to.Equal(toStart) {
		toStart = toStart.AddDate(0, 1, 0)
	}
	if !from.Before(toStart) {
		toStart = from.AddDate(0, 1, 0)
	}
	return OutcomeFeedbackPeriod{From: from, To: toStart}
}

type OutcomeFeedbackPeriodView struct {
	From        string `json:"from"`
	To          string `json:"to"`
	ToExclusive bool   `json:"to_exclusive"`
}

// OutcomeFeedbackMetric reports only observed positive facts. Records without
// an observed fact stay UNKNOWN; they are never coerced to zero.
type OutcomeFeedbackMetric struct {
	Observed *int   `json:"observed"`
	Unknown  *int   `json:"unknown"`
	Status   string `json:"status"`
}

type OutcomeFeedbackStates struct {
	Won     *int   `json:"won"`
	Lost    *int   `json:"lost"`
	Unknown *int   `json:"unknown"`
	Status  string `json:"status"`
}

// OutcomeFeedbackValue is the sum of values legitimately present on the
// commercial chain. It is an observed lower bound, never estimated value.
type OutcomeFeedbackValue struct {
	ContractedCents *int64 `json:"contracted_cents"`
	ReceivedCents   *int64 `json:"received_cents"`
	KnownRecords    *int   `json:"known_records"`
	UnknownRecords  *int   `json:"unknown_records"`
	Status          string `json:"status"`
}

// OutcomeFeedbackMargin remains UNKNOWN until an evidence-backed margin fact
// is legitimately persisted by Warmbly.
type OutcomeFeedbackMargin struct {
	KnownCents     *int64 `json:"known_cents"`
	KnownRecords   *int   `json:"known_records"`
	UnknownRecords *int   `json:"unknown_records"`
	Status         string `json:"status"`
}

type OutcomeFeedbackRow struct {
	Cohort                 string                `json:"cohort"`
	AcquisitionSource      string                `json:"acquisition_source"`
	OrganicSource          string                `json:"organic_source"`
	RouteFamily            string                `json:"route_family"`
	AcquisitionRoute       string                `json:"acquisition_route"`
	AssetFamily            string                `json:"asset_family"`
	AssetID                string                `json:"asset_id"`
	CTAID                  string                `json:"cta_id"`
	IntentClass            string                `json:"intent_class"`
	BuyerJob               string                `json:"buyer_job"`
	CohortMonth            string                `json:"cohort_month"`
	RecordKind             string                `json:"record_kind"`
	JoinStatus             string                `json:"join_status"`
	PrivacyUnits           *int                  `json:"privacy_units"`
	Receipts               OutcomeFeedbackMetric `json:"receipts"`
	Leads                  OutcomeFeedbackMetric `json:"leads"`
	QualifiedOpportunities OutcomeFeedbackMetric `json:"qualified_opportunities"`
	Proposals              OutcomeFeedbackMetric `json:"proposals"`
	Contracts              OutcomeFeedbackMetric `json:"contracts"`
	Outcomes               OutcomeFeedbackStates `json:"outcomes"`
	KnownValue             OutcomeFeedbackValue  `json:"known_value"`
	KnownMargin            OutcomeFeedbackMargin `json:"known_margin"`
	Suppressed             bool                  `json:"suppressed"`
}

type OutcomeFeedbackGap struct {
	Field  string `json:"field"`
	Status string `json:"status"`
	Owner  string `json:"owner"`
	Reason string `json:"reason"`
}

type OutcomeFeedbackPrivacy struct {
	PIIExported              bool `json:"pii_exported"`
	MinimumCellSize          int  `json:"minimum_cell_size"`
	DirectCells              int  `json:"direct_cells"`
	RolledUpSourceCells      int  `json:"rolled_up_source_cells"`
	WithheldRollup           bool `json:"withheld_rollup"`
	SuppressionApplied       bool `json:"suppression_applied"`
	SensitiveMetricsWithheld bool `json:"sensitive_metrics_withheld"`
}

// AcquisitionOutcomeFeedback is a read-only, PII-free read model. It never
// writes web-cfg or changes commercial/outbound state.
type AcquisitionOutcomeFeedback struct {
	SchemaVersion             string                    `json:"schema_version"`
	GeneratedAt               string                    `json:"generated_at"`
	Period                    OutcomeFeedbackPeriodView `json:"period"`
	IncludeSynthetic          bool                      `json:"include_synthetic"`
	RouteAttributionAvailable string                    `json:"route_attribution_available"`
	Privacy                   OutcomeFeedbackPrivacy    `json:"privacy"`
	Rows                      []OutcomeFeedbackRow      `json:"rows"`
	JoinGaps                  []OutcomeFeedbackGap      `json:"join_gaps"`
	CausalProof               bool                      `json:"causal_proof"`
	RealEmpty                 bool                      `json:"real_empty"`
}

type outcomeFeedbackGroup struct {
	key    string
	dims   OutcomeFeedbackRow
	chains []Chain
}

// ProjectAcquisitionOutcomeFeedback groups persisted inbound acquisition
// chains, then rolls all direct cells below five privacy units into one
// dimensionless cell. If the roll-up is still below five, every count/value is
// withheld.
func ProjectAcquisitionOutcomeFeedback(chains []Chain, period OutcomeFeedbackPeriod, now time.Time, includeSynthetic bool) AcquisitionOutcomeFeedback {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	period = canonicalOutcomeFeedbackPeriod(period, now)
	out := AcquisitionOutcomeFeedback{
		SchemaVersion: OutcomeFeedbackSchemaV1,
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		Period: OutcomeFeedbackPeriodView{
			From: period.From.UTC().Format(time.RFC3339), To: period.To.UTC().Format(time.RFC3339), ToExclusive: true,
		},
		IncludeSynthetic:          includeSynthetic,
		RouteAttributionAvailable: RouteAttributionPartial,
		Privacy:                   OutcomeFeedbackPrivacy{PIIExported: false, MinimumCellSize: OutcomeFeedbackMinCellN},
		Rows:                      []OutcomeFeedbackRow{},
		JoinGaps: []OutcomeFeedbackGap{
			{Field: "acquisition_route", Status: FeedbackStatusUnjoinable, Owner: "web-cfg", Reason: "raw landing paths have no versioned closed route registry on the inbound contract"},
			{Field: "asset_id", Status: FeedbackStatusUnjoinable, Owner: "web-cfg", Reason: "asset IDs have no versioned closed aggregate taxonomy on the inbound contract"},
			{Field: "cta_id", Status: FeedbackStatusUnjoinable, Owner: "web-cfg", Reason: "CTA IDs have no versioned closed aggregate taxonomy on the inbound contract"},
			{Field: "intent_class", Status: FeedbackStatusUnjoinable, Owner: "web-cfg", Reason: "intent classes have no versioned closed aggregate taxonomy on the inbound contract"},
			{Field: "qualified_opportunity", Status: FeedbackStatusPartial, Owner: "warmbly", Reason: "a bare lead_validated flag is not a QCO; only explicit QUALIFIED_CONVERSATION with an opportunity ID is counted"},
			{Field: "buyer_job", Status: FeedbackStatusUnjoinable, Owner: "web-cfg", Reason: "no versioned buyer_job field is persisted on the inbound contract"},
			{Field: "proposal", Status: FeedbackStatusPartial, Owner: "warmbly", Reason: "only proposal facts already projected onto the commercial chain are visible; native confenge_proposals are not joined by this read model"},
			{Field: "contract", Status: FeedbackStatusUnjoinable, Owner: "warmbly", Reason: "no dedicated evidence-backed contract state is persisted; human-confirmed WON/CLIENT remains an outcome, not a contract"},
			{Field: "margin", Status: FeedbackStatusUnknown, Owner: "warmbly", Reason: "no evidence-backed margin fact is persisted"},
		},
		CausalProof: false,
	}
	groups := map[string]*outcomeFeedbackGroup{}
	for _, chain := range chains {
		if !includeSynthetic && feedbackRecordKind(chain) == RecordKindSynthetic {
			continue
		}
		if !isInboundAcquisitionChain(chain) || !feedbackChainInPeriod(chain, period) {
			continue
		}
		dims := feedbackDimensions(chain)
		key := strings.Join([]string{
			dims.AcquisitionSource, dims.OrganicSource, dims.RouteFamily,
			dims.AcquisitionRoute, dims.AssetFamily, dims.AssetID, dims.CTAID, dims.IntentClass,
			dims.CohortMonth, dims.RecordKind,
		}, "|")
		group := groups[key]
		if group == nil {
			group = &outcomeFeedbackGroup{key: key, dims: dims}
			groups[key] = group
		}
		group.chains = append(group.chains, chain)
	}
	if len(groups) == 0 {
		out.RealEmpty = true
		return out
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	small := map[string][]Chain{}
	for _, key := range keys {
		group := groups[key]
		if feedbackPrivacyUnitCount(group.chains) < OutcomeFeedbackMinCellN {
			out.Privacy.RolledUpSourceCells++
			rollupKey := group.dims.CohortMonth + "|" + group.dims.RecordKind
			small[rollupKey] = append(small[rollupKey], group.chains...)
			continue
		}
		row := projectOutcomeFeedbackRow(group.dims, group.chains)
		out.Rows = append(out.Rows, row)
		out.Privacy.SensitiveMetricsWithheld = out.Privacy.SensitiveMetricsWithheld || feedbackRowWithholdsSensitive(row)
		out.Privacy.DirectCells++
	}
	if len(small) > 0 {
		out.Privacy.SuppressionApplied = true
		rollupKeys := make([]string, 0, len(small))
		for key := range small {
			rollupKeys = append(rollupKeys, key)
		}
		sort.Strings(rollupKeys)
		for _, key := range rollupKeys {
			cohortMonth, recordKind, _ := strings.Cut(key, "|")
			rollupChains := small[key]
			if feedbackPrivacyUnitCount(rollupChains) < OutcomeFeedbackMinCellN {
				out.Rows = append(out.Rows, withheldOutcomeFeedbackRow(cohortMonth, recordKind))
				out.Privacy.WithheldRollup = true
				continue
			}
			dims := unknownFeedbackDimensions(cohortMonth, recordKind)
			dims.Cohort = FeedbackCohortRollup
			row := projectOutcomeFeedbackRow(dims, rollupChains)
			out.Rows = append(out.Rows, row)
			out.Privacy.SensitiveMetricsWithheld = out.Privacy.SensitiveMetricsWithheld || feedbackRowWithholdsSensitive(row)
		}
	}
	return out
}

func isInboundAcquisitionChain(c Chain) bool {
	if normalizeFamily(firstKnownFeedback(c.RouteFamily, c.Keys.RouteFamily)) == FamilyInbound {
		return true
	}
	return allowedSearchProducer(firstKnownFeedback(c.Source, c.Keys.Source)) &&
		(firstKnownFeedback(c.ReceiptID, c.Keys.ReceiptID, c.LeadID, c.Keys.LeadID) != "")
}

func feedbackChainInPeriod(c Chain, period OutcomeFeedbackPeriod) bool {
	at := c.LeadCreatedAt
	if at.IsZero() {
		at = c.CreatedAt
	}
	return !at.IsZero() && !at.Before(period.From) && at.Before(period.To)
}

func feedbackDimensions(c Chain) OutcomeFeedbackRow {
	source := Unknown
	if allowedSearchProducer(firstKnownFeedback(c.Source, c.Keys.Source)) {
		source = ProducerCONFENGEWeb
	}
	organic := NormalizeOrganicSource(c.Keys.OrganicSource)
	if organic == "" {
		organic = Unknown
	}
	route := Unknown
	assetFamily := Unknown
	if rawFamily := firstKnownFeedback(c.AssetFamily, c.Keys.AssetFamily); knownAssetFamily(rawFamily) && rawFamily != "" {
		assetFamily = normalizeAssetFamily(rawFamily)
	}
	assetID := Unknown
	ctaID := Unknown
	intent := Unknown
	at := c.LeadCreatedAt
	if at.IsZero() {
		at = c.CreatedAt
	}
	recordKind := feedbackRecordKind(c)
	dims := OutcomeFeedbackRow{
		Cohort: FeedbackCohortDirect, AcquisitionSource: source, OrganicSource: organic,
		RouteFamily:      normalizeFamily(firstKnownFeedback(c.RouteFamily, c.Keys.RouteFamily)),
		AcquisitionRoute: route, AssetFamily: assetFamily, AssetID: assetID, CTAID: ctaID,
		IntentClass: intent, BuyerJob: Unknown, CohortMonth: monthStart(at).Format("2006-01"), RecordKind: recordKind,
	}
	hasRoute := assetFamily != Unknown
	switch {
	case source != Unknown && hasRoute && intent != Unknown:
		dims.JoinStatus = FeedbackJoinJoined
	case source != Unknown || hasRoute || intent != Unknown:
		dims.JoinStatus = FeedbackJoinPartial
	default:
		dims.JoinStatus = FeedbackJoinUnjoinable
	}
	return dims
}

func unknownFeedbackDimensions(cohortMonth, recordKind string) OutcomeFeedbackRow {
	return OutcomeFeedbackRow{
		Cohort: FeedbackCohortRollup, AcquisitionSource: Unknown, OrganicSource: Unknown,
		RouteFamily: Unknown, AcquisitionRoute: Unknown, AssetFamily: Unknown,
		AssetID: Unknown, CTAID: Unknown, IntentClass: Unknown, BuyerJob: Unknown,
		CohortMonth: cohortMonth, RecordKind: recordKind,
		JoinStatus: FeedbackJoinPartial, Suppressed: true,
	}
}

func feedbackPrivacyUnit(c Chain) string {
	if accountID := firstKnownFeedback(c.AccountID, c.Keys.AccountID); accountID != "" {
		return "account:" + accountID
	}
	// Person/receipt/lead identity does not prove an independent account and
	// therefore contributes nothing to the small-cell threshold.
	return ""
}

func feedbackRecordKind(c Chain) string {
	if c.Synthetic || c.Label == LabelSynthetic {
		return RecordKindSynthetic
	}
	raw := strings.TrimSpace(c.Keys.RecordKind)
	if raw == "" {
		return RecordKindReal
	}
	if NormalizeRecordKind(raw, c.Synthetic) != RecordKindReal {
		return RecordKindSynthetic
	}
	return RecordKindReal
}

func feedbackPrivacyUnitCount(chains []Chain) int {
	return len(feedbackUnitSet(chains))
}

func feedbackUnitSet(chains []Chain) map[string]struct{} {
	set := map[string]struct{}{}
	for _, c := range chains {
		if unit := feedbackPrivacyUnit(c); unit != "" {
			set[unit] = struct{}{}
		}
	}
	return set
}

func distinctFeedbackField(chains []Chain, pick func(Chain) string) int {
	set := map[string]struct{}{}
	for _, c := range chains {
		if v := strings.TrimSpace(pick(c)); v != "" {
			set[v] = struct{}{}
		}
	}
	return len(set)
}

func projectOutcomeFeedbackRow(dims OutcomeFeedbackRow, chains []Chain) OutcomeFeedbackRow {
	row := dims
	units := feedbackPrivacyUnitCount(chains)
	row.PrivacyUnits = intPtr(units)
	receipts := distinctFeedbackField(chains, func(c Chain) string { return firstKnownFeedback(c.ReceiptID, c.Keys.ReceiptID) })
	leads := distinctFeedbackField(chains, func(c Chain) string { return firstKnownFeedback(c.LeadID, c.Keys.LeadID) })
	qco := countFeedbackUnits(chains, feedbackHasQCO, feedbackQCOUnit)
	qcoContributors := countFeedbackUnits(chains, feedbackHasQCO, feedbackPrivacyUnit)
	proposals := countFeedbackUnits(chains, feedbackHasProposal, feedbackProposalUnit)
	proposalContributors := countFeedbackUnits(chains, feedbackHasProposal, feedbackPrivacyUnit)
	won := countFeedbackUnits(chains, func(c Chain) bool { return c.HumanConfirmed && isWonType(c.OutcomeType) }, feedbackOutcomeUnit)
	wonContributors := countFeedbackUnits(chains, func(c Chain) bool { return c.HumanConfirmed && isWonType(c.OutcomeType) }, feedbackPrivacyUnit)
	lost := countFeedbackUnits(chains, func(c Chain) bool { return c.HumanConfirmed && isLostType(c.OutcomeType) }, feedbackOutcomeUnit)
	lostContributors := countFeedbackUnits(chains, func(c Chain) bool { return c.HumanConfirmed && isLostType(c.OutcomeType) }, feedbackPrivacyUnit)
	row.Receipts = observedFeedbackMetric(receipts, units-receipts)
	row.Leads = observedFeedbackMetric(leads, units-leads)
	row.QualifiedOpportunities = sensitiveFeedbackMetric(qco, units-qcoContributors, qcoContributors)
	row.Proposals = sensitiveFeedbackMetric(proposals, units-proposalContributors, proposalContributors)
	row.Contracts = OutcomeFeedbackMetric{Observed: nil, Unknown: intPtr(units), Status: FeedbackStatusUnjoinable}
	row.Outcomes = OutcomeFeedbackStates{Won: positiveFeedbackCount(won), Lost: positiveFeedbackCount(lost), Unknown: intPtr(maxInt(0, units-won-lost))}
	if insufficientSensitiveContributors(won, wonContributors) || insufficientSensitiveContributors(lost, lostContributors) {
		row.Outcomes = OutcomeFeedbackStates{Status: FeedbackStatusWithheld}
	} else if won+lost == 0 {
		row.Outcomes.Status = FeedbackStatusUnknown
	} else if won+lost < units {
		row.Outcomes.Status = FeedbackStatusPartial
	} else {
		row.Outcomes.Status = FeedbackStatusObserved
	}
	row.KnownValue = feedbackKnownValue(chains, units)
	row.KnownMargin = OutcomeFeedbackMargin{
		KnownCents: nil, KnownRecords: intPtr(0), UnknownRecords: intPtr(units), Status: FeedbackStatusUnknown,
	}
	return row
}

func observedFeedbackMetric(observed, unknown int) OutcomeFeedbackMetric {
	status := FeedbackStatusObserved
	if observed == 0 {
		return OutcomeFeedbackMetric{Observed: nil, Unknown: intPtr(maxInt(unknown, 0)), Status: FeedbackStatusUnknown}
	} else if unknown > 0 {
		status = FeedbackStatusPartial
	}
	return OutcomeFeedbackMetric{Observed: intPtr(observed), Unknown: intPtr(maxInt(unknown, 0)), Status: status}
}

func sensitiveFeedbackMetric(observed, unknown, contributors int) OutcomeFeedbackMetric {
	if insufficientSensitiveContributors(observed, contributors) {
		return withheldFeedbackMetric()
	}
	return observedFeedbackMetric(observed, unknown)
}

func insufficientSensitiveContributors(observed, contributors int) bool {
	return observed > 0 && contributors < OutcomeFeedbackMinCellN
}

func countFeedbackUnits(chains []Chain, match func(Chain) bool, keyFor func(Chain) string) int {
	set := map[string]struct{}{}
	for _, c := range chains {
		if !match(c) {
			continue
		}
		if key := keyFor(c); key != "" {
			set[key] = struct{}{}
		}
	}
	return len(set)
}

func feedbackQCOUnit(c Chain) string {
	if feedbackPrivacyUnit(c) == "" {
		return ""
	}
	if id := firstKnownFeedback(c.OpportunityID, c.Keys.OpportunityID); id != "" {
		return "opportunity:" + id
	}
	return feedbackPrivacyUnit(c)
}

func feedbackProposalUnit(c Chain) string {
	if feedbackPrivacyUnit(c) == "" {
		return ""
	}
	if id := firstKnownFeedback(c.ProposalID, c.Keys.ProposalID); id != "" {
		return "proposal:" + id
	}
	return feedbackQCOUnit(c)
}

func feedbackOutcomeUnit(c Chain) string {
	if feedbackPrivacyUnit(c) == "" {
		return ""
	}
	if id := firstKnownFeedback(c.OutcomeID, c.Keys.OutcomeID); id != "" {
		return "outcome:" + id
	}
	if id := firstKnownFeedback(c.OpportunityID, c.Keys.OpportunityID); id != "" {
		return "opportunity:" + id
	}
	return feedbackProposalUnit(c)
}

func feedbackHasProposal(c Chain) bool {
	return firstKnownFeedback(c.ProposalID, c.Keys.ProposalID) != ""
}

func feedbackKnownValue(chains []Chain, units int) OutcomeFeedbackValue {
	contractedValues := map[string]int64{}
	receivedValues := map[string]int64{}
	contractedContributors := map[string]struct{}{}
	receivedContributors := map[string]struct{}{}
	contributors := map[string]struct{}{}
	for _, c := range chains {
		privacyUnit := feedbackPrivacyUnit(c)
		if privacyUnit == "" {
			continue
		}
		if c.Commercial.Payment.ContractedCents > contractedValues[privacyUnit] {
			contractedValues[privacyUnit] = c.Commercial.Payment.ContractedCents
		}
		if c.Commercial.Payment.ContractedCents > 0 {
			contractedContributors[privacyUnit] = struct{}{}
			contributors[privacyUnit] = struct{}{}
		}
		receivedCents := c.Commercial.Payment.ReceivedCents
		if receivedCents == 0 && c.RevenueEvidenced && c.RevenueCents > 0 {
			receivedCents = c.RevenueCents
		}
		receivedKey := feedbackReceivedValueUnit(c)
		if receivedCents > receivedValues[receivedKey] {
			receivedValues[receivedKey] = receivedCents
		}
		if receivedCents > 0 {
			receivedContributors[privacyUnit] = struct{}{}
			contributors[feedbackPrivacyUnit(c)] = struct{}{}
		}
	}
	var contracted, received int64
	for _, value := range contractedValues {
		contracted += value
	}
	for _, value := range receivedValues {
		received += value
	}
	known := len(contributors)
	unknown := maxInt(0, units-known)
	out := OutcomeFeedbackValue{KnownRecords: intPtr(known), UnknownRecords: intPtr(unknown), Status: FeedbackStatusUnknown}
	if known == 0 {
		return out
	}
	if (contracted > 0 && len(contractedContributors) < OutcomeFeedbackMinCellN) ||
		(received > 0 && len(receivedContributors) < OutcomeFeedbackMinCellN) {
		return OutcomeFeedbackValue{Status: FeedbackStatusWithheld}
	}
	if len(contractedContributors) > 0 {
		out.ContractedCents = feedbackInt64Ptr(contracted)
	}
	if len(receivedContributors) > 0 {
		out.ReceivedCents = feedbackInt64Ptr(received)
	}
	if unknown > 0 {
		out.Status = FeedbackStatusPartial
	} else {
		out.Status = FeedbackStatusObserved
	}
	return out
}

func feedbackReceivedValueUnit(c Chain) string {
	for _, v := range []string{
		c.PaymentID, c.Keys.PaymentID, c.ChargeID, c.Keys.ChargeID,
	} {
		if id := feedbackKnownID(v); id != "" {
			return "payment:" + id
		}
	}
	return feedbackPrivacyUnit(c)
}

func withheldOutcomeFeedbackRow(cohortMonth, recordKind string) OutcomeFeedbackRow {
	row := unknownFeedbackDimensions(cohortMonth, recordKind)
	row.Cohort = FeedbackCohortWithheld
	row.JoinStatus = FeedbackStatusWithheld
	row.PrivacyUnits = nil
	row.Receipts = withheldFeedbackMetric()
	row.Leads = withheldFeedbackMetric()
	row.QualifiedOpportunities = withheldFeedbackMetric()
	row.Proposals = withheldFeedbackMetric()
	row.Contracts = withheldFeedbackMetric()
	row.Outcomes = OutcomeFeedbackStates{Status: FeedbackStatusWithheld}
	row.KnownValue = OutcomeFeedbackValue{Status: FeedbackStatusWithheld}
	row.KnownMargin = OutcomeFeedbackMargin{Status: FeedbackStatusWithheld}
	return row
}

func withheldFeedbackMetric() OutcomeFeedbackMetric {
	return OutcomeFeedbackMetric{Status: FeedbackStatusWithheld}
}

func feedbackInt64Ptr(v int64) *int64 { return &v }

func positiveFeedbackCount(v int) *int {
	if v == 0 {
		return nil
	}
	return intPtr(v)
}

func feedbackRowWithholdsSensitive(row OutcomeFeedbackRow) bool {
	return row.QualifiedOpportunities.Status == FeedbackStatusWithheld ||
		row.Proposals.Status == FeedbackStatusWithheld ||
		row.Outcomes.Status == FeedbackStatusWithheld ||
		row.KnownValue.Status == FeedbackStatusWithheld
}

func feedbackHasQCO(c Chain) bool {
	return strings.EqualFold(strings.TrimSpace(c.OutcomeType), OutcomeQualifiedConversation) &&
		firstKnownFeedback(c.OpportunityID, c.Keys.OpportunityID) != ""
}

func firstKnownFeedback(values ...string) string {
	for _, value := range values {
		if id := feedbackKnownID(value); id != "" {
			return id
		}
	}
	return ""
}

func feedbackKnownID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, Unknown) {
		return ""
	}
	return value
}
