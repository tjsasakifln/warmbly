package confenge

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/models"
)

type delegatedValidationFixture struct {
	service   *service
	repo      *memRepo
	orgID     uuid.UUID
	manifest  DelegatedFirstTouchManifest
	entry     DelegatedFirstTouchEntry
	account   *models.OutreachAccount
	candidate models.OutreachContactCandidate
	importID  uuid.UUID
}

func newDelegatedValidationFixture(t *testing.T, routeClass, email string) delegatedValidationFixture {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	orgID, accountID, candidateID, importID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	runID := "run-current"
	snapshot := strings.Repeat("b", 64)
	evidenceHash := strings.Repeat("a", 64)
	repo := newMemRepo()
	repo.feedSync = map[uuid.UUID]*models.OutreachFeedSyncState{orgID: {
		OrganizationID: orgID, LastRunID: runID, LastSnapshotHash: snapshot,
		LastStatus: "completed", SourceGeneratedAt: &now,
	}}
	account := &models.OutreachAccount{
		ID: accountID, OrganizationID: orgID, SourceLeadID: "lead-1", CNPJ14: "11222333000144", CNPJRoot: "11222333",
		RazaoSocial: "Empresa Alfa Ltda", NomeFantasia: "Empresa Alfa", ServiceCode: "CONTRACT_MANAGEMENT",
		FactToMention: "Atuação como contratada em contrato público confirmada.",
		QueueState:    models.OutreachQueueNeedsReview, SourceSystem: "extra-cli", SourceRunID: runID,
		LastImportRunID: &importID, MessageContextHash: strings.Repeat("c", 64), EmailSendReady: true,
		TargetFitClass: TargetFitConfirmed, TargetFitVersion: "target-fit.v1", TargetFitSourceWatermark: now.Format(time.RFC3339),
		TargetFitObservedAt: &now, TargetFitFresh: true, TargetFitSendTier: "A_AUTOMATIC", TargetFitEligible: true,
		ContractorRoleStatus: ContractorRoleConfirmed, TargetPartyRole: "SUPPLIER",
		ContractorRolePolicyVersion: DelegatedFirstTouchEvidenceV1,
		ContractorRoleSource:        "extra-cli:v_contracts_canonical_v2", ContractorRoleSourceRunID: runID,
		ContractorRoleObservedAt: &now, ContractorRoleEvidenceHash: evidenceHash,
		ContractorRoleEvidenceReference: "extra-cli:v_contracts_canonical_v2:sha256:" + evidenceHash,
		ContractorRoleEvidenceIDs:       []string{"contract-1"}, SupplierCNPJ14: "11222333000144",
		SupplierIdentityRef: "cnpj:11222333000144", BuyerCNPJ14: "99888777000166",
		BuyerIdentityRef: "cnpj:99888777000166", ContractorRoleMatchMethod: "SUPPLIER_EXACT_CNPJ14",
		ContractorRoleConfidence: "HIGH", ContractorRoleReasonCodes: []string{"lead_matches_supplier", "lead_differs_from_buyer"},
	}
	personUnknown := routeClass != RouteClassDirectPerson
	discovery := []byte(`{"route_class":"` + routeClass + `","controlled_email_eligible":true,"preferred_initial":true,"mailbox_company_evidence":"OBSERVED","person_unknown":` + map[bool]string{true: "true", false: "false"}[personUnknown] + `,"risk_class":"ALLOWED"}`)
	candidate := models.OutreachContactCandidate{
		ID: candidateID, OrganizationID: orgID, AccountID: accountID, SourceContactID: "route-1",
		Name: "Equipe", Role: "Atendimento", Email: email, SourceURL: "https://empresa.example/contato",
		VerificationStatus: models.OutreachVerifyOfficialSource, EmailSendReady: true, OwnershipStatus: "COMPANY_OWNED",
		RecipientCommercialSuitability: "SUITABLE", LastImportRunID: &importID, DiscoveryJSON: discovery,
		ChannelEpistemic: "OBSERVED", RouteFreshness: "FRESH", RouteSuppression: "NONE", EmailDerivation: "OBSERVED",
	}
	repo.accounts[accKey(orgID, account.CNPJ14)] = account
	repo.byID[accountID] = account
	repo.cands[accountID] = []models.OutreachContactCandidate{candidate}
	repo.evidence[accountID] = []models.OutreachEvidence{{
		ID: uuid.New(), OrganizationID: orgID, AccountID: accountID, SourceEvidenceID: "contract-1",
		EpistemicClass: models.OutreachEpistemicConfirmedFact, Reliability: "HIGH", URL: "https://pncp.gov.br/contratos/1",
		Synthesis: "Empresa Alfa figura como contratada.", ConsultedAt: &now, LastImportRunID: &importID,
	}}
	svc := NewService(Config{Enabled: true, FeedMaxAge: 24 * time.Hour, MaxInitialEmailWords: 120}, repo, nil).(*service)
	body := "Olá, equipe da Empresa Alfa. Meu nome é Tiago Sasaki, da CONFENGE. A Empresa Alfa aparece como contratada em contratos públicos confirmados em fonte pública. Trabalhamos com organização técnica de rotinas ligadas à administração pública. Quem é a pessoa responsável por esse tema na empresa? Você poderia indicar o contato correto ou encaminhar esta mensagem, por favor? Obrigado, Tiago Sasaki, confenge.com.br."
	entry := DelegatedFirstTouchEntry{
		IdempotencyKey: "first-touch:lead-1:v1", CorrelationID: "corr-1", AccountID: accountID,
		ContactCandidateID: candidateID, CNPJ14: account.CNPJ14, SupplierCNPJ14: account.SupplierCNPJ14,
		BuyerCNPJ14: account.BuyerCNPJ14, ContractorRoleStatus: ContractorRoleConfirmed, TargetPartyRole: "SUPPLIER",
		ContractRoleSource: account.ContractorRoleSource, ContractEvidenceIDs: account.ContractorRoleEvidenceIDs,
		ContractEvidenceHash: evidenceHash, ContractEvidenceReference: account.ContractorRoleEvidenceReference,
		SupplierIdentityRef: account.SupplierIdentityRef, BuyerIdentityRef: account.BuyerIdentityRef,
		RoleMatchMethod: account.ContractorRoleMatchMethod, RoleConfidence: account.ContractorRoleConfidence,
		ContractRoleReasonCodes: account.ContractorRoleReasonCodes, EvidenceObservedAt: now,
		ReconciliationStatus: ReconciliationCorroborated,
		WebSources:           []DelegatedWebSource{{URL: candidate.SourceURL, Kind: "OFFICIAL_COMPANY_PAGE", Supports: "COMPANY_IDENTITY_AND_MAILBOX", ObservedAt: now}},
		RouteClass:           routeClass, Recipient: email, Subject: "Responsável por contratos públicos na Empresa Alfa", BodyText: body,
		EvidenceIDs: []string{"contract-1"}, QA: DelegatedFirstTouchQA{Result: "PASS", Attempts: 1, IdentityPassed: true,
			FactualPassed: true, CopyPassed: true, OperationalPassed: true, Reviewer: "agent:codex"},
	}
	entry.SubjectHash, entry.BodyHash = hashText(entry.Subject), hashText(entry.BodyText)
	manifest := DelegatedFirstTouchManifest{
		SchemaVersion: DelegatedFirstTouchManifestV1, BatchID: "batch-1", AgentID: "agent:codex",
		PolicyVersion: DelegatedFirstTouchPolicyV1, PolicyHash: DelegatedFirstTouchPolicyHashV1,
		AuthorityReference: DelegatedFirstTouchAuthorityRef, PolicyAuthorizationID: uuid.New(),
		SourceRunID: runID, SourceSnapshotHash: snapshot, EvidenceVersion: DelegatedFirstTouchEvidenceV1,
		ComposerVersion: ComposerVersion, TemplateVersion: DelegatedFirstTouchTemplateV1, PromptVersion: PromptVersion,
		GeneratedAt: now, Entries: []DelegatedFirstTouchEntry{entry},
	}
	return delegatedValidationFixture{service: svc, repo: repo, orgID: orgID, manifest: manifest, entry: entry, account: account, candidate: candidate, importID: importID}
}

