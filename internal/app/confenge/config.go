// Package confenge implements the CONFENGE outreach operational layer:
// import of extra-cli intelligence feeds into Warmbly staging, review queue
// foundation, and outcome outbox (later PRs). Intelligence stays in extra-cli;
// Warmbly remains the execution plane (contacts, campaigns, mailboxes, CRM).
package confenge

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
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
	// EnvDefaultDailyLimit is the Warmbly *campaign shell* daily cap used when
	// bootstrapping the CONFENGE campaign. It is NOT the primary outbound
	// governor. The global ~10 outbound/hour cap lives in
	// CONFENGE_GLOBAL_SENDS_PER_HOUR (dispatch package). Keep this daily value
	// high enough that it does not silently collapse capacity to ~10/day.
	EnvDefaultDailyLimit = "CONFENGE_DEFAULT_CAMPAIGN_DAILY_LIMIT"
	EnvMaxInitialWords   = "CONFENGE_MAX_INITIAL_EMAIL_WORDS"
	EnvRequireHuman      = "CONFENGE_REQUIRE_HUMAN_APPROVAL"
	EnvMaxPayloadBytes   = "CONFENGE_MAX_FEED_PAYLOAD_BYTES"
	// WhatsApp orchestration (channel flags also live in whatsapp.Config).
	EnvWhatsAppEnabled   = "CONFENGE_WHATSAPP_ENABLED"
	EnvCrossChannelHours = "CONFENGE_CROSS_CHANNEL_MIN_INTERVAL_HOURS"
	EnvWhatsAppMaxWords  = "CONFENGE_MAX_WHATSAPP_WORDS"
	// Dynamic activation priority (extra-cli planner). Default OFF (shadow mode).
	EnvDynamicPriority  = "CONFENGE_DYNAMIC_PRIORITY_ENABLED"
	EnvFeedSyncEnabled  = "CONFENGE_FEED_SYNC_ENABLED"
	EnvFeedSyncInterval = "CONFENGE_FEED_SYNC_INTERVAL"
	EnvFeedMaxAge       = "CONFENGE_FEED_MAX_AGE"
	EnvManifestURL      = "CONFENGE_EXTRA_CLI_MANIFEST_URL"
	// Preparation-only ceiling. It grants no approval, scheduling, queueing or
	// transport authority.
	EnvDraftReviewBacklogTarget = "CONFENGE_DRAFT_REVIEW_BACKLOG_TARGET"
	// GREEN autorun under campaign policy authorization (fail-closed default).
	EnvGreenAutorun = "CONFENGE_GREEN_AUTORUN_ENABLED"
	// Delegated first-touch is an explicit, manifest-driven agent authority. It
	// does not enable runtime generation or the legacy GREEN autorun path.
	EnvDelegatedFirstTouch = "CONFENGE_DELEGATED_FIRST_TOUCH_ENABLED"
	// Delegated first-touch autorun evaluates the prepared rolling backlog.
	EnvDelegatedFirstTouchAutorun = "CONFENGE_DELEGATED_FIRST_TOUCH_AUTORUN_ENABLED"
	// Delegated first-touch runway is a scheduling-only ceiling. It grants no
	// transport authority and every entry remains subject to live final gates.
	EnvDelegatedFirstTouchRunwayTarget = "CONFENGE_DELEGATED_FIRST_TOUCH_RUNWAY_TARGET"
	// Adaptive rate (single capacity authority with dispatch governor).
	EnvRateMode                     = "CONFENGE_RATE_MODE"
	EnvRateStartPerHour             = "CONFENGE_RATE_START_PER_HOUR"
	EnvRateMaxPerHour               = "CONFENGE_RATE_MAX_PER_HOUR"
	EnvAllowEnrollMint              = "CONFENGE_ALLOW_ENROLL_MINT" // dev/Mailpit only; ignored in production
	EnvOperatorMode                 = "CONFENGE_OPERATOR_MODE"
	EnvOperatorUserID               = "CONFENGE_OPERATOR_USER_ID"
	EnvOperatorOrgID                = "CONFENGE_OPERATOR_ORG_ID"
	EnvInboundWebhookSec            = "CONFENGE_INBOUND_WEBHOOK_SECRET"
	EnvInboundOrgID                 = "CONFENGE_INBOUND_ORG_ID"
	EnvOperatorAlertEmail           = "CONFENGE_OPERATOR_ALERT_EMAIL"
	EnvOperatorAlertEmailEnabled    = "CONFENGE_OPERATOR_ALERT_EMAIL_ENABLED"
	EnvOperatorAlertEmailKillSwitch = "CONFENGE_OPERATOR_ALERT_EMAIL_KILL_SWITCH"
	// Runtime version bindings compared by the transport gate and release evaluator.
	// Production fail-closes when CONFENGE_OUTREACH_ENABLED=true and SHA is empty.
	EnvRepositorySHA     = "CONFENGE_REPOSITORY_SHA"
	EnvFeedSchemaVersion = "CONFENGE_FEED_SCHEMA_VERSION"
	EnvEvidenceVersion   = "CONFENGE_EVIDENCE_VERSION"
)

