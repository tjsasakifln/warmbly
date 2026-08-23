package intel

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// producerEnvelope is the web-cfg#88 body from scripts/offers/events.cjs.
// Extra Warmbly-only fields are ignored. Missing fields stay empty.
type producerEnvelope struct {
	Schema                string           `json:"schema"`
	Version               string           `json:"version"`
	EventID               string           `json:"event_id"`
	Type                  string           `json:"type"`
	OccurredAt            time.Time        `json:"occurred_at"`
	OfferID               string           `json:"offer_id"`
	OfferVersion          string           `json:"offer_version"`
	TermsVersion          string           `json:"terms_version"`
	ScopeVersion          string           `json:"scope_version"`
	ExternalReference     string           `json:"external_reference"`
	ProviderEventID       string           `json:"provider_event_id"`
	ProviderRawStatus     string           `json:"provider_raw_status"`
	CanonicalStatus       string           `json:"canonical_status"`
	AmountCents           *int64           `json:"amount_cents"`
	Currency              string           `json:"currency"`
	Source                string           `json:"source"`
	AssetID               string           `json:"asset_id"`
	CTAID                 string           `json:"cta_id"`
	CorrelationID         string           `json:"correlation_id"`
	LeadID                string           `json:"lead_id"`
	ReceiptID             string           `json:"receipt_id"`
	IdempotencyKey        string           `json:"idempotency_key"`
	ExceptionCode         string           `json:"exception_code"`
	FinancialConfirmation bool             `json:"financial_confirmation"`
	ReceivedRevenue       json.RawMessage  `json:"received_revenue"`
	Revenue               json.RawMessage  `json:"revenue"`
	Synthetic             bool             `json:"synthetic"`
	OrganizationID        string           `json:"organization_id"`
	Offer                 OfferSnapshot    `json:"offer"`
	Capacity              CapacitySnapshot `json:"capacity"`
	Provider              ProviderRefs     `json:"provider"`
}

