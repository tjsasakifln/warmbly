package confenge

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// CONFENGE_WEB intent intake.
//
// One envelope, four intents, two destinations. MONITOR_* records standing
// consent as an INTEL_WATCH subscription. REQUEST_* converges through
// ConvergeHandRaise onto the same next-action surface the other engines land
// on, stamped with the confenge_web engine lane.
//
// Validation is fail-closed and whole-envelope: a rejected body creates
// nothing, not even the half of it that was well formed.

// Machine-readable web-intent rejection reasons. They are returned as the
// response `code` so a caller can branch without parsing prose.
const (
	WebIntentReasonSchema         = "confenge_web_intent_schema_mismatch"
	WebIntentReasonKindUnknown    = "confenge_web_intent_kind_unknown"
	WebIntentReasonLane           = "confenge_web_intent_lane_not_confenge_web"
	WebIntentReasonEmailMissing   = "confenge_web_intent_contact_email_missing"
	WebIntentReasonCompanyMissing = "confenge_web_intent_company_ref_missing"
	WebIntentReasonOpportunity    = "confenge_web_intent_opportunity_id_missing"
	WebIntentReasonKeyIsCNPJ      = "confenge_web_intent_correlation_key_is_cnpj"
	WebIntentReasonKeyIsShare     = "confenge_web_intent_correlation_key_is_share_token"
	WebIntentReasonConsent        = "confenge_web_intent_consent_provenance_missing"
	WebIntentReasonCadence        = "confenge_web_intent_cadence_unknown"
	WebIntentReasonNoStore        = "confenge_web_intent_store_unavailable"
)

// Subject-key prefixes. This intake produces these two shapes and no other:
// a free-form subject key would let a caller aim watch mail at any string.
const (
	WebIntentSubjectCompanyPrefix     = "company:"
	WebIntentSubjectOpportunityPrefix = "opportunity:"
)

// webIntentDefaultWatchIntent is the subscription trigger a "watch this"
// request maps to. The web envelope names the subject, not the trigger event,
// and FIT_BECAME_RELEVANT is the only member of the closed set that does not
// assert which kind of change the watcher asked for.
const webIntentDefaultWatchIntent = models.IntelWatchIntentFitBecameRelevant

// IntelWatchSubscriptionStore is the narrow subscription surface web intake
// needs. The repository satisfies it; a test fake satisfies it just as well.
type IntelWatchSubscriptionStore interface {
	CreateOrReactivateSubscription(ctx context.Context, sub *models.IntelWatchSubscription) (*models.IntelWatchSubscription, error)
}

// WebIntentResult is what one accepted envelope did.
type WebIntentResult struct {
	Schema     string `json:"schema"`
	IntentKind string `json:"intent_kind"`
	EngineLane string `json:"engine_lane"`
	SubjectKey string `json:"subject_key,omitempty"`
	// SubscriptionID is set for MONITOR_*; ActionID for REQUEST_*. Never both.
	SubscriptionID *uuid.UUID `json:"subscription_id,omitempty"`
	ActionID       *uuid.UUID `json:"action_id,omitempty"`
	// Matched reports whether the contact email resolved to a known account.
	// An unmatched REQUEST_* cannot converge, because a commercial action is
	// filed against an account; saying so is better than inventing one.
	Matched bool   `json:"matched"`
	Reason  string `json:"reason,omitempty"`
}

// WebIntentSubjectKey canonicalizes the subject a web intent points at. It is
// the only producer of subject keys on this path.
func WebIntentSubjectKey(intentKind, companyRef, opportunityID string) (string, *errx.Error) {
	company := strings.TrimSpace(companyRef)
	opportunity := strings.TrimSpace(opportunityID)
	switch strings.ToUpper(strings.TrimSpace(intentKind)) {
	case intel.WebIntentMonitorOpportunity:
		if xerr := assertCorrelationKey(opportunity, WebIntentReasonOpportunity); xerr != nil {
			return "", xerr
		}
		return WebIntentSubjectOpportunityPrefix + opportunity, nil
	case intel.WebIntentMonitorCompany, intel.WebIntentRequestDeepDive, intel.WebIntentRequestHumanReview:
		if xerr := assertCorrelationKey(company, WebIntentReasonCompanyMissing); xerr != nil {
			return "", xerr
		}
		return WebIntentSubjectCompanyPrefix + company, nil
	default:
		return "", errx.NewWithIdentifier(errx.BadRequest, WebIntentReasonKindUnknown,
			"unknown confenge web intent_kind")
	}
}

