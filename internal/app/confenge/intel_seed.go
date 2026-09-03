package confenge

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/liveintel"
	"github.com/warmbly/warmbly/internal/models"
)

// The INTEL_SEED lane.
//
// A distinct commercial intent from first touch: first touch proposes work,
// INTEL_SEED offers to watch one specific public asset on the recipient's
// behalf. It runs ALONGSIDE first touch, never through it.
//
// What it reuses, deliberately, is the admission question -- the same
// suppression, DNC, commercial-authority and target-fit predicates that decide
// whether this system is allowed to cold-email a person at all. What it does
// NOT reuse is GateCampaignEmail itself, for two reasons that are both load
// bearing: that function reserves from the shared dispatch governor (so calling
// it here would spend first touch's send budget), and it requires an approved
// first-touch touchpoint to exist (so it answers a question INTEL_SEED is not
// asking). Composition, not branching.
//
// Consent note: INTEL_SEED is cold outreach. It is admitted on the
// cold-outreach basis and it is NOT, and must never become, a substitute for
// the explicit subscription consent INTEL_WATCH requires. The two lanes never
// read each other's consent.

// EnvIntelSeedEnabled is the explicit opt-in. Unset means the lane is dormant:
// admission is still computable and testable, but nothing is deliverable.
const EnvIntelSeedEnabled = "CONFENGE_INTEL_SEED_ENABLED"

// IntelSeedEnabled reports whether the INTEL_SEED lane may produce deliverable
// messages.
func IntelSeedEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(EnvIntelSeedEnabled)))
	return v == "1" || v == "true" || v == "on"
}

// IntelSeedKind is the closed set of admission outcomes.
type IntelSeedKind int

const (
	// IntelSeedProceed: admitted and composed. The caller holds a deliverable
	// message; it holds no reservation, because this lane takes none.
	IntelSeedProceed IntelSeedKind = iota
	// IntelSeedBlocked: DNC, opt-out, bounce, suppression. Permanent.
	IntelSeedBlocked
	// IntelSeedNotQualified: reversible commercial/target-fit refusal.
	IntelSeedNotQualified
	// IntelSeedNoAmmunition: no trustworthy live intelligence, so there is no
	// specific public asset to offer to watch. Nothing to send, not a failure.
	IntelSeedNoAmmunition
	// IntelSeedDormant: the lane is not opted in.
	IntelSeedDormant
	// IntelSeedTransient: infrastructure failure. Retry; never suppress.
	IntelSeedTransient
	// IntelSeedPaused: kill switch or durable pause. Same gate first touch obeys.
	IntelSeedPaused
)

// Machine-readable INTEL_SEED reasons.
const (
	IntelSeedReasonDNCOrBounce   = "dnc_or_bounce"
	IntelSeedReasonAccountDNC    = "account_dnc"
	IntelSeedReasonSuppressed    = "recipient_suppressed"
	IntelSeedReasonNoIntel       = "no_live_intelligence"
	IntelSeedReasonTargetFit     = "target_fit_not_operational"
	IntelSeedReasonAuthority     = "commercial_authority_invalid"
	IntelSeedReasonSendingOff    = "sending_paused"
	IntelSeedReasonDormant       = "intel_seed_lane_dormant"
	IntelSeedReasonMissingPerson = "contact_candidate_missing"
	IntelSeedReasonLookup        = "lookup_failed"
)

// IntelSeedMessage is one composed, deliverable INTEL_SEED touch. It carries
// its own idempotency key, in its own namespace.
type IntelSeedMessage struct {
	OrganizationID uuid.UUID
	AccountID      uuid.UUID
	CandidateID    uuid.UUID
	MessageKey     string
	To             string
	Subject        string
	BodyText       string
	// SubjectKey is the watched asset's identity, carried through so an
	// accepted INTEL_SEED can be turned into an INTEL_WATCH subscription with
	// the recipient's own explicit consent -- never automatically.
	SubjectKey string
	PublicURL  string
}

// IntelSeedDecision is the discriminant admission result.
type IntelSeedDecision struct {
	Kind    IntelSeedKind
	Reason  string
	Err     error
	Message *IntelSeedMessage
}

// PermanentSuppress reports whether this refusal justifies org-wide
// suppression. Only IntelSeedBlocked does, exactly as only GateHardBlock does
// on the first-touch gate.
func (d IntelSeedDecision) PermanentSuppress() bool { return d.Kind == IntelSeedBlocked }

// MessageKeyIntelSeed is the INTEL_SEED idempotency identity, namespaced away
// from first touch ("email:campaign:...") and from watch
// ("email:intel_watch:..."). A collision between lanes would let one lane's
// "already sent" answer another lane's question.
func MessageKeyIntelSeed(candidateID uuid.UUID, subjectKey string) string {
	return fmt.Sprintf("email:intel_seed:contact:%s:subject:%s", candidateID, strings.TrimSpace(subjectKey))
}

