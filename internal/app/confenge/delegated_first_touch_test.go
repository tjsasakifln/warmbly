package confenge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

type delegatedTestOrgRisk struct{ suspended bool }

func (r delegatedTestOrgRisk) SendingSuspended(context.Context, uuid.UUID) bool { return r.suspended }

func TestDelegatedPolicyAuthorizationRequiresConfiguredFounderActor(t *testing.T) {
	orgID, founderID := uuid.New(), uuid.New()
	svc := NewService(Config{
		Enabled: true, DelegatedFirstTouchEnabled: true,
		OperatorOrgID: orgID, OperatorUserID: founderID,
	}, newMemRepo(), nil).(*service)
	svc.WirePolicyAuth(newMemPolicyStore())
	build := func() *models.CampaignPolicyAuthorization {
		return &models.CampaignPolicyAuthorization{
			CampaignID: uuid.New(), PromptPolicyVersion: DelegatedFirstTouchPolicyV1,
			ValidatorVersion: DelegatedFirstTouchValidatorV1, ContactPolicyVersion: DelegatedFirstTouchContactPolicyV1,
			TemplatePolicyVersion: DelegatedFirstTouchTemplateV1, AllowPolicyTemplateGREEN: true,
			SenderMailbox: "tiago@confenge.com.br", Channel: models.OutreachChannelEmail,
			AllowedRiskClass: "GREEN", MaxRatePerHour: 10, EffectiveAt: time.Now().UTC(),
			AuthorizedByLabel: DelegatedFirstTouchAuthority,
		}
	}
	if _, xerr := svc.AuthorizeCampaignPolicy(context.Background(), orgID, uuid.New(), build()); xerr == nil {
		t.Fatal("unauthorized actor minted delegated founder policy")
	}
	if _, xerr := svc.AuthorizeCampaignPolicy(context.Background(), uuid.New(), founderID, build()); xerr == nil {
		t.Fatal("founder actor minted delegated policy for an unauthorized organization")
	}
	auth, xerr := svc.AuthorizeCampaignPolicy(context.Background(), orgID, founderID, build())
	if xerr != nil || auth == nil || auth.AuthorizedBy != founderID {
		t.Fatalf("configured founder authority was not persisted: auth=%+v err=%v", auth, xerr)
	}
}

func TestDelegatedFirstTouchUnknownAndV2PolicyHold(t *testing.T) {
	now := time.Now().UTC()
	base := DelegatedFirstTouchManifest{
		SchemaVersion: DelegatedFirstTouchManifestV1, BatchID: "batch-1", AgentID: "agent:test",
		PolicyHash: DelegatedFirstTouchPolicyHashV1, AuthorityReference: DelegatedFirstTouchAuthorityRef,
		PolicyAuthorizationID: uuid.New(), SourceRunID: "run-1", SourceSnapshotHash: strings.Repeat("b", 64),
		EvidenceVersion: DelegatedFirstTouchEvidenceV1, ComposerVersion: ComposerVersion,
		TemplateVersion: DelegatedFirstTouchTemplateV1, PromptVersion: PromptVersion,
		GeneratedAt: now, Entries: []DelegatedFirstTouchEntry{{IdempotencyKey: "k"}},
	}
	v1 := base
	v1.PolicyVersion = DelegatedFirstTouchPolicyV1
	if got := validateDelegatedManifestHeader(v1); delegatedTestContains(got, ReasonPolicyUnknown) || delegatedTestContains(got, ReasonPolicyHold) {
		t.Fatalf("v1 held: %v", got)
	}
	v2 := base
	v2.PolicyVersion = DelegatedFirstTouchPolicyV2
	v2.PolicyHash = DelegatedFirstTouchPolicyHashV2
	if got := validateDelegatedManifestHeader(v2); delegatedTestContains(got, ReasonPolicyHold) || delegatedTestContains(got, ReasonPolicyUnknown) || delegatedTestContains(got, "policy_hash_mismatch") {
		t.Fatalf("v2 held: %v", got)
	}
	fuzzy := base
	fuzzy.PolicyVersion = "CFG-FIRST-TOUCH-ROUTING-v1-beta"
	if got := validateDelegatedManifestHeader(fuzzy); !delegatedTestContains(got, ReasonPolicyUnknown) {
		t.Fatalf("fuzzy name accepted: %v", got)
	}
}

