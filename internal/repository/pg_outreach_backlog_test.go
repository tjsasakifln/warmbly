package repository

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestDelegatedEligibilityKeepsHTTPGateAndAllowsObservedRegistry(t *testing.T) {
	b, err := os.ReadFile("pg_outreach_backlog.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "c.source_url ~* '^https?://'") {
		t.Fatal("http(s) source_url remains required for web-attributed recipients")
	}
	if !strings.Contains(s, "company_registry") {
		t.Fatal("company_registry OBSERVED contacts must be delegated-eligible without inventing a URL")
	}
	if !strings.Contains(s, "CURRENT_DATE - 29") {
		t.Fatal("recipient freshness window must stay 29 days")
	}
	if !strings.Contains(s, "discovery_json->>'source'") || !strings.Contains(s, "discovery_json->>'source_type'") {
		t.Fatal("registry provenance must be read from discovery_json; outreach_contact_candidates has no source/source_type columns")
	}
	if !strings.Contains(s, "OFFICIAL_SOURCE") || !strings.Contains(s, "COALESCE(c.source_url,'')=''") {
		t.Fatal("already-imported company_registry ammo is OFFICIAL_SOURCE with empty source_url and must rematerialize without a new snapshot")
	}
	// c.source / c.source_type are not columns. source_url, source_date, source_document,
	// source_contact_id, source_run_id (on accounts) remain valid.
	if regexp.MustCompile(`c\.source[^_a-zA-Z]`).MatchString(s) {
		t.Fatal("do not reference missing column c.source; use discovery_json")
	}
	if regexp.MustCompile(`c\.source_type[^_a-zA-Z]`).MatchString(s) {
		t.Fatal("do not reference missing column c.source_type; use discovery_json")
	}
}

// Producer cadence must not dequeue proven work. Every superseded-run statement
// is scoped to accounts that actually lost COMMERCIAL_AUTHORITY/2.0.
func TestSupersededRetirementRequiresLostCommercialQualification(t *testing.T) {
	b, err := os.ReadFile("pg_outreach_backlog.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	head := s[:strings.Index(s, "CREATE TEMP TABLE confenge_current_initial_backlog")]
	superseded := strings.Count(head, "COALESCE(t.source_run_id,'')<>$2")
	if superseded != 4 {
		t.Fatalf("expected the four superseded-run statements, found %d", superseded)
	}
	if got := strings.Count(head, "supersededLostQualification"); got != 5 {
		t.Fatalf("qualification scope appears %d times; it must define once and gate all four statements", got)
	}
	if !strings.Contains(s, `confenge_commercially_qualified`) {
		t.Fatal("retirement must use the dynamic commercial fact, not a sticky state")
	}
	if strings.Count(head, "outreach_accounts a") != 4 {
		t.Fatal("all four superseded-run statements must join the account that owns the touchpoint")
	}
}
