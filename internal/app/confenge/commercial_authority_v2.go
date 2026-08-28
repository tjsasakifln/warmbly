package confenge

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"
)

// COMMERCIAL_AUTHORITY/2.0 replaces the age-banded V1 contract. A CONFENGE lead
// is qualified by public evidence that it was the CONTRACTED SUPPLIER on an
// engineering work or service inside a rolling three-year window. Crawler,
// feed, snapshot and publication age answer a different question and never
// revoke that fact.
const (
	CommercialAuthorityContractV2 = "COMMERCIAL_AUTHORITY/2.0"
	CommercialAuthorityPolicyV2   = "COMMERCIAL_AUTHORITY_POLICY/2.0"

	// QualificationWindowYears is the rolling window of the canonical rule.
	QualificationWindowYears = 3

	// V2 commercial states describe commercial reality, never source age.
	CommercialQualified = "QUALIFIED"
	CommercialExpired   = "EXPIRED"
	CommercialRevoked   = "REVOKED"
	CommercialUnknown   = "UNKNOWN"

	// PartyRoleSupplier is the only role that qualifies. A contracting body is
	// never a CONFENGE lead.
	PartyRoleSupplier = "SUPPLIER"

	// Qualifying-date precedence over the canonical contracts view. The
	// contracting act, never the crawler observation.
	QualifyingDateFieldAssinatura      = "data_assinatura"
	QualifyingDateFieldInicio          = "data_inicio"
	QualifyingDateFieldPublicacao      = "data_publicacao"
	QualifyingDateFieldPublicacaoFonte = "data_publicacao_fonte"

	ReasonQualified                  = "COMMERCIAL_QUALIFIED"
	ReasonQualificationExpired       = "commercial_qualification_expired"
	ReasonQualificationRevoked       = "commercial_qualification_revoked"
	ReasonQualificationMissing       = "commercial_authority_missing"
	ReasonQualificationEvidenceDrift = "commercial_qualification_evidence_drift"
	ReasonQualificationRoleInvalid   = "commercial_qualification_party_role_invalid"
	ReasonQualificationWindowInvalid = "commercial_qualification_window_invalid"
	ReasonPolicyVersionUnsupported   = "commercial_authority_policy_unsupported"
)

var errQualifyingDateMissing = errors.New("qualifying contract date missing")

// QualifyingDatePrecedence is the deterministic order used to represent the
// contracting act. data_fim is deliberately excluded: it is an execution-end
// estimate, frequently null, and would make the window non-deterministic.
var QualifyingDatePrecedence = []string{
	QualifyingDateFieldAssinatura,
	QualifyingDateFieldInicio,
	QualifyingDateFieldPublicacao,
	QualifyingDateFieldPublicacaoFonte,
}

// FeedCommercialAuthorityV2 is the population-level attestation emitted by
// extra-cli. It binds the publication identity and the qualification corpus.
// It carries no TTL: nothing here expires because time passed.
type FeedCommercialAuthorityV2 struct {
	Schema          string `json:"schema,omitempty"`
	ContractVersion string `json:"contract_version,omitempty"`
	PolicyVersion   string `json:"policy_version,omitempty"`

	BasisSourceRunID             string `json:"basis_source_run_id,omitempty"`
	BasisSnapshotHash            string `json:"basis_snapshot_hash,omitempty"`
	BasisMembershipHash          string `json:"basis_membership_hash,omitempty"`
	BasisPublicationSemanticHash string `json:"basis_publication_semantic_hash,omitempty"`
	ProducerIdentity             string `json:"producer_identity,omitempty"`

	QualificationWindowYears  int    `json:"qualification_window_years,omitempty"`
	QualificationEvidenceHash string `json:"qualification_evidence_hash,omitempty"`
	QualifiedRootCount        int    `json:"qualified_root_count,omitempty"`
	// EvaluatedAt is provenance only. It is never an expiry clock.
	EvaluatedAt string   `json:"evaluated_at,omitempty"`
	State       string   `json:"state,omitempty"`
	ReasonCodes []string `json:"reason_codes,omitempty"`
}

// RootQualification is the per-CNPJ-root qualifying fact. Every field is the
// evidence itself, so the decision is reproducible without the producer.
type RootQualification struct {
	CNPJRoot8               string `json:"cnpj_root8"`
	TargetFitClass          string `json:"target_fit_class,omitempty"`
	PartyRole               string `json:"party_role,omitempty"`
	QualifyingContractID    string `json:"qualifying_contract_id,omitempty"`
	QualifyingContractDate  string `json:"qualifying_contract_date,omitempty"`
	QualifyingDateField     string `json:"qualifying_date_field,omitempty"`
	QualifyingContractCount int    `json:"qualifying_contract_count,omitempty"`
	QualifiedUntil          string `json:"qualified_until,omitempty"`
	EvidenceHash            string `json:"qualification_evidence_hash,omitempty"`
	EvidenceReference       string `json:"qualification_evidence_reference,omitempty"`
	Provenance              string `json:"provenance,omitempty"`
	Deactivated             bool   `json:"deactivated,omitempty"`
	DeactivationReason      string `json:"deactivation_reason,omitempty"`
}

