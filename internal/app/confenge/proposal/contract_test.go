package proposal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSyntheticCanaryMatchesGoldenAndConverges(t *testing.T) {
	goldenPath := contractPath("fixtures", "delivery-order-requested.synthetic.v1.json")
	raw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var golden DeliveryOrderRequested
	if err := decoder.Decode(&golden); err != nil {
		t.Fatal(err)
	}
	var first SyntheticCanaryResult
	for run := 0; run < 3; run++ {
		result, err := RunSyntheticCanary(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(result.Handoff, golden) {
			t.Fatalf("run %d diverged from golden\n got: %+v\nwant: %+v", run+1, result.Handoff, golden)
		}
		if run == 0 {
			first = result
			continue
		}
		if result.Proposal.ProposalID != first.Proposal.ProposalID ||
			result.Proposal.AcceptedSnapshotHash != first.Proposal.AcceptedSnapshotHash ||
			result.Handoff.EventID != first.Handoff.EventID {
			t.Fatalf("run %d generated unstable identities", run+1)
		}
	}
	if golden.FinancialGate.ReceivedRevenue || !golden.Synthetic || golden.FinancialGate.State != FinancialGateSyntheticValid {
		t.Fatalf("golden broke synthetic finance invariant: %+v", golden.FinancialGate)
	}
	proposalRaw, err := os.ReadFile(contractPath("fixtures", "proposal.accepted.synthetic.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	proposalDecoder := json.NewDecoder(bytes.NewReader(proposalRaw))
	proposalDecoder.DisallowUnknownFields()
	var proposalGolden Proposal
	if err := proposalDecoder.Decode(&proposalGolden); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Proposal, proposalGolden) {
		t.Fatalf("accepted proposal diverged from golden\n got: %+v\nwant: %+v", first.Proposal, proposalGolden)
	}
}

func TestVersionedContractSchemasAreValidJSON(t *testing.T) {
	for _, name := range []string{
		"confenge.proposal.v1.schema.json",
		"confenge.proposal_event.v1.schema.json",
		"confenge.financial_gate.v1.schema.json",
		"confenge.delivery_order_requested.v1.schema.json",
	} {
		raw, err := os.ReadFile(contractPath(name))
		if err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.HasPrefix(schema["title"].(string), "confenge.") {
			t.Fatalf("%s has no versioned confenge title", name)
		}
		sum := sha256.Sum256(raw)
		t.Logf("%s sha256=%s", name, hex.EncodeToString(sum[:]))
	}
}

func contractPath(parts ...string) string {
	base := []string{"..", "..", "..", "..", "docs", "contracts", "proposal-v1"}
	return filepath.Join(append(base, parts...)...)
}
