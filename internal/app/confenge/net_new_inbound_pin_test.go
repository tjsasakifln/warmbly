package confenge

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
)

// testOnlyAuthorityPin is the test-only adapter, keyed on this repository's
// LOCAL drift digest. It is never a runtime fallback: production consults
// RuntimeInboundAuthorityPin, which carries the published Governance
// policy_hash instead.
func testOnlyAuthorityPin() InboundAuthorityPin {
	return InboundAuthorityPin{
		ContractID:  NetNewInboundContractID,
		Version:     NetNewInboundPinVersion,
		ContentHash: NetNewInboundPinnedHash,
		SourceSHA:   "",
		AcceptedVersions: []string{
			NetNewInboundPinVersion,
			NetNewInboundHandraiserSchema,
		},
		TestOnly: true,
	}
}

func netNewTestService(t *testing.T) (*service, *memRepo, uuid.UUID) {
	t.Helper()
	svc, repo, org := inboundTestService(t)
	svc.authorityPin = testOnlyAuthorityPin()
	return svc, repo, org
}

// TestRuntimeAuthorityPinIsTheGovernanceAuthority asserts the production pin
// names the published Governance authority verbatim, and that a local-fixture
// envelope (carrying this repo's own drift digest) is still rejected by it.
// A fixture must never be admissible against production authority.
func TestRuntimeAuthorityPinIsTheGovernanceAuthority(t *testing.T) {
	pin := RuntimeInboundAuthorityPin()
	if !pin.Pinned() || pin.TestOnly {
		t.Fatalf("production pin must be pinned and not test-only: %+v", pin)
	}
	if pin.ContractID != NetNewInboundContractID || pin.Version != NetNewInboundPinVersion {
		t.Fatalf("production pin identity drifted: %+v", pin)
	}
	if pin.ContentHash != GovernanceInboundPolicyHash {
		t.Fatalf("production pin hash=%s want the published Governance policy_hash", pin.ContentHash)
	}
	if pin.SourceSHA != GovernanceInboundSourceSHA {
		t.Fatalf("production pin source_sha=%s", pin.SourceSHA)
	}
	// The local fixture digest is not the authority, so the fixture envelope
	// must be rejected on hash mismatch (not admitted, not "unpinned").
	env, err := ParseNetNewInboundEnvelope(marshalNetNew(t, validNetNewMap("nnhr-fixture-vs-authority")))
	if err != nil {
		t.Fatal(err)
	}
	d := DecideNetNewInbound(env, pin)
	if d.Outcome == NetNewInboundOutcomeAccepted {
		t.Fatal("production authority accepted a local fixture envelope")
	}
	if d.Reason != NetNewInboundReasonHashMismatch {
		t.Fatalf("fixture-vs-authority reason=%s want %s", d.Reason, NetNewInboundReasonHashMismatch)
	}
	// The Governance envelope is the one that admits.
	genv, err := ParseNetNewInboundEnvelope(marshalNetNew(t, governanceNetNewMap("nnhr-authority-admits")))
	if err != nil {
		t.Fatal(err)
	}
	if gd := DecideNetNewInbound(genv, pin); gd.Outcome != NetNewInboundOutcomeAccepted {
		t.Fatalf("published authority envelope not accepted: %+v", gd)
	}
	if testOnlyAuthorityPin().TestOnly == false || !testOnlyAuthorityPin().Pinned() {
		t.Fatal("test-only adapter must be pinned and marked test-only")
	}
}

func TestTestOnlyAdapterIsNotProductionAuthority(t *testing.T) {
	raw, err := os.ReadFile("testdata/net_new_inbound_handraiser/conformance.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Authority string `json:"authority"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Authority == "" {
		t.Fatal("fixture missing authority disclaimer")
	}
	// Stronger than "production pin is empty": the production pin must not be
	// this repository's own material digest. Warmbly consumes an authority it
	// does not compute.
	if RuntimeInboundAuthorityPin().ContentHash == NetNewInboundPinnedHash {
		t.Fatal("production pin copied this repo's local drift digest as authority")
	}
	if RuntimeInboundAuthorityPin().ContentHash == NetNewInboundPinHash() {
		t.Fatal("production pin is self-computed, not consumed from Governance")
	}
}
