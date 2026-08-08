package confenge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// FeedSyncResult is the outcome of one manifest sync cycle.
type FeedSyncResult struct {
	Status         string         `json:"status"` // completed|noop|failed|partial
	SnapshotHash   string         `json:"snapshot_hash,omitempty"`
	RunID          string         `json:"run_id,omitempty"`
	ChunksTotal    int            `json:"chunks_total"`
	ChunksImported int            `json:"chunks_imported"`
	Deactivations  int            `json:"deactivations_applied"`
	SkippedSame    bool           `json:"skipped_same_snapshot"`
	Errors         []string       `json:"errors,omitempty"`
	Counts         map[string]int `json:"counts,omitempty"`
}

// outreachManifest is confenge.outreach.manifest.v1 (extra-cli export).
type outreachManifest struct {
	SchemaVersion string `json:"schema_version"`
	GeneratedAt   string `json:"generated_at"`
	Source        struct {
		System       string `json:"system"`
		RunID        string `json:"run_id"`
		SnapshotHash string `json:"snapshot_hash"`
		RepoSHA      string `json:"repo_sha"`
		ProfileID    string `json:"profile_id"`
		ProfileVer   string `json:"profile_version"`
	} `json:"source"`
	LeadCount       int              `json:"lead_count"`
	ChunkCount      int              `json:"chunk_count"`
	Chunks          []manifestChunk  `json:"chunks"`
	Deactivations   []map[string]any `json:"deactivations"`
	DeactivationCnt int              `json:"deactivation_count"`
}

type manifestChunk struct {
	File        string `json:"file"`
	ChunkIndex  int    `json:"chunk_index"`
	ContentHash string `json:"content_hash"`
	LeadCount   int    `json:"lead_count"`
	HasMore     bool   `json:"has_more"`
}

// org-level single-flight so two sync workers cannot double-import.
var feedSyncLocks sync.Map // orgID string → *sync.Mutex

func orgFeedSyncLock(orgID uuid.UUID) *sync.Mutex {
	v, _ := feedSyncLocks.LoadOrStore(orgID.String(), &sync.Mutex{})
	return v.(*sync.Mutex)
}

