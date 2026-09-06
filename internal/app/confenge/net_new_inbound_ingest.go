package confenge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// NetNewInboundResult is one persist-first consume. HTTP 2xx is not acceptance;
// Outcome is. Readback of LogicalID is the proof.
type NetNewInboundResult struct {
	Schema             string     `json:"schema"`
	SchemaVersion      string     `json:"schema_version"`
	PolicyID           string     `json:"policy_id"`
	PolicyVersion      string     `json:"policy_version"`
	CanonicalName      string     `json:"canonical_name"`
	DecisionID         string     `json:"decision_id"`
	LogicalAdmissionID string     `json:"logical_admission_id"`
	Decision           string     `json:"decision"`
	ReasonCodes        []string   `json:"reason_codes"`
	IdempotencyKey     string     `json:"idempotency_key"`
	Hash               string     `json:"hash,omitempty"`
	IntakeSchema       string     `json:"intake_schema,omitempty"`
	StateSchema        string     `json:"state_schema,omitempty"`
	LogicalID          string     `json:"logical_id"`
	Receipt            string     `json:"receipt"`
	ReceiptID          string     `json:"receipt_id,omitempty"`
	CorrelationID      string     `json:"correlation_id,omitempty"`
	Outcome            string     `json:"outcome"`
	Reason             string     `json:"reason,omitempty"`
	Replayed           bool       `json:"replayed"`
	Origin             string     `json:"origin"`
	AcquisitionLane    string     `json:"acquisition_lane"`
	IntentKind         string     `json:"intent_kind"`
	IntakeSource       string     `json:"intake_source"`
	Nucleus            string     `json:"nucleus,omitempty"`
	NucleusID          string     `json:"nucleus_id,omitempty"`
	OfferCandidate     string     `json:"offer_candidate,omitempty"`
	OfferCandidateID   string     `json:"offer_candidate_id,omitempty"`
	SourceAsset        string     `json:"source_asset,omitempty"`
	CityClass          string     `json:"-"`
	Urgency            string     `json:"-"`
	WhyNow             string     `json:"-"`
	ConflictRef        string     `json:"-"`
	ConflictStatus     string     `json:"conflict_status,omitempty"`
	QualificationState string     `json:"qualification_state,omitempty"`
	PreferredChannel   string     `json:"preferred_channel,omitempty"`
	InboundOnly        bool       `json:"inbound_only"`
	OutboundEligible   bool       `json:"outbound_eligible"`
	AutoSend           bool       `json:"auto_send"`
	SMTPAuthorized     bool       `json:"smtp_authorized"`
	FollowupAuthorized bool       `json:"followup_authorized"`
	AccountRequired    bool       `json:"account_required_for_acceptance"`
	IdentityAuth       string     `json:"identity_authorization"`
	DispatchAttempted  bool       `json:"dispatch_attempted"`
	MeetcfgHandoff     bool       `json:"meetcfg_handoff_allowed"`
	Replay             bool       `json:"replay"`
	AcknowledgedBy     string     `json:"acknowledged_by,omitempty"`
	AcknowledgedAt     *time.Time `json:"acknowledged_at,omitempty"`
	EvaluatedAt        *time.Time `json:"evaluated_at,omitempty"`
	AccountID          *uuid.UUID `json:"account_id,omitempty"`
	ActionID           *uuid.UUID `json:"action_id,omitempty"`
	CanonicalEntityID  string     `json:"-"`
	Reconciled         bool       `json:"reconciled,omitempty"`
}

// NetNewInboundReadback is the same logical ID after persist.
type NetNewInboundReadback struct {
	NetNewInboundResult
}

// NetNewInboundMetric is PII-free: nucleus, state, reason only.
type NetNewInboundMetric struct {
	Nucleus string `json:"nucleus"`
	State   string `json:"state"`
	Reason  string `json:"reason,omitempty"`
}

