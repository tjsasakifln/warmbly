package fbl

import "testing"

func TestParseComplaint(t *testing.T) {
	body := "Feedback-Type: abuse\nOriginal-Rcpt-To: rfc822; victim@example.com\nOriginal-Message-ID: <send-123@example.net>"
	if !Detect("multipart/report; report-type=feedback-report") {
		t.Fatal("feedback report was not detected")
	}
	report := Parse(body)
	if !report.Complaint || report.Recipient != "victim@example.com" || report.OriginalMessageID != "send-123@example.net" {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestDetectRejectsComplaintLikePlainText(t *testing.T) {
	if Detect("text/plain") {
		t.Fatal("plain text must not be treated as a feedback report")
	}
}