// CommercialQualificationDecision is what every downstream gate consumes.
type CommercialQualificationDecision struct {
	Present                bool       `json:"present"`
	State                  string     `json:"state"`
	PolicyVersion          string     `json:"policy_version,omitempty"`
	CNPJRoot8              string     `json:"cnpj_root8,omitempty"`
	QualifyingContractID   string     `json:"qualifying_contract_id,omitempty"`
	QualifyingContractDate *time.Time `json:"qualifying_contract_date,omitempty"`
	QualifyingDateField    string     `json:"qualifying_date_field,omitempty"`
	QualifiedUntil         *time.Time `json:"qualified_until,omitempty"`
	EvidenceHash           string     `json:"qualification_evidence_hash,omitempty"`
	ReasonCodes            []string   `json:"reason_codes,omitempty"`
}

// AllowsTransport is the only question the send path asks of qualification.
func (d CommercialQualificationDecision) AllowsTransport() bool {
	return d.Present && d.State == CommercialQualified
}

// AllowsNewAdmission is deliberately identical to AllowsTransport. A proven
// company may always be worked; nothing about producer age narrows it.
func (d CommercialQualificationDecision) AllowsNewAdmission() bool {
	return d.AllowsTransport()
}

// QualifiedUntilFor derives the natural expiry of one qualifying fact: the
// contracting date plus the rolling window. No grace period is added.
func QualifiedUntilFor(contractDate time.Time) time.Time {
	return contractDate.UTC().AddDate(QualificationWindowYears, 0, 0)
}

// WithinQualificationWindow is the canonical rule, evaluated against now.
func WithinQualificationWindow(contractDate, now time.Time) bool {
	if contractDate.IsZero() {
		return false
	}
	return now.UTC().Before(QualifiedUntilFor(contractDate))
}

