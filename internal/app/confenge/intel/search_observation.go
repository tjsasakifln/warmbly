package intel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	ConsentPolicyNotApplicable = "not_applicable"
	ConsentPolicyAggregate     = "aggregate"

	CoverageObserved = "OBSERVED"
	CoverageAbsent   = "ABSENT"
	CoverageBlocked  = "BLOCKED"
	CoverageUnknown  = "UNKNOWN"

	ProducerCONFENGEWeb         = "CONFENGE_WEB"
	ProducerWebCfg              = "web-cfg"
	searchObservationFutureSkew = 5 * time.Minute
	searchObservationStaleAfter = 90 * 24 * time.Hour
)

// EnvelopeError is a 4xx-class contract violation. Nothing is persisted.
type EnvelopeError struct {
	Msg string
}

func (e EnvelopeError) Error() string { return e.Msg }

func envelopeErrorf(format string, args ...any) EnvelopeError {
	return EnvelopeError{Msg: fmt.Sprintf(format, args...)}
}

// SearchObservation is a durable aggregated discovery snapshot.
// Counts are nullable. Individual query/query_hash never persist.
type SearchObservation struct {
	OrganizationID string    `json:"organization_id,omitempty"`
	EventID        string    `json:"event_id"`
	ReceiptID      string    `json:"receipt_id,omitempty"`
	Schema         string    `json:"schema"`
	Version        string    `json:"version"`
	Type           string    `json:"type"`
	Source         string    `json:"source"`
	OrganicSource  string    `json:"organic_source"`
	AssetFamily    string    `json:"asset_family,omitempty"`
	AssetID        string    `json:"asset_id,omitempty"`
	LandingPath    string    `json:"landing_path,omitempty"`
	IntentClass    string    `json:"intent_class,omitempty"`
	QueryClass     string    `json:"query_class,omitempty"`
	Window         string    `json:"window"`
	Eligible       *int      `json:"eligible"`
	Appeared       *int      `json:"appeared"`
	Clicked        *int      `json:"clicked"`
	Engaged        *int      `json:"engaged"`
	Coverage       string    `json:"coverage,omitempty"`
	Freshness      string    `json:"freshness,omitempty"`
	MeasurementAt  time.Time `json:"measurement_at"`
	ProducerSHA    string    `json:"producer_sha,omitempty"`
	PayloadHash    string    `json:"payload_hash"`
	Synthetic      bool      `json:"synthetic"`
	RecordKind     string    `json:"record_kind"`
	ConsentPolicy  string    `json:"consent_policy"`
	Replay         bool      `json:"replay,omitempty"`
	OutOfOrder     bool      `json:"out_of_order,omitempty"`
	CreatedAt      time.Time `json:"created_at,omitempty"`
}

// SearchObservationReceipt is the accept echo. not_a_lead is always true.
type SearchObservationReceipt struct {
	EventID         string `json:"event_id"`
	AcceptedVersion string `json:"accepted_version"`
	ReceiptID       string `json:"receipt_id"`
	Persisted       bool   `json:"persisted"`
	Replay          bool   `json:"replay"`
	RecordKind      string `json:"record_kind"`
	NotALead        bool   `json:"not_a_lead"`
	Synthetic       bool   `json:"synthetic,omitempty"`
	OutOfOrder      bool   `json:"out_of_order,omitempty"`
}

// AcceptedEventVersions is the machine-readable inbound capability list.
func AcceptedEventVersions() []string {
	return []string{EventSchemaV1, OrganicDiscoveryContract}
}

// IsSearchObservationEnvelope reports a search_observation v1 body.
func IsSearchObservationEnvelope(raw []byte) bool {
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
	return isSearchObservationType(peek.Type, peek.Version, peek.Schema)
}

func isSearchObservationType(typ, version, schema string) bool {
	if strings.EqualFold(strings.TrimSpace(typ), EventSearchObservation) {
		return true
	}
	if strings.TrimSpace(version) == OrganicDiscoveryContract {
		return true
	}
	_ = schema
	return false
}

