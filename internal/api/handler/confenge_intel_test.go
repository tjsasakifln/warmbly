package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

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

func jsonHasKey(body, key string) bool {
	needle := `"` + key + `"`
	for i := 0; i+len(needle) <= len(body); i++ {
		if body[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
