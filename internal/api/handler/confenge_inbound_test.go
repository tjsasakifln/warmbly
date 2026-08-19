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
	"github.com/warmbly/warmbly/internal/errx"
)

type inboundHTTPStub struct {
	confenge.Service
	cfg    confenge.Config
	gotRaw []byte
	ingest func(raw []byte, opts confenge.IngestOptions) (*confenge.InboundIngestResult, *errx.Error)
}

func (s *inboundHTTPStub) Enabled() bool           { return true }
func (s *inboundHTTPStub) Config() confenge.Config { return s.cfg }
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
