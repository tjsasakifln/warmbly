package intel

import (
	"strings"
	"time"
)

// ClassifyExceptions inspects one observed record against an optional
// existing chain. It never invents a causal order.
func ClassifyExceptions(in ObservedFacts, existing *Chain) []Exception {
	now := time.Now().UTC()
	identity := ChainIdentity(in.Keys)
	metric := MetricKey(in.Keys)
	base := Exception{
		OrganizationID: strings.TrimSpace(in.Keys.OrganizationID),
		Identity:       identity,
		MetricKey:      metric,
		ActionID:       strings.TrimSpace(in.Keys.ActionID),
		OutcomeID:      strings.TrimSpace(in.Keys.OutcomeID),
		AccountID:      strings.TrimSpace(in.Keys.AccountID),
		LeadID:         strings.TrimSpace(in.Keys.LeadID),
		Synthetic:      in.Synthetic,
		At:             now,
	}

	base.ReceiptID = strings.TrimSpace(in.Keys.ReceiptID)

	var out []Exception
	add := func(code, reason, next string, held bool) {
		ex := base
		ex.Code = code
		ex.Reason = reason
		ex.NextAction = next
		ex.Held = held
		if strings.TrimSpace(ex.Owner) == "" {
			ex.Owner = ExceptionOwner(code)
		}
		enrichException(&ex, in)
		out = append(out, ex)
	}

	if identity == "" {
		add(ExceptionOrphan, "no lead_id, receipt_id, action_id, or idempotency_key", "hold until a durable ID arrives", true)
	}

	hasOutcome := knownID(in.Keys.OutcomeID) != "" || (strings.TrimSpace(in.OutcomeType) != "" && in.OutcomeType != OutcomeUnknown)
	hasAction := knownID(in.Keys.ActionID) != "" || chainHasAction(existing)
	if hasOutcome && !hasAction && !in.NotALead {
		add(ExceptionOrphan, "outcome without action", "hold on exception queue until action arrives", true)
	}
	if normalizeFamily(in.Keys.RouteFamily) == FamilyInbound && strings.TrimSpace(in.Keys.LeadID) == "" && strings.TrimSpace(in.Keys.ReceiptID) == "" && !in.NotALead && !isNonLeadEvent(in.EventType) {
		add(ExceptionOrphan, "inbound family missing lead_id/receipt_id", "hold until the web-cfg receipt IDs arrive", true)
	}

	if existing != nil && identity != "" {
		if conflictAccount(existing.Keys, in.Keys) {
			add(ExceptionConflictingAccount, "incoming account_id disagrees with the first chain", "keep the first extra-cli account; do not overwrite", false)
		}
		if !additiveIDs(existing, in) {
			add(ExceptionDuplicate, "same join IDs already have a chain", "return the first chain; do not insert a second", false)
		}
	}

	if strings.TrimSpace(in.Keys.AccountID) != "" || strings.TrimSpace(in.Keys.SourceLeadID) != "" {
		if strings.TrimSpace(in.Keys.TargetFitVersion) == "" || strings.TrimSpace(in.Keys.ActivationPolicyVersion) == "" {
			add(ExceptionMissingVersion, "extra-cli account present without target_fit or activation policy version", "leave version UNKNOWN; do not invent a watermark", false)
		}
	} else if strings.TrimSpace(in.Keys.TargetFitVersion) == "" && strings.TrimSpace(in.Keys.ActivationPolicyVersion) == "" && (hasAction || strings.TrimSpace(in.Keys.LeadID) != "") {
		// Observed path with no extra-cli versions: record missing, stay UNKNOWN.
		add(ExceptionMissingVersion, "no extra-cli version on the observed path", "leave version UNKNOWN", false)
	}

	if in.AttributionStale || in.RequiresFresh {
		add(ExceptionStaleAttribution, "attribution or target-fit freshness is stale", "keep the observed IDs; do not invent a newer source/asset", false)
	}

	if outOfOrderAgainst(in, existing) {
		add(ExceptionOutOfOrder, "outcome timestamp precedes action or inbound; order is not invented", "hold; do not reorder into a fake chain", true)
	}

	if isWonType(in.OutcomeType) && !in.HumanConfirmed {
		add(ExceptionUnconfirmedWon, "WON cannot be inferred", "require human or document confirmation; keep UNKNOWN", true)
	}
	if isLostType(in.OutcomeType) && !in.HumanConfirmed {
		add(ExceptionUnconfirmedLost, "LOST cannot be inferred", "require human or document confirmation; keep UNKNOWN", true)
	}

	if in.Keys.AssetFamily != "" && !knownAssetFamily(in.Keys.AssetFamily) && normalizeAssetFamily(in.Keys.AssetFamily) != "" {
		add(ExceptionInvalidAssetFamily, "asset_family is not market_answer, contract_analysis, or b2g_xray", "exclude from assisted slices; do not invent a family", false)
	}
	if missingInboundAttribution(in) {
		add(ExceptionMissingAttribution, "inbound path missing source/query/asset", "keep UNKNOWN attribution; do not invent a source or asset", false)
	}
	if outboundLabeledInbound(in) {
		add(ExceptionOutboundAsInbound, "outbound action labeled inbound without a receipt", "hold off inbound denominators", true)
	}
	if code, reason := latencyViolation(in, existing); code != "" {
		add(code, reason, "hold; do not coerce a negative or overlapping duration", true)
	}
	if reason := impossibleTransition(in, existing); reason != "" {
		add(ExceptionImpossibleTransition, reason, "hold; do not invent the missing stage", true)
	}

	return out
}