func TestDelegatedOrganizationRiskFailsClosed(t *testing.T) {
	svc := &service{orgRisk: delegatedTestOrgRisk{suspended: true}}
	if svc.orgRisk == nil || !svc.orgRisk.SendingSuspended(context.Background(), uuid.New()) {
		t.Fatal("suspended organization risk did not close delegated approval")
	}
}

func TestDelegatedDraftIdentityIsStableWithinRunAndDistinctAcrossRuns(t *testing.T) {
	orgID, accountID := uuid.New(), uuid.New()
	first := delegatedFirstTouchDraftID(orgID, "source-run-1", accountID)
	if replay := delegatedFirstTouchDraftID(orgID, "source-run-1", accountID); replay != first {
		t.Fatalf("same source run produced a different draft id: %s != %s", replay, first)
	}
	if refreshed := delegatedFirstTouchDraftID(orgID, "source-run-2", accountID); refreshed == first {
		t.Fatal("source refresh reused the previous draft id")
	}
}

func TestDelegatedRunwayIdempotencyPreservesPublicationBindingAndAddsCandidate(t *testing.T) {
	accountID, oldCandidateID, safeCandidateID := uuid.New(), uuid.New(), uuid.New()
	old := DelegatedFirstTouchEntry{
		IdempotencyKey: "delegated-first-touch:runway-v1:old-binding:" + accountID.String(),
		AccountID:      accountID, ContactCandidateID: oldCandidateID,
	}
	safe := old
	safe.ContactCandidateID = safeCandidateID
	oldKey := delegatedFirstTouchEffectiveIdempotencyKey(old)
	safeKey := delegatedFirstTouchEffectiveIdempotencyKey(safe)
	if oldKey == safeKey {
		t.Fatalf("old HOLD would still block safe replacement candidate: %q", oldKey)
	}
	if !strings.HasPrefix(oldKey, old.IdempotencyKey+":candidate:") || !strings.Contains(oldKey, oldCandidateID.String()) {
		t.Fatalf("runway key must preserve publication binding and identify candidate: %q", oldKey)
	}
}

func TestDelegatedRunwayPreparesExactlyTheClaimedTouchpoint(t *testing.T) {
	f := newDelegatedValidationFixture(t, RouteClassGenericCompany, "contato@empresa.example")
	selectedID := uuid.New()
	for i, state := range []string{models.TouchpointCancelled, models.TouchpointCancelled, models.TouchpointDue} {
		id := uuid.New()
		if i == 2 {
			id = selectedID
		}
		sourceRunID := f.manifest.SourceRunID
		if i == 2 {
			sourceRunID = "committed-carryover-run"
		}
		f.repo.touchpoints[id] = &models.OutreachTouchpoint{
			ID: id, OrganizationID: f.orgID, AccountID: f.account.ID,
			ContactCandidateID: &f.candidate.ID, Ordinal: 1, CadenceStep: "INITIAL",
			Channel: models.OutreachChannelEmail, Purpose: models.TouchpointPurposeInitial,
			DueAt: time.Now().UTC(), State: state, SourceRunID: sourceRunID,
		}
	}
	entry := f.entry
	entry.IdempotencyKey = delegatedFirstTouchIdempotencyPrefix + "runway-v1:binding:" + f.account.ID.String()
	entry.CorrelationID = "touchpoint:" + selectedID.String()
	tp, _, err := f.service.prepareDelegatedTouchpoint(context.Background(), f.orgID, f.account, &f.candidate, f.manifest, entry)
	if err != nil {
		t.Fatal(err)
	}
	if tp == nil || tp.ID != selectedID || tp.State != models.TouchpointNeedsReview {
		t.Fatalf("prepared wrong touchpoint: %+v", tp)
	}
	for id, stored := range f.repo.touchpoints {
		if id != selectedID && stored.State != models.TouchpointCancelled {
			t.Fatalf("terminal sibling %s was mutated to %s", id, stored.State)
		}
	}
}

