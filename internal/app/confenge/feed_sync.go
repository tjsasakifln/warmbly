package confenge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"sort"
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
	// ModuleVersion is an input to the producer's source.run_id derivation and
	// is therefore required to verify the freshness binding (see
	// deriveFeedBuildRunID). Fail-closed: never inferred, never defaulted.
	ModuleVersion string `json:"module_version"`
	GeneratedAt   string `json:"generated_at"`
	Source        struct {
		System       string `json:"system"`
		RunID        string `json:"run_id"`
		SnapshotHash string `json:"snapshot_hash"`
		RepoSHA      string `json:"repo_sha"`
		ProfileID    string `json:"profile_id"`
		ProfileVer   string `json:"profile_version"`
		// Freshness is the SAME attestation object the producer also publishes
		// at manifest.authoritative_source_freshness; FreshnessHash is the
		// producer's content hash of it and is committed to by source.run_id.
		Freshness           *FeedSourceFreshness     `json:"authoritative_freshness"`
		FreshnessHash       string                   `json:"authoritative_freshness_hash"`
		CommercialAuthority *FeedCommercialAuthority `json:"commercial_authority"`
	} `json:"source"`
	CommercialAuthority *FeedCommercialAuthority       `json:"commercial_authority"`
	LeadCount           int                            `json:"lead_count"`
	ChunkCount          int                            `json:"chunk_count"`
	Chunks              []manifestChunk                `json:"chunks"`
	Deactivations       []map[string]any               `json:"deactivations"`
	DeactivationCnt     int                            `json:"deactivation_count"`
	SourceFreshness     *FeedSourceFreshness           `json:"authoritative_source_freshness"`
	TargetMembership    *authoritativeTargetMembership `json:"authoritative_target_membership"`
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
	Commercial               *FeedCommercialAuthority
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
		if s.cfg.DelegatedFirstTouchEnabled && (lastRun != man.Source.RunID ||
			!sameFeedAuthority(current, authority)) {
			result.Status = "failed"
			result.Errors = append(result.Errors, "same snapshot reused with different run or authority")
			s.persistFeedSync(ctx, orgID, lastSnap, lastRun, uri, "failed", result, false, nil)
			return result, errx.New(errx.Conflict, "same snapshot cannot change authoritative binding")
		}
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
	if len(partialErrs) == 0 && authority != nil {
		membershipHash, membershipCount, membershipErr := hashStagedTargetMembership(seenCNPJ)
		if membershipErr != nil {
			partialErrs = append(partialErrs, membershipErr.Error())
		} else if membershipCount != authority.TargetMembershipCount || membershipCount != man.LeadCount ||
			membershipHash != authority.TargetMembershipHash {
			partialErrs = append(partialErrs, "authoritative TARGET_CONFIRMED membership does not match staged chunks")
		}
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
		if authority != nil && backlog.SupplierConfirmed != authority.SupplierConfirmedCount {
			result.Status = "partial"
			result.Errors = append(result.Errors, "supplier-confirmed denominator mismatch")
			s.persistFeedSync(ctx, orgID, lastSnap, lastRun, uri, "partial", result, false, nil)
			return result, errx.New(errx.ServiceUnavailable, "feed supplier count did not close; snapshot not committed")
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

func manifestCommercialAuthority(manifest *outreachManifest) *FeedCommercialAuthority {
	if manifest == nil {
		return nil
	}
	if authorityPresent(manifest.CommercialAuthority) {
		return manifest.CommercialAuthority
	}
	if authorityPresent(manifest.Source.CommercialAuthority) {
		return manifest.Source.CommercialAuthority
	}
	return nil
}

func validateManifestAuthority(manifest *outreachManifest, now time.Time, required bool) (*feedAuthority, error) {
	if manifest == nil {
		return nil, fmt.Errorf("authoritative feed manifest is required")
	}
	if !required && manifest.SourceFreshness == nil && manifest.TargetMembership == nil {
		return nil, nil
	}
	commercialPayload := manifestCommercialAuthority(manifest)
	if !authorityPresent(commercialPayload) {
		if err := ValidateAuthoritativeSourceFreshness(manifest.SourceFreshness, now); err != nil {
			return nil, err
		}
	} else if ClassifySourceHealth(manifest.SourceFreshness, now, 24*time.Hour).State == SourceHealthMissing {
		return nil, fmt.Errorf("authoritative PNCP freshness missing")
	}
	if err := bindManifestFreshnessToBuild(manifest); err != nil {
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
	if authorityPresent(commercialPayload) {
		decision := EvaluateCommercialAuthority(commercialPayload, CommercialAuthorityBinding{
			SourceRunID:    strings.TrimSpace(manifest.Source.RunID),
			SnapshotHash:   strings.TrimSpace(manifest.Source.SnapshotHash),
			MembershipHash: strings.ToLower(membership.MembershipHash),
		}, now)
		if decision.State == CommercialAuthorityUnknown {
			return nil, fmt.Errorf("commercial authority binding invalid")
		}
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
		Commercial:               commercialPayload,
	}, nil
}

// feedBuildRunIDPrefix is the literal prefix the producer prepends to the
// truncated digest when minting a feed BUILD id.
const feedBuildRunIDPrefix = "run-"

// deriveFeedBuildRunID recomputes manifest.source.run_id exactly as extra-cli
// mints it in scripts/warmbly_bridge/export.py::_run_id:
//
//	"run-" + sha256(snapshot_hash|profile_id|profile_version|module_version|authoritative_freshness_hash)[:16]
//
// WHY THIS REPLACED A STRING COMPARISON.
//
// The previous gate required
//
//	manifest.authoritative_source_freshness.run_id == manifest.source.run_id
//
// which is unsatisfiable by construction against the real producer, because the
// two identifiers are minted by two different generators in two disjoint
// namespaces:
//
//   - manifest.source.run_id is a content-derived feed BUILD id, minted by
//     extra-cli scripts/warmbly_bridge/export.py::_run_id (the derivation above).
//     Real production value: "run-25d918a5801fa976".
//
//   - manifest.authoritative_source_freshness.run_id is the PNCP INGESTION
//     attempt id, minted by extra-cli scripts/crawl/run_evidence.py::new_run_id
//     as f"{prefix}-{utcstamp}-{uuid4().hex[:10]}" with prefix "contracts-90d".
//     Real production value: "contracts-90d-20260826T230341Z-5ca7f36505".
//
// A build id can never equal an ingestion attempt id, so the gate refused every
// genuine manifest and left the authority projection empty.
//
// The INTENT of that gate was correct and is preserved here, and strengthened
// from a lexical check into a cryptographic one: source.run_id is itself a
// commitment over authoritative_freshness_hash, so recomputing it proves the
// freshness attestation attached to this manifest is the one this feed build
// was actually produced against, and was not substituted from another run.
func deriveFeedBuildRunID(snapshotHash, profileID, profileVersion, moduleVersion, freshnessHash string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		snapshotHash, profileID, profileVersion, moduleVersion, freshnessHash,
	}, "|")))
	return feedBuildRunIDPrefix + hex.EncodeToString(sum[:])[:16]
}

