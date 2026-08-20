package intel

import (
	"strings"
	"time"
)

const (
	ScoreboardSchemaV1 = "confenge.inbound_truth_scoreboard.v1"

	StageURLsLiveIndexable     = "urls_live_indexable"
	StageNonBrandedImpressions = "non_branded_impressions_clicks"
	StageCTACompleted          = "cta_completed"
	StageLeadPersisted         = "lead_persisted"
	StageQualifiedConversation = "qualified_conversation"
	StageProposalEmitted       = "proposal_emitted"
	StageRevenueReceived       = "revenue_received"

	TruthTrue    = "TRUE"
	TruthFalse   = "FALSE"
	TruthUnknown = "UNKNOWN"
	TruthBlocked = "BLOCKED"

	SearchIngestContract   = "confenge.search_observation.v1"
	URLIndexIngestContract = "confenge.url_index.v1"

	OwnerSearchOps  = "search-ops"
	OwnerInboundOps = "inbound-ops"
	OwnerFounder    = "founder"
	OwnerExtraCLI   = "extra-cli"
	OwnerWebCfg     = "web-cfg"
	OwnerFinance    = "finance"
	OwnerCommercial = "commercial-intel"

	MetricPipelineContracted = "pipeline_contracted"
	MetricMRR                = "mrr"
	MetricChargeCreated      = "charge_created"
	MetricCashReceived       = "cash_received"
)

// ScoreboardSources is the existing-plane input. Missing externals stay
// BLOCKED. Nothing is invented from parallel-goal outputs.
type ScoreboardSources struct {
	Now                time.Time
	IncludeSynthetic   bool
	InboundHealthReady bool
	InboundHealth      string
	AutoSendEnabled    bool
	DispatchAttempted  bool
	PublicEndpoint     string
	GSCAvailable       bool
	URLIndexAvailable  bool
	InboundNowCount    int
	CTACompletedCount  int
	LeadPersistedCount int
	Executive          ExecutiveView
}

// ScoreboardStage is one of the seven executive stages.
type ScoreboardStage struct {
	ID                string `json:"id"`
	Label             string `json:"label"`
	Order             int    `json:"order"`
	Status            string `json:"status"`
	SourceOfTruth     string `json:"source_of_truth"`
	SnapshotAt        string `json:"snapshot_at"`
	Freshness         string `json:"freshness"`
	Numerator         *int   `json:"numerator"`
	Denominator       *int   `json:"denominator"`
	SyntheticIncluded bool   `json:"synthetic_included"`
	Owner             string `json:"owner"`
	NextAction        string `json:"next_action"`
	Latency           string `json:"latency"`
	Observation       string `json:"observation"`
}

// ScoreboardMetric is a commercial number that must stay off the seven stages.
type ScoreboardMetric struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	Status        string `json:"status"`
	ValueCents    int64  `json:"value_cents"`
	Count         int    `json:"count,omitempty"`
	SourceOfTruth string `json:"source_of_truth"`
	Observation   string `json:"observation"`
}

// Scoreboard is the seven-stage executive placar. Default excludes synthetic.
type Scoreboard struct {
	SchemaVersion     string             `json:"schema_version"`
	GeneratedAt       string             `json:"generated_at"`
	IncludeSynthetic  bool               `json:"include_synthetic"`
	ProductionPath    string             `json:"production_path"`
	HumanBlocker      string             `json:"human_blocker,omitempty"`
	NextRealEvent     string             `json:"next_real_event"`
	CausalProof       bool               `json:"causal_proof"`
	AutoSendEnabled   bool               `json:"auto_send_enabled"`
	DispatchAttempted bool               `json:"dispatch_attempted"`
	Stages            []ScoreboardStage  `json:"stages"`
	SeparateMetrics   []ScoreboardMetric `json:"separate_metrics"`
}

