package confenge

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/liveintel"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// The real-socket tests below talk to a local fake MTA that offers no STARTTLS.
// netbind caches this decision in a sync.Once on first use, so it has to be set
// before any test in this binary reaches the SMTP client. It only ever relaxes
// TLS for a listener this test file created on 127.0.0.1.
func init() {
	_ = os.Setenv("MAIL_TLS_INSECURE", "true")
}

const intelWatchTestSecret = "intel-watch-test-secret-0123456789"

func watchTestDelivery(t *testing.T, fence func(context.Context) error) liveintel.WatchDelivery {
	t.Helper()
	return liveintel.WatchDelivery{
		Subscription: models.IntelWatchSubscription{
			ID: uuid.New(), OrganizationID: uuid.New(),
			ContactEmail: "watcher@example.test",
			IntentKind:   models.IntelWatchIntentDeadlineChanged,
			SubjectKey:   "contrato-2026-0001", Topic: "prazos",
			Cadence: models.IntelWatchCadenceImmediate,
		},
		Event: liveintel.OpportunityEvent{
			Schema: liveintel.EventSchemaV1, EventID: "evt-1",
			EventType: liveintel.EventDeadlineChanged, SubjectKey: "contrato-2026-0001",
			OrgID: uuid.New(), OccurredAt: time.Now().UTC(),
			Payload: map[string]string{
				liveintel.PayloadKeyTitle:     "Pregão 12/2026",
				liveintel.PayloadKeyDeadline:  "2026-10-01",
				liveintel.PayloadKeyPublicURL: "https://example.test/edital",
			},
		},
		ContentHash:   "hash-1",
		BeforeHandoff: fence,
	}
}

// stubWatchTransport records what the adapter handed the transport and returns
// a scripted provider answer.
type stubWatchTransport struct {
	outcome  FirstTouchOutcome
	err      error
	captured FirstTouchMessage
	calls    int
}

func (s *stubWatchTransport) SendFirstTouch(ctx context.Context, msg FirstTouchMessage) (FirstTouchAcceptance, FirstTouchOutcome, error) {
	s.calls++
	s.captured = msg
	if msg.BeforeHandoff != nil && s.outcome != FirstTouchTransient {
		if err := msg.BeforeHandoff(ctx); err != nil {
			return FirstTouchAcceptance{}, FirstTouchTransient, err
		}
	}
	return FirstTouchAcceptance{Provider: "smtp"}, s.outcome, s.err
}

func watchDispatcherFor(transport FirstTouchTransport) *IntelWatchDispatcher {
	mailboxID := uuid.New()
	return NewIntelWatchDispatcher(transport, func(context.Context, uuid.UUID) (uuid.UUID, error) {
		return mailboxID, nil
	})
}

// Every provider reality maps onto exactly one ledger outcome. This is the
// whole reason the adapter exists, so it is pinned explicitly.
func TestIntelWatchDispatcherMapsEveryProviderOutcome(t *testing.T) {
	t.Setenv(liveintel.EnvUnsubscribeSecret, intelWatchTestSecret)
	for _, tc := range []struct {
		provider FirstTouchOutcome
		want     liveintel.WatchDispatchOutcome
	}{
		{FirstTouchAccepted, liveintel.WatchDelivered},
		{FirstTouchPermanent, liveintel.WatchPermanent},
		{FirstTouchTransient, liveintel.WatchTransient},
		{FirstTouchAmbiguous, liveintel.WatchAmbiguous},
		{FirstTouchOutcome("something new"), liveintel.WatchAmbiguous},
	} {
		transport := &stubWatchTransport{outcome: tc.provider}
		got, _ := watchDispatcherFor(transport).DispatchWatchUpdate(context.Background(),
			watchTestDelivery(t, func(context.Context) error { return nil }))
		if got != tc.want {
			t.Fatalf("provider %q mapped to %q, want %q", tc.provider, got, tc.want)
		}
	}
}

