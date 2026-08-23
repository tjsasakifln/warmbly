package confenge

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func freshSource(now time.Time) *FeedSourceFreshness {
	expected, fetched := 93, 93
	lag := 1.0
	return &FeedSourceFreshness{
		ContractVersion: AuthoritativeFreshnessContractV1,
		Status:          "FRESH", AsOf: now.Add(-time.Minute).Format(time.RFC3339Nano),
		ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano),
		RunID:     "pncp-run-1", PagesExpected: &expected, PagesFetched: &fetched,
		CurrentLagHours: &lag,
	}
}

func TestFeedPreservesAuthoritativeFreshnessAndRequiredPrepareFailsClosed(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	raw := `{"schema_version":"confenge.outreach.v1","generated_at":"2026-08-24T11:59:00Z","source":{"system":"extra-cli","run_id":"r1","snapshot_hash":"s1","authoritative_freshness":{"contract_version":"PNCP_CONTRACT_FRESHNESS/1.0","status":"FRESH","as_of":"2026-08-24T11:59:00Z","expires_at":"2026-08-24T13:00:00Z"}},"pagination":{},"leads":[]}`
	var feed Feed
	if err := json.Unmarshal([]byte(raw), &feed); err != nil {
		t.Fatal(err)
	}
	if feed.Source.AuthoritativeFreshness == nil || feed.Source.AuthoritativeFreshness.Status != "FRESH" {
		t.Fatalf("freshness discarded by parser: %+v", feed.Source)
	}
	if _, err := PrepareControlledCohort(nil, CohortPrepareOptions{Now: now, RequireAuthoritativeFreshness: true}); err == nil {
		t.Fatal("production prepare accepted a feed without authoritative freshness")
	}
}

func TestFrozenFreshnessExpiryAndTamperBlockAuthorizationAndTransport(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	snap := &FrozenCohortSnapshot{
		SchemaVersion:                FrozenCohortSchemaV1,
		Members:                      []FrozenCohortMember{{AccountRef: "acc-1", CandidateRef: "cand-1", Mailbox: "contact@example.com", RouteClass: RouteClassGenericCompany, ContentHash: "content", EvidenceHash: "evidence"}},
		AuthoritativeSourceFreshness: freshSource(now), AuthoritativeFreshnessRequired: true,
	}
	snap.AuthoritativeFreshnessHash = HashAuthoritativeSourceFreshness(snap.AuthoritativeSourceFreshness)
	snap.CohortHash = HashFrozenCohort(snap)
	if err := ValidateFrozenSourceFreshness(snap, now, true); err != nil {
		t.Fatalf("fresh source rejected: %v", err)
	}

	tampered := *snap
	f := *snap.AuthoritativeSourceFreshness
	tampered.AuthoritativeSourceFreshness = &f
	tampered.AuthoritativeSourceFreshness.RunID = "substituted-run"
	if err := ValidateFrozenSourceFreshness(&tampered, now, true); err == nil || !strings.Contains(err.Error(), "hash drift") {
		t.Fatalf("post-freeze freshness tamper accepted: %v", err)
	}

	if err := ValidateFrozenSourceFreshness(snap, now.Add(2*time.Hour), true); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired source accepted: %v", err)
	}
	auth := &BoundedCohortAuthorization{FrozenManifest: snap}
	reasons := ValidateBoundedCohortAuthorization(auth, CohortTransportInput{Now: now.Add(2 * time.Hour)})
	if !containsReason(reasons, "authoritative_source_freshness_invalid") {
		t.Fatalf("transport did not block expired source: %v", reasons)
	}
}

func containsReason(reasons []string, want string) bool {
	for _, reason := range reasons {
		if reason == want {
			return true
		}
	}
	return false
}
