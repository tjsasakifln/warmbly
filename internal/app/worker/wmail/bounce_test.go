package wmail

import (
	"testing"

	"github.com/warmbly/warmbly/internal/models"
)

func TestMaybeEmitBouncePreservesSoftDSN(t *testing.T) {
	var got *models.JobEventInboundBounce
	w := &WMail{onEvent: func(kind models.JobEventType, body any) error {
		if kind != models.JobEventTypeInboundBounce {
			t.Fatalf("kind=%s", kind)
		}
		got, _ = body.(*models.JobEventInboundBounce)
		return nil
	}}
	w.maybeEmitBounce(&models.EmailMessageData{
		From:    []string{"Mailer-Daemon <mailer-daemon@example.com>"},
		Subject: "Delivery delayed",
		BodyPlain: "Final-Recipient: rfc822; busy@example.com\n" +
			"Action: delayed\nStatus: 4.4.1\nDiagnostic-Code: smtp; 421 4.4.1 try again\n" +
			"Message-ID: <cohort-message@example.com>\n",
	})
	if got == nil {
		t.Fatal("soft DSN was discarded before the observable event")
	}
	if got.BounceClass != "SOFT" || got.EnhancedStatus != "4.4.1" || got.SMTPStatus != "421" {
		t.Fatalf("soft provenance lost: %+v", got)
	}
	if got.OriginalMessageID != "cohort-message@example.com" {
		t.Fatalf("correlation id=%q", got.OriginalMessageID)
	}
}
