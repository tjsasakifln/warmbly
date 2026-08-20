package intel

import (
	"sort"
	"strings"
	"time"
)

// OfferExecutiveRow is the minimum executive view keyed by offer_id/version.
// Denominators are explicit. UNKNOWN stays visible. Not a vanity funnel.
type OfferExecutiveRow struct {
	OfferID                string           `json:"offer_id"`
	OfferVersion           string           `json:"offer_version"`
	Selected               int              `json:"selected"`
	CheckoutCreated        int              `json:"checkout_created"`
	PaymentPending         int              `json:"payment_pending"`
	PaymentReceived        int              `json:"payment_received"`
	OnboardingStarted      int              `json:"onboarding_started"`
	ServiceActive          int              `json:"service_active"`
	Overdue                int              `json:"overdue"`
	Refunded               int              `json:"refund"`
	Canceled               int              `json:"cancel"`
	QualifiedPipeline      int              `json:"qualified_pipeline"`
	ReceivedRevenueCents   int64            `json:"received_revenue_cents"`
	ContractedRevenueCents int64            `json:"contracted_revenue_cents"`
	MRRCents               int64            `json:"mrr_cents"`
	Exceptions             int              `json:"exceptions"`
	Unknown                int              `json:"unknown"`
	OnboardingBlocked      int              `json:"onboarding_blocked"`
	OnboardingEligible     int              `json:"onboarding_eligible"`
	DenominatorChains      int              `json:"denominator_chains"`
	Stages                 OfferStageClocks `json:"stage_timestamps"`
}

// OfferStageClocks are observed timestamps for #55. No SLA is invented.
type OfferStageClocks struct {
	EligibilityAt *time.Time `json:"eligibility_submitted_at,omitempty"`
	CapacityAt    *time.Time `json:"capacity_decision_at,omitempty"`
	TermsAt       *time.Time `json:"terms_accepted_at,omitempty"`
	CheckoutAt    *time.Time `json:"checkout_created_at,omitempty"`
	PaymentAt     *time.Time `json:"payment_received_at,omitempty"`
	OnboardingAt  *time.Time `json:"onboarding_started_at,omitempty"`
	ActivationAt  *time.Time `json:"service_activated_at,omitempty"`
}

func offerExecKey(offerID, version string) string {
	o := firstNonEmpty(strings.TrimSpace(offerID), Unknown)
	v := firstNonEmpty(strings.TrimSpace(version), Unknown)
	return o + "\x00" + v
}

func addOfferExecutive(rows map[string]*OfferExecutiveRow, c Chain) {
	st := c.Commercial
	offerID := firstNonEmpty(st.Offer.OfferID, c.OfferID, c.Keys.OfferID)
	version := firstNonEmpty(st.Offer.OfferVersion, c.Keys.OfferVersion)
	key := offerExecKey(offerID, version)
	row, ok := rows[key]
	if !ok {
		row = &OfferExecutiveRow{OfferID: firstNonEmpty(offerID, Unknown), OfferVersion: firstNonEmpty(version, Unknown)}
		rows[key] = row
	}
	row.DenominatorChains++
	if hasTimelineType(st, EventOfferSelected) || hasTimelineType(st, EventOfferViewed) {
		row.Selected++
	}
	if hasTimelineType(st, EventCheckoutCreated) {
		row.CheckoutCreated++
	}
	status := strings.ToLower(strings.TrimSpace(st.Payment.CanonicalStatus))
	if st.Payment.ReceivedCount > 0 || hasTimelineType(st, EventPaymentReceived) {
		row.PaymentReceived++
	}
	switch status {
	case PaymentStatusPending, PaymentStatusCreated:
		row.PaymentPending++
	case PaymentStatusOverdue:
		row.Overdue++
	case PaymentStatusRefunded:
		row.Refunded++
	case PaymentStatusUnknown:
		row.Unknown++
	}
	dec := firstNonEmpty(st.Delivery.OnboardingDecision, DecideOnboarding(c))
	switch dec {
	case OnboardingEligible:
		row.OnboardingEligible++
	case OnboardingStarted:
		row.OnboardingStarted++
	case ServiceActive:
		row.ServiceActive++
		row.OnboardingStarted++
	case OnboardingBlocked:
		row.OnboardingBlocked++
	default:
		row.Unknown++
	}
	if st.Subscription.CanceledAt != nil || st.Subscription.CanonicalStatus == EventSubscriptionCanceled {
		row.Canceled++
	}
	if c.Held {
		row.Exceptions++
	}
	if !c.Held && status != PaymentStatusReceived && (c.Qualified || hasTimelineType(st, EventOfferSelected) || hasTimelineType(st, EventCheckoutCreated)) {
		row.QualifiedPipeline++
	}
	if !c.Held {
		row.ReceivedRevenueCents += st.Payment.ReceivedCents
		row.ContractedRevenueCents += st.Payment.ContractedCents
		row.MRRCents += st.Payment.MRRCents
	}
	mergeOfferStage(&row.Stages, st)
}

func mergeOfferStage(dst *OfferStageClocks, st CommercialState) {
	set := func(dst **time.Time, src *time.Time) {
		if src == nil || src.IsZero() {
			return
		}
		if *dst == nil || src.Before(**dst) {
			cp := *src
			*dst = &cp
		}
	}
	set(&dst.EligibilityAt, timelineTime(st, EventEligibilitySubmitted))
	set(&dst.CapacityAt, firstTime(timelineTime(st, EventCapacityApproved), timelineTime(st, EventCapacityRejected), timelineTime(st, EventCapacityWaitlisted)))
	set(&dst.TermsAt, firstTime(st.Delivery.OneOffAcceptedAt, timelineTime(st, EventTermsAccepted)))
	set(&dst.CheckoutAt, timelineTime(st, EventCheckoutCreated))
	set(&dst.PaymentAt, st.Payment.ReceivedAt)
	set(&dst.OnboardingAt, st.Delivery.OnboardingStartedAt)
	set(&dst.ActivationAt, st.Delivery.ServiceActivatedAt)
}

func timelineTime(st CommercialState, typ string) *time.Time {
	for i := range st.Timeline {
		if st.Timeline[i].Type == typ && !st.Timeline[i].OccurredAt.IsZero() {
			t := st.Timeline[i].OccurredAt
			return &t
		}
	}
	return nil
}

func firstTime(vs ...*time.Time) *time.Time {
	for _, t := range vs {
		if t != nil && !t.IsZero() {
			return t
		}
	}
	return nil
}

func mapOfferExecutive(rows map[string]*OfferExecutiveRow) []OfferExecutiveRow {
	out := make([]OfferExecutiveRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].OfferID == out[j].OfferID {
			return out[i].OfferVersion < out[j].OfferVersion
		}
		return out[i].OfferID < out[j].OfferID
	})
	return out
}