// ParseSearchObservation decodes and validates the frozen v1 contract.
func ParseSearchObservation(raw []byte, orgID string, now time.Time) (SearchObservation, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return SearchObservation{}, envelopeErrorf("search observation body is required")
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return SearchObservation{}, envelopeErrorf("search observation must be JSON")
	}
	if v, ok := m["query"]; ok && v != nil && strings.TrimSpace(fmt.Sprint(v)) != "" {
		return SearchObservation{}, envelopeErrorf("individual query is not accepted on search_observation")
	}
	if v, ok := m["query_hash"]; ok && v != nil && strings.TrimSpace(fmt.Sprint(v)) != "" {
		return SearchObservation{}, envelopeErrorf("query_hash is not accepted on search_observation")
	}
	if v, ok := m["gsc_query"]; ok && v != nil && strings.TrimSpace(fmt.Sprint(v)) != "" {
		return SearchObservation{}, envelopeErrorf("gsc_query is not accepted on search_observation")
	}

	obs := SearchObservation{
		OrganizationID: strings.TrimSpace(orgID),
		EventID:        strings.TrimSpace(strAnyMap(m, "event_id")),
		ReceiptID:      strings.TrimSpace(firstNonEmpty(strAnyMap(m, "receipt_id"), strAnyMap(m, "event_id"))),
		Schema:         strings.TrimSpace(strAnyMap(m, "schema")),
		Version:        strings.TrimSpace(strAnyMap(m, "version")),
		Type:           strings.TrimSpace(strAnyMap(m, "type")),
		Source:         strings.TrimSpace(strAnyMap(m, "source")),
		OrganicSource:  ComposeOrganicSource(strAnyMap(m, "organic_source"), strAnyMap(m, "source"), strAnyMap(m, "medium")),
		AssetFamily:    strings.TrimSpace(strAnyMap(m, "asset_family")),
		AssetID:        strings.TrimSpace(strAnyMap(m, "asset_id")),
		LandingPath:    CanonicalLandingPath(firstNonEmpty(strAnyMap(m, "landing_path"), strAnyMap(m, "landing_url"))),
		IntentClass:    strings.TrimSpace(strAnyMap(m, "intent_class")),
		QueryClass:     strings.TrimSpace(strAnyMap(m, "query_class")),
		Window:         strings.TrimSpace(strAnyMap(m, "window")),
		Coverage:       strings.ToUpper(strings.TrimSpace(strAnyMap(m, "coverage"))),
		Freshness:      strings.TrimSpace(strAnyMap(m, "freshness")),
		ProducerSHA:    strings.TrimSpace(strAnyMap(m, "producer_sha")),
		Synthetic:      boolAnyMap(m, "synthetic"),
		RecordKind:     NormalizeRecordKind(strAnyMap(m, "record_kind"), boolAnyMap(m, "synthetic")),
		ConsentPolicy:  strings.TrimSpace(firstNonEmpty(strAnyMap(m, "consent_policy"), ConsentPolicyNotApplicable)),
	}
	if !IsAllowedQueryClass(obs.QueryClass) {
		obs.QueryClass = ""
	}
	if !IsAllowedQueryClass(obs.IntentClass) {
		obs.IntentClass = ""
	}
	eligible, err := nullableNonNegInt(m, "eligible")
	if err != nil {
		return SearchObservation{}, err
	}
	appeared, err := nullableNonNegInt(m, "appeared")
	if err != nil {
		return SearchObservation{}, err
	}
	clicked, err := nullableNonNegInt(m, "clicked")
	if err != nil {
		return SearchObservation{}, err
	}
	engaged, err := nullableNonNegInt(m, "engaged")
	if err != nil {
		return SearchObservation{}, err
	}
	obs.Eligible, obs.Appeared, obs.Clicked, obs.Engaged = eligible, appeared, clicked, engaged
	if obs.Coverage == "" {
		if eligible != nil || appeared != nil || clicked != nil || engaged != nil {
			obs.Coverage = CoverageObserved
		} else {
			obs.Coverage = CoverageUnknown
		}
	}

	meas := parseFlexibleTimeMap(m, "measurement_at", "occurred_at")
	if meas.IsZero() {
		return SearchObservation{}, envelopeErrorf("measurement_at is required")
	}
	obs.MeasurementAt = meas.UTC()
	if err := validateSearchObservation(obs, now); err != nil {
		return SearchObservation{}, err
	}
	if obs.MeasurementAt.Before(now.Add(-searchObservationStaleAfter)) && obs.Freshness == "" {
		obs.Freshness = "stale"
	}
	obs.PayloadHash = hashSearchObservation(obs)
	obs.CreatedAt = now.UTC()
	return obs, nil
}

