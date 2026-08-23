package dispatch

import (
	"context"
	"testing"
	"time"
)

// The founder saw "fora da janela" on Sunday 2026-08-23 while the same panel
// offered next_slot_at on that same Sunday. Outbound was paused, and the paused
// branch derived the slot from now+MinGap, which never consults the business-day
// rule that produced in_send_window=false.
func TestNextSlotNeverLandsOnAWeekend(t *testing.T) {
	sp, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	// 2026-08-23 14:09 America/Sao_Paulo is the Sunday the founder observed.
	sunday := time.Date(2026, 8, 23, 14, 9, 0, 0, sp)
	if sunday.Weekday() != time.Sunday {
		t.Fatalf("fixture is not a Sunday: %s", sunday.Weekday())
	}

	got := NextEligibleSlot(sunday.Add(30*time.Second), "America/Sao_Paulo", "09:00", "18:00", true)
	local := got.In(sp)
	if local.Weekday() != time.Monday {
		t.Fatalf("next slot after a Sunday must be Monday, got %s (%s)", local, local.Weekday())
	}
	if local.Hour() < 9 || local.Hour() >= 18 {
		t.Fatalf("next slot must fall inside 09:00-18:00, got %s", local)
	}
	if !got.After(sunday) {
		t.Fatalf("next slot %s must be after now %s", got, sunday)
	}
}

// Every weekend instant and every out-of-hours instant must normalize forward
// into a business-day window; none may be handed back unchanged.
func TestNextEligibleSlotNormalizesEveryOutOfWindowInstant(t *testing.T) {
	sp, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	cases := []time.Time{
		time.Date(2026, 8, 22, 11, 0, 0, 0, sp), // Saturday inside hours
		time.Date(2026, 8, 23, 14, 9, 0, 0, sp), // Sunday inside hours
		time.Date(2026, 8, 21, 19, 30, 0, 0, sp), // Friday after close
		time.Date(2026, 8, 24, 6, 0, 0, 0, sp),  // Monday before open
		time.Date(2026, 8, 24, 23, 59, 0, 0, sp), // Monday after close
	}
	for _, c := range cases {
		got := NextEligibleSlot(c, "America/Sao_Paulo", "09:00", "18:00", true)
		in, err := InSendWindowBusiness(got, "America/Sao_Paulo", "09:00", "18:00", true)
		if err != nil {
			t.Fatalf("%s: %v", c, err)
		}
		if !in {
			t.Fatalf("normalized slot for %s is still outside the window: %s", c, got.In(sp))
		}
		if got.Before(c) {
			t.Fatalf("normalized slot for %s went backwards: %s", c, got.In(sp))
		}
	}
}

// An instant already inside a business window is its own next slot.
func TestNextEligibleSlotKeepsAnInWindowInstant(t *testing.T) {
	sp, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	monday := time.Date(2026, 8, 24, 10, 0, 0, 0, sp)
	got := NextEligibleSlot(monday, "America/Sao_Paulo", "09:00", "18:00", true)
	if !got.Equal(monday.UTC()) {
		t.Fatalf("in-window instant must be returned unchanged, got %s want %s", got, monday.UTC())
	}
}

// The window must be resolved by the zone's own rules. Brazil has no DST in
// 2026, but the São Paulo offset is still a zone property and must never be
// read from a hardcoded -03:00: asserting the boundary through the location
// catches any future reintroduction of a fixed offset in the window path.
func TestWindowBoundaryUsesZoneRulesNotAFixedOffset(t *testing.T) {
	for _, tz := range []string{"America/Sao_Paulo", "Europe/Lisbon", "America/New_York"} {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			t.Skipf("tzdata unavailable for %s: %v", tz, err)
		}
		// A zone that does observe DST proves the boundary follows the zone.
		for _, month := range []time.Month{time.January, time.July} {
			// Monday nearest the 12th of that month, at 08:59 and 09:01 local.
			d := time.Date(2026, month, 12, 0, 0, 0, 0, loc)
			for d.Weekday() != time.Monday {
				d = d.AddDate(0, 0, 1)
			}
			before := time.Date(d.Year(), d.Month(), d.Day(), 8, 59, 0, 0, loc)
			after := time.Date(d.Year(), d.Month(), d.Day(), 9, 1, 0, 0, loc)
			inBefore, err := InSendWindowBusiness(before, tz, "09:00", "18:00", true)
			if err != nil {
				t.Fatal(err)
			}
			inAfter, err := InSendWindowBusiness(after, tz, "09:00", "18:00", true)
			if err != nil {
				t.Fatal(err)
			}
			if inBefore {
				t.Fatalf("%s %s: 08:59 local must be outside the window", tz, month)
			}
			if !inAfter {
				t.Fatalf("%s %s: 09:01 local must be inside the window", tz, month)
			}
		}
	}
}

// End-to-end over the exact production shape the founder hit: outbound paused
// by the kill switch, on a Sunday. Status must not report an out-of-window
// state and a Sunday next slot in the same payload. Reverting the paused branch
// of Governor.Status to now+MinGap fails this test.
func TestPausedStatusOnSundayOffersABusinessDaySlot(t *testing.T) {
	sp, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	sunday := time.Date(2026, 8, 23, 14, 9, 0, 0, sp)
	clock := &FixedClock{T: sunday.UTC()}
	cfg := DefaultConfig()
	cfg.BusinessDaysOnly = true
	cfg.EnvPaused = true
	cfg.EnvPauseReason = "kill_switch"
	g := NewGovernor(cfg, NewMemoryStore(), clock)

	st, err := g.Status(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Paused {
		t.Fatal("fixture must be paused")
	}
	if st.InSendWindow {
		t.Fatal("Sunday must not be reported as inside the send window")
	}
	if st.NextSlotAt == nil {
		t.Fatal("a paused, out-of-window status still owes the founder a next slot")
	}
	local := st.NextSlotAt.In(sp)
	if local.Weekday() == time.Saturday || local.Weekday() == time.Sunday {
		t.Fatalf("next_slot_at contradicts in_send_window=false: %s (%s)", local, local.Weekday())
	}
	if local.Hour() < 9 || local.Hour() >= 18 {
		t.Fatalf("next_slot_at must be inside 09:00-18:00, got %s", local)
	}
}
