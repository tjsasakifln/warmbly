package intel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"time"
	"unicode"
)

// ApplyCommercialTransition is the shipped offer/capacity/payment/onboarding
// transition. Tests drive this function. It never infers revenue or WON.
func ApplyCommercialTransition(existing *Chain, ev CommercialEvent) TransitionResult {
	now := time.Now().UTC()
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = now
	}
	if ev.IngestedAt.IsZero() {
		ev.IngestedAt = now
	}

	canonical, raw, unknown := NormalizeEventType(ev.Type)
	ev.Type = canonical
	if ev.RawEventType == "" {
		ev.RawEventType = raw
	}

	facts := EventToFacts(ev)
	facts.Commercial = stateFromEvent(ev, existing)
	res := TransitionResult{Facts: facts}

	add := func(code, reason, next string, held bool) {
		ex := Exception{
			OrganizationID: strings.TrimSpace(ev.OrganizationID),
			Code:           code,
			CodeVersion:    ExceptionCodeVersion,
			Reason:         reason,
			NextAction:     next,
			Identity:       ChainIdentity(facts.Keys),
			MetricKey:      MetricKey(facts.Keys),
			LeadID:         facts.Keys.LeadID,
			ReceiptID:      facts.Keys.ReceiptID,
			Held:           held,
			Synthetic:      ev.Synthetic,
			Owner:          firstNonEmpty(ev.ActorRef, "commercial-intel"),
			EvidenceRefs:   evidenceRefs(ev),
			OpenedAt:       now,
			At:             now,
			RetryState:     "pending",
		}
		res.Exceptions = append(res.Exceptions, ex)
		if held {
			res.Held = true
		}
	}

	if unknown {
		add(ExceptionUnknownProviderEvent, "unknown provider event type preserved unpromoted", "review raw type; do not promote status", true)
		res.Facts.Commercial.Payment.RawProviderStatus = firstNonEmpty(ev.RawProviderStatus, ev.RawEventType)
		res.Facts.Commercial.Payment.CanonicalStatus = PaymentStatusUnknown
		res.Rejected = false
		res.Held = true
		res.Reason = "unknown_provider_event"
		return res
	}

	if IsPrivateExtra(ev.OfferID) || IsPrivateExtra(ev.Offer.OfferID) || IsPrivateExtra(ev.Offer.InternalCode) || IsPrivateExtra(ev.Offer.PublicCode) {
		add(ExceptionPrivateExtraAsOffer, "Extra R$10k is a private historical exception, never a public offer", "keep Extra off the catalog; open a private exception", true)
		res.Rejected = true
		res.Reason = "private_extra"
		res.Facts.Commercial.Offer = OfferSnapshot{}
		return res
	}

	if rawCNPJ := strings.TrimSpace(ev.CNPJ); rawCNPJ != "" {
		if looksLikeRawCNPJ(rawCNPJ) {
			res.Facts.Keys.CNPJHash = HashCNPJ(rawCNPJ)
			res.Facts.Commercial.CNPJHash = res.Facts.Keys.CNPJHash
		}
		ev.CNPJ = ""
	}

	st := CommercialState{}
	if existing != nil {
		st = existing.Commercial
	}
	st = mergeCommercial(st, res.Facts.Commercial, ev)

	typ := canonical
	switch typ {
	case EventCheckoutCreated:
		if !capacityAllowsCheckout(st.Capacity, ev.OccurredAt) {
			add(ExceptionNoCapacity, "checkout without capacity APPROVED or a valid hold", "hold; do not open checkout", true)
			res.Rejected = true
			res.Reason = "no_capacity"
			return res
		}
		if holdExpired(st.Capacity, ev.OccurredAt) {
			add(ExceptionCapacityExpired, "capacity hold expired before checkout", "open exception; do not activate service", true)
			res.Rejected = true
			res.Reason = "capacity_expired"
			return res
		}
	case EventCapacityHoldExpired:
		st.Capacity.State = CapacityStateExpired
		add(ExceptionCapacityExpired, "capacity hold expired", "do not activate service; re-qualify capacity", true)
		res.Held = true
	case EventCapacityRejected:
		st.Capacity.State = CapacityStateReject
		st.Capacity.RejectReason = firstNonEmpty(ev.Capacity.RejectReason, ev.EvidenceRef, "rejected")
		add(ExceptionCapacityRejected, "capacity rejected", "do not open checkout", true)
		res.Held = true
	case EventCapacityWaitlisted:
		st.Capacity.State = CapacityStateWait
		st.Capacity.WaitlistReason = firstNonEmpty(ev.Capacity.WaitlistReason, ev.EvidenceRef, "waitlisted")
		add(ExceptionWaitlisted, "capacity waitlisted", "do not open checkout until approved", true)
		res.Held = true
	case EventCapacityReserved:
		if !paymentConfirmed(st.Payment) {
			add(ExceptionImpossibleCommercial, "final reservation before first confirmed payment", "hold; do not finalize reservation", true)
			res.Rejected = true
			res.Reason = "reserve_before_payment"
			return res
		}
		t := ev.OccurredAt
		st.Capacity.State = CapacityStateFinal
		st.Capacity.FinalReservedAt = &t
	case EventPaymentCreated, EventPaymentPending, EventSubscriptionCreated:
		// created objects never increment received or contracted-as-received
	case EventPaymentConfirmed:
		if blocked, why := paymentFinancialGate(existing, ev, st); blocked {
			add(ExceptionOutOfOrder, why, "hold; do not infer confirmed payment or revenue", true)
			res.Held = true
			res.Rejected = true
			res.Reason = why
			return res
		}
		if ev.OccurredAt.Before(checkoutTime(existing)) && !checkoutTime(existing).IsZero() {
			add(ExceptionOutOfOrder, "payment timestamp precedes checkout", "hold; do not reorder", true)
			res.Held = true
		}
		t := ev.OccurredAt
		st.Payment.CanonicalStatus = PaymentStatusConfirmed
		st.Payment.ConfirmedAt = &t
		st.Payment.ConfirmedCount++
		st.Payment.LastPaymentAt = &t
		if st.Offer.TotalCommitmentCents > 0 {
			st.Payment.ContractedCents = st.Offer.TotalCommitmentCents
		}
		if recurringActive(st) {
			st.Payment.MRRCents = st.Offer.AmountCents
		}
		// Manual NFS-e queue only. Do not auto-issue and do not change price.
		st.Payment.FinanceReviewReq = true
		if st.Gates.Finance == "" {
			st.Gates.Finance = ReviewRequired
		}
		res.Exceptions = append(res.Exceptions, Exception{
			OrganizationID: strings.TrimSpace(ev.OrganizationID),
			Code:           ExceptionNfseManualQueue,
			CodeVersion:    ExceptionCodeVersion,
			Reason:         "manual NFS-e queue; do not auto-issue or change price",
			NextAction:     "human finance issues NFS-e; do not mutate amount_cents",
			Identity:       ChainIdentity(facts.Keys),
			MetricKey:      MetricKey(facts.Keys),
			LeadID:         facts.Keys.LeadID,
			ReceiptID:      facts.Keys.ReceiptID,
			Held:           true,
			Synthetic:      ev.Synthetic,
			Owner:          OwnerFinance,
			EvidenceRefs:   evidenceRefs(ev),
			OpenedAt:       now,
			At:             now,
			RetryState:     "pending",
		})
	case EventPaymentReceived:
		if blocked, why := paymentFinancialGate(existing, ev, st); blocked {
			add(ExceptionOutOfOrder, why, "hold; do not apply received revenue", true)
			res.Held = true
			res.Rejected = true
			res.Reason = why
			st.Payment.CanonicalStatus = firstNonEmpty(st.Payment.CanonicalStatus, PaymentStatusUnknown)
			return res
		}
		t := ev.OccurredAt
		st.Payment.CanonicalStatus = PaymentStatusReceived
		st.Payment.ReceivedAt = &t
		st.Payment.LastPaymentAt = &t
		inc := ev.Payment.ReceivedCents
		if inc <= 0 {
			inc = ev.Payment.PrincipalCents
		}
		if inc <= 0 {
			inc = ev.Offer.AmountCents
		}
		if inc <= 0 {
			inc = st.Offer.AmountCents
		}
		maxPay := st.Offer.MaxPayments
		if maxPay <= 0 {
			maxPay = st.Subscription.MaxPayments
		}
		if maxPay > 0 && st.Payment.ReceivedCount >= maxPay {
			add(ExceptionSilentRenewal, "payment beyond maxPayments refused", "do not accept a silent seventh/renewal payment", true)
			res.Rejected = true
			res.Reason = "max_payments"
			return res
		}
		st.Payment.ReceivedCents += inc
		st.Payment.ReceivedCount++
		if st.Payment.ReceivedCount == 1 && st.Offer.OfferID == OfferDiagnostico {
			// Reminder only. Not a kill switch; cash and onboarding stay open.
			add(ExceptionCounselReviewDue, "counsel review due within 10 business days of first payment_received", "hire/review counsel; do not block cash or onboarding", false)
		}
		if st.Offer.TotalCommitmentCents > 0 {
			st.Payment.ContractedCents = st.Offer.TotalCommitmentCents
		}
		if recurringActive(st) || st.Offer.BillingMode == BillingRecurring {
			st.Payment.MRRCents = st.Offer.AmountCents
		}
		if maxPay > 0 && st.Payment.ReceivedCount >= maxPay {
			end := ev.OccurredAt
			st.Subscription.CanonicalStatus = EventSubscriptionEnded
			st.Subscription.EndedAt = &end
			st.Subscription.EndedAfterMax = true
			st.Payment.MRRCents = 0
		}
	case EventPaymentOverdue:
		t := ev.OccurredAt
		st.Payment.CanonicalStatus = PaymentStatusOverdue
		st.Payment.OverdueAt = &t
		st.Payment.FinanceReviewReq = true
		add(ExceptionPaymentOverdue, "payment overdue", "run overdue calculator; finance review required", false)
	case EventPaymentRefunded:
		st.Payment.CanonicalStatus = PaymentStatusRefunded
		ref := ev.Payment.RefundedCents
		if ref <= 0 {
			ref = ev.Offer.AmountCents
		}
		st.Payment.RefundedCents += ref
		if isChargebackEvent(ev) {
			add(ExceptionChargeback, "payment chargeback", "finance review; do not infer WON or LOST; do not auto-mutate the provider", false)
		} else {
			add(ExceptionPaymentRefund, "payment refunded", "finance review; do not infer WON", false)
		}
	case EventPaymentFailed:
		st.Payment.CanonicalStatus = PaymentStatusFailed
	case EventSubscriptionEnded, EventSubscriptionCanceled:
		t := ev.OccurredAt
		st.Subscription.CanonicalStatus = typ
		if typ == EventSubscriptionCanceled {
			st.Subscription.CanceledAt = &t
		} else {
			st.Subscription.EndedAt = &t
		}
		st.Payment.MRRCents = 0
		add(ExceptionSubscriptionEnded, "subscription ended or canceled", "do not silently renew", false)
	case EventRenewed, EventRenewalDue:
		maxPay := st.Offer.MaxPayments
		if maxPay > 0 && (st.Payment.ReceivedCount >= maxPay || st.Subscription.EndedAfterMax) {
			add(ExceptionSilentRenewal, "silent renewal refused after maxPayments", "require a new offer/terms snapshot", true)
			res.Rejected = true
			res.Reason = "silent_renewal"
			return res
		}
	case EventOnboardingStarted:
		if !paymentConfirmed(st.Payment) {
			add(ExceptionOnboardingBeforePay, "onboarding before reconciled financial confirmation", "hold; do not start onboarding", true)
			res.Rejected = true
			res.Reason = "onboarding_before_payment"
			return res
		}
		if st.Offer.BillingMode == BillingRecurring && st.Capacity.State != CapacityStateFinal {
			add(ExceptionNoCapacity, "recurring onboarding requires final capacity reservation", "hold; finalize reservation after first payment", true)
			res.Rejected = true
			res.Reason = "onboarding_without_reservation"
			return res
		}
		if holdExpired(st.Capacity, ev.OccurredAt) && !paymentConfirmed(st.Payment) {
			add(ExceptionCapacityLost, "capacity lost before payment; do not activate service", "open exception", true)
			res.Rejected = true
			return res
		}
		t := ev.OccurredAt
		st.Delivery.OnboardingStartedAt = &t
		applySLA(&st)
	case EventServiceActivated:
		if !paymentConfirmed(st.Payment) {
			add(ExceptionOnboardingBeforePay, "service activation before financial confirmation", "hold; do not activate service", true)
			res.Rejected = true
			res.Reason = "activate_before_payment"
			return res
		}
		if st.Offer.BillingMode == BillingRecurring && st.Capacity.State != CapacityStateFinal {
			add(ExceptionNoCapacity, "recurring activation requires final reservation", "hold", true)
			res.Rejected = true
			return res
		}
		t := ev.OccurredAt
		st.Delivery.ServiceActivatedAt = &t
		st.Delivery.ServiceStatus = EventServiceActivated
	case EventTermsAccepted:
		if ev.Offer.TotalCommitmentCents > 0 {
			st.Payment.ContractedCents = ev.Offer.TotalCommitmentCents
		} else if st.Offer.TotalCommitmentCents > 0 {
			st.Payment.ContractedCents = st.Offer.TotalCommitmentCents
		}
		if st.Offer.BillingMode == BillingOneTime {
			t := ev.OccurredAt
			st.Delivery.OneOffAcceptedAt = &t
		}
		if existing != nil && offerInputDrift(existing.Commercial.Offer, ev.Offer) {
			add(ExceptionTermsDrift, "incoming terms/price/version disagrees with the first snapshot", "keep the first snapshot; do not overwrite", true)
			st.Offer = existing.Commercial.Offer
			st.Payment.ContractedCents = existing.Commercial.Payment.ContractedCents
			res.Held = true
		}
	case EventOfferSelected, EventOfferViewed, EventEligibilitySubmitted, EventCapacityApproved, EventCapacityReleased:
		if typ == EventCapacityApproved {
			st.Capacity.State = CapacityStateOK
			st.Capacity.Eligibility = EligibilityEligible
		}
	case EventCommercialExceptionOpen, EventCommercialException:
		reason := firstNonEmpty(ev.EvidenceRef, ev.ProducerExceptionCode, "producer commercial exception")
		add(ExceptionUnknownProviderEvent, reason, "review; do not infer revenue or onboarding", true)
		res.Held = true
	}

	if ev.Correction {
		st.HumanCorrected = true
		st.ManualTouches++
	}

	if existing != nil && conflictingExternal(existing.Commercial.Provider.ExternalRef, firstNonEmpty(ev.ExternalReference, ev.Provider.ExternalRef), existing, ev) {
		add(ExceptionConflictingExternal, "externalReference already bound to a different commercial identity", "hold; keep the first binding", true)
		res.Held = true
		res.Reason = "conflicting_external_reference"
	}

	st.Timeline = appendReceipt(st.Timeline, ev, canonical)
	st.Delivery.OnboardingDecision = decideOnboardingState(st, res.Held)
	res.Facts.Commercial = st
	copyCommercialKeys(&res.Facts, st)
	return res
}

