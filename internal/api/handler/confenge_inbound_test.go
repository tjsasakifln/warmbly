package handler

import (
	"bytes"
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

	"github.com/warmbly/warmbly/internal/app/confenge"
	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/app/confenge/liveintel"
	"github.com/warmbly/warmbly/internal/errx"
)

type inboundHTTPStub struct {
	confenge.Service
	cfg       confenge.Config
	gotRaw    []byte
	gotNetNew []byte
	gotOpp    bool
	ingest    func(raw []byte, opts confenge.IngestOptions) (*confenge.InboundIngestResult, *errx.Error)
	netNew    func(raw []byte) (*confenge.NetNewInboundResult, *errx.Error)
	obsStore  *intel.MemoryStore
}

func (s *inboundHTTPStub) Enabled() bool           { return true }
func (s *inboundHTTPStub) Config() confenge.Config { return s.cfg }
func (s *inboundHTTPStub) IngestSearchObservation(_ context.Context, _ uuid.UUID, raw []byte, opts confenge.IngestOptions) (*intel.SearchObservationReceipt, *errx.Error) {
	if xerr := confenge.RejectInboundQueryPII(opts.Query); xerr != nil {
		return nil, xerr
	}
	if s.cfg.AutoSendEnabled {
		return nil, errx.New(errx.Forbidden, "inbound receive is refused while auto_send is enabled")
	}
	obs, err := intel.ParseSearchObservation(raw, s.cfg.InboundOrgID.String(), time.Now().UTC())
	if err != nil {
		return nil, errx.New(errx.BadRequest, err.Error())
	}
	if s.obsStore == nil {
		s.obsStore = intel.NewMemoryStore()
	}
	rec, err := intel.PersistSearchObservation(s.obsStore, obs, time.Now().UTC())
	if err != nil {
		return nil, errx.New(errx.BadRequest, err.Error())
	}
	return &rec, nil
}

func (s *inboundHTTPStub) IngestCommercialEvent(_ context.Context, orgID uuid.UUID, ev intel.CommercialEvent) (intel.JoinResult, *errx.Error) {
	if s.obsStore == nil {
		s.obsStore = intel.NewMemoryStore()
	}
	if ev.OrganizationID == "" {
		ev.OrganizationID = orgID.String()
	}
	res := intel.IngestEvent(s.obsStore, ev)
	if intel.JoinUnavailable(res) && !res.Replay {
		return res, errx.New(errx.ServiceUnavailable, "commercial intel store unavailable")
	}
	return res, nil
}

func (s *inboundHTTPStub) IngestInboundLead(_ context.Context, _ uuid.UUID, raw []byte, opts confenge.IngestOptions) (*confenge.InboundIngestResult, *errx.Error) {
	if xerr := confenge.RejectInboundQueryPII(opts.Query); xerr != nil {
		return nil, xerr
	}
	s.gotRaw = append([]byte(nil), raw...)
	if s.ingest != nil {
		return s.ingest(raw, opts)
	}
	return &confenge.InboundIngestResult{
		NextAction:        "CALL",
		DispatchAttempted: false,
	}, nil
}

func (s *inboundHTTPStub) IngestNetNewInboundHandraiser(_ context.Context, _ uuid.UUID, raw []byte, _ time.Time) (*confenge.NetNewInboundResult, *errx.Error) {
	s.gotNetNew = append([]byte(nil), raw...)
	if s.netNew != nil {
		return s.netNew(raw)
	}
	return &confenge.NetNewInboundResult{
		Schema:            confenge.NetNewInboundHandraiserSchema,
		LogicalID:         "stub-logical",
		Outcome:           confenge.NetNewInboundOutcomeAccepted,
		Receipt:           "stub-receipt",
		InboundOnly:       true,
		DispatchAttempted: false,
	}, nil
}

