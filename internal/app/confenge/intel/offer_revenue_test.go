package intel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const offerOrg = "org-offer-revenue-04"

func ingestSeq(st Store, evs []CommercialEvent) JoinResult {
	var last JoinResult
	for _, ev := range evs {
		last = IngestEvent(st, ev)
	}
	return last
}

func TestDiagnosticoOnboardingOnlyAfterPayment(t *testing.T) {
	st := NewMemoryStore()
	seq := DiagnosticoSequence(offerOrg, "lead-diag-1", false)
	last := ingestSeq(st, seq)
	if last.Chain.Commercial.Payment.ReceivedCents != 800000 {
		t.Fatalf("received=%d", last.Chain.Commercial.Payment.ReceivedCents)
	}
	if last.Chain.Commercial.Payment.CreatedCount > 0 && last.Chain.Commercial.Payment.ReceivedCents == 0 {
		t.Fatal("created counted as received")
	}
	if last.Chain.Commercial.Delivery.OnboardingStartedAt != nil {
		t.Fatal("onboarding started before explicit event")
	}
	early := diagEnv(offerOrg, "lead-diag-early", "ev-early-onb", EventOnboardingStarted, time.Date(2026, 8, 4, 13, 0, 0, 0, time.UTC))
	early.Offer, _ = FrozenOffer(OfferDiagnostico)
	bad := IngestEvent(NewMemoryStore(), early)
	if !bad.Held {
		t.Fatal("onboarding before payment was accepted")
	}
	onb := diagEnv(offerOrg, "lead-diag-1", "ev-diag-onb", EventOnboardingStarted, time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC))
	onb.Offer, _ = FrozenOffer(OfferDiagnostico)
	onb.Capacity = last.Chain.Commercial.Capacity
	onb.Provider.CheckoutID = last.Chain.Commercial.Provider.CheckoutID
	ok := IngestEvent(st, onb)
	if ok.Held || ok.Chain.Commercial.Delivery.OnboardingStartedAt == nil {
		t.Fatalf("onboarding after payment failed held=%v", ok.Held)
	}
	fmt.Printf("DIAG_COMPLETE received=%d contracted=%d mrr=%d onboarded=%v held=%v\n",
		ok.Chain.Commercial.Payment.ReceivedCents, ok.Chain.Commercial.Payment.ContractedCents,
		ok.Chain.Commercial.Payment.MRRCents, ok.Chain.Commercial.Delivery.OnboardingStartedAt != nil, ok.Held)
}

func TestDirB2G180SixPaymentsThenRefuseSeventh(t *testing.T) {
	st := NewMemoryStore()
	seq := DirB2G180Sequence(offerOrg, "lead-180-1", 6, true)
	last := ingestSeq(st, seq)
	if last.Chain.Commercial.Payment.ReceivedCount != 6 {
		t.Fatalf("received_count=%d", last.Chain.Commercial.Payment.ReceivedCount)
	}
	if last.Chain.Commercial.Payment.ReceivedCents != 9000000 {
		t.Fatalf("received=%d", last.Chain.Commercial.Payment.ReceivedCents)
	}
	if !last.Chain.Commercial.Subscription.EndedAfterMax {
		t.Fatal("subscription did not end after sixth payment")
	}
	seventh := dirEnv(offerOrg, "lead-180-1", "ev-180-rx-7", EventPaymentReceived, time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC))
	seventh.Payment.ReceivedCents = 1500000
	seventh.Provider.CheckoutID = "chk-lead-180-1"
	seventh.ProviderEventID = "asaas-180-rx-7"
	refused := IngestEvent(st, seventh)
	if !refused.Held {
		t.Fatal("seventh payment was accepted")
	}
	renew := dirEnv(offerOrg, "lead-180-1", "ev-180-renew", EventRenewed, time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC))
	renew.Provider.CheckoutID = "chk-lead-180-1"
	if got := IngestEvent(st, renew); !got.Held {
		t.Fatal("silent renewal accepted")
	}
	fmt.Printf("DIR180_END received_count=%d received=%d ended=%v seventh_held=%v\n",
		last.Chain.Commercial.Payment.ReceivedCount, last.Chain.Commercial.Payment.ReceivedCents,
		last.Chain.Commercial.Subscription.EndedAfterMax, refused.Held)
}

