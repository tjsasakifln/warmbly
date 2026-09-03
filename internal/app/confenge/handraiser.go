package confenge

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

// Hand-raiser convergence.
//
// Four engines can produce the same commercial fact: a human raised their hand.
// They must land in ONE place a founder already looks -- the existing
// OutreachCommercialAction next-action surface -- while still saying which
// engine produced them, because an aggregate that hides which engine is working
// is worse than no number at all.
//
// This is explicit signal-to-action mapping. There is no scoring, no ranking
// model and no hidden behavioural inference: every input below is a signal a
// human or an existing classifier already asserted.

// HandRaiseSignal is the closed set of things that mean "a human wants
// something from us". Each maps to exactly one next action.
type HandRaiseSignal string

const (
	// SignalPositiveReplyFirstTouch: a positive reply to OUTBOUND_FIRST_TOUCH.
	SignalPositiveReplyFirstTouch HandRaiseSignal = "POSITIVE_REPLY_FIRST_TOUCH"
	// SignalIntelSeedResponse: a reply or action on an INTEL_SEED offer.
	SignalIntelSeedResponse HandRaiseSignal = "INTEL_SEED_RESPONSE"
	// SignalRequestHumanReview: an explicit ask for a person to look.
	SignalRequestHumanReview HandRaiseSignal = "REQUEST_HUMAN_REVIEW"
	// SignalRequestDeepDive: an explicit ask for deeper analysis.
	SignalRequestDeepDive HandRaiseSignal = "REQUEST_DEEP_DIVE"
	// SignalMeetingOrProposalRequest: an explicit ask to meet or to be quoted.
	SignalMeetingOrProposalRequest HandRaiseSignal = "MEETING_OR_PROPOSAL_REQUEST"
	// SignalInferredEmailReview is the sixth signal, already recognised by the
	// existing model as ActionInferredEmailReview / LaneHumanReviewEmail: a
	// route the classifier will not send to without a human deciding first.
	SignalInferredEmailReview HandRaiseSignal = "INFERRED_EMAIL_REVIEW"
)

// HandRaiseSignals is the closed set in declaration order.
var HandRaiseSignals = []HandRaiseSignal{
	SignalPositiveReplyFirstTouch,
	SignalIntelSeedResponse,
	SignalRequestHumanReview,
	SignalRequestDeepDive,
	SignalMeetingOrProposalRequest,
	SignalInferredEmailReview,
}

// Engine lanes. This vocabulary is deliberately separate from the cockpit's
// operational Lane (EMAIL_NEEDS_REVIEW, INBOUND_NOW, ...) and from the intel
// package's RouteFamily (inbound/outbound/partner/customer_expansion). It
// answers only "which acquisition engine produced this".
const (
	EngineLaneFirstTouch  = "outbound_first_touch"
	EngineLaneIntelSeed   = "intel_seed"
	EngineLaneIntelWatch  = "intel_watch"
	EngineLaneConfengeWeb = "confenge_web"
	// EngineLaneUnattributed is the honest answer for a signal that predates
	// engine attribution. It is never guessed at.
	EngineLaneUnattributed = ""
)

// EngineLanes is the closed set of attributable engines.
var EngineLanes = []string{
	EngineLaneFirstTouch, EngineLaneIntelSeed, EngineLaneIntelWatch, EngineLaneConfengeWeb,
}

// NormalizeEngineLane maps a raw value onto the closed set. Anything unknown
// becomes unattributed rather than being coerced into an engine: a wrong
// attribution is worse than a missing one, because it makes a working engine
// look like it produced someone else's result.
func NormalizeEngineLane(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case EngineLaneFirstTouch:
		return EngineLaneFirstTouch
	case EngineLaneIntelSeed:
		return EngineLaneIntelSeed
	case EngineLaneIntelWatch:
		return EngineLaneIntelWatch
	case EngineLaneConfengeWeb:
		return EngineLaneConfengeWeb
	default:
		return EngineLaneUnattributed
	}
}

// HandRaise is one converged signal, before it becomes an action row.
type HandRaise struct {
	OrganizationID uuid.UUID
	AccountID      uuid.UUID
	CandidateID    *uuid.UUID
	Signal         HandRaiseSignal
	// EngineLane is the engine of origin. Callers pass what they know; an
	// unknown value normalizes to unattributed rather than to a default engine.
	EngineLane string
	OccurredAt time.Time
	// Evidence is what a human can read to check the mapping. Free text, and
	// deliberately not parsed into a decision.
	Evidence   string
	PersonName string
	HumanNotes string
}

// handRaiseMapping is the whole policy: signal -> (next action, cockpit lane,
// outcome code, urgency). Nothing else decides where a hand-raiser lands.
type handRaiseMapping struct {
	NextActionType string
	CockpitLane    string
	ActionType     string
	OutcomeCode    string
	// Within is how long the founder has before this is late. It becomes
	// NextActionAt; it is a commitment, not a prediction.
	Within time.Duration
}

