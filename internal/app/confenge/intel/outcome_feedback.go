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
	Won           *int   `json:"won"`
	WonStatus     string `json:"won_status"`
	Lost          *int   `json:"lost"`
	LostStatus    string `json:"lost_status"`
	Unknown       *int   `json:"unknown"`
	UnknownStatus string `json:"unknown_status"`
	Status        string `json:"status"`
}

type OutcomeFeedbackKnownCurrencyValue struct {
	Currency         string `json:"currency"`
	ContractedCents  *int64 `json:"contracted_cents"`
	ContractedStatus string `json:"contracted_status"`
	ReceivedCents    *int64 `json:"received_cents"`
	ReceivedStatus   string `json:"received_status"`
}

// OutcomeFeedbackValue contains only currency-separated values legitimately
// present on the commercial chain. It is an observed lower bound.
type OutcomeFeedbackValue struct {
	ByCurrency        []OutcomeFeedbackKnownCurrencyValue `json:"by_currency"`
	KnownRecords      *int                                `json:"known_records"`
	UnknownRecords    *int                                `json:"unknown_records"`
	Status            string                              `json:"status"`
	WithheldSmallCell bool                                `json:"withheld_small_cell"`
}

type OutcomeFeedbackCurrencyValue struct {
	Currency      string `json:"currency"`
	AmountMinor   int64  `json:"amount_minor"`
	ProposalCount int    `json:"proposal_count"`
}

type OutcomeFeedbackProposalState struct {
	State    string `json:"state"`
	Observed int    `json:"observed"`
}

type OutcomeFeedbackProposalStates struct {
	States            []OutcomeFeedbackProposalState `json:"states"`
	KnownRecords      *int                           `json:"known_records"`
	UnknownRecords    *int                           `json:"unknown_records"`
	Status            string                         `json:"status"`
	WithheldSmallCell bool                           `json:"withheld_small_cell"`
}

// OutcomeFeedbackProposedValue never feeds contracted or received value. It
// reports only observed native proposal snapshots, in minor currency units.
type OutcomeFeedbackProposedValue struct {
	ByCurrency        []OutcomeFeedbackCurrencyValue `json:"by_currency"`
	KnownRecords      *int                           `json:"known_records"`
	UnknownRecords    *int                           `json:"unknown_records"`
	Status            string                         `json:"status"`
	WithheldSmallCell bool                           `json:"withheld_small_cell"`
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
	Cohort                 string                        `json:"cohort"`
	AcquisitionSource      string                        `json:"acquisition_source"`
	OrganicSource          string                        `json:"organic_source"`
	RouteFamily            string                        `json:"route_family"`
	AcquisitionRoute       string                        `json:"acquisition_route"`
	AssetFamily            string                        `json:"asset_family"`
	AssetID                string                        `json:"asset_id"`
	CTAID                  string                        `json:"cta_id"`
	IntentClass            string                        `json:"intent_class"`
	BuyerJob               string                        `json:"buyer_job"`
	CohortMonth            string                        `json:"cohort_month"`
	RecordKind             string                        `json:"record_kind"`
	JoinStatus             string                        `json:"join_status"`
	PrivacyUnits           *int                          `json:"privacy_units"`
	Receipts               OutcomeFeedbackMetric         `json:"receipts"`
	Leads                  OutcomeFeedbackMetric         `json:"leads"`
	QualifiedOpportunities OutcomeFeedbackMetric         `json:"qualified_opportunities"`
	Proposals              OutcomeFeedbackMetric         `json:"proposals"`
	ProposalStates         OutcomeFeedbackProposalStates `json:"proposal_states"`
	ProposedValue          OutcomeFeedbackProposedValue  `json:"proposed_value"`
	Contracts              OutcomeFeedbackMetric         `json:"contracts"`
	Outcomes               OutcomeFeedbackStates         `json:"outcomes"`
	KnownValue             OutcomeFeedbackValue          `json:"known_value"`
	KnownMargin            OutcomeFeedbackMargin         `json:"known_margin"`
	Suppressed             bool                          `json:"suppressed"`
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
	key             string
	dims            OutcomeFeedbackRow
	chains          []Chain
	nativeProposals []NativeProposalFact
}