// ProjectScoreboard maps existing health/intel/inbound sources onto seven
// stages. GSC and URL index stay BLOCKED until an ingest contract exists.
func ProjectScoreboard(src ScoreboardSources) Scoreboard {
	now := src.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	stamp := now.UTC().Format(time.RFC3339)
	exec := src.Executive
	if !src.IncludeSynthetic {
		exec.IncludeSynthetic = false
	}

	ctaN := src.CTACompletedCount
	if ctaN == 0 {
		ctaN = src.LeadPersistedCount
	}
	if ctaN == 0 && src.InboundNowCount > 0 {
		ctaN = src.InboundNowCount
	}
	leads := src.LeadPersistedCount
	if leads == 0 {
		leads = exec.Denominators.Leads
	}
	if leads == 0 {
		leads = src.InboundNowCount
	}
	qco := exec.QCO
	if qco == 0 {
		qco = exec.Conversations
	}
	proposals := exec.Proposals
	revenueN := 0
	if exec.RevenueCents > 0 && strings.EqualFold(strings.TrimSpace(exec.RevenueStatus), "evidenced") {
		revenueN = 1
	}

	stages := []ScoreboardStage{
		projectURLStage(src, stamp),
		projectImpressionStage(src, stamp),
		projectCTAStage(src, stamp, ctaN, leads),
		projectLeadStage(src, stamp, leads, ctaN),
		projectQCOStage(src, stamp, qco, leads),
		projectProposalStage(src, stamp, proposals, qco),
		projectRevenueStage(src, stamp, revenueN, proposals, exec),
	}

	board := Scoreboard{
		SchemaVersion:     ScoreboardSchemaV1,
		GeneratedAt:       stamp,
		IncludeSynthetic:  src.IncludeSynthetic,
		CausalProof:       false,
		AutoSendEnabled:   src.AutoSendEnabled,
		DispatchAttempted: src.DispatchAttempted,
		Stages:            stages,
		SeparateMetrics:   projectSeparateMetrics(exec, stamp),
	}
	board.ProductionPath, board.HumanBlocker, board.NextRealEvent = scoreboardVerdict(src, stages)
	return board
}

func projectURLStage(src ScoreboardSources, stamp string) ScoreboardStage {
	st := ScoreboardStage{
		ID:                StageURLsLiveIndexable,
		Label:             "URLs técnicas live/indexáveis",
		Order:             1,
		SourceOfTruth:     URLIndexIngestContract,
		SnapshotAt:        stamp,
		Freshness:         "no consumer",
		SyntheticIncluded: src.IncludeSynthetic,
		Owner:             OwnerSearchOps,
		Latency:           "n/a",
		Observation:       "Indexability is not inferred from inbound volume or page views. Future ingest is " + URLIndexIngestContract + ".",
	}
	if src.URLIndexAvailable {
		st.Status = TruthUnknown
		st.Freshness = "ingest present, no snapshot"
		st.NextAction = "wait for a non-synthetic url_index snapshot"
		return st
	}
	st.Status = TruthBlocked
	st.NextAction = "do not invent live/indexable counts; land " + URLIndexIngestContract + " when the search producer is ready"
	return st
}

func projectImpressionStage(src ScoreboardSources, stamp string) ScoreboardStage {
	st := ScoreboardStage{
		ID:                StageNonBrandedImpressions,
		Label:             "Impressões e cliques não branded",
		Order:             2,
		SourceOfTruth:     SearchIngestContract,
		SnapshotAt:        stamp,
		Freshness:         "no consumer",
		SyntheticIncluded: src.IncludeSynthetic,
		Owner:             OwnerSearchOps,
		Latency:           "n/a",
		Observation:       "Non-branded impressions and clicks are not inferred from CTA or lead counts. Future ingest is " + SearchIngestContract + ".",
	}
	if src.GSCAvailable {
		st.Status = TruthUnknown
		st.Freshness = "ingest present, no snapshot"
		st.NextAction = "wait for a non-synthetic GSC snapshot"
		return st
	}
	st.Status = TruthBlocked
	st.NextAction = "do not invent GSC numbers; land " + SearchIngestContract + " when Search Console access exists"
	return st
}

