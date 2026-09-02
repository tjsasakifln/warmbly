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
	expiresAt := now.Add(time.Hour)
	repo.feedSync = map[uuid.UUID]*models.OutreachFeedSyncState{orgID: {
		OrganizationID: orgID, LastRunID: runID, LastSnapshotHash: snapshot,
		LastStatus: "completed", LastSuccessAt: &now, SourceGeneratedAt: &now, SourceExpiresAt: &expiresAt,
		SourceFreshnessHash: strings.Repeat("d", 64), TargetMembershipComplete: true,
		TargetMembershipHash: strings.Repeat("e", 64), TargetMembershipCount: 1, SupplierConfirmedCount: 1,
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
	// COMMERCIAL_AUTHORITY/2.0: a supplier contract signed a year ago, well
	// inside the rolling three-year window.
	qualification := testRootQualification(account.CNPJRoot, now.AddDate(-1, 0, 0))
	applyCommercialQualificationToAccount(account, &qualification, now)
	stampFeedStateWithV2(repo.feedSync[orgID], []RootQualification{qualification})
	personUnknown := routeClass != RouteClassDirectPerson
	discovery := []byte(`{"route_class":"` + routeClass + `","controlled_email_eligible":true,"preferred_initial":true,"mailbox_company_evidence":"OBSERVED","person_unknown":` + map[bool]string{true: "true", false: "false"}[personUnknown] + `,"risk_class":"ALLOWED"}`)
	candidate := models.OutreachContactCandidate{
		ID: candidateID, OrganizationID: orgID, AccountID: accountID, SourceContactID: "route-1",
		Name: "Equipe", Role: "Atendimento", Email: email, SourceURL: "https://empresa.example/contato",
		VerificationStatus: models.OutreachVerifyOfficialSource, EmailSendReady: true, OwnershipStatus: "COMPANY_OWNED",
		RecipientCommercialSuitability: "SUITABLE", LastImportRunID: &importID, DiscoveryJSON: discovery,
		ChannelEpistemic: "OBSERVED", RouteFreshness: "FRESH", RouteSuppression: "NONE", EmailDerivation: "OBSERVED",
	}
	if routeClass == RouteClassDirectPerson {
		candidate.Name = "Ana Souza"
		candidate.Role = "Gerente de Contratos"
		candidate.MailboxPurpose = "PERSONAL_WORK"
	} else if routeClass == RouteClassRoleOrDepartment {
		candidate.Name = "Equipe"
		candidate.Role = "Licitações"
		candidate.MailboxPurpose = "LICITACOES"
	} else {
		candidate.MailboxPurpose = "GENERIC_CONTACT"
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
	copy := buildDelegatedRoutingCopy(account, &candidate, repo.evidence[accountID])
	entry := delegatedEntryFromCurrentState(account, &candidate, uuid.New(), copy)
	entry.IdempotencyKey = "first-touch:lead-1:v1"
	entry.CorrelationID = "corr-1"
	entry.ReconciliationStatus = ReconciliationCorroborated
	entry.WebSources = []DelegatedWebSource{{URL: candidate.SourceURL, Kind: "OFFICIAL_COMPANY_PAGE", Supports: "COMPANY_IDENTITY_AND_MAILBOX", ObservedAt: now}}
	entry.RouteClass = routeClass
	entry.Recipient = email
	entry.QA.Reviewer = "agent:codex"
	entry.SubjectHash, entry.BodyHash = hashText(entry.Subject), hashText(entry.BodyText)
	manifest := DelegatedFirstTouchManifest{
		SchemaVersion: DelegatedFirstTouchManifestV1, BatchID: "batch-1", AgentID: "agent:codex",
		PolicyVersion: DelegatedFirstTouchPolicyV3, PolicyHash: DelegatedFirstTouchPolicyHashV3,
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

// COMMERCIAL_AUTHORITY/2.0 admission is decided by the qualifying public fact,
// never by how old the crawler's last run is.
func TestDelegatedFirstTouchCommercialAuthorityGatesAdmission(t *testing.T) {
	f := newDelegatedValidationFixture(t, RouteClassGenericCompany, "contato@empresa.example")
	now := f.service.now()

	// Baseline: a supplier contract inside the window admits.
	if got := f.validate(); len(got) != 0 {
		t.Fatalf("qualified company blocked: %v", got)
	}

	// A STALE, even expired, producer window must not change the verdict.
	feed := f.repo.feedSync[f.orgID]
	stale := *feed
	past := now.Add(-96 * time.Hour)
	expired := now.Add(-time.Hour)
	stale.SourceGeneratedAt = &past
	stale.SourceExpiresAt = &expired
	f.repo.feedSync[f.orgID] = &stale
	if got := f.validate(); len(got) != 0 {
		t.Fatalf("stale source blocked an otherwise qualified company: %v", got)
	}
	f.repo.feedSync[f.orgID] = feed

	// A contract that fell out of the three-year window expires the company.
	expiredQual := testRootQualification(f.account.CNPJRoot, now.AddDate(-3, 0, -1))
	applyCommercialQualificationToAccount(f.account, &expiredQual, now)
	if got := f.validate(); !delegatedTestContains(got, ReasonQualificationExpired) {
		t.Fatalf("a contract older than three years still admitted: %v", got)
	}

	// Explicit deactivation beats everything, inside the window or not.
	revoked := testRootQualification(f.account.CNPJRoot, now.AddDate(-1, 0, 0))
	revoked.Deactivated = true
	revoked.DeactivationReason = "EXPLICIT_REVOCATION"
	revoked.EvidenceHash = HashRootQualification(revoked)
	applyCommercialQualificationToAccount(f.account, &revoked, now)
	if got := f.validate(); !delegatedTestContains(got, ReasonQualificationRevoked) {
		t.Fatalf("explicit deactivation did not block admission: %v", got)
	}

	// Absent qualification is fail-closed and explicitly named.
	f.account.CommercialQualificationState = ""
	if got := f.validate(); !delegatedTestContains(got, ReasonQualificationMissing) {
		t.Fatalf("missing commercial authority was not fail-closed: %v", got)
	}
}

func TestDelegatedFirstTouchAttributedFreemailPasses(t *testing.T) {
	f := newDelegatedValidationFixture(t, RouteClassPublicCompanyFreemail, "empresa.alfa@gmail.com")
	if blockers := f.validate(); len(blockers) != 0 {
		t.Fatalf("attributed company freemail blocked: %v", blockers)
	}
}

func TestDelegatedFirstTouchSupportedSpecificFactPassesAllDeterministicGates(t *testing.T) {
	f := newDelegatedValidationFixture(t, RouteClassGenericCompany, "contato@empresa.example")
	factID := "fact-pavimentacao"
	f.account.FactToMention = "Contratação de empresa para pavimentação asfáltica na Avenida Ipê"
	f.account.MomentEvidenceIDs = []string{factID}
	now := time.Now().UTC().Truncate(time.Second)
	f.repo.evidence[f.account.ID] = append(f.repo.evidence[f.account.ID], models.OutreachEvidence{
		ID: uuid.New(), OrganizationID: f.orgID, AccountID: f.account.ID, SourceEvidenceID: factID,
		EpistemicClass: models.OutreachEpistemicConfirmedFact, Reliability: "HIGH",
		URL: "https://pncp.gov.br/contratos/2", Synthesis: f.account.FactToMention,
		ConsultedAt: &now, LastImportRunID: &f.importID,
	})
	copy := buildDelegatedRoutingCopy(f.account, &f.candidate, f.repo.evidence[f.account.ID])
	if len(copy.FactEvidenceIDs) != 1 {
		t.Fatalf("specific fact was not bound to evidence: %+v", copy)
	}
	f.entry.Subject, f.entry.BodyText = copy.Subject, copy.Body
	f.entry.CopyRulesVersion, f.entry.FactUsed = DelegatedFirstTouchCopyRulesV1, copy.FactUsed
	f.entry.FactEvidenceIDs = append([]string{}, copy.FactEvidenceIDs...)
	f.entry.Practice, f.entry.CTA, f.entry.SemanticSignature = copy.Practice, copy.CTA, copy.SemanticSignature
	f.entry.EvidenceIDs = uniqueStrings(append(append([]string{}, f.entry.ContractEvidenceIDs...), copy.FactEvidenceIDs...))
	f.entry.SubjectHash, f.entry.BodyHash = hashText(f.entry.Subject), hashText(f.entry.BodyText)
	if blockers := f.validate(); len(blockers) != 0 {
		t.Fatalf("supported specific fact blocked: %v", blockers)
	}
}

func TestDelegatedFirstTouchControlledInstitutionalRouteDoesNotRequireNamedHumanReadiness(t *testing.T) {
	f := newDelegatedValidationFixture(t, RouteClassGenericCompany, "contato@empresa.example")
	f.candidate.EmailSendReady = false
	f.candidate.MailboxPurposeSendBlocked = true
	f.candidate.RecipientCommercialSuitability = "UNSUITABLE_MAILBOX"
	f.storeCandidate()
	if blockers := f.validate(); len(blockers) != 0 {
		t.Fatalf("explicit controlled route inherited named-person projections: %v", blockers)
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
		{"copy_editorial", func(f *delegatedValidationFixture) {
			f.entry.BodyText += " Espero que este e-mail o encontre bem."
			f.entry.BodyHash = hashText(f.entry.BodyText)
		}, "deterministic_qa:banned_phrase_espero_que_este_e_mail_o_encontre_bem"},
		{"empty_subject", func(f *delegatedValidationFixture) {
			f.entry.Subject = ""
			f.entry.SubjectHash = hashText(f.entry.Subject)
		}, "subject_empty"},
		{"empty_body", func(f *delegatedValidationFixture) {
			f.entry.BodyText = ""
			f.entry.BodyHash = hashText(f.entry.BodyText)
		}, "body_empty"},
		{"hallucinated_person", func(f *delegatedValidationFixture) {
			f.entry.BodyText = strings.Replace(f.entry.BodyText, "Olá,", "Olá, Roberto,", 1)
			f.entry.BodyHash = hashText(f.entry.BodyText)
		}, "hallucinated_person"},
		{"internal_metadata", func(f *delegatedValidationFixture) {
			f.entry.BodyText += " route_class=GENERIC_COMPANY"
			f.entry.BodyHash = hashText(f.entry.BodyText)
		}, "internal_metadata_leak"},
		{"offensive_or_manipulative", func(f *delegatedValidationFixture) {
			f.entry.BodyText += " Você precisa responder agora."
			f.entry.BodyHash = hashText(f.entry.BodyText)
		}, "offensive_or_manipulative_language"},
		{"route_cta_mismatch", func(f *delegatedValidationFixture) {
			f.entry.BodyText = strings.Replace(f.entry.BodyText, f.entry.CTA, "Faz sentido marcarmos uma reunião?", 1)
			f.entry.BodyHash = hashText(f.entry.BodyText)
		}, "route_cta_mismatch"},
		{"fact_evidence_mismatch", func(f *delegatedValidationFixture) { f.entry.EvidenceIDs = []string{"other"} }, "fact_evidence_binding_mismatch"},
		{"buyer_claim_in_copy", func(f *delegatedValidationFixture) {
			f.entry.BodyText = strings.Replace(f.entry.BodyText, "aparece como contratada", "aparece como contratante", 1)
			f.entry.BodyHash = hashText(f.entry.BodyText)
		}, "target_role_claim_mismatch"},
		{"unsupported_numeric_fact", func(f *delegatedValidationFixture) {
			f.entry.BodyText += " Há 3 contratos."
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

func TestDelegatedFirstTouchRejectsExactContentReuse(t *testing.T) {
	f := newDelegatedValidationFixture(t, RouteClassGenericCompany, "contato@empresa.example")
	_, _, blockers := f.service.validateDelegatedEntry(
		context.Background(), f.orgID, f.manifest, f.entry, map[string]bool{},
		[]delegatedRecentBody{{AccountID: uuid.New(), Body: f.entry.BodyText}}, true,
	)
	if !delegatedTestContains(blockers, "corpus_exact_content_limit") {
		t.Fatalf("identical reader-facing content passed: %v", blockers)
	}
}

func TestDelegatedFirstTouchCanonicalTransportStateFailsClosed(t *testing.T) {
	authCheckedAt := time.Now().UTC()
	valid := delegatedTransportAuthority{
		CampaignStatus: "paused", MailboxStatus: "active", MailboxRiskBand: "clean",
		WorkerAssigned: true, WorkerHealthy: true, CredentialsPresent: true, SenderSelected: true,
		AuthState: "passing", AuthSPF: true, AuthDKIM: true, AuthDMARC: true, AuthCheckedAt: &authCheckedAt,
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
		"unhealthy_worker":    func(s *delegatedTransportAuthority) { s.WorkerHealthy = false },
		"missing_credentials": func(s *delegatedTransportAuthority) { s.CredentialsPresent = false },
		"dns_auth_unknown":    func(s *delegatedTransportAuthority) { s.AuthState = "unknown" },
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