// NativeProposalFact is the PII-free in-process projection of a persisted
// confenge_proposals version. Join IDs are consumed only for exact matching and
// are never copied into the response.
type NativeProposalFact struct {
	ProposalID           string
	ProposalVersion      int
	AccountID            string
	OpportunityID        string
	SourceLeadID         string
	CorrelationID        string
	DecisionState        string
	AmountMinor          int64
	Currency             string
	SentAt               *time.Time
	Synthetic            bool
	AcceptedSnapshotHash string
}

// ProjectAcquisitionOutcomeFeedback groups persisted inbound acquisition
// chains, then rolls all direct cells below five privacy units into one
// dimensionless cell. If the roll-up is still below five, every count/value is
// withheld.
func ProjectAcquisitionOutcomeFeedback(chains []Chain, period OutcomeFeedbackPeriod, now time.Time, includeSynthetic bool) AcquisitionOutcomeFeedback {
	return ProjectAcquisitionOutcomeFeedbackWithNativeProposals(chains, nil, period, now, includeSynthetic)
}

// ProjectAcquisitionOutcomeFeedbackWithNativeProposals augments existing
// commercial chains with native proposal versions only when opaque,
// organization-scoped facts identify exactly one acquisition chain.
func ProjectAcquisitionOutcomeFeedbackWithNativeProposals(chains []Chain, nativeProposals []NativeProposalFact, period OutcomeFeedbackPeriod, now time.Time, includeSynthetic bool) AcquisitionOutcomeFeedback {
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
			{Field: "proposal", Status: FeedbackStatusPartial, Owner: "warmbly", Reason: "chain proposals plus native confenge_proposals with exact safe correlation/account/opportunity joins are visible; drafts, ambiguous, conflicting, held, or unjoinable native rows remain unknown"},
			{Field: "contract", Status: FeedbackStatusUnjoinable, Owner: "warmbly", Reason: "no dedicated evidence-backed contract state is persisted; human-confirmed WON/CLIENT remains an outcome, not a contract"},
			{Field: "margin", Status: FeedbackStatusUnknown, Owner: "warmbly", Reason: "no evidence-backed margin fact is persisted"},
		},
		CausalProof: false,
	}
	groups := map[string]*outcomeFeedbackGroup{}
	eligibleChains := make([]Chain, 0, len(chains))
	hasReal := false
	for _, chain := range chains {
		if !includeSynthetic && feedbackRecordKind(chain) == RecordKindSynthetic {
			continue
		}
		if !isInboundAcquisitionChain(chain) || !feedbackChainInPeriod(chain, period) {
			continue
		}
		eligibleChains = append(eligibleChains, chain)
		if feedbackRecordKind(chain) == RecordKindReal {
			hasReal = true
		}
		dims := feedbackDimensions(chain)
		key := feedbackDimensionKey(dims)
		group := groups[key]
		if group == nil {
			group = &outcomeFeedbackGroup{key: key, dims: dims}
			groups[key] = group
		}
		group.chains = append(group.chains, chain)
	}
	nativeChainIndex := indexNativeProposalChains(eligibleChains)
	for _, fact := range latestEmittedNativeProposals(nativeProposals) {
		chain, ok := matchNativeProposalChainIndex(fact, nativeChainIndex)
		if !ok {
			continue
		}
		dims := feedbackDimensions(chain)
		key := feedbackDimensionKey(dims)
		if group := groups[key]; group != nil {
			group.nativeProposals = append(group.nativeProposals, fact)
		}
	}
	if len(groups) == 0 {
		out.RealEmpty = true
		return out
	}
	out.RealEmpty = !hasReal
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	small := map[string]*outcomeFeedbackGroup{}
	for _, key := range keys {
		group := groups[key]
		if feedbackPrivacyUnitCount(group.chains) < OutcomeFeedbackMinCellN {
			out.Privacy.RolledUpSourceCells++
			rollupKey := group.dims.CohortMonth + "|" + group.dims.RecordKind
			rollup := small[rollupKey]
			if rollup == nil {
				rollup = &outcomeFeedbackGroup{}
				small[rollupKey] = rollup
			}
			rollup.chains = append(rollup.chains, group.chains...)
			rollup.nativeProposals = append(rollup.nativeProposals, group.nativeProposals...)
			continue
		}
		row := projectOutcomeFeedbackRow(group.dims, group.chains, group.nativeProposals)
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
			rollup := small[key]
			rollupChains := rollup.chains
			if feedbackPrivacyUnitCount(rollupChains) < OutcomeFeedbackMinCellN {
				out.Rows = append(out.Rows, withheldOutcomeFeedbackRow(cohortMonth, recordKind))
				out.Privacy.WithheldRollup = true
				continue
			}
			dims := unknownFeedbackDimensions(cohortMonth, recordKind)
			dims.Cohort = FeedbackCohortRollup
			row := projectOutcomeFeedbackRow(dims, rollupChains, rollup.nativeProposals)
			out.Rows = append(out.Rows, row)
			out.Privacy.SensitiveMetricsWithheld = out.Privacy.SensitiveMetricsWithheld || feedbackRowWithholdsSensitive(row)
		}
	}
	return out
}