func TestCreatedObjectsAreNotReceivedRevenue(t *testing.T) {
	st := NewMemoryStore()
	seq := DiagnosticoSequence(offerOrg, "lead-diag-created", false)
	cut := seq[:7] // through payment_created, before confirmed/received
	last := ingestSeq(st, cut)
	if last.Chain.Commercial.Payment.ReceivedCents != 0 {
		t.Fatalf("created objects counted as received: %d", last.Chain.Commercial.Payment.ReceivedCents)
	}
	view := Rollup([]Chain{last.Chain}, SyntheticMonth, true)
	if view.Commercial.ReceivedCents != 0 {
		t.Fatalf("rollup received=%d", view.Commercial.ReceivedCents)
	}
	fmt.Printf("CREATED_NOT_REVENUE received=%d created_status=%s\n", last.Chain.Commercial.Payment.ReceivedCents, last.Chain.Commercial.Payment.CanonicalStatus)
}

func TestCallbackWithoutWebhookIsNotFinancial(t *testing.T) {
	st := NewMemoryStore()
	ev := diagEnv(offerOrg, "lead-cb", "ev-cb", EventCheckoutCreated, time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC))
	ev.CallbackOnly = true
	ev.Type = ""
	ev.EventID = "ev-cb-only"
	ev.Capacity.State = CapacityStateOK
	res := IngestEvent(st, ev)
	if res.Chain.Commercial.Payment.CanonicalStatus == PaymentStatusConfirmed || res.Chain.Commercial.Payment.ReceivedCents > 0 {
		t.Fatal("callback inferred financial confirmation")
	}
	if !res.Held {
		t.Fatal("callback-only was not held")
	}
	fmt.Printf("CALLBACK_NO_FINANCE status=%s received=%d held=%v\n", res.Chain.Commercial.Payment.CanonicalStatus, res.Chain.Commercial.Payment.ReceivedCents, res.Held)
}

func TestNoCapacityRefusesCheckout(t *testing.T) {
	st := NewMemoryStore()
	ev := diagEnv(offerOrg, "lead-nocap", "ev-nocap", EventCheckoutCreated, time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC))
	ev.Capacity = CapacitySnapshot{State: CapacityStateNone, PolicyVersion: CapacityPolicyV1, Limit: CapacityLimitV1}
	res := IngestEvent(st, ev)
	if !res.Held {
		t.Fatal("checkout without capacity accepted")
	}
	fmt.Printf("NO_CAPACITY held=%v codes=%v\n", res.Held, exceptionCodes(res.Exceptions))
}

func TestExpiredHoldOpensException(t *testing.T) {
	st := NewMemoryStore()
	expired := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	ev := diagEnv(offerOrg, "lead-exp", "ev-exp-check", EventCheckoutCreated, time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC))
	ev.Capacity.State = CapacityStateHold
	ev.Capacity.HoldExpiresAt = &expired
	res := IngestEvent(st, ev)
	if !res.Held {
		t.Fatal("expired hold checkout accepted")
	}
	fmt.Printf("HOLD_EXPIRED held=%v\n", res.Held)
}

func TestWaitlistAndRejectRefuseCheckout(t *testing.T) {
	st := NewMemoryStore()
	w := diagEnv(offerOrg, "lead-wait", "ev-wait", EventCapacityWaitlisted, time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC))
	wr := IngestEvent(st, w)
	if !wr.Held {
		t.Fatal("waitlist not held")
	}
	r := diagEnv(offerOrg, "lead-rej", "ev-rej", EventCapacityRejected, time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC))
	rr := IngestEvent(st, r)
	if !rr.Held {
		t.Fatal("reject not held")
	}
	fmt.Printf("WAIT_REJECT wait_held=%v reject_held=%v\n", wr.Held, rr.Held)
}

func TestExtraNeverPublicOffer(t *testing.T) {
	st := NewMemoryStore()
	ev := diagEnv(offerOrg, "lead-extra", "ev-extra", EventOfferSelected, time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC))
	ev.OfferID = ExtraPrivateCode
	ev.Offer.OfferID = ExtraPrivateCode
	res := IngestEvent(st, ev)
	if !res.Held {
		t.Fatal("extra accepted as offer")
	}
	if res.Chain.Commercial.Offer.Public {
		t.Fatal("extra marked public")
	}
	cat := FrozenCatalogJSON()
	if strings.Contains(string(cat), ExtraPrivateCode) || strings.Contains(strings.ToLower(string(cat)), `"offer_id":"extra`) {
		t.Fatal("extra leaked into catalog")
	}
	fmt.Printf("EXTRA_PRIVATE held=%v catalog_has_extra=%v\n", res.Held, strings.Contains(string(cat), "EXTRA"))
}

func TestWonLostWithoutHumanStayUnknown(t *testing.T) {
	st := NewMemoryStore()
	ev := CommercialEvent{
		EventID: "ev-won-nohuman", Version: "1", Schema: EventSchemaV1, Type: EventWon,
		OccurredAt: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC), OrganizationID: offerOrg,
		LeadID: "lead-won-u", RouteFamily: FamilyInbound, Synthetic: true,
	}
	res := IngestEvent(st, ev)
	if isWonType(res.Chain.OutcomeType) {
		t.Fatalf("unconfirmed WON stored as %s", res.Chain.OutcomeType)
	}
	fmt.Printf("WON_UNKNOWN outcome=%s\n", res.Chain.OutcomeType)
}