func (f delegatedValidationFixture) validate() []string {
	_, _, blockers := f.service.validateDelegatedEntry(context.Background(), f.orgID, f.manifest, f.entry, map[string]bool{}, nil, true)
	return blockers
}

func (f *delegatedValidationFixture) storeCandidate() {
	f.repo.cands[f.account.ID] = []models.OutreachContactCandidate{f.candidate}
}

func TestDelegatedFirstTouchAttributedRouteClassesPassAllDeterministicGates(t *testing.T) {
	for name, route := range map[string]struct{ class, email string }{
		"direct_person":      {RouteClassDirectPerson, "ana@empresa.example"},
		"role_or_department": {RouteClassRoleOrDepartment, "licitacoes@empresa.example"},
		"generic_company":    {RouteClassGenericCompany, "contato@empresa.example"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newDelegatedValidationFixture(t, route.class, route.email)
			if blockers := f.validate(); len(blockers) != 0 {
				t.Fatalf("attributed route blocked: %v", blockers)
			}
		})
	}
}

func TestDelegatedFirstTouchAttributedFreemailPasses(t *testing.T) {
	f := newDelegatedValidationFixture(t, RouteClassPublicCompanyFreemail, "empresa.alfa@gmail.com")
	if blockers := f.validate(); len(blockers) != 0 {
		t.Fatalf("attributed company freemail blocked: %v", blockers)
	}
}

