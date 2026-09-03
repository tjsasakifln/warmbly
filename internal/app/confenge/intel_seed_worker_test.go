package confenge

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/config"
	"github.com/warmbly/warmbly/internal/models"
)

// INTEL_SEED cap and non-interference proofs.
//
// E: the lane has its own daily counter, enforced on top of the shared
//    admission gates rather than carved out of them.
// H: first-touch admission and queue drain are byte-identical with the lane
//    unset, and with it enabled and its dedicated cap fully exhausted.

// fakeSeedLedger is an in-memory IntelSeedLedger.
type fakeSeedLedger struct {
	sends []models.IntelSeedSend
}

func (f *fakeSeedLedger) CountSendsSince(_ context.Context, orgID uuid.UUID, since time.Time) (int, error) {
	n := 0
	for _, s := range f.sends {
		if s.OrganizationID == orgID && !s.SentAt.Before(since) {
			n++
		}
	}
	return n, nil
}

func (f *fakeSeedLedger) RecordSend(_ context.Context, send models.IntelSeedSend) (bool, error) {
	for _, s := range f.sends {
		if s.OrganizationID == send.OrganizationID && s.MessageKey == send.MessageKey {
			return false, nil
		}
	}
	f.sends = append(f.sends, send)
	return true, nil
}

