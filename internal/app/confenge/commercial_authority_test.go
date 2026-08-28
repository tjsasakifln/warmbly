package confenge

import (
	"strings"
	"testing"
	"time"
)

func authorityBool(v bool) *bool { return &v }

func testAuthorityPayload(state string, now time.Time) FeedCommercialAuthority {
	validated := now.Add(-time.Hour)
	switch state {
	case CommercialAuthorityDegraded:
		validated = now.Add(-25 * time.Hour)
	case CommercialAuthorityFrozenForNewAdmission:
		validated = now.Add(-73 * time.Hour)
	case CommercialAuthorityExpired:
		validated = now.Add(-7*24*time.Hour - time.Second)
	}
	return FeedCommercialAuthority{
		SchemaVersion:                      CommercialAuthoritySchemaV1,
		Schema:                             "COMMERCIAL_AUTHORITY/1.0",
		BasisSourceRunID:                   "run-abc",
		BasisSnapshotHash:                  "snap-abc",
		BasisMembershipHash:                strings.Repeat("a", 64),
		BasisPublicationSemanticHash:       strings.Repeat("s", 64),
		ProducerIdentity:                   strings.Repeat("p", 64),
		ValidatedAt:                        validated.Format(time.RFC3339Nano),
		ValidUntil:                         now.Add(24 * time.Hour).Format(time.RFC3339Nano),
		State:                              state,
		NewAdmissionAllowed:                authorityBool(state == CommercialAuthorityCurrent || state == CommercialAuthorityDegraded),
		ExistingBoundTouchTransportAllowed: authorityBool(state != CommercialAuthorityExpired),
	}
}

func testBinding() CommercialAuthorityBinding {
	return CommercialAuthorityBinding{
		SourceRunID:             "run-abc",
		SnapshotHash:            "snap-abc",
		MembershipHash:          strings.Repeat("a", 64),
		PublicationSemanticHash: strings.Repeat("s", 64),
		ProducerIdentity:        strings.Repeat("p", 64),
	}
}

func testFreshSource(now time.Time) *FeedSourceFreshness {
	return &FeedSourceFreshness{
		ContractVersion: AuthoritativeFreshnessContractV1,
		Status:          SourceHealthFresh,
		AsOf:            now.Add(-time.Minute).Format(time.RFC3339Nano),
		ExpiresAt:       now.Add(2 * time.Hour).Format(time.RFC3339Nano),
	}
}

