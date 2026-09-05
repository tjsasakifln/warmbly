package confenge

import (
	"context"
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
	Schema            string     `json:"schema"`
	PolicyVersion     string     `json:"policy_version"`
	Hash              string     `json:"hash,omitempty"`
	IntakeSchema      string     `json:"intake_schema,omitempty"`
	StateSchema       string     `json:"state_schema,omitempty"`
	LogicalID         string     `json:"logical_id"`
	Receipt           string     `json:"receipt"`
	CorrelationID     string     `json:"correlation_id,omitempty"`
	Outcome           string     `json:"outcome"`
	Reason            string     `json:"reason,omitempty"`
	Nucleus           string     `json:"nucleus,omitempty"`
	OfferCandidate    string     `json:"offer_candidate,omitempty"`
	SourceAsset       string     `json:"source_asset,omitempty"`
	CityClass         string     `json:"city_class,omitempty"`
	Urgency           string     `json:"urgency,omitempty"`
	WhyNow            string     `json:"why_now,omitempty"`
	ConflictRef       string     `json:"conflict_ref,omitempty"`
	InboundOnly       bool       `json:"inbound_only"`
	OutboundEligible  bool       `json:"outbound_eligible"`
	AutoSend          bool       `json:"auto_send"`
	DispatchAttempted bool       `json:"dispatch_attempted"`
	MeetcfgHandoff    bool       `json:"meetcfg_handoff_allowed"`
	Replay            bool       `json:"replay"`
	AcknowledgedBy    string     `json:"acknowledged_by,omitempty"`
	AcknowledgedAt    *time.Time `json:"acknowledged_at,omitempty"`
	AccountID         *uuid.UUID `json:"account_id,omitempty"`
	ActionID          *uuid.UUID `json:"action_id,omitempty"`
	CanonicalEntityID string     `json:"canonical_entity_id,omitempty"`
	Reconciled        bool       `json:"reconciled,omitempty"`
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

	row := netNewReceiptRow(orgID, env, raw, pinHash, now)
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
		raise := HandRaise{
			OrganizationID: orgID, AccountID: admitted.Account.ID,
			Signal: SignalRequestHumanReview, EngineLane: EngineLaneConfengeWeb, OccurredAt: now,
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
		row.Channel = "human_review"
		row.WhyNow = SanitizeText(env.WhyNow, 500)
	}

	row.UpdatedAt = now
	if err := st.UpdateInboundLead(ctx, row); err != nil {
		return nil, errx.New(errx.Internal, "update inbound lead: "+err.Error())
	}
	s.ensureOperatorAlert(ctx, orgID, row, now)
	res := netNewResultFromLead(row, replay)
	if admitted != nil {
		res.Reconciled = admitted.Reused
		res.CanonicalEntityID = netNewCanonicalEntityID(env)
		res.InboundOnly = true
		res.OutboundEligible = false
		if admitted.Account != nil {
			res.OutboundEligible = accountOutboundEligible(admitted.Account)
			res.InboundOnly = models.AccountIsInboundOnly(admitted.Account) || admitted.InboundOnly
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
	logicalID = SanitizeText(logicalID, 160)
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
	email := normalizeWebIntentEmail(env.Person.Email)
	name := firstNonEmpty(SanitizeText(env.Person.Name, 120), SanitizeText(env.Company.Name, 120))
	contextText := netNewContext(env)

	if canonical != "" {
		if acc, err := s.repo.GetAccountBySourceLeadID(ctx, orgID, canonical); err != nil {
			return nil, errx.NewWithIdentifier(errx.Internal, NetNewInboundReasonDownstream,
				"canonical entity lookup: "+err.Error())
		} else if acc != nil {
			out := &InboundAdmission{
				Account: acc, Origin: EngineLaneConfengeWeb, Lane: EngineLaneConfengeWeb,
				IntentKind: intelWebIntentHumanReview(), Context: contextText,
				Receipt: InboundReceiptID(EngineLaneConfengeWeb, intelWebIntentHumanReview(), firstNonEmpty(email, logicalID), canonical),
				Reused:  true, InboundOnly: models.AccountIsInboundOnly(acc),
				OutboundEligible: accountOutboundEligible(acc),
			}
			if email != "" {
				created, xerr := s.upsertInboundCandidate(ctx, orgID, acc.ID, email, name, now)
				if xerr != nil {
					return nil, xerr
				}
				out.Candidate = created
			}
			return out, nil
		}
	}

	// Never merge on display name. Identity is logical_id (and canonical ID).
	key := inboundOnlyLeadIDPrefix + "confenge_web:NET_NEW:" + logicalID
	return s.AdmitInboundOnly(ctx, orgID, EngineLaneConfengeWeb, "NET_NEW_INBOUND_HANDRAISER",
		firstNonEmpty(email, logicalID+"@inbound.invalid"), name, key, contextText, now)
}

func intelWebIntentHumanReview() string {
	return "REQUEST_HUMAN_REVIEW"
}

func netNewContext(env NetNewInboundEnvelope) string {
	return SanitizeText(strings.Join(filterEmpty([]string{
		env.Nucleus, env.OfferCandidate, env.SourceAsset, env.CityClass, env.Urgency, env.WhyNow,
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

func netNewReceiptRow(orgID uuid.UUID, env NetNewInboundEnvelope, raw []byte, pinHash string, now time.Time) *models.OutreachInboundLead {
	logicalID := netNewLogicalID(env)
	payload := raw
	if env.SensitiveData {
		redacted, _ := json.Marshal(map[string]any{
			"redacted": true, "logical_id": logicalID, "schema": NetNewInboundHandraiserSchema,
		})
		payload = redacted
	}
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	receipt := InboundReceiptID(EngineLaneConfengeWeb, "NET_NEW_INBOUND_HANDRAISER",
		firstNonEmpty(normalizeWebIntentEmail(env.Person.Email), logicalID), logicalID)
	row := &models.OutreachInboundLead{
		ID:                uuid.New(),
		OrganizationID:    orgID,
		LeadID:            logicalID,
		ReceiptID:         receipt,
		IdentityKey:       inboundIdentityKey("", normalizeWebIntentEmail(env.Person.Email), ""),
		LeadCreatedAt:     now,
		WarmblyIngestedAt: now,
		Source:            NetNewInboundSource,
		RouteFamily:       "inbound",
		AssetID:           SanitizeText(firstNonEmpty(env.SourceAsset, NetNewInboundSourceAsset), 120),
		CTAID:             SanitizeText(env.OfferCandidate, 120),
		EntityID:          SanitizeText(netNewCanonicalEntityID(env), 120),
		CompanyName:       SanitizeText(env.Company.Name, 200),
		LeadName:          SanitizeText(env.Person.Name, 160),
		LeadEmail:         normalizeWebIntentEmail(env.Person.Email),
		CorrelationID:     SanitizeText(env.CorrelationID, 160),
		ConsentJSON:       []byte(`{"granted":true}`),
		UTMJSON:           netNewUTM(env),
		RawPayload:        payload,
		EnrichmentStatus:  models.InboundEnrichmentUnknown,
		Owner:             NetNewInboundAckActor,
		Status:            models.InboundStatusOpen,
		WhyNow:            SanitizeText(env.WhyNow, 500),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if env.SensitiveData {
		row.LeadName = ""
		row.LeadEmail = ""
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

func netNewUTM(env NetNewInboundEnvelope) []byte {
	m := map[string]string{
		"nucleus":         SanitizeText(env.Nucleus, 80),
		"offer_candidate": SanitizeText(firstNonEmpty(env.OfferCandidate, NetNewInboundOfferCandidate), 120),
		"source_asset":    SanitizeText(firstNonEmpty(env.SourceAsset, NetNewInboundSourceAsset), 120),
		"city_class":      SanitizeText(env.CityClass, 40),
		"urgency":         SanitizeText(env.Urgency, 40),
		"policy":          NetNewInboundHandraiserSchema,
		"intake_schema":   NetNewInboundIntakeSchema,
	}
	if ref := NetNewConflictRef(env.Conflict); ref != "" {
		m["conflict_ref"] = ref
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
		netNewProvIntake + NetNewInboundIntakeSchema,
		netNewProvState + NetNewInboundStateSchema,
		netNewProvHash + normalizeContentHash(pinHash),
		netNewProvDigest + NetNewAdmissionDigest(env),
		netNewProvAckBy + NetNewInboundAckActor,
		netNewProvAckAt + now.UTC().Format(time.RFC3339),
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
	if ref := NetNewConflictRef(env.Conflict); ref != "" {
		prov = append(prov, netNewProvConflict+ref)
	}
	if outcome != "" {
		prov = append(prov, netNewProvOutcome+outcome)
	}
	if reason != "" {
		prov = append(prov, netNewProvReason+reason)
	}
	row.Provenance = prov
	if env.SensitiveData {
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
		Schema:            NetNewInboundHandraiserSchema,
		PolicyVersion:     firstNonEmpty(provenanceValue(row.Provenance, netNewProvPolicy), NetNewInboundHandraiserSchema),
		Hash:              provenanceValue(row.Provenance, netNewProvHash),
		IntakeSchema:      firstNonEmpty(provenanceValue(row.Provenance, netNewProvIntake), NetNewInboundIntakeSchema),
		StateSchema:       firstNonEmpty(provenanceValue(row.Provenance, netNewProvState), NetNewInboundStateSchema),
		LogicalID:         row.LeadID,
		Receipt:           row.ReceiptID,
		CorrelationID:     row.CorrelationID,
		Outcome:           outcome,
		Reason:            reason,
		Nucleus:           firstNonEmpty(provenanceValue(row.Provenance, netNewProvNucleus), utmField(row.UTMJSON, "nucleus")),
		OfferCandidate:    firstNonEmpty(provenanceValue(row.Provenance, netNewProvOffer), utmField(row.UTMJSON, "offer_candidate")),
		SourceAsset:       firstNonEmpty(provenanceValue(row.Provenance, netNewProvAsset), row.AssetID),
		CityClass:         firstNonEmpty(provenanceValue(row.Provenance, netNewProvCity), utmField(row.UTMJSON, "city_class")),
		Urgency:           firstNonEmpty(provenanceValue(row.Provenance, netNewProvUrgency), utmField(row.UTMJSON, "urgency")),
		WhyNow:            row.WhyNow,
		ConflictRef:       provenanceValue(row.Provenance, netNewProvConflict),
		InboundOnly:       true,
		OutboundEligible:  false,
		AutoSend:          false,
		DispatchAttempted: false,
		MeetcfgHandoff:    MeetcfgHandoffAllowed(outcome),
		Replay:            replay,
		AcknowledgedBy:    firstNonEmpty(provenanceValue(row.Provenance, netNewProvAckBy), row.Owner, NetNewInboundAckActor),
		AcknowledgedAt:    ackAt,
		AccountID:         row.AccountID,
		ActionID:          row.ActionID,
		CanonicalEntityID: row.EntityID,
	}
	return res
}

func rejectedNetNew(logicalID, reason string, now time.Time) *NetNewInboundResult {
	res := &NetNewInboundResult{
		Schema:            NetNewInboundHandraiserSchema,
		PolicyVersion:     NetNewInboundHandraiserSchema,
		LogicalID:         logicalID,
		Outcome:           NetNewInboundOutcomeRejected,
		Reason:            reason,
		InboundOnly:       false,
		OutboundEligible:  false,
		AutoSend:          false,
		DispatchAttempted: false,
		MeetcfgHandoff:    false,
		AcknowledgedBy:    NetNewInboundAckActor,
	}
	if !now.IsZero() {
		t := now.UTC()
		res.AcknowledgedAt = &t
	}
	return res
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
