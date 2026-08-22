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

func AccountsFromOrg(ctx context.Context, repo repository.OutreachRepository, orgID uuid.UUID, source string) ([]CohortAccountInput, error) {
	if repo == nil {
		return nil, fmt.Errorf("repository required")
	}
	accs, err := repo.ListAccounts(ctx, orgID, repository.OutreachAccountFilter{})
	if err != nil {
		return nil, err
	}
	out := make([]CohortAccountInput, 0, len(accs))
	for i := range accs {
		cands, err := repo.ListCandidates(ctx, orgID, accs[i].ID)
		if err != nil {
			return nil, err
		}
		out = append(out, CohortAccountInput{Account: accs[i], Candidates: cands, Source: source})
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
	if fc.ProvenanceChainValid != nil && !*fc.ProvenanceChainValid {
		c.EmailSendReady = false
		if c.BlockReason == "" {
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

// routeSuppressionActive is extra-cli's suppression enum. NONE/CLEAR/empty are not suppressed.
func routeSuppressionActive(v string) bool {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case "SUPPRESSED", "DO_NOT_CONTACT", "DNC":
		return true
	default:
		return false
	}
}