func mappingFor(signal HandRaiseSignal) (handRaiseMapping, bool) {
	switch signal {
	case SignalMeetingOrProposalRequest:
		// The strongest ask there is. Same day.
		return handRaiseMapping{
			NextActionType: models.OutcomeMeetingScheduled, CockpitLane: models.LaneInboundNow,
			ActionType: models.ActionOtherManual, OutcomeCode: models.OutcomeQualifiedConversation,
			Within: 4 * time.Hour,
		}, true
	case SignalPositiveReplyFirstTouch, SignalIntelSeedResponse:
		// Someone answered a cold touch positively. This is the qualified
		// conversation the whole outbound engine exists to produce.
		return handRaiseMapping{
			NextActionType: models.OutcomeInterested, CockpitLane: models.LaneInboundNow,
			ActionType: models.ActionOtherManual, OutcomeCode: models.OutcomeQualifiedConversation,
			Within: 8 * time.Hour,
		}, true
	case SignalRequestDeepDive:
		return handRaiseMapping{
			NextActionType: models.OutcomeFollowUp, CockpitLane: models.LaneInboundNow,
			ActionType: models.ActionOtherManual, OutcomeCode: models.OutcomeQualifiedConversation,
			Within: 24 * time.Hour,
		}, true
	case SignalRequestHumanReview:
		return handRaiseMapping{
			NextActionType: models.OutcomeFollowUp, CockpitLane: models.LaneEmailNeedsReview,
			ActionType: models.ActionOtherManual, OutcomeCode: models.OutcomeContactedCode,
			Within: 24 * time.Hour,
		}, true
	case SignalInferredEmailReview:
		// Already recognised by the existing model: a route no engine may send
		// to until a human decides.
		return handRaiseMapping{
			NextActionType: models.OutcomeFollowUp, CockpitLane: models.LaneHumanReviewEmail,
			ActionType: models.ActionInferredEmailReview, OutcomeCode: "",
			Within: 48 * time.Hour,
		}, true
	default:
		return handRaiseMapping{}, false
	}
}

// ConvergeHandRaise turns one signal into one commercial action carrying a
// next action, its due time and its engine of origin.
//
// It returns nil for an unknown signal rather than inventing a mapping: an
// unrecognised signal must stay visible as unhandled, not be quietly filed.
func ConvergeHandRaise(raise HandRaise) *models.OutreachCommercialAction {
	mapping, ok := mappingFor(raise.Signal)
	if !ok {
		return nil
	}
	if raise.OrganizationID == uuid.Nil || raise.AccountID == uuid.Nil {
		return nil
	}
	occurred := raise.OccurredAt.UTC()
	if occurred.IsZero() {
		occurred = time.Now().UTC()
	}
	due := occurred.Add(mapping.Within)
	action := &models.OutreachCommercialAction{
		ID:             uuid.New(),
		OrganizationID: raise.OrganizationID,
		AccountID:      raise.AccountID,
		CandidateID:    raise.CandidateID,
		PersonName:     strings.TrimSpace(raise.PersonName),
		ActionType:     mapping.ActionType,
		State:          models.ActionStateReady,
		Lane:           mapping.CockpitLane,
		EngineLane:     NormalizeEngineLane(raise.EngineLane),
		// The next action IS the convergence. Everything else on this row is
		// context for the human who will do it.
		NextActionType:      mapping.NextActionType,
		NextActionAt:        &due,
		OutcomeCode:         mapping.OutcomeCode,
		ConversationStarted: handRaiseStartsAConversation(raise.Signal),
		Actionable:          true,
		WhyNow:              strings.TrimSpace(raise.Evidence),
		HumanNotes:          strings.TrimSpace(raise.HumanNotes),
		// The idempotency key makes the same signal, for the same person, from
		// the same engine, converge once rather than pile up duplicates.
		IdempotencyKey: HandRaiseIdempotencyKey(raise),
		CreatedAt:      occurred,
		UpdatedAt:      occurred,
	}
	return action
}

// handRaiseStartsAConversation reports whether the signal is itself evidence a
// two-way conversation exists. A review request is our own machinery asking for
// help; it is not the prospect talking to us.
func handRaiseStartsAConversation(signal HandRaiseSignal) bool {
	switch signal {
	case SignalPositiveReplyFirstTouch, SignalIntelSeedResponse, SignalMeetingOrProposalRequest:
		return true
	default:
		return false
	}
}

// HandRaiseIdempotencyKey identifies one converged signal. The engine is part
// of the identity on purpose: the same person raising their hand through two
// different engines is two facts about two engines, not one duplicate.
func HandRaiseIdempotencyKey(raise HandRaise) string {
	contact := ""
	if raise.CandidateID != nil {
		contact = raise.CandidateID.String()
	}
	return strings.Join([]string{
		"handraise", NormalizeEngineLane(raise.EngineLane), string(raise.Signal),
		raise.AccountID.String(), contact,
	}, ":")
}

// HandRaiseAwaitsHuman reports whether an action row is a hand-raiser that
// still needs a person. This is the predicate the Founder Interrupt Budget
// projection filters on, kept next to the mapping it mirrors.
func HandRaiseAwaitsHuman(action *models.OutreachCommercialAction) bool {
	if action == nil {
		return false
	}
	switch action.State {
	case models.ActionStateCompleted, models.ActionStateSkipped, models.ActionStateFailed:
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(action.IdempotencyKey), "handraise:")
}

// HandRaiseHasNoNextAction reports a hand-raiser nobody has committed to. This
// is the one a founder most needs surfaced: it is invisible in every
// due-date-ordered view precisely because it has no due date.
func HandRaiseHasNoNextAction(action *models.OutreachCommercialAction) bool {
	if !HandRaiseAwaitsHuman(action) {
		return false
	}
	return strings.TrimSpace(action.NextActionType) == "" || action.NextActionAt == nil
}
