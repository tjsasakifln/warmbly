package confenge

import (
	"testing"
	"time"

	"github.com/warmbly/warmbly/internal/models"
)

func qualifiedAccount(now time.Time) *models.OutreachAccount {
	acc := &models.OutreachAccount{
		CNPJ14: "11222333000144", CNPJRoot: "11222333",
		TargetPartyRole:      PartyRoleSupplier,
		ContractorRoleStatus: ContractorRoleConfirmed,
	}
	applyTestQualification(acc, now.AddDate(-1, 0, 0), now)
	return acc
}

// 1/2/10. A company proven inside the window stays transportable no matter how
// old, stale or missing the producer's last run is: the account decision reads
// no source signal at all.
func TestAccountQualificationIgnoresSourceAgeEntirely(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	acc := qualifiedAccount(now)
	if got := AccountCommercialQualification(acc, now); !got.AllowsTransport() {
		t.Fatalf("qualified account not transportable: %+v", got)
	}
	// Evaluate the same account a full year later. Only the window matters.
	oneYearOn := now.AddDate(1, 0, 0)
	if got := AccountCommercialQualification(acc, oneYearOn); !got.AllowsTransport() {
		t.Fatalf("a year of crawler silence revoked a 3-year-valid company: %+v", got)
	}
	// Two years and a day past the contract the window finally closes.
	past := acc.CommercialQualifiedUntil.Add(time.Hour)
	got := AccountCommercialQualification(acc, past)
	if got.State != CommercialExpired {
		t.Fatalf("the window never closed: %+v", got)
	}
	if !containsStr(got.ReasonCodes, ReasonQualificationExpired) {
		t.Fatalf("expiry not reported: %+v", got.ReasonCodes)
	}
}

// 9. Recipient-level expiry is a separate concern and must not be expressed as
// company disqualification.
func TestRecipientConcernsDoNotTouchCompanyQualification(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	acc := qualifiedAccount(now)
	acc.DoNotContact = true
	if got := AccountCommercialQualification(acc, now); !got.AllowsTransport() {
		t.Fatalf("DNC leaked into commercial qualification: %+v", got)
	}
	acc.DoNotContact = false
	acc.Blocked = true
	if got := AccountCommercialQualification(acc, now); !got.AllowsTransport() {
		t.Fatalf("suppression leaked into commercial qualification: %+v", got)
	}
}

// 5. Deactivation on the durable row blocks immediately.
func TestDurableDeactivationBlocksTransport(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	acc := qualifiedAccount(now)
	acc.CommercialQualificationDeactivated = true
	got := AccountCommercialQualification(acc, now)
	if got.AllowsTransport() || got.State != CommercialRevoked {
		t.Fatalf("deactivated account still transportable: %+v", got)
	}
}

// 4. A buyer row can never transport, whatever else is stamped on it.
func TestBuyerAccountNeverTransports(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	acc := qualifiedAccount(now)
	acc.TargetPartyRole = "BUYER"
	if got := AccountCommercialQualification(acc, now); got.AllowsTransport() {
		t.Fatalf("a contracting body was transportable: %+v", got)
	}
}

