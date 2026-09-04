package confenge

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/models"
)

// Collection vs item schema identities are the MeetCFG admission contract.
// Labelling the collection with CONFENGE_SALES_CONTEXT/1.0 is the producer
// bug that yields SCHEMA_MISMATCH_COLLECTION.

func TestSalesContextCollectionSchemaIsExportNotDossier(t *testing.T) {
	ctx := context.Background()
	svc, orgID := newWebIntentService(t)
	if _, xerr := svc.PersistHandRaise(ctx, HandRaise{
		OrganizationID: orgID, AccountID: uuid.New(),
		Signal: SignalRequestHumanReview, EngineLane: EngineLaneConfengeWeb,
		OccurredAt: time.Now().UTC(), Evidence: "asked", SubjectKey: "company:acme-holdings",
		Origin: EngineLaneConfengeWeb, Receipt: "receipt-1", InboundOnly: true,
	}); xerr != nil {
		t.Fatal(xerr)
	}
	export, xerr := svc.ExportSalesContext(ctx, orgID, 50, "")
	if xerr != nil {
		t.Fatal(xerr)
	}
	if export.Schema == SalesContextSchemaV1 {
		t.Fatalf("collection labeled with individual dossier schema %q (SCHEMA_MISMATCH_COLLECTION)", export.Schema)
	}
	if export.Schema != SalesContextExportSchemaV1 {
		t.Fatalf("collection schema = %q want %q", export.Schema, SalesContextExportSchemaV1)
	}
	if len(export.Items) != 1 {
		t.Fatalf("items = %d", len(export.Items))
	}
	item := export.Items[0]
	if item.Schema != SalesContextSchemaV1 {
		t.Fatalf("item schema = %q want %q", item.Schema, SalesContextSchemaV1)
	}
	if item.Schema == export.Schema {
		t.Fatal("collection and item share a schema identity")
	}
	if item.Wrapper.Kind != salesContextWrapperAdmission || item.Wrapper.MappedFrom != salesContextMappedFrom {
		t.Fatalf("wrapper = %+v", item.Wrapper)
	}
	if !item.InboundOnly {
		t.Fatal("inbound_only dropped")
	}
	if item.Origin != EngineLaneConfengeWeb || item.Lane != EngineLaneConfengeWeb {
		t.Fatalf("origin/lane = %q/%q", item.Origin, item.Lane)
	}
	if item.Receipt != "receipt-1" || item.HandraiserID != item.ActionID.String() {
		t.Fatalf("receipt/handraiser_id = %q/%q", item.Receipt, item.HandraiserID)
	}
	if item.Intent == "" {
		t.Fatal("intent missing")
	}
}

func TestSalesContextDoesNotInventFacts(t *testing.T) {
	ctx := context.Background()
	svc, orgID := newWebIntentService(t)
	if _, xerr := svc.PersistHandRaise(ctx, HandRaise{
		OrganizationID: orgID, AccountID: uuid.New(),
		Signal: SignalRequestDeepDive, EngineLane: EngineLaneConfengeWeb,
		OccurredAt: time.Now().UTC(), Evidence: "asked",
	}); xerr != nil {
		t.Fatal(xerr)
	}
	export, xerr := svc.ExportSalesContext(ctx, orgID, 50, "")
	if xerr != nil {
		t.Fatal(xerr)
	}
	raw, err := json.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}
	if found := SalesContextJSONHasInventedField(raw); found != "" {
		t.Fatalf("invented field %q in %s", found, raw)
	}
	item := export.Items[0]
	if item.CompanyName != "" && item.CompanyRef == "" {
		t.Fatal("company_name present without company_ref copy")
	}
	if item.Freshness != "" {
		t.Fatalf("invented freshness %q", item.Freshness)
	}
}

func TestSalesContextReplay100IsOneLogicalRepresentation(t *testing.T) {
	ctx := context.Background()
	svc, orgID := newWebIntentService(t)
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	svc.nowFn = func() time.Time { return now }
	raise := HandRaise{
		OrganizationID: orgID, AccountID: uuid.New(),
		Signal: SignalRequestHumanReview, EngineLane: EngineLaneConfengeWeb,
		OccurredAt: now, Evidence: "asked", SubjectKey: "company:replay-co",
		Receipt: "stable-receipt",
	}
	if _, xerr := svc.PersistHandRaise(ctx, raise); xerr != nil {
		t.Fatal(xerr)
	}
	seen := map[string]struct{}{}
	var first []byte
	for i := 0; i < 100; i++ {
		export, xerr := svc.ExportSalesContext(ctx, orgID, 50, "")
		if xerr != nil {
			t.Fatalf("replay %d: %v", i, xerr)
		}
		if len(export.Items) != 1 {
			t.Fatalf("replay %d items=%d", i, len(export.Items))
		}
		id := SalesContextLogicalID(export.Items[0])
		seen[id] = struct{}{}
		clone := *export
		clone.GeneratedAt = time.Time{}
		raw, err := json.Marshal(clone)
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = raw
			continue
		}
		if string(raw) != string(first) {
			t.Fatalf("replay %d produced a second logical representation", i)
		}
	}
	if len(seen) != 1 {
		t.Fatalf("logical ids = %d want 1", len(seen))
	}
}

