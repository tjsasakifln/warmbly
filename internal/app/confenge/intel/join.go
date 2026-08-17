package intel

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"
)

// ChainIdentity is the durable idempotency key for one observed path.
// lead_id wins when present so a replay of the same inbound receipt
// cannot open a second chain.
func ChainIdentity(k JoinKeys) string {
	if id := strings.TrimSpace(k.LeadID); id != "" {
		return "lead:" + id
	}
	if id := strings.TrimSpace(k.ReceiptID); id != "" {
		return "receipt:" + id
	}
	if id := strings.TrimSpace(k.ActionID); id != "" {
		return "action:" + id
	}
	if id := strings.TrimSpace(k.IdempotencyKey); id != "" {
		return "idem:" + id
	}
	if id := strings.TrimSpace(k.EventID); id != "" {
		return "event:" + id
	}
	return ""
}

// MetricKey hashes join IDs only. Email, phone, name, and company are
// never part of the material.
func MetricKey(k JoinKeys) string {
	events := append([]string{}, k.EventIDs...)
	sort.Strings(events)
	parts := []string{
		strings.TrimSpace(k.OrganizationID),
		strings.TrimSpace(k.Source),
		strings.TrimSpace(k.Query),
		strings.TrimSpace(k.AssetID),
		strings.TrimSpace(k.CTAID),
		strings.TrimSpace(k.CorrelationID),
		strings.TrimSpace(k.LeadID),
		strings.TrimSpace(k.ReceiptID),
		strings.TrimSpace(k.AccountID),
		strings.TrimSpace(k.SourceLeadID),
		strings.TrimSpace(k.PersonID),
		strings.Join(events, ","),
		strings.TrimSpace(k.TargetFitVersion),
		strings.TrimSpace(k.ActivationPolicyVersion),
		strings.TrimSpace(k.ActionID),
		strings.TrimSpace(k.OutcomeID),
		strings.TrimSpace(k.OutboxEventID),
		strings.TrimSpace(k.IdempotencyKey),
		strings.TrimSpace(k.RouteFamily),
		strings.TrimSpace(k.Trigger),
		strings.TrimSpace(k.OfferID),
		strings.TrimSpace(k.Route),
		strings.TrimSpace(k.EventID),
		strings.TrimSpace(k.AssetFamily),
		strings.TrimSpace(k.MarketAnswerID),
		strings.TrimSpace(k.AnalysisID),
		strings.TrimSpace(k.Referrer),
		strings.TrimSpace(k.IntentClass),
		strings.TrimSpace(k.OfferVersion),
		strings.TrimSpace(k.TermsVersion),
		strings.TrimSpace(k.ExternalReference),
		strings.TrimSpace(k.ProviderEventID),
		strings.TrimSpace(k.CompanyRef),
		strings.TrimSpace(k.CNPJHash),
		strings.TrimSpace(k.HoldID),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}

// MetricKeyContainsPII reports whether a metric key string (or the raw
// join material) carries obvious PII tokens. Shipped keys are hashes.
func MetricKeyContainsPII(key string) bool {
	low := strings.ToLower(key)
	for _, tok := range []string{"@", "email=", "phone=", "nome=", "name=", "cnpj=", "tel:", "company="} {
		if strings.Contains(low, tok) {
			return true
		}
	}
	return strings.Contains(key, " ")
}

// Reconcile joins one observed record into the store. Same IDs return
// the first chain. The store is fail-closed when nil.
func Reconcile(store Store, in ObservedFacts) JoinResult {
	now := time.Now().UTC()
	if store == nil {
		ex := Exception{
			Code:       ExceptionUnavailable,
			Reason:     "commercial intelligence store unavailable",
			NextAction: "retry with the same IDs; do not invent a chain",
			At:         now,
		}
		return JoinResult{Exceptions: []Exception{ex}, Held: true}
	}

	in.Keys.RouteFamily = normalizeFamily(in.Keys.RouteFamily)
	identity := ChainIdentity(in.Keys)
	metric := MetricKey(in.Keys)
	existing, _ := store.GetChain(in.Keys.OrganizationID, identity)
	exceptions := ClassifyExceptions(in, existing)

	closeBlocked := false
	held := false
	for _, ex := range exceptions {
		if ex.Code == ExceptionUnconfirmedWon || ex.Code == ExceptionUnconfirmedLost {
			closeBlocked = true
		}
		if ex.Held {
			held = true
		}
	}

	if identity == "" {
		persistExceptions(store, exceptions)
		return JoinResult{Exceptions: exceptions, Held: true}
	}

	if existing != nil {
		persistExceptions(store, exceptions)
		merged, changed := mergeIntoChain(*existing, in, closeBlocked, held)
		if !changed {
			return JoinResult{
				Chain:      *existing,
				Exceptions: exceptions,
				Created:    false,
				Replay:     true,
				Held:       existing.Held || held,
			}
		}
		if err := store.UpdateChain(merged); err != nil {
			ex := Exception{
				Code:       ExceptionUnavailable,
				Reason:     "update chain failed: " + err.Error(),
				NextAction: "retry with the same IDs",
				Identity:   identity,
				MetricKey:  metric,
				At:         now,
			}
			return JoinResult{Chain: *existing, Exceptions: []Exception{ex}, Held: true}
		}
		saved, _ := store.GetChain(in.Keys.OrganizationID, identity)
		if saved == nil {
			saved = &merged
		}
		return JoinResult{
			Chain:      *saved,
			Exceptions: exceptions,
			Created:    false,
			Replay:     false,
			Held:       saved.Held,
		}
	}

	chain := buildChain(in, identity, metric, now, closeBlocked, held)
	saved, created, err := store.PutChain(chain)
	if err != nil {
		ex := Exception{
			Code:       ExceptionUnavailable,
			Reason:     "put chain failed: " + err.Error(),
			NextAction: "retry with the same IDs",
			Identity:   identity,
			MetricKey:  metric,
			At:         now,
		}
		return JoinResult{Exceptions: []Exception{ex}, Held: true}
	}
	persistExceptions(store, exceptions)
	return JoinResult{
		Chain:      saved,
		Exceptions: exceptions,
		Created:    created,
		Replay:     !created,
		Held:       saved.Held,
	}
}

func buildChain(in ObservedFacts, identity, metric string, now time.Time, closeBlocked, held bool) Chain {
	k := in.Keys
	label := strings.TrimSpace(in.Label)
	if in.Synthetic {
		label = LabelSynthetic
	} else if label == "" {
		label = LabelReal
	}
	outcome := strings.ToUpper(strings.TrimSpace(in.OutcomeType))
	// Held keeps QCO/MEETING; only unconfirmed/held WON/LOST become UNKNOWN.
	if outcome == "" || closeOutcomeBlocked(outcome, in.HumanConfirmed, held, closeBlocked) {
		outcome = OutcomeUnknown
	}
	qualified := in.Qualified || outcome == OutcomeQualifiedConversation
	conversation := in.Conversation || in.ConversationAt != nil || outcome == OutcomeReplied
	// Pipeline is only opened by an explicit pipeline event. Lead,
	// conversation, meeting, and X-Ray completion never become pipeline.
	// Held exceptions do not open pipeline or evidence revenue.
	pipeline := in.PipelineOpen && !in.NotALead && !held
	if isWonType(outcome) || isLostType(outcome) {
		pipeline = false
	}
	if in.NotALead {
		qualified = false
		pipeline = false
	}
	closeOK := in.HumanConfirmed && !held && !closeBlocked && (isWonType(strings.ToUpper(strings.TrimSpace(in.OutcomeType))) || isLostType(strings.ToUpper(strings.TrimSpace(in.OutcomeType))))
	var closeAt *time.Time
	if !held {
		closeAt = in.CloseAt
	}
	return Chain{
		SchemaVersion:     SchemaV1,
		Identity:          identity,
		MetricKey:         metric,
		Keys:              k,
		Source:            idOrUnknown(k.Source),
		Query:             idOrUnknown(k.Query),
		AssetID:           idOrUnknown(k.AssetID),
		LeadID:            idOrUnknown(k.LeadID),
		ReceiptID:         idOrUnknown(k.ReceiptID),
		CorrelationID:     idOrUnknown(k.CorrelationID),
		AccountID:         idOrUnknown(k.AccountID),
		PersonID:          idOrUnknown(k.PersonID),
		ActionID:          idOrUnknown(k.ActionID),
		OutcomeID:         idOrUnknown(k.OutcomeID),
		OutboxEventID:     idOrUnknown(k.OutboxEventID),
		IdempotencyKey:    idOrUnknown(k.IdempotencyKey),
		RouteFamily:       idOrUnknown(k.RouteFamily),
		Trigger:           idOrUnknown(k.Trigger),
		OfferID:           idOrUnknown(k.OfferID),
		Route:             idOrUnknown(k.Route),
		CTAID:             idOrUnknown(k.CTAID),
		AssetFamily:       idOrUnknown(firstNonEmpty(k.AssetFamily, normalizeAssetFamily(k.AssetFamily))),
		MarketAnswerID:    idOrUnknown(k.MarketAnswerID),
		AnalysisID:        idOrUnknown(k.AnalysisID),
		Referrer:          idOrUnknown(k.Referrer),
		IntentClass:       idOrUnknown(k.IntentClass),
		CitationRoute:     idOrUnknown(k.CitationRoute),
		DistributionRoute: idOrUnknown(k.DistributionRoute),
		Versions: Versions{
			TargetFit:          idOrUnknown(k.TargetFitVersion),
			ActivationPolicy:   idOrUnknown(k.ActivationPolicyVersion),
			TargetFitWatermark: idOrUnknown(k.TargetFitWatermark),
			Fresh:              k.TargetFitFresh && !in.AttributionStale && !in.RequiresFresh,
		},
		LeadCreatedAt:     in.LeadCreatedAt,
		IngestedAt:        in.IngestedAt,
		EnrichmentAt:      in.EnrichmentAt,
		FirstActionAt:     in.FirstActionAt,
		ConversationAt:    in.ConversationAt,
		ProposalAt:        in.ProposalAt,
		CloseAt:           closeAt,
		PublishedAt:       in.PublishedAt,
		DetectedAt:        in.DetectedAt,
		OutcomeType:       outcome,
		HumanConfirmed:    closeOK,
		Qualified:         qualified,
		Conversation:      conversation,
		PipelineOpen:      pipeline,
		Held:              held,
		NotALead:          in.NotALead,
		RevenueEvidenced:  !held && in.RevenueEvidenced && strings.TrimSpace(k.RevenueDocumentID) != "" && in.RevenueCents > 0,
		RevenueCents:      heldRevenueCents(held, in),
		CorrectionApplied: in.Correction,
		EventType:         in.EventType,
		Timezone:          firstNonEmpty(in.Timezone, "UTC"),
		AttributionKind:   AssociationObserved,
		CausalProof:       false,
		Commercial:        in.Commercial,
		Synthetic:         in.Synthetic || label == LabelSynthetic,
		Label:             label,
		CreatedAt:         now,
	}
}

func heldRevenueCents(held bool, in ObservedFacts) int64 {
	if held {
		return 0
	}
	return revenueCents(in)
}

func revenueCents(in ObservedFacts) int64 {
	if !in.RevenueEvidenced || strings.TrimSpace(in.Keys.RevenueDocumentID) == "" || in.RevenueCents <= 0 {
		return 0
	}
	return in.RevenueCents
}

func mergeIntoChain(existing Chain, in ObservedFacts, closeBlocked, held bool) (Chain, bool) {
	merged := existing
	changed := false
	fill := func(dst *string, incoming string) {
		inc := knownID(incoming)
		if inc == "" {
			return
		}
		if knownID(*dst) == "" {
			*dst = inc
			changed = true
		}
	}
	fill(&merged.Keys.ActionID, in.Keys.ActionID)
	fill(&merged.ActionID, in.Keys.ActionID)
	fill(&merged.Keys.OutcomeID, in.Keys.OutcomeID)
	fill(&merged.OutcomeID, in.Keys.OutcomeID)
	fill(&merged.Keys.OutboxEventID, in.Keys.OutboxEventID)
	fill(&merged.OutboxEventID, in.Keys.OutboxEventID)
	fill(&merged.Keys.IdempotencyKey, in.Keys.IdempotencyKey)
	fill(&merged.IdempotencyKey, in.Keys.IdempotencyKey)
	fill(&merged.Keys.ReceiptID, in.Keys.ReceiptID)
	fill(&merged.ReceiptID, in.Keys.ReceiptID)
	fill(&merged.Keys.CorrelationID, in.Keys.CorrelationID)
	fill(&merged.CorrelationID, in.Keys.CorrelationID)
	fill(&merged.Keys.PersonID, in.Keys.PersonID)
	fill(&merged.PersonID, in.Keys.PersonID)
	fill(&merged.Keys.AssetID, in.Keys.AssetID)
	fill(&merged.AssetID, in.Keys.AssetID)
	fill(&merged.Keys.Query, in.Keys.Query)
	fill(&merged.Query, in.Keys.Query)
	fill(&merged.Keys.Trigger, in.Keys.Trigger)
	fill(&merged.Trigger, in.Keys.Trigger)
	fill(&merged.Keys.OfferID, in.Keys.OfferID)
	fill(&merged.OfferID, in.Keys.OfferID)
	fill(&merged.Keys.Route, in.Keys.Route)
	fill(&merged.Route, in.Keys.Route)
	fill(&merged.Keys.EventID, in.Keys.EventID)
	fill(&merged.Keys.AssetFamily, in.Keys.AssetFamily)
	fill(&merged.AssetFamily, in.Keys.AssetFamily)
	fill(&merged.Keys.MarketAnswerID, in.Keys.MarketAnswerID)
	fill(&merged.MarketAnswerID, in.Keys.MarketAnswerID)
	fill(&merged.Keys.AnalysisID, in.Keys.AnalysisID)
	fill(&merged.AnalysisID, in.Keys.AnalysisID)
	fill(&merged.Keys.Referrer, in.Keys.Referrer)
	fill(&merged.Referrer, in.Keys.Referrer)
	fill(&merged.Keys.IntentClass, in.Keys.IntentClass)
	fill(&merged.IntentClass, in.Keys.IntentClass)
	fill(&merged.Keys.CTAID, in.Keys.CTAID)
	fill(&merged.CTAID, in.Keys.CTAID)
	if !held {
		fill(&merged.Keys.RevenueDocumentID, in.Keys.RevenueDocumentID)
	}
	if !conflictAccount(existing.Keys, in.Keys) {
		fill(&merged.Keys.AccountID, in.Keys.AccountID)
		fill(&merged.AccountID, in.Keys.AccountID)
		fill(&merged.Keys.SourceLeadID, in.Keys.SourceLeadID)
	}
	for _, ev := range in.Keys.EventIDs {
		if knownID(ev) == "" {
			continue
		}
		found := false
		for _, have := range merged.Keys.EventIDs {
			if have == ev {
				found = true
				break
			}
		}
		if !found {
			merged.Keys.EventIDs = append(merged.Keys.EventIDs, ev)
			changed = true
		}
	}

	incoming := strings.ToUpper(strings.TrimSpace(in.OutcomeType))
	if closeOutcomeBlocked(incoming, in.HumanConfirmed, held, closeBlocked) {
		incoming = OutcomeUnknown
	}
	if incoming != "" && incoming != OutcomeUnknown {
		if merged.OutcomeType == "" || merged.OutcomeType == OutcomeUnknown {
			merged.OutcomeType = incoming
			changed = true
		} else if outcomeRank(incoming) > outcomeRank(merged.OutcomeType) && !contradictsConfirmed(merged, incoming) {
			merged.OutcomeType = incoming
			changed = true
		}
	}
	if !held && in.Correction && incoming != "" && incoming != OutcomeUnknown && !contradictsConfirmed(merged, incoming) {
		if merged.OutcomeType != incoming {
			merged.OutcomeType = incoming
			changed = true
		}
		if !merged.CorrectionApplied {
			merged.CorrectionApplied = true
			changed = true
		}
	}
	if !held && in.HumanConfirmed && (isWonType(in.OutcomeType) || isLostType(in.OutcomeType)) && !merged.HumanConfirmed {
		merged.HumanConfirmed = true
		changed = true
	}
	if in.Qualified && !merged.Qualified {
		merged.Qualified = true
		changed = true
	}
	if in.Conversation && !merged.Conversation {
		merged.Conversation = true
		changed = true
	}
	if in.FirstActionAt != nil && merged.FirstActionAt == nil {
		merged.FirstActionAt = in.FirstActionAt
		changed = true
	}
	if in.ConversationAt != nil && merged.ConversationAt == nil {
		merged.ConversationAt = in.ConversationAt
		changed = true
	}
	if in.ProposalAt != nil && merged.ProposalAt == nil {
		merged.ProposalAt = in.ProposalAt
		changed = true
	}
	if !held && in.CloseAt != nil && merged.CloseAt == nil && merged.HumanConfirmed {
		merged.CloseAt = in.CloseAt
		changed = true
	}
	if in.EnrichmentAt != nil && merged.EnrichmentAt == nil {
		merged.EnrichmentAt = in.EnrichmentAt
		changed = true
	}
	if in.PublishedAt != nil && merged.PublishedAt == nil {
		merged.PublishedAt = in.PublishedAt
		changed = true
	}
	if in.DetectedAt != nil && merged.DetectedAt == nil {
		merged.DetectedAt = in.DetectedAt
		changed = true
	}
	if !held && in.PipelineOpen && !merged.PipelineOpen && !in.NotALead && !isWonType(merged.OutcomeType) && !isLostType(merged.OutcomeType) {
		merged.PipelineOpen = true
		changed = true
	}
	if !held && in.RevenueEvidenced && strings.TrimSpace(in.Keys.RevenueDocumentID) != "" && in.RevenueCents > 0 && !merged.RevenueEvidenced {
		merged.RevenueEvidenced = true
		merged.RevenueCents = in.RevenueCents
		changed = true
	}
	if in.NotALead && !merged.NotALead && knownID(merged.LeadID) == "" {
		merged.NotALead = true
		changed = true
	}
	if in.EventType != "" && merged.EventType == "" {
		merged.EventType = in.EventType
		changed = true
	}
	if held && !merged.Held {
		merged.Held = true
		changed = true
	}
	if (in.Synthetic || in.Label == LabelSynthetic) && !merged.Synthetic {
		merged.Synthetic = true
		merged.Label = LabelSynthetic
		changed = true
	}

	qualified := merged.Qualified || merged.OutcomeType == OutcomeQualifiedConversation
	conversation := merged.Conversation || merged.ConversationAt != nil || merged.OutcomeType == OutcomeReplied
	pipeline := merged.PipelineOpen
	if isWonType(merged.OutcomeType) || isLostType(merged.OutcomeType) {
		pipeline = false
	}
	if merged.NotALead {
		qualified = false
		pipeline = false
	}
	if qualified != merged.Qualified || conversation != merged.Conversation || pipeline != merged.PipelineOpen {
		merged.Qualified = qualified
		merged.Conversation = conversation
		merged.PipelineOpen = pipeline
		changed = true
	}

	if in.Commercial.Offer.OfferID != "" || len(in.Commercial.Timeline) > 0 {
		merged.Commercial = mergeCommercial(merged.Commercial, in.Commercial, CommercialEvent{Type: in.EventType})
		if len(in.Commercial.Timeline) > 0 {
			merged.Commercial.Timeline = in.Commercial.Timeline
		}
		merged.Commercial.Payment = in.Commercial.Payment
		merged.Commercial.Subscription = in.Commercial.Subscription
		merged.Commercial.Delivery = in.Commercial.Delivery
		merged.Commercial.Capacity = in.Commercial.Capacity
		changed = true
	}

	if changed {
		merged.MetricKey = MetricKey(merged.Keys)
		merged.AttributionKind = AssociationObserved
		merged.CausalProof = false
	}
	return merged, changed
}

func closeOutcomeBlocked(outcome string, humanConfirmed, held, closeBlocked bool) bool {
	if closeBlocked {
		return true
	}
	if isWonType(outcome) || isLostType(outcome) {
		return held || !humanConfirmed
	}
	return false
}

func persistExceptions(store Store, xs []Exception) {
	if store == nil {
		return
	}
	for i := range xs {
		_ = store.PutException(xs[i])
	}
}

func outcomeRank(t string) int {
	switch strings.ToUpper(strings.TrimSpace(t)) {
	case OutcomeContacted:
		return 1
	case OutcomeReplied:
		return 2
	case OutcomeQualifiedConversation:
		return 3
	case OutcomeMeeting:
		return 4
	case OutcomeProposal:
		return 5
	case OutcomeWon, OutcomeClient, OutcomeLost, OutcomeDoNotContact:
		return 6
	default:
		return 0
	}
}

func contradictsConfirmed(existing Chain, incoming string) bool {
	if !existing.HumanConfirmed {
		return false
	}
	have := strings.ToUpper(strings.TrimSpace(existing.OutcomeType))
	if have == "" || have == OutcomeUnknown {
		return false
	}
	if isWonType(have) && (isLostType(incoming) || incoming == OutcomeDoNotContact) {
		return true
	}
	if isLostType(have) && isWonType(incoming) {
		return true
	}
	return false
}
