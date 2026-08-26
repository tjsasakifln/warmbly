package smtp

import (
	"errors"
	"net/textproto"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/warmbly/warmbly/internal/errx"
)

func TestClassifySMTPFailureRecognizesMicrosoftAuthFailure(t *testing.T) {
	mailErr := classifySMTPFailure(errors.New("535 5.7.515 Access denied, authentication unsuccessful"), true)
	require.Equal(t, errx.MailErrorCodeAuthenticationFailed, mailErr.Code)
}

func TestClassifySMTPFailureRecordsOnlyPermanentRecipientFailures(t *testing.T) {
	permanent := classifySMTPFailure(&textproto.Error{Code: 550, Msg: "5.1.1 mailbox unavailable"}, true)
	transient := classifySMTPFailure(&textproto.Error{Code: 450, Msg: "4.2.0 mailbox busy"}, true)

	require.Equal(t, errx.MailErrorCodeRecipientRejected, permanent.Code)
	require.Equal(t, errx.MailErrorCodeRecipientTemporaryRejected, transient.Code)
}

func TestClassifySMTPFailureDoesNotTreatSenderStageAsRecipientBounce(t *testing.T) {
	mailErr := classifySMTPFailure(&textproto.Error{Code: 550, Msg: "5.7.1 sender rejected"}, false)
	require.Equal(t, errx.MailErrorCodeServerUnreachable, mailErr.Code)
}
