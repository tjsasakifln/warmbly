package confenge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
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

const feedChunkStaleImportRecoveryAfter = 2 * time.Minute

const (
	maxOutreachManifestLeads  = 100_000
	maxOutreachManifestChunks = 1_000
	maxOutreachStagedBytes    = int64(1 << 30)
)

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

	// Process-local single-flight + durable advisory lock when PG is available.
	mu := orgFeedSyncLock(orgID)
	if !mu.TryLock() {
		return nil, errx.New(errx.Conflict, "feed sync already in progress for this organization")
	}
	defer mu.Unlock()

	advKey := feedSyncAdvisoryKey(orgID)
	locked, lockErr := s.repo.TryAdvisoryLock(ctx, advKey)
	if lockErr != nil {
		return nil, errx.New(errx.ServiceUnavailable, "feed sync lock unavailable")
	}
	if !locked {
		return nil, errx.New(errx.Conflict, "feed sync already in progress (advisory lock)")
	}
	defer func() { _ = s.repo.AdvisoryUnlock(ctx, advKey) }()

	result := &FeedSyncResult{Status: "failed", Counts: map[string]int{}}
	// Read durable last snapshot BEFORE mutating status (running write must not wipe it).
	var lastGeneratedAt *time.Time
	current, stateErr := s.repo.GetFeedSyncState(ctx, orgID)
	if stateErr != nil {
		return nil, errx.New(errx.ServiceUnavailable, "feed sync state unavailable")
	}
	lastSnap, lastRun := "", ""
	if current != nil {
		lastSnap, lastRun = current.LastSnapshotHash, current.LastRunID
		lastGeneratedAt = current.SourceGeneratedAt
	} else {
		lastSnap, lastRun = s.lastAppliedSnapshot(ctx, orgID)
		if runs, err := s.repo.ListImportRuns(ctx, orgID, 1); err != nil {
			return nil, errx.New(errx.ServiceUnavailable, "feed import history unavailable")
		} else if len(runs) > 0 && runs[0].Status == models.OutreachImportCompleted {
			lastGeneratedAt = runs[0].SourceGeneratedAt
		}
	}
	now := time.Now().UTC()
	if err := s.repo.UpsertFeedSyncState(ctx, &models.OutreachFeedSyncState{
		OrganizationID:   orgID,
		LastSnapshotHash: lastSnap,
		LastRunID:        lastRun,
		LastManifestURI:  uri,
		LastAttemptAt:    &now,
		LastStatus:       "running",
	}); err != nil {
		return nil, errx.New(errx.ServiceUnavailable, "feed sync state unavailable")
	}

	fetcher := &FeedFetcher{
		AllowedHosts: s.cfg.AllowedHosts,
		Token:        s.cfg.FeedToken,
		MaxBytes:     s.cfg.MaxFeedPayloadBytes,
		AllowFile:    !strings.EqualFold(s.cfg.AppEnv, "prod") && !strings.EqualFold(s.cfg.AppEnv, "production"),
		RequireHTTPS: strings.EqualFold(s.cfg.AppEnv, "prod") || strings.EqualFold(s.cfg.AppEnv, "production"),
	}
	raw, err := fetcher.Fetch(ctx, uri)
	if err != nil {
		result.Errors = append(result.Errors, "manifest fetch: "+err.Error())
		s.persistFeedSync(ctx, orgID, lastSnap, lastRun, uri, "failed", result, false, nil)
		return result, errx.New(errx.BadRequest, "manifest fetch failed: "+err.Error())
	}
	var man outreachManifest
	if err := json.Unmarshal(raw, &man); err != nil {
		s.persistFeedSync(ctx, orgID, lastSnap, lastRun, uri, "failed", result, false, nil)
		return result, errx.New(errx.BadRequest, "invalid manifest JSON: "+err.Error())
	}
	if man.Source.SnapshotHash == "" || man.Source.RunID == "" {
		s.persistFeedSync(ctx, orgID, lastSnap, lastRun, uri, "failed", result, false, nil)
		return result, errx.New(errx.BadRequest, "manifest missing source.snapshot_hash or run_id")
	}
	if validationErr := validateOutreachManifest(&man); validationErr != nil {
		s.persistFeedSync(ctx, orgID, lastSnap, lastRun, uri, "failed", result, false, nil)
		return result, errx.New(errx.BadRequest, validationErr.Error())
	}
	generatedAt, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(man.GeneratedAt))
	if parseErr != nil || generatedAt.After(time.Now().UTC().Add(5*time.Minute)) {
		s.persistFeedSync(ctx, orgID, lastSnap, lastRun, uri, "failed", result, false, nil)
		return result, errx.New(errx.BadRequest, "manifest generated_at is missing or invalid")
	}
	if lastGeneratedAt != nil && (generatedAt.Before(lastGeneratedAt.UTC()) ||
		(generatedAt.Equal(lastGeneratedAt.UTC()) && man.Source.SnapshotHash != lastSnap)) {
		result.Errors = append(result.Errors, "snapshot rollback rejected")
		s.persistFeedSync(ctx, orgID, lastSnap, lastRun, uri, "failed", result, false, nil)
		return result, errx.New(errx.Conflict, "manifest is older than the applied authoritative snapshot")
	}
	result.SnapshotHash = man.Source.SnapshotHash
	result.RunID = man.Source.RunID
	result.ChunksTotal = len(man.Chunks)

	// Idempotent: same snapshot already applied → noop
	if lastSnap == man.Source.SnapshotHash && lastSnap != "" {
		result.Status = "noop"
		result.SkippedSame = true
		result.RunID = lastRun
		restorePersistedFeedCounts(result.Counts, current)
		s.persistFeedSync(ctx, orgID, man.Source.SnapshotHash, lastRun, uri, "completed", result, true, &generatedAt)
		return result, nil
	}

	baseURI := manifestBaseURI(uri)
	var partialErrs []string
	stagingDir, stageErr := os.MkdirTemp("", "warmbly-confenge-feed-")
	if stageErr != nil {
		s.persistFeedSync(ctx, orgID, lastSnap, lastRun, uri, "failed", result, false, nil)
		return result, errx.New(errx.ServiceUnavailable, "feed staging unavailable")
	}
	defer os.RemoveAll(stagingDir)
	chunkPaths := make([]string, 0, len(man.Chunks))
	seenCNPJ := make(map[string]struct{}, man.LeadCount)
	seenLeadID := make(map[string]struct{}, man.LeadCount)
	var stagedBytes int64
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
		chunkFeed, normalizeErr := DetectAndNormalize(chunkRaw)
		if normalizeErr != nil {
			partialErrs = append(partialErrs, fmt.Sprintf("%s invalid feed: %v", ch.File, normalizeErr))
			break
		}
		chunkGeneratedAt, timeErr := time.Parse(time.RFC3339, strings.TrimSpace(chunkFeed.GeneratedAt))
		if timeErr != nil || chunkFeed.Source.RunID != man.Source.RunID ||
			chunkFeed.Source.SnapshotHash != man.Source.SnapshotHash || !chunkGeneratedAt.Equal(generatedAt) {
			partialErrs = append(partialErrs, fmt.Sprintf("%s source metadata does not match manifest", ch.File))
			break
		}
		if len(chunkFeed.Leads) != ch.LeadCount {
			partialErrs = append(partialErrs, fmt.Sprintf("%s lead_count does not match payload", ch.File))
			break
		}
		for _, lead := range chunkFeed.Leads {
			cnpj := NormalizeCNPJ14(lead.Company.CNPJ14)
			leadID := strings.TrimSpace(lead.SourceLeadID)
			if _, exists := seenCNPJ[cnpj]; exists {
				partialErrs = append(partialErrs, fmt.Sprintf("%s duplicate cnpj14 across manifest", ch.File))
				break
			}
			if _, exists := seenLeadID[leadID]; exists {
				partialErrs = append(partialErrs, fmt.Sprintf("%s duplicate source_lead_id across manifest", ch.File))
				break
			}
			seenCNPJ[cnpj] = struct{}{}
			seenLeadID[leadID] = struct{}{}
		}
		if len(partialErrs) != 0 {
			break
		}
		stagedBytes += int64(len(chunkRaw))
		if stagedBytes > maxOutreachStagedBytes {
			partialErrs = append(partialErrs, "manifest staged payload exceeds limit")
			break
		}
		stagedPath := filepath.Join(stagingDir, fmt.Sprintf("chunk-%06d.json", ch.ChunkIndex))
		if err := os.WriteFile(stagedPath, chunkRaw, 0o600); err != nil {
			partialErrs = append(partialErrs, fmt.Sprintf("%s staging: %v", ch.File, err))
			break
		}
		chunkPaths = append(chunkPaths, stagedPath)
	}

	// Validate every remote object before the first database mutation. A corrupt,
	// missing, or mismatched later chunk must leave the current snapshot untouched.
	imported := 0
	for index, stagedPath := range chunkPaths {
		if len(partialErrs) != 0 {
			break
		}
		ch := man.Chunks[index]
		chunkRaw, readErr := os.ReadFile(stagedPath)
		if readErr != nil {
			partialErrs = append(partialErrs, fmt.Sprintf("%s staged read: %v", ch.File, readErr))
			break
		}
		chunkURI := joinURI(baseURI, ch.File)
		idem := fmt.Sprintf("sync:%s:%s:%d", orgID, man.Source.SnapshotHash, ch.ChunkIndex)
		importRun, xerr := s.ImportFromBytes(ctx, orgID, userID, chunkRaw, ImportOptions{
			IdempotencyKey:          idem,
			SourceURI:               chunkURI,
			resumeStaleRunningAfter: feedChunkStaleImportRecoveryAfter,
		})
		if xerr != nil {
			partialErrs = append(partialErrs, fmt.Sprintf("%s import: %s", ch.File, xerr.Message))
			break
		}
		if importRun == nil || importRun.Status != models.OutreachImportCompleted {
			partialErrs = append(partialErrs, fmt.Sprintf("%s import did not complete", ch.File))
			break
		}
		imported++
	}
	result.ChunksImported = imported
	result.Errors = partialErrs

	// Apply deactivations only after all chunks imported successfully.
	if len(partialErrs) == 0 {
		n, deactivateErr := s.ApplyDeactivations(ctx, orgID, man.Deactivations)
		if deactivateErr != nil {
			result.Status = "partial"
			result.Errors = append(result.Errors, "deactivations: "+deactivateErr.Error())
			s.persistFeedSync(ctx, orgID, lastSnap, lastRun, uri, "partial", result, false, nil)
			return result, errx.New(errx.ServiceUnavailable, "feed deactivations failed; snapshot not committed")
		}
		result.Deactivations = n
		delegatedRetired, delegatedErr := s.retireStaleDelegatedFirstTouches(ctx, orgID, man.Source.RunID, man.Source.SnapshotHash)
		if delegatedErr != nil {
			result.Status = "partial"
			result.Errors = append(result.Errors, "stale delegated first-touch retirement: "+delegatedErr.Error())
			s.persistFeedSync(ctx, orgID, lastSnap, lastRun, uri, "partial", result, false, nil)
			return result, errx.New(errx.ServiceUnavailable, "feed stale delegated first-touch retirement failed; snapshot not committed")
		}
		result.Counts["stale_delegated_first_touches_retired"] = delegatedRetired
		retired, requeued, staleErr := s.retireStaleReviewBacklog(ctx, orgID)
		if staleErr != nil {
			result.Status = "partial"
			result.Errors = append(result.Errors, "stale review retirement: "+staleErr.Error())
			s.persistFeedSync(ctx, orgID, lastSnap, lastRun, uri, "partial", result, false, nil)
			return result, errx.New(errx.ServiceUnavailable, "feed stale review retirement failed; snapshot not committed")
		}
		result.Counts["stale_reviews_retired"] = retired
		result.Counts["stale_review_accounts_requeued"] = requeued
		backlog, backlogErr := s.repo.MaterializeCurrentInitialBacklog(ctx, orgID, man.Source.RunID)
		if backlogErr != nil {
			result.Status = "partial"
			result.Errors = append(result.Errors, "current INITIAL backlog materialization: "+backlogErr.Error())
			s.persistFeedSync(ctx, orgID, lastSnap, lastRun, uri, "partial", result, false, nil)
			return result, errx.New(errx.ServiceUnavailable, "feed INITIAL backlog materialization failed; snapshot not committed")
		}
		result.Counts["imported"] = backlog.Imported
		result.Counts["supplier_confirmed"] = backlog.SupplierConfirmed
		result.Counts["candidate_attributed"] = backlog.CandidateAttributed
		result.Counts["initial_touchpoint_prepared"] = backlog.InitialPrepared
		result.Counts["delegated_eligible"] = backlog.DelegatedEligible
		result.Counts["held_exception"] = backlog.HeldException
		result.Counts["stale_initial_retired"] = backlog.StaleRetired
		if backlog.Imported != man.LeadCount || backlog.DelegatedEligible+backlog.HeldException != man.LeadCount {
			result.Status = "partial"
			result.Errors = append(result.Errors, "current INITIAL backlog closure count mismatch")
			s.persistFeedSync(ctx, orgID, lastSnap, lastRun, uri, "partial", result, false, nil)
			return result, errx.New(errx.ServiceUnavailable, "feed INITIAL backlog closure failed; snapshot not committed")
		}
		woken, wakeErr := s.wakeEligibleEnrichmentRecovery(ctx, orgID)
		if wakeErr != nil {
			result.Status = "partial"
			result.Errors = append(result.Errors, "enrichment recovery wakeup: "+wakeErr.Error())
			s.persistFeedSync(ctx, orgID, lastSnap, lastRun, uri, "partial", result, false, nil)
			return result, errx.New(errx.ServiceUnavailable, "feed enrichment recovery wakeup failed; snapshot not committed")
		}
		result.Counts["enrichment_recovery_woken"] = woken
		result.Status = "completed"
		s.persistFeedSync(ctx, orgID, man.Source.SnapshotHash, man.Source.RunID, uri, "completed", result, true, &generatedAt)
		return result, nil
	}

	// Partial: do NOT mark snapshot complete (Warmbly must not treat as success).
	result.Status = "partial"
	s.persistFeedSync(ctx, orgID, lastSnap, lastRun, uri, "partial", result, false, nil)
	return result, errx.New(errx.BadRequest, "feed sync partial: "+strings.Join(partialErrs, "; "))
}