func validateSearchObservation(obs SearchObservation, now time.Time) error {
	if strings.TrimSpace(obs.EventID) == "" {
		return envelopeErrorf("event_id is required")
	}
	if obs.Schema != EventSchemaV1 {
		if strings.TrimSpace(obs.Schema) == "" {
			return envelopeErrorf("schema is required")
		}
		return envelopeErrorf("unsupported schema %q", obs.Schema)
	}
	if obs.Version != OrganicDiscoveryContract {
		if strings.TrimSpace(obs.Version) == "" {
			return envelopeErrorf("version is required")
		}
		return envelopeErrorf("unsupported version %q", obs.Version)
	}
	if !strings.EqualFold(obs.Type, EventSearchObservation) {
		return envelopeErrorf("type must be %s", EventSearchObservation)
	}
	if !allowedSearchProducer(obs.Source) {
		if strings.TrimSpace(obs.Source) == "" {
			return envelopeErrorf("source is required")
		}
		return envelopeErrorf("invalid source %q", obs.Source)
	}
	if !allowedSearchWindow(obs.Window) {
		if strings.TrimSpace(obs.Window) == "" {
			return envelopeErrorf("window is required")
		}
		return envelopeErrorf("invalid window %q", obs.Window)
	}
	if obs.MeasurementAt.After(now.Add(searchObservationFutureSkew)) {
		return envelopeErrorf("measurement_at must not be in the future")
	}
	switch strings.ToLower(obs.ConsentPolicy) {
	case ConsentPolicyNotApplicable, ConsentPolicyAggregate:
		obs.ConsentPolicy = strings.ToLower(obs.ConsentPolicy)
	default:
		return envelopeErrorf("search observation consent_policy must be not_applicable or aggregate")
	}
	switch obs.Coverage {
	case CoverageObserved, CoverageAbsent, CoverageBlocked, CoverageUnknown, "":
	default:
		return envelopeErrorf("invalid coverage %q", obs.Coverage)
	}
	if LooksLikeIndividualSearchQuery(obs.QueryClass) || LooksLikeQueryHash(obs.QueryClass) {
		return envelopeErrorf("query_class must be an aggregate class, not a raw query")
	}
	return nil
}

func allowedSearchProducer(v string) bool {
	switch strings.TrimSpace(v) {
	case ProducerCONFENGEWeb, ProducerWebCfg, "WEB_CFG", "confenge_web":
		return true
	default:
		return false
	}
}

func allowedSearchWindow(v string) bool {
	switch strings.TrimSpace(v) {
	case Window7dComplete, Window28dComplete, Window90d, WindowOpen:
		return true
	default:
		return false
	}
}

