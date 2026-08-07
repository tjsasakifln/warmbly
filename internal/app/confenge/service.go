package confenge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/pkg/generation"
	"github.com/warmbly/warmbly/internal/repository"
)

// AuditLogger writes org-scoped audit entries (realtime spine).
type AuditLogger interface {
	LogAction(ctx context.Context, orgID, actorID uuid.UUID, action models.AuditAction, entityType models.AuditEntityType, entityID *uuid.UUID, ip, userAgent string, changes, metadata map[string]string)
}

// Service is the control-plane surface for CONFENGE outreach staging.
type Service interface {
	Enabled() bool
	Config() Config

	// ImportFromBytes validates and optionally applies a feed payload.
	ImportFromBytes(ctx context.Context, orgID uuid.UUID, userID *uuid.UUID, raw []byte, opts ImportOptions) (*models.OutreachImportRun, *errx.Error)
	// ImportFromURI fetches a feed (file/https) then imports.
	ImportFromURI(ctx context.Context, orgID uuid.UUID, userID *uuid.UUID, uri string, opts ImportOptions) (*models.OutreachImportRun, *errx.Error)

	GetImportRun(ctx context.Context, orgID, id uuid.UUID) (*models.OutreachImportRun, *errx.Error)
	ListImportRuns(ctx context.Context, orgID uuid.UUID, limit int) ([]models.OutreachImportRun, *errx.Error)

	Summary(ctx context.Context, orgID uuid.UUID) (*models.OutreachQueueSummary, *errx.Error)
	ListAccounts(ctx context.Context, orgID uuid.UUID, filter repository.OutreachAccountFilter) ([]models.OutreachAccount, *errx.Error)
	GetAccount(ctx context.Context, orgID, id uuid.UUID) (*models.OutreachAccount, *errx.Error)
	BlockAccount(ctx context.Context, orgID, userID, id uuid.UUID, reason string, dnc bool) (*models.OutreachAccount, *errx.Error)

	// Drafts / review (PR2)
	GenerateDraft(ctx context.Context, orgID, userID, accountID uuid.UUID, contactID *uuid.UUID) (*models.OutreachDraft, *errx.Error)
	GetDraft(ctx context.Context, orgID, id uuid.UUID) (*models.OutreachDraft, *errx.Error)
	ListDrafts(ctx context.Context, orgID uuid.UUID, status string, limit, offset int) ([]models.OutreachDraft, *errx.Error)
	ReviewDraft(ctx context.Context, orgID, userID, draftID uuid.UUID, action string, edit *DraftEdit) (*models.OutreachDraft, *errx.Error)
	SetAI(p generation.Provider)
}

// ImportOptions controls dry-run, idempotency, and source tracking.
type ImportOptions struct {
	DryRun         bool
	IdempotencyKey string
	SourceURI      string
}

type service struct {
	cfg   Config
	repo  repository.OutreachRepository
	audit AuditLogger
	fetch *FeedFetcher
	ai    generation.Provider
}

// NewService wires confenge outreach. When cfg.Enabled is false, mutators return 404-style disabled errors.
func NewService(cfg Config, repo repository.OutreachRepository, audit AuditLogger) Service {
	return &service{
		cfg:   cfg,
		repo:  repo,
		audit: audit,
		fetch: &FeedFetcher{
			AllowedHosts: cfg.AllowedHosts,
			Token:        cfg.FeedToken,
			MaxBytes:     cfg.MaxFeedPayloadBytes,
		},
	}
}

// NewServiceWithAI wires confenge with an optional LLM provider for drafts.
func NewServiceWithAI(cfg Config, repo repository.OutreachRepository, audit AuditLogger, ai generation.Provider) Service {
	svc := &service{
		cfg:   cfg,
		repo:  repo,
		audit: audit,
		fetch: &FeedFetcher{
			AllowedHosts: cfg.AllowedHosts,
			Token:        cfg.FeedToken,
			MaxBytes:     cfg.MaxFeedPayloadBytes,
		},
		ai: ai,
	}
	return svc
}

func (s *service) Enabled() bool  { return s.cfg.Enabled }
func (s *service) Config() Config { return s.cfg }

func (s *service) requireEnabled() *errx.Error {
	if !s.cfg.Enabled {
		return errx.New(errx.NotFound, "CONFENGE outreach is not enabled on this server")
	}
	return nil
}

