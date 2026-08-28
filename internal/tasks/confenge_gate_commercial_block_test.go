package tasks

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/repository"
)

type commercialBlockProgressRepo struct {
	repository.CampaignProgressRepository
	sent     int
	bounced  int
	sentErr  error
	lastPair [3]uuid.UUID
}

func (r *commercialBlockProgressRepo) RecordEmailSent(_ context.Context, campaignID, contactID, sequenceID uuid.UUID) error {
	r.sent++
	r.lastPair = [3]uuid.UUID{campaignID, contactID, sequenceID}
	return r.sentErr
}

func (r *commercialBlockProgressRepo) RecordEmailBounced(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	r.bounced++
	return nil
}

// A reversible commercial block must advance campaign routing past the lead.
// Without this the scheduler reselects the same ineligible contact every cycle
// and no other contact in the campaign is ever reached.
func TestAdvancePastCommercialBlockAdvancesWithoutBounce(t *testing.T) {
	repo := &commercialBlockProgressRepo{}
	svc := &tasksService{campaignProgressRepo: repo}
	campaignID, contactID, sequenceID := uuid.New(), uuid.New(), uuid.New()

	svc.advancePastCommercialBlock(context.Background(), campaignID, contactID, sequenceID)

	if repo.sent != 1 {
		t.Fatalf("commercial block did not advance campaign routing: sent=%d", repo.sent)
	}
	if repo.lastPair != [3]uuid.UUID{campaignID, contactID, sequenceID} {
		t.Fatalf("advanced the wrong pair: %+v", repo.lastPair)
	}
	// The block is reversible: it must never bounce-mark the contact.
	if repo.bounced != 0 {
		t.Fatalf("a reversible commercial block bounce-marked the contact: bounced=%d", repo.bounced)
	}
}

// A progress failure must not panic or escalate; the campaign simply retries.
func TestAdvancePastCommercialBlockToleratesProgressFailure(t *testing.T) {
	repo := &commercialBlockProgressRepo{sentErr: errors.New("progress unavailable")}
	svc := &tasksService{campaignProgressRepo: repo}
	svc.advancePastCommercialBlock(context.Background(), uuid.New(), uuid.New(), uuid.New())
	if repo.sent != 1 {
		t.Fatalf("expected one advance attempt, got %d", repo.sent)
	}
}

// A service without a progress repo must stay inert rather than nil-panic.
func TestAdvancePastCommercialBlockWithoutProgressRepo(t *testing.T) {
	svc := &tasksService{}
	svc.advancePastCommercialBlock(context.Background(), uuid.New(), uuid.New(), uuid.New())
}

// An unverifiable mailbox (550 / no MX) must advance campaign routing the same
// way a reversible commercial block does. Production selected
// vtsconstrucoeslocacoe@hotmail.com on every tick after 19:12Z on 2026-08-28
// because the invalid-verification skip did not mark the pair done, so no
// other first-touch behind it could send.
func TestAdvancePastUnverifiableRecipientAdvancesWithoutBounce(t *testing.T) {
	repo := &commercialBlockProgressRepo{}
	svc := &tasksService{campaignProgressRepo: repo}
	campaignID, contactID, sequenceID := uuid.New(), uuid.New(), uuid.New()

	svc.advancePastSkippedPair(context.Background(), campaignID, contactID, sequenceID, "test")

	if repo.sent != 1 {
		t.Fatalf("unverifiable skip did not advance campaign routing: sent=%d", repo.sent)
	}
	if repo.lastPair != [3]uuid.UUID{campaignID, contactID, sequenceID} {
		t.Fatalf("advanced the wrong pair: %+v", repo.lastPair)
	}
	if repo.bounced != 0 {
		t.Fatalf("unverifiable skip bounce-marked the contact: bounced=%d", repo.bounced)
	}
}

// A cycle that sent no mail must not consume the mailbox min-gap. Pacing a skip
// at the next send slot made each ineligible lead cost a full gap, so a handful
// of blocked leads at the head of the campaign delayed the first eligible
// contact behind them by hours.
func TestSkipRetryTimeDoesNotBurnTheSendSlot(t *testing.T) {
	now := time.Now().UTC()
	nextSlot := now.Add(27 * time.Minute)

	got := skipRetryTime(nextSlot)

	if !got.Before(nextSlot) {
		t.Fatalf("a skip consumed the send slot: got=%s next_slot=%s", got, nextSlot)
	}
	if got.Before(now.Add(5 * time.Second)) {
		t.Fatalf("a skip must not hot-loop: got=%s now=%s", got, now)
	}
}

// When the mailbox is already due, the real slot is sooner than the skip delay
// and must win, so a skip never delays an eligible send.
func TestSkipRetryTimeKeepsAnAlreadyDueSlot(t *testing.T) {
	due := time.Now().UTC().Add(-time.Minute)
	if got := skipRetryTime(due); !got.Equal(due) {
		t.Fatalf("an already-due slot was pushed back: got=%s due=%s", got, due)
	}
}

// Send-time optimization is a preference about when inside a day to send, not
// an authority over which day. Unbounded it rolled a CONFENGE send from Friday
// 17:11 UTC to Monday 09:00 UTC: three days of silence after a single send, at
// 06:00 in the campaign's own timezone, outside its 09:00-18:00 window.
func TestOptimizedSendTimeCannotParkTheCampaignOnALaterDay(t *testing.T) {
	tz := "America/Sao_Paulo"
	base := time.Date(2026, 8, 28, 17, 11, 0, 0, time.UTC)
	parked := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)

	if got := withinDayOptimizedSendTime(base, parked, tz); !got.Equal(base) {
		t.Fatalf("optimization moved the send to a later day: got=%s base=%s", got, base)
	}
}

// Within the same local day the optimizer still wins, so the feature keeps working.
func TestOptimizedSendTimeKeepsAWithinDayPreference(t *testing.T) {
	tz := "America/Sao_Paulo"
	base := time.Date(2026, 8, 28, 15, 5, 0, 0, time.UTC)   // 12:05 local
	better := time.Date(2026, 8, 28, 17, 0, 0, 0, time.UTC) // 14:00 local, same day

	if got := withinDayOptimizedSendTime(base, better, tz); !got.Equal(better) {
		t.Fatalf("a same-day optimization was discarded: got=%s want=%s", got, better)
	}
}

// An optimizer result before the scheduler's slot must never pull a send early,
// because the slot already encodes the mailbox min-gap.
func TestOptimizedSendTimeNeverPullsASendEarlier(t *testing.T) {
	base := time.Date(2026, 8, 28, 17, 11, 0, 0, time.UTC)
	earlier := base.Add(-2 * time.Hour)
	if got := withinDayOptimizedSendTime(base, earlier, "America/Sao_Paulo"); !got.Equal(base) {
		t.Fatalf("optimization pulled the send earlier than its slot: got=%s base=%s", got, base)
	}
}
