package confenge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/models"
)

func realInboundBody(leadID string) []byte {
	return []byte(`{
		"lead_id":"` + leadID + `",
		"receipt_id":"rcpt-` + leadID + `",
		"created_at":"2026-08-19T12:00:00Z",
		"source":"CONFENGE_WEB",
		"route_family":"inbound",
		"asset_id":"segunda-leitura",
		"cta_id":"segunda-leitura-contrato",
		"cnpj":"55.444.333/0001-22",
		"company":"Construtora Norte",
		"name":"Ana Souza",
		"email":"ana.souza@norte.example",
		"phone":"+5541999887766",
		"message":"Quero uma segunda leitura do contrato",
		"correlation_id":"attr-speed-1"
	}`)
}

func syntheticInboundBody(leadID string) []byte {
	return []byte(`{
		"lead_id":"` + leadID + `",
		"receipt_id":"rcpt-` + leadID + `",
		"source":"infrastructure_canary",
		"route_family":"inbound",
		"asset_id":"canary",
		"company":"synthetic-inbound canary",
		"email":"qa@internal.example"
	}`)
}

func TestOperatorAlertOnePerRealLeadAndReplay(t *testing.T) {
	svc, repo, org := inboundTestService(t)
	now := time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)
	body := realInboundBody("webcfg-speed-1")
	first, xerr := svc.IngestInboundLead(context.Background(), org, body, IngestOptions{Now: now})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if first.DispatchAttempted || first.Lead == nil {
		t.Fatalf("persist failed: %+v", first)
	}
	if first.Lead.LeadID != "webcfg-speed-1" {
		t.Fatalf("lead_id=%s", first.Lead.LeadID)
	}
	alerts, _ := repo.ListOperatorAlerts(context.Background(), org, true, 20)
	if len(alerts) != 1 {
		t.Fatalf("want 1 alert, got %d", len(alerts))
	}
	if alerts[0].AlertType != AlertTypeInboundOperatorAttention || alerts[0].Synthetic {
		t.Fatalf("alert=%+v", alerts[0])
	}
	if alerts[0].EventID != OperatorAlertEventID("webcfg-speed-1") {
		t.Fatalf("event_id=%s", alerts[0].EventID)
	}

	replay, xerr := svc.IngestInboundLead(context.Background(), org, body, IngestOptions{Now: now.Add(time.Minute)})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if !replay.Duplicate {
		t.Fatal("replay must be duplicate")
	}
	alerts, _ = repo.ListOperatorAlerts(context.Background(), org, true, 20)
	if len(alerts) != 1 {
		t.Fatalf("replay duplicated alerts: %d", len(alerts))
	}
	leads, _ := repo.ListInboundLeads(context.Background(), org, false, 20)
	if len(leads) != 1 {
		t.Fatalf("replay duplicated receipts: %d", len(leads))
	}
	fmt.Printf("ALERT_ONE lead=%s alerts=%d replay=%v dispatch=%v auto_send=%v\n",
		first.Lead.LeadID, len(alerts), replay.Duplicate, first.DispatchAttempted, svc.cfg.AutoSendEnabled)
}

func TestOperatorAlertSecondaryDedupeKeepsDistinctLeads(t *testing.T) {
	svc, repo, org := inboundTestService(t)
	now := time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)
	a := realInboundBody("webcfg-speed-a")
	b := []byte(`{
		"lead_id":"webcfg-speed-b",
		"receipt_id":"rcpt-webcfg-speed-b",
		"source":"CONFENGE_WEB",
		"cnpj":"55.444.333/0001-22",
		"email":"ana.souza@norte.example",
		"phone":"+5541999887766",
		"company":"Construtora Norte"
	}`)
	if _, xerr := svc.IngestInboundLead(context.Background(), org, a, IngestOptions{Now: now}); xerr != nil {
		t.Fatal(xerr)
	}
	second, xerr := svc.IngestInboundLead(context.Background(), org, b, IngestOptions{Now: now.Add(10 * time.Minute)})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if second.Lead == nil || second.Lead.LeadID != "webcfg-speed-b" {
		t.Fatalf("distinct lead missing: %+v", second)
	}
	alerts, _ := repo.ListOperatorAlerts(context.Background(), org, true, 20)
	if len(alerts) != 2 {
		t.Fatalf("secondary dedupe must not merge distinct alerts: %d", len(alerts))
	}
	fmt.Printf("ALERT_DISTINCT a=webcfg-speed-a b=%s alerts=%d secondary=%v\n",
		second.Lead.LeadID, len(alerts), second.SecondaryDedupe)
}

