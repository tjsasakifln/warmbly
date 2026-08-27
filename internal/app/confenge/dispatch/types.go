// Package dispatch implements mailbox-first CONFENGE email pacing plus the
// shared multi-worker-safe outbound ceiling for email and WhatsApp.
//
// Pacing is operational capacity and reputation protection, not anti-spam evasion.
package dispatch

import (
	"time"

	"github.com/google/uuid"
)

const (
	ChannelEmail    = "EMAIL"
	ChannelWhatsApp = "WHATSAPP"
)

const (
	StateReserved  = "reserved"
	StateCommitted = "committed"
	StateReleased  = "released"
	StateFailed    = "failed"
)

const (
	QueueQueued   = "queued"
	QueueReserved = "reserved"
	// QueueAttempted is the terminal state of a hand-off: the message was given
	// to the transport and nothing has confirmed what the provider did with it.
	// It is deliberately not "sent". Promotion to QueueSent requires an observed
	// provider fact; without one, acceptance stays UNKNOWN.
	QueueAttempted = "attempted"
	QueueSent      = "sent"
	QueueCancelled = "cancelled"
	QueueFailed    = "failed"
)

const (
	DefaultSendsPerHour   = 10
	DefaultMinGapSeconds  = 360
	DefaultTimezone       = "America/Sao_Paulo"
	DefaultWindowStart    = "09:00"
	DefaultWindowEnd      = "18:00"
	DefaultLeaseTTL       = 5 * time.Minute
	RollingWindow         = 60 * time.Minute
	DefaultMaxRecentFails = 20
)

type Clock interface {
	Now() time.Time
}

type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now().UTC() }

type FixedClock struct {
	T time.Time
}

func (c *FixedClock) Now() time.Time { return c.T.UTC() }

func (c *FixedClock) Advance(d time.Duration) { c.T = c.T.Add(d) }

func (c *FixedClock) Set(t time.Time) { c.T = t.UTC() }

type Config struct {
	SendsPerHour   int
	MinGap         time.Duration
	Timezone       string
	WindowStart    string
	WindowEnd      string
	LeaseTTL       time.Duration
	EnvPaused      bool
	EnvPauseReason string
	// BusinessDaysOnly rejects Sat/Sun even inside HH:MM window (default true).
	BusinessDaysOnly bool
	// Adaptive rate: when RateMode=="adaptive", SendsPerHour is the current effective cap.
	RateMode         string // "fixed" | "adaptive"
	RateStartPerHour int
	RateMaxPerHour   int
	// Health counters for adaptive step (in-memory; store may also track failures).
	AdaptiveBatchSize int // commits required before step-up evaluation
}

func DefaultConfig() Config {
	return Config{
		SendsPerHour:      DefaultSendsPerHour,
		MinGap:            time.Duration(DefaultMinGapSeconds) * time.Second,
		Timezone:          DefaultTimezone,
		WindowStart:       DefaultWindowStart,
		WindowEnd:         DefaultWindowEnd,
		LeaseTTL:          DefaultLeaseTTL,
		BusinessDaysOnly:  true,
		RateMode:          "adaptive",
		RateStartPerHour:  DefaultSendsPerHour,
		RateMaxPerHour:    20,
		AdaptiveBatchSize: 20,
	}
}

// MinGapForRate returns the nominal min-gap for a target hourly rate.
func MinGapForRate(sendsPerHour int) time.Duration {
	switch {
	case sendsPerHour >= 20:
		return 180 * time.Second
	case sendsPerHour >= 15:
		return 240 * time.Second
	default:
		return 360 * time.Second
	}
}

type Reservation struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	EmailAccountID *uuid.UUID
	TaskID         *uuid.UUID
	Channel        string
	MessageKey     string
	DraftID        *uuid.UUID
	State          string
	ReservedAt     time.Time
	AttemptedAt    *time.Time
	LeaseUntil     time.Time
	CommittedAt    *time.Time
	WorkerToken    string
	LastError      string
}

