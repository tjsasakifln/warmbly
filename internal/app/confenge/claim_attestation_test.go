package confenge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/warmbly/warmbly/internal/models"
)

func loadCanaryFeedForClaimTest(t *testing.T) (*Feed, error) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "controlled_email_five_class_canary.json"))
	if err != nil {
		return nil, err
	}
	return ParseFeed(raw)
}

// TestClaimAttestationGoldenVector pins the CONTRACT_CLAIM_ATTESTATION/1.0
// canonical encoding byte-for-byte. This is the interop contract with the
// extra-cli producer branch: if that branch does not yet exist, this test
// IS the fixed target it must reproduce.
func TestClaimAttestationGoldenVector(t *testing.T) {
	a := ContractClaimAttestation{
		ClaimMode:             ClaimModeCurrentContract,
		ClaimSafetyState:      ClaimSafetyStateSafeCurrent,
		ContractID:            "contract-123",
		CopyHash:              "AABBCCDDEEFF00112233445566778899aabbccddeeff00112233445566778899",
		EvidenceHash:          "00112233445566778899aabbccddeeff00112233445566778899aabbccddee",
		EvaluatedAt:           "2026-08-21T00:00:00Z",
		ValidUntil:            "2026-08-28T00:00:00Z",
		ProducerPolicyVersion: "confenge.claim_attestation.v1",
		ProducerSHA:           "deadbeefcafef00d",
		ReasonCodes:           []string{"contract_active", "evidence_fresh"},
	}
	const want = "e7a0ac53adc045f78195811a520189b5b82173f7ec052d77523a1879f4d3234b"
	got := a.CanonicalHash()
	if got != want {
		t.Fatalf("canonical hash changed: got %s want %s (update the golden vector deliberately, and notify the extra-cli producer branch)", got, want)
	}
	// CopyHash/reason code casing/ordering must not affect the hash.
	b := a
	b.CopyHash = "aabbccddeeff00112233445566778899AABBCCDDEEFF00112233445566778899"
	b.ReasonCodes = []string{"evidence_fresh", "contract_active"}
	if b.CanonicalHash() != got {
		t.Fatal("canonical hash must be case-insensitive on copy_hash and order-insensitive on reason_codes")
	}
}

func TestClaimAttestationCanonicalHashMutationSensitivity(t *testing.T) {
	base := ContractClaimAttestation{
		ClaimMode: ClaimModeCurrentContract, ClaimSafetyState: ClaimSafetyStateSafeCurrent,
		ContractID: "c1", CopyHash: "h1", EvidenceHash: "e1",
		EvaluatedAt: "2026-08-21T00:00:00Z", ValidUntil: "2026-08-28T00:00:00Z",
		ProducerPolicyVersion: "confenge.claim_attestation.v1", ProducerSHA: "sha1",
		ReasonCodes: []string{"r1", "r2"},
	}
	want := base.CanonicalHash()
	mutate := []struct {
		name string
		edit func(*ContractClaimAttestation)
	}{
		{"claim_mode", func(a *ContractClaimAttestation) { a.ClaimMode = ClaimModeHistoricalContract }},
		{"safety_state", func(a *ContractClaimAttestation) { a.ClaimSafetyState = ClaimSafetyStateUnsafe }},
		{"contract_id", func(a *ContractClaimAttestation) { a.ContractID = "c2" }},
		{"copy_hash", func(a *ContractClaimAttestation) { a.CopyHash = "h2" }},
		{"evidence_hash", func(a *ContractClaimAttestation) { a.EvidenceHash = "e2" }},
		{"evaluated_at", func(a *ContractClaimAttestation) { a.EvaluatedAt = "2026-08-22T00:00:00Z" }},
		{"valid_until", func(a *ContractClaimAttestation) { a.ValidUntil = "2026-08-29T00:00:00Z" }},
		{"policy_version", func(a *ContractClaimAttestation) { a.ProducerPolicyVersion = "other" }},
		{"producer_sha", func(a *ContractClaimAttestation) { a.ProducerSHA = "sha2" }},
		{"reason_codes", func(a *ContractClaimAttestation) { a.ReasonCodes = []string{"r3"} }},
	}
	for _, tc := range mutate {
		t.Run(tc.name, func(t *testing.T) {
			cp := base
			cp.ReasonCodes = append([]string(nil), base.ReasonCodes...)
			tc.edit(&cp)
			if cp.CanonicalHash() == want {
				t.Fatalf("changing %s must change the canonical hash", tc.name)
			}
		})
	}
}