func TestOperatorAlertSyntheticExcludedFromDefaultReal(t *testing.T) {
	svc, repo, org := inboundTestService(t)
	now := time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)
	if _, xerr := svc.IngestInboundLead(context.Background(), org, realInboundBody("webcfg-real-1"), IngestOptions{Now: now}); xerr != nil {
		t.Fatal(xerr)
	}
	canary, xerr := svc.IngestInboundLead(context.Background(), org, syntheticInboundBody("synthetic-speed-canary-1"), IngestOptions{Now: now})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if canary.Lead == nil {
		t.Fatal("synthetic must persist")
	}
	if InboundCommercialSkipReason(*canary.Lead) == "" {
		t.Fatal("canary must be labeled synthetic")
	}
	queue, xerr := svc.CollectInboundNow(context.Background(), org)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if len(queue) != 1 || queue[0].LeadID != "webcfg-real-1" {
		t.Fatalf("default INBOUND NOW must exclude synthetic: %+v", queue)
	}
	if queue[0].Synthetic {
		t.Fatal("real card labeled synthetic")
	}
	if countUnacknowledgedReal(queue) != 1 {
		t.Fatalf("unacked real=%d", countUnacknowledgedReal(queue))
	}
	withSyn, xerr := svc.CollectInboundNowFiltered(context.Background(), org, true)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if len(withSyn) != 2 {
		t.Fatalf("include_synthetic want 2 got %d", len(withSyn))
	}
	alerts, _ := repo.ListOperatorAlerts(context.Background(), org, true, 20)
	syn := 0
	for _, a := range alerts {
		if a.Synthetic {
			syn++
		}
	}
	if syn != 1 {
		t.Fatalf("synthetic alerts=%d", syn)
	}
	cockpit, xerr := svc.CollectContactCockpit(context.Background(), org)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if cockpit.UnacknowledgedReal != 1 {
		t.Fatalf("cockpit unacked real=%d", cockpit.UnacknowledgedReal)
	}
	fmt.Printf("SYNTHETIC_SKIP inbound_now=%d unacked_real=%d canary_persisted=%s\n",
		len(queue), cockpit.UnacknowledgedReal, canary.Lead.LeadID)
}

func TestOperatorAlertPersistSurvivesAlertStoreFailure(t *testing.T) {
	svc, repo, org := inboundTestService(t)
	now := time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)
	repo.alertInsertErr = errors.New("postgres alert unavailable")
	res, xerr := svc.IngestInboundLead(context.Background(), org, realInboundBody("webcfg-alert-fail-1"), IngestOptions{Now: now})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if res.Lead == nil || res.Lead.LeadID != "webcfg-alert-fail-1" {
		t.Fatalf("receipt lost: %+v", res)
	}
	leads, _ := repo.ListInboundLeads(context.Background(), org, false, 20)
	if len(leads) != 1 {
		t.Fatalf("receipt missing after alert fail: %d", len(leads))
	}
	alerts, _ := repo.ListOperatorAlerts(context.Background(), org, true, 20)
	if len(alerts) != 0 {
		t.Fatalf("alert should not persist: %d", len(alerts))
	}
	held := false
	exs, _ := svc.intelStore().ListExceptions(org.String())
	for _, ex := range exs {
		if ex.Code == intel.ExceptionAlertStoreFailed && ex.Held && ex.LeadID == "webcfg-alert-fail-1" {
			held = true
			if ex.Owner != intel.OwnerInboundOps {
				t.Fatalf("owner=%s", ex.Owner)
			}
			if ex.Reason == "" || ex.NextAction == "" {
				t.Fatalf("exception incomplete: %+v", ex)
			}
		}
	}
	if !held {
		t.Fatalf("alert store failure must hold exception: %+v", exs)
	}
	fmt.Printf("ALERT_STORE_FAIL receipt=%s held=%v alerts=%d\n", res.Lead.LeadID, held, len(alerts))
}

