package confenge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/warmbly/warmbly/internal/models"
)

// CONTRACT_CLAIM_ATTESTATION/1.0: extra-cli's proof that a message asserting
// a present contractual state ("contrato em execução/vigente") is backed by
// contemporary evidence bound to the exact copy being sent. Warmbly verifies
// this attestation; it never performs contract-lifecycle inference itself
// (no PNCP lookups, no promotion of historical claims to current).
const ClaimAttestationSchemaV1 = "CONTRACT_CLAIM_ATTESTATION/1.0"

// Claim modes.
const (
	ClaimModeCurrentContract    = "CURRENT_CONTRACT"
	ClaimModeHistoricalContract = "HISTORICAL_CONTRACT"
	ClaimModeNone               = "NONE"
)

// Claim safety states.
const (
	ClaimSafetyStateSafeCurrent    = "SAFE_CURRENT"
	ClaimSafetyStateSafeHistorical = "SAFE_HISTORICAL"
	ClaimSafetyStateUnsafe         = "UNSAFE"
	ClaimSafetyStateUnknown        = "UNKNOWN"
)

// Machine-readable claim-guard outcomes. These are the spec's "metrics":
// this repo has no counter/metrics framework, so the reason string logged
// via logCampaignDecision-style event types IS the observable signal.
const (
	ClaimBlockedMissingAttestation = "blocked_missing_attestation"
	ClaimBlockedUnsafeClaim        = "blocked_unsafe_claim"
	ClaimBlockedExpiredAttestation = "blocked_expired_attestation"
	ClaimBlockedCopyHashMismatch   = "blocked_copy_hash_mismatch"
	ClaimBlockedLegacyCurrentClaim = "blocked_legacy_current_claim"
	ClaimAllowedSafeCurrent        = "allowed_safe_current"
	ClaimAllowedSafeHistorical     = "allowed_safe_historical"
)

// SupportedClaimProducerPolicyVersions is the closed set of producer policy
// versions this consumer knows how to validate. An attestation stamped with
// an unrecognized version is treated as UNSAFE (fail closed), never ignored.
var SupportedClaimProducerPolicyVersions = map[string]bool{
	"confenge.claim_attestation.v1": true,
}

// ContractClaimAttestation is the CONTRACT_CLAIM_ATTESTATION/1.0 envelope, as
// projected from the extra-cli feed onto a lead. Optional/additive: its
// absence on a FeedLead is NONE and preserves pre-existing behavior exactly
// (rule 4). Warmbly never mints or rewrites this envelope; it only verifies
// producer_sha/producer_policy_version, temporal validity, attestation_hash
// integrity, and — for CURRENT_CONTRACT — that copy_hash matches the body
// actually about to be sent.
type ContractClaimAttestation struct {
	ClaimMode             string   `json:"claim_mode"`
	ClaimSafetyState      string   `json:"claim_safety_state"`
	ContractID            string   `json:"contract_id"`
	CopyHash              string   `json:"copy_hash"`
	EvidenceHash          string   `json:"evidence_hash"`
	EvaluatedAt           string   `json:"evaluated_at"`
	ValidUntil            string   `json:"valid_until"`
	ProducerPolicyVersion string   `json:"producer_policy_version"`
	ProducerSHA           string   `json:"producer_sha"`
	ReasonCodes           []string `json:"reason_codes"`
	AttestationHash       string   `json:"attestation_hash"`
}

// AccountClaimAttestation decodes the persisted envelope, if any. A nil
// return (missing/undecodable JSON) is claim_mode NONE for guard purposes.
func AccountClaimAttestation(acc *models.OutreachAccount) *ContractClaimAttestation {
	if acc == nil || len(acc.ClaimAttestationJSON) == 0 {
		return nil
	}
	var a ContractClaimAttestation
	if err := json.Unmarshal(acc.ClaimAttestationJSON, &a); err != nil {
		return nil
	}
	return &a
}

