package intel

import (
	"fmt"
	"time"
)

const (
	FixtureDiagnosticoComplete = "diagnostico_one_time_complete"
	FixtureDirB2G180Complete   = "dirb2g_180_six_payments"
)

// OfferNamedFixtures returns the two frozen commercial paths.
func OfferNamedFixtures(orgID string) []NamedFixture {
	return []NamedFixture{
		{Name: FixtureDiagnosticoComplete, Events: DiagnosticoSequence(orgID, "lead-diag-1", true)},
		{Name: FixtureDirB2G180Complete, Events: DirB2G180Sequence(orgID, "lead-180-1", 6, true)},
	}
}

func diagEnv(org, lead, id, typ string, at time.Time) CommercialEvent {
	off, _ := FrozenOffer(OfferDiagnostico)
	return CommercialEvent{
		EventID: id, Version: "1", Schema: EventSchemaV1, Type: typ,
		OccurredAt: at, IngestedAt: at.Add(time.Minute), Timezone: "America/Sao_Paulo",
		OrganizationID: org, LeadID: lead, ReceiptID: "rcpt-" + lead,
		CorrelationID: "corr-" + lead, IdempotencyKey: "idem-" + id,
		OfferID: OfferDiagnostico, Offer: off, Source: "web-cfg",
		AssetID: "landing-diagnostico", CTAID: "cta-diag", Query: "diagnostico",
		RouteFamily: FamilyInbound, Synthetic: true, ProducerSHA: FixtureProducerSHA,
		Capacity: DefaultCapacityPolicy(),
	}
}

// DiagnosticoSequence is CFG-DIAG-EXP-v1 through onboarding after confirmation.
func DiagnosticoSequence(orgID, lead string, complete bool) []CommercialEvent {
	base := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ts := func(h int) time.Time { return base.Add(time.Duration(h) * time.Hour) }
	holdExp := ts(4).Add(CapacityHoldTTL)
	holdCreated := ts(4)
	steps := []CommercialEvent{
		diagEnv(orgID, lead, "ev-diag-view-"+lead, EventOfferViewed, ts(0)),
		diagEnv(orgID, lead, "ev-diag-sel-"+lead, EventOfferSelected, ts(1)),
		diagEnv(orgID, lead, "ev-diag-elig-"+lead, EventEligibilitySubmitted, ts(2)),
		diagEnv(orgID, lead, "ev-diag-cap-"+lead, EventCapacityApproved, ts(3)),
		diagEnv(orgID, lead, "ev-diag-terms-"+lead, EventTermsAccepted, ts(4)),
		diagEnv(orgID, lead, "ev-diag-check-"+lead, EventCheckoutCreated, ts(5)),
		diagEnv(orgID, lead, "ev-diag-payc-"+lead, EventPaymentCreated, ts(6)),
		diagEnv(orgID, lead, "ev-diag-payok-"+lead, EventPaymentConfirmed, ts(7)),
		diagEnv(orgID, lead, "ev-diag-payrx-"+lead, EventPaymentReceived, ts(8)),
	}
	if complete {
		steps = append(steps,
			diagEnv(orgID, lead, "ev-diag-onb-"+lead, EventOnboardingStarted, ts(9)),
			diagEnv(orgID, lead, "ev-diag-act-"+lead, EventServiceActivated, ts(10)),
		)
	}
	for i := range steps {
		steps[i].Capacity.State = CapacityStateOK
		steps[i].Capacity.HoldID = "hold-" + lead
		steps[i].Capacity.HoldCreatedAt = &holdCreated
		steps[i].Capacity.HoldExpiresAt = &holdExp
		steps[i].Provider.CheckoutID = "chk-" + lead
		steps[i].Provider.ExternalRef = "ext-" + lead
		steps[i].ExternalReference = "ext-" + lead
		if steps[i].Type == EventPaymentCreated || steps[i].Type == EventPaymentConfirmed || steps[i].Type == EventPaymentReceived {
			steps[i].Provider.PaymentID = "pay-" + lead
			steps[i].ProviderEventID = "asaas-" + steps[i].EventID
			steps[i].Payment.EvidenceRef = "evidence:diag-pay"
			steps[i].Payment.ReviewStatus = ReviewRequired
			steps[i].Payment.PrincipalCents = 800000
			if steps[i].Type == EventPaymentReceived {
				steps[i].Payment.ReceivedCents = 800000
			}
		}
	}
	return steps
}