func TestOperatorAlertOrgIsolationAndAckIdempotency(t *testing.T) {
	svc, _, orgA := inboundTestService(t)
	orgB := uuid.MustParse("bbbbbbbb-cccc-4ddd-8eee-ffffffffffff")
	now := time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)
	actor := uuid.MustParse("11111111-2222-4333-8444-555555555555")
	if _, xerr := svc.IngestInboundLead(context.Background(), orgA, realInboundBody("webcfg-org-a"), IngestOptions{Now: now}); xerr != nil {
		t.Fatal(xerr)
	}
	queueB, xerr := svc.CollectInboundNow(context.Background(), orgB)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if len(queueB) != 0 {
		t.Fatalf("other org saw inbound: %+v", queueB)
	}
	if _, xerr := svc.AcknowledgeInboundAlert(context.Background(), orgB, actor, "webcfg-org-a", now.Add(time.Minute)); xerr == nil {
		t.Fatal("other org must not ack")
	}
	first, xerr := svc.AcknowledgeInboundAlert(context.Background(), orgA, actor, "webcfg-org-a", now.Add(time.Minute))
	if xerr != nil || first == nil || first.AcknowledgedAt == nil {
		t.Fatalf("ack failed: %v %+v", xerr, first)
	}
	again, xerr := svc.AcknowledgeInboundAlert(context.Background(), orgA, actor, "webcfg-org-a", now.Add(2*time.Minute))
	if xerr != nil {
		t.Fatal(xerr)
	}
	if !again.AcknowledgedAt.Equal(*first.AcknowledgedAt) || again.AcknowledgedBy != actor.String() {
		t.Fatalf("ack not idempotent: first=%v again=%v", first.AcknowledgedAt, again.AcknowledgedAt)
	}
	if _, xerr := svc.AcknowledgeInboundAlert(context.Background(), orgA, uuid.Nil, "webcfg-org-a", now); xerr == nil {
		t.Fatal("ack without actor must fail")
	}
	fmt.Printf("ACK_IDEM lead=webcfg-org-a actor=%s at=%s\n", again.AcknowledgedBy, again.AcknowledgedAt.UTC().Format(time.RFC3339))
}

func TestOperatorAlertActionRequiresActorAndRefusesWon(t *testing.T) {
	svc, _, org := inboundTestService(t)
	now := time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)
	actor := uuid.MustParse("11111111-2222-4333-8444-555555555555")
	if _, xerr := svc.IngestInboundLead(context.Background(), org, realInboundBody("webcfg-action-1"), IngestOptions{Now: now}); xerr != nil {
		t.Fatal(xerr)
	}
	if _, xerr := svc.RecordInboundOutcome(context.Background(), org, uuid.Nil, "webcfg-action-1", OutcomeRequest{
		OutcomeCode: models.OutcomeFollowUp, Now: now.Add(time.Minute),
	}); xerr == nil {
		t.Fatal("action without actor must be refused")
	}
	if _, xerr := svc.ResolveInboundNoAction(context.Background(), org, actor, "webcfg-action-1", "WON", now.Add(time.Minute)); xerr == nil {
		t.Fatal("resolve must not set WON")
	}
	if _, xerr := svc.ResolveInboundNoAction(context.Background(), org, actor, "webcfg-action-1", "LOST", now.Add(time.Minute)); xerr == nil {
		t.Fatal("resolve must not set LOST")
	}
	if _, xerr := svc.ResolveInboundNoAction(context.Background(), org, actor, "webcfg-action-1", "REVENUE", now.Add(time.Minute)); xerr == nil {
		t.Fatal("resolve must not set revenue")
	}
	out, xerr := svc.RecordInboundOutcome(context.Background(), org, actor, "webcfg-action-1", OutcomeRequest{
		OutcomeCode: models.OutcomeFollowUp, Now: now.Add(2 * time.Minute),
	})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if out.Action.OutcomeCode != models.OutcomeFollowUp {
		t.Fatalf("outcome=%s", out.Action.OutcomeCode)
	}
	alert, _ := svc.alertStore().GetOperatorAlertByLead(context.Background(), org, "webcfg-action-1")
	if alert == nil || alert.FirstActionAt == nil || alert.FirstActionType != models.OutcomeFollowUp {
		t.Fatalf("first action not stamped: %+v", alert)
	}
	if ProjectOperatorAlertState(*alert, now.Add(2*time.Minute)) != AlertStateActionRecorded {
		t.Fatalf("state=%s", alert.State)
	}
	fmt.Printf("FIRST_ACTION lead=webcfg-action-1 type=%s won=false\n", alert.FirstActionType)
}