// The adapter must hand the ledger's fence straight to the transport: that is
// what makes an end-of-DATA failure ambiguous instead of retryable.
func TestIntelWatchDispatcherPassesTheLedgerFenceToTheTransport(t *testing.T) {
	t.Setenv(liveintel.EnvUnsubscribeSecret, intelWatchTestSecret)
	fenced := false
	transport := &stubWatchTransport{outcome: FirstTouchAccepted}
	if _, err := watchDispatcherFor(transport).DispatchWatchUpdate(context.Background(),
		watchTestDelivery(t, func(context.Context) error { fenced = true; return nil })); err != nil {
		t.Fatal(err)
	}
	if !fenced {
		t.Fatal("the transport never took the ledger's handoff fence")
	}
	if transport.captured.BeforeHandoff == nil {
		t.Fatal("the adapter dropped BeforeHandoff on the way to the transport")
	}
}

// The watch message key must be unmistakable for a first touch. A collision
// would let one lane's idempotency answer the other lane's question.
func TestIntelWatchMessageKeyNamespaceCannotCollideWithFirstTouch(t *testing.T) {
	t.Setenv(liveintel.EnvUnsubscribeSecret, intelWatchTestSecret)
	transport := &stubWatchTransport{outcome: FirstTouchAccepted}
	delivery := watchTestDelivery(t, func(context.Context) error { return nil })
	if _, err := watchDispatcherFor(transport).DispatchWatchUpdate(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	key := transport.captured.MessageKey
	if !strings.HasPrefix(key, "email:intel_watch:") {
		t.Fatalf("watch message key %q is not namespaced", key)
	}
	if strings.HasPrefix(key, "email:campaign:") || strings.Contains(key, ":seq:") {
		t.Fatalf("watch message key %q looks like a first-touch key", key)
	}
	firstTouch := MessageKeyCampaignEmail(uuid.New(), uuid.New(), uuid.New())
	if key == firstTouch {
		t.Fatal("watch and first-touch message keys collided")
	}
}

// An unsubscribed watcher is never written to, even if a stale claim got this
// far, and the answer is terminal so no retry re-attempts it.
func TestIntelWatchDispatcherRefusesAnUnsubscribedWatcher(t *testing.T) {
	t.Setenv(liveintel.EnvUnsubscribeSecret, intelWatchTestSecret)
	transport := &stubWatchTransport{outcome: FirstTouchAccepted}
	delivery := watchTestDelivery(t, func(context.Context) error { return nil })
	stopped := time.Now().UTC()
	delivery.Subscription.UnsubscribedAt = &stopped
	outcome, err := watchDispatcherFor(transport).DispatchWatchUpdate(context.Background(), delivery)
	if outcome != liveintel.WatchPermanent || err == nil {
		t.Fatalf("unsubscribed watcher produced %q / %v", outcome, err)
	}
	if transport.calls != 0 {
		t.Fatal("an unsubscribed watcher reached the transport")
	}
}

// Without a signable opt-out link there is no message we are willing to send.
func TestIntelWatchDispatcherRefusesToSendWithoutAWorkingOptOut(t *testing.T) {
	t.Setenv(liveintel.EnvUnsubscribeSecret, "")
	transport := &stubWatchTransport{outcome: FirstTouchAccepted}
	outcome, err := watchDispatcherFor(transport).DispatchWatchUpdate(context.Background(),
		watchTestDelivery(t, func(context.Context) error { return nil }))
	if outcome != liveintel.WatchPermanent || err == nil {
		t.Fatalf("unsignable opt-out produced %q / %v", outcome, err)
	}
	if transport.calls != 0 {
		t.Fatal("a message with no opt-out link reached the transport")
	}
}

// An unresolvable mailbox is a transient condition, so the delivery stays
// claimable rather than becoming a permanently lost notification.
func TestIntelWatchDispatcherTreatsAnUnresolvedMailboxAsRetryable(t *testing.T) {
	t.Setenv(liveintel.EnvUnsubscribeSecret, intelWatchTestSecret)
	transport := &stubWatchTransport{outcome: FirstTouchAccepted}
	dispatcher := NewIntelWatchDispatcher(transport, func(context.Context, uuid.UUID) (uuid.UUID, error) {
		return uuid.Nil, errors.New("no active CONFENGE mailbox")
	})
	outcome, err := dispatcher.DispatchWatchUpdate(context.Background(),
		watchTestDelivery(t, func(context.Context) error { return nil }))
	if outcome != liveintel.WatchTransient || err == nil {
		t.Fatalf("unresolved mailbox produced %q / %v", outcome, err)
	}
	if transport.calls != 0 {
		t.Fatal("an unresolved mailbox still reached the transport")
	}
}

// ---------------------------------------------------------------------------
// Real-socket tests. These drive the actual SMTPFirstTouchTransport against a
// local TCP listener that speaks SMTP, so the ambiguous classification is
// produced by a real connection dying at end-of-DATA rather than by a stub that
// declares itself ambiguous.
// ---------------------------------------------------------------------------

// fakeMTA is a minimal SMTP server. dropAfterData closes the connection after
// the message body instead of answering the final 250, which is exactly the
// end-of-DATA failure the ambiguous outcome exists for.
type fakeMTA struct {
	listener      net.Listener
	dropAfterData bool
	mu            sync.Mutex
	sawData       bool
}

func startFakeMTA(t *testing.T, dropAfterData bool) *fakeMTA {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &fakeMTA{listener: listener, dropAfterData: dropAfterData}
	go server.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return server
}

func (m *fakeMTA) port() int { return m.listener.Addr().(*net.TCPAddr).Port }

func (m *fakeMTA) reachedData() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sawData
}