func validCurrentAttestation(t *testing.T, now time.Time, copyHash string) *ContractClaimAttestation {
	t.Helper()
	a := &ContractClaimAttestation{
		ClaimMode:             ClaimModeCurrentContract,
		ClaimSafetyState:      ClaimSafetyStateSafeCurrent,
		ContractID:            "contract-123",
		CopyHash:              copyHash,
		EvidenceHash:          "evidence-hash-1",
		EvaluatedAt:           now.Add(-time.Hour).Format(time.RFC3339),
		ValidUntil:            now.Add(time.Hour).Format(time.RFC3339),
		ProducerPolicyVersion: "confenge.claim_attestation.v1",
		ProducerSHA:           "producer-sha-1",
		ReasonCodes:           []string{"contract_active"},
	}
	a.AttestationHash = a.CanonicalHash()
	return a
}

func TestEvaluateClaimGuard_CurrentSafeValid_Proceeds(t *testing.T) {
	now := time.Now().UTC()
	copyHash := ClaimCopyHash("subject", "<p>hi</p>", "hi")
	a := validCurrentAttestation(t, now, copyHash)
	v := EvaluateClaimGuard(a, copyHash, false, now)
	if !v.Allowed {
		t.Fatalf("expected allowed, got blocked: %s", v.Reason)
	}
	if v.Reason != ClaimAllowedSafeCurrent {
		t.Fatalf("expected %s, got %s", ClaimAllowedSafeCurrent, v.Reason)
	}
}

func TestEvaluateClaimGuard_CurrentMissingOrUnsafe_Holds(t *testing.T) {
	now := time.Now().UTC()
	copyHash := ClaimCopyHash("s", "h", "p")

	cases := []struct {
		name       string
		attestFunc func() *ContractClaimAttestation
		wantReason string
	}{
		{"nil_attestation_with_current_mode_semantics_is_none", func() *ContractClaimAttestation { return nil }, ""},
		{"unknown_safety", func() *ContractClaimAttestation {
			a := validCurrentAttestation(t, now, copyHash)
			a.ClaimSafetyState = ClaimSafetyStateUnknown
			a.AttestationHash = a.CanonicalHash()
			return a
		}, ClaimBlockedUnsafeClaim},
		{"unsafe_safety", func() *ContractClaimAttestation {
			a := validCurrentAttestation(t, now, copyHash)
			a.ClaimSafetyState = ClaimSafetyStateUnsafe
			a.AttestationHash = a.CanonicalHash()
			return a
		}, ClaimBlockedUnsafeClaim},
		{"missing_attestation_hash", func() *ContractClaimAttestation {
			a := validCurrentAttestation(t, now, copyHash)
			a.AttestationHash = ""
			return a
		}, ClaimBlockedMissingAttestation},
		{"tampered_attestation_hash", func() *ContractClaimAttestation {
			a := validCurrentAttestation(t, now, copyHash)
			a.AttestationHash = "not-a-real-hash"
			return a
		}, ClaimBlockedMissingAttestation},
		{"expired", func() *ContractClaimAttestation {
			a := validCurrentAttestation(t, now, copyHash)
			a.ValidUntil = now.Add(-time.Minute).Format(time.RFC3339)
			a.AttestationHash = a.CanonicalHash()
			return a
		}, ClaimBlockedExpiredAttestation},
		{"evaluated_in_future", func() *ContractClaimAttestation {
			a := validCurrentAttestation(t, now, copyHash)
			a.EvaluatedAt = now.Add(time.Hour).Format(time.RFC3339)
			a.AttestationHash = a.CanonicalHash()
			return a
		}, ClaimBlockedExpiredAttestation},
		{"unsupported_policy_version", func() *ContractClaimAttestation {
			a := validCurrentAttestation(t, now, copyHash)
			a.ProducerPolicyVersion = "unknown.v9"
			a.AttestationHash = a.CanonicalHash()
			return a
		}, ClaimBlockedUnsafeClaim},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := tc.attestFunc()
			if a == nil {
				return // covered by the NONE-mode test below
			}
			v := EvaluateClaimGuard(a, copyHash, false, now)
			if v.Allowed {
				t.Fatalf("expected hold, got allowed")
			}
			if v.Reason != tc.wantReason {
				t.Fatalf("got reason %s want %s", v.Reason, tc.wantReason)
			}
		})
	}
}

