package intel

import (
	"fmt"
	"strings"
	"time"
)

const (
	EnvelopeEXTRA    = "EXTRA"
	EnvelopeAccount1 = "ACCOUNT_1"
	EnvelopeAccount2 = "ACCOUNT_2"
	EnvelopeAccount3 = "ACCOUNT_3"

	HumanAttempted        = "attempted"
	HumanReached          = "reached"
	HumanNotReached       = "not_reached"
	HumanRouted           = "routed"
	HumanWrongRoute       = "wrong_route"
	HumanReply            = "reply"
	HumanMeetingScheduled = "meeting_scheduled"
	HumanMeetingHeld      = "meeting_held"
	HumanFollowUp         = "follow_up"
	HumanDisqualified     = "disqualified"
	HumanProposalEmitted  = "proposal_emitted"
	HumanWon              = "won"
	HumanLost             = "lost"
	HumanRevenueReceived  = "revenue_received"
)

// HumanOutcomeEntry is the founder capture. Blank IDs stay blank.
type HumanOutcomeEntry struct {
	EnvelopeID        string     `json:"envelope_id"`
	IdempotencyKey    string     `json:"idempotency_key"`
	LeadID            string     `json:"lead_id"`
	ReceiptID         string     `json:"receipt_id,omitempty"`
	AccountID         string     `json:"account_id"`
	Action            string     `json:"action"`
	Reached           *bool      `json:"reached,omitempty"`
	RouteValid        *bool      `json:"route_valid,omitempty"`
	Reply             bool       `json:"reply,omitempty"`
	MeetingScheduled  *bool      `json:"meeting_scheduled,omitempty"`
	MeetingHeld       *bool      `json:"meeting_held,omitempty"`
	FollowUpAt        *time.Time `json:"follow_up_at,omitempty"`
	Disqualified      bool       `json:"disqualified,omitempty"`
	ProposalEmitted   bool       `json:"proposal_emitted,omitempty"`
	OutcomeState      string     `json:"outcome_state,omitempty"`
	HumanConfirmed    bool       `json:"human_confirmed,omitempty"`
	EvidenceRef       string     `json:"evidence_ref,omitempty"`
	RevenueDocumentID string     `json:"revenue_document_id,omitempty"`
	RevenueCents      int64      `json:"revenue_cents,omitempty"`
	ActorRef          string     `json:"actor_ref,omitempty"`
	Notes             string     `json:"notes,omitempty"`
	OccurredAt        time.Time  `json:"occurred_at,omitempty"`
	Synthetic         bool       `json:"synthetic,omitempty"`
	Source            string     `json:"source,omitempty"`
	Query             string     `json:"query,omitempty"`
	Referrer          string     `json:"referrer,omitempty"`
	AssetFamily       string     `json:"asset_family,omitempty"`
	AssetID           string     `json:"asset_id,omitempty"`
	CTAID             string     `json:"cta_id,omitempty"`
	CorrelationID     string     `json:"correlation_id,omitempty"`
	OrganizationID    string     `json:"organization_id,omitempty"`
}

// HumanOutcomeEnvelope is an empty later-fill slot. IDs are never invented.
type HumanOutcomeEnvelope struct {
	Slot           string `json:"slot"`
	LeadID         string `json:"lead_id"`
	AccountID      string `json:"account_id"`
	IdempotencyKey string `json:"idempotency_key"`
	Status         string `json:"status"`
	InventedIDs    bool   `json:"invented_ids"`
	NextAction     string `json:"next_action"`
}

// EmptyEnvelopes returns EXTRA + ACCOUNT_1 + ACCOUNT_2 + ACCOUNT_3 with blank IDs.
func EmptyEnvelopes() []HumanOutcomeEnvelope {
	out := make([]HumanOutcomeEnvelope, 0, 4)
	for _, slot := range []string{EnvelopeEXTRA, EnvelopeAccount1, EnvelopeAccount2, EnvelopeAccount3} {
		out = append(out, HumanOutcomeEnvelope{
			Slot:           slot,
			LeadID:         "",
			AccountID:      "",
			IdempotencyKey: EnvelopeIdempotencyKey(slot),
			Status:         "empty",
			InventedIDs:    false,
			NextAction:     "fill after a real interaction; do not invent a lead_id or account_id",
		})
	}
	return out
}

// EnvelopeIdempotencyKey is the slot prefix only. Event identity is
// HumanOutcomeEventKey (slot + action + lead_id). A slot-only key is expanded.
func EnvelopeIdempotencyKey(slot string) string {
	return "envelope:" + strings.ToUpper(strings.TrimSpace(slot))
}