// IngestNetNewInboundHandraiser persists the receipt before any commercial
// effect. Unknown or unpinned schema is never ACCEPTED.
func (s *service) IngestNetNewInboundHandraiser(ctx context.Context, orgID uuid.UUID, raw []byte, now time.Time) (*NetNewInboundResult, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	if orgID == uuid.Nil {
		return nil, errx.New(errx.ServiceUnavailable, "inbound org is not configured")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	env, err := ParseNetNewInboundEnvelope(raw)
	if err != nil {
		res := rejectedNetNew("", NetNewInboundReasonSchemaMismatch, now)
		s.observeNetNewMetric(res)
		return res, nil
	}
	logicalID := netNewLogicalID(env)
	pin := s.activeAuthorityPin()
	pinHash := normalizeContentHash(pin.ContentHash)
	decision := DecideNetNewInbound(env, pin)
	if logicalID == "" {
		res := rejectedNetNew("", firstNonEmpty(decision.Reason, NetNewInboundReasonLogicalID), now)
		res.Outcome = NetNewInboundOutcomeRejected
		s.observeNetNewMetric(res)
		return res, nil
	}

	st := s.inboundStore()
	if st == nil {
		return nil, errx.NewWithIdentifier(errx.ServiceUnavailable, NetNewInboundReasonStoreUnavailable,
			"inbound lead store unavailable")
	}

	row := netNewReceiptRow(orgID, env, pinHash, now)
	created, existing, insertErr := st.InsertInboundLead(ctx, row)
	if insertErr != nil {
		return nil, errx.New(errx.Internal, "persist inbound receipt: "+insertErr.Error())
	}
	replay := false
	if !created && existing != nil {
		// A reused logical_id must carry the same admission material. If it
		// does not, the stored decision was earned by a different payload and
		// must never be handed to this one: fail closed, leave the stored
		// receipt untouched, and make the caller resolve the collision.
		if stored := provenanceValue(existing.Provenance, netNewProvDigest); stored != "" && stored != NetNewAdmissionDigest(env) {
			res := rejectedNetNew(logicalID, NetNewInboundReasonKeyConflict, now)
			res.Receipt = existing.ReceiptID
			res.CorrelationID = existing.CorrelationID
			s.observeNetNewMetric(res)
			return res, nil
		}
		if netNewReceiptComplete(existing) {
			res := netNewResultFromLead(existing, true)
			s.observeNetNewMetric(res)
			return res, nil
		}
		row = existing
		replay = true
	}
	if s.netNewAfterPersist != nil {
		if hookErr := s.netNewAfterPersist(row); hookErr != nil {
			res := netNewResultFromLead(row, replay)
			res.Outcome = NetNewInboundOutcomeUnknown
			res.Reason = NetNewInboundReasonDownstream
			s.observeNetNewMetric(res)
			return res, nil
		}
	}

	applyNetNewDecision(row, env, decision, pinHash, now)
	if decision.Outcome != NetNewInboundOutcomeAccepted {
		_ = st.UpdateInboundLead(ctx, row)
		res := netNewResultFromLead(row, replay)
		s.observeNetNewMetric(res)
		return res, nil
	}

	admitted, xerr := s.admitNetNewHandraiser(ctx, orgID, env, now)
	if xerr != nil {
		if id := strings.TrimSpace(xerr.Identifier); id != "" && id != NetNewInboundReasonDownstream {
			row.Warnings = appendUnique(row.Warnings, id)
		}
		markNetNewDownstreamUnavailable(row, env, NetNewInboundReasonDownstream, pinHash, now)
		_ = st.UpdateInboundLead(ctx, row)
		res := netNewResultFromLead(row, replay)
		res.Outcome = NetNewInboundOutcomeUnknown
		res.Reason = NetNewInboundReasonDownstream
		s.observeNetNewMetric(res)
		return res, nil
	}
	if admitted != nil && admitted.Account != nil {
		id := admitted.Account.ID
		row.AccountID = &id
		if admitted.Candidate != nil {
			cid := admitted.Candidate.ID
			row.CandidateID = &cid
		}
		signal := SignalRequestHumanReview
		if strings.EqualFold(strings.TrimSpace(env.IntentKind), "DEEP_DIVE") || strings.EqualFold(strings.TrimSpace(env.IntentKind), "REQUEST_DEEP_DIVE") {
			signal = SignalRequestDeepDive
		}
		raise := HandRaise{
			OrganizationID: orgID, AccountID: admitted.Account.ID,
			Signal: signal, EngineLane: EngineLaneConfengeWeb, OccurredAt: now,
			SubjectKey:  "company:" + firstNonEmpty(SanitizeText(env.Company.Name, 120), logicalID),
			Evidence:    SanitizeText(env.WhyNow, 500),
			PersonName:  SanitizeText(env.Person.Name, 120),
			Origin:      EngineLaneConfengeWeb,
			Receipt:     row.ReceiptID,
			Context:     netNewContext(env),
			InboundOnly: true,
		}
		if admitted.Candidate != nil {
			cid := admitted.Candidate.ID
			raise.CandidateID = &cid
		}
		action, persistErr := s.PersistHandRaise(ctx, raise)
		if persistErr != nil || action == nil {
			if persistErr != nil {
				if id := strings.TrimSpace(persistErr.Identifier); id != "" && id != NetNewInboundReasonDownstream {
					row.Warnings = appendUnique(row.Warnings, id)
				}
			}
			markNetNewDownstreamUnavailable(row, env, NetNewInboundReasonDownstream, pinHash, now)
			_ = st.UpdateInboundLead(ctx, row)
			res := netNewResultFromLead(row, replay)
			res.Outcome = NetNewInboundOutcomeUnknown
			res.Reason = NetNewInboundReasonDownstream
			if admitted.Account != nil {
				res.AccountID = &admitted.Account.ID
				res.Reconciled = admitted.Reused
			}
			s.observeNetNewMetric(res)
			return res, nil
		}
		aid := action.ID
		row.ActionID = &aid
		row.NextAction = models.InboundNextManualOutreach
		row.Channel = strings.ToLower(netNewPreferredChannel(env))
		row.WhyNow = SanitizeText(firstNonEmpty(env.WhyNow, env.WhyNowClass), 500)
	}

	row.UpdatedAt = now
	if err := st.UpdateInboundLead(ctx, row); err != nil {
		return nil, errx.New(errx.Internal, "update inbound lead: "+err.Error())
	}
	s.ensureNetNewOperatorAlert(ctx, orgID, row, now)
	res := netNewResultFromLead(row, replay)
	if admitted != nil {
		res.Reconciled = admitted.Reused
		res.CanonicalEntityID = netNewCanonicalEntityID(env)
		res.InboundOnly = true
		res.OutboundEligible = false
		if admitted.Account != nil {
			// This receipt never inherits outbound authority from a pre-existing
			// account. It authorizes only this inbound human action.
			res.OutboundEligible = false
			res.InboundOnly = true
		}
	}
	s.observeNetNewMetric(res)
	return res, nil
}

// ReadbackNetNewInboundHandraiser returns the persisted receipt for one logical ID.
func (s *service) ReadbackNetNewInboundHandraiser(ctx context.Context, orgID uuid.UUID, logicalID string) (*NetNewInboundReadback, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	logicalID = normalizeNetNewOpaqueRef(logicalID)
	if logicalID == "" {
		return nil, errx.NewWithIdentifier(errx.BadRequest, NetNewInboundReasonLogicalID, "logical_id is required")
	}
	st := s.inboundStore()
	if st == nil {
		return nil, errx.NewWithIdentifier(errx.ServiceUnavailable, NetNewInboundReasonStoreUnavailable,
			"inbound lead store unavailable")
	}
	row, err := st.GetInboundLeadByLeadID(ctx, orgID, logicalID)
	if err != nil {
		return nil, errx.New(errx.Internal, "read inbound receipt: "+err.Error())
	}
	if row == nil {
		return nil, errx.New(errx.NotFound, "inbound hand-raiser receipt not found")
	}
	res := netNewResultFromLead(row, false)
	if !netNewReceiptComplete(row) {
		res.Outcome = NetNewInboundOutcomeUnknown
		if !netNewDownstreamRetryable(row) {
			res.Reason = NetNewInboundReasonStale
		}
	}
	if ast := s.alertStore(); ast != nil {
		if alert, aerr := ast.GetOperatorAlertByLead(ctx, orgID, logicalID); aerr == nil && alert != nil {
			if strings.TrimSpace(alert.AcknowledgedBy) != "" {
				res.AcknowledgedBy = alert.AcknowledgedBy
			}
			if alert.AcknowledgedAt != nil {
				res.AcknowledgedAt = alert.AcknowledgedAt
			}
		}
	}
	return &NetNewInboundReadback{NetNewInboundResult: *res}, nil
}

func (s *service) admitNetNewHandraiser(ctx context.Context, orgID uuid.UUID, env NetNewInboundEnvelope, now time.Time) (*InboundAdmission, *errx.Error) {
	logicalID := netNewLogicalID(env)
	canonical := netNewCanonicalEntityID(env)
	channel := netNewPreferredChannel(env)
	email := ""
	phone := ""
	switch channel {
	case NetNewInboundPreferredEmail:
		email = netNewSelectedEmail(env)
	case NetNewInboundPreferredPhone:
		phone = netNewSelectedPhone(env)
	case NetNewInboundPreferredWhatsApp:
		phone = netNewSelectedWhatsApp(env)
	}
	name := firstNonEmpty(SanitizeText(env.Person.Name, 120), SanitizeText(env.Company.Name, 120))
	contextText := netNewContext(env)

	if canonical != "" {
		if acc, err := s.repo.GetAccountBySourceLeadID(ctx, orgID, canonical); err != nil {
			return nil, errx.NewWithIdentifier(errx.Internal, NetNewInboundReasonDownstream,
				"canonical entity lookup: "+err.Error())
		} else if acc != nil {
			out := &InboundAdmission{
				Account: acc, Origin: EngineLaneConfengeWeb, Lane: EngineLaneConfengeWeb,
				IntentKind: netNewIntentKind(env), Context: contextText,
				Receipt: InboundReceiptID(EngineLaneConfengeWeb, netNewIntentKind(env), firstNonEmpty(email, phone, logicalID), canonical),
				Reused:  true, InboundOnly: models.AccountIsInboundOnly(acc),
				OutboundEligible: accountOutboundEligible(acc),
			}
			created, xerr := s.upsertNetNewInboundCandidate(ctx, orgID, acc.ID, logicalID, email, phone, name, channel, env.Consent, now)
			if xerr != nil {
				return nil, xerr
			}
			out.Candidate = created
			return out, nil
		}
	}

	// An explicit contact may already belong to an account. Display name never
	// participates in identity, and no placeholder email is invented.
	var candidate *models.OutreachContactCandidate
	var account *models.OutreachAccount
	var err error
	if email != "" {
		candidate, account, err = s.repo.FindCandidateByEmail(ctx, orgID, email)
	} else if phone != "" {
		candidate, account, err = s.repo.FindCandidateByPhone(ctx, orgID, phone)
	}
	if err != nil {
		return nil, errx.NewWithIdentifier(errx.Internal, NetNewInboundReasonDownstream,
			"inbound-only contact lookup: "+err.Error())
	}
	if account != nil {
		updated, xerr := s.upsertNetNewInboundCandidate(ctx, orgID, account.ID, logicalID, email, phone, name, channel, env.Consent, now)
		if xerr != nil {
			return nil, xerr
		}
		if updated != nil {
			candidate = updated
		}
		return &InboundAdmission{
			Account: account, Candidate: candidate, Origin: EngineLaneConfengeWeb, Lane: EngineLaneConfengeWeb,
			IntentKind: netNewIntentKind(env), Context: contextText,
			Receipt: InboundReceiptID(EngineLaneConfengeWeb, netNewIntentKind(env), firstNonEmpty(email, phone, logicalID), logicalID),
			Reused:  true, InboundOnly: models.AccountIsInboundOnly(account),
			OutboundEligible: accountOutboundEligible(account),
		}, nil
	}

	key := inboundOnlyLeadIDPrefix + "confenge_web:NET_NEW:" + logicalID
	account, err = s.repo.GetAccountBySourceLeadID(ctx, orgID, key)
	if err != nil {
		return nil, errx.NewWithIdentifier(errx.Internal, NetNewInboundReasonDownstream,
			"inbound-only account lookup: "+err.Error())
	}
	reused := account != nil
	if account == nil {
		placeholderCNPJ := inboundOnlyPlaceholderCNPJ(key)
		account = &models.OutreachAccount{
			ID: uuid.New(), OrganizationID: orgID, SourceLeadID: key,
			SourceSystem: models.SourceSystemInboundOnly, InboundOnly: true,
			CNPJ14: placeholderCNPJ, CNPJRoot: placeholderCNPJ[:8],
			RazaoSocial:  firstNonEmpty(SanitizeText(env.Company.Name, 120), name, "Demanda técnica inbound"),
			NomeFantasia: SanitizeText(env.Company.Name, 120),
			Municipio:    SanitizeText(env.City, 120), UF: strings.ToUpper(SanitizeText(env.UF, 2)),
			QueueState: models.OutreachQueueNeedsReview, TargetFitEligible: false, EmailSendReady: false,
			CreatedAt: now, UpdatedAt: now,
		}
		if _, err = s.repo.UpsertAccount(ctx, account); err != nil {
			if stored, _ := s.repo.GetAccountBySourceLeadID(ctx, orgID, key); stored != nil {
				account = stored
				reused = true
			} else {
				return nil, errx.NewWithIdentifier(errx.Internal, NetNewInboundReasonDownstream,
					"inbound-only account write: "+err.Error())
			}
		}
	}
	created, xerr := s.upsertNetNewInboundCandidate(ctx, orgID, account.ID, logicalID, email, phone, name, channel, env.Consent, now)
	if xerr != nil {
		return nil, xerr
	}
	return &InboundAdmission{
		Account: account, Candidate: created, Origin: EngineLaneConfengeWeb, Lane: EngineLaneConfengeWeb,
		IntentKind: netNewIntentKind(env), Context: contextText,
		Receipt:     InboundReceiptID(EngineLaneConfengeWeb, netNewIntentKind(env), firstNonEmpty(email, phone, logicalID), logicalID),
		InboundOnly: true, OutboundEligible: false, Reused: reused,
	}, nil
}

func (s *service) upsertNetNewInboundCandidate(ctx context.Context, orgID, accountID uuid.UUID, logicalID, email, phone, name, channel string, consent NetNewInboundConsent, now time.Time) (*models.OutreachContactCandidate, *errx.Error) {
	if email == "" && phone == "" {
		return nil, errx.NewWithIdentifier(errx.BadRequest, NetNewInboundReasonContact, "a return channel is required")
	}
	var existing *models.OutreachContactCandidate
	if email != "" {
		if found, acc, err := s.repo.FindCandidateByEmail(ctx, orgID, email); err != nil {
			return nil, errx.NewWithIdentifier(errx.Internal, NetNewInboundReasonDownstream, "contact lookup: "+err.Error())
		} else if found != nil && acc != nil && acc.ID == accountID {
			existing = found
		}
	}
	if existing == nil && phone != "" {
		if found, acc, err := s.repo.FindCandidateByPhone(ctx, orgID, phone); err != nil {
			return nil, errx.NewWithIdentifier(errx.Internal, NetNewInboundReasonDownstream, "contact lookup: "+err.Error())
		} else if found != nil && acc != nil && acc.ID == accountID {
			existing = found
		}
	}
	candidate := &models.OutreachContactCandidate{
		ID: uuid.New(), OrganizationID: orgID, AccountID: accountID,
		SourceContactID: inboundOnlyLeadIDPrefix + "net_new:" + logicalID,
		Name:            SanitizeText(name, 120), Email: email, Phone: phone, PhoneE164: phone,
		PhoneSource: "CONFENGE_WEB_EXPLICIT_FORM", VerificationStatus: models.OutreachVerifyCandidateUnverified,
		Confidence: "LEAD_SUPPLIED", Recommended: true, EmailSendReady: false,
	}
	if existing != nil {
		candidate.ID = existing.ID
		candidate.SourceContactID = firstNonEmpty(existing.SourceContactID, candidate.SourceContactID)
		candidate.Name = firstNonEmpty(candidate.Name, existing.Name)
		candidate.Email = firstNonEmpty(candidate.Email, existing.Email)
		candidate.Phone = firstNonEmpty(candidate.Phone, existing.Phone)
		candidate.PhoneE164 = firstNonEmpty(candidate.PhoneE164, existing.PhoneE164)
		candidate.DoNotContact = existing.DoNotContact
		candidate.Blocked = existing.Blocked
		candidate.BlockReason = existing.BlockReason
		candidate.Bounced = existing.Bounced
	}
	if channel == NetNewInboundPreferredWhatsApp && consent.Granted {
		candidate.WhatsAppConsentStatus = "OPTED_IN"
		candidate.WhatsAppConsentSource = SanitizeText(consent.Source, 120)
		candidate.WhatsAppConsentAt = consent.At
		if candidate.WhatsAppConsentAt == nil {
			t := now
			candidate.WhatsAppConsentAt = &t
		}
		candidate.WhatsAppConsentProvenanceOK = strings.TrimSpace(consent.Source) != "" &&
			(strings.TrimSpace(consent.EvidenceRef) != "" || consent.At != nil)
	} else {
		candidate.WhatsAppConsentStatus = "UNKNOWN"
	}
	if existing != nil && (existing.WhatsAppConsentStatus == "OPTED_OUT" || existing.WhatsAppConsentStatus == "DO_NOT_CONTACT" || existing.DoNotContact) {
		candidate.WhatsAppConsentStatus = existing.WhatsAppConsentStatus
		candidate.WhatsAppConsentSource = existing.WhatsAppConsentSource
		candidate.WhatsAppConsentAt = existing.WhatsAppConsentAt
		candidate.WhatsAppConsentProvenanceOK = existing.WhatsAppConsentProvenanceOK
	}
	if _, err := s.repo.UpsertCandidate(ctx, candidate); err != nil {
		return nil, errx.NewWithIdentifier(errx.Internal, NetNewInboundReasonDownstream, "inbound-only candidate write: "+err.Error())
	}
	return candidate, nil
}

func intelWebIntentHumanReview() string {
	return "REQUEST_HUMAN_REVIEW"
}

func netNewIntentKind(env NetNewInboundEnvelope) string {
	switch strings.ToUpper(strings.TrimSpace(env.IntentKind)) {
	case "DEEP_DIVE", "REQUEST_DEEP_DIVE":
		return "REQUEST_DEEP_DIVE"
	case "HUMAN_REVIEW", "REQUEST_HUMAN_REVIEW", "":
		return intelWebIntentHumanReview()
	default:
		return intelWebIntentHumanReview()
	}
}

func netNewContext(env NetNewInboundEnvelope) string {
	return SanitizeText(strings.Join(filterEmpty([]string{
		env.Nucleus, env.OfferCandidate, env.SourceAsset, env.CityClass, env.Urgency, env.WhyNow,
		env.WhyNowClass, env.DesiredDecision, env.DocumentAvailability,
		NetNewQualificationState(env), netNewPreferredChannel(env),
	}), " "), 500)
}

func filterEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if strings.TrimSpace(v) != "" {
			out = append(out, strings.TrimSpace(v))
		}
	}
	return out
}