func (s *inboundHTTPStub) ReadbackNetNewInboundHandraiser(_ context.Context, _ uuid.UUID, logicalID string) (*confenge.NetNewInboundReadback, *errx.Error) {
	at := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	return &confenge.NetNewInboundReadback{NetNewInboundResult: confenge.NetNewInboundResult{
		Schema:         confenge.NetNewInboundHandraiserSchema,
		PolicyVersion:  confenge.NetNewInboundHandraiserSchema,
		LogicalID:      logicalID,
		Receipt:        "stub-receipt",
		Outcome:        confenge.NetNewInboundOutcomeAccepted,
		AcknowledgedBy: confenge.NetNewInboundAckActor,
		AcknowledgedAt: &at,
	}}, nil
}

func (s *inboundHTTPStub) IngestOpportunityEvent(_ context.Context, _ uuid.UUID, event liveintel.OpportunityEvent, _ time.Time) (*confenge.OpportunityEventReceipt, *errx.Error) {
	s.gotOpp = true
	return &confenge.OpportunityEventReceipt{EventID: event.EventID}, nil
}

func TestConfengeInboundWebhookRejectsQueryPIIAndAcceptsSignedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	org := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	secret := "inbound-http-secret"
	stub := &inboundHTTPStub{cfg: confenge.Config{
		Enabled: true, InboundWebhookSecret: secret, InboundOrgID: org, AutoSendEnabled: false,
	}}
	h := &Handler{ConfengeService: stub}

	now := time.Now().UTC()
	body := []byte(`{"lead_id":"http-live-1","company":"Obra Sul","phone":"41991112222","source":"web-cfg","contract_public_id":"CTR-2"}`)
	sig := confenge.SignOutcomeHMAC(secret, now, body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/confenge/inbound?email=leak@x.com", bytes.NewReader(body))
	req.Header.Set("X-Warmbly-Signature", sig)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	h.ConfengeInboundWebhook(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("query PII status=%d body=%s", w.Code, w.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/confenge/inbound", bytes.NewReader(body))
	req2.Header.Set("X-Warmbly-Signature", sig)
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = req2
	h.ConfengeInboundWebhook(c2)
	if w2.Code != http.StatusCreated && w2.Code != http.StatusOK {
		t.Fatalf("signed ingest status=%d body=%s", w2.Code, w2.Body.String())
	}
	var wrap struct {
		Data confenge.InboundIngestResult `json:"data"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &wrap); err != nil {
		t.Fatal(err)
	}
	if wrap.Data.NextAction == "" {
		t.Fatalf("empty next_action: %s", w2.Body.String())
	}
	if wrap.Data.DispatchAttempted {
		t.Fatal("http ingest attempted send")
	}
	if !bytes.Contains(stub.gotRaw, []byte("http-live-1")) {
		t.Fatalf("handler did not pass body to ingest: %s", stub.gotRaw)
	}
	fmt.Printf("HTTP_HANDLER lead_id=http-live-1 next_action=%s send=false status=%d\n", wrap.Data.NextAction, w2.Code)
}

func TestConfengeInboundWebhookRetryAfterPersist5xx(t *testing.T) {
	gin.SetMode(gin.TestMode)
	org := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	secret := "inbound-http-secret"
	calls := 0
	stub := &inboundHTTPStub{cfg: confenge.Config{
		Enabled: true, InboundWebhookSecret: secret, InboundOrgID: org, AutoSendEnabled: false,
	}}
	stub.ingest = func(raw []byte, _ confenge.IngestOptions) (*confenge.InboundIngestResult, *errx.Error) {
		calls++
		if calls == 1 {
			return nil, errx.New(errx.Internal, "persist inbound receipt: postgres unavailable")
		}
		if calls == 2 {
			return &confenge.InboundIngestResult{NextAction: "CALL", DispatchAttempted: false}, nil
		}
		return &confenge.InboundIngestResult{NextAction: "CALL", Duplicate: true, DispatchAttempted: false}, nil
	}
	h := &Handler{ConfengeService: stub}
	now := time.Now().UTC()
	body := []byte(`{"lead_id":"retry-http-1","receipt_id":"retry-http-1","source":"CONFENGE_WEB"}`)
	sig := confenge.SignOutcomeHMAC(secret, now, body)

	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/confenge/inbound", bytes.NewReader(body))
		req.Header.Set("X-Warmbly-Signature", sig)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = req
		h.ConfengeInboundWebhook(c)
		return w
	}

	w1 := post()
	if w1.Code != http.StatusInternalServerError {
		t.Fatalf("persist 5xx status=%d body=%s", w1.Code, w1.Body.String())
	}
	w2 := post()
	if w2.Code != http.StatusCreated {
		t.Fatalf("retry create status=%d body=%s", w2.Code, w2.Body.String())
	}
	w3 := post()
	if w3.Code != http.StatusOK {
		t.Fatalf("same lead_id replay status=%d body=%s", w3.Code, w3.Body.String())
	}
	var wrap struct {
		Data confenge.InboundIngestResult `json:"data"`
	}
	if err := json.Unmarshal(w3.Body.Bytes(), &wrap); err != nil {
		t.Fatal(err)
	}
	if !wrap.Data.Duplicate || wrap.Data.DispatchAttempted {
		t.Fatalf("replay: %+v", wrap.Data)
	}
	fmt.Printf("HTTP_RETRY persist_5xx=500 then_201=true replay_200=true dispatch=false calls=%d\n", calls)
}

func TestConfengeInboundWebhookInvalidHMACIs401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	org := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	stub := &inboundHTTPStub{cfg: confenge.Config{
		Enabled: true, InboundWebhookSecret: "inbound-http-secret", InboundOrgID: org, AutoSendEnabled: false,
	}}
	h := &Handler{ConfengeService: stub}
	body := []byte(`{"lead_id":"http-401-1","source":"CONFENGE_WEB"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/confenge/inbound", bytes.NewReader(body))
	req.Header.Set("X-Warmbly-Signature", "t=1,v1=deadbeef")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	h.ConfengeInboundWebhook(c)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("invalid HMAC status=%d body=%s", w.Code, w.Body.String())
	}
	fmt.Printf("HTTP_HMAC invalid=401 class=unauthorized\n")
}

func TestConfengeInboundWebhookUnavailableOrgIs503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &inboundHTTPStub{cfg: confenge.Config{
		Enabled: true, InboundWebhookSecret: "inbound-http-secret", AutoSendEnabled: false,
	}}
	h := &Handler{ConfengeService: stub}
	body := []byte(`{"lead_id":"http-503-1","source":"CONFENGE_WEB"}`)
	now := time.Now().UTC()
	sig := confenge.SignOutcomeHMAC("inbound-http-secret", now, body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/confenge/inbound", bytes.NewReader(body))
	req.Header.Set("X-Warmbly-Signature", sig)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	h.ConfengeInboundWebhook(c)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing org status=%d want 503 body=%s", w.Code, w.Body.String())
	}
	if w.Code == http.StatusUnauthorized {
		t.Fatal("unavailable must stay distinct from 401")
	}
	fmt.Printf("HTTP_UNAVAILABLE class=503 distinct_from_401=true\n")
}

func TestConfengeInboundHealthReadyBlockedNoPII(t *testing.T) {
	gin.SetMode(gin.TestMode)
	org := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	readyStub := &inboundHTTPStub{cfg: confenge.Config{
		Enabled: true, InboundWebhookSecret: "inbound-http-secret", InboundOrgID: org, AutoSendEnabled: false,
	}}
	h := &Handler{ConfengeService: readyStub}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/webhooks/confenge/inbound/health", nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	h.ConfengeInboundHealth(c)
	if w.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", w.Code, w.Body.String())
	}
	var probe confenge.InboundReceiveProbe
	if err := json.Unmarshal(w.Body.Bytes(), &probe); err != nil {
		t.Fatal(err)
	}
	if probe.Status != confenge.InboundReceiveReady || probe.AutoSendEnabled {
		t.Fatalf("health READY: %+v", probe)
	}
	joined := strings.Join(probe.AcceptedEventVersions, ",")
	if !strings.Contains(joined, intel.EventSchemaV1) || !strings.Contains(joined, intel.OrganicDiscoveryContract) {
		t.Fatalf("accepted_event_versions=%v", probe.AcceptedEventVersions)
	}
	if strings.Contains(w.Body.String(), "inbound-http-secret") || strings.Contains(w.Body.String(), org.String()) {
		t.Fatalf("health leaked secret or org: %s", w.Body.String())
	}
	fmt.Printf("HTTP_HEALTH status=%s auto_send=%v http=%d\n", probe.Status, probe.AutoSendEnabled, w.Code)

	blocked := &inboundHTTPStub{cfg: confenge.Config{Enabled: true, AutoSendEnabled: false}}
	h2 := &Handler{ConfengeService: blocked}
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest(http.MethodGet, "/api/v1/webhooks/confenge/inbound/health", nil)
	h2.ConfengeInboundHealth(c2)
	var blockedProbe confenge.InboundReceiveProbe
	if err := json.Unmarshal(w2.Body.Bytes(), &blockedProbe); err != nil {
		t.Fatal(err)
	}
	if blockedProbe.Status != confenge.InboundReceiveBlocked {
		t.Fatalf("unset config must BLOCK: %+v", blockedProbe)
	}
	fmt.Printf("HTTP_HEALTH_BLOCKED status=%s reasons=%v\n", blockedProbe.Status, blockedProbe.Reasons)
}

func TestConfengeActorUUIDParsesJWTString(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	actor := uuid.MustParse("11111111-0000-0000-0000-000000000001")
	c.Set("user_id", actor.String())
	if got := confengeActorUUID(c); got != actor {
		t.Fatalf("string user_id: got %s want %s", got, actor)
	}
	c.Set("user_id", actor)
	if got := confengeActorUUID(c); got != actor {
		t.Fatalf("uuid user_id: got %s want %s", got, actor)
	}
	c.Set("user_id", "")
	if got := confengeActorUUID(c); got != uuid.Nil {
		t.Fatalf("empty user_id: got %s", got)
	}
}

func TestConfengeInboundWebhookUnsignedIs401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	org := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	stub := &inboundHTTPStub{cfg: confenge.Config{
		Enabled: true, InboundWebhookSecret: "inbound-http-secret", InboundOrgID: org, AutoSendEnabled: false,
	}}
	h := &Handler{ConfengeService: stub}
	body := []byte(`{"lead_id":"http-unsigned-1","source":"CONFENGE_WEB"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/confenge/inbound", bytes.NewReader(body))
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	h.ConfengeInboundWebhook(c)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned status=%d body=%s", w.Code, w.Body.String())
	}
	fmt.Printf("HTTP_HMAC unsigned=401\n")
}

func TestConfengeInboundWebhookSearchObservationEchoAndRejects(t *testing.T) {
	gin.SetMode(gin.TestMode)
	org := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	secret := "inbound-http-secret"
	stub := &inboundHTTPStub{cfg: confenge.Config{
		Enabled: true, InboundWebhookSecret: secret, InboundOrgID: org, AutoSendEnabled: false,
	}}
	h := &Handler{ConfengeService: stub}
	now := time.Now().UTC().Add(-time.Minute)
	bodyMap := map[string]any{
		"schema": intel.EventSchemaV1, "version": intel.OrganicDiscoveryContract,
		"type": intel.EventSearchObservation, "source": intel.ProducerCONFENGEWeb,
		"event_id": "http-so-1", "organic_source": intel.SourceOrganicSearch,
		"asset_id": "landing-segunda-leitura", "landing_path": "/guias/segunda-leitura",
		"window": intel.Window28dComplete, "eligible": 11, "appeared": 4, "clicked": 1,
		"measurement_at": now.Format(time.RFC3339), "synthetic": true,
		"record_kind": intel.RecordKindSynthetic, "consent_policy": intel.ConsentPolicyNotApplicable,
	}
	body, _ := json.Marshal(bodyMap)
	sig := confenge.SignOutcomeHMAC(secret, time.Now().UTC(), body)
	post := func(raw []byte, signature string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/confenge/inbound", bytes.NewReader(raw))
		if signature != "" {
			req.Header.Set("X-Warmbly-Signature", signature)
		}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = req
		h.ConfengeInboundWebhook(c)
		return w
	}
	w := post(body, sig)
	if w.Code != http.StatusCreated {
		t.Fatalf("search observation status=%d body=%s", w.Code, w.Body.String())
	}
	var wrap struct {
		Data intel.SearchObservationReceipt `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &wrap); err != nil {
		t.Fatal(err)
	}
	if !wrap.Data.Persisted || !wrap.Data.NotALead || wrap.Data.AcceptedVersion != intel.OrganicDiscoveryContract {
		t.Fatalf("echo=%+v", wrap.Data)
	}
	if intel.ContainsForbiddenQuery(w.Body.Bytes()) {
		t.Fatal("HTTP echo leaked query")
	}
	w2 := post(body, sig)
	if w2.Code != http.StatusOK {
		t.Fatalf("replay status=%d body=%s", w2.Code, w2.Body.String())
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &wrap); err != nil {
		t.Fatal(err)
	}
	if !wrap.Data.Replay || !wrap.Data.Persisted {
		t.Fatalf("replay echo=%+v", wrap.Data)
	}

	badVer := cloneMap(bodyMap)
	badVer["version"] = "confenge.search_observation.v9"
	badVer["event_id"] = "http-so-bad-ver"
	rawBad, _ := json.Marshal(badVer)
	w3 := post(rawBad, confenge.SignOutcomeHMAC(secret, time.Now().UTC(), rawBad))
	if w3.Code < 400 || w3.Code >= 500 {
		t.Fatalf("unsupported version status=%d body=%s", w3.Code, w3.Body.String())
	}

	withQuery := cloneMap(bodyMap)
	withQuery["event_id"] = "http-so-query"
	withQuery["query"] = "segunda leitura contrato"
	rawQ, _ := json.Marshal(withQuery)
	w4 := post(rawQ, confenge.SignOutcomeHMAC(secret, time.Now().UTC(), rawQ))
	if w4.Code < 400 || w4.Code >= 500 {
		t.Fatalf("query literal status=%d body=%s", w4.Code, w4.Body.String())
	}
	fmt.Printf("HTTP_SEARCH_OBS created=%d replay=%d unsupported=%d query=%d not_a_lead=%v\n",
		w.Code, w2.Code, w3.Code, w4.Code, wrap.Data.NotALead)
}