// HumanOutcomeEventKey is the durable ingest identity: one action per slot/lead.
func HumanOutcomeEventKey(in HumanOutcomeEntry, action string) string {
	slot := strings.ToLower(firstNonEmpty(in.EnvelopeID, "unbound"))
	lead := strings.ToLower(strings.TrimSpace(in.LeadID))
	if lead == "" {
		lead = "unbound"
	}
	return "human:" + slot + ":" + action + ":" + lead
}

func isSlotOnlyKey(k string) bool {
	k = strings.TrimSpace(k)
	if k == "" {
		return true
	}
	low := strings.ToLower(k)
	if strings.HasPrefix(low, "envelope:") {
		rest := strings.TrimPrefix(low, "envelope:")
		return !strings.Contains(rest, ":")
	}
	return false
}

func resolveHumanEventKey(in HumanOutcomeEntry, action string) string {
	computed := HumanOutcomeEventKey(in, action)
	if isSlotOnlyKey(in.IdempotencyKey) {
		return computed
	}
	return strings.TrimSpace(in.IdempotencyKey)
}

// ValidateHumanOutcome refuses WON/LOST/receita without evidence. Blank IDs stay blank.
func ValidateHumanOutcome(in HumanOutcomeEntry) error {
	action := normalizeHumanAction(in.Action)
	if action == "" {
		return fmt.Errorf("action required")
	}
	if !knownHumanAction(action) {
		return fmt.Errorf("unknown action %q", in.Action)
	}
	if inventedPlaceholderID(in.LeadID) || inventedPlaceholderID(in.AccountID) {
		return fmt.Errorf("placeholder IDs are not accepted; leave blank until a real id exists")
	}
	state := strings.ToUpper(strings.TrimSpace(in.OutcomeState))
	if action == HumanWon || state == OutcomeWon || state == OutcomeClient {
		if !in.HumanConfirmed || strings.TrimSpace(in.EvidenceRef) == "" {
			return fmt.Errorf("WON requires human_confirmed and evidence_ref")
		}
	}
	if action == HumanLost || state == OutcomeLost {
		if !in.HumanConfirmed || strings.TrimSpace(in.EvidenceRef) == "" {
			return fmt.Errorf("LOST requires human_confirmed and evidence_ref")
		}
	}
	if action == HumanRevenueReceived || strings.TrimSpace(in.RevenueDocumentID) != "" {
		if !in.HumanConfirmed || strings.TrimSpace(in.RevenueDocumentID) == "" || in.RevenueCents <= 0 {
			return fmt.Errorf("received revenue requires human_confirmed, revenue_document_id, and revenue_cents")
		}
	}
	if action == HumanFollowUp && (in.FollowUpAt == nil || in.FollowUpAt.IsZero()) {
		return fmt.Errorf("follow_up_at required")
	}
	return nil
}

// HumanOutcomeToEvent maps one founder capture onto confenge.commercial_event.v1.
// It does not invent IDs or copy notes into metric keys.
func HumanOutcomeToEvent(in HumanOutcomeEntry) (CommercialEvent, error) {
	if err := ValidateHumanOutcome(in); err != nil {
		return CommercialEvent{}, err
	}
	action := normalizeHumanAction(in.Action)
	occurred := in.OccurredAt
	if occurred.IsZero() {
		occurred = time.Now().UTC()
	}
	typ, state := humanEventType(in, action)
	key := resolveHumanEventKey(in, action)
	ev := CommercialEvent{
		EventID:           key,
		Version:           "1",
		Schema:            EventSchemaV1,
		Type:              typ,
		OccurredAt:        occurred,
		Timezone:          "America/Sao_Paulo",
		IdempotencyKey:    key,
		LeadID:            strings.TrimSpace(in.LeadID),
		ReceiptID:         firstNonEmpty(in.ReceiptID, in.LeadID),
		AccountPublicID:   strings.TrimSpace(in.AccountID),
		OutcomeState:      state,
		HumanConfirmed:    in.HumanConfirmed,
		EvidenceRef:       strings.TrimSpace(in.EvidenceRef),
		RevenueDocumentID: strings.TrimSpace(in.RevenueDocumentID),
		RevenueCents:      in.RevenueCents,
		FollowUpAt:        in.FollowUpAt,
		ActorRef:          firstNonEmpty(in.ActorRef, OwnerFounder),
		Source:            firstNonEmpty(in.Source, "founder-capture"),
		Query:             in.Query,
		Referrer:          in.Referrer,
		AssetFamily:       in.AssetFamily,
		AssetID:           in.AssetID,
		CTAID:             in.CTAID,
		CorrelationID:     in.CorrelationID,
		RouteFamily:       FamilyInbound,
		OrganizationID:    in.OrganizationID,
		Synthetic:         in.Synthetic,
	}
	return ev, nil
}