// bindManifestFreshnessToBuild proves the authoritative freshness attestation
// carried by this manifest genuinely belongs to this feed build. Fail-closed:
// every input to the derivation must be present and well-formed, and no branch
// may skip the recomputation.
func bindManifestFreshnessToBuild(manifest *outreachManifest) error {
	if manifest == nil || manifest.SourceFreshness == nil {
		return fmt.Errorf("authoritative PNCP freshness missing")
	}
	moduleVersion := strings.TrimSpace(manifest.ModuleVersion)
	if moduleVersion == "" {
		return fmt.Errorf("manifest module_version is required to bind the authoritative PNCP freshness attestation to this feed build")
	}
	freshnessHash := strings.TrimSpace(manifest.Source.FreshnessHash)
	if !validSHA256(freshnessHash) {
		return fmt.Errorf("manifest source.authoritative_freshness_hash is not a valid sha256")
	}
	if freshnessHash != strings.ToLower(freshnessHash) {
		return fmt.Errorf("manifest source.authoritative_freshness_hash must be lowercase hex")
	}
	nested := manifest.Source.Freshness
	if nested == nil {
		return fmt.Errorf("manifest source.authoritative_freshness block is missing")
	}
	// The producer writes the same freshness object twice (top level and inside
	// source). Disagreement means one of the two copies was substituted.
	nestedRunID := strings.TrimSpace(nested.RunID)
	topRunID := strings.TrimSpace(manifest.SourceFreshness.RunID)
	if nestedRunID == "" || topRunID == "" {
		return fmt.Errorf("authoritative PNCP freshness run_id is empty")
	}
	if nestedRunID != topRunID {
		return fmt.Errorf(
			"authoritative PNCP freshness block was substituted: source.authoritative_freshness.run_id %q does not match authoritative_source_freshness.run_id %q",
			nestedRunID, topRunID)
	}
	declaredRunID := strings.TrimSpace(manifest.Source.RunID)
	derivedRunID := deriveFeedBuildRunID(
		strings.TrimSpace(manifest.Source.SnapshotHash),
		strings.TrimSpace(manifest.Source.ProfileID),
		strings.TrimSpace(manifest.Source.ProfileVer),
		moduleVersion,
		freshnessHash,
	)
	if declaredRunID != derivedRunID {
		return fmt.Errorf(
			"manifest source.run_id %q does not recompute from its published build inputs (derived %q): the authoritative PNCP freshness attestation is not bound to this feed build",
			declaredRunID, derivedRunID)
	}
	return nil
}

