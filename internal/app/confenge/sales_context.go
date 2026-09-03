package confenge

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// CONFENGE_SALES_CONTEXT/1.0.
//
// A flat, versioned artifact describing the hand-raisers the acquisition
// engines produced, so a downstream sales tool can pick a conversation up
// without a second data model and without calling back into Warmbly per row.
//
// It is a projection, not a store. Every field is copied from an
// OutreachCommercialAction that already exists; nothing here scores, ranks,
// infers intent or invents a fact. Where the underlying row has no answer the
// field is absent, because an empty field is honest and a filled one would not
// be.

// SalesContextSchemaV1 tags the artifact.
const SalesContextSchemaV1 = "CONFENGE_SALES_CONTEXT/1.0"

// salesContextScanLimit bounds how many open actions the export reads. It
// matches the interrupt budget's scan: both project the same working queue.
const salesContextScanLimit = 500

// SalesContextFacts is one hand-raiser's evidence and its limits, kept together
// so a reader cannot pick up the claim without the caveat attached to it.
type SalesContextFacts struct {
	// WhyNow and FactualHook are the assertions a human already wrote or a
	// classifier already made. They are quoted, never re-derived.
	WhyNow      string `json:"why_now,omitempty"`
	FactualHook string `json:"factual_hook,omitempty"`
	// EvidenceIDs is the provenance: what these assertions point at.
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
	// Confidence and StatedLimits are the limits travelling with the facts.
	// StatedLimits is the row's own warnings; it is never summarised away.
	Confidence   string   `json:"confidence,omitempty"`
	StatedLimits []string `json:"stated_limits,omitempty"`
}

// SalesContextTouchpoint is one outbound attempt or its answer.
type SalesContextTouchpoint struct {
	Ordinal    int        `json:"ordinal"`
	Channel    string     `json:"channel"`
	State      string     `json:"state"`
	Subject    string     `json:"subject,omitempty"`
	SentAt     *time.Time `json:"sent_at,omitempty"`
	StopReason string     `json:"stop_reason,omitempty"`
}

// SalesContextItem is one hand-raiser, fully described.
type SalesContextItem struct {
	ActionID  uuid.UUID `json:"action_id"`
	AccountID uuid.UUID `json:"account_id"`
	// AcquisitionChannel is the engine of origin. Empty means the signal
	// predates engine attribution; it is never guessed.
	AcquisitionChannel string     `json:"acquisition_channel"`
	CompanyRef         string     `json:"company_ref,omitempty"`
	CompanyName        string     `json:"company_name,omitempty"`
	PersonName         string     `json:"person_name,omitempty"`
	CandidateID        *uuid.UUID `json:"candidate_id,omitempty"`
	// OpportunityID is the watched asset when one is known. Only the engines
	// that carry a subject key can fill it.
	OpportunityID string `json:"opportunity_id,omitempty"`
	// IntentReason is why this person is in the artifact at all: the converged
	// signal, read back off the row's own idempotency key.
	IntentReason string `json:"intent_reason,omitempty"`
	// ReplyReason is the recorded outcome, when the row carries one.
	ReplyReason         string `json:"reply_reason,omitempty"`
	ConversationStarted bool   `json:"conversation_started"`
	// Facts and its limits travel together; Touchpoints is what has already
	// been said to this person and what came back.
	Facts          SalesContextFacts        `json:"facts"`
	Touchpoints    []SalesContextTouchpoint `json:"touchpoints"`
	NextActionType string                   `json:"next_action_type,omitempty"`
	NextActionAt   *time.Time               `json:"next_action_at,omitempty"`
	State          string                   `json:"state"`
	CreatedAt      time.Time                `json:"created_at"`
}

// SalesContextExport is the whole artifact.
type SalesContextExport struct {
	Schema         string    `json:"schema"`
	OrganizationID uuid.UUID `json:"organization_id"`
	GeneratedAt    time.Time `json:"generated_at"`
	Total          int       `json:"total"`
	// ByEngine counts the artifact's own items per engine, so a reader can see
	// which engine produced the context they are holding.
	ByEngine     map[string]int     `json:"by_engine"`
	Unattributed int                `json:"unattributed"`
	Items        []SalesContextItem `json:"items"`
}

