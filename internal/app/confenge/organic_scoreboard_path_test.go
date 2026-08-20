package confenge

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/models"
)

func TestOrganicScoreboardOpenInboundIsNotQualifiedPipeline(t *testing.T) {
	svc, _, org := inboundTestService(t)
	svc.cfg.AutoSendEnabled = false
	svc.WireIntel(nil)
	now := time.Now().UTC()
	body := []byte(`{
		"lead_id":"webcfg-organic-open-1",
		"receipt_id":"rcpt-organic-open-1",
		"source":"organic_search",
		"organic_source":"organic_search",
		"route_family":"inbound",
		"asset_id":"landing-segunda-leitura",
		"cta_id":"segunda-leitura-contrato",
		"company":"Construtora Norte",
		"email":"ana.souza@norte.example",
		"consent":{"granted":true,"consent_source":"web-cfg-consent-v1"}
	}`)
	ing, xerr := svc.IngestInboundLead(context.Background(), org, body, IngestOptions{Now: now})
	if xerr != nil || ing.Lead == nil {
		t.Fatalf("ingest: %v %+v", xerr, ing)
	}
	if ing.Lead.Status != models.InboundStatusOpen {
		t.Fatalf("status=%s want OPEN", ing.Lead.Status)
	}
	if ing.DispatchAttempted || svc.cfg.AutoSendEnabled {
		t.Fatal("dispatch/auto-send must stay off")
	}

	board, xerr := svc.OrganicScoreboard(context.Background(), org, false)
	if xerr != nil {
		t.Fatal(xerr)
	}
	pipe := organicLayerTotal(*board, intel.LayerQualifiedPipeline)
	leads := organicLayerTotal(*board, intel.LayerLeadValid)
	if leads == 0 {
		t.Fatalf("open inbound missing from LEAD_VALID: %+v", board.Windows)
	}
	if pipe != 0 {
		t.Fatalf("OPEN inbound counted as QUALIFIED_PIPELINE=%d", pipe)
	}
	fmt.Printf("ORGANIC_OPEN_NOT_PIPELINE leads=%d pipeline=%d status=%s auto_send=%v\n",
		leads, pipe, ing.Lead.Status, svc.cfg.AutoSendEnabled)
}

func organicLayerTotal(board intel.OrganicScoreboard, id string) int {
	n := 0
	for _, w := range board.Windows {
		if w.ID != intel.Window90d {
			continue
		}
		for _, sl := range w.BySource {
			for _, ly := range sl.Layers {
				if ly.ID == id {
					n += ly.Count
				}
			}
		}
	}
	return n
}
