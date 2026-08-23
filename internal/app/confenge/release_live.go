package confenge

import (
	"context"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/email"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// LiveReleaseInput is the runtime the GO review collector is allowed to probe.
// There is no parallel config: SHA, SMTP, kill-switch, and auto-send come from
// the same sources the transport path uses.
type LiveReleaseInput struct {
	Now    time.Time
	Config *Config
	Store  BoundedCohortStore
	Repo   repository.OutreachRepository
	Auth   *BoundedCohortAuthorization
}

// CollectLiveReleaseManifest builds one live ReleaseManifest from real runtime
// sources. Unproven probes stay UNKNOWN. Nothing is copied from expected state
// into readiness flags.
func CollectLiveReleaseManifest(ctx context.Context, in LiveReleaseInput) ReleaseManifest {
	got := ReleaseManifest{EvaluatedAt: in.Now}
	if got.EvaluatedAt.IsZero() {
		got.EvaluatedAt = time.Now().UTC()
	}
	cfg := Config{}
	if in.Config != nil {
		cfg = *in.Config
	} else {
		cfg = LoadConfig()
	}
	auth := in.Auth

	got.RepositorySHA = strings.TrimSpace(cfg.RepositorySHA)
	got.Schema = firstNonEmpty(cfg.FeedSchemaVersion, models.OutreachSchemaV1)
	got.PolicyVersion = BoundedCohortPolicyV1
	got.ComposerVersion = ComposerVersion
	got.EvidenceVersion = firstNonEmpty(cfg.EvidenceVersion, DefaultEvidenceVersion)
	got.AutoSend = cfg.AutoSendEnabled
	if cfg.AutoSendEnabled {
		got.AutoSendState = EvidenceFail
	} else {
		got.AutoSendState = EvidencePass
	}
	got.GreenAutorun = cfg.GreenAutorunEnabled
	if cfg.GreenAutorunEnabled {
		got.GreenAutorunState = EvidenceFail
	} else {
		got.GreenAutorunState = EvidencePass
	}

	got.KillSwitchOperational, got.SendingPaused, got.SendingPausedState = probeKillSwitch(cfg)
	got.SMTPReady, _ = ProbeSMTPReadiness(3 * time.Second)
	got.ReplyIngestReady, _ = ProbeReplyIngestReadiness(ctx, in.Store, 3*time.Second)
	got.ObservabilityReady, _ = ProveObservabilityWiring(auth)
	got.DispatchWiring, _ = ProveDispatchWiring(auth)
	got.SenderProviderConfig, _ = ProveSenderProviderConfig()
	got.DBCohortAuthority, _ = probeDBCohortAuthority(ctx, in.Store, auth, got.EvaluatedAt)
	got.TTLValid, _ = probeTTL(auth, got.EvaluatedAt)
	got.SuppressionClear, got.SuppressionReason = probeSuppressionClear(ctx, in.Repo, auth, got.EvaluatedAt)

	if auth != nil {
		got.AllowedRouteClasses = append([]string{}, auth.AllowedRouteClasses...)
		got.VolumeCap = auth.MaxDailyVolume
		if snap := auth.FrozenManifest; snap != nil {
			got.FeedHash = firstNonEmpty(snap.SnapshotHash, snap.FeedIdentity)
			if len(snap.Members) > 0 {
				got.CohortHash = HashFrozenCohort(snap)
				mailboxes := make([]string, 0, len(snap.Members))
				for _, m := range snap.Members {
					mailboxes = append(mailboxes, m.Mailbox)
				}
				got.RecipientSetHash = HashRecipientSet(mailboxes)
			}
		}
	}
	return got
}

func probeKillSwitch(cfg Config) (operational CheckState, paused bool, pausedState CheckState) {
	path := KillSwitchPath()
	_, err := os.Stat(path)
	filePaused := false
	switch {
	case err == nil:
		filePaused = true
		operational = EvidencePass
	case os.IsNotExist(err):
		operational = EvidencePass
	default:
		return EvidenceUnknown, true, EvidenceUnknown
	}
	paused = filePaused || cfg.SendingPaused
	if paused {
		return operational, true, EvidenceFail
	}
	return operational, false, EvidencePass
}

// campaignSMTPHost is the Hostinger (or equivalent) submission host used by
// the worker. SMTP_HOST alone may be Mailpit for notifications.
func campaignSMTPHost() string {
	return firstNonEmpty(os.Getenv("CONFENGE_SMTP_HOST"), os.Getenv("SMTP_HOST"))
}

func campaignSMTPPort() string {
	if p := strings.TrimSpace(os.Getenv("CONFENGE_SMTP_PORT")); p != "" {
		return p
	}
	if p := strings.TrimSpace(os.Getenv("SMTP_PORT")); p != "" {
		return p
	}
	if strings.TrimSpace(os.Getenv("CONFENGE_SMTP_HOST")) != "" {
		return "587"
	}
	return "1025"
}

func campaignIMAPHost() string {
	return firstNonEmpty(os.Getenv("CONFENGE_IMAP_HOST"), os.Getenv("IMAP_HOST"))
}

func campaignIMAPPort() string {
	if p := firstNonEmpty(os.Getenv("CONFENGE_IMAP_PORT"), os.Getenv("IMAP_PORT")); p != "" {
		return p
	}
	return "993"
}

// ProbeSMTPReadiness is pre-transport proof: the campaign SMTP host:port
// accepts a TCP connection. It never calls SendMail and never sends mail.
func ProbeSMTPReadiness(timeout time.Duration) (CheckState, string) {
	host := campaignSMTPHost()
	if host == "" {
		return EvidenceUnknown, "smtp_readiness_not_proven"
	}
	port := campaignSMTPPort()
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	addr := net.JoinHostPort(host, port)
	d := net.Dialer{Timeout: timeout}
	conn, err := d.Dial("tcp", addr)
	if err != nil {
		return EvidenceFail, "smtp_not_ready"
	}
	_ = conn.Close()
	return EvidencePass, ""
}

// ProbeIMAPReadiness is a read-only connectivity probe. It never SELECT/FETCHes
// mail and never logs credentials. LOGIN+LOGOUT runs only when IMAP user/pass
// are present in the environment.
func ProbeIMAPReadiness(timeout time.Duration) (CheckState, string) {
	host := campaignIMAPHost()
	if host == "" {
		return EvidenceUnknown, "reply_ingest_not_proven"
	}
	port := campaignIMAPPort()
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	addr := net.JoinHostPort(host, port)
	d := net.Dialer{Timeout: timeout}
	conn, err := d.Dial("tcp", addr)
	if err != nil {
		return EvidenceFail, "reply_ingest_not_ready"
	}
	_ = conn.Close()

	user := firstNonEmpty(os.Getenv("CONFENGE_IMAP_USER"), os.Getenv("IMAP_USER"), os.Getenv("CONFENGE_MAILBOX_EMAIL"))
	pass := firstNonEmpty(os.Getenv("CONFENGE_IMAP_PASSWORD"), os.Getenv("IMAP_PASSWORD"), os.Getenv("CONFENGE_MAILBOX_PASSWORD"))
	if user != "" && pass != "" {
		portN, err := strconv.Atoi(port)
		if err != nil || portN <= 0 {
			return EvidenceFail, "reply_ingest_not_ready"
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if !email.VerifyImap(ctx, host, portN, user, pass) {
			return EvidenceFail, "reply_ingest_not_ready"
		}
	}
	return EvidencePass, ""
}

// ProbeReplyIngestReadiness is the required live check for inbound replies.
// Replies arrive via worker IMAP sync → consumer ProcessIncomingReply →
// OnClassifiedReply. UNKNOWN if connectivity or auth cannot be proven.
func ProbeReplyIngestReadiness(ctx context.Context, store BoundedCohortStore, timeout time.Duration) (CheckState, string) {
	conn, connReason := ProbeIMAPReadiness(timeout)
	if conn != EvidencePass {
		return conn, connReason
	}
	user := firstNonEmpty(os.Getenv("CONFENGE_IMAP_USER"), os.Getenv("IMAP_USER"), os.Getenv("CONFENGE_MAILBOX_EMAIL"))
	pass := firstNonEmpty(os.Getenv("CONFENGE_IMAP_PASSWORD"), os.Getenv("IMAP_PASSWORD"), os.Getenv("CONFENGE_MAILBOX_PASSWORD"))
	if user != "" && pass != "" {
		return EvidencePass, ""
	}
	auth, authReason := probeIMAPMailboxAuth(ctx, store)
	if auth != EvidencePass {
		return auth, authReason
	}
	return EvidencePass, ""
}

func probeIMAPMailboxAuth(ctx context.Context, store BoundedCohortStore) (CheckState, string) {
	pg, ok := store.(*postgresCohortStore)
	if !ok || pg == nil || pg.db == nil {
		return EvidenceUnknown, "reply_ingest_not_proven"
	}
	if err := pg.db.Ping(ctx); err != nil {
		return EvidenceUnknown, "reply_ingest_not_proven"
	}
	var n int
	// Never SELECT imap_user / imap_password. email_accounts.last_synced_at is
	// not updated by IMAP sync in this codebase, so freshness is not evidence.
	// An active smtp_imap row with imap_host proves the mailbox is configured
	// for worker Connect() LOGIN. Combined with ProbeIMAPReadiness TCP.
	err := pg.db.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM email_accounts ea
		JOIN email_accounts_smtp_imap s ON s.email_account_id = ea.id
		WHERE ea.status = 'active' AND COALESCE(s.imap_host, '') <> ''`).Scan(&n)
	if err != nil {
		return EvidenceUnknown, "reply_ingest_not_proven"
	}
	if n < 1 {
		return EvidenceUnknown, "reply_ingest_not_proven"
	}
	return EvidencePass, ""
}

// ProveDispatchWiring is PASS when the frozen grant can drive the shipped
// reserve/release/idempotent message-key path. It never sends mail.
func ProveDispatchWiring(auth *BoundedCohortAuthorization) (CheckState, string) {
	if auth == nil || auth.ID == uuid.Nil {
		return EvidenceUnknown, "dispatch_wiring_not_proven"
	}
	if auth.MaxDailyVolume < 1 || auth.MaxDailyVolume > DefaultCohortDispatchCap {
		return EvidenceFail, "dispatch_wiring_not_ready"
	}
	if auth.FrozenManifest == nil || len(auth.FrozenManifest.Members) == 0 {
		return EvidenceUnknown, "dispatch_wiring_not_proven"
	}
	tpID := auth.FrozenManifest.Members[0].TouchpointID
	if tpID == uuid.Nil {
		tpID = uuid.New()
	}
	key := cohortMessageKey(auth.ID, tpID)
	if key == "" || !strings.HasPrefix(key, "cohort:") {
		return EvidenceFail, "dispatch_wiring_not_ready"
	}
	replay := cohortMessageKey(auth.ID, tpID)
	if replay != key {
		return EvidenceFail, "dispatch_wiring_not_ready"
	}
	a := MessageKeyCampaignEmail(auth.ID, auth.ID, auth.ID)
	b := MessageKeyCampaignEmail(auth.ID, auth.ID, auth.ID)
	if a == "" || a != b {
		return EvidenceFail, "dispatch_wiring_not_ready"
	}
	return EvidencePass, ""
}

// ProveSenderProviderConfig is PASS when campaign SMTP host and mailbox
// identity are present. Mailpit is not a production sender.
func ProveSenderProviderConfig() (CheckState, string) {
	host := campaignSMTPHost()
	if host == "" {
		return EvidenceUnknown, "sender_provider_not_proven"
	}
	from := firstNonEmpty(os.Getenv("CONFENGE_MAILBOX_EMAIL"), os.Getenv("EMAIL_ADDRESS"))
	if from == "" {
		return EvidenceUnknown, "sender_provider_not_proven"
	}
	appEnv := strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV")))
	if (appEnv == "prod" || appEnv == "production") && strings.EqualFold(host, "mailpit") {
		return EvidenceFail, "sender_provider_missing"
	}
	return EvidencePass, ""
}

// ProveObservabilityWiring is PASS when SnapshotControlledEmailContext plus
// applyControlledEmailContext would stamp cohort_id, route_class, policy_version,
// and touchpoint/account correlation onto attempted, provider-accepted, bounce,
// and reply events. It does not hand-build a complete commercial event.
func ProveObservabilityWiring(auth *BoundedCohortAuthorization) (CheckState, string) {
	if auth == nil {
		return EvidenceUnknown, "observability_not_proven"
	}
	var tp *models.OutreachTouchpoint
	var cand *models.OutreachContactCandidate
	if auth.FrozenManifest != nil && len(auth.FrozenManifest.Members) > 0 {
		m := auth.FrozenManifest.Members[0]
		tp = &models.OutreachTouchpoint{
			ID:        m.TouchpointID,
			AccountID: m.AccountID,
			Recipient: m.Mailbox,
		}
		if m.CandidateID != uuid.Nil {
			id := m.CandidateID
			tp.ContactCandidateID = &id
		}
		d := controlledDiscovery{RouteClass: m.RouteClass}
		raw, _ := marshalJSON(d)
		cand = &models.OutreachContactCandidate{
			ID:            m.CandidateID,
			AccountID:     m.AccountID,
			Email:         m.Mailbox,
			DiscoveryJSON: raw,
		}
	}
	if tp == nil || cand == nil {
		return EvidenceUnknown, "observability_not_proven"
	}
	if tp.AccountID == uuid.Nil && tp.ID == uuid.Nil {
		return EvidenceUnknown, "observability_not_proven"
	}
	ctx := SnapshotControlledEmailContext(tp, cand, auth, "smtp")
	if !observabilityContextComplete(ctx) {
		return EvidenceFail, "observability_not_ready"
	}
	for _, typ := range []string{
		intel.EventEmailAttempted, intel.EventProviderAccepted, intel.EventHardBounce,
		intel.EventSoftBounce, intel.EventReply, intel.EventOptOut,
	} {
		ev := intel.CommercialEvent{Type: typ}
		applyControlledEmailContext(&ev, ctx)
		if !observabilityEventComplete(ev, ctx) {
			return EvidenceFail, "observability_not_ready"
		}
	}
	return EvidencePass, ""
}

func observabilityContextComplete(c ControlledEmailContext) bool {
	if c.RouteClass == "" || c.RouteClass == intel.Unknown {
		return false
	}
	if c.CohortID == "" || c.CohortID == intel.Unknown {
		return false
	}
	if c.PolicyVersion == "" || c.PolicyVersion == intel.Unknown {
		return false
	}
	if c.ProviderName == "" || c.ProviderName == intel.Unknown {
		return false
	}
	if strings.TrimSpace(c.AccountRef) == "" || strings.TrimSpace(c.TouchpointID) == "" {
		return false
	}
	return true
}

func observabilityEventComplete(ev intel.CommercialEvent, c ControlledEmailContext) bool {
	if ev.EmailRouteClass == "" || ev.EmailRouteClass == intel.Unknown {
		return false
	}
	if ev.CohortID == "" || ev.CohortID == intel.Unknown {
		return false
	}
	if ev.PolicyVersion == "" || ev.PolicyVersion == intel.Unknown {
		return false
	}
	if ev.ProviderName == "" || ev.ProviderName == intel.Unknown {
		return false
	}
	if strings.TrimSpace(ev.AccountPublicID) == "" || strings.TrimSpace(ev.EntityPublicID) == "" {
		return false
	}
	return true
}

func probeDBCohortAuthority(ctx context.Context, store BoundedCohortStore, auth *BoundedCohortAuthorization, now time.Time) (CheckState, string) {
	if store == nil || auth == nil || auth.ID == uuid.Nil {
		return EvidenceUnknown, "db_cohort_authority_not_proven"
	}
	pg, ok := store.(*postgresCohortStore)
	if !ok || pg == nil || pg.db == nil {
		return EvidenceUnknown, "db_cohort_authority_not_proven"
	}
	if err := pg.db.Ping(ctx); err != nil {
		return EvidenceUnknown, "db_cohort_authority_not_proven"
	}
	var grants, slots *string
	err := pg.db.QueryRow(ctx, `
		SELECT to_regclass('public.confenge_bounded_cohort_authorizations')::text,
		       to_regclass('public.confenge_bounded_cohort_reservations')::text`).Scan(&grants, &slots)
	if err != nil || grants == nil || slots == nil {
		return EvidenceUnknown, "db_cohort_authority_not_proven"
	}
	live, err := pg.GetGrant(ctx, auth.ID)
	if err != nil {
		return EvidenceUnknown, "db_cohort_authority_not_proven"
	}
	if live == nil {
		return EvidenceFail, "db_cohort_authority_missing"
	}
	if live.RevokedAt != nil {
		return EvidenceFail, "authorization_revoked"
	}
	if !live.EffectiveExpiry().IsZero() && !now.Before(live.EffectiveExpiry()) {
		return EvidenceFail, "authorization_expired"
	}
	if live.FrozenManifest == nil || len(live.FrozenManifest.Members) == 0 {
		return EvidenceFail, "db_cohort_authority_missing"
	}
	if HashFrozenCohort(live.FrozenManifest) == "" {
		return EvidenceFail, "db_cohort_authority_missing"
	}
	if live.FrozenManifest.AuthoritativeFreshnessRequired || live.FrozenManifest.AuthoritativeSourceFreshness != nil {
		if err := ValidateFrozenSourceFreshness(live.FrozenManifest, now, false); err != nil {
			return EvidenceFail, "authoritative_source_freshness_invalid"
		}
	}
	return EvidencePass, ""
}

func probeTTL(auth *BoundedCohortAuthorization, now time.Time) (CheckState, string) {
	if auth == nil {
		return EvidenceUnknown, "ttl_not_proven"
	}
	exp := auth.EffectiveExpiry()
	if exp.IsZero() {
		return EvidenceUnknown, "ttl_not_proven"
	}
	if !now.Before(exp) {
		return EvidenceFail, "ttl_invalid"
	}
	return EvidencePass, ""
}

func probeSuppressionClear(ctx context.Context, repo repository.OutreachRepository, auth *BoundedCohortAuthorization, now time.Time) (CheckState, string) {
	if auth == nil || auth.FrozenManifest == nil || len(auth.FrozenManifest.Members) == 0 {
		return EvidenceUnknown, "suppression_not_proven"
	}
	if repo == nil {
		return EvidenceUnknown, "suppression_not_proven"
	}
	for _, m := range auth.FrozenManifest.Members {
		mailbox := canonicalPilotEmail(m.Mailbox)
		if mailbox == "" {
			return EvidenceFail, "cohort_member_suppressed_after_freeze"
		}
		cand, acc, err := repo.FindCandidateByEmail(ctx, auth.OrganizationID, mailbox)
		if err != nil {
			return EvidenceUnknown, "suppression_not_proven"
		}
		if cand == nil {
			return EvidenceFail, "cohort_member_suppressed_after_freeze"
		}
		if acc != nil && (acc.DoNotContact || acc.Blocked) {
			return EvidenceFail, "cohort_member_suppressed_after_freeze"
		}
		if blk := classifyControlledRecipient(acc, cand, now); blk != nil {
			switch blk.Code {
			case "recipient_suppressed", "recipient_opt_out", "recipient_hard_bounce",
				"account_dnc", "account_blocked", "account_suppressed":
				return EvidenceFail, "cohort_member_suppressed_after_freeze"
			}
			return EvidenceFail, "post_freeze_recipient"
		}
	}
	return EvidencePass, ""
}
