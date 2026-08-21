package confenge

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

const (
	AuthorizationModeBoundedCohort = "BOUNDED_COHORT_AUTHORIZATION"
	BoundedCohortPolicyV1          = "bounded-cohort-policy.v1"
)

// BoundedCohortAuthorization is a frozen, hashed, TTL'd human grant.
// It is not auto-send and not CAMPAIGN_POLICY.
type BoundedCohortAuthorization struct {
	ID                  uuid.UUID
	ActorID             uuid.UUID
	AuthorizedAt        time.Time
	RepositorySHA       string
	FeedSchemaVersion   string
	CohortID            string
	CohortHash          string
	PolicyVersion       string
	AllowedRouteClasses []string
	MaxDailyVolume      int
	RecipientSetHash    string
	ComposerVersion     string
	EvidenceVersion     string
	TTL                 time.Duration
	ExpiresAt           time.Time
	KillSwitchEngaged   bool
	AutoSendEnabled     bool
	GreenAutorunEnabled bool
	RevokedAt           *time.Time
}

func (a *BoundedCohortAuthorization) FrozenHash() string {
	if a == nil {
		return ""
	}
	classes := append([]string{}, a.AllowedRouteClasses...)
	sort.Strings(classes)
	var b strings.Builder
	b.WriteString(a.ActorID.String())
	b.WriteByte(0)
	b.WriteString(strings.TrimSpace(a.RepositorySHA))
	b.WriteByte(0)
	b.WriteString(strings.TrimSpace(a.FeedSchemaVersion))
	b.WriteByte(0)
	b.WriteString(strings.TrimSpace(a.CohortID))
	b.WriteByte(0)
	b.WriteString(strings.TrimSpace(a.CohortHash))
	b.WriteByte(0)
	b.WriteString(strings.TrimSpace(a.PolicyVersion))
	b.WriteByte(0)
	b.WriteString(strings.Join(classes, ","))
	b.WriteByte(0)
	b.WriteString(fmt.Sprintf("%d", a.MaxDailyVolume))
	b.WriteByte(0)
	b.WriteString(strings.TrimSpace(a.RecipientSetHash))
	b.WriteByte(0)
	b.WriteString(strings.TrimSpace(a.ComposerVersion))
	b.WriteByte(0)
	b.WriteString(strings.TrimSpace(a.EvidenceVersion))
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

func HashRecipientSet(mailboxes []string) string {
	norm := make([]string, 0, len(mailboxes))
	for _, m := range mailboxes {
		m = canonicalPilotEmail(m)
		if m != "" {
			norm = append(norm, m)
		}
	}
	sort.Strings(norm)
	sum := sha256.Sum256([]byte(strings.Join(norm, "\n")))
	return hex.EncodeToString(sum[:])
}

func HashCohortID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}

type CohortTransportInput struct {
	Now                 time.Time
	RepositorySHA       string
	FeedSchemaVersion   string
	CohortHash          string
	PolicyVersion       string
	ComposerVersion     string
	EvidenceVersion     string
	RecipientSetHash    string
	RecipientMailbox    string
	RouteClass          string
	Suppressed          bool
	OptOut              bool
	PostFreezeRecipient bool
	SentToday           int
	KillSwitchEngaged   bool
	AutoSendEnabled     bool
	GreenAutorunEnabled bool
}

func ValidateBoundedCohortAuthorization(auth *BoundedCohortAuthorization, in CohortTransportInput) []string {
	var reasons []string
	add := func(code string) { reasons = append(reasons, code) }
	if auth == nil {
		add("cohort_authorization_missing")
		return reasons
	}
	if auth.ActorID == uuid.Nil {
		add("human_actor_required")
	}
	if auth.AutoSendEnabled || in.AutoSendEnabled {
		add("auto_send_forbidden")
	}
	if auth.GreenAutorunEnabled || in.GreenAutorunEnabled {
		add("green_autorun_forbidden")
	}
	if auth.KillSwitchEngaged || in.KillSwitchEngaged || FileKillSwitchActive() {
		add("kill_switch_engaged")
	}
	if auth.RevokedAt != nil && !auth.RevokedAt.After(in.Now) {
		add("authorization_revoked")
	}
	exp := auth.ExpiresAt
	if exp.IsZero() && auth.TTL > 0 && !auth.AuthorizedAt.IsZero() {
		exp = auth.AuthorizedAt.Add(auth.TTL)
	}
	if !exp.IsZero() && (in.Now.IsZero() || !in.Now.Before(exp)) {
		add("authorization_expired")
	}
	if auth.MaxDailyVolume < 1 {
		add("daily_cap_missing")
	}
	if in.SentToday >= auth.MaxDailyVolume {
		add("daily_cap_exceeded")
	}
	bound := func(grant, live, code string) {
		if strings.TrimSpace(grant) == "" {
			return
		}
		if strings.TrimSpace(live) == "" || live != grant {
			add(code)
		}
	}
	bound(auth.RepositorySHA, in.RepositorySHA, "sha_drift")
	bound(auth.FeedSchemaVersion, in.FeedSchemaVersion, "feed_schema_drift")
	bound(auth.CohortHash, in.CohortHash, "cohort_hash_drift")
	bound(auth.PolicyVersion, in.PolicyVersion, "policy_drift")
	bound(auth.ComposerVersion, in.ComposerVersion, "copy_drift")
	bound(auth.EvidenceVersion, in.EvidenceVersion, "evidence_drift")
	bound(auth.RecipientSetHash, in.RecipientSetHash, "recipient_drift")
	if in.PostFreezeRecipient {
		add("post_freeze_recipient")
	}
	if in.Suppressed {
		add("suppressed_mailbox")
	}
	if in.OptOut {
		add("opt_out")
	}
	class := strings.ToUpper(strings.TrimSpace(in.RouteClass))
	if class == RouteClassProbabilisticOrRisky {
		add("risky_outside_policy")
	}
	allowed := map[string]bool{}
	for _, c := range auth.AllowedRouteClasses {
		allowed[strings.ToUpper(strings.TrimSpace(c))] = true
	}
	if class != "" && !allowed[class] {
		add("route_class_outside_policy")
	}
	if auth.FrozenHash() == "" {
		add("authorization_hash_missing")
	}
	return reasons
}

