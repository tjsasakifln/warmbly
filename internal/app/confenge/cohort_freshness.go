package confenge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/warmbly/warmbly/internal/models"
)

const AuthoritativeFreshnessContractV1 = "PNCP_CONTRACT_FRESHNESS/1.0"

// FeedSourceFreshness is the bounded source-truth attestation emitted by
// extra-cli. SourceWindow is intentionally opaque: Warmbly verifies the
// contract/status/expiry and binds every supplied field into the cohort hash.
type FeedSourceFreshness struct {
	ContractVersion              string   `json:"contract_version"`
	Status                       string   `json:"status"`
	ReasonCodes                  []string `json:"reason_codes,omitempty"`
	AsOf                         string   `json:"as_of"`
	ExpiresAt                    string   `json:"expires_at"`
	DeployedSHA                  string   `json:"deployed_sha,omitempty"`
	PolicyVersion                string   `json:"policy_version,omitempty"`
	RunID                        string   `json:"run_id,omitempty"`
	SourceWindow                 any      `json:"source_window,omitempty"`
	LatestSuccessfulClosedWindow string   `json:"latest_successful_closed_window,omitempty"`
	CurrentLagHours              *float64 `json:"current_lag_hours,omitempty"`
	PagesExpected                *int     `json:"pages_expected,omitempty"`
	PagesFetched                 *int     `json:"pages_fetched,omitempty"`
}

// ValidateAuthoritativeSourceFreshness is fail-closed. SMTP eligibility never
// extends the producer's source-truth validity window.
func ValidateAuthoritativeSourceFreshness(f *FeedSourceFreshness, now time.Time) error {
	if f == nil {
		return fmt.Errorf("authoritative PNCP freshness missing")
	}
	if strings.TrimSpace(f.ContractVersion) != AuthoritativeFreshnessContractV1 {
		return fmt.Errorf("authoritative PNCP freshness contract unsupported")
	}
	if strings.ToUpper(strings.TrimSpace(f.Status)) != "FRESH" {
		return fmt.Errorf("authoritative PNCP freshness is %s", firstNonEmpty(f.Status, "UNKNOWN"))
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	asOf, err := parseFreshnessTime(f.AsOf)
	if err != nil {
		return fmt.Errorf("authoritative PNCP freshness as_of invalid")
	}
	expires, err := parseFreshnessTime(f.ExpiresAt)
	if err != nil {
		return fmt.Errorf("authoritative PNCP freshness expires_at invalid")
	}
	if asOf.After(now.UTC().Add(5 * time.Minute)) {
		return fmt.Errorf("authoritative PNCP freshness is future-dated")
	}
	if !now.UTC().Before(expires) {
		return fmt.Errorf("authoritative PNCP freshness expired")
	}
	if !expires.After(asOf) {
		return fmt.Errorf("authoritative PNCP freshness validity window invalid")
	}
	if f.PagesExpected != nil && (f.PagesFetched == nil || *f.PagesFetched < *f.PagesExpected) {
		return fmt.Errorf("authoritative PNCP freshness pagination incomplete")
	}
	return nil
}

// validateAuthoritativeFeedStructure proves the feed was completely applied and
// that its membership attestation is internally whole. It is deliberately
// timeless: an intact snapshot does not decay because the crawler went quiet.
// Commercial eligibility is decided by COMMERCIAL_AUTHORITY/2.0, not here.
// ValidateHistoricalSourceFreshness proves the attestation was well-formed and
// FRESH when it was published. It deliberately does NOT re-test expires_at
// against now: a snapshot that was proven good stays the last known truth even
// after the producer window closes.
func ValidateHistoricalSourceFreshness(f *FeedSourceFreshness) error {
	if f == nil {
		return fmt.Errorf("authoritative PNCP freshness missing")
	}
	if strings.TrimSpace(f.ContractVersion) != AuthoritativeFreshnessContractV1 {
		return fmt.Errorf("authoritative PNCP freshness contract unsupported")
	}
	if strings.ToUpper(strings.TrimSpace(f.Status)) != "FRESH" {
		return fmt.Errorf("authoritative PNCP freshness was never proven FRESH at publication: %s", firstNonEmpty(f.Status, "UNKNOWN"))
	}
	asOf, err := parseFreshnessTime(f.AsOf)
	if err != nil {
		return fmt.Errorf("authoritative PNCP freshness as_of invalid")
	}
	expires, err := parseFreshnessTime(f.ExpiresAt)
	if err != nil {
		return fmt.Errorf("authoritative PNCP freshness expires_at invalid")
	}
	if !expires.After(asOf) {
		return fmt.Errorf("authoritative PNCP freshness validity window invalid")
	}
	if f.PagesExpected != nil && (f.PagesFetched == nil || *f.PagesFetched < *f.PagesExpected) {
		return fmt.Errorf("authoritative PNCP freshness pagination incomplete")
	}
	return nil
}

func validateAuthoritativeFeedStructure(state *models.OutreachFeedSyncState, requireAttestation bool) error {
	if state == nil || state.LastSuccessAt == nil || state.SourceGeneratedAt == nil ||
		strings.TrimSpace(state.LastSnapshotHash) == "" || strings.TrimSpace(state.LastRunID) == "" {
		return fmt.Errorf("authoritative feed is not completely applied")
	}
	if !requireAttestation {
		return nil
	}
	if state.SourceExpiresAt == nil || !validSHA256(state.SourceFreshnessHash) ||
		!state.TargetMembershipComplete || !validSHA256(state.TargetMembershipHash) ||
		state.TargetMembershipCount < 1 || state.SupplierConfirmedCount < 0 ||
		state.SupplierConfirmedCount > state.TargetMembershipCount {
		return fmt.Errorf("authoritative feed attestation is incomplete")
	}
	return nil
}

// authoritativeLastGoodFeedSQL is the SQL equivalent of
// validateAuthoritativeFeedStructure(..., true). The alias must be `feed`.
const authoritativeLastGoodFeedSQL = `feed.last_success_at IS NOT NULL
	AND feed.source_generated_at IS NOT NULL
	AND feed.last_snapshot_hash<>'' AND feed.last_run_id<>''
	AND feed.source_expires_at IS NOT NULL
	AND feed.source_freshness_hash ~ '^[0-9a-f]{64}$'
	AND feed.target_membership_complete
	AND feed.target_membership_hash ~ '^[0-9a-f]{64}$'
	AND feed.target_membership_count > 0
	AND feed.supplier_confirmed_count BETWEEN 0 AND feed.target_membership_count`

// validateAuthoritativeFeedAge is SOURCE ACQUISITION HEALTH ONLY. It answers
// "are we still acquiring facts?" and must never be part of an admission,
// queueing or transport conjunction for an already-proven member.
func validateAuthoritativeFeedAge(state *models.OutreachFeedSyncState, now time.Time, maxAge time.Duration) error {
	if state == nil || state.SourceGeneratedAt == nil {
		return fmt.Errorf("authoritative feed is not completely applied")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if maxAge <= 0 {
		maxAge = 24 * time.Hour
	}
	if state.SourceGeneratedAt.After(now.UTC().Add(5*time.Minute)) || now.UTC().Sub(state.SourceGeneratedAt.UTC()) > maxAge {
		return fmt.Errorf("authoritative feed is stale")
	}
	if state.SourceExpiresAt != nil && !now.UTC().Before(state.SourceExpiresAt.UTC()) {
		return fmt.Errorf("authoritative feed source attestation expired")
	}
	return nil
}

// validateAuthoritativeFeedState is the INGEST-side gate: publishing new facts
// still demands a live FRESH producer window. Callers on the commercial path
// must use validateAuthoritativeFeedStructure instead.
func validateAuthoritativeFeedState(state *models.OutreachFeedSyncState, now time.Time, maxAge time.Duration, requireAttestation bool) error {
	if err := validateAuthoritativeFeedStructure(state, requireAttestation); err != nil {
		return err
	}
	return validateAuthoritativeFeedAge(state, now, maxAge)
}

func parseFreshnessTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("missing timestamp")
	}
	return time.Parse(time.RFC3339Nano, value)
}