func stateFromEvent(ev CommercialEvent, existing *Chain) CommercialState {
	st := CommercialState{}
	if existing != nil {
		st = existing.Commercial
	}
	off := ev.Offer
	if off.OfferID == "" {
		off.OfferID = ev.OfferID
	}
	if off.Currency == "" {
		off.Currency = CurrencyBRL
	}
	if off.CanonicalAPIHost == "" {
		off.CanonicalAPIHost = CanonicalAPIHost
	}
	if off.TaxPremisePercent == 0 {
		off.TaxPremisePercent = TaxPremisePercent
	}
	if off.CatalogAuthorityHash == "" {
		off.CatalogAuthorityHash = CatalogAuthorityHash()
	}
	if off.SnapshotHash == "" && off.OfferID != "" {
		off.SnapshotHash = HashOfferSnapshot(off)
	}
	if knownCatalogOffer(off.OfferID) {
		off.Public = true
		if frozen, ok := FrozenOffer(off.OfferID); ok {
			if off.AmountCents == 0 {
				off.AmountCents = frozen.AmountCents
			}
			if off.BillingMode == "" {
				off.BillingMode = frozen.BillingMode
			}
			if off.CommitmentMonths == 0 {
				off.CommitmentMonths = frozen.CommitmentMonths
			}
			if off.MaxPayments == 0 {
				off.MaxPayments = frozen.MaxPayments
			}
			if off.TotalCommitmentCents == 0 {
				off.TotalCommitmentCents = frozen.TotalCommitmentCents
			}
			if off.TermsVersion == "" {
				off.TermsVersion = frozen.TermsVersion
			}
			if off.OfferVersion == "" {
				off.OfferVersion = frozen.OfferVersion
			}
			if off.Cycle == "" {
				off.Cycle = frozen.Cycle
			}
		}
	}
	st.Offer = mergeOffer(st.Offer, off)
	st.Capacity = mergeCapacity(st.Capacity, ev.Capacity)
	st.Provider = mergeProvider(st.Provider, ev.Provider)
	if ev.ExternalReference != "" && st.Provider.ExternalRef == "" {
		st.Provider.ExternalRef = ev.ExternalReference
	}
	if ev.ProviderEventID != "" && st.Provider.ProviderEventID == "" {
		st.Provider.ProviderEventID = ev.ProviderEventID
	}
	st.Payment = mergePayment(st.Payment, ev.Payment)
	if ev.RawProviderStatus != "" {
		st.Payment.RawProviderStatus = ev.RawProviderStatus
	}
	st.Gates = mergeGates(st.Gates, ev.Gates)
	if ev.CompanyRef != "" && st.CompanyRef == "" {
		st.CompanyRef = ev.CompanyRef
	}
	if ev.CNPJHash != "" && st.CNPJHash == "" {
		st.CNPJHash = ev.CNPJHash
	}
	if ev.QueryClass != "" && st.QueryClass == "" {
		st.QueryClass = ev.QueryClass
	}
	if ev.ReferrerClass != "" && st.ReferrerClass == "" {
		st.ReferrerClass = ev.ReferrerClass
	}
	return st
}

