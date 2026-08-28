package confenge

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

func TestMergeControlledDiscoveryPersistsRegistrySource(t *testing.T) {
	eligible := true
	preferred := true
	unknown := true
	raw := mergeControlledDiscovery(nil, FeedContact{
		RouteClass:              RouteClassGenericCompany,
		ControlledEmailEligible: &eligible,
		PreferredInitial:        &preferred,
		MailboxCompanyEvidence:  "OBSERVED",
		PersonUnknown:           &unknown,
		RiskClass:               "ALLOWED",
		Source:                  "company_registry",
		SourceType:              "company_registry",
	})
	var d controlledDiscovery
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	if d.Source != "company_registry" || d.SourceType != "company_registry" {
		t.Fatalf("registry source dropped on import: %+v json=%s", d, raw)
	}
}

func TestMergeControlledDiscoveryCopiesSourceTypeWhenSourceBlank(t *testing.T) {
	eligible := true
	raw := mergeControlledDiscovery(nil, FeedContact{
		RouteClass:              RouteClassPublicCompanyFreemail,
		ControlledEmailEligible: &eligible,
		SourceType:              "official_registry",
	})
	var d controlledDiscovery
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	if d.Source != "official_registry" || d.SourceType != "official_registry" {
		t.Fatalf("source_type was not copied onto source: %+v json=%s", d, raw)
	}
}

func TestLeadToCandidatePersistsRegistrySourceInDiscoveryJSON(t *testing.T) {
	eligible, preferred, unknown, notFixture, ready := true, true, true, false, true
	cand := leadToCandidate(uuid.Nil, uuid.Nil, uuid.Nil, FeedContact{
		Email:                   "contato@empresa.example",
		Source:                  "company_registry",
		SourceType:              "company_registry",
		VerificationStatus:      models.OutreachVerifyOfficialSource,
		OwnershipStatus:         "COMPANY_OWNED",
		ChannelEpistemic:        "OBSERVED",
		RouteFreshness:          "FRESH",
		RouteSuppression:        "NONE",
		RouteClass:              RouteClassGenericCompany,
		ControlledEmailEligible: &eligible,
		PreferredInitial:        &preferred,
		PersonUnknown:           &unknown,
		DerivedFromFixture:      &notFixture,
		ProvenanceChainValid:    &ready,
		MailboxCompanyEvidence:  "OBSERVED",
		RiskClass:               "ALLOWED",
	})
	var d controlledDiscovery
	if err := json.Unmarshal(cand.DiscoveryJSON, &d); err != nil {
		t.Fatal(err)
	}
	if d.Source != "company_registry" || d.SourceType != "company_registry" {
		t.Fatalf("import dropped extra-cli registry source: %+v json=%s", d, cand.DiscoveryJSON)
	}
}
