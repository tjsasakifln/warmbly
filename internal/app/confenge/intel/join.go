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
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}

// MetricKeyContainsPII reports whether a metric key string (or the raw
// join material) carries obvious PII tokens. Shipped keys are hashes.
func MetricKeyContainsPII(key string) bool {
	low := strings.ToLower(key)
	for _, tok := range []string{"@", "email=", "phone=", "nome=", "name=", "cnpj=", "tel:"} {
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

	wonBlocked := false
	held := false
	for _, ex := range exceptions {
		if ex.Code == ExceptionUnconfirmedWon {
			wonBlocked = true
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
		return JoinResult{
			Chain:      *existing,
			Exceptions: exceptions,
			Created:    false,
			Replay:     true,
			Held:       existing.Held || held,
		}
	}

	chain := buildChain(in, identity, metric, now, wonBlocked, held)
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

func buildChain(in ObservedFacts, identity, metric string, now time.Time, wonBlocked, held bool) Chain {
	k := in.Keys
	label := strings.TrimSpace(in.Label)
	if in.Synthetic {
		label = LabelSynthetic
	} else if label == "" {
		label = LabelReal
	}
	outcome := strings.ToUpper(strings.TrimSpace(in.OutcomeType))
	if outcome == "" || wonBlocked {
		outcome = OutcomeUnknown
	}
	if isWonType(outcome) && !in.HumanConfirmed {
		outcome = OutcomeUnknown
	}
	qualified := in.Qualified || outcome == OutcomeQualifiedConversation
	conversation := in.Conversation || in.ConversationAt != nil || qualified || outcome == OutcomeReplied
	pipeline := in.PipelineOpen
	if !isWonType(outcome) && !isLostType(outcome) && outcome != OutcomeDoNotContact {
		if qualified || conversation || outcome == OutcomeMeeting || outcome == OutcomeProposal || outcome == OutcomeContacted {
			pipeline = true
		}
	}
	if isWonType(outcome) || isLostType(outcome) {
		pipeline = false
	}
	return Chain{
		SchemaVersion:  SchemaV1,
		Identity:       identity,
		MetricKey:      metric,
		Keys:           k,
		Source:         idOrUnknown(k.Source),
		Query:          idOrUnknown(k.Query),
		AssetID:        idOrUnknown(k.AssetID),
		LeadID:         idOrUnknown(k.LeadID),
		ReceiptID:      idOrUnknown(k.ReceiptID),
		CorrelationID:  idOrUnknown(k.CorrelationID),
		AccountID:      idOrUnknown(k.AccountID),
		PersonID:       idOrUnknown(k.PersonID),
		ActionID:       idOrUnknown(k.ActionID),
		OutcomeID:      idOrUnknown(k.OutcomeID),
		OutboxEventID:  idOrUnknown(k.OutboxEventID),
		IdempotencyKey: idOrUnknown(k.IdempotencyKey),
		RouteFamily:    idOrUnknown(k.RouteFamily),
		Trigger:        idOrUnknown(k.Trigger),
		OfferID:        idOrUnknown(k.OfferID),
		Route:          idOrUnknown(k.Route),
		Versions: Versions{
			TargetFit:          idOrUnknown(k.TargetFitVersion),
			ActivationPolicy:   idOrUnknown(k.ActivationPolicyVersion),
			TargetFitWatermark: idOrUnknown(k.TargetFitWatermark),
			Fresh:              k.TargetFitFresh && !in.AttributionStale && !in.RequiresFresh,
		},
		LeadCreatedAt:   in.LeadCreatedAt,
		IngestedAt:      in.IngestedAt,
		EnrichmentAt:    in.EnrichmentAt,
		FirstActionAt:   in.FirstActionAt,
		ConversationAt:  in.ConversationAt,
		ProposalAt:      in.ProposalAt,
		CloseAt:         in.CloseAt,
		OutcomeType:     outcome,
		HumanConfirmed:  in.HumanConfirmed && isWonType(strings.ToUpper(strings.TrimSpace(in.OutcomeType))),
		Qualified:       qualified,
		Conversation:    conversation,
		PipelineOpen:    pipeline,
		Held:            held,
		AttributionKind: AssociationObserved,
		CausalProof:     false,
		Synthetic:       in.Synthetic || label == LabelSynthetic,
		Label:           label,
		CreatedAt:       now,
	}
}

func persistExceptions(store Store, xs []Exception) {
	if store == nil {
		return
	}
	for i := range xs {
		_ = store.PutException(xs[i])
	}
}
