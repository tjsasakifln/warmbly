package confenge

import (
	"testing"
	"time"
)

func TestSignAndVerifyOutcomeHMAC(t *testing.T) {
	secret := "whsec_test"
	body := []byte(`{"event_type":"REPLIED"}`)
	ts := time.Unix(1700000000, 0).UTC()
	hdr := SignOutcomeHMAC(secret, ts, body)
	if !VerifyOutcomeHMAC(secret, hdr, body, ts, 5*time.Minute) {
		t.Fatal("valid signature rejected")
	}
	if VerifyOutcomeHMAC("other", hdr, body, ts, 5*time.Minute) {
		t.Fatal("wrong secret accepted")
	}
	if VerifyOutcomeHMAC(secret, hdr, []byte(`{}`), ts, 5*time.Minute) {
		t.Fatal("tampered body accepted")
	}
	// Outside skew window
	if VerifyOutcomeHMAC(secret, hdr, body, ts.Add(10*time.Minute), 5*time.Minute) {
		t.Fatal("stale timestamp accepted")
	}
}

func TestOutcomeBackoffGrows(t *testing.T) {
	if OutcomeBackoff(1) != 30*time.Second {
		t.Fatal(OutcomeBackoff(1))
	}
	if OutcomeBackoff(6) != time.Hour {
		t.Fatal(OutcomeBackoff(6))
	}
}