func TestEvaluateCommercialAuthorityMatrix(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	binding := testBinding()
	fresh := testFreshSource(now)
	active := TransportState{State: TransportActive}

	t.Run("absent_strict_fallback", func(t *testing.T) {
		got := EvaluateOutboundEligibility(fresh, nil, binding, active, now, 24*time.Hour, nil, false)
		if got.CommercialAuthority.Present || got.CommercialAuthority.State != CommercialAuthorityAbsent {
			t.Fatalf("absent: %+v", got.CommercialAuthority)
		}
		if got.SourceHealth.State != SourceHealthFresh {
			t.Fatalf("source=%s", got.SourceHealth.State)
		}
		if !got.AllowNewAdmission || !got.AllowExistingBoundTouchTransport {
			t.Fatalf("strict fresh should admit: %+v", got)
		}
		stale := ClassifySourceHealth(&FeedSourceFreshness{
			ContractVersion: AuthoritativeFreshnessContractV1, Status: SourceHealthStale,
			AsOf: now.Add(-48 * time.Hour).Format(time.RFC3339Nano), ExpiresAt: now.Add(-time.Hour).Format(time.RFC3339Nano),
		}, now, 24*time.Hour)
		hold := EvaluateOutboundEligibility(&FeedSourceFreshness{
			ContractVersion: AuthoritativeFreshnessContractV1, Status: SourceHealthStale,
			AsOf: now.Add(-48 * time.Hour).Format(time.RFC3339Nano), ExpiresAt: now.Add(-time.Hour).Format(time.RFC3339Nano),
		}, nil, binding, active, now, 24*time.Hour, nil, true)
		if stale.State != SourceHealthStale || hold.AllowNewAdmission || hold.AllowExistingBoundTouchTransport {
			t.Fatalf("absent+stale must fail closed without deactivating by itself: source=%+v elig=%+v", stale, hold)
		}
	})

	t.Run("CURRENT", func(t *testing.T) {
		p := testAuthorityPayload(CommercialAuthorityCurrent, now)
		got := EvaluateCommercialAuthority(&p, binding, now)
		if !got.Present || got.State != CommercialAuthorityCurrent || !got.NewAdmissionAllowed || !got.ExistingBoundTouchTransportAllowed {
			t.Fatalf("%+v", got)
		}
	})

	t.Run("DEGRADED", func(t *testing.T) {
		p := testAuthorityPayload(CommercialAuthorityDegraded, now)
		got := EvaluateCommercialAuthority(&p, binding, now)
		if got.State != CommercialAuthorityDegraded || !got.NewAdmissionAllowed {
			t.Fatalf("%+v", got)
		}
	})

	t.Run("FROZEN_FOR_NEW_ADMISSION", func(t *testing.T) {
		p := testAuthorityPayload(CommercialAuthorityFrozenForNewAdmission, now)
		p.NewAdmissionAllowed = authorityBool(false)
		p.ExistingBoundTouchTransportAllowed = authorityBool(true)
		elig := EvaluateOutboundEligibility(fresh, &p, binding, active, now, 24*time.Hour, nil, true)
		if elig.AllowNewAdmission || !elig.AllowExistingBoundTouchTransport {
			t.Fatalf("frozen: %+v", elig)
		}
		if elig.SourceHealth.State != SourceHealthFresh {
			t.Fatalf("source masked: %s", elig.SourceHealth.State)
		}
	})

	t.Run("EXPIRED", func(t *testing.T) {
		p := testAuthorityPayload(CommercialAuthorityExpired, now)
		got := EvaluateCommercialAuthority(&p, binding, now)
		if got.State != CommercialAuthorityExpired || got.NewAdmissionAllowed || got.ExistingBoundTouchTransportAllowed {
			t.Fatalf("%+v", got)
		}
	})

	t.Run("binding_mismatch", func(t *testing.T) {
		p := testAuthorityPayload(CommercialAuthorityCurrent, now)
		got := EvaluateCommercialAuthority(&p, CommercialAuthorityBinding{SourceRunID: "run-other", SnapshotHash: "snap-abc", MembershipHash: strings.Repeat("a", 64)}, now)
		if got.State != CommercialAuthorityUnknown || got.NewAdmissionAllowed {
			t.Fatalf("%+v", got)
		}
	})

	t.Run("explicit_deactivation_always_blocks", func(t *testing.T) {
		p := testAuthorityPayload(CommercialAuthorityCurrent, now)
		elig := EvaluateOutboundEligibility(fresh, &p, binding, active, now, 24*time.Hour, []string{"explicit_deactivation"}, true)
		if elig.AllowNewAdmission || elig.AllowExistingBoundTouchTransport {
			t.Fatalf("%+v", elig)
		}
	})

	t.Run("recipient_expired_always_blocks", func(t *testing.T) {
		p := testAuthorityPayload(CommercialAuthorityCurrent, now)
		elig := EvaluateOutboundEligibility(fresh, &p, binding, active, now, 24*time.Hour, []string{"recipient_expired"}, false)
		if elig.AllowNewAdmission || elig.AllowExistingBoundTouchTransport {
			t.Fatalf("%+v", elig)
		}
	})

	t.Run("party_role_conflict_always_blocks", func(t *testing.T) {
		p := testAuthorityPayload(CommercialAuthorityCurrent, now)
		elig := EvaluateOutboundEligibility(fresh, &p, binding, active, now, 24*time.Hour, []string{"party_role_conflict"}, true)
		if elig.AllowExistingBoundTouchTransport {
			t.Fatalf("%+v", elig)
		}
	})

	t.Run("suppression_after_approval_always_blocks", func(t *testing.T) {
		p := testAuthorityPayload(CommercialAuthorityCurrent, now)
		elig := EvaluateOutboundEligibility(fresh, &p, binding, active, now, 24*time.Hour, []string{"suppression"}, true)
		if elig.AllowExistingBoundTouchTransport {
			t.Fatalf("%+v", elig)
		}
	})

	t.Run("source_stale_bound_still_authorized", func(t *testing.T) {
		p := testAuthorityPayload(CommercialAuthorityFrozenForNewAdmission, now)
		p.ExistingBoundTouchTransportAllowed = authorityBool(true)
		stale := &FeedSourceFreshness{
			ContractVersion: AuthoritativeFreshnessContractV1, Status: SourceHealthStale,
			AsOf:      now.Add(-48 * time.Hour).Format(time.RFC3339Nano),
			ExpiresAt: now.Add(-time.Hour).Format(time.RFC3339Nano),
		}
		elig := EvaluateOutboundEligibility(stale, &p, binding, active, now, 24*time.Hour, nil, true)
		if elig.SourceHealth.State != SourceHealthStale {
			t.Fatalf("source should stay stale: %+v", elig.SourceHealth)
		}
		if elig.AllowNewAdmission {
			t.Fatal("stale source must not mint new admissions")
		}
		if !elig.AllowExistingBoundTouchTransport {
			t.Fatalf("bound touch should remain authorized: %+v", elig)
		}
		if elig.CommercialAuthority.State != CommercialAuthorityFrozenForNewAdmission {
			t.Fatalf("commercial masked by source: %+v", elig.CommercialAuthority)
		}
	})

	t.Run("new_membership_does_not_inherit", func(t *testing.T) {
		p := testAuthorityPayload(CommercialAuthorityCurrent, now)
		got := EvaluateCommercialAuthority(&p, CommercialAuthorityBinding{
			SourceRunID: "run-abc", SnapshotHash: "snap-abc", MembershipHash: strings.Repeat("b", 64),
		}, now)
		if got.State != CommercialAuthorityUnknown || got.NewAdmissionAllowed {
			t.Fatalf("similar membership inherited: %+v", got)
		}
	})
}