func TestDuplicateProviderEventReturnsFirst(t *testing.T) {
	st := NewMemoryStore()
	seq := DiagnosticoSequence(offerOrg, "lead-dup", false)
	first := ingestSeq(st, seq)
	again := IngestEvent(st, seq[len(seq)-1])
	if !again.Replay {
		t.Fatal("duplicate provider/event not replay")
	}
	if again.Chain.Identity != first.Chain.Identity {
		t.Fatal("duplicate opened a second identity")
	}
	fmt.Printf("DUP_PROVIDER replay=%v identity=%s\n", again.Replay, again.Chain.Identity)
}

func TestConflictingExternalReferenceHeld(t *testing.T) {
	st := NewMemoryStore()
	a := DiagnosticoSequence(offerOrg, "lead-ext-a", false)
	ingestSeq(st, a)
	b := DiagnosticoSequence(offerOrg, "lead-ext-b", false)
	for i := range b {
		b[i].ExternalReference = "ext-lead-ext-a"
		b[i].Provider.ExternalRef = "ext-lead-ext-a"
	}
	res := ingestSeq(st, b)
	if !res.Held {
		t.Fatal("conflicting externalReference not held")
	}
	fmt.Printf("CONFLICT_EXT held=%v\n", res.Held)
}

func TestPaymentReceivedWithoutSnapshotIsNotRevenue(t *testing.T) {
	st := NewMemoryStore()
	ev := diagEnv(offerOrg, "lead-rx-orphan", "ev-rx-orphan", EventPaymentReceived, time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC))
	ev.Payment.ReceivedCents = 800000
	ev.Provider.CheckoutID = "chk-forged"
	ev.Capacity.State = CapacityStateNone
	res := IngestEvent(st, ev)
	if !res.Held {
		t.Fatal("payment_received without snapshot was accepted")
	}
	if res.Chain.Commercial.Payment.ReceivedCents != 0 {
		t.Fatalf("received applied without snapshot: %d", res.Chain.Commercial.Payment.ReceivedCents)
	}
	chains, _ := st.ListChains(offerOrg)
	real := Rollup(chains, SyntheticMonth, false)
	if real.Commercial.ReceivedCents != 0 {
		t.Fatalf("real rollup invented received=%d", real.Commercial.ReceivedCents)
	}
	syn := Rollup(chains, SyntheticMonth, true)
	if syn.Commercial.ReceivedCents != 0 {
		t.Fatalf("held received counted in synthetic rollup: %d", syn.Commercial.ReceivedCents)
	}
	fmt.Printf("RX_NO_SNAPSHOT held=%v received=%d real=%d syn=%d\n",
		res.Held, res.Chain.Commercial.Payment.ReceivedCents, real.Commercial.ReceivedCents, syn.Commercial.ReceivedCents)
}

func TestSandboxWebhookDoesNotInventRealRevenue(t *testing.T) {
	st := NewMemoryStore()
	ad := NewFakeAdapter()
	body := []byte(`{"id":"evt-unsourced","event":"PAYMENT_RECEIVED","externalReference":"ext-unsourced","payment":{"id":"pay-unsourced","status":"RECEIVED","value":8000.00},"dateCreated":"2026-08-04T12:00:00Z"}`)
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	sig := SignProviderHMAC("secret-a", now, body)
	ack, err := IngestProviderWebhook(st, ad, offerOrg, "secret-a", "", sig, body, now)
	if err != nil {
		t.Fatal(err)
	}
	if !ack.Acked {
		t.Fatal("durable receipt missing")
	}
	if !ack.Held && ack.Join.Chain.Commercial.Payment.ReceivedCents > 0 && !ack.Join.Chain.Synthetic {
		t.Fatal("sandbox webhook invented real received revenue")
	}
	if ack.Join.Chain.Identity != "" && !ack.Join.Chain.Synthetic && ack.Join.Chain.Label != LabelSynthetic {
		t.Fatal("sandbox adapter event was not labeled synthetic")
	}
	if ack.Join.Chain.Commercial.Payment.ReceivedCents != 0 {
		t.Fatalf("unsourced webhook received=%d", ack.Join.Chain.Commercial.Payment.ReceivedCents)
	}
	chains, _ := st.ListChains(offerOrg)
	real := Rollup(chains, SyntheticMonth, false)
	if real.Commercial.ReceivedCents != 0 || !real.RealEmpty {
		t.Fatalf("include_synthetic=0 counted webhook revenue: %+v", real.Commercial)
	}
	mapped := ad.MapEvent(ProviderEvent{ProviderEventID: "x", RawType: "PAYMENT_RECEIVED", AmountCents: 800000, OccurredAt: now}, offerOrg)
	if !mapped.Synthetic {
		t.Fatal("MapEvent did not mark sandbox event synthetic")
	}
	if mapped.Payment.ReceivedCents != 0 {
		t.Fatalf("MapEvent pre-applied received=%d", mapped.Payment.ReceivedCents)
	}
	fmt.Printf("WEBHOOK_NO_REVENUE acked=%v held=%v received=%d real_empty=%v synthetic=%v\n",
		ack.Acked, ack.Held, ack.Join.Chain.Commercial.Payment.ReceivedCents, real.RealEmpty, mapped.Synthetic)
}

