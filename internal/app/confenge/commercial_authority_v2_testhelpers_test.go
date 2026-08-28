package confenge

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/warmbly/warmbly/internal/models"
)

// testRootQualification builds a self-consistent COMMERCIAL_AUTHORITY/2.0
// qualifying fact: a supplier contract signed one year ago, so it sits well
// inside the rolling three-year window.
func testRootQualification(root8 string, contractDate time.Time) RootQualification {
	q := RootQualification{
		CNPJRoot8:               root8,
		TargetFitClass:          TargetFitConfirmed,
		PartyRole:               PartyRoleSupplier,
		QualifyingContractID:    "contract-" + root8,
		QualifyingContractDate:  contractDate.UTC().Format("2006-01-02"),
		QualifyingDateField:     QualifyingDateFieldAssinatura,
		QualifyingContractCount: 1,
		QualifiedUntil:          QualifiedUntilFor(contractDate).Format("2006-01-02"),
		EvidenceReference:       "extra-cli:v_contracts_canonical_v2:contract-" + root8,
		Provenance:              "extra-cli:v_contracts_canonical_v2",
	}
	q.EvidenceHash = HashRootQualification(q)
	return q
}

// applyTestQualification stamps a valid qualifying fact onto an account.
func applyTestQualification(acc *models.OutreachAccount, contractDate, now time.Time) {
	root := acc.CNPJRoot
	if root == "" && len(acc.CNPJ14) >= 8 {
		root = acc.CNPJ14[:8]
	}
	q := testRootQualification(root, contractDate)
	applyCommercialQualificationToAccount(acc, &q, now)
}

// testCommercialAuthorityV2JSON builds the population attestation bound to the
// live publication identity.
func testCommercialAuthorityV2JSON(runID, snapshotHash, membershipHash, semanticHash, producer string, roots []RootQualification) []byte {
	payload := FeedCommercialAuthorityV2{
		Schema:                       CommercialAuthorityContractV2,
		ContractVersion:              CommercialAuthorityContractV2,
		PolicyVersion:                CommercialAuthorityPolicyV2,
		BasisSourceRunID:             runID,
		BasisSnapshotHash:            snapshotHash,
		BasisMembershipHash:          strings.ToLower(membershipHash),
		BasisPublicationSemanticHash: strings.ToLower(semanticHash),
		ProducerIdentity:             strings.ToLower(producer),
		QualificationWindowYears:     QualificationWindowYears,
		QualificationEvidenceHash:    HashQualificationCorpus(roots),
		QualifiedRootCount:           len(roots),
		State:                        CommercialQualified,
	}
	raw, _ := json.Marshal(payload)
	return raw
}

// stampFeedStateWithV2 attaches a valid V2 attestation to a feed sync state.
func stampFeedStateWithV2(st *models.OutreachFeedSyncState, roots []RootQualification) {
	if st == nil {
		return
	}
	semantic := strings.Repeat("s", 64)
	producer := strings.Repeat("p", 64)
	st.CommercialAuthorityV2JSON = testCommercialAuthorityV2JSON(
		st.LastRunID, st.LastSnapshotHash, st.TargetMembershipHash, semantic, producer, roots)
	st.QualificationEvidenceHash = HashQualificationCorpus(roots)
	st.QualifiedRootCount = len(roots)
	st.QualificationWindowYears = QualificationWindowYears
}
