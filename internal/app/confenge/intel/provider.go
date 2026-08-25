package intel

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ProviderAdapter is the sandbox/disabled provider surface. It never
// mutates Asaas production.
type ProviderAdapter interface {
	Name() string
	Mode() string
	ParseWebhook(body []byte) (ProviderEvent, error)
	VerifySignature(secret, previous, header string, body []byte, now time.Time) error
	MapEvent(p ProviderEvent, orgID string) CommercialEvent
}

// FakeAdapter is the injectable local client. No network.
type FakeAdapter struct {
	mode string
}

func NewFakeAdapter() *FakeAdapter {
	mode := strings.TrimSpace(os.Getenv(EnvProviderMode))
	if mode == "" {
		mode = ProviderModeSandbox
	}
	if mode != ProviderModeSandbox && mode != ProviderModeDisabled {
		mode = ProviderModeDisabled
	}
	return &FakeAdapter{mode: mode}
}

func (a *FakeAdapter) Name() string { return "asaas-sandbox-fake" }
func (a *FakeAdapter) Mode() string {
	if a == nil {
		return ProviderModeDisabled
	}
	return a.mode
}

// ParseWebhook accepts a minimized provider payload. Unknown fields are listed, not dropped.
func (a *FakeAdapter) ParseWebhook(body []byte) (ProviderEvent, error) {
	if a == nil || a.mode == ProviderModeDisabled {
		return ProviderEvent{}, fmt.Errorf("provider adapter disabled")
	}
	var raw map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return ProviderEvent{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return ProviderEvent{}, fmt.Errorf("provider payload must contain one JSON object")
	}
	known := map[string]struct{}{
		"id": {}, "event": {}, "dateCreated": {}, "payment": {}, "checkout": {},
		"subscription": {}, "customer": {}, "externalReference": {}, "status": {},
		"value": {}, "netValue": {}, "billingType": {}, "paymentDate": {},
		"confirmedDate": {}, "clientPaymentDate": {}, "object": {},
	}
	var unknown []string
	for k := range raw {
		if _, ok := known[k]; !ok && safeProviderFieldName(k) {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	p := ProviderEvent{
		ProviderEventID: anyString(raw["id"]),
		ExternalRef:     anyString(raw["externalReference"]),
		RawType:         firstNonEmpty(anyString(raw["event"]), anyString(raw["object"])),
		RawStatus:       anyString(raw["status"]),
		OccurredAt:      parseProviderTime(anyString(raw["dateCreated"])),
		UnknownFields:   unknown,
		RawMinimized:    minimizeProviderRaw(raw),
	}
	if pay, ok := raw["payment"].(map[string]any); ok {
		p.PaymentID = anyString(pay["id"])
		p.CustomerID = anyString(pay["customer"])
		p.ExternalRef = firstNonEmpty(p.ExternalRef, anyString(pay["externalReference"]))
		p.RawStatus = firstNonEmpty(anyString(pay["status"]), p.RawStatus)
		p.AmountCents = reaisToCents(pay["value"])
		p.Currency = CurrencyBRL
		p.PaymentMethod = anyString(pay["billingType"])
		if p.OccurredAt.IsZero() {
			p.OccurredAt = parseProviderTime(firstNonEmpty(anyString(pay["paymentDate"]), anyString(pay["confirmedDate"])))
		}
	}
	if sub, ok := raw["subscription"].(map[string]any); ok {
		p.SubscriptionID = anyString(sub["id"])
		p.CustomerID = firstNonEmpty(p.CustomerID, anyString(sub["customer"]))
		p.ExternalRef = firstNonEmpty(p.ExternalRef, anyString(sub["externalReference"]))
		p.RawStatus = firstNonEmpty(p.RawStatus, anyString(sub["status"]))
	}
	if chk, ok := raw["checkout"].(map[string]any); ok {
		p.CheckoutID = anyString(chk["id"])
		p.ExternalRef = firstNonEmpty(p.ExternalRef, anyString(chk["externalReference"]))
	}
	if p.CustomerID == "" {
		p.CustomerID = anyString(raw["customer"])
	}
	if p.ProviderEventID == "" {
		return p, fmt.Errorf("provider event id required")
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "provider_event_type", value: p.RawType},
		{name: "provider_status", value: p.RawStatus},
		{name: "payment_method", value: p.PaymentMethod},
	} {
		if field.value != "" && !safeProviderFieldName(field.value) {
			return p, fmt.Errorf("%s must be an enum token", field.name)
		}
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "provider_event_id", value: p.ProviderEventID},
		{name: "external_reference", value: p.ExternalRef},
		{name: "provider_customer_id", value: p.CustomerID},
		{name: "provider_checkout_id", value: p.CheckoutID},
		{name: "provider_subscription_id", value: p.SubscriptionID},
		{name: "provider_payment_id", value: p.PaymentID},
	} {
		if err := validateOpaqueIdentifier(field.name, field.value); err != nil {
			return p, err
		}
	}
	return p, nil
}