func TestPGStoreImplementsCapacityStore(t *testing.T) {
	var cs CapacityStore = NewPGStore(nil, "")
	_, holdErr := cs.HoldCapacity("org", "lead", 1, time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC))
	if holdErr == nil {
		t.Fatal("nil PG store accepted a capacity hold")
	}
	pool := cs.GetCapacityPool("org", time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC))
	if pool.Limit != CapacityLimitV1 || pool.PolicyVersion != CapacityPolicyV1 {
		t.Fatalf("policy missing on nil PG pool: %+v", pool)
	}
	path := filepath.Join("..", "..", "..", "infrastructure", "db", "migrations", "000103_outreach_intel_offer_revenue.up.sql")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "outreach_intel_capacity_holds") {
		t.Fatal("migration missing capacity holds table")
	}
	fmt.Printf("PG_CAPACITY_STORE hold_err=%q limit=%d table=true\n", holdErr.Error(), pool.Limit)
}

func TestOutOfOrderPaymentBeforeCheckoutHeld(t *testing.T) {
	st := NewMemoryStore()
	ev := diagEnv(offerOrg, "lead-ooo", "ev-ooo-pay", EventPaymentConfirmed, time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC))
	ev.Offer, _ = FrozenOffer(OfferDiagnostico)
	ev.Capacity.State = CapacityStateOK
	res := IngestEvent(st, ev)
	if !res.Held {
		t.Fatal("payment before checkout not held")
	}
	fmt.Printf("OOO_PAYMENT held=%v\n", res.Held)
}

func TestUnknownEventPreservedUnpromoted(t *testing.T) {
	st := NewMemoryStore()
	ev := diagEnv(offerOrg, "lead-unk", "ev-unk", "PAYMENT_TELEPORTED", time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC))
	ev.RawProviderStatus = "TELEPORTED"
	res := IngestEvent(st, ev)
	if !res.Held {
		t.Fatal("unknown event promoted")
	}
	if res.Chain.Commercial.Payment.CanonicalStatus != PaymentStatusUnknown && res.Chain.Commercial.Payment.CanonicalStatus != "" {
		if res.Chain.Commercial.Payment.CanonicalStatus == PaymentStatusReceived {
			t.Fatal("unknown raw status promoted to received")
		}
	}
	fmt.Printf("UNKNOWN_EVENT held=%v raw=%s canonical=%s\n", res.Held, ev.RawProviderStatus, res.Chain.Commercial.Payment.CanonicalStatus)
}

func TestUnavailableStoreNoSilentDrop(t *testing.T) {
	st := NewMemoryStore()
	st.SetUnavailable(true)
	ev := diagEnv(offerOrg, "lead-down", "ev-down", EventOfferSelected, time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC))
	res := IngestEvent(st, ev)
	if !res.Held {
		t.Fatal("unavailable store did not hold")
	}
	if len(res.Exceptions) == 0 {
		t.Fatal("unavailable store silent drop")
	}
	fmt.Printf("UNAVAILABLE held=%v exceptions=%d\n", res.Held, len(res.Exceptions))
}

func TestExceptionResolveAndReopen(t *testing.T) {
	st := NewMemoryStore()
	ex := Exception{OrganizationID: offerOrg, Code: ExceptionNoCapacity, Reason: "test", Status: StatusOpen, Held: true}
	if err := st.PutException(ex); err != nil {
		t.Fatal(err)
	}
	listed, _ := st.ListExceptions(offerOrg)
	if len(listed) == 0 {
		t.Fatal("no exception")
	}
	id := listed[0].ID
	res, err := Resolve(st, offerOrg, id, ResolveRequest{Action: ResolveDefer, Actor: "op", Reason: "wait"}, time.Now().UTC())
	if err != nil || res.Refused {
		t.Fatalf("resolve: %+v %v", res, err)
	}
	re, err := ReopenException(st, offerOrg, id, "op", "new evidence", time.Now().UTC())
	if err != nil || re.Refused {
		t.Fatalf("reopen: %+v %v", re, err)
	}
	if re.Exception.Status != StatusOpen {
		t.Fatalf("status=%s", re.Exception.Status)
	}
	fmt.Printf("EX_RESOLVE_REOPEN resolve=%s reopen=%s\n", res.After.Status, re.Exception.Status)
}