func netNewReceiptRow(orgID uuid.UUID, env NetNewInboundEnvelope, pinHash string, now time.Time) *models.OutreachInboundLead {
	logicalID := netNewLogicalID(env)
	payload := netNewPIIFreeRawPayload(env, pinHash)
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	selectedEmail := ""
	selectedPhone := ""
	switch netNewPreferredChannel(env) {
	case NetNewInboundPreferredEmail:
		selectedEmail = netNewSelectedEmail(env)
	case NetNewInboundPreferredPhone:
		selectedPhone = netNewSelectedPhone(env)
	case NetNewInboundPreferredWhatsApp:
		selectedPhone = netNewSelectedWhatsApp(env)
	}
	receipt := InboundReceiptID(EngineLaneConfengeWeb, "NET_NEW_INBOUND_HANDRAISER",
		firstNonEmpty(selectedEmail, selectedPhone, logicalID), logicalID)
	row := &models.OutreachInboundLead{
		ID:                uuid.New(),
		OrganizationID:    orgID,
		LeadID:            logicalID,
		ReceiptID:         receipt,
		IdentityKey:       inboundIdentityKey("", selectedEmail, selectedPhone),
		LeadCreatedAt:     now,
		WarmblyIngestedAt: now,
		Source:            NetNewInboundSource,
		RouteFamily:       "inbound",
		AssetID:           SanitizeText(firstNonEmpty(env.SourceAsset, NetNewInboundSourceAsset), 120),
		CTAID:             SanitizeText(env.OfferCandidate, 120),
		EntityID:          SanitizeText(netNewCanonicalEntityID(env), 120),
		CompanyName:       SanitizeText(env.Company.Name, 200),
		LeadName:          SanitizeText(env.Person.Name, 160),
		LeadEmail:         selectedEmail,
		LeadPhone:         selectedPhone,
		CorrelationID:     normalizeNetNewOpaqueRef(env.CorrelationID),
		ConsentJSON:       []byte(`{"granted":true}`),
		UTMJSON:           netNewUTM(env),
		RawPayload:        payload,
		EnrichmentStatus:  models.InboundEnrichmentUnknown,
		Owner:             NetNewInboundAckActor,
		Status:            models.InboundStatusOpen,
		Channel:           strings.ToLower(netNewPreferredChannel(env)),
		WhyNow:            SanitizeText(firstNonEmpty(env.WhyNow, env.WhyNowClass), 500),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if env.SensitiveData.Present {
		row.LeadName = ""
		row.LeadEmail = ""
		row.LeadPhone = ""
		row.CompanyName = SanitizeText(env.Company.Name, 200)
		row.Message = ""
	}
	if !env.Consent.Granted {
		row.ConsentJSON = []byte(`{"granted":false}`)
	}
	t := now
	row.OwnerAssignedAt = &t
	setNetNewProvenance(row, env, "", "", pinHash, now)
	return row
}

func netNewPIIFreeRawPayload(env NetNewInboundEnvelope, pinHash string) []byte {
	// Positive allowlist only. Free text, contact data, opaque references,
	// attribution and unknown producer extensions never enter RawPayload.
	payload := map[string]any{
		"schema":              NetNewInboundHandraiserSchema,
		"policy_hash":         normalizeContentHash(pinHash),
		"qualification_state": NetNewQualificationState(env),
		"sensitive_present":   env.SensitiveData.Present,
		"outbound_eligible":   false,
		"auto_send":           false,
		"dispatch_attempted":  false,
	}
	if env.FinalContractClaim && env.FinalContractConformant {
		payload["schema_version"] = "net-new-inbound-handraiser-request.1.0.0-draft.20260904"
	}
	if netNewNucleusOK(env.Nucleus) {
		payload["nucleus"] = env.Nucleus
	}
	if strings.TrimSpace(env.OfferCandidate) != "" && netNewOfferCandidateOK(env.OfferCandidate) {
		payload["offer_candidate"] = env.OfferCandidate
	}
	if strings.TrimSpace(env.SourceAsset) != "" && netNewSourceAssetOK(env.SourceAsset) {
		payload["source_asset"] = env.SourceAsset
	}
	if channel := netNewPreferredChannel(env); channel != "" {
		payload["preferred_channel"] = channel
	}
	status := strings.ToUpper(strings.TrimSpace(env.Conflict.Status))
	switch status {
	case netNewInboundConflictNone, netNewInboundConflictClear, netNewInboundConflictDecline,
		netNewInboundConflictHit, netNewInboundConflictUnknown, netNewInboundConflictNotScreened:
		payload["conflict_status"] = status
	}
	clean, err := json.Marshal(payload)
	if err != nil || len(clean) == 0 {
		return []byte("{}")
	}
	return clean
}

func netNewUTM(env NetNewInboundEnvelope) []byte {
	m := map[string]string{
		"nucleus":             SanitizeText(env.Nucleus, 80),
		"offer_candidate":     SanitizeText(firstNonEmpty(env.OfferCandidate, NetNewInboundOfferCandidate), 120),
		"source_asset":        SanitizeText(firstNonEmpty(env.SourceAsset, NetNewInboundSourceAsset), 120),
		"city_class":          SanitizeText(env.CityClass, 40),
		"urgency":             SanitizeText(env.Urgency, 40),
		"policy":              NetNewInboundHandraiserSchema,
		"intake_schema":       firstNonEmpty(env.IntakeSchema, NetNewInboundIntakeSchema),
		"qualification_state": NetNewQualificationState(env),
		"preferred_channel":   netNewPreferredChannel(env),
		"conflict_status":     strings.ToUpper(SanitizeText(env.Conflict.Status, 40)),
	}
	raw, _ := json.Marshal(m)
	if len(raw) == 0 {
		return []byte("{}")
	}
	return raw
}

func applyNetNewDecision(row *models.OutreachInboundLead, env NetNewInboundEnvelope, decision NetNewInboundDecision, pinHash string, now time.Time) {
	if row == nil {
		return
	}
	setNetNewProvenance(row, env, decision.Outcome, decision.Reason, pinHash, now)
	row.UpdatedAt = now
	switch decision.Outcome {
	case NetNewInboundOutcomeAccepted:
		row.EnrichmentStatus = models.InboundEnrichmentCompleted
		row.Status = models.InboundStatusOpen
		row.NextAction = models.InboundNextManualOutreach
		t := now
		row.EnrichmentCompletedAt = &t
	case NetNewInboundOutcomeRejected:
		row.EnrichmentStatus = models.InboundEnrichmentCompleted
		row.Status = models.InboundStatusSuppressed
		row.NextAction = models.InboundNextSuppressed
		row.SuppressReason = decision.Reason
	default:
		row.EnrichmentStatus = models.InboundEnrichmentUnknown
		row.Status = models.InboundStatusOpen
		row.NextAction = models.InboundNextNeedsEnrichment
		row.Warnings = appendUnique(row.Warnings, firstNonEmpty(decision.Reason, NetNewInboundOutcomeUnknown))
	}
}

// setNetNewProvenance records the authority the decision was actually made
// against. pinHash is the admitted pin's content hash (the Governance
// policy_hash), never this repository's local drift digest.
func setNetNewProvenance(row *models.OutreachInboundLead, env NetNewInboundEnvelope, outcome, reason, pinHash string, now time.Time) {
	if row == nil {
		return
	}
	prov := []string{
		netNewProvPolicy + NetNewInboundHandraiserSchema,
		netNewProvIntake + firstNonEmpty(env.IntakeSchema, NetNewInboundIntakeSchema),
		netNewProvState + NetNewInboundStateSchema,
		netNewProvHash + normalizeContentHash(pinHash),
		netNewProvDigest + NetNewAdmissionDigest(env),
		netNewProvAckBy + NetNewInboundAckActor,
		netNewProvAckAt + now.UTC().Format(time.RFC3339),
		netNewProvOrigin + strings.TrimSpace(env.Source),
		netNewProvLane + strings.TrimSpace(env.Lane),
		netNewProvIntent + strings.TrimSpace(env.IntentKind),
		netNewProvIntakeSource + strings.TrimSpace(env.IntakeSource),
	}
	if receiptID := normalizeNetNewOpaqueRef(env.ReceiptID); receiptID != "" {
		prov = append(prov, netNewProvRequestReceipt+receiptID)
	}
	if env.Nucleus != "" {
		prov = append(prov, netNewProvNucleus+SanitizeText(env.Nucleus, 80))
	}
	if env.OfferCandidate != "" {
		prov = append(prov, netNewProvOffer+SanitizeText(env.OfferCandidate, 120))
	}
	if env.SourceAsset != "" {
		prov = append(prov, netNewProvAsset+SanitizeText(env.SourceAsset, 120))
	}
	if env.CityClass != "" {
		prov = append(prov, netNewProvCity+SanitizeText(env.CityClass, 40))
	}
	if env.Urgency != "" {
		prov = append(prov, netNewProvUrgency+SanitizeText(env.Urgency, 40))
	}
	prov = append(prov,
		netNewProvQualification+NetNewQualificationState(env),
		netNewProvChannel+netNewPreferredChannel(env),
		netNewProvConflictStatus+strings.ToUpper(SanitizeText(env.Conflict.Status, 40)),
	)
	if outcome != "" {
		prov = append(prov, netNewProvOutcome+outcome)
	}
	if reason != "" {
		prov = append(prov, netNewProvReason+reason)
	}
	row.Provenance = prov
	if env.SensitiveData.Present {
		row.Evidence = []string{NetNewInboundReasonSensitiveRefOnly}
		row.Message = ""
	}
}

func markNetNewDownstreamUnavailable(row *models.OutreachInboundLead, env NetNewInboundEnvelope, reason, pinHash string, now time.Time) {
	if row == nil {
		return
	}
	if strings.TrimSpace(reason) == "" {
		reason = NetNewInboundReasonDownstream
	}
	// Leave enrichment and next-action unset so a later replay can finish.
	row.EnrichmentStatus = models.InboundEnrichmentUnknown
	row.NextAction = ""
	row.Status = models.InboundStatusOpen
	row.UpdatedAt = now
	row.Warnings = appendUnique(row.Warnings, reason)
	setNetNewProvenance(row, env, NetNewInboundOutcomeUnknown, reason, pinHash, now)
}

func netNewDownstreamRetryable(row *models.OutreachInboundLead) bool {
	if row == nil {
		return false
	}
	reason := firstNonEmpty(provenanceValue(row.Provenance, netNewProvReason), row.SuppressReason)
	switch reason {
	case NetNewInboundReasonDownstream, NetNewInboundReasonStoreUnavailable, NetNewInboundReasonStale:
		return true
	}
	if provenanceValue(row.Provenance, netNewProvOutcome) == NetNewInboundOutcomeAccepted && row.ActionID == nil {
		return true
	}
	return false
}

func netNewReceiptComplete(row *models.OutreachInboundLead) bool {
	if row == nil {
		return false
	}
	if netNewDownstreamRetryable(row) {
		return false
	}
	outcome := provenanceValue(row.Provenance, netNewProvOutcome)
	switch outcome {
	case NetNewInboundOutcomeAccepted:
		return row.ActionID != nil
	case NetNewInboundOutcomeRejected, NetNewInboundOutcomeUnknown:
		return true
	}
	return inboundReceiptComplete(row)
}

func provenanceValue(items []string, prefix string) string {
	for _, id := range items {
		if strings.HasPrefix(id, prefix) {
			return strings.TrimPrefix(id, prefix)
		}
	}
	return ""
}

func netNewResultFromLead(row *models.OutreachInboundLead, replay bool) *NetNewInboundResult {
	if row == nil {
		return rejectedNetNew("", NetNewInboundOutcomeUnknown, time.Time{})
	}
	outcome := firstNonEmpty(provenanceValue(row.Provenance, netNewProvOutcome), NetNewInboundOutcomeUnknown)
	reason := firstNonEmpty(provenanceValue(row.Provenance, netNewProvReason), row.SuppressReason)
	ackAt := row.OwnerAssignedAt
	if ts := provenanceValue(row.Provenance, netNewProvAckAt); ts != "" {
		if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
			t := parsed
			ackAt = &t
		}
	}
	res := &NetNewInboundResult{
		Schema:             NetNewInboundHandraiserSchema,
		SchemaVersion:      NetNewInboundDecisionSchema,
		PolicyID:           NetNewInboundContractID,
		PolicyVersion:      NetNewInboundPinVersion,
		CanonicalName:      NetNewInboundHandraiserSchema,
		Hash:               provenanceValue(row.Provenance, netNewProvHash),
		IntakeSchema:       firstNonEmpty(provenanceValue(row.Provenance, netNewProvIntake), NetNewInboundIntakeSchema),
		StateSchema:        firstNonEmpty(provenanceValue(row.Provenance, netNewProvState), NetNewInboundStateSchema),
		LogicalID:          row.LeadID,
		IdempotencyKey:     row.LeadID,
		Receipt:            row.ReceiptID,
		ReceiptID:          provenanceValue(row.Provenance, netNewProvRequestReceipt),
		CorrelationID:      row.CorrelationID,
		Outcome:            outcome,
		Decision:           outcome,
		Reason:             reason,
		ReasonCodes:        netNewGovernanceReasonCodes(outcome, reason),
		Replayed:           replay,
		Origin:             firstNonEmpty(provenanceValue(row.Provenance, netNewProvOrigin), NetNewInboundSource),
		AcquisitionLane:    firstNonEmpty(provenanceValue(row.Provenance, netNewProvLane), "NET_NEW_INBOUND"),
		IntentKind:         firstNonEmpty(provenanceValue(row.Provenance, netNewProvIntent), "HUMAN_REVIEW"),
		IntakeSource:       firstNonEmpty(provenanceValue(row.Provenance, netNewProvIntakeSource), NetNewInboundSource),
		Nucleus:            firstNonEmpty(provenanceValue(row.Provenance, netNewProvNucleus), utmField(row.UTMJSON, "nucleus")),
		OfferCandidate:     firstNonEmpty(provenanceValue(row.Provenance, netNewProvOffer), utmField(row.UTMJSON, "offer_candidate")),
		SourceAsset:        firstNonEmpty(provenanceValue(row.Provenance, netNewProvAsset), row.AssetID),
		CityClass:          firstNonEmpty(provenanceValue(row.Provenance, netNewProvCity), utmField(row.UTMJSON, "city_class")),
		Urgency:            firstNonEmpty(provenanceValue(row.Provenance, netNewProvUrgency), utmField(row.UTMJSON, "urgency")),
		WhyNow:             row.WhyNow,
		ConflictRef:        provenanceValue(row.Provenance, netNewProvConflict),
		ConflictStatus:     firstNonEmpty(provenanceValue(row.Provenance, netNewProvConflictStatus), utmField(row.UTMJSON, "conflict_status")),
		QualificationState: firstNonEmpty(provenanceValue(row.Provenance, netNewProvQualification), utmField(row.UTMJSON, "qualification_state")),
		PreferredChannel:   firstNonEmpty(provenanceValue(row.Provenance, netNewProvChannel), strings.ToUpper(row.Channel)),
		InboundOnly:        outcome == NetNewInboundOutcomeAccepted,
		OutboundEligible:   false,
		AutoSend:           false,
		SMTPAuthorized:     false,
		FollowupAuthorized: false,
		AccountRequired:    false,
		IdentityAuth:       map[bool]string{true: "INBOUND_ONLY", false: "NONE"}[outcome == NetNewInboundOutcomeAccepted],
		DispatchAttempted:  false,
		MeetcfgHandoff:     MeetcfgHandoffAllowed(outcome),
		Replay:             replay,
		AcknowledgedBy:     firstNonEmpty(provenanceValue(row.Provenance, netNewProvAckBy), row.Owner, NetNewInboundAckActor),
		AcknowledgedAt:     ackAt,
		EvaluatedAt:        ackAt,
		AccountID:          row.AccountID,
		ActionID:           row.ActionID,
		CanonicalEntityID:  row.EntityID,
	}
	res.NucleusID = res.Nucleus
	res.OfferCandidateID = res.OfferCandidate
	res.DecisionID = netNewGovernanceDecisionID(row.LeadID, provenanceValue(row.Provenance, netNewProvDigest))
	res.LogicalAdmissionID = res.DecisionID
	return res
}

