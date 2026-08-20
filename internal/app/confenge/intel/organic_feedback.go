package intel

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

const organicFeedbackMinN = 5

// OrganicFeedbackRow is one asset/cohort editorial recommendation.
// Warmbly never writes web-cfg, extra-cli, or SmartLic.
type OrganicFeedbackRow struct {
	AssetID          string   `json:"asset_id"`
	AssetFamily      string   `json:"asset_family"`
	LandingPath      string   `json:"landing_path"`
	OrganicSource    string   `json:"organic_source"`
	IntentClass      string   `json:"intent_class,omitempty"`
	QueryClass       string   `json:"query_class,omitempty"`
	Window           string   `json:"window"`
	Verdict          string   `json:"verdict"`
	ReasonCodes      []string `json:"reason_codes"`
	Confidence       string   `json:"confidence"`
	Uncertainty      string   `json:"uncertainty"`
	NextTest         string   `json:"next_test"`
	DenominatorLeads int      `json:"denominator_leads"`
	Qualified        int      `json:"qualified"`
	Acknowledged     int      `json:"acknowledged"`
	Conversations    int      `json:"conversations"`
	Meetings         int      `json:"meetings"`
	Proposals        int      `json:"proposals"`
	Pipeline         int      `json:"pipeline"`
	Won              int      `json:"won"`
	Lost             int      `json:"lost"`
	Unknown          int      `json:"unknown"`
	OpenCensored     int      `json:"open_censored"`
	ContractedCents  int64    `json:"contracted_revenue_cents"`
	ReceivedCents    int64    `json:"received_revenue_cents"`
	Freshness        string   `json:"freshness"`
	DiscoveryStatus  string   `json:"discovery_status"`
	CausalProof      bool     `json:"causal_proof"`
	UpstreamWrites   []string `json:"upstream_writes"`
}

// OrganicFeedbackExport is the versioned read-only artifact.
type OrganicFeedbackExport struct {
	SchemaVersion    string               `json:"schema_version"`
	GeneratedAt      string               `json:"generated_at"`
	IncludeSynthetic bool                 `json:"include_synthetic"`
	CausalProof      bool                 `json:"causal_proof"`
	UpstreamWrites   []string             `json:"upstream_writes"`
	RealEmpty        bool                 `json:"real_empty"`
	Rows             []OrganicFeedbackRow `json:"rows"`
	Recommendation   string               `json:"recommendation"`
}

// ExportOrganicFeedback projects REPEAT|CHANGE|STOP|NEED_MORE_DATA per
// asset/cohort. It does not write upstream.
func ExportOrganicFeedback(store Store, orgID string, now time.Time, includeSynthetic bool) OrganicFeedbackExport {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	out := OrganicFeedbackExport{
		SchemaVersion:    OrganicFeedbackSchemaV1,
		GeneratedAt:      now.UTC().Format(time.RFC3339),
		IncludeSynthetic: includeSynthetic,
		CausalProof:      false,
		UpstreamWrites:   []string{},
		Rows:             []OrganicFeedbackRow{},
		Recommendation:   "NEEDS_WEB_CFG_EVENT",
	}
	if store == nil {
		out.RealEmpty = true
		return out
	}
	chains, _ := store.ListChains(orgID)
	board := ProjectOrganicScoreboard(OrganicScoreboardSources{
		Now: now, IncludeSynthetic: includeSynthetic, Chains: chains,
	})
	out.RealEmpty = board.RealEmpty
	var w90 *OrganicWindow
	for i := range board.Windows {
		if board.Windows[i].ID == Window90d {
			w90 = &board.Windows[i]
			break
		}
	}
	if w90 == nil && len(board.Windows) > 0 {
		w90 = &board.Windows[len(board.Windows)-1]
	}
	if w90 != nil {
		for _, sl := range w90.Slices {
			out.Rows = append(out.Rows, feedbackFromSlice(sl, w90.ID, now))
		}
	}
	if len(out.Rows) == 0 {
		out.Rows = append(out.Rows, OrganicFeedbackRow{
			AssetID:         Unknown,
			AssetFamily:     Unknown,
			LandingPath:     Unknown,
			OrganicSource:   Unknown,
			Window:          Window90d,
			Verdict:         LearningNeedMore,
			ReasonCodes:     []string{"no_observed_cohort"},
			Confidence:      "low",
			Uncertainty:     "no real consented organic events in the window",
			NextTest:        "wait for a web-cfg attributed lead with organic_source and asset_id",
			Freshness:       "empty",
			DiscoveryStatus: TruthBlocked,
			CausalProof:     false,
			UpstreamWrites:  []string{},
		})
	}
	sort.Slice(out.Rows, func(i, j int) bool {
		if out.Rows[i].AssetID == out.Rows[j].AssetID {
			if out.Rows[i].OrganicSource == out.Rows[j].OrganicSource {
				return out.Rows[i].LandingPath < out.Rows[j].LandingPath
			}
			return out.Rows[i].OrganicSource < out.Rows[j].OrganicSource
		}
		return out.Rows[i].AssetID < out.Rows[j].AssetID
	})
	out.Recommendation = board.Recommendation
	return out
}