func ApplyBoundedCohortAuthorization(tp *models.OutreachTouchpoint, auth *BoundedCohortAuthorization, now time.Time) error {
	if tp == nil {
		return fmt.Errorf("nil touchpoint")
	}
	if auth == nil || auth.ID == uuid.Nil {
		return fmt.Errorf("bounded cohort authorization id required")
	}
	if auth.ActorID == uuid.Nil {
		return fmt.Errorf("bounded cohort requires a human actor")
	}
	if auth.AutoSendEnabled {
		return fmt.Errorf("bounded cohort authorization is not auto-send")
	}
	if tp.ContentHash == "" {
		RecomputeContentHash(tp)
	}
	if tp.ContentHash == "" || tp.BodyText == "" {
		return fmt.Errorf("cannot cohort-authorize empty content")
	}
	if tp.Recipient == "" {
		return fmt.Errorf("cannot cohort-authorize without exact recipient")
	}
	switch tp.State {
	case models.TouchpointDrafted, models.TouchpointNeedsReview, models.TouchpointApproved:
	default:
		return fmt.Errorf("cannot cohort-authorize from state %s", tp.State)
	}
	tp.ApprovedBy = &auth.ActorID
	tp.ApprovedAt = &now
	tp.ApprovedContentHash = tp.ContentHash
	tp.AuthorizationMode = AuthorizationModeBoundedCohort
	id := auth.ID
	tp.CampaignPolicyAuthorizationID = &id
	tp.AuthorizationPolicyHash = auth.FrozenHash()
	at := now
	tp.AuthorizationAt = &at
	tp.State = models.TouchpointApproved
	return nil
}

func EnforceDailyCap(sentToday, maxDaily int) error {
	if maxDaily < 1 {
		return fmt.Errorf("daily cap missing")
	}
	if sentToday >= maxDaily {
		return fmt.Errorf("daily cap exceeded: %d/%d", sentToday, maxDaily)
	}
	return nil
}

// BoundedCohortStore is the live grant + daily-volume authority used at transport.
type BoundedCohortStore interface {
	Put(auth *BoundedCohortAuthorization)
	Get(id uuid.UUID) *BoundedCohortAuthorization
	SentToday(id uuid.UUID, day string) int
	RecordSent(id uuid.UUID, day string)
}

type memoryCohortStore struct {
	byID map[uuid.UUID]*BoundedCohortAuthorization
	sent map[string]int
}

func NewMemoryCohortStore() BoundedCohortStore {
	return &memoryCohortStore{byID: map[uuid.UUID]*BoundedCohortAuthorization{}, sent: map[string]int{}}
}

func (m *memoryCohortStore) Put(auth *BoundedCohortAuthorization) {
	if m == nil || auth == nil || auth.ID == uuid.Nil {
		return
	}
	cp := *auth
	m.byID[auth.ID] = &cp
}

func (m *memoryCohortStore) Get(id uuid.UUID) *BoundedCohortAuthorization {
	if m == nil {
		return nil
	}
	return m.byID[id]
}

func (m *memoryCohortStore) SentToday(id uuid.UUID, day string) int {
	if m == nil {
		return 0
	}
	return m.sent[id.String()+"|"+day]
}

func (m *memoryCohortStore) RecordSent(id uuid.UUID, day string) {
	if m == nil {
		return
	}
	m.sent[id.String()+"|"+day]++
}