func validateOutreachManifest(manifest *outreachManifest) error {
	if manifest == nil {
		return fmt.Errorf("manifest is required")
	}
	if manifest.SchemaVersion != "confenge.outreach.manifest.v1" {
		return fmt.Errorf("unsupported manifest schema_version")
	}
	if manifest.ChunkCount != len(manifest.Chunks) || manifest.ChunkCount < 1 {
		return fmt.Errorf("manifest chunk_count does not match chunks")
	}
	if manifest.LeadCount < 1 || manifest.LeadCount > maxOutreachManifestLeads {
		return fmt.Errorf("manifest lead_count is outside the supported limit")
	}
	if manifest.ChunkCount > maxOutreachManifestChunks {
		return fmt.Errorf("manifest chunk_count exceeds the supported limit")
	}
	seenFiles := make(map[string]struct{}, len(manifest.Chunks))
	totalLeads := 0
	for index, chunk := range manifest.Chunks {
		if chunk.ChunkIndex != index {
			return fmt.Errorf("manifest chunk indexes must be contiguous and ordered")
		}
		name := strings.TrimSpace(chunk.File)
		if name == "" || strings.TrimSpace(chunk.ContentHash) == "" {
			return fmt.Errorf("manifest chunks require file and content_hash")
		}
		if _, duplicate := seenFiles[name]; duplicate {
			return fmt.Errorf("manifest contains a duplicate chunk file")
		}
		seenFiles[name] = struct{}{}
		if chunk.HasMore != (index < len(manifest.Chunks)-1) {
			return fmt.Errorf("manifest chunk has_more sequence is invalid")
		}
		totalLeads += chunk.LeadCount
	}
	if totalLeads != manifest.LeadCount {
		return fmt.Errorf("manifest lead_count does not match chunks")
	}
	if manifest.DeactivationCnt != len(manifest.Deactivations) {
		return fmt.Errorf("manifest deactivation_count does not match deactivations")
	}
	for _, deactivation := range manifest.Deactivations {
		cnpj, _ := deactivation["cnpj14"].(string)
		if NormalizeCNPJ14(cnpj) == "" {
			return fmt.Errorf("manifest deactivation has invalid cnpj14")
		}
		toState, _ := deactivation["to_state"].(string)
		switch strings.ToUpper(strings.TrimSpace(toState)) {
		case ActivationWatch, ActivationResearchRequired, ActivationSuppressed:
		default:
			return fmt.Errorf("manifest deactivation has unsupported to_state")
		}
	}
	return nil
}