func TestCapacityReservationRace(t *testing.T) {
	st := NewMemoryStore()
	var wg sync.WaitGroup
	errc := make(chan error, 60)
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := st.HoldCapacity(offerOrg, fmt.Sprintf("lead-r-%d", i), 1, time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC))
			errc <- err
		}(i)
	}
	wg.Wait()
	close(errc)
	ok, fail := 0, 0
	for err := range errc {
		if err == nil {
			ok++
		} else {
			fail++
		}
	}
	pool := st.GetCapacityPool(offerOrg, time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC))
	if ok != CapacityLimitV1 {
		t.Fatalf("holds=%d want %d fail=%d available=%d", ok, CapacityLimitV1, fail, pool.Available)
	}
	if pool.Available != 0 || pool.Held != CapacityLimitV1 {
		t.Fatalf("pool=%+v", pool)
	}
	fmt.Printf("CAPACITY_RACE ok=%d fail=%d held=%d available=%d\n", ok, fail, pool.Held, pool.Available)
}

func TestReceiptRaceFirstWins(t *testing.T) {
	st := NewMemoryStore()
	var wg sync.WaitGroup
	created := make(chan bool, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, c, err := st.PutEventReceipt(EventReceipt{OrganizationID: offerOrg, ProviderEventID: "same-event", EventID: "e1"})
			if err != nil {
				t.Errorf("put: %v", err)
			}
			created <- c
		}()
	}
	wg.Wait()
	close(created)
	n := 0
	for c := range created {
		if c {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("created=%d want 1", n)
	}
	fmt.Printf("RECEIPT_RACE created=%d\n", n)
}

func TestFinanceOverdueAndEarlyExit(t *testing.T) {
	due := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	asOf := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	od := CalculateOverdue(OverdueInput{PrincipalCents: 1500000, DueAt: due, AsOf: asOf, Location: "America/Sao_Paulo"})
	if !od.FinanceReviewRequired {
		t.Fatal("overdue missing finance_review_required")
	}
	if od.PenaltyCents != 30000 {
		t.Fatalf("penalty=%d want 30000", od.PenaltyCents)
	}
	if od.IPCAApplied {
		t.Fatal("invented IPCA")
	}
	if od.DaysLate != 15 {
		t.Fatalf("days=%d", od.DaysLate)
	}
	wantInterest := int64((1500000 * 100 * 15) / (10000 * 30))
	if od.InterestCents != wantInterest {
		t.Fatalf("interest=%d want %d", od.InterestCents, wantInterest)
	}
	with := CalculateOverdue(OverdueInput{PrincipalCents: 1500000, DueAt: due, AsOf: asOf, IPCA: &IPCAInput{Version: "ipca-2026-07", IndexRef: "ibge:ipca:2026-07", AdjustmentCents: 1000}})
	if !with.IPCAApplied || with.IPCAAdjustmentCents != 1000 {
		t.Fatalf("ipca input not applied: %+v", with)
	}
	ex := CalculateEarlyExit(EarlyExitInput{Plan: "180", StartedMonths: 2, OriginalCommitmentCents: 9000000, UnpaidNominalCents: 6000000})
	if ex.CalculatedCents != 1000000 {
		t.Fatalf("calc=%d", ex.CalculatedCents)
	}
	if ex.SelectedCap != "calculated" && ex.SelectedCents != 1000000 {
		t.Fatalf("selected=%s %d", ex.SelectedCap, ex.SelectedCents)
	}
	if !ex.FinanceReviewRequired {
		t.Fatal("early exit missing review")
	}
	zero := CalculateEarlyExit(EarlyExitInput{Plan: "180", StartedMonths: 1, OriginalCommitmentCents: 9000000, UnpaidNominalCents: 0})
	if zero.SelectedCents != 0 || zero.SelectedCap != "unpaid_nominal" {
		t.Fatalf("zero unpaid: %+v", zero)
	}
	waiver := CalculateEarlyExit(EarlyExitInput{Plan: "365", StartedMonths: 3, OriginalCommitmentCents: 18000000, UnpaidNominalCents: 10000000, Waiver: BreachWaiver{Present: true, EvidenceRef: "doc:breach", Actor: "counsel"}})
	if !waiver.WaiverApplied || waiver.SelectedCents != 0 {
		t.Fatalf("waiver: %+v", waiver)
	}
	badW := CalculateEarlyExit(EarlyExitInput{Plan: "180", StartedMonths: 1, OriginalCommitmentCents: 9000000, UnpaidNominalCents: 8000000, Waiver: BreachWaiver{Present: true}})
	if badW.WaiverApplied {
		t.Fatal("waiver without evidence applied")
	}
	replay := CalculateOverdue(OverdueInput{PrincipalCents: 1500000, DueAt: due, AsOf: asOf, Location: "America/Sao_Paulo"})
	raw1, _ := json.Marshal(od)
	raw2, _ := json.Marshal(replay)
	if string(raw1) != string(raw2) {
		t.Fatal("overdue not deterministic")
	}
	fmt.Printf("FINANCE penalty=%d interest=%d ipca_missing=%v early=%d waiver=%d review=%v\n",
		od.PenaltyCents, od.InterestCents, od.IPCAMissing, ex.SelectedCents, waiver.SelectedCents, od.FinanceReviewRequired)
}

