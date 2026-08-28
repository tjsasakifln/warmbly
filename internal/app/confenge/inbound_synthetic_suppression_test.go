package confenge

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

func TestIngestInboundSyntheticNeverCreatesCommercialAction(t *testing.T) {
	svc, repo, org := inboundTestService(t)
	now := time.Date(2026, 8, 27, 18, 0, 0, 0, time.UTC)

	existing := &models.OutreachAccount{
		ID: uuid.New(), OrganizationID: org, CNPJ14: "55444333000122", CNPJRoot: "55444333",
		RazaoSocial: "Construtora Norte", QueueState: models.OutreachQueueReadyToGenerate,
		EmailSendReady: true, TargetFitClass: TargetFitConfirmed, TargetFitFresh: true, TargetFitEligible: true,
	}
	if _, err := repo.UpsertAccount(context.Background(), existing); err != nil {
		t.Fatal(err)
	}

	nonexistent := []byte(`{"lead_id":"syn-missing","receipt_id":"syn-missing","source":"CONFENGE_WEB","record_kind":"synthetic","synthetic":true,"company":"Probe","cnpj":"11999888000177"}`)
	againstExisting := []byte(`{"lead_id":"syn-existing","receipt_id":"syn-existing","source":"CONFENGE_WEB","record_kind":"synthetic","synthetic":true,"company":"Construtora Norte","cnpj":"55.444.333/0001-22","email":"ana@norte.example","phone":"41999887766"}`)
	qaBody := []byte(`{"lead_id":"qa-probe","receipt_id":"qa-probe","source":"qa","company":"Construtora Norte","cnpj":"55444333000122"}`)
	internalBody := []byte(`{"lead_id":"ops-probe","receipt_id":"ops-probe","label":"internal","company":"Construtora Norte","cnpj":"55444333000122"}`)
	realBody := []byte(`{"lead_id":"webcfg-real-1","receipt_id":"webcfg-real-1","source":"CONFENGE_WEB","company":"Construtora Norte","cnpj":"55444333000122","phone":"41991112222","consent":{"granted":true}}`)

	for _, body := range [][]byte{nonexistent, againstExisting, qaBody, internalBody} {
		res, xerr := svc.IngestInboundLead(context.Background(), org, body, IngestOptions{Now: now})
		if xerr != nil {
			t.Fatal(xerr)
		}
		if res.Action != nil {
			t.Fatalf("synthetic created action: lead=%s action=%+v", res.Lead.LeadID, res.Action)
		}
		if res.Lead == nil || InboundCommercialSkipReason(*res.Lead) == "" {
			t.Fatalf("synthetic skip reason missing: %+v", res.Lead)
		}
		if res.DispatchAttempted {
			t.Fatal("synthetic dispatch attempted")
		}
		switch res.Lead.LeadID {
		case "syn-missing", "syn-existing", "qa-probe":
			if res.Lead.Status != models.InboundStatusSuppressed {
				t.Fatalf("explicit synthetic/qa next=%s status=%s", res.NextAction, res.Lead.Status)
			}
		}
	}

	replay, xerr := svc.IngestInboundLead(context.Background(), org, againstExisting, IngestOptions{Now: now.Add(time.Minute)})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if replay.Action != nil {
		t.Fatalf("replay created action: %+v", replay.Action)
	}

	real, xerr := svc.IngestInboundLead(context.Background(), org, realBody, IngestOptions{Now: now.Add(2 * time.Minute)})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if real.Action == nil {
		t.Fatal("consented real lead lost commercial action")
	}

	actions, _ := repo.ListCommercialActions(context.Background(), org, uuid.Nil, false, 50)
	if len(actions) != 1 || actions[0].SourceLeadID != "webcfg-real-1" {
		t.Fatalf("real-only totals contaminated: %+v", actions)
	}
	leads, _ := repo.ListInboundLeads(context.Background(), org, false, 50)
	if len(leads) != 5 {
		t.Fatalf("receipts=%d", len(leads))
	}
	alert, _ := svc.alertStore().GetOperatorAlertByLead(context.Background(), org, "syn-existing")
	if alert == nil || !alert.Synthetic {
		t.Fatalf("synthetic operator alert contract drifted: %+v", alert)
	}
	queue, xerr := svc.CollectInboundNow(context.Background(), org)
	if xerr != nil {
		t.Fatal(xerr)
	}
	for _, item := range queue {
		if item.LeadID == "syn-existing" || item.LeadID == "syn-missing" || item.LeadID == "qa-probe" || item.LeadID == "ops-probe" {
			t.Fatalf("synthetic leaked into INBOUND NOW: %s", item.LeadID)
		}
	}
}