// RegisterHumanOutcome ingests the founder capture on the shipped event path.
// Same idempotency key is a replay. Rejected WON/LOST/receita become a held exception.
func RegisterHumanOutcome(store Store, in HumanOutcomeEntry) JoinResult {
	ev, err := HumanOutcomeToEvent(in)
	if err != nil {
		now := time.Now().UTC()
		code := ExceptionOrphan
		owner := OwnerFounder
		switch {
		case strings.Contains(err.Error(), "WON"):
			code = ExceptionUnconfirmedWon
		case strings.Contains(err.Error(), "LOST"):
			code = ExceptionUnconfirmedLost
		case strings.Contains(err.Error(), "revenue"):
			code = ExceptionCreatedAsRevenue
			owner = OwnerFinance
		case strings.Contains(err.Error(), "follow_up_at"):
			code = ExceptionOrphan
			owner = OwnerFounder
		}
		next := "supply human/document evidence; do not invent WON, LOST, or receita"
		if strings.Contains(err.Error(), "follow_up_at") {
			next = "supply follow_up_at; do not drop the date"
		}
		ex := Exception{
			OrganizationID: in.OrganizationID,
			Code:           code,
			Reason:         err.Error(),
			NextAction:     next,
			LeadID:         strings.TrimSpace(in.LeadID),
			AccountID:      strings.TrimSpace(in.AccountID),
			Owner:          owner,
			Held:           true,
			At:             now,
			OpenedAt:       now,
			Synthetic:      in.Synthetic,
			RetryState:     "pending",
		}
		if store != nil {
			_ = store.PutException(ex)
		}
		return JoinResult{Exceptions: []Exception{ex}, Held: true}
	}
	return IngestEvent(store, ev)
}

func knownHumanAction(a string) bool {
	switch a {
	case HumanAttempted, HumanReached, HumanNotReached, HumanRouted, HumanWrongRoute,
		HumanReply, HumanMeetingScheduled, HumanMeetingHeld, HumanFollowUp,
		HumanDisqualified, HumanProposalEmitted, HumanWon, HumanLost, HumanRevenueReceived:
		return true
	default:
		return false
	}
}

func normalizeHumanAction(v string) string {
	s := strings.ToLower(strings.TrimSpace(v))
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "-", "_")
	switch s {
	case "attempt", HumanAttempted:
		return HumanAttempted
	case HumanReached, "target_reached":
		return HumanReached
	case HumanNotReached, "no_answer":
		return HumanNotReached
	case HumanRouted, "route_ok":
		return HumanRouted
	case HumanWrongRoute, "invalid_route":
		return HumanWrongRoute
	case HumanReply, "replied":
		return HumanReply
	case HumanMeetingScheduled, "meeting_set":
		return HumanMeetingScheduled
	case HumanMeetingHeld, "meeting":
		return HumanMeetingHeld
	case HumanFollowUp, "followup", "follow_up_date":
		return HumanFollowUp
	case HumanDisqualified, "dq":
		return HumanDisqualified
	case HumanProposalEmitted, "proposal":
		return HumanProposalEmitted
	case HumanWon:
		return HumanWon
	case HumanLost:
		return HumanLost
	case HumanRevenueReceived, "receita", "cash_received":
		return HumanRevenueReceived
	default:
		return s
	}
}

func humanEventType(in HumanOutcomeEntry, action string) (typ, state string) {
	switch action {
	case HumanAttempted, HumanReached, HumanNotReached, HumanRouted, HumanWrongRoute, HumanFollowUp:
		return EventActionExecuted, OutcomeContacted
	case HumanReply:
		return EventReply, OutcomeReplied
	case HumanMeetingScheduled, HumanMeetingHeld:
		return EventMeeting, OutcomeMeeting
	case HumanDisqualified:
		return EventLeadRejected, OutcomeUnknown
	case HumanProposalEmitted:
		return EventProposal, OutcomeProposal
	case HumanWon:
		return EventWon, OutcomeWon
	case HumanLost:
		return EventLost, OutcomeLost
	case HumanRevenueReceived:
		return EventRevenueEvidenced, OutcomeWon
	default:
		return EventOutcomeObserved, firstNonEmpty(strings.ToUpper(strings.TrimSpace(in.OutcomeState)), OutcomeUnknown)
	}
}

func inventedPlaceholderID(v string) bool {
	s := strings.ToLower(strings.TrimSpace(v))
	switch s {
	case "tbd", "todo", "placeholder", "n/a", "na", "unknown", "changeme", "xxx", "account_1", "account_2", "account_3", "extra":
		return true
	default:
		return false
	}
}