// GateIntelSeed decides whether one contact may receive an INTEL_SEED touch and
// composes it when they may.
//
// It never reserves a dispatch slot, never reads or writes a touchpoint, never
// claims a cohort slot and never touches the first-touch queue. A caller that
// runs this on every contact in the book changes nothing about first-touch
// admission, volume, cadence or queue state.
func (s *service) GateIntelSeed(ctx context.Context, orgID, candidateID uuid.UUID) IntelSeedDecision {
	if s == nil || s.repo == nil {
		return IntelSeedDecision{Kind: IntelSeedTransient, Reason: IntelSeedReasonLookup,
			Err: fmt.Errorf("confenge service not wired")}
	}
	// The same kill switch and durable pause first touch obeys. INTEL_SEED is
	// cold outreach; an operator who stopped sending stopped this too.
	if !s.cfg.SendingAllowed() || FileKillSwitchActive() {
		return IntelSeedDecision{Kind: IntelSeedPaused, Reason: IntelSeedReasonSendingOff}
	}
	// The same resolved transport state the fast lane gates on, which is where
	// the durable pause and the business window actually live. Without it this
	// lane would compose deliverable cold outreach at 3am -- outside the hours
	// first touch is allowed to send in.
	if s.governor != nil {
		if transport := s.ResolveTransportState(ctx, &orgID); !transport.Active {
			return IntelSeedDecision{Kind: IntelSeedPaused, Reason: intelSeedTransportReason(transport)}
		}
	}

	cand, err := s.repo.GetCandidate(ctx, orgID, candidateID)
	if err != nil {
		return IntelSeedDecision{Kind: IntelSeedTransient, Reason: IntelSeedReasonLookup,
			Err: fmt.Errorf("intel seed candidate lookup: %w", err)}
	}
	if cand == nil {
		return IntelSeedDecision{Kind: IntelSeedNotQualified, Reason: IntelSeedReasonMissingPerson}
	}
	// Dominant blocks first, exactly as the first-touch gate orders them.
	if cand.DoNotContact || cand.Bounced || cand.Blocked {
		return IntelSeedDecision{Kind: IntelSeedBlocked, Reason: IntelSeedReasonDNCOrBounce}
	}
	acc, err := s.repo.GetAccount(ctx, orgID, cand.AccountID)
	if err != nil {
		return IntelSeedDecision{Kind: IntelSeedTransient, Reason: IntelSeedReasonLookup,
			Err: fmt.Errorf("intel seed account lookup: %w", err)}
	}
	if acc == nil {
		return IntelSeedDecision{Kind: IntelSeedNotQualified, Reason: TargetFitReasonMissing}
	}
	if acc.DoNotContact || acc.Blocked {
		return IntelSeedDecision{Kind: IntelSeedBlocked, Reason: IntelSeedReasonAccountDNC}
	}

	recipient := strings.ToLower(strings.TrimSpace(cand.Email))
	if recipient == "" || !strings.Contains(recipient, "@") || strings.ContainsAny(recipient, " \t\r\n") {
		return IntelSeedDecision{Kind: IntelSeedNotQualified, Reason: "recipient_not_usable"}
	}
	// The same durable suppression list AssertTransportable consults. A bounce,
	// complaint or unsubscribe recorded by any lane stops this one too.
	if blocked, reason, lookupErr := s.intelSeedSuppressed(ctx, orgID, recipient); lookupErr != nil {
		return IntelSeedDecision{Kind: IntelSeedTransient, Reason: IntelSeedReasonLookup, Err: lookupErr}
	} else if blocked {
		return IntelSeedDecision{Kind: IntelSeedBlocked, Reason: reason}
	}
	// The same commercial-authority conjunction the first-touch gate applies.
	if err := s.assertAuthoritativeFeedForTransport(ctx, orgID, acc); err != nil {
		return IntelSeedDecision{Kind: IntelSeedNotQualified, Reason: IntelSeedReasonAuthority, Err: err}
	}
	// The same target-fit / email-outbound predicate.
	if err := RequireEmailOutbound(acc, cand); err != nil {
		reason := acc.TargetFitSuppressionReason
		if reason == "" {
			reason = IntelSeedReasonTargetFit
		}
		return IntelSeedDecision{Kind: IntelSeedNotQualified, Reason: reason, Err: err}
	}

	// Ammunition. This is the one place INTEL_SEED needs live intelligence, and
	// it is looked up only after admission is already decided -- so a missing,
	// slow or broken resolver can only mean "INTEL_SEED has nothing to say to
	// this person", never a changed admission answer for any lane.
	intel := liveintel.Attach(ctx, s.liveIntel, orgID, acc.ID)
	if intel == nil {
		return IntelSeedDecision{Kind: IntelSeedNoAmmunition, Reason: IntelSeedReasonNoIntel}
	}
	if !IntelSeedEnabled() {
		// Admission was computable and is recorded above; delivery is not
		// authorised. Kept distinct from "nothing to send" on purpose.
		return IntelSeedDecision{Kind: IntelSeedDormant, Reason: IntelSeedReasonDormant}
	}

	message := ComposeIntelSeed(acc, cand, intel)
	if message == nil {
		return IntelSeedDecision{Kind: IntelSeedNoAmmunition, Reason: "intel_seed_copy_unavailable"}
	}
	message.OrganizationID = orgID
	message.AccountID = acc.ID
	message.CandidateID = cand.ID
	message.To = recipient
	message.MessageKey = MessageKeyIntelSeed(cand.ID, intel.SubjectKey)
	return IntelSeedDecision{Kind: IntelSeedProceed, Message: message}
}