// assertCorrelationKey applies the three rules a correlation key must pass.
func assertCorrelationKey(value, missingReason string) *errx.Error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errx.NewWithIdentifier(errx.BadRequest, missingReason,
			"this intent_kind requires a correlation key")
	}
	if RejectsAsCNPJ(value) {
		return errx.NewWithIdentifier(errx.BadRequest, WebIntentReasonKeyIsCNPJ,
			"a CNPJ is never accepted as a correlation key")
	}
	if IsShareToken(value) {
		return errx.NewWithIdentifier(errx.BadRequest, WebIntentReasonKeyIsShare,
			"a share token is never accepted as a correlation key")
	}
	return nil
}

// ValidateWebIntent applies every envelope rule. It returns the canonical
// subject key so a caller cannot derive a second one.
func ValidateWebIntent(env intel.WebIntentEnvelope) (string, *errx.Error) {
	if strings.TrimSpace(env.Schema) != intel.WebIntentSchemaV1 {
		return "", errx.NewWithIdentifier(errx.BadRequest, WebIntentReasonSchema,
			"expected schema "+intel.WebIntentSchemaV1)
	}
	kind := strings.ToUpper(strings.TrimSpace(env.IntentKind))
	if !containsStr(intel.WebIntentKinds, kind) {
		return "", errx.NewWithIdentifier(errx.BadRequest, WebIntentReasonKindUnknown,
			"unknown confenge web intent_kind")
	}
	// The lane is ours, not the caller's. It is verified rather than read.
	if strings.TrimSpace(env.Lane) != EngineLaneConfengeWeb {
		return "", errx.NewWithIdentifier(errx.BadRequest, WebIntentReasonLane,
			"lane must be "+EngineLaneConfengeWeb)
	}
	if normalizeWebIntentEmail(env.ContactEmail) == "" {
		return "", errx.NewWithIdentifier(errx.BadRequest, WebIntentReasonEmailMissing,
			"contact_email is required")
	}
	// Both correlation fields are screened, not only the one this intent uses:
	// a rejected value must not ride along on the envelope into storage.
	for _, candidate := range []string{env.CompanyRef, env.OpportunityID} {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if RejectsAsCNPJ(candidate) {
			return "", errx.NewWithIdentifier(errx.BadRequest, WebIntentReasonKeyIsCNPJ,
				"a CNPJ is never accepted as a correlation key")
		}
		if IsShareToken(candidate) {
			return "", errx.NewWithIdentifier(errx.BadRequest, WebIntentReasonKeyIsShare,
				"a share token is never accepted as a correlation key")
		}
	}
	subject, xerr := WebIntentSubjectKey(kind, env.CompanyRef, env.OpportunityID)
	if xerr != nil {
		return "", xerr
	}
	if webIntentCreatesSubscription(kind) {
		// Only the MONITOR_* intents put an address on a standing mail list, so
		// only they need consent provenance. A REQUEST_* is answered by a human.
		if strings.TrimSpace(env.ConsentSource) == "" || env.ConsentAt == nil || !env.ConsentProvenanceOK {
			return "", errx.NewWithIdentifier(errx.BadRequest, WebIntentReasonConsent,
				"consent_source, consent_at and consent_provenance_ok are required to start a watch")
		}
		if cadence := strings.TrimSpace(env.Cadence); cadence != "" && !containsStr(models.IntelWatchCadences, cadence) {
			return "", errx.NewWithIdentifier(errx.BadRequest, WebIntentReasonCadence,
				"unknown cadence")
		}
	}
	return subject, nil
}

func webIntentCreatesSubscription(kind string) bool {
	return kind == intel.WebIntentMonitorCompany || kind == intel.WebIntentMonitorOpportunity
}

func normalizeWebIntentEmail(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || !strings.Contains(email, "@") || strings.ContainsAny(email, " \t\r\n") {
		return ""
	}
	return email
}

// webIntentSignal maps a REQUEST_* intent onto its hand-raise signal. The
// strings are identical on purpose, so this is a membership check, not a
// translation.
func webIntentSignal(kind string) (HandRaiseSignal, bool) {
	switch kind {
	case intel.WebIntentRequestDeepDive:
		return SignalRequestDeepDive, true
	case intel.WebIntentRequestHumanReview:
		return SignalRequestHumanReview, true
	default:
		return "", false
	}
}

// WireIntelWatchSubscriptions installs the subscription store CONFENGE_WEB
// intake writes through. It is wired independently of the INTEL_WATCH delivery
// lane: recording consent must work even while delivery is dormant.
func (s *service) WireIntelWatchSubscriptions(store IntelWatchSubscriptionStore) {
	if s == nil {
		return
	}
	s.intelWatchSubs = store
}

