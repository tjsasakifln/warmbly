package dispatch

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

var smtpStatusPattern = regexp.MustCompile(`\b([45])(?:[0-9]{2}|\.[0-9](?:\.[0-9])?)\b`)

const (
	AlertAuthFailure         = "auth_failure"
	AlertProviderInterrupted = "provider_interrupted"
	AlertRepeated4xx         = "repeated_provider_4xx"
	AlertRepeated5xx         = "repeated_provider_5xx"
	AlertRateLimit           = "rate_limit_rejection"
	AlertComplaint           = "complaint"
	AlertMailboxDisabled     = "mailbox_disabled"
	AlertQueueRunoff         = "queue_runoff_without_capacity"
)

func DerivedHourlyCap(minGap time.Duration) int {
	if minGap <= 0 {
		return 1
	}
	capN := int(time.Hour / minGap)
	if capN < 1 {
		return 1
	}
	return capN
}

func effectiveMailboxCap(configured int, provider *int) int {
	capN := configured
	if provider != nil && *provider > 0 && (capN < 1 || *provider < capN) {
		capN = *provider
	}
	if capN < 0 {
		return 0
	}
	return capN
}

func minPositive(values ...int) int {
	out := 0
	for _, value := range values {
		if value > 0 && (out == 0 || value < out) {
			out = value
		}
	}
	return out
}

// ClassifyProviderError classifies facts without inferring thresholds or volume changes.
func ClassifyProviderError(code, text string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	upper := strings.ToUpper(text)
	switch code {
	case "GOOGLE_AUTHENTICATION_FAILED", "INVALID_CREDENTIALS", "AUTHENTICATION_FAILED":
		return "auth_failure"
	case "AUTHORIZATION_FAILED", "ACCOUNT_SUSPENDED", "GOOGLE_FORBIDDEN", "GOOGLE_PAYMENT_REQUIRED":
		return "provider_block"
	case "RATE_LIMIT_EXCEEDED", "SENDING_TOO_FAST", "QUOTA_EXCEEDED":
		return "rate_limit"
	case "SERVER_UNREACHABLE", "CONNECTION_LOST", "IMAP_UNKNOWN":
		return "provider_interrupted"
	case "RECIPIENT_REJECTED":
		return "recipient_rejection"
	}
	if match := smtpStatusPattern.FindStringSubmatch(upper); len(match) == 2 {
		if match[1] == "4" {
			return "provider_4xx"
		}
		return "provider_5xx"
	}
	return "unknown"
}

type forecastMailbox struct {
	mailbox MailboxCapacity
	cursor  time.Time
	times   []time.Time
	used    map[string]int
}

func buildCapacityForecast(now time.Time, cfg Config, mailboxes []MailboxCapacity, globalOccupied []time.Time, queued int, paused bool) (CapacityForecast, *time.Time) {
	now = now.UTC()
	end24 := now.Add(24 * time.Hour)
	end7 := now.Add(7 * 24 * time.Hour)
	potential, first := simulateCapacity(now, end7, cfg, mailboxes, globalOccupied)
	forecast := CapacityForecast{DeliveryPromised: false}
	for _, slot := range potential {
		if slot.Before(end24) {
			forecast.PotentialSlotsNext24h++
		}
		forecast.PotentialSlotsNext7d++
	}
	if paused {
		return forecast, nil
	}
	forecast.SlotsNext24h = forecast.PotentialSlotsNext24h
	forecast.SlotsNext7d = forecast.PotentialSlotsNext7d
	if queued > 0 && forecast.SlotsNext7d > 0 {
		days := int(math.Ceil(float64(queued*7) / float64(forecast.SlotsNext7d)))
		if days < 1 {
			days = 1
		}
		forecast.EstimatedDaysToDrain = &days
	}
	return forecast, first
}

