package confenge

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/models"
)

// ControlledEmailContext is snapshotted at the observe site. UNKNOWN stays UNKNOWN.
type ControlledEmailContext struct {
	OccurredAt     time.Time
	RouteClass     string
	CohortID       string
	PolicyVersion  string
	ProviderName   string
	Source         string
	BounceClass    string
	ReplyClass     string
	AccountRef     string
	TouchpointID   string
	SMTPStatus     string
	EnhancedStatus string
	Diagnostic     string
}

func SnapshotControlledEmailContext(tp *models.OutreachTouchpoint, cand *models.OutreachContactCandidate, auth *BoundedCohortAuthorization, provider string) ControlledEmailContext {
	ctx := ControlledEmailContext{
		RouteClass:    intel.Unknown,
		CohortID:      intel.Unknown,
		PolicyVersion: intel.Unknown,
		ProviderName:  intel.Unknown,
		Source:        intel.Unknown,
	}
	if cand != nil {
		if class := CandidateRouteClass(cand); class != "" {
			ctx.RouteClass = class
		}
	}
	if auth != nil {
		if auth.CohortID != "" {
			ctx.CohortID = auth.CohortID
		}
		if auth.PolicyVersion != "" {
			ctx.PolicyVersion = auth.PolicyVersion
		}
		if auth.FrozenManifest != nil && auth.FrozenManifest.FeedIdentity != "" {
			ctx.Source = auth.FrozenManifest.FeedIdentity
		}
	}
	if strings.TrimSpace(provider) != "" {
		ctx.ProviderName = strings.TrimSpace(provider)
	}
	if tp != nil {
		if tp.ID != uuid.Nil {
			ctx.TouchpointID = tp.ID.String()
		}
		if ctx.AccountRef == "" && tp.AccountID != uuid.Nil {
			ctx.AccountRef = tp.AccountID.String()
		}
	}
	return ctx
}

func applyControlledEmailContext(ev *intel.CommercialEvent, c ControlledEmailContext) {
	if ev == nil {
		return
	}
	if ev.EmailRouteClass == "" {
		ev.EmailRouteClass = c.RouteClass
	}
	if ev.CohortID == "" {
		ev.CohortID = c.CohortID
	}
	if ev.PolicyVersion == "" {
		ev.PolicyVersion = c.PolicyVersion
	}
	if ev.ProviderName == "" {
		ev.ProviderName = c.ProviderName
	}
	if ev.Source == "" {
		ev.Source = c.Source
	}
	if ev.BounceClass == "" {
		ev.BounceClass = c.BounceClass
	}
	if ev.ReplyClass == "" {
		ev.ReplyClass = c.ReplyClass
	}
	if ev.AccountPublicID == "" && strings.TrimSpace(c.AccountRef) != "" {
		ev.AccountPublicID = c.AccountRef
	}
	if ev.EntityPublicID == "" && strings.TrimSpace(c.TouchpointID) != "" {
		ev.EntityPublicID = c.TouchpointID
	}
	if ev.CorrelationID == "" && (strings.TrimSpace(c.TouchpointID) != "" || strings.TrimSpace(c.AccountRef) != "") {
		ev.CorrelationID = strings.TrimSpace(c.TouchpointID) + ":" + strings.TrimSpace(c.AccountRef)
	}
	if ev.SMTPStatus == "" {
		ev.SMTPStatus = c.SMTPStatus
	}
	if ev.EnhancedStatus == "" {
		ev.EnhancedStatus = c.EnhancedStatus
	}
	if ev.Diagnostic == "" {
		ev.Diagnostic = c.Diagnostic
	}
}

