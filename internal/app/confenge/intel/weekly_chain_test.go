package intel

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type readFailureStore struct {
	*MemoryStore
	getErr  error
	listErr error
}

func (s *readFailureStore) GetChain(orgID, identity string) (*Chain, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.MemoryStore.GetChain(orgID, identity)
}

func (s *readFailureStore) ListChains(orgID string) ([]Chain, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.MemoryStore.ListChains(orgID)
}

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
		if hasCode(res.Exceptions, ExceptionNoCapacity) {
			t.Fatalf("Asaas sequence required an unrelated hosted checkout for %s: %+v", ev.EventID, res.Exceptions)
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
	if hasCode(last.Exceptions, ExceptionNoCapacity) {
		t.Fatalf("received payment required an unrelated hosted checkout: %+v", last.Exceptions)
	}
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
	if receipt == nil || receipt.Processed || receipt.Identity != "corr:corr_retry_sbx_001" {
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
	key := receiptKey("org_retry_sandbox", "asaas_evt_retry_sbx_001")
	stale := store.receipts[key]
	stale.Processed = false
	store.receipts[key] = stale
	duplicate, err := IngestProviderWebhook(store, adapter, "org_retry_sandbox", "sandbox-secret", "", sig, body, now)
	if err != nil || !duplicate.Replay || !duplicate.Processed {
		t.Fatalf("processed duplicate was not idempotent: %+v err=%v", duplicate, err)
	}
	if duplicate.Join.Chain.Commercial.Payment.ReceivedCents != 800000 ||
		duplicate.Join.Chain.Commercial.Payment.ReceivedCount != 1 {
		t.Fatalf("stale receipt marker reapplied revenue: %+v", duplicate.Join.Chain.Commercial.Payment)
	}
	receipt, _ = store.GetEventReceipt("org_retry_sandbox", "asaas_evt_retry_sbx_001")
	if receipt == nil || !receipt.Processed {
		t.Fatalf("receipt marker was not repaired on replay: %+v", receipt)
	}
}

func TestCanonicalIdentityFallbackAndControlFreshness(t *testing.T) {
	now := time.Date(2026, 8, 20, 16, 0, 0, 0, time.UTC)
	chain := Chain{
		CorrelationID: Unknown,
		ChargeID:      Unknown,
		Keys: JoinKeys{
			ExternalReference: "corr_fallback_001",
			ChargeID:          "charge_fallback_001",
		},
		Commercial: CommercialState{Control: CommercialControlState{
			Decision:         DecisionWait,
			LatestObservedAt: &now,
		}},
	}
	identity := CanonicalIdentityOf(chain)
	if identity.CorrelationID != "corr_fallback_001" || identity.ChargeID != "charge_fallback_001" {
		t.Fatalf("UNKNOWN shadowed observed fallback: %+v", identity)
	}
	view := WeeklyRevenueView(chain)
	if view.Deadline.Availability != AvailabilityUnknown || view.Deadline.ObservedAt != nil {
		t.Fatalf("missing deadline carried an observed timestamp: %+v", view.Deadline)
	}

	control := mergeControl(CommercialControlState{}, CommercialEvent{
		OccurredAt: now, CommercialDecision: DecisionGo, Responsible: "role_owner",
	})
	control = mergeControl(control, CommercialEvent{
		OccurredAt: now.Add(-time.Hour), CommercialDecision: DecisionWait, Responsible: "role_stale",
	})
	if control.Decision != DecisionGo || control.Responsible != "role_owner" {
		t.Fatalf("older control evidence replaced the latest snapshot: %+v", control)
	}
}

func TestCommercialIdentityOrderingAndReuse(t *testing.T) {
	if got := ChainIdentity(JoinKeys{IdempotencyKey: "idem-1", CorrelationID: "corr-1"}); got != "idem:idem-1" {
		t.Fatalf("non-commercial identity priority changed: %s", got)
	}
	if got := ChainIdentity(JoinKeys{PreferCorrelation: true, IdempotencyKey: "idem-1", CorrelationID: "corr-1"}); got != "corr:corr-1" {
		t.Fatalf("commercial correlation did not win: %s", got)
	}
	if got := ChainIdentity(JoinKeys{PreferCorrelation: true, IdempotencyKey: "idem-1", OpportunityID: "opp-1"}); got != "opportunity:opp-1" {
		t.Fatalf("commercial stable id did not precede transport id: %s", got)
	}

	store := NewMemoryStore()
	events := loadExtraWeeklyFixture(t)
	first := events[0]
	second := first
	second.EventID = "evt_second_account_offer"
	second.IdempotencyKey = "idem_second_account_offer"
	second.CorrelationID = "corr_second_purchase"
	second.OpportunityID = "opp_second_purchase"
	second.ProposalID = "prop_second_purchase"
	if result := IngestEvent(store, first); result.Held {
		t.Fatalf("first commercial identity held: %+v", result.Exceptions)
	}
	if result := IngestEvent(store, second); result.Held {
		t.Fatalf("reused account/offer across another opportunity was held: %+v", result.Exceptions)
	}
	chains, err := store.ListChains(first.OrganizationID)
	if err != nil || len(chains) != 2 {
		t.Fatalf("reusable account/offer did not create two commercial chains: count=%d err=%v", len(chains), err)
	}

	conflict := second
	conflict.EventID = "evt_conflicting_opportunity"
	conflict.IdempotencyKey = "idem_conflicting_opportunity"
	conflict.CorrelationID = "corr_conflicting_opportunity"
	conflict.ProposalID = "prop_conflicting_opportunity"
	if result := IngestEvent(store, conflict); !result.Held {
		t.Fatal("opportunity reused across correlations was not held")
	}
}

func TestCanonicalIngestFailsClosedOnReadErrorsAndLegacyFragments(t *testing.T) {
	events := loadExtraWeeklyFixture(t)
	for name, store := range map[string]*readFailureStore{
		"get chain":   {MemoryStore: NewMemoryStore(), getErr: errors.New("read unavailable")},
		"list chains": {MemoryStore: NewMemoryStore(), listErr: errors.New("read unavailable")},
	} {
		t.Run(name, func(t *testing.T) {
			result := IngestEvent(store, events[0])
			if !result.Held || !JoinUnavailable(result) {
				t.Fatalf("read error did not fail closed: %+v", result)
			}
			chains, err := store.MemoryStore.ListChains(events[0].OrganizationID)
			if err != nil || len(chains) != 0 {
				t.Fatalf("read error created a chain: count=%d err=%v", len(chains), err)
			}
		})
	}

	orphanReceiptStore := NewMemoryStore()
	providerEvent := events[2]
	_, _, err := orphanReceiptStore.PutEventReceipt(EventReceipt{
		OrganizationID:  providerEvent.OrganizationID,
		ProviderEventID: providerEvent.ProviderEventID,
		EventID:         providerEvent.EventID,
		Identity:        "corr:" + providerEvent.CorrelationID,
		Processed:       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	orphanResult := IngestEvent(orphanReceiptStore, providerEvent)
	if !orphanResult.Held || !JoinUnavailable(orphanResult) {
		t.Fatalf("processed receipt without its chain was reapplied: %+v", orphanResult)
	}
	webhookNow := providerEvent.OccurredAt.Add(time.Hour)
	body := []byte(`{"id":"asaas_evt_extra_sbx_created_001","event":"PAYMENT_CREATED","externalReference":"corr_extra_sbx_week_2026_34","payment":{"id":"charge_asaas_sbx_001","value":"8000.00"},"dateCreated":"2026-08-20T16:00:00Z"}`)
	signature := SignProviderHMAC("sandbox-secret", webhookNow, body)
	ack, webhookErr := IngestProviderWebhook(orphanReceiptStore, NewFakeAdapter(), providerEvent.OrganizationID, "sandbox-secret", "", signature, body, webhookNow)
	if webhookErr != nil || ack.Processed || !ack.Held || !JoinUnavailable(ack.Join) {
		t.Fatalf("provider endpoint acknowledged a processed receipt without its chain: ack=%+v err=%v", ack, webhookErr)
	}
	chains, err := orphanReceiptStore.ListChains(providerEvent.OrganizationID)
	if err != nil || len(chains) != 0 {
		t.Fatalf("orphan processed receipt created a replacement chain: count=%d err=%v", len(chains), err)
	}

	source := NewMemoryStore()
	first := IngestEvent(source, events[0])
	legacy := first.Chain
	legacy.Identity = "idem:" + events[0].IdempotencyKey
	legacyStore := NewMemoryStore()
	if _, _, err := legacyStore.PutChain(legacy); err != nil {
		t.Fatal(err)
	}
	legacyResult := IngestEvent(legacyStore, events[1])
	if !legacyResult.Held || !hasCode(legacyResult.Exceptions, ExceptionConflictingExternal) {
		t.Fatalf("legacy fragment was not held for reconciliation: %+v", legacyResult)
	}
	chains, err = legacyStore.ListChains(events[0].OrganizationID)
	if err != nil || len(chains) != 1 || chains[0].Identity != legacy.Identity {
		t.Fatalf("legacy fragment created another chain: chains=%+v err=%v", chains, err)
	}
}

func TestProviderPayloadRejectsTypedIdentityAndLossyMoney(t *testing.T) {
	adapter := NewFakeAdapter()
	if _, err := adapter.ParseWebhook([]byte(`{"id":{"email":"hidden@example.test"},"event":"PAYMENT_CREATED"}`)); err == nil {
		t.Fatal("non-string provider event id was accepted")
	}
	event, err := adapter.ParseWebhook([]byte(`{"id":"evt-safe","event":"PAYMENT_CREATED","payment":{"id":{"email":"hidden@example.test"},"value":"8000.00junk"},"hidden@example.test":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if event.PaymentID != "" || event.AmountCents != 0 || len(event.UnknownFields) != 0 {
		t.Fatalf("malformed provider fields escaped minimization: %+v", event)
	}
	if !event.OccurredAt.IsZero() {
		t.Fatalf("missing provider time was invented during parse: %s", event.OccurredAt)
	}
	if _, err := adapter.ParseWebhook([]byte(`{"id":"evt-pii","event":"PAYMENT_CREATED","externalReference":"hidden@example.test"}`)); err == nil {
		t.Fatal("PII-like external reference was accepted before durable receipt storage")
	}

	store := NewMemoryStore()
	prerequisite := loadExtraWeeklyFixture(t)[0]
	if result := IngestEvent(store, prerequisite); result.Held {
		t.Fatalf("commercial prerequisite held: %+v", result.Exceptions)
	}
	now := prerequisite.OccurredAt.Add(time.Hour)
	body := []byte(`{"id":"evt-invalid-amount","event":"PAYMENT_RECEIVED","externalReference":"corr_extra_sbx_week_2026_34","payment":{"id":"charge_asaas_sbx_001","value":"8000.00junk"}}`)
	signature := SignProviderHMAC("sandbox-secret", now, body)
	ack, err := IngestProviderWebhook(store, adapter, prerequisite.OrganizationID, "sandbox-secret", "", signature, body, now)
	if err != nil || !ack.Acked || ack.Processed || !ack.Held {
		t.Fatalf("invalid provider amount was not durably held: ack=%+v err=%v", ack, err)
	}
	chain, err := store.GetChain(prerequisite.OrganizationID, "corr:corr_extra_sbx_week_2026_34")
	if err != nil || chain == nil || chain.Commercial.Payment.ReceivedCents != 0 {
		t.Fatalf("invalid provider amount became revenue: chain=%+v err=%v", chain, err)
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
	up, err := os.ReadFile(filepath.Join("..", "..", "..", "infrastructure", "db", "migrations", "000126_outreach_intel_canonical_identity.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile(filepath.Join("..", "..", "..", "infrastructure", "db", "migrations", "000126_outreach_intel_canonical_identity.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"account", "opportunity", "offer", "proposal", "charge", "payment", "correlation_id", "entity_id", "outreach_intel_identity_links_singleton_kind_idx", "outreach_intel_identity_links_unique_entity_idx"} {
		if !strings.Contains(string(up), token) {
			t.Fatalf("canonical migration missing %q", token)
		}
	}
	if !strings.Contains(string(down), "DROP TABLE IF EXISTS outreach_intel_identity_links") {
		t.Fatal("canonical migration has no reversible down")
	}
}