func TestConfengeInboundWebhookAutoSendRefused(t *testing.T) {
	gin.SetMode(gin.TestMode)
	org := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	secret := "inbound-http-secret"
	stub := &inboundHTTPStub{cfg: confenge.Config{
		Enabled: true, InboundWebhookSecret: secret, InboundOrgID: org, AutoSendEnabled: true,
	}}
	h := &Handler{ConfengeService: stub}
	body := []byte(`{"lead_id":"http-autosend-1","source":"CONFENGE_WEB"}`)
	sig := confenge.SignOutcomeHMAC(secret, time.Now().UTC(), body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/confenge/inbound", bytes.NewReader(body))
	req.Header.Set("X-Warmbly-Signature", sig)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	h.ConfengeInboundWebhook(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("auto-send status=%d body=%s", w.Code, w.Body.String())
	}
	fmt.Printf("HTTP_AUTOSEND refused=%d\n", w.Code)
}

func TestConfengeInboundWebhookCommercialEventHMAC(t *testing.T) {
	gin.SetMode(gin.TestMode)
	org := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	secret := "inbound-http-secret"
	stub := &inboundHTTPStub{cfg: confenge.Config{
		Enabled: true, InboundWebhookSecret: secret, InboundOrgID: org, AutoSendEnabled: false,
	}, obsStore: intel.NewMemoryStore()}
	h := &Handler{ConfengeService: stub}

	now := time.Now().UTC()
	bodyMap := map[string]any{
		"schema":             intel.EventSchemaV1,
		"event_id":           "evt_SYNTHETIC_http_sel",
		"type":               intel.EventOfferSelected,
		"occurred_at":        now.Format(time.RFC3339),
		"offer_id":           intel.OfferDiagnostico,
		"offer_version":      "v1",
		"terms_version":      "CFG-TERMS-B2B-2026-08-17-v1",
		"external_reference": "SYNTHETIC-HTTP-1",
		"provider_event_id":  "asaas_SYNTHETIC_http_sel",
		"amount_cents":       800000,
		"currency":           "BRL",
		"source":             "CONFENGE_WEB",
		"revenue":            false,
		"received_revenue":   false,
		"synthetic":          true,
	}
	body, _ := json.Marshal(bodyMap)
	post := func(raw []byte, sig string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/confenge/inbound", bytes.NewReader(raw))
		req.Header.Set("X-Warmbly-Signature", sig)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = req
		h.ConfengeInboundWebhook(c)
		return w
	}

	bad := post(body, confenge.SignOutcomeHMAC("wrong-secret", now, body))
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("invalid secret status=%d body=%s", bad.Code, bad.Body.String())
	}

	ok := post(body, confenge.SignOutcomeHMAC(secret, now, body))
	if ok.Code != http.StatusCreated && ok.Code != http.StatusOK {
		t.Fatalf("commercial ingest status=%d body=%s", ok.Code, ok.Body.String())
	}
	replay := post(body, confenge.SignOutcomeHMAC(secret, time.Now().UTC(), body))
	if replay.Code != http.StatusOK {
		t.Fatalf("commercial replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	var wrap struct {
		Data intel.JoinResult `json:"data"`
	}
	if err := json.Unmarshal(replay.Body.Bytes(), &wrap); err != nil {
		t.Fatal(err)
	}
	if !wrap.Data.Replay {
		t.Fatal("replay flag false")
	}

	unkMap := cloneMap(bodyMap)
	unkMap["event_id"] = "evt_SYNTHETIC_http_unk"
	unkMap["provider_event_id"] = "asaas_SYNTHETIC_http_unk"
	unkMap["type"] = "TELEPORTED"
	unkMap["provider_raw_status"] = "TELEPORTED"
	unkRaw, _ := json.Marshal(unkMap)
	unk := post(unkRaw, confenge.SignOutcomeHMAC(secret, time.Now().UTC(), unkRaw))
	if unk.Code != http.StatusCreated && unk.Code != http.StatusOK {
		t.Fatalf("unknown type status=%d body=%s", unk.Code, unk.Body.String())
	}
	fmt.Printf("HTTP_COMMERCIAL invalid_secret=%d created=%d replay=%d unknown=%d\n", bad.Code, ok.Code, replay.Code, unk.Code)
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func TestConfengeInboundWebhookNetNewHandraiserRoutesAndReadback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	org := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	secret := "inbound-http-secret"
	var gotOutcome string
	stub := &inboundHTTPStub{cfg: confenge.Config{
		Enabled: true, InboundWebhookSecret: secret, InboundOrgID: org, AutoSendEnabled: false,
	}}
	stub.netNew = func(raw []byte) (*confenge.NetNewInboundResult, *errx.Error) {
		at := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
		res := &confenge.NetNewInboundResult{
			Schema:         confenge.NetNewInboundHandraiserSchema,
			PolicyVersion:  confenge.NetNewInboundHandraiserSchema,
			LogicalID:      "nnhr-http-1",
			Receipt:        "receipt-http-1",
			Outcome:        confenge.NetNewInboundOutcomeAccepted,
			InboundOnly:    true,
			AcknowledgedBy: confenge.NetNewInboundAckActor,
			AcknowledgedAt: &at,
		}
		gotOutcome = res.Outcome
		return res, nil
	}
	h := &Handler{ConfengeService: stub}
	body, _ := json.Marshal(map[string]any{
		"schema":      confenge.NetNewInboundHandraiserSchema,
		"schema_hash": confenge.NetNewInboundPinnedHash,
		"source":      "CONFENGE_WEB",
		"lane":        "CONFENGE_WEB",
		"logical_id":  "nnhr-http-1",
	})
	sig := confenge.SignOutcomeHMAC(secret, time.Now().UTC(), body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/confenge/inbound", bytes.NewReader(body))
	req.Header.Set("X-Warmbly-Signature", sig)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	h.ConfengeInboundWebhook(c)
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("net-new status=%d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(stub.gotNetNew, []byte("nnhr-http-1")) {
		t.Fatal("handler did not route net-new envelope")
	}
	if stub.gotOpp {
		t.Fatal("net-new envelope routed to INTEL_WATCH")
	}
	var wrap struct {
		Data confenge.NetNewInboundResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &wrap); err != nil {
		t.Fatal(err)
	}
	if wrap.Data.Outcome != confenge.NetNewInboundOutcomeAccepted {
		t.Fatalf("HTTP %d is not acceptance; outcome=%q", w.Code, wrap.Data.Outcome)
	}
	if gotOutcome != wrap.Data.Outcome {
		t.Fatal("handler invented an outcome")
	}

	req2 := httptest.NewRequest(http.MethodGet, "/confenge/inbound/handraisers/nnhr-http-1", nil)
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = req2
	c2.Params = gin.Params{{Key: "logicalId", Value: "nnhr-http-1"}}
	c2.Set("organization_id", org)
	h.GetConfengeInboundHandraiser(c2)
	if w2.Code != http.StatusOK {
		t.Fatalf("readback status=%d body=%s", w2.Code, w2.Body.String())
	}
	var rb struct {
		Data confenge.NetNewInboundReadback `json:"data"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &rb); err != nil {
		t.Fatal(err)
	}
	if rb.Data.AcknowledgedBy == "" || rb.Data.Receipt == "" || rb.Data.PolicyVersion == "" {
		t.Fatalf("readback missing fields: %+v", rb.Data)
	}
}

func TestConfengeInboundWebhookIntelWatchDoesNotCallNetNew(t *testing.T) {
	gin.SetMode(gin.TestMode)
	org := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	secret := "inbound-http-secret"
	stub := &inboundHTTPStub{cfg: confenge.Config{
		Enabled: true, InboundWebhookSecret: secret, InboundOrgID: org, AutoSendEnabled: false,
	}}
	h := &Handler{ConfengeService: stub}
	body, _ := json.Marshal(map[string]any{
		"schema":      liveintel.EventSchemaV1,
		"event_id":    "intel-watch-http-1",
		"event_type":  "NEW_OPPORTUNITY",
		"subject_key": "company:watched",
		"payload":     map[string]string{"change": "factual"},
	})
	sig := confenge.SignOutcomeHMAC(secret, time.Now().UTC(), body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/confenge/inbound", bytes.NewReader(body))
	req.Header.Set("X-Warmbly-Signature", sig)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	h.ConfengeInboundWebhook(c)
	if len(stub.gotNetNew) != 0 {
		t.Fatalf("INTEL_WATCH routed to net-new consumer: %s", stub.gotNetNew)
	}
	if !stub.gotOpp {
		t.Fatalf("INTEL_WATCH not routed to opportunity ingest status=%d body=%s", w.Code, w.Body.String())
	}
}
