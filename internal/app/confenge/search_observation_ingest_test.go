package confenge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
)

func TestIngestSearchObservationServiceEchoAndPrivacy(t *testing.T) {
	svc, _, org := inboundTestService(t)
	svc.WireIntel(nil)
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	body := intelSearchBody(map[string]any{
		"event_id": "svc-so-1", "measurement_at": now.Format(time.RFC3339),
	})
	rec, xerr := svc.IngestSearchObservation(context.Background(), org, body, IngestOptions{Now: now})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if rec == nil || !rec.Persisted || !rec.NotALead || rec.Replay {
		t.Fatalf("receipt=%+v", rec)
	}
	if rec.AcceptedVersion != intel.OrganicDiscoveryContract || rec.EventID != "svc-so-1" {
		t.Fatalf("echo=%+v", rec)
	}
	replay, xerr := svc.IngestSearchObservation(context.Background(), org, body, IngestOptions{Now: now})
	if xerr != nil || replay == nil || !replay.Replay || !replay.Persisted {
		t.Fatalf("replay=%+v %v", replay, xerr)
	}
	board, xerr := svc.OrganicScoreboard(context.Background(), org, false)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if board.Recommendation != intel.RecommendNeedsWebCfg {
		t.Fatalf("synthetic default must not become real: %s", board.Recommendation)
	}
	syn, xerr := svc.OrganicScoreboard(context.Background(), org, true)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if syn.Recommendation != intel.RecommendNeedsReal {
		t.Fatalf("include_synthetic=1 should surface discovery: %s", syn.Recommendation)
	}
	assertNoRawQueryLeak(t, "scoreboard", mustJSONBytes(board))
	assertNoRawQueryLeak(t, "receipt", mustJSONBytes(rec))
	queue, _ := svc.CollectInboundNow(context.Background(), org)
	if len(queue) != 0 {
		t.Fatalf("search observation leaked into INBOUND NOW: %+v", queue)
	}
	fmt.Printf("SVC_SEARCH_OBS persisted=%v replay=%v rec_real=%s rec_syn=%s inbound_now=%d\n",
		rec.Persisted, replay.Replay, board.Recommendation, syn.Recommendation, len(queue))
}

func TestIngestSearchObservationAutoSendRefused(t *testing.T) {
	svc, _, org := inboundTestService(t)
	svc.cfg.AutoSendEnabled = true
	_, xerr := svc.IngestSearchObservation(context.Background(), org, intelSearchBody(nil), IngestOptions{})
	if xerr == nil {
		t.Fatal("auto-send true must refuse")
	}
	if !strings.Contains(strings.ToLower(xerr.Error()), "auto_send") {
		t.Fatalf("error=%v", xerr)
	}
	fmt.Printf("SEARCH_OBS_AUTOSEND refused=true\n")
}

func TestIngestSearchObservationDoesNotMail(t *testing.T) {
	svc, _, org := inboundTestService(t)
	svc.WireIntel(nil)
	svc.cfg.OperatorAlertEmailEnabled = true
	svc.cfg.OperatorAlertEmailKillSwitch = false
	svc.cfg.OperatorAlertEmail = "ops@confenge.com.br"
	sent := 0
	svc.operatorMail = func(to, subject, body string) error {
		sent++
		return nil
	}
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	if _, xerr := svc.IngestSearchObservation(context.Background(), org, intelSearchBody(map[string]any{
		"event_id": "so-mail-1", "synthetic": true, "record_kind": "synthetic",
		"measurement_at": now.Format(time.RFC3339),
	}), IngestOptions{Now: now}); xerr != nil {
		t.Fatal(xerr)
	}
	if sent != 0 {
		t.Fatalf("search observation emailed operator: sent=%d", sent)
	}
	fmt.Printf("SEARCH_OBS_NO_MAIL sent=%d\n", sent)
}

func intelSearchBody(overrides map[string]any) []byte {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	m := map[string]any{
		"schema":         intel.EventSchemaV1,
		"version":        intel.OrganicDiscoveryContract,
		"type":           intel.EventSearchObservation,
		"source":         intel.ProducerCONFENGEWeb,
		"event_id":       "svc-so-default",
		"organic_source": intel.SourceOrganicSearch,
		"asset_id":       "landing-segunda-leitura",
		"landing_path":   "/guias/segunda-leitura",
		"window":         intel.Window28dComplete,
		"eligible":       20,
		"appeared":       8,
		"clicked":        2,
		"engaged":        1,
		"measurement_at": now.Format(time.RFC3339),
		"synthetic":      true,
		"record_kind":    intel.RecordKindSynthetic,
		"consent_policy": intel.ConsentPolicyNotApplicable,
	}
	for k, v := range overrides {
		m[k] = v
	}
	raw, _ := json.Marshal(m)
	return raw
}
