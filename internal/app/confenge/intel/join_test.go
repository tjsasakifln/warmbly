package intel

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func testFacts(org, lead, receipt, account, action, outcome string) ObservedFacts {
	return ObservedFacts{
		Keys: JoinKeys{
			OrganizationID:          org,
			Source:                  "web-cfg",
			Query:                   "segunda-leitura",
			AssetID:                 "landing-segunda-leitura",
			CTAID:                   "cta-1",
			CorrelationID:           "corr-1",
			LeadID:                  lead,
			ReceiptID:               receipt,
			AccountID:               account,
			SourceLeadID:            account,
			PersonID:                "person-1",
			EventIDs:                []string{"ev-1"},
			TargetFitVersion:        "tf-v1",
			ActivationPolicyVersion: "ap-v1",
			TargetFitWatermark:      "wm-1",
			TargetFitFresh:          true,
			ActionID:                action,
			OutcomeID:               outcome,
			OutboxEventID:           "evt-1",
			IdempotencyKey:          "idem-" + lead,
			RouteFamily:             FamilyInbound,
			Trigger:                 "CONTRACT_MARGIN_EVENT",
			OfferID:                 "segunda-leitura",
			Route:                   "R3",
		},
		LeadCreatedAt:     time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC),
		IngestedAt:        time.Date(2026, 8, 4, 10, 5, 0, 0, time.UTC),
		ActionOccurredAt:  time.Date(2026, 8, 4, 11, 0, 0, 0, time.UTC),
		OutcomeOccurredAt: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
		OutcomeType:       OutcomeQualifiedConversation,
		Qualified:         true,
		Label:             LabelReal,
	}
}

func TestReconcileIdempotentSameIDs(t *testing.T) {
	st := NewMemoryStore()
	org := "11111111-1111-4111-8111-111111111111"
	in := testFacts(org, "webcfg-1", "rcpt-1", "acc-1", "act-1", "out-1")
	first := Reconcile(st, in)
	if !first.Created {
		t.Fatalf("first join should create a chain")
	}
	second := Reconcile(st, in)
	if second.Created {
		t.Fatal("replay created a second chain")
	}
	if !second.Replay {
		t.Fatal("replay flag unset")
	}
	if first.Chain.Identity != second.Chain.Identity {
		t.Fatalf("identity mismatch %s vs %s", first.Chain.Identity, second.Chain.Identity)
	}
	chains, _ := st.ListChains(org)
	if len(chains) != 1 {
		t.Fatalf("chains=%d want 1", len(chains))
	}
	if MetricKeyContainsPII(first.Chain.MetricKey) {
		t.Fatalf("metric key looks like PII: %s", first.Chain.MetricKey)
	}
	if first.Chain.CausalProof {
		t.Fatal("join must not claim causal proof")
	}
	fmt.Printf("JOIN identity=%s replay=%v chains=%d metric_pii=%v\n",
		first.Chain.Identity, second.Replay, len(chains), MetricKeyContainsPII(first.Chain.MetricKey))
}

func TestMetricKeyOmitsPII(t *testing.T) {
	k := JoinKeys{
		LeadID: "lead-1", ReceiptID: "rcpt-1", AccountID: "acc-1",
		PersonID: "person-1", ActionID: "act-1", OutcomeID: "out-1",
		AssetID: "asset-1", Source: "web-cfg",
	}
	key := MetricKey(k)
	blob := key + k.LeadID + k.AccountID
	if strings.Contains(strings.ToLower(blob), "@") || strings.Contains(key, "ana") {
		t.Fatalf("pii leaked into metric material: %s", key)
	}
	if MetricKeyContainsPII(key) {
		t.Fatalf("hashed key flagged as PII: %s", key)
	}
	if MetricKeyContainsPII("lead@empresa.com") {
		// sanity: detector works
	} else {
		t.Fatal("detector missed email")
	}
	fmt.Printf("METRIC_KEY hash=%s pii=false\n", key[:16])
}

func TestMissingVersionStaysUnknown(t *testing.T) {
	st := NewMemoryStore()
	in := testFacts("org", "lead-mv", "rcpt-mv", "acc-mv", "act-mv", "out-mv")
	in.Keys.TargetFitVersion = ""
	in.Keys.ActivationPolicyVersion = ""
	res := Reconcile(st, in)
	if res.Chain.Versions.TargetFit != Unknown || res.Chain.Versions.ActivationPolicy != Unknown {
		t.Fatalf("versions invented: %+v", res.Chain.Versions)
	}
	found := false
	for _, ex := range res.Exceptions {
		if ex.Code == ExceptionMissingVersion {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing_version not classified: %+v", res.Exceptions)
	}
	fmt.Printf("VERSION target_fit=%s activation=%s missing_version=true\n",
		res.Chain.Versions.TargetFit, res.Chain.Versions.ActivationPolicy)
}

func TestReconcileMergesAdditiveActionThenOutcome(t *testing.T) {
	st := NewMemoryStore()
	org := "44444444-4444-4444-8444-444444444444"
	lead := testFacts(org, "webcfg-merge-1", "rcpt-merge-1", "acc-merge", "act-merge-1", "")
	lead.Keys.OutcomeID = ""
	lead.Keys.OutboxEventID = ""
	lead.OutcomeType = ""
	lead.Qualified = false
	first := Reconcile(st, lead)
	if !first.Created {
		t.Fatal("lead+action should create")
	}
	if first.Chain.ActionID != "act-merge-1" {
		t.Fatalf("action_id=%s", first.Chain.ActionID)
	}
	if knownID(first.Chain.OutcomeID) != "" {
		t.Fatalf("outcome invented on first write: %s", first.Chain.OutcomeID)
	}

	secondIn := lead
	secondIn.Keys.ActionID = ""
	secondIn.Keys.OutcomeID = "out-merge-1"
	secondIn.Keys.OutboxEventID = "evt-merge-1"
	secondIn.OutcomeType = OutcomeQualifiedConversation
	secondIn.Qualified = true
	second := Reconcile(st, secondIn)
	if second.Created {
		t.Fatal("second reconcile opened another chain")
	}
	if second.Chain.ActionID != "act-merge-1" {
		t.Fatalf("merged action_id lost: %s", second.Chain.ActionID)
	}
	if second.Chain.OutcomeID != "out-merge-1" {
		t.Fatalf("outcome_id not merged: %s", second.Chain.OutcomeID)
	}
	if second.Chain.OutboxEventID != "evt-merge-1" {
		t.Fatalf("outbox event_id not merged: %s", second.Chain.OutboxEventID)
	}
	if second.Chain.OutcomeType != OutcomeQualifiedConversation {
		t.Fatalf("outcome type=%s", second.Chain.OutcomeType)
	}
	chains, _ := st.ListChains(org)
	if len(chains) != 1 {
		t.Fatalf("chains=%d want 1", len(chains))
	}
	if hasCode(second.Exceptions, ExceptionDuplicate) {
		t.Fatal("additive merge must not be classified as duplicate")
	}
	fmt.Printf("JOIN_MERGE identity=%s action=%s outcome=%s chains=1\n",
		second.Chain.Identity, second.Chain.ActionID, second.Chain.OutcomeID)
}

func TestNilStoreFailClosed(t *testing.T) {
	res := Reconcile(nil, testFacts("org", "l", "r", "a", "act", "out"))
	if len(res.Exceptions) == 0 || res.Exceptions[0].Code != ExceptionUnavailable {
		t.Fatalf("want fail-closed unavailable, got %+v", res.Exceptions)
	}
	if res.Created {
		t.Fatal("nil store must not create a chain")
	}
}
