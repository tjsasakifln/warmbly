package confenge

import (
	"encoding/json"
	"os"
	"sort"
	"testing"
)

// This file is a READ-ONLY test adapter. It does not add an endpoint, a service
// or a second authoritative registry: it serializes exactly what the canonical
// registry and the real selector already produce, so a consumer repository can
// test against produced destinations instead of a hand-copied table.
//
// Regenerate the consumer fixture with:
//
//	CONFENGE_DESTINATION_EXPORT=/path/to/warmbly-commercial-destinations.json \
//	  go test ./internal/app/confenge -run TestExportCommercialDestinationContract -count=1

type exportedDestination struct {
	ID                   string   `json:"id"`
	Vertical             string   `json:"vertical"`
	CanonicalURL         string   `json:"canonical_url"`
	Anchor               string   `json:"anchor"`
	State                string   `json:"state"`
	Activation           string   `json:"activation"`
	PublicProofExpected  string   `json:"public_proof_expected"`
	TerminalAction       string   `json:"terminal_action"`
	AllowedCampaignKinds []string `json:"allowed_campaign_kinds"`
	UTMAllowlist         []string `json:"utm_allowlist"`
	// EmittedURL is the URL the producer actually emits for a first-touch
	// email, or "" when the destination is withheld. It is the field a
	// consumer must test against; CanonicalURL alone omits the fragment.
	EmittedURL string `json:"emitted_first_touch_url"`
	EmitError  string `json:"emit_error,omitempty"`
}

type exportedRoute struct {
	ServiceCode      string `json:"service_code"`
	MomentCode       string `json:"moment_code,omitempty"`
	ClaimKey         string `json:"claim_key"`
	VisitorSituation string `json:"visitor_situation"`
	LandingID        string `json:"landing_id"`
	URL              string `json:"url"`
}

type destinationExport struct {
	Schema       string                `json:"schema"`
	Producer     string                `json:"producer"`
	Note         string                `json:"note"`
	Destinations []exportedDestination `json:"destinations"`
	Routes       []exportedRoute       `json:"routes"`
}

// exportedServiceCodes are the codes the producer actually routes today. They
// are read from the switch's own behaviour, not invented for the fixture.
var exportedServiceCodes = []struct{ service, moment string }{
	{service: "ADITIVOS"},
	{service: "EXTRACONTRATUAIS"},
	{service: "MEDICOES"},
	{service: "REAJUSTE"},
	{service: "REEQUILIBRIO"},
	{service: "PLANILHAS"},
	{service: "APOIO_LICITACAO"},
	{service: "INTELIGENCIA_PNCP"},
	{service: "ENCERRAMENTO_CONTRATUAL"},
	{service: "MONITORAMENTO_CONTRATUAL"},
	{service: "BACKOFFICE"},
	{service: "UNKNOWN_SERVICE_CODE"},
}

func buildCommercialDestinationExport() destinationExport {
	out := destinationExport{
		Schema:   "confenge.warmbly-commercial-destinations/1.0",
		Producer: "warmbly:internal/app/confenge",
		Note:     "Read-only projection of CommercialDestinations() and commercialRouteRule via ResolveCommercialMessageRoute. Warmbly remains the sole writer.",
	}
	for _, d := range CommercialDestinations() {
		entry := exportedDestination{
			ID: d.ID, Vertical: string(d.Vertical), CanonicalURL: d.CanonicalURL, Anchor: d.Anchor,
			State: string(d.State), Activation: string(d.Activation),
			PublicProofExpected: d.PublicProofExpected, TerminalAction: d.TerminalAction,
			UTMAllowlist: append([]string(nil), d.UTMAllowlist...),
		}
		for _, k := range d.AllowedCampaignKinds {
			entry.AllowedCampaignKinds = append(entry.AllowedCampaignKinds, string(k))
		}
		url, err := CommercialDestinationURL(d.ID, CommercialCampaignFirstTouchEmail)
		entry.EmittedURL = url
		if err != nil {
			entry.EmitError = err.Error()
		}
		out.Destinations = append(out.Destinations, entry)
	}
	sort.Slice(out.Destinations, func(i, j int) bool { return out.Destinations[i].ID < out.Destinations[j].ID })

	for _, tc := range exportedServiceCodes {
		claim, situation, landing := commercialRouteRule(tc.service, tc.moment)
		route := exportedRoute{
			ServiceCode: tc.service, MomentCode: tc.moment,
			ClaimKey: claim, VisitorSituation: situation, LandingID: landing,
		}
		if r, err := ResolveCommercialMessageRoute(tc.service, tc.moment, CommercialCampaignFirstTouchEmail); err == nil {
			route.URL = r.URL
		}
		out.Routes = append(out.Routes, route)
	}
	return out
}

// TestExportCommercialDestinationContract keeps the projection honest and, when
// asked, writes it out for the consumer repository. It never mutates state.
func TestExportCommercialDestinationContract(t *testing.T) {
	export := buildCommercialDestinationExport()

	if len(export.Destinations) != len(commercialDestinationRegistry) {
		t.Fatalf("export dropped destinations: %d vs %d", len(export.Destinations), len(commercialDestinationRegistry))
	}
	for _, d := range export.Destinations {
		if d.State == string(CommercialDestinationActive) && d.Activation == string(CommercialActivationActive) {
			if d.EmittedURL == "" {
				t.Fatalf("active destination %s exported no URL: %s", d.ID, d.EmitError)
			}
		} else if d.EmittedURL != "" {
			t.Fatalf("withheld destination %s must not export a URL, got %s", d.ID, d.EmittedURL)
		}
		if d.PublicProofExpected == "" || d.TerminalAction == "" {
			t.Fatalf("destination %s exported without proof/terminal action", d.ID)
		}
	}
	// The routing projection must agree with the live resolver, so the fixture
	// cannot drift away from the selector a campaign actually uses.
	for _, r := range export.Routes {
		if r.URL == "" {
			continue
		}
		live, err := ResolveCommercialMessageRoute(r.ServiceCode, r.MomentCode, CommercialCampaignFirstTouchEmail)
		if err != nil {
			t.Fatalf("route %s no longer resolves: %v", r.ServiceCode, err)
		}
		if live.URL != r.URL || string(live.ClaimKey) != r.ClaimKey || live.Destination.ID != r.LandingID {
			t.Fatalf("export drifted from resolver for %s", r.ServiceCode)
		}
	}

	if path := os.Getenv("CONFENGE_DESTINATION_EXPORT"); path != "" {
		blob, err := json.MarshalIndent(export, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(blob, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %d destinations and %d routes to %s", len(export.Destinations), len(export.Routes), path)
	}
}