func dirEnv(org, lead, id, typ string, at time.Time) CommercialEvent {
	off, _ := FrozenOffer(OfferDirB2G180)
	return CommercialEvent{
		EventID: id, Version: "1", Schema: EventSchemaV1, Type: typ,
		OccurredAt: at, IngestedAt: at.Add(time.Minute), Timezone: "America/Sao_Paulo",
		OrganizationID: org, LeadID: lead, ReceiptID: "rcpt-" + lead,
		CorrelationID: "corr-" + lead, IdempotencyKey: "idem-" + id,
		OfferID: OfferDirB2G180, Offer: off, Source: "web-cfg",
		AssetID: "landing-direcao", CTAID: "cta-180", Query: "direcao-b2g",
		RouteFamily: FamilyInbound, Synthetic: true, ProducerSHA: FixtureProducerSHA,
		Capacity: DefaultCapacityPolicy(),
	}
}

// DirB2G180Sequence is CFG-DIRB2G-180-v1 through n received payments.
func DirB2G180Sequence(orgID, lead string, payments int, activate bool) []CommercialEvent {
	base := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ts := func(h int) time.Time { return base.Add(time.Duration(h) * time.Hour) }
	holdExp := ts(4).Add(CapacityHoldTTL)
	holdCreated := ts(4)
	steps := []CommercialEvent{
		dirEnv(orgID, lead, "ev-180-sel-"+lead, EventOfferSelected, ts(0)),
		dirEnv(orgID, lead, "ev-180-elig-"+lead, EventEligibilitySubmitted, ts(1)),
		dirEnv(orgID, lead, "ev-180-cap-"+lead, EventCapacityApproved, ts(2)),
		dirEnv(orgID, lead, "ev-180-terms-"+lead, EventTermsAccepted, ts(3)),
		dirEnv(orgID, lead, "ev-180-check-"+lead, EventCheckoutCreated, ts(4)),
		dirEnv(orgID, lead, "ev-180-subc-"+lead, EventSubscriptionCreated, ts(5)),
		dirEnv(orgID, lead, "ev-180-suba-"+lead, EventSubscriptionActive, ts(6)),
		dirEnv(orgID, lead, "ev-180-payc-"+lead, EventPaymentCreated, ts(7)),
		dirEnv(orgID, lead, "ev-180-payok-"+lead, EventPaymentConfirmed, ts(8)),
	}
	for i := 0; i < payments; i++ {
		e := dirEnv(orgID, lead, fmt.Sprintf("ev-180-rx-%d-%s", i+1, lead), EventPaymentReceived, ts(9+i))
		e.Payment.ReceivedCents = 1500000
		e.Payment.PrincipalCents = 1500000
		e.ProviderEventID = fmt.Sprintf("asaas-180-rx-%d-%s", i+1, lead)
		e.Provider.PaymentID = fmt.Sprintf("pay-180-%d-%s", i+1, lead)
		steps = append(steps, e)
	}
	if activate && payments > 0 {
		res := dirEnv(orgID, lead, "ev-180-res-"+lead, EventCapacityReserved, ts(9))
		onb := dirEnv(orgID, lead, "ev-180-onb-"+lead, EventOnboardingStarted, ts(20))
		act := dirEnv(orgID, lead, "ev-180-act-"+lead, EventServiceActivated, ts(21))
		// reservation after first confirmed; insert after confirmed
		steps = append(steps[:9], append([]CommercialEvent{res}, steps[9:]...)...)
		steps = append(steps, onb, act)
	}
	for i := range steps {
		steps[i].Capacity.State = CapacityStateOK
		steps[i].Capacity.HoldID = "hold-" + lead
		steps[i].Capacity.HoldCreatedAt = &holdCreated
		steps[i].Capacity.HoldExpiresAt = &holdExp
		steps[i].Provider.CheckoutID = "chk-" + lead
		steps[i].Provider.SubscriptionID = "sub-" + lead
		steps[i].Provider.ExternalRef = "ext-" + lead
		steps[i].ExternalReference = "ext-" + lead
		if steps[i].Type == EventCapacityReserved {
			steps[i].Capacity.State = CapacityStateFinal
		}
	}
	return steps
}
