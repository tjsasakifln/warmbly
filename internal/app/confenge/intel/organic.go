package intel

import (
	"net/url"
	"strings"
	"unicode"
)

// OrganicSources is the closed taxonomy. Slices never mix.
func OrganicSources() []string {
	return []string{
		SourceOrganicSearch, SourceDirect, SourceReferral,
		SourceAIReferral, SourcePartner, SourceOutbound, Unknown,
	}
}

// NormalizeOrganicSource maps a provided token onto the closed taxonomy.
// unknown, producer identities, and unlabeled values stay UNKNOWN.
// Bare "google" is not rewritten to organic_search. unknown is never
// rewritten to direct or organic_search.
func NormalizeOrganicSource(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case SourceOrganicSearch, "organic", "seo", "google_organic", "bing_organic",
		"organic-search", "organicsearch":
		return SourceOrganicSearch
	case SourceDirect:
		return SourceDirect
	case SourceReferral, "referrer":
		return SourceReferral
	case SourceAIReferral, "ai-referral", "aireferral", "chatgpt", "perplexity",
		"claude", "gemini", "copilot":
		return SourceAIReferral
	case SourcePartner, "parceiro":
		return SourcePartner
	case SourceOutbound, "outbound-pilot":
		return SourceOutbound
	default:
		return Unknown
	}
}

// ComposeOrganicSource prefers an explicit organic token. Medium alone
// never invents organic_search from an unlabeled source.
func ComposeOrganicSource(explicit, source, medium string) string {
	if s := NormalizeOrganicSource(explicit); s != Unknown {
		return s
	}
	if s := NormalizeOrganicSource(source); s != Unknown {
		return s
	}
	_ = medium
	return Unknown
}

// NormalizeRecordKind is real or synthetic. Empty stays empty so callers
// can derive from the synthetic flag without inventing REAL.
func NormalizeRecordKind(v string, synthetic bool) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case RecordKindReal, LabelReal, "live":
		if synthetic {
			return RecordKindSynthetic
		}
		return RecordKindReal
	case RecordKindSynthetic, LabelSynthetic, "test", "canary", "infrastructure_canary":
		return RecordKindSynthetic
	default:
		if synthetic {
			return RecordKindSynthetic
		}
		if strings.TrimSpace(v) == "" {
			return ""
		}
		return Unknown
	}
}

// LooksLikeIndividualSearchQuery is a raw GSC/search phrase, not a class.
func LooksLikeIndividualSearchQuery(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	if strings.ContainsAny(v, " \t\n@") {
		return true
	}
	if len(v) > 80 {
		return true
	}
	return false
}

// LooksLikeQueryHash is a hex digest presented as an individual attribute.
func LooksLikeQueryHash(v string) bool {
	v = strings.TrimSpace(v)
	if len(v) < 16 || len(v) > 128 {
		return false
	}
	for _, r := range v {
		if !unicode.Is(unicode.ASCII_Hex_Digit, r) {
			return false
		}
	}
	return true
}

// IsAllowedQueryClass is an aggregate class slug, never a raw phrase.
func IsAllowedQueryClass(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" || LooksLikeIndividualSearchQuery(v) || LooksLikeQueryHash(v) {
		return false
	}
	if strings.ContainsAny(v, "/?&#") {
		return false
	}
	return len(v) <= 80
}

// CanonicalLandingPath keeps the path only. Query strings are dropped
// because they can carry PII or individual search terms.
func CanonicalLandingPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		path := u.EscapedPath()
		if path == "" {
			path = "/"
		}
		return path
	}
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	return raw
}

// ClassifyReferrerClass never stores a sensitive URL. Host hints only.
func ClassifyReferrerClass(raw, provided string) string {
	if c := strings.ToLower(strings.TrimSpace(provided)); c != "" {
		switch c {
		case "search", "organic", "organic_search":
			return "search"
		case "ai", "ai_referral":
			return "ai"
		case "direct":
			return "direct"
		case "referral", "referrer":
			return "referral"
		case "partner":
			return "partner"
		case "outbound":
			return "outbound"
		case "unknown":
			return Unknown
		default:
			if IsAllowedQueryClass(c) {
				return c
			}
			return Unknown
		}
	}
	low := strings.ToLower(strings.TrimSpace(raw))
	if low == "" {
		return ""
	}
	switch {
	case strings.Contains(low, "google.") || strings.Contains(low, "bing.") ||
		strings.Contains(low, "search."):
		return "search"
	case strings.Contains(low, "chatgpt") || strings.Contains(low, "perplexity") ||
		strings.Contains(low, "claude") || strings.Contains(low, "gemini"):
		return "ai"
	default:
		return "referral"
	}
}

