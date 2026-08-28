package repository

import (
	"os"
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
}