// ExportSalesContext projects the artifact from the hand-raisers the engines
// already produced. Read-only: it reserves nothing, sends nothing, mutates no
// row.
func (s *service) ExportSalesContext(ctx context.Context, orgID uuid.UUID, limit int) (*SalesContextExport, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	store := s.actionStore()
	if store == nil {
		return nil, errx.New(errx.Internal, "commercial action store is not wired")
	}
	actions, err := store.ListCommercialActions(ctx, orgID, uuid.Nil, true, salesContextScanLimit)
	if err != nil {
		return nil, errx.New(errx.Internal, "failed to export sales context: "+err.Error())
	}
	out := &SalesContextExport{
		Schema: SalesContextSchemaV1, OrganizationID: orgID,
		GeneratedAt: s.now(), ByEngine: map[string]int{},
	}
	for _, engine := range EngineLanes {
		out.ByEngine[engine] = 0
	}
	var items []SalesContextItem
	for i := range actions {
		action := &actions[i]
		// Only hand-raisers. The artifact describes people who asked for
		// something, not every open row in the cockpit.
		if !HandRaiseAwaitsHuman(action) {
			continue
		}
		item := salesContextItem(action)
		if action.AccountID != uuid.Nil && s.repo != nil {
			if tps, tpErr := s.repo.ListTouchpoints(ctx, orgID, action.AccountID, "", 50, 0); tpErr == nil {
				item.Touchpoints = salesContextTouchpoints(tps)
			}
		}
		if item.AcquisitionChannel == EngineLaneUnattributed {
			out.Unattributed++
		} else {
			out.ByEngine[item.AcquisitionChannel]++
		}
		items = append(items, item)
	}
	out.Total = len(items)
	// Newest first: a sales tool reads the top of this list.
	sort.SliceStable(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	if len(items) > limit {
		items = items[:limit]
	}
	out.Items = items
	return out, nil
}

func salesContextItem(action *models.OutreachCommercialAction) SalesContextItem {
	company, opportunity := SubjectKeyRefs(HandRaiseSubjectKeyOf(action))
	if company == "" {
		company = strings.TrimSpace(action.SourceLeadID)
	}
	return SalesContextItem{
		ActionID: action.ID, AccountID: action.AccountID, CandidateID: action.CandidateID,
		AcquisitionChannel:  NormalizeEngineLane(action.EngineLane),
		CompanyRef:          company,
		CompanyName:         strings.TrimSpace(action.CompanyName),
		PersonName:          strings.TrimSpace(action.PersonName),
		OpportunityID:       opportunity,
		IntentReason:        HandRaiseSignalOf(action),
		ReplyReason:         strings.TrimSpace(action.OutcomeCode),
		ConversationStarted: action.ConversationStarted,
		Facts: SalesContextFacts{
			WhyNow: strings.TrimSpace(action.WhyNow), FactualHook: strings.TrimSpace(action.FactualHook),
			EvidenceIDs: action.EvidenceIDs, Confidence: strings.TrimSpace(action.Confidence),
			StatedLimits: action.Warnings,
		},
		NextActionType: strings.TrimSpace(action.NextActionType),
		NextActionAt:   action.NextActionAt,
		State:          action.State,
		CreatedAt:      action.CreatedAt,
	}
}

// SubjectKeyRefs splits a canonical subject key back into the reference it
// names. It understands only the two shapes WebIntentSubjectKey produces.
func SubjectKeyRefs(subjectKey string) (companyRef, opportunityID string) {
	subjectKey = strings.TrimSpace(subjectKey)
	switch {
	case strings.HasPrefix(subjectKey, WebIntentSubjectCompanyPrefix):
		return strings.TrimPrefix(subjectKey, WebIntentSubjectCompanyPrefix), ""
	case strings.HasPrefix(subjectKey, WebIntentSubjectOpportunityPrefix):
		return "", strings.TrimPrefix(subjectKey, WebIntentSubjectOpportunityPrefix)
	default:
		return "", ""
	}
}

func salesContextTouchpoints(tps []models.OutreachTouchpoint) []SalesContextTouchpoint {
	out := make([]SalesContextTouchpoint, 0, len(tps))
	for _, tp := range tps {
		out = append(out, SalesContextTouchpoint{
			Ordinal: tp.Ordinal, Channel: tp.Channel, State: tp.State,
			Subject: tp.Subject, SentAt: tp.SentAt, StopReason: tp.StopReason,
		})
	}
	return out
}

// HandRaiseSignalOf reads the converged signal back off an action's own
// idempotency key. The key is "handraise:<engine>:<signal>:...", so the signal
// is recorded rather than re-derived from the row's shape.
func HandRaiseSignalOf(action *models.OutreachCommercialAction) string {
	if !HandRaiseAwaitsHuman(action) {
		return ""
	}
	parts := strings.Split(strings.TrimSpace(action.IdempotencyKey), ":")
	if len(parts) < 3 {
		return ""
	}
	return parts[2]
}
