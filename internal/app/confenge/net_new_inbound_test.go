package confenge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/liveintel"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

func netNewConsentAt() *time.Time {
	at := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	return &at
}

func validNetNewMap(logicalID string) map[string]any {
	return map[string]any{
		"schema":          NetNewInboundHandraiserSchema,
		"contract_id":     NetNewInboundContractID,
		"policy_id":       NetNewInboundContractID,
		"version":         NetNewInboundPinVersion,
		"policy_version":  NetNewInboundPinVersion,
		"content_hash":    NetNewInboundPinnedHash,
		"schema_hash":     NetNewInboundPinnedHash,
		"policy":          NetNewInboundHandraiserSchema,
		"policy_hash":     NetNewInboundPinnedHash,
		"intake_schema":   NetNewInboundIntakeSchema,
		"taxonomy":        NetNewInboundTaxonomySchema,
		"catalog":         NetNewInboundCatalogSchema,
		"source":          NetNewInboundSource,
		"lane":            NetNewInboundLane,
		"logical_id":      logicalID,
		"event_id":        logicalID,
		"idempotency_key": logicalID,
		"correlation_id":  "corr-" + logicalID,
		"nucleus":         "property_valuation",
		"offer_candidate": NetNewInboundOfferCandidate,
		"source_asset":    NetNewInboundSourceAsset,
		"city_class":      "capital",
		"urgency":         "this_week",
		"why_now":         "requested a technical readiness assessment from the public form",
		"person":          map[string]any{"email": logicalID + "@example.test", "name": "Net New " + logicalID},
		"company":         map[string]any{"name": "Obra " + logicalID},
		"consent":         map[string]any{"granted": true, "source": "web_form:confenge.com/inbound", "at": "2026-09-04T12:00:00Z"},
		"conflict":        map[string]any{"status": "NONE", "ref": "conflict:none"},
		"sensitive_data":  false,
	}
}

// governanceNetNewMap is the fixture as a REAL producer sends it: identical to
// validNetNewMap except the hash fields carry the published Governance
// policy_hash instead of this repository's local drift digest. It is the only
// fixture admissible against RuntimeInboundAuthorityPin.
func governanceNetNewMap(logicalID string) map[string]any {
	m := validNetNewMap(logicalID)
	m["content_hash"] = "sha256:" + GovernanceInboundPolicyHash
	m["schema_hash"] = "sha256:" + GovernanceInboundPolicyHash
	m["policy_hash"] = "sha256:" + GovernanceInboundPolicyHash
	return m
}

