package tasks

import (
	"context"
	"errors"
	"testing"

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