// HashRootQualification binds every material qualification byte. Any mutation
// of role, contract identity, date or window fails closed downstream.
//
// Dates are normalized to YYYY-MM-DD before hashing. The columns behind this
// fact are DATE, so a producer that legitimately sent RFC3339 would otherwise
// sign one string, round-trip through Postgres as another, and fail closed on
// every read as if it had been tampered with.
func HashRootQualification(q RootQualification) string {
	parts := []string{
		strings.TrimSpace(q.CNPJRoot8),
		strings.ToUpper(strings.TrimSpace(q.PartyRole)),
		strings.TrimSpace(q.QualifyingContractID),
		normalizeQualifyingDate(q.QualifyingContractDate),
		strings.TrimSpace(q.QualifyingDateField),
		normalizeQualifyingDate(q.QualifiedUntil),
		strings.TrimSpace(q.EvidenceReference),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// normalizeQualifyingDate renders any accepted date form as the canonical
// YYYY-MM-DD. An unparseable value is hashed verbatim so it still fails closed.
func normalizeQualifyingDate(value string) string {
	parsed, err := parseQualifyingDate(value)
	if err != nil {
		return strings.TrimSpace(value)
	}
	return parsed.Format("2006-01-02")
}

// HashQualificationCorpus is the population-level evidence hash. Sorted by
// root so the same membership always produces the same digest.
func HashQualificationCorpus(roots []RootQualification) string {
	digests := make([]string, 0, len(roots))
	for i := range roots {
		digests = append(digests, HashRootQualification(roots[i]))
	}
	sort.Strings(digests)
	sum := sha256.Sum256([]byte(strings.Join(digests, "\n")))
	return hex.EncodeToString(sum[:])
}

func authorityV2Present(p *FeedCommercialAuthorityV2) bool {
	if p == nil {
		return false
	}
	return strings.TrimSpace(p.Schema) != "" ||
		strings.TrimSpace(p.ContractVersion) != "" ||
		strings.TrimSpace(p.PolicyVersion) != "" ||
		strings.TrimSpace(p.QualificationEvidenceHash) != "" ||
		strings.TrimSpace(p.BasisMembershipHash) != ""
}

// RecognizeCommercialAuthorityPolicy accepts only the versions this runtime
// actually implements. An unknown policy is fail-closed, never "probably v2".
func RecognizeCommercialAuthorityPolicy(version string) bool {
	switch strings.TrimSpace(version) {
	case CommercialAuthorityPolicyV2, CommercialAuthorityContractV2:
		return true
	default:
		return false
	}
}

// EvaluateCommercialAuthorityV2 validates the population-level attestation
// against the live publication binding. It is a pure function of evidence and
// identity. now is used only to evaluate the rolling window, never to age the
// attestation itself.
func EvaluateCommercialAuthorityV2(payload *FeedCommercialAuthorityV2, binding CommercialAuthorityBinding) CommercialQualificationDecision {
	out := CommercialQualificationDecision{State: CommercialUnknown}
	if !authorityV2Present(payload) {
		out.ReasonCodes = []string{ReasonQualificationMissing}
		return out
	}
	out.Present = true
	out.PolicyVersion = firstNonEmpty(strings.TrimSpace(payload.PolicyVersion), strings.TrimSpace(payload.ContractVersion), strings.TrimSpace(payload.Schema))
	if !RecognizeCommercialAuthorityPolicy(payload.PolicyVersion) && !RecognizeCommercialAuthorityPolicy(payload.Schema) {
		out.ReasonCodes = []string{ReasonPolicyVersionUnsupported}
		return out
	}
	if payload.QualificationWindowYears != 0 && payload.QualificationWindowYears != QualificationWindowYears {
		out.ReasonCodes = []string{ReasonQualificationWindowInvalid}
		return out
	}
	bindRun := strings.TrimSpace(binding.SourceRunID)
	bindSnap := strings.TrimSpace(binding.SnapshotHash)
	bindMem := strings.ToLower(strings.TrimSpace(binding.MembershipHash))
	bindSem := strings.ToLower(strings.TrimSpace(binding.PublicationSemanticHash))
	bindProd := strings.ToLower(strings.TrimSpace(binding.ProducerIdentity))
	gotRun := strings.TrimSpace(payload.BasisSourceRunID)
	gotSnap := strings.TrimSpace(payload.BasisSnapshotHash)
	gotMem := strings.ToLower(strings.TrimSpace(payload.BasisMembershipHash))
	gotSem := strings.ToLower(strings.TrimSpace(payload.BasisPublicationSemanticHash))
	gotProd := strings.ToLower(strings.TrimSpace(payload.ProducerIdentity))
	if gotRun == "" || gotSnap == "" || gotMem == "" || gotSem == "" || gotProd == "" ||
		bindRun == "" || bindSnap == "" || bindMem == "" || bindSem == "" || bindProd == "" ||
		gotRun != bindRun || gotSnap != bindSnap || gotMem != bindMem || gotSem != bindSem || gotProd != bindProd {
		out.ReasonCodes = []string{ReasonAuthorityBindingMismatch}
		return out
	}
	if !validSHA256(payload.QualificationEvidenceHash) {
		out.ReasonCodes = []string{ReasonQualificationEvidenceDrift}
		return out
	}
	out.EvidenceHash = strings.ToLower(strings.TrimSpace(payload.QualificationEvidenceHash))
	if explicitRevocation(payload.ReasonCodes) || strings.EqualFold(strings.TrimSpace(payload.State), CommercialRevoked) {
		out.State = CommercialRevoked
		out.ReasonCodes = []string{ReasonQualificationRevoked}
		return out
	}
	out.State = CommercialQualified
	out.ReasonCodes = []string{ReasonQualified}
	return out
}

// EvaluateRootQualification decides one company. The population attestation
// proves the corpus; this proves the individual fact inside the window.
func EvaluateRootQualification(q *RootQualification, now time.Time) CommercialQualificationDecision {
	out := CommercialQualificationDecision{State: CommercialUnknown}
	if q == nil {
		out.ReasonCodes = []string{ReasonQualificationMissing}
		return out
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	out.Present = true
	out.CNPJRoot8 = strings.TrimSpace(q.CNPJRoot8)
	out.QualifyingContractID = strings.TrimSpace(q.QualifyingContractID)
	out.QualifyingDateField = strings.TrimSpace(q.QualifyingDateField)
	out.EvidenceHash = strings.ToLower(strings.TrimSpace(q.EvidenceHash))

	if !strings.EqualFold(strings.TrimSpace(q.PartyRole), PartyRoleSupplier) {
		out.ReasonCodes = []string{ReasonQualificationRoleInvalid}
		return out
	}
	if q.Deactivated {
		out.State = CommercialRevoked
		out.ReasonCodes = []string{ReasonQualificationRevoked}
		return out
	}
	observed := HashRootQualification(*q)
	if out.EvidenceHash == "" || out.EvidenceHash != observed {
		out.ReasonCodes = []string{ReasonQualificationEvidenceDrift}
		return out
	}
	contractDate, err := parseQualifyingDate(q.QualifyingContractDate)
	if err != nil {
		out.ReasonCodes = []string{ReasonQualificationEvidenceDrift}
		return out
	}
	out.QualifyingContractDate = &contractDate
	if !containsStr(QualifyingDatePrecedence, out.QualifyingDateField) {
		out.ReasonCodes = []string{ReasonQualificationEvidenceDrift}
		return out
	}
	// qualified_until must be exactly the contracting date plus the window.
	// A producer cannot buy extra runway by writing a later date.
	expected := QualifiedUntilFor(contractDate)
	declared, err := parseQualifyingDate(q.QualifiedUntil)
	if err != nil || !declared.Equal(expected) {
		out.ReasonCodes = []string{ReasonQualificationWindowInvalid}
		return out
	}
	out.QualifiedUntil = &expected
	if !now.Before(expected) {
		out.State = CommercialExpired
		out.ReasonCodes = []string{ReasonQualificationExpired}
		return out
	}
	out.State = CommercialQualified
	out.ReasonCodes = []string{ReasonQualified}
	return out
}

func parseQualifyingDate(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errQualifyingDateMissing
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}
