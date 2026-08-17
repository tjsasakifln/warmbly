package intel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func versionedExceptionCodes() []string {
	return []string{
		ExceptionOrphan,
		ExceptionDuplicate,
		ExceptionConflictingAccount,
		ExceptionMissingVersion,
		ExceptionStaleAttribution,
		ExceptionOutOfOrder,
		ExceptionUnconfirmedWon,
		ExceptionUnconfirmedLost,
		ExceptionUnavailable,
		ExceptionImpossibleTransition,
		ExceptionNegativeLatency,
		ExceptionOverlappingLatency,
		ExceptionOutboundAsInbound,
		ExceptionInvalidAssetFamily,
		ExceptionMissingAttribution,
	}
}

func TestMemoryStorePersistsAndListsEveryVersionedExceptionCode(t *testing.T) {
	st := NewMemoryStore()
	org := "org-exception-codes"
	for i, code := range versionedExceptionCodes() {
		err := st.PutException(Exception{
			OrganizationID: org,
			Code:           code,
			Identity:       fmt.Sprintf("id-%s", code),
			Reason:         "persist-proof-" + code,
			Held:           i%2 == 0,
		})
		if err != nil {
			t.Fatalf("PutException %s: %v", code, err)
		}
	}
	listed, err := st.ListExceptions(org)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]Exception{}
	for _, ex := range listed {
		if want := "persist-proof-" + ex.Code; ex.Reason != want {
			t.Fatalf("CHECK-failing rewrite: code=%s reason=%q want %q", ex.Code, ex.Reason, want)
		}
		seen[ex.Code] = ex
	}
	for _, code := range versionedExceptionCodes() {
		if _, ok := seen[code]; !ok {
			t.Fatalf("ListExceptions missing %s (have %d)", code, len(seen))
		}
	}
	fmt.Printf("EXCEPTION_PERSIST_LIST codes=%d store=MemoryStore\n", len(versionedExceptionCodes()))
}

func TestMigration102CHECKContainsEveryVersionedExceptionCode(t *testing.T) {
	path := filepath.Join("..", "..", "..", "infrastructure", "db", "migrations", "000102_outreach_intel_event_exception_codes.up.sql")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, code := range versionedExceptionCodes() {
		quoted := "'" + code + "'"
		if !strings.Contains(body, quoted) {
			t.Fatalf("migration 000102 missing exception code %s", code)
		}
	}
	fmt.Printf("MIGRATION_102 exception_codes_present=true bytes=%d\n", len(raw))
}

func TestRunFixtureReportJSONAndMarkdownByteStable(t *testing.T) {
	a := RunFixtureReport(loopOrg, SyntheticMonth, true)
	b := RunFixtureReport(loopOrg, SyntheticMonth, true)
	rawA, err := ReportJSON(a)
	if err != nil {
		t.Fatal(err)
	}
	rawB, err := ReportJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(rawA) != string(rawB) {
		t.Fatal("JSON reports are not byte-stable")
	}
	mdA := ReportMarkdown(a)
	mdB := ReportMarkdown(b)
	if mdA != mdB {
		t.Fatal("markdown reports are not byte-stable")
	}
	if strings.Contains(mdA, "Generated at") {
		t.Fatal("markdown still embeds a generated-at clock")
	}
	if a.Latency.Baseline != BaselineSynthetic || strings.Contains(string(rawA), BaselineObserved) {
		t.Fatalf("synthetic report claimed observed baseline=%s", a.Latency.Baseline)
	}
	if a.CausalProof || strings.Contains(string(rawA), `"causal_proof": true`) {
		t.Fatal("synthetic report claimed causal_proof")
	}
	if !strings.Contains(string(rawA), BaselineSynthetic) {
		t.Fatal("synthetic report missing BASELINE_SYNTHETIC")
	}
	fmt.Printf("REPORT_STABLE json_bytes=%d md_bytes=%d baseline=%s\n", len(rawA), len(mdA), a.Latency.Baseline)
}

func TestRunFixtureReportRealEmptyNotObserved(t *testing.T) {
	rep := RunFixtureReport(loopOrg, SyntheticMonth, false)
	raw, err := ReportJSON(rep)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.RealEmpty {
		t.Fatalf("fixture-only store with include_synthetic=false must be real_empty: %+v", rep)
	}
	if rep.InboundQualifiedPipeline != 0 || rep.Won != 0 || rep.ValidLeads != 0 {
		t.Fatalf("synthetics leaked into real report: iqp=%d won=%d leads=%d", rep.InboundQualifiedPipeline, rep.Won, rep.ValidLeads)
	}
	if rep.Latency.Baseline == BaselineObserved || strings.Contains(string(raw), BaselineObserved) {
		t.Fatal("real_empty report claimed BASELINE_OBSERVED")
	}
	if !strings.Contains(strings.ToLower(strings.Join(rep.Blockers, " ")), "real_empty") {
		t.Fatalf("real_empty blocker missing: %+v", rep.Blockers)
	}
	fmt.Printf("REAL_EMPTY iqp=%d baseline=%s real_empty=%v\n", rep.InboundQualifiedPipeline, rep.Latency.Baseline, rep.RealEmpty)
}

func TestPGStorePersistsAndListsEveryVersionedExceptionCode(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, first_name, last_name, email) VALUES ($1,'Ex','Codes',$2)`, userID, "ex-codes-"+userID.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (id, name, slug, owner_user_id) VALUES ($1,'Ex Codes',$2,$3)`, orgID, "ex-codes-"+orgID.String(), userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM outreach_intel_exceptions WHERE organization_id=$1`, orgID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, orgID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id=$1`, userID)
		pool.Close()
	})

	st := NewPGStore(pool, orgID.String())
	for i, code := range versionedExceptionCodes() {
		err := st.PutException(Exception{
			OrganizationID: orgID.String(),
			Code:           code,
			Identity:       fmt.Sprintf("pg-%s", code),
			Reason:         "persist-proof-" + code,
			Held:           i%2 == 0,
		})
		if err != nil {
			t.Fatalf("PG PutException %s: %v", code, err)
		}
	}
	listed, err := st.ListExceptions(orgID.String())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, ex := range listed {
		seen[ex.Code] = true
	}
	for _, code := range versionedExceptionCodes() {
		if !seen[code] {
			t.Fatalf("PG ListExceptions missing %s (have %d)", code, len(seen))
		}
	}
	fmt.Printf("EXCEPTION_PERSIST_LIST codes=%d store=PGStore\n", len(versionedExceptionCodes()))
}