func missingInboundAttribution(in ObservedFacts) bool {
	if normalizeFamily(in.Keys.RouteFamily) != FamilyInbound || in.NotALead {
		return false
	}
	if isNonLeadEvent(in.EventType) {
		return false
	}
	return knownID(in.Keys.Source) == "" || knownID(in.Keys.Query) == "" || knownID(in.Keys.AssetID) == ""
}

func outboundLabeledInbound(in ObservedFacts) bool {
	if normalizeFamily(in.Keys.RouteFamily) != FamilyInbound {
		return false
	}
	if knownID(in.Keys.LeadID) != "" || knownID(in.Keys.ReceiptID) != "" {
		return false
	}
	src := strings.ToLower(strings.TrimSpace(in.Keys.Source))
	return src == "extra-cli" || src == FamilyOutbound || src == "outbound-pilot"
}

func latencyViolation(in ObservedFacts, existing *Chain) (string, string) {
	type pair struct {
		a, b time.Time
		name string
	}
	lead := in.LeadCreatedAt
	if lead.IsZero() && existing != nil {
		lead = existing.LeadCreatedAt
	}
	var action time.Time
	if in.FirstActionAt != nil {
		action = *in.FirstActionAt
	} else if !in.ActionOccurredAt.IsZero() {
		action = in.ActionOccurredAt
	} else if existing != nil && existing.FirstActionAt != nil {
		action = *existing.FirstActionAt
	}
	var conv time.Time
	if in.ConversationAt != nil {
		conv = *in.ConversationAt
	} else if existing != nil && existing.ConversationAt != nil {
		conv = *existing.ConversationAt
	}
	var prop time.Time
	if in.ProposalAt != nil {
		prop = *in.ProposalAt
	} else if existing != nil && existing.ProposalAt != nil {
		prop = *existing.ProposalAt
	}
	var closeT time.Time
	if in.CloseAt != nil {
		closeT = *in.CloseAt
	} else if existing != nil && existing.CloseAt != nil {
		closeT = *existing.CloseAt
	}
	var pub, det time.Time
	if in.PublishedAt != nil {
		pub = *in.PublishedAt
	} else if existing != nil && existing.PublishedAt != nil {
		pub = *existing.PublishedAt
	}
	if in.DetectedAt != nil {
		det = *in.DetectedAt
	} else if existing != nil && existing.DetectedAt != nil {
		det = *existing.DetectedAt
	}
	pairs := []pair{
		{pub, det, "published→detected"},
		{det, lead, "detected→lead"},
		{lead, action, "lead→first action"},
		{action, conv, "action→conversation"},
		{conv, prop, "conversation→proposal"},
		{prop, closeT, "proposal→close"},
	}
	if !in.ActionOccurredAt.IsZero() && !lead.IsZero() && in.ActionOccurredAt.Before(lead) {
		return ExceptionNegativeLatency, "action timestamp precedes lead; duration is not coerced"
	}
	if !closeT.IsZero() && !lead.IsZero() && closeT.Before(lead) {
		return ExceptionNegativeLatency, "close timestamp precedes lead; duration is not coerced"
	}
	for _, p := range pairs {
		if p.a.IsZero() || p.b.IsZero() {
			continue
		}
		if p.b.Before(p.a) {
			return ExceptionNegativeLatency, p.name + " is negative; duration is not coerced"
		}
	}
	if !action.IsZero() && !prop.IsZero() && !conv.IsZero() && prop.Before(conv) {
		return ExceptionOverlappingLatency, "proposal overlaps conversation; duration is not coerced"
	}
	return "", ""
}

