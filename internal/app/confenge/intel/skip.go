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
)

// InboundCommercialSkipReason is the commercial-queue skip gate. Receipts
// still persist. A non-empty reason means the row is SYNTHETIC/qa/internal
// and must not enter real INBOUND NOW or include_synthetic=0 rollups.
func InboundCommercialSkipReason(lead models.OutreachInboundLead) string {
	// Identity fields and explicit payload flags only. Company/name/message
	// may contain "QA" or "internal" in real commercial Portuguese.
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
	if reason := skipOfficialMarker(lead.CompanyName, lead.LeadEmail, lead.LeadName, lead.Message); reason != "" {
		return reason
	}
	return ""
}

func skipOfficialMarker(parts ...string) string {
	for _, p := range parts {
		for _, tok := range splitSkipTokens(strings.ToLower(strings.TrimSpace(p))) {
			if tok == InboundSkipSynthetic {
				return InboundSkipSynthetic
			}
		}
	}
	return ""
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
		return skipTokenIn(string(raw))
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