// Defaults for conservative cold outreach.
//
// Primary pacing: dispatch.DefaultSendsPerHour = 10 (rolling hour, email+WA).
// Campaign daily limit is a secondary mailbox/campaign safety ceiling only.
// Default 200 allows adaptive peak 20/h × 9h (=180) plus margin so the daily
// campaign cap is never the binding constraint ahead of the rolling-hour governor.
const (
	DefaultCampaignDailyLimit              = 200
	DefaultMaxInitialWords                 = 120
	DefaultMaxPayloadBytes                 = 32 << 20 // 32 MiB
	DefaultCrossChannelHours               = 24
	DefaultMaxWhatsAppWords                = 70
	DefaultDraftReviewBacklogTarget        = 100
	DefaultDelegatedFirstTouchRunwayTarget = 100
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
	// DynamicPriority uses activation_state / next_best_action_at for work queue.
	// When false (default), import may store activation fields but queue order stays legacy.
	DynamicPriorityEnabled bool
	// FeedSync continuous pull of extra-cli manifest (fail-closed default off).
	FeedSyncEnabled  bool
	FeedSyncInterval time.Duration
	FeedMaxAge       time.Duration
	ManifestURL      string
	// DraftReviewBacklogTarget keeps at most this many first-touch messages in
	// NEEDS_REVIEW per organization. Approved messages leave the preparation
	// backlog and allow the next reviewed batch to be replenished.
	DraftReviewBacklogTarget int
	// GreenAutorunEnabled auto-queues GREEN messages under CAMPAIGN_POLICY_AUTHORIZATION.
	// Default false (fail-closed). Distinct from AutoSendEnabled (legacy ambiguous).
	GreenAutorunEnabled bool
	// DelegatedFirstTouchEnabled allows only CFG-FIRST-TOUCH-ROUTING-v1
	// manifests to bind a founder-authorized campaign policy to one message.
	DelegatedFirstTouchEnabled bool
	// DelegatedFirstTouchAutorunEnabled continuously evaluates that narrow path.
	DelegatedFirstTouchAutorunEnabled bool
	// DelegatedFirstTouchRunwayTarget bounds queued+reserved EMAIL work per org.
	// It fills the canonical dispatch queue only; it never invokes transport.
	DelegatedFirstTouchRunwayTarget int
	// RateMode: "fixed" | "adaptive". Adaptive starts at RateStartPerHour and may climb to RateMaxPerHour.
	RateMode         string
	RateStartPerHour int
	RateMaxPerHour   int
	// AllowEnrollMint enables HUMAN_CONFIRMED mint outside production only.
	AllowEnrollMint bool
	// AppEnv mirrors APP_ENV for production guards.
	AppEnv string
	// OperatorMode removes interactive sign-in only for the loopback-only
	// CONFENGE deployment. API calls still use a normal org-scoped JWT session.
	OperatorMode   bool
	OperatorUserID uuid.UUID
	OperatorOrgID  uuid.UUID
	// InboundWebhookSecret authenticates POST /api/v1/webhooks/confenge/inbound.
	InboundWebhookSecret string
	InboundOrgID         uuid.UUID
	// OperatorAlertEmail is the single allowlisted internal recipient.
	// Default-off. Never derived from the lead. Isolated from campaign SMTP.
	OperatorAlertEmail           string
	OperatorAlertEmailEnabled    bool
	OperatorAlertEmailKillSwitch bool
	RepositorySHA                string
	FeedSchemaVersion            string
	EvidenceVersion              string
}

