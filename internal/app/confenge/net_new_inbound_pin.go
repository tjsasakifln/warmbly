package confenge

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// InboundAuthorityPin is the only admission key for this consume path:
// contract_id + version + content hash, as published by the Governance
// producer. Fixture schemas are never a runtime fallback; READY requires a
// published authority SHA recorded in RuntimeInboundAuthorityPin.
type InboundAuthorityPin struct {
	ContractID       string
	Version          string
	ContentHash      string
	SourceSHA        string
	AcceptedVersions []string
	TestOnly         bool
}

// Pinned is true only when contract_id, version, and content hash are all set.
func (p InboundAuthorityPin) Pinned() bool {
	return strings.TrimSpace(p.ContractID) != "" &&
		strings.TrimSpace(p.Version) != "" &&
		strings.TrimSpace(p.ContentHash) != ""
}

// GovernanceInboundPolicyHash is the policy_hash published by the Governance
// producer for NET_NEW_INBOUND_HANDRAISER/1.0.0-draft.20260904, taken from
// commercial/inbound/CONSUMER-PIN.1.0.0-draft.20260904.md at the merge commit
// named by GovernanceInboundSourceSHA. It is the authority Warmbly consumes;
// it is NOT derived from anything in this repository.
const GovernanceInboundPolicyHash = "984f442690f7c74f309173b31008518631170d63733b5cc04c32abaf88c67e28"

// GovernanceInboundSourceSHA is the Governance merge commit that published the
// authority above (PR #171 on origin/main).
const GovernanceInboundSourceSHA = "22ad810a8c1d46d9a787efcfac825d6ba0336bff"

// RuntimeInboundAuthorityPin is the production pin. It names the published
// Governance authority: contract_id, version, and the producer's policy_hash.
// Testdata under testdata/net_new_inbound_handraiser is not consulted, and
// NetNewInboundPinHash (this repo's local drift digest) is never a fallback.
func RuntimeInboundAuthorityPin() InboundAuthorityPin {
	return InboundAuthorityPin{
		ContractID:  NetNewInboundContractID,
		Version:     NetNewInboundPinVersion,
		ContentHash: GovernanceInboundPolicyHash,
		SourceSHA:   GovernanceInboundSourceSHA,
		TestOnly:    false,
	}
}

func (s *service) activeAuthorityPin() InboundAuthorityPin {
	if s != nil && s.authorityPin.Pinned() {
		return s.authorityPin
	}
	return RuntimeInboundAuthorityPin()
}

func (p InboundAuthorityPin) acceptsVersion(version string) bool {
	version = strings.TrimSpace(version)
	if version == "" {
		return false
	}
	if version == strings.TrimSpace(p.Version) {
		return true
	}
	canonical := strings.TrimSpace(p.ContractID) + "/" + strings.TrimSpace(p.Version)
	if version == canonical {
		return true
	}
	for _, v := range p.AcceptedVersions {
		if version == strings.TrimSpace(v) {
			return true
		}
	}
	return false
}

func (p InboundAuthorityPin) acceptsContractID(id string) bool {
	id = strings.TrimSpace(id)
	want := strings.TrimSpace(p.ContractID)
	if id == "" || want == "" {
		return false
	}
	if id == want {
		return true
	}
	if strings.HasPrefix(id, want+"/") {
		return true
	}
	return false
}

func normalizeContentHash(h string) string {
	h = strings.TrimSpace(strings.ToLower(h))
	h = strings.TrimPrefix(h, "sha256:")
	return h
}

// NetNewInboundPinMaterial is the canonical multi-vertical pin document.
// Tests recompute the content hash from this string. RuntimeInboundAuthorityPin does
// not read it; it is not a production fallback and never admits.
func NetNewInboundPinMaterial() string {
	return strings.Join([]string{
		NetNewInboundTaxonomySchema,
		"nuclei=" + strings.Join(NetNewInboundNuclei, ","),
		NetNewInboundCatalogSchema,
		NetNewInboundIntakeSchema,
		NetNewInboundHandraiserSchema,
		NetNewInboundStateSchema,
		NetNewInboundMeetcfgSchema,
		"source=" + NetNewInboundSource,
		"lane=" + NetNewInboundLane,
		"asset=" + NetNewInboundSourceAsset,
		"offer=" + NetNewInboundOfferCandidate,
		"invariants=outbound_eligible=false,auto_send=false",
	}, "\n")
}

// NetNewInboundPinHash is SHA-256 of the pin material (no sha256: prefix).
func NetNewInboundPinHash() string {
	sum := sha256.Sum256([]byte(NetNewInboundPinMaterial()))
	return hex.EncodeToString(sum[:])
}
