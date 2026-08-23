package intel

import (
	"sort"
	"strings"
	"time"
)

const (
	AvailabilityObserved = "OBSERVED"
	AvailabilityUnknown  = "UNKNOWN"
	DecisionGo           = "GO"
	DecisionNoGo         = "NO-GO"
	DecisionWait         = "WAIT"
)

type ObservedText struct {
	Availability string     `json:"availability"`
	Value        string     `json:"value,omitempty"`
	ObservedAt   *time.Time `json:"observed_at,omitempty"`
}

type ObservedMoney struct {
	Availability string     `json:"availability"`
	ID           string     `json:"id,omitempty"`
	Status       string     `json:"status,omitempty"`
	AmountCents  *int64     `json:"amount_cents,omitempty"`
	Currency     string     `json:"currency,omitempty"`
	ObservedAt   *time.Time `json:"observed_at,omitempty"`
}

// WeeklyRevenueChain is a read model over Warmbly facts. It does not mutate Asaas.
type WeeklyRevenueChain struct {
	SchemaVersion     string                      `json:"schema_version"`
	Identity          CanonicalCommercialIdentity `json:"canonical_identity"`
	LatestDeliverable ObservedText                `json:"latest_deliverable"`
	LatestEvidence    ObservedText                `json:"latest_evidence"`
	Decision          ObservedText                `json:"decision"`
	Responsible       ObservedText                `json:"responsible"`
	Deadline          ObservedText                `json:"deadline"`
	NextAction        ObservedText                `json:"next_action"`
	Proposal          ObservedText                `json:"proposal"`
	Charge            ObservedMoney               `json:"charge"`
	Receipt           ObservedMoney               `json:"receipt"`
	Held              bool                        `json:"held"`
	Synthetic         bool                        `json:"synthetic"`
}

func canonicalID(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == Unknown {
		return Unknown
	}
	return v
}

func CanonicalIdentityOf(c Chain) CanonicalCommercialIdentity {
	chargeID := firstNonEmpty(c.ChargeID, c.Keys.ChargeID, c.Commercial.Provider.ChargeID,
		c.Commercial.Provider.PaymentID)
	paymentID := firstNonEmpty(c.PaymentID, c.Keys.PaymentID)
	if paymentID == "" && c.Commercial.Payment.ReceivedCount > 0 {
		paymentID = c.Commercial.Provider.PaymentID
	}
	return CanonicalCommercialIdentity{
		CorrelationID: canonicalID(firstNonEmpty(c.CorrelationID, c.Keys.CorrelationID,
			c.Keys.ExternalReference, c.Commercial.Provider.ExternalRef)),
		AccountID:     canonicalID(firstNonEmpty(c.AccountID, c.Keys.AccountID)),
		OpportunityID: canonicalID(firstNonEmpty(c.OpportunityID, c.Keys.OpportunityID)),
		OfferID:       canonicalID(firstNonEmpty(c.OfferID, c.Keys.OfferID, c.Commercial.Offer.OfferID)),
		ProposalID:    canonicalID(firstNonEmpty(c.ProposalID, c.Keys.ProposalID)),
		ChargeID:      canonicalID(chargeID),
		PaymentID:     canonicalID(paymentID),
	}
}

func IsWeeklyRevenueChain(c Chain) bool {
	identity := CanonicalIdentityOf(c)
	return identity.OpportunityID != Unknown || identity.ProposalID != Unknown ||
		identity.ChargeID != Unknown || identity.PaymentID != Unknown ||
		c.Commercial.Control.LatestDeliverableID != "" || c.Commercial.Control.Decision != ""
}

func observedText(value string, at *time.Time) ObservedText {
	value = strings.TrimSpace(value)
	if value == "" || value == Unknown {
		return ObservedText{Availability: AvailabilityUnknown}
	}
	return ObservedText{Availability: AvailabilityObserved, Value: value, ObservedAt: at}
}

func WeeklyRevenueView(c Chain) WeeklyRevenueChain {
	identity := CanonicalIdentityOf(c)
	control := c.Commercial.Control
	decision := normalizeDecision(control.Decision)
	deadline := AvailabilityUnknown
	deadlineValue := ""
	if control.Deadline != nil && !control.Deadline.IsZero() {
		deadline = AvailabilityObserved
		deadlineValue = control.Deadline.UTC().Format(time.RFC3339)
	}
	proposalAt := c.ProposalAt
	proposal := observedText(identity.ProposalID, proposalAt)
	charge := ObservedMoney{Availability: AvailabilityUnknown}
	if identity.ChargeID != Unknown {
		charge = ObservedMoney{
			Availability: AvailabilityObserved,
			ID:           identity.ChargeID,
			Status:       canonicalID(c.Commercial.Payment.CanonicalStatus),
			Currency:     canonicalID(c.Commercial.Offer.Currency),
			ObservedAt:   latestTimelineAt(c, EventPaymentCreated, EventCheckoutCreated),
		}
		if c.Commercial.Payment.PrincipalCents > 0 {
			amount := c.Commercial.Payment.PrincipalCents
			charge.AmountCents = &amount
		}
	}
	receipt := ObservedMoney{Availability: AvailabilityUnknown}
	if c.Commercial.Payment.ReceivedCount > 0 && identity.PaymentID != Unknown {
		amount := c.Commercial.Payment.ReceivedCents
		receipt = ObservedMoney{
			Availability: AvailabilityObserved,
			ID:           identity.PaymentID,
			Status:       PaymentStatusReceived,
			AmountCents:  &amount,
			Currency:     canonicalID(c.Commercial.Offer.Currency),
			ObservedAt:   c.Commercial.Payment.ReceivedAt,
		}
	}
	return WeeklyRevenueChain{
		SchemaVersion:     "confenge.weekly_revenue_chain.v1",
		Identity:          identity,
		LatestDeliverable: observedText(control.LatestDeliverableID, control.LatestObservedAt),
		LatestEvidence:    observedText(control.LatestEvidenceRef, control.LatestObservedAt),
		Decision:          observedText(decision, control.LatestObservedAt),
		Responsible:       observedText(control.Responsible, control.LatestObservedAt),
		Deadline:          ObservedText{Availability: deadline, Value: deadlineValue, ObservedAt: control.LatestObservedAt},
		NextAction:        observedText(control.NextAction, control.LatestObservedAt),
		Proposal:          proposal,
		Charge:            charge,
		Receipt:           receipt,
		Held:              c.Held,
		Synthetic:         c.Synthetic,
	}
}

func normalizeDecision(v string) string {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case DecisionGo:
		return DecisionGo
	case "NO_GO", "NOGO", DecisionNoGo:
		return DecisionNoGo
	case DecisionWait:
		return DecisionWait
	default:
		return Unknown
	}
}

func latestTimelineAt(c Chain, types ...string) *time.Time {
	wanted := make(map[string]struct{}, len(types))
	for _, typ := range types {
		wanted[typ] = struct{}{}
	}
	var latest *time.Time
	for _, receipt := range c.Commercial.Timeline {
		if _, ok := wanted[receipt.Type]; !ok || receipt.OccurredAt.IsZero() {
			continue
		}
		if latest == nil || receipt.OccurredAt.After(*latest) {
			t := receipt.OccurredAt
			latest = &t
		}
	}
	return latest
}

func SortWeeklyRevenueChains(rows []WeeklyRevenueChain) {
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Identity.CorrelationID < rows[j].Identity.CorrelationID
	})
}
