package intel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPGStoreSearchObservationUniqueReplayAndListWindow(t *testing.T) {
	dsn := os.Getenv("WARMBLY_TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = os.Getenv("INTEL_PG_TEST_URL")
	}
	if dsn == "" {
		t.Skip("WARMBLY_TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	userID, orgID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, first_name, last_name, email) VALUES ($1,'So','Obs',$2)`, userID, "so-obs-"+userID.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (id, name, slug, owner_user_id) VALUES ($1,'So Obs',$2,$3)`, orgID, "so-obs-"+orgID.String(), userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM outreach_intel_search_observations WHERE organization_id=$1`, orgID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, orgID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id=$1`, userID)
		pool.Close()
	})

	st := NewPGStore(pool, orgID.String())
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	obs := SearchObservation{
		OrganizationID: orgID.String(), EventID: "pg-so-1", ReceiptID: "pg-so-1",
		Schema: EventSchemaV1, Version: OrganicDiscoveryContract, Type: EventSearchObservation,
		Source: ProducerCONFENGEWeb, OrganicSource: SourceOrganicSearch,
		AssetID: "asset-sl", LandingPath: "/guias/segunda-leitura", Window: Window28dComplete,
		Eligible: IntPtr(9), Appeared: IntPtr(3), Clicked: nil, Engaged: IntPtr(0),
		Coverage: CoverageObserved, Freshness: "gsc", MeasurementAt: now,
		ProducerSHA: "deadbeef", PayloadHash: "hash-1", Synthetic: true,
		RecordKind: RecordKindSynthetic, ConsentPolicy: ConsentPolicyNotApplicable,
	}
	saved, created, err := st.PutSearchObservation(obs)
	if err != nil || !created {
		t.Fatalf("put: created=%v err=%v", created, err)
	}
	again, created, err := st.PutSearchObservation(obs)
	if err != nil || created {
		t.Fatalf("unique replay must not insert: created=%v err=%v", created, err)
	}
	if again.EventID != saved.EventID {
		t.Fatalf("replay row=%+v", again)
	}
	listed, err := st.ListSearchObservations(orgID.String(), Window28dComplete)
	if err != nil || len(listed) != 1 {
		t.Fatalf("list window=%d err=%v", len(listed), err)
	}
	empty, err := st.ListSearchObservations(orgID.String(), Window7dComplete)
	if err != nil || len(empty) != 0 {
		t.Fatalf("other window leaked: %d", len(empty))
	}
	got, err := st.GetSearchObservation(orgID.String(), "pg-so-1")
	if err != nil || got == nil || got.Clicked != nil || got.Engaged == nil || *got.Engaged != 0 {
		t.Fatalf("nullable/zero mismatch: %+v err=%v", got, err)
	}
	raw, _ := json.Marshal(got)
	if ContainsForbiddenQuery(raw) {
		t.Fatal("pg payload leaked query")
	}
	fmt.Printf("PG_SEARCH_OBS created=%v replay=%v listed=%d clicked_null=%v engaged0=%v\n",
		true, !created, len(listed), got.Clicked == nil, *got.Engaged == 0)
}