func TestSalesContextPaginationIsDeterministic(t *testing.T) {
	ctx := context.Background()
	svc, orgID := newWebIntentService(t)
	base := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if _, xerr := svc.PersistHandRaise(ctx, HandRaise{
			OrganizationID: orgID, AccountID: uuid.New(),
			Signal: SignalRequestHumanReview, EngineLane: EngineLaneConfengeWeb,
			OccurredAt: base.Add(time.Duration(i) * time.Minute), Evidence: "asked",
			SubjectKey: "company:page-" + uuid.NewString(),
		}); xerr != nil {
			t.Fatal(xerr)
		}
	}
	first, xerr := svc.ExportSalesContext(ctx, orgID, 1, "")
	if xerr != nil || len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("first page = %+v err=%v", first, xerr)
	}
	second, xerr := svc.ExportSalesContext(ctx, orgID, 1, first.NextCursor)
	if xerr != nil || len(second.Items) != 1 {
		t.Fatalf("second page = %+v err=%v", second, xerr)
	}
	if first.Items[0].ActionID == second.Items[0].ActionID {
		t.Fatal("cursor did not advance")
	}
	if !first.Items[0].CreatedAt.After(second.Items[0].CreatedAt) && first.Items[0].ActionID.String() <= second.Items[0].ActionID.String() {
		t.Fatalf("order is not newest-first: %s then %s", first.Items[0].CreatedAt, second.Items[0].CreatedAt)
	}
	again, xerr := svc.ExportSalesContext(ctx, orgID, 1, first.NextCursor)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if again.Items[0].ActionID != second.Items[0].ActionID {
		t.Fatal("cursor replay is not deterministic")
	}
	if _, xerr := svc.ExportSalesContext(ctx, orgID, 1, "not-a-cursor"); xerr == nil {
		t.Fatal("invalid cursor was accepted")
	}
}

func TestSalesContextHandraiserWrapperIsExplicitNotGuessed(t *testing.T) {
	ctx := context.Background()
	svc, orgID := newWebIntentService(t)
	if _, xerr := svc.PersistHandRaise(ctx, HandRaise{
		OrganizationID: orgID, AccountID: uuid.New(),
		Signal: SignalPositiveReplyFirstTouch, EngineLane: EngineLaneFirstTouch,
		OccurredAt: time.Now().UTC(), Evidence: "reply",
	}); xerr != nil {
		t.Fatal(xerr)
	}
	export, xerr := svc.ExportSalesContext(ctx, orgID, 50, "")
	if xerr != nil {
		t.Fatal(xerr)
	}
	item := export.Items[0]
	if item.Wrapper.Kind != salesContextWrapperHandraiser {
		t.Fatalf("wrapper kind = %q", item.Wrapper.Kind)
	}
	if item.InboundOnly {
		t.Fatal("first-touch reply was marked inbound_only")
	}
}

func TestSalesContextNetNewPreservesOriginIntentReceipt(t *testing.T) {
	svc, orgID := newEmptyWebIntentService(t)
	svc.WireIntelWatchSubscriptions(&fakeWatchSubs{})
	env := validWebIntent(intel.WebIntentRequestHumanReview)
	env.ContactEmail = "sales-context@example.com"
	res, xerr := svc.IngestWebIntent(context.Background(), orgID, env, time.Now().UTC())
	if xerr != nil || res.ActionID == nil {
		t.Fatalf("ingest: %+v err=%v", res, xerr)
	}
	export, xerr := svc.ExportSalesContext(context.Background(), orgID, 50, "")
	if xerr != nil {
		t.Fatal(xerr)
	}
	if len(export.Items) != 1 {
		t.Fatalf("items = %d", len(export.Items))
	}
	item := export.Items[0]
	if item.Schema != SalesContextSchemaV1 || export.Schema != SalesContextExportSchemaV1 {
		t.Fatalf("schemas collection=%q item=%q", export.Schema, item.Schema)
	}
	if !item.InboundOnly || item.Wrapper.Kind != salesContextWrapperAdmission {
		t.Fatalf("admission wrapper lost: %+v", item)
	}
	if item.Receipt != res.Receipt || item.Origin != EngineLaneConfengeWeb {
		t.Fatalf("receipt/origin = %q/%q", item.Receipt, item.Origin)
	}
	acc, err := svc.repo.GetAccount(context.Background(), orgID, *res.AccountID)
	if err != nil || acc == nil || !models.AccountIsInboundOnly(acc) {
		t.Fatal("inbound_only account missing")
	}
}
