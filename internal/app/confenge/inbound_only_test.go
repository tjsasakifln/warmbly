package confenge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/models"
)

func newEmptyWebIntentService(t *testing.T) (*service, uuid.UUID) {
	t.Helper()
	repo := newMemRepo()
	orgID := uuid.New()
	svc := NewService(Config{
		Enabled: true, RequireHumanApproval: true, DefaultDailyLimit: 50,
		FeedSchemaVersion: models.OutreachSchemaV1, EvidenceVersion: DefaultEvidenceVersion,
	}, repo, nil).(*service)
	return svc, orgID
}

func TestNetNewRequestHumanReviewAdmitsInboundOnlyAndLandsOnQueue(t *testing.T) {
	for _, kind := range []string{intel.WebIntentRequestHumanReview, intel.WebIntentRequestDeepDive} {
		t.Run(kind, func(t *testing.T) {
			svc, orgID := newEmptyWebIntentService(t)
			svc.WireIntelWatchSubscriptions(&fakeWatchSubs{})
			env := validWebIntent(kind)
			env.ContactEmail = "net-new@example.com"
			env.ContactName = "Net New"
			env.Evidence = "asked for a human from the public form"

			res, xerr := svc.IngestWebIntent(context.Background(), orgID, env, time.Now().UTC())
			if xerr != nil {
				t.Fatalf("valid-contact REQUEST_* was dropped: %v", xerr)
			}
			if res.Reason == "contact_not_matched_to_account" {
				t.Fatal("silent drop: contact_not_matched_to_account")
			}
			if res.ActionID == nil {
				t.Fatalf("accepted REQUEST_* did not land on the commercial queue: %+v", res)
			}
			if !res.InboundOnly {
				t.Fatal("net-new representation was not marked inbound_only")
			}
			if res.OutboundEligible {
				t.Fatal("inbound_only representation became outbound-eligible by default")
			}
			if res.Origin != EngineLaneConfengeWeb || res.Receipt == "" || res.AccountID == nil {
				t.Fatalf("origin/lane/intent/receipt missing: %+v", res)
			}
			action, err := svc.actionStore().GetCommercialAction(context.Background(), orgID, *res.ActionID)
			if err != nil || action == nil {
				t.Fatalf("queue row missing: %v", err)
			}
			if action.EngineLane != EngineLaneConfengeWeb {
				t.Fatalf("engine_lane=%q", action.EngineLane)
			}
			if !HandRaiseInboundOnlyOf(action) {
				t.Fatal("queue row lost inbound_only provenance")
			}
			if HandRaiseOriginOf(action) != EngineLaneConfengeWeb {
				t.Fatalf("origin=%q", HandRaiseOriginOf(action))
			}
			if HandRaiseReceiptOf(action) != res.Receipt {
				t.Fatalf("receipt=%q want %q", HandRaiseReceiptOf(action), res.Receipt)
			}
			if HandRaiseIntentOf(action) != kind {
				t.Fatalf("intent=%q want %q", HandRaiseIntentOf(action), kind)
			}
			acc, err := svc.repo.GetAccount(context.Background(), orgID, *res.AccountID)
			if err != nil || acc == nil {
				t.Fatalf("account missing: %v", err)
			}
			if !models.AccountIsInboundOnly(acc) {
				t.Fatalf("account source_system=%q inbound_only=%v", acc.SourceSystem, acc.InboundOnly)
			}
			if acc.TargetFitEligible || acc.EmailSendReady {
				t.Fatal("inbound_only account is outbound-eligible")
			}
		})
	}
}

