package confenge

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// The INTEL_SEED production loop.
//
// GateIntelSeed already answers "may this person receive a seed touch, and what
// would it say". What was missing is something that asks. This is that caller,
// and it is deliberately its own loop rather than a branch inside the
// first-touch one: it claims no queue row, takes no dispatch reservation and
// spends no first-touch budget, so first touch behaves identically whether this
// loop runs, is dormant, or is enabled and fully capped out.
//
// The cap below is the one thing this lane adds on TOP of the shared admission
// predicates. It is additional, never a carve-out: a seed send must pass every
// first-touch gate GateIntelSeed already applies AND fit inside this lane's own
// daily counter.

// EnvIntelSeedDailyCap is the INTEL_SEED lane's own daily send ceiling, per
// organization. Unset or zero means no seed is deliverable, which is the same
// dormant answer an unset opt-in gives.
const EnvIntelSeedDailyCap = "CONFENGE_INTEL_SEED_DAILY_CAP"

// EnvIntelSeedOrgID scopes the loop to one organization. INTEL_SEED is an
// operator-run experiment; it does not sweep every tenant by default.
const EnvIntelSeedOrgID = "CONFENGE_INTEL_SEED_ORG_ID"

// intelSeedMaxPerTick bounds one pass so a large book cannot turn a single tick
// into an unbounded send burst.
const intelSeedMaxPerTick = 10

// intelSeedGateAttemptsPerPass bounds how many contacts one pass puts to the
// gate before giving up. A refusal writes nothing, so without a bound a book
// where nobody is admissible would never end its pass.
const intelSeedGateAttemptsPerPass = 25

// intelSeedContactCooldown is how long one contact stays out of the lane's
// candidate pool after a seed touch. It is long on purpose: a seed offer is a
// cold email, and repeating it is the failure mode this lane must not have.
const intelSeedContactCooldown = 90 * 24 * time.Hour

