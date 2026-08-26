package confenge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
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
	authority      *feedAuthority
}

const (
	feedChunkStaleImportRecoveryAfter = 2 * time.Minute
	feedManifestMaxLeads              = 100_000
	feedManifestMaxChunks             = 1_000
	feedManifestMaxStagedBytes        = int64(1 << 30)
	feedManifestImportConcurrency     = 4
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
	LeadCount        int                            `json:"lead_count"`
	ChunkCount       int                            `json:"chunk_count"`
	Chunks           []manifestChunk                `json:"chunks"`
	Deactivations    []map[string]any               `json:"deactivations"`
	DeactivationCnt  int                            `json:"deactivation_count"`
	SourceFreshness  *FeedSourceFreshness           `json:"authoritative_source_freshness"`
	TargetMembership *authoritativeTargetMembership `json:"authoritative_target_membership"`
}

const (
	targetMembershipSchemaV1  = "confenge.target_membership.v1"
	targetMembershipIdentity  = "cnpj_root8"
	targetMembershipAlgorithm = "sha256(sorted_unique_cnpj_root8_newline_utf8)"
)

type authoritativeTargetMembership struct {
	SchemaVersion          string `json:"schema_version"`
	IdentityKey            string `json:"identity_key"`
	HashAlgorithm          string `json:"hash_algorithm"`
	PopulationCount        int    `json:"population_count"`
	MembershipHash         string `json:"membership_hash"`
	DuplicateMemberCount   int    `json:"duplicate_member_count"`
	TargetFitClass         string `json:"target_fit_class"`
	TargetConfirmedCount   int    `json:"target_confirmed_count"`
	SupplierConfirmedCount int    `json:"supplier_confirmed_count"`
	SourceMemberCount      int    `json:"source_member_count"`
	MembershipComplete     bool   `json:"membership_complete"`
}

type feedAuthority struct {
	SourceExpiresAt          time.Time
	SourceFreshnessHash      string
	TargetMembershipComplete bool
	TargetMembershipHash     string
	TargetMembershipCount    int
	SupplierConfirmedCount   int
}

type manifestChunk struct {
	File        string `json:"file"`
	ChunkIndex  int    `json:"chunk_index"`
	ContentHash string `json:"content_hash"`
	LeadCount   int    `json:"lead_count"`
	HasMore     bool   `json:"has_more"`
}

type validatedManifestChunk struct {
	manifest manifestChunk
	path     string
}

type manifestChunkImportResult struct {
	completed bool
	err       string
}

// org-level single-flight so two sync workers cannot double-import.
var feedSyncLocks sync.Map // orgID string → *sync.Mutex

func orgFeedSyncLock(orgID uuid.UUID) *sync.Mutex {
	v, _ := feedSyncLocks.LoadOrStore(orgID.String(), &sync.Mutex{})
	return v.(*sync.Mutex)
}