type ReserveRequest struct {
	OrganizationID uuid.UUID
	EmailAccountID *uuid.UUID
	TaskID         *uuid.UUID
	Channel        string
	MessageKey     string
	DraftID        *uuid.UUID
	WorkerToken    string
	// CapOverride, when >0, tightens the hourly cap for this reserve to min(SendsPerHour, CapOverride).
	// Used so campaign_policy max_rate_per_hour cannot be exceeded by adaptive ramp.
	CapOverride int
}

type ReserveResult struct {
	Allowed          bool
	AlreadyCommitted bool
	Reservation      *Reservation
	Reason           string
	NextSlot         time.Time
	SentLastHour     int
	Cap              int
}

type QueueItem struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	EmailAccountID *uuid.UUID
	Channel        string
	DraftID        uuid.UUID
	MessageKey     string
	RecipientRef   string // email or E.164 for DNC/opt-out cancel before reserve
	DueAt          time.Time
	Priority       int
	Attempts       int
	Status         string
	CancelReason   string
	LastError      string
	CreatedAt      time.Time
}

type EnqueueRequest struct {
	OrganizationID uuid.UUID
	EmailAccountID *uuid.UUID
	Channel        string
	DraftID        uuid.UUID
	MessageKey     string
	RecipientRef   string
	DueAt          time.Time
	Priority       int
}

type ControlState struct {
	Paused      bool
	PauseReason string
	PausedAt    *time.Time
	PausedBy    *uuid.UUID
	PauseSource string
}

type FailureRecord struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID *uuid.UUID `json:"organization_id,omitempty"`
	EmailAccountID *uuid.UUID `json:"email_account_id,omitempty"`
	TaskID         *uuid.UUID `json:"task_id,omitempty"`
	Channel        string     `json:"channel"`
	MessageKey     string     `json:"message_key"`
	DraftID        *uuid.UUID `json:"draft_id,omitempty"`
	ErrorCode      string     `json:"error_code,omitempty"`
	ErrorClass     string     `json:"error_class"`
	ErrorText      string     `json:"error_text"`
	OccurredAt     time.Time  `json:"occurred_at"`
}

// MailboxEnvelope keeps unknown provider caps nullable instead of guessing them.
type MailboxEnvelope struct {
	EmailAccountID    uuid.UUID
	OrganizationID    uuid.UUID
	DailyCap          int
	HourlyCap         int
	MinGap            time.Duration
	Ready             bool
	HealthReason      string
	Timezone          string
	ProviderDailyCap  *int
	ProviderHourlyCap *int
	ProviderCapSource string
}

type MailboxBusinessWindow struct {
	Timezone         string `json:"timezone"`
	Start            string `json:"start"`
	End              string `json:"end"`
	BusinessDaysOnly bool   `json:"business_days_only"`
}

type MailboxThroughput struct {
	AcceptedLastHour int `json:"accepted_last_hour"`
	AcceptedToday    int `json:"accepted_today"`
	AcceptedLast7d   int `json:"accepted_last_7d"`
}

type MailboxLatestSignals struct {
	AttemptAt           *time.Time `json:"attempt_at,omitempty"`
	AcceptedAt          *time.Time `json:"accepted_at,omitempty"`
	BounceAt            *time.Time `json:"bounce_at,omitempty"`
	ComplaintAt         *time.Time `json:"complaint_at,omitempty"`
	ReplyAt             *time.Time `json:"reply_at,omitempty"`
	ProviderRejectionAt *time.Time `json:"provider_rejection_at,omitempty"`
	ProviderErrorClass  string     `json:"provider_error_class,omitempty"`
}

