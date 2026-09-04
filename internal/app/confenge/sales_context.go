package confenge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// CONFENGE_SALES_CONTEXT_EXPORT/1.0 is the collection identity. Each item
// keeps CONFENGE_SALES_CONTEXT/1.0, the individual dossier MeetCFG already
// consumes. Labelling the collection with the item schema is SCHEMA_MISMATCH_COLLECTION.
//
// A flat, versioned projection of the hand-raisers the acquisition engines
// produced, so a downstream sales tool can pick a conversation up without a
// second data model and without calling back into Warmbly per row.
//
// It is a projection, not a store. Every field is copied from an
// OutreachCommercialAction that already exists; nothing here scores, ranks,
// infers intent or invents a fact. Where the underlying row has no answer the
// field is absent, because an empty field is honest and a filled one would not
// be.

const (
	// SalesContextExportSchemaV1 tags the collection/index artifact.
	SalesContextExportSchemaV1 = "CONFENGE_SALES_CONTEXT_EXPORT/1.0"
	// SalesContextSchemaV1 tags one item/dossiê. MeetCFG admits this identity
	// fail-closed for a single hand-raiser; it must never appear on the collection.
	SalesContextSchemaV1 = "CONFENGE_SALES_CONTEXT/1.0"

	salesContextWrapperHandraiser = "handraiser"
	salesContextWrapperAdmission  = "admission"
	salesContextMappedFrom        = "outreach_commercial_action"
)

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

// SalesContextWrapper names how this item was projected. Kind is never guessed:
// admission is the inbound-only net-new path; handraiser is every other
// converged commercial action.
type SalesContextWrapper struct {
	Kind       string `json:"kind"`
	MappedFrom string `json:"mapped_from"`
}

