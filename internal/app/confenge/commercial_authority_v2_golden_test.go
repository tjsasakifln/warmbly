package confenge

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// The producer (extra-cli) and this runtime must agree on the qualification
// digest byte for byte. A drift here means Warmbly would fail closed on every
// lead the producer publishes, so the vector is pinned on both sides.
func TestCommercialAuthorityV2CrossLanguageGoldenVector(t *testing.T) {
	raw, err := os.ReadFile("testdata/commercial_authority_v2_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		Qualification struct {
			CNPJRoot8              string `json:"cnpj_root8"`
			PartyRole              string `json:"party_role"`
			QualifyingContractID   string `json:"qualifying_contract_id"`
			QualifyingContractDate string `json:"qualifying_contract_date"`
			QualifyingDateField    string `json:"qualifying_date_field"`
			QualifiedUntil         string `json:"qualified_until"`
			EvidenceReference      string `json:"qualification_evidence_reference"`
		} `json:"qualification"`
		ExpectedEvidenceHash string `json:"expected_evidence_hash"`
		ExpectedCorpusHash   string `json:"expected_corpus_hash"`
		LeapNormalization    struct {
			ContractDate   string `json:"contract_date"`
			QualifiedUntil string `json:"qualified_until"`
		} `json:"leap_normalization"`
	}
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	q := RootQualification{
		CNPJRoot8:              golden.Qualification.CNPJRoot8,
		PartyRole:              golden.Qualification.PartyRole,
		QualifyingContractID:   golden.Qualification.QualifyingContractID,
		QualifyingContractDate: golden.Qualification.QualifyingContractDate,
		QualifyingDateField:    golden.Qualification.QualifyingDateField,
		QualifiedUntil:         golden.Qualification.QualifiedUntil,
		EvidenceReference:      golden.Qualification.EvidenceReference,
	}
	if got := HashRootQualification(q); got != golden.ExpectedEvidenceHash {
		t.Fatalf("evidence hash drifted from the producer: got %s want %s", got, golden.ExpectedEvidenceHash)
	}
	q.EvidenceHash = golden.ExpectedEvidenceHash
	if got := HashQualificationCorpus([]RootQualification{q}); got != golden.ExpectedCorpusHash {
		t.Fatalf("corpus hash drifted from the producer: got %s want %s", got, golden.ExpectedCorpusHash)
	}

	// Leap-day normalization must be Go's forward normalization on both sides.
	leap, err := time.Parse("2006-01-02", golden.LeapNormalization.ContractDate)
	if err != nil {
		t.Fatal(err)
	}
	if got := QualifiedUntilFor(leap).Format("2006-01-02"); got != golden.LeapNormalization.QualifiedUntil {
		t.Fatalf("leap normalization drifted: got %s want %s", got, golden.LeapNormalization.QualifiedUntil)
	}
}
