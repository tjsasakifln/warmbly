package confenge

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func validFeedContractorRole(now time.Time) FeedContractorRole {
	hash := strings.Repeat("a", 64)
	return FeedContractorRole{
		Status: ContractorRoleConfirmed, TargetPartyRole: "SUPPLIER", PolicyVersion: DelegatedFirstTouchEvidenceV1,
		Source: "extra-cli:v_contracts_canonical_v2", SourceRunID: "run-1", ObservedAt: now.UTC().Format(time.RFC3339),
		EvidenceHash: hash, EvidenceReference: "extra-cli:v_contracts_canonical_v2:sha256:" + hash,
		EvidenceIDs: []string{"contract-1"}, ReasonCodes: []string{"lead_matches_supplier", "lead_differs_from_buyer"},
		SupplierCNPJ14: "11222333000144", SupplierIdentityRef: "cnpj:11222333000144",
		BuyerCNPJ14: "99888777000166", BuyerIdentityRef: "cnpj:99888777000166",
		RoleMatchMethod: "SUPPLIER_EXACT_CNPJ14", Confidence: "HIGH",
	}
}

func TestContractorRoleProjectionPersistsWithoutRawPNCPReinterpretation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	role := validFeedContractorRole(now)
	lead := FeedLead{
		SourceLeadID: "lead-1", Company: FeedCompany{CNPJ14: "11222333000144", CNPJRoot: "11222333", RazaoSocial: "Empresa Alfa"},
		ContractorRole: role,
	}
	if err := ValidateLead(0, lead); err != nil {
		t.Fatalf("valid typed role rejected: %v", err)
	}
	feed := &Feed{Source: FeedSource{System: "extra-cli", RunID: "run-1"}}
	acc := leadToAccount(uuid.New(), lead, feed, uuid.New(), LeadContentHash(lead), "NEEDS_REVIEW", nil)
	if acc.ContractorRoleStatus != ContractorRoleConfirmed || acc.TargetPartyRole != "SUPPLIER" ||
		acc.SupplierIdentityRef != role.SupplierIdentityRef || acc.BuyerIdentityRef != role.BuyerIdentityRef ||
		acc.ContractorRoleEvidenceHash != role.EvidenceHash || acc.ContractorRoleSourceRunID != "run-1" {
		t.Fatalf("typed role projection lost fields: %+v", acc)
	}
}

func TestContractorRoleChangesMessageAndLeadBindings(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	base := FeedLead{SourceLeadID: "lead-1", Company: FeedCompany{CNPJ14: "11222333000144", RazaoSocial: "Empresa Alfa"}, ContractorRole: validFeedContractorRole(now)}
	leadHash, contextHash := LeadContentHash(base), MessageContextHash(base)
	drift := base
	drift.ContractorRole = base.ContractorRole
	drift.ContractorRole.Status = ContractorRoleConflict
	drift.ContractorRole.TargetPartyRole = "BUYER_CONFLICT"
	if LeadContentHash(drift) == leadHash || MessageContextHash(drift) == contextHash {
		t.Fatal("party-role drift did not invalidate lead/context binding")
	}
	drift = base
	drift.ContractorRole = base.ContractorRole
	drift.ContractorRole.SourceRunID = "run-2"
	if MessageContextHash(drift) == contextHash {
		t.Fatal("source-run drift did not invalidate context binding")
	}
}

func TestContractorRoleRunMustMatchFeedRun(t *testing.T) {
	role := validFeedContractorRole(time.Now().UTC().Truncate(time.Second))
	if got := validateFeedContractorRoleRun("run-1", role); got != "" {
		t.Fatalf("current typed role run rejected: %s", got)
	}
	if got := validateFeedContractorRoleRun("run-2", role); got == "" {
		t.Fatal("stale typed role run passed feed import")
	}
}

func TestContractorRoleFeedContractFailsClosed(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	base := FeedLead{SourceLeadID: "lead-1", Company: FeedCompany{CNPJ14: "11222333000144", RazaoSocial: "Empresa Alfa"}, ContractorRole: validFeedContractorRole(now)}
	tests := []struct {
		name   string
		mutate func(*FeedContractorRole)
	}{
		{"buyer_inversion", func(r *FeedContractorRole) {
			r.BuyerCNPJ14 = "11222333000144"
			r.BuyerIdentityRef = "cnpj:11222333000144"
		}},
		{"unknown_semantics", func(r *FeedContractorRole) { r.Status = ContractorRoleUnknown; r.TargetPartyRole = "SUPPLIER" }},
		{"missing_evidence", func(r *FeedContractorRole) { r.EvidenceIDs = nil }},
		{"wrong_authority", func(r *FeedContractorRole) { r.Source = "raw-pncp-local-guess" }},
		{"role_method", func(r *FeedContractorRole) { r.RoleMatchMethod = "PRESENCE_ONLY" }},
		{"root_only_supplier", func(r *FeedContractorRole) {
			r.SupplierCNPJ14 = "11222333000225"
			r.SupplierIdentityRef = "cnpj:11222333000225"
			r.RoleMatchMethod = "SUPPLIER_CNPJ_ROOT"
			r.Confidence = "MEDIUM"
		}},
		{"semantic_reason_missing", func(r *FeedContractorRole) { r.ReasonCodes = []string{"lead_matches_supplier"} }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lead := base
			lead.ContractorRole = base.ContractorRole
			tc.mutate(&lead.ContractorRole)
			if err := ValidateLead(0, lead); err == nil {
				t.Fatal("invalid typed party role was imported")
			}
		})
	}
}
