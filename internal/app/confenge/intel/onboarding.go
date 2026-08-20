package intel

import (
	"strings"
)

const (
	OnboardingBlocked  = "ONBOARDING_BLOCKED"
	OnboardingEligible = "ONBOARDING_ELIGIBLE"
	OnboardingStarted  = "ONBOARDING_STARTED"
	ServiceActive      = "SERVICE_ACTIVE"
)

// DecideOnboarding is the shipped four-way decision. Checkout, callback,
// subscription created, and payment_confirmed never start delivery.
func DecideOnboarding(c Chain) string {
	return decideOnboardingState(c.Commercial, c.Held)
}

func decideOnboardingState(st CommercialState, held bool) string {
	if st.Delivery.ServiceActivatedAt != nil && st.Delivery.ServiceEndedAt == nil {
		return ServiceActive
	}
	if st.Delivery.OnboardingStartedAt != nil {
		return OnboardingStarted
	}
	if onboardingEligible(st, held) {
		return OnboardingEligible
	}
	return OnboardingBlocked
}

func onboardingEligible(st CommercialState, held bool) bool {
	if IsPrivateExtra(st.Offer.OfferID) || IsPrivateExtra(st.Offer.PublicCode) || IsPrivateExtra(st.Offer.InternalCode) {
		return false
	}
	if strings.TrimSpace(st.Offer.OfferID) == "" || strings.TrimSpace(st.Offer.TermsVersion) == "" {
		return false
	}
	if !paymentReceived(st.Payment) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(st.Payment.CanonicalStatus)) {
	case PaymentStatusRefunded, PaymentStatusFailed, PaymentStatusOverdue:
		return false
	}
	switch st.Capacity.State {
	case CapacityStateOK, CapacityStateFinal, CapacityStateHold:
	default:
		return false
	}
	if held {
		return false
	}
	return true
}

func paymentReceived(p PaymentState) bool {
	if strings.ToLower(strings.TrimSpace(p.CanonicalStatus)) != PaymentStatusReceived {
		return false
	}
	return p.ReceivedCents > 0 || p.ReceivedCount > 0
}