func TestDelegatedFirstTouchControlledInstitutionalRouteDoesNotRequireNamedHumanReadiness(t *testing.T) {
	f := newDelegatedValidationFixture(t, RouteClassGenericCompany, "contato@empresa.example")
	f.candidate.EmailSendReady = false
	f.candidate.VerificationStatus = models.OutreachVerifyInstitutionalGeneric
	f.candidate.RecipientCommercialSuitability = "UNSUITABLE_HUMAN_EVIDENCE"
	f.storeCandidate()
	if blockers := f.validate(); len(blockers) != 0 {
		t.Fatalf("typed controlled route inherited named-human readiness: %v", blockers)
	}
}

func TestDelegatedFirstTouchUnclassifiedRecipientStillRequiresSendReadiness(t *testing.T) {
	f := newDelegatedValidationFixture(t, RouteClassGenericCompany, "contato@empresa.example")
	f.candidate.EmailSendReady = false
	f.candidate.DiscoveryJSON = nil
	f.candidate.VerificationStatus = models.OutreachVerifyCandidateUnverified
	f.storeCandidate()
	if blockers := f.validate(); !delegatedTestContains(blockers, "recipient_not_controlled_eligible") ||
		!delegatedTestContains(blockers, "email_outbound_gate_failed") {
		t.Fatalf("unclassified recipient without send readiness passed: %v", blockers)
	}
}

