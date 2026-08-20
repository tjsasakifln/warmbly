package intel

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

const reconOrg = "org-offer-to-revenue-01"

func producerBody(typ, eventID, offerID, ext, providerID string, amount int64, at time.Time, extra map[string]any) []byte {
	m := map[string]any{
		"schema":                 EventSchemaV1,
		"event_id":               eventID,
		"type":                   typ,
		"occurred_at":            at.Format(time.RFC3339),
		"offer_id":               offerID,
		"offer_version":          "v1",
		"terms_version":          "CFG-TERMS-B2B-2026-08-17-v1",
		"external_reference":     ext,
		"provider_event_id":      providerID,
		"provider_raw_status":    typ,
		"canonical_status":       strings.ToUpper(typ),
		"amount_cents":           amount,
		"currency":               CurrencyBRL,
		"source":                 ProducerCONFENGEWeb,
		"financial_confirmation": typ == EventPaymentConfirmed || typ == EventPaymentReceived,
		"received_revenue":       typ == EventPaymentReceived,
		"revenue":                false,
		"synthetic":              true,
		"organization_id":        reconOrg,
		"correlation_id":         "corr-" + ext,
		"asset_id":               "landing-SYNTHETIC",
		"cta_id":                 "cta-SYNTHETIC",
	}
	for k, v := range extra {
		m[k] = v
	}
	raw, _ := json.Marshal(m)
	return raw
}

func ingestProducer(t *testing.T, st Store, raw []byte) JoinResult {
	t.Helper()
	ev, err := ParseProducerCommercialEvent(raw)
	if err != nil {
		t.Fatalf("parse producer: %v body=%s", err, raw)
	}
	return IngestEvent(st, ev)
}

func TestProducerEnvelopeParsesWithoutInventedFields(t *testing.T) {
	raw := producerBody(EventCheckoutCreated, "evt_SYNTHETIC_parse_1", OfferDiagnostico, "SYNTHETIC-PARSE-1", "asaas_SYNTHETIC_chk_1", 800000, time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC), nil)
	ev, err := ParseProducerCommercialEvent(raw)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Schema != EventSchemaV1 || ev.OfferID != OfferDiagnostico || ev.ExternalReference != "SYNTHETIC-PARSE-1" {
		t.Fatalf("producer overlay missing: %+v", ev)
	}
	if ev.RevenueClaim {
		t.Fatal("producer revenue=false was treated as true")
	}
	if ev.Payment.ReceivedCents != 0 {
		t.Fatalf("parse pre-applied received cents=%d", ev.Payment.ReceivedCents)
	}
	if ev.Offer.AmountCents != 800000 {
		t.Fatalf("amount snapshot=%d", ev.Offer.AmountCents)
	}
	if !ev.Synthetic {
		t.Fatal("SYNTHETIC id was not labeled")
	}
}

func TestHMACPathRejectsInvalidSecretAndUnknownType(t *testing.T) {
	// HMAC invalid secret is the inbound handler's job; here the shipped
	// ingest preserves unknown types and does not crash.
	st := NewMemoryStore()
	raw := producerBody("TELEPORTED", "evt_SYNTHETIC_unk_1", OfferDiagnostico, "SYNTHETIC-UNK-1", "asaas_SYNTHETIC_unk_1", 800000, time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC), map[string]any{
		"provider_raw_status": "TELEPORTED",
		"canonical_status":    "UNKNOWN",
	})
	res := ingestProducer(t, st, raw)
	if !res.Held {
		t.Fatal("unknown type was not held")
	}
	if res.Chain.Commercial.Payment.RawProviderStatus == "" && res.Chain.Commercial.Payment.CanonicalStatus != PaymentStatusUnknown {
		t.Fatalf("raw/canonical not preserved: %+v", res.Chain.Commercial.Payment)
	}
	fmt.Printf("UNKNOWN_EVENT held=%v raw=%s canonical=%s\n", res.Held, res.Chain.Commercial.Payment.RawProviderStatus, res.Chain.Commercial.Payment.CanonicalStatus)
}

