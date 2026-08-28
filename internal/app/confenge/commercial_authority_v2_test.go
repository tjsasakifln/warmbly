package confenge

import (
	"strings"
	"testing"
	"time"
)

func v2Binding() CommercialAuthorityBinding {
	return CommercialAuthorityBinding{
		SourceRunID:             "run-abc",
		SnapshotHash:            "snap-abc",
		MembershipHash:          strings.Repeat("a", 64),
		PublicationSemanticHash: strings.Repeat("s", 64),
		ProducerIdentity:        strings.Repeat("p", 64),
	}
}

func v2Payload(roots []RootQualification) *FeedCommercialAuthorityV2 {
	return &FeedCommercialAuthorityV2{
		Schema:                       CommercialAuthorityContractV2,
		ContractVersion:              CommercialAuthorityContractV2,
		PolicyVersion:                CommercialAuthorityPolicyV2,
		BasisSourceRunID:             "run-abc",
		BasisSnapshotHash:            "snap-abc",
		BasisMembershipHash:          strings.Repeat("a", 64),
		BasisPublicationSemanticHash: strings.Repeat("s", 64),
		ProducerIdentity:             strings.Repeat("p", 64),
		QualificationWindowYears:     QualificationWindowYears,
		QualificationEvidenceHash:    HashQualificationCorpus(roots),
		QualifiedRootCount:           len(roots),
		State:                        CommercialQualified,
	}
}

// 3. A contract outside the rolling window does not qualify, however fresh the
// crawler is. 1/2/10. Source age never enters the commercial verdict.
func TestCommercialQualificationIsDecidedByContractDateNotSourceAge(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)

	inWindow := testRootQualification("11222333", time.Date(2025, 5, 10, 0, 0, 0, 0, time.UTC))
	if got := EvaluateRootQualification(&inWindow, now); got.State != CommercialQualified {
		t.Fatalf("a 2025 supplier contract did not qualify: %+v", got)
	}

	// Exactly three years and one day old: outside the window.
	outOfWindow := testRootQualification("11222333", now.AddDate(-3, 0, -1))
	if got := EvaluateRootQualification(&outOfWindow, now); got.State != CommercialExpired {
		t.Fatalf("a contract older than three years still qualified: %+v", got)
	}

	// The boundary itself is inclusive of the last qualifying instant.
	boundary := testRootQualification("11222333", now.AddDate(-3, 0, 1))
	if got := EvaluateRootQualification(&boundary, now); got.State != CommercialQualified {
		t.Fatalf("a contract one day inside the window expired: %+v", got)
	}
}

// 4. A contracting body is never a lead, even with a contract in the window.
func TestBuyerRoleNeverQualifies(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	buyer := testRootQualification("99888777", now.AddDate(-1, 0, 0))
	buyer.PartyRole = "BUYER"
	buyer.EvidenceHash = HashRootQualification(buyer)
	got := EvaluateRootQualification(&buyer, now)
	if got.State == CommercialQualified {
		t.Fatalf("a contracting body qualified as a lead: %+v", got)
	}
	if !containsStr(got.ReasonCodes, ReasonQualificationRoleInvalid) {
		t.Fatalf("buyer role not reported: %+v", got.ReasonCodes)
	}
}

// 5. Explicit revocation blocks immediately, inside the window or not.
func TestExplicitRevocationBlocksImmediately(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	q := testRootQualification("11222333", now.AddDate(-1, 0, 0))
	q.Deactivated = true
	q.DeactivationReason = "EXPLICIT_REVOCATION"
	q.EvidenceHash = HashRootQualification(q)
	if got := EvaluateRootQualification(&q, now); got.State != CommercialRevoked {
		t.Fatalf("explicit revocation did not block: %+v", got)
	}

	payload := v2Payload([]RootQualification{q})
	payload.ReasonCodes = []string{"EXPLICIT_REVOCATION"}
	if got := EvaluateCommercialAuthorityV2(payload, v2Binding()); got.State != CommercialRevoked {
		t.Fatalf("population-level revocation did not block: %+v", got)
	}
}

// 16. A single mutated byte in the evidence binding fails closed.
func TestOneByteOfEvidenceCorruptionFailsClosed(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	base := testRootQualification("11222333", now.AddDate(-1, 0, 0))

	for _, tc := range []struct {
		name   string
		mutate func(*RootQualification)
	}{
		{"contract_id", func(q *RootQualification) { q.QualifyingContractID += "x" }},
		{"contract_date", func(q *RootQualification) { q.QualifyingContractDate = "2024-01-01" }},
		{"date_field", func(q *RootQualification) { q.QualifyingDateField = QualifyingDateFieldInicio }},
		{"root", func(q *RootQualification) { q.CNPJRoot8 = "00000000" }},
		{"evidence_reference", func(q *RootQualification) { q.EvidenceReference += "x" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := base
			tc.mutate(&q) // evidence hash deliberately NOT recomputed
			if got := EvaluateRootQualification(&q, now); got.State == CommercialQualified {
				t.Fatalf("tampered evidence still qualified: %+v", got)
			}
		})
	}

	// A producer cannot buy runway by declaring a later qualified_until.
	stretched := base
	stretched.QualifiedUntil = QualifiedUntilFor(now).AddDate(5, 0, 0).Format("2006-01-02")
	stretched.EvidenceHash = HashRootQualification(stretched)
	got := EvaluateRootQualification(&stretched, now)
	if got.State == CommercialQualified {
		t.Fatalf("a stretched qualified_until was accepted: %+v", got)
	}
	if !containsStr(got.ReasonCodes, ReasonQualificationWindowInvalid) {
		t.Fatalf("window tampering not reported: %+v", got.ReasonCodes)
	}
}

