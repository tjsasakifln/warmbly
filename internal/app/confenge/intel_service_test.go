package confenge

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/app/confenge/proposal"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

func TestCommercialExecutiveViewEmptyReal(t *testing.T) {
	svc, _, org := inboundTestService(t)
	view, xerr := svc.CommercialExecutiveView(context.Background(), org, intel.SyntheticMonth, false)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if view.InboundQualifiedPipeline != 0 || view.QCO != 0 || view.Won != 0 || !view.RealEmpty {
		t.Fatalf("empty real view invented data: %+v", view)
	}
	fmt.Printf("SERVICE_REAL_EMPTY iqp=%d qco=%d real_empty=%v\n", view.InboundQualifiedPipeline, view.QCO, view.RealEmpty)
}

func TestAcquisitionOutcomeFeedbackDoesNotMaterializeReadModel(t *testing.T) {
	svc, _, org := inboundTestService(t)
	now := time.Date(2026, 8, 31, 15, 0, 0, 0, time.UTC)
	proposalStore := proposal.NewMemoryStore()
	proposalID := uuid.MustParse("11111111-2222-4333-8444-555555555555")
	if _, _, err := proposalStore.Apply(context.Background(), proposal.Mutation{
		OrganizationID: org, IdempotencyKey: "feedback-readonly-proposal", PayloadHash: "hash", Insert: true,
		Proposal: proposal.Proposal{
			SchemaVersion: proposal.ProposalSchemaVersion, ProposalID: proposalID, ProposalVersion: 1,
			OrganizationID: org, AccountID: "extra-account-readonly-1", OpportunityID: "opportunity-readonly-1",
			SourceLeadID: "webcfg-feedback-readonly-1", CorrelationID: "corr-feedback-readonly-1",
			DecisionState: proposal.StateSent, Amount: 100_000, Currency: "BRL", SentAt: &now,
		},
	}); err != nil {
		t.Fatal(err)
	}
	svc.proposalFacts = proposalStore
	body := []byte(`{
		"lead_id":"webcfg-feedback-readonly-1","receipt_id":"rcpt-feedback-readonly-1",
		"created_at":"2026-08-20T12:00:00Z","source":"CONFENGE_WEB","route_family":"inbound",
		"asset_id":"defesa-margem","cta_id":"diagnostico","landing_url":"https://confenge.com.br/defesa-margem",
		"entity_public_id":"extra-account-readonly-1","consent":{"granted":true},
		"utm":{"organic_source":"organic_search","intent_class":"reequilibrio"}
	}`)
	if _, xerr := svc.IngestInboundLead(context.Background(), org, body, IngestOptions{Now: now}); xerr != nil {
		t.Fatal(xerr)
	}
	before, err := svc.intelStore().ListChains(org.String())
	if err != nil || len(before) == 0 {
		t.Fatalf("precondition chains=%d err=%v", len(before), err)
	}
	beforeRaw, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	proposalsBefore, err := proposalStore.ListOutcomeFeedback(context.Background(), org)
	if err != nil {
		t.Fatal(err)
	}
	proposalsBeforeRaw, err := json.Marshal(proposalsBefore)
	if err != nil {
		t.Fatal(err)
	}
	period := intel.OutcomeFeedbackPeriod{
		From: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}
	report, xerr := svc.AcquisitionOutcomeFeedback(context.Background(), org, period, false)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if len(report.Rows) != 1 || report.Rows[0].Cohort != intel.FeedbackCohortWithheld {
		t.Fatalf("read projection missing or unsafe: %+v", report)
	}
	after, err := svc.intelStore().ListChains(org.String())
	if err != nil {
		t.Fatalf("read-only endpoint materialized chains=%d err=%v", len(after), err)
	}
	afterRaw, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	if string(beforeRaw) != string(afterRaw) {
		t.Fatalf("read-only endpoint changed durable chains\nbefore=%s\nafter=%s", beforeRaw, afterRaw)
	}
	proposalsAfter, err := proposalStore.ListOutcomeFeedback(context.Background(), org)
	if err != nil {
		t.Fatal(err)
	}
	proposalsAfterRaw, err := json.Marshal(proposalsAfter)
	if err != nil {
		t.Fatal(err)
	}
	if string(proposalsBeforeRaw) != string(proposalsAfterRaw) {
		t.Fatalf("read-only endpoint changed native proposals\nbefore=%s\nafter=%s", proposalsBeforeRaw, proposalsAfterRaw)
	}
}

