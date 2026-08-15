package confenge

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

func TestProjectInboundNowUnknownsAndHumanNext(t *testing.T) {
	now := time.Date(2026, 8, 15, 19, 0, 0, 0, time.UTC)
	lead := models.OutreachInboundLead{
		LeadID:            "webcfg-sparse-1",
		ReceiptID:         "rcpt-sparse-1",
		LeadCreatedAt:     now.Add(-10 * time.Minute),
		WarmblyIngestedAt: now,
		EnrichmentStatus:  models.InboundEnrichmentUnknown,
		NextAction:        models.InboundNextNeedsEnrichment,
		Status:            models.InboundStatusOpen,
	}
	item := ProjectInboundNowItem(lead, nil, nil, now)
	if item.Origin != inboundUnknown || item.Query != inboundUnknown || item.CTA != inboundUnknown ||
		item.Trigger != inboundUnknown || item.Offer != inboundUnknown || item.EntityID != inboundUnknown ||
		item.Freshness != inboundUnknown || item.Confidence != inboundUnknown || item.Reachability != inboundUnknown {
		t.Fatalf("missing facts must render UNKNOWN: %+v", item)
	}
	if item.Dispatchable || item.EmailSendable {
		t.Fatal("sparse card must not be email-gated or dispatchable")
	}
	if !strings.Contains(strings.ToLower(item.RecommendedAction), "enriquecer") {
		t.Fatalf("next action must stay human: %s", item.RecommendedAction)
	}
	if item.SuggestedCopyReview != "human_review_required" {
		t.Fatalf("suggested copy must stay review-only: %s", item.SuggestedCopyReview)
	}
	fmt.Printf("COCKPIT_UNKNOWN origin=%s query=%s cta=%s trigger=%s offer=%s freshness=%s confidence=%s next=%s\n",
		item.Origin, item.Query, item.CTA, item.Trigger, item.Offer, item.Freshness, item.Confidence, item.NextAction)
}

func TestRecordInboundOutcomeEmitsLearning(t *testing.T) {
	svc, repo, org := inboundTestService(t)
	owner := uuid.MustParse("11111111-2222-4333-8444-555555555555")
	repo.orgOwner[org] = owner
	now := time.Date(2026, 8, 15, 19, 30, 0, 0, time.UTC)
	body := []byte(`{"lead_id":"webcfg-learn-1","receipt_id":"rcpt-learn-1","source":"CONFENGE_WEB","company":"Norte","phone":"41991112222"}`)
	if _, xerr := svc.IngestInboundLead(context.Background(), org, body, IngestOptions{Now: now}); xerr != nil {
		t.Fatal(xerr)
	}
	if _, xerr := svc.RecordInboundOutcome(context.Background(), org, owner, "webcfg-learn-1", OutcomeRequest{
		OutcomeCode: models.OutcomeFollowUp, Now: now.Add(time.Minute),
	}); xerr != nil {
		t.Fatal(xerr)
	}
	cands, err := svc.intelStore().ListLearning(org.String())
	if err != nil || len(cands) == 0 {
		t.Fatalf("learning candidate missing: %v %v", cands, err)
	}
	got := cands[0]
	if got.Kind != "LEARNING_CANDIDATE" {
		t.Fatalf("kind=%s", got.Kind)
	}
	if len(got.UpstreamWrites) != 0 {
		t.Fatalf("upstream writes: %v", got.UpstreamWrites)
	}
	if got.CausalProof {
		t.Fatal("causal_proof must stay false")
	}
	fmt.Printf("LEARNING_FROM_OUTCOME lead=%s rec=%s causal_proof=%v upstream=%d\n",
		got.LeadID, got.Recommendation, got.CausalProof, len(got.UpstreamWrites))
}