func mergeCommercial(dst, src CommercialState, ev CommercialEvent) CommercialState {
	dst.Offer = mergeOffer(dst.Offer, src.Offer)
	dst.Capacity = mergeCapacity(dst.Capacity, src.Capacity)
	dst.Provider = mergeProvider(dst.Provider, src.Provider)
	dst.Payment = mergePayment(dst.Payment, src.Payment)
	dst.Control = mergeControl(dst.Control, ev)
	dst.Gates = mergeGates(dst.Gates, src.Gates)
	if dst.CompanyRef == "" {
		dst.CompanyRef = src.CompanyRef
	}
	if dst.CNPJHash == "" {
		dst.CNPJHash = src.CNPJHash
	}
	if dst.QueryClass == "" {
		dst.QueryClass = src.QueryClass
	}
	if dst.ReferrerClass == "" {
		dst.ReferrerClass = src.ReferrerClass
	}
	switch ev.Type {
	case EventPaymentCreated:
		dst.Payment.CreatedCount++
		if dst.Payment.CanonicalStatus == "" || dst.Payment.CanonicalStatus == PaymentStatusNone {
			dst.Payment.CanonicalStatus = PaymentStatusCreated
		}
	case EventPaymentPending:
		if dst.Payment.CanonicalStatus != PaymentStatusReceived && dst.Payment.CanonicalStatus != PaymentStatusConfirmed {
			dst.Payment.CanonicalStatus = PaymentStatusPending
		}
	case EventSubscriptionCreated:
		t := ev.OccurredAt
		if dst.Subscription.CreatedAt == nil {
			dst.Subscription.CreatedAt = &t
		}
		if dst.Subscription.CanonicalStatus == "" {
			dst.Subscription.CanonicalStatus = EventSubscriptionCreated
		}
		if dst.Offer.MaxPayments > 0 {
			dst.Subscription.MaxPayments = dst.Offer.MaxPayments
		}
	case EventSubscriptionActive:
		t := ev.OccurredAt
		dst.Subscription.ActiveAt = &t
		dst.Subscription.CanonicalStatus = EventSubscriptionActive
	case EventEligibilitySubmitted:
		if dst.Capacity.Eligibility == "" || dst.Capacity.Eligibility == EligibilityUnknown {
			dst.Capacity.Eligibility = EligibilityPending
		}
	}
	return dst
}