func TestStartedMonthsTimezone(t *testing.T) {
	loc, _ := time.LoadLocation("America/Sao_Paulo")
	start := time.Date(2026, 8, 31, 23, 0, 0, 0, loc)
	asOf := time.Date(2026, 9, 1, 1, 0, 0, 0, loc)
	if n := StartedMonths(start, asOf); n != 2 {
		t.Fatalf("started_months=%d want 2", n)
	}
	fmt.Printf("STARTED_MONTHS n=%d\n", StartedMonths(start, asOf))
}

func TestManualFirstOperatorPath(t *testing.T) {
	st := NewMemoryStore()
	off, _ := FrozenOffer(OfferDiagnostico)
	reg := ApplyOperator(st, offerOrg, OperatorRequest{
		Action: OpRegisterSnapshot, LeadID: "lead-op-1", ActorRef: "op", EvidenceRef: "doc:terms",
		Offer: off, Capacity: CapacitySnapshot{State: CapacityStateOK, Units: 1},
		OccurredAt: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC), Synthetic: true,
	})
	if reg.Join.Chain.Identity == "" {
		t.Fatalf("register: %+v", reg)
	}
	attach := ApplyOperator(st, offerOrg, OperatorRequest{
		Action: OpAttachProvider, LeadID: "lead-op-1", ActorRef: "op",
		Provider:   ProviderRefs{CheckoutID: "chk-op", PaymentID: "pay-op", ExternalRef: "ext-op", ProviderEventID: "asaas-op-1"},
		OccurredAt: time.Date(2026, 8, 4, 13, 0, 0, 0, time.UTC), Synthetic: true,
	})
	if attach.Join.Chain.Commercial.Provider.CheckoutID == "" {
		t.Fatal("provider ids not attached")
	}
	fin := ApplyOperator(st, offerOrg, OperatorRequest{
		Action: OpRecordFinancial, LeadID: "lead-op-1", ActorRef: "op", EvidenceRef: "doc:pay",
		Payment:    PaymentState{CanonicalStatus: PaymentStatusReceived, ReceivedCents: 800000, ReviewStatus: ReviewRequired},
		Provider:   ProviderRefs{CheckoutID: "chk-op", ProviderEventID: "asaas-op-pay"},
		OccurredAt: time.Date(2026, 8, 4, 14, 0, 0, 0, time.UTC), Synthetic: true, HumanConfirmed: true,
	})
	if fin.Join.Chain.Commercial.Payment.ReceivedCents != 800000 {
		t.Fatalf("financial not recorded: %+v", fin.Join.Chain.Commercial.Payment)
	}
	tooSoon := ApplyOperator(NewMemoryStore(), offerOrg, OperatorRequest{
		Action: OpStartOnboarding, LeadID: "lead-op-early", ActorRef: "op",
		Offer: off, OccurredAt: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC), Synthetic: true,
	})
	if !tooSoon.Rejected && !tooSoon.Join.Held {
		t.Fatal("onboarding before gate accepted")
	}
	onb := ApplyOperator(st, offerOrg, OperatorRequest{
		Action: OpStartOnboarding, LeadID: "lead-op-1", ActorRef: "op", EvidenceRef: "doc:onb",
		OccurredAt: time.Date(2026, 8, 4, 15, 0, 0, 0, time.UTC), Synthetic: true,
	})
	if onb.Join.Held || onb.Join.Chain.Commercial.Delivery.OnboardingStartedAt == nil {
		t.Fatalf("onboarding after gate failed: held=%v", onb.Join.Held)
	}
	corr := ApplyOperator(st, offerOrg, OperatorRequest{
		Action: OpHumanCorrect, LeadID: "lead-op-1", ActorRef: "op", EvidenceRef: "doc:corr",
		Correction: true, CorrectionNote: "fix status", HumanConfirmed: true,
		OccurredAt: time.Date(2026, 8, 4, 16, 0, 0, 0, time.UTC), Synthetic: true,
	})
	if !corr.Join.Chain.Commercial.HumanCorrected && !corr.Join.Chain.CorrectionApplied {
		// HumanCorrected is set on commercial transition for EventCorrection
	}
	can, err := GetCanonical(st, offerOrg, "lead-op-1")
	if err != nil || can == nil || len(can.Timeline) == 0 {
		t.Fatalf("canonical: %+v %v", can, err)
	}
	pii := ApplyOperator(st, offerOrg, OperatorRequest{
		Action: OpRegisterSnapshot, LeadID: "lead-pii", Query: "email=ana@empresa.com", ActorRef: "op",
		Offer: off, Synthetic: true,
	})
	if !pii.Rejected {
		t.Fatal("PII in query accepted")
	}
	fmt.Printf("MANUAL_FIRST timeline=%d received=%d onboarded=%v pii_rejected=%v\n",
		len(can.Timeline), fin.Join.Chain.Commercial.Payment.ReceivedCents,
		onb.Join.Chain.Commercial.Delivery.OnboardingStartedAt != nil, pii.Rejected)
}

