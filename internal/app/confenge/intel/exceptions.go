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

	var out []Exception
	add := func(code, reason, next string, held bool) {
		ex := base
		ex.Code = code
		ex.Reason = reason
		ex.NextAction = next
		ex.Held = held
		out = append(out, ex)
	}

	if identity == "" {
		add(ExceptionOrphan, "no lead_id, receipt_id, action_id, or idempotency_key", "hold until a durable ID arrives", true)
	}

	hasOutcome := strings.TrimSpace(in.Keys.OutcomeID) != "" || strings.TrimSpace(in.OutcomeType) != ""
	hasAction := strings.TrimSpace(in.Keys.ActionID) != ""
	if hasOutcome && !hasAction {
		add(ExceptionOrphan, "outcome without action", "hold on exception queue until action arrives", true)
	}
	if normalizeFamily(in.Keys.RouteFamily) == FamilyInbound && strings.TrimSpace(in.Keys.LeadID) == "" && strings.TrimSpace(in.Keys.ReceiptID) == "" {
		add(ExceptionOrphan, "inbound family missing lead_id/receipt_id", "hold until the web-cfg receipt IDs arrive", true)
	}

	if existing != nil && identity != "" {
		add(ExceptionDuplicate, "same join IDs already have a chain", "return the first chain; do not insert a second", false)
		if conflictAccount(existing.Keys, in.Keys) {
			add(ExceptionConflictingAccount, "incoming account_id disagrees with the first chain", "keep the first extra-cli account; do not overwrite", false)
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

	if outOfOrder(in) {
		add(ExceptionOutOfOrder, "outcome timestamp precedes action or inbound; order is not invented", "hold; do not reorder into a fake chain", true)
	}

	if isWonType(in.OutcomeType) && !in.HumanConfirmed {
		add(ExceptionUnconfirmedWon, "WON cannot be inferred", "require human or document confirmation; keep UNKNOWN", true)
	}

	return out
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
	if in.OutcomeOccurredAt.IsZero() {
		return false
	}
	if !in.ActionOccurredAt.IsZero() && in.OutcomeOccurredAt.Before(in.ActionOccurredAt) {
		return true
	}
	if !in.LeadCreatedAt.IsZero() && in.OutcomeOccurredAt.Before(in.LeadCreatedAt) {
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
