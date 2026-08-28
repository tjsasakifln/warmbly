package confenge

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// Secondary identity window: same account+contact inside this span shares
// one commercial action. Distinct receipts are still stored.
const inboundDedupeWindow = 24 * time.Hour

// Query keys that must never carry PII. web-cfg already has a POST body.
var inboundQueryPIIKeys = []string{
	"email", "e-mail", "mail", "phone", "telefone", "tel", "whatsapp",
	"name", "nome", "cnpj", "cnpj14", "message", "mensagem", "consent",
	"consentimento", "lead_name", "lead_email", "lead_phone",
}

// InboundLeadV1 is the sanitized web-cfg handoff after body parse.
type InboundLeadV1 struct {
	LeadID         string
	ReceiptID      string
	CreatedAt      time.Time
	Source         string
	RouteFamily    string
	AssetID        string
	CTAID          string
	LandingURL     string
	ContractID     string
	EntityID       string
	CNPJ           string
	CompanyName    string
	Name           string
	Email          string
	Phone          string
	Referrer       string
	Message        string
	CorrelationID  string
	Query          string
	QueryClass     string
	IntentClass    string
	OrganicSource  string
	LandingPath    string
	AssetVersion   string
	CTAVersion     string
	RecordKind     string
	Synthetic      bool
	ConsentVersion string
	Consent        InboundConsent
	UTM            map[string]string
	HighIntentHint bool
}

// InboundConsent is observed opt-in / DNC metadata from the form.
type InboundConsent struct {
	Granted          bool   `json:"granted"`
	Channel          string `json:"channel,omitempty"`
	Source           string `json:"source,omitempty"`
	PreferredChannel string `json:"preferred_channel,omitempty"`
	DNC              bool   `json:"dnc"`
	Spam             bool   `json:"spam"`
	RecordedAt       string `json:"recorded_at,omitempty"`
	Text             string `json:"text,omitempty"`
}

// IngestOptions controls the receive path. Query is inspected for PII.
type IngestOptions struct {
	Now                   time.Time
	Query                 url.Values
	EnrichmentUnavailable bool
	SkipCommercialAction  bool
}

// InboundIngestResult is the durable receive outcome.
type InboundIngestResult struct {
	Lead              *models.OutreachInboundLead      `json:"lead"`
	Action            *models.OutreachCommercialAction `json:"action,omitempty"`
	Duplicate         bool                             `json:"duplicate"`
	SecondaryDedupe   bool                             `json:"secondary_dedupe"`
	EnrichmentStatus  string                           `json:"enrichment_status"`
	NextAction        string                           `json:"next_action"`
	DispatchAttempted bool                             `json:"dispatch_attempted"`
}

// RejectInboundQueryPII fails closed when PII is presented on the query string.
func RejectInboundQueryPII(q url.Values) *errx.Error {
	if q == nil {
		return nil
	}
	for _, key := range inboundQueryPIIKeys {
		if strings.TrimSpace(q.Get(key)) != "" {
			return errx.New(errx.BadRequest, "PII is not accepted on the query string; send it in the POST body")
		}
	}
	return nil
}

// ParseInboundLead extracts observed fields from a web-cfg JSON body.
// Missing optional fields stay empty. Nothing is invented.
func ParseInboundLead(raw []byte, now time.Time) (InboundLeadV1, *errx.Error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return InboundLeadV1{}, errx.New(errx.BadRequest, "inbound body is required")
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return InboundLeadV1{}, errx.New(errx.BadRequest, "inbound body must be JSON")
	}
	lead := InboundLeadV1{
		LeadID:         firstNonEmpty(strAny(m, "lead_id", "id"), strAny(m, "receipt_id", "receipt")),
		ReceiptID:      firstNonEmpty(strAny(m, "receipt_id", "receipt"), strAny(m, "lead_id", "id")),
		Source:         SanitizeText(strAny(m, "source"), 120),
		RouteFamily:    SanitizeText(strAny(m, "route_family", "route"), 80),
		AssetID:        SanitizeText(strAny(m, "asset_id", "asset"), 120),
		CTAID:          SanitizeText(strAny(m, "cta_id", "cta"), 120),
		LandingURL:     sanitizeInboundURL(strAny(m, "landing_url", "url", "page_url")),
		ContractID:     SanitizeText(strAny(m, "contract_public_id", "contract_id", "contrato_id"), 120),
		EntityID:       SanitizeText(strAny(m, "entity_public_id", "entity_id", "account_public_id"), 120),
		CompanyName:    SanitizeText(strAny(m, "company_name", "company", "empresa", "razao_social"), 200),
		Name:           SanitizeText(strAny(m, "name", "nome", "lead_name"), 160),
		Email:          strings.ToLower(strings.TrimSpace(strAny(m, "email", "lead_email"))),
		Phone:          SanitizeText(strAny(m, "phone", "telefone", "tel", "lead_phone"), 40),
		Referrer:       sanitizeInboundURL(strAny(m, "referrer", "referer")),
		Message:        SanitizeText(strAny(m, "message", "contexto", "notes", "context"), 4000),
		CorrelationID:  SanitizeText(strAny(m, "correlation_id", "attribution_correlation_id"), 160),
		QueryClass:     SanitizeText(strAny(m, "query_class", "intent_class"), 80),
		IntentClass:    SanitizeText(strAny(m, "intent_class"), 80),
		OrganicSource:  SanitizeText(strAny(m, "organic_source", "attribution_source"), 80),
		LandingPath:    SanitizeText(strAny(m, "landing_path"), 200),
		AssetVersion:   SanitizeText(strAny(m, "asset_version"), 80),
		CTAVersion:     SanitizeText(strAny(m, "cta_version"), 80),
		RecordKind:     SanitizeText(strAny(m, "record_kind"), 40),
		Synthetic:      boolAny(m, "synthetic", "is_synthetic"),
		ConsentVersion: SanitizeText(strAny(m, "consent_version"), 80),
		UTM:            parseUTM(m),
	}
	// Individual GSC/search queries are not a lead attribute. query_class may stay.
	rawQuery := SanitizeText(strAny(m, "query", "search_query", "q"), 200)
	if inboundQueryClassOK(rawQuery) {
		lead.Query = rawQuery
		if lead.QueryClass == "" {
			lead.QueryClass = rawQuery
		}
	}
	if lead.QueryClass == "" && lead.UTM != nil && inboundQueryClassOK(lead.UTM["query"]) {
		lead.QueryClass = lead.UTM["query"]
		lead.Query = lead.UTM["query"]
	}
	if lead.LandingPath == "" {
		lead.LandingPath = lead.LandingURL
	}
	lead.CNPJ = NormalizeCNPJ14(strAny(m, "cnpj14", "cnpj"))
	if ts := parseFlexibleTime(strAny(m, "created_at", "lead_created_at")); !ts.IsZero() {
		lead.CreatedAt = ts
	} else {
		lead.CreatedAt = now
	}
	if nested, ok := m["consent"].(map[string]any); ok {
		lead.Consent = parseConsent(nested)
	} else {
		lead.Consent = parseConsent(m)
	}
	lead.HighIntentHint = inboundHighIntent(lead)
	if strings.TrimSpace(lead.LeadID) == "" {
		return InboundLeadV1{}, errx.New(errx.BadRequest, "lead_id or receipt_id is required")
	}
	lead.LeadID = SanitizeText(lead.LeadID, 160)
	lead.ReceiptID = SanitizeText(lead.ReceiptID, 160)
	if lead.LeadID == "" {
		return InboundLeadV1{}, errx.New(errx.BadRequest, "lead_id or receipt_id is required")
	}
	return lead, nil
}