func impossibleTransition(in ObservedFacts, existing *Chain) string {
	typ := strings.ToLower(strings.TrimSpace(in.EventType))
	hasLead := knownID(in.Keys.LeadID) != "" || knownID(in.Keys.ReceiptID) != "" || (existing != nil && (knownID(existing.LeadID) != "" || knownID(existing.Keys.LeadID) != ""))
	hasPipeline := in.PipelineOpen || (existing != nil && existing.PipelineOpen)
	hasWon := (isWonType(in.OutcomeType) && in.HumanConfirmed) || (existing != nil && isWonType(existing.OutcomeType) && existing.HumanConfirmed)

	if typ == EventRevenueEvidenced && !hasWon && !hasPipeline {
		return "revenue_evidenced without pipeline or confirmed won"
	}
	if (typ == EventPipelineCreated || typ == EventPipelineUpdated) && normalizeFamily(in.Keys.RouteFamily) == FamilyInbound && !hasLead {
		return "inbound pipeline event without lead"
	}
	if (typ == EventWon || typ == EventLost) && normalizeFamily(in.Keys.RouteFamily) == FamilyInbound && !hasLead {
		return "inbound close without lead"
	}
	if existing != nil && contradictsConfirmed(*existing, strings.ToUpper(strings.TrimSpace(in.OutcomeType))) {
		return "incoming close contradicts a human-confirmed close"
	}
	if in.NotALead && (in.PipelineOpen || isWonType(in.OutcomeType)) {
		return "page view, citation, or X-Ray completion cannot become pipeline or won"
	}
	return ""
}

func chainHasAction(existing *Chain) bool {
	if existing == nil {
		return false
	}
	return knownID(existing.ActionID) != "" || knownID(existing.Keys.ActionID) != ""
}

func additiveIDs(existing *Chain, in ObservedFacts) bool {
	if existing == nil {
		return false
	}
	if inc := knownID(in.Keys.ActionID); inc != "" && knownID(existing.ActionID) == "" && knownID(existing.Keys.ActionID) == "" {
		return true
	}
	if inc := knownID(in.Keys.OutcomeID); inc != "" && knownID(existing.OutcomeID) == "" && knownID(existing.Keys.OutcomeID) == "" {
		return true
	}
	if inc := knownID(in.Keys.OutboxEventID); inc != "" && knownID(existing.OutboxEventID) == "" && knownID(existing.Keys.OutboxEventID) == "" {
		return true
	}
	if inc := knownID(in.Keys.ReceiptID); inc != "" && knownID(existing.ReceiptID) == "" && knownID(existing.Keys.ReceiptID) == "" {
		return true
	}
	return false
}

func knownID(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == Unknown {
		return ""
	}
	return v
}

func conflictAccount(a, b JoinKeys) bool {
	left := firstNonEmpty(a.AccountID, a.SourceLeadID)
	right := firstNonEmpty(b.AccountID, b.SourceLeadID)
	if left == "" || right == "" {
		return false
	}
	return left != right
}

func outOfOrder(in ObservedFacts) bool {
	return outOfOrderAgainst(in, nil)
}

func outOfOrderAgainst(in ObservedFacts, existing *Chain) bool {
	if in.OutcomeOccurredAt.IsZero() {
		return false
	}
	action := in.ActionOccurredAt
	if action.IsZero() && existing != nil && existing.FirstActionAt != nil {
		action = *existing.FirstActionAt
	}
	lead := in.LeadCreatedAt
	if lead.IsZero() && existing != nil {
		lead = existing.LeadCreatedAt
	}
	if !action.IsZero() && in.OutcomeOccurredAt.Before(action) {
		return true
	}
	if !lead.IsZero() && in.OutcomeOccurredAt.Before(lead) {
		return true
	}
	if !in.IngestedAt.IsZero() && in.OutcomeOccurredAt.Before(in.IngestedAt) && strings.TrimSpace(in.Keys.ActionID) == "" {
		return true
	}
	return false
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
