package intel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func loadExtraWeeklyFixture(t *testing.T) []CommercialEvent {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "extra_weekly_revenue_chain.json"))
	if err != nil {
		t.Fatal(err)
	}
	var events []CommercialEvent
	if err := json.Unmarshal(raw, &events); err != nil {
		t.Fatal(err)
	}
	return events
}

func TestExtraWeeklyFixtureUsesOneCanonicalCorrelationAndUnknownReceipt(t *testing.T) {
	store := NewMemoryStore()
	events := loadExtraWeeklyFixture(t)
	for _, ev := range events[:len(events)-1] {
		res := IngestEvent(store, ev)
		if res.Chain.Identity != "corr:corr_extra_sbx_week_2026_34" {
			t.Fatalf("fragmented identity for %s: %q", ev.EventID, res.Chain.Identity)
		}
	}
	chains, err := store.ListChains("org_extra_sandbox")
	if err != nil {
		t.Fatal(err)
	}
	if len(chains) != 1 {
		t.Fatalf("canonical chain count=%d", len(chains))
	}
	before := WeeklyRevenueView(chains[0])
	if before.Receipt.Availability != AvailabilityUnknown || before.Receipt.AmountCents != nil {
		t.Fatalf("missing receipt became zero/observed: %+v", before.Receipt)
	}
	if before.Decision.Value != DecisionWait || before.Proposal.Availability != AvailabilityObserved {
		t.Fatalf("human gate or proposal missing: %+v", before)
	}

	last := IngestEvent(store, events[len(events)-1])
	view := WeeklyRevenueView(last.Chain)
	ids := view.Identity
	if ids.CorrelationID != "corr_extra_sbx_week_2026_34" ||
		ids.AccountID != "acc_extra_sbx_001" || ids.OpportunityID != "opp_extra_sbx_001" ||
		ids.OfferID != OfferDiagnostico || ids.ProposalID != "prop_extra_sbx_001" ||
		ids.ChargeID != "charge_asaas_sbx_001" || ids.PaymentID != "payment_asaas_sbx_001" {
		t.Fatalf("incomplete canonical identity: %+v", ids)
	}
	if view.Receipt.Availability != AvailabilityObserved || view.Receipt.AmountCents == nil || *view.Receipt.AmountCents != 800000 {
		t.Fatalf("received outcome not observable: %+v", view.Receipt)
	}
	if view.Charge.Availability != AvailabilityObserved || view.Charge.AmountCents == nil || *view.Charge.AmountCents != 800000 {
		t.Fatalf("charge not observable: %+v", view.Charge)
	}
	rollup := Rollup([]Chain{last.Chain}, "2026-08", true)
	if len(rollup.WeeklyRevenueChains) != 1 || rollup.WeeklyRevenueChains[0].Identity.CorrelationID != ids.CorrelationID {
		t.Fatalf("weekly chain missing from executive contract: %+v", rollup.WeeklyRevenueChains)
	}
}