// CanonicalHash computes the CONTRACT_CLAIM_ATTESTATION/1.0 canonical hash:
// the same material fields, in this fixed order, UTF-8, joined by a NUL
// (0x00) separator between fields; reason_codes is itself sorted and joined
// by ASCII 0x1F before being placed as one field. attestation_hash is never
// part of its own input. This is the interop contract with the producer
// branch (extra-cli); it must be reproduced byte-for-byte — see the golden
// vector test in claim_attestation_test.go.
func (a *ContractClaimAttestation) CanonicalHash() string {
	if a == nil {
		return ""
	}
	codes := append([]string(nil), a.ReasonCodes...)
	sort.Strings(codes)
	reasonField := strings.Join(codes, "\x1f")

	var b strings.Builder
	b.WriteString(strings.TrimSpace(a.ClaimMode))
	b.WriteByte(0)
	b.WriteString(strings.TrimSpace(a.ClaimSafetyState))
	b.WriteByte(0)
	b.WriteString(strings.TrimSpace(a.ContractID))
	b.WriteByte(0)
	b.WriteString(strings.ToLower(strings.TrimSpace(a.CopyHash)))
	b.WriteByte(0)
	b.WriteString(strings.ToLower(strings.TrimSpace(a.EvidenceHash)))
	b.WriteByte(0)
	b.WriteString(strings.TrimSpace(a.EvaluatedAt))
	b.WriteByte(0)
	b.WriteString(strings.TrimSpace(a.ValidUntil))
	b.WriteByte(0)
	b.WriteString(strings.TrimSpace(a.ProducerPolicyVersion))
	b.WriteByte(0)
	b.WriteString(strings.TrimSpace(a.ProducerSHA))
	b.WriteByte(0)
	b.WriteString(reasonField)
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// VerifyIntegrity reports whether attestation_hash matches the recomputed
// canonical hash over the other fields.
func (a *ContractClaimAttestation) VerifyIntegrity() bool {
	if a == nil {
		return false
	}
	want := strings.ToLower(strings.TrimSpace(a.AttestationHash))
	if want == "" {
		return false
	}
	return want == a.CanonicalHash()
}

// ClaimCopyHash returns the sha256 hex digest of the exact pre-decoration
// body the dispatcher is about to send. Callers must snapshot subject/body
// BEFORE tracking pixel, link wrapping, signature, or unsubscribe headers
// are applied — those are transport decoration, not the approved copy, and
// must never affect this hash (rule: footers added after do not mismatch).
func ClaimCopyHash(subject, bodyHTML, bodyPlain string) string {
	var b strings.Builder
	b.WriteString(subject)
	b.WriteByte(0)
	b.WriteString(bodyHTML)
	b.WriteByte(0)
	b.WriteString(bodyPlain)
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// ClaimGuardVerdict is the closed outcome of evaluating a claim attestation
// against the live pre-decoration copy hash.
type ClaimGuardVerdict struct {
	Allowed bool
	Reason  string // one of the Claim* constants above
}

// EvaluateClaimGuard implements REGRAS 1-9. It never mutates state and is
// pure/deterministic (replay-idempotent): the same attestation + copy hash
// always produce the same verdict.
//
// legacyClaimDetected is the conservative rule-6 legacy phrase detector's
// result, evaluated by the caller against the same pre-decoration body when
// attestation is nil. It must never fire on ordinary commercial copy.
func EvaluateClaimGuard(a *ContractClaimAttestation, liveCopyHash string, legacyClaimDetected bool, now time.Time) ClaimGuardVerdict {
	if a == nil || strings.TrimSpace(a.ClaimMode) == "" || strings.EqualFold(strings.TrimSpace(a.ClaimMode), ClaimModeNone) {
		if legacyClaimDetected {
			return ClaimGuardVerdict{Allowed: false, Reason: ClaimBlockedLegacyCurrentClaim}
		}
		return ClaimGuardVerdict{Allowed: true}
	}

	mode := strings.ToUpper(strings.TrimSpace(a.ClaimMode))
	safety := strings.ToUpper(strings.TrimSpace(a.ClaimSafetyState))

	switch mode {
	case ClaimModeHistoricalContract:
		if safety != ClaimSafetyStateSafeHistorical {
			return ClaimGuardVerdict{Allowed: false, Reason: ClaimBlockedUnsafeClaim}
		}
		// The consumer never rewrites copy for a historical claim, so
		// copy_hash agreement is not required here (rule 3).
		return ClaimGuardVerdict{Allowed: true, Reason: ClaimAllowedSafeHistorical}

	case ClaimModeCurrentContract:
		if safety != ClaimSafetyStateSafeCurrent {
			return ClaimGuardVerdict{Allowed: false, Reason: ClaimBlockedUnsafeClaim}
		}
		if strings.TrimSpace(a.AttestationHash) == "" || !a.VerifyIntegrity() {
			return ClaimGuardVerdict{Allowed: false, Reason: ClaimBlockedMissingAttestation}
		}
		if !SupportedClaimProducerPolicyVersions[strings.TrimSpace(a.ProducerPolicyVersion)] {
			return ClaimGuardVerdict{Allowed: false, Reason: ClaimBlockedUnsafeClaim}
		}
		evaluatedAt := parseTimePtr(a.EvaluatedAt)
		validUntil := parseTimePtr(a.ValidUntil)
		if evaluatedAt == nil || validUntil == nil {
			return ClaimGuardVerdict{Allowed: false, Reason: ClaimBlockedExpiredAttestation}
		}
		if now.After(*validUntil) || evaluatedAt.After(now) {
			return ClaimGuardVerdict{Allowed: false, Reason: ClaimBlockedExpiredAttestation}
		}
		if strings.ToLower(strings.TrimSpace(a.CopyHash)) != strings.ToLower(strings.TrimSpace(liveCopyHash)) {
			return ClaimGuardVerdict{Allowed: false, Reason: ClaimBlockedCopyHashMismatch}
		}
		return ClaimGuardVerdict{Allowed: true, Reason: ClaimAllowedSafeCurrent}

	default:
		// Unknown/unsupported claim_mode: fail closed as unsafe rather than
		// silently treating it as NONE (an unrecognized mode is never proof
		// of safety).
		return ClaimGuardVerdict{Allowed: false, Reason: ClaimBlockedUnsafeClaim}
	}
}

// legacyCurrentContractPhrases is a conservative, low-recall Portuguese
// phrase list for detecting copy that asserts a present contractual state
// without any attestation at all (rule 6). False positives on ordinary
// commercial copy are unacceptable, so this only matches unambiguous,
// specific phrasings — never generic words like "contrato" alone.
var legacyCurrentContractPhrases = []string{
	"contrato em execução",
	"contrato em execucao",
	"contrato vigente",
	"contrato em vigor",
	"contrato em curso",
	"contrato atualmente em execução",
	"contrato atualmente em execucao",
	"contrato ainda vigente",
	"durante a execução do contrato atual",
	"durante a execucao do contrato atual",
	"no contrato que vocês estão executando",
	"no contrato que voces estao executando",
	"o contrato em vigor com",
	"contrato que está em vigor",
	"contrato que esta em vigor",
}

// DetectLegacyCurrentContractClaim conservatively flags copy that asserts a
// present-tense contractual state. It is defense-in-depth for messages that
// carry no claim attestation at all (mode NONE / absent) — it must never run
// against, or override, an attestation-bearing message.
func DetectLegacyCurrentContractClaim(subject, bodyHTML, bodyPlain string) bool {
	text := strings.ToLower(strings.Join([]string{subject, bodyHTML, bodyPlain}, "\n"))
	for _, phrase := range legacyCurrentContractPhrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}