// SyncFeedManifest fetches a confenge.outreach.manifest.v1, validates chunks,
// imports in order, applies deactivations. Fail-closed on hash mismatch.
// Never deletes DNC/human state. Never auto-generates or sends.
func (s *service) SyncFeedManifest(ctx context.Context, orgID uuid.UUID, userID *uuid.UUID, manifestURI string) (*FeedSyncResult, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	uri := strings.TrimSpace(manifestURI)
	if uri == "" {
		uri = strings.TrimSpace(s.cfg.ManifestURL)
	}
	if uri == "" {
		// Fall back to FeedURL when it points at a manifest.
		if strings.HasSuffix(strings.ToLower(s.cfg.FeedURL), "manifest.json") {
			uri = s.cfg.FeedURL
		}
	}
	if uri == "" {
		return nil, errx.New(errx.BadRequest, "manifest URI not configured")
	}

	mu := orgFeedSyncLock(orgID)
	if !mu.TryLock() {
		return nil, errx.New(errx.Conflict, "feed sync already in progress for this organization")
	}
	defer mu.Unlock()

	result := &FeedSyncResult{Status: "failed", Counts: map[string]int{}}
	fetcher := &FeedFetcher{
		AllowedHosts: s.cfg.AllowedHosts,
		Token:        s.cfg.FeedToken,
		MaxBytes:     s.cfg.MaxFeedPayloadBytes,
	}
	raw, err := fetcher.Fetch(ctx, uri)
	if err != nil {
		result.Errors = append(result.Errors, "manifest fetch: "+err.Error())
		return result, errx.New(errx.BadRequest, "manifest fetch failed: "+err.Error())
	}
	var man outreachManifest
	if err := json.Unmarshal(raw, &man); err != nil {
		return result, errx.New(errx.BadRequest, "invalid manifest JSON: "+err.Error())
	}
	if man.Source.SnapshotHash == "" || man.Source.RunID == "" {
		return result, errx.New(errx.BadRequest, "manifest missing source.snapshot_hash or run_id")
	}
	result.SnapshotHash = man.Source.SnapshotHash
	result.RunID = man.Source.RunID
	result.ChunksTotal = len(man.Chunks)

	// Idempotent: same snapshot already applied → noop
	lastSnap, lastRun := s.lastAppliedSnapshot(ctx, orgID)
	if lastSnap == man.Source.SnapshotHash && lastSnap != "" {
		result.Status = "noop"
		result.SkippedSame = true
		return result, nil
	}
	_ = lastRun

	baseURI := manifestBaseURI(uri)
	imported := 0
	var partialErrs []string
	for _, ch := range man.Chunks {
		if strings.TrimSpace(ch.File) == "" {
			partialErrs = append(partialErrs, "chunk missing file name")
			continue
		}
		chunkURI := joinURI(baseURI, ch.File)
		chunkRaw, err := fetcher.Fetch(ctx, chunkURI)
		if err != nil {
			partialErrs = append(partialErrs, fmt.Sprintf("%s fetch: %v", ch.File, err))
			break // fail closed — do not mark snapshot complete
		}
		if ch.ContentHash != "" {
			sum := sha256.Sum256(chunkRaw)
			got := hex.EncodeToString(sum[:])
			if got != ch.ContentHash {
				partialErrs = append(partialErrs, fmt.Sprintf("%s hash mismatch", ch.File))
				break
			}
		}
		idem := fmt.Sprintf("sync:%s:%s:%d", orgID, man.Source.SnapshotHash, ch.ChunkIndex)
		_, xerr := s.ImportFromBytes(ctx, orgID, userID, chunkRaw, ImportOptions{
			IdempotencyKey: idem,
			SourceURI:      chunkURI,
		})
		if xerr != nil {
			partialErrs = append(partialErrs, fmt.Sprintf("%s import: %s", ch.File, xerr.Message))
			break
		}
		imported++
	}
	result.ChunksImported = imported
	result.Errors = partialErrs

	// Apply deactivations only after all chunks imported successfully.
	if len(partialErrs) == 0 {
		if n, err := s.ApplyDeactivations(ctx, orgID, man.Deactivations); err == nil {
			result.Deactivations = n
		}
		s.rememberSnapshot(ctx, orgID, man.Source.SnapshotHash, man.Source.RunID, uri, "completed", result)
		result.Status = "completed"
		return result, nil
	}

	// Partial: do NOT mark snapshot complete (Warmbly must not treat as success).
	result.Status = "partial"
	s.rememberSnapshot(ctx, orgID, "", "", uri, "partial", result)
	return result, errx.New(errx.BadRequest, "feed sync partial: "+strings.Join(partialErrs, "; "))
}

func manifestBaseURI(uri string) string {
	// strip trailing filename
	if i := strings.LastIndex(uri, "/"); i >= 0 {
		return uri[:i+1]
	}
	return uri
}

func joinURI(base, file string) string {
	if strings.Contains(file, "://") {
		return file
	}
	file = path.Base(strings.ReplaceAll(file, "\\", "/"))
	if strings.HasPrefix(base, "file://") {
		return strings.TrimRight(base, "/") + "/" + file
	}
	return strings.TrimRight(base, "/") + "/" + file
}

// In-memory last-snapshot fallback when feed_sync_state table not yet used by repo.
var lastSnapMu sync.Mutex
var lastSnapByOrg = map[string]struct {
	Snap, Run string
	At        time.Time
}{}

func (s *service) lastAppliedSnapshot(ctx context.Context, orgID uuid.UUID) (snap, run string) {
	lastSnapMu.Lock()
	defer lastSnapMu.Unlock()
	if v, ok := lastSnapByOrg[orgID.String()]; ok {
		return v.Snap, v.Run
	}
	// Best-effort: last import run snapshot
	runs, err := s.repo.ListImportRuns(ctx, orgID, 1)
	if err == nil && len(runs) > 0 && runs[0].Status == models.OutreachImportCompleted {
		return runs[0].SnapshotHash, runs[0].SourceRunID
	}
	return "", ""
}

func (s *service) rememberSnapshot(ctx context.Context, orgID uuid.UUID, snap, run, uri, status string, res *FeedSyncResult) {
	_ = ctx
	_ = uri
	_ = res
	if snap == "" {
		return
	}
	lastSnapMu.Lock()
	lastSnapByOrg[orgID.String()] = struct {
		Snap, Run string
		At        time.Time
	}{Snap: snap, Run: run, At: time.Now().UTC()}
	lastSnapMu.Unlock()
}