func (m *fakeMTA) serve() {
	for {
		conn, err := m.listener.Accept()
		if err != nil {
			return
		}
		go m.handle(conn)
	}
}

func (m *fakeMTA) handle(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	write := func(s string) bool {
		_, err := conn.Write([]byte(s))
		return err == nil
	}
	if !write("220 127.0.0.1 ESMTP fake\r\n") {
		return
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(command, "EHLO"), strings.HasPrefix(command, "HELO"):
			// Deliberately no STARTTLS: the dev-only MAIL_TLS_INSECURE knob set
			// in this file's init is what lets the client continue.
			write("250-127.0.0.1 hello\r\n250 AUTH PLAIN\r\n")
		case strings.HasPrefix(command, "AUTH"):
			write("235 2.7.0 authenticated\r\n")
		case strings.HasPrefix(command, "MAIL FROM"):
			write("250 2.1.0 sender ok\r\n")
		case strings.HasPrefix(command, "RCPT TO"):
			write("250 2.1.5 recipient ok\r\n")
		case command == "DATA":
			m.mu.Lock()
			m.sawData = true
			m.mu.Unlock()
			write("354 end with <CRLF>.<CRLF>\r\n")
			for {
				body, berr := reader.ReadString('\n')
				if berr != nil {
					return
				}
				if strings.TrimRight(body, "\r\n") == "." {
					break
				}
			}
			if m.dropAfterData {
				// The message is on the wire and the answer never arrives. This
				// is the only honest "we do not know" a sender ever gets.
				return
			}
			write("250 2.0.0 accepted\r\n")
		case command == "QUIT":
			write("221 2.0.0 bye\r\n")
			return
		case command == "RSET", command == "NOOP":
			write("250 2.0.0 ok\r\n")
		default:
			write("250 2.0.0 ok\r\n")
		}
	}
}

// stubEmailRepo answers only the two lookups the SMTP transport performs. The
// embedded interface is nil on purpose: any other call panics loudly rather
// than silently succeeding.
type stubEmailRepo struct {
	repository.EmailRepository
	account *models.Email
	creds   *repository.SMTPCredentials
}