func parseConsent(m map[string]any) InboundConsent {
	c := InboundConsent{
		Granted:          boolAny(m, "granted", "consent_granted", "opt_in"),
		Channel:          SanitizeText(strAny(m, "channel", "consent_channel"), 40),
		Source:           SanitizeText(strAny(m, "consent_source"), 80),
		PreferredChannel: SanitizeText(strAny(m, "preferred_channel"), 40),
		DNC:              boolAny(m, "dnc", "do_not_contact"),
		Spam:             boolAny(m, "spam", "is_spam"),
		RecordedAt:       SanitizeText(strAny(m, "consent_at", "recorded_at"), 40),
		Text:             SanitizeText(strAny(m, "consent_text", "text"), 400),
	}
	return c
}

func parseUTM(m map[string]any) map[string]string {
	out := map[string]string{}
	if nested, ok := m["utm"].(map[string]any); ok {
		for _, k := range []string{"source", "medium", "campaign", "term", "content"} {
			if v := SanitizeText(strAny(nested, k, "utm_"+k), 120); v != "" {
				out[k] = v
			}
		}
	}
	for _, k := range []string{"utm_source", "utm_medium", "utm_campaign", "utm_term", "utm_content"} {
		if v := SanitizeText(strAny(m, k), 120); v != "" {
			out[strings.TrimPrefix(k, "utm_")] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func inboundIdentityKey(cnpj, email, phone string) string {
	cnpj = NormalizeCNPJ14(cnpj)
	email = strings.ToLower(strings.TrimSpace(email))
	phone = digitsOnly(phone)
	if cnpj == "" && email == "" && phone == "" {
		return ""
	}
	return cnpj + "|" + email + "|" + phone
}

func inboundHighIntent(lead InboundLeadV1) bool {
	if lead.ContractID != "" || lead.EntityID != "" {
		return true
	}
	blob := strings.ToLower(strings.Join([]string{lead.CTAID, lead.RouteFamily, lead.Message, lead.Source}, " "))
	for _, tok := range []string{
		"segunda leitura", "reler", "re-leitura", "contrato", "falar",
		"retorno", "proposta", "high_intent", "high-intent", "inbound",
	} {
		if strings.Contains(blob, tok) {
			return true
		}
	}
	return false
}

func inboundQueryClassOK(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" || strings.ContainsAny(v, " \t\n@/?#") || len(v) > 80 {
		return false
	}
	return true
}

func sanitizeInboundURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	low := strings.ToLower(s)
	if strings.HasPrefix(low, "javascript:") || strings.HasPrefix(low, "data:") {
		return ""
	}
	return SanitizeText(s, 500)
}

func strAny(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case string:
				if strings.TrimSpace(t) != "" {
					return t
				}
			case float64:
				return strings.TrimSpace(strconv.FormatFloat(t, 'f', -1, 64))
			}
		}
	}
	return ""
}

func boolAny(m map[string]any, keys ...string) bool {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case bool:
				return t
			case string:
				s := strings.ToLower(strings.TrimSpace(t))
				return s == "true" || s == "1" || s == "yes" || s == "sim"
			}
		}
	}
	return false
}

func parseFlexibleTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02T15:04:05Z07:00", "2006-01-02 15:04:05", "2006-01-02"} {
		if ts, err := time.Parse(layout, raw); err == nil {
			return ts.UTC()
		}
	}
	return time.Time{}
}