func (s *service) liveControlledEmailContext(ctx context.Context, orgID uuid.UUID, tp *models.OutreachTouchpoint, cand *models.OutreachContactCandidate, provider string) ControlledEmailContext {
	if cand == nil && s != nil && s.repo != nil && tp != nil {
		if tp.ContactCandidateID != nil {
			cand, _ = s.repo.GetCandidate(ctx, orgID, *tp.ContactCandidateID)
		}
		if cand == nil && strings.TrimSpace(tp.Recipient) != "" {
			cand, _, _ = s.repo.FindCandidateByEmail(ctx, orgID, strings.TrimSpace(strings.ToLower(tp.Recipient)))
		}
	}
	if tp == nil && s != nil && s.repo != nil && cand != nil && cand.AccountID != uuid.Nil {
		if list, err := s.repo.ListTouchpoints(ctx, orgID, cand.AccountID, "", 50, 0); err == nil {
			for i := range list {
				if list[i].CampaignPolicyAuthorizationID != nil {
					tp = &list[i]
					break
				}
			}
		}
	}
	var auth *BoundedCohortAuthorization
	if s != nil && s.cohortStore != nil && tp != nil && tp.CampaignPolicyAuthorizationID != nil {
		auth, _ = s.cohortStore.GetGrant(ctx, *tp.CampaignPolicyAuthorizationID)
	}
	result := SnapshotControlledEmailContext(tp, cand, auth, provider)
	if result.ProviderName == "" || result.ProviderName == intel.Unknown {
		result.ProviderName = s.observedControlledProvider(orgID, result.AccountRef, result.TouchpointID)
	}
	return result
}

// observedControlledProvider carries the provider from the already-persisted
// attempt/acceptance receipt to later IMAP/DSN/opt-out events. It never guesses
// from a Message-ID shape or mailbox domain.
func (s *service) observedControlledProvider(orgID uuid.UUID, accountRef, touchpointID string) string {
	if s == nil || strings.TrimSpace(accountRef) == "" {
		return intel.Unknown
	}
	chains, err := s.intelStore().ListChains(orgID.String())
	if err != nil {
		return intel.Unknown
	}
	var provider string
	var latest time.Time
	for _, chain := range chains {
		for _, receipt := range chain.Commercial.Timeline {
			if receipt.AccountPublicID != accountRef || strings.TrimSpace(receipt.ProviderName) == "" {
				continue
			}
			if touchpointID != "" && receipt.TouchpointID != "" && receipt.TouchpointID != touchpointID {
				continue
			}
			if provider == "" || receipt.OccurredAt.After(latest) {
				provider = receipt.ProviderName
				latest = receipt.OccurredAt
			}
		}
	}
	return firstNonEmpty(provider, intel.Unknown)
}

func (s *service) observeControlledEmail(ctx context.Context, orgID uuid.UUID, typ string, tp *models.OutreachTouchpoint, cand *models.OutreachContactCandidate, extra ControlledEmailContext) {
	if s == nil {
		return
	}
	now := extra.OccurredAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	c := s.liveControlledEmailContext(ctx, orgID, tp, cand, extra.ProviderName)
	if extra.BounceClass != "" {
		c.BounceClass = extra.BounceClass
	}
	if extra.ReplyClass != "" {
		c.ReplyClass = extra.ReplyClass
	}
	if extra.Source != "" && (c.Source == "" || c.Source == intel.Unknown) {
		c.Source = extra.Source
	}
	c.SMTPStatus = extra.SMTPStatus
	c.EnhancedStatus = extra.EnhancedStatus
	c.Diagnostic = extra.Diagnostic
	ev := intel.CommercialEvent{
		EventID:        uuid.NewString(),
		Version:        intel.EventSchemaV1,
		Type:           typ,
		OccurredAt:     now,
		IngestedAt:     now,
		OrganizationID: orgID.String(),
		IdempotencyKey: typ + ":" + orgID.String() + ":" + c.TouchpointID + ":" + c.AccountRef,
		// This path is invoked by real cohort transport/IMAP/DSN handling.
		// Fixtures and canaries set Synthetic at their own construction sites.
		Synthetic: false,
	}
	applyControlledEmailContext(&ev, c)
	if extra.AccountRef != "" {
		ev.AccountPublicID = extra.AccountRef
	}
	recordObservedControlledEmail(s, ev)
	if s.intel != nil || s.intelStore() != nil {
		_ = intel.IngestEvent(s.intelStore(), ev)
	}
}

var observedMu sync.Mutex

func recordObservedControlledEmail(s *service, ev intel.CommercialEvent) {
	if s == nil {
		return
	}
	observedMu.Lock()
	defer observedMu.Unlock()
	s.observedEvents = append(s.observedEvents, ev)
}

func (s *service) ObservedControlledEmailEvents() []intel.CommercialEvent {
	if s == nil {
		return nil
	}
	observedMu.Lock()
	defer observedMu.Unlock()
	out := make([]intel.CommercialEvent, len(s.observedEvents))
	copy(out, s.observedEvents)
	return out
}

