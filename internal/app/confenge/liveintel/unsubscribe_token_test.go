package liveintel

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

const testWatchSecret = "intel-watch-test-secret-0123456789"

// A minted link verifies; anything tampered with does not.
func TestUnsubscribeTokenRoundTrip(t *testing.T) {
	t.Setenv(EnvUnsubscribeSecret, testWatchSecret)
	orgID, subscriptionID := uuid.New(), uuid.New()

	token := UnsubscribeToken(orgID, subscriptionID)
	if token == "" {
		t.Fatal("a configured deployment must mint a token")
	}
	if !VerifyUnsubscribeToken(orgID, subscriptionID, token) {
		t.Fatal("a freshly minted token did not verify")
	}
	// Surrounding whitespace survives a mail client's line wrapping.
	if !VerifyUnsubscribeToken(orgID, subscriptionID, "  "+token+"\n") {
		t.Fatal("a padded token did not verify")
	}

	// Flip the first nibble to a value it definitely is not, so the case is
	// deterministic whatever the signature happens to start with.
	flipped := "a" + token[1:]
	if token[0] == 'a' {
		flipped = "b" + token[1:]
	}
	for name, bad := range map[string]string{
		"empty":     "",
		"truncated": token[:len(token)-2],
		"flipped":   flipped,
		"garbage":   "not-a-token",
	} {
		if VerifyUnsubscribeToken(orgID, subscriptionID, bad) {
			t.Fatalf("%s token verified", name)
		}
	}
	// A token is bound to both ids: neither half is transferable.
	if VerifyUnsubscribeToken(uuid.New(), subscriptionID, token) {
		t.Fatal("a token verified under another organization")
	}
	if VerifyUnsubscribeToken(orgID, uuid.New(), token) {
		t.Fatal("a token verified for another subscription")
	}
	if UnsubscribeToken(uuid.Nil, subscriptionID) != "" || UnsubscribeToken(orgID, uuid.Nil) != "" {
		t.Fatal("an incomplete identity minted a token")
	}
}

// With no secret configured nothing can be minted and nothing verifies. The
// endpoint fails closed rather than accepting an unauthenticated id.
func TestUnsubscribeTokenFailsClosedWithoutASecret(t *testing.T) {
	t.Setenv(EnvUnsubscribeSecret, testWatchSecret)
	orgID, subscriptionID := uuid.New(), uuid.New()
	token := UnsubscribeToken(orgID, subscriptionID)

	t.Setenv(EnvUnsubscribeSecret, "   ")
	if UnsubscribeToken(orgID, subscriptionID) != "" {
		t.Fatal("a token was minted without a secret")
	}
	if VerifyUnsubscribeToken(orgID, subscriptionID, token) {
		t.Fatal("a token verified without a secret")
	}
	if VerifyUnsubscribeToken(orgID, subscriptionID, "") {
		t.Fatal("an empty token verified without a secret")
	}
}

func TestWatchEnabledReadsTheExplicitOptIn(t *testing.T) {
	for _, on := range []string{"1", "true", "TRUE", " on "} {
		t.Setenv(EnvWatchEnabled, on)
		if !WatchEnabled() {
			t.Fatalf("%q did not enable the lane", on)
		}
	}
	for _, off := range []string{"", "0", "false", "no", "yes"} {
		t.Setenv(EnvWatchEnabled, off)
		if WatchEnabled() {
			t.Fatalf("%q enabled the lane", off)
		}
	}
}

// The non-negotiable invariant: a dormant or unconfigured INTEL_WATCH lane can
// never produce a startup error, because the caller turns a startup error into
// a loud log and nothing else must ever be able to hold up first touch.
func TestWatchStartupDecisionNeverFailsWhileDormant(t *testing.T) {
	cases := map[string]struct{ enabled, secret string }{
		"nothing configured at all": {"", ""},
		"secret set but lane off":   {"", testWatchSecret},
		"lane explicitly off":       {"false", ""},
		"lane off with a bad key":   {"0", "x"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv(EnvWatchEnabled, tc.enabled)
			t.Setenv(EnvUnsubscribeSecret, tc.secret)
			enabled, err := WatchStartupDecision()
			if enabled {
				t.Fatal("a dormant lane reported itself enabled")
			}
			if err != nil {
				t.Fatalf("a dormant lane produced a startup error: %v", err)
			}
		})
	}
}

// Opted in and misconfigured is diagnosable at boot, not at the first
// unusable opt-out link.
func TestWatchStartupDecisionFailsLoudlyWhenEnabledAndMisconfigured(t *testing.T) {
	for name, secret := range map[string]string{
		"missing":        "",
		"whitespace":     "    ",
		"too short":      "short-secret",
		"one char short": strings.Repeat("k", minUnsubscribeSecretLen-1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(EnvWatchEnabled, "true")
			t.Setenv(EnvUnsubscribeSecret, secret)
			enabled, err := WatchStartupDecision()
			if !enabled {
				t.Fatal("an opted-in lane reported itself dormant")
			}
			if err == nil {
				t.Fatal("a misconfigured enabled lane started quietly")
			}
			if !strings.Contains(err.Error(), EnvUnsubscribeSecret) {
				t.Fatalf("the error does not name the variable to fix: %v", err)
			}
		})
	}

	t.Setenv(EnvWatchEnabled, "true")
	t.Setenv(EnvUnsubscribeSecret, testWatchSecret)
	enabled, err := WatchStartupDecision()
	if !enabled || err != nil {
		t.Fatalf("a correctly configured lane was refused: enabled=%v err=%v", enabled, err)
	}
	// And an enabled, configured lane can actually mint a usable link.
	orgID, subscriptionID := uuid.New(), uuid.New()
	if !VerifyUnsubscribeToken(orgID, subscriptionID, UnsubscribeToken(orgID, subscriptionID)) {
		t.Fatal("an enabled, configured lane cannot round-trip an opt-out link")
	}
}
