package confenge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// PrepareControlledCohortFromFeed derives the frozen snapshot from an extra-cli
// document. No hashes need to be supplied by the founder.
func PrepareControlledCohortFromFeed(feed *Feed, opts CohortPrepareOptions) (*FrozenCohortSnapshot, error) {
	if feed == nil {
		return nil, nil
	}
	if opts.FeedSchemaVersion == "" {
		opts.FeedSchemaVersion = firstNonEmpty(feed.SchemaVersion, models.OutreachSchemaV1)
	}
	if opts.SnapshotHash == "" {
		opts.SnapshotHash = feed.Source.SnapshotHash
	}
	if opts.FeedIdentity == "" {
		opts.FeedIdentity = firstNonEmpty(feed.Source.RunID, feed.Source.SnapshotHash)
	}
	if opts.RepositorySHA == "" {
		opts.RepositorySHA = feed.Source.RepoSHA
	}
	if opts.Source == "" {
		opts.Source = firstNonEmpty(feed.Source.System, "extra-cli")
	}
	opts.AuthoritativeSourceFreshness = feed.Source.AuthoritativeFreshness
	accounts := accountsFromFeed(feed, opts.Source)
	return PrepareControlledCohort(accounts, opts)
}

func accountsFromFeed(feed *Feed, source string) []CohortAccountInput {
	if feed == nil {
		return nil
	}
	out := make([]CohortAccountInput, 0, len(feed.Leads))
	for i := range feed.Leads {
		lead := &feed.Leads[i]
		acc := models.OutreachAccount{
			SourceLeadID:  lead.SourceLeadID,
			CNPJ14:        lead.Company.CNPJ14,
			RazaoSocial:   lead.Company.RazaoSocial,
			NomeFantasia:  lead.Company.NomeFantasia,
			Municipio:     lead.Company.Municipio,
			UF:            lead.Company.UF,
			MomentCode:    lead.Moment.Code,
			MomentSummary: lead.Moment.Summary,
			ServiceCode:   lead.Offer.ServiceCode,
			ServiceName:   lead.Offer.ServiceName,
			FactToMention: lead.MessagingContext.FactToMention,
			QuestionToAsk: lead.MessagingContext.QuestionToAsk,
			CTA:           lead.MessagingContext.CTA,
			SourceRunID:   feed.Source.RunID,

			// Carried for the founder preview's admission evidence. Nothing in
			// the prepare ladder reads these: the ladder admits on controlled
			// route eligibility and copy QA, and dropping them here only hid
			// from the reviewer what the feed had actually asserted.
			PriorityRank:             lead.Priority.Rank,
			PriorityScore:            lead.Priority.Score,
			PriorityTier:             lead.Priority.Tier,
			PriorityConfidence:       lead.Priority.Confidence,
			MomentConfidence:         lead.Moment.Confidence,
			MomentObservedAt:         parseFeedDate(lead.Moment.ObservedAt),
			MomentEvidenceIDs:        append([]string(nil), lead.Moment.EvidenceIDs...),
			TargetFitSendTier:        lead.TargetFitSendTier,
			TargetFitReasons:         append([]string(nil), lead.TargetFitReasons...),
			TargetFitClass:           lead.TargetFitClass,
			TargetFitConfidence:      lead.TargetFitConfidence,
			TargetFitVersion:         lead.TargetFitVersion,
			TargetFitComputedAt:      parseFeedDate(lead.TargetFitComputedAt),
			TargetFitSourceWatermark: lead.TargetFitSourceWatermark,
			TargetFitEvidenceIDs:     append([]string(nil), lead.TargetFitEvidenceIDs...),
			TargetFitFreshnessReason: lead.TargetFitFreshnessReason,
		}
		if lead.TargetFitFresh != nil {
			acc.TargetFitFresh = *lead.TargetFitFresh
		}
		if lead.Activation != nil && strings.EqualFold(lead.Activation.State, ActivationSuppressed) {
			acc.Blocked = true
			acc.BlockReason = "activation_suppressed"
		}
		cands := make([]models.OutreachContactCandidate, 0, len(lead.Contacts))
		for j := range lead.Contacts {
			cands = append(cands, candidateFromFeedContact(lead.Contacts[j]))
		}
		out = append(out, CohortAccountInput{Account: acc, Candidates: cands, Source: source})
	}
	return out
}

// orgAccountPageSize is the per-query page for org-bound account scans. An empty
// filter silently means 50 rows in the repository, which is never what a freeze wants.
const orgAccountPageSize = 1000

// maxOrgFreezeAccounts bounds an org-bound freeze. Beyond it the operator must
// scope by feed run: truncating would freeze a different cohort than the one asked for.
const maxOrgFreezeAccounts = 5000

func AccountsFromOrg(ctx context.Context, repo repository.OutreachRepository, orgID uuid.UUID, source string) ([]CohortAccountInput, error) {
	return accountsFromOrgScoped(ctx, repo, orgID, source, "")
}