func mergeOutcomeControlledEmail(meta map[string]any, c ControlledEmailContext) {
	if meta == nil {
		return
	}
	putIfMissing(meta, "email_route_class", c.RouteClass)
	putIfMissing(meta, "cohort_id", c.CohortID)
	putIfMissing(meta, "policy_version", c.PolicyVersion)
	putIfMissing(meta, "provider_name", c.ProviderName)
	putIfMissing(meta, "bounce_class", c.BounceClass)
	putIfMissing(meta, "reply_class", c.ReplyClass)
}

func CommercialEventFromOutcome(ev models.OutreachOutcome) intel.CommercialEvent {
	meta := map[string]any{}
	if len(ev.Payload) > 0 {
		_ = json.Unmarshal(ev.Payload, &meta)
	}
	typ := outcomeToControlledType(ev.EventType, meta)
	out := intel.CommercialEvent{
		EventID:         ev.EventID.String(),
		Version:         intel.EventSchemaV1,
		Type:            typ,
		OccurredAt:      ev.OccurredAt,
		OrganizationID:  ev.OrganizationID.String(),
		IdempotencyKey:  ev.IdempotencyKey,
		EmailRouteClass: metaString(meta, "email_route_class"),
		CohortID:        metaString(meta, "cohort_id"),
		PolicyVersion:   metaString(meta, "policy_version"),
		ProviderName:    metaString(meta, "provider_name"),
		BounceClass:     metaString(meta, "bounce_class"),
		ReplyClass:      metaString(meta, "reply_class"),
		SMTPStatus:      metaString(meta, "smtp_status"),
		EnhancedStatus:  metaString(meta, "enhanced_status"),
		Diagnostic:      metaString(meta, "diagnostic"),
		Source:          metaString(meta, "source"),
	}
	if out.EmailRouteClass == "" {
		out.EmailRouteClass = intel.Unknown
	}
	return out
}

// ClassifyBounceClass never promotes ambiguous human text to a definitive hard
// bounce. Permanent suppression requires explicit 5xx/permanent evidence.
func ClassifyBounceClass(reason string) string {
	low := strings.ToLower(reason)
	for _, tok := range []string{"550", "551", "552", "553", "554", "5.0.", "5.1.", "5.2.", "5.3.", "5.4.", "5.5.", "5.6.", "5.7.", "user unknown", "no such user", "mailbox does not exist"} {
		if strings.Contains(low, tok) {
			return "HARD"
		}
	}
	for _, tok := range []string{"mailbox full", "over quota", "temporarily rejected", "try again later", "4.2.2", "4.4.1", "greylist"} {
		if strings.Contains(low, tok) {
			return "SOFT"
		}
	}
	return "UNKNOWN"
}

func normalizeBounceClass(class string) string {
	switch strings.ToUpper(strings.TrimSpace(class)) {
	case "HARD":
		return "HARD"
	case "SOFT":
		return "SOFT"
	default:
		return "UNKNOWN"
	}
}

func outcomeToControlledType(eventType string, meta map[string]any) string {
	switch strings.ToUpper(strings.TrimSpace(eventType)) {
	case OutcomeContacted, "ATTEMPTED":
		return intel.EventEmailAttempted
	case OutcomeBounced:
		if strings.EqualFold(metaString(meta, "bounce_class"), "SOFT") {
			return intel.EventSoftBounce
		}
		if strings.EqualFold(metaString(meta, "bounce_class"), "HARD") {
			return intel.EventHardBounce
		}
		return intel.EventUnknownState
	case OutcomeReplied:
		return intel.EventReply
	case OutcomeDoNotContact:
		return intel.EventOptOut
	case OutcomeMeeting:
		return intel.EventMeeting
	case OutcomeProposal:
		return intel.EventProposal
	case OutcomeQualifiedConversation:
		return intel.EventPipelineCreated
	case OutcomeWon, OutcomeClient:
		return intel.EventRevenueEvidenced
	case OutcomeNoResponse:
		return intel.EventNoReply
	default:
		low := strings.ToLower(strings.TrimSpace(eventType))
		if low != "" {
			return low
		}
		return intel.EventUnknownState
	}
}
