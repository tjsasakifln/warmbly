package confenge

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/models"
)

func TestPermanentSuppressOnlyHardBlock(t *testing.T) {
	cases := []struct {
		kind GateKind
		want bool
	}{
		{GateProceed, false},
		{GateDeferred, false},
		{GateAlready, false},
		{GateHardBlock, true},
		{GateTransient, false},
		{GateBypass, false},
	}
	for _, tc := range cases {
		r := CampaignGateResult{Kind: tc.kind}
		if got := r.PermanentSuppress(); got != tc.want {
			t.Fatalf("kind=%d PermanentSuppress=%v want %v", tc.kind, got, tc.want)
		}
	}
}

func TestGateCampaignEmailHardBlockDNC(t *testing.T) {
	org := uuid.New()
	accID := uuid.New()
	candID := uuid.New()
	repo := newMemRepoWithSettings()
	acc := &models.OutreachAccount{
		ID: accID, OrganizationID: org, CNPJ14: "12345678000199",
		DoNotContact: false, Blocked: false,
	}
	_, _ = repo.UpsertAccount(context.Background(), acc)
	cand := &models.OutreachContactCandidate{
		ID: candID, OrganizationID: org, AccountID: accID,
		Email: "dnc@example.com", DoNotContact: true,
	}
	_, _ = repo.UpsertCandidate(context.Background(), cand)

	svc := &service{cfg: Config{Enabled: true}, repo: repo}
	clock := &dispatch.FixedClock{T: time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)}
	cfg := dispatch.DefaultConfig()
	cfg.WindowStart, cfg.WindowEnd, cfg.Timezone, cfg.MinGap = "00:00", "23:59", "UTC", 0
	svc.governor = dispatch.NewGovernor(cfg, dispatch.NewMemoryStore(), clock)

	r := svc.GateCampaignEmail(context.Background(), org, DefaultCampaignName, "dnc@example.com",
		uuid.New(), uuid.New(), uuid.New())
	if r.Kind != GateHardBlock || r.Reason != ReasonDNCOrBounce {
		t.Fatalf("got kind=%d reason=%s want HardBlock dnc_or_bounce", r.Kind, r.Reason)
	}
	if r.Err != nil {
		t.Fatalf("HardBlock must not set Err: %v", r.Err)
	}
	if !r.PermanentSuppress() {
		t.Fatal("HardBlock must PermanentSuppress")
	}
}

func TestGateCampaignEmailHardBlockAccountDNC(t *testing.T) {
	org := uuid.New()
	accID := uuid.New()
	repo := newMemRepoWithSettings()
	acc := &models.OutreachAccount{
		ID: accID, OrganizationID: org, CNPJ14: "12345678000188",
		DoNotContact: true, Blocked: true,
	}
	_, _ = repo.UpsertAccount(context.Background(), acc)
	cand := &models.OutreachContactCandidate{
		ID: uuid.New(), OrganizationID: org, AccountID: accID,
		Email: "ok@example.com", DoNotContact: false,
	}
	_, _ = repo.UpsertCandidate(context.Background(), cand)

	svc := &service{cfg: Config{Enabled: true}, repo: repo}
	clock := &dispatch.FixedClock{T: time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)}
	cfg := dispatch.DefaultConfig()
	cfg.WindowStart, cfg.WindowEnd, cfg.Timezone, cfg.MinGap = "00:00", "23:59", "UTC", 0
	svc.governor = dispatch.NewGovernor(cfg, dispatch.NewMemoryStore(), clock)

	r := svc.GateCampaignEmail(context.Background(), org, DefaultCampaignName, "ok@example.com",
		uuid.New(), uuid.New(), uuid.New())
	if r.Kind != GateHardBlock || r.Reason != ReasonAccountDNC {
		t.Fatalf("got kind=%d reason=%s", r.Kind, r.Reason)
	}
	if r.PermanentSuppress() != true {
		t.Fatal("account DNC must permanent suppress")
	}
}