func TestCommercialReconciliationE2E(t *testing.T) {
	st := NewMemoryStore()
	base := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ts := func(h int) time.Time { return base.Add(time.Duration(h) * time.Hour) }

	diagExt := "SYNTHETIC-DIAG-CFG-DIAG-EXP-v1"
	diagSteps := []struct {
		typ string
		id  string
		h   int
	}{
		{EventOfferViewed, "evt_SYNTHETIC_diag_view", 0},
		{EventOfferSelected, "evt_SYNTHETIC_diag_sel", 1},
		{EventEligibilitySubmitted, "evt_SYNTHETIC_diag_elig", 2},
		{EventCapacityApproved, "evt_SYNTHETIC_diag_cap", 3},
		{EventTermsAccepted, "evt_SYNTHETIC_diag_terms", 4},
		{EventCheckoutCreated, "evt_SYNTHETIC_diag_chk", 5},
		{EventPaymentCreated, "evt_SYNTHETIC_diag_payc", 6},
		{EventPaymentReceived, "evt_SYNTHETIC_diag_rx", 8},
	}
	var last JoinResult
	for _, step := range diagSteps {
		prov := "asaas_SYNTHETIC_" + step.id
		last = ingestProducer(t, st, producerBody(step.typ, step.id, OfferDiagnostico, diagExt, prov, 800000, ts(step.h), nil))
		if last.Held && step.typ != EventPaymentCreated {
			t.Fatalf("%s held unexpectedly exceptions=%v", step.typ, exceptionCodes(last.Exceptions))
		}
	}
	if last.Replay {
		t.Fatal("first DIAG path was a replay")
	}
	if last.Chain.Commercial.Payment.ReceivedCents != 800000 {
		t.Fatalf("diag received=%d", last.Chain.Commercial.Payment.ReceivedCents)
	}
	dec := DecideOnboarding(last.Chain)
	if dec != OnboardingEligible {
		t.Fatalf("diag onboarding=%s held=%v capacity=%s pay=%s", dec, last.Held, last.Chain.Commercial.Capacity.State, last.Chain.Commercial.Payment.CanonicalStatus)
	}
	if last.Chain.Commercial.Delivery.OnboardingStartedAt != nil {
		t.Fatal("payment_received started onboarding")
	}

	dup := ingestProducer(t, st, producerBody(EventPaymentReceived, "evt_SYNTHETIC_diag_rx", OfferDiagnostico, diagExt, "asaas_SYNTHETIC_evt_SYNTHETIC_diag_rx", 800000, ts(8), nil))
	if !dup.Replay {
		t.Fatal("duplicate webhook was not a replay")
	}
	if dup.Chain.Commercial.Payment.ReceivedCents != 800000 {
		t.Fatalf("replay mutated received=%d", dup.Chain.Commercial.Payment.ReceivedCents)
	}

	createdOnly := NewMemoryStore()
	for _, step := range diagSteps[:7] {
		ingestProducer(t, createdOnly, producerBody(step.typ, step.id+"-c", OfferDiagnostico, diagExt+"-c", "asaas_SYNTHETIC_"+step.id+"_c", 800000, ts(step.h), nil))
	}
	createdView := Rollup(mustList(createdOnly, reconOrg), "2026-08", true)
	if createdView.Commercial.ReceivedCents != 0 {
		t.Fatalf("checkout/created counted as received=%d", createdView.Commercial.ReceivedCents)
	}

	dirExt := "SYNTHETIC-DIR-CFG-DIRB2G-180-v1"
	dirLeadSteps := []struct {
		typ string
		id  string
		h   int
	}{
		{EventOfferSelected, "evt_SYNTHETIC_180_sel", 0},
		{EventEligibilitySubmitted, "evt_SYNTHETIC_180_elig", 1},
		{EventCapacityApproved, "evt_SYNTHETIC_180_cap", 2},
		{EventTermsAccepted, "evt_SYNTHETIC_180_terms", 3},
		{EventCheckoutCreated, "evt_SYNTHETIC_180_chk", 4},
		{EventSubscriptionCreated, "evt_SYNTHETIC_180_subc", 5},
		{EventSubscriptionActive, "evt_SYNTHETIC_180_suba", 6},
		{EventPaymentCreated, "evt_SYNTHETIC_180_payc", 7},
	}
	var dir JoinResult
	for _, step := range dirLeadSteps {
		dir = ingestProducer(t, st, producerBody(step.typ, step.id, OfferDirB2G180, dirExt, "asaas_SYNTHETIC_"+step.id, 1500000, ts(step.h), nil))
	}
	if dir.Chain.Commercial.Payment.ReceivedCents != 0 {
		t.Fatalf("subscription_active counted as received=%d", dir.Chain.Commercial.Payment.ReceivedCents)
	}
	if DecideOnboarding(dir.Chain) == OnboardingEligible {
		t.Fatal("onboarding eligible before payment_received")
	}
	for i := 1; i <= 6; i++ {
		dir = ingestProducer(t, st, producerBody(EventPaymentReceived, fmt.Sprintf("evt_SYNTHETIC_180_rx_%d", i), OfferDirB2G180, dirExt, fmt.Sprintf("asaas_SYNTHETIC_180_rx_%d", i), 1500000, ts(8+i), nil))
	}
	if dir.Chain.Commercial.Payment.ReceivedCount != 6 || dir.Chain.Commercial.Payment.ReceivedCents != 9000000 {
		t.Fatalf("180 received count=%d cents=%d", dir.Chain.Commercial.Payment.ReceivedCount, dir.Chain.Commercial.Payment.ReceivedCents)
	}
	if DecideOnboarding(dir.Chain) != OnboardingEligible {
		t.Fatalf("180 onboarding=%s", DecideOnboarding(dir.Chain))
	}

	pending := ingestProducer(t, st, producerBody(EventPaymentPending, "evt_SYNTHETIC_pend", OfferDiagnostico, "SYNTHETIC-PEND-1", "asaas_SYNTHETIC_pend", 800000, ts(1), nil))
	if pending.Chain.Commercial.Payment.ReceivedCents != 0 && pending.Chain.Identity == last.Chain.Identity {
		t.Fatal("pending mixed into DIAG cash")
	}

	oooStore := NewMemoryStore()
	ingestProducer(t, oooStore, producerBody(EventOfferSelected, "evt_SYNTHETIC_ooo_sel", OfferDiagnostico, "SYNTHETIC-OOO-1", "asaas_SYNTHETIC_ooo_sel", 800000, ts(1), nil))
	ooo := ingestProducer(t, oooStore, producerBody(EventPaymentReceived, "evt_SYNTHETIC_ooo_rx", OfferDiagnostico, "SYNTHETIC-OOO-1", "asaas_SYNTHETIC_ooo_rx", 800000, ts(2), nil))
	if !ooo.Held {
		t.Fatal("out-of-order payment_received was accepted")
	}

	overdue := ingestProducer(t, st, producerBody(EventPaymentOverdue, "evt_SYNTHETIC_od", OfferDiagnostico, diagExt, "asaas_SYNTHETIC_od", 800000, ts(30), nil))
	if overdue.Chain.Commercial.Payment.CanonicalStatus != PaymentStatusOverdue {
		t.Fatalf("overdue status=%s", overdue.Chain.Commercial.Payment.CanonicalStatus)
	}
	refund := ingestProducer(t, st, producerBody(EventPaymentRefunded, "evt_SYNTHETIC_rf", OfferDiagnostico, diagExt, "asaas_SYNTHETIC_rf", 800000, ts(31), nil))
	if refund.Chain.Commercial.Payment.RefundedCents <= 0 {
		t.Fatal("refund did not preserve history")
	}
	cancel := ingestProducer(t, st, producerBody(EventSubscriptionCanceled, "evt_SYNTHETIC_cx", OfferDirB2G180, dirExt, "asaas_SYNTHETIC_cx", 1500000, ts(40), nil))
	if cancel.Chain.Commercial.Subscription.CanceledAt == nil && cancel.Chain.Commercial.Subscription.CanonicalStatus != EventSubscriptionCanceled {
		t.Fatal("cancel not recorded")
	}

	lostCap := NewMemoryStore()
	ingestProducer(t, lostCap, producerBody(EventOfferSelected, "evt_SYNTHETIC_cap_sel", OfferDiagnostico, "SYNTHETIC-CAPLOST-1", "asaas_SYNTHETIC_cap_sel", 800000, ts(0), nil))
	ingestProducer(t, lostCap, producerBody(EventCapacityApproved, "evt_SYNTHETIC_cap_ok", OfferDiagnostico, "SYNTHETIC-CAPLOST-1", "asaas_SYNTHETIC_cap_ok", 800000, ts(1), nil))
	lost := ingestProducer(t, lostCap, producerBody(EventCapacityHoldExpired, "evt_SYNTHETIC_cap_exp", OfferDiagnostico, "SYNTHETIC-CAPLOST-1", "asaas_SYNTHETIC_cap_exp", 800000, ts(80), nil))
	if !lost.Held {
		t.Fatal("capacity lost before payment was not an exception")
	}

	down := NewMemoryStore()
	down.SetUnavailable(true)
	unav := ingestProducer(t, down, producerBody(EventOfferSelected, "evt_SYNTHETIC_unav", OfferDiagnostico, "SYNTHETIC-UNAV-1", "asaas_SYNTHETIC_unav", 800000, ts(0), nil))
	if !JoinUnavailable(unav) && !unav.Held {
		t.Fatal("unavailable store did not fail closed")
	}

	extra := ingestProducer(t, NewMemoryStore(), producerBody(EventOfferSelected, "evt_SYNTHETIC_extra", ExtraPrivateCode, "SYNTHETIC-EXTRA-1", "asaas_SYNTHETIC_extra", 1000000, ts(0), nil))
	if !extra.Held || extra.Chain.Commercial.Offer.OfferID == ExtraPrivateCode {
		t.Fatalf("extra serialized as offer: %+v", extra.Chain.Commercial.Offer)
	}
	cat := string(FrozenCatalogJSON())
	if strings.Contains(strings.ToLower(cat), "cfg-extra") {
		t.Fatal("extra leaked into catalog json")
	}

	view := Rollup(mustList(st, reconOrg), "2026-08", true)
	if view.Commercial.ReceivedCents <= 0 {
		t.Fatal("synthetic received_revenue missing")
	}
	if view.Commercial.QualifiedPipeline < 0 {
		t.Fatal("qualified_pipeline missing")
	}
	if len(view.ByOfferVersion) == 0 {
		t.Fatal("by_offer_version empty")
	}
	real := Rollup(mustList(st, reconOrg), "2026-08", false)
	if !real.RealEmpty && real.Commercial.ReceivedCents != 0 {
		t.Fatalf("include_synthetic=0 counted cash=%d chains=%d", real.Commercial.ReceivedCents, real.ChainCount)
	}

	qp := view.Commercial.QualifiedPipeline
	rx := view.Commercial.ReceivedCents
	var diagRow OfferExecutiveRow
	for _, row := range view.ByOfferVersion {
		if row.OfferID == OfferDiagnostico {
			diagRow = row
		}
		if MetricKeyContainsPII(row.OfferID) || strings.Contains(row.OfferID, "@") {
			t.Fatalf("PII in offer metric key: %s", row.OfferID)
		}
	}
	if diagRow.PaymentReceived < 1 {
		t.Fatalf("diag executive payment_received=%d", diagRow.PaymentReceived)
	}

	fmt.Printf("E2E durable_receipt=true duplicate=%v payment_received=%d onboarding=%s qualified_pipeline=%d received_revenue=%d by_offer=%d\n",
		dup.Replay, last.Chain.Commercial.Payment.ReceivedCents, dec, qp, rx, len(view.ByOfferVersion))
	fmt.Printf("VERDICT=COMMERCIAL_RECONCILIATION_READY residual=live_consented_event_required\n")
}

