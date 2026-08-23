// Package dsn parses delivery-status notifications (bounce messages, RFC 3464)
// well enough to decide whether a permanent failure occurred, who it was for,
// and which original message it concerns. It is deliberately conservative:
// callers act (suppress a recipient) only on a confirmed permanent failure, so
// a soft/transient bounce is never treated as hard.
package dsn

import (
	"regexp"
	"strings"
)

// Report is the extracted result of parsing a bounce message.
type Report struct {
	// IsBounce is true when the message looks like a delivery-status report at
	// all (worth acting on); false for ordinary mail.
	IsBounce bool
	// Permanent is true only when a 5.x.x status or an explicit permanent
	// "Action: failed" was found. Transient (4.x.x / delayed) stays false.
	Permanent bool
	// BounceClass is HARD for an observed 5.x.x/final failure, SOFT for an
	// observed 4.x.x/delay, and UNKNOWN when the DSN does not prove either.
	BounceClass string
	// EnhancedStatus preserves the machine-readable RFC 3463 status (for
	// example 5.1.1) when present. SMTPStatus preserves the three-digit code.
	EnhancedStatus string
	SMTPStatus     string
	// Diagnostic is a minimized provider diagnostic, capped by Parse. It must
	// never contain the full raw DSN or returned message body.
	Diagnostic string
	// FailedRecipient is the address that bounced (Final/Original-Recipient).
	FailedRecipient string
	// OriginalMessageID is the Message-ID of the original outbound message the
	// bounce concerns, recovered from the returned headers section.
	OriginalMessageID string
}

var (
	reStatus     = regexp.MustCompile(`(?im)^\s*Status:\s*([245]\.\d{1,3}\.\d{1,3})`)
	reAction     = regexp.MustCompile(`(?im)^\s*Action:\s*(failed|delayed|delivered|relayed|expanded)`)
	reFinalRcpt  = regexp.MustCompile(`(?im)^\s*(?:Final|Original)-Recipient:\s*[^;\r\n]*;\s*<?([^\s<>]+@[^\s<>]+?)>?\s*$`)
	reMessageID  = regexp.MustCompile(`(?im)^\s*Message-ID:\s*<([^>\s]+)>`)
	reDiagnostic = regexp.MustCompile(`(?im)^\s*Diagnostic-Code:\s*[^;\r\n]*;\s*([245]\d\d)(?:\s+([^\r\n]*))?`)
	reEmail      = regexp.MustCompile(`(?i)[a-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+`)
)

// senderMarkers identify a machine bounce source in the From line.
var senderMarkers = []string{"mailer-daemon", "postmaster@", "mail-daemon"}

// subjectMarkers are common bounce subjects across providers.
var subjectMarkers = []string{
	"undeliverable", "undelivered mail", "delivery status notification",
	"returned mail", "delivery failure", "mail delivery failed",
	"failure notice", "message not delivered", "delivery incomplete",
}

// Detect reports whether an inbound message looks like a bounce, using only the
// cheap envelope signals (no body parse). Callers gate the full Parse on this.
func Detect(from, subject, contentType string) bool {
	f := strings.ToLower(from)
	for _, m := range senderMarkers {
		if strings.Contains(f, m) {
			return true
		}
	}
	if strings.Contains(strings.ToLower(contentType), "multipart/report") {
		return true
	}
	s := strings.ToLower(subject)
	for _, m := range subjectMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// Parse extracts bounce details from the message body (the DSN parts). It is
// safe on non-DSN bodies: fields simply stay empty. Permanence is decided from
// the machine-readable Status / Diagnostic-Code / Action fields, never from the
// human-readable prose, so a "delayed" notice is never a permanent bounce.
func Parse(body string) Report {
	var r Report

	if m := reStatus.FindStringSubmatch(body); m != nil {
		r.EnhancedStatus = m[1]
		r.BounceClass = classFromLeadingDigit(m[1])
		r.IsBounce = r.BounceClass == "HARD" || r.BounceClass == "SOFT"
		r.Permanent = r.BounceClass == "HARD"
	}
	if m := reDiagnostic.FindStringSubmatch(body); m != nil {
		r.SMTPStatus = m[1]
		if r.BounceClass == "" || r.BounceClass == "UNKNOWN" {
			r.BounceClass = classFromLeadingDigit(m[1])
		}
		r.IsBounce = r.IsBounce || r.BounceClass == "HARD" || r.BounceClass == "SOFT"
		r.Permanent = r.BounceClass == "HARD"
		if len(m) > 2 {
			r.Diagnostic = minimizeDiagnostic(m[2])
		}
	}
	if a := reAction.FindStringSubmatch(body); a != nil {
		if strings.EqualFold(a[1], "failed") && r.hasNo4xx(body) {
			// "failed" is permanent per RFC 3464, but a co-present 4.x.x status
			// (retry-then-fail) means transient — defer to the status code.
			r.Permanent = true
			r.BounceClass = "HARD"
			r.IsBounce = true
		} else if strings.EqualFold(a[1], "delayed") && r.BounceClass == "" {
			r.BounceClass = "SOFT"
			r.IsBounce = true
		}
	}

	if m := reFinalRcpt.FindStringSubmatch(body); m != nil {
		r.FailedRecipient = strings.TrimSpace(m[1])
	}
	// The last Message-ID in the body belongs to the returned original message
	// (the DSN's own id, if present, is a header, not in the body parts).
	if ids := reMessageID.FindAllStringSubmatch(body, -1); len(ids) > 0 {
		r.OriginalMessageID = strings.TrimSpace(ids[len(ids)-1][1])
	}
	if r.BounceClass == "" {
		r.BounceClass = "UNKNOWN"
	}

	return r
}

func classFromLeadingDigit(code string) string {
	switch {
	case strings.HasPrefix(code, "5"):
		return "HARD"
	case strings.HasPrefix(code, "4"):
		return "SOFT"
	default:
		return "UNKNOWN"
	}
}

func minimizeDiagnostic(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	value = reEmail.ReplaceAllString(value, "<redacted-email>")
	const max = 240
	if len(value) > max {
		value = value[:max]
	}
	return value
}

func (r Report) hasNo4xx(body string) bool {
	if m := reStatus.FindStringSubmatch(body); m != nil {
		return !strings.HasPrefix(m[1], "4")
	}
	return true
}