// IsCommercialEventEnvelope reports a web-cfg/Warmbly commercial_event.v1
// body that is not a search observation and not an inbound lead.
func IsCommercialEventEnvelope(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	var peek struct {
		Type    string `json:"type"`
		Version string `json:"version"`
		Schema  string `json:"schema"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil {
		return false
	}
	if isSearchObservationType(peek.Type, peek.Version, peek.Schema) {
		return false
	}
	schema := firstNonEmpty(strings.TrimSpace(peek.Schema), strings.TrimSpace(peek.Version))
	return schema == EventSchemaV1
}

// ParseProducerCommercialEvent decodes the live producer body without
// inventing IDs, amounts, or revenue. Catalog fill only backfills empty
// snapshot fields for known public offer_ids.
func ParseProducerCommercialEvent(raw []byte) (CommercialEvent, error) {
	if !IsCommercialEventEnvelope(raw) {
		return CommercialEvent{}, fmt.Errorf("not %s", EventSchemaV1)
	}
	return ParseCommercialEvent(raw)
}

// NormalizeProducerCommercialEvent copies producer aliases onto the
// existing nested snapshot. Boolean revenue claims are never authority.
func NormalizeProducerCommercialEvent(ev CommercialEvent) CommercialEvent {
	if strings.TrimSpace(ev.Schema) == "" {
		switch strings.TrimSpace(ev.Version) {
		case EventSchemaV1, OrganicAttributionV1, OrganicDiscoveryContract:
			ev.Schema = ev.Version
		default:
			ev.Schema = EventSchemaV1
		}
	}
	if strings.TrimSpace(ev.Version) == "" {
		ev.Version = ev.Schema
	}
	if strings.TrimSpace(ev.OfferID) == "" {
		ev.OfferID = ev.Offer.OfferID
	}
	if strings.TrimSpace(ev.Offer.OfferID) == "" {
		ev.Offer.OfferID = ev.OfferID
	}
	if strings.TrimSpace(ev.Offer.OfferVersion) == "" {
		ev.Offer.OfferVersion = strings.TrimSpace(ev.ProducerOfferVersion)
	}
	if strings.TrimSpace(ev.Offer.TermsVersion) == "" {
		ev.Offer.TermsVersion = strings.TrimSpace(ev.ProducerTermsVersion)
	}
	if strings.TrimSpace(ev.Offer.ScopeVersion) == "" {
		ev.Offer.ScopeVersion = strings.TrimSpace(ev.ProducerScopeVersion)
	}
	if ev.Offer.AmountCents == 0 && ev.ProducerAmountCents != 0 {
		ev.Offer.AmountCents = ev.ProducerAmountCents
	}
	if strings.TrimSpace(ev.Offer.Currency) == "" {
		ev.Offer.Currency = firstNonEmpty(ev.ProducerCurrency, CurrencyBRL)
	}
	if strings.TrimSpace(ev.ExternalReference) == "" {
		ev.ExternalReference = ev.Provider.ExternalRef
	}
	if strings.TrimSpace(ev.Provider.ExternalRef) == "" {
		ev.Provider.ExternalRef = ev.ExternalReference
	}
	if strings.TrimSpace(ev.ProviderEventID) == "" {
		ev.ProviderEventID = ev.Provider.ProviderEventID
	}
	if strings.TrimSpace(ev.Provider.ProviderEventID) == "" {
		ev.Provider.ProviderEventID = ev.ProviderEventID
	}
	if strings.TrimSpace(ev.CorrelationID) == "" && isCommercialEvent(ev.Type) {
		ev.CorrelationID = firstNonEmpty(ev.ExternalReference, ev.Provider.ExternalRef,
			ev.ChargeID, ev.Provider.ChargeID, ev.Provider.PaymentID)
	}
	if strings.TrimSpace(ev.ChargeID) == "" {
		ev.ChargeID = firstNonEmpty(ev.Provider.ChargeID, ev.Provider.PaymentID)
	}
	if ev.Type == EventPaymentReceived && strings.TrimSpace(ev.PaymentID) == "" {
		ev.PaymentID = ev.Provider.PaymentID
	}
	if strings.TrimSpace(ev.RawProviderStatus) == "" {
		ev.RawProviderStatus = ev.ProducerCanonicalStatus
	}
	if strings.TrimSpace(ev.Payment.RawProviderStatus) == "" {
		ev.Payment.RawProviderStatus = firstNonEmpty(ev.RawProviderStatus, ev.ProducerCanonicalStatus)
	}
	if strings.TrimSpace(ev.Source) == "" && isCommercialEvent(ev.Type) {
		ev.Source = ProducerCONFENGEWeb
	}
	if frozen, ok := FrozenOffer(firstNonEmpty(ev.Offer.OfferID, ev.OfferID)); ok {
		if ev.Offer.AmountCents == 0 {
			ev.Offer.AmountCents = frozen.AmountCents
		}
		if ev.Offer.BillingMode == "" {
			ev.Offer.BillingMode = frozen.BillingMode
		}
		if ev.Offer.CommitmentMonths == 0 {
			ev.Offer.CommitmentMonths = frozen.CommitmentMonths
		}
		if ev.Offer.MaxPayments == 0 {
			ev.Offer.MaxPayments = frozen.MaxPayments
		}
		if ev.Offer.TotalCommitmentCents == 0 {
			ev.Offer.TotalCommitmentCents = frozen.TotalCommitmentCents
		}
		if ev.Offer.TermsVersion == "" {
			ev.Offer.TermsVersion = frozen.TermsVersion
		}
		if ev.Offer.OfferVersion == "" {
			ev.Offer.OfferVersion = frozen.OfferVersion
		}
		if ev.Offer.Cycle == "" {
			ev.Offer.Cycle = frozen.Cycle
		}
		if ev.Offer.ScopeVersion == "" {
			ev.Offer.ScopeVersion = frozen.ScopeVersion
		}
		ev.Offer.Public = true
	}
	if looksSyntheticID(ev.EventID) || looksSyntheticID(ev.ProviderEventID) || looksSyntheticID(ev.ExternalReference) || looksSyntheticID(ev.LeadID) {
		ev.Synthetic = true
		ev.RecordKind = RecordKindSynthetic
	}
	if ev.Type == EventCommercialException && strings.TrimSpace(ev.ProducerExceptionCode) != "" {
		ev.Type = EventCommercialExceptionOpen
	}
	return ev
}

func looksSyntheticID(v string) bool {
	u := strings.ToUpper(strings.TrimSpace(v))
	return strings.Contains(u, "SYNTHETIC")
}

func overlayProducerEnvelope(ev CommercialEvent, env producerEnvelope) CommercialEvent {
	if ev.Schema == "" {
		ev.Schema = env.Schema
	}
	if ev.Version == "" {
		ev.Version = env.Version
	}
	if ev.EventID == "" {
		ev.EventID = env.EventID
	}
	if ev.Type == "" {
		ev.Type = env.Type
	}
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = env.OccurredAt
	}
	if ev.OfferID == "" {
		ev.OfferID = env.OfferID
	}
	ev.ProducerOfferVersion = firstNonEmpty(ev.ProducerOfferVersion, env.OfferVersion)
	ev.ProducerTermsVersion = firstNonEmpty(ev.ProducerTermsVersion, env.TermsVersion)
	ev.ProducerScopeVersion = firstNonEmpty(ev.ProducerScopeVersion, env.ScopeVersion)
	if ev.ProducerAmountCents == 0 && env.AmountCents != nil {
		ev.ProducerAmountCents = *env.AmountCents
	}
	ev.ProducerCurrency = firstNonEmpty(ev.ProducerCurrency, env.Currency)
	ev.ProducerCanonicalStatus = firstNonEmpty(ev.ProducerCanonicalStatus, env.CanonicalStatus)
	ev.ProducerExceptionCode = firstNonEmpty(ev.ProducerExceptionCode, env.ExceptionCode)
	ev.FinancialConfirmation = ev.FinancialConfirmation || env.FinancialConfirmation
	if len(env.ReceivedRevenue) > 0 && string(env.ReceivedRevenue) == "true" {
		ev.ReceivedRevenueClaim = true
	}
	if len(env.Revenue) > 0 && string(env.Revenue) == "true" {
		ev.RevenueClaim = true
	}
	if ev.ExternalReference == "" {
		ev.ExternalReference = env.ExternalReference
	}
	if ev.ProviderEventID == "" {
		ev.ProviderEventID = env.ProviderEventID
	}
	if ev.RawProviderStatus == "" {
		ev.RawProviderStatus = env.ProviderRawStatus
	}
	if ev.Source == "" {
		ev.Source = env.Source
	}
	if ev.AssetID == "" {
		ev.AssetID = env.AssetID
	}
	if ev.CTAID == "" {
		ev.CTAID = env.CTAID
	}
	if ev.CorrelationID == "" {
		ev.CorrelationID = env.CorrelationID
	}
	if ev.LeadID == "" {
		ev.LeadID = env.LeadID
	}
	if ev.ReceiptID == "" {
		ev.ReceiptID = env.ReceiptID
	}
	if ev.IdempotencyKey == "" {
		ev.IdempotencyKey = env.IdempotencyKey
	}
	if ev.OrganizationID == "" {
		ev.OrganizationID = env.OrganizationID
	}
	if env.Synthetic {
		ev.Synthetic = true
	}
	return ev
}