func TestNetNewHandRaiserReplay100xIsOneLogicalIntent(t *testing.T) {
	svc, orgID := newEmptyWebIntentService(t)
	svc.WireIntelWatchSubscriptions(&fakeWatchSubs{})
	env := validWebIntent(intel.WebIntentRequestHumanReview)
	env.ContactEmail = "replay@example.com"
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

	var first *WebIntentResult
	for i := 0; i < 100; i++ {
		res, xerr := svc.IngestWebIntent(context.Background(), orgID, env, now.Add(time.Duration(i)*time.Second))
		if xerr != nil {
			t.Fatalf("replay %d failed: %v", i, xerr)
		}
		if res.ActionID == nil {
			t.Fatalf("replay %d dropped the intent: %+v", i, res)
		}
		if first == nil {
			first = res
			continue
		}
		if *res.ActionID != *first.ActionID {
			t.Fatalf("replay %d created a second logical intent: %s vs %s", i, res.ActionID, first.ActionID)
		}
		if *res.AccountID != *first.AccountID {
			t.Fatalf("replay %d created a second representation", i)
		}
		if res.Receipt != first.Receipt {
			t.Fatalf("replay %d changed receipt", i)
		}
	}
	actions, err := svc.actionStore().ListCommercialActions(context.Background(), orgID, *first.AccountID, false, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 {
		t.Fatalf("100 replays produced %d queue rows", len(actions))
	}
	t.Logf("REPLAY_100_LOSS=0 REPLAY_100_DUPLICATES=0")
}

func TestDistinctHandRaiserKeysDoNotCollapse(t *testing.T) {
	svc, orgID := newEmptyWebIntentService(t)
	svc.WireIntelWatchSubscriptions(&fakeWatchSubs{})
	a := validWebIntent(intel.WebIntentRequestHumanReview)
	a.ContactEmail = "one@example.com"
	b := validWebIntent(intel.WebIntentRequestDeepDive)
	b.ContactEmail = "two@example.com"
	now := time.Now().UTC()
	first, xerr := svc.IngestWebIntent(context.Background(), orgID, a, now)
	if xerr != nil {
		t.Fatal(xerr)
	}
	second, xerr := svc.IngestWebIntent(context.Background(), orgID, b, now)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if first.ActionID == nil || second.ActionID == nil || *first.ActionID == *second.ActionID {
		t.Fatalf("distinct keys collapsed: %v vs %v", first.ActionID, second.ActionID)
	}
}

func TestExistingOutboundAccountIsNotRewrittenInboundOnly(t *testing.T) {
	svc, orgID := newWebIntentService(t)
	svc.WireIntelWatchSubscriptions(&fakeWatchSubs{})
	res, xerr := svc.IngestWebIntent(context.Background(), orgID, validWebIntent(intel.WebIntentRequestHumanReview), time.Now().UTC())
	if xerr != nil {
		t.Fatal(xerr)
	}
	if res.InboundOnly {
		t.Fatal("existing outbound account was rewritten inbound_only")
	}
	if res.ActionID == nil {
		t.Fatal("matched REQUEST_* did not land on the queue")
	}
}

func TestNetNewInvalidEnvelopeIsRejectedWithReasonOrUnknown(t *testing.T) {
	svc, orgID := newEmptyWebIntentService(t)
	svc.WireIntelWatchSubscriptions(&fakeWatchSubs{})
	cases := []struct {
		name   string
		mutate func(*intel.WebIntentEnvelope)
		want   string
	}{
		{"missing_email", func(e *intel.WebIntentEnvelope) { e.ContactEmail = "" }, WebIntentReasonEmailMissing},
		{"unknown_kind", func(e *intel.WebIntentEnvelope) { e.IntentKind = "REQUEST_EVERYTHING" }, WebIntentReasonKindUnknown},
		{"cnpj_key", func(e *intel.WebIntentEnvelope) { e.CompanyRef = "11222333000181" }, WebIntentReasonKeyIsCNPJ},
		{"missing_company", func(e *intel.WebIntentEnvelope) { e.CompanyRef = "" }, WebIntentReasonCompanyMissing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := validWebIntent(intel.WebIntentRequestHumanReview)
			tc.mutate(&env)
			res, xerr := svc.IngestWebIntent(context.Background(), orgID, env, time.Now().UTC())
			if xerr == nil {
				t.Fatalf("invalid envelope was accepted: %+v", res)
			}
			if xerr.Identifier != tc.want && xerr.Identifier != WebIntentReasonUnknown {
				t.Fatalf("identifier=%q want %q or UNKNOWN", xerr.Identifier, tc.want)
			}
			for _, banned := range []string{"ACCOUNT_MISSING", "ACCOUNT_NOT_FOUND", "contact_not_matched_to_account"} {
				if xerr.Identifier == banned {
					t.Fatalf("silent-drop reason %q", banned)
				}
			}
		})
	}
}

