package worker

import (
	"os"
	"strings"
	"testing"
)

// This wiring guard is deliberately structural: removing or moving the
// pre-transport event would make failed SMTP attempts disappear while all
// projection unit tests could still remain green.
func TestEmailAttemptObservationPrecedesProviderTransport(t *testing.T) {
	raw, err := os.ReadFile("event_send_email.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	attempt := strings.Index(source, "w.sendEmailAttempted(sendEmail.TaskID)")
	transport := strings.Index(source, "result := mail.Send(ctx")
	if attempt < 0 || transport < 0 || attempt >= transport {
		t.Fatal("EMAIL_ATTEMPTED must be published before mail.Send")
	}
	guard := source[attempt:transport]
	if !strings.Contains(guard, "return fmt.Errorf") {
		t.Fatal("failed attempt publication must block provider transport")
	}
}