func mergeControl(dst CommercialControlState, ev CommercialEvent) CommercialControlState {
	changed := false
	if value := strings.TrimSpace(ev.DeliverableID); value != "" {
		dst.LatestDeliverableID = value
		changed = true
	}
	if value := strings.TrimSpace(ev.EvidenceRef); value != "" {
		dst.LatestEvidenceRef = value
		changed = true
	}
	if value := normalizeDecision(ev.CommercialDecision); value != Unknown {
		dst.Decision = value
		changed = true
	}
	if value := strings.TrimSpace(ev.Responsible); value != "" {
		dst.Responsible = value
		changed = true
	}
	if ev.Deadline != nil && !ev.Deadline.IsZero() {
		t := ev.Deadline.UTC()
		dst.Deadline = &t
		changed = true
	}
	if value := strings.TrimSpace(ev.NextAction); value != "" {
		dst.NextAction = value
		changed = true
	}
	if changed {
		t := ev.OccurredAt.UTC()
		dst.LatestObservedAt = &t
	}
	return dst
}

func mergeOffer(dst, src OfferSnapshot) OfferSnapshot {
	if dst.OfferID == "" {
		dst.OfferID = src.OfferID
	}
	if dst.OfferVersion == "" {
		dst.OfferVersion = src.OfferVersion
	}
	if dst.PublicCode == "" {
		dst.PublicCode = src.PublicCode
	}
	if dst.InternalCode == "" {
		dst.InternalCode = src.InternalCode
	}
	if dst.TermsVersion == "" {
		dst.TermsVersion = src.TermsVersion
	}
	if dst.TermsHash == "" {
		dst.TermsHash = src.TermsHash
	}
	if dst.AmountCents == 0 {
		dst.AmountCents = src.AmountCents
	}
	if dst.Currency == "" {
		dst.Currency = src.Currency
	}
	if dst.BillingMode == "" {
		dst.BillingMode = src.BillingMode
	}
	if dst.Cycle == "" {
		dst.Cycle = src.Cycle
	}
	if dst.CommitmentMonths == 0 {
		dst.CommitmentMonths = src.CommitmentMonths
	}
	if dst.MaxPayments == 0 {
		dst.MaxPayments = src.MaxPayments
	}
	if dst.TotalCommitmentCents == 0 {
		dst.TotalCommitmentCents = src.TotalCommitmentCents
	}
	if dst.NoticeDays == 0 {
		dst.NoticeDays = src.NoticeDays
	}
	if dst.ScopeVersion == "" {
		dst.ScopeVersion = src.ScopeVersion
	}
	if dst.SnapshotHash == "" {
		dst.SnapshotHash = src.SnapshotHash
	}
	if dst.CatalogAuthorityHash == "" {
		dst.CatalogAuthorityHash = src.CatalogAuthorityHash
	}
	if dst.TaxPremisePercent == 0 {
		dst.TaxPremisePercent = src.TaxPremisePercent
	}
	if dst.CanonicalAPIHost == "" {
		dst.CanonicalAPIHost = src.CanonicalAPIHost
	}
	if src.Public {
		dst.Public = true
	}
	return dst
}