func TestProviderWebhookErrorsUseStableHTTPClass(t *testing.T) {
	svc, _, org := inboundTestService(t)
	now := time.Now().UTC()
	secret := "sandbox-secret"
	malformed := []byte(`{"id":`)
	malformedSignature := intel.SignProviderHMAC(secret, now, malformed)
	if _, xerr := svc.IngestProviderWebhook(context.Background(), org, secret, "", malformedSignature, malformed); xerr == nil || xerr.Code != errx.Unprocessable {
		t.Fatalf("signed malformed payload should be 422, got %v", xerr)
	}

	valid := []byte(`{"id":"evt-service-503","event":"PAYMENT_CREATED","dateCreated":"2026-08-25T12:00:00Z"}`)
	if _, xerr := svc.IngestProviderWebhook(context.Background(), org, secret, "", "t=1,v1=dead", valid); xerr == nil || xerr.Code != errx.Unauthorized {
		t.Fatalf("invalid signature should be 401, got %v", xerr)
	}

	unavailable := intel.NewMemoryStore()
	unavailable.SetUnavailable(true)
	svc.intel = unavailable
	validSignature := intel.SignProviderHMAC(secret, now, valid)
	if _, xerr := svc.IngestProviderWebhook(context.Background(), org, secret, "", validSignature, valid); xerr == nil || xerr.Code != errx.ServiceUnavailable {
		t.Fatalf("receipt-store outage should be 503, got %v", xerr)
	}
}

func TestCommercialExecutiveViewExcludesIngestedSyntheticQAInternal(t *testing.T) {
	svc, _, org := inboundTestService(t)
	now := time.Date(2026, 8, 15, 21, 0, 0, 0, time.UTC)
	month := "2026-08"
	bodies := [][]byte{
		[]byte(`{"lead_id":"SYNTHETIC-INBOUND-20260815T210000Z","receipt_id":"SYNTHETIC-INBOUND-20260815T210000Z","source":"CONFENGE_WEB","company":"SYNTHETIC-INBOUND","email":"synthetic-inbound@example.com","message":"SYNTHETIC-INBOUND do not contact"}`),
		[]byte(`{"lead_id":"qa-lead-exec-1","receipt_id":"qa-lead-exec-1","source":"qa","company":"QA Fixture"}`),
		[]byte(`{"lead_id":"internal-probe-exec","receipt_id":"internal-probe-exec","label":"internal","company":"Ops"}`),
	}
	for _, body := range bodies {
		if _, xerr := svc.IngestInboundLead(context.Background(), org, body, IngestOptions{Now: now}); xerr != nil {
			t.Fatal(xerr)
		}
	}
	queue, xerr := svc.CollectInboundNow(context.Background(), org)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if len(queue) != 0 {
		t.Fatalf("commercial INBOUND NOW leaked skipped receipts: %+v", queue)
	}
	view, xerr := svc.CommercialExecutiveView(context.Background(), org, month, false)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if !view.RealEmpty || view.ChainCount != 0 || view.InboundQualifiedPipeline != 0 || view.QCO != 0 || view.Conversations != 0 || view.Meetings != 0 || view.Proposals != 0 || view.Pipeline != 0 || view.Won != 0 || view.Lost != 0 || view.Denominators.Leads != 0 {
		t.Fatalf("synthetic/qa/internal leaked into include_synthetic=0: %+v", view)
	}
	labeled, xerr := svc.CommercialExecutiveView(context.Background(), org, month, true)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if labeled.ChainCount == 0 {
		t.Fatal("include_synthetic=1 must still see the labeled receipts")
	}
	fmt.Printf("EXEC_SKIP_SYNTHETIC real_empty=%v iqp=%d leads=%d labeled_chains=%d auto_send=%v\n",
		view.RealEmpty, view.InboundQualifiedPipeline, view.Denominators.Leads, labeled.ChainCount, svc.cfg.AutoSendEnabled)
}

