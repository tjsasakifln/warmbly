package confenge

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/models"
)

// Engine-lane attribution proofs.
//
// G: a hand raise from each wired engine converges to the right engine_lane and
//    shows up correctly in the interrupt budget's by_engine and in the
//    CONFENGE_SALES_CONTEXT_EXPORT/1.0 collection.

// newWebIntentService builds a service over the in-memory repository with one
// account and one contact already in the book, so intake has something to match.
func newWebIntentService(t *testing.T) (*service, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	repo := newMemRepo()
	orgID := uuid.New()
	svc := NewService(Config{
		Enabled: true, RequireHumanApproval: true, DefaultDailyLimit: 50,
		FeedSchemaVersion: models.OutreachSchemaV1, EvidenceVersion: DefaultEvidenceVersion,
	}, repo, nil).(*service)
	acc := &models.OutreachAccount{
		ID: uuid.New(), OrganizationID: orgID, CNPJ14: "12345678000190",
		RazaoSocial: "Acme Holdings", QueueState: models.OutreachQueueNeedsContact,
	}
	if _, err := repo.UpsertAccount(ctx, acc); err != nil {
		t.Fatal(err)
	}
	cand := &models.OutreachContactCandidate{
		ID: uuid.New(), OrganizationID: orgID, AccountID: acc.ID,
		Name: "Ana Souza", Email: "ana@example.com",
	}
	if _, err := repo.UpsertCandidate(ctx, cand); err != nil {
		t.Fatal(err)
	}
	return svc, orgID
}

// A REQUEST_* intake converges onto the hand-raiser surface with the
// confenge_web engine lane, and creates no watch subscription.
func TestWebIntentRequestConvergesAsConfengeWebHandRaise(t *testing.T) {
	for _, tc := range []struct {
		kind       string
		wantSignal HandRaiseSignal
		wantLane   string
	}{
		{intel.WebIntentRequestDeepDive, SignalRequestDeepDive, models.LaneInboundNow},
		{intel.WebIntentRequestHumanReview, SignalRequestHumanReview, models.LaneEmailNeedsReview},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			svc, orgID := newWebIntentService(t)
			subs := &fakeWatchSubs{}
			svc.WireIntelWatchSubscriptions(subs)

			res, xerr := svc.IngestWebIntent(context.Background(), orgID, validWebIntent(tc.kind), time.Now().UTC())
			if xerr != nil {
				t.Fatalf("intake rejected: %v", xerr)
			}
			if len(subs.saved) != 0 {
				t.Fatalf("a REQUEST_* intake created %d subscriptions", len(subs.saved))
			}
			if res.ActionID == nil {
				t.Fatalf("no commercial action was persisted: %+v", res)
			}
			action, err := svc.actionStore().GetCommercialAction(context.Background(), orgID, *res.ActionID)
			if err != nil || action == nil {
				t.Fatalf("action not readable: %v", err)
			}
			if action.EngineLane != EngineLaneConfengeWeb {
				t.Fatalf("engine_lane = %q, want %q", action.EngineLane, EngineLaneConfengeWeb)
			}
			if action.Lane != tc.wantLane {
				t.Fatalf("cockpit lane = %q, want %q", action.Lane, tc.wantLane)
			}
			if got := HandRaiseSignalOf(action); got != string(tc.wantSignal) {
				t.Fatalf("recorded signal = %q, want %q", got, tc.wantSignal)
			}
			if got := HandRaiseSubjectKeyOf(action); got != "company:acme-holdings" {
				t.Fatalf("recorded subject key = %q", got)
			}
		})
	}
}

// The same web request twice is one hand-raiser, not two.
func TestWebIntentRequestConvergesOnce(t *testing.T) {
	svc, orgID := newWebIntentService(t)
	svc.WireIntelWatchSubscriptions(&fakeWatchSubs{})
	env := validWebIntent(intel.WebIntentRequestDeepDive)
	first, xerr := svc.IngestWebIntent(context.Background(), orgID, env, time.Now().UTC())
	if xerr != nil {
		t.Fatal(xerr)
	}
	second, xerr := svc.IngestWebIntent(context.Background(), orgID, env, time.Now().UTC().Add(time.Hour))
	if xerr != nil {
		t.Fatal(xerr)
	}
	if first.ActionID == nil || second.ActionID == nil || *first.ActionID != *second.ActionID {
		t.Fatalf("repeat request did not converge: %v vs %v", first.ActionID, second.ActionID)
	}
}