func TestRecognizeFirstTouchPolicyExact(t *testing.T) {
	known, hold, reason := RecognizeFirstTouchPolicy(DelegatedFirstTouchPolicyV1)
	if !known || hold || reason != "" {
		t.Fatalf("v1: known=%v hold=%v reason=%s", known, hold, reason)
	}
	known, hold, reason = RecognizeFirstTouchPolicy(DelegatedFirstTouchPolicyV2)
	if !known || hold || reason != "" {
		t.Fatalf("v2: known=%v hold=%v reason=%s", known, hold, reason)
	}
	for _, name := range []string{"CFG-FIRST-TOUCH-ROUTING", "CFG-FIRST-TOUCH-ROUTING-v1-beta", "CFG-FIRST-TOUCH-ROUTING-v10", ""} {
		known, hold, reason = RecognizeFirstTouchPolicy(name)
		if known || !hold || reason != ReasonPolicyUnknown {
			t.Fatalf("fuzzy %q accepted: known=%v hold=%v reason=%s", name, known, hold, reason)
		}
	}
}

func TestClassifySourceHealthIndependentOfCommercial(t *testing.T) {
	now := time.Date(2026, 8, 27, 15, 0, 0, 0, time.UTC)
	fresh := ClassifySourceHealth(testFreshSource(now), now, 24*time.Hour)
	if fresh.State != SourceHealthFresh {
		t.Fatalf("%+v", fresh)
	}
	degraded := ClassifySourceHealth(&FeedSourceFreshness{
		ContractVersion: AuthoritativeFreshnessContractV1, Status: SourceHealthDegraded,
		AsOf: now.Add(-time.Hour).Format(time.RFC3339Nano), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano),
	}, now, 24*time.Hour)
	if degraded.State != SourceHealthDegraded {
		t.Fatalf("%+v", degraded)
	}
	if EvaluateCommercialAuthority(nil, testBinding(), now).Present {
		t.Fatal("source health must not invent commercial authority")
	}
}

func TestManifestAuthorityAllowsDegradedSourceWhenCommercialAuthorityBinds(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	manifest := productionManifest(now)
	top, nested := manifest.SourceFreshness, manifest.Source.Freshness
	top.Status = SourceHealthDegraded
	nested.Status = SourceHealthDegraded
	manifest.Source.FreshnessHash = HashAuthoritativeSourceFreshness(top)
	manifest.Source.RunID = deriveFeedBuildRunID(manifest.Source.SnapshotHash, manifest.Source.ProfileID, manifest.Source.ProfileVer, manifest.ModuleVersion, manifest.Source.FreshnessHash)
	payload := FeedCommercialAuthority{
		SchemaVersion:                      CommercialAuthoritySchemaV1,
		BasisSourceRunID:                   manifest.Source.RunID,
		BasisSnapshotHash:                  manifest.Source.SnapshotHash,
		BasisMembershipHash:                manifest.TargetMembership.MembershipHash,
		BasisPublicationSemanticHash:       strings.Repeat("s", 64),
		ProducerIdentity:                   strings.Repeat("p", 64),
		ValidatedAt:                        now.Add(-25 * time.Hour).Format(time.RFC3339Nano),
		ValidUntil:                         now.Add(time.Hour).Format(time.RFC3339Nano),
		State:                              CommercialAuthorityDegraded,
		NewAdmissionAllowed:                authorityBool(true),
		ExistingBoundTouchTransportAllowed: authorityBool(true),
	}
	manifest.CommercialAuthority = &payload
	manifest.ProducerIdentity = strings.Repeat("p", 64)
	manifest.PublicationSemanticHash = strings.Repeat("s", 64)
	authority, err := validateManifestAuthority(manifest, now, true)
	if err != nil || authority == nil {
		t.Fatalf("degraded source with bound commercial authority rejected: %+v err=%v", authority, err)
	}
}