func (a *FakeAdapter) VerifySignature(secret, previous, header string, body []byte, now time.Time) error {
	if strings.TrimSpace(secret) == "" && strings.TrimSpace(previous) == "" {
		return fmt.Errorf("no webhook secret configured")
	}
	if VerifyProviderHMAC(secret, header, body, now, 5*time.Minute) {
		return nil
	}
	if previous != "" && VerifyProviderHMAC(previous, header, body, now, 5*time.Minute) {
		return nil
	}
	return fmt.Errorf("invalid or rotated secret")
}

func (a *FakeAdapter) MapEvent(p ProviderEvent, orgID string) CommercialEvent {
	typ := mapProviderType(p.RawType, p.RawStatus)
	return CommercialEvent{
		EventID:           "prov-" + p.ProviderEventID,
		Version:           "1",
		Schema:            EventSchemaV1,
		Type:              typ,
		RawEventType:      p.RawType,
		RawProviderStatus: p.RawStatus,
		OccurredAt:        p.OccurredAt,
		IngestedAt:        time.Now().UTC(),
		Timezone:          "America/Sao_Paulo",
		OrganizationID:    orgID,
		CorrelationID:     firstNonEmpty(p.ExternalRef, p.PaymentID),
		ProviderEventID:   p.ProviderEventID,
		ExternalReference: p.ExternalRef,
		Synthetic:         true,
		Provider: ProviderRefs{
			CustomerID:      p.CustomerID,
			CheckoutID:      p.CheckoutID,
			SubscriptionID:  p.SubscriptionID,
			PaymentID:       p.PaymentID,
			ExternalRef:     p.ExternalRef,
			ProviderEventID: p.ProviderEventID,
			PaymentMethod:   p.PaymentMethod,
			ChargeID:        p.PaymentID,
		},
		Payment: PaymentState{
			RawProviderStatus: p.RawStatus,
			PrincipalCents:    p.AmountCents,
			// Amount is principal only. Received revenue is applied by
			// ApplyCommercialTransition after a prior commercial snapshot.
			ReceivedCents: 0,
		},
		Offer: OfferSnapshot{
			Currency: firstNonEmpty(p.Currency, CurrencyBRL),
		},
	}
}

func mapProviderType(rawType, rawStatus string) string {
	blob := strings.ToUpper(strings.TrimSpace(rawType + " " + rawStatus))
	switch {
	case strings.Contains(blob, "PAYMENT_RECEIVED") || blob == "RECEIVED":
		return EventPaymentReceived
	case strings.Contains(blob, "PAYMENT_CONFIRMED") || blob == "CONFIRMED":
		return EventPaymentConfirmed
	case strings.Contains(blob, "PAYMENT_CREATED"):
		return EventPaymentCreated
	case strings.Contains(blob, "PAYMENT_OVERDUE") || blob == "OVERDUE":
		return EventPaymentOverdue
	case strings.Contains(blob, "PAYMENT_REFUNDED") || strings.Contains(blob, "CHARGEBACK") || blob == "REFUNDED":
		return EventPaymentRefunded
	case strings.Contains(blob, "PAYMENT_DELETED") || blob == "FAILED":
		return EventPaymentFailed
	case strings.Contains(blob, "PAYMENT_UPDATED") && strings.Contains(blob, "PENDING"):
		return EventPaymentPending
	case blob == "PENDING":
		return EventPaymentPending
	case strings.Contains(blob, "CHECKOUT"):
		return EventCheckoutCreated
	case strings.Contains(blob, "SUBSCRIPTION") && (strings.Contains(blob, "DELETED") || strings.Contains(blob, "CANCELED") || strings.Contains(blob, "CANCELLED")):
		return EventSubscriptionCanceled
	case strings.Contains(blob, "SUBSCRIPTION_CREATED") || (strings.Contains(blob, "SUBSCRIPTION") && strings.Contains(blob, "CREATED")):
		return EventSubscriptionCreated
	case strings.Contains(blob, "SUBSCRIPTION") && strings.Contains(blob, "ACTIVE"):
		return EventSubscriptionActive
	default:
		return EventUnknownProvider
	}
}