func TestDelegatedPolicyBindsTemplateAndExplicitGreenTemplateAuthority(t *testing.T) {
	now := time.Now().UTC()
	orgID, founderID := uuid.New(), uuid.New()
	auth := &models.CampaignPolicyAuthorization{
		ID: uuid.New(), CampaignID: uuid.New(), PromptPolicyVersion: DelegatedFirstTouchPolicyV1,
		ValidatorVersion: DelegatedFirstTouchValidatorV1, ContactPolicyVersion: DelegatedFirstTouchContactPolicyV1,
		TemplatePolicyVersion: DelegatedFirstTouchTemplateV1, AllowPolicyTemplateGREEN: true,
		SenderMailbox: "sender@example.test", Channel: models.OutreachChannelEmail, AllowedRiskClass: "GREEN",
		MaxRatePerHour: 10, EffectiveAt: now.Add(-time.Minute), AuthorizedBy: founderID, AuthorizedByLabel: DelegatedFirstTouchAuthority,
	}
	svc := &service{cfg: Config{OperatorOrgID: orgID, OperatorUserID: founderID}}
	manifest := DelegatedFirstTouchManifest{PolicyAuthorizationID: auth.ID}
	if blockers := validateDelegatedPolicy(auth, manifest, now); len(blockers) != 0 {
		t.Fatalf("complete delegated grant blocked: %v", blockers)
	}
	if blockers := svc.validateDelegatedFounderBinding(orgID, auth); len(blockers) != 0 {
		t.Fatalf("exact founder binding blocked: %v", blockers)
	}
	forged := *auth
	forged.AuthorizedBy = uuid.New()
	if blockers := svc.validateDelegatedFounderBinding(orgID, &forged); !delegatedTestContains(blockers, "founder_authority_binding_mismatch") {
		t.Fatalf("forged founder binding passed: %v", blockers)
	}
	auth.TemplatePolicyVersion = ""
	if blockers := validateDelegatedPolicy(auth, manifest, now); !delegatedTestContains(blockers, "delegated_policy_contract_mismatch") {
		t.Fatalf("unbound template grant passed: %v", blockers)
	}
}

func TestDelegatedFirstTouchRejectsContractingAuthorityInversion(t *testing.T) {
	entry := DelegatedFirstTouchEntry{
		CNPJ14:               "11222333000144",
		SupplierCNPJ14:       "99888777000166",
		BuyerCNPJ14:          "11222333000144",
		ContractorRoleStatus: ContractorRoleConfirmed,
	}
	got := validateDelegatedPartyRole(entry)
	if !delegatedTestContains(got, "lead_supplier_identity_mismatch") || !delegatedTestContains(got, "party_role_inversion") {
		t.Fatalf("contracting authority inversion passed: %v", got)
	}
}

func TestDelegatedFirstTouchNeverPromotesUnknownRole(t *testing.T) {
	entry := DelegatedFirstTouchEntry{
		CNPJ14:               "11222333000144",
		SupplierCNPJ14:       "11222333000144",
		BuyerCNPJ14:          "99888777000166",
		ContractorRoleStatus: ContractorRoleUnknown,
	}
	if got := validateDelegatedPartyRole(entry); !delegatedTestContains(got, "contractor_role_not_confirmed") {
		t.Fatalf("UNKNOWN role passed: %v", got)
	}
}

func TestDelegatedFirstTouchRejectsSupplierRootWithoutBranchBinding(t *testing.T) {
	entry := DelegatedFirstTouchEntry{
		CNPJ14:                    "11222333000225",
		SupplierCNPJ14:            "11222333000144",
		BuyerCNPJ14:               "99888777000166",
		ContractorRoleStatus:      ContractorRoleConfirmed,
		TargetPartyRole:           "SUPPLIER",
		SupplierIdentityRef:       "cnpj:11222333000144",
		BuyerIdentityRef:          "cnpj:99888777000166",
		RoleMatchMethod:           "SUPPLIER_CNPJ_ROOT",
		RoleConfidence:            "MEDIUM",
		ContractEvidenceReference: "extra-cli:v_contracts_canonical_v2:sha256:" + strings.Repeat("a", 64),
		ContractRoleReasonCodes:   []string{"lead_matches_supplier", "lead_differs_from_buyer"},
	}
	if got := validateDelegatedPartyRole(entry); !delegatedTestContains(got, "supplier_branch_binding_unproven") {
		t.Fatalf("supplier root match passed without branch binding: %v", got)
	}
}

