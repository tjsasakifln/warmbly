package dispatch

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// The capacity contract has two independent limits and the scheduler must obey
// the most conservative one that applies, in either direction. Production runs
// them equal today (mailbox min_wait_time 600s, CONFENGE_MIN_SEND_GAP_SECONDS
// 600, CONFENGE_GLOBAL_SENDS_PER_HOUR 6 → 3600/6 = 600), so a regression here
// would be invisible until the day the two numbers diverge. The correct answer
// is never to lower one to match the other.

func TestMailboxGapWinsWhenItIsMoreConservativeThanTheGlobalGap(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	orgID, mailbox := uuid.New(), uuid.New()
	store.SetMailboxEnvelope(readyTestMailbox(orgID, mailbox, 50, 30*time.Minute))

	cfg := mailboxTestConfig()
	cfg.MinGap = 1 * time.Minute // a permissive global stream
	governor := NewGovernor(cfg, store, &FixedClock{T: now})

	first := reserveMailbox(t, governor, orgID, mailbox, "first")
	if !first.Allowed {
		t.Fatalf("first slot denied: %s", first.Reason)
	}
	if err := governor.Commit(t.Context(), first.Reservation.ID); err != nil {
		t.Fatal(err)
	}

	second := reserveMailbox(t, governor, orgID, mailbox, "second")
	if second.Allowed {
		t.Fatal("the global gap must never schedule a mailbox faster than its own min wait")
	}
	if second.Reason != "mailbox_min_gap" {
		t.Fatalf("reason=%q want mailbox_min_gap", second.Reason)
	}
	if !second.NextSlot.Equal(now.Add(30 * time.Minute)) {
		t.Fatalf("next slot=%s want the mailbox gap %s", second.NextSlot, now.Add(30*time.Minute))
	}
}

func TestGlobalGapStillAppliesWhenItIsTheMoreConservativeLimit(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	orgID, mailboxA, mailboxB := uuid.New(), uuid.New(), uuid.New()
	// Both mailboxes are individually permissive; the global stream is not.
	store.SetMailboxEnvelope(readyTestMailbox(orgID, mailboxA, 50, time.Minute))
	store.SetMailboxEnvelope(readyTestMailbox(orgID, mailboxB, 50, time.Minute))

	cfg := mailboxTestConfig()
	cfg.MinGap = 20 * time.Minute
	governor := NewGovernor(cfg, store, &FixedClock{T: now})

	first := reserveMailbox(t, governor, orgID, mailboxA, "first")
	if !first.Allowed {
		t.Fatalf("first slot denied: %s", first.Reason)
	}
	if err := governor.Commit(t.Context(), first.Reservation.ID); err != nil {
		t.Fatal(err)
	}

	// A different mailbox cannot be used to outrun the global gap.
	second := reserveMailbox(t, governor, orgID, mailboxB, "second")
	if second.Allowed {
		t.Fatal("a second mailbox must not bypass the global send gap")
	}
	if !second.NextSlot.Equal(now.Add(20 * time.Minute)) {
		t.Fatalf("next slot=%s want the global gap %s", second.NextSlot, now.Add(20*time.Minute))
	}
}

func TestMailboxDailyCapIsNotWidenedByTheGlobalHourlyRate(t *testing.T) {
	now := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	clock := &FixedClock{T: now}
	store := NewMemoryStore()
	orgID, mailbox := uuid.New(), uuid.New()
	store.SetMailboxEnvelope(readyTestMailbox(orgID, mailbox, 2, time.Minute))

	cfg := mailboxTestConfig() // SendsPerHour 100, no global gap
	governor := NewGovernor(cfg, store, clock)

	for i, key := range []string{"one", "two"} {
		result := reserveMailbox(t, governor, orgID, mailbox, key)
		if !result.Allowed {
			t.Fatalf("slot %d denied: %s", i+1, result.Reason)
		}
		if err := governor.Commit(t.Context(), result.Reservation.ID); err != nil {
			t.Fatal(err)
		}
		clock.T = clock.T.Add(2 * time.Minute)
	}

	third := reserveMailbox(t, governor, orgID, mailbox, "three")
	if third.Allowed {
		t.Fatal("the mailbox daily cap must hold regardless of the global hourly rate")
	}
	if third.Reason != "mailbox_daily_cap" {
		t.Fatalf("reason=%q want mailbox_daily_cap", third.Reason)
	}
}
