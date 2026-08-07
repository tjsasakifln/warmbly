package confenge

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/whatsapp"
	"github.com/warmbly/warmbly/internal/models"
)

// Channel readiness codes for the operator panel.
const (
	ReadyOK            = "ready"
	ReadyNotReady      = "not_ready"
	ReadyBlockedPolicy = "blocked_by_policy"
	ReadyNotConfigured = "not_configured"
	ReadyFallback      = "fallback_template"
)

// Readiness is the discrete operator status panel payload.
type Readiness struct {
	Email            string `json:"email"`
	WhatsApp         string `json:"whatsapp"`
	FeedConfigured   bool   `json:"feed_configured"`
	FeedAgeSeconds   *int64 `json:"feed_age_seconds,omitempty"`
	FeedAgeLabel     string `json:"feed_age"`
	OutcomeLoop      string `json:"outcome_loop"`
	AI               string `json:"ai"`
	GovernorCap      int    `json:"governor_cap"`
	QueueCount       int    `json:"queue_count"`
	KillSwitch       bool   `json:"kill_switch"`
	SendingAllowed   bool   `json:"sending_allowed"`
	OutreachEnabled  bool   `json:"outreach_enabled"`
	RequireHuman     bool   `json:"require_human_approval"`
	AutoSendEnabled  bool   `json:"auto_send_enabled"`
	WhatsAppEnabled  bool   `json:"whatsapp_enabled"`
	WhatsAppProvider string `json:"whatsapp_provider,omitempty"`
}

// ReadinessInputs are optional live signals for BuildReadiness.
type ReadinessInputs struct {
	EmailReady            bool
	WhatsAppReady         bool
	WhatsAppPolicyBlocked bool
	LastImportAt          *time.Time
	Queue                 *models.OutreachQueueSummary
	AIConfigured          bool
	WA                    *whatsapp.Config
	Now                   time.Time
}

// BuildReadiness aggregates operator-facing readiness without side effects.
func BuildReadiness(cfg Config, in ReadinessInputs) Readiness {
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	waCfg := whatsapp.LoadConfig()
	if in.WA != nil {
		waCfg = *in.WA
	}

	r := Readiness{
		OutreachEnabled:  cfg.Enabled,
		RequireHuman:     cfg.RequireHumanApproval,
		AutoSendEnabled:  cfg.AutoSendEnabled,
		GovernorCap:      cfg.DefaultDailyLimit,
		FeedConfigured:   strings.TrimSpace(cfg.FeedURL) != "",
		KillSwitch:       !cfg.SendingAllowed(),
		SendingAllowed:   cfg.SendingAllowed(),
		WhatsAppEnabled:  cfg.WhatsAppEnabled || waCfg.Enabled,
		WhatsAppProvider: waCfg.Provider,
	}

	if in.EmailReady {
		r.Email = ReadyOK
	} else {
		r.Email = ReadyNotReady
	}

	switch {
	case !r.WhatsAppEnabled:
		r.WhatsApp = ReadyNotConfigured
	case in.WhatsAppPolicyBlocked:
		r.WhatsApp = ReadyBlockedPolicy
	case in.WhatsAppReady:
		r.WhatsApp = ReadyOK
	default:
		r.WhatsApp = ReadyNotReady
	}

	if cfg.OutcomeWebhookURL != "" && cfg.OutcomeWebhookSecret != "" {
		r.OutcomeLoop = ReadyOK
	} else {
		r.OutcomeLoop = ReadyNotConfigured
	}

	if in.AIConfigured {
		r.AI = ReadyOK
	} else {
		r.AI = ReadyFallback
	}

	if in.LastImportAt != nil {
		age := int64(now.Sub(in.LastImportAt.UTC()).Seconds())
		if age < 0 {
			age = 0
		}
		r.FeedAgeSeconds = &age
		r.FeedAgeLabel = formatAge(age)
	} else {
		r.FeedAgeLabel = "unknown"
	}

	if in.Queue != nil {
		r.QueueCount = in.Queue.NeedsReview + in.Queue.ReadyToGenerate + in.Queue.Approved
	}
	return r
}

func formatAge(seconds int64) string {
	if seconds < 60 {
		return "just now"
	}
	if seconds < 3600 {
		return formatInt(seconds/60) + "m"
	}
	if seconds < 86400 {
		return formatInt(seconds/3600) + "h"
	}
	return formatInt(seconds/86400) + "d"
}

func formatInt(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// CollectReadiness loads org signals and builds Readiness for the API.
func (s *service) CollectReadiness(ctx context.Context, orgID uuid.UUID, emailReady bool) Readiness {
	in := ReadinessInputs{
		EmailReady:   emailReady,
		AIConfigured: s.ai != nil,
	}
	if s.cfg.WhatsAppEnabled && s.wa != nil {
		in.WhatsAppReady = true
	}
	if runs, err := s.repo.ListImportRuns(ctx, orgID, 1); err == nil && len(runs) > 0 {
		if runs[0].FinishedAt != nil {
			in.LastImportAt = runs[0].FinishedAt
		} else {
			t := runs[0].StartedAt
			in.LastImportAt = &t
		}
	}
	if sum, err := s.repo.CountByQueueState(ctx, orgID); err == nil {
		in.Queue = sum
	}
	return BuildReadiness(s.cfg, in)
}

// RedactSecret returns a non-empty secret marker without leaking the value.
func RedactSecret(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return "****"
	}
	return s[:2] + strings.Repeat("*", len(s)-4) + s[len(s)-2:]
}

// AIProviderName reports the configured AI_PROVIDER env (empty = template fallback).
func AIProviderName() string {
	return strings.TrimSpace(os.Getenv("AI_PROVIDER"))
}
