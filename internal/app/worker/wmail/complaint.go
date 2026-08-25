package wmail

import (
	"strings"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/pkg/fbl"
)

func (w *WMail) maybeEmitComplaint(msg *models.EmailMessageData) error {
	body := msg.BodyPlain + "\n" + msg.BodyHTML
	if !fbl.Detect(headerFlagValue(msg.Flags, "Content-Type")) {
		return nil
	}
	report := fbl.Parse(body)
	if !report.Complaint {
		return nil
	}
	originalID := report.OriginalMessageID
	if originalID == "" && len(msg.InReplyTo) > 0 {
		originalID = msg.InReplyTo[len(msg.InReplyTo)-1]
	}
	if strings.Trim(originalID, "<>") == "" {
		return nil
	}
	return w.onEvent(models.JobEventTypeInboundComplaint, &models.JobEventInboundComplaint{
		UserID:            w.UserID,
		EmailID:           w.ID,
		OriginalMessageID: strings.Trim(originalID, "<>"),
		Recipient:         report.Recipient,
		FeedbackType:      report.FeedbackType,
	})
}
