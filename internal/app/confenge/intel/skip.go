package intel

import (
	"encoding/json"
	"strings"
	"unicode"

	"github.com/warmbly/warmbly/internal/models"
)

const (
	InboundSkipSynthetic = "synthetic"
	InboundSkipQA        = "qa"
	InboundSkipInternal  = "internal"

	// officialSyntheticMarker is the only free-text skip in company/name/email/message.
	// Tokens "qa" or "internal" in those fields are real commercial Portuguese.
	officialSyntheticMarker = "synthetic-inbound"
)

// InboundCommercialSkipReason is the commercial-queue skip gate. Receipts
// still persist. A non-empty reason means the row is SYNTHETIC/qa/internal
// and must not enter real INBOUND NOW or include_synthetic=0 rollups.
func InboundCommercialSkipReason(lead models.OutreachInboundLead) string {
	// Identity, source, and explicit flags only. Company/name/email/message
	// skip solely on the official SYNTHETIC-INBOUND marker.
	if reason := skipTokenIn(lead.Source); reason != "" {
		return reason
	}
	if reason := skipTokenIn(lead.LeadID); reason != "" {
		return reason
	}
	if reason := skipTokenIn(lead.ReceiptID); reason != "" {
		return reason
	}
	if reason := skipFromPayload(lead.RawPayload); reason != "" {
		return reason
	}
	if skipOfficialMarker(lead.CompanyName, lead.LeadEmail, lead.LeadName, lead.Message) {
		return InboundSkipSynthetic
	}
	return ""
}

func skipOfficialMarker(parts ...string) bool {
	for _, p := range parts {
		if strings.Contains(strings.ToLower(p), officialSyntheticMarker) {
			return true
		}
	}
	return false
}

func skipTokenIn(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	low := strings.ToLower(s)
	for _, tok := range splitSkipTokens(low) {
		switch tok {
		case InboundSkipSynthetic:
			return InboundSkipSynthetic
		case InboundSkipQA:
			return InboundSkipQA
		case InboundSkipInternal:
			return InboundSkipInternal
		}
	}
	return ""
}

func splitSkipTokens(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == '/' || r == '@' || r == ':' || unicode.IsSpace(r)
	})
}

func skipFromPayload(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		if skipOfficialMarker(string(raw)) {
			return InboundSkipSynthetic
		}
		return ""
	}
	if truthy(m["synthetic"]) || truthy(m["is_synthetic"]) {
		return InboundSkipSynthetic
	}
	for _, key := range []string{"label", "environment", "env", "fixture"} {
		if reason := skipTokenIn(asString(m[key])); reason != "" {
			return reason
		}
	}
	return ""
}

func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		s := strings.ToLower(strings.TrimSpace(t))
		return s == "true" || s == "1" || s == "yes"
	}
	return false
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