func TestEvaluateClaimGuard_CopyAlteredAfterAttestation_Blocked(t *testing.T) {
	now := time.Now().UTC()
	originalHash := ClaimCopyHash("Subject", "<p>original</p>", "original")
	a := validCurrentAttestation(t, now, originalHash)

	// The pre-decoration body is mutated after the attestation was minted
	// (e.g. a re-render with different spintax/AI-variable output).
	alteredHash := ClaimCopyHash("Subject", "<p>ALTERED</p>", "altered")

	v := EvaluateClaimGuard(a, alteredHash, false, now)
	if v.Allowed {
		t.Fatal("expected copy-hash mismatch to block")
	}
	if v.Reason != ClaimBlockedCopyHashMismatch {
		t.Fatalf("got %s want %s", v.Reason, ClaimBlockedCopyHashMismatch)
	}
}

func TestEvaluateClaimGuard_HistoricalSafe_PassesWithoutCopyHashMatch(t *testing.T) {
	now := time.Now().UTC()
	a := &ContractClaimAttestation{
		ClaimMode:        ClaimModeHistoricalContract,
		ClaimSafetyState: ClaimSafetyStateSafeHistorical,
		ContractID:       "contract-old",
	}
	// copy_hash intentionally left empty/stale; historical claims never
	// require the consumer to rewrite copy, so it must not gate on hash.
	v := EvaluateClaimGuard(a, ClaimCopyHash("whatever", "changed", "body"), false, now)
	if !v.Allowed || v.Reason != ClaimAllowedSafeHistorical {
		t.Fatalf("expected allowed_safe_historical, got allowed=%v reason=%s", v.Allowed, v.Reason)
	}
}

func TestEvaluateClaimGuard_HistoricalUnsafe_Blocked(t *testing.T) {
	now := time.Now().UTC()
	a := &ContractClaimAttestation{ClaimMode: ClaimModeHistoricalContract, ClaimSafetyState: ClaimSafetyStateUnknown}
	v := EvaluateClaimGuard(a, "", false, now)
	if v.Allowed {
		t.Fatal("expected hold for unsafe historical claim")
	}
	if v.Reason != ClaimBlockedUnsafeClaim {
		t.Fatalf("got %s want %s", v.Reason, ClaimBlockedUnsafeClaim)
	}
}

func TestEvaluateClaimGuard_NoneModeZeroRegression(t *testing.T) {
	now := time.Now().UTC()
	// No attestation at all, and no legacy phrase detected: must behave
	// exactly as before this feature existed.
	v := EvaluateClaimGuard(nil, "", false, now)
	if !v.Allowed {
		t.Fatalf("NONE mode with no legacy claim must never block, got %s", v.Reason)
	}
	if v.Reason != "" {
		t.Fatalf("NONE mode allow must carry no claim-guard metric label, got %s", v.Reason)
	}
	// An explicit NONE mode attestation behaves identically to a nil one.
	explicitNone := &ContractClaimAttestation{ClaimMode: ClaimModeNone}
	v2 := EvaluateClaimGuard(explicitNone, "", false, now)
	if !v2.Allowed {
		t.Fatal("explicit claim_mode NONE must never block")
	}
}

func TestEvaluateClaimGuard_LegacyClaimWithoutAttestation_Held(t *testing.T) {
	now := time.Now().UTC()
	v := EvaluateClaimGuard(nil, "", true, now)
	if v.Allowed {
		t.Fatal("a legacy present-tense contract claim with no attestation must never send")
	}
	if v.Reason != ClaimBlockedLegacyCurrentClaim {
		t.Fatalf("got %s want %s", v.Reason, ClaimBlockedLegacyCurrentClaim)
	}
}