func TestCommercialExecutiveViewPromotesLegacySyntheticChain(t *testing.T) {
	svc, repo, org := inboundTestService(t)
	now := time.Date(2026, 8, 15, 21, 30, 0, 0, time.UTC)
	leadID := "SYNTHETIC-INBOUND-legacy-1"
	legacy := intel.ObservedFacts{
		Keys: intel.JoinKeys{
			OrganizationID: org.String(),
			Source:         "CONFENGE_WEB",
			LeadID:         leadID,
			ReceiptID:      leadID,
			RouteFamily:    intel.FamilyInbound,
		},
		LeadCreatedAt: now,
		IngestedAt:    now,
		Synthetic:     false,
		Label:         intel.LabelReal,
	}
	seed, xerr := svc.ReconcileCommercialIntel(context.Background(), org, legacy)
	if xerr != nil || !seed.Created || seed.Chain.Synthetic {
		t.Fatalf("seed legacy REAL: %+v %v", seed, xerr)
	}
	contaminated, xerr := svc.CommercialExecutiveView(context.Background(), org, "2026-08", false)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if contaminated.ChainCount == 0 {
		t.Fatal("legacy REAL identity should contaminate include_synthetic=0 before receipt observe")
	}

	body := []byte(`{"lead_id":"SYNTHETIC-INBOUND-legacy-1","receipt_id":"SYNTHETIC-INBOUND-legacy-1","source":"CONFENGE_WEB","company":"SYNTHETIC-INBOUND","email":"synthetic-inbound@example.com","message":"SYNTHETIC-INBOUND do not contact"}`)
	if _, xerr := svc.IngestInboundLead(context.Background(), org, body, IngestOptions{Now: now}); xerr != nil {
		t.Fatal(xerr)
	}
	queue, xerr := svc.CollectInboundNow(context.Background(), org)
	if xerr != nil {
		t.Fatal(xerr)
	}
	for _, item := range queue {
		if item.LeadID == leadID {
			t.Fatal("official synthetic receipt entered INBOUND NOW")
		}
	}
	cleaned, xerr := svc.CommercialExecutiveView(context.Background(), org, "2026-08", false)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if !cleaned.RealEmpty || cleaned.ChainCount != 0 {
		t.Fatalf("observeExisting must promote legacy chain out of include_synthetic=0: %+v", cleaned)
	}
	labeled, xerr := svc.CommercialExecutiveView(context.Background(), org, "2026-08", true)
	if xerr != nil || labeled.ChainCount == 0 {
		t.Fatalf("labeled path must keep promoted chain: %+v %v", labeled, xerr)
	}
	stored, _ := svc.intelStore().GetChain(org.String(), "lead:"+leadID)
	if stored == nil || !stored.Synthetic || stored.Label != intel.LabelSynthetic {
		t.Fatalf("legacy chain not promoted: %+v", stored)
	}
	fmt.Printf("LEGACY_PROMOTE identity=lead:%s synthetic=%v include_synthetic=0 empty=%v\n", leadID, stored.Synthetic, cleaned.RealEmpty)
	_ = repo
}

func TestCommercialIntelJoinAndLearningViaService(t *testing.T) {
	svc, _, org := inboundTestService(t)
	facts := intel.SyntheticFacts(org.String())[1]
	res, xerr := svc.ReconcileCommercialIntel(context.Background(), org, facts)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if !res.Created {
		t.Fatal("first service join should create")
	}
	again, xerr := svc.ReconcileCommercialIntel(context.Background(), org, facts)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if again.Created || !again.Replay {
		t.Fatal("service replay must return the first chain")
	}
	view, xerr := svc.CommercialExecutiveView(context.Background(), org, intel.SyntheticMonth, true)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if view.QCO == 0 {
		t.Fatalf("synthetic QCO missing: %+v", view)
	}
	cand, xerr := svc.RecordIntelLearning(context.Background(), org, intel.LearningInput{
		From: intel.LearningFromCorrection, CorrectionCodes: []string{"wrong_service"},
		Keys: facts.Keys, Synthetic: true,
	})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if cand.Target != intel.TargetOffer || len(cand.UpstreamWrites) != 0 {
		t.Fatalf("learning via service: %+v", cand)
	}
	fmt.Printf("SERVICE_JOIN identity=%s replay=%v qco=%d learning_target=%s\n",
		res.Chain.Identity, again.Replay, view.QCO, cand.Target)
}