func marshalNetNew(t *testing.T, body map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestNetNewInboundPinMatchesPublishedFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/net_new_inbound_handraiser/conformance.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		PinMaterial string `json:"pin_material"`
		SchemaHash  string `json:"schema_hash"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.PinMaterial != NetNewInboundPinMaterial() {
		t.Fatalf("fixture pin_material diverged from runtime pin")
	}
	sum := sha256.Sum256([]byte(doc.PinMaterial))
	got := hex.EncodeToString(sum[:])
	if got != NetNewInboundPinnedHash || got != doc.SchemaHash || got != NetNewInboundPinHash() {
		t.Fatalf("pin hash fixture=%s const=%s recomputed=%s", doc.SchemaHash, NetNewInboundPinnedHash, got)
	}
}

func TestDecideNetNewInboundFailClosed(t *testing.T) {
	env, err := ParseNetNewInboundEnvelope(marshalNetNew(t, validNetNewMap("nnhr-ok")))
	if err != nil {
		t.Fatal(err)
	}
	if d := DecideNetNewInbound(env, testOnlyAuthorityPin()); d.Outcome != NetNewInboundOutcomeAccepted {
		t.Fatalf("valid envelope not accepted: %+v", d)
	}
	cases := []struct {
		name    string
		mutate  func(map[string]any)
		outcome string
		reason  string
	}{
		{"unknown version", func(m map[string]any) {
			m["schema"] = "NET_NEW_INBOUND_HANDRAISER/9.9.9"
			m["version"] = "9.9.9"
			m["policy_version"] = "9.9.9"
		}, NetNewInboundOutcomeUnknown, NetNewInboundReasonSchemaUnknown},
		{"missing hash", func(m map[string]any) { m["schema_hash"] = ""; m["policy_hash"] = ""; m["content_hash"] = "" }, NetNewInboundOutcomeRejected, NetNewInboundReasonHashUnpinned},
		{"divergent hash", func(m map[string]any) {
			bad := strings.Repeat("ab", 32)
			m["schema_hash"] = bad
			m["policy_hash"] = bad
			m["content_hash"] = bad
		}, NetNewInboundOutcomeRejected, NetNewInboundReasonHashMismatch},
		{"missing consent", func(m map[string]any) { m["consent"] = map[string]any{"granted": false} }, NetNewInboundOutcomeRejected, NetNewInboundReasonConsent},
		{"conflict decline", func(m map[string]any) { m["conflict"] = map[string]any{"status": "DECLINE", "ref": "conflict:abc"} }, NetNewInboundOutcomeRejected, NetNewInboundReasonConflictDecline},
		{"conflict unknown", func(m map[string]any) { m["conflict"] = map[string]any{"status": "UNKNOWN", "ref": "conflict:xyz"} }, NetNewInboundOutcomeUnknown, NetNewInboundReasonConflictUnknown},
		{"intel watch source", func(m map[string]any) { m["source"] = "INTEL_WATCH" }, NetNewInboundOutcomeRejected, NetNewInboundReasonIntelWatch},
		{"unknown nucleus", func(m map[string]any) { m["nucleus"] = "not_a_nucleus" }, NetNewInboundOutcomeRejected, NetNewInboundReasonNucleus},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := validNetNewMap("nnhr-neg")
			tc.mutate(body)
			parsed, err := ParseNetNewInboundEnvelope(marshalNetNew(t, body))
			if err != nil {
				t.Fatal(err)
			}
			d := DecideNetNewInbound(parsed, testOnlyAuthorityPin())
			if d.Outcome != tc.outcome || d.Reason != tc.reason {
				t.Fatalf("got %+v want %s/%s", d, tc.outcome, tc.reason)
			}
			if d.Outcome == NetNewInboundOutcomeAccepted {
				t.Fatal("negative case was accepted")
			}
		})
	}
}

func TestIsNetNewInboundDistinctFromIntelWatch(t *testing.T) {
	if !IsNetNewInboundHandraiserEnvelope(marshalNetNew(t, validNetNewMap("nnhr-det"))) {
		t.Fatal("valid envelope not detected")
	}
	opp := []byte(`{"schema":"` + liveintel.EventSchemaV1 + `","event_id":"e1","event_type":"NEW_OPPORTUNITY","subject_key":"company:x","payload":{"k":"v"}}`)
	if IsNetNewInboundHandraiserEnvelope(opp) {
		t.Fatal("opportunity event classified as net-new hand-raiser")
	}
	bundle := []byte(`{"schema":"` + liveintel.OfficialLiveIntelligenceSchema + `"}`)
	if IsNetNewInboundHandraiserEnvelope(bundle) {
		t.Fatal("live-intelligence bundle classified as net-new hand-raiser")
	}
	intent := []byte(`{"schema":"CONFENGE_WEB_INTENT/1.0","intent_kind":"REQUEST_HUMAN_REVIEW"}`)
	if IsNetNewInboundHandraiserEnvelope(intent) {
		t.Fatal("web intent classified as net-new hand-raiser")
	}
}

func TestNetNewAcceptedInboundOnlyNoSMTP(t *testing.T) {
	svc, repo, org := netNewTestService(t)
	now := *netNewConsentAt()
	sends := 0
	body := marshalNetNew(t, validNetNewMap("nnhr-accepted"))
	res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, body, now)
	if xerr != nil {
		t.Fatalf("ingest: %v", xerr)
	}
	if res.Outcome != NetNewInboundOutcomeAccepted {
		t.Fatalf("outcome=%s reason=%s", res.Outcome, res.Reason)
	}
	if res.Receipt == "" || res.LogicalID != "nnhr-accepted" {
		t.Fatalf("receipt missing: %+v", res)
	}
	if !res.InboundOnly || res.OutboundEligible || res.AutoSend || res.DispatchAttempted {
		t.Fatalf("outbound leaked: %+v", res)
	}
	if !res.MeetcfgHandoff || !MeetcfgHandoffAllowed(res.Outcome) {
		t.Fatal("meetcfg handoff not allowed after ACCEPTED")
	}
	if res.Nucleus != "property_valuation" || res.OfferCandidate != NetNewInboundOfferCandidate ||
		res.SourceAsset != NetNewInboundSourceAsset || res.CityClass != "capital" || res.Urgency != "this_week" {
		t.Fatalf("fields not preserved: %+v", res)
	}
	if res.ActionID == nil || res.AccountID == nil {
		t.Fatalf("missing commercial row: %+v", res)
	}
	acc, err := repo.GetAccount(context.Background(), org, *res.AccountID)
	if err != nil || acc == nil {
		t.Fatalf("account: %v", err)
	}
	if !models.AccountIsInboundOnly(acc) {
		t.Fatal("net-new account is not inbound-only")
	}
	if FirstTouchEligibleAccount(acc) || svc.netNewInFirstTouchEligibleSet(context.Background(), org, acc.ID) {
		t.Fatal("accepted inbound appeared in first-touch eligible set")
	}
	actions, err := svc.actionStore().ListCommercialActions(context.Background(), org, acc.ID, false, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 {
		t.Fatalf("want 1 hand-raiser got %d", len(actions))
	}
	if actions[0].EmailSendable || actions[0].Dispatchable {
		t.Fatal("hand-raiser is sendable")
	}
	if svc.governor != nil || svc.firstTouchTransport != nil {
		t.Fatal("ingest wired a send path")
	}
	progressed, err := svc.ProcessFastLaneOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if progressed || sends != 0 {
		t.Fatalf("provider mutation progressed=%v sends=%d", progressed, sends)
	}
	rb, xerr := svc.ReadbackNetNewInboundHandraiser(context.Background(), org, "nnhr-accepted")
	if xerr != nil {
		t.Fatal(xerr)
	}
	if rb.Outcome != NetNewInboundOutcomeAccepted || rb.Receipt != res.Receipt {
		t.Fatalf("readback: %+v", rb)
	}
	if rb.AcknowledgedBy != NetNewInboundAckActor || rb.AcknowledgedAt == nil || rb.PolicyVersion != NetNewInboundHandraiserSchema || rb.Hash == "" || rb.Receipt == "" {
		t.Fatalf("readback ack/policy/hash/receipt: %+v", rb)
	}
	if rb.Reason != "" && rb.Outcome == NetNewInboundOutcomeAccepted {
		// accepted may carry empty reason
	}
	exp, xerr := svc.ExportSalesContext(context.Background(), org, 50, "")
	if xerr != nil {
		t.Fatal(xerr)
	}
	found := false
	for _, item := range exp.Items {
		if item.ActionID == *res.ActionID {
			found = true
			if !item.InboundOnly {
				t.Fatal("sales context lost inbound_only")
			}
		}
	}
	if !found {
		t.Fatal("ACCEPTED hand-raiser missing from meetcfg projection")
	}
}

func TestNetNewCanonicalIDReconcileDoesNotNameMerge(t *testing.T) {
	svc, repo, org := netNewTestService(t)
	now := *netNewConsentAt()
	canonical := "canonical-acme-1"
	existing := &models.OutreachAccount{
		OrganizationID: org, SourceLeadID: canonical, SourceSystem: "extra-cli",
		CNPJ14: "55444333000122", RazaoSocial: "Same Display Name LTDA",
		QueueState: models.OutreachQueueNeedsContact, InboundOnly: false,
		TargetFitEligible: false, EmailSendReady: false,
	}
	if _, err := repo.UpsertAccount(context.Background(), existing); err != nil {
		t.Fatal(err)
	}

	body := validNetNewMap("nnhr-canonical")
	body["canonical_entity_id"] = canonical
	body["company"] = map[string]any{"name": "Same Display Name LTDA", "canonical_id": canonical}
	res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, body), now)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if res.Outcome != NetNewInboundOutcomeAccepted || res.AccountID == nil {
		t.Fatalf("canonical ingest: %+v", res)
	}
	if *res.AccountID != existing.ID {
		t.Fatalf("canonical ID did not reuse account %s vs %s", res.AccountID, existing.ID)
	}
	if !res.Reconciled {
		t.Fatal("canonical match was not marked reconciled")
	}

	a := validNetNewMap("nnhr-name-a")
	a["company"] = map[string]any{"name": "Same Display Name LTDA"}
	b := validNetNewMap("nnhr-name-b")
	b["company"] = map[string]any{"name": "Same Display Name LTDA"}
	first, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, a), now)
	if xerr != nil {
		t.Fatal(xerr)
	}
	second, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, b), now)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if first.Outcome != NetNewInboundOutcomeAccepted || second.Outcome != NetNewInboundOutcomeAccepted {
		t.Fatalf("name-collision ingest: %+v %+v", first, second)
	}
	if first.AccountID == nil || second.AccountID == nil || *first.AccountID == *second.AccountID {
		t.Fatalf("same name without ID merged: %v vs %v", first.AccountID, second.AccountID)
	}
	if *first.AccountID == existing.ID || *second.AccountID == existing.ID {
		t.Fatal("name-only envelope fused onto canonical entity")
	}
}

func TestNetNewReplay100OneReceiptOneHandraiser(t *testing.T) {
	svc, _, org := netNewTestService(t)
	now := *netNewConsentAt()
	body := marshalNetNew(t, validNetNewMap("nnhr-replay"))
	var first *NetNewInboundResult
	for i := 0; i < 100; i++ {
		res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, body, now.Add(time.Duration(i)*time.Second))
		if xerr != nil {
			t.Fatalf("replay %d: %v", i, xerr)
		}
		if res.Outcome != NetNewInboundOutcomeAccepted {
			t.Fatalf("replay %d outcome=%s", i, res.Outcome)
		}
		if first == nil {
			first = res
			continue
		}
		if res.Receipt != first.Receipt || res.LogicalID != first.LogicalID {
			t.Fatalf("replay %d changed receipt", i)
		}
		if res.ActionID == nil || first.ActionID == nil || *res.ActionID != *first.ActionID {
			t.Fatalf("replay %d extra hand-raiser", i)
		}
		if res.AccountID == nil || *res.AccountID != *first.AccountID {
			t.Fatalf("replay %d extra account", i)
		}
	}
	lead, err := svc.inboundStore().GetInboundLeadByLeadID(context.Background(), org, "nnhr-replay")
	if err != nil || lead == nil {
		t.Fatalf("receipt: %v", err)
	}
	actions, err := svc.actionStore().ListCommercialActions(context.Background(), org, *first.AccountID, false, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 {
		t.Fatalf("100 replays produced %d queue rows", len(actions))
	}
	acc, _ := svc.repo.GetAccount(context.Background(), org, *first.AccountID)
	if FirstTouchEligibleAccount(acc) {
		t.Fatal("replay made the account first-touch eligible")
	}
	t.Logf("REPLAY_100_LOSS=0 REPLAY_100_DUPLICATES=0 receipt=%s action=%s", first.Receipt, first.ActionID)
}

func TestNetNewRejectsDoNotCreateHandraiserOrMeetcfg(t *testing.T) {
	svc, repo, org := netNewTestService(t)
	now := *netNewConsentAt()
	cases := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{"decline", func(m map[string]any) {
			m["conflict"] = map[string]any{"status": "DECLINE", "ref": "conflict:only-ref"}
		}, NetNewInboundReasonConflictDecline},
		{"conflict unknown", func(m map[string]any) { m["conflict"] = map[string]any{"status": "UNKNOWN", "ref": "conflict:unk"} }, NetNewInboundReasonConflictUnknown},
		{"no consent", func(m map[string]any) { delete(m, "consent") }, NetNewInboundReasonConsent},
		{"unpinned", func(m map[string]any) { m["schema_hash"] = ""; m["policy_hash"] = ""; m["content_hash"] = "" }, NetNewInboundReasonHashUnpinned},
	}
	before, _ := repo.ListAccounts(context.Background(), org, repository.OutreachAccountFilter{Limit: 500})
	for i, tc := range cases {
		body := validNetNewMap("nnhr-rej-" + string(rune('a'+i)))
		tc.mutate(body)
		res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, body), now)
		if xerr != nil {
			t.Fatalf("%s: %v", tc.name, xerr)
		}
		if res.Outcome == NetNewInboundOutcomeAccepted || res.ActionID != nil || res.MeetcfgHandoff {
			t.Fatalf("%s accepted: %+v", tc.name, res)
		}
		if res.Reason != tc.want {
			t.Fatalf("%s reason=%s want %s", tc.name, res.Reason, tc.want)
		}
		rb, xerr := svc.ReadbackNetNewInboundHandraiser(context.Background(), org, res.LogicalID)
		if xerr != nil {
			t.Fatalf("%s readback: %v", tc.name, xerr)
		}
		if rb.Outcome == NetNewInboundOutcomeAccepted {
			t.Fatalf("%s readback accepted", tc.name)
		}
		if strings.Contains(strings.ToLower(rb.ConflictRef), "corpus") || strings.Contains(strings.ToLower(rb.WhyNow), "parts") {
			t.Fatalf("%s leaked conflict corpus: %+v", tc.name, rb)
		}
	}
	after, _ := repo.ListAccounts(context.Background(), org, repository.OutreachAccountFilter{Limit: 500})
	if len(after) != len(before) {
		t.Fatalf("rejects created accounts: before=%d after=%d", len(before), len(after))
	}
	exp, xerr := svc.ExportSalesContext(context.Background(), org, 50, "")
	if xerr != nil {
		t.Fatal(xerr)
	}
	if len(exp.Items) != 0 {
		t.Fatalf("rejected events leaked to meetcfg: %d", len(exp.Items))
	}
}

func TestNetNewSensitiveDataStoresRefsOnly(t *testing.T) {
	svc, _, org := netNewTestService(t)
	body := validNetNewMap("nnhr-sensitive")
	body["sensitive_data"] = true
	body["person"] = map[string]any{"email": "secret.person@example.test", "name": "Secret Person"}
	body["why_now"] = "do not copy this corpus into analytics"
	res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, body), *netNewConsentAt())
	if xerr != nil {
		t.Fatal(xerr)
	}
	if res.Outcome != NetNewInboundOutcomeAccepted {
		t.Fatalf("sensitive rejected: %+v", res)
	}
	lead, err := svc.inboundStore().GetInboundLeadByLeadID(context.Background(), org, "nnhr-sensitive")
	if err != nil || lead == nil {
		t.Fatal(err)
	}
	raw := strings.ToLower(string(lead.RawPayload))
	if strings.Contains(raw, "secret.person@example.test") || strings.Contains(raw, "secret person") {
		t.Fatalf("raw payload kept sensitive data: %s", lead.RawPayload)
	}
	metrics, _ := json.Marshal(NetNewInboundMetric{Nucleus: res.Nucleus, State: res.Outcome, Reason: res.Reason})
	if strings.Contains(strings.ToLower(string(metrics)), "secret.person") {
		t.Fatalf("metrics leaked PII: %s", metrics)
	}
}

func TestNetNewDownstreamUnavailableThenReplay(t *testing.T) {
	t.Run("action store", func(t *testing.T) {
		svc, repo, org := netNewTestService(t)
		now := *netNewConsentAt()
		body := marshalNetNew(t, validNetNewMap("nnhr-rollback-action"))
		repo.actionUpsertErr = errors.New("commercial action store down")
		first, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, body, now)
		if xerr != nil {
			t.Fatal(xerr)
		}
		if first.Outcome != NetNewInboundOutcomeUnknown || first.ActionID != nil {
			t.Fatalf("action-store failure accepted: %+v", first)
		}
		if first.Reason != NetNewInboundReasonDownstream {
			t.Fatalf("action-store reason=%s", first.Reason)
		}
		lead, err := svc.inboundStore().GetInboundLeadByLeadID(context.Background(), org, "nnhr-rollback-action")
		if err != nil || lead == nil {
			t.Fatalf("receipt: %v", err)
		}
		if netNewReceiptComplete(lead) {
			t.Fatal("downstream UNKNOWN marked complete; replay would skip PersistHandRaise")
		}
		rb, xerr := svc.ReadbackNetNewInboundHandraiser(context.Background(), org, "nnhr-rollback-action")
		if xerr != nil {
			t.Fatal(xerr)
		}
		if rb.Outcome == NetNewInboundOutcomeAccepted {
			t.Fatal("stale readback reported ACCEPTED")
		}
		if rb.Reason != NetNewInboundReasonStale && rb.Reason != NetNewInboundReasonDownstream {
			t.Fatalf("stale reason=%s", rb.Reason)
		}
		repo.actionUpsertErr = nil
		second, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, body, now.Add(time.Minute))
		if xerr != nil {
			t.Fatal(xerr)
		}
		if second.Outcome != NetNewInboundOutcomeAccepted || second.ActionID == nil {
			t.Fatalf("replay after action-store failure: %+v", second)
		}
		actions, err := svc.actionStore().ListCommercialActions(context.Background(), org, *second.AccountID, false, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(actions) != 1 {
			t.Fatalf("action-store rollback/replay produced %d actions", len(actions))
		}
	})
	t.Run("account store", func(t *testing.T) {
		svc, repo, org := netNewTestService(t)
		now := *netNewConsentAt()
		body := marshalNetNew(t, validNetNewMap("nnhr-rollback-account"))
		repo.accountUpsertErr = errors.New("account store down")
		first, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, body, now)
		if xerr != nil {
			t.Fatal(xerr)
		}
		if first.Outcome != NetNewInboundOutcomeUnknown || first.ActionID != nil || first.AccountID != nil {
			t.Fatalf("account-store failure accepted: %+v", first)
		}
		if first.Reason != NetNewInboundReasonDownstream {
			t.Fatalf("account-store reason=%s", first.Reason)
		}
		lead, err := svc.inboundStore().GetInboundLeadByLeadID(context.Background(), org, "nnhr-rollback-account")
		if err != nil || lead == nil {
			t.Fatalf("receipt: %v", err)
		}
		if netNewReceiptComplete(lead) {
			t.Fatal("admit failure marked complete; replay would skip AdmitInboundOnly")
		}
		repo.accountUpsertErr = nil
		second, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, body, now.Add(time.Minute))
		if xerr != nil {
			t.Fatal(xerr)
		}
		if second.Outcome != NetNewInboundOutcomeAccepted || second.ActionID == nil {
			t.Fatalf("replay after account-store failure: %+v", second)
		}
		actions, err := svc.actionStore().ListCommercialActions(context.Background(), org, *second.AccountID, false, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(actions) != 1 {
			t.Fatalf("account-store rollback/replay produced %d actions", len(actions))
		}
	})
}

func TestNetNewTelemetryFailureDoesNotDropOrGrantOutbound(t *testing.T) {
	svc, _, org := netNewTestService(t)
	svc.netNewMetricSink = func(NetNewInboundMetric) { panic("metrics down") }
	res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, validNetNewMap("nnhr-metrics")), *netNewConsentAt())
	if xerr != nil {
		t.Fatal(xerr)
	}
	if res.Outcome != NetNewInboundOutcomeAccepted {
		t.Fatalf("telemetry panic dropped event: %+v", res)
	}
	if res.OutboundEligible || res.AutoSend || res.DispatchAttempted {
		t.Fatal("telemetry failure granted outbound")
	}
}

func TestIntelWatchFactualEventDoesNotCreateHandraiser(t *testing.T) {
	svc, repo, org := netNewTestService(t)
	inbox := &netNewFakeInbox{}
	svc.WireIntelWatchInbox(inbox)
	event := liveintel.OpportunityEvent{
		Schema: liveintel.EventSchemaV1, EventID: "intel-watch-fact-1",
		EventType: liveintel.EventNewOpportunity, SubjectKey: "company:watched",
		OrgID: org, OccurredAt: *netNewConsentAt(),
		Payload: map[string]string{"change": "new bid published"},
	}
	receipt, xerr := svc.IngestOpportunityEvent(context.Background(), org, event, *netNewConsentAt())
	if xerr != nil {
		t.Fatalf("opportunity ingest: %v", xerr)
	}
	if receipt == nil || receipt.EventID != "intel-watch-fact-1" {
		t.Fatalf("receipt: %+v", receipt)
	}
	accs, err := repo.ListAccounts(context.Background(), org, repository.OutreachAccountFilter{Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	for i := range accs {
		if models.AccountIsInboundOnly(&accs[i]) {
			t.Fatal("INTEL_WATCH factual event created inbound-only entity")
		}
	}
	lead, err := svc.inboundStore().GetInboundLeadByLeadID(context.Background(), org, "intel-watch-fact-1")
	if err != nil {
		t.Fatal(err)
	}
	if lead != nil {
		t.Fatal("INTEL_WATCH event created inbound lead/hand-raiser")
	}
	if inbox.n != 1 {
		t.Fatalf("inbox writes=%d", inbox.n)
	}

	watchBody := validNetNewMap("nnhr-watch-src")
	watchBody["source"] = "INTEL_WATCH"
	res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, watchBody), *netNewConsentAt())
	if xerr != nil {
		t.Fatal(xerr)
	}
	if res.Outcome == NetNewInboundOutcomeAccepted || res.ActionID != nil {
		t.Fatalf("INTEL_WATCH source created hand-raiser: %+v", res)
	}
}

func TestNetNewFiveNucleiAcceptedInboundOnly(t *testing.T) {
	svc, repo, org := netNewTestService(t)
	now := *netNewConsentAt()
	if len(NetNewInboundNuclei) != 5 {
		t.Fatalf("closed nuclei want 5 got %d", len(NetNewInboundNuclei))
	}
	ids := map[uuid.UUID]string{}
	for _, nucleus := range NetNewInboundNuclei {
		body := validNetNewMap("nnhr-nucleus-" + nucleus)
		body["nucleus"] = nucleus
		res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, body), now)
		if xerr != nil {
			t.Fatalf("%s: %v", nucleus, xerr)
		}
		if res.Outcome != NetNewInboundOutcomeAccepted {
			t.Fatalf("%s outcome=%s reason=%s", nucleus, res.Outcome, res.Reason)
		}
		if !res.InboundOnly || res.OutboundEligible || res.AutoSend || res.DispatchAttempted {
			t.Fatalf("%s outbound leaked: %+v", nucleus, res)
		}
		if res.AccountID == nil || res.ActionID == nil {
			t.Fatalf("%s missing commercial row: %+v", nucleus, res)
		}
		if _, dup := ids[*res.AccountID]; dup {
			t.Fatalf("%s reused another nucleus account", nucleus)
		}
		ids[*res.AccountID] = nucleus
		acc, err := repo.GetAccount(context.Background(), org, *res.AccountID)
		if err != nil || acc == nil {
			t.Fatalf("%s account: %v", nucleus, err)
		}
		if !models.AccountIsInboundOnly(acc) || FirstTouchEligibleAccount(acc) {
			t.Fatalf("%s first-touch eligible", nucleus)
		}
		actions, err := svc.actionStore().ListCommercialActions(context.Background(), org, acc.ID, false, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(actions) != 1 {
			t.Fatalf("%s want 1 action got %d", nucleus, len(actions))
		}
	}
}

func TestNetNewConformanceFixtureIngest(t *testing.T) {
	raw, err := os.ReadFile("testdata/net_new_inbound_handraiser/conformance.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Envelope json.RawMessage `json:"envelope"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	svc, _, org := netNewTestService(t)
	res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, doc.Envelope, *netNewConsentAt())
	if xerr != nil {
		t.Fatal(xerr)
	}
	if res.Outcome != NetNewInboundOutcomeAccepted {
		t.Fatalf("conformance fixture: %+v", res)
	}
}

type netNewFakeInbox struct{ n int }

func (f *netNewFakeInbox) AppendOpportunityEvent(_ context.Context, _ models.IntelWatchInboxEvent) (bool, error) {
	f.n++
	return true, nil
}

func (f *netNewFakeInbox) ClaimReplayableEvents(_ context.Context, _ uuid.UUID, _ time.Time, _, _ time.Duration, _ int) ([]models.IntelWatchInboxEvent, error) {
	return nil, nil
}