func TestProviderReceiptHeldThenRetriedAfterSnapshot(t *testing.T) {
	store := NewMemoryStore()
	adapter := NewFakeAdapter()
	now := time.Date(2026, 8, 20, 16, 0, 0, 0, time.UTC)
	body := []byte(`{"id":"asaas_evt_retry_sbx_001","event":"PAYMENT_RECEIVED","externalReference":"corr_retry_sbx_001","payment":{"id":"charge_retry_sbx_001","externalReference":"corr_retry_sbx_001","status":"RECEIVED","value":8000.00},"dateCreated":"2026-08-20T16:00:00Z"}`)
	sig := SignProviderHMAC("sandbox-secret", now, body)
	first, err := IngestProviderWebhook(store, adapter, "org_retry_sandbox", "sandbox-secret", "", sig, body, now)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Acked || first.Processed || !first.Held {
		t.Fatalf("out-of-order event was not durably held: %+v", first)
	}
	receipt, _ := store.GetEventReceipt("org_retry_sandbox", "asaas_evt_retry_sbx_001")
	if receipt == nil || receipt.Processed {
		t.Fatalf("receipt should remain retryable: %+v", receipt)
	}

	offer, _ := FrozenOffer(OfferDiagnostico)
	prerequisite := CommercialEvent{
		EventID: "evt_retry_terms_sbx_001", Version: "1", Schema: EventSchemaV1,
		Type: EventTermsAccepted, OccurredAt: now.Add(-time.Hour), IngestedAt: now,
		OrganizationID: "org_retry_sandbox", CorrelationID: "corr_retry_sbx_001",
		AccountPublicID: "acc_retry_sbx_001", OpportunityID: "opp_retry_sbx_001",
		ProposalID: "prop_retry_sbx_001", OfferID: OfferDiagnostico, Offer: offer,
		Capacity:           CapacitySnapshot{State: CapacityStateOK, Eligibility: EligibilityEligible, Units: 1},
		CommercialDecision: DecisionWait, Synthetic: true,
	}
	IngestEvent(store, prerequisite)

	retry, err := IngestProviderWebhook(store, adapter, "org_retry_sandbox", "sandbox-secret", "", sig, body, now)
	if err != nil {
		t.Fatal(err)
	}
	if !retry.Replay || !retry.Processed || retry.Join.Chain.Commercial.Payment.ReceivedCents != 800000 {
		t.Fatalf("retry did not apply held receipt: %+v", retry)
	}
	receipt, _ = store.GetEventReceipt("org_retry_sandbox", "asaas_evt_retry_sbx_001")
	if receipt == nil || !receipt.Processed {
		t.Fatalf("receipt not marked processed: %+v", receipt)
	}
	duplicate, err := IngestProviderWebhook(store, adapter, "org_retry_sandbox", "sandbox-secret", "", sig, body, now)
	if err != nil || !duplicate.Replay || !duplicate.Processed {
		t.Fatalf("processed duplicate was not idempotent: %+v err=%v", duplicate, err)
	}
}

func TestTermsSnapshotIsImmutableAndMigrationIsReversible(t *testing.T) {
	store := NewMemoryStore()
	event := loadExtraWeeklyFixture(t)[0]
	first := IngestEvent(store, event)
	frozenAmount := first.Chain.Commercial.Offer.AmountCents
	drift := event
	drift.EventID = "evt_extra_sbx_terms_drift_001"
	drift.IdempotencyKey = "idem_extra_sbx_terms_drift_001"
	drift.OccurredAt = drift.OccurredAt.Add(time.Hour)
	drift.Offer.AmountCents = frozenAmount + 1
	result := IngestEvent(store, drift)
	if !result.Held || result.Chain.Commercial.Offer.AmountCents != frozenAmount {
		t.Fatalf("terms drift changed immutable snapshot: held=%v amount=%d", result.Held, result.Chain.Commercial.Offer.AmountCents)
	}
	found := false
	for _, ex := range result.Exceptions {
		found = found || ex.Code == ExceptionTermsDrift
	}
	if !found {
		t.Fatalf("terms drift occurrence missing: %+v", result.Exceptions)
	}
	up, err := os.ReadFile(filepath.Join("..", "..", "..", "infrastructure", "db", "migrations", "000115_outreach_intel_canonical_identity.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile(filepath.Join("..", "..", "..", "infrastructure", "db", "migrations", "000115_outreach_intel_canonical_identity.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"account", "opportunity", "offer", "proposal", "charge", "payment", "correlation_id", "entity_id"} {
		if !strings.Contains(string(up), token) {
			t.Fatalf("canonical migration missing %q", token)
		}
	}
	if !strings.Contains(string(down), "DROP TABLE IF EXISTS outreach_intel_identity_links") {
		t.Fatal("canonical migration has no reversible down")
	}
}
