package intel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExportOrganicFeedbackSchemaAndVerdicts(t *testing.T) {
	now := time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)
	st := NewMemoryStore()
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("lead-fb-%d", i)
		at := now.AddDate(0, 0, -8)
		IngestEvent(st, CommercialEvent{
			EventID: "ev-" + id, Version: "1", Schema: EventSchemaV1,
			Type: EventLeadReceived, OccurredAt: at, OrganizationID: "org-fb",
			LeadID: id, ReceiptID: "rcpt-" + id, Source: SourceOrganicSearch,
			OrganicSource: SourceOrganicSearch, AssetID: "asset-fb",
			AssetFamily: AssetFamilyContractAnalysis, LandingPath: "/guias/x",
			RouteFamily: FamilyInbound, Consent: "c1", Qualified: true,
		})
		act := at.Add(time.Hour)
		IngestEvent(st, CommercialEvent{
			EventID: "ev-act-" + id, Version: "1", Schema: EventSchemaV1,
			Type: EventActionExecuted, OccurredAt: act, OrganizationID: "org-fb",
			LeadID: id, ActionID: "act-" + id, Source: SourceOrganicSearch,
			OrganicSource: SourceOrganicSearch, AssetID: "asset-fb",
			RouteFamily: FamilyInbound, Consent: "c1",
		})
		if i < 2 {
			IngestEvent(st, CommercialEvent{
				EventID: "ev-won-" + id, Version: "1", Schema: EventSchemaV1,
				Type: EventWon, OccurredAt: act.Add(24 * time.Hour), OrganizationID: "org-fb",
				LeadID: id, ActionID: "act-" + id, Source: SourceOrganicSearch,
				OrganicSource: SourceOrganicSearch, AssetID: "asset-fb",
				RouteFamily: FamilyInbound, HumanConfirmed: true, Consent: "c1",
				RevenueDocumentID: "nf-1", RevenueCents: 1000,
			})
		}
	}
	chains, _ := st.ListChains("org-fb")
	for i := range chains {
		if isWonType(chains[i].OutcomeType) {
			chains[i].RevenueEvidenced = true
			chains[i].RevenueCents = 1000
			chains[i].Commercial.Payment.ReceivedCents = 1000
			_ = st.UpdateChain(chains[i])
		}
	}

	exp := ExportOrganicFeedback(st, "org-fb", now, false)
	if exp.SchemaVersion != OrganicFeedbackSchemaV1 {
		t.Fatalf("schema=%s", exp.SchemaVersion)
	}
	if exp.CausalProof || len(exp.UpstreamWrites) != 0 {
		t.Fatalf("causal or upstream: %+v", exp)
	}
	if len(exp.Rows) == 0 {
		t.Fatal("no rows")
	}
	found := false
	for _, row := range exp.Rows {
		if row.CausalProof || len(row.UpstreamWrites) != 0 {
			t.Fatalf("row wrote upstream: %+v", row)
		}
		switch row.Verdict {
		case LearningRepeat, LearningChange, LearningStop, LearningNeedMore:
		default:
			t.Fatalf("verdict=%s", row.Verdict)
		}
		if row.AssetID == "asset-fb" && row.OrganicSource == SourceOrganicSearch {
			found = true
			if row.DenominatorLeads < 5 {
				t.Fatalf("denominator=%d", row.DenominatorLeads)
			}
			if row.Verdict == "" || row.Confidence == "" || row.Uncertainty == "" || row.NextTest == "" {
				t.Fatalf("incomplete row: %+v", row)
			}
		}
	}
	if !found {
		t.Fatalf("asset-fb row missing: %+v", exp.Rows)
	}

	empty := ExportOrganicFeedback(NewMemoryStore(), "org-none", now, false)
	if empty.Rows[0].Verdict != LearningNeedMore {
		t.Fatalf("empty verdict=%s", empty.Rows[0].Verdict)
	}

	raw, err := OrganicFeedbackJSON(exp)
	if err != nil {
		t.Fatal(err)
	}
	if ReportHasPII(raw) {
		t.Fatal("feedback PII")
	}
	if strings.Contains(strings.ToLower(string(raw)), "query_hash") {
		t.Fatal("query_hash leaked into feedback")
	}
	fmt.Printf("ORGANIC_FEEDBACK schema=%s rows=%d verdict=%s causal_proof=%v upstream=%d\n",
		exp.SchemaVersion, len(exp.Rows), exp.Rows[0].Verdict, exp.CausalProof, len(exp.UpstreamWrites))
}

func TestOrganicFeedbackSourceHasNoUpstreamClients(t *testing.T) {
	src, err := os.ReadFile("organic_feedback.go")
	if err != nil {
		t.Fatal(err)
	}
	low := strings.ToLower(string(src))
	for _, tok := range []string{"http.post", "http.newrequest", "os.writefile", "sql.exec"} {
		if strings.Contains(low, tok) {
			t.Fatalf("feedback source talks to upstream (%s)", tok)
		}
	}
}

func TestMigration106CHECKContainsOrganicExceptionCodes(t *testing.T) {
	up := filepath.Join("..", "..", "..", "infrastructure", "db", "migrations", "000106_outreach_intel_organic_learning.up.sql")
	down := filepath.Join("..", "..", "..", "infrastructure", "db", "migrations", "000106_outreach_intel_organic_learning.down.sql")
	upRaw, err := os.ReadFile(up)
	if err != nil {
		t.Fatal(err)
	}
	downRaw, err := os.ReadFile(down)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{
		ExceptionLeadWithoutAssetID, ExceptionUnknownAssetVersion, ExceptionContradictorySource,
		ExceptionSyntheticTreatedAsReal, ExceptionMissingConsent, ExceptionPipelineWithoutEvidence,
		ExceptionRevenueWithoutFinancial, ExceptionGSCQueryOnLead, ExceptionQueryHashOnLead,
		"alert_store_failed",
	} {
		if !strings.Contains(string(upRaw), "'"+code+"'") {
			t.Fatalf("000106 up missing %s", code)
		}
	}
	if strings.Contains(string(downRaw), "'"+ExceptionGSCQueryOnLead+"'") {
		t.Fatal("000106 down must restore 000105 CHECK without organic codes")
	}
	if !strings.Contains(string(downRaw), "'alert_store_failed'") {
		t.Fatal("000106 down must keep alert_store_failed")
	}
	if !strings.Contains(string(upRaw), "CREATE TABLE IF NOT EXISTS outreach_intel_search_observations") {
		t.Fatal("000106 up missing search observations table")
	}
	if !strings.Contains(string(upRaw), `"window"`) {
		t.Fatal("000106 must quote reserved column window")
	}
	if !strings.Contains(string(upRaw), "UNIQUE INDEX IF NOT EXISTS outreach_intel_search_obs_org_event_uidx") &&
		!strings.Contains(string(upRaw), "outreach_intel_search_obs_org_event_uidx") {
		t.Fatal("000106 up missing unique (org,event)")
	}
	if !strings.Contains(string(downRaw), "DROP TABLE IF EXISTS outreach_intel_search_observations") {
		t.Fatal("000106 down missing drop of search observations")
	}
	fmt.Printf("MIGRATION_106 up=%d down=%d organic_codes=9 search_observations=true\n", len(upRaw), len(downRaw))
}
