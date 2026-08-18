package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/api/middleware"
	"github.com/warmbly/warmbly/internal/app/confenge"
	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/errx"
)

type intelHTTPStub struct {
	confenge.Service
	view intel.ExecutiveView
}

func (s *intelHTTPStub) Enabled() bool { return true }
func (s *intelHTTPStub) CommercialExecutiveView(_ context.Context, _ uuid.UUID, month string, includeSynthetic bool) (*intel.ExecutiveView, *errx.Error) {
	v := s.view
	if month != "" {
		v.Month = month
	}
	v.IncludeSynthetic = includeSynthetic
	return &v, nil
}

func TestGetConfengeExecutiveIntelBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	org := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	st := intel.NewMemoryStore()
	intel.LoadSynthetic(st, org.String())
	chains, _ := st.ListChains(org.String())
	view := intel.Rollup(chains, intel.SyntheticMonth, true)
	h := &Handler{ConfengeService: &intelHTTPStub{view: view}}

	req := httptest.NewRequest(http.MethodGet, "/confenge/intel/executive?month=2026-08&include_synthetic=1", nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Set(middleware.OrganizationIDKey, org)
	h.GetConfengeExecutiveIntel(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var wrap struct {
		Data intel.ExecutiveView `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &wrap); err != nil {
		t.Fatal(err)
	}
	body := w.Body.String()
	for _, field := range []string{
		"inbound_qualified_pipeline", "qco", "conversations", "meetings",
		"proposals", "pipeline", "won", "lost", "unknown",
	} {
		if !jsonHasKey(body, field) {
			t.Fatalf("body missing %s: %s", field, body)
		}
	}
	if wrap.Data.InboundQualifiedPipeline == 0 || wrap.Data.QCO == 0 {
		t.Fatalf("monthly fields empty: %+v", wrap.Data)
	}
	if wrap.Data.CausalProof {
		t.Fatal("HTTP payload claimed causal proof")
	}
	fmt.Printf("HTTP_EXEC status=%d iqp=%d qco=%d won=%d lost=%d unknown=%d\n",
		w.Code, wrap.Data.InboundQualifiedPipeline, wrap.Data.QCO, wrap.Data.Won, wrap.Data.Lost, wrap.Data.Unknown)
}

type commercialHTTPStub struct {
	confenge.Service
	op  intel.OperatorResult
	can intel.CanonicalState
	ack intel.WebhookAck
}

func (s *commercialHTTPStub) Enabled() bool { return true }
func (s *commercialHTTPStub) ApplyCommercialOperator(_ context.Context, _ uuid.UUID, req intel.OperatorRequest) (intel.OperatorResult, *errx.Error) {
	if strings.Contains(req.Query, "@") {
		return intel.OperatorResult{Rejected: true, Reason: "pii"}, nil
	}
	return s.op, nil
}
func (s *commercialHTTPStub) GetCommercialCanonical(_ context.Context, _ uuid.UUID, lead string) (*intel.CanonicalState, *errx.Error) {
	c := s.can
	c.LeadID = lead
	return &c, nil
}

func TestConfengeCommercialOperatorRequiresOrg(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{ConfengeService: &commercialHTTPStub{}}
	req := httptest.NewRequest(http.MethodPost, "/confenge/intel/commercial", strings.NewReader(`{"action":"register_snapshot"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	h.ApplyConfengeCommercialOperator(c)
	if w.Code == http.StatusCreated || w.Code == http.StatusOK {
		t.Fatalf("unauthenticated operator write accepted status=%d", w.Code)
	}
	fmt.Printf("OPERATOR_UNAUTH status=%d\n", w.Code)
}

func TestConfengeCommercialOperatorRegister(t *testing.T) {
	gin.SetMode(gin.TestMode)
	org := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	st := intel.NewMemoryStore()
	off, _ := intel.FrozenOffer(intel.OfferDiagnostico)
	op := intel.ApplyOperator(st, org.String(), intel.OperatorRequest{
		Action: intel.OpRegisterSnapshot, LeadID: "lead-http-1", Offer: off,
		Capacity: intel.CapacitySnapshot{State: intel.CapacityStateOK}, ActorRef: "op", Synthetic: true,
		OccurredAt: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
	})
	h := &Handler{ConfengeService: &commercialHTTPStub{op: op}}
	body := `{"action":"register_snapshot","lead_id":"lead-http-1","offer":{"offer_id":"CFG-DIAG-EXP-v1"}}`
	req := httptest.NewRequest(http.MethodPost, "/confenge/intel/commercial", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Set(middleware.OrganizationIDKey, org)
	h.ApplyConfengeCommercialOperator(c)
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "@") && strings.Contains(w.Body.String(), "email=") {
		t.Fatal("PII in operator response")
	}
	fmt.Printf("OPERATOR_HTTP status=%d\n", w.Code)
}

type intelQueueHTTPStub struct {
	confenge.Service
	svc confenge.Service
}

func (s *intelQueueHTTPStub) Enabled() bool { return true }
func (s *intelQueueHTTPStub) ListIntelExceptions(ctx context.Context, orgID uuid.UUID, filter intel.ExceptionFilter) ([]intel.Exception, *errx.Error) {
	return s.svc.ListIntelExceptions(ctx, orgID, filter)
}
func (s *intelQueueHTTPStub) GetIntelException(ctx context.Context, orgID uuid.UUID, id string) (*intel.Exception, *errx.Error) {
	return s.svc.GetIntelException(ctx, orgID, id)
}
func (s *intelQueueHTTPStub) ResolveIntelException(ctx context.Context, orgID uuid.UUID, id string, req intel.ResolveRequest) (intel.ResolveResult, *errx.Error) {
	return s.svc.ResolveIntelException(ctx, orgID, id, req)
}

func newIntelQueueService(t *testing.T) (confenge.Service, uuid.UUID) {
	t.Helper()
	cfg := confenge.Config{Enabled: true, RequireHumanApproval: true}
	svc := confenge.NewService(cfg, nil, nil)
	svc.WireIntel(nil)
	org := uuid.MustParse(intel.OperatorQueueOrgID)
	// Seed through the shipped reconcile + fixture put by listing after
	// a service-level load is not exported; drive Reconcile for a live path
	// and List/Get/Resolve for the operator path via the service methods
	// after putting fixtures through intel.LoadOperatorQueue on a memory
	// store the service owns. Reconcile an orphan so classify is exercised.
	complete := intel.ObservedFacts{
		Keys: intel.JoinKeys{
			OrganizationID: org.String(), Source: "web-cfg", LeadID: "webcfg-syn-in-1",
			ReceiptID: "rcpt-syn-in-1", AccountID: "extra-acc-norte", ActionID: "act-syn-in-1",
			OutcomeID: "out-syn-in-1", RouteFamily: intel.FamilyInbound,
			TargetFitVersion: "tf-v1", ActivationPolicyVersion: "ap-v1",
		},
		OutcomeType: intel.OutcomeQualifiedConversation, Qualified: true,
		Synthetic: true, Label: intel.LabelSynthetic,
	}
	if _, xerr := svc.ReconcileCommercialIntel(context.Background(), org, complete); xerr != nil {
		t.Fatal(xerr)
	}
	orphan := complete
	orphan.Keys.LeadID = ""
	orphan.Keys.ReceiptID = ""
	orphan.Keys.ActionID = ""
	orphan.Keys.IdempotencyKey = ""
	orphan.Keys.OutcomeID = "out-http-orphan"
	orphan.OutcomeType = intel.OutcomeMeeting
	if _, xerr := svc.ReconcileCommercialIntel(context.Background(), org, orphan); xerr != nil {
		t.Fatal(xerr)
	}
	return svc, org
}

func TestListAndResolveIntelExceptionsHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc, org := newIntelQueueService(t)
	h := &Handler{ConfengeService: &intelQueueHTTPStub{svc: svc}}

	req := httptest.NewRequest(http.MethodGet, "/confenge/intel/exceptions?type=orphan&lane=inbound&source=web-cfg&severity=high", nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Set(middleware.OrganizationIDKey, org)
	h.ListConfengeIntelExceptions(c)
	if w.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", w.Code, w.Body.String())
	}
	var listed struct {
		Data []intel.Exception `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Data) == 0 {
		t.Fatalf("filtered list empty: %s", w.Body.String())
	}
	ex := listed.Data[0]
	if ex.Code != intel.ExceptionOrphan || ex.Lane != intel.FamilyInbound || ex.Severity != intel.SeverityHigh {
		t.Fatalf("filter mismatch: %+v", ex)
	}
	if ex.NextAction == "" || len(ex.Evidence) == 0 {
		t.Fatalf("list item missing next/evidence: %+v", ex)
	}

	req = httptest.NewRequest(http.MethodGet, "/confenge/intel/exceptions/"+ex.ID, nil)
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = req
	c.Params = gin.Params{{Key: "id", Value: ex.ID}}
	c.Set(middleware.OrganizationIDKey, org)
	h.GetConfengeIntelException(c)
	if w.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", w.Code, w.Body.String())
	}

	body := `{"action":"defer","actor":"http-op","reason":"wait for action"}`
	req = httptest.NewRequest(http.MethodPost, "/confenge/intel/exceptions/"+ex.ID+"/resolve", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "defer-"+ex.ID)
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = req
	c.Params = gin.Params{{Key: "id", Value: ex.ID}}
	c.Set(middleware.OrganizationIDKey, org)
	h.ResolveConfengeIntelException(c)
	if w.Code != http.StatusOK {
		t.Fatalf("resolve status=%d body=%s", w.Code, w.Body.String())
	}
	var resolved struct {
		Data intel.ResolveResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.Data.After.Status != intel.StatusDeferred || resolved.Data.Actor != "http-op" {
		t.Fatalf("resolve payload: %+v", resolved.Data)
	}

	req = httptest.NewRequest(http.MethodPost, "/confenge/intel/exceptions/"+ex.ID+"/resolve", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "defer-"+ex.ID)
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = req
	c.Params = gin.Params{{Key: "id", Value: ex.ID}}
	c.Set(middleware.OrganizationIDKey, org)
	h.ResolveConfengeIntelException(c)
	if w.Code != http.StatusOK {
		t.Fatalf("replay status=%d body=%s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resolved); err != nil {
		t.Fatal(err)
	}
	if !resolved.Data.Replay {
		t.Fatal("HTTP replay was not a no-op")
	}

	invent := `{"action":"defer","actor":"http-op","reason":"book it","outcome_type":"WON"}`
	req = httptest.NewRequest(http.MethodPost, "/confenge/intel/exceptions/"+ex.ID+"/resolve", strings.NewReader(invent))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = req
	c.Params = gin.Params{{Key: "id", Value: ex.ID}}
	c.Set(middleware.OrganizationIDKey, org)
	h.ResolveConfengeIntelException(c)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invent WON status=%d body=%s", w.Code, w.Body.String())
	}
	fmt.Printf("HTTP_QUEUE list=%d defer=%s replay=%v invent=%d\n",
		len(listed.Data), resolved.Data.After.Status, resolved.Data.Replay, w.Code)
}

func jsonHasKey(body, key string) bool {
	needle := `"` + key + `"`
	for i := 0; i+len(needle) <= len(body); i++ {
		if body[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

type scoreboardHTTPStub struct {
	confenge.Service
	board intel.Scoreboard
	join  intel.JoinResult
}

func (s *scoreboardHTTPStub) Enabled() bool { return true }
func (s *scoreboardHTTPStub) TruthScoreboard(_ context.Context, _ uuid.UUID, _ string, includeSynthetic bool) (*intel.Scoreboard, *errx.Error) {
	b := s.board
	b.IncludeSynthetic = includeSynthetic
	return &b, nil
}
func (s *scoreboardHTTPStub) HumanOutcomeEnvelopes() []intel.HumanOutcomeEnvelope {
	return intel.EmptyEnvelopes()
}
func (s *scoreboardHTTPStub) RegisterHumanOutcome(_ context.Context, _ uuid.UUID, in intel.HumanOutcomeEntry) (intel.JoinResult, *errx.Error) {
	if s.join.Chain.Identity == "" {
		return intel.RegisterHumanOutcome(intel.NewMemoryStore(), in), nil
	}
	return s.join, nil
}

func TestGetConfengeTruthScoreboardBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	org := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	board := intel.ProjectScoreboard(intel.ScoreboardSources{
		Now:                time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC),
		InboundHealthReady: true, Executive: intel.Rollup(nil, "2026-08", false),
	})
	h := &Handler{ConfengeService: &scoreboardHTTPStub{board: board}}
	req := httptest.NewRequest(http.MethodGet, "/confenge/intel/scoreboard?include_synthetic=0", nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Set(middleware.OrganizationIDKey, org)
	h.GetConfengeTruthScoreboard(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var wrap struct {
		Data intel.Scoreboard `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &wrap); err != nil {
		t.Fatal(err)
	}
	if len(wrap.Data.Stages) != 7 {
		t.Fatalf("stages=%d", len(wrap.Data.Stages))
	}
	if wrap.Data.IncludeSynthetic || wrap.Data.CausalProof {
		t.Fatal("HTTP scoreboard leaked synthetic or causal_proof")
	}
	fmt.Printf("HTTP_SCOREBOARD status=%d stages=%d path=%s\n", w.Code, len(wrap.Data.Stages), wrap.Data.ProductionPath)
}

func TestListConfengeHumanEnvelopes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	org := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	h := &Handler{ConfengeService: &scoreboardHTTPStub{}}
	req := httptest.NewRequest(http.MethodGet, "/confenge/intel/human-envelopes", nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Set(middleware.OrganizationIDKey, org)
	h.ListConfengeHumanEnvelopes(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var wrap struct {
		Data []intel.HumanOutcomeEnvelope `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &wrap); err != nil {
		t.Fatal(err)
	}
	if len(wrap.Data) != 4 {
		t.Fatalf("envelopes=%d", len(wrap.Data))
	}
	for _, env := range wrap.Data {
		if env.LeadID != "" || env.AccountID != "" || env.InventedIDs {
			t.Fatalf("invented envelope: %+v", env)
		}
	}
}

func TestRecordConfengeHumanOutcomeRejectsBareWon(t *testing.T) {
	gin.SetMode(gin.TestMode)
	org := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	h := &Handler{ConfengeService: &scoreboardHTTPStub{}}
	req := httptest.NewRequest(http.MethodPost, "/confenge/intel/human-outcomes", strings.NewReader(`{"lead_id":"lead-http-won","action":"won"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Set(middleware.OrganizationIDKey, org)
	h.RecordConfengeHumanOutcome(c)
	if w.Code == http.StatusCreated || w.Code == http.StatusOK {
		t.Fatalf("bare WON accepted status=%d body=%s", w.Code, w.Body.String())
	}
	fmt.Printf("HTTP_HUMAN_WON_BARE status=%d\n", w.Code)
}