// PersistSearchObservation writes the observation (idempotent on org+event_id).
// It never creates a commercial chain.
func PersistSearchObservation(store Store, obs SearchObservation, now time.Time) (SearchObservationReceipt, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if store == nil {
		return SearchObservationReceipt{}, envelopeErrorf("search observation store unavailable")
	}
	if existing, err := store.GetSearchObservation(obs.OrganizationID, obs.EventID); err != nil {
		return SearchObservationReceipt{}, err
	} else if existing != nil {
		return receiptFromObservation(*existing, true), nil
	}

	ooo := false
	if peers, err := store.ListSearchObservations(obs.OrganizationID, obs.Window); err == nil {
		for _, p := range peers {
			if p.AssetID == obs.AssetID && p.LandingPath == obs.LandingPath && p.OrganicSource == obs.OrganicSource {
				if p.MeasurementAt.After(obs.MeasurementAt) {
					ooo = true
					break
				}
			}
		}
	}
	obs.OutOfOrder = ooo
	if obs.CreatedAt.IsZero() {
		obs.CreatedAt = now.UTC()
	}
	saved, created, err := store.PutSearchObservation(obs)
	if err != nil {
		return SearchObservationReceipt{}, err
	}
	if !created {
		return receiptFromObservation(saved, true), nil
	}
	if ooo {
		_ = store.PutException(Exception{
			OrganizationID: obs.OrganizationID,
			Code:           ExceptionOutOfOrder,
			Reason:         "search observation measurement_at precedes an existing snapshot",
			NextAction:     "keep both snapshots; do not reorder counts",
			Held:           false,
			Synthetic:      obs.Synthetic,
			At:             now.UTC(),
			ReceiptID:      obs.ReceiptID,
			Owner:          OwnerWebCfg,
			Severity:       SeverityMedium,
			Status:         StatusOpen,
		})
	}
	if obs.Synthetic && obs.RecordKind == RecordKindReal {
		_ = store.PutException(Exception{
			OrganizationID: obs.OrganizationID,
			Code:           ExceptionSyntheticTreatedAsReal,
			Reason:         "synthetic search observation labeled real",
			NextAction:     "keep synthetic; exclude from real denominators",
			Held:           true,
			Synthetic:      true,
			At:             now.UTC(),
			ReceiptID:      obs.ReceiptID,
			Owner:          OwnerInboundOps,
			Severity:       SeverityHigh,
			Status:         StatusOpen,
		})
	}
	if obs.Freshness == "stale" {
		_ = store.PutException(Exception{
			OrganizationID: obs.OrganizationID,
			Code:           ExceptionStaleAttribution,
			Reason:         "search observation measurement_at is older than 90d",
			NextAction:     "keep the snapshot; freshness stays stale",
			Held:           false,
			Synthetic:      obs.Synthetic,
			At:             now.UTC(),
			ReceiptID:      obs.ReceiptID,
			Owner:          OwnerWebCfg,
			Severity:       SeverityLow,
			Status:         StatusOpen,
		})
	}
	return receiptFromObservation(saved, false), nil
}

func receiptFromObservation(obs SearchObservation, replay bool) SearchObservationReceipt {
	return SearchObservationReceipt{
		EventID:         obs.EventID,
		AcceptedVersion: OrganicDiscoveryContract,
		ReceiptID:       firstNonEmpty(obs.ReceiptID, obs.EventID),
		Persisted:       true,
		Replay:          replay,
		RecordKind:      firstNonEmpty(obs.RecordKind, NormalizeRecordKind("", obs.Synthetic)),
		NotALead:        true,
		Synthetic:       obs.Synthetic,
		OutOfOrder:      obs.OutOfOrder,
	}
}

func joinFromSearchReceipt(rec SearchObservationReceipt) JoinResult {
	return JoinResult{
		Replay:          rec.Replay,
		Created:         rec.Persisted && !rec.Replay,
		EventID:         rec.EventID,
		AcceptedVersion: rec.AcceptedVersion,
		ReceiptID:       rec.ReceiptID,
		Persisted:       rec.Persisted,
		RecordKind:      rec.RecordKind,
		NotALead:        true,
	}
}

// SearchObservationsToDiscovery projects persisted rows onto scoreboard input.
// Null counts stay nil so the projector can render UNKNOWN, not zero.
func SearchObservationsToDiscovery(rows []SearchObservation) []OrganicDiscoveryAggregate {
	out := make([]OrganicDiscoveryAggregate, 0, len(rows))
	for _, r := range rows {
		out = append(out, OrganicDiscoveryAggregate{
			OrganicSource: r.OrganicSource,
			AssetFamily:   r.AssetFamily,
			AssetID:       r.AssetID,
			LandingPath:   r.LandingPath,
			IntentClass:   r.IntentClass,
			QueryClass:    r.QueryClass,
			Window:        r.Window,
			Eligible:      r.Eligible,
			Appeared:      r.Appeared,
			Clicked:       r.Clicked,
			Engaged:       r.Engaged,
			Coverage:      r.Coverage,
			Freshness:     r.Freshness,
			At:            r.MeasurementAt,
			Synthetic:     r.Synthetic,
		})
	}
	return out
}

