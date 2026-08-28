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