// SignProviderHMAC matches the inbound t=,v1= envelope.
func SignProviderHMAC(secret string, ts time.Time, body []byte) string {
	msg := fmt.Sprintf("%d.", ts.Unix()) + string(body)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(msg))
	return "t=" + fmt.Sprintf("%d", ts.Unix()) + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

// VerifyProviderHMAC is constant-time on the hex digest.
func VerifyProviderHMAC(secret, header string, body []byte, now time.Time, maxSkew time.Duration) bool {
	if strings.TrimSpace(secret) == "" || strings.TrimSpace(header) == "" {
		return false
	}
	var tUnix int64
	var sig string
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "t=") {
			fmt.Sscanf(strings.TrimPrefix(part, "t="), "%d", &tUnix)
		}
		if strings.HasPrefix(part, "v1=") {
			sig = strings.TrimPrefix(part, "v1=")
		}
	}
	if tUnix == 0 || sig == "" {
		return false
	}
	ts := time.Unix(tUnix, 0).UTC()
	if now.Sub(ts) > maxSkew || ts.Sub(now) > maxSkew {
		return false
	}
	expected := SignProviderHMAC(secret, ts, body)
	want := expected[strings.Index(expected, "v1=")+3:]
	return hmac.Equal([]byte(want), []byte(sig))
}