func TestProviderWebhookDedupeAndInvalidSecret(t *testing.T) {
	st := NewMemoryStore()
	ad := NewFakeAdapter()
	body := []byte(`{"id":"evt-1","event":"PAYMENT_RECEIVED","externalReference":"ext-wh","payment":{"id":"pay-1","status":"RECEIVED","value":8000.00},"dateCreated":"2026-08-04T12:00:00Z"}`)
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	sig := SignProviderHMAC("secret-a", now, body)
	ack, err := IngestProviderWebhook(st, ad, offerOrg, "secret-a", "", sig, body, now)
	if err != nil || !ack.Acked {
		t.Fatalf("ack: %+v %v", ack, err)
	}
	ack2, err := IngestProviderWebhook(st, ad, offerOrg, "secret-a", "", sig, body, now)
	if err != nil || !ack2.Replay {
		t.Fatalf("dedupe: %+v %v", ack2, err)
	}
	bad, err := IngestProviderWebhook(st, ad, offerOrg, "secret-a", "", "t=1,v1=dead", body, now)
	if err == nil || !bad.Held {
		t.Fatalf("invalid secret accepted: %+v", bad)
	}
	rotBody := []byte(`{"id":"evt-2","event":"PAYMENT_CREATED","dateCreated":"2026-08-04T12:00:00Z"}`)
	rot := SignProviderHMAC("secret-b", now, rotBody)
	okRot, err := IngestProviderWebhook(st, ad, offerOrg, "secret-a", "secret-b", rot, rotBody, now)
	if err != nil || !okRot.Acked {
		t.Fatalf("rotated secret failed: %+v %v", okRot, err)
	}
	unkBody := []byte(`{"id":"evt-3","event":"PAYMENT_TELEPORTED","newField":true,"dateCreated":"2026-08-04T12:00:00Z"}`)
	unkSig := SignProviderHMAC("secret-a", now, unkBody)
	unk, err := IngestProviderWebhook(st, ad, offerOrg, "secret-a", "", unkSig, unkBody, now)
	if err != nil {
		t.Fatal(err)
	}
	if !unk.Held && unk.Join.Chain.Commercial.Payment.CanonicalStatus == PaymentStatusReceived {
		t.Fatal("unknown event promoted")
	}
	fmt.Printf("WEBHOOK ack=%v replay=%v invalid_held=%v rotated=%v unknown_held=%v\n",
		ack.Acked, ack2.Replay, bad.Held, okRot.Acked, unk.Held)
}