func rejectedNetNew(logicalID, reason string, now time.Time) *NetNewInboundResult {
	res := &NetNewInboundResult{
		Schema:             NetNewInboundHandraiserSchema,
		SchemaVersion:      NetNewInboundDecisionSchema,
		PolicyID:           NetNewInboundContractID,
		PolicyVersion:      NetNewInboundPinVersion,
		CanonicalName:      NetNewInboundHandraiserSchema,
		LogicalID:          logicalID,
		IdempotencyKey:     logicalID,
		Outcome:            NetNewInboundOutcomeRejected,
		Decision:           NetNewInboundOutcomeRejected,
		Reason:             reason,
		ReasonCodes:        netNewGovernanceReasonCodes(NetNewInboundOutcomeRejected, reason),
		Origin:             NetNewInboundSource,
		AcquisitionLane:    "NET_NEW_INBOUND",
		IntentKind:         "HUMAN_REVIEW",
		IntakeSource:       NetNewInboundSource,
		InboundOnly:        false,
		OutboundEligible:   false,
		AutoSend:           false,
		SMTPAuthorized:     false,
		FollowupAuthorized: false,
		AccountRequired:    false,
		IdentityAuth:       "NONE",
		DispatchAttempted:  false,
		MeetcfgHandoff:     false,
		AcknowledgedBy:     NetNewInboundAckActor,
	}
	if !now.IsZero() {
		t := now.UTC()
		res.AcknowledgedAt = &t
		res.EvaluatedAt = &t
	}
	return res
}

