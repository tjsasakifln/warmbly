package liveintel

import (
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/config"
)

// EnvWatchBaseURL overrides the host the opt-out link points at. Unset falls
// back to the platform domain, so a deployment that forgets it still mints a
// resolvable link rather than a relative path.
const EnvWatchBaseURL = "CONFENGE_INTEL_WATCH_BASE_URL"

// watchBaseURL is the scheme+host every INTEL_WATCH opt-out link is built on.
func watchBaseURL() string {
	raw := strings.TrimRight(strings.TrimSpace(os.Getenv(EnvWatchBaseURL)), "/")
	if raw == "" {
		return "https://" + config.Domain
	}
	return raw
}

// UnsubscribeURL builds the one-click opt-out link for one subscription. It
// returns "" when no secret is configured: a watch mail that cannot carry a
// working opt-out must not be sent at all.
func UnsubscribeURL(organizationID, subscriptionID uuid.UUID) string {
	token := UnsubscribeToken(organizationID, subscriptionID)
	if token == "" {
		return ""
	}
	query := url.Values{}
	query.Set("oid", organizationID.String())
	query.Set("sid", subscriptionID.String())
	query.Set("t", token)
	return watchBaseURL() + "/unsubscribe/watch?" + query.Encode()
}

// WatchMessage is the composed notification for one watcher.
type WatchMessage struct {
	Subject        string
	BodyText       string
	UnsubscribeURL string
}

// Payload keys the composer reads when the producer supplies them. Anything
// else in the payload is rendered generically, so a producer adding a field
// never silently drops it from the notification.
const (
	PayloadKeyTitle     = "title"
	PayloadKeySummary   = "summary"
	PayloadKeyPublicURL = "public_url"
	PayloadKeyDeadline  = "deadline"
)

// watchEventHeadline is the human sentence for each event type. The closed set
// is mirrored from EventTypes, so a new type must be given copy here.
func watchEventHeadline(eventType EventType) string {
	switch eventType {
	case EventNewOpportunity:
		return "Nova oportunidade no assunto que você acompanha"
	case EventOpportunityChanged:
		return "Mudou algo na oportunidade que você acompanha"
	case EventDeadlineChanged:
		return "Mudou um prazo no assunto que você acompanha"
	case EventFitBecameRelevant:
		return "O assunto que você acompanha passou a ser relevante para o seu perfil"
	default:
		return "Atualização no assunto que você acompanha"
	}
}

// ComposeWatchMessage renders one watcher notification. It fails when the
// message would not carry a working opt-out, because an update the recipient
// cannot stop is not a notification we are willing to send.
func ComposeWatchMessage(delivery WatchDelivery) (WatchMessage, error) {
	subscription := delivery.Subscription
	if subscription.ID == uuid.Nil || subscription.OrganizationID == uuid.Nil {
		return WatchMessage{}, fmt.Errorf("intel watch message needs an identified subscription")
	}
	if ok, reason := delivery.Event.Validate(); !ok {
		return WatchMessage{}, fmt.Errorf("intel watch message rejected: %s", reason)
	}
	optOut := UnsubscribeURL(subscription.OrganizationID, subscription.ID)
	if optOut == "" {
		return WatchMessage{}, fmt.Errorf("intel watch message has no signable opt-out link")
	}

	topic := strings.TrimSpace(subscription.Topic)
	if topic == "" {
		topic = strings.TrimSpace(subscription.SubjectKey)
	}
	headline := watchEventHeadline(delivery.Event.EventType)
	subject := fmt.Sprintf("%s: %s", headline, topic)

	var body strings.Builder
	body.WriteString(headline + ".\n\n")
	body.WriteString("Assunto acompanhado: " + topic + "\n")
	if title := strings.TrimSpace(delivery.Event.Payload[PayloadKeyTitle]); title != "" {
		body.WriteString("Referência: " + title + "\n")
	}
	if deadline := strings.TrimSpace(delivery.Event.Payload[PayloadKeyDeadline]); deadline != "" {
		body.WriteString("Prazo: " + deadline + "\n")
	}
	if summary := strings.TrimSpace(delivery.Event.Payload[PayloadKeySummary]); summary != "" {
		body.WriteString("\n" + summary + "\n")
	}
	// Everything the producer sent that the template does not name explicitly is
	// still rendered, in a stable order, so no fact is silently dropped.
	if extra := watchExtraPayloadLines(delivery.Event.Payload); extra != "" {
		body.WriteString("\n" + extra)
	}
	if public := strings.TrimSpace(delivery.Event.Payload[PayloadKeyPublicURL]); public != "" {
		body.WriteString("\nFonte pública: " + public + "\n")
	}
	body.WriteString("\nVocê recebeu isto porque pediu para acompanhar este assunto.\n")
	body.WriteString("Para parar de receber: " + optOut + "\n")

	return WatchMessage{Subject: subject, BodyText: body.String(), UnsubscribeURL: optOut}, nil
}

func watchExtraPayloadLines(payload map[string]string) string {
	named := map[string]bool{
		PayloadKeyTitle: true, PayloadKeySummary: true,
		PayloadKeyPublicURL: true, PayloadKeyDeadline: true,
	}
	keys := make([]string, 0, len(payload))
	for key := range payload {
		if named[key] || strings.TrimSpace(payload[key]) == "" {
			continue
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	var out strings.Builder
	for _, key := range keys {
		out.WriteString(key + ": " + strings.TrimSpace(payload[key]) + "\n")
	}
	return out.String()
}
