package liveintel

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
)

// EnvUnsubscribeSecret keys the INTEL_WATCH opt-out link signature. Without it
// no link can be minted and none verifies: the endpoint fails closed rather
// than accepting an unauthenticated subscription id.
const EnvUnsubscribeSecret = "CONFENGE_INTEL_WATCH_SECRET"

// EnvWatchEnabled is the explicit opt-in for the INTEL_WATCH lane, mirroring
// CONFENGE_FAST_LANE_ENABLED. Unset means dormant, and a dormant lane must
// never be able to hold up Warmbly or CONFENGE first-touch startup.
const EnvWatchEnabled = "CONFENGE_INTEL_WATCH_ENABLED"

func unsubscribeSecret() string {
	return strings.TrimSpace(os.Getenv(EnvUnsubscribeSecret))
}

// WatchEnabled reports whether the INTEL_WATCH lane is opted in.
func WatchEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(EnvWatchEnabled)))
	return v == "1" || v == "true" || v == "on"
}

// WatchStartupDecision is the whole startup contract for this lane, in one pure
// function so a caller cannot get it subtly wrong.
//
// enabled is the operator's explicit opt-in. err is non-nil ONLY when the lane
// is opted in and cannot work: a dormant lane always returns (false, nil), so a
// missing secret can never become a boot failure for the rest of the backend.
// The caller decides how loud to be; it must not turn a dormant lane into one.
func WatchStartupDecision() (enabled bool, err error) {
	if !WatchEnabled() {
		return false, nil
	}
	secret := unsubscribeSecret()
	if secret == "" {
		return true, fmt.Errorf("%s is set but %s is empty: INTEL_WATCH opt-out links cannot be signed or verified",
			EnvWatchEnabled, EnvUnsubscribeSecret)
	}
	// A short key is a typo or a placeholder far more often than a deliberate
	// choice, and an unverifiable opt-out link is a compliance problem.
	if len(secret) < minUnsubscribeSecretLen {
		return true, fmt.Errorf("%s is shorter than %d characters: INTEL_WATCH opt-out links would be trivially forgeable",
			EnvUnsubscribeSecret, minUnsubscribeSecretLen)
	}
	return true, nil
}

const minUnsubscribeSecretLen = 16

// UnsubscribeToken mints the capability carried by one subscription's opt-out
// link. It returns "" when the deployment has no secret configured.
func UnsubscribeToken(organizationID, subscriptionID uuid.UUID) string {
	secret := unsubscribeSecret()
	if secret == "" || organizationID == uuid.Nil || subscriptionID == uuid.Nil {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("intel_watch_unsubscribe/1.0\n" + organizationID.String() + "\n" + subscriptionID.String()))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyUnsubscribeToken checks a link's capability in constant time.
func VerifyUnsubscribeToken(organizationID, subscriptionID uuid.UUID, token string) bool {
	want := UnsubscribeToken(organizationID, subscriptionID)
	if want == "" {
		return false
	}
	return hmac.Equal([]byte(want), []byte(strings.TrimSpace(token)))
}