type MailboxCapacity struct {
	EmailAccountID       uuid.UUID             `json:"email_account_id"`
	Email                string                `json:"email"`
	Enabled              bool                  `json:"enabled"`
	Status               string                `json:"status"`
	Provider             string                `json:"provider"`
	CredentialsReady     bool                  `json:"credentials_ready"`
	WorkerAssigned       bool                  `json:"worker_assigned"`
	WorkerHealthy        bool                  `json:"worker_healthy"`
	WorkerLastSeenAt     *time.Time            `json:"worker_last_seen_at,omitempty"`
	AuthState            string                `json:"auth_state"`
	AuthSPF              bool                  `json:"auth_spf"`
	AuthDKIM             bool                  `json:"auth_dkim"`
	AuthDMARC            bool                  `json:"auth_dmarc"`
	AuthDMARCPolicy      string                `json:"auth_dmarc_policy,omitempty"`
	AuthCheckedAt        *time.Time            `json:"auth_checked_at,omitempty"`
	MailboxAgeDays       int                   `json:"mailbox_age_days"`
	WarmupStartedAt      *time.Time            `json:"warmup_started_at,omitempty"`
	WarmupAgeDays        *int                  `json:"warmup_age_days,omitempty"`
	WarmupDaysObserved   int                   `json:"warmup_days_observed"`
	ColdRampStartedAt    *time.Time            `json:"cold_ramp_started_at,omitempty"`
	ConfiguredDailyCap   int                   `json:"configured_daily_cap"`
	ConfiguredMinWaitSec int                   `json:"configured_min_wait_seconds"`
	DerivedHourlyCap     int                   `json:"derived_hourly_cap"`
	EffectiveDailyCap    int                   `json:"effective_daily_cap"`
	EffectiveHourlyCap   int                   `json:"effective_hourly_cap"`
	ProviderDailyCap     *int                  `json:"provider_daily_cap,omitempty"`
	ProviderHourlyCap    *int                  `json:"provider_hourly_cap,omitempty"`
	ProviderCapSource    string                `json:"provider_cap_source"`
	BusinessWindow       MailboxBusinessWindow `json:"business_window"`
	Throughput           MailboxThroughput     `json:"observed_throughput"`
	Latest               MailboxLatestSignals  `json:"latest"`
	PauseSource          string                `json:"pause_source,omitempty"`
	Health               string                `json:"health"`
	HealthReason         string                `json:"health_reason"`
	HealthSignals        []string              `json:"health_signals,omitempty"`
	Unknown              []string              `json:"unknown,omitempty"`
	UsedToday            int                   `json:"used_today"`
	NextEligibleSlot     *time.Time            `json:"next_eligible_slot,omitempty"`
	occupiedAt           []time.Time
	errorCounts          map[string]int
	errorLatest          map[string]time.Time
}

type CapacityAlert struct {
	Code           string     `json:"code"`
	Severity       string     `json:"severity"`
	EmailAccountID *uuid.UUID `json:"email_account_id,omitempty"`
	Count          int        `json:"count,omitempty"`
	OccurredAt     *time.Time `json:"occurred_at,omitempty"`
	Reason         string     `json:"reason"`
}

type CapacityForecast struct {
	SlotsNext24h          int  `json:"slots_next_24h"`
	SlotsNext7d           int  `json:"slots_next_7d"`
	PotentialSlotsNext24h int  `json:"potential_slots_next_24h"`
	PotentialSlotsNext7d  int  `json:"potential_slots_next_7d"`
	EstimatedDaysToDrain  *int `json:"estimated_days_to_drain,omitempty"`
	DeliveryPromised      bool `json:"delivery_promised"`
}

type Status struct {
	SentLastHour   int               `json:"sent_last_hour"`
	Cap            int               `json:"cap"`
	MinGapSeconds  int               `json:"min_gap_seconds"`
	NextSlotAt     *time.Time        `json:"next_slot_at,omitempty"`
	QueuedApproved int               `json:"queued_approved"`
	Paused         bool              `json:"paused"`
	PauseReason    string            `json:"pause_reason,omitempty"`
	PausedBy       *uuid.UUID        `json:"paused_by,omitempty"`
	PausedAt       *time.Time        `json:"paused_at,omitempty"`
	PauseSources   []string          `json:"pause_sources,omitempty"`
	InSendWindow   bool              `json:"in_send_window"`
	Timezone       string            `json:"timezone"`
	WindowStart    string            `json:"window_start"`
	WindowEnd      string            `json:"window_end"`
	ActiveLeases   int               `json:"active_leases"`
	RecentFailures []FailureRecord   `json:"recent_failures,omitempty"`
	PauseSource    string            `json:"pause_source,omitempty"`
	CapacitySource string            `json:"capacity_source"`
	Mailboxes      []MailboxCapacity `json:"mailboxes"`
	Forecast       CapacityForecast  `json:"forecast"`
	Alerts         []CapacityAlert   `json:"alerts,omitempty"`
}