func TestGateCampaignEmailTransientOnGovernorError(t *testing.T) {
	// errStore fails TryReserveAtomic (simulates DB blip).
	store := &errStore{inner: dispatch.NewMemoryStore(), failReserve: true}
	clock := &dispatch.FixedClock{T: time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)}
	cfg := dispatch.DefaultConfig()
	cfg.WindowStart, cfg.WindowEnd, cfg.Timezone, cfg.MinGap = "00:00", "23:59", "UTC", 0
	gov := dispatch.NewGovernor(cfg, store, clock)

	repo := newMemRepoWithSettings()
	org := uuid.New()
	accID := uuid.New()
	_, _ = repo.UpsertAccount(context.Background(), &models.OutreachAccount{
		ID: accID, OrganizationID: org, CNPJ14: "12345678000177",
	})
	_, _ = repo.UpsertCandidate(context.Background(), &models.OutreachContactCandidate{
		ID: uuid.New(), OrganizationID: org, AccountID: accID, Email: "lead@example.com",
	})

	svc := &service{cfg: Config{Enabled: true}, repo: repo, governor: gov}
	r := svc.GateCampaignEmail(context.Background(), org, DefaultCampaignName, "lead@example.com",
		uuid.New(), uuid.New(), uuid.New())
	if r.Kind != GateTransient {
		t.Fatalf("governor error must be Transient, got kind=%d reason=%s err=%v", r.Kind, r.Reason, r.Err)
	}
	if r.PermanentSuppress() {
		t.Fatal("Transient must never PermanentSuppress")
	}
	if r.Err == nil {
		t.Fatal("Transient should carry Err")
	}
}

func TestGateCampaignEmailDeferredCap(t *testing.T) {
	store := dispatch.NewMemoryStore()
	clock := &dispatch.FixedClock{T: time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)}
	cfg := dispatch.DefaultConfig()
	cfg.WindowStart, cfg.WindowEnd, cfg.Timezone, cfg.MinGap = "00:00", "23:59", "UTC", 0
	cfg.SendsPerHour = 1
	gov := dispatch.NewGovernor(cfg, store, clock)

	repo := newMemRepoWithSettings()
	org := uuid.New()
	accID := uuid.New()
	_, _ = repo.UpsertAccount(context.Background(), &models.OutreachAccount{
		ID: accID, OrganizationID: org, CNPJ14: "12345678000166",
	})
	_, _ = repo.UpsertCandidate(context.Background(), &models.OutreachContactCandidate{
		ID: uuid.New(), OrganizationID: org, AccountID: accID, Email: "lead@example.com",
	})
	svc := &service{cfg: Config{Enabled: true}, repo: repo, governor: gov}

	// First send consumes the only slot.
	r1 := svc.GateCampaignEmail(context.Background(), org, DefaultCampaignName, "lead@example.com",
		uuid.New(), uuid.New(), uuid.New())
	if r1.Kind != GateProceed {
		t.Fatalf("first: kind=%d", r1.Kind)
	}
	_ = svc.CommitCampaignEmail(context.Background(), r1.ReservationID)

	// Second is deferred (cap), not hard-block.
	r2 := svc.GateCampaignEmail(context.Background(), org, DefaultCampaignName, "lead@example.com",
		uuid.New(), uuid.New(), uuid.New())
	if r2.Kind != GateDeferred {
		t.Fatalf("second: kind=%d reason=%s want Deferred", r2.Kind, r2.Reason)
	}
	if r2.PermanentSuppress() {
		t.Fatal("Deferred must not permanent suppress")
	}
}

func TestGateCampaignEmailBypassNonConfenge(t *testing.T) {
	svc := &service{cfg: Config{Enabled: true}}
	r := svc.GateCampaignEmail(context.Background(), uuid.New(), "Regular campaign", "a@b.com",
		uuid.New(), uuid.New(), uuid.New())
	if r.Kind != GateBypass {
		t.Fatalf("kind=%d", r.Kind)
	}
	if r.PermanentSuppress() {
		t.Fatal("Bypass must not suppress")
	}
}

// errStore wraps MemoryStore and can fail TryReserveAtomic.
type errStore struct {
	inner       *dispatch.MemoryStore
	failReserve bool
}

