package confenge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

const (
	AuthorizationModeBoundedCohort = "BOUNDED_COHORT_AUTHORIZATION"
	BoundedCohortPolicyV1          = "bounded-cohort-policy.v1"
	DefaultEvidenceVersion         = "controlled-email-policy.v1"

	CohortSlotReserved = "reserved"
	CohortSlotSent     = "sent"
	CohortSlotReleased = "released"
)

var (
	ErrCohortStoreUnavailable = errors.New("bounded cohort store unavailable")
	ErrCohortGrantMissing     = errors.New("bounded cohort grant missing")
	ErrCohortGrantRevoked     = errors.New("authorization revoked")
	ErrCohortGrantExpired     = errors.New("authorization expired")
	ErrCohortDailyCap         = errors.New("daily cap exceeded")
	ErrCohortSlotNotHeld      = errors.New("cohort slot is not reserved")
	ErrCohortHumanActor       = errors.New("human actor required")
	ErrCohortNotConfirmed     = errors.New("bounded cohort create requires explicit confirmation")
)

// BoundedCohortAuthorization is a frozen, hashed, TTL'd human grant.
// It is not auto-send and not CAMPAIGN_POLICY.
type BoundedCohortAuthorization struct {
	ID                  uuid.UUID
	OrganizationID      uuid.UUID
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
	RevokeActor         uuid.UUID
	RevokeReason        string
	FrozenHashValue     string
	// FrozenManifest is the exact membership bound to this grant. Optional
	// on legacy grants created before membership persistence.
	FrozenManifest  *FrozenCohortSnapshot
	GOReviewVerdict string
	GOReviewActor   uuid.UUID
	GOReviewAt      *time.Time
	GOReviewReason  string
}