func loadDiscovery(store Store, orgID string) []OrganicDiscoveryAggregate {
	if store == nil {
		return nil
	}
	rows, err := store.ListSearchObservations(orgID, "")
	if err != nil || len(rows) == 0 {
		return nil
	}
	return SearchObservationsToDiscovery(rows)
}

// RejectUnsupportedEnvelope returns a 4xx-class error for unknown versions.
// Known lead/commercial types with version "1" or the v1 schema stay accepted.
func RejectUnsupportedEnvelope(ev CommercialEvent) error {
	typ := strings.ToLower(strings.TrimSpace(ev.Type))
	ver := strings.TrimSpace(ev.Version)
	schema := strings.TrimSpace(ev.Schema)
	if isSearchObservationType(typ, ver, schema) {
		return nil
	}
	if typ == "" {
		return envelopeErrorf("type is required")
	}
	if ver == "" && schema == "" {
		return envelopeErrorf("version or schema is required")
	}
	if !knownEventType(typ) {
		return envelopeErrorf("unsupported event type %q", ev.Type)
	}
	if ver != "" && ver != "1" && ver != EventSchemaV1 && ver != OrganicAttributionV1 && ver != OrganicDiscoveryContract {
		return envelopeErrorf("unsupported version %q", ver)
	}
	if schema != "" && schema != EventSchemaV1 && schema != OrganicAttributionV1 && schema != OrganicDiscoveryContract {
		return envelopeErrorf("unsupported schema %q", schema)
	}
	return nil
}

func ingestSearchObservationEvent(store Store, ev CommercialEvent) JoinResult {
	raw, _ := json.Marshal(searchEventToMap(ev))
	now := ev.IngestedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	obs, err := ParseSearchObservation(raw, ev.OrganizationID, now)
	if err != nil {
		ex := Exception{
			OrganizationID: ev.OrganizationID,
			Code:           ExceptionOrphan,
			Reason:         "invalid search observation: " + err.Error(),
			NextAction:     "fix the envelope and retry; do not invent a chain",
			Held:           true,
			Synthetic:      ev.Synthetic,
			At:             now.UTC(),
			Owner:          OwnerWebCfg,
			Severity:       SeverityHigh,
			Status:         StatusOpen,
		}
		if store != nil {
			_ = store.PutException(ex)
		}
		return JoinResult{Held: true, Exceptions: []Exception{ex}, NotALead: true, EventID: ev.EventID}
	}
	rec, err := PersistSearchObservation(store, obs, now)
	if err != nil {
		ex := Exception{
			OrganizationID: obs.OrganizationID,
			Code:           ExceptionUnavailable,
			Reason:         "search observation persist failed: " + err.Error(),
			NextAction:     "retry; do not invent discovery from leads",
			Held:           true,
			Synthetic:      obs.Synthetic,
			At:             now.UTC(),
			ReceiptID:      obs.ReceiptID,
			Owner:          OwnerInboundOps,
			Severity:       SeverityHigh,
			Status:         StatusOpen,
		}
		if store != nil {
			_ = store.PutException(ex)
		}
		return JoinResult{Held: true, Exceptions: []Exception{ex}, NotALead: true, EventID: obs.EventID}
	}
	return joinFromSearchReceipt(rec)
}