func sameFeedAuthority(current *models.OutreachFeedSyncState, authority *feedAuthority) bool {
	if current == nil || authority == nil || current.SourceExpiresAt == nil {
		return false
	}
	return current.SourceExpiresAt.Equal(authority.SourceExpiresAt) &&
		current.SourceFreshnessHash == authority.SourceFreshnessHash &&
		current.TargetMembershipComplete == authority.TargetMembershipComplete &&
		current.TargetMembershipHash == authority.TargetMembershipHash &&
		current.TargetMembershipCount == authority.TargetMembershipCount &&
		current.SupplierConfirmedCount == authority.SupplierConfirmedCount &&
		bytes.Equal(marshalCommercialAuthority(storedCommercialAuthority(current)), marshalCommercialAuthority(authority.Commercial))
}

func hashStagedTargetMembership(cnpjs map[string]string) (string, int, error) {
	roots := make(map[string]struct{}, len(cnpjs))
	for cnpj := range cnpjs {
		if len(cnpj) != 14 {
			return "", 0, fmt.Errorf("staged TARGET_CONFIRMED CNPJ is invalid")
		}
		root := cnpj[:8]
		if _, duplicate := roots[root]; duplicate {
			return "", 0, fmt.Errorf("staged TARGET_CONFIRMED membership contains duplicate CNPJ roots")
		}
		roots[root] = struct{}{}
	}
	ordered := make([]string, 0, len(roots))
	for root := range roots {
		ordered = append(ordered, root)
	}
	sort.Strings(ordered)
	var canonical strings.Builder
	for _, root := range ordered {
		canonical.WriteString(root)
		canonical.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(canonical.String()))
	return hex.EncodeToString(sum[:]), len(ordered), nil
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
		switch strings.ToUpper(strings.TrimSpace(toState)) {
		case ActivationWatch, ActivationResearchRequired, ActivationSuppressed:
		case ActivationActionableNow:
			return fmt.Errorf("manifest deactivation cannot target ACTIONABLE_NOW")
		default:
			return fmt.Errorf("manifest deactivation has unsupported to_state")
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

func marshalCommercialAuthority(p *FeedCommercialAuthority) []byte {
	if !authorityPresent(p) {
		return nil
	}
	b, err := json.Marshal(p)
	if err != nil {
		return nil
	}
	return b
}

func storedCommercialAuthority(st *models.OutreachFeedSyncState) *FeedCommercialAuthority {
	if st == nil || len(bytes.TrimSpace(st.CommercialAuthorityJSON)) == 0 || bytes.Equal(bytes.TrimSpace(st.CommercialAuthorityJSON), []byte("null")) {
		return nil
	}
	var p FeedCommercialAuthority
	if err := json.Unmarshal(st.CommercialAuthorityJSON, &p); err != nil {
		return nil
	}
	if !authorityPresent(&p) {
		return nil
	}
	return &p
}

func (s *service) persistFeedSync(ctx context.Context, orgID uuid.UUID, snap, run, uri, status string, res *FeedSyncResult, success bool, sourceGeneratedAt *time.Time) error {
	now := s.now()
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
			st.CommercialAuthorityJSON = marshalCommercialAuthority(res.authority.Commercial)
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
