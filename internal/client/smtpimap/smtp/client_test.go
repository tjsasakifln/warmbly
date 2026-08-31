package smtp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/textproto"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/warmbly/warmbly/internal/errx"
)

type trackedDataClient struct {
	events *[]string
	err    error
}

func (c trackedDataClient) Data() (io.WriteCloser, error) {
	*c.events = append(*c.events, "data")
	if c.err != nil {
		return nil, c.err
	}
	return nopWriteCloser{Writer: &bytes.Buffer{}}, nil
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

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

func TestSendSMTPDataRunsDurableHookImmediatelyBeforeDATA(t *testing.T) {
	events := []string{"rcpt"}
	err := sendSMTPData(context.Background(), trackedDataClient{events: &events}, []byte("message"), func(context.Context) error {
		events = append(events, "hook")
		return nil
	})
	require.Nil(t, err)
	require.Equal(t, []string{"rcpt", "hook", "data"}, events)
}

func TestSendSMTPDataHookFailureNeverStartsDATA(t *testing.T) {
	events := []string{"rcpt"}
	err := sendSMTPData(context.Background(), trackedDataClient{events: &events}, []byte("message"), func(context.Context) error {
		events = append(events, "hook")
		return errors.New("durable fence unavailable")
	})
	require.Equal(t, errx.MailErrorCodeServerUnreachable, err.Code)
	require.Equal(t, []string{"rcpt", "hook"}, events)
}

func TestSendSMTPDataFailureAfterHookIsAmbiguous(t *testing.T) {
	events := []string{"rcpt"}
	err := sendSMTPData(context.Background(), trackedDataClient{events: &events, err: errors.New("connection lost")}, nil, func(context.Context) error {
		events = append(events, "hook")
		return nil
	})
	require.Equal(t, errx.MailErrorCodeDeliveryUnknown, err.Code)
	require.Equal(t, []string{"rcpt", "hook", "data"}, events)
}