func searchEventToMap(ev CommercialEvent) map[string]any {
	m := map[string]any{
		"event_id":       ev.EventID,
		"schema":         firstNonEmpty(ev.Schema, EventSchemaV1),
		"version":        firstNonEmpty(ev.Version, OrganicDiscoveryContract),
		"type":           firstNonEmpty(ev.Type, EventSearchObservation),
		"source":         ev.Source,
		"organic_source": ev.OrganicSource,
		"asset_family":   ev.AssetFamily,
		"asset_id":       ev.AssetID,
		"landing_path":   firstNonEmpty(ev.LandingPath, ev.LandingURL),
		"intent_class":   ev.IntentClass,
		"query_class":    ev.QueryClass,
		"window":         ev.Window,
		"coverage":       ev.Coverage,
		"freshness":      ev.Freshness,
		"producer_sha":   ev.ProducerSHA,
		"synthetic":      ev.Synthetic,
		"record_kind":    ev.RecordKind,
		"consent_policy": ev.ConsentPolicy,
		"receipt_id":     ev.ReceiptID,
		"medium":         ev.Medium,
		"measurement_at": ev.OccurredAt,
		"eligible":       ev.Eligible,
		"appeared":       ev.Appeared,
		"clicked":        ev.Clicked,
		"engaged":        ev.Engaged,
	}
	if ev.MeasurementAt != nil {
		m["measurement_at"] = *ev.MeasurementAt
	}
	if strings.TrimSpace(ev.Query) != "" {
		m["query"] = ev.Query
	}
	if strings.TrimSpace(ev.QueryHash) != "" {
		m["query_hash"] = ev.QueryHash
	}
	if strings.TrimSpace(ev.GSCQuery) != "" {
		m["gsc_query"] = ev.GSCQuery
	}
	return m
}

func hashSearchObservation(obs SearchObservation) string {
	canon := map[string]any{
		"event_id":       obs.EventID,
		"schema":         obs.Schema,
		"version":        obs.Version,
		"type":           obs.Type,
		"source":         obs.Source,
		"organic_source": obs.OrganicSource,
		"asset_family":   obs.AssetFamily,
		"asset_id":       obs.AssetID,
		"landing_path":   obs.LandingPath,
		"intent_class":   obs.IntentClass,
		"query_class":    obs.QueryClass,
		"window":         obs.Window,
		"eligible":       obs.Eligible,
		"appeared":       obs.Appeared,
		"clicked":        obs.Clicked,
		"engaged":        obs.Engaged,
		"coverage":       obs.Coverage,
		"freshness":      obs.Freshness,
		"measurement_at": obs.MeasurementAt.UTC().Format(time.RFC3339),
		"producer_sha":   obs.ProducerSHA,
		"synthetic":      obs.Synthetic,
		"record_kind":    obs.RecordKind,
		"consent_policy": obs.ConsentPolicy,
	}
	raw, _ := json.Marshal(canon)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func nullableNonNegInt(m map[string]any, key string) (*int, error) {
	v, ok := m[key]
	if !ok || v == nil {
		return nil, nil
	}
	n, ok := asInt(v)
	if !ok {
		return nil, envelopeErrorf("%s must be an integer or null", key)
	}
	if n < 0 {
		return nil, envelopeErrorf("%s must not be negative", key)
	}
	return &n, nil
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		if n != float64(int(n)) {
			return 0, false
		}
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	default:
		return 0, false
	}
}

func strAnyMap(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case string:
				if s := strings.TrimSpace(t); s != "" {
					return s
				}
			}
		}
	}
	return ""
}

func boolAnyMap(m map[string]any, keys ...string) bool {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case bool:
				return t
			case string:
				low := strings.ToLower(strings.TrimSpace(t))
				if low == "true" || low == "1" {
					return true
				}
			}
		}
	}
	return false
}

func parseFlexibleTimeMap(m map[string]any, keys ...string) time.Time {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case string:
				if ts := parseFlexibleTimeValue(t); !ts.IsZero() {
					return ts
				}
			case time.Time:
				return t
			}
		}
	}
	return time.Time{}
}

func parseFlexibleTimeValue(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02T15:04:05Z", "2006-01-02"} {
		if ts, err := time.Parse(layout, s); err == nil {
			return ts.UTC()
		}
	}
	return time.Time{}
}

// IntPtr is a test/helper constructor for nullable counts.
func IntPtr(n int) *int { return &n }

// ContainsForbiddenQuery reports raw query/hash leakage in a serialized blob.
func ContainsForbiddenQuery(raw []byte) bool {
	low := strings.ToLower(string(raw))
	for _, tok := range []string{
		"segunda leitura contrato",
		`"gscquery"`,
		`"gsc_query"`,
		"query_hash",
		`"query_hash"`,
	} {
		if strings.Contains(low, tok) {
			return true
		}
	}
	return false
}