func restorePersistedFeedCounts(dst map[string]int, state *models.OutreachFeedSyncState) {
	if dst == nil || state == nil || len(state.CountsJSON) == 0 {
		return
	}
	var persisted map[string]json.RawMessage
	if json.Unmarshal(state.CountsJSON, &persisted) != nil {
		return
	}
	for key, raw := range persisted {
		var value int
		if json.Unmarshal(raw, &value) == nil {
			dst[key] = value
		}
	}
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

func feedSyncAdvisoryKey(orgID uuid.UUID) int64 {
	// Stable 63-bit key from org UUID (avoid sign bit).
	h := sha256.Sum256([]byte("confenge-feed-sync:" + orgID.String()))
	var n uint64
	for i := 0; i < 8; i++ {
		n = (n << 8) | uint64(h[i])
	}
	return int64(n & 0x7fffffffffffffff)
}

func (s *service) lastAppliedSnapshot(ctx context.Context, orgID uuid.UUID) (snap, run string) {
	if st, err := s.repo.GetFeedSyncState(ctx, orgID); err == nil && st != nil {
		// The state row always retains the last fully applied snapshot while a
		// newer attempt is running/partial. Never fall back to a completed chunk
		// import from an uncommitted manifest once this authoritative row exists.
		if st.LastSnapshotHash != "" && st.LastRunID != "" && st.SourceGeneratedAt != nil {
			return st.LastSnapshotHash, st.LastRunID
		}
		return "", ""
	}
	// Fallback: last completed import run snapshot
	runs, err := s.repo.ListImportRuns(ctx, orgID, 1)
	if err == nil && len(runs) > 0 && runs[0].Status == models.OutreachImportCompleted {
		return runs[0].SnapshotHash, runs[0].SourceRunID
	}
	return "", ""
}

func (s *service) persistFeedSync(ctx context.Context, orgID uuid.UUID, snap, run, uri, status string, res *FeedSyncResult, success bool, sourceGeneratedAt *time.Time) {
	now := time.Now().UTC()
	st := &models.OutreachFeedSyncState{
		OrganizationID:   orgID,
		LastSnapshotHash: snap,
		LastRunID:        run,
		LastManifestURI:  uri,
		LastAttemptAt:    &now,
		LastStatus:       status,
		LastError:        "",
	}
	if res != nil && len(res.Errors) > 0 {
		st.LastError = strings.Join(res.Errors, "; ")
	}
	if success {
		st.LastSuccessAt = &now
		st.SourceGeneratedAt = sourceGeneratedAt
	}
	if res != nil {
		counts := map[string]any{
			"chunks_total":    res.ChunksTotal,
			"chunks_imported": res.ChunksImported,
			"deactivations":   res.Deactivations,
			"skipped_same":    res.SkippedSame,
		}
		for key, value := range res.Counts {
			counts[key] = value
		}
		b, _ := json.Marshal(counts)
		st.CountsJSON = b
	}
	_ = s.repo.UpsertFeedSyncState(ctx, st)
}
