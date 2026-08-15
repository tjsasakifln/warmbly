package confenge

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

// InboundCommercialSkipReason returns why a persisted receipt must stay
// out of the commercial INBOUND NOW queue. Receipts are still durable.
// Empty means the row may appear as a commercial card.
func InboundCommercialSkipReason(lead models.OutreachInboundLead) string {
	if reason := skipTokenIn(lead.Source); reason != "" {
		return reason
	}
	if reason := skipTokenIn(lead.LeadID); reason != "" {
		return reason
	}
	if reason := skipTokenIn(lead.ReceiptID); reason != "" {
		return reason
	}
	if reason := skipTokenIn(lead.CompanyName); reason != "" {
		return reason
	}
	if reason := skipTokenIn(lead.LeadEmail); reason != "" {
		return reason
	}
	if reason := skipTokenIn(lead.LeadName); reason != "" {
		return reason
	}
	if reason := skipTokenIn(lead.Message); reason != "" {
		return reason
	}
	if reason := skipFromPayload(lead.RawPayload); reason != "" {
		return reason
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
	if boolAny(m, "synthetic", "is_synthetic") {
		return InboundSkipSynthetic
	}
	if reason := skipTokenIn(strAny(m, "label", "environment", "env", "fixture")); reason != "" {
		return reason
	}
	return ""
}