func TestCommercialAuthorityDoesNotHardcodeSevenDays(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	p := testAuthorityPayload(CommercialAuthorityCurrent, now)
	p.ValidUntil = now.Add(30 * time.Minute).Format(time.RFC3339Nano)
	if got := EvaluateCommercialAuthority(&p, testBinding(), now); got.State != CommercialAuthorityCurrent {
		t.Fatalf("live CURRENT rejected because of a stale snapshot valid_until: %+v", got)
	}
	if got := EvaluateCommercialAuthority(&p, testBinding(), now.Add(31*time.Minute)); got.State != CommercialAuthorityCurrent {
		t.Fatalf("static 30m valid_until revoked a still-current population: %+v", got)
	}
	p.WindowsHours = &CommercialAuthorityWindows{CurrentMaxHours: 1, DegradedMaxHours: 2, FrozenMaxHours: 3}
	p.ValidatedAt = now.Add(-90 * time.Minute).Format(time.RFC3339Nano)
	if got := EvaluateCommercialAuthority(&p, testBinding(), now); got.State != CommercialAuthorityDegraded {
		t.Fatalf("payload windows ignored: %+v", got)
	}
}

func TestOneByteProducerBindingDriftFailsClosed(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	p := testAuthorityPayload(CommercialAuthorityCurrent, now)
	binding := testBinding()
	if got := EvaluateCommercialAuthority(&p, binding, now); got.State != CommercialAuthorityCurrent {
		t.Fatalf("happy path: %+v", got)
	}
	flip := func(value string) string { return value[:len(value)-1] + "b" }
	cases := []CommercialAuthorityBinding{
		{SourceRunID: binding.SourceRunID, SnapshotHash: binding.SnapshotHash, MembershipHash: flip(binding.MembershipHash), PublicationSemanticHash: binding.PublicationSemanticHash, ProducerIdentity: binding.ProducerIdentity},
		{SourceRunID: binding.SourceRunID, SnapshotHash: binding.SnapshotHash, MembershipHash: binding.MembershipHash, PublicationSemanticHash: flip(binding.PublicationSemanticHash), ProducerIdentity: binding.ProducerIdentity},
		{SourceRunID: binding.SourceRunID, SnapshotHash: binding.SnapshotHash, MembershipHash: binding.MembershipHash, PublicationSemanticHash: binding.PublicationSemanticHash, ProducerIdentity: flip(binding.ProducerIdentity)},
	}
	for i, drifted := range cases {
		got := EvaluateCommercialAuthority(&p, drifted, now)
		if got.State != CommercialAuthorityUnknown || got.NewAdmissionAllowed {
			t.Fatalf("case %d did not fail closed: %+v", i, got)
		}
	}
}

func TestLosslessAliasFillsCanonicalAndConflictFailsClosed(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	p := testAuthorityPayload(CommercialAuthorityCurrent, now)
	p.SourceRunIDAlias = p.BasisSourceRunID
	p.SnapshotIDAlias = p.BasisSnapshotHash
	p.MembershipHashAlias = p.BasisMembershipHash
	if got := EvaluateCommercialAuthority(&p, testBinding(), now); got.State != CommercialAuthorityCurrent {
		t.Fatalf("matching aliases rejected: %+v", got)
	}
	p.SourceRunIDAlias = "run-other"
	if got := EvaluateCommercialAuthority(&p, testBinding(), now); got.State != CommercialAuthorityUnknown {
		t.Fatalf("alias conflict accepted: %+v", got)
	}
}
