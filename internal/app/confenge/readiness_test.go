package confenge

import (
	"testing"
	"time"
)

// A stale acquisition source must never revoke a commercially qualified
// population: feed age is acquisition health, qualification is commercial fact.
func TestReadinessStaleFeedKeepsCommercialQualification(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	staleAt := now.Add(-72 * time.Hour)
	cfg := Config{Enabled: true, FeedURL: "https://feed.internal/manifest.json", FeedMaxAge: 24 * time.Hour}

	readiness := BuildReadiness(cfg, ReadinessInputs{
		Now: now, LastImportAt: &staleAt, FeedSnapshot: "stale-snapshot",
		CommercialQualificationKnown: true, CommercialQualifiedCount: 412, CommercialUnknownCount: 27,
	})

	if readiness.FeedState != "stale" {
		t.Fatalf("acquisition health must still report staleness: %+v", readiness)
	}
	if readiness.CommercialQualificationState != CommercialQualified {
		t.Fatalf("stale feed downgraded a qualified population: %+v", readiness)
	}
	if readiness.CommercialQualifiedCount != 412 || !readiness.CommercialQualificationKnown {
		t.Fatalf("qualified readback not surfaced: %+v", readiness)
	}
}

func TestReadinessCommercialQualificationRollup(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	freshAt := now.Add(-time.Hour)
	cfg := Config{Enabled: true, FeedURL: "https://feed.internal/manifest.json", FeedMaxAge: 24 * time.Hour}

	for _, test := range []struct {
		name string
		in   ReadinessInputs
		want string
	}{
		{"readback unavailable stays unknown", ReadinessInputs{}, CommercialUnknown},
		{"qualified wins over expired", ReadinessInputs{
			CommercialQualificationKnown: true, CommercialQualifiedCount: 1, CommercialExpiredCount: 9,
		}, CommercialQualified},
		{"expired population", ReadinessInputs{
			CommercialQualificationKnown: true, CommercialExpiredCount: 3,
		}, CommercialExpired},
		{"revoked population", ReadinessInputs{
			CommercialQualificationKnown: true, CommercialRevokedCount: 3,
		}, CommercialRevoked},
		{"empty population", ReadinessInputs{
			CommercialQualificationKnown: true, CommercialUnknownCount: 5,
		}, CommercialUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			in := test.in
			in.Now = now
			in.LastImportAt = &freshAt
			readiness := BuildReadiness(cfg, in)
			if readiness.CommercialQualificationState != test.want {
				t.Fatalf("got %q want %q: %+v", readiness.CommercialQualificationState, test.want, readiness)
			}
			if readiness.FeedState != "fresh" {
				t.Fatalf("feed freshness must stay independent: %+v", readiness)
			}
		})
	}
}