// intelSeedTransportReason names why transport is closed, so an operator can
// tell a business-window wait from an actual pause. A window blocker outside
// business hours is the healthy steady state, not an outage.
func intelSeedTransportReason(transport TransportState) string {
	if len(transport.Blockers) > 0 {
		return strings.TrimSpace(transport.Blockers[0])
	}
	return IntelSeedReasonSendingOff
}

// intelSeedSuppressed consults the same durable recipient-suppression table
// AssertTransportable uses. A repository that cannot answer is a transient
// failure, never permission to send.
func (s *service) intelSeedSuppressed(ctx context.Context, orgID uuid.UUID, recipient string) (bool, string, error) {
	suppressions, ok := s.repo.(interface {
		GetOutreachRecipientSuppression(context.Context, uuid.UUID, string) (*models.SuppressedRecipient, error)
	})
	if !ok {
		return false, "", nil
	}
	suppression, err := suppressions.GetOutreachRecipientSuppression(ctx, orgID, recipient)
	if err != nil {
		return false, "", fmt.Errorf("intel seed suppression lookup: %w", err)
	}
	if suppression == nil {
		return false, "", nil
	}
	return true, IntelSeedReasonSuppressed + ":" + string(suppression.Source), nil
}

// ComposeIntelSeed writes the INTEL_SEED touch. The CTA is to consume or
// monitor one specific public asset, never a generic pitch: the whole reason
// this lane is separate is that it asks for a different thing.
//
// Every factual sentence comes from the attested LiveIntelligenceV1 payload and
// the public URL it names, so the message makes no claim this system cannot
// point at a public source for.
func ComposeIntelSeed(acc *models.OutreachAccount, cand *models.OutreachContactCandidate, intel *liveintel.LiveIntelligenceV1) *IntelSeedMessage {
	if acc == nil || cand == nil || intel == nil {
		return nil
	}
	if ok, _ := intel.Validate(); !ok {
		return nil
	}
	headline := strings.TrimSpace(intel.Headline)
	if headline == "" {
		return nil
	}
	company := strings.TrimSpace(editorialCompanyName(acc))
	subject := "Acompanhamento: " + headline

	var body strings.Builder
	if name := strings.TrimSpace(cand.Name); name != "" {
		body.WriteString(name + ",\n\n")
	}
	if company != "" {
		body.WriteString("Vi uma publicação pública ligada a " + company + ": " + headline + ".\n")
	} else {
		body.WriteString("Vi uma publicação pública sobre " + headline + ".\n")
	}
	if summary := strings.TrimSpace(intel.Summary); summary != "" {
		body.WriteString(summary + "\n")
	}
	body.WriteString("Fonte: " + strings.TrimSpace(intel.PublicURL) + "\n\n")
	// The ask. Monitoring, not a meeting.
	body.WriteString("Se for útil, posso acompanhar esse assunto e te avisar quando mudar ")
	body.WriteString("(novas publicações, mudanças de prazo). É só responder confirmando ")
	body.WriteString("que eu ativo o acompanhamento.\n\n")
	body.WriteString(delegatedContactExit + "\n")

	return &IntelSeedMessage{
		Subject:    ApplyCopyHygiene(subject),
		BodyText:   ApplyCopyHygiene(body.String()),
		SubjectKey: strings.TrimSpace(intel.SubjectKey),
		PublicURL:  strings.TrimSpace(intel.PublicURL),
	}
}

// IntelSeedObservedAtFresh reports whether intelligence is recent enough to be
// worth offering to watch. Stale intelligence is not wrong, it is just not news.
func IntelSeedObservedAtFresh(intel *liveintel.LiveIntelligenceV1, now time.Time, maxAge time.Duration) bool {
	if intel == nil || intel.ObservedAt.IsZero() || maxAge <= 0 {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return !intel.ObservedAt.UTC().Before(now.UTC().Add(-maxAge))
}
