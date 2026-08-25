package stripe

import (
	"crypto/sha256"
	"fmt"
	"testing"

	stripeapi "github.com/stripe/stripe-go/v76"
)

func TestPaymentFingerprintForCheckoutHashesExpandedCardFingerprint(t *testing.T) {
	checkout := &stripeapi.CheckoutSession{
		PaymentIntent: &stripeapi.PaymentIntent{
			PaymentMethod: &stripeapi.PaymentMethod{
				Card: &stripeapi.PaymentMethodCard{Fingerprint: "card_fingerprint"},
			},
		},
	}
	wantSum := sha256.Sum256([]byte("card_fingerprint"))
	want := fmt.Sprintf("%x", wantSum[:])

	got := paymentFingerprintForCheckout(checkout)

	if got != want {
		t.Fatalf("fingerprint = %q, want %q", got, want)
	}
	if got == "card_fingerprint" {
		t.Fatal("raw card fingerprint must not be stored")
	}
}

func TestPaymentFingerprintForCheckoutIsStableAcrossCustomers(t *testing.T) {
	checkoutA := &stripeapi.CheckoutSession{
		Customer: &stripeapi.Customer{ID: "cus_a"},
		SetupIntent: &stripeapi.SetupIntent{
			PaymentMethod: &stripeapi.PaymentMethod{
				Card: &stripeapi.PaymentMethodCard{Fingerprint: "shared_card"},
			},
		},
	}
	checkoutB := &stripeapi.CheckoutSession{
		Customer: &stripeapi.Customer{ID: "cus_b"},
		PaymentIntent: &stripeapi.PaymentIntent{
			PaymentMethod: &stripeapi.PaymentMethod{
				Card: &stripeapi.PaymentMethodCard{Fingerprint: "shared_card"},
			},
		},
	}

	if gotA, gotB := paymentFingerprintForCheckout(checkoutA), paymentFingerprintForCheckout(checkoutB); gotA == "" || gotA != gotB {
		t.Fatalf("same card produced different fingerprints: %q != %q", gotA, gotB)
	}
}