func validAssetVersion(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return true
	}
	if strings.ContainsAny(v, " \t\n@/?#") || len(v) > 80 {
		return false
	}
	return true
}

// SanitizeCommercialEvent applies the organic attribution contract.
// Individual GSC queries and query hashes never stay on the envelope.
func SanitizeCommercialEvent(ev CommercialEvent) CommercialEvent {
	if LooksLikeIndividualSearchQuery(ev.Query) {
		ev.GSCQuery = ev.Query
		ev.Query = ""
	}
	if LooksLikeIndividualSearchQuery(ev.GSCQuery) {
		ev.Query = ""
	}
	if h := strings.TrimSpace(ev.QueryHash); h != "" {
		ev.QueryHash = h
		if ev.Query == h || LooksLikeQueryHash(ev.Query) {
			ev.Query = ""
		}
	}
	if LooksLikeQueryHash(ev.Query) {
		ev.QueryHash = ev.Query
		ev.Query = ""
	}
	if ev.QueryClass == "" && IsAllowedQueryClass(ev.Query) {
		ev.QueryClass = ev.Query
	}
	if !IsAllowedQueryClass(ev.Query) {
		ev.Query = ""
	}
	if !IsAllowedQueryClass(ev.QueryClass) {
		ev.QueryClass = ""
	}
	ev.OrganicSource = ComposeOrganicSource(ev.OrganicSource, ev.Source, ev.Medium)
	ev.LandingPath = CanonicalLandingPath(firstNonEmpty(ev.LandingPath, ev.LandingURL))
	if ev.ReferrerClass == "" {
		ev.ReferrerClass = ClassifyReferrerClass(ev.Referrer, "")
	} else {
		ev.ReferrerClass = ClassifyReferrerClass("", ev.ReferrerClass)
	}
	if strings.ContainsAny(ev.Referrer, "?#@") || LooksLikeIndividualSearchQuery(ev.Referrer) {
		ev.Referrer = ""
	}
	ev.RecordKind = NormalizeRecordKind(ev.RecordKind, ev.Synthetic)
	if ev.Synthetic && ev.RecordKind == RecordKindReal {
		ev.RecordKind = RecordKindSynthetic
	}
	if !validAssetVersion(ev.AssetVersion) {
		ev.AssetVersion = ""
	}
	return ev
}

// ApplyOrganicKeys copies sanitized organic fields onto join keys.
func ApplyOrganicKeys(k JoinKeys, ev CommercialEvent) JoinKeys {
	k.OrganicSource = ev.OrganicSource
	k.Medium = strings.TrimSpace(ev.Medium)
	k.Campaign = strings.TrimSpace(ev.Campaign)
	k.LandingPath = ev.LandingPath
	k.AssetVersion = strings.TrimSpace(ev.AssetVersion)
	k.CTAVersion = strings.TrimSpace(ev.CTAVersion)
	k.RecordKind = ev.RecordKind
	k.ConsentVersion = strings.TrimSpace(ev.ConsentVersion)
	k.PageVersion = strings.TrimSpace(ev.PageVersion)
	k.ContentVersion = strings.TrimSpace(ev.ContentVersion)
	k.FirstTouchAt = ev.FirstTouchAt
	k.LastTouchAt = ev.LastTouchAt
	k.QueryClass = firstNonEmpty(ev.QueryClass, k.QueryClass)
	k.ReferrerClass = firstNonEmpty(ev.ReferrerClass, k.ReferrerClass)
	k.Query = ev.Query
	k.Referrer = ev.Referrer
	if k.OrganicSource == "" {
		k.OrganicSource = ComposeOrganicSource("", k.Source, k.Medium)
	}
	return k
}

func organicSourceOf(k JoinKeys) string {
	if s := NormalizeOrganicSource(k.OrganicSource); s != Unknown {
		return s
	}
	return ComposeOrganicSource("", k.Source, k.Medium)
}

func recordKindOf(in ObservedFacts) string {
	if k := NormalizeRecordKind(firstNonEmpty(in.RecordKind, in.Keys.RecordKind), in.Synthetic); k != "" && k != Unknown {
		return k
	}
	if in.Synthetic || in.Label == LabelSynthetic {
		return RecordKindSynthetic
	}
	return RecordKindReal
}