func feedbackFromSlice(sl OrganicSlice, window string, now time.Time) OrganicFeedbackRow {
	_ = now
	leads := layerCount(sl, LayerLeadValid)
	qualified := layerCount(sl, LayerQualifiedLead)
	acked := layerCount(sl, LayerAcknowledged)
	conv := layerCount(sl, LayerConversation)
	meet := layerCount(sl, LayerMeeting)
	prop := layerCount(sl, LayerProposal)
	pipe := layerCount(sl, LayerQualifiedPipeline)
	disc := layerByID(sl.Layers, LayerEligible)
	discStatus := TruthBlocked
	if disc != nil {
		discStatus = disc.Status
	}
	row := OrganicFeedbackRow{
		AssetID:          sl.AssetID,
		AssetFamily:      sl.AssetFamily,
		LandingPath:      sl.LandingPath,
		OrganicSource:    sl.OrganicSource,
		IntentClass:      sl.IntentClass,
		Window:           window,
		DenominatorLeads: leads,
		Qualified:        qualified,
		Acknowledged:     acked,
		Conversations:    conv,
		Meetings:         meet,
		Proposals:        prop,
		Pipeline:         pipe,
		Won:              sl.Won,
		Lost:             sl.Lost,
		Unknown:          sl.Unknown,
		OpenCensored:     sl.OpenCensored,
		ContractedCents:  sl.ContractedCents,
		ReceivedCents:    sl.ReceivedCents,
		Freshness:        firstNonEmpty(sl.DiscoveryFreshness, "month_window="+window),
		DiscoveryStatus:  discStatus,
		CausalProof:      false,
		UpstreamWrites:   []string{},
	}
	row.Verdict, row.ReasonCodes, row.Confidence, row.Uncertainty, row.NextTest = decideOrganicVerdict(row, discStatus)
	return row
}

func decideOrganicVerdict(row OrganicFeedbackRow, discStatus string) (verdict string, reasons []string, confidence, uncertainty, next string) {
	if row.DenominatorLeads < organicFeedbackMinN {
		reasons = []string{"insufficient_n", "NEED_MORE_DATA"}
		if discStatus == TruthBlocked {
			reasons = append(reasons, "discovery_blocked")
		}
		return LearningNeedMore, reasons, "low",
			fmt.Sprintf("n below %d or discovery aggregates missing", organicFeedbackMinN),
			"collect more complete-window leads before changing the asset"
	}
	if row.ReceivedCents > 0 && row.Won > 0 {
		return LearningRepeat, []string{"received_revenue", "confirmed_won"}, "medium",
			"observed association only; causal_proof stays false",
			"repeat the asset family and keep source slices unmixed"
	}
	if row.Won > 0 && row.Lost == 0 && row.Pipeline > 0 {
		return LearningRepeat, []string{"confirmed_won", "qualified_pipeline"}, "low",
			"no received revenue yet; do not claim SEO sold",
			"keep the asset; wait for a reconciled financial event"
	}
	if row.Lost > 0 && row.Won == 0 && row.Conversations == 0 {
		return LearningStop, []string{"confirmed_lost", "no_conversation"}, "medium",
			"lost without a conversation; do not infer a better query",
			"stop promoting this asset until a human reviews the offer"
	}
	if row.Lost > 0 && row.Won == 0 && row.Conversations > 0 {
		return LearningChange, []string{"confirmed_lost", "conversation_no_close"}, "low",
			"conversation happened; close is UNKNOWN or lost",
			"change CTA or offer framing; do not rewrite the query class"
	}
	if row.Unknown == row.DenominatorLeads || row.Won+row.Lost == 0 {
		return LearningNeedMore, []string{"unknown_outcome", "NEED_MORE_DATA"}, "low",
			"outcomes still UNKNOWN or open/censored",
			"record a human outcome before expanding or killing the asset"
	}
	if row.Acknowledged == 0 {
		return LearningChange, []string{"no_first_action"}, "low",
			"leads exist without first human action",
			"change speed-to-lead handling; do not auto-send"
	}
	return LearningNeedMore, []string{"NEED_MORE_DATA"}, "low",
		"not enough observed closes to recommend REPEAT/CHANGE/STOP",
		"keep observing; do not auto-change content or indexation"
}

func layerCount(sl OrganicSlice, id string) int {
	if ly := layerByID(sl.Layers, id); ly != nil {
		return ly.Count
	}
	return 0
}

func layerByID(layers []OrganicLayer, id string) *OrganicLayer {
	for i := range layers {
		if layers[i].ID == id {
			return &layers[i]
		}
	}
	return nil
}

// OrganicFeedbackJSON encodes the export.
func OrganicFeedbackJSON(rep OrganicFeedbackExport) ([]byte, error) {
	return json.MarshalIndent(rep, "", "  ")
}

// OrganicScoreboardJSON encodes the scoreboard.
func OrganicScoreboardJSON(board OrganicScoreboard) ([]byte, error) {
	return json.MarshalIndent(board, "", "  ")
}
