package wmail

import (
	"errors"
	"testing"

	"github.com/warmbly/warmbly/internal/models"
)

func TestMaybeEmitComplaintReturnsPublishFailure(t *testing.T) {
	w := &WMail{onEvent: func(models.JobEventType, any) error {
		return errors.New("broker unavailable")
	}}
	err := w.maybeEmitComplaint(&models.EmailMessageData{
		Flags: []string{"Content-Type: message/feedback-report"},
		BodyPlain: "Feedback-Type: abuse\n" +
			"Original-Rcpt-To: rfc822; lead@example.com\n" +
			"Original-Message-ID: <campaign-message@example.com>\n",
	})
	if err == nil {
		t.Fatal("publish failure was discarded")
	}
}