func mergeCapacity(dst, src CapacitySnapshot) CapacitySnapshot {
	if dst.PolicyVersion == "" {
		dst.PolicyVersion = firstNonEmpty(src.PolicyVersion, CapacityPolicyV1)
	}
	if dst.Limit == 0 {
		dst.Limit = src.Limit
		if dst.Limit == 0 {
			dst.Limit = CapacityLimitV1
		}
	}
	if dst.Units == 0 {
		dst.Units = src.Units
		if dst.Units == 0 {
			dst.Units = 1
		}
	}
	if src.Eligibility != "" && (dst.Eligibility == "" || dst.Eligibility == EligibilityUnknown) {
		dst.Eligibility = src.Eligibility
	}
	if src.State != "" && dst.State != CapacityStateFinal {
		if dst.State == "" || dst.State == CapacityStateNone || rankCapacity(src.State) >= rankCapacity(dst.State) {
			dst.State = src.State
		}
	}
	if dst.HoldID == "" {
		dst.HoldID = src.HoldID
	}
	if dst.HoldCreatedAt == nil {
		dst.HoldCreatedAt = src.HoldCreatedAt
	}
	if dst.HoldExpiresAt == nil {
		dst.HoldExpiresAt = src.HoldExpiresAt
	}
	if dst.FinalReservedAt == nil {
		dst.FinalReservedAt = src.FinalReservedAt
	}
	if dst.WaitlistReason == "" {
		dst.WaitlistReason = src.WaitlistReason
	}
	if dst.RejectReason == "" {
		dst.RejectReason = src.RejectReason
	}
	return dst
}

func rankCapacity(s string) int {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case CapacityStateNone:
		return 0
	case CapacityStateWait:
		return 1
	case CapacityStateHold:
		return 2
	case CapacityStateOK:
		return 3
	case CapacityStateFinal:
		return 4
	case CapacityStateReject, CapacityStateExpired, CapacityStateRelease:
		return 5
	default:
		return 0
	}
}

func mergeProvider(dst, src ProviderRefs) ProviderRefs {
	if dst.CustomerID == "" {
		dst.CustomerID = src.CustomerID
	}
	if dst.CheckoutID == "" {
		dst.CheckoutID = src.CheckoutID
	}
	if dst.SubscriptionID == "" {
		dst.SubscriptionID = src.SubscriptionID
	}
	if dst.PaymentID == "" {
		dst.PaymentID = src.PaymentID
	}
	if dst.ExternalRef == "" {
		dst.ExternalRef = src.ExternalRef
	}
	if dst.ProviderEventID == "" {
		dst.ProviderEventID = src.ProviderEventID
	}
	if dst.PaymentMethod == "" {
		dst.PaymentMethod = src.PaymentMethod
	}
	if dst.ChargeID == "" {
		dst.ChargeID = firstNonEmpty(src.ChargeID, src.PaymentID)
	}
	return dst
}

func mergePayment(dst, src PaymentState) PaymentState {
	if dst.CanonicalStatus == "" {
		dst.CanonicalStatus = src.CanonicalStatus
	}
	if dst.RawProviderStatus == "" {
		dst.RawProviderStatus = src.RawProviderStatus
	}
	if dst.PrincipalCents == 0 {
		dst.PrincipalCents = src.PrincipalCents
	}
	if dst.ReviewStatus == "" {
		dst.ReviewStatus = src.ReviewStatus
	}
	if dst.EvidenceRef == "" {
		dst.EvidenceRef = src.EvidenceRef
	}
	if src.FinanceReviewReq {
		dst.FinanceReviewReq = true
	}
	return dst
}

func mergeGates(dst, src GateStates) GateStates {
	if dst.Legal == "" {
		dst.Legal = src.Legal
	}
	if dst.Accounting == "" {
		dst.Accounting = src.Accounting
	}
	if dst.Security == "" {
		dst.Security = src.Security
	}
	if dst.Delivery == "" {
		dst.Delivery = src.Delivery
	}
	if dst.Capacity == "" {
		dst.Capacity = src.Capacity
	}
	if dst.Publication == "" {
		dst.Publication = src.Publication
	}
	if dst.Finance == "" {
		dst.Finance = src.Finance
	}
	if dst.ApproverRef == "" {
		dst.ApproverRef = src.ApproverRef
	}
	if dst.EvidenceRef == "" {
		dst.EvidenceRef = src.EvidenceRef
	}
	if dst.EvidenceHash == "" {
		dst.EvidenceHash = src.EvidenceHash
	}
	return dst
}