func (e *errStore) GetControl(ctx context.Context) (dispatch.ControlState, error) {
	return e.inner.GetControl(ctx)
}
func (e *errStore) SetPaused(ctx context.Context, paused bool, reason string, by *uuid.UUID) error {
	return e.inner.SetPaused(ctx, paused, reason, by)
}
func (e *errStore) TryReserveAtomic(ctx context.Context, in dispatch.AtomicReserveInput) (dispatch.AtomicReserveOutput, error) {
	if e.failReserve {
		return dispatch.AtomicReserveOutput{}, errors.New("simulated store failure")
	}
	return e.inner.TryReserveAtomic(ctx, in)
}
func (e *errStore) GetReservationByKey(ctx context.Context, messageKey string) (*dispatch.Reservation, error) {
	return e.inner.GetReservationByKey(ctx, messageKey)
}
func (e *errStore) GetSendByKey(ctx context.Context, messageKey string) (time.Time, bool, error) {
	return e.inner.GetSendByKey(ctx, messageKey)
}
func (e *errStore) RefreshReservation(ctx context.Context, id uuid.UUID, leaseUntil time.Time, workerToken string) error {
	return e.inner.RefreshReservation(ctx, id, leaseUntil, workerToken)
}
func (e *errStore) CommitReservation(ctx context.Context, id uuid.UUID, sentAt time.Time) error {
	return e.inner.CommitReservation(ctx, id, sentAt)
}
func (e *errStore) ReleaseReservation(ctx context.Context, id uuid.UUID, state, errText string) error {
	return e.inner.ReleaseReservation(ctx, id, state, errText)
}
func (e *errStore) ExpireStaleReservations(ctx context.Context, now time.Time) (int, error) {
	return e.inner.ExpireStaleReservations(ctx, now)
}
func (e *errStore) ListOccupied(ctx context.Context, now time.Time, window time.Duration) ([]time.Time, time.Time, error) {
	return e.inner.ListOccupied(ctx, now, window)
}
func (e *errStore) Enqueue(ctx context.Context, item *dispatch.QueueItem) error {
	return e.inner.Enqueue(ctx, item)
}
func (e *errStore) CancelQueue(ctx context.Context, messageKey, reason string) error {
	return e.inner.CancelQueue(ctx, messageKey, reason)
}
func (e *errStore) CancelQueueByRecipient(ctx context.Context, orgID uuid.UUID, recipientRef, reason string) (int, error) {
	return e.inner.CancelQueueByRecipient(ctx, orgID, recipientRef, reason)
}
func (e *errStore) CountQueued(ctx context.Context, orgID *uuid.UUID) (int, error) {
	return e.inner.CountQueued(ctx, orgID)
}
func (e *errStore) ClaimNextQueued(ctx context.Context, now time.Time) (*dispatch.QueueItem, error) {
	return e.inner.ClaimNextQueued(ctx, now)
}
func (e *errStore) UpdateQueueStatus(ctx context.Context, id uuid.UUID, status, errText string) error {
	return e.inner.UpdateQueueStatus(ctx, id, status, errText)
}
func (e *errStore) RecordFailure(ctx context.Context, f dispatch.FailureRecord) error {
	return e.inner.RecordFailure(ctx, f)
}
func (e *errStore) ListRecentFailures(ctx context.Context, limit int) ([]dispatch.FailureRecord, error) {
	return e.inner.ListRecentFailures(ctx, limit)
}
func (e *errStore) CountActiveLeases(ctx context.Context, now time.Time) (int, error) {
	return e.inner.CountActiveLeases(ctx, now)
}
func (e *errStore) CountSendsSince(ctx context.Context, since time.Time) (int, error) {
	return e.inner.CountSendsSince(ctx, since)
}

func TestContextStaleIsDeferredNotPermanentSuppress(t *testing.T) {
	// GateDeferred must never permanent-suppress (campaign_task only suppresses HardBlock).
	r := CampaignGateResult{Kind: GateDeferred, Reason: "context_stale"}
	if r.PermanentSuppress() {
		t.Fatal("context_stale deferred must not PermanentSuppress")
	}
	// GateHardBlock remains the only permanent suppress kind.
	hard := CampaignGateResult{Kind: GateHardBlock, Reason: ReasonDNCOrBounce}
	if !hard.PermanentSuppress() {
		t.Fatal("HardBlock must PermanentSuppress")
	}
	// Sanity: stale reason is not DNC/bounce.
	if r.Reason == ReasonDNCOrBounce || r.Reason == ReasonAccountDNC {
		t.Fatal("stale reason must not masquerade as DNC")
	}
}
