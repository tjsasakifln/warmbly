package wmail

import (
	"strings"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/pkg/dsn"
)

// maybeEmitBounce inspects a freshly synced inbound message and, when it is a
// attributable delivery-status notification for one of our sends, emits an
// INBOUND_BOUNCE event so the consumer can suppress the recipient and record the
// bounce against the campaign. This is where API-sent (Gmail/Graph) mail finally
// gets bounce tracking: those sends succeed synchronously, so the only bounce
// signal is the NDR that lands back in the mailbox.
//
// Runs on the worker because the full DSN body is in hand here (the consumer has
// no S3 access). It only PARSES — resolution and suppression stay control-plane.
// Best-effort: a message without a machine-readable bounce class or resolvable
// original id is ignored. Transient (4.x.x) observations are emitted as SOFT;
// suppression remains a consumer-side HARD-only decision.
func (w *WMail) maybeEmitBounce(msg *models.EmailMessageData) error {
	from := strings.Join(msg.From, " ")
	if !dsn.Detect(from, msg.Subject, headerFlagValue(msg.Flags, "Content-Type")) {
		return nil
	}

	report := dsn.Parse(msg.BodyPlain + "\n" + msg.BodyHTML)
	if !report.IsBounce && !report.IsDelivery {
		return nil
	}

	// Resolve the original outbound Message-ID: the DSN body's returned headers
	// are the reliable source; fall back to the envelope In-Reply-To.
	originalID := report.OriginalMessageID
	if originalID == "" && len(msg.InReplyTo) > 0 {
		originalID = strings.Trim(msg.InReplyTo[len(msg.InReplyTo)-1], "<>")
	}
	if originalID == "" {
		return nil
	}
	if report.IsDelivery {
		return w.onEvent(models.JobEventTypeInboundDelivery, &models.JobEventInboundDelivery{
			UserID: w.UserID, EmailID: w.ID,
			OriginalMessageID: strings.Trim(originalID, "<>"), Recipient: report.FailedRecipient,
			EnhancedStatus: report.EnhancedStatus, SMTPStatus: report.SMTPStatus, Diagnostic: report.Diagnostic,
		})
	}

	return w.onEvent(models.JobEventTypeInboundBounce, &models.JobEventInboundBounce{
		UserID:            w.UserID,
		EmailID:           w.ID,
		OriginalMessageID: strings.Trim(originalID, "<>"),
		FailedRecipient:   report.FailedRecipient,
		Reason:            msg.Subject,
		BounceClass:       report.BounceClass,
		EnhancedStatus:    report.EnhancedStatus,
		SMTPStatus:        report.SMTPStatus,
		Diagnostic:        report.Diagnostic,
	})
}

// headerFlagValue reads a "Header:value" pseudo-flag out of the flag slice (the
// sidecar the sync mappers use to carry internet headers). Returns "" if absent.
func headerFlagValue(flags []string, name string) string {
	prefix := name + ":"
	for _, f := range flags {
		if strings.HasPrefix(f, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(f, prefix))
		}
	}
	return ""
}