func (s *service) ImportFromURI(ctx context.Context, orgID uuid.UUID, userID *uuid.UUID, uri string, opts ImportOptions) (*models.OutreachImportRun, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	// Prefer configured feed URL when client sends empty.
	if strings.TrimSpace(uri) == "" {
		uri = s.cfg.FeedURL
	}
	if strings.TrimSpace(uri) == "" {
		return nil, errx.New(errx.BadRequest, "feed URI is required (or set CONFENGE_EXTRA_CLI_FEED_URL)")
	}
	opts.SourceURI = uri
	raw, err := s.fetch.Fetch(ctx, uri)
	if err != nil {
		return nil, errx.New(errx.BadRequest, "failed to fetch feed: "+err.Error())
	}
	return s.ImportFromBytes(ctx, orgID, userID, raw, opts)
}

func (s *service) ImportFromBytes(ctx context.Context, orgID uuid.UUID, userID *uuid.UUID, raw []byte, opts ImportOptions) (*models.OutreachImportRun, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	if len(raw) == 0 {
		return nil, errx.New(errx.BadRequest, "empty feed payload")
	}
	if s.cfg.MaxFeedPayloadBytes > 0 && int64(len(raw)) > s.cfg.MaxFeedPayloadBytes {
		return nil, errx.New(errx.BadRequest, "feed payload too large")
	}

	payloadHash := CanonicalPayloadHash(raw)
	if opts.IdempotencyKey != "" {
		existing, err := s.repo.GetImportRunByIdempotency(ctx, orgID, opts.IdempotencyKey)
		if err != nil {
			return nil, errx.New(errx.Internal, "failed to check idempotency key")
		}
		if existing != nil {
			// Same key + same hash: return prior result (idempotent).
			if existing.PayloadHash == payloadHash {
				return existing, nil
			}
			// Same key, different body: conflict.
			return nil, errx.New(errx.Conflict, "idempotency key already used with a different payload")
		}
	}

	feed, err := DetectAndNormalize(raw)
	if err != nil {
		return nil, errx.New(errx.BadRequest, "invalid feed: "+err.Error())
	}
	if err := ValidateFeed(feed); err != nil {
		return nil, errx.New(errx.BadRequest, err.Error())
	}

	run := &models.OutreachImportRun{
		OrganizationID:  orgID,
		SourceSystem:    feed.Source.System,
		SourceRunID:     feed.Source.RunID,
		SchemaVersion:   feed.SchemaVersion,
		SnapshotHash:    feed.Source.SnapshotHash,
		RepoSHA:         feed.Source.RepoSHA,
		PayloadHash:     payloadHash,
		ProfileID:       feed.Source.ProfileID,
		ProfileVersion:  feed.Source.ProfileVersion,
		Status:          models.OutreachImportRunning,
		DryRun:          opts.DryRun,
		CreatedByUserID: userID,
		IdempotencyKey:  opts.IdempotencyKey,
		SourceURI:       opts.SourceURI,
		Warnings:        nil,
		Errors:          nil,
	}
	if feed.Pagination.Cursor != nil {
		run.CursorIn = *feed.Pagination.Cursor
	}
	if feed.Pagination.NextCursor != nil {
		run.CursorOut = *feed.Pagination.NextCursor
	}

	if err := s.repo.CreateImportRun(ctx, run); err != nil {
		return nil, errx.New(errx.Internal, "failed to create import run: "+err.Error())
	}

	counts, leadErrs, warns := s.applyFeed(ctx, orgID, run, feed, opts.DryRun)
	run.Counts = counts
	run.Errors = leadErrs
	run.Warnings = warns
	now := time.Now().UTC()
	run.FinishedAt = &now
	if len(leadErrs) > 0 && counts.Creates+counts.Updates > 0 {
		run.Status = models.OutreachImportPartial
	} else if len(leadErrs) > 0 && counts.Creates+counts.Updates == 0 && counts.LeadsProcessed == 0 {
		run.Status = models.OutreachImportFailed
	} else {
		run.Status = models.OutreachImportCompleted
	}
	if err := s.repo.UpdateImportRun(ctx, run); err != nil {
		return nil, errx.New(errx.Internal, "failed to finalize import run")
	}

	if s.audit != nil && userID != nil && !opts.DryRun {
		s.audit.LogAction(ctx, orgID, *userID, models.AuditActionCreate, models.AuditEntityOutreachImportRun, &run.ID, "", "",
			map[string]string{
				"payload_hash": payloadHash,
				"creates":      fmt.Sprintf("%d", counts.Creates),
				"updates":      fmt.Sprintf("%d", counts.Updates),
			},
			map[string]string{"source_system": feed.Source.System, "source_run_id": feed.Source.RunID},
		)
	}
	return run, nil
}