func TestObserveFromInboundKeepsIDsOnly(t *testing.T) {
	svc, repo, org := inboundTestService(t)
	now := parseFlexibleTime("2026-08-14T14:55:00Z")
	body := []byte(`{
		"lead_id":"webcfg-obs-1","receipt_id":"rcpt-obs-1","created_at":"2026-08-14T14:55:00Z",
		"source":"web-cfg","route_family":"inbound","asset_id":"landing-segunda-leitura",
		"cta_id":"segunda-leitura-contrato","entity_public_id":"extra-obs",
		"company":"Construtora Norte","name":"Ana Souza","email":"ana.souza@norte.example",
		"phone":"+5541999887766","correlation_id":"corr-obs-1",
		"utm":{"source":"google","campaign":"segunda-leitura"}
	}`)
	ing, xerr := svc.IngestInboundLead(context.Background(), org, body, IngestOptions{Now: now})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if ing.Lead == nil {
		t.Fatal("ingest dropped lead")
	}
	facts := intel.ObserveFromInbound(*ing.Lead, nil, ing.Action, nil)
	if facts.Keys.LeadID != "webcfg-obs-1" || facts.Keys.ReceiptID != "rcpt-obs-1" {
		t.Fatalf("inbound IDs not copied: %+v", facts.Keys)
	}
	if facts.Keys.AssetID != "landing-segunda-leitura" || facts.Keys.CorrelationID != "corr-obs-1" {
		t.Fatalf("web-cfg attribution not copied: %+v", facts.Keys)
	}
	key := intel.MetricKey(facts.Keys)
	if intel.MetricKeyContainsPII(key) {
		t.Fatalf("observe metric key has PII: %s", key)
	}
	raw, _ := json.Marshal(facts.Keys)
	if containsAny(string(raw), "ana.souza@norte.example", "+5541999887766", "Ana Souza") {
		t.Fatalf("PII entered join keys: %s", raw)
	}
	fmt.Printf("OBSERVE lead=%s receipt=%s asset=%s metric_pii=false\n",
		facts.Keys.LeadID, facts.Keys.ReceiptID, facts.Keys.AssetID)
	_ = repo
}