// SalesContextItem is one hand-raiser, fully described.
type SalesContextItem struct {
	Schema string `json:"schema"`
	// HandraiserID is the commercial-action identity under an explicit name.
	// It equals ActionID; both are copies of the same row, not two records.
	HandraiserID string              `json:"handraiser_id"`
	ActionID     uuid.UUID           `json:"action_id"`
	AccountID    uuid.UUID           `json:"account_id"`
	Wrapper      SalesContextWrapper `json:"wrapper"`
	// AcquisitionChannel is the engine of origin. Empty means the signal
	// predates engine attribution; it is never guessed.
	AcquisitionChannel string     `json:"acquisition_channel"`
	Origin             string     `json:"origin,omitempty"`
	Lane               string     `json:"lane,omitempty"`
	Intent             string     `json:"intent,omitempty"`
	Receipt            string     `json:"receipt,omitempty"`
	InboundOnly        bool       `json:"inbound_only,omitempty"`
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
	Outcome             string `json:"outcome,omitempty"`
	Reason              string `json:"reason,omitempty"`
	Freshness           string `json:"freshness,omitempty"`
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

// SalesContextExport is the collection artifact. Schema is the collection
// identity, never the item identity.
type SalesContextExport struct {
	Schema         string    `json:"schema"`
	OrganizationID uuid.UUID `json:"organization_id"`
	GeneratedAt    time.Time `json:"generated_at"`
	Total          int       `json:"total"`
	Limit          int       `json:"limit"`
	NextCursor     string    `json:"next_cursor,omitempty"`
	// ByEngine counts the artifact's own items per engine, so a reader can see
	// which engine produced the context they are holding.
	ByEngine     map[string]int     `json:"by_engine"`
	Unattributed int                `json:"unattributed"`
	Items        []SalesContextItem `json:"items"`
}

// ExportSalesContext projects the artifact from the hand-raisers the engines
// already produced. Read-only: it reserves nothing, sends nothing, mutates no
// row. An invalid cursor is 400, not a silent first page.
func (s *service) ExportSalesContext(ctx context.Context, orgID uuid.UUID, limit int, cursor string) (*SalesContextExport, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	cursorAt, cursorID, xerr := decodeSalesContextCursor(cursor)
	if xerr != nil {
		return nil, xerr
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
		Schema: SalesContextExportSchemaV1, OrganizationID: orgID,
		GeneratedAt: s.now(), Limit: limit, ByEngine: map[string]int{},
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
	sortSalesContextItems(items)
	if cursor != "" {
		filtered := items[:0]
		for _, item := range items {
			if salesContextAfterCursor(item, cursorAt, cursorID) {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	out.Total = len(items)
	if len(items) > limit {
		next := items[limit-1]
		out.NextCursor = encodeSalesContextCursor(next.CreatedAt, next.ActionID)
		items = items[:limit]
	}
	if items == nil {
		items = []SalesContextItem{}
	}
	out.Items = items
	return out, nil
}

func salesContextItem(action *models.OutreachCommercialAction) SalesContextItem {
	company, opportunity := SubjectKeyRefs(HandRaiseSubjectKeyOf(action))
	if company == "" {
		company = strings.TrimSpace(action.SourceLeadID)
	}
	origin := HandRaiseOriginOf(action)
	lane := NormalizeEngineLane(action.EngineLane)
	intent := HandRaiseIntentOf(action)
	receipt := HandRaiseReceiptOf(action)
	inboundOnly := HandRaiseInboundOnlyOf(action)
	outcome := strings.TrimSpace(action.OutcomeCode)
	wrapperKind := salesContextWrapperHandraiser
	if inboundOnly {
		wrapperKind = salesContextWrapperAdmission
	}
	return SalesContextItem{
		Schema:       SalesContextSchemaV1,
		HandraiserID: action.ID.String(),
		ActionID:     action.ID, AccountID: action.AccountID, CandidateID: action.CandidateID,
		Wrapper: SalesContextWrapper{
			Kind: wrapperKind, MappedFrom: salesContextMappedFrom,
		},
		AcquisitionChannel:  lane,
		Origin:              origin,
		Lane:                lane,
		Intent:              intent,
		Receipt:             receipt,
		InboundOnly:         inboundOnly,
		CompanyRef:          company,
		CompanyName:         strings.TrimSpace(action.CompanyName),
		PersonName:          strings.TrimSpace(action.PersonName),
		OpportunityID:       opportunity,
		IntentReason:        HandRaiseSignalOf(action),
		ReplyReason:         outcome,
		Outcome:             outcome,
		Reason:              firstNonEmpty(outcome, intent),
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

func sortSalesContextItems(items []SalesContextItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CreatedAt.After(items[j].CreatedAt)
		}
		return items[i].ActionID.String() > items[j].ActionID.String()
	})
}

func encodeSalesContextCursor(createdAt time.Time, id uuid.UUID) string {
	raw := createdAt.UTC().Format(time.RFC3339Nano) + "|" + id.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeSalesContextCursor(cursor string) (time.Time, uuid.UUID, *errx.Error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return time.Time{}, uuid.Nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, uuid.Nil, errx.New(errx.BadRequest, "sales-context cursor is invalid")
	}
	atRaw, idRaw, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, uuid.Nil, errx.New(errx.BadRequest, "sales-context cursor is invalid")
	}
	at, err := time.Parse(time.RFC3339Nano, atRaw)
	if err != nil {
		return time.Time{}, uuid.Nil, errx.New(errx.BadRequest, "sales-context cursor is invalid")
	}
	id, err := uuid.Parse(idRaw)
	if err != nil {
		return time.Time{}, uuid.Nil, errx.New(errx.BadRequest, "sales-context cursor is invalid")
	}
	return at.UTC(), id, nil
}

func salesContextAfterCursor(item SalesContextItem, cursorAt time.Time, cursorID uuid.UUID) bool {
	if item.CreatedAt.Before(cursorAt) {
		return true
	}
	if item.CreatedAt.After(cursorAt) {
		return false
	}
	return item.ActionID.String() < cursorID.String()
}

// SalesContextLogicalID is the replay identity of one item: schema plus the
// hand-raiser it projects. GeneratedAt is excluded on purpose.
func SalesContextLogicalID(item SalesContextItem) string {
	return strings.Join([]string{item.Schema, item.HandraiserID, item.ActionID.String()}, "|")
}

// SalesContextExportMustNotInvent reports fields a projection is forbidden to
// mint. Used by the contract test so a future field cannot quietly appear.
func SalesContextExportMustNotInvent() []string {
	return []string{"cnpj", "cargo", "decisor", "fit", "chance", "prazo", "prova"}
}

// SalesContextJSONHasInventedField scans a marshaled artifact for forbidden keys.
func SalesContextJSONHasInventedField(raw []byte) string {
	var walk func(v any, path string) string
	walk = func(v any, path string) string {
		switch typed := v.(type) {
		case map[string]any:
			for key, child := range typed {
				lower := strings.ToLower(key)
				for _, banned := range SalesContextExportMustNotInvent() {
					if lower == banned {
						return path + key
					}
				}
				if found := walk(child, path+key+"."); found != "" {
					return found
				}
			}
		case []any:
			for i, child := range typed {
				if found := walk(child, path); found != "" {
					return found
				}
				_ = i
			}
		}
		return ""
	}
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return ""
	}
	return walk(root, "")
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
