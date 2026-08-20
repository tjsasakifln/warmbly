package intel

import (
	"fmt"
	"testing"
	"time"
)

func TestOrganicScoreboardSeparatesLayersAndSources(t *testing.T) {
	now := time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)
	leadAt := now.AddDate(0, 0, -10)
	actionAt := leadAt.Add(20 * time.Minute)
	st := NewMemoryStore()
	organic := CommercialEvent{
		EventID: "ev-sb-org", Version: "1", Schema: EventSchemaV1,
		Type: EventLeadReceived, OccurredAt: leadAt, OrganizationID: "org-sb",
		LeadID: "lead-sb-org", ReceiptID: "rcpt-sb-org", Source: SourceOrganicSearch,
		OrganicSource: SourceOrganicSearch, AssetID: "asset-sl", AssetFamily: AssetFamilyContractAnalysis,
		LandingPath: "/guias/segunda-leitura", RouteFamily: FamilyInbound,
		Consent: "consent-1", Qualified: true,
	}
	IngestEvent(st, organic)
	IngestEvent(st, CommercialEvent{
		EventID: "ev-sb-act", Version: "1", Schema: EventSchemaV1,
		Type: EventActionExecuted, OccurredAt: actionAt, OrganizationID: "org-sb",
		LeadID: "lead-sb-org", ActionID: "act-sb-org", Source: SourceOrganicSearch,
		OrganicSource: SourceOrganicSearch, AssetID: "asset-sl", RouteFamily: FamilyInbound,
		Consent: "consent-1",
	})
	IngestEvent(st, CommercialEvent{
		EventID: "ev-sb-ai", Version: "1", Schema: EventSchemaV1,
		Type: EventLeadReceived, OccurredAt: leadAt, OrganizationID: "org-sb",
		LeadID: "lead-sb-ai", ReceiptID: "rcpt-sb-ai", Source: SourceAIReferral,
		OrganicSource: SourceAIReferral, AssetID: "asset-ma", AssetFamily: AssetFamilyMarketAnswer,
		LandingPath: "/respostas/x", RouteFamily: FamilyInbound, Consent: "consent-1",
	})
	IngestEvent(st, CommercialEvent{
		EventID: "ev-sb-out", Version: "1", Schema: EventSchemaV1,
		Type: EventLeadReceived, OccurredAt: leadAt, OrganizationID: "org-sb",
		LeadID: "lead-sb-out", ReceiptID: "rcpt-sb-out", Source: SourceOutbound,
		OrganicSource: SourceOutbound, AssetID: "asset-out", RouteFamily: FamilyOutbound,
		Consent: "consent-1",
	})
	syn := organic
	syn.EventID, syn.LeadID, syn.ReceiptID = "ev-sb-syn", "lead-sb-syn", "rcpt-sb-syn"
	syn.Synthetic = true
	syn.RecordKind = RecordKindSynthetic
	IngestEvent(st, syn)

	chains, _ := st.ListChains("org-sb")
	board := ProjectOrganicScoreboard(OrganicScoreboardSources{Now: now, Chains: chains})
	if board.CausalProof || board.SchemaVersion != OrganicScoreboardSchemaV1 {
		t.Fatalf("board=%+v", board)
	}
	if board.IncludeSynthetic {
		t.Fatal("default included synthetic")
	}
	if len(board.Windows) != 4 {
		t.Fatalf("windows=%d", len(board.Windows))
	}

	var w28 *OrganicWindow
	for i := range board.Windows {
		if board.Windows[i].ID == Window28dComplete {
			w28 = &board.Windows[i]
		}
		if !board.Windows[i].Censored && board.Windows[i].ID == WindowOpen {
			t.Fatal("open window not censored")
		}
	}
	if w28 == nil {
		t.Fatal("28d window missing")
	}
	sources := map[string]OrganicSlice{}
	for _, sl := range w28.BySource {
		sources[sl.OrganicSource] = sl
		if sl.ROAS != TruthBlocked || sl.CAC != TruthBlocked {
			t.Fatalf("invented ROAS/CAC on %s", sl.OrganicSource)
		}
	}
	for _, src := range OrganicSources() {
		if _, ok := sources[src]; !ok {
			t.Fatalf("missing unmixed source slice %s", src)
		}
	}
	if sources[SourceOrganicSearch].OrganicSource == sources[SourceAIReferral].OrganicSource {
		t.Fatal("organic and ai mixed")
	}
	orgLead := layerCount(sources[SourceOrganicSearch], LayerLeadValid)
	aiLead := layerCount(sources[SourceAIReferral], LayerLeadValid)
	outLead := layerCount(sources[SourceOutbound], LayerLeadValid)
	if orgLead == 0 || aiLead == 0 {
		t.Fatalf("source leads org=%d ai=%d", orgLead, aiLead)
	}
	if orgLead != 1 || aiLead != 1 || outLead != 1 {
		t.Fatalf("unmixed counts org=%d ai=%d out=%d", orgLead, aiLead, outLead)
	}
	elig := layerByID(sources[SourceOrganicSearch].Layers, LayerEligible)
	if elig == nil || elig.Status != TruthBlocked {
		t.Fatalf("discovery invented without web-cfg aggregate: %+v", elig)
	}

	withDisc := ProjectOrganicScoreboard(OrganicScoreboardSources{
		Now: now, Chains: chains,
		Discovery: []OrganicDiscoveryAggregate{{
			OrganicSource: SourceOrganicSearch, AssetID: "asset-sl",
			LandingPath: "/guias/segunda-leitura", Window: Window28dComplete,
			Eligible: 100, Appeared: 40, Clicked: 8, Engaged: 3,
			Freshness: "gsc-top-rows",
		}},
	})
	var w28d *OrganicWindow
	for i := range withDisc.Windows {
		if withDisc.Windows[i].ID == Window28dComplete {
			w28d = &withDisc.Windows[i]
		}
	}
	clicked := layerByID(w28d.BySource[indexOfSource(w28d.BySource, SourceOrganicSearch)].Layers, LayerClicked)
	if clicked == nil || clicked.Status == TruthBlocked || clicked.Count != 8 {
		t.Fatalf("discovery aggregate not applied: %+v", clicked)
	}

	open := windowByID(board.Windows, WindowOpen)
	if open == nil || !open.Censored {
		t.Fatal("open cohorts not censored")
	}

	raw, err := OrganicScoreboardJSON(board)
	if err != nil {
		t.Fatal(err)
	}
	if ReportHasPII(raw) {
		t.Fatal("scoreboard PII")
	}
	fmt.Printf("ORGANIC_SCOREBOARD windows=%d org_leads=%d discovery=%s roas=%s real_empty=%v\n",
		len(board.Windows), orgLead, elig.Status, sources[SourceOrganicSearch].ROAS, board.RealEmpty)
}

