package intel

import (
	"strings"
	"time"
)

// CalculateOverdue is pure overdue math. It never mutates a provider
// and never invents an IPCA index.
func CalculateOverdue(in OverdueInput) OverdueResult {
	loc := locationOrUTC(in.Location)
	due := in.DueAt.In(loc)
	asOf := in.AsOf.In(loc)
	if asOf.IsZero() {
		asOf = time.Now().In(loc)
	}
	out := OverdueResult{
		PrincipalCents:        in.PrincipalCents,
		FinanceReviewRequired: true,
		IPCAMissing:           in.IPCA == nil || strings.TrimSpace(in.IPCA.Version) == "" || strings.TrimSpace(in.IPCA.IndexRef) == "",
	}
	if in.PrincipalCents <= 0 {
		return out
	}
	if asOf.Before(due) || asOf.Equal(due) {
		return out
	}
	days := calendarDays(due, asOf)
	if days < 0 {
		days = 0
	}
	out.DaysLate = days
	out.PenaltyCents = (in.PrincipalCents * PenaltyRateBPS) / 10000
	out.InterestCents = (in.PrincipalCents * InterestMonthBPS * int64(days)) / (10000 * int64(CommercialDays))
	if in.IPCA != nil && strings.TrimSpace(in.IPCA.Version) != "" && strings.TrimSpace(in.IPCA.IndexRef) != "" {
		out.IPCAAdjustmentCents = in.IPCA.AdjustmentCents
		out.IPCAApplied = true
		out.IPCAMissing = false
	}
	out.TotalCents = in.PrincipalCents + out.PenaltyCents + out.InterestCents + out.IPCAAdjustmentCents
	noticeDays := in.NoticeDays
	if noticeDays <= 0 {
		noticeDays = 7
	}
	n := due.AddDate(0, 0, noticeDays)
	s := due.AddDate(0, 0, noticeDays+15)
	term := due.AddDate(0, 0, noticeDays+30)
	out.NoticeAt = &n
	out.SuspensionAt = &s
	out.TerminationAt = &term
	return out
}

// CalculateEarlyExit is pure early-exit math. Waiver requires a human
// evidence ref. The result is always finance_review_required.
func CalculateEarlyExit(in EarlyExitInput) EarlyExitResult {
	per := EarlyExit180Cents
	plan := strings.ToLower(strings.TrimSpace(in.Plan))
	if strings.Contains(plan, "365") {
		per = EarlyExit365Cents
	}
	started := in.StartedMonths
	if started < 0 {
		started = 0
	}
	calc := per * int64(started)
	cap20 := (in.OriginalCommitmentCents * 20) / 100
	unpaid := in.UnpaidNominalCents
	if unpaid < 0 {
		unpaid = 0
	}

	selected := calc
	which := "calculated"
	if cap20 < selected {
		selected = cap20
		which = "cap_20_percent"
	}
	if unpaid < selected {
		selected = unpaid
		which = "unpaid_nominal"
	}

	waiverOK := in.Waiver.Present && strings.TrimSpace(in.Waiver.EvidenceRef) != "" && strings.TrimSpace(in.Waiver.Actor) != ""
	if waiverOK {
		selected = 0
		which = "waiver"
	}

	return EarlyExitResult{
		CalculatedCents:       calc,
		Cap20PercentCents:     cap20,
		UnpaidNominalCents:    unpaid,
		SelectedCents:         selected,
		SelectedCap:           which,
		WaiverApplied:         waiverOK,
		WaiverValid:           waiverOK,
		FinanceReviewRequired: true,
	}
}

func locationOrUTC(name string) *time.Location {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "America/Sao_Paulo"
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}

func calendarDays(from, to time.Time) int {
	a := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, from.Location())
	b := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, to.Location())
	return int(b.Sub(a).Hours() / 24)
}

// StartedMonths counts calendar months that have begun between start and asOf.
func StartedMonths(start, asOf time.Time) int {
	if asOf.Before(start) {
		return 0
	}
	s := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, start.Location())
	a := time.Date(asOf.Year(), asOf.Month(), 1, 0, 0, 0, 0, asOf.Location())
	n := 0
	for !s.After(a) {
		n++
		s = s.AddDate(0, 1, 0)
	}
	if n == 0 {
		return 1
	}
	return n
}

// AddBusinessDays advances from start by n São Paulo business days (Mon-Fri).
func AddBusinessDays(start time.Time, n int) time.Time {
	loc := locationOrUTC("America/Sao_Paulo")
	t := start.In(loc)
	step := 1
	if n < 0 {
		step = -1
		n = -n
	}
	for n > 0 {
		t = t.AddDate(0, 0, step)
		wd := t.Weekday()
		if wd != time.Saturday && wd != time.Sunday {
			n--
		}
	}
	return t
}

// AddBusinessHours advances by n weekday hours in São Paulo.
func AddBusinessHours(start time.Time, n int) time.Time {
	loc := locationOrUTC("America/Sao_Paulo")
	t := start.In(loc)
	for n > 0 {
		t = t.Add(time.Hour)
		wd := t.Weekday()
		if wd == time.Saturday || wd == time.Sunday {
			continue
		}
		n--
	}
	return t
}