// 16. Mutating a durable qualification column without re-deriving the evidence
// hash fails closed at the gate.
func TestDurableQualificationTamperFailsClosed(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		mutate func(*models.OutreachAccount)
	}{
		{"contract_id", func(a *models.OutreachAccount) { a.CommercialQualifyingContractID = "forged" }},
		{"date_field", func(a *models.OutreachAccount) { a.CommercialQualifyingDateField = QualifyingDateFieldPublicacao }},
		{"root", func(a *models.OutreachAccount) { a.CommercialQualificationCNPJRoot8 = "00000000" }},
		{"evidence_ref", func(a *models.OutreachAccount) { a.CommercialQualificationEvidenceReference = "forged" }},
		{"evidence_hash", func(a *models.OutreachAccount) { a.CommercialQualificationEvidenceHash = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			acc := qualifiedAccount(now)
			tc.mutate(acc)
			if got := AccountCommercialQualification(acc, now); got.AllowsTransport() {
				t.Fatalf("tampered durable qualification transported: %+v", got)
			}
		})
	}

	// Stretching qualified_until past contract_date + 3y is refused.
	acc := qualifiedAccount(now)
	stretched := acc.CommercialQualifiedUntil.AddDate(5, 0, 0)
	acc.CommercialQualifiedUntil = &stretched
	got := AccountCommercialQualification(acc, now)
	if got.AllowsTransport() {
		t.Fatalf("a stretched window transported: %+v", got)
	}
	// qualified_until is inside the evidence hash, so stretching it trips
	// evidence drift before the window check. Either is fail-closed.
	if !containsStr(got.ReasonCodes, ReasonQualificationEvidenceDrift) &&
		!containsStr(got.ReasonCodes, ReasonQualificationWindowInvalid) {
		t.Fatalf("window tampering not reported: %+v", got.ReasonCodes)
	}

	// Re-deriving the hash over the stretched window still fails: the window is
	// derived from the contracting date, never declared.
	acc2 := qualifiedAccount(now)
	stretched2 := acc2.CommercialQualifiedUntil.AddDate(5, 0, 0)
	acc2.CommercialQualifiedUntil = &stretched2
	acc2.CommercialQualificationEvidenceHash = HashRootQualification(RootQualification{
		CNPJRoot8:              acc2.CommercialQualificationCNPJRoot8,
		PartyRole:              PartyRoleSupplier,
		QualifyingContractID:   acc2.CommercialQualifyingContractID,
		QualifyingDateField:    acc2.CommercialQualifyingDateField,
		EvidenceReference:      acc2.CommercialQualificationEvidenceReference,
		QualifyingContractDate: acc2.CommercialQualifyingContractDate.UTC().Format("2006-01-02"),
		QualifiedUntil:         stretched2.UTC().Format("2006-01-02"),
	})
	got2 := AccountCommercialQualification(acc2, now)
	if got2.AllowsTransport() {
		t.Fatalf("a re-signed stretched window transported: %+v", got2)
	}
	if !containsStr(got2.ReasonCodes, ReasonQualificationWindowInvalid) {
		t.Fatalf("derived-window violation not reported: %+v", got2.ReasonCodes)
	}
}

// 20. An account with no V2 evidence is refused explicitly, never rescued by a
// healthy source.
func TestMissingQualificationIsExplicitAndFailClosed(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	acc := &models.OutreachAccount{CNPJ14: "11222333000144", TargetPartyRole: PartyRoleSupplier}
	got := AccountCommercialQualification(acc, now)
	if got.AllowsTransport() {
		t.Fatal("an unqualified account transported")
	}
	if !containsStr(got.ReasonCodes, ReasonQualificationMissing) {
		t.Fatalf("absence not named: %+v", got.ReasonCodes)
	}
}

// 11/12/13. A new publication deterministically changes membership: a company
// whose last qualifying contract has aged out expires, and one that re-enters
// with a newer contract qualifies again.
func TestMembershipChangesDeterministicallyWithNewEvidence(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	acc := &models.OutreachAccount{CNPJ14: "11222333000144", CNPJRoot: "11222333", TargetPartyRole: PartyRoleSupplier}

	aged := testRootQualification("11222333", now.AddDate(-3, 0, -30))
	applyCommercialQualificationToAccount(acc, &aged, now)
	if AccountCommercialQualification(acc, now).AllowsTransport() {
		t.Fatal("a company whose only contract aged out still transports")
	}

	renewed := testRootQualification("11222333", now.AddDate(0, -2, 0))
	applyCommercialQualificationToAccount(acc, &renewed, now)
	if !AccountCommercialQualification(acc, now).AllowsTransport() {
		t.Fatal("a company with a new qualifying contract did not re-enter")
	}

	// 14/15. Replaying identical evidence is a no-op.
	before := *acc
	applyCommercialQualificationToAccount(acc, &renewed, now)
	if acc.CommercialQualificationEvidenceHash != before.CommercialQualificationEvidenceHash ||
		!acc.CommercialQualifiedUntil.Equal(*before.CommercialQualifiedUntil) ||
		acc.CommercialQualificationState != before.CommercialQualificationState {
		t.Fatal("replaying the same evidence was not idempotent")
	}
}

// target_fit_fresh is producer watermark lag, not a commercial fact. A company
// still inside the three-year window must not be disqualified because the
// classifier has not re-scored since the datalake moved.
func TestTargetFitWatermarkLagDoesNotDisqualifyAQualifiedCompany(t *testing.T) {
	now := time.Now().UTC()
	acc := qualifiedAccount(now)
	acc.TargetFitClass = TargetFitConfirmed
	acc.TargetFitVersion = "confenge-target-fit-v2"
	acc.TargetFitSourceWatermark = now.Format(time.RFC3339)
	acc.TargetFitObservedAt = &now
	acc.TargetFitFresh = false // producer lag

	if err := RequireTargetFit(acc); err != nil {
		t.Fatalf("watermark lag disqualified a qualified company: %v", err)
	}

	// Without a live qualification the lag still fails closed.
	acc.CommercialQualificationState = CommercialUnknown
	if err := RequireTargetFit(acc); err == nil {
		t.Fatal("watermark lag stopped failing closed for an unqualified company")
	}
}
