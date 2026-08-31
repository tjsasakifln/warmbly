package confenge

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/warmbly/warmbly/internal/models"
)

// Verified values from the real extra-cli production manifest
// (confenge.outreach.manifest.v1, 2026-08-26). source.run_id is a
// content-derived feed BUILD id; the freshness run_id is a PNCP INGESTION
// attempt id. They live in disjoint namespaces and can never be string-equal.
const (
	prodSnapshotHash  = "1c507558ec0e803bd2344d565f5adcb541375778b1b247b47e152470f024402a"
	prodProfileID     = "confenge"
	prodProfileVer    = "2.0.0"
	prodModuleVersion = "1.1.1"
	prodFreshnessHash = "c26a18a410c5f6d3654733a7485db38b7eafbfc77163b630edfa8e63614a91c0"
	prodSourceRunID   = "run-25d918a5801fa976"
	prodIngestRunID   = "contracts-90d-20260826T230341Z-5ca7f36505"
)

func freshSource(now time.Time) *FeedSourceFreshness {
	expected, fetched := 93, 93
	lag := 1.0
	return &FeedSourceFreshness{
		ContractVersion: AuthoritativeFreshnessContractV1,
		Status:          "FRESH", AsOf: now.Add(-time.Minute).Format(time.RFC3339Nano),
		ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano),
		RunID:     prodIngestRunID, PagesExpected: &expected, PagesFetched: &fetched,
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

// productionManifest models what the real producer actually emits: the same
// freshness object published twice (top level and nested under source), a
// producer content hash of it, and a source.run_id that is a commitment over
// (snapshot_hash, profile_id, profile_version, module_version, freshness_hash).
func productionManifest(now time.Time) *outreachManifest {
	top := freshSource(now)
	nested := *top
	manifest := &outreachManifest{
		SchemaVersion:    "confenge.outreach.manifest.v1",
		ModuleVersion:    prodModuleVersion,
		SourceFreshness:  top,
		TargetMembership: completeTargetMembership(),
	}
	manifest.Source.System = "extra-cli"
	manifest.Source.RunID = prodSourceRunID
	manifest.Source.SnapshotHash = prodSnapshotHash
	manifest.Source.ProfileID = prodProfileID
	manifest.Source.ProfileVer = prodProfileVer
	manifest.Source.Freshness = &nested
	manifest.Source.FreshnessHash = prodFreshnessHash
	return manifest
}

// withFreshness returns a shallow manifest copy whose top-level and nested
// freshness blocks are independently mutable copies of the originals.
func withFreshness(manifest *outreachManifest) (*outreachManifest, *FeedSourceFreshness, *FeedSourceFreshness) {
	clone := *manifest
	top := *manifest.SourceFreshness
	nested := *manifest.Source.Freshness
	clone.SourceFreshness = &top
	clone.Source.Freshness = &nested
	return &clone, &top, &nested
}

func rejects(t *testing.T, manifest *outreachManifest, now time.Time, wantSubstring, label string) {
	t.Helper()
	authority, err := validateManifestAuthority(manifest, now, true)
	if err == nil {
		t.Fatalf("%s: accepted, authority=%+v", label, authority)
	}
	if !strings.Contains(err.Error(), wantSubstring) {
		t.Fatalf("%s: error %q does not contain %q", label, err.Error(), wantSubstring)
	}
	if authority != nil {
		t.Fatalf("%s: rejected but still returned authority %+v", label, authority)
	}
}

func TestManifestAuthorityRequiresContemporaryFreshnessAndCompleteMembership(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	manifest := productionManifest(now)
	authority, err := validateManifestAuthority(manifest, now, true)
	if err != nil || authority == nil || authority.TargetMembershipCount != 10 || authority.SupplierConfirmedCount != 8 {
		t.Fatalf("valid authority rejected: authority=%+v err=%v", authority, err)
	}

	missingMembership := *manifest
	missingMembership.TargetMembership = nil
	rejects(t, &missingMembership, now, "membership missing", "missing membership")

	expired, top, _ := withFreshness(manifest)
	top.ExpiresAt = now.Format(time.RFC3339Nano)
	rejects(t, expired, now, "expired", "expired source")

	notFresh, top, _ := withFreshness(manifest)
	top.Status = "STALE"
	rejects(t, notFresh, now, "STALE", "non-FRESH source")

	futureDated, top, _ := withFreshness(manifest)
	futureDated.SourceFreshness.AsOf = now.Add(time.Hour).Format(time.RFC3339Nano)
	rejects(t, futureDated, now, "future-dated", "future-dated source")

	badContract, top, _ := withFreshness(manifest)
	top.ContractVersion = "PNCP_CONTRACT_FRESHNESS/0.9"
	rejects(t, badContract, now, "contract unsupported", "unsupported freshness contract")

	shortPages, top, _ := withFreshness(manifest)
	fetched := 12
	top.PagesFetched = &fetched
	rejects(t, shortPages, now, "pagination incomplete", "incomplete pagination")

	incomplete := *manifest
	incompleteMembership := *manifest.TargetMembership
	incompleteMembership.MembershipComplete = false
	incomplete.TargetMembership = &incompleteMembership
	rejects(t, &incomplete, now, "membership_complete", "incomplete membership")

	badMembershipContract := *manifest
	badSchema := *manifest.TargetMembership
	badSchema.SchemaVersion = "confenge.target_membership.v0"
	badMembershipContract.TargetMembership = &badSchema
	rejects(t, &badMembershipContract, now, "membership contract unsupported", "unsupported membership contract")

	badCounts := *manifest
	skewed := *manifest.TargetMembership
	skewed.TargetConfirmedCount = 9
	badCounts.TargetMembership = &skewed
	rejects(t, &badCounts, now, "membership counts are invalid", "skewed membership counts")

	badMembershipHash := *manifest
	badHash := *manifest.TargetMembership
	badHash.MembershipHash = "not-a-sha256"
	badMembershipHash.TargetMembership = &badHash
	rejects(t, &badMembershipHash, now, "membership_hash is invalid", "invalid membership hash")
}

// TestManifestAuthorityBindsFreshnessToProducerRunDerivation pins the real
// binding. The previous gate compared source.run_id (a feed BUILD id from
// export.py::_run_id) to authoritative_source_freshness.run_id (a PNCP
// INGESTION attempt id from run_evidence.py::new_run_id) — disjoint namespaces,
// so it could never pass. The binding below is cryptographic instead.
func TestManifestAuthorityBindsFreshnessToProducerRunDerivation(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)

	derived := deriveFeedBuildRunID(prodSnapshotHash, prodProfileID, prodProfileVer, prodModuleVersion, prodFreshnessHash)
	if derived != prodSourceRunID {
		t.Fatalf("producer run-id derivation drifted: derived=%q want=%q", derived, prodSourceRunID)
	}
	if prodSourceRunID == prodIngestRunID {
		t.Fatal("fixture no longer models disjoint build/ingestion namespaces")
	}

	manifest := productionManifest(now)
	authority, err := validateManifestAuthority(manifest, now, true)
	if err != nil || authority == nil {
		t.Fatalf("real production manifest values rejected: authority=%+v err=%v", authority, err)
	}
	if authority.SourceFreshnessHash != HashAuthoritativeSourceFreshness(manifest.SourceFreshness) {
		t.Fatalf("authority did not carry the freshness hash: %+v", authority)
	}

	// Top-level freshness swapped for another ingestion run, nested left alone.
	substitutedTop, top, _ := withFreshness(manifest)
	top.RunID = "contracts-90d-20260825T010101Z-deadbeef01"
	rejects(t, substitutedTop, now, "freshness block was substituted", "substituted top-level freshness")

	// Nested freshness swapped, top-level left alone.
	substitutedNested, _, nested := withFreshness(manifest)
	nested.RunID = "contracts-90d-20260825T010101Z-deadbeef01"
	rejects(t, substitutedNested, now, "freshness block was substituted", "substituted nested freshness")

	emptyNestedRun, _, nested := withFreshness(manifest)
	nested.RunID = ""
	rejects(t, emptyNestedRun, now, "run_id is empty", "empty nested freshness run_id")

	emptyTopRun, top, _ := withFreshness(manifest)
	top.RunID = ""
	rejects(t, emptyTopRun, now, "run_id is empty", "empty top-level freshness run_id")

	// Well-formed sha256, wrong value: the run-id commitment must not reproduce.
	tamperedHash := *manifest
	tamperedHash.Source.FreshnessHash = strings.Repeat("b", 64)
	rejects(t, &tamperedHash, now, "does not recompute", "tampered authoritative_freshness_hash")

	malformedHash := *manifest
	malformedHash.Source.FreshnessHash = "not-a-sha256"
	rejects(t, &malformedHash, now, "not a valid sha256", "malformed authoritative_freshness_hash")

	missingHash := *manifest
	missingHash.Source.FreshnessHash = ""
	rejects(t, &missingHash, now, "not a valid sha256", "missing authoritative_freshness_hash")

	uppercaseHash := *manifest
	uppercaseHash.Source.FreshnessHash = strings.ToUpper(prodFreshnessHash)
	rejects(t, &uppercaseHash, now, "lowercase hex", "uppercase authoritative_freshness_hash")

	missingModuleVersion := *manifest
	missingModuleVersion.ModuleVersion = ""
	rejects(t, &missingModuleVersion, now, "module_version is required", "missing module_version")

	wrongModuleVersion := *manifest
	wrongModuleVersion.ModuleVersion = "1.1.0"
	rejects(t, &wrongModuleVersion, now, "does not recompute", "module_version drift")

	missingNested := *manifest
	missingNested.Source.Freshness = nil
	rejects(t, &missingNested, now, "source.authoritative_freshness block is missing", "missing nested freshness block")

	tamperedSnapshot := *manifest
	tamperedSnapshot.Source.SnapshotHash = strings.Repeat("c", 64)
	rejects(t, &tamperedSnapshot, now, "does not recompute", "tampered snapshot_hash")

	tamperedProfile := *manifest
	tamperedProfile.Source.ProfileVer = "1.0.0"
	rejects(t, &tamperedProfile, now, "does not recompute", "tampered profile_version")

	// The old, impossible predicate must not be resurrected: a manifest whose
	// source.run_id was forced equal to the ingestion run id is a forgery.
	oldStyleEquality := *manifest
	oldStyleEquality.Source.RunID = prodIngestRunID
	rejects(t, &oldStyleEquality, now, "does not recompute", "build run_id forced equal to ingestion run_id")
}