func copyCommercialKeys(in *ObservedFacts, st CommercialState) {
	if in.Keys.OfferID == "" {
		in.Keys.OfferID = st.Offer.OfferID
	}
	if in.Keys.OfferVersion == "" {
		in.Keys.OfferVersion = st.Offer.OfferVersion
	}
	if in.Keys.TermsVersion == "" {
		in.Keys.TermsVersion = st.Offer.TermsVersion
	}
	if in.Keys.ExternalReference == "" {
		in.Keys.ExternalReference = st.Provider.ExternalRef
	}
	if in.Keys.ProviderEventID == "" {
		in.Keys.ProviderEventID = st.Provider.ProviderEventID
	}
	if in.Keys.ChargeID == "" {
		in.Keys.ChargeID = firstNonEmpty(st.Provider.ChargeID, st.Provider.PaymentID)
	}
	if in.Keys.CompanyRef == "" {
		in.Keys.CompanyRef = st.CompanyRef
	}
	if in.Keys.CNPJHash == "" {
		in.Keys.CNPJHash = st.CNPJHash
	}
	if in.Keys.HoldID == "" {
		in.Keys.HoldID = st.Capacity.HoldID
	}
}

func hasCommercialSnapshot(existing *Chain) bool {
	if existing == nil {
		return false
	}
	st := existing.Commercial
	if strings.TrimSpace(st.Offer.OfferID) != "" && st.Offer.OfferID != Unknown {
		return true
	}
	if hasTimelineType(st, EventTermsAccepted) || hasTimelineType(st, EventCheckoutCreated) || hasTimelineType(st, EventOfferSelected) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(st.Capacity.State)) {
	case CapacityStateOK, CapacityStateHold, CapacityStateFinal:
		return true
	default:
		return false
	}
}

func hasCheckoutOn(existing *Chain, ev CommercialEvent, st CommercialState) bool {
	if st.Provider.CheckoutID != "" || hasTimelineType(st, EventCheckoutCreated) {
		return true
	}
	if existing != nil && (existing.Commercial.Provider.CheckoutID != "" || hasTimelineType(existing.Commercial, EventCheckoutCreated)) {
		return true
	}
	// An inbound checkout id without a prior snapshot is not a gate pass.
	_ = ev
	return false
}

func paymentFinancialGate(existing *Chain, ev CommercialEvent, st CommercialState) (blocked bool, reason string) {
	if !hasCommercialSnapshot(existing) {
		return true, "payment without prior offer/capacity/checkout snapshot"
	}
	if !hasCheckoutOn(existing, ev, st) {
		providerCharge := firstNonEmpty(ev.ChargeID, ev.Provider.ChargeID, ev.Provider.PaymentID,
			st.Provider.ChargeID, st.Provider.PaymentID)
		providerCorrelation := firstNonEmpty(ev.CorrelationID, ev.ExternalReference, ev.Provider.ExternalRef)
		if providerCharge == "" || providerCorrelation == "" {
			return true, "payment before checkout and without a stable provider charge correlation"
		}
	}
	return false, ""
}

func paymentConfirmed(p PaymentState) bool {
	s := strings.ToLower(strings.TrimSpace(p.CanonicalStatus))
	return s == PaymentStatusConfirmed || s == PaymentStatusReceived
}

func isChargebackEvent(ev CommercialEvent) bool {
	blob := strings.ToUpper(strings.TrimSpace(ev.RawEventType + " " + ev.RawProviderStatus + " " + ev.Type))
	return strings.Contains(blob, "CHARGEBACK")
}

func capacityAllowsCheckout(c CapacitySnapshot, now time.Time) bool {
	if strings.EqualFold(c.State, CapacityStateOK) || strings.EqualFold(c.State, CapacityStateFinal) {
		return true
	}
	return holdValid(c, now)
}

func recurringActive(st CommercialState) bool {
	return st.Offer.BillingMode == BillingRecurring &&
		(st.Subscription.CanonicalStatus == EventSubscriptionActive || st.Subscription.ActiveAt != nil)
}

func applySLA(st *CommercialState) {
	if st.Offer.OfferID == OfferDiagnostico || st.Offer.BillingMode == BillingOneTime && st.Offer.OfferID == OfferDiagnostico {
		st.Delivery.SLAMinBusinessDays = SLADiagnosticoMinDays
		st.Delivery.SLAMaxBusinessDays = SLADiagnosticoMaxDays
	} else if st.Offer.BillingMode == BillingOneTime {
		st.Delivery.SLABusinessHours = SLAOneOffHours
	}
}

func offerInputDrift(frozen, incoming OfferSnapshot) bool {
	if incoming.OfferID != "" && frozen.OfferID != incoming.OfferID {
		return true
	}
	if incoming.OfferVersion != "" && frozen.OfferVersion != incoming.OfferVersion {
		return true
	}
	if incoming.TermsVersion != "" && frozen.TermsVersion != incoming.TermsVersion {
		return true
	}
	if incoming.TermsHash != "" && frozen.TermsHash != incoming.TermsHash {
		return true
	}
	if incoming.AmountCents != 0 && frozen.AmountCents != incoming.AmountCents {
		return true
	}
	if incoming.Currency != "" && frozen.Currency != incoming.Currency {
		return true
	}
	if incoming.BillingMode != "" && frozen.BillingMode != incoming.BillingMode {
		return true
	}
	if incoming.TotalCommitmentCents != 0 && frozen.TotalCommitmentCents != incoming.TotalCommitmentCents {
		return true
	}
	return false
}

