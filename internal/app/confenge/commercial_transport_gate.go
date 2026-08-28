package confenge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
)

// AccountCommercialQualification rebuilds the per-account decision from the
// durable columns. It reads no source-health signal on purpose.
func AccountCommercialQualification(acc *models.OutreachAccount, now time.Time) CommercialQualificationDecision {
	out := CommercialQualificationDecision{State: CommercialUnknown}
	if acc == nil {
		out.ReasonCodes = []string{ReasonQualificationMissing}
		return out
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	state := strings.ToUpper(strings.TrimSpace(acc.CommercialQualificationState))
	if state == "" || state == CommercialUnknown {
		out.ReasonCodes = []string{ReasonQualificationMissing}
		return out
	}
	out.Present = true
	out.PolicyVersion = strings.TrimSpace(acc.CommercialQualificationPolicyVersion)
	out.CNPJRoot8 = strings.TrimSpace(acc.CommercialQualificationCNPJRoot8)
	out.QualifyingContractID = strings.TrimSpace(acc.CommercialQualifyingContractID)
	out.QualifyingDateField = strings.TrimSpace(acc.CommercialQualifyingDateField)
	out.EvidenceHash = strings.ToLower(strings.TrimSpace(acc.CommercialQualificationEvidenceHash))
	out.QualifyingContractDate = acc.CommercialQualifyingContractDate
	out.QualifiedUntil = acc.CommercialQualifiedUntil

	if !RecognizeCommercialAuthorityPolicy(out.PolicyVersion) {
		out.ReasonCodes = []string{ReasonPolicyVersionUnsupported}
		return out
	}
	if acc.CommercialQualificationDeactivated || state == CommercialRevoked {
		out.State = CommercialRevoked
		out.ReasonCodes = []string{ReasonQualificationRevoked}
		return out
	}
	if !strings.EqualFold(strings.TrimSpace(acc.TargetPartyRole), PartyRoleSupplier) {
		out.ReasonCodes = []string{ReasonQualificationRoleInvalid}
		return out
	}
	// Recompute the qualifying fact from its own stored evidence. A single
	// mutated byte in role, contract identity, date or window fails closed.
	rebuilt := RootQualification{
		CNPJRoot8:              out.CNPJRoot8,
		PartyRole:              PartyRoleSupplier,
		QualifyingContractID:   out.QualifyingContractID,
		QualifyingDateField:    out.QualifyingDateField,
		EvidenceReference:      strings.TrimSpace(acc.CommercialQualificationEvidenceReference),
		QualifyingContractDate: formatQualifyingDate(acc.CommercialQualifyingContractDate),
		QualifiedUntil:         formatQualifyingDate(acc.CommercialQualifiedUntil),
	}
	if out.EvidenceHash == "" || out.EvidenceHash != HashRootQualification(rebuilt) {
		out.ReasonCodes = []string{ReasonQualificationEvidenceDrift}
		return out
	}
	if acc.CommercialQualifyingContractDate == nil || acc.CommercialQualifiedUntil == nil {
		out.ReasonCodes = []string{ReasonQualificationWindowInvalid}
		return out
	}
	// qualified_until is derived, never declared. It must be exactly the
	// contracting act plus the rolling window.
	if !acc.CommercialQualifiedUntil.UTC().Equal(QualifiedUntilFor(*acc.CommercialQualifyingContractDate)) {
		out.ReasonCodes = []string{ReasonQualificationWindowInvalid}
		return out
	}
	if !now.Before(acc.CommercialQualifiedUntil.UTC()) {
		out.State = CommercialExpired
		out.ReasonCodes = []string{ReasonQualificationExpired}
		return out
	}
	out.State = CommercialQualified
	out.ReasonCodes = []string{ReasonQualified}
	return out
}

func formatQualifyingDate(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

// storedCommercialAuthorityV2 decodes the applied population attestation.
func storedCommercialAuthorityV2(state *models.OutreachFeedSyncState) *FeedCommercialAuthorityV2 {
	if state == nil || len(state.CommercialAuthorityV2JSON) == 0 {
		return nil
	}
	var payload FeedCommercialAuthorityV2
	if json.Unmarshal(state.CommercialAuthorityV2JSON, &payload) != nil {
		return nil
	}
	return &payload
}

// commercialBindingV2FromStoredFeed derives the live publication identity the
// attestation must close against.
func commercialBindingV2FromStoredFeed(state *models.OutreachFeedSyncState, payload *FeedCommercialAuthorityV2) CommercialAuthorityBinding {
	if state == nil {
		return CommercialAuthorityBinding{}
	}
	binding := CommercialAuthorityBinding{
		SourceRunID:    strings.TrimSpace(state.LastRunID),
		SnapshotHash:   strings.TrimSpace(state.LastSnapshotHash),
		MembershipHash: strings.ToLower(strings.TrimSpace(state.TargetMembershipHash)),
	}
	// Read from the stored columns, not from the payload being validated, so
	// these two dimensions are a real comparison rather than a self-check.
	binding.PublicationSemanticHash = strings.ToLower(strings.TrimSpace(state.PublicationSemanticHash))
	binding.ProducerIdentity = strings.ToLower(strings.TrimSpace(state.ProducerIdentity))
	_ = payload
	return binding
}

// FeedCommercialAuthorityState evaluates the applied population attestation.
func FeedCommercialAuthorityState(state *models.OutreachFeedSyncState) CommercialQualificationDecision {
	payload := storedCommercialAuthorityV2(state)
	return EvaluateCommercialAuthorityV2(payload, commercialBindingV2FromStoredFeed(state, payload))
}

// assertCommercialAuthorityForTransport is the replacement for the old
// freshness assertion on the send path. It proves the snapshot is structurally
// whole, the population attestation is intact, and this specific account is
// still QUALIFIED by the three-year rule. Producer age is never consulted.
func (s *service) assertCommercialAuthorityForTransport(ctx context.Context, orgID uuid.UUID, acc *models.OutreachAccount) error {
	if s == nil || (!s.cfg.FeedSyncEnabled && !s.cfg.OperatorMode) {
		return nil
	}
	state, err := s.repo.GetFeedSyncState(ctx, orgID)
	if err != nil || state == nil {
		return fmt.Errorf("authoritative feed state unavailable")
	}
	if err := validateAuthoritativeFeedStructure(state, s.cfg.DelegatedFirstTouchEnabled); err != nil {
		return err
	}
	if acc == nil {
		return fmt.Errorf("target-fit account missing at transport")
	}
	now := time.Now().UTC()
	qual := AccountCommercialQualification(acc, now)
	if qual.Present {
		if !qual.AllowsTransport() {
			return fmt.Errorf("commercial qualification blocks transport: %s", firstNonEmpty(firstHold(qual.ReasonCodes), ReasonQualificationMissing))
		}
		// A qualified member binds to the publication that proved it. The run
		// id is provenance: an account carried forward from an earlier run
		// stays transportable while its evidence and membership hold.
		authority := FeedCommercialAuthorityState(state)
		if authority.Present && authority.State != CommercialQualified {
			return fmt.Errorf("commercial authority blocks transport: %s", firstNonEmpty(firstHold(authority.ReasonCodes), ReasonQualificationMissing))
		}
		return nil
	}
	// No V2 qualification on this account: fail closed on the commercial fact
	// itself. Source freshness must never be substituted for it.
	return fmt.Errorf("%s", ReasonQualificationMissing)
}