func TestSealDelegatedFirstTouchEntryBindsOperationalCandidate(t *testing.T) {
	accountID := uuid.New()
	candidateID := uuid.New()
	entry := DelegatedFirstTouchEntry{
		AccountID:          accountID,
		ContactCandidateID: candidateID,
		Subject:            "Quem cuida dos contratos públicos na Empresa?",
		BodyText:           "Olá, tudo bem? Texto de roteamento.",
	}
	cand := &models.OutreachContactCandidate{
		ID:            candidateID,
		AccountID:     accountID,
		Email:         " Contato@Empresa.Example ",
		DiscoveryJSON: []byte(`{"route_class":"GENERIC_COMPANY"}`),
	}
	sealed, err := SealDelegatedFirstTouchEntry(entry, cand)
	if err != nil {
		t.Fatal(err)
	}
	if sealed.Recipient != "contato@empresa.example" || sealed.RouteClass != RouteClassGenericCompany {
		t.Fatalf("unexpected sealed route: recipient=%q route=%q", sealed.Recipient, sealed.RouteClass)
	}
	if sealed.SubjectHash != hashText(entry.Subject) || sealed.BodyHash != hashText(entry.BodyText) {
		t.Fatal("sealed content hashes do not match exact copy")
	}
}

func TestSealDelegatedFirstTouchEntryRejectsPrepopulatedMailbox(t *testing.T) {
	accountID := uuid.New()
	candidateID := uuid.New()
	entry := DelegatedFirstTouchEntry{
		AccountID:          accountID,
		ContactCandidateID: candidateID,
		Recipient:          "inventado@empresa.example",
	}
	cand := &models.OutreachContactCandidate{ID: candidateID, AccountID: accountID, Email: "contato@empresa.example"}
	if _, err := SealDelegatedFirstTouchEntry(entry, cand); err == nil {
		t.Fatal("prepopulated mailbox was not rejected")
	}
}

func TestCampaignPolicyStructuralTransportKeepsHumanActorEmpty(t *testing.T) {
	t.Setenv(EnvKillSwitchPath, filepath.Join(t.TempDir(), "absent"))
	authID := uuid.New()
	now := time.Now().UTC()
	tp := &models.OutreachTouchpoint{
		State:                         models.TouchpointApproved,
		Recipient:                     "licitacoes@empresa.example",
		Subject:                       "Quem cuida dos contratos públicos na Empresa?",
		BodyText:                      "mensagem",
		AuthorizationMode:             AuthorizationModeCampaignPolicy,
		CampaignPolicyAuthorizationID: &authID,
		AuthorizationPolicyHash:       "policy-hash",
		AuthorizationAt:               &now,
	}
	RecomputeContentHash(tp)
	tp.ApprovedContentHash = tp.ContentHash
	if err := CanTransportCampaignPolicy(tp); err != nil {
		t.Fatalf("delegated structural path blocked: %v", err)
	}
	actor := uuid.New()
	tp.ApprovedBy = &actor
	if err := CanTransportCampaignPolicy(tp); err == nil {
		t.Fatal("policy path accepted forged human approved_by")
	}
}

func TestCampaignPolicyStructuralTransportHonorsFileKillSwitch(t *testing.T) {
	killSwitch := filepath.Join(t.TempDir(), "kill-switch")
	t.Setenv(EnvKillSwitchPath, killSwitch)
	if err := os.WriteFile(killSwitch, []byte("paused\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	authID := uuid.New()
	now := time.Now().UTC()
	tp := &models.OutreachTouchpoint{
		State:                         models.TouchpointApproved,
		Recipient:                     "licitacoes@empresa.example",
		Subject:                       "Quem cuida dos contratos públicos na Empresa?",
		BodyText:                      "mensagem",
		AuthorizationMode:             AuthorizationModeCampaignPolicy,
		CampaignPolicyAuthorizationID: &authID,
		AuthorizationPolicyHash:       "policy-hash",
		AuthorizationAt:               &now,
	}
	RecomputeContentHash(tp)
	tp.ApprovedContentHash = tp.ContentHash
	if err := CanTransportCampaignPolicy(tp); err == nil || !strings.Contains(err.Error(), "kill switch") {
		t.Fatalf("file kill switch did not block delegated transport: %v", err)
	}
	if err := CanQueueCampaignPolicy(tp); err != nil {
		t.Fatalf("kill switch must not discard safe queued work: %v", err)
	}
}

func delegatedTestContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