func HashAuthoritativeSourceFreshness(f *FeedSourceFreshness) string {
	if f == nil {
		return ""
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// HashFrozenCohort keeps legacy fixture hashes stable when no attestation is
// present, while binding every real source-freshness byte into new cohorts.
func HashFrozenCohort(snap *FrozenCohortSnapshot) string {
	if snap == nil {
		return ""
	}
	membership := HashFrozenMembership(snap.Members)
	freshness := HashAuthoritativeSourceFreshness(snap.AuthoritativeSourceFreshness)
	if freshness == "" && !snap.AuthoritativeFreshnessRequired {
		return membership
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		membership, freshness, fmt.Sprintf("required=%t", snap.AuthoritativeFreshnessRequired),
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

// ValidateFrozenSourceFreshness also detects post-freeze removal or mutation.
// require=true is used by the production CLI even for an old manifest whose
// boolean predates this contract.
func ValidateFrozenSourceFreshness(snap *FrozenCohortSnapshot, now time.Time, require bool) error {
	if snap == nil {
		return fmt.Errorf("frozen cohort required")
	}
	required := require || snap.AuthoritativeFreshnessRequired
	if required || snap.AuthoritativeSourceFreshness != nil {
		// A frozen cohort is already-proven work. It is validated against the
		// attestation it froze, not against the current producer window.
		if err := ValidateHistoricalSourceFreshness(snap.AuthoritativeSourceFreshness); err != nil {
			return err
		}
		observed := HashAuthoritativeSourceFreshness(snap.AuthoritativeSourceFreshness)
		if observed == "" || observed != snap.AuthoritativeFreshnessHash {
			return fmt.Errorf("authoritative PNCP freshness hash drift")
		}
	}
	if strings.TrimSpace(snap.CohortHash) == "" || snap.CohortHash != HashFrozenCohort(snap) {
		return fmt.Errorf("frozen cohort hash drift")
	}
	return nil
}
