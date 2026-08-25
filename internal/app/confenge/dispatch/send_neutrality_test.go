package dispatch

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The window fix touches the dispatch package, which the repository guards as
// the last-mile send path. The file-level guard only compares the working tree
// to HEAD, so it cannot say whether a committed change altered behaviour. This
// test states the property that actually matters: correcting the *reported*
// next slot must not change any decision about whether something is sent.
func TestNextSlotReportingDoesNotChangeSendDecisions(t *testing.T) {
	sp, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	org := uuid.New()
	instants := []time.Time{
		time.Date(2026, 8, 23, 14, 9, 0, 0, sp), // Sunday, the observed case
		time.Date(2026, 8, 22, 11, 0, 0, 0, sp), // Saturday
		time.Date(2026, 8, 24, 10, 0, 0, 0, sp), // Monday inside window
		time.Date(2026, 8, 24, 8, 0, 0, 0, sp),  // Monday before open
		time.Date(2026, 8, 24, 19, 0, 0, 0, sp), // Monday after close
	}
	for _, inst := range instants {
		for _, paused := range []bool{false, true} {
			cfg := DefaultConfig()
			cfg.BusinessDaysOnly = true
			cfg.EnvPaused = paused
			g := NewGovernor(cfg, NewMemoryStore(), &FixedClock{T: inst.UTC()})

			res, err := g.TryReserve(context.Background(), ReserveRequest{
				OrganizationID: org,
				Channel:        ChannelEmail,
				MessageKey:     "email:draft:" + inst.Format(time.RFC3339) + "-neutrality",
			})
			if err != nil {
				t.Fatalf("%s paused=%v: %v", inst, paused, err)
			}
			// A weekend, an out-of-hours instant, or a pause must never allow a
			// reservation. This is the send decision; the fix must not move it.
			inWindow, werr := InSendWindowBusiness(inst.UTC(), cfg.Timezone, cfg.WindowStart, cfg.WindowEnd, true)
			if werr != nil {
				t.Fatal(werr)
			}
			wantAllowed := inWindow && !paused
			if res.Allowed != wantAllowed {
				t.Fatalf("%s paused=%v: allowed=%v want %v (reason=%s)",
					inst.In(sp), paused, res.Allowed, wantAllowed, res.Reason)
			}
		}
	}
}

// Whatever next_slot_at reports, a paused governor must refuse to reserve. The
// advertised slot is information for the founder, never an authorization.
func TestReportedNextSlotIsNotAnAuthorization(t *testing.T) {
	sp, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	// Monday inside the window, but paused: the reported slot is "now-ish" and
	// must still not permit a send.
	monday := time.Date(2026, 8, 24, 10, 0, 0, 0, sp)
	cfg := DefaultConfig()
	cfg.BusinessDaysOnly = true
	cfg.EnvPaused = true
	g := NewGovernor(cfg, NewMemoryStore(), &FixedClock{T: monday.UTC()})

	st, err := g.Status(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Paused {
		t.Fatal("fixture must be paused")
	}
	if st.NextSlotAt == nil {
		t.Fatal("expected a reported next slot")
	}
	res, err := g.TryReserve(context.Background(), ReserveRequest{
		OrganizationID: uuid.New(), Channel: ChannelEmail, MessageKey: "email:draft:paused-authz",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Allowed {
		t.Fatal("a paused governor reserved a slot: next_slot_at must never authorize a send")
	}
}
