package intel

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestNormalizeOrganicSourceNeverCoercesUnknown(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"organic_search", SourceOrganicSearch},
		{"organic", SourceOrganicSearch},
		{"direct", SourceDirect},
		{"referral", SourceReferral},
		{"ai_referral", SourceAIReferral},
		{"chatgpt", SourceAIReferral},
		{"partner", SourcePartner},
		{"outbound", SourceOutbound},
		{"unknown", Unknown},
		{"", Unknown},
		{"web-cfg", Unknown},
		{"CONFENGE_WEB", Unknown},
		{"google", Unknown},
		{"cpc", Unknown},
	}
	for _, tc := range cases {
		got := NormalizeOrganicSource(tc.in)
		if got != tc.want {
			t.Fatalf("NormalizeOrganicSource(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
	if ComposeOrganicSource("", "google", "organic") != Unknown {
		t.Fatal("medium=organic must not invent organic_search from unlabeled source")
	}
	if ComposeOrganicSource("organic_search", "web-cfg", "") != SourceOrganicSearch {
		t.Fatal("explicit organic_source lost")
	}
	fmt.Printf("ORGANIC_SOURCE unknown_stays=%s google_stays=%s explicit=%s\n",
		NormalizeOrganicSource("unknown"), NormalizeOrganicSource("google"), ComposeOrganicSource("organic_search", "web-cfg", ""))
}

func TestSanitizeCommercialEventStripsGSCQueryAndQueryHash(t *testing.T) {
	ev := CommercialEvent{
		EventID: "ev-gsc-1", Version: "1", Schema: EventSchemaV1,
		Type: EventLeadReceived, OccurredAt: time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
		LeadID: "lead-gsc-1", ReceiptID: "rcpt-gsc-1", RouteFamily: FamilyInbound,
		Source: "organic_search", Query: "segunda leitura contrato preco",
		QueryHash:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		LandingURL: "https://www.confenge.com.br/segunda-leitura?q=secret&email=a@b.com",
		Referrer:   "https://www.google.com/search?q=segunda+leitura",
		AssetID:    "landing-segunda-leitura", OrganizationID: "org-gsc",
	}
	got := SanitizeCommercialEvent(ev)
	if got.Query != "" {
		t.Fatalf("raw GSC query stayed on event: %q", got.Query)
	}
	if got.QueryHash == "" {
		// hash is input-only; Sanitize keeps it on the envelope field but EventToFacts must not copy it
	}
	if got.LandingPath != "/segunda-leitura" {
		t.Fatalf("landing_path=%q", got.LandingPath)
	}
	if strings.Contains(got.Referrer, "?") || strings.Contains(got.Referrer, "q=") {
		t.Fatalf("sensitive referrer kept: %q", got.Referrer)
	}
	if got.OrganicSource != SourceOrganicSearch {
		t.Fatalf("organic_source=%s", got.OrganicSource)
	}

	facts := EventToFacts(ev)
	if facts.Keys.Query != "" {
		t.Fatalf("GSC query joined to lead keys: %q", facts.Keys.Query)
	}
	if !facts.StrippedGSCQuery {
		t.Fatal("stripped_gsc_query unset")
	}
	if !facts.StrippedQueryHash {
		t.Fatal("stripped_query_hash unset")
	}
	if facts.Keys.LandingPath != "/segunda-leitura" {
		t.Fatalf("facts landing=%q", facts.Keys.LandingPath)
	}
	if facts.Keys.FirstTouchAt != nil {
		// not provided
	}
	st := NewMemoryStore()
	res := IngestEvent(st, ev)
	if !hasCode(res.Exceptions, ExceptionGSCQueryOnLead) {
		t.Fatalf("gsc_query_on_lead missing: %v", codesOf(res.Exceptions))
	}
	if !hasCode(res.Exceptions, ExceptionQueryHashOnLead) {
		t.Fatalf("query_hash_on_lead missing: %v", codesOf(res.Exceptions))
	}
	if res.Chain.Keys.Query != "" {
		t.Fatalf("chain stored GSC query: %q", res.Chain.Keys.Query)
	}
	if MetricKeyContainsPII(res.Chain.MetricKey) {
		t.Fatal("metric key has PII")
	}
	fmt.Printf("GSC_STRIP query=%q hash_on_lead=%v landing=%s codes=%v\n",
		res.Chain.Keys.Query, hasCode(res.Exceptions, ExceptionQueryHashOnLead), res.Chain.Keys.LandingPath, codesOf(res.Exceptions))
}

func TestIngestPreservesOrganicAttributionContract(t *testing.T) {
	first := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	last := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	ev := CommercialEvent{
		EventID: "ev-org-1", Version: "1", Schema: EventSchemaV1,
		Type: EventLeadReceived, OccurredAt: last, IngestedAt: last.Add(time.Minute),
		Timezone: "America/Sao_Paulo", OrganizationID: "org-attr",
		LeadID: "lead-org-1", ReceiptID: "rcpt-org-1", CorrelationID: "corr-org-1",
		Source: "organic_search", Medium: "organic", Campaign: "segunda-leitura-aug",
		ReferrerClass: "search", LandingPath: "/guias/segunda-leitura",
		AssetFamily: AssetFamilyContractAnalysis, AssetID: "landing-segunda-leitura",
		AssetVersion: "v3", CTAID: "cta-sl-1", CTAVersion: "cta-v2",
		IntentClass: "contract-margin", QueryClass: "segunda-leitura-preco",
		Query: "segunda-leitura-preco", RecordKind: RecordKindReal,
		Consent: "web-cfg-consent-v4", ConsentVersion: "consent-v4",
		PageVersion: "page-9", ContentVersion: "copy-12", ProducerSHA: "abc123",
		FirstTouchAt: &first, LastTouchAt: &last, RouteFamily: FamilyInbound,
		Synthetic: false,
	}
	st := NewMemoryStore()
	res := IngestEvent(st, ev)
	k := res.Chain.Keys
	if k.OrganicSource != SourceOrganicSearch {
		t.Fatalf("organic_source=%s", k.OrganicSource)
	}
	if k.LandingPath != "/guias/segunda-leitura" || k.AssetVersion != "v3" || k.CTAVersion != "cta-v2" {
		t.Fatalf("path/version dropped: %+v", k)
	}
	if k.QueryClass != "segunda-leitura-preco" || k.Query != "segunda-leitura-preco" {
		t.Fatalf("query_class slug lost: query=%q class=%q", k.Query, k.QueryClass)
	}
	if k.Consent != "web-cfg-consent-v4" || k.ConsentVersion != "consent-v4" {
		t.Fatalf("consent dropped: %+v", k)
	}
	if k.FirstTouchAt == nil || !k.FirstTouchAt.Equal(first) || k.LastTouchAt == nil || !k.LastTouchAt.Equal(last) {
		t.Fatalf("touches dropped: first=%v last=%v", k.FirstTouchAt, k.LastTouchAt)
	}
	if k.PageVersion != "page-9" || k.ProducerSHA != "abc123" {
		t.Fatalf("page/sha dropped: %+v", k)
	}
	if k.RecordKind != RecordKindReal {
		t.Fatalf("record_kind=%s", k.RecordKind)
	}
	replay := IngestEvent(st, ev)
	if !replay.Replay || replay.Created {
		t.Fatalf("replay: %+v", replay)
	}
	chains, _ := st.ListChains("org-attr")
	if len(chains) != 1 {
		t.Fatalf("chains=%d", len(chains))
	}
	fmt.Printf("ATTR_PRESERVE source=%s landing=%s asset=%s@%s first=%s kind=%s replay=%v\n",
		k.OrganicSource, k.LandingPath, k.AssetID, k.AssetVersion, k.FirstTouchAt.UTC().Format(time.RFC3339), k.RecordKind, replay.Replay)
}

func TestUnknownSourceNeverBecomesDirectOrOrganic(t *testing.T) {
	st := NewMemoryStore()
	ev := CommercialEvent{
		EventID: "ev-unk-1", Version: "1", Schema: EventSchemaV1,
		Type: EventLeadReceived, OccurredAt: time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
		LeadID: "lead-unk-1", ReceiptID: "rcpt-unk-1", Source: "unknown",
		AssetID: "asset-1", RouteFamily: FamilyInbound, OrganizationID: "org-unk",
	}
	res := IngestEvent(st, ev)
	if res.Chain.Keys.OrganicSource != Unknown {
		t.Fatalf("unknown coerced to %s", res.Chain.Keys.OrganicSource)
	}
	if res.Chain.Keys.OrganicSource == SourceDirect || res.Chain.Keys.OrganicSource == SourceOrganicSearch {
		t.Fatal("unknown rewritten")
	}
	fmt.Printf("UNKNOWN_STAYS source=%s organic=%s\n", res.Chain.Source, res.Chain.Keys.OrganicSource)
}