func feedbackDimensionKey(dims OutcomeFeedbackRow) string {
	return strings.Join([]string{
		dims.AcquisitionSource, dims.OrganicSource, dims.RouteFamily,
		dims.AcquisitionRoute, dims.AssetFamily, dims.AssetID, dims.CTAID, dims.IntentClass,
		dims.CohortMonth, dims.RecordKind,
	}, "|")
}

func isInboundAcquisitionChain(c Chain) bool {
	return normalizeFamily(firstKnownFeedback(c.RouteFamily, c.Keys.RouteFamily)) == FamilyInbound &&
		allowedSearchProducer(firstKnownFeedback(c.Source, c.Keys.Source)) &&
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

func latestEmittedNativeProposals(facts []NativeProposalFact) []NativeProposalFact {
	latest := map[string]NativeProposalFact{}
	conflicted := map[string]bool{}
	for _, fact := range facts {
		if !validNativeProposalFact(fact) {
			continue
		}
		id := strings.TrimSpace(fact.ProposalID)
		current, ok := latest[id]
		switch {
		case !ok || fact.ProposalVersion > current.ProposalVersion:
			latest[id] = fact
			conflicted[id] = false
		case fact.ProposalVersion == current.ProposalVersion && nativeProposalFingerprint(fact) != nativeProposalFingerprint(current):
			conflicted[id] = true
		}
	}
	ids := make([]string, 0, len(latest))
	for id := range latest {
		if !conflicted[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	proposalByCorrelation := map[string]string{}
	ambiguousCorrelations := map[string]bool{}
	for _, id := range ids {
		correlationID := strings.TrimSpace(latest[id].CorrelationID)
		if existingID, ok := proposalByCorrelation[correlationID]; ok && existingID != id {
			ambiguousCorrelations[correlationID] = true
			continue
		}
		proposalByCorrelation[correlationID] = id
	}
	out := make([]NativeProposalFact, 0, len(ids))
	for _, id := range ids {
		if ambiguousCorrelations[strings.TrimSpace(latest[id].CorrelationID)] {
			continue
		}
		out = append(out, latest[id])
	}
	return out
}

func validNativeProposalFact(fact NativeProposalFact) bool {
	if fact.ProposalVersion < 1 || fact.SentAt == nil || fact.SentAt.IsZero() || fact.AmountMinor < 0 {
		return false
	}
	for name, value := range map[string]string{
		"proposal_id": fact.ProposalID, "account_id": fact.AccountID,
		"opportunity_id": fact.OpportunityID, "correlation_id": fact.CorrelationID,
	} {
		if feedbackKnownID(value) == "" || validateOpaqueIdentifier(name, value) != nil {
			return false
		}
	}
	if id := strings.TrimSpace(fact.SourceLeadID); id != "" && validateOpaqueIdentifier("source_lead_id", id) != nil {
		return false
	}
	switch strings.ToUpper(strings.TrimSpace(fact.DecisionState)) {
	case "SENT", "NEGOTIATING", "REJECTED", "EXPIRED", Unknown:
		return true
	case "ACCEPTED":
		hash := strings.TrimSpace(fact.AcceptedSnapshotHash)
		if !strings.HasPrefix(hash, "sha256:") || len(hash) != len("sha256:")+64 {
			return false
		}
		for _, char := range strings.TrimPrefix(hash, "sha256:") {
			if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func nativeProposalFingerprint(fact NativeProposalFact) string {
	sentAt := ""
	if fact.SentAt != nil {
		sentAt = fact.SentAt.UTC().Format(time.RFC3339Nano)
	}
	return strings.Join([]string{
		strings.TrimSpace(fact.AccountID), strings.TrimSpace(fact.OpportunityID),
		strings.TrimSpace(fact.SourceLeadID), strings.TrimSpace(fact.CorrelationID),
		strings.ToUpper(strings.TrimSpace(fact.DecisionState)), fmt.Sprintf("%d", fact.AmountMinor),
		strings.ToUpper(strings.TrimSpace(fact.Currency)), sentAt, fmt.Sprintf("%t", fact.Synthetic),
		strings.TrimSpace(fact.AcceptedSnapshotHash),
	}, "|")
}

type nativeProposalChainCandidate struct {
	chain     Chain
	identity  string
	ambiguous bool
}

func indexNativeProposalChains(chains []Chain) map[string]nativeProposalChainCandidate {
	index := make(map[string]nativeProposalChainCandidate, len(chains))
	for i := range chains {
		chain := chains[i]
		if chain.Held {
			continue
		}
		key := nativeProposalChainKey(
			feedbackRecordKind(chain), firstKnownFeedback(chain.CorrelationID, chain.Keys.CorrelationID),
			firstKnownFeedback(chain.AccountID, chain.Keys.AccountID),
			firstKnownFeedback(chain.OpportunityID, chain.Keys.OpportunityID),
		)
		if key == "" {
			continue
		}
		identity := strings.TrimSpace(chain.Identity)
		if identity == "" {
			identity = ChainIdentity(chain.Keys)
		}
		candidate, exists := index[key]
		if exists && candidate.identity != identity {
			candidate.ambiguous = true
			index[key] = candidate
			continue
		}
		if !exists {
			index[key] = nativeProposalChainCandidate{chain: chain, identity: identity}
		}
	}
	return index
}

func nativeProposalChainKey(recordKind, correlationID, accountID, opportunityID string) string {
	correlationID = feedbackKnownID(correlationID)
	accountID = feedbackKnownID(accountID)
	opportunityID = feedbackKnownID(opportunityID)
	if correlationID == "" || accountID == "" || opportunityID == "" {
		return ""
	}
	return strings.Join([]string{recordKind, correlationID, accountID, opportunityID}, "\x00")
}

func matchNativeProposalChainIndex(fact NativeProposalFact, index map[string]nativeProposalChainCandidate) (Chain, bool) {
	key := nativeProposalChainKey(nativeProposalRecordKind(fact), fact.CorrelationID, fact.AccountID, fact.OpportunityID)
	candidate, ok := index[key]
	if !ok || candidate.ambiguous {
		return Chain{}, false
	}
	chain := candidate.chain
	if sourceLeadID := strings.TrimSpace(fact.SourceLeadID); sourceLeadID != "" && !nativeProposalSourceMatches(sourceLeadID, chain) {
		return Chain{}, false
	}
	if chainProposalID := firstKnownFeedback(chain.ProposalID, chain.Keys.ProposalID); chainProposalID != "" && chainProposalID != strings.TrimSpace(fact.ProposalID) {
		return Chain{}, false
	}
	return chain, true
}

func matchNativeProposalChain(fact NativeProposalFact, chains []Chain) (Chain, bool) {
	return matchNativeProposalChainIndex(fact, indexNativeProposalChains(chains))
}

func nativeProposalRecordKind(fact NativeProposalFact) string {
	if fact.Synthetic {
		return RecordKindSynthetic
	}
	return RecordKindReal
}

func nativeProposalSourceMatches(sourceLeadID string, chain Chain) bool {
	for _, value := range []string{chain.LeadID, chain.Keys.LeadID} {
		if firstKnownFeedback(value) == sourceLeadID {
			return true
		}
	}
	return false
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

func projectOutcomeFeedbackRow(dims OutcomeFeedbackRow, chains []Chain, nativeProposals []NativeProposalFact) OutcomeFeedbackRow {
	row := dims
	units := feedbackPrivacyUnitCount(chains)
	row.PrivacyUnits = intPtr(units)
	receipts := distinctFeedbackField(chains, func(c Chain) string { return firstKnownFeedback(c.ReceiptID, c.Keys.ReceiptID) })
	receiptContributors := countFeedbackUnits(chains, func(c Chain) bool {
		return firstKnownFeedback(c.ReceiptID, c.Keys.ReceiptID) != ""
	}, feedbackPrivacyUnit)
	leads := distinctFeedbackField(chains, func(c Chain) string { return firstKnownFeedback(c.LeadID, c.Keys.LeadID) })
	leadContributors := countFeedbackUnits(chains, func(c Chain) bool {
		return firstKnownFeedback(c.LeadID, c.Keys.LeadID) != ""
	}, feedbackPrivacyUnit)
	qco := countFeedbackUnits(chains, feedbackHasQCO, feedbackQCOUnit)
	qcoContributors := countFeedbackUnits(chains, feedbackHasQCO, feedbackPrivacyUnit)
	proposals, proposalContributors, proposalIDs := feedbackProposalStats(chains, nativeProposals)
	wonMatch := func(c Chain) bool { return !c.Held && c.HumanConfirmed && isWonType(c.OutcomeType) }
	lostMatch := func(c Chain) bool { return !c.Held && c.HumanConfirmed && isLostType(c.OutcomeType) }
	won := countFeedbackUnits(chains, wonMatch, feedbackOutcomeUnit)
	wonContributors := countFeedbackUnits(chains, wonMatch, feedbackPrivacyUnit)
	lost := countFeedbackUnits(chains, lostMatch, feedbackOutcomeUnit)
	lostContributors := countFeedbackUnits(chains, lostMatch, feedbackPrivacyUnit)
	outcomeContributors := countFeedbackUnits(chains, func(c Chain) bool { return wonMatch(c) || lostMatch(c) }, feedbackPrivacyUnit)
	row.Receipts = sensitiveFeedbackMetric(receipts, units-receiptContributors, receiptContributors)
	row.Leads = sensitiveFeedbackMetric(leads, units-leadContributors, leadContributors)
	row.QualifiedOpportunities = sensitiveFeedbackMetric(qco, units-qcoContributors, qcoContributors)
	row.Proposals = sensitiveFeedbackMetric(proposals, units-proposalContributors, proposalContributors)
	row.ProposalStates = feedbackProposalStates(nativeProposals, proposalIDs)
	row.ProposedValue = feedbackProposedValue(nativeProposals, proposalIDs)
	row.Contracts = OutcomeFeedbackMetric{Observed: nil, Unknown: intPtr(units), Status: FeedbackStatusUnjoinable}
	row.Outcomes = feedbackOutcomeStates(won, wonContributors, lost, lostContributors, units, outcomeContributors)
	row.KnownValue = feedbackKnownValue(chains, units)
	row.KnownMargin = OutcomeFeedbackMargin{
		KnownCents: nil, KnownRecords: intPtr(0), UnknownRecords: intPtr(units), Status: FeedbackStatusUnknown,
	}
	return row
}

func feedbackProposalStats(chains []Chain, nativeProposals []NativeProposalFact) (int, int, map[string]struct{}) {
	proposalIDs := map[string]struct{}{}
	contributors := map[string]struct{}{}
	for _, chain := range chains {
		if !feedbackHasProposal(chain) {
			continue
		}
		if id := feedbackProposalUnit(chain); id != "" {
			proposalIDs[id] = struct{}{}
		}
		if unit := feedbackPrivacyUnit(chain); unit != "" {
			contributors[unit] = struct{}{}
		}
	}
	for _, fact := range nativeProposals {
		proposalIDs["proposal:"+strings.TrimSpace(fact.ProposalID)] = struct{}{}
		contributors["account:"+strings.TrimSpace(fact.AccountID)] = struct{}{}
	}
	return len(proposalIDs), len(contributors), proposalIDs
}

func feedbackOutcomeStates(won, wonContributors, lost, lostContributors, units, outcomeContributors int) OutcomeFeedbackStates {
	wonMetric := sensitiveFeedbackMetric(won, units-wonContributors, wonContributors)
	lostMetric := sensitiveFeedbackMetric(lost, units-lostContributors, lostContributors)
	out := OutcomeFeedbackStates{
		Won: wonMetric.Observed, WonStatus: wonMetric.Status,
		Lost: lostMetric.Observed, LostStatus: lostMetric.Status,
	}
	withheld := wonMetric.Status == FeedbackStatusWithheld || lostMetric.Status == FeedbackStatusWithheld
	if withheld {
		out.UnknownStatus = FeedbackStatusWithheld
		if wonMetric.Observed != nil || lostMetric.Observed != nil {
			out.Status = FeedbackStatusPartial
		} else {
			out.Status = FeedbackStatusWithheld
		}
		return out
	}
	out.Unknown = intPtr(maxInt(0, units-outcomeContributors))
	out.UnknownStatus = FeedbackStatusObserved
	switch {
	case won+lost == 0:
		out.Status = FeedbackStatusUnknown
	case outcomeContributors < units:
		out.Status = FeedbackStatusPartial
	default:
		out.Status = FeedbackStatusObserved
	}
	return out
}

func feedbackProposalStates(nativeProposals []NativeProposalFact, proposalIDs map[string]struct{}) OutcomeFeedbackProposalStates {
	type stateGroup struct {
		proposals    map[string]struct{}
		contributors map[string]struct{}
	}
	groups := map[string]*stateGroup{}
	knownProposalIDs := map[string]struct{}{}
	for _, fact := range nativeProposals {
		state := strings.ToUpper(strings.TrimSpace(fact.DecisionState))
		proposalID := "proposal:" + strings.TrimSpace(fact.ProposalID)
		group := groups[state]
		if group == nil {
			group = &stateGroup{proposals: map[string]struct{}{}, contributors: map[string]struct{}{}}
			groups[state] = group
		}
		group.proposals[proposalID] = struct{}{}
		group.contributors["account:"+strings.TrimSpace(fact.AccountID)] = struct{}{}
		knownProposalIDs[proposalID] = struct{}{}
	}
	states := make([]string, 0, len(groups))
	for state := range groups {
		states = append(states, state)
	}
	sort.Strings(states)
	out := OutcomeFeedbackProposalStates{States: []OutcomeFeedbackProposalState{}, Status: FeedbackStatusUnknown}
	for _, state := range states {
		group := groups[state]
		if len(group.contributors) < OutcomeFeedbackMinCellN {
			out.WithheldSmallCell = true
			continue
		}
		out.States = append(out.States, OutcomeFeedbackProposalState{State: state, Observed: len(group.proposals)})
	}
	unknown := maxInt(0, len(proposalIDs)-len(knownProposalIDs))
	if out.WithheldSmallCell {
		if len(out.States) == 0 {
			out.Status = FeedbackStatusWithheld
		} else {
			out.Status = FeedbackStatusPartial
		}
		return out
	}
	out.KnownRecords = intPtr(len(knownProposalIDs))
	out.UnknownRecords = intPtr(unknown)
	switch {
	case len(knownProposalIDs) == 0:
		out.Status = FeedbackStatusUnknown
	case unknown > 0:
		out.Status = FeedbackStatusPartial
	default:
		out.Status = FeedbackStatusObserved
	}
	return out
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
	return !c.Held && firstKnownFeedback(c.ProposalID, c.Keys.ProposalID) != ""
}

func feedbackProposedValue(nativeProposals []NativeProposalFact, proposalIDs map[string]struct{}) OutcomeFeedbackProposedValue {
	type currencyGroup struct {
		amount       int64
		overflow     bool
		proposals    map[string]struct{}
		contributors map[string]struct{}
	}
	groups := map[string]*currencyGroup{}
	knownProposalIDs := map[string]struct{}{}
	withheld := false
	for _, fact := range nativeProposals {
		currency := strings.ToUpper(strings.TrimSpace(fact.Currency))
		if len(currency) != 3 || currency[0] < 'A' || currency[0] > 'Z' ||
			currency[1] < 'A' || currency[1] > 'Z' || currency[2] < 'A' || currency[2] > 'Z' {
			continue
		}
		proposalID := "proposal:" + strings.TrimSpace(fact.ProposalID)
		group := groups[currency]
		if group == nil {
			group = &currencyGroup{proposals: map[string]struct{}{}, contributors: map[string]struct{}{}}
			groups[currency] = group
		}
		if _, duplicate := group.proposals[proposalID]; !duplicate {
			var ok bool
			group.amount, ok = checkedFeedbackAdd(group.amount, fact.AmountMinor)
			if !ok {
				group.overflow = true
			}
			group.proposals[proposalID] = struct{}{}
			knownProposalIDs[proposalID] = struct{}{}
		}
		group.contributors["account:"+strings.TrimSpace(fact.AccountID)] = struct{}{}
	}
	currencies := make([]string, 0, len(groups))
	for currency := range groups {
		currencies = append(currencies, currency)
	}
	sort.Strings(currencies)
	out := OutcomeFeedbackProposedValue{ByCurrency: []OutcomeFeedbackCurrencyValue{}, Status: FeedbackStatusUnknown}
	invalid := false
	for _, currency := range currencies {
		group := groups[currency]
		if group.overflow {
			invalid = true
			continue
		}
		if len(group.contributors) < OutcomeFeedbackMinCellN {
			withheld = true
			out.WithheldSmallCell = true
			continue
		}
		out.ByCurrency = append(out.ByCurrency, OutcomeFeedbackCurrencyValue{
			Currency: currency, AmountMinor: group.amount, ProposalCount: len(group.proposals),
		})
	}
	unknown := maxInt(0, len(proposalIDs)-len(knownProposalIDs))
	if withheld || invalid {
		if len(out.ByCurrency) == 0 {
			if withheld {
				out.Status = FeedbackStatusWithheld
			}
		} else {
			out.Status = FeedbackStatusPartial
		}
		return out
	}
	out.KnownRecords = intPtr(len(knownProposalIDs))
	out.UnknownRecords = intPtr(unknown)
	switch {
	case len(knownProposalIDs) == 0:
		out.Status = FeedbackStatusUnknown
	case unknown > 0:
		out.Status = FeedbackStatusPartial
	default:
		out.Status = FeedbackStatusObserved
	}
	return out
}

func feedbackKnownValue(chains []Chain, units int) OutcomeFeedbackValue {
	type currencyGroup struct {
		contractedValues       map[string]int64
		receivedValues         map[string]int64
		contractedContributors map[string]struct{}
		receivedContributors   map[string]struct{}
	}
	groups := map[string]*currencyGroup{}
	contributors := map[string]struct{}{}
	invalid := false
	for _, c := range chains {
		if c.Held {
			continue
		}
		privacyUnit := feedbackPrivacyUnit(c)
		if privacyUnit == "" {
			continue
		}
		contractedCents := c.Commercial.Payment.ContractedCents
		receivedCents := c.Commercial.Payment.ReceivedCents
		if receivedCents == 0 && c.RevenueEvidenced && c.RevenueCents > 0 {
			receivedCents = c.RevenueCents
		}
		if contractedCents <= 0 && receivedCents <= 0 {
			continue
		}
		currency, ok := feedbackKnownValueCurrency(c.Commercial.Offer.Currency)
		if !ok {
			invalid = true
			continue
		}
		group := groups[currency]
		if group == nil {
			group = &currencyGroup{
				contractedValues: map[string]int64{}, receivedValues: map[string]int64{},
				contractedContributors: map[string]struct{}{}, receivedContributors: map[string]struct{}{},
			}
			groups[currency] = group
		}
		if contractedCents > group.contractedValues[privacyUnit] {
			group.contractedValues[privacyUnit] = contractedCents
		}
		if contractedCents > 0 {
			group.contractedContributors[privacyUnit] = struct{}{}
			contributors[privacyUnit] = struct{}{}
		}
		receivedKey := feedbackReceivedValueUnit(c)
		if receivedCents > group.receivedValues[receivedKey] {
			group.receivedValues[receivedKey] = receivedCents
		}
		if receivedCents > 0 {
			group.receivedContributors[privacyUnit] = struct{}{}
			contributors[privacyUnit] = struct{}{}
		}
	}
	currencies := make([]string, 0, len(groups))
	for currency := range groups {
		currencies = append(currencies, currency)
	}
	sort.Strings(currencies)
	out := OutcomeFeedbackValue{ByCurrency: []OutcomeFeedbackKnownCurrencyValue{}, Status: FeedbackStatusUnknown}
	withheld := false
	for _, currency := range currencies {
		group := groups[currency]
		contracted, contractedOK := sumFeedbackValues(group.contractedValues)
		received, receivedOK := sumFeedbackValues(group.receivedValues)
		if !contractedOK || !receivedOK {
			invalid = true
		}
		entry := OutcomeFeedbackKnownCurrencyValue{
			Currency: currency, ContractedStatus: FeedbackStatusUnknown, ReceivedStatus: FeedbackStatusUnknown,
		}
		if contractedOK && contracted > 0 {
			if len(group.contractedContributors) < OutcomeFeedbackMinCellN {
				entry.ContractedStatus = FeedbackStatusWithheld
				withheld = true
				out.WithheldSmallCell = true
			} else {
				entry.ContractedCents = feedbackInt64Ptr(contracted)
				entry.ContractedStatus = FeedbackStatusObserved
			}
		}
		if receivedOK && received > 0 {
			if len(group.receivedContributors) < OutcomeFeedbackMinCellN {
				entry.ReceivedStatus = FeedbackStatusWithheld
				withheld = true
				out.WithheldSmallCell = true
			} else {
				entry.ReceivedCents = feedbackInt64Ptr(received)
				entry.ReceivedStatus = FeedbackStatusObserved
			}
		}
		if entry.ContractedCents != nil || entry.ReceivedCents != nil {
			out.ByCurrency = append(out.ByCurrency, entry)
		}
	}
	known := len(contributors)
	unknown := maxInt(0, units-known)
	if withheld || invalid {
		if len(out.ByCurrency) > 0 {
			out.Status = FeedbackStatusPartial
		} else if withheld {
			out.Status = FeedbackStatusWithheld
		}
		return out
	}
	out.KnownRecords = intPtr(known)
	out.UnknownRecords = intPtr(unknown)
	if known == 0 {
		return out
	}
	if unknown > 0 {
		out.Status = FeedbackStatusPartial
	} else {
		out.Status = FeedbackStatusObserved
	}
	return out
}

func feedbackKnownValueCurrency(raw string) (string, bool) {
	currency := strings.ToUpper(strings.TrimSpace(raw))
	if currency == "" {
		currency = CurrencyBRL
	}
	if len(currency) != 3 || currency[0] < 'A' || currency[0] > 'Z' ||
		currency[1] < 'A' || currency[1] > 'Z' || currency[2] < 'A' || currency[2] > 'Z' {
		return "", false
	}
	return currency, true
}

func sumFeedbackValues(values map[string]int64) (int64, bool) {
	var total int64
	for _, value := range values {
		var ok bool
		total, ok = checkedFeedbackAdd(total, value)
		if !ok {
			return 0, false
		}
	}
	return total, true
}

func checkedFeedbackAdd(total, value int64) (int64, bool) {
	const maxInt64 = int64(^uint64(0) >> 1)
	if total < 0 || value < 0 || value > maxInt64-total {
		return 0, false
	}
	return total + value, true
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
	row.ProposalStates = OutcomeFeedbackProposalStates{
		States: []OutcomeFeedbackProposalState{}, Status: FeedbackStatusWithheld, WithheldSmallCell: true,
	}
	row.ProposedValue = OutcomeFeedbackProposedValue{
		ByCurrency: []OutcomeFeedbackCurrencyValue{}, Status: FeedbackStatusWithheld, WithheldSmallCell: true,
	}
	row.Contracts = withheldFeedbackMetric()
	row.Outcomes = OutcomeFeedbackStates{
		WonStatus: FeedbackStatusWithheld, LostStatus: FeedbackStatusWithheld,
		UnknownStatus: FeedbackStatusWithheld, Status: FeedbackStatusWithheld,
	}
	row.KnownValue = OutcomeFeedbackValue{
		ByCurrency: []OutcomeFeedbackKnownCurrencyValue{}, Status: FeedbackStatusWithheld, WithheldSmallCell: true,
	}
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
	return row.Receipts.Status == FeedbackStatusWithheld ||
		row.Leads.Status == FeedbackStatusWithheld ||
		row.QualifiedOpportunities.Status == FeedbackStatusWithheld ||
		row.Proposals.Status == FeedbackStatusWithheld ||
		row.ProposalStates.WithheldSmallCell ||
		row.ProposedValue.WithheldSmallCell ||
		row.Outcomes.WonStatus == FeedbackStatusWithheld ||
		row.Outcomes.LostStatus == FeedbackStatusWithheld ||
		row.KnownValue.WithheldSmallCell
}

func feedbackHasQCO(c Chain) bool {
	return !c.Held && strings.EqualFold(strings.TrimSpace(c.OutcomeType), OutcomeQualifiedConversation) &&
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