func TestOperatorAlertAgingBandsAndDisplayTZ(t *testing.T) {
	created := time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)
	a := OperatorAlert{CreatedAt: created}
	if got := ProjectOperatorAlertState(a, created.Add(5*time.Minute)); got != AlertBandNew {
		t.Fatalf("new=%s", got)
	}
	if got := ProjectOperatorAlertState(a, created.Add(20*time.Minute)); got != AlertBandAttention {
		t.Fatalf("attention=%s", got)
	}
	if got := ProjectOperatorAlertState(a, created.Add(61*time.Minute)); got != AlertBandAged {
		t.Fatalf("aged=%s", got)
	}
	failed := a
	failed.FailureCode = "email_transport"
	if got := ProjectOperatorAlertState(failed, created.Add(time.Minute)); got != AlertStateAlertFailed {
		t.Fatalf("failed=%s", got)
	}
	tAck := created.Add(10 * time.Minute)
	acked := a
	acked.AcknowledgedAt = &tAck
	if got := ProjectOperatorAlertState(acked, created.Add(2*time.Hour)); got != AlertStateAcknowledged {
		t.Fatalf("acked=%s", got)
	}
	tAct := created.Add(15 * time.Minute)
	acted := acked
	acted.FirstActionAt = &tAct
	if got := ProjectOperatorAlertState(acted, created.Add(2*time.Hour)); got != AlertStateActionRecorded {
		t.Fatalf("acted=%s", got)
	}
	tRes := created.Add(12 * time.Minute)
	res := acked
	res.ResolvedAt = &tRes
	if got := ProjectOperatorAlertState(res, created.Add(2*time.Hour)); got != AlertStateResolvedNoAction {
		t.Fatalf("resolved=%s", got)
	}
	loc := saoPauloLocation()
	display := FormatAlertDisplay(created, loc)
	if !strings.Contains(display, "12:00") {
		t.Fatalf("Sao Paulo display want 12:00 got %s (utc=%s)", display, created.UTC().Format(time.RFC3339))
	}
	if created.Location() != time.UTC && created.UTC().Hour() != 15 {
		t.Fatalf("UTC corrupted: %v", created)
	}
	fmt.Printf("AGING new/attention/aged ok display=%s utc=%s\n", display, created.UTC().Format(time.RFC3339))
}

func TestOperatorAlertEmailPolicyNoPIINoLeadRecipient(t *testing.T) {
	lead := models.OutreachInboundLead{
		LeadName: "Ana Souza", LeadEmail: "ana.souza@norte.example",
		LeadPhone: "+5541999887766", CNPJ14: "55444333000122",
		Message: "Quero uma segunda leitura", CompanyName: "Construtora Norte",
		Source: "CONFENGE_WEB", AssetID: "segunda-leitura",
		WarmblyIngestedAt: time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC),
	}
	mail := BuildOperatorAlertEmail(lead.WarmblyIngestedAt, lead.Source, lead.AssetID, 3*time.Minute, "/app/confenge#inbound-agora")
	if mail.Subject != OperatorAlertEmailSubject {
		t.Fatalf("subject=%s", mail.Subject)
	}
	if OperatorAlertContainsPII(mail.Subject, lead) || OperatorAlertContainsPII(mail.Body, lead) {
		t.Fatalf("PII leaked: %s\n%s", mail.Subject, mail.Body)
	}
	cfg := Config{OperatorAlertEmailEnabled: false, OperatorAlertEmailKillSwitch: true, OperatorAlertEmail: "ops@confenge.com.br"}
	if to, reason := ResolveOperatorAlertRecipient(cfg, lead.LeadEmail); to != "" || reason != AlertEmailFlagOff {
		t.Fatalf("flag off: to=%s reason=%s", to, reason)
	}
	cfg.OperatorAlertEmailEnabled = true
	if to, reason := ResolveOperatorAlertRecipient(cfg, lead.LeadEmail); to != "" || reason != AlertEmailKillSwitch {
		t.Fatalf("kill switch: to=%s reason=%s", to, reason)
	}
	cfg.OperatorAlertEmailKillSwitch = false
	to, reason := ResolveOperatorAlertRecipient(cfg, lead.LeadEmail)
	if to != "ops@confenge.com.br" || reason != AlertEmailBlockedNoTransport {
		t.Fatalf("allowlist: to=%s reason=%s", to, reason)
	}
	if to == lead.LeadEmail {
		t.Fatal("recipient derived from lead")
	}
	cfg.OperatorAlertEmail = lead.LeadEmail
	to2, _ := ResolveOperatorAlertRecipient(cfg, "someone-else@x.com")
	if to2 != cfg.OperatorAlertEmail {
		t.Fatal("recipient must come from allowlist config, not lead lookup")
	}
	svc, _, org := inboundTestService(t)
	svc.cfg.OperatorAlertEmailEnabled = false
	now := time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)
	if _, xerr := svc.IngestInboundLead(context.Background(), org, realInboundBody("webcfg-mail-1"), IngestOptions{Now: now}); xerr != nil {
		t.Fatal(xerr)
	}
	queue, _ := svc.CollectInboundNow(context.Background(), org)
	if len(queue) != 1 {
		t.Fatal("flag off must not block cockpit")
	}
	alert, _ := svc.alertStore().GetOperatorAlertByLead(context.Background(), org, "webcfg-mail-1")
	if alert == nil || alert.ChannelStates[AlertChannelEmail] != AlertEmailFlagOff {
		t.Fatalf("email channel=%v", alert)
	}
	if alert.ChannelStates[AlertChannelCockpit] != "ready" {
		t.Fatalf("cockpit channel=%v", alert.ChannelStates)
	}
	fmt.Printf("EMAIL_POLICY subject=%q reason=%s cockpit=%s\n", mail.Subject, reason, alert.ChannelStates[AlertChannelCockpit])
}