func projectCTAStage(src ScoreboardSources, stamp string, cta, leads int) ScoreboardStage {
	st := ScoreboardStage{
		ID:                StageCTACompleted,
		Label:             "CTA concluído",
		Order:             3,
		SourceOfTruth:     "POST /api/v1/webhooks/confenge/inbound persist-first receipt",
		SnapshotAt:        stamp,
		Freshness:         inboundFreshness(src),
		SyntheticIncluded: src.IncludeSynthetic,
		Owner:             OwnerInboundOps,
		Numerator:         intPtr(cta),
		Denominator:       intPtr(maxInt(cta, leads)),
		Latency:           latencyOrUnknown(src.Executive.Latency.LeadToIngest),
		Observation:       "CTA completion is the signed form POST. It is not a lead, proposal, or cash. Same persist-first hop as the receipt; do not treat two stages as two conversions.",
	}
	switch {
	case !src.InboundHealthReady:
		st.Status = TruthBlocked
		st.NextAction = "keep auto-send off; configure inbound secret, dest org, and public endpoint"
	case cta > 0:
		st.Status = TruthTrue
		st.NextAction = "keep the receipt; do not contact a synthetic lead"
	default:
		st.Status = TruthFalse
		st.NextAction = "wait for a real consented form POST; do not POST through a synthetic form"
	}
	return st
}

func projectLeadStage(src ScoreboardSources, stamp string, leads, cta int) ScoreboardStage {
	st := ScoreboardStage{
		ID:                StageLeadPersisted,
		Label:             "Lead persistido",
		Order:             4,
		SourceOfTruth:     "INBOUND NOW + outreach_inbound_leads durable receipt",
		SnapshotAt:        stamp,
		Freshness:         inboundFreshness(src),
		SyntheticIncluded: src.IncludeSynthetic,
		Owner:             OwnerInboundOps,
		Numerator:         intPtr(leads),
		Denominator:       intPtr(maxInt(cta, leads)),
		Latency:           latencyOrUnknown(src.Executive.Latency.LeadToIngest),
		Observation:       "A durable receipt is a lead. A page view, citation, or X-Ray completion is not. Synthetic/infrastructure_canary receipts stay off this default view.",
	}
	switch {
	case !src.InboundHealthReady:
		st.Status = TruthBlocked
		st.NextAction = "unblock inbound receive (secret + dest org + auto-send false) before claiming a live lead"
	case leads > 0:
		st.Status = TruthTrue
		st.NextAction = "record the human action/outcome on the real lead_id"
	default:
		st.Status = TruthFalse
		st.NextAction = "no real receipt yet; do not invent a lead_id"
	}
	return st
}

func projectQCOStage(src ScoreboardSources, stamp string, qco, leads int) ScoreboardStage {
	st := ScoreboardStage{
		ID:                StageQualifiedConversation,
		Label:             "Conversa qualificada",
		Order:             5,
		SourceOfTruth:     "GET /confenge/intel/executive include_synthetic=0 (qco)",
		SnapshotAt:        stamp,
		Freshness:         executiveFreshness(src.Executive),
		SyntheticIncluded: src.IncludeSynthetic,
		Owner:             OwnerFounder,
		Numerator:         intPtr(qco),
		Denominator:       intPtr(maxInt(leads, qco)),
		Latency:           latencyOrUnknown(src.Executive.Latency.ActionToConversation),
		Observation:       "Qualified conversation requires a human-recorded reply/meeting/QCO. A callback or HTTP 200 is not a conversation.",
	}
	switch {
	case leads == 0 && qco == 0:
		st.Status = TruthUnknown
		st.NextAction = "persist a real lead before recording a conversation"
	case qco > 0:
		st.Status = TruthTrue
		st.NextAction = "keep the human outcome; do not infer a proposal"
	default:
		st.Status = TruthFalse
		st.NextAction = "record attempted/reached/reply after a real interaction"
	}
	return st
}