// SyncFeedManifest validates a manifest, imports bounded chunks, and applies deactivations.
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
	runConflict, conflictErr := s.repo.HasImportRunSnapshotConflict(ctx, orgID, man.Source.RunID, man.Source.SnapshotHash)
	if conflictErr != nil {
		s.persistFeedSync(ctx, orgID, lastSnap, lastRun, uri, "failed", result, false, nil)
		return result, errx.New(errx.ServiceUnavailable, "feed source run history unavailable")
	}
	if runConflict || (lastRun != "" && man.Source.RunID == lastRun && man.Source.SnapshotHash != lastSnap) {
		result.Errors = append(result.Errors, "source run ID reused with different snapshot")
		s.persistFeedSync(ctx, orgID, lastSnap, lastRun, uri, "failed", result, false, nil)
		return result, errx.New(errx.Conflict, "source run ID cannot identify more than one snapshot")
	}
	if lastGeneratedAt != nil && (generatedAt.Before(lastGeneratedAt.UTC()) ||
		(generatedAt.Equal(lastGeneratedAt.UTC()) && man.Source.SnapshotHash != lastSnap)) {
		result.Errors = append(result.Errors, "snapshot rollback rejected")
		s.persistFeedSync(ctx, orgID, lastSnap, lastRun, uri, "failed", result, false, nil)
		return result, errx.New(errx.Conflict, "manifest is older than the applied authoritative snapshot")
	}
	authority, authorityErr := validateManifestAuthority(&man, now, s.cfg.DelegatedFirstTouchEnabled)
	if authorityErr != nil {
		result.Errors = append(result.Errors, authorityErr.Error())
		s.persistFeedSync(ctx, orgID, lastSnap, lastRun, uri, "failed", result, false, nil)
		return result, errx.New(errx.BadRequest, authorityErr.Error())
	}
	result.authority = authority
	result.SnapshotHash = man.Source.SnapshotHash
	result.RunID = man.Source.RunID
	result.ChunksTotal = len(man.Chunks)

	// Idempotent: same snapshot already applied → noop
	if lastSnap == man.Source.SnapshotHash && lastSnap != "" {
		result.Status = "noop"
		result.SkippedSame = true
		result.RunID = lastRun
		if current != nil {
			result.Counts = decodeFeedSyncCounts(current.CountsJSON)
		}
		if persistErr := s.persistFeedSync(ctx, orgID, man.Source.SnapshotHash, lastRun, uri, "completed", result, true, &generatedAt); persistErr != nil {
			result.Status = "partial"
			result.Errors = append(result.Errors, "feed state publication failed")
			return result, errx.New(errx.ServiceUnavailable, "feed state publication failed")
		}
		return result, nil
	}

	baseURI := manifestBaseURI(uri)
	var partialErrs []string
	stageDir, err := os.MkdirTemp("", "warmbly-confenge-feed-")
	if err != nil {
		result.Errors = append(result.Errors, "chunk staging unavailable")
		s.persistFeedSync(ctx, orgID, lastSnap, lastRun, uri, "failed", result, false, nil)
		return result, errx.New(errx.ServiceUnavailable, "feed chunk staging unavailable")
	}
	defer func() { _ = os.RemoveAll(stageDir) }()
	validatedChunks := make([]validatedManifestChunk, 0, len(man.Chunks))
	seenCNPJ := make(map[string]string, man.LeadCount)
	seenLeadID := make(map[string]string, man.LeadCount)
	var stagedBytes int64
	for index, ch := range man.Chunks {
		if strings.TrimSpace(ch.File) == "" {
			partialErrs = append(partialErrs, "chunk missing file name")
			continue
		}
		chunkURI := joinURI(baseURI, ch.File)
		chunkRaw, err := fetcher.Fetch(ctx, chunkURI)
		if err != nil {
			partialErrs = append(partialErrs, fmt.Sprintf("%s fetch: %v", ch.File, err))
			break // A fetch failure cannot publish the snapshot.
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
		if validateErr := ValidateFeed(chunkFeed); validateErr != nil {
			partialErrs = append(partialErrs, fmt.Sprintf("%s invalid feed: %v", ch.File, validateErr))
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
		for leadIndex := range chunkFeed.Leads {
			lead := chunkFeed.Leads[leadIndex]
			cnpj := NormalizeCNPJ14(lead.Company.CNPJ14)
			if previous, duplicate := seenCNPJ[cnpj]; duplicate {
				partialErrs = append(partialErrs, fmt.Sprintf("%s duplicates cnpj14 from %s", ch.File, previous))
				break
			}
			seenCNPJ[cnpj] = ch.File
			leadID := strings.TrimSpace(lead.SourceLeadID)
			if previous, duplicate := seenLeadID[leadID]; duplicate {
				partialErrs = append(partialErrs, fmt.Sprintf("%s duplicates source_lead_id from %s", ch.File, previous))
				break
			}
			seenLeadID[leadID] = ch.File
		}
		if len(partialErrs) != 0 {
			break
		}
		stagedBytes += int64(len(chunkRaw))
		if stagedBytes > feedManifestMaxStagedBytes {
			partialErrs = append(partialErrs, "manifest staged payload exceeds safe disk ceiling")
			break
		}
		stagedPath := path.Join(stageDir, fmt.Sprintf("%06d.chunk", index))
		if writeErr := os.WriteFile(stagedPath, chunkRaw, 0o600); writeErr != nil {
			partialErrs = append(partialErrs, fmt.Sprintf("%s staging: %v", ch.File, writeErr))
			break
		}
		validatedChunks = append(validatedChunks, validatedManifestChunk{manifest: ch, path: stagedPath})
	}

	// Validate every remote object before the first account mutation.
	imported := 0
	if len(partialErrs) == 0 {
		var importErrs []string
		imported, importErrs = s.importValidatedManifestChunks(
			ctx, orgID, userID, baseURI, man.Source.SnapshotHash, validatedChunks,
		)
		partialErrs = append(partialErrs, importErrs...)
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
			result.Errors = append(result.Errors, "initial backlog materialization: "+backlogErr.Error())
			s.persistFeedSync(ctx, orgID, lastSnap, lastRun, uri, "partial", result, false, nil)
			return result, errx.New(errx.ServiceUnavailable, "feed initial backlog materialization failed; snapshot not committed")
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
			result.Errors = append(result.Errors, "initial backlog denominator mismatch")
			s.persistFeedSync(ctx, orgID, lastSnap, lastRun, uri, "partial", result, false, nil)
			return result, errx.New(errx.ServiceUnavailable, "feed counts did not close; snapshot not committed")
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
		if persistErr := s.persistFeedSync(ctx, orgID, man.Source.SnapshotHash, man.Source.RunID, uri, "completed", result, true, &generatedAt); persistErr != nil {
			result.Status = "partial"
			result.Errors = append(result.Errors, "feed state publication failed")
			return result, errx.New(errx.ServiceUnavailable, "feed state publication failed")
		}
		return result, nil
	}

	// Partial: do NOT mark snapshot complete (Warmbly must not treat as success).
	result.Status = "partial"
	s.persistFeedSync(ctx, orgID, lastSnap, lastRun, uri, "partial", result, false, nil)
	return result, errx.New(errx.BadRequest, "feed sync partial: "+strings.Join(partialErrs, "; "))
}

func validateManifestAuthority(manifest *outreachManifest, now time.Time, required bool) (*feedAuthority, error) {
	if manifest == nil {
		return nil, fmt.Errorf("authoritative feed manifest is required")
	}
	if !required && manifest.SourceFreshness == nil && manifest.TargetMembership == nil {
		return nil, nil
	}
	if err := ValidateAuthoritativeSourceFreshness(manifest.SourceFreshness, now); err != nil {
		return nil, err
	}
	expiresAt, err := parseFreshnessTime(manifest.SourceFreshness.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("authoritative PNCP freshness expires_at invalid")
	}
	membership := manifest.TargetMembership
	if membership == nil {
		return nil, fmt.Errorf("authoritative TARGET_CONFIRMED membership missing")
	}
	if membership.SchemaVersion != targetMembershipSchemaV1 || membership.IdentityKey != targetMembershipIdentity ||
		membership.HashAlgorithm != targetMembershipAlgorithm {
		return nil, fmt.Errorf("authoritative TARGET_CONFIRMED membership contract unsupported")
	}
	if !membership.MembershipComplete {
		return nil, fmt.Errorf("authoritative TARGET_CONFIRMED membership_complete=true is required")
	}
	if membership.TargetFitClass != TargetFitConfirmed || membership.PopulationCount < 1 ||
		membership.TargetConfirmedCount != membership.PopulationCount || membership.SourceMemberCount != membership.PopulationCount ||
		membership.DuplicateMemberCount != 0 || membership.SupplierConfirmedCount < 0 ||
		membership.SupplierConfirmedCount > membership.PopulationCount {
		return nil, fmt.Errorf("authoritative TARGET_CONFIRMED membership counts are invalid")
	}
	if !validSHA256(membership.MembershipHash) {
		return nil, fmt.Errorf("authoritative TARGET_CONFIRMED membership_hash is invalid")
	}
	freshnessHash := HashAuthoritativeSourceFreshness(manifest.SourceFreshness)
	if !validSHA256(freshnessHash) {
		return nil, fmt.Errorf("authoritative PNCP freshness hash is invalid")
	}
	return &feedAuthority{
		SourceExpiresAt:          expiresAt.UTC(),
		SourceFreshnessHash:      freshnessHash,
		TargetMembershipComplete: true,
		TargetMembershipHash:     strings.ToLower(membership.MembershipHash),
		TargetMembershipCount:    membership.PopulationCount,
		SupplierConfirmedCount:   membership.SupplierConfirmedCount,
	}, nil
}

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(strings.TrimSpace(value))
	return err == nil && len(decoded) == sha256.Size
}

