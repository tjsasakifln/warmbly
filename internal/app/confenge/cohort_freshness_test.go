package confenge

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/warmbly/warmbly/internal/models"
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

func completeTargetMembership() *authoritativeTargetMembership {
	return &authoritativeTargetMembership{
		SchemaVersion: targetMembershipSchemaV1, IdentityKey: targetMembershipIdentity,
		HashAlgorithm: targetMembershipAlgorithm, PopulationCount: 10,
		MembershipHash: strings.Repeat("a", 64), TargetConfirmedCount: 10,
		SupplierConfirmedCount: 8, SourceMemberCount: 10, MembershipComplete: true,
		TargetFitClass: TargetFitConfirmed,
	}
}

func TestManifestAuthorityRequiresContemporaryFreshnessAndCompleteMembership(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	manifest := &outreachManifest{SourceFreshness: freshSource(now), TargetMembership: completeTargetMembership()}
	authority, err := validateManifestAuthority(manifest, now, true)
	if err != nil || authority == nil || authority.TargetMembershipCount != 10 || authority.SupplierConfirmedCount != 8 {
		t.Fatalf("valid authority rejected: authority=%+v err=%v", authority, err)
	}

	missingMembership := *manifest
	missingMembership.TargetMembership = nil
	if _, err := validateManifestAuthority(&missingMembership, now, true); err == nil || !strings.Contains(err.Error(), "membership missing") {
		t.Fatalf("missing membership accepted: %v", err)
	}

	expired := *manifest
	expiredFreshness := *manifest.SourceFreshness
	expiredFreshness.ExpiresAt = now.Format(time.RFC3339Nano)
	expired.SourceFreshness = &expiredFreshness
	if _, err := validateManifestAuthority(&expired, now, true); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired source accepted: %v", err)
	}

	incomplete := *manifest
	incompleteMembership := *manifest.TargetMembership
	incompleteMembership.MembershipComplete = false
	incomplete.TargetMembership = &incompleteMembership
	if _, err := validateManifestAuthority(&incomplete, now, true); err == nil || !strings.Contains(err.Error(), "membership_complete") {
		t.Fatalf("incomplete membership accepted: %v", err)
	}
}

func TestAuthoritativeFeedStateExpiryOverridesGenericMaxAge(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	generatedAt := now.Add(-time.Hour)
	expiresAt := now.Add(-time.Second)
	state := &models.OutreachFeedSyncState{
		LastStatus: "completed", LastSnapshotHash: "snapshot", LastRunID: "run",
		SourceGeneratedAt: &generatedAt, SourceExpiresAt: &expiresAt,
		SourceFreshnessHash: strings.Repeat("a", 64), TargetMembershipComplete: true,
		TargetMembershipHash: strings.Repeat("b", 64), TargetMembershipCount: 10,
		SupplierConfirmedCount: 8,
	}
	if err := validateAuthoritativeFeedState(state, now, 24*time.Hour, false); err != nil {
		t.Fatalf("legacy max-age state unexpectedly rejected: %v", err)
	}
	if err := validateAuthoritativeFeedState(state, now, 24*time.Hour, true); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("producer expiry did not dominate delegated gate: %v", err)
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
