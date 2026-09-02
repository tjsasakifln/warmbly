package liveintel

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

// FakeProducer is an in-memory EventProducer for tests. There is no production
// producer yet; this exists so the consumer contract can be exercised.
type FakeProducer struct {
	events []OpportunityEvent
	err    error
}

func (p *FakeProducer) Subscribe(ctx context.Context) (<-chan OpportunityEvent, error) {
	if p.err != nil {
		return nil, p.err
	}
	out := make(chan OpportunityEvent, len(p.events))
	for _, event := range p.events {
		out <- event
	}
	close(out)
	return out, nil
}

// fakeWatchStore is an in-memory WatchStore. Its mutex stands in for the
// database row lock the real ledger relies on: every claim/settle/release
// transition is evaluated and applied atomically, exactly as the conditional
// upsert in pg_intel_watch.go does. It models the state machine, not the SQL.
type fakeWatchStore struct {
	mu            sync.Mutex
	subscriptions []models.IntelWatchSubscription
	ledger        map[models.IntelWatchDeliveryKey]*models.IntelWatchDelivery
	listErr       error
	claimErr      error
	settleErr     error
	claims        int
}

func newFakeWatchStore(subs ...models.IntelWatchSubscription) *fakeWatchStore {
	return &fakeWatchStore{
		subscriptions: subs,
		ledger:        map[models.IntelWatchDeliveryKey]*models.IntelWatchDelivery{},
	}
}

func (s *fakeWatchStore) ListActiveSubscriptionsBySubject(_ context.Context, organizationID uuid.UUID, subjectKey string) ([]models.IntelWatchSubscription, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []models.IntelWatchSubscription
	for _, sub := range s.subscriptions {
		if sub.OrganizationID == organizationID && sub.SubjectKey == subjectKey && sub.Active() {
			out = append(out, sub)
		}
	}
	return out, nil
}

func (s *fakeWatchStore) ClaimDelivery(_ context.Context, key models.IntelWatchDeliveryKey, now time.Time, lease time.Duration, maxAttempts int) (models.IntelWatchClaim, error) {
	if s.claimErr != nil {
		return models.IntelWatchClaim{}, s.claimErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claims++
	leaseUntil := now.Add(lease)
	row, exists := s.ledger[key]
	if !exists {
		s.ledger[key] = &models.IntelWatchDelivery{
			IntelWatchDeliveryKey: key, State: models.IntelWatchDeliveryPending,
			Attempts: 1, ClaimedAt: &now, LeaseUntil: &leaseUntil,
		}
		return models.IntelWatchClaim{Granted: true, State: models.IntelWatchDeliveryPending, Attempts: 1}, nil
	}
	claimable := row.State == models.IntelWatchDeliveryPending &&
		(row.LeaseUntil == nil || !row.LeaseUntil.After(now)) &&
		row.Attempts < maxAttempts
	if !claimable {
		return models.IntelWatchClaim{
			State: row.State, Attempts: row.Attempts,
			Reason: fakeRefusalReason(row, now, maxAttempts),
		}, nil
	}
	row.Attempts++
	row.ClaimedAt = &now
	row.LeaseUntil = &leaseUntil
	row.LastError = ""
	return models.IntelWatchClaim{Granted: true, State: row.State, Attempts: row.Attempts}, nil
}

func fakeRefusalReason(row *models.IntelWatchDelivery, now time.Time, maxAttempts int) string {
	switch row.State {
	case models.IntelWatchDeliveryDispatched:
		return models.IntelWatchClaimAlreadyDispatched
	case models.IntelWatchDeliveryFailed:
		return models.IntelWatchClaimTerminalFailed
	case models.IntelWatchDeliveryAmbiguous:
		return models.IntelWatchClaimParkedAmbiguous
	case models.IntelWatchDeliveryInFlight:
		if row.LeaseUntil != nil && row.LeaseUntil.After(now) {
			return models.IntelWatchClaimHeldElsewhere
		}
		return models.IntelWatchClaimParkedAmbiguous
	}
	if row.Attempts >= maxAttempts {
		return models.IntelWatchClaimAttemptsExhausted
	}
	return models.IntelWatchClaimHeldElsewhere
}

func (s *fakeWatchStore) MarkDeliveryInFlight(_ context.Context, key models.IntelWatchDeliveryKey, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.ledger[key]
	if !ok || row.State != models.IntelWatchDeliveryPending {
		return errors.New("intel watch delivery claim was lost before handoff")
	}
	row.State = models.IntelWatchDeliveryInFlight
	return nil
}

func (s *fakeWatchStore) SettleDelivery(_ context.Context, key models.IntelWatchDeliveryKey, state, errText string, at time.Time) (bool, error) {
	if s.settleErr != nil {
		return false, s.settleErr
	}
	if !models.IntelWatchDeliveryTerminal(state) {
		return false, errors.New("not a terminal state")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.ledger[key]
	if !ok || models.IntelWatchDeliveryTerminal(row.State) {
		return false, nil
	}
	row.State = state
	row.LastError = errText
	row.LeaseUntil = nil
	if state == models.IntelWatchDeliveryDispatched {
		sentAt := at
		row.SentAt = &sentAt
	}
	return true, nil
}

func (s *fakeWatchStore) ReleaseDelivery(_ context.Context, key models.IntelWatchDeliveryKey, errText string, _ time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.ledger[key]
	if !ok || row.State != models.IntelWatchDeliveryPending {
		return false, nil
	}
	row.LeaseUntil = nil
	row.LastError = errText
	return true, nil
}

func (s *fakeWatchStore) ExpireStaleDeliveryHandoffs(_ context.Context, now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	swept := 0
	for _, row := range s.ledger {
		if row.State == models.IntelWatchDeliveryInFlight && row.LeaseUntil != nil && !row.LeaseUntil.After(now) {
			row.State = models.IntelWatchDeliveryAmbiguous
			row.LastError = "handoff_lease_expired"
			row.LeaseUntil = nil
			swept++
		}
	}
	return swept, nil
}

func (s *fakeWatchStore) unsubscribe(id uuid.UUID, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.subscriptions {
		if s.subscriptions[i].ID == id {
			s.subscriptions[i].UnsubscribedAt = &at
		}
	}
}

func (s *fakeWatchStore) state(key models.IntelWatchDeliveryKey) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if row, ok := s.ledger[key]; ok {
		return row.State
	}
	return ""
}

func (s *fakeWatchStore) rows() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.ledger)
}