func (s *service) applyFeed(ctx context.Context, orgID uuid.UUID, run *models.OutreachImportRun, feed *Feed, dryRun bool) (models.OutreachImportCounts, []models.OutreachImportError, []string) {
	var counts models.OutreachImportCounts
	var leadErrs []models.OutreachImportError
	var warns []string

	for i, lead := range feed.Leads {
		if lv := ValidateLead(i, lead); lv != nil {
			counts.Invalid++
			counts.LeadsSkippedError++
			leadErrs = append(leadErrs, models.OutreachImportError{
				SourceLeadID: lv.SourceLeadID,
				CNPJ14:       lv.CNPJ14,
				Message:      lv.Message,
			})
			continue
		}
		cnpj := NormalizeCNPJ14(lead.Company.CNPJ14)
		existing, err := s.repo.GetAccountByCNPJ(ctx, orgID, cnpj)
		if err != nil {
			counts.LeadsSkippedError++
			leadErrs = append(leadErrs, models.OutreachImportError{
				SourceLeadID: lead.SourceLeadID, CNPJ14: cnpj, Message: "db error loading account",
			})
			continue
		}

		// Preserve DNC: never re-open.
		if existing != nil && existing.DoNotContact {
			counts.Blocked++
			counts.Unchanged++
			counts.LeadsProcessed++
			continue
		}

		contentHash := LeadContentHash(lead)
		queueState := DefaultQueueState(lead, existing)
		if !hasEnrollableContact(lead) {
			counts.MissingContact++
		}

		if dryRun {
			if existing == nil {
				counts.Creates++
			} else if existing.LastPayloadHash == contentHash {
				counts.Unchanged++
			} else {
				counts.Updates++
			}
			// Estimate evidence adds
			if existing != nil {
				existingEv, _ := s.repo.ListEvidence(ctx, orgID, existing.ID)
				known := map[string]bool{}
				for _, e := range existingEv {
					known[e.SourceEvidenceID] = true
				}
				for _, e := range lead.Evidence {
					if e.ID != "" && !known[e.ID] {
						counts.EvidenceAdded++
					}
				}
			} else {
				counts.EvidenceAdded += len(lead.Evidence)
			}
			counts.LeadsProcessed++
			continue
		}

		// APPLY
		acc := leadToAccount(orgID, lead, feed, run.ID, contentHash, queueState, existing)
		created, err := s.repo.UpsertAccount(ctx, acc)
		if err != nil {
			counts.LeadsSkippedError++
			leadErrs = append(leadErrs, models.OutreachImportError{
				SourceLeadID: lead.SourceLeadID, CNPJ14: cnpj, Message: "upsert account: " + err.Error(),
			})
			continue
		}
		if created {
			counts.Creates++
		} else if existing != nil && existing.LastPayloadHash == contentHash {
			counts.Unchanged++
		} else {
			counts.Updates++
		}

		for _, fc := range lead.Contacts {
			cand := leadToCandidate(orgID, acc.ID, run.ID, fc)
			if _, err := s.repo.UpsertCandidate(ctx, cand); err != nil {
				warns = append(warns, fmt.Sprintf("%s contact %s: %v", cnpj, fc.Email, err))
				counts.Warnings++
			}
		}
		for _, fe := range lead.Evidence {
			ev := leadToEvidence(orgID, acc.ID, run.ID, fe)
			createdEv, err := s.repo.UpsertEvidence(ctx, ev)
			if err != nil {
				warns = append(warns, fmt.Sprintf("%s evidence %s: %v", cnpj, fe.ID, err))
				counts.Warnings++
				continue
			}
			if createdEv {
				counts.EvidenceAdded++
			}
		}
		counts.LeadsProcessed++
	}
	return counts, leadErrs, warns
}

