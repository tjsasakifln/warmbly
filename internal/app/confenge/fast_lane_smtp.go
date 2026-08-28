package confenge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	smtpclient "github.com/warmbly/warmbly/internal/client/smtpimap/smtp"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// SMTPFirstTouchTransport sends one approved first touch over the mailbox's own
// authenticated SMTP session and reports what the provider did.
//
// It runs in the backend on purpose. The provider fact is the one thing this
// system cannot reconstruct, so the process that observes acceptance is the
// process that commits it -- rather than publishing a command and hoping a
// worker, a broker and a consumer all survive long enough to record the answer.
type SMTPFirstTouchTransport struct {
	emails repository.EmailRepository
	// now is injectable for tests.
	now func() time.Time
}

func NewSMTPFirstTouchTransport(emails repository.EmailRepository) *SMTPFirstTouchTransport {
	return &SMTPFirstTouchTransport{emails: emails}
}

func (t *SMTPFirstTouchTransport) clock() time.Time {
	if t.now != nil {
		return t.now().UTC()
	}
	return time.Now().UTC()
}

// SendFirstTouch performs the send. The returned outcome is the whole failure
// model: accepted, permanently rejected, worth retrying, or unknown.
func (t *SMTPFirstTouchTransport) SendFirstTouch(ctx context.Context, msg FirstTouchMessage) (FirstTouchAcceptance, FirstTouchOutcome, error) {
	if t == nil || t.emails == nil {
		return FirstTouchAcceptance{}, FirstTouchTransient, fmt.Errorf("smtp transport not configured")
	}
	to := strings.TrimSpace(msg.To)
	if to == "" {
		return FirstTouchAcceptance{}, FirstTouchPermanent, fmt.Errorf("empty recipient")
	}

	account, xerr := t.emails.GetByID(ctx, msg.EmailAccountID)
	if xerr != nil {
		return FirstTouchAcceptance{}, FirstTouchTransient, fmt.Errorf("mailbox load failed: %s", xerr.Error())
	}
	if account == nil {
		return FirstTouchAcceptance{}, FirstTouchTransient, fmt.Errorf("mailbox not found")
	}
	creds, xerr := t.emails.GetSMTPCredentials(ctx, msg.EmailAccountID)
	if xerr != nil {
		return FirstTouchAcceptance{}, FirstTouchTransient, fmt.Errorf("mailbox credentials unavailable: %s", xerr.Error())
	}
	if creds == nil || strings.TrimSpace(creds.SMTPHost) == "" {
		return FirstTouchAcceptance{}, FirstTouchTransient, fmt.Errorf("mailbox has no SMTP host")
	}

	// We mint the Message-ID and write it as a header, so the id recorded in the
	// ledger is the id the recipient's provider will quote back in a bounce or a
	// reply. Without it there is nothing to correlate an ambiguous result to.
	messageID := firstTouchMessageID(account.Email)

	client := &smtpclient.Client{
		FirstName: account.Name,
		Email:     account.Email,
		AuthType:  models.AuthPlain,
		Credentials: &models.Service{
			Host:     creds.SMTPHost,
			Port:     creds.SMTPPort,
			Username: creds.SMTPUser,
			Password: creds.SMTPPassword,
		},
	}

	mailErr := client.Send(ctx, []string{to}, nil, nil, messageID, msg.Subject, msg.BodyText, "", "", nil)
	if mailErr == nil {
		return FirstTouchAcceptance{
			Provider:          "smtp",
			ProviderMessageID: messageID,
			AcceptedAt:        t.clock(),
		}, FirstTouchAccepted, nil
	}

	err := fmt.Errorf("%s: %s", mailErr.Code, mailErr.Message)
	switch mailErr.Code {
	case errx.MailErrorCodeRecipientRejected:
		// A 5xx at RCPT TO. Retrying this address cannot succeed.
		return FirstTouchAcceptance{}, FirstTouchPermanent, err
	case errx.MailErrorCodeDeliveryUnknown:
		// The failure happened at end-of-DATA. The message may already be on its
		// way. Never resend on this: correlate by Message-ID instead.
		return FirstTouchAcceptance{ProviderMessageID: messageID, Provider: "smtp"}, FirstTouchAmbiguous, err
	default:
		return FirstTouchAcceptance{}, FirstTouchTransient, err
	}
}

// firstTouchMessageID mints an RFC 5322 Message-ID in the sender's own domain.
func firstTouchMessageID(from string) string {
	domain := "confenge.com.br"
	if at := strings.LastIndex(from, "@"); at >= 0 && at+1 < len(from) {
		if d := strings.TrimSpace(from[at+1:]); d != "" {
			domain = d
		}
	}
	return fmt.Sprintf("<%s@%s>", uuid.New().String(), domain)
}
