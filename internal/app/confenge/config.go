// Package confenge implements the CONFENGE outreach operational layer:
// import of extra-cli intelligence feeds into Warmbly staging, review queue
// foundation, and outcome outbox (later PRs). Intelligence stays in extra-cli;
// Warmbly remains the execution plane (contacts, campaigns, mailboxes, CRM).
package confenge

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Env keys for CONFENGE outreach. Documented in deploy/config/env.example
// and docs/confenge/.
const (
	EnvEnabled           = "CONFENGE_OUTREACH_ENABLED"
	EnvAutoSend          = "CONFENGE_AUTO_SEND_ENABLED"
	EnvFeedURL           = "CONFENGE_EXTRA_CLI_FEED_URL"
	EnvFeedToken         = "CONFENGE_EXTRA_CLI_FEED_TOKEN"
	EnvAllowedHosts      = "CONFENGE_EXTRA_CLI_ALLOWED_HOSTS"
	EnvOutcomeWebhookURL = "CONFENGE_OUTCOME_WEBHOOK_URL"
	EnvOutcomeWebhookSec = "CONFENGE_OUTCOME_WEBHOOK_SECRET"
	EnvDefaultDailyLimit = "CONFENGE_DEFAULT_CAMPAIGN_DAILY_LIMIT"
	EnvMaxInitialWords   = "CONFENGE_MAX_INITIAL_EMAIL_WORDS"
	EnvRequireHuman      = "CONFENGE_REQUIRE_HUMAN_APPROVAL"
	EnvMaxPayloadBytes   = "CONFENGE_MAX_FEED_PAYLOAD_BYTES"
	// WhatsApp orchestration (channel flags also live in whatsapp.Config).
	EnvWhatsAppEnabled   = "CONFENGE_WHATSAPP_ENABLED"
	EnvCrossChannelHours = "CONFENGE_CROSS_CHANNEL_MIN_INTERVAL_HOURS"
	EnvWhatsAppMaxWords  = "CONFENGE_MAX_WHATSAPP_WORDS"
)

// Defaults for conservative cold outreach.
const (
	DefaultCampaignDailyLimit = 10
	DefaultMaxInitialWords    = 120
	DefaultMaxPayloadBytes    = 32 << 20 // 32 MiB
	DefaultCrossChannelHours  = 24
	DefaultMaxWhatsAppWords   = 70
)

// Config is runtime configuration for the confenge outreach feature.
type Config struct {
	Enabled              bool
	AutoSendEnabled      bool
	FeedURL              string
	FeedToken            string
	AllowedHosts         []string
	OutcomeWebhookURL    string
	OutcomeWebhookSecret string
	DefaultDailyLimit    int
	MaxInitialEmailWords int
	RequireHumanApproval bool
	MaxFeedPayloadBytes  int64
	// WhatsApp commercial orchestration (transport flags: whatsapp.LoadConfig).
	WhatsAppEnabled   bool
	CrossChannelHours int
	MaxWhatsAppWords  int
	// SendingPaused is the env-based kill switch (see also FileKillSwitchActive).
	SendingPaused bool
}

// LoadConfig reads CONFENGE_* env vars. Safe defaults keep the feature off.
func LoadConfig() Config {
	cfg := Config{
		Enabled:              envBool(EnvEnabled, false),
		AutoSendEnabled:      envBool(EnvAutoSend, false),
		FeedURL:              strings.TrimSpace(os.Getenv(EnvFeedURL)),
		FeedToken:            strings.TrimSpace(os.Getenv(EnvFeedToken)),
		AllowedHosts:         splitHosts(os.Getenv(EnvAllowedHosts)),
		OutcomeWebhookURL:    strings.TrimSpace(os.Getenv(EnvOutcomeWebhookURL)),
		OutcomeWebhookSecret: strings.TrimSpace(os.Getenv(EnvOutcomeWebhookSec)),
		DefaultDailyLimit:    envInt(EnvDefaultDailyLimit, DefaultCampaignDailyLimit),
		MaxInitialEmailWords: envInt(EnvMaxInitialWords, DefaultMaxInitialWords),
		RequireHumanApproval: envBool(EnvRequireHuman, true),
		MaxFeedPayloadBytes:  int64(envInt(EnvMaxPayloadBytes, DefaultMaxPayloadBytes)),
		WhatsAppEnabled:      envBool(EnvWhatsAppEnabled, false),
		CrossChannelHours:    envInt(EnvCrossChannelHours, DefaultCrossChannelHours),
		MaxWhatsAppWords:     envInt(EnvWhatsAppMaxWords, DefaultMaxWhatsAppWords),
		SendingPaused:        envBool(EnvSendingPaused, false),
	}
	return cfg
}

// ValidateStartup fails closed on insecure production combinations.
// Called when the feature is enabled.
func (c Config) ValidateStartup(appEnv string) error {
	if !c.Enabled {
		return nil
	}
	prod := strings.EqualFold(appEnv, "prod") || strings.EqualFold(appEnv, "production")
	if c.AutoSendEnabled && c.RequireHumanApproval {
		// Explicit: auto-send may be on only when human approval is also required
		// is contradictory; refuse auto-send when we cannot verify intent.
		// Keep RequireHumanApproval as the safety net — auto-send alone is never default.
	}
	if prod {
		if c.FeedURL != "" {
			if !strings.HasPrefix(strings.ToLower(c.FeedURL), "https://") {
				return fmt.Errorf("%s must be https in production", EnvFeedURL)
			}
			if len(c.AllowedHosts) == 0 {
				return fmt.Errorf("%s is required when feed URL is set in production", EnvAllowedHosts)
			}
		}
		if c.OutcomeWebhookURL != "" {
			if !strings.HasPrefix(strings.ToLower(c.OutcomeWebhookURL), "https://") {
				return fmt.Errorf("%s must be https in production", EnvOutcomeWebhookURL)
			}
			if c.OutcomeWebhookSecret == "" {
				return fmt.Errorf("%s is required when outcome webhook is set", EnvOutcomeWebhookSec)
			}
		}
	}
	if c.DefaultDailyLimit < 1 || c.DefaultDailyLimit > 100 {
		return fmt.Errorf("%s must be between 1 and 100", EnvDefaultDailyLimit)
	}
	if c.MaxInitialEmailWords < 20 || c.MaxInitialEmailWords > 500 {
		return fmt.Errorf("%s must be between 20 and 500", EnvMaxInitialWords)
	}
	return nil
}

func envBool(key string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func splitHosts(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