// SendWindowHours returns whole hours in [start, end) for HH:MM window strings.
// Returns 0 when parse fails or the window is empty/inverted.
func SendWindowHours(start, end string) int {
	sh, sm, ok1 := parseHHMM(start)
	eh, em, ok2 := parseHHMM(end)
	if !ok1 || !ok2 {
		return 0
	}
	startMin := sh*60 + sm
	endMin := eh*60 + em
	if endMin <= startMin {
		return 0
	}
	// Floor to whole hours of capacity (partial hour still counts as available
	// pacing slots under the hourly governor, but we keep a conservative int).
	return (endMin - startMin) / 60
}

func parseHHMM(s string) (h, m int, ok bool) {
	s = strings.TrimSpace(s)
	if len(s) < 4 {
		return 0, 0, false
	}
	// Accept H:MM or HH:MM
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	hh, err1 := strconv.Atoi(parts[0])
	mm, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, 0, false
	}
	return hh, mm, true
}

// LoadConfig reads CONFENGE_* env vars. Safe defaults keep the feature off.
func LoadConfig() Config {
	cfg := Config{
		Enabled:                           envBool(EnvEnabled, false),
		AutoSendEnabled:                   envBool(EnvAutoSend, false),
		FeedURL:                           strings.TrimSpace(os.Getenv(EnvFeedURL)),
		FeedToken:                         strings.TrimSpace(os.Getenv(EnvFeedToken)),
		AllowedHosts:                      splitHosts(os.Getenv(EnvAllowedHosts)),
		OutcomeWebhookURL:                 strings.TrimSpace(os.Getenv(EnvOutcomeWebhookURL)),
		OutcomeWebhookSecret:              strings.TrimSpace(os.Getenv(EnvOutcomeWebhookSec)),
		DefaultDailyLimit:                 envInt(EnvDefaultDailyLimit, DefaultCampaignDailyLimit),
		MaxInitialEmailWords:              envInt(EnvMaxInitialWords, DefaultMaxInitialWords),
		RequireHumanApproval:              envBool(EnvRequireHuman, true),
		MaxFeedPayloadBytes:               int64(envInt(EnvMaxPayloadBytes, DefaultMaxPayloadBytes)),
		WhatsAppEnabled:                   envBool(EnvWhatsAppEnabled, false),
		CrossChannelHours:                 envInt(EnvCrossChannelHours, DefaultCrossChannelHours),
		MaxWhatsAppWords:                  envInt(EnvWhatsAppMaxWords, DefaultMaxWhatsAppWords),
		SendingPaused:                     envBool(EnvSendingPaused, false),
		DynamicPriorityEnabled:            envBool(EnvDynamicPriority, false),
		FeedSyncEnabled:                   envBool(EnvFeedSyncEnabled, false),
		FeedSyncInterval:                  envDuration(EnvFeedSyncInterval, 15*time.Minute),
		FeedMaxAge:                        envDuration(EnvFeedMaxAge, 24*time.Hour),
		ManifestURL:                       strings.TrimSpace(os.Getenv(EnvManifestURL)),
		DraftReviewBacklogTarget:          envInt(EnvDraftReviewBacklogTarget, DefaultDraftReviewBacklogTarget),
		GreenAutorunEnabled:               envBool(EnvGreenAutorun, false),
		DelegatedFirstTouchEnabled:        envBool(EnvDelegatedFirstTouch, false),
		DelegatedFirstTouchAutorunEnabled: envBool(EnvDelegatedFirstTouchAutorun, false),
		DelegatedFirstTouchRunwayTarget:   envInt(EnvDelegatedFirstTouchRunwayTarget, DefaultDelegatedFirstTouchRunwayTarget),
		RateMode:                          strings.ToLower(strings.TrimSpace(os.Getenv(EnvRateMode))),
		RateStartPerHour:                  envInt(EnvRateStartPerHour, 10),
		RateMaxPerHour:                    envInt(EnvRateMaxPerHour, 20),
		AllowEnrollMint:                   envBool(EnvAllowEnrollMint, false),
		AppEnv:                            strings.TrimSpace(os.Getenv("APP_ENV")),
		OperatorMode:                      envBool(EnvOperatorMode, false),
		OperatorUserID:                    parseUUIDEnv(EnvOperatorUserID),
		OperatorOrgID:                     parseUUIDEnv(EnvOperatorOrgID),
		InboundWebhookSecret:              strings.TrimSpace(os.Getenv(EnvInboundWebhookSec)),
		InboundOrgID:                      parseUUIDEnv(EnvInboundOrgID),
		OperatorAlertEmail:                strings.TrimSpace(os.Getenv(EnvOperatorAlertEmail)),
		OperatorAlertEmailEnabled:         envBool(EnvOperatorAlertEmailEnabled, false),
		OperatorAlertEmailKillSwitch:      envBool(EnvOperatorAlertEmailKillSwitch, true),
		RepositorySHA: firstNonEmpty(
			strings.TrimSpace(os.Getenv(EnvRepositorySHA)),
			strings.TrimSpace(os.Getenv("GIT_SHA")),
			strings.TrimSpace(os.Getenv("GITHUB_SHA")),
			strings.TrimSpace(os.Getenv("WARMBLY_GIT_SHA")),
		),
		FeedSchemaVersion: firstNonEmpty(
			strings.TrimSpace(os.Getenv(EnvFeedSchemaVersion)),
			"confenge.outreach.v1",
		),
		EvidenceVersion: firstNonEmpty(
			strings.TrimSpace(os.Getenv(EnvEvidenceVersion)),
			DefaultEvidenceVersion,
		),
	}
	if cfg.InboundOrgID == uuid.Nil {
		cfg.InboundOrgID = cfg.OperatorOrgID
	}
	if cfg.RateMode == "" {
		cfg.RateMode = "adaptive"
	}
	if cfg.RateStartPerHour < 1 {
		cfg.RateStartPerHour = 10
	}
	if cfg.RateMaxPerHour < cfg.RateStartPerHour {
		cfg.RateMaxPerHour = 20
	}
	if cfg.DraftReviewBacklogTarget < 1 {
		cfg.DraftReviewBacklogTarget = DefaultDraftReviewBacklogTarget
	}
	if cfg.DelegatedFirstTouchRunwayTarget < 1 {
		cfg.DelegatedFirstTouchRunwayTarget = DefaultDelegatedFirstTouchRunwayTarget
	}

	// Fall back to chunk feed URL for manifest if only FeedURL is set.
	if cfg.ManifestURL == "" && cfg.FeedURL != "" && strings.HasSuffix(cfg.FeedURL, "manifest.json") {
		cfg.ManifestURL = cfg.FeedURL
	}
	return cfg
}

