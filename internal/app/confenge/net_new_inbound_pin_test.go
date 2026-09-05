package confenge

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
)

// rev02TestOnlyPin is the test-only adapter. It is never a runtime fallback.
// Production consults RuntimeRev02Pin, which stays unpinned until a final
// REV-02 SHA is recorded and this suite is re-run.
func rev02TestOnlyPin() Rev02ContractPin {
	return Rev02ContractPin{
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
	svc.rev02Pin = rev02TestOnlyPin()
	return svc, repo, org
}

func TestRuntimeRev02PinIsUnpinnedAndNeverAccepts(t *testing.T) {
	if RuntimeRev02Pin().Pinned() {
		t.Fatal("runtime pin must stay unpinned until REV-02 final SHA")
	}
	if rev02TestOnlyPin().TestOnly == false || !rev02TestOnlyPin().Pinned() {
		t.Fatal("test-only adapter must be pinned and marked test-only")
	}
	env, err := ParseNetNewInboundEnvelope(marshalNetNew(t, validNetNewMap("nnhr-unpinned-runtime")))
	if err != nil {
		t.Fatal(err)
	}
	d := DecideNetNewInbound(env, RuntimeRev02Pin())
	if d.Outcome == NetNewInboundOutcomeAccepted {
		t.Fatal("unpinned runtime pin accepted a fixture envelope")
	}
	if d.Reason != NetNewInboundReasonHashUnpinned {
		t.Fatalf("unpinned reason=%s", d.Reason)
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
	if RuntimeRev02Pin().ContentHash != "" {
		t.Fatal("production pin copied fixture content hash")
	}
}
