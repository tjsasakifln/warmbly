package confenge

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/app/whatsapp"
	"github.com/warmbly/warmbly/internal/models"
)

// CheckSeverity is pass / warn / fail.
type CheckSeverity string

const (
	CheckPass CheckSeverity = "pass"
	CheckWarn CheckSeverity = "warn"
	CheckFail CheckSeverity = "fail"
)

// Check is one preflight row.
type Check struct {
	Name     string        `json:"name"`
	Severity CheckSeverity `json:"severity"`
	Message  string        `json:"message"`
}

// PreflightReport is the full operator preflight result.
type PreflightReport struct {
	Checks   []Check `json:"checks"`
	OK       bool    `json:"ok"`
	Warnings int     `json:"warnings"`
	Fails    int     `json:"fails"`
}

// PreflightDeps optional live probes. Nil fields skip live connectivity.
type PreflightDeps struct {
	PingDB               func(ctx context.Context) error
	PingRedis            func(ctx context.Context) error
	PingNATS             func(ctx context.Context) error
	CountActiveMailboxes func(ctx context.Context, orgID uuid.UUID) (int, error)
	OrgID                uuid.UUID
}

// RunPreflight evaluates config + optional live deps.
func RunPreflight(cfg Config, deps PreflightDeps) PreflightReport {
	var checks []Check
	add := func(name string, sev CheckSeverity, msg string) {
		checks = append(checks, Check{Name: name, Severity: sev, Message: msg})
	}

	if !cfg.Enabled {
		add("feature_flags", CheckWarn, EnvEnabled+"=false (outreach API disabled)")
	} else {
		add("feature_flags", CheckPass, EnvEnabled+"=true")
	}
	if !cfg.RequireHumanApproval {
		add("human_approval", CheckFail, EnvRequireHuman+" must be true (fail-closed)")
	} else {
		add("human_approval", CheckPass, EnvRequireHuman+"=true")
	}
	if cfg.AutoSendEnabled {
		add("auto_send", CheckFail, EnvAutoSend+"=true is prohibited for CONFENGE; set false")
	} else {
		add("auto_send", CheckPass, EnvAutoSend+"=false (global auto-send off)")
	}

	// Primary governor: rolling-hour global cap (email + WhatsApp share it).
	dcfg := dispatch.LoadConfig()
	hourly := dcfg.SendsPerHour
	if hourly <= 0 {
		hourly = dispatch.DefaultSendsPerHour
	}
	windowH := SendWindowHours(dcfg.WindowStart, dcfg.WindowEnd)
	if windowH <= 0 {
		windowH = SendWindowHours(dispatch.DefaultWindowStart, dispatch.DefaultWindowEnd)
	}
	if windowH <= 0 {
		windowH = 9
	}
	hourlyDayBudget := hourly * windowH

	add("governor_hourly", CheckPass, fmt.Sprintf(
		"%s=%d (primary CONFENGE pace; email+WhatsApp share rolling hour; gap=%ds)",
		dispatch.EnvGlobalSendsPerHour, hourly, int(dcfg.MinGap.Seconds()),
	))

	if cfg.DefaultDailyLimit < 1 || cfg.DefaultDailyLimit > 100 {
		add("campaign_daily_limit", CheckFail, fmt.Sprintf("%s=%d out of range 1-100", EnvDefaultDailyLimit, cfg.DefaultDailyLimit))
	} else if cfg.DefaultDailyLimit < hourly {
		// Daily cap below one hour of governor capacity is always contradictory.
		add("campaign_daily_limit", CheckWarn, fmt.Sprintf(
			"%s=%d < hourly governor %d: effective capacity collapses to %d/day (not ~%d/hour)",
			EnvDefaultDailyLimit, cfg.DefaultDailyLimit, hourly, cfg.DefaultDailyLimit, hourly,
		))
	} else if cfg.DefaultDailyLimit < hourlyDayBudget {
		add("campaign_daily_limit", CheckWarn, fmt.Sprintf(
			"%s=%d < hourly×window (%d×%dh=%d): effective daily capacity is %d, not full governor day",
			EnvDefaultDailyLimit, cfg.DefaultDailyLimit, hourly, windowH, hourlyDayBudget, cfg.DefaultDailyLimit,
		))
	} else {
		add("campaign_daily_limit", CheckPass, fmt.Sprintf(
			"%s=%d (secondary campaign shell cap; primary pace remains %d/hour)",
			EnvDefaultDailyLimit, cfg.DefaultDailyLimit, hourly,
		))
	}

	if !cfg.SendingAllowed() {
		add("kill_switch", CheckWarn, "sending paused (env or kill-switch file active)")
	} else {
		add("kill_switch", CheckPass, "sending not paused")
	}

	ai := AIProviderName()
	if ai == "" {
		add("ai", CheckWarn, "AI_PROVIDER unset; drafts use deterministic template fallback")
	} else {
		add("ai", CheckPass, "AI_PROVIDER="+ai+" (AI never approves or sends)")
	}

	if cfg.FeedURL == "" {
		add("import_feed", CheckWarn, EnvFeedURL+" unset; pass FEED= path to make confenge-import")
	} else if strings.HasPrefix(strings.ToLower(cfg.FeedURL), "file://") || strings.HasPrefix(cfg.FeedURL, "/") {
		path := strings.TrimPrefix(cfg.FeedURL, "file://")
		path = strings.TrimPrefix(path, "file:")
		if _, err := os.Stat(path); err != nil {
			if _, err2 := os.Stat(filepath.Clean(path)); err2 != nil {
				add("import_feed", CheckWarn, "feed path not found: "+path)
			} else {
				add("import_feed", CheckPass, "feed file present: "+path)
			}
		} else {
			add("import_feed", CheckPass, "feed file present: "+path)
		}
	} else {
		add("import_feed", CheckPass, "feed URL configured (host allowlist enforced in prod)")
	}

	if cfg.OutcomeWebhookURL == "" && cfg.OutcomeWebhookSecret == "" {
		add("outcome_loop", CheckWarn, "outcome webhook unset (offline demo OK)")
	} else if cfg.OutcomeWebhookURL != "" && cfg.OutcomeWebhookSecret == "" {
		add("outcome_loop", CheckFail, EnvOutcomeWebhookSec+" required when URL is set")
	} else if cfg.OutcomeWebhookURL != "" && cfg.OutcomeWebhookSecret != "" {
		add("outcome_loop", CheckPass, "outcome webhook configured (secret="+RedactSecret(cfg.OutcomeWebhookSecret)+")")
	}

	wa := whatsapp.LoadConfig()
	if !cfg.WhatsAppEnabled && !wa.Enabled {
		add("whatsapp", CheckWarn, "WhatsApp disabled (email path unaffected)")
	} else {
		if wa.AllowBaileys {
			add("whatsapp_baileys", CheckFail, "Baileys/Web allow flag is set; lab only, refuse production")
		}
		if wa.Provider == whatsapp.ProviderMock {
			add("whatsapp", CheckPass, "WhatsApp mock provider (offline)")
		} else if wa.EvolutionAPIKey == "" || wa.EvolutionBaseURL == "" {
			add("whatsapp", CheckWarn, "WhatsApp enabled but credentials incomplete; email still works")
		} else {
			add("whatsapp", CheckPass, "WhatsApp provider="+wa.Provider+" (consent required for public phones)")
		}
		add("whatsapp_consent", CheckPass, "public phones without opt-in stay blocked for API outbound")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if deps.PingDB != nil {
		if err := deps.PingDB(ctx); err != nil {
			add("postgres", CheckFail, "database unreachable: "+err.Error())
		} else {
			add("postgres", CheckPass, "database accepting connections")
		}
	} else {
		add("postgres", CheckWarn, "skipped (no probe wired)")
	}
	if deps.PingRedis != nil {
		if err := deps.PingRedis(ctx); err != nil {
			add("redis", CheckFail, "redis unreachable: "+err.Error())
		} else {
			add("redis", CheckPass, "redis PING ok")
		}
	} else {
		add("redis", CheckWarn, "skipped (no probe wired)")
	}
	if deps.PingNATS != nil {
		if err := deps.PingNATS(ctx); err != nil {
			add("nats", CheckFail, "nats unreachable: "+err.Error())
		} else {
			add("nats", CheckPass, "nats dial ok")
		}
	} else {
		add("nats", CheckWarn, "skipped (no probe wired)")
	}

	if deps.CountActiveMailboxes != nil && deps.OrgID != uuid.Nil {
		n, err := deps.CountActiveMailboxes(ctx, deps.OrgID)
		if err != nil {
			add("mailbox", CheckWarn, "could not count mailboxes: "+err.Error())
		} else if n < 1 {
			add("mailbox", CheckWarn, "no active mailboxes; connect SMTP/IMAP or M365 OAuth in dashboard")
		} else {
			add("mailbox", CheckPass, fmt.Sprintf("%d active mailbox(es) with send capability", n))
		}
	} else {
		add("mailbox", CheckWarn, "skipped (no org/probe); after seed, confenge-local has Mailpit SMTP mailboxes")
	}

	apiHost := strings.TrimSpace(os.Getenv("API_HOST"))
	if apiHost == "" || strings.HasPrefix(apiHost, "127.0.0.1") || strings.HasPrefix(apiHost, "localhost") {
		add("bind", CheckPass, "API bind local-safe (default 127.0.0.1 / localhost)")
	} else if strings.HasPrefix(apiHost, "0.0.0.0") {
		add("bind", CheckWarn, "API_HOST="+apiHost+" exposes all interfaces; prefer 127.0.0.1 for local ops")
	} else {
		add("bind", CheckPass, "API_HOST="+apiHost)
	}

	rep := PreflightReport{Checks: checks, OK: true}
	for _, c := range checks {
		switch c.Severity {
		case CheckFail:
			rep.Fails++
			rep.OK = false
		case CheckWarn:
			rep.Warnings++
		}
	}
	return rep
}

// FormatPreflight renders a human-readable report for CLI/Make.
func FormatPreflight(r PreflightReport) string {
	var b strings.Builder
	b.WriteString("CONFENGE preflight\n")
	b.WriteString(strings.Repeat("-", 48) + "\n")
	for _, c := range r.Checks {
		mark := ".."
		switch c.Severity {
		case CheckPass:
			mark = "ok"
		case CheckWarn:
			mark = "!!"
		case CheckFail:
			mark = "XX"
		}
		fmt.Fprintf(&b, "  [%s] %-18s %s\n", mark, c.Name, c.Message)
	}
	b.WriteString(strings.Repeat("-", 48) + "\n")
	if r.OK {
		fmt.Fprintf(&b, "OK (%d warnings)\n", r.Warnings)
	} else {
		fmt.Fprintf(&b, "FAILED: %d hard failure(s), %d warning(s)\n", r.Fails, r.Warnings)
	}
	return b.String()
}

// FormatImportSummary prints creates/updates/blocked counts from an import run.
func FormatImportSummary(run *models.OutreachImportRun) string {
	if run == nil {
		return "no import run"
	}
	c := run.Counts
	var b strings.Builder
	mode := "apply"
	if run.DryRun {
		mode = "dry-run"
	}
	fmt.Fprintf(&b, "CONFENGE import (%s) status=%s\n", mode, run.Status)
	fmt.Fprintf(&b, "  creates=%d updates=%d unchanged=%d blocked=%d\n", c.Creates, c.Updates, c.Unchanged, c.Blocked)
	fmt.Fprintf(&b, "  missing_contact=%d invalid=%d leads_processed=%d errors=%d\n",
		c.MissingContact, c.Invalid, c.LeadsProcessed, c.LeadsSkippedError)
	if len(run.Errors) > 0 {
		fmt.Fprintf(&b, "  first errors:\n")
		n := len(run.Errors)
		if n > 5 {
			n = 5
		}
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "    - %s\n", run.Errors[i].Message)
		}
	}
	return b.String()
}