// accountsFromOrgScoped pages through outreach_accounts with an explicit limit,
// pushing the run scope into the query so the freeze never depends on page one.
func accountsFromOrgScoped(ctx context.Context, repo repository.OutreachRepository, orgID uuid.UUID, source, runID string) ([]CohortAccountInput, error) {
	if repo == nil {
		return nil, fmt.Errorf("repository required")
	}
	var accs []models.OutreachAccount
	for offset := 0; ; offset += orgAccountPageSize {
		batch, err := repo.ListAccounts(ctx, orgID, repository.OutreachAccountFilter{
			Limit:       orgAccountPageSize,
			Offset:      offset,
			StableOrder: true,
			SourceRunID: runID,
		})
		if err != nil {
			return nil, err
		}
		accs = append(accs, batch...)
		if len(accs) > maxOrgFreezeAccounts {
			if runID == "" {
				return nil, fmt.Errorf("organization has more than %d accounts; scope the freeze to one feed run (--feed) instead of the whole org", maxOrgFreezeAccounts)
			}
			return nil, fmt.Errorf("feed run %q has more than %d accounts; split the import before freezing", runID, maxOrgFreezeAccounts)
		}
		if len(batch) < orgAccountPageSize {
			break
		}
	}
	out := make([]CohortAccountInput, 0, len(accs))
	for i := range accs {
		cands, err := repo.ListCandidates(ctx, orgID, accs[i].ID)
		if err != nil {
			return nil, err
		}
		out = append(out, CohortAccountInput{Account: accs[i], Candidates: cands, Source: source, Persisted: true})
	}
	return out, nil
}

func candidateFromFeedContact(fc FeedContact) models.OutreachContactCandidate {
	c := models.OutreachContactCandidate{
		SourceContactID:                fc.SourceContactID,
		PersonID:                       fc.PersonID,
		Name:                           fc.Name,
		Role:                           fc.Role,
		Email:                          fc.Email,
		Phone:                          fc.Phone,
		SourceURL:                      fc.SourceURL,
		SourceDocument:                 fc.SourceDocument,
		VerificationStatus:             fc.VerificationStatus,
		Confidence:                     fc.Confidence,
		Recommended:                    fc.Recommended,
		MailboxPurpose:                 fc.MailboxPurpose,
		OwnershipStatus:                fc.OwnershipStatus,
		RecipientCommercialSuitability: fc.RecipientCommercialSuitability,
		EmailDerivation:                fc.EmailDerivation,
		ChannelEpistemic:               fc.ChannelEpistemic,
		RouteFreshness:                 fc.RouteFreshness,
		RouteSuppression:               fc.RouteSuppression,
		ReachabilityClass:              fc.ReachabilityClass,
		RouteType:                      fc.RouteType,
		RouteRelation:                  fc.RouteRelation,
	}
	if fc.EmailSendReady != nil {
		c.EmailSendReady = *fc.EmailSendReady
	}
	if fc.MailboxPurposeSendBlocked != nil {
		c.MailboxPurposeSendBlocked = *fc.MailboxPurposeSendBlocked
	}
	if d := strings.TrimSpace(fc.SourceDate); d != "" {
		if ts, err := time.Parse("2006-01-02", d); err == nil {
			c.SourceDate = &ts
		} else if ts, err := time.Parse(time.RFC3339, d); err == nil {
			c.SourceDate = &ts
		}
	}
	switch strings.ToUpper(strings.TrimSpace(fc.VerificationStatus)) {
	case models.OutreachVerifyBounced:
		c.Bounced = true
	case models.OutreachVerifyDoNotContact:
		c.DoNotContact = true
	}
	if routeSuppressionActive(fc.RouteSuppression) {
		c.DoNotContact = true
		if c.BlockReason == "" {
			c.BlockReason = "route_suppression:" + strings.ToUpper(strings.TrimSpace(fc.RouteSuppression))
		}
	}
	controlledReviewAuthority := FeedControlledReviewAuthority(fc)
	if fc.ProvenanceChainValid != nil && !*fc.ProvenanceChainValid {
		c.EmailSendReady = false
		if !controlledReviewAuthority && c.BlockReason == "" {
			c.BlockReason = "provenance_chain_invalid"
		}
	}
	derivedFixture := fc.DerivedFromFixture != nil && *fc.DerivedFromFixture
	if t, reason := ContactProvenanceTainted(fc.Email, fc.SourceURL, "", fc.VerificationStatus, derivedFixture); t {
		c.EmailSendReady = false
		c.Blocked = true
		c.BlockReason = "provenance_taint:" + reason
	}
	c.DiscoveryJSON = mergeControlledDiscovery(fc.DiscoveryJSON, fc)
	return c
}

// parseFeedDate accepts both shapes extra-cli emits. An unparseable date stays
// nil so the preview reports it absent instead of inventing an epoch.
func parseFeedDate(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339} {
		if ts, err := time.Parse(layout, s); err == nil {
			ts = ts.UTC()
			return &ts
		}
	}
	return nil
}

// routeSuppressionActive is extra-cli's suppression enum. NONE/CLEAR/empty are not suppressed.
func routeSuppressionActive(v string) bool {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case "SUPPRESSED", "DO_NOT_CONTACT", "DNC":
		return true
	default:
		return false
	}
}

// AccountsFromOrgForRun loads PG-bound accounts limited to one extra-cli feed
// run. Freezing from Postgres keeps real account/candidate ids for dispatch;
// scoping by run id keeps the frozen set tied to the imported feed.
func AccountsFromOrgForRun(ctx context.Context, repo repository.OutreachRepository, orgID uuid.UUID, source, runID string) ([]CohortAccountInput, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return AccountsFromOrg(ctx, repo, orgID, source)
	}
	out, err := accountsFromOrgScoped(ctx, repo, orgID, source, runID)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no imported accounts for feed run %q; import the feed first", runID)
	}
	return out, nil
}