func envDuration(key string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < time.Minute {
		return def
	}
	return d
}

// ForbiddenAutomation reports env/config that must never birth a CONFENGE send
// job. Isolated env cannot reactivate auto-send, bulk/green autorun, or
// human-approval-off. Used at startup, enroll/queue, and the worker boundary.
func (c Config) ForbiddenAutomation() error {
	if c.AutoSendEnabled {
		return fmt.Errorf("%s=true is not supported; CONFENGE requires an explicit dispatch action", EnvAutoSend)
	}
	if !c.RequireHumanApproval {
		return fmt.Errorf("%s must remain true; global automatic approval is prohibited", EnvRequireHuman)
	}
	if c.GreenAutorunEnabled {
		return fmt.Errorf("%s=true is not supported; use an explicit bounded or delegated policy", EnvGreenAutorun)
	}
	return nil
}

// ValidateStartup fails closed on insecure or policy-violating combinations.
// Called when the feature is enabled. Auto-send, green autorun, and
// human-approval-off are rejected in every environment, not only operator mode.
func (c Config) ValidateStartup(appEnv string) error {
	if !c.Enabled {
		if c.OperatorMode {
			return fmt.Errorf("%s requires %s=true", EnvOperatorMode, EnvEnabled)
		}
		return nil
	}
	if err := c.ForbiddenAutomation(); err != nil {
		return err
	}
	if c.DelegatedFirstTouchAutorunEnabled && !c.DelegatedFirstTouchEnabled {
		return fmt.Errorf("%s requires %s=true", EnvDelegatedFirstTouchAutorun, EnvDelegatedFirstTouch)
	}
	if c.DelegatedFirstTouchAutorunEnabled && (c.OperatorUserID == uuid.Nil || c.OperatorOrgID == uuid.Nil) {
		return fmt.Errorf("%s requires valid operator user and organization IDs", EnvDelegatedFirstTouchAutorun)
	}
	if c.OperatorMode {
		if c.OperatorUserID == uuid.Nil {
			return fmt.Errorf("%s must be a valid non-zero UUID when %s=true", EnvOperatorUserID, EnvOperatorMode)
		}
		if c.OperatorOrgID == uuid.Nil {
			return fmt.Errorf("%s must be a valid non-zero UUID when %s=true", EnvOperatorOrgID, EnvOperatorMode)
		}
		if !isLoopbackURL(os.Getenv("APP_URL")) {
			return fmt.Errorf("APP_URL must use a loopback host when %s=true", EnvOperatorMode)
		}
	}
	prod := strings.EqualFold(appEnv, "prod") || strings.EqualFold(appEnv, "production")
	if prod {
		if strings.TrimSpace(c.RepositorySHA) == "" {
			return fmt.Errorf("%s (or GIT_SHA/GITHUB_SHA) is required when %s=true in production", EnvRepositorySHA, EnvEnabled)
		}
		if strings.TrimSpace(c.FeedSchemaVersion) == "" {
			return fmt.Errorf("%s is required when %s=true in production", EnvFeedSchemaVersion, EnvEnabled)
		}
		if strings.TrimSpace(c.EvidenceVersion) == "" {
			return fmt.Errorf("%s is required when %s=true in production", EnvEvidenceVersion, EnvEnabled)
		}
		if (c.FeedURL != "" || c.ManifestURL != "") && len(c.AllowedHosts) == 0 {
			return fmt.Errorf("%s is required when a feed URL is set in production", EnvAllowedHosts)
		}
		for key, rawURL := range map[string]string{EnvFeedURL: c.FeedURL, EnvManifestURL: c.ManifestURL} {
			if rawURL == "" {
				continue
			}
			if !strings.HasPrefix(strings.ToLower(rawURL), "https://") {
				return fmt.Errorf("%s must be https in production", key)
			}
			parsed, err := url.Parse(rawURL)
			if err != nil || parsed.Hostname() == "" || !hostAllowed(parsed.Hostname(), c.AllowedHosts) {
				return fmt.Errorf("%s host must be in %s", key, EnvAllowedHosts)
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
	if c.DefaultDailyLimit < 1 || c.DefaultDailyLimit > 200 {
		return fmt.Errorf("%s must be between 1 and 200", EnvDefaultDailyLimit)
	}
	if c.MaxInitialEmailWords < 20 || c.MaxInitialEmailWords > 500 {
		return fmt.Errorf("%s must be between 20 and 500", EnvMaxInitialWords)
	}
	return nil
}

// IsProduction reports production-like APP_ENV (mint and other fail-open paths blocked).
func (c Config) IsProduction() bool {
	e := strings.ToLower(strings.TrimSpace(c.AppEnv))
	if e == "" {
		e = strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV")))
	}
	return e == "prod" || e == "production"
}

// AllowSilentEnrollMint is true only for non-production when explicitly allowed or default dev.
func (c Config) AllowSilentEnrollMint() bool {
	if c.IsProduction() {
		return false
	}
	// Dev/test default: mint allowed unless APP_ENV is production.
	return true
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

func parseUUIDEnv(key string) uuid.UUID {
	id, err := uuid.Parse(strings.TrimSpace(os.Getenv(key)))
	if err != nil {
		return uuid.Nil
	}
	return id
}

func isLoopbackURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	host := strings.TrimSpace(strings.ToLower(u.Hostname()))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
