package confenge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
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
		if err := ValidateAuthoritativeSourceFreshness(snap.AuthoritativeSourceFreshness, now); err != nil {
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