func netNewGovernanceDecisionID(logicalID, materialHash string) string {
	if logicalID == "" || materialHash == "" {
		return ""
	}
	basis := map[string]string{
		"policy_id":       NetNewInboundContractID,
		"policy_version":  NetNewInboundPinVersion,
		"idempotency_key": logicalID,
		"material_hash":   materialHash,
	}
	raw, _ := marshalCanonicalNetNewJSON(basis)
	sum := sha256.Sum256(raw)
	return "nihr_" + hex.EncodeToString(sum[:])[:32]
}

func netNewGovernanceReasonCodes(outcome, reason string) []string {
	if outcome == NetNewInboundOutcomeAccepted {
		return []string{"ADMISSION_GATES_SATISFIED"}
	}
	code := map[string]string{
		NetNewInboundReasonConsent:          "CONSENT_REFUSED",
		NetNewInboundReasonOptOut:           "OPT_OUT_PRESENT",
		NetNewInboundReasonFuzzyIdentity:    "FUZZY_IDENTITY_FORBIDDEN",
		NetNewInboundReasonIntent:           "INTENT_KIND_NOT_ADMITTED",
		NetNewInboundReasonLocation:         "LOCATION_NOT_MINIMIZED",
		NetNewInboundReasonSensitiveContent: "SENSITIVE_CONTENT_FORBIDDEN",
		NetNewInboundReasonConflictCoercion: "CONFLICT_CLEAR_COERCION_FORBIDDEN",
		NetNewInboundReasonKeyConflict:      "IDEMPOTENCY_PAYLOAD_CONFLICT",
		NetNewInboundReasonConflictDecline:  "CONFLICT_HIT",
		NetNewInboundReasonConflictHit:      "CONFLICT_HIT",
		NetNewInboundReasonOutboundClaim:    "OUTBOUND_INHERITANCE_FORBIDDEN",
		NetNewInboundReasonAutoSendClaim:    "AUTO_SEND_FORBIDDEN",
		NetNewInboundReasonContactUnknown:   "CONTACT_EVIDENCE_UNKNOWN",
		NetNewInboundReasonContact:          "CONTACT_EVIDENCE_UNKNOWN",
		NetNewInboundReasonPreferredChannel: "CONTACT_EVIDENCE_UNKNOWN",
		NetNewInboundReasonNucleus:          "NUCLEUS_NOT_ADMITTED",
		NetNewInboundReasonOfferCandidate:   "OFFER_CANDIDATE_NOT_ADMITTED",
		NetNewInboundReasonLane:             "ACQUISITION_LANE_NOT_ADMITTED",
		NetNewInboundReasonSource:           "ORIGIN_NOT_ADMITTED",
		NetNewInboundReasonIntelWatch:       "LIVE_INTELLIGENCE_NOT_INBOUND",
		NetNewInboundReasonSchemaMismatch:   "POLICY_VERSION_NOT_ADMITTED",
		NetNewInboundReasonContractMismatch: "POLICY_ID_UNKNOWN",
		NetNewInboundReasonHashMismatch:     "POLICY_VERSION_NOT_ADMITTED",
		NetNewInboundReasonHashUnpinned:     "AUTHORITY_UNAVAILABLE",
		NetNewInboundReasonDownstream:       "AUTHORITY_UNAVAILABLE",
		NetNewInboundReasonRequestInvalid:   "REQUEST_INVALID",
	}
	if value := code[reason]; value != "" {
		return []string{value}
	}
	return []string{"REQUEST_INVALID"}
}