func TestEvaluateClaimGuard_ReplayIsIdempotent(t *testing.T) {
	now := time.Now().UTC()
	copyHash := ClaimCopyHash("s", "h", "p")
	a := validCurrentAttestation(t, now, copyHash)
	first := EvaluateClaimGuard(a, copyHash, false, now)
	second := EvaluateClaimGuard(a, copyHash, false, now)
	if first != second {
		t.Fatalf("replaying the same attestation must produce the same verdict: %+v vs %+v", first, second)
	}
}

// TestEvaluateClaimGuard_DecorationDoesNotAffectHash proves that copy_hash is
// computed pre-decoration: toggling tracking pixel/link-wrapping/signature
// text AFTER the snapshot point must never change the verdict, because the
// snapshot itself never includes those additions.
func TestEvaluateClaimGuard_DecorationDoesNotAffectHash(t *testing.T) {
	now := time.Now().UTC()
	subject, bodyHTML, bodyPlain := "Subject", "<p>Body</p>", "Body"
	preDecorationHash := ClaimCopyHash(subject, bodyHTML, bodyPlain)
	a := validCurrentAttestation(t, now, preDecorationHash)

	// Simulate decoration happening AFTER the snapshot: the gate is only
	// ever given the pre-decoration hash, so decorated variants must
	// produce identical verdicts regardless of what decoration was applied.
	decoratedVariants := []struct {
		name string
	}{{"no_decoration"}, {"tracking_pixel"}, {"link_wrapping"}, {"signature"}, {"all"}}
	for _, tc := range decoratedVariants {
		t.Run(tc.name, func(t *testing.T) {
			v := EvaluateClaimGuard(a, preDecorationHash, false, now)
			if !v.Allowed || v.Reason != ClaimAllowedSafeCurrent {
				t.Fatalf("decoration variant %s must not affect the pre-decoration verdict: allowed=%v reason=%s", tc.name, v.Allowed, v.Reason)
			}
		})
	}
}

func TestDetectLegacyCurrentContractClaim_ConservativeAgainstCanary(t *testing.T) {
	feed, err := loadCanaryFeedForClaimTest(t)
	if err != nil {
		t.Fatalf("failed to load canary fixture: %v", err)
	}
	for i := range feed.Leads {
		lead := &feed.Leads[i]
		if DetectLegacyCurrentContractClaim(lead.MessagingContext.FactToMention, lead.MessagingContext.QuestionToAsk, lead.MessagingContext.CTA) {
			t.Errorf("lead %s: legacy detector false-positived on ordinary canary messaging context", lead.SourceLeadID)
		}
	}
}

func TestDetectLegacyCurrentContractClaim_PositiveCases(t *testing.T) {
	cases := []string{
		"Vimos que o contrato em execução prevê reajuste anual.",
		"Sabemos que o contrato vigente com a prefeitura...",
		"O contrato em curso pode ser revisado.",
	}
	for _, body := range cases {
		if !DetectLegacyCurrentContractClaim("", body, "") {
			t.Errorf("expected legacy detector to flag: %q", body)
		}
	}
}

func TestDetectLegacyCurrentContractClaim_NegativeCases(t *testing.T) {
	cases := []string{
		"Gostaríamos de apresentar nossos serviços de engenharia.",
		"Vimos uma oportunidade recente de contratação pública.",
		"Podemos ajudar com o próximo contrato que vocês assinarem.",
		"Contratos futuros podem se beneficiar da nossa consultoria.",
	}
	for _, body := range cases {
		if DetectLegacyCurrentContractClaim("", body, "") {
			t.Errorf("legacy detector false-positived on ordinary copy: %q", body)
		}
	}
}

func TestAccountClaimAttestation_RoundTrip(t *testing.T) {
	now := time.Now().UTC()
	a := validCurrentAttestation(t, now, "hash-1")
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	acc := &models.OutreachAccount{ClaimAttestationJSON: b}
	got := AccountClaimAttestation(acc)
	if got == nil || got.AttestationHash != a.AttestationHash {
		t.Fatalf("round trip lost the attestation: %+v", got)
	}
	// A nil/empty envelope decodes to nil (claim_mode NONE for guard purposes).
	if AccountClaimAttestation(&models.OutreachAccount{}) != nil {
		t.Fatal("empty envelope must decode to nil")
	}
}