func indexOfSource(slices []OrganicSlice, src string) int {
	for i := range slices {
		if slices[i].OrganicSource == src {
			return i
		}
	}
	return 0
}

func windowByID(ws []OrganicWindow, id string) *OrganicWindow {
	for i := range ws {
		if ws[i].ID == id {
			return &ws[i]
		}
	}
	return nil
}

func TestOrganicScoreboardContractedVsReceivedAndUnknown(t *testing.T) {
	now := time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)
	st := NewMemoryStore()
	leadAt := now.AddDate(0, 0, -3)
	IngestEvent(st, CommercialEvent{
		EventID: "ev-rev-lead", Version: "1", Schema: EventSchemaV1,
		Type: EventLeadReceived, OccurredAt: leadAt, OrganizationID: "org-rev",
		LeadID: "lead-rev", ReceiptID: "rcpt-rev", Source: SourceOrganicSearch,
		OrganicSource: SourceOrganicSearch, AssetID: "asset-rev", RouteFamily: FamilyInbound,
		Consent: "c1",
	})
	won := CommercialEvent{
		EventID: "ev-rev-won", Version: "1", Schema: EventSchemaV1,
		Type: EventWon, OccurredAt: leadAt.Add(48 * time.Hour), OrganizationID: "org-rev",
		LeadID: "lead-rev", ActionID: "act-rev", Source: SourceOrganicSearch,
		OrganicSource: SourceOrganicSearch, AssetID: "asset-rev", RouteFamily: FamilyInbound,
		HumanConfirmed: true, Consent: "c1",
	}
	IngestEvent(st, won)
	chains, _ := st.ListChains("org-rev")
	if len(chains) != 1 {
		t.Fatalf("chains=%d", len(chains))
	}
	chains[0].Commercial.Payment.ContractedCents = 180000
	chains[0].RevenueEvidenced = false
	chains[0].RevenueCents = 0
	board := ProjectOrganicScoreboard(OrganicScoreboardSources{Now: now, Chains: chains, IncludeSynthetic: true})
	w90 := windowByID(board.Windows, Window90d)
	if w90 == nil || len(w90.BySource) == 0 {
		t.Fatal("90d missing")
	}
	org := w90.BySource[indexOfSource(w90.BySource, SourceOrganicSearch)]
	if org.ContractedCents != 180000 {
		t.Fatalf("contracted=%d", org.ContractedCents)
	}
	if org.ReceivedCents != 0 {
		t.Fatalf("received invented=%d", org.ReceivedCents)
	}
	if org.Unknown == 0 && org.Won == 0 {
		t.Fatalf("UNKNOWN/WON missing: %+v", org)
	}
	rev := layerByID(org.Layers, LayerRevenue)
	if rev == nil || rev.Observation == "" {
		t.Fatal("revenue layer missing")
	}
	fmt.Printf("REVENUE_SPLIT contracted=%d received=%d won=%d unknown=%d\n",
		org.ContractedCents, org.ReceivedCents, org.Won, org.Unknown)
}

func TestOrganicLatencyCensoredWhenNTooSmall(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	var chains []Chain
	for i := 0; i < 3; i++ {
		lead := now.AddDate(0, 0, -5)
		act := lead.Add(10 * time.Minute)
		chains = append(chains, Chain{
			Keys:    JoinKeys{OrganicSource: SourceOrganicSearch, AssetID: "a", LeadID: fmt.Sprintf("l%d", i), RecordKind: RecordKindReal},
			AssetID: "a", LeadCreatedAt: lead, FirstActionAt: &act, Source: SourceOrganicSearch,
		})
	}
	board := ProjectOrganicScoreboard(OrganicScoreboardSources{Now: now, Chains: chains})
	w := windowByID(board.Windows, Window28dComplete)
	sl := w.BySource[indexOfSource(w.BySource, SourceOrganicSearch)]
	if sl.LeadToAction.Median != Unknown || sl.LeadToAction.N != 3 {
		t.Fatalf("latency with n=3 must stay UNKNOWN: %+v", sl.LeadToAction)
	}
	fmt.Printf("LATENCY_CENSOR n=%d median=%s\n", sl.LeadToAction.N, sl.LeadToAction.Median)
}