func (s *service) importValidatedManifestChunks(
	ctx context.Context,
	orgID uuid.UUID,
	userID *uuid.UUID,
	baseURI, snapshotHash string,
	chunks []validatedManifestChunk,
) (int, []string) {
	results := make([]manifestChunkImportResult, len(chunks))
	jobs := make(chan int, len(chunks))
	workers := min(feedManifestImportConcurrency, len(chunks))
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				staged := chunks[index]
				ch := staged.manifest
				chunkRaw, readErr := os.ReadFile(staged.path)
				if readErr != nil {
					results[index].err = fmt.Sprintf("%s staged read: %v", ch.File, readErr)
					continue
				}
				idem := fmt.Sprintf("sync:%s:%s:%d", orgID, snapshotHash, ch.ChunkIndex)
				importRun, xerr := s.ImportFromBytes(ctx, orgID, userID, chunkRaw, ImportOptions{
					IdempotencyKey:          idem,
					SourceURI:               joinURI(baseURI, ch.File),
					resumeStaleRunningAfter: feedChunkStaleImportRecoveryAfter,
					skipCommercialPlanning:  true,
				})
				if xerr != nil {
					results[index].err = fmt.Sprintf("%s import: %s", ch.File, xerr.Message)
					continue
				}
				if importRun == nil || importRun.Status != models.OutreachImportCompleted {
					results[index].err = fmt.Sprintf("%s import did not complete", ch.File)
					continue
				}
				results[index].completed = true
			}
		}()
	}
	for index := range chunks {
		jobs <- index
	}
	close(jobs)
	wg.Wait()

	completed := 0
	errs := make([]string, 0)
	for index := range results {
		if results[index].completed {
			completed++
		}
		if results[index].err != "" {
			errs = append(errs, results[index].err)
		}
	}
	return completed, errs
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
	if manifest.ChunkCount > feedManifestMaxChunks {
		return fmt.Errorf("manifest exceeds safe chunk ceiling")
	}
	if manifest.LeadCount < 1 || manifest.LeadCount > feedManifestMaxLeads {
		return fmt.Errorf("manifest lead_count exceeds safe import ceiling")
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
		if strings.EqualFold(strings.TrimSpace(toState), ActivationActionableNow) {
			return fmt.Errorf("manifest deactivation cannot target ACTIONABLE_NOW")
		}
	}
	return nil
}

func decodeFeedSyncCounts(raw []byte) map[string]int {
	out := map[string]int{}
	var stored map[string]any
	if json.Unmarshal(raw, &stored) != nil {
		return out
	}
	for key, value := range stored {
		switch key {
		case "chunks_total", "chunks_imported", "deactivations", "skipped_same":
			continue
		}
		switch n := value.(type) {
		case float64:
			out[key] = int(n)
		case int:
			out[key] = n
		}
	}
	return out
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

func (s *service) persistFeedSync(ctx context.Context, orgID uuid.UUID, snap, run, uri, status string, res *FeedSyncResult, success bool, sourceGeneratedAt *time.Time) error {
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
		if res != nil && res.authority != nil {
			st.SourceExpiresAt = &res.authority.SourceExpiresAt
			st.SourceFreshnessHash = res.authority.SourceFreshnessHash
			st.TargetMembershipComplete = res.authority.TargetMembershipComplete
			st.TargetMembershipHash = res.authority.TargetMembershipHash
			st.TargetMembershipCount = res.authority.TargetMembershipCount
			st.SupplierConfirmedCount = res.authority.SupplierConfirmedCount
		}
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
	return s.repo.UpsertFeedSyncState(ctx, st)
}