func (f *fakeSeedLedger) AlreadySent(_ context.Context, orgID uuid.UUID, messageKey string) (bool, error) {
	for _, s := range f.sends {
		if s.OrganizationID == orgID && s.MessageKey == messageKey {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeSeedLedger) SeededCandidateIDs(_ context.Context, orgID uuid.UUID, since time.Time) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	for _, s := range f.sends {
		if s.OrganizationID == orgID && !s.SentAt.Before(since) && s.CandidateID != uuid.Nil {
			out[s.CandidateID] = true
		}
	}
	return out, nil
}

// The cap defaults closed. Neither an unset value nor a malformed one may fall
// back to a first-touch number budgeted for a different lane.
func TestIntelSeedDailyCapFailsClosed(t *testing.T) {
	for _, raw := range []string{"", "   ", "not-a-number", "-5"} {
		t.Setenv(EnvIntelSeedDailyCap, raw)
		if got := IntelSeedDailyCap(); got != 0 {
			t.Fatalf("cap %q = %d, want 0", raw, got)
		}
	}
	t.Setenv(EnvIntelSeedDailyCap, "3")
	if got := IntelSeedDailyCap(); got != 3 {
		t.Fatalf("cap = %d, want 3", got)
	}
}

// Proof E. Headroom is the lane's OWN counter: it starts at the configured cap
// and reaches zero after that many seed sends, with no reference to first
// touch's budget.
func TestIntelSeedHeadroomIsItsOwnCounter(t *testing.T) {
	t.Setenv(EnvIntelSeedEnabled, "true")
	t.Setenv(EnvIntelSeedDailyCap, "2")
	ctx := context.Background()
	svc, orgID := newWebIntentService(t)
	ledger := &fakeSeedLedger{}
	svc.WireIntelSeed(ledger)

	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if got := svc.IntelSeedHeadroom(ctx, orgID, now); got != 2 {
		t.Fatalf("fresh headroom = %d, want 2", got)
	}
	for i := 0; i < 2; i++ {
		if _, err := ledger.RecordSend(ctx, models.IntelSeedSend{
			OrganizationID: orgID, MessageKey: fmt.Sprintf("email:intel_seed:contact:%d", i),
			Recipient: "x@example.com", SentAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if got := svc.IntelSeedHeadroom(ctx, orgID, now); got != 0 {
		t.Fatalf("exhausted headroom = %d, want 0", got)
	}
	// The counter is daily. Yesterday's sends do not spend today's cap.
	if got := svc.IntelSeedHeadroom(ctx, orgID, now.Add(48*time.Hour)); got != 2 {
		t.Fatalf("headroom on a later day = %d, want 2", got)
	}
}

// A dormant lane sends nothing regardless of the cap, and a capped-at-zero lane
// sends nothing regardless of the opt-in. Both gates must pass.
func TestIntelSeedStaysDormantWithoutBothOptIns(t *testing.T) {
	ctx := context.Background()
	svc, orgID := newWebIntentService(t)
	svc.WireIntelSeed(&fakeSeedLedger{})
	now := time.Now().UTC()

	t.Setenv(EnvIntelSeedEnabled, "")
	t.Setenv(EnvIntelSeedDailyCap, "10")
	if got := svc.IntelSeedHeadroom(ctx, orgID, now); got != 0 {
		t.Fatalf("headroom without the opt-in = %d, want 0", got)
	}
	t.Setenv(EnvIntelSeedEnabled, "true")
	t.Setenv(EnvIntelSeedDailyCap, "0")
	if got := svc.IntelSeedHeadroom(ctx, orgID, now); got != 0 {
		t.Fatalf("headroom without a cap = %d, want 0", got)
	}
	// An unwired ledger is an uncountable cap, which is not a cap.
	t.Setenv(EnvIntelSeedDailyCap, "10")
	bare, bareOrg := newWebIntentService(t)
	if got := bare.IntelSeedHeadroom(ctx, bareOrg, now); got != 0 {
		t.Fatalf("headroom with no ledger = %d, want 0", got)
	}
}

// A dormant or exhausted lane never reaches the transport.
func TestProcessIntelSeedOnceDoesNothingWhileDormantOrExhausted(t *testing.T) {
	ctx := context.Background()
	svc, orgID := newWebIntentService(t)
	ledger := &fakeSeedLedger{}
	svc.WireIntelSeed(ledger)

	t.Setenv(EnvIntelSeedEnabled, "")
	t.Setenv(EnvIntelSeedDailyCap, "5")
	t.Setenv(EnvIntelSeedOrgID, orgID.String())
	if progressed, err := svc.ProcessIntelSeedOnce(ctx); progressed || err != nil {
		t.Fatalf("dormant lane progressed=%v err=%v", progressed, err)
	}
	t.Setenv(EnvIntelSeedEnabled, "true")
	t.Setenv(EnvIntelSeedDailyCap, "0")
	if progressed, err := svc.ProcessIntelSeedOnce(ctx); progressed || err != nil {
		t.Fatalf("uncapped lane progressed=%v err=%v", progressed, err)
	}
	if len(ledger.sends) != 0 {
		t.Fatalf("a dormant lane recorded %d sends", len(ledger.sends))
	}
}

// The loop advances instead of re-offering the same contact forever: a contact
// this lane already wrote to is skipped by the pre-filter.
func TestIntelSeedLoopSkipsAlreadySeededContacts(t *testing.T) {
	ctx := context.Background()
	svc, orgID := newWebIntentService(t)
	ledger := &fakeSeedLedger{}
	svc.WireIntelSeed(ledger)
	now := time.Now().UTC()

	offered := svc.intelSeedCandidates(ctx, orgID, now, 25)
	if len(offered) == 0 {
		t.Fatal("no candidate offered")
	}
	first := offered[0]
	if _, err := ledger.RecordSend(ctx, models.IntelSeedSend{
		OrganizationID: orgID, MessageKey: MessageKeyIntelSeed(first, "company:acme"),
		CandidateID: first, Recipient: "ana@example.com", SentAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range svc.intelSeedCandidates(ctx, orgID, now, 25) {
		if id == first {
			t.Fatal("the loop re-offered a contact this lane already wrote to")
		}
	}
}

// A contact the gate refuses writes no ledger row, so the pre-filter cannot
// skip them. The pass must still put the contacts behind them to the gate
// rather than stopping at the first refusal and re-offering it every tick.
func TestIntelSeedPassTriesPastAContactTheGateRefuses(t *testing.T) {
	ctx := context.Background()
	svc, orgID := newWebIntentService(t)
	svc.WireIntelSeed(&fakeSeedLedger{})
	now := time.Now().UTC()

	acc, err := svc.repo.GetAccountByCNPJ(ctx, orgID, "12345678000190")
	if err != nil || acc == nil {
		t.Fatalf("seed account missing: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := svc.repo.UpsertCandidate(ctx, &models.OutreachContactCandidate{
			ID: uuid.New(), OrganizationID: orgID, AccountID: acc.ID,
			Name: fmt.Sprintf("Contato %d", i), Email: fmt.Sprintf("c%d@example.com", i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	offered := svc.intelSeedCandidates(ctx, orgID, now, intelSeedGateAttemptsPerPass)
	if len(offered) < 3 {
		t.Fatalf("one pass offered %d contacts, want the ones behind the first", len(offered))
	}
	seen := map[uuid.UUID]bool{}
	for _, id := range offered {
		if seen[id] {
			t.Fatalf("the same contact was offered twice in one pass: %s", id)
		}
		seen[id] = true
	}
}

// Proof H / E. First-touch admission and drain are identical with the lane off
// and with it on and fully exhausted. The two runs share this one body, so a
// difference is a real difference, not a difference in the harness.
func TestFirstTouchUnchangedByIntelSeedState(t *testing.T) {
	baseline := lane0FirstTouchFingerprint(t)

	t.Run("intel seed enabled and its dedicated cap exhausted", func(t *testing.T) {
		t.Setenv(EnvIntelSeedEnabled, "true")
		t.Setenv(EnvIntelSeedDailyCap, "3")
		t.Setenv(EnvIntelSeedOrgID, uuid.New().String())
		// Exhaust the lane's own counter, then re-measure first touch.
		ctx := context.Background()
		svc, orgID := newWebIntentService(t)
		ledger := &fakeSeedLedger{}
		svc.WireIntelSeed(ledger)
		now := time.Now().UTC()
		for i := 0; i < 3; i++ {
			if _, err := ledger.RecordSend(ctx, models.IntelSeedSend{
				OrganizationID: orgID, MessageKey: fmt.Sprintf("email:intel_seed:contact:%d", i),
				Recipient: "x@example.com", SentAt: now,
			}); err != nil {
				t.Fatal(err)
			}
		}
		if headroom := svc.IntelSeedHeadroom(ctx, orgID, now); headroom != 0 {
			t.Fatalf("the seed cap was not exhausted: headroom = %d", headroom)
		}
		if got := lane0FirstTouchFingerprint(t); got != baseline {
			t.Fatalf("first touch changed with INTEL_SEED enabled and exhausted:\n  off: %s\n   on: %s", baseline, got)
		}
	})
}

// lane0FirstTouchFingerprint re-runs the LANE 0 queue-drain measurement and
// returns its result as a string, so two runs under different INTEL_SEED
// environments can be compared as one value.
//
// It reuses the LANE 0 pack's own governor helper and cap rather than
// duplicating the numbers, so a change to first-touch pacing shows up here as
// a changed fingerprint instead of being silently re-asserted.
func lane0FirstTouchFingerprint(t *testing.T) string {
	t.Helper()
	t.Setenv(EnvKillSwitchPath, filepath.Join(t.TempDir(), "absent"))
	ctx := context.Background()
	const capPerHour = 5
	const queued = 12
	store := dispatch.NewMemoryStore()
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	gov := newLane0Governor(store, capPerHour, now)
	orgID := uuid.New()

	for i := 0; i < queued; i++ {
		draftID := uuid.New()
		if err := store.Enqueue(ctx, &dispatch.QueueItem{
			ID: uuid.New(), OrganizationID: orgID, Channel: dispatch.ChannelEmail,
			DraftID: draftID, MessageKey: dispatch.MessageKeyEmail(draftID),
			RecipientRef: fmt.Sprintf("lane0-%d@example.com", i),
			DueAt:        now.Add(-time.Minute), Status: dispatch.QueueQueued, CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	reserved, claimed, refused := 0, 0, 0
	for i := 0; i < queued; i++ {
		item, err := gov.ClaimNextQueued(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if item == nil {
			break
		}
		claimed++
		res, err := gov.TryReserve(ctx, dispatch.ReserveRequest{
			OrganizationID: item.OrganizationID, Channel: item.Channel,
			MessageKey: item.MessageKey, DraftID: &item.DraftID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Allowed {
			reserved++
			if err := gov.Commit(ctx, res.Reservation.ID); err != nil {
				t.Fatal(err)
			}
		} else {
			refused++
		}
	}
	return fmt.Sprintf("claimed=%d reserved=%d refused=%d cap=%d default_daily=%d min_gap=%d",
		claimed, reserved, refused, capPerHour, config.CampaignLimitDefault, config.MinWaitTimeDefault)
}