// expireLease backdates a row's lease so a later claim sees it as abandoned,
// standing in for the worker that held it disappearing.
func (s *fakeWatchStore) expireLease(key models.IntelWatchDeliveryKey, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if row, ok := s.ledger[key]; ok {
		expired := at
		row.LeaseUntil = &expired
	}
}

// recordingDispatcher counts what a real composer would have been asked to
// send, and can be scripted with a per-attempt outcome sequence.
type recordingDispatcher struct {
	mu sync.Mutex
	// outcomes is consumed one entry per attempt; once exhausted the dispatcher
	// delivers. A nil outcomes slice always delivers.
	outcomes []dispatchStep
	sent     []string
	attempts int
	// skipFence makes the dispatcher deliver without taking the handoff fence.
	skipFence bool
	// perEmail overrides the outcome for one recipient, so a batch can contain
	// a failing watcher beside healthy ones.
	perEmail map[string]dispatchStep
}

type dispatchStep struct {
	outcome WatchDispatchOutcome
	err     error
	// fence decides whether this attempt reaches the point of no return.
	fence bool
}

func (d *recordingDispatcher) DispatchWatchUpdate(ctx context.Context, delivery WatchDelivery) (WatchDispatchOutcome, error) {
	d.mu.Lock()
	step := dispatchStep{outcome: WatchDelivered, fence: !d.skipFence}
	if override, ok := d.perEmail[delivery.Subscription.ContactEmail]; ok {
		step = override
	} else if len(d.outcomes) > 0 {
		step = d.outcomes[0]
		d.outcomes = d.outcomes[1:]
	}
	d.attempts++
	d.mu.Unlock()

	if step.fence && delivery.BeforeHandoff != nil {
		if err := delivery.BeforeHandoff(ctx); err != nil {
			// The fence failed closed: the claim is gone, so nothing is sent.
			return WatchTransient, err
		}
	}
	if step.outcome == WatchDelivered {
		d.mu.Lock()
		d.sent = append(d.sent, delivery.Subscription.ContactEmail+"|"+delivery.Event.EventID)
		d.mu.Unlock()
	}
	return step.outcome, step.err
}

func (d *recordingDispatcher) delivered() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.sent...)
}

var errDispatcherDown = errors.New("composer unavailable")