func TestNetNewMetricsReadbackHasNoPII(t *testing.T) {
	svc, orgID := newEmptyWebIntentService(t)
	svc.WireIntelWatchSubscriptions(&fakeWatchSubs{})
	env := validWebIntent(intel.WebIntentRequestHumanReview)
	env.ContactEmail = "pii-person@example.com"
	env.ContactName = "Pii Person"
	env.Evidence = "call pii-person@example.com at +5511999999999"
	res, xerr := svc.IngestWebIntent(context.Background(), orgID, env, time.Now().UTC())
	if xerr != nil {
		t.Fatal(xerr)
	}
	raw, err := json.Marshal(WebIntentMetricsView(res))
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ToLower(string(raw))
	for _, needle := range []string{"pii-person@example.com", "pii person", "+5511999999999", "@example.com"} {
		if strings.Contains(body, strings.ToLower(needle)) {
			t.Fatalf("metrics leaked %q: %s", needle, body)
		}
	}
	budget, xerr := svc.FounderInterruptBudget(context.Background(), orgID, 50)
	if xerr != nil {
		t.Fatal(xerr)
	}
	agg, err := json.Marshal(map[string]any{"by_bucket": budget.ByBucket, "by_engine": budget.ByEngine, "total": budget.Total})
	if err != nil {
		t.Fatal(err)
	}
	aggBody := strings.ToLower(string(agg))
	for _, needle := range []string{"pii-person@example.com", "pii person", "+5511"} {
		if strings.Contains(aggBody, strings.ToLower(needle)) {
			t.Fatalf("aggregate leaked %q: %s", needle, agg)
		}
	}
}

func TestNetNewE2EIntakeToQueueNeverSendsSMTP(t *testing.T) {
	svc, orgID := newEmptyWebIntentService(t)
	svc.WireIntelWatchSubscriptions(&fakeWatchSubs{})
	env := validWebIntent(intel.WebIntentRequestDeepDive)
	env.ContactEmail = "qa-net-new@example.com"
	env.ContactName = "QA Net New"
	env.Evidence = "synthetic QA intake"
	res, xerr := svc.IngestWebIntent(context.Background(), orgID, env, time.Now().UTC())
	if xerr != nil {
		t.Fatalf("QA intake dropped: %v", xerr)
	}
	if res.ActionID == nil || !res.InboundOnly || res.OutboundEligible {
		t.Fatalf("QA path did not land inbound-only on the queue: %+v", res)
	}
	if svc.governor != nil {
		t.Fatal("QA intake wired a send governor")
	}
	t.Logf("NET_NEW_HANDRAISER_TO_QUEUE=YES SMTP_SENT=NO action=%s account=%s", res.ActionID, res.AccountID)
}

func TestInboundOnlyPlaceholderCNPJIsFourteenDigits(t *testing.T) {
	a := inboundOnlyPlaceholderCNPJ("inbound_only:confenge_web:REQUEST_HUMAN_REVIEW:one@example.com")
	b := inboundOnlyPlaceholderCNPJ("inbound_only:confenge_web:REQUEST_HUMAN_REVIEW:one@example.com")
	c := inboundOnlyPlaceholderCNPJ("inbound_only:confenge_web:REQUEST_DEEP_DIVE:one@example.com")
	if len(a) != 14 || a != b {
		t.Fatalf("placeholder not stable 14 digits: %q %q", a, b)
	}
	if a == c {
		t.Fatal("distinct keys produced the same placeholder CNPJ")
	}
	for _, ch := range a {
		if ch < '0' || ch > '9' {
			t.Fatalf("non-digit placeholder %q", a)
		}
	}
}