func leadToAccount(orgID uuid.UUID, lead FeedLead, feed *Feed, runID uuid.UUID, contentHash, queueState string, existing *models.OutreachAccount) *models.OutreachAccount {
	cnpj := NormalizeCNPJ14(lead.Company.CNPJ14)
	root := digitsOnly(lead.Company.CNPJRoot)
	if root == "" && len(cnpj) == 14 {
		root = cnpj[:8]
	}
	contracts, _ := json.Marshal(lead.Contracts)
	if contracts == nil {
		contracts = []byte("[]")
	}
	acc := &models.OutreachAccount{
		OrganizationID:     orgID,
		SourceLeadID:       SanitizeText(lead.SourceLeadID, 200),
		CNPJ14:             cnpj,
		CNPJRoot:           root,
		RazaoSocial:        SanitizeText(lead.Company.RazaoSocial, 500),
		NomeFantasia:       SanitizeText(lead.Company.NomeFantasia, 500),
		Municipio:          SanitizeText(lead.Company.Municipio, 200),
		UF:                 strings.ToUpper(SanitizeText(lead.Company.UF, 2)),
		Website:            SanitizeText(lead.Company.Website, 500),
		PriorityRank:       lead.Priority.Rank,
		PriorityScore:      lead.Priority.Score,
		PriorityTier:       SanitizeText(lead.Priority.Tier, 100),
		PriorityConfidence: SanitizeText(lead.Priority.Confidence, 50),
		MomentCode:         SanitizeText(lead.Moment.Code, 100),
		MomentSummary:      SanitizeText(lead.Moment.Summary, 2000),
		MomentObservedAt:   ParseDate(lead.Moment.ObservedAt),
		MomentConfidence:   SanitizeText(lead.Moment.Confidence, 50),
		MomentEvidenceIDs:  lead.Moment.EvidenceIDs,
		ServiceCode:        SanitizeText(lead.Offer.ServiceCode, 100),
		ServiceName:        SanitizeText(lead.Offer.ServiceName, 200),
		EntryOffer:         SanitizeText(lead.Offer.EntryOffer, 1000),
		OfferRationale:     SanitizeText(lead.Offer.Rationale, 2000),
		FactToMention:      SanitizeText(lead.MessagingContext.FactToMention, 2000),
		QuestionToAsk:      SanitizeText(lead.MessagingContext.QuestionToAsk, 1000),
		CTA:                SanitizeText(lead.MessagingContext.CTA, 500),
		ClaimsToAvoid:      lead.MessagingContext.ClaimsToAvoid,
		CommercialState:    firstNonEmpty(SanitizeText(lead.CommercialState, 50), "NEW"),
		QueueState:         queueState,
		SourceSystem:       feed.Source.System,
		SourceRunID:        feed.Source.RunID,
		LastImportRunID:    &runID,
		LastPayloadHash:    contentHash,
		ContractsJSON:      contracts,
	}
	if existing != nil {
		acc.ID = existing.ID
		acc.HumanOverride = existing.HumanOverride
		acc.Blocked = existing.Blocked
		acc.BlockReason = existing.BlockReason
		acc.DoNotContact = existing.DoNotContact
		acc.CreatedAt = existing.CreatedAt
	}
	return acc
}

func leadToCandidate(orgID, accountID, runID uuid.UUID, fc FeedContact) *models.OutreachContactCandidate {
	email := strings.TrimSpace(strings.ToLower(fc.Email))
	vs := NormalizeVerification(fc.VerificationStatus, email)
	dnc := vs == models.OutreachVerifyDoNotContact
	bounced := vs == models.OutreachVerifyBounced
	return &models.OutreachContactCandidate{
		OrganizationID:     orgID,
		AccountID:          accountID,
		SourceContactID:    SanitizeText(fc.SourceContactID, 200),
		Name:               SanitizeText(fc.Name, 300),
		Role:               SanitizeText(fc.Role, 200),
		Email:              email,
		Phone:              SanitizeText(fc.Phone, 50),
		LinkedInURL:        SanitizeText(fc.LinkedInURL, 500),
		SourceURL:          SanitizeText(fc.SourceURL, 1000),
		SourceDocument:     SanitizeText(fc.SourceDocument, 500),
		SourceDate:         ParseDate(fc.SourceDate),
		VerificationStatus: vs,
		Confidence:         SanitizeText(fc.Confidence, 50),
		Recommended:        fc.Recommended,
		DoNotContact:       dnc,
		Bounced:            bounced,
		LastImportRunID:    &runID,
	}
}

