package intel

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ParseCommercialEvent decodes one versioned envelope. Unknown extra
// fields are ignored. Narrative/PII blobs are not copied.
func ParseCommercialEvent(raw []byte) (CommercialEvent, error) {
	var ev CommercialEvent
	if len(raw) == 0 {
		return ev, fmt.Errorf("empty commercial event")
	}
	if err := json.Unmarshal(raw, &ev); err != nil {
		return ev, err
	}
	return ev, ValidateCommercialEvent(ev)
}

// ValidateCommercialEvent checks the minimum envelope. It does not
// invent missing IDs.
func ValidateCommercialEvent(ev CommercialEvent) error {
	if strings.TrimSpace(ev.EventID) == "" {
		return fmt.Errorf("event_id required")
	}
	if strings.TrimSpace(ev.Version) == "" && strings.TrimSpace(ev.Schema) == "" {
		return fmt.Errorf("version or schema required")
	}
	if strings.TrimSpace(ev.Type) == "" {
		return fmt.Errorf("type required")
	}
	if ev.OccurredAt.IsZero() {
		return fmt.Errorf("occurred_at required")
	}
	if !knownEventType(ev.Type) {
		return fmt.Errorf("unknown event type %q", ev.Type)
	}
	return nil
}

func knownEventType(t string) bool {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case EventLeadReceived, EventLeadValidated, EventLeadRejected,
		EventHandoffAccepted, EventHandoffException,
		EventActionApproved, EventActionExecuted,
		EventReply, EventOutcomeObserved, EventMeeting, EventProposal,
		EventPipelineCreated, EventPipelineUpdated,
		EventWon, EventLost, EventUnknownState, EventRevenueEvidenced,
		EventLearningCandidate, EventXRayCompleted, EventPageView,
		EventCitation, EventCorrection:
		return true
	default:
		return false
	}
}