// IntelSeedDailyCap reads the lane's own ceiling. A missing, unparseable or
// non-positive value is zero: fail closed, never fall back to a first-touch
// number that was budgeted for a different lane.
func IntelSeedDailyCap() int {
	raw := strings.TrimSpace(os.Getenv(EnvIntelSeedDailyCap))
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// IntelSeedLedger is the INTEL_SEED lane's own send record. It is what the
// daily cap counts; nothing here reads or writes first-touch state.
type IntelSeedLedger interface {
	// CountSendsSince is the cap's counter.
	CountSendsSince(ctx context.Context, orgID uuid.UUID, since time.Time) (int, error)
	// RecordSend is idempotent on the message key, so a retry after a crash
	// cannot double-count the cap or double-send the recipient.
	RecordSend(ctx context.Context, send models.IntelSeedSend) (bool, error)
	// AlreadySent reports whether this exact seed touch was already delivered.
	AlreadySent(ctx context.Context, orgID uuid.UUID, messageKey string) (bool, error)
	// SeededCandidateIDs is what stops the loop re-offering the same contact
	// every tick. The message key depends on the subject, which is only known
	// after admission, so the pre-filter has to dedupe on the contact instead.
	SeededCandidateIDs(ctx context.Context, orgID uuid.UUID, since time.Time) (map[uuid.UUID]bool, error)
}

// WireIntelSeed installs the lane's own ledger. Without it the lane stays
// dormant: an uncountable cap is not a cap, so nothing may be sent.
func (s *service) WireIntelSeed(ledger IntelSeedLedger) {
	if s == nil {
		return
	}
	s.intelSeedLedger = ledger
}

// IntelSeedHeadroom reports how many seed touches this organization may still
// send today. Zero is the dormant answer as well as the exhausted one, and the
// caller does not need to tell them apart: both mean "send nothing".
func (s *service) IntelSeedHeadroom(ctx context.Context, orgID uuid.UUID, now time.Time) int {
	limit := IntelSeedDailyCap()
	if limit <= 0 || s == nil || s.intelSeedLedger == nil || !IntelSeedEnabled() {
		return 0
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	dayStart := now.UTC().Truncate(24 * time.Hour)
	sent, err := s.intelSeedLedger.CountSendsSince(ctx, orgID, dayStart)
	if err != nil {
		// An unreadable counter is never permission to send.
		return 0
	}
	if sent >= limit {
		return 0
	}
	return limit - sent
}

// ProcessIntelSeedOnce sends at most one INTEL_SEED touch and reports whether
// it did. It returns false without touching anything when the lane is dormant,
// uncapped, exhausted, or has nothing admissible to say.
func (s *service) ProcessIntelSeedOnce(ctx context.Context) (bool, error) {
	if s == nil || !IntelSeedEnabled() || s.intelSeedLedger == nil || s.firstTouchTransport == nil {
		return false, nil
	}
	orgID := intelSeedOrgID()
	if orgID == uuid.Nil {
		return false, nil
	}
	now := s.now()
	if s.IntelSeedHeadroom(ctx, orgID, now) <= 0 {
		return false, nil
	}
	// Several candidates are tried, not one. A refusal writes no ledger row, so
	// a loop that stopped at the first refused contact would re-offer that same
	// person on every tick and never reach anyone behind them. Bounded, because
	// a book where nobody is admissible must still end the pass.
	for _, candidate := range s.intelSeedCandidates(ctx, orgID, now, intelSeedGateAttemptsPerPass) {
		// Admission and composition. Every predicate here is the first-touch
		// one, applied by composition; none of it reserves anything.
		decision := s.GateIntelSeed(ctx, orgID, candidate)
		switch decision.Kind {
		case IntelSeedProceed:
			if decision.Message == nil {
				continue
			}
			return s.deliverIntelSeed(ctx, decision.Message, now)
		case IntelSeedPaused, IntelSeedDormant:
			// Lane-wide, not contact-specific. Trying more contacts cannot
			// change the answer, so stop rather than burning the pass.
			return false, decision.Err
		default:
			continue
		}
	}
	return false, nil
}

func (s *service) deliverIntelSeed(ctx context.Context, msg *IntelSeedMessage, now time.Time) (bool, error) {
	sent, err := s.intelSeedLedger.AlreadySent(ctx, msg.OrganizationID, msg.MessageKey)
	if err != nil {
		return false, err
	}
	if sent {
		return false, nil
	}
	mailboxID, err := s.ResolveOutboundMailbox(ctx, msg.OrganizationID)
	if err != nil {
		return false, err
	}
	// Record BEFORE the handoff. The ledger row is the no-resend fence and the
	// cap's counter at the same time; writing it after the send would let a
	// crash in between re-send the recipient and under-count the cap.
	recorded, err := s.intelSeedLedger.RecordSend(ctx, models.IntelSeedSend{
		OrganizationID: msg.OrganizationID, MessageKey: msg.MessageKey,
		CandidateID: msg.CandidateID, AccountID: msg.AccountID,
		Recipient: msg.To, SubjectKey: msg.SubjectKey, SentAt: now,
	})
	if err != nil {
		return false, err
	}
	if !recorded {
		// Another worker holds this exact touch. Not ours to send.
		return false, nil
	}
	_, outcome, sendErr := s.firstTouchTransport.SendFirstTouch(ctx, FirstTouchMessage{
		OrganizationID: msg.OrganizationID, EmailAccountID: mailboxID,
		MessageKey: msg.MessageKey, To: msg.To,
		Subject: msg.Subject, BodyText: msg.BodyText,
	})
	if outcome != FirstTouchAccepted {
		// The ledger row stays. A seed touch is not worth a retry that risks a
		// duplicate cold email, and the spent cap slot is the honest record of
		// an attempt this lane made.
		log.Warn().Str("org_id", msg.OrganizationID.String()).Str("outcome", string(outcome)).
			Msg("confenge intel seed: touch not accepted, slot spent and not retried")
		return false, sendErr
	}
	return true, nil
}

// intelSeedCandidates lists contacts worth putting to the gate. It reads only
// accounts the shared admission rules already consider operational, and skips
// contacts this lane already wrote to; GateIntelSeed re-checks every predicate
// itself, so this is a cheap pre-filter and never the authority.
func (s *service) intelSeedCandidates(ctx context.Context, orgID uuid.UUID, now time.Time, limit int) []uuid.UUID {
	if s.repo == nil || s.intelSeedLedger == nil || limit <= 0 {
		return nil
	}
	seeded, err := s.intelSeedLedger.SeededCandidateIDs(ctx, orgID, now.UTC().Add(-intelSeedContactCooldown))
	if err != nil {
		// An unreadable skip set would re-offer people this lane already wrote
		// to. Send nothing instead.
		return nil
	}
	accounts, err := s.repo.ListAccounts(ctx, orgID, repository.OutreachAccountFilter{
		Limit: limit * 10, ExcludeTerminal: true, RequireOperational: true,
	})
	if err != nil {
		return nil
	}
	var out []uuid.UUID
	for i := range accounts {
		cands, candErr := s.repo.ListCandidates(ctx, orgID, accounts[i].ID)
		if candErr != nil {
			continue
		}
		for j := range cands {
			if cands[j].DoNotContact || cands[j].Bounced || cands[j].Blocked || seeded[cands[j].ID] {
				continue
			}
			out = append(out, cands[j].ID)
			if len(out) >= limit {
				return out
			}
		}
	}
	return out
}

func intelSeedOrgID() uuid.UUID {
	raw := strings.TrimSpace(os.Getenv(EnvIntelSeedOrgID))
	if raw == "" {
		return uuid.Nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil
	}
	return id
}

// IntelSeedWorker drives the loop on the same ticker shape every other CONFENGE
// worker uses. It runs and does nothing while the lane is dormant.
type IntelSeedWorker struct {
	service  Service
	interval time.Duration
}

func NewIntelSeedWorker(service Service, interval time.Duration) *IntelSeedWorker {
	if interval <= 0 {
		interval = time.Minute
	}
	return &IntelSeedWorker{service: service, interval: interval}
}

func (w *IntelSeedWorker) Run(ctx context.Context) {
	if w == nil || w.service == nil {
		return
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		for i := 0; i < intelSeedMaxPerTick; i++ {
			progressed, err := w.service.ProcessIntelSeedOnce(ctx)
			if err != nil || !progressed {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