func TestHashStagedTargetMembershipUsesSortedUniqueRoots(t *testing.T) {
	got, count, err := hashStagedTargetMembership(map[string]string{
		"99888777000166": "chunk-2", "11222333000144": "chunk-1",
	})
	if err != nil || count != 2 {
		t.Fatalf("membership hash failed: hash=%q count=%d err=%v", got, count, err)
	}
	want := hashText("11222333\n99888777\n")
	if got != want {
		t.Fatalf("membership hash=%q want %q", got, want)
	}
	if _, _, err := hashStagedTargetMembership(map[string]string{
		"11222333000144": "chunk-1", "11222333000225": "chunk-2",
	}); err == nil || !strings.Contains(err.Error(), "duplicate CNPJ roots") {
		t.Fatalf("duplicate root accepted: %v", err)
	}
}

// The ingest gate still fails closed on an expired producer window, but the
// STRUCTURE of the same snapshot stays valid: an intact membership does not
// dissolve because the crawler window closed.
func TestAuthoritativeFeedAgeIsIngestOnlyAndStructureSurvivesExpiry(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	generatedAt := now.Add(-time.Hour)
	expiresAt := now.Add(-time.Second)
	state := &models.OutreachFeedSyncState{
		LastStatus: "completed", LastSnapshotHash: "snapshot", LastRunID: "run",
		LastSuccessAt: &now, SourceGeneratedAt: &generatedAt, SourceExpiresAt: &expiresAt,
		SourceFreshnessHash: strings.Repeat("a", 64), TargetMembershipComplete: true,
		TargetMembershipHash: strings.Repeat("b", 64), TargetMembershipCount: 10,
		SupplierConfirmedCount: 8,
	}
	// Ingest of NEW facts still demands a live producer window.
	if err := validateAuthoritativeFeedState(state, now, 24*time.Hour, true); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("ingest accepted an expired producer window: %v", err)
	}
	// The commercial path asks only whether the snapshot is whole.
	if err := validateAuthoritativeFeedStructure(state, true); err != nil {
		t.Fatalf("expired producer window dissolved an intact snapshot: %v", err)
	}
	if err := validateAuthoritativeFeedAge(state, now, 24*time.Hour); err == nil {
		t.Fatal("age check stopped reporting an expired producer window")
	}
	// Age is reported, never fatal, once the window is merely old.
	stale := generatedAt.Add(-72 * time.Hour)
	state.SourceGeneratedAt = &stale
	if err := validateAuthoritativeFeedStructure(state, true); err != nil {
		t.Fatalf("a three-day-old snapshot lost structural validity: %v", err)
	}
}