// 17. Binding mismatch fails closed; a stale run id alone is not the test.
func TestPopulationAuthorityBindingMustClose(t *testing.T) {
	roots := []RootQualification{testRootQualification("11222333", time.Now().UTC().AddDate(-1, 0, 0))}
	payload := v2Payload(roots)
	if got := EvaluateCommercialAuthorityV2(payload, v2Binding()); got.State != CommercialQualified {
		t.Fatalf("intact binding rejected: %+v", got)
	}
	for _, tc := range []struct {
		name string
		bind func(*CommercialAuthorityBinding)
	}{
		{"run", func(b *CommercialAuthorityBinding) { b.SourceRunID = "other-run" }},
		{"snapshot", func(b *CommercialAuthorityBinding) { b.SnapshotHash = "other-snap" }},
		{"membership", func(b *CommercialAuthorityBinding) { b.MembershipHash = strings.Repeat("b", 64) }},
		{"semantic", func(b *CommercialAuthorityBinding) { b.PublicationSemanticHash = strings.Repeat("z", 64) }},
		{"producer", func(b *CommercialAuthorityBinding) { b.ProducerIdentity = strings.Repeat("q", 64) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := v2Binding()
			tc.bind(&b)
			if got := EvaluateCommercialAuthorityV2(payload, b); got.State == CommercialQualified {
				t.Fatalf("binding drift accepted: %+v", got)
			}
		})
	}
}

// 20. Source FRESH never mints commercial authority.
func TestSourceFreshnessNeverGrantsCommercialAuthority(t *testing.T) {
	if got := EvaluateCommercialAuthorityV2(nil, v2Binding()); got.State != CommercialUnknown || got.Present {
		t.Fatalf("absent authority was not fail-closed: %+v", got)
	}
	if got := EvaluateCommercialAuthorityV2(nil, v2Binding()); !containsStr(got.ReasonCodes, ReasonQualificationMissing) {
		t.Fatalf("absence not reported as commercial_authority_missing: %+v", got.ReasonCodes)
	}
}

// An unrecognised policy version is fail-closed, never assumed to be v2.
func TestUnknownPolicyVersionFailsClosed(t *testing.T) {
	roots := []RootQualification{testRootQualification("11222333", time.Now().UTC().AddDate(-1, 0, 0))}
	payload := v2Payload(roots)
	payload.PolicyVersion = "COMMERCIAL_AUTHORITY_POLICY/9.9"
	payload.Schema = "COMMERCIAL_AUTHORITY/9.9"
	payload.ContractVersion = "COMMERCIAL_AUTHORITY/9.9"
	if got := EvaluateCommercialAuthorityV2(payload, v2Binding()); got.State == CommercialQualified {
		t.Fatalf("an unknown policy version was honoured: %+v", got)
	}
}

// A disagreeing window size is refused rather than silently reinterpreted.
func TestDeclaredWindowMustMatchTheCanonicalRule(t *testing.T) {
	roots := []RootQualification{testRootQualification("11222333", time.Now().UTC().AddDate(-1, 0, 0))}
	payload := v2Payload(roots)
	payload.QualificationWindowYears = 5
	if got := EvaluateCommercialAuthorityV2(payload, v2Binding()); got.State == CommercialQualified {
		t.Fatalf("a five-year window was accepted: %+v", got)
	}
}

// The corpus hash is order-independent and change-sensitive.
func TestQualificationCorpusHashIsStableAndSensitive(t *testing.T) {
	now := time.Now().UTC()
	a := testRootQualification("11222333", now.AddDate(-1, 0, 0))
	b := testRootQualification("99888777", now.AddDate(-2, 0, 0))
	if HashQualificationCorpus([]RootQualification{a, b}) != HashQualificationCorpus([]RootQualification{b, a}) {
		t.Fatal("corpus hash depends on member order")
	}
	c := testRootQualification("44555666", now.AddDate(-1, 0, 0))
	if HashQualificationCorpus([]RootQualification{a, b}) == HashQualificationCorpus([]RootQualification{a, b, c}) {
		t.Fatal("adding a member did not change the corpus hash")
	}
}