func TestOperatorAlertChannelFailureSetsAlertFailedWithoutLosingLead(t *testing.T) {
	svc, repo, org := inboundTestService(t)
	svc.cfg.OperatorAlertEmailEnabled = true
	svc.cfg.OperatorAlertEmailKillSwitch = false
	svc.cfg.OperatorAlertEmail = "ops@confenge.com.br"
	svc.operatorMail = func(to, subject, body string) error {
		if to != "ops@confenge.com.br" {
			t.Fatalf("recipient=%s", to)
		}
		if strings.Contains(strings.ToLower(subject+body), "ana") {
			t.Fatal("PII in send")
		}
		return errors.New("smtp down")
	}
	now := time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)
	res, xerr := svc.IngestInboundLead(context.Background(), org, realInboundBody("webcfg-mail-fail"), IngestOptions{Now: now})
	if xerr != nil || res.Lead == nil {
		t.Fatalf("lead lost: %v %+v", xerr, res)
	}
	leads, _ := repo.ListInboundLeads(context.Background(), org, false, 10)
	if len(leads) != 1 {
		t.Fatal("receipt missing")
	}
	alert, _ := repo.GetOperatorAlertByLead(context.Background(), org, "webcfg-mail-fail")
	if alert == nil || alert.FailureCode == "" {
		t.Fatalf("ALERT_FAILED missing: %+v", alert)
	}
	if ProjectOperatorAlertState(*alert, now) != AlertStateAlertFailed {
		t.Fatalf("state=%s", ProjectOperatorAlertState(*alert, now))
	}
	fmt.Printf("ALERT_FAILED lead=%s failure=%s receipt=1\n", res.Lead.LeadID, alert.FailureCode)
}

func TestOperatorAlertResolveNoAction(t *testing.T) {
	svc, _, org := inboundTestService(t)
	now := time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)
	actor := uuid.MustParse("11111111-2222-4333-8444-555555555555")
	if _, xerr := svc.IngestInboundLead(context.Background(), org, realInboundBody("webcfg-resolve-1"), IngestOptions{Now: now}); xerr != nil {
		t.Fatal(xerr)
	}
	if _, xerr := svc.ResolveInboundNoAction(context.Background(), org, uuid.Nil, "webcfg-resolve-1", "DUPLICATE", now); xerr == nil {
		t.Fatal("resolve without actor must fail")
	}
	got, xerr := svc.ResolveInboundNoAction(context.Background(), org, actor, "webcfg-resolve-1", "DUPLICATE", now.Add(time.Minute))
	if xerr != nil {
		t.Fatal(xerr)
	}
	if got.ResolutionReason != "DUPLICATE" || got.ResolvedAt == nil {
		t.Fatalf("resolve=%+v", got)
	}
	again, xerr := svc.ResolveInboundNoAction(context.Background(), org, actor, "webcfg-resolve-1", "SPAM", now.Add(2*time.Minute))
	if xerr != nil {
		t.Fatal(xerr)
	}
	if again.ResolutionReason != "DUPLICATE" {
		t.Fatalf("resolve not idempotent: %s", again.ResolutionReason)
	}
	fmt.Printf("RESOLVE_NO_ACTION lead=webcfg-resolve-1 reason=%s\n", got.ResolutionReason)
}