func (s stubEmailRepo) GetByID(context.Context, uuid.UUID) (*models.Email, *errx.Error) {
	return s.account, nil
}

func (s stubEmailRepo) GetSMTPCredentials(context.Context, uuid.UUID) (*repository.SMTPCredentials, *errx.Error) {
	return s.creds, nil
}

func realSocketWatchDispatcher(t *testing.T, server *fakeMTA) *IntelWatchDispatcher {
	t.Helper()
	mailboxID := uuid.New()
	repo := stubEmailRepo{
		account: &models.Email{ID: mailboxID, Email: "sender@example.test", Name: "CONFENGE"},
		creds: &repository.SMTPCredentials{
			SMTPHost: "127.0.0.1", SMTPPort: server.port(),
			SMTPUser: "sender@example.test", SMTPPassword: "secret",
		},
	}
	return NewIntelWatchDispatcher(NewSMTPFirstTouchTransport(repo),
		func(context.Context, uuid.UUID) (uuid.UUID, error) { return mailboxID, nil })
}

// A connection that dies at end-of-DATA, over a real socket, must produce
// AMBIGUOUS -- never a transient result that the ledger would let retry.
func TestIntelWatchDispatcherRealSocketEndOfDataFailureIsAmbiguous(t *testing.T) {
	t.Setenv(liveintel.EnvUnsubscribeSecret, intelWatchTestSecret)
	server := startFakeMTA(t, true)
	fenced := false
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	outcome, err := realSocketWatchDispatcher(t, server).DispatchWatchUpdate(ctx,
		watchTestDelivery(t, func(context.Context) error { fenced = true; return nil }))
	if !server.reachedData() {
		t.Fatal("the transport never reached SMTP DATA; the test proved nothing")
	}
	if !fenced {
		t.Fatal("the ledger fence was not taken before the irreversible handoff")
	}
	if outcome != liveintel.WatchAmbiguous {
		t.Fatalf("a real end-of-DATA drop produced %q (err=%v), want AMBIGUOUS", outcome, err)
	}
}

// The same real socket, answering normally, must produce DELIVERED. Without
// this the ambiguous test above could pass for the wrong reason.
func TestIntelWatchDispatcherRealSocketAcceptedIsDelivered(t *testing.T) {
	t.Setenv(liveintel.EnvUnsubscribeSecret, intelWatchTestSecret)
	server := startFakeMTA(t, false)
	fenced := false
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	outcome, err := realSocketWatchDispatcher(t, server).DispatchWatchUpdate(ctx,
		watchTestDelivery(t, func(context.Context) error { fenced = true; return nil }))
	if err != nil {
		t.Fatalf("real-socket accepted send failed: %v", err)
	}
	if !fenced {
		t.Fatal("the ledger fence was not taken before the irreversible handoff")
	}
	if outcome != liveintel.WatchDelivered {
		t.Fatalf("a real accepted send produced %q, want DELIVERED", outcome)
	}
}

// A fence that refuses must stop the send before the message leaves, and the
// result must be retryable rather than a delivered notification.
func TestIntelWatchDispatcherRealSocketRefusedFenceNeverSendsData(t *testing.T) {
	t.Setenv(liveintel.EnvUnsubscribeSecret, intelWatchTestSecret)
	server := startFakeMTA(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	outcome, _ := realSocketWatchDispatcher(t, server).DispatchWatchUpdate(ctx,
		watchTestDelivery(t, func(context.Context) error {
			return fmt.Errorf("claim lost before handoff")
		}))
	if server.reachedData() {
		t.Fatal("SMTP DATA started even though the ledger fence refused")
	}
	if outcome != liveintel.WatchTransient {
		t.Fatalf("a refused fence produced %q, want TRANSIENT", outcome)
	}
}