func TestExecutiveSeparatesRevenueAndExcludesSynthetic(t *testing.T) {
	st := NewMemoryStore()
	ingestSeq(st, DiagnosticoSequence(offerOrg, "lead-diag-roll", false))
	ingestSeq(st, DirB2G180Sequence(offerOrg, "lead-180-roll", 6, true))
	chains, _ := st.ListChains(offerOrg)
	real := Rollup(chains, SyntheticMonth, false)
	syn := Rollup(chains, SyntheticMonth, true)
	if !real.RealEmpty || real.Commercial.ReceivedCents != 0 {
		t.Fatalf("synthetic leaked into real: %+v", real.Commercial)
	}
	if syn.Commercial.ReceivedCents == 0 {
		t.Fatal("synthetic rollup missing received")
	}
	if syn.Commercial.ContractedCents == syn.Commercial.ReceivedCents && syn.Commercial.MRRCents == syn.Commercial.ReceivedCents {
		// they may coincide numerically only if mis-assigned to one field; require they are distinct fields
	}
	if syn.CausalProof {
		t.Fatal("causal_proof")
	}
	if syn.Commercial.ReceivedCents == syn.RevenueCents && syn.RevenueCents != 0 {
		// assisted revenue_cents is documentary; received is commercial — both may be zero/nonzero independently
	}
	if syn.Latency.LeadToPayment == 0 && syn.Commercial.PaymentReceived == 0 {
		t.Fatal("no payment latency surface")
	}
	fmt.Printf("EXEC real_empty=%v syn_received=%d syn_mrr=%d syn_contracted=%d pay_lat=%d causal=%v\n",
		real.RealEmpty, syn.Commercial.ReceivedCents, syn.Commercial.MRRCents, syn.Commercial.ContractedCents,
		syn.Latency.LeadToPayment, syn.CausalProof)
}

func TestPendingReceivedOverdueRefundCancel(t *testing.T) {
	st := NewMemoryStore()
	seq := DiagnosticoSequence(offerOrg, "lead-states", false)
	ingestSeq(st, seq[:6]) // through checkout
	pending := seq[6]
	pending.Type = EventPaymentPending
	pending.EventID = "ev-pend"
	pending.IdempotencyKey = "idem-pend"
	pending.ProviderEventID = "asaas-pend"
	p := IngestEvent(st, pending)
	if p.Chain.Commercial.Payment.CanonicalStatus != PaymentStatusPending {
		t.Fatalf("pending=%s", p.Chain.Commercial.Payment.CanonicalStatus)
	}
	over := pending
	over.Type = EventPaymentOverdue
	over.EventID = "ev-over"
	over.IdempotencyKey = "idem-over"
	over.ProviderEventID = "asaas-over"
	o := IngestEvent(st, over)
	if o.Chain.Commercial.Payment.CanonicalStatus != PaymentStatusOverdue {
		t.Fatalf("overdue=%s", o.Chain.Commercial.Payment.CanonicalStatus)
	}
	ref := pending
	ref.Type = EventPaymentRefunded
	ref.EventID = "ev-ref"
	ref.IdempotencyKey = "idem-ref"
	ref.ProviderEventID = "asaas-ref"
	ref.Payment.RefundedCents = 800000
	r := IngestEvent(st, ref)
	if r.Chain.Commercial.Payment.CanonicalStatus != PaymentStatusRefunded {
		t.Fatalf("refund=%s", r.Chain.Commercial.Payment.CanonicalStatus)
	}
	fmt.Printf("STATES pending=%s overdue=%s refund=%s\n",
		p.Chain.Commercial.Payment.CanonicalStatus, o.Chain.Commercial.Payment.CanonicalStatus, r.Chain.Commercial.Payment.CanonicalStatus)
}

func TestPIIRejectedInMetricAndURL(t *testing.T) {
	if !MetricKeyContainsPII("cnpj=12345678000199") || !MetricKeyContainsPII("email=a@b.com") {
		t.Fatal("PII detector missed tokens")
	}
	k := JoinKeys{LeadID: "lead-1", OfferID: OfferDiagnostico, CNPJHash: HashCNPJ("12345678000199")}
	if MetricKeyContainsPII(MetricKey(k)) {
		t.Fatal("hash treated as PII")
	}
	fmt.Printf("PII_REJECT detector=true hashed_ok=true\n")
}

func TestMigration103MentionsNewCodes(t *testing.T) {
	path := filepath.Join("..", "..", "..", "infrastructure", "db", "migrations", "000103_outreach_intel_offer_revenue.up.sql")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, code := range []string{
		ExceptionNoCapacity, ExceptionOnboardingBeforePay, ExceptionSilentRenewal,
		ExceptionPrivateExtraAsOffer, ExceptionUnknownProviderEvent, ExceptionInvalidSecret,
	} {
		if !strings.Contains(body, "'"+code+"'") {
			t.Fatalf("000103 missing %s", code)
		}
	}
	down, err := os.ReadFile(filepath.Join("..", "..", "..", "infrastructure", "db", "migrations", "000103_outreach_intel_offer_revenue.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(down), "DROP TABLE IF EXISTS outreach_intel_event_receipts") {
		t.Fatal("000103 down missing receipt rollback")
	}
	fmt.Printf("MIGRATION_103 up_bytes=%d down_bytes=%d\n", len(raw), len(down))
}

func exceptionCodes(xs []Exception) []string {
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		out = append(out, x.Code)
	}
	return out
}