// EventToFacts maps one envelope onto ObservedFacts. PIIPointer is
// discarded. Pipeline and revenue flags are set only from their events.
func EventToFacts(ev CommercialEvent) ObservedFacts {
	typ := strings.ToLower(strings.TrimSpace(ev.Type))
	ingested := ev.IngestedAt
	if ingested.IsZero() {
		ingested = time.Now().UTC()
	}
	occurred := ev.OccurredAt
	in := ObservedFacts{
		Keys: JoinKeys{
			OrganizationID:    strings.TrimSpace(ev.OrganizationID),
			Source:            strings.TrimSpace(ev.Source),
			Query:             strings.TrimSpace(ev.Query),
			AssetID:           strings.TrimSpace(ev.AssetID),
			CTAID:             strings.TrimSpace(ev.CTAID),
			CorrelationID:     strings.TrimSpace(ev.CorrelationID),
			LeadID:            strings.TrimSpace(ev.LeadID),
			ReceiptID:         strings.TrimSpace(ev.ReceiptID),
			AccountID:         firstNonEmpty(ev.AccountPublicID, ev.EntityPublicID),
			SourceLeadID:      strings.TrimSpace(ev.EntityPublicID),
			ActionID:          strings.TrimSpace(ev.ActionID),
			OutcomeID:         strings.TrimSpace(ev.OutcomeID),
			IdempotencyKey:    strings.TrimSpace(ev.IdempotencyKey),
			RouteFamily:       strings.TrimSpace(ev.RouteFamily),
			Trigger:           strings.TrimSpace(ev.Trigger),
			OfferID:           strings.TrimSpace(ev.OfferID),
			Route:             strings.TrimSpace(ev.Route),
			EventID:           strings.TrimSpace(ev.EventID),
			EventIDs:          eventIDList(ev),
			AssetFamily:       normalizeAssetFamily(ev.AssetFamily),
			MarketAnswerID:    strings.TrimSpace(ev.MarketAnswerID),
			AnalysisID:        strings.TrimSpace(ev.AnalysisID),
			Referrer:          strings.TrimSpace(ev.Referrer),
			IntentClass:       strings.TrimSpace(ev.IntentClass),
			ActorRef:          strings.TrimSpace(ev.ActorRef),
			EvidenceRef:       strings.TrimSpace(ev.EvidenceRef),
			Consent:           strings.TrimSpace(ev.Consent),
			ProducerSHA:       strings.TrimSpace(ev.ProducerSHA),
			Schema:            firstNonEmpty(ev.Schema, ev.Version),
			RevenueDocumentID: strings.TrimSpace(ev.RevenueDocumentID),
			CustomerProofLane: ev.CustomerProofLane,
		},
		LeadCreatedAt:     leadStamp(typ, occurred),
		IngestedAt:        ingested,
		ActionOccurredAt:  actionStamp(typ, occurred),
		OutcomeOccurredAt: outcomeStamp(typ, occurred),
		EventType:         typ,
		Qualified:         ev.Qualified || typ == EventLeadValidated,
		HumanConfirmed:    ev.HumanConfirmed,
		Correction:        ev.Correction || typ == EventCorrection,
		HandRaise:         ev.HandRaise,
		Suppression:       ev.Suppression,
		Timezone:          firstNonEmpty(ev.Timezone, "UTC"),
		PublishedAt:       ev.PublishedAt,
		DetectedAt:        ev.DetectedAt,
		Synthetic:         ev.Synthetic,
		Label:             labelFor(ev.Synthetic),
	}
	if ev.Synthetic {
		in.Label = LabelSynthetic
	}
	if ev.PublishedAt == nil && (typ == EventLeadReceived || typ == EventPageView || typ == EventCitation || typ == EventXRayCompleted) {
		t := occurred
		in.PublishedAt = &t
	}
	if ev.DetectedAt == nil && (typ == EventLeadReceived || typ == EventLeadValidated) {
		t := occurred
		if !ingested.IsZero() && !ingested.Before(occurred) {
			t = occurred
		}
		in.DetectedAt = &t
	}

	switch typ {
	case EventLeadReceived:
		in.NotALead = false
	case EventLeadValidated:
		in.Qualified = true
	case EventLeadRejected:
		in.OutcomeType = OutcomeUnknown
	case EventHandoffException:
		in.OutcomeType = OutcomeUnknown
	case EventActionApproved, EventActionExecuted:
		t := occurred
		in.FirstActionAt = &t
	case EventReply:
		in.Conversation = true
		in.OutcomeType = OutcomeReplied
		t := occurred
		in.ConversationAt = &t
	case EventOutcomeObserved:
		in.OutcomeType = normalizeOutcomeState(ev.OutcomeState)
		if in.OutcomeType == OutcomeReplied || in.OutcomeType == OutcomeQualifiedConversation {
			in.Conversation = true
			t := occurred
			in.ConversationAt = &t
		}
	case EventMeeting:
		in.OutcomeType = OutcomeMeeting
		in.Conversation = true
		t := occurred
		in.ConversationAt = &t
	case EventProposal:
		in.OutcomeType = OutcomeProposal
		t := occurred
		in.ProposalAt = &t
	case EventPipelineCreated, EventPipelineUpdated:
		in.PipelineOpen = true
	case EventWon:
		in.OutcomeType = OutcomeWon
		t := occurred
		in.CloseAt = &t
	case EventLost:
		in.OutcomeType = OutcomeLost
		t := occurred
		in.CloseAt = &t
	case EventUnknownState:
		in.OutcomeType = OutcomeUnknown
	case EventRevenueEvidenced:
		if strings.TrimSpace(ev.RevenueDocumentID) != "" && ev.RevenueCents > 0 {
			in.RevenueEvidenced = true
			in.RevenueCents = ev.RevenueCents
		}
	case EventXRayCompleted, EventPageView, EventCitation:
		in.NotALead = true
		in.PipelineOpen = false
		in.Qualified = false
	case EventCorrection:
		in.Correction = true
		in.OutcomeType = normalizeOutcomeState(ev.OutcomeState)
		if isWonType(in.OutcomeType) || isLostType(in.OutcomeType) {
			t := occurred
			in.CloseAt = &t
		}
	}

	if typ == EventLearningCandidate {
		in.OutcomeType = normalizeOutcomeState(ev.OutcomeState)
	}
	return in
}

func eventIDList(ev CommercialEvent) []string {
	var out []string
	if id := strings.TrimSpace(ev.EventID); id != "" {
		out = append(out, id)
	}
	if id := strings.TrimSpace(ev.IdempotencyKey); id != "" {
		out = append(out, "idem:"+id)
	}
	return out
}

func leadStamp(typ string, occurred time.Time) time.Time {
	switch typ {
	case EventLeadReceived, EventLeadValidated, EventLeadRejected:
		return occurred
	default:
		return time.Time{}
	}
}

func actionStamp(typ string, occurred time.Time) time.Time {
	switch typ {
	case EventActionApproved, EventActionExecuted:
		return occurred
	default:
		return time.Time{}
	}
}

func outcomeStamp(typ string, occurred time.Time) time.Time {
	switch typ {
	case EventReply, EventOutcomeObserved, EventMeeting, EventProposal,
		EventWon, EventLost, EventUnknownState, EventCorrection:
		return occurred
	default:
		return time.Time{}
	}
}

func labelFor(synthetic bool) string {
	if synthetic {
		return LabelSynthetic
	}
	return LabelReal
}

