package confenge

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/warmbly/warmbly/internal/models"
)

// Reply-side hand-raise convergence.
//
// A prospect answering a cold touch positively is the commercial fact the whole
// outbound surface exists to produce. It has always updated the account; what
// it did not do was land on the one next-action surface a founder looks at,
// carrying which engine produced it. This is that writer.
//
// Attribution is never guessed. The lane comes from what the caller declared,
// or from a correlated first-touch touchpoint, and otherwise stays empty. An
// unattributed hand-raiser is still surfaced; it is simply honest about not
// knowing which engine earned it.

// ReplyEngineLane resolves the engine of origin for one inbound reply.
func ReplyEngineLane(in InboundHandoff) string {
	if lane := NormalizeEngineLane(in.EngineLane); lane != EngineLaneUnattributed {
		return lane
	}
	// A correlated touchpoint is first-touch cadence state. No other engine
	// writes one, so this is a read of an existing fact, not an inference.
	if in.TouchpointID != uuid.Nil {
		return EngineLaneFirstTouch
	}
	return EngineLaneUnattributed
}

// ReplyHandRaiseSignal maps one reply onto a hand-raise signal, given the
// engine that produced the message being answered.
//
// Only the positive-interest intent converges. Every other intent already has
// its own handling on the reply path, and duplicating it here as a hand-raiser
// would put the same fact in two queues.
//
// INTEL_WATCH has no member of the closed signal set: the vocabulary has a
// first-touch reply and an INTEL_SEED response and nothing for subscription
// mail. A watch reply therefore converges under its own lane with the generic
// positive-reply signal rather than a fabricated one, and adding a dedicated
// signal is a policy decision, not something this mapping may invent.
func ReplyHandRaiseSignal(intent, engineLane string) (HandRaiseSignal, bool) {
	if strings.ToUpper(strings.TrimSpace(intent)) != IntentPositiveInterest {
		return "", false
	}
	if NormalizeEngineLane(engineLane) == EngineLaneIntelSeed {
		return SignalIntelSeedResponse, true
	}
	return SignalPositiveReplyFirstTouch, true
}

// convergeReplyHandRaise files a positive reply on the commercial-action
// surface. It is best-effort by design: a reply is already recorded as an
// outcome and an account state change by the time this runs, so failing to
// converge must not fail the reply.
func (s *service) convergeReplyHandRaise(ctx context.Context, orgID uuid.UUID, in InboundHandoff,
	intent CommercialIntent, cand *models.OutreachContactCandidate, acc *models.OutreachAccount) {
	if s == nil || acc == nil {
		return
	}
	lane := ReplyEngineLane(in)
	signal, ok := ReplyHandRaiseSignal(intent.Intent, lane)
	if !ok {
		return
	}
	raise := HandRaise{
		OrganizationID: orgID, AccountID: acc.ID,
		Signal: signal, EngineLane: lane, OccurredAt: in.OccurredAt,
		Evidence: SanitizeText(in.Subject, 300),
	}
	if cand != nil {
		id := cand.ID
		raise.CandidateID = &id
		if name := strings.TrimSpace(cand.Name); name != "" {
			raise.PersonName = name
		}
	}
	if _, xerr := s.PersistHandRaise(ctx, raise); xerr != nil {
		log.Warn().Str("org_id", orgID.String()).Str("account_id", acc.ID.String()).
			Str("engine_lane", lane).Str("reason", xerr.Message).
			Msg("confenge: positive reply did not converge onto the hand-raiser surface")
	}
}