func TestOperatorAlertIngestLaunchRealSyntheticReplay(t *testing.T) {
	svc, repo, org := inboundTestService(t)
	now := time.Date(2026, 8, 19, 16, 0, 0, 0, time.UTC)
	real, xerr := svc.IngestInboundLead(context.Background(), org, realInboundBody("webcfg-launch-real"), IngestOptions{Now: now})
	if xerr != nil {
		t.Fatal(xerr)
	}
	syn, xerr := svc.IngestInboundLead(context.Background(), org, syntheticInboundBody("synthetic-launch-canary"), IngestOptions{Now: now})
	if xerr != nil {
		t.Fatal(xerr)
	}
	replay, xerr := svc.IngestInboundLead(context.Background(), org, realInboundBody("webcfg-launch-real"), IngestOptions{Now: now.Add(time.Minute)})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if real.DispatchAttempted || syn.DispatchAttempted {
		t.Fatal("dispatch must stay false")
	}
	if svc.cfg.AutoSendEnabled {
		t.Fatal("auto_send must stay false")
	}
	alerts, _ := repo.ListOperatorAlerts(context.Background(), org, true, 20)
	realAlerts := 0
	for _, a := range alerts {
		if a.LeadID == "webcfg-launch-real" && !a.Synthetic {
			realAlerts++
		}
	}
	if realAlerts != 1 {
		t.Fatalf("real alerts=%d total=%d", realAlerts, len(alerts))
	}
	queue, _ := svc.CollectInboundNow(context.Background(), org)
	for _, item := range queue {
		if item.LeadID == "synthetic-launch-canary" {
			t.Fatal("synthetic in default INBOUND NOW")
		}
	}
	if !replay.Duplicate {
		t.Fatal("replay must not create a second lead")
	}
	fmt.Printf("LAUNCH persist=%v replay=%v inbound_now=%d real_alerts=%d auto_send=%v dispatch=%v\n",
		real.Lead != nil, replay.Duplicate, len(queue), realAlerts, svc.cfg.AutoSendEnabled, real.DispatchAttempted)
}

func TestMigration105AddsOperatorAlertsAndAlertStoreFailed(t *testing.T) {
	path := filepath.Join("..", "..", "infrastructure", "db", "migrations", "000105_outreach_operator_alerts.up.sql")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, need := range []string{
		"CREATE TABLE IF NOT EXISTS outreach_operator_alerts",
		"alert_store_failed",
		"inbound_operator_attention",
		"organization_id, event_id",
		"organization_id, lead_id, alert_type",
	} {
		if !strings.Contains(body, need) {
			t.Fatalf("migration 000105 missing %s", need)
		}
	}
}

func TestRequireAlertActorRejectsNilUUIDString(t *testing.T) {
	if xerr := requireAlertActor(uuid.Nil, ""); xerr == nil {
		t.Fatal("empty actor must fail")
	}
	if xerr := requireAlertActor(uuid.Nil, uuid.Nil.String()); xerr == nil {
		t.Fatal("nil uuid string must fail")
	}
	actor := uuid.MustParse("11111111-2222-4333-8444-555555555555")
	if xerr := requireAlertActor(actor, ""); xerr != nil {
		t.Fatal(xerr)
	}
	if xerr := requireAlertActor(uuid.Nil, actor.String()); xerr != nil {
		t.Fatal(xerr)
	}
}

func TestOperatorAlertEventsDoNotOpenPipeline(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"ok": true})
	_ = raw
	st := intel.NewMemoryStore()
	now := time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)
	res := intel.IngestEvent(st, intel.CommercialEvent{
		EventID:        "operator_alert_created:inbound_operator_attention:webcfg-ev-1",
		Version:        "1",
		Schema:         intel.EventSchemaV1,
		Type:           intel.EventOperatorAlertCreated,
		OccurredAt:     now,
		Timezone:       OperatorAlertDisplayTZ,
		LeadID:         "webcfg-ev-1",
		ReceiptID:      "rcpt-ev-1",
		RouteFamily:    intel.FamilyInbound,
		OrganizationID: "org-ev",
		Synthetic:      false,
	})
	if res.Chain.PipelineOpen || res.Chain.RevenueEvidenced {
		t.Fatalf("alert event opened pipeline: %+v", res.Chain)
	}
	if res.Chain.OutcomeType == intel.OutcomeWon || res.Chain.OutcomeType == intel.OutcomeLost {
		t.Fatalf("alert event set WON/LOST: %s", res.Chain.OutcomeType)
	}
}