// IngestProviderWebhook persist-first acks, then processes. Unavailable store is not a silent drop.
func IngestProviderWebhook(store Store, adapter ProviderAdapter, orgID, secret, previous, header string, body []byte, now time.Time) (WebhookAck, error) {
	if adapter == nil {
		adapter = NewFakeAdapter()
	}
	if adapter.Mode() == ProviderModeDisabled {
		return WebhookAck{}, fmt.Errorf("provider adapter disabled")
	}
	if err := adapter.VerifySignature(secret, previous, header, body, now); err != nil {
		ex := Exception{
			OrganizationID: orgID, Code: ExceptionInvalidSecret, CodeVersion: ExceptionCodeVersion,
			Reason: err.Error(), NextAction: "rotate/check secret; do not process", Held: true,
			Owner: "provider-webhook", OpenedAt: now, At: now, RetryState: "pending",
		}
		if store != nil {
			_ = store.PutException(ex)
		}
		return WebhookAck{Acked: false, Held: true, Join: JoinResult{Exceptions: []Exception{ex}, Held: true}}, err
	}
	parsed, err := adapter.ParseWebhook(body)
	if err != nil {
		return WebhookAck{}, err
	}
	rec := EventReceipt{
		OrganizationID: orgID, ProviderEventID: parsed.ProviderEventID,
		ExternalRef: parsed.ExternalRef, Type: parsed.RawType, RawType: parsed.RawType,
		RawStatus: parsed.RawStatus, At: now,
	}
	if rs, ok := store.(interface {
		PutEventReceipt(EventReceipt) (EventReceipt, bool, error)
	}); ok {
		saved, created, perr := rs.PutEventReceipt(rec)
		if perr != nil {
			ex := Exception{
				OrganizationID: orgID, Code: ExceptionUnavailable, CodeVersion: ExceptionCodeVersion,
				Reason: "durable receipt failed: " + perr.Error(), NextAction: "retry same provider event id",
				Held: true, At: now, OpenedAt: now, Owner: "provider-webhook", RetryState: "pending",
			}
			return WebhookAck{Acked: false, Held: true, Join: JoinResult{Exceptions: []Exception{ex}, Held: true}}, perr
		}
		if parsed.OccurredAt.IsZero() {
			parsed.OccurredAt = saved.At
			if parsed.OccurredAt.IsZero() {
				parsed.OccurredAt = now
			}
		}
		ack := WebhookAck{ReceiptID: saved.ID, Replay: !created, Acked: true}
		if !created {
			ack.Processed = saved.Processed
			ch, findErr := findSeenEvent(store, CommercialEvent{OrganizationID: orgID, ProviderEventID: parsed.ProviderEventID, EventID: "prov-" + parsed.ProviderEventID})
			if findErr != nil {
				ex := storeUnavailableException(CommercialEvent{OrganizationID: orgID}, saved.Identity, "find provider receipt chain", findErr)
				_ = store.PutException(ex)
				ack.Held = true
				ack.Join = JoinResult{Exceptions: []Exception{ex}, Held: true}
				return ack, nil
			}
			if ch != nil {
				ack.Join = JoinResult{Chain: *ch, Replay: true, Held: ch.Held}
			}
			if saved.Processed {
				if ch == nil {
					ex := storeUnavailableException(CommercialEvent{OrganizationID: orgID}, saved.Identity, "get processed provider receipt chain", fmt.Errorf("chain %q is missing", saved.Identity))
					_ = store.PutException(ex)
					ack.Processed = false
					ack.Held = true
					ack.Join = JoinResult{Exceptions: []Exception{ex}, Held: true}
				}
				return ack, nil
			}
		}
		ev := adapter.MapEvent(parsed, orgID)
		ev.Synthetic = true
		ev.AllowReceiptRetry = !created
		join := IngestEvent(store, ev)
		applied := commercialReceiptApplied(join.Chain, ev)
		ack.Processed = applied && !JoinUnavailable(join)
		ack.Held = join.Held
		if ms, ok := store.(interface {
			MarkReceiptProcessed(string, string) error
		}); ok && join.Replay && applied && !JoinUnavailable(join) {
			if err := ms.MarkReceiptProcessed(orgID, parsed.ProviderEventID); err != nil {
				ex := storeUnavailableException(ev, join.Chain.Identity, "mark receipt processed", err)
				_ = store.PutException(ex)
				join.Exceptions = append(join.Exceptions, ex)
				join.Held = true
				ack.Processed = false
				ack.Held = true
			}
		}
		ack.Join = join
		return ack, nil
	}
	if parsed.OccurredAt.IsZero() {
		parsed.OccurredAt = now
	}
	ev := adapter.MapEvent(parsed, orgID)
	ev.Synthetic = true
	join := IngestEvent(store, ev)
	return WebhookAck{ReceiptID: ev.EventID, Acked: true, Processed: commercialReceiptApplied(join.Chain, ev), Held: join.Held, Join: join}, nil
}

func anyString(v any) string {
	if value, ok := v.(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func reaisToCents(v any) int64 {
	var value string
	switch amount := v.(type) {
	case json.Number:
		value = amount.String()
	case string:
		value = strings.TrimSpace(amount)
	default:
		return 0
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || len(parts[0]) == 0 ||
		(len(parts) == 2 && (len(parts[1]) == 0 || len(parts[1]) > 2)) {
		return 0
	}
	for _, part := range parts {
		for _, character := range part {
			if character < '0' || character > '9' {
				return 0
			}
		}
	}
	reais, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	fraction += strings.Repeat("0", 2-len(fraction))
	centavos, err := strconv.ParseInt(fraction, 10, 64)
	if err != nil || reais > (int64(1<<63-1)-centavos)/100 {
		return 0
	}
	return reais*100 + centavos
}

func safeProviderFieldName(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func parseProviderTime(v string) time.Time {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func minimizeProviderRaw(raw map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range []string{"id", "event", "status", "externalReference", "object"} {
		if value := anyString(raw[k]); value != "" {
			if (k == "event" || k == "status" || k == "object") && !safeProviderFieldName(value) {
				continue
			}
			out[k] = value
		}
	}
	return out
}
