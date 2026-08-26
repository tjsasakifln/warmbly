package dispatch

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

func mailboxTestConfig() Config {
	cfg := testCfg()
	cfg.SendsPerHour = 100
	cfg.MinGap = 0
	cfg.BusinessDaysOnly = false
	return cfg
}

func readyTestMailbox(orgID, mailboxID uuid.UUID, dailyCap int, minGap time.Duration) MailboxEnvelope {
	return MailboxEnvelope{
		EmailAccountID: mailboxID,
		OrganizationID: orgID,
		DailyCap:       dailyCap,
		HourlyCap:      DerivedHourlyCap(minGap),
		MinGap:         minGap,
		Ready:          true,
		Timezone:       "UTC",
	}
}

func reserveMailbox(t *testing.T, governor *Governor, orgID, mailboxID uuid.UUID, key string) ReserveResult {
	t.Helper()
	result, err := governor.TryReserve(context.Background(), ReserveRequest{
		OrganizationID: orgID,
		EmailAccountID: &mailboxID,
		Channel:        ChannelEmail,
		MessageKey:     key,
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestMailboxEnvelopeSeparatesSlotsWithoutDuplicatingMessage(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	clock := &FixedClock{T: now}
	store := NewMemoryStore()
	orgID, mailboxA, mailboxB := uuid.New(), uuid.New(), uuid.New()
	store.SetMailboxEnvelope(readyTestMailbox(orgID, mailboxA, 50, 10*time.Minute))
	store.SetMailboxEnvelope(readyTestMailbox(orgID, mailboxB, 50, 10*time.Minute))
	governor := NewGovernor(mailboxTestConfig(), store, clock)

	first := reserveMailbox(t, governor, orgID, mailboxA, "account-recipient-sequence")
	if !first.Allowed {
		t.Fatalf("first mailbox slot denied: %s", first.Reason)
	}
	if err := governor.Commit(context.Background(), first.Reservation.ID); err != nil {
		t.Fatal(err)
	}

	sameMailbox := reserveMailbox(t, governor, orgID, mailboxA, "other-recipient")
	if sameMailbox.Allowed || sameMailbox.Reason != "mailbox_min_gap" {
		t.Fatalf("same mailbox must honor its own min wait: %+v", sameMailbox)
	}
	if !sameMailbox.NextSlot.Equal(now.Add(10 * time.Minute)) {
		t.Fatalf("next mailbox slot=%s want=%s", sameMailbox.NextSlot, now.Add(10*time.Minute))
	}

	otherMailbox := reserveMailbox(t, governor, orgID, mailboxB, "second-account-recipient")
	if !otherMailbox.Allowed {
		t.Fatalf("second configured mailbox should have an independent slot: %s", otherMailbox.Reason)
	}
	if err := governor.Commit(context.Background(), otherMailbox.Reservation.ID); err != nil {
		t.Fatal(err)
	}

	disabled := readyTestMailbox(orgID, mailboxB, 50, 10*time.Minute)
	disabled.Ready = false
	disabled.HealthReason = "mailbox_disabled"
	store.SetMailboxEnvelope(disabled)
	replayOnOtherMailbox := reserveMailbox(t, governor, orgID, mailboxB, "account-recipient-sequence")
	if !replayOnOtherMailbox.Allowed || !replayOnOtherMailbox.AlreadyCommitted {
		t.Fatalf("same account/recipient/sequence key must remain idempotent across mailboxes: %+v", replayOnOtherMailbox)
	}
}

func TestOpenMessageLeaseCannotMoveAcrossMailboxes(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	clock := &FixedClock{T: now}
	store := NewMemoryStore()
	orgID, mailboxA, mailboxB, taskA, taskB := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	store.SetMailboxEnvelope(readyTestMailbox(orgID, mailboxA, 50, 10*time.Minute))
	store.SetMailboxEnvelope(readyTestMailbox(orgID, mailboxB, 50, 10*time.Minute))
	governor := NewGovernor(mailboxTestConfig(), store, clock)

	first, err := governor.TryReserve(context.Background(), ReserveRequest{
		OrganizationID: orgID, EmailAccountID: &mailboxA, TaskID: &taskA,
		Channel: ChannelEmail, MessageKey: "one-recipient-sequence",
	})
	if err != nil || !first.Allowed {
		t.Fatalf("first binding failed: result=%+v err=%v", first, err)
	}
	second, err := governor.TryReserve(context.Background(), ReserveRequest{
		OrganizationID: orgID, EmailAccountID: &mailboxB, TaskID: &taskB,
		Channel: ChannelEmail, MessageKey: "one-recipient-sequence",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Allowed || second.Reason != "message_binding_conflict" || !second.NextSlot.Equal(first.Reservation.LeaseUntil) {
		t.Fatalf("open message moved across mailbox/task binding: %+v", second)
	}
}

func TestMailboxDailyCapCountsAttemptsAndQueueCannotRaiseRate(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	clock := &FixedClock{T: now}
	store := NewMemoryStore()
	orgID, mailboxID := uuid.New(), uuid.New()
	store.SetMailboxEnvelope(readyTestMailbox(orgID, mailboxID, 2, 10*time.Minute))
	governor := NewGovernor(mailboxTestConfig(), store, clock)

	for i := 0; i < 500; i++ {
		draftID := uuid.New()
		if err := governor.Enqueue(context.Background(), EnqueueRequest{
			OrganizationID: orgID,
			EmailAccountID: &mailboxID,
			Channel:        ChannelEmail,
			DraftID:        draftID,
			MessageKey:     fmt.Sprintf("queued:%d", i),
			DueAt:          now.Add(-time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}

	first := reserveMailbox(t, governor, orgID, mailboxID, "attempted:1")
	if !first.Allowed {
		t.Fatalf("first reserve denied: %s", first.Reason)
	}
	if err := governor.MarkAttempt(context.Background(), "attempted:1", now); err != nil {
		t.Fatal(err)
	}
	if err := governor.RecordProviderFailure(context.Background(), uuid.New(), "421", "421 temporary rejection", now); err != nil {
		t.Fatal(err)
	}
	clock.Advance(10 * time.Minute)
	second := reserveMailbox(t, governor, orgID, mailboxID, "attempted:2")
	if !second.Allowed {
		t.Fatalf("second reserve denied: %s", second.Reason)
	}
	if err := governor.MarkAttempt(context.Background(), "attempted:2", clock.Now()); err != nil {
		t.Fatal(err)
	}
	clock.Advance(10 * time.Minute)
	third := reserveMailbox(t, governor, orgID, mailboxID, "attempted:3")
	if third.Allowed || third.Reason != "mailbox_daily_cap" {
		t.Fatalf("backlog must wait after two mailbox attempts: %+v", third)
	}

	status, err := governor.Status(context.Background(), &orgID)
	if err != nil {
		t.Fatal(err)
	}
	if status.QueuedApproved != 500 {
		t.Fatalf("queued=%d want=500", status.QueuedApproved)
	}
	if len(status.Mailboxes) != 1 || status.Mailboxes[0].UsedToday != 2 {
		t.Fatalf("daily attempt usage not reflected: %+v", status.Mailboxes)
	}
	if status.Forecast.SlotsNext24h > 2 {
		t.Fatalf("queue size inflated mailbox forecast: %+v", status.Forecast)
	}
}

func TestCapacityForecastIsDerivedAndNeverPromisesDelivery(t *testing.T) {
	now := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC) // Monday
	cfg := mailboxTestConfig()
	cfg.WindowStart = "09:00"
	cfg.WindowEnd = "18:00"
	cfg.BusinessDaysOnly = true
	mailbox := MailboxCapacity{
		ConfiguredDailyCap:   50,
		ConfiguredMinWaitSec: 600,
		EffectiveDailyCap:    50,
		EffectiveHourlyCap:   6,
		Health:               "ready",
	}

	forecast, first := buildCapacityForecast(now, cfg, []MailboxCapacity{mailbox}, nil, 100, false)
	if first == nil || !first.Equal(now) {
		t.Fatalf("first slot=%v want=%s", first, now)
	}
	if forecast.SlotsNext24h != 50 || forecast.SlotsNext7d != 250 {
		t.Fatalf("forecast=%+v want 50/250 configured slots", forecast)
	}
	if forecast.EstimatedDaysToDrain == nil || *forecast.EstimatedDaysToDrain != 3 {
		t.Fatalf("derived drain estimate=%v want=3 calendar days", forecast.EstimatedDaysToDrain)
	}
	if forecast.DeliveryPromised {
		t.Fatal("capacity slots must never be represented as delivery promises")
	}

	paused, pausedNext := buildCapacityForecast(now, cfg, []MailboxCapacity{mailbox}, nil, 100, true)
	if paused.SlotsNext24h != 0 || paused.PotentialSlotsNext24h != 50 || paused.EstimatedDaysToDrain != nil {
		t.Fatalf("paused forecast must separate potential from effective capacity: %+v", paused)
	}
	if pausedNext != nil {
		t.Fatalf("paused transport exposed an effective next slot: %v", pausedNext)
	}
}

func TestSanitizedRuntimeForecastCrossesMailboxCalendarDays(t *testing.T) {
	now := time.Date(2026, 8, 26, 13, 35, 0, 0, time.UTC) // Wednesday 10:35 in São Paulo
	cfg := mailboxTestConfig()
	cfg.Timezone = "America/Sao_Paulo"
	cfg.WindowStart = "09:00"
	cfg.WindowEnd = "18:00"
	cfg.BusinessDaysOnly = true
	cfg.SendsPerHour = 6
	mailbox := MailboxCapacity{
		ConfiguredDailyCap:   50,
		ConfiguredMinWaitSec: 600,
		EffectiveDailyCap:    50,
		EffectiveHourlyCap:   6,
		Health:               "ready",
	}

	forecast, _ := buildCapacityForecast(now, cfg, []MailboxCapacity{mailbox}, nil, 1, true)
	if forecast.PotentialSlotsNext24h != 55 || forecast.PotentialSlotsNext7d != 255 {
		t.Fatalf("time-sensitive runtime forecast=%+v want potential 55/255", forecast)
	}
	if forecast.SlotsNext24h != 0 || forecast.EstimatedDaysToDrain != nil {
		t.Fatalf("engaged kill switch must leave no effective forecast: %+v", forecast)
	}
}

func TestProviderErrorsStayFactualWithoutInventedThresholds(t *testing.T) {
	cases := map[string]string{
		"421 service unavailable": "provider_4xx",
		"smtp 4.7.0 deferred":     "provider_4xx",
		"550 mailbox unavailable": "provider_5xx",
		"smtp 5.1.1 rejected":     "provider_5xx",
		"RATE_LIMIT_EXCEEDED":     "rate_limit",
		"one hard bounce":         "unknown",
	}
	for input, want := range cases {
		if got := ClassifyProviderError(input, input); got != want {
			t.Errorf("ClassifyProviderError(%q)=%q want=%q", input, got, want)
		}
	}
}

func TestBlockedMailboxRaisesAuthAndQueueRunoffAlerts(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	clock := &FixedClock{T: now}
	store := NewMemoryStore()
	orgID, mailboxID, draftID := uuid.New(), uuid.New(), uuid.New()
	envelope := readyTestMailbox(orgID, mailboxID, 50, 10*time.Minute)
	envelope.Ready = false
	envelope.HealthReason = "auth_failure"
	store.SetMailboxEnvelope(envelope)
	governor := NewGovernor(mailboxTestConfig(), store, clock)
	if err := governor.Enqueue(context.Background(), EnqueueRequest{
		OrganizationID: orgID,
		EmailAccountID: &mailboxID,
		Channel:        ChannelEmail,
		DraftID:        draftID,
		MessageKey:     "blocked-auth",
		DueAt:          now,
	}); err != nil {
		t.Fatal(err)
	}

	status, err := governor.Status(context.Background(), &orgID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Forecast.SlotsNext24h != 0 {
		t.Fatalf("blocked mailbox exposed capacity: %+v", status.Forecast)
	}
	want := map[string]bool{AlertAuthFailure: false, AlertQueueRunoff: false}
	for _, alert := range status.Alerts {
		if _, ok := want[alert.Code]; ok {
			want[alert.Code] = true
		}
	}
	for code, found := range want {
		if !found {
			t.Errorf("missing %s alert: %+v", code, status.Alerts)
		}
	}
}
