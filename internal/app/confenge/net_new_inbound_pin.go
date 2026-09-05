package confenge

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Rev02ContractPin is the only admission key for this consume path:
// contract_id + version + content hash. Fixture schemas are never a
// runtime fallback. READY requires a final REV-02 SHA in RuntimeRev02Pin.
type Rev02ContractPin struct {
	ContractID       string
	Version          string
	ContentHash      string
	SourceSHA        string
	AcceptedVersions []string
	TestOnly         bool
}

// Pinned is true only when contract_id, version, and content hash are all set.
func (p Rev02ContractPin) Pinned() bool {
	return strings.TrimSpace(p.ContractID) != "" &&
		strings.TrimSpace(p.Version) != "" &&
		strings.TrimSpace(p.ContentHash) != ""
}

// RuntimeRev02Pin is the production pin. It stays empty until REV-02
// publishes a final SHA that is recorded here and the suite is re-run.
// Testdata under testdata/net_new_inbound_handraiser is not consulted.
func RuntimeRev02Pin() Rev02ContractPin {
	return Rev02ContractPin{
		SourceSHA: "",
		TestOnly:  false,
	}
}

func (s *service) activeRev02Pin() Rev02ContractPin {
	if s != nil && s.rev02Pin.Pinned() {
		return s.rev02Pin
	}
	return RuntimeRev02Pin()
}

func (p Rev02ContractPin) acceptsVersion(version string) bool {
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

func (p Rev02ContractPin) acceptsContractID(id string) bool {
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
// Tests recompute the content hash from this string. RuntimeRev02Pin does
// not read it; it is not a production fallback.
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
