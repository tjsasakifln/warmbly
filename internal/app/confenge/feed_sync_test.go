package confenge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestSyncFeedManifestIdempotentAndHashFailClosed(t *testing.T) {
	dir := t.TempDir()
	org := uuid.New()
	user := uuid.New()
	r := newMemRepo()
	svc := NewService(Config{
		Enabled: true, DefaultDailyLimit: 100, MaxInitialEmailWords: 120,
		RequireHumanApproval: true, MaxFeedPayloadBytes: 8 << 20,
	}, r, nil).(*service)

	lead := sampleLeadWithActivation(70, ActivationActionableNow)
	chunk := Feed{
		SchemaVersion: "confenge.outreach.v1",
		GeneratedAt:   "2026-08-08T10:00:00Z",
		Source: FeedSource{
			System: "extra-cli", RunID: "run-sync-1", SnapshotHash: "snap-sync-1",
			ProfileID: "p", ProfileVersion: "1",
		},
		Pagination: FeedPagination{HasMore: false},
		Leads:      []FeedLead{lead},
	}
	// Match schema constant
	chunk.SchemaVersion = modelsOutreachSchema()
	chunkRaw, err := json.MarshalIndent(chunk, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	chunkRaw = append(chunkRaw, '\n')
	sum := sha256.Sum256(chunkRaw)
	chash := hex.EncodeToString(sum[:])
	chunkPath := filepath.Join(dir, "chunk_0000.json")
	if err := os.WriteFile(chunkPath, chunkRaw, 0o644); err != nil {
		t.Fatal(err)
	}

	man := map[string]any{
		"schema_version": "confenge.outreach.manifest.v1",
		"generated_at":   "2026-08-08T10:00:00Z",
		"source": map[string]any{
			"system": "extra-cli", "run_id": "run-sync-1", "snapshot_hash": "snap-sync-1",
			"profile_id": "p", "profile_version": "1",
		},
		"lead_count":  1,
		"chunk_count": 1,
		"chunks": []map[string]any{
			{"file": "chunk_0000.json", "chunk_index": 0, "content_hash": chash, "lead_count": 1},
		},
		"deactivations": []any{},
	}
	manRaw, _ := json.MarshalIndent(man, "", "  ")
	manPath := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(manPath, manRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	uri := "file://" + manPath

	ctx := context.Background()
	res, xerr := svc.SyncFeedManifest(ctx, org, &user, uri)
	if xerr != nil {
		t.Fatalf("sync1: %v", xerr)
	}
	if res.Status != "completed" || res.ChunksImported != 1 {
		t.Fatalf("unexpected res: %+v", res)
	}
	acc, _ := r.GetAccountByCNPJ(ctx, org, "11222333000181")
	if acc == nil || acc.ActivationState != ActivationActionableNow {
		t.Fatal("account not imported with activation")
	}

	// Same snapshot → noop
	res2, xerr := svc.SyncFeedManifest(ctx, org, &user, uri)
	if xerr != nil {
		t.Fatalf("sync2: %v", xerr)
	}
	if !res2.SkippedSame || res2.Status != "noop" {
		t.Fatalf("expected noop, got %+v", res2)
	}

	// Corrupt hash → fail closed, no success
	manBad := man
	manBad["source"] = map[string]any{
		"system": "extra-cli", "run_id": "run-sync-2", "snapshot_hash": "snap-sync-2",
		"profile_id": "p", "profile_version": "1",
	}
	manBad["chunks"] = []map[string]any{
		{"file": "chunk_0000.json", "chunk_index": 0, "content_hash": "deadbeef", "lead_count": 1},
	}
	manBadRaw, _ := json.MarshalIndent(manBad, "", "  ")
	manBadPath := filepath.Join(dir, "manifest_bad.json")
	_ = os.WriteFile(manBadPath, manBadRaw, 0o644)
	res3, xerr := svc.SyncFeedManifest(ctx, org, &user, "file://"+manBadPath)
	if xerr == nil {
		t.Fatal("expected hash mismatch failure")
	}
	if res3 != nil && res3.Status == "completed" {
		t.Fatal("must not complete on bad hash")
	}
}

func modelsOutreachSchema() string {
	// Keep in sync with models.OutreachSchemaV1 without importing cycle issues in test helper name.
	return "confenge.outreach.v1"
}
