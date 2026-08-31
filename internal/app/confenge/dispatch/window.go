package dispatch

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type WindowSnapshot struct {
	OccupiedAt   []time.Time
	LastOccupied time.Time
	Cap          int
	MinGap       time.Duration
	Window       time.Duration
	Now          time.Time
}

func (w WindowSnapshot) OccupiedCount() int {
	if w.Window <= 0 {
		w.Window = RollingWindow
	}
	cutoff := w.Now.Add(-w.Window)
	n := 0
	for _, t := range w.OccupiedAt {
		if !t.Before(cutoff) && !t.After(w.Now) {
			n++
		}
	}
	return n
}

func (w WindowSnapshot) CanGrant() (ok bool, reason string, next time.Time) {
	if w.Cap < 1 {
		return false, "cap_disabled", w.Now.Add(w.MinGap)
	}
	if w.Window <= 0 {
		w.Window = RollingWindow
	}
	count := w.OccupiedCount()
	if count >= w.Cap {
		cutoff := w.Now.Add(-w.Window)
		var oldest time.Time
		for _, t := range w.OccupiedAt {
			if !t.Before(cutoff) && !t.After(w.Now) {
				if oldest.IsZero() || t.Before(oldest) {
					oldest = t
				}
			}
		}
		if !oldest.IsZero() {
			next = oldest.Add(w.Window)
		} else {
			next = w.Now.Add(w.MinGap)
		}
		if w.MinGap > 0 && !w.LastOccupied.IsZero() {
			gapNext := w.LastOccupied.Add(w.MinGap)
			if gapNext.After(next) {
				next = gapNext
			}
		}
		return false, "cap_reached", next
	}
	if w.MinGap > 0 && !w.LastOccupied.IsZero() {
		gapNext := w.LastOccupied.Add(w.MinGap)
		if w.Now.Before(gapNext) {
			return false, "min_gap", gapNext
		}
	}
	return true, "", time.Time{}
}

func InSendWindow(now time.Time, tzName, startHHMM, endHHMM string) (bool, error) {
	startHHMM = strings.TrimSpace(startHHMM)
	endHHMM = strings.TrimSpace(endHHMM)
	if startHHMM == "" || endHHMM == "" {
		return true, nil
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		loc = time.FixedZone("SP", -3*3600)
	}
	local := now.In(loc)
	startM, err := parseHHMM(startHHMM)
	if err != nil {
		return false, err
	}
	endM, err := parseHHMM(endHHMM)
	if err != nil {
		return false, err
	}
	cur := local.Hour()*60 + local.Minute()
	if endM > startM {
		return cur >= startM && cur < endM, nil
	}
	return cur >= startM || cur < endM, nil
}

func NextWindowOpen(now time.Time, tzName, startHHMM, endHHMM string) time.Time {
	in, err := InSendWindow(now, tzName, startHHMM, endHHMM)
	if err != nil || startHHMM == "" || endHHMM == "" {
		return now
	}
	if in {
		return now
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		loc = time.FixedZone("SP", -3*3600)
	}
	local := now.In(loc)
	startM, err := parseHHMM(startHHMM)
	if err != nil {
		return now
	}
	today := time.Date(local.Year(), local.Month(), local.Day(), startM/60, startM%60, 0, 0, loc)
	if !today.After(local) {
		today = today.Add(24 * time.Hour)
	}
	return today.UTC()
}

func parseHHMM(s string) (int, error) {
	var h, m int
	n, err := fmt.Sscanf(s, "%d:%d", &h, &m)
	if err != nil || n != 2 || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("invalid HH:MM %q", s)
	}
	return h*60 + m, nil
}

func MessageKeyEmail(draftID uuid.UUID) string {
	return "email:draft:" + draftID.String()
}

// MessageKeyFirstTouch is the delivery identity of an account's initial email.
// A draft is an editable rendering of that message, so it must not affect
// exactly-once delivery.
func MessageKeyFirstTouch(accountID uuid.UUID) string {
	return "email:first-touch:account:" + accountID.String()
}

func MessageKeyWhatsApp(draftID uuid.UUID) string {
	return "wa:draft:" + draftID.String()
}

// IsBusinessDay reports Mon–Fri in the given timezone (holiday calendar out of scope).
func IsBusinessDay(now time.Time, tzName string) bool {
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		loc = time.FixedZone("SP", -3*3600)
	}
	wd := now.In(loc).Weekday()
	return wd >= time.Monday && wd <= time.Friday
}

// InSendWindowBusiness is InSendWindow plus Mon–Fri when businessDaysOnly is true.
func InSendWindowBusiness(now time.Time, tzName, startHHMM, endHHMM string, businessDaysOnly bool) (bool, error) {
	if businessDaysOnly && !IsBusinessDay(now, tzName) {
		return false, nil
	}
	return InSendWindow(now, tzName, startHHMM, endHHMM)
}

// NextWindowOpenBusiness advances to next weekday open when businessDaysOnly.
func NextWindowOpenBusiness(now time.Time, tzName, startHHMM, endHHMM string, businessDaysOnly bool) time.Time {
	if !businessDaysOnly {
		return NextWindowOpen(now, tzName, startHHMM, endHHMM)
	}
	// Walk up to 8 days to find next Mon–Fri window open.
	t := now
	for i := 0; i < 8; i++ {
		in, err := InSendWindowBusiness(t, tzName, startHHMM, endHHMM, true)
		if err != nil {
			return NextWindowOpen(now, tzName, startHHMM, endHHMM)
		}
		if in {
			return t.UTC()
		}
		next := NextWindowOpen(t, tzName, startHHMM, endHHMM)
		if !IsBusinessDay(next, tzName) {
			// Jump to next Monday  start
			loc, err := time.LoadLocation(tzName)
			if err != nil {
				loc = time.FixedZone("SP", -3*3600)
			}
			local := next.In(loc)
			for local.Weekday() == time.Saturday || local.Weekday() == time.Sunday {
				local = local.Add(24 * time.Hour)
			}
			startM, err := parseHHMM(startHHMM)
			if err != nil {
				return next
			}
			next = time.Date(local.Year(), local.Month(), local.Day(), startM/60, startM%60, 0, 0, loc).UTC()
		}
		if !next.After(t) {
			t = t.Add(24 * time.Hour)
			continue
		}
		t = next
	}
	return NextWindowOpen(now, tzName, startHHMM, endHHMM)
}

// NextEligibleSlot normalizes any candidate instant to a time the send window
// would actually admit. Callers derive candidates from a pause gap, a min-gap
// or a rolling-cap expiry, and none of those arithmetic results know about
// business days: 09:00 Monday plus a cap window is 10:00 Monday, but Sunday
// plus a min-gap is still Sunday. Offering such an instant as "next slot"
// contradicts an in_send_window=false computed by the business-day rule.
//
// The returned instant is always either inside the window or the next window
// open, in the configured location, so daylight-saving shifts are resolved by
// the tzdata rules for that zone rather than a hardcoded offset.
func NextEligibleSlot(candidate time.Time, tzName, startHHMM, endHHMM string, businessDaysOnly bool) time.Time {
	in, err := InSendWindowBusiness(candidate, tzName, startHHMM, endHHMM, businessDaysOnly)
	if err == nil && in {
		return candidate.UTC()
	}
	return NextWindowOpenBusiness(candidate, tzName, startHHMM, endHHMM, businessDaysOnly)
}