func conflictingExternal(existingRef, incoming string, chain *Chain, ev CommercialEvent) bool {
	existingRef = strings.TrimSpace(existingRef)
	incoming = strings.TrimSpace(incoming)
	if existingRef == "" || incoming == "" || existingRef != incoming {
		return false
	}
	wantLead := strings.TrimSpace(chain.LeadID)
	gotLead := strings.TrimSpace(ev.LeadID)
	if wantLead != "" && gotLead != "" && wantLead != gotLead && wantLead != Unknown {
		return true
	}
	wantOffer := strings.TrimSpace(chain.Commercial.Offer.OfferID)
	gotOffer := firstNonEmpty(ev.Offer.OfferID, ev.OfferID)
	return wantOffer != "" && gotOffer != "" && wantOffer != gotOffer
}

func hasTimelineType(st CommercialState, typ string) bool {
	for _, r := range st.Timeline {
		if r.Type == typ {
			return true
		}
	}
	return false
}

func checkoutTime(existing *Chain) time.Time {
	if existing == nil {
		return time.Time{}
	}
	for _, r := range existing.Commercial.Timeline {
		if r.Type == EventCheckoutCreated {
			return r.OccurredAt
		}
	}
	return time.Time{}
}

func appendReceipt(tl []CommercialReceipt, ev CommercialEvent, typ string) []CommercialReceipt {
	id := strings.TrimSpace(ev.EventID)
	for _, r := range tl {
		if r.EventID == id && id != "" {
			return tl
		}
		if ev.ProviderEventID != "" && r.ProviderEventID == ev.ProviderEventID {
			return tl
		}
	}
	ing := ev.IngestedAt
	if ing.IsZero() {
		ing = time.Now().UTC()
	}
	return append(tl, CommercialReceipt{
		EventID:         id,
		Type:            typ,
		RawType:         ev.RawEventType,
		ProviderEventID: firstNonEmpty(ev.ProviderEventID, ev.Provider.ProviderEventID),
		ExternalRef:     firstNonEmpty(ev.ExternalReference, ev.Provider.ExternalRef),
		OccurredAt:      ev.OccurredAt,
		IngestedAt:      ing,
		EvidenceRef:     ev.EvidenceRef,
		RawStatus:       ev.RawProviderStatus,
		CanonicalStatus: ev.Type,
		ActorRef:        ev.ActorRef,
		EmailRouteClass: ev.EmailRouteClass,
		Source:          ev.Source,
		ProviderName:    ev.ProviderName,
		CohortID:        ev.CohortID,
		PolicyVersion:   ev.PolicyVersion,
		BounceClass:     ev.BounceClass,
		ReplyClass:      ev.ReplyClass,
		AccountPublicID: ev.AccountPublicID,
		TouchpointID:    ev.EntityPublicID,
		SMTPStatus:      ev.SMTPStatus,
		EnhancedStatus:  ev.EnhancedStatus,
		Diagnostic:      ev.Diagnostic,
	})
}

func evidenceRefs(ev CommercialEvent) []string {
	var out []string
	if s := strings.TrimSpace(ev.EvidenceRef); s != "" {
		out = append(out, s)
	}
	if s := strings.TrimSpace(ev.Gates.EvidenceRef); s != "" {
		out = append(out, s)
	}
	return out
}