func TestDelegatedFirstTouchFailsClosedOnRecipientComplianceAndSourceDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*delegatedValidationFixture)
		want   string
	}{
		{"guessed_email", func(f *delegatedValidationFixture) {
			f.candidate.EmailDerivation = "INFERRED"
			f.candidate.EmailSendReady = false
			f.storeCandidate()
		}, "recipient_attribution_or_freshness_invalid"},
		{"stale_recipient", func(f *delegatedValidationFixture) { f.entry.Recipient = "old@empresa.example" }, "recipient_candidate_mismatch"},
		{"stale_import", func(f *delegatedValidationFixture) {
			other := uuid.New()
			f.candidate.LastImportRunID = &other
			f.storeCandidate()
		}, "recipient_stale_import_run"},
		{"stale_source_run", func(f *delegatedValidationFixture) { f.manifest.SourceRunID = "run-old" }, "stale_source_run"},
		{"evidence_conflict", func(f *delegatedValidationFixture) { f.entry.ContractEvidenceHash = strings.Repeat("d", 64) }, "contractor_role_evidence_binding_mismatch"},
		{"evidence_reference_forged", func(f *delegatedValidationFixture) {
			f.entry.ContractEvidenceReference = "https://example.invalid/evidence"
		}, "contractor_role_evidence_reference_invalid"},
		{"role_reason_codes_missing", func(f *delegatedValidationFixture) {
			f.entry.ContractRoleReasonCodes = []string{"lead_matches_supplier"}
		}, "party_role_reason_codes_invalid"},
		{"idempotency_key_missing", func(f *delegatedValidationFixture) { f.entry.IdempotencyKey = "" }, "idempotency_key_invalid"},
		{"suppression", func(f *delegatedValidationFixture) { f.account.DoNotContact = true }, "account_suppressed_or_interacted"},
		{"opt_out", func(f *delegatedValidationFixture) { f.candidate.DoNotContact = true; f.storeCandidate() }, "recipient_not_controlled_eligible"},
		{"hard_bounce", func(f *delegatedValidationFixture) { f.candidate.Bounced = true; f.storeCandidate() }, "recipient_not_controlled_eligible"},
		{"mailbox_purpose_blocked", func(f *delegatedValidationFixture) {
			f.candidate.MailboxPurpose = "ORCAMENTO"
			f.candidate.MailboxPurposeSendBlocked = true
			f.storeCandidate()
		}, "recipient_not_controlled_eligible"},
		{"mailbox_commercially_unsuitable", func(f *delegatedValidationFixture) {
			f.candidate.RecipientCommercialSuitability = "UNSUITABLE_MAILBOX"
			f.storeCandidate()
		}, "recipient_not_controlled_eligible"},
		{"copy_editorial", func(f *delegatedValidationFixture) {
			f.entry.BodyText += " Espero que este e-mail o encontre bem."
			f.entry.BodyHash = hashText(f.entry.BodyText)
		}, "deterministic_qa:banned_phrase_espero_que_este_e_mail_o_encontre_bem"},
		{"fact_evidence_mismatch", func(f *delegatedValidationFixture) { f.entry.EvidenceIDs = []string{"other"} }, "fact_evidence_binding_mismatch"},
		{"buyer_claim_in_copy", func(f *delegatedValidationFixture) {
			f.entry.BodyText = strings.Replace(f.entry.BodyText, "aparece como contratada", "aparece como contratante", 1)
			f.entry.BodyHash = hashText(f.entry.BodyText)
		}, "target_role_claim_mismatch"},
		{"unsupported_numeric_fact", func(f *delegatedValidationFixture) {
			f.entry.BodyText = strings.Replace(f.entry.BodyText, "contratos públicos confirmados", "3 contratos públicos confirmados", 1)
			f.entry.BodyHash = hashText(f.entry.BodyText)
		}, "unsupported_specific_fact"},
		{"unsupported_qualitative_fact", func(f *delegatedValidationFixture) {
			f.entry.BodyText += " A Empresa Alfa possui grande volume de contratos."
			f.entry.BodyHash = hashText(f.entry.BodyText)
		}, "unsupported_factual_claim"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newDelegatedValidationFixture(t, RouteClassGenericCompany, "contato@empresa.example")
			tc.mutate(&f)
			if got := f.validate(); !delegatedTestContains(got, tc.want) {
				t.Fatalf("missing blocker %q: %v", tc.want, got)
			}
		})
	}
}