func (a *BoundedCohortAuthorization) FrozenHash() string {
	if a == nil {
		return ""
	}
	classes := append([]string{}, a.AllowedRouteClasses...)
	sort.Strings(classes)
	exp := a.ExpiresAt.UTC()
	if exp.IsZero() && a.TTL > 0 && !a.AuthorizedAt.IsZero() {
		exp = a.AuthorizedAt.UTC().Add(a.TTL)
	}
	ttlSec := int64(a.TTL / time.Second)
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
	b.WriteByte(0)
	b.WriteString(fmt.Sprintf("%d", exp.Unix()))
	b.WriteByte(0)
	b.WriteString(fmt.Sprintf("%d", ttlSec))
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

func (a *BoundedCohortAuthorization) EffectiveExpiry() time.Time {
	if a == nil {
		return time.Time{}
	}
	if !a.ExpiresAt.IsZero() {
		return a.ExpiresAt.UTC()
	}
	if a.TTL > 0 && !a.AuthorizedAt.IsZero() {
		return a.AuthorizedAt.UTC().Add(a.TTL)
	}
	return time.Time{}
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
	// SlotHeld is true when this message_key already occupies a reserved or
	// sent slot. Complete-time revalidation must not treat that occupancy as a
	// new send that exceeds the daily cap.
	SlotHeld            bool
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
	if !in.SlotHeld && in.SentToday >= auth.MaxDailyVolume {
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

// CohortReserveResult is the outcome of an atomic slot reservation.
type CohortReserveResult struct {
	State    string
	Already  bool
	Occupied int
}

// BoundedCohortStore is the live grant + daily-volume authority used at transport.
// Production must use PostgreSQL. Memory is tests/unit/dev only.
type BoundedCohortStore interface {
	Put(auth *BoundedCohortAuthorization)
	Get(id uuid.UUID) *BoundedCohortAuthorization
	SentToday(id uuid.UUID, day string) int
	RecordSent(id uuid.UUID, day string)
	PutGrant(ctx context.Context, auth *BoundedCohortAuthorization) error
	GetGrant(ctx context.Context, id uuid.UUID) (*BoundedCohortAuthorization, error)
	RevokeGrant(ctx context.Context, id, actor uuid.UUID, reason string, now time.Time) error
	ReserveSlot(ctx context.Context, id uuid.UUID, messageKey string, now time.Time) (CohortReserveResult, error)
	CommitSlot(ctx context.Context, id uuid.UUID, messageKey string, now time.Time) error
	ReleaseSlot(ctx context.Context, id uuid.UUID, messageKey, reason string) error
	ReleaseSlotByLease(ctx context.Context, leaseToken, reason string) error
	BindLeaseToken(ctx context.Context, id uuid.UUID, messageKey, leaseToken string) error
	HeldSlot(ctx context.Context, id uuid.UUID, messageKey string) (string, error)
	RecordGOReview(ctx context.Context, id, actor uuid.UUID, verdict, reason string, now time.Time) error
}

type memoryCohortStore struct {
	mu    sync.Mutex
	byID  map[uuid.UUID]*BoundedCohortAuthorization
	slots map[string]memorySlot // authID|messageKey
}

type memorySlot struct {
	day         string
	state       string
	leaseToken  string
	reservedAt  time.Time
	committedAt time.Time
}

func NewMemoryCohortStore() BoundedCohortStore {
	return &memoryCohortStore{
		byID:  map[uuid.UUID]*BoundedCohortAuthorization{},
		slots: map[string]memorySlot{},
	}
}

func memorySlotKey(id uuid.UUID, messageKey string) string {
	return id.String() + "|" + messageKey
}

func (m *memoryCohortStore) Put(auth *BoundedCohortAuthorization) {
	_ = m.PutGrant(context.Background(), auth)
}

func (m *memoryCohortStore) Get(id uuid.UUID) *BoundedCohortAuthorization {
	auth, err := m.GetGrant(context.Background(), id)
	if err != nil {
		return nil
	}
	return auth
}

func (m *memoryCohortStore) SentToday(id uuid.UUID, day string) int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.occupiedLocked(id, day)
}

func (m *memoryCohortStore) RecordSent(id uuid.UUID, day string) {
	if m == nil || id == uuid.Nil {
		return
	}
	now := time.Now().UTC()
	key := fmt.Sprintf("legacy-record:%s:%d", id, now.UnixNano())
	_, _ = m.ReserveSlot(context.Background(), id, key, now)
	_ = m.CommitSlot(context.Background(), id, key, now)
}

func (m *memoryCohortStore) PutGrant(_ context.Context, auth *BoundedCohortAuthorization) error {
	if m == nil {
		return ErrCohortStoreUnavailable
	}
	if auth == nil || auth.ID == uuid.Nil {
		return ErrCohortGrantMissing
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *auth
	if len(auth.AllowedRouteClasses) > 0 {
		cp.AllowedRouteClasses = append([]string{}, auth.AllowedRouteClasses...)
	}
	if cp.RevokedAt != nil {
		ts := *cp.RevokedAt
		cp.RevokedAt = &ts
	}
	cp.FrozenHashValue = cp.FrozenHash()
	m.byID[auth.ID] = &cp
	return nil
}

func (m *memoryCohortStore) GetGrant(_ context.Context, id uuid.UUID) (*BoundedCohortAuthorization, error) {
	if m == nil {
		return nil, ErrCohortStoreUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	auth := m.byID[id]
	if auth == nil {
		return nil, nil
	}
	cp := *auth
	if len(auth.AllowedRouteClasses) > 0 {
		cp.AllowedRouteClasses = append([]string{}, auth.AllowedRouteClasses...)
	}
	if auth.RevokedAt != nil {
		ts := *auth.RevokedAt
		cp.RevokedAt = &ts
	}
	return &cp, nil
}

func (m *memoryCohortStore) RevokeGrant(_ context.Context, id, actor uuid.UUID, reason string, now time.Time) error {
	if m == nil {
		return ErrCohortStoreUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	auth := m.byID[id]
	if auth == nil {
		return ErrCohortGrantMissing
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if auth.RevokedAt == nil {
		ts := now.UTC()
		auth.RevokedAt = &ts
		auth.RevokeActor = actor
		auth.RevokeReason = strings.TrimSpace(reason)
	}
	return nil
}

func (m *memoryCohortStore) occupiedLocked(id uuid.UUID, day string) int {
	n := 0
	prefix := id.String() + "|"
	for k, slot := range m.slots {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		if slot.day == day && (slot.state == CohortSlotReserved || slot.state == CohortSlotSent) {
			n++
		}
	}
	return n
}

func (m *memoryCohortStore) ReserveSlot(_ context.Context, id uuid.UUID, messageKey string, now time.Time) (CohortReserveResult, error) {
	out := CohortReserveResult{}
	if m == nil {
		return out, ErrCohortStoreUnavailable
	}
	if id == uuid.Nil || strings.TrimSpace(messageKey) == "" {
		return out, ErrCohortGrantMissing
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	day := now.Format("2006-01-02")
	m.mu.Lock()
	defer m.mu.Unlock()
	auth := m.byID[id]
	if auth == nil {
		return out, ErrCohortGrantMissing
	}
	if auth.RevokedAt != nil && !auth.RevokedAt.After(now) {
		return out, ErrCohortGrantRevoked
	}
	exp := auth.EffectiveExpiry()
	if !exp.IsZero() && !now.Before(exp) {
		return out, ErrCohortGrantExpired
	}
	k := memorySlotKey(id, messageKey)
	if existing, ok := m.slots[k]; ok {
		if existing.state == CohortSlotReserved || existing.state == CohortSlotSent {
			out.State = existing.state
			out.Already = true
			out.Occupied = m.occupiedLocked(id, day)
			return out, nil
		}
	}
	occupied := m.occupiedLocked(id, day)
	if occupied >= auth.MaxDailyVolume {
		return out, ErrCohortDailyCap
	}
	m.slots[k] = memorySlot{day: day, state: CohortSlotReserved, reservedAt: now}
	out.State = CohortSlotReserved
	out.Occupied = occupied + 1
	return out, nil
}

func (m *memoryCohortStore) CommitSlot(_ context.Context, id uuid.UUID, messageKey string, now time.Time) error {
	if m == nil {
		return ErrCohortStoreUnavailable
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := memorySlotKey(id, messageKey)
	slot, ok := m.slots[k]
	if !ok {
		return ErrCohortSlotNotHeld
	}
	if slot.state == CohortSlotSent {
		return nil
	}
	if slot.state != CohortSlotReserved {
		return ErrCohortSlotNotHeld
	}
	slot.state = CohortSlotSent
	slot.committedAt = now.UTC()
	m.slots[k] = slot
	return nil
}

func (m *memoryCohortStore) ReleaseSlot(_ context.Context, id uuid.UUID, messageKey, reason string) error {
	if m == nil {
		return ErrCohortStoreUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := memorySlotKey(id, messageKey)
	slot, ok := m.slots[k]
	if !ok {
		return nil
	}
	if slot.state == CohortSlotSent {
		return nil // never release after provider accept
	}
	if slot.state != CohortSlotReserved {
		return nil
	}
	slot.state = CohortSlotReleased
	m.slots[k] = slot
	_ = reason
	return nil
}

func (m *memoryCohortStore) ReleaseSlotByLease(_ context.Context, leaseToken, reason string) error {
	if m == nil {
		return ErrCohortStoreUnavailable
	}
	if strings.TrimSpace(leaseToken) == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, slot := range m.slots {
		if slot.leaseToken == leaseToken && slot.state == CohortSlotReserved {
			slot.state = CohortSlotReleased
			m.slots[k] = slot
		}
	}
	_ = reason
	return nil
}

func (m *memoryCohortStore) BindLeaseToken(_ context.Context, id uuid.UUID, messageKey, leaseToken string) error {
	if m == nil {
		return ErrCohortStoreUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := memorySlotKey(id, messageKey)
	slot, ok := m.slots[k]
	if !ok || slot.state != CohortSlotReserved {
		return nil
	}
	slot.leaseToken = leaseToken
	m.slots[k] = slot
	return nil
}

func (m *memoryCohortStore) HeldSlot(_ context.Context, id uuid.UUID, messageKey string) (string, error) {
	if m == nil {
		return "", ErrCohortStoreUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	slot, ok := m.slots[memorySlotKey(id, messageKey)]
	if !ok {
		return "", nil
	}
	return slot.state, nil
}

func (m *memoryCohortStore) RecordGOReview(_ context.Context, id, actor uuid.UUID, verdict, reason string, now time.Time) error {
	if m == nil {
		return ErrCohortStoreUnavailable
	}
	if actor == uuid.Nil {
		return ErrCohortHumanActor
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	auth := m.byID[id]
	if auth == nil {
		return ErrCohortGrantMissing
	}
	auth.GOReviewVerdict = strings.TrimSpace(verdict)
	auth.GOReviewActor = actor
	ts := now.UTC()
	auth.GOReviewAt = &ts
	auth.GOReviewReason = strings.TrimSpace(reason)
	return nil
}