func normalizeOutcomeState(v string) string {
	u := strings.ToUpper(strings.TrimSpace(v))
	switch u {
	case OutcomeWon, EventWon:
		return OutcomeWon
	case OutcomeLost, EventLost:
		return OutcomeLost
	case OutcomeMeeting, EventMeeting:
		return OutcomeMeeting
	case OutcomeProposal, EventProposal:
		return OutcomeProposal
	case OutcomeQualifiedConversation, "QCO":
		return OutcomeQualifiedConversation
	case OutcomeReplied, EventReply:
		return OutcomeReplied
	case OutcomeContacted:
		return OutcomeContacted
	case OutcomeClient:
		return OutcomeClient
	case OutcomeNoResponse:
		return OutcomeNoResponse
	case OutcomeDoNotContact, "DNC":
		return OutcomeDoNotContact
	case "", OutcomeUnknown, EventUnknownState:
		return OutcomeUnknown
	default:
		return OutcomeUnknown
	}
}

func normalizeAssetFamily(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case AssetFamilyMarketAnswer, "market-answer", "marketanswer":
		return AssetFamilyMarketAnswer
	case AssetFamilyContractAnalysis, "contract-analysis", "contractanalysis":
		return AssetFamilyContractAnalysis
	case AssetFamilyB2GXRay, "b2g-xray", "b2gxray", "xray":
		return AssetFamilyB2GXRay
	case "":
		return ""
	default:
		return strings.ToLower(strings.TrimSpace(v))
	}
}

func knownAssetFamily(v string) bool {
	switch normalizeAssetFamily(v) {
	case AssetFamilyMarketAnswer, AssetFamilyContractAnalysis, AssetFamilyB2GXRay, "":
		return true
	default:
		return false
	}
}

// IngestEvent is the shipped entry for versioned events. Fixtures and
// future real events share this path. Same event_id or idempotency key
// is a replay.
func IngestEvent(store Store, ev CommercialEvent) JoinResult {
	if err := ValidateCommercialEvent(ev); err != nil {
		now := time.Now().UTC()
		ex := Exception{
			Code:       ExceptionOrphan,
			Reason:     "invalid envelope: " + err.Error(),
			NextAction: "fix the event and retry; do not invent a chain",
			At:         now,
			Synthetic:  ev.Synthetic,
		}
		if store != nil {
			_ = store.PutException(ex)
		}
		return JoinResult{Exceptions: []Exception{ex}, Held: true}
	}

	if existing := findSeenEvent(store, ev); existing != nil {
		return JoinResult{Chain: *existing, Replay: true, Held: existing.Held}
	}

	if ev.Type == EventLearningCandidate {
		facts := EventToFacts(ev)
		cand := EmitLearning(store, LearningInput{
			From:           LearningFromOutcome,
			Reason:         firstNonEmpty(ev.EvidenceRef, ev.OutcomeState, "learning_candidate"),
			OutcomeType:    facts.OutcomeType,
			HumanConfirmed: ev.HumanConfirmed,
			Keys:           facts.Keys,
			Synthetic:      ev.Synthetic,
		})
		res := Reconcile(store, facts)
		_ = cand
		return res
	}

	facts := EventToFacts(ev)
	res := Reconcile(store, facts)
	if ev.Type == EventCorrection && !res.Held {
		EmitLearning(store, LearningInput{
			From:            LearningFromCorrection,
			Reason:          firstNonEmpty(ev.EvidenceRef, "late_correction"),
			CorrectionCodes: []string{"correction"},
			OutcomeType:     facts.OutcomeType,
			HumanConfirmed:  ev.HumanConfirmed,
			Keys:            facts.Keys,
			Synthetic:       ev.Synthetic,
		})
	}
	return res
}

func findSeenEvent(store Store, ev CommercialEvent) *Chain {
	if store == nil {
		return nil
	}
	eventID := strings.TrimSpace(ev.EventID)
	idem := strings.TrimSpace(ev.IdempotencyKey)
	if eventID == "" && idem == "" {
		return nil
	}
	chains, err := store.ListChains(strings.TrimSpace(ev.OrganizationID))
	if err != nil {
		return nil
	}
	for i := range chains {
		for _, id := range chains[i].Keys.EventIDs {
			if eventID != "" && id == eventID {
				return &chains[i]
			}
			if idem != "" && id == "idem:"+idem {
				return &chains[i]
			}
		}
		if eventID != "" && chains[i].Keys.EventID == eventID {
			return &chains[i]
		}
	}
	return nil
}

func isNonLeadEvent(typ string) bool {
	switch strings.ToLower(strings.TrimSpace(typ)) {
	case EventXRayCompleted, EventPageView, EventCitation:
		return true
	default:
		return false
	}
}