func (s *service) observeNetNewMetric(res *NetNewInboundResult) {
	if res == nil {
		return
	}
	metric := NetNewInboundMetric{Nucleus: res.Nucleus, State: res.Outcome, Reason: res.Reason}
	if s == nil || s.netNewMetricSink == nil {
		return
	}
	func() {
		defer func() { _ = recover() }()
		s.netNewMetricSink(metric)
	}()
}

// FirstTouchEligibleAccount observes the existing first-touch sendable
// predicates without changing them. Inbound-only is never eligible.
func FirstTouchEligibleAccount(acc *models.OutreachAccount) bool {
	return accountOutboundEligible(acc)
}

func (s *service) netNewInFirstTouchEligibleSet(ctx context.Context, orgID uuid.UUID, accountID uuid.UUID) bool {
	if s == nil || s.repo == nil || accountID == uuid.Nil {
		return false
	}
	acc, err := s.repo.GetAccount(ctx, orgID, accountID)
	if err != nil || acc == nil {
		return false
	}
	if FirstTouchEligibleAccount(acc) {
		return true
	}
	listed, listErr := s.repo.ListAccounts(ctx, orgID, repository.OutreachAccountFilter{Limit: 500})
	if listErr != nil {
		return false
	}
	for i := range listed {
		if listed[i].ID == accountID && FirstTouchEligibleAccount(&listed[i]) {
			return true
		}
	}
	return false
}