func TestObserveExistingJoinsOutcomeByLeadIDOnly(t *testing.T) {
	svc, repo, org := inboundTestService(t)
	now := time.Date(2026, 8, 14, 15, 0, 0, 0, time.UTC)
	sharedEmail := "shared@empresa.com"
	sharedCNPJ := "11222333000181"
	leadA := &models.OutreachInboundLead{
		OrganizationID: org, LeadID: "webcfg-join-a", ReceiptID: "rcpt-a",
		LeadCreatedAt: now, WarmblyIngestedAt: now,
		LeadEmail: sharedEmail, CNPJ14: sharedCNPJ,
		RouteFamily: "inbound", Status: models.InboundStatusOpen,
		EnrichmentStatus: models.InboundEnrichmentUnknown, Owner: models.InboundOwnerUnknown,
	}
	leadB := &models.OutreachInboundLead{
		OrganizationID: org, LeadID: "webcfg-join-b", ReceiptID: "rcpt-b",
		LeadCreatedAt: now, WarmblyIngestedAt: now,
		LeadEmail: sharedEmail, CNPJ14: sharedCNPJ,
		RouteFamily: "inbound", Status: models.InboundStatusOpen,
		EnrichmentStatus: models.InboundEnrichmentUnknown, Owner: models.InboundOwnerUnknown,
	}
	if _, _, err := repo.InsertInboundLead(context.Background(), leadA); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.InsertInboundLead(context.Background(), leadB); err != nil {
		t.Fatal(err)
	}
	if err := repo.EnqueueOutcome(context.Background(), &models.OutreachOutcome{
		OrganizationID: org, SourceLeadID: "webcfg-join-a", EventType: intel.OutcomeQualifiedConversation,
		ContactEmail: sharedEmail, CNPJ14: sharedCNPJ, OccurredAt: now, IdempotencyKey: "out-a",
		EventID: uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.EnqueueOutcome(context.Background(), &models.OutreachOutcome{
		OrganizationID: org, SourceLeadID: "webcfg-join-b", EventType: intel.OutcomeMeeting,
		ContactEmail: sharedEmail, CNPJ14: sharedCNPJ, OccurredAt: now.Add(time.Hour), IdempotencyKey: "out-b",
		EventID: uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"),
	}); err != nil {
		t.Fatal(err)
	}

	svc.observeExisting(context.Background(), org)
	chains, err := svc.intelStore().ListChains(org.String())
	if err != nil {
		t.Fatal(err)
	}
	byLead := map[string]intel.Chain{}
	for _, c := range chains {
		byLead[c.LeadID] = c
	}
	a, okA := byLead["webcfg-join-a"]
	b, okB := byLead["webcfg-join-b"]
	if !okA || !okB {
		t.Fatalf("missing chains a=%v b=%v have=%d", okA, okB, len(chains))
	}
	if a.OutcomeType == intel.OutcomeMeeting {
		t.Fatal("lead A absorbed lead B meeting via email/cnpj")
	}
	if a.OutcomeType != intel.OutcomeQualifiedConversation {
		t.Fatalf("lead A outcome=%s want QCO (old outbox filter would drop it)", a.OutcomeType)
	}
	if b.OutcomeType != intel.OutcomeMeeting {
		t.Fatalf("lead B outcome=%s want MEETING", b.OutcomeType)
	}
	fmt.Printf("OBSERVE_EXISTING a=%s b=%s email_or_rejected=true qco_visible=true\n", a.OutcomeType, b.OutcomeType)
}

func TestIntelExceptionQueueListAndResolve(t *testing.T) {
	svc, _, org := inboundTestService(t)
	intel.LoadOperatorQueue(svc.intelStore(), org.String())
	ctx := context.Background()
	orphans, xerr := svc.ListIntelExceptions(ctx, org, intel.ExceptionFilter{Type: intel.ExceptionOrphan})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if len(orphans) == 0 {
		t.Fatal("service list missed orphans")
	}
	got, xerr := svc.GetIntelException(ctx, org, orphans[0].ID)
	if xerr != nil || got == nil {
		t.Fatalf("get: %v", xerr)
	}
	if got.NextAction == "" || len(got.Evidence) == 0 {
		t.Fatalf("detail incomplete: %+v", got)
	}
	res, xerr := svc.ResolveIntelException(ctx, org, orphans[0].ID, intel.ResolveRequest{
		Action: intel.ResolveDefer, Actor: "svc-test", Reason: "wait for action id",
	})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if res.After.Status != intel.StatusDeferred {
		t.Fatalf("defer status=%s", res.After.Status)
	}
	replay, xerr := svc.ResolveIntelException(ctx, org, orphans[0].ID, intel.ResolveRequest{
		Action: intel.ResolveDefer, Actor: "svc-test", Reason: "wait for action id",
	})
	if xerr != nil || !replay.Replay {
		t.Fatalf("replay: %v %+v", xerr, replay)
	}
	_, xerr = svc.ResolveIntelException(ctx, org, orphans[0].ID, intel.ResolveRequest{
		Action: intel.ResolveReject, Actor: "svc-test", Reason: "force", OutcomeType: intel.OutcomeWon,
	})
	if xerr == nil {
		t.Fatal("invent WON must fail closed")
	}
	fmt.Printf("SERVICE_QUEUE orphan=%s deferred=%s replay=%v\n", orphans[0].ID, res.After.Status, replay.Replay)
}

func containsAny(s string, parts ...string) bool {
	for _, p := range parts {
		if len(p) > 0 && indexOfStr(s, p) >= 0 {
			return true
		}
	}
	return false
}
func indexOfStr(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