func projectProposalStage(src ScoreboardSources, stamp string, proposals, qco int) ScoreboardStage {
	st := ScoreboardStage{
		ID:                StageProposalEmitted,
		Label:             "Proposta emitida",
		Order:             6,
		SourceOfTruth:     "GET /confenge/intel/executive include_synthetic=0 (proposals)",
		SnapshotAt:        stamp,
		Freshness:         executiveFreshness(src.Executive),
		SyntheticIncluded: src.IncludeSynthetic,
		Owner:             OwnerFounder,
		Numerator:         intPtr(proposals),
		Denominator:       intPtr(maxInt(qco, proposals)),
		Latency:           latencyOrUnknown(src.Executive.Latency.ConversationToProposal),
		Observation:       "A proposal is a human-emitted commercial document. Pipeline open and checkout created are not a proposal.",
	}
	switch {
	case qco == 0 && proposals == 0:
		st.Status = TruthUnknown
		st.NextAction = "do not emit a proposal without a qualified conversation"
	case proposals > 0:
		st.Status = TruthTrue
		st.NextAction = "wait for a financial document before recording receita"
	default:
		st.Status = TruthFalse
		st.NextAction = "record proposal_emitted only after the founder actually sent a proposal"
	}
	return st
}

func projectRevenueStage(src ScoreboardSources, stamp string, revenueN, proposals int, exec ExecutiveView) ScoreboardStage {
	st := ScoreboardStage{
		ID:                StageRevenueReceived,
		Label:             "Receita recebida",
		Order:             7,
		SourceOfTruth:     "revenue_evidenced + revenue_document_id (not checkout/payment created)",
		SnapshotAt:        stamp,
		Freshness:         executiveFreshness(exec),
		SyntheticIncluded: src.IncludeSynthetic,
		Owner:             OwnerFinance,
		Numerator:         intPtr(revenueN),
		Denominator:       intPtr(maxInt(proposals, revenueN)),
		Latency:           latencyOrUnknown(exec.Latency.ProposalToClose),
		Observation:       "Callback, HTTP 200, checkout created, payment created, and page view are not receita. WON without a financial document is not caixa.",
	}
	switch {
	case revenueN > 0:
		st.Status = TruthTrue
		st.NextAction = "keep contracted, MRR, charge created, and cash as separate metrics"
	case proposals == 0:
		st.Status = TruthUnknown
		st.NextAction = "do not record receita without a proposal and a financial document"
	default:
		st.Status = TruthFalse
		st.NextAction = "record received revenue only with a financial document id"
	}
	return st
}

func projectSeparateMetrics(exec ExecutiveView, stamp string) []ScoreboardMetric {
	_ = stamp
	comm := exec.Commercial
	return []ScoreboardMetric{
		{
			ID: MetricPipelineContracted, Label: "Pipeline contratado",
			Status: statusFromCents(comm.ContractedCents), ValueCents: comm.ContractedCents,
			SourceOfTruth: "executive.commercial.contracted_revenue_cents",
			Observation:   "Contracted commitment is not caixa recebido.",
		},
		{
			ID: MetricMRR, Label: "MRR",
			Status: statusFromCents(comm.MRRCents), ValueCents: comm.MRRCents,
			SourceOfTruth: "executive.commercial.mrr_cents",
			Observation:   "MRR is a recurring snapshot, not received cash.",
		},
		{
			ID: MetricChargeCreated, Label: "Cobrança criada",
			Status:        statusFromCount(comm.PaymentCreated + comm.CheckoutCreated),
			Count:         comm.PaymentCreated + comm.CheckoutCreated,
			SourceOfTruth: "executive.commercial.payment_created + checkout_created",
			Observation:   "A created checkout or payment object is not receita recebida.",
		},
		{
			ID: MetricCashReceived, Label: "Caixa recebido",
			Status: statusFromCents(comm.ReceivedCents), ValueCents: comm.ReceivedCents,
			SourceOfTruth: "executive.commercial.received_revenue_cents",
			Observation:   "Only reconciled received cents. Distinct from stage 7 until a document exists.",
		},
	}
}