// DefaultPreflightDeps builds live probes from standard env vars.
func DefaultPreflightDeps(orgID uuid.UUID) PreflightDeps {
	deps := PreflightDeps{OrgID: orgID}
	dsn := strings.TrimSpace(os.Getenv("PRIMARY_DB"))
	if dsn == "" {
		dsn = "postgres://warmbly:warmbly@localhost:15432/warmbly_dev?sslmode=disable"
	}
	deps.PingDB = func(ctx context.Context) error {
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			return err
		}
		defer pool.Close()
		return pool.Ping(ctx)
	}
	deps.CountActiveMailboxes = func(ctx context.Context, oid uuid.UUID) (int, error) {
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			return 0, err
		}
		defer pool.Close()
		var n int
		err = pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM email_accounts
			WHERE organization_id = $1 AND status = 'active'
		`, oid).Scan(&n)
		return n, err
	}

	redisURL := strings.TrimSpace(os.Getenv("REDIS"))
	if redisURL == "" {
		redisURL = "redis://localhost:16379"
	}
	deps.PingRedis = func(ctx context.Context) error {
		opt, err := redis.ParseURL(redisURL)
		if err != nil {
			return err
		}
		c := redis.NewClient(opt)
		defer c.Close()
		return c.Ping(ctx).Err()
	}

	natsURL := strings.TrimSpace(os.Getenv("NATS_URL"))
	if natsURL == "" {
		natsURL = "nats://localhost:4222"
	}
	deps.PingNATS = func(ctx context.Context) error {
		u, err := url.Parse(natsURL)
		if err != nil {
			return err
		}
		host := u.Host
		if host == "" {
			host = strings.TrimPrefix(natsURL, "nats://")
		}
		d := net.Dialer{}
		conn, err := d.DialContext(ctx, "tcp", host)
		if err != nil {
			return err
		}
		return conn.Close()
	}
	return deps
}