func leadToEvidence(orgID, accountID, runID uuid.UUID, fe FeedEvidence) *models.OutreachEvidence {
	return &models.OutreachEvidence{
		OrganizationID:   orgID,
		AccountID:        accountID,
		SourceEvidenceID: SanitizeText(fe.ID, 200),
		EvidenceType:     SanitizeText(fe.Type, 100),
		Title:            SanitizeText(fe.Title, 500),
		URL:              SanitizeText(fe.URL, 1000),
		Document:         SanitizeText(fe.Document, 500),
		EvidenceDate:     ParseDate(fe.Date),
		Location:         SanitizeText(fe.Location, 200),
		Excerpt:          SanitizeText(fe.Excerpt, 4000),
		Synthesis:        SanitizeText(fe.Synthesis, 2000),
		EpistemicClass:   NormalizeEpistemic(fe.EpistemicClass),
		Reliability:      SanitizeText(fe.Reliability, 50),
		ConsultedAt:      ParseDate(fe.ConsultedAt),
		LastImportRunID:  &runID,
	}
}

func (s *service) GetImportRun(ctx context.Context, orgID, id uuid.UUID) (*models.OutreachImportRun, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	run, err := s.repo.GetImportRun(ctx, orgID, id)
	if err != nil {
		return nil, errx.New(errx.Internal, "failed to load import run")
	}
	if run == nil {
		return nil, errx.New(errx.NotFound, "import run not found")
	}
	return run, nil
}

func (s *service) ListImportRuns(ctx context.Context, orgID uuid.UUID, limit int) ([]models.OutreachImportRun, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	list, err := s.repo.ListImportRuns(ctx, orgID, limit)
	if err != nil {
		return nil, errx.New(errx.Internal, "failed to list import runs")
	}
	if list == nil {
		list = []models.OutreachImportRun{}
	}
	return list, nil
}

func (s *service) Summary(ctx context.Context, orgID uuid.UUID) (*models.OutreachQueueSummary, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	sum, err := s.repo.CountByQueueState(ctx, orgID)
	if err != nil {
		return nil, errx.New(errx.Internal, "failed to load summary")
	}
	return sum, nil
}

func (s *service) ListAccounts(ctx context.Context, orgID uuid.UUID, filter repository.OutreachAccountFilter) ([]models.OutreachAccount, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	list, err := s.repo.ListAccounts(ctx, orgID, filter)
	if err != nil {
		return nil, errx.New(errx.Internal, "failed to list accounts")
	}
	if list == nil {
		list = []models.OutreachAccount{}
	}
	return list, nil
}

func (s *service) GetAccount(ctx context.Context, orgID, id uuid.UUID) (*models.OutreachAccount, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	acc, err := s.repo.GetAccount(ctx, orgID, id)
	if err != nil {
		return nil, errx.New(errx.Internal, "failed to load account")
	}
	if acc == nil {
		return nil, errx.New(errx.NotFound, "account not found")
	}
	contacts, err := s.repo.ListCandidates(ctx, orgID, id)
	if err != nil {
		return nil, errx.New(errx.Internal, "failed to load contacts")
	}
	evidence, err := s.repo.ListEvidence(ctx, orgID, id)
	if err != nil {
		return nil, errx.New(errx.Internal, "failed to load evidence")
	}
	acc.Contacts = contacts
	acc.Evidence = evidence
	acc.ContactN = len(contacts)
	acc.EvidenceN = len(evidence)
	return acc, nil
}

func (s *service) BlockAccount(ctx context.Context, orgID, userID, id uuid.UUID, reason string, dnc bool) (*models.OutreachAccount, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	queue := models.OutreachQueueBlocked
	if dnc {
		queue = models.OutreachQueueDoNotContact
	}
	if err := s.repo.SetAccountHumanFlags(ctx, orgID, id, true, dnc, SanitizeText(reason, 500), queue); err != nil {
		return nil, errx.New(errx.NotFound, "account not found")
	}
	if s.audit != nil {
		s.audit.LogAction(ctx, orgID, userID, models.AuditActionUpdate, models.AuditEntityOutreachAccount, &id, "", "",
			map[string]string{"blocked": "true", "do_not_contact": fmt.Sprintf("%v", dnc)},
			map[string]string{"reason": reason},
		)
	}
	return s.GetAccount(ctx, orgID, id)
}