func scoreboardVerdict(src ScoreboardSources, stages []ScoreboardStage) (path, blocker, next string) {
	if src.AutoSendEnabled || src.DispatchAttempted {
		return "BLOCKED", "CONFENGE_AUTO_SEND_ENABLED or dispatch_attempted is true", "turn auto-send off and refuse dispatch"
	}
	if !src.InboundHealthReady {
		return "BLOCKED", "inbound receive is not READY (secret, dest org, or public endpoint)", "set CONFENGE_INBOUND_WEBHOOK_SECRET, CONFENGE_INBOUND_ORG_ID, CONFENGE_AUTO_SEND_ENABLED=false, and a reachable public inbound URL"
	}
	lead := stageByID(stages, StageLeadPersisted)
	if lead != nil && lead.Status == TruthTrue {
		return "PROVED", "", "record the next real human action/outcome on the persisted lead"
	}
	cta := stageByID(stages, StageCTACompleted)
	if cta != nil && cta.Status == TruthTrue {
		return "PROVED", "", "confirm the durable receipt on INBOUND NOW, then record the human outcome"
	}
	return "BLOCKED", "no real consented non-synthetic inbound receipt observed", "wait for a real form POST; do not treat a synthetic canary as the first commercial event"
}

func stageByID(stages []ScoreboardStage, id string) *ScoreboardStage {
	for i := range stages {
		if stages[i].ID == id {
			return &stages[i]
		}
	}
	return nil
}

func inboundFreshness(src ScoreboardSources) string {
	if !src.InboundHealthReady {
		return "inbound health " + firstNonEmpty(src.InboundHealth, "BLOCKED")
	}
	return "inbound health READY"
}

func executiveFreshness(v ExecutiveView) string {
	if strings.TrimSpace(v.Month) == "" {
		return "no executive snapshot"
	}
	if v.RealEmpty {
		return "real_empty month=" + v.Month
	}
	return "month=" + v.Month
}

func latencyOrUnknown(ms int64) string {
	if ms <= 0 {
		return "UNKNOWN"
	}
	return time.Duration(ms * int64(time.Millisecond)).String()
}

func statusFromCents(v int64) string {
	if v > 0 {
		return TruthTrue
	}
	return TruthFalse
}

func statusFromCount(v int) string {
	if v > 0 {
		return TruthTrue
	}
	return TruthFalse
}

func intPtr(v int) *int { return &v }

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ScoreboardExcludesSynthetic reports whether a labeled canary must stay off
// the default executive placar.
func ScoreboardExcludesSynthetic(includeSynthetic, synthetic bool) bool {
	return !includeSynthetic && synthetic
}

// ExceptionOwner is the durable queue owner for one classified code.
func ExceptionOwner(code string) string {
	switch strings.TrimSpace(code) {
	case ExceptionUnconfirmedWon, ExceptionUnconfirmedLost:
		return OwnerFounder
	case ExceptionConflictingAccount, ExceptionMissingVersion:
		return OwnerExtraCLI
	case ExceptionStaleAttribution, ExceptionMissingAttribution,
		ExceptionLeadWithoutAssetID, ExceptionUnknownAssetVersion,
		ExceptionContradictorySource, ExceptionGSCQueryOnLead, ExceptionQueryHashOnLead:
		return OwnerWebCfg
	case ExceptionCreatedAsRevenue, ExceptionOnboardingBeforePay, ExceptionNfseManualQueue, ExceptionChargeback, ExceptionPaymentRefund,
		ExceptionRevenueWithoutFinancial:
		return OwnerFinance
	case ExceptionPipelineWithoutEvidence:
		return OwnerFounder
	case ExceptionCounselReviewDue:
		return OwnerFounder
	case ExceptionUnknownProviderEvent:
		return OwnerCommercial
	default:
		return OwnerInboundOps
	}
}
