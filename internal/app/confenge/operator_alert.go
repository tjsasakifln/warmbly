package confenge

import (
	"net/mail"
	"strings"
	"time"

	"github.com/warmbly/warmbly/internal/models"
)

const (
	AlertTypeInboundOperatorAttention = models.OperatorAlertTypeInboundAttention
	AlertPolicyV1                     = models.OperatorAlertPolicyV1

	AlertBandNew       = models.OperatorAlertBandNew
	AlertBandAttention = models.OperatorAlertBandAttention
	AlertBandAged      = models.OperatorAlertBandAged

	AlertStateAcknowledged     = models.OperatorAlertStateAcknowledged
	AlertStateActionRecorded   = models.OperatorAlertStateActionRecorded
	AlertStateResolvedNoAction = models.OperatorAlertStateResolvedNoAction
	AlertStateAlertFailed      = models.OperatorAlertStateAlertFailed

	AlertAgingNewMax       = 15 * time.Minute
	AlertAgingAttentionMax = 60 * time.Minute

	OperatorAlertEmailSubject = "Novo lead real no INBOUND NOW"
	OperatorAlertDisplayTZ    = "America/Sao_Paulo"

	AlertChannelCockpit = "cockpit"
	AlertChannelBrowser = "browser"
	AlertChannelEmail   = "email"

	AlertEmailFlagOff            = "flag_off"
	AlertEmailKillSwitch         = "kill_switch"
	AlertEmailNoAllowlist        = "no_allowlist"
	AlertEmailInvalid            = "invalid_allowlist"
	AlertEmailBlockedNoTransport = "blocked_no_isolated_transport"
	AlertEmailSendFailed         = "send_failed"
	AlertEmailDisabled           = "disabled"
)

type OperatorAlert = models.OutreachOperatorAlert

type OperatorAlertEmail struct {
	To      string
	Subject string
	Body    string
	Reason  string
	Allowed bool
}

// OperatorAlertEventID is the idempotency key: one logical alert per lead.
func OperatorAlertEventID(leadID string) string {
	return AlertTypeInboundOperatorAttention + ":" + strings.TrimSpace(leadID)
}

// ProjectOperatorAlertState is the operational band. Not an SLA.
// Persisted timestamps stay UTC; now must be UTC.
func ProjectOperatorAlertState(a OperatorAlert, now time.Time) string {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if a.ResolvedAt != nil && !a.ResolvedAt.IsZero() && a.FirstActionAt == nil {
		return AlertStateResolvedNoAction
	}
	if a.FirstActionAt != nil && !a.FirstActionAt.IsZero() {
		return AlertStateActionRecorded
	}
	if a.AcknowledgedAt != nil && !a.AcknowledgedAt.IsZero() {
		return AlertStateAcknowledged
	}
	if strings.TrimSpace(a.FailureCode) != "" {
		return AlertStateAlertFailed
	}
	created := a.CreatedAt.UTC()
	if created.IsZero() {
		return AlertBandNew
	}
	age := now.Sub(created)
	if age < 0 {
		age = 0
	}
	switch {
	case age < AlertAgingNewMax:
		return AlertBandNew
	case age < AlertAgingAttentionMax:
		return AlertBandAttention
	default:
		return AlertBandAged
	}
}

func operatorAlertUrgencyRank(state string) int {
	switch state {
	case AlertStateAlertFailed:
		return 0
	case AlertBandAged:
		return 1
	case AlertBandAttention:
		return 2
	case AlertBandNew:
		return 3
	case AlertStateAcknowledged:
		return 4
	case AlertStateActionRecorded:
		return 5
	case AlertStateResolvedNoAction:
		return 6
	default:
		return 9
	}
}

func ValidOperatorResolveReason(reason string) bool {
	switch strings.ToUpper(strings.TrimSpace(reason)) {
	case models.OperatorAlertResolveDuplicate, models.OperatorAlertResolveNotALead,
		models.OperatorAlertResolveSpam, models.OperatorAlertResolveTest,
		models.OperatorAlertResolveOther:
		return true
	default:
		return false
	}
}

func operatorAlertForbidsCommercialClose(code string) bool {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case models.OutcomeWonCode, models.OutcomeLostCode, models.OutcomeClientCode,
		"REVENUE", "PIPELINE", "PAYMENT_RECEIVED", "REVENUE_RECEIVED":
		return true
	default:
		return false
	}
}

// FormatAlertDisplay renders UTC in America/Sao_Paulo without mutating t.
func FormatAlertDisplay(t time.Time, loc *time.Location) string {
	if t.IsZero() {
		return inboundUnknown
	}
	if loc == nil {
		var err error
		loc, err = time.LoadLocation(OperatorAlertDisplayTZ)
		if err != nil {
			loc = time.FixedZone("BRT", -3*3600)
		}
	}
	return t.UTC().In(loc).Format("02/01/2006 15:04")
}

func saoPauloLocation() *time.Location {
	loc, err := time.LoadLocation(OperatorAlertDisplayTZ)
	if err != nil {
		return time.FixedZone("BRT", -3*3600)
	}
	return loc
}