// Proof G. Hand raises from three engines are attributed separately in
// by_engine and carried through to the sales-context export.
func TestInterruptBudgetAndSalesContextAttributeEachEngine(t *testing.T) {
	ctx := context.Background()
	svc, orgID := newWebIntentService(t)
	accountID := uuid.New()

	lanes := []struct {
		lane   string
		signal HandRaiseSignal
	}{
		{EngineLaneFirstTouch, SignalPositiveReplyFirstTouch},
		{EngineLaneIntelSeed, SignalIntelSeedResponse},
		{EngineLaneConfengeWeb, SignalRequestDeepDive},
	}
	for _, l := range lanes {
		if _, xerr := svc.PersistHandRaise(ctx, HandRaise{
			OrganizationID: orgID, AccountID: accountID, Signal: l.signal, EngineLane: l.lane,
			OccurredAt: time.Now().UTC(), Evidence: "reply", SubjectKey: "company:acme-holdings",
		}); xerr != nil {
			t.Fatalf("%s did not converge: %v", l.lane, xerr)
		}
	}

	budget, xerr := svc.FounderInterruptBudget(ctx, orgID, 50)
	if xerr != nil {
		t.Fatal(xerr)
	}
	for _, l := range lanes {
		if budget.ByEngine[l.lane] != 1 {
			t.Fatalf("by_engine[%s] = %d, want 1 (full map %v)", l.lane, budget.ByEngine[l.lane], budget.ByEngine)
		}
	}
	if budget.ByEngine[EngineLaneIntelWatch] != 0 {
		t.Fatalf("intel_watch attributed %d rows it did not produce", budget.ByEngine[EngineLaneIntelWatch])
	}
	if budget.Unattributed != 0 {
		t.Fatalf("unattributed = %d, want 0 now that every writer stamps a lane", budget.Unattributed)
	}

	export, xerr := svc.ExportSalesContext(ctx, orgID, 50, "")
	if xerr != nil {
		t.Fatal(xerr)
	}
	if export.Schema != SalesContextExportSchemaV1 {
		t.Fatalf("collection schema = %q", export.Schema)
	}
	if export.Total != len(lanes) {
		t.Fatalf("export total = %d, want %d", export.Total, len(lanes))
	}
	for _, l := range lanes {
		if export.ByEngine[l.lane] != 1 {
			t.Fatalf("export by_engine[%s] = %d, want 1", l.lane, export.ByEngine[l.lane])
		}
	}
	for _, item := range export.Items {
		if item.AcquisitionChannel == EngineLaneUnattributed {
			t.Fatalf("export item has no acquisition channel: %+v", item)
		}
		if item.CompanyRef != "acme-holdings" {
			t.Fatalf("export item company_ref = %q, want acme-holdings", item.CompanyRef)
		}
		if item.IntentReason == "" {
			t.Fatalf("export item has no intent reason: %+v", item)
		}
	}
}

// An unattributed hand raise still round-trips its signal. This is the guard on
// the idempotency-key split: an engine segment that is the empty string must
// not shift the signal out from under the reader.
func TestHandRaiseSignalRoundTripsWithoutAnEngine(t *testing.T) {
	action := ConvergeHandRaise(HandRaise{
		OrganizationID: uuid.New(), AccountID: uuid.New(),
		Signal: SignalMeetingOrProposalRequest, EngineLane: "",
	})
	if action == nil {
		t.Fatal("unattributed hand raise did not converge")
	}
	if action.EngineLane != EngineLaneUnattributed {
		t.Fatalf("engine_lane = %q, want unattributed", action.EngineLane)
	}
	if got := HandRaiseSignalOf(action); got != string(SignalMeetingOrProposalRequest) {
		t.Fatalf("signal = %q, want %q", got, SignalMeetingOrProposalRequest)
	}
}

// Reply attribution never guesses. A correlated touchpoint is first touch; an
// uncorrelated reply with no declared lane stays unattributed.
func TestReplyEngineLaneIsDerivedNeverGuessed(t *testing.T) {
	if got := ReplyEngineLane(InboundHandoff{}); got != EngineLaneUnattributed {
		t.Fatalf("bare reply attributed to %q", got)
	}
	if got := ReplyEngineLane(InboundHandoff{TouchpointID: uuid.New()}); got != EngineLaneFirstTouch {
		t.Fatalf("correlated touchpoint attributed to %q, want first touch", got)
	}
	if got := ReplyEngineLane(InboundHandoff{EngineLane: EngineLaneIntelSeed}); got != EngineLaneIntelSeed {
		t.Fatalf("declared lane became %q", got)
	}
	if got := ReplyEngineLane(InboundHandoff{EngineLane: "not-an-engine"}); got != EngineLaneUnattributed {
		t.Fatalf("unknown declared lane became %q, want unattributed", got)
	}
}

func TestReplyHandRaiseSignalPerEngine(t *testing.T) {
	if _, ok := ReplyHandRaiseSignal(IntentObjection, EngineLaneFirstTouch); ok {
		t.Fatal("a non-positive reply converged as a hand raise")
	}
	sig, ok := ReplyHandRaiseSignal(IntentPositiveInterest, EngineLaneFirstTouch)
	if !ok || sig != SignalPositiveReplyFirstTouch {
		t.Fatalf("first touch positive reply = %q", sig)
	}
	sig, ok = ReplyHandRaiseSignal(IntentPositiveInterest, EngineLaneIntelSeed)
	if !ok || sig != SignalIntelSeedResponse {
		t.Fatalf("intel seed positive reply = %q", sig)
	}
}