func simulateCapacity(now, end time.Time, cfg Config, mailboxes []MailboxCapacity, globalOccupied []time.Time) ([]time.Time, *time.Time) {
	states := make([]forecastMailbox, 0, len(mailboxes))
	for i := range mailboxes {
		mailbox := mailboxes[i]
		if mailbox.Health == "blocked" || mailbox.EffectiveDailyCap < 1 || mailbox.EffectiveHourlyCap < 1 {
			continue
		}
		cursor := now
		if mailbox.NextEligibleSlot != nil && mailbox.NextEligibleSlot.After(cursor) {
			cursor = mailbox.NextEligibleSlot.UTC()
		}
		states = append(states, forecastMailbox{
			mailbox: mailbox,
			cursor:  cursor,
			times:   append([]time.Time(nil), mailbox.occupiedAt...),
			used:    map[string]int{localDateKey(now, cfg.Timezone): mailbox.UsedToday},
		})
	}
	if len(states) == 0 {
		return nil, nil
	}
	globalTimes := append([]time.Time(nil), globalOccupied...)
	var slots []time.Time
	for len(slots) < 100000 {
		selected := -1
		var candidate time.Time
		for i := range states {
			next, ok := nextMailboxCandidate(&states[i], cfg, end)
			if ok && (selected < 0 || next.Before(candidate)) {
				selected, candidate = i, next
			}
		}
		if selected < 0 || !candidate.Before(end) {
			break
		}
		globalNext := nextRollingSlot(candidate, globalTimes, cfg.SendsPerHour, cfg.MinGap, RollingWindow)
		globalNext = NextEligibleSlot(globalNext, cfg.Timezone, cfg.WindowStart, cfg.WindowEnd, cfg.BusinessDaysOnly)
		if globalNext.After(candidate) {
			states[selected].cursor = globalNext
			continue
		}
		slots = append(slots, candidate)
		globalTimes = append(globalTimes, candidate)
		state := &states[selected]
		state.times = append(state.times, candidate)
		state.used[localDateKey(candidate, cfg.Timezone)]++
		state.cursor = candidate.Add(time.Duration(state.mailbox.ConfiguredMinWaitSec) * time.Second)
	}
	if len(slots) == 0 {
		return slots, nil
	}
	first := slots[0]
	return slots, &first
}

func nextMailboxCandidate(state *forecastMailbox, cfg Config, end time.Time) (time.Time, bool) {
	for i := 0; i < 10000; i++ {
		candidate := NextEligibleSlot(state.cursor, cfg.Timezone, cfg.WindowStart, cfg.WindowEnd, cfg.BusinessDaysOnly)
		if !candidate.Before(end) {
			return time.Time{}, false
		}
		dayKey := localDateKey(candidate, cfg.Timezone)
		if state.used[dayKey] >= state.mailbox.EffectiveDailyCap {
			state.cursor = nextLocalDay(candidate, cfg.Timezone)
			continue
		}
		next := nextRollingSlot(candidate, state.times, state.mailbox.EffectiveHourlyCap,
			time.Duration(state.mailbox.ConfiguredMinWaitSec)*time.Second, RollingWindow)
		next = NextEligibleSlot(next, cfg.Timezone, cfg.WindowStart, cfg.WindowEnd, cfg.BusinessDaysOnly)
		if next.After(candidate) {
			state.cursor = next
			continue
		}
		return candidate, true
	}
	return time.Time{}, false
}

func nextRollingSlot(candidate time.Time, occupied []time.Time, capN int, minGap, window time.Duration) time.Time {
	if window <= 0 {
		window = RollingWindow
	}
	sorted := append([]time.Time(nil), occupied...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Before(sorted[j]) })
	for i := 0; i < len(sorted)+2; i++ {
		var active []time.Time
		cutoff := candidate.Add(-window)
		var last time.Time
		for _, value := range sorted {
			if !value.Before(cutoff) && !value.After(candidate) {
				active = append(active, value)
				last = value
			}
		}
		next := candidate
		if capN > 0 && len(active) >= capN {
			capNext := active[0].Add(window).Add(time.Microsecond)
			if capNext.After(next) {
				next = capNext
			}
		}
		if minGap > 0 && !last.IsZero() {
			gapNext := last.Add(minGap)
			if gapNext.After(next) {
				next = gapNext
			}
		}
		if !next.After(candidate) {
			return candidate
		}
		candidate = next
	}
	return candidate
}

func localDateKey(value time.Time, timezone string) string {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	return value.In(loc).Format("2006-01-02")
}

func nextLocalDay(value time.Time, timezone string) time.Time {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	local := value.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, loc).UTC()
}