// NormalizeEventType maps SCREAMING or alias names onto the shipped taxonomy.
func NormalizeEventType(t string) (canonical, raw string, unknown bool) {
	raw = strings.TrimSpace(t)
	low := strings.ToLower(raw)
	low = strings.ReplaceAll(low, "-", "_")
	switch low {
	case EventLeadReceived, EventLeadValidated, EventLeadRejected,
		EventHandoffAccepted, EventHandoffException,
		EventActionApproved, EventActionExecuted,
		EventReply, EventOutcomeObserved, EventMeeting, EventProposal,
		EventPipelineCreated, EventPipelineUpdated,
		EventWon, EventLost, EventUnknownState, EventRevenueEvidenced,
		EventLearningCandidate, EventXRayCompleted, EventPageView,
		EventCitation, EventCorrection,
		EventEmailAttempted, EventProviderAccepted, EventDelivered,
		EventHardBounce, EventSoftBounce, EventOptOut, EventSpamComplaint, EventNoReply,
		EventOfferViewed, EventOfferSelected, EventEligibilitySubmitted,
		EventCapacityApproved, EventCapacityRejected, EventCapacityWaitlisted,
		EventCapacityHoldExpired, EventCapacityReserved, EventCapacityReleased,
		EventTermsAccepted, EventCheckoutCreated, EventCheckoutExpired,
		EventPaymentCreated, EventPaymentPending, EventPaymentConfirmed,
		EventPaymentReceived, EventPaymentOverdue, EventPaymentRefunded,
		EventPaymentFailed, EventSubscriptionCreated, EventSubscriptionActive,
		EventSubscriptionEnded, EventSubscriptionCanceled,
		EventOnboardingStarted, EventServiceActivated, EventServicePaused,
		EventServiceEnded, EventRenewalDue, EventRenewed,
		EventCommercialExceptionOpen, EventCommercialExceptionRes,
		EventCommercialException, EventUnknownProvider:
		return low, raw, false
	}
	switch strings.ToUpper(strings.ReplaceAll(raw, "-", "_")) {
	case "OFFER_VIEWED":
		return EventOfferViewed, raw, false
	case "OFFER_SELECTED":
		return EventOfferSelected, raw, false
	case "ELIGIBILITY_SUBMITTED":
		return EventEligibilitySubmitted, raw, false
	case "CAPACITY_APPROVED":
		return EventCapacityApproved, raw, false
	case "CAPACITY_REJECTED":
		return EventCapacityRejected, raw, false
	case "CAPACITY_WAITLISTED":
		return EventCapacityWaitlisted, raw, false
	case "CAPACITY_HOLD_EXPIRED":
		return EventCapacityHoldExpired, raw, false
	case "CAPACITY_RESERVED":
		return EventCapacityReserved, raw, false
	case "CAPACITY_RELEASED":
		return EventCapacityReleased, raw, false
	case "TERMS_ACCEPTED":
		return EventTermsAccepted, raw, false
	case "CHECKOUT_CREATED":
		return EventCheckoutCreated, raw, false
	case "CHECKOUT_EXPIRED":
		return EventCheckoutExpired, raw, false
	case "PAYMENT_CREATED":
		return EventPaymentCreated, raw, false
	case "PAYMENT_PENDING":
		return EventPaymentPending, raw, false
	case "PAYMENT_CONFIRMED":
		return EventPaymentConfirmed, raw, false
	case "PAYMENT_RECEIVED":
		return EventPaymentReceived, raw, false
	case "PAYMENT_OVERDUE":
		return EventPaymentOverdue, raw, false
	case "PAYMENT_REFUNDED":
		return EventPaymentRefunded, raw, false
	case "CHARGEBACK", "PAYMENT_CHARGEBACK":
		return EventPaymentRefunded, raw, false
	case "PAYMENT_FAILED":
		return EventPaymentFailed, raw, false
	case "SUBSCRIPTION_CREATED":
		return EventSubscriptionCreated, raw, false
	case "SUBSCRIPTION_ACTIVE":
		return EventSubscriptionActive, raw, false
	case "SUBSCRIPTION_ENDED":
		return EventSubscriptionEnded, raw, false
	case "SUBSCRIPTION_CANCELED", "SUBSCRIPTION_CANCELLED":
		return EventSubscriptionCanceled, raw, false
	case "ONBOARDING_STARTED":
		return EventOnboardingStarted, raw, false
	case "SERVICE_ACTIVATED":
		return EventServiceActivated, raw, false
	case "SERVICE_PAUSED":
		return EventServicePaused, raw, false
	case "SERVICE_ENDED":
		return EventServiceEnded, raw, false
	case "RENEWAL_DUE":
		return EventRenewalDue, raw, false
	case "RENEWED":
		return EventRenewed, raw, false
	case "COMMERCIAL_EXCEPTION_OPENED", "COMMERCIAL_EXCEPTION":
		return EventCommercialExceptionOpen, raw, false
	case "COMMERCIAL_EXCEPTION_RESOLVED":
		return EventCommercialExceptionRes, raw, false
	case "PAYMENT_UNKNOWN":
		return EventUnknownProvider, raw, false
	case "UNKNOWN_PROVIDER_EVENT":
		return EventUnknownProvider, raw, false
	}
	if raw == "" {
		return "", raw, true
	}
	return EventUnknownProvider, raw, true
}

func isCommercialEvent(t string) bool {
	c, _, unk := NormalizeEventType(t)
	if unk {
		return true
	}
	switch c {
	case EventOfferViewed, EventOfferSelected, EventEligibilitySubmitted,
		EventCapacityApproved, EventCapacityRejected, EventCapacityWaitlisted,
		EventCapacityHoldExpired, EventCapacityReserved, EventCapacityReleased,
		EventTermsAccepted, EventCheckoutCreated, EventCheckoutExpired,
		EventPaymentCreated, EventPaymentPending, EventPaymentConfirmed,
		EventPaymentReceived, EventPaymentOverdue, EventPaymentRefunded,
		EventPaymentFailed, EventSubscriptionCreated, EventSubscriptionActive,
		EventSubscriptionEnded, EventSubscriptionCanceled,
		EventOnboardingStarted, EventServiceActivated, EventServicePaused,
		EventServiceEnded, EventRenewalDue, EventRenewed,
		EventCommercialExceptionOpen, EventCommercialExceptionRes,
		EventCommercialException, EventUnknownProvider,
		EventEmailAttempted, EventProviderAccepted, EventDelivered,
		EventHardBounce, EventSoftBounce, EventReply, EventOptOut,
		EventSpamComplaint, EventNoReply:
		return true
	default:
		return false
	}
}

// IsPrivateExtra reports the historical Extra R$10k exception. Never an offer.
func IsPrivateExtra(code string) bool {
	u := strings.ToUpper(strings.TrimSpace(code))
	if u == "" {
		return false
	}
	if u == ExtraPrivateCode || u == "EXTRA" || u == "CFG-EXTRA" {
		return true
	}
	return strings.Contains(u, "EXTRA") && (strings.Contains(u, "10K") || strings.Contains(u, "10000") || strings.Contains(u, "HIST"))
}

func knownCatalogOffer(id string) bool {
	switch strings.TrimSpace(id) {
	case OfferDiagnostico, OfferDirB2G180:
		return true
	default:
		return false
	}
}

// HashOfferSnapshot is the immutable offer fingerprint.
func HashOfferSnapshot(o OfferSnapshot) string {
	o.SnapshotHash = ""
	raw, _ := json.Marshal(o)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// HashCNPJ stores a minimized digest. Raw CNPJ never enters a metric key.
func HashCNPJ(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	digits := b.String()
	if digits == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("cnpj:" + digits))
	return hex.EncodeToString(sum[:8])
}

func looksLikeRawCNPJ(v string) bool {
	n := 0
	for _, r := range v {
		if unicode.IsDigit(r) {
			n++
		}
	}
	return n >= 8
}

// CatalogAuthorityHash is configurable so parallel catalog goals cannot stall this plane.
func CatalogAuthorityHash() string {
	if v := strings.TrimSpace(os.Getenv(EnvCatalogAuthorityHash)); v != "" {
		return v
	}
	return FrozenCatalogAuthorityHash
}