func TestAuthoritativeFeedStructureUsesLastGoodNotLatestAttemptStatus(t *testing.T) {
	now := time.Now().UTC()
	expiresAt := now.Add(time.Hour)
	state := &models.OutreachFeedSyncState{
		LastStatus: "partial", LastSuccessAt: &now, LastAttemptAt: &now,
		LastSnapshotHash: "snapshot", LastRunID: "run", SourceGeneratedAt: &now,
		SourceExpiresAt: &expiresAt, SourceFreshnessHash: strings.Repeat("a", 64),
		TargetMembershipComplete: true, TargetMembershipHash: strings.Repeat("b", 64),
		TargetMembershipCount: 2, SupplierConfirmedCount: 1,
	}
	if err := validateAuthoritativeFeedStructure(state, true); err != nil {
		t.Fatalf("partial retry erased the last-good snapshot: %v", err)
	}
	state.LastSuccessAt = nil
	if err := validateAuthoritativeFeedStructure(state, true); err == nil {
		t.Fatal("feed with no successful snapshot passed the structural gate")
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

	// A frozen cohort is already-proven work. The producer window closing does
	// NOT un-prove it; only tamper does.
	if err := ValidateFrozenSourceFreshness(snap, now.Add(2*time.Hour), true); err != nil {
		t.Fatalf("a closed producer window revoked already-frozen work: %v", err)
	}
	auth := &BoundedCohortAuthorization{FrozenManifest: snap}
	reasons := ValidateBoundedCohortAuthorization(auth, CohortTransportInput{Now: now.Add(2 * time.Hour)})
	if containsReason(reasons, "authoritative_source_freshness_invalid") {
		t.Fatalf("transport blocked a frozen cohort purely on source age: %v", reasons)
	}
	// A snapshot that was never FRESH at publication is still refused.
	neverFresh := *snap
	nf := *snap.AuthoritativeSourceFreshness
	nf.Status = "STALE"
	neverFresh.AuthoritativeSourceFreshness = &nf
	neverFresh.AuthoritativeFreshnessHash = HashAuthoritativeSourceFreshness(&nf)
	neverFresh.CohortHash = HashFrozenCohort(&neverFresh)
	if err := ValidateFrozenSourceFreshness(&neverFresh, now, true); err == nil {
		t.Fatal("a snapshot never proven FRESH at publication was accepted")
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
