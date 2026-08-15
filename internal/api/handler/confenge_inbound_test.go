package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
}

func (s *inboundHTTPStub) Enabled() bool           { return true }
func (s *inboundHTTPStub) Config() confenge.Config { return s.cfg }
func (s *inboundHTTPStub) IngestInboundLead(_ context.Context, _ uuid.UUID, raw []byte, opts confenge.IngestOptions) (*confenge.InboundIngestResult, *errx.Error) {
	if xerr := confenge.RejectInboundQueryPII(opts.Query); xerr != nil {
		return nil, xerr
	}
	s.gotRaw = append([]byte(nil), raw...)
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