func TestDelegatedFirstTouchCanonicalTransportStateFailsClosed(t *testing.T) {
	valid := delegatedTransportAuthority{
		CampaignStatus: "paused", MailboxStatus: "active", MailboxRiskBand: "clean",
		WorkerAssigned: true, CredentialsPresent: true, SenderSelected: true,
		CampaignLimit: 50, MinWaitTime: 600,
	}
	if blockers := validateDelegatedTransportState(valid); len(blockers) != 0 {
		t.Fatalf("canonical paused canary transport was blocked: %v", blockers)
	}
	runtimeCfg := dispatch.Config{SendsPerHour: 10, MinGap: 10 * time.Minute}
	if blockers := validateDelegatedRuntimeTransportBounds(valid, 10, runtimeCfg); len(blockers) != 0 {
		t.Fatalf("canonical scheduler bounds were blocked: %v", blockers)
	}
	if blockers := validateDelegatedRuntimeTransportBounds(valid, 10, dispatch.Config{SendsPerHour: 10, MinGap: time.Minute}); !delegatedTestContains(blockers, "canonical_scheduler_min_gap_below_mailbox") {
		t.Fatalf("scheduler below mailbox min_wait_time passed: %v", blockers)
	}
	if blockers := validateDelegatedRuntimeTransportBounds(valid, 60, runtimeCfg); !delegatedTestContains(blockers, "delegated_policy_rate_exceeds_mailbox_bounds") {
		t.Fatalf("policy above mailbox campaign limit passed: %v", blockers)
	}
	for name, mutate := range map[string]func(*delegatedTransportAuthority){
		"completed_campaign":  func(s *delegatedTransportAuthority) { s.CampaignStatus = "completed" },
		"inactive_mailbox":    func(s *delegatedTransportAuthority) { s.MailboxStatus = "inactive" },
		"quarantined_mailbox": func(s *delegatedTransportAuthority) { s.MailboxRiskBand = "quarantine" },
		"missing_worker":      func(s *delegatedTransportAuthority) { s.WorkerAssigned = false },
		"missing_credentials": func(s *delegatedTransportAuthority) { s.CredentialsPresent = false },
		"outside_sender_pool": func(s *delegatedTransportAuthority) { s.SenderSelected = false },
		"invalid_rate_bounds": func(s *delegatedTransportAuthority) { s.MinWaitTime = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			state := valid
			mutate(&state)
			if blockers := validateDelegatedTransportState(state); len(blockers) == 0 {
				t.Fatal("invalid canonical transport state passed")
			}
		})
	}
}

func TestDelegatedFirstTouchManifestAndReplayBindingsFailClosed(t *testing.T) {
	f := newDelegatedValidationFixture(t, RouteClassGenericCompany, "contato@empresa.example")
	if got := validateDelegatedManifestHeader(f.manifest); len(got) != 0 {
		t.Fatalf("valid header blocked: %v", got)
	}
	base := delegatedMaterialBinding(f.manifest, f.entry)
	for name, mutate := range map[string]func(*delegatedValidationFixture){
		"recipient": func(f *delegatedValidationFixture) { f.entry.Recipient = "outro@empresa.example" },
		"evidence":  func(f *delegatedValidationFixture) { f.entry.ContractEvidenceHash = strings.Repeat("e", 64) },
		"policy":    func(f *delegatedValidationFixture) { f.manifest.PolicyHash = strings.Repeat("f", 64) },
		"source":    func(f *delegatedValidationFixture) { f.manifest.SourceRunID = "other-run" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := f
			mutate(&copy)
			if got := delegatedMaterialBinding(copy.manifest, copy.entry); got == base {
				t.Fatal("material drift kept the replay binding")
			}
		})
	}
	f.manifest.PolicyHash = strings.Repeat("f", 64)
	if got := validateDelegatedManifestHeader(f.manifest); !delegatedTestContains(got, "policy_hash_mismatch") {
		t.Fatalf("policy drift passed: %v", got)
	}
}

func TestDelegatedFirstTouchBatch500AndPartialIsolationContract(t *testing.T) {
	f := newDelegatedValidationFixture(t, RouteClassGenericCompany, "contato@empresa.example")
	f.manifest.Entries = make([]DelegatedFirstTouchEntry, 500)
	for i := range f.manifest.Entries {
		f.manifest.Entries[i] = f.entry
		f.manifest.Entries[i].IdempotencyKey += ":" + uuid.NewString()
	}
	if got := validateDelegatedManifestHeader(f.manifest); len(got) != 0 {
		t.Fatalf("batch 500 rejected: %v", got)
	}
	f.manifest.Entries = append(f.manifest.Entries, make([]DelegatedFirstTouchEntry, 501)...)
	if got := validateDelegatedManifestHeader(f.manifest); !delegatedTestContains(got, "manifest_entry_count_invalid") {
		t.Fatalf("batch above hard ceiling passed: %v", got)
	}
	// A duplicate root is item-scoped: the batch loop records HOLD for those
	// entries and continues processing unrelated roots.
	dupes := duplicateManifestRoots([]DelegatedFirstTouchEntry{{CNPJ14: "11222333000144"}, {CNPJ14: "11222333000225"}, {CNPJ14: "88777666000155"}})
	if !dupes["11222333"] || dupes["88777666"] {
		t.Fatalf("duplicate-root isolation drifted: %v", dupes)
	}
}