func OperatorAlertContainsPII(s string, lead models.OutreachInboundLead) bool {
	s = strings.ToLower(s)
	if s == "" {
		return false
	}
	candidates := []string{
		lead.LeadName, lead.LeadEmail, lead.LeadPhone, lead.CNPJ14,
		lead.PersonName, lead.Message, lead.CompanyName,
	}
	for _, c := range candidates {
		c = strings.TrimSpace(strings.ToLower(c))
		if len(c) < 3 {
			continue
		}
		if strings.Contains(s, c) {
			return true
		}
	}
	return false
}

func BuildOperatorAlertEmail(now time.Time, origin, asset string, age time.Duration, panelURL string) OperatorAlertEmail {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	origin = firstNonEmpty(sanitizeAlertToken(origin), inboundUnknown)
	asset = firstNonEmpty(sanitizeAlertToken(asset), inboundUnknown)
	panelURL = strings.TrimSpace(panelURL)
	if panelURL == "" {
		panelURL = "/app/confenge#inbound-agora"
	}
	body := strings.Join([]string{
		"timestamp_utc=" + now.UTC().Format(time.RFC3339),
		"origin=" + origin,
		"asset=" + asset,
		"age=" + formatLeadAge(age),
		"panel=" + panelURL,
	}, "\n")
	return OperatorAlertEmail{
		Subject: OperatorAlertEmailSubject,
		Body:    body,
	}
}

func sanitizeAlertToken(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	low := strings.ToLower(v)
	if strings.Contains(low, "@") || strings.ContainsAny(v, "<>") {
		return ""
	}
	if looksLikeCNPJ(v) || looksLikePhone(v) {
		return ""
	}
	return v
}

func looksLikeCNPJ(v string) bool {
	digits := 0
	for _, r := range v {
		if r >= '0' && r <= '9' {
			digits++
		}
	}
	return digits >= 11
}

func looksLikePhone(v string) bool {
	digits := 0
	for _, r := range v {
		if r >= '0' && r <= '9' {
			digits++
		}
	}
	return digits >= 10 && (strings.Contains(v, "+") || digits >= 11)
}

// ResolveOperatorAlertRecipient never copies a lead address. Optional
// email stays default-off and is blocked without an isolated transporter.
func ResolveOperatorAlertRecipient(cfg Config, leadEmail string) (to, reason string) {
	_ = leadEmail
	if !cfg.OperatorAlertEmailEnabled {
		return "", AlertEmailFlagOff
	}
	if cfg.OperatorAlertEmailKillSwitch {
		return "", AlertEmailKillSwitch
	}
	allow := strings.TrimSpace(cfg.OperatorAlertEmail)
	if allow == "" {
		return "", AlertEmailNoAllowlist
	}
	if _, err := mail.ParseAddress(allow); err != nil {
		return "", AlertEmailInvalid
	}
	if allow != strings.TrimSpace(cfg.OperatorAlertEmail) {
		return "", AlertEmailInvalid
	}
	return allow, AlertEmailBlockedNoTransport
}

type OperatorAlertLatency struct {
	LeadPersistedToAlertDurable string `json:"lead_persisted_to_alert_durable,omitempty"`
	AlertDurableToFirstEmitted  string `json:"alert_durable_to_first_emitted,omitempty"`
	LeadPersistedToAck          string `json:"lead_persisted_to_acknowledged,omitempty"`
	AckToFirstHumanAction       string `json:"acknowledged_to_first_human_action,omitempty"`
	LeadPersistedToFirstAction  string `json:"lead_persisted_to_first_human_action,omitempty"`
	OpenCensored                bool   `json:"open_censored"`
}

func MeasureOperatorAlertLatency(lead models.OutreachInboundLead, a OperatorAlert) OperatorAlertLatency {
	out := OperatorAlertLatency{OpenCensored: a.FirstActionAt == nil && a.ResolvedAt == nil}
	persisted := lead.WarmblyIngestedAt.UTC()
	if persisted.IsZero() {
		return out
	}
	if !a.CreatedAt.IsZero() {
		out.LeadPersistedToAlertDurable = a.CreatedAt.UTC().Sub(persisted).String()
	}
	if a.FirstEmittedAt != nil && !a.FirstEmittedAt.IsZero() && !a.CreatedAt.IsZero() {
		out.AlertDurableToFirstEmitted = a.FirstEmittedAt.UTC().Sub(a.CreatedAt.UTC()).String()
	}
	if a.AcknowledgedAt != nil && !a.AcknowledgedAt.IsZero() {
		out.LeadPersistedToAck = a.AcknowledgedAt.UTC().Sub(persisted).String()
	}
	if a.FirstActionAt != nil && !a.FirstActionAt.IsZero() {
		out.LeadPersistedToFirstAction = a.FirstActionAt.UTC().Sub(persisted).String()
		if a.AcknowledgedAt != nil && !a.AcknowledgedAt.IsZero() {
			out.AckToFirstHumanAction = a.FirstActionAt.UTC().Sub(a.AcknowledgedAt.UTC()).String()
		}
	}
	return out
}