// IngestWebIntent validates one CONFENGE_WEB envelope and routes it.
//
// orgID is the organization Warmbly's own webhook auth resolved. Nothing on
// the envelope can change it: an external caller must not be able to reach
// another organization's watch list by naming it in the body.
func (s *service) IngestWebIntent(ctx context.Context, orgID uuid.UUID, env intel.WebIntentEnvelope, now time.Time) (*WebIntentResult, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	if orgID == uuid.Nil {
		return nil, errx.New(errx.ServiceUnavailable, "inbound org is not configured")
	}
	subject, xerr := ValidateWebIntent(env)
	if xerr != nil {
		return nil, xerr
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	kind := strings.ToUpper(strings.TrimSpace(env.IntentKind))
	email := normalizeWebIntentEmail(env.ContactEmail)
	result := &WebIntentResult{
		Schema: intel.WebIntentSchemaV1, IntentKind: kind,
		EngineLane: EngineLaneConfengeWeb, SubjectKey: subject,
	}

	// The account and contact are looked up, never created. A web intent is
	// evidence about a person; it is not authority to add them to the book.
	var cand *models.OutreachContactCandidate
	var acc *models.OutreachAccount
	if s.repo != nil {
		found, account, err := s.repo.FindCandidateByEmail(ctx, orgID, email)
		if err != nil {
			return nil, errx.New(errx.Internal, "confenge web intent contact lookup: "+err.Error())
		}
		cand, acc = found, account
	}
	result.Matched = acc != nil

	if webIntentCreatesSubscription(kind) {
		return s.webIntentSubscribe(ctx, orgID, env, subject, email, cand, acc, result)
	}
	return s.webIntentHandRaise(ctx, orgID, kind, env, cand, acc, now, result)
}

func (s *service) webIntentSubscribe(ctx context.Context, orgID uuid.UUID, env intel.WebIntentEnvelope, subject, email string,
	cand *models.OutreachContactCandidate, acc *models.OutreachAccount, result *WebIntentResult) (*WebIntentResult, *errx.Error) {
	if s.intelWatchSubs == nil {
		return nil, errx.NewWithIdentifier(errx.ServiceUnavailable, WebIntentReasonNoStore,
			"intel watch subscription store is not wired")
	}
	cadence := strings.TrimSpace(env.Cadence)
	if cadence == "" {
		cadence = models.IntelWatchCadenceImmediate
	}
	sub := &models.IntelWatchSubscription{
		OrganizationID: orgID, ContactEmail: email,
		IntentKind: webIntentDefaultWatchIntent, SubjectKey: subject,
		Topic: SanitizeText(env.Topic, 300), Cadence: cadence,
		ConsentSource: SanitizeText(env.ConsentSource, 200),
		ConsentAt:     env.ConsentAt, ConsentProvenanceOK: env.ConsentProvenanceOK,
	}
	if cand != nil {
		id := cand.ID
		sub.ContactID = &id
	}
	if acc != nil {
		id := acc.ID
		sub.AccountID = &id
	}
	saved, err := s.intelWatchSubs.CreateOrReactivateSubscription(ctx, sub)
	if err != nil {
		return nil, errx.New(errx.Internal, "confenge web intent subscription: "+err.Error())
	}
	if saved != nil {
		id := saved.ID
		result.SubscriptionID = &id
	}
	return result, nil
}

func (s *service) webIntentHandRaise(ctx context.Context, orgID uuid.UUID, kind string, env intel.WebIntentEnvelope,
	cand *models.OutreachContactCandidate, acc *models.OutreachAccount, now time.Time, result *WebIntentResult) (*WebIntentResult, *errx.Error) {
	signal, ok := webIntentSignal(kind)
	if !ok {
		return nil, errx.NewWithIdentifier(errx.BadRequest, WebIntentReasonKindUnknown,
			"unknown confenge web intent_kind")
	}
	if acc == nil {
		// A commercial action is filed against an account. Without one there is
		// nothing to file, and inventing an account would be worse than saying so.
		result.Reason = "contact_not_matched_to_account"
		return result, nil
	}
	raise := HandRaise{
		OrganizationID: orgID, AccountID: acc.ID,
		Signal: signal, EngineLane: EngineLaneConfengeWeb, OccurredAt: now,
		SubjectKey: result.SubjectKey,
		Evidence:   SanitizeText(firstNonEmpty(env.Evidence, env.Topic), 500),
		HumanNotes: SanitizeText(env.Notes, 1000),
	}
	if env.OccurredAt != nil && !env.OccurredAt.IsZero() {
		raise.OccurredAt = env.OccurredAt.UTC()
	}
	if cand != nil {
		id := cand.ID
		raise.CandidateID = &id
		raise.PersonName = strings.TrimSpace(cand.Name)
	}
	if name := strings.TrimSpace(env.ContactName); raise.PersonName == "" && name != "" {
		raise.PersonName = SanitizeText(name, 120)
	}
	action, xerr := s.PersistHandRaise(ctx, raise)
	if xerr != nil {
		return nil, xerr
	}
	if action != nil {
		id := action.ID
		result.ActionID = &id
	}
	return result, nil
}