func TestCheckoutCallbackNotEligible(t *testing.T) {
	st := NewMemoryStore()
	base := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ext := "SYNTHETIC-CB-1"
	for i, typ := range []string{EventOfferSelected, EventCapacityApproved, EventTermsAccepted, EventCheckoutCreated, EventSubscriptionActive} {
		ingestProducer(t, st, producerBody(typ, fmt.Sprintf("evt_SYNTHETIC_cb_%d", i), OfferDiagnostico, ext, fmt.Sprintf("asaas_SYNTHETIC_cb_%d", i), 800000, base.Add(time.Duration(i)*time.Hour), map[string]any{
			"callback_only":          typ == EventSubscriptionActive,
			"received_revenue":       true,
			"financial_confirmation": true,
			"revenue":                false,
		}))
	}
	chains := mustList(st, reconOrg)
	if len(chains) != 1 {
		t.Fatalf("chains=%d", len(chains))
	}
	if chains[0].Commercial.Payment.ReceivedCents != 0 {
		t.Fatalf("callback claimed revenue applied: %d", chains[0].Commercial.Payment.ReceivedCents)
	}
	if DecideOnboarding(chains[0]) != OnboardingBlocked {
		t.Fatalf("callback onboarding=%s", DecideOnboarding(chains[0]))
	}
}
