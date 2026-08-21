package confenge

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestFrozenHashIncludesExpiryAndCriticalBindings(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	base := BoundedCohortAuthorization{
		ID: uuid.New(), ActorID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		AuthorizedAt: now, RepositorySHA: "sha-a", FeedSchemaVersion: "confenge.outreach.v1",
		CohortID: "c1", CohortHash: "h1", PolicyVersion: BoundedCohortPolicyV1,
		AllowedRouteClasses: []string{RouteClassGenericCompany, RouteClassDirectPerson},
		MaxDailyVolume:      50, RecipientSetHash: "r1", ComposerVersion: ComposerVersion,
		EvidenceVersion: DefaultEvidenceVersion, TTL: time.Hour, ExpiresAt: now.Add(time.Hour),
	}
	want := base.FrozenHash()
	if want == "" {
		t.Fatal("FrozenHash must not be empty")
	}
	if base.FrozenHash() != want {
		t.Fatal("FrozenHash must be deterministic")
	}

	mutate := []struct {
		name string
		edit func(*BoundedCohortAuthorization)
	}{
		{"actor", func(a *BoundedCohortAuthorization) { a.ActorID = uuid.New() }},
		{"sha", func(a *BoundedCohortAuthorization) { a.RepositorySHA = "sha-b" }},
		{"schema", func(a *BoundedCohortAuthorization) { a.FeedSchemaVersion = "other.schema" }},
		{"cohort_id", func(a *BoundedCohortAuthorization) { a.CohortID = "c2" }},
		{"cohort_hash", func(a *BoundedCohortAuthorization) { a.CohortHash = "h2" }},
		{"policy", func(a *BoundedCohortAuthorization) { a.PolicyVersion = "other-policy" }},
		{"classes", func(a *BoundedCohortAuthorization) {
			a.AllowedRouteClasses = []string{RouteClassRoleOrDepartment}
		}},
		{"volume", func(a *BoundedCohortAuthorization) { a.MaxDailyVolume = 25 }},
		{"recipients", func(a *BoundedCohortAuthorization) { a.RecipientSetHash = "r2" }},
		{"composer", func(a *BoundedCohortAuthorization) { a.ComposerVersion = "other-composer" }},
		{"evidence", func(a *BoundedCohortAuthorization) { a.EvidenceVersion = "other-evidence" }},
		{"expires", func(a *BoundedCohortAuthorization) { a.ExpiresAt = now.Add(2 * time.Hour) }},
		{"ttl", func(a *BoundedCohortAuthorization) { a.TTL = 2 * time.Hour }},
	}
	for _, tc := range mutate {
		t.Run(tc.name, func(t *testing.T) {
			cp := base
			cp.AllowedRouteClasses = append([]string{}, base.AllowedRouteClasses...)
			tc.edit(&cp)
			got := cp.FrozenHash()
			if got == want {
				t.Fatalf("changing %s must change FrozenHash", tc.name)
			}
		})
	}
}

func TestChangingTTLAfterAuthorizeInvalidatesGrantHash(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	auth := BoundedCohortAuthorization{
		ID: uuid.New(), ActorID: uuid.New(), AuthorizedAt: now,
		RepositorySHA: "sha", FeedSchemaVersion: "v", CohortID: "c", CohortHash: "h",
		PolicyVersion: "p", AllowedRouteClasses: []string{RouteClassGenericCompany},
		MaxDailyVolume: 10, RecipientSetHash: "r", ComposerVersion: "c", EvidenceVersion: "e",
		TTL: time.Hour, ExpiresAt: now.Add(time.Hour),
	}
	bound := auth.FrozenHash()
	auth.TTL = 48 * time.Hour
	if auth.FrozenHash() == bound {
		t.Fatal("TTL change after authorize must invalidate frozen binding")
	}
	auth.TTL = time.Hour
	auth.ExpiresAt = now.Add(48 * time.Hour)
	if auth.FrozenHash() == bound {
		t.Fatal("ExpiresAt change after authorize must invalidate frozen binding")
	}
}
