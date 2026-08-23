package intel

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

// ControlledEmailOutcomeSlice is one (route class, source, provider, cohort, policy) bucket.
type ControlledEmailOutcomeSlice struct {
	RouteClass    string `json:"route_class"`
	Source        string `json:"source"`
	Provider      string `json:"provider"`
	CohortID      string `json:"cohort_id"`
	PolicyVersion string `json:"policy_version"`
	// Nullable counts are intentional: no event is not proof of zero. A zero is
	// emitted only when a reconciled/classified observation establishes it.
	Attempted         *int `json:"attempted"`
	ProviderAccepted  *int `json:"provider_accepted"`
	Delivered         *int `json:"delivered"`
	HardBounce        *int `json:"hard_bounce"`
	SoftBounce        *int `json:"soft_bounce"`
	Reply             *int `json:"reply"`
	PositiveReply     *int `json:"positive_reply"`
	RoutedForwarded   *int `json:"routed_forwarded_reply"`
	OptOut            *int `json:"opt_out"`
	SpamComplaint     *int `json:"spam_complaint"`
	Meeting           *int `json:"meeting"`
	Proposal          *int `json:"proposal"`
	QualifiedPipeline *int `json:"qualified_pipeline"`
	ObservedRevenue   *int `json:"observed_revenue"`
	Unknown           *int `json:"unknown"`
}

func orUnknown(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return OutcomeUnknown
	}
	return v
}

func normalizeControlledProvider(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "smtp", "smtp_imap":
		return "smtp_imap"
	case "gmail", "google":
		return "gmail"
	case "outlook", "microsoft", "graph":
		return "outlook"
	default:
		return orUnknown(value)
	}
}

func SliceControlledEmailOutcomes(events []CommercialEvent) []ControlledEmailOutcomeSlice {
	idx := map[string]int{}
	var out []ControlledEmailOutcomeSlice
	keyOf := func(ev CommercialEvent) string {
		return strings.Join([]string{
			orUnknown(ev.EmailRouteClass),
			orUnknown(ev.Source),
			normalizeControlledProvider(ev.ProviderName),
			orUnknown(ev.CohortID),
			orUnknown(ev.PolicyVersion),
		}, "|")
	}
	bump := func(ev CommercialEvent) *ControlledEmailOutcomeSlice {
		k := keyOf(ev)
		if i, ok := idx[k]; ok {
			return &out[i]
		}
		out = append(out, ControlledEmailOutcomeSlice{
			RouteClass:    orUnknown(ev.EmailRouteClass),
			Source:        orUnknown(ev.Source),
			Provider:      normalizeControlledProvider(ev.ProviderName),
			CohortID:      orUnknown(ev.CohortID),
			PolicyVersion: orUnknown(ev.PolicyVersion),
		})
		idx[k] = len(out) - 1
		return &out[len(out)-1]
	}
	seen := map[string]bool{}
	for _, ev := range events {
		dedupeKey := strings.TrimSpace(ev.EventID)
		if dedupeKey == "" {
			dedupeKey = strings.TrimSpace(ev.IdempotencyKey)
		}
		if dedupeKey != "" {
			if seen[dedupeKey] {
				continue
			}
			seen[dedupeKey] = true
		}
		row := bump(ev)
		typ := strings.ToLower(strings.TrimSpace(ev.Type))
		switch {
		case typ == EventActionExecuted || typ == "attempted" || typ == "email_attempted":
			incrementObserved(&row.Attempted)
		case typ == "provider_accepted" || typ == "accepted":
			incrementObserved(&row.ProviderAccepted)
		case typ == "delivered":
			incrementObserved(&row.Delivered)
		case typ == "hard_bounce" || strings.EqualFold(ev.BounceClass, "HARD"):
			incrementObserved(&row.HardBounce)
		case typ == "soft_bounce" || strings.EqualFold(ev.BounceClass, "SOFT"):
			incrementObserved(&row.SoftBounce)
		case typ == EventReply || typ == "reply":
			incrementObserved(&row.Reply)
			if isClassifiedReply(ev.ReplyClass) {
				observeZero(&row.PositiveReply)
				observeZero(&row.RoutedForwarded)
			}
			if isPositiveReply(ev.ReplyClass) {
				incrementObserved(&row.PositiveReply)
			}
			if isRoutedReply(ev.ReplyClass) {
				incrementObserved(&row.RoutedForwarded)
			}
		case typ == "opt_out" || ev.Suppression:
			incrementObserved(&row.OptOut)
			if normalizedReplyClass(ev.ReplyClass) == "OPT_OUT" {
				incrementObserved(&row.Reply)
				observeZero(&row.PositiveReply)
				observeZero(&row.RoutedForwarded)
			}
		case typ == "spam_complaint":
			incrementObserved(&row.SpamComplaint)
		case typ == EventMeeting || typ == "meeting":
			incrementObserved(&row.Meeting)
		case typ == EventProposal || typ == "proposal":
			incrementObserved(&row.Proposal)
		case typ == EventPipelineCreated || typ == "qualified_pipeline":
			incrementObserved(&row.QualifiedPipeline)
		case typ == EventRevenueEvidenced:
			incrementObserved(&row.ObservedRevenue)
		case typ == "no_reply":
			incrementObserved(&row.Unknown)
		default:
			if typ == "" {
				incrementObserved(&row.Unknown)
			}
		}
	}
	return out
}

func incrementObserved(value **int) {
	if *value == nil {
		zero := 0
		*value = &zero
	}
	**value = **value + 1
}

func observeZero(value **int) {
	if *value == nil {
		zero := 0
		*value = &zero
	}
}

func normalizedReplyClass(class string) string {
	return strings.ToUpper(strings.TrimSpace(class))
}

func isPositiveReply(class string) bool {
	switch normalizedReplyClass(class) {
	case "POSITIVE", "POSITIVE_INTEREST", "OFFER_ACCEPTED", "SEND_MORE_INFO":
		return true
	default:
		return false
	}
}

func isRoutedReply(class string) bool {
	switch normalizedReplyClass(class) {
	case "ROUTED", "FORWARDED", "ROUTED/FORWARDED", "REFERRAL":
		return true
	default:
		return false
	}
}

func isClassifiedReply(class string) bool {
	switch normalizedReplyClass(class) {
	case "POSITIVE", "POSITIVE_INTEREST", "OFFER_ACCEPTED", "SEND_MORE_INFO",
		"NEGATIVE", "ROUTED", "FORWARDED", "ROUTED/FORWARDED", "REFERRAL",
		"OPT_OUT", "NEUTRAL":
		return true
	default:
		return false
	}
}

// NonReplyDoesNotInvalidateMailbox is the explicit contract: no-reply is UNKNOWN, not bounce.
func NonReplyDoesNotInvalidateMailbox(ev CommercialEvent) bool {
	typ := strings.ToLower(strings.TrimSpace(ev.Type))
	return typ == "no_reply" || typ == EventUnknownState
}

// ControlledEmailExecutiveReport is the founder-facing table. No composite score.
type ControlledEmailExecutiveReport struct {
	Rows []ControlledEmailOutcomeSlice `json:"rows"`
}

func BuildControlledEmailExecutiveReport(events []CommercialEvent) ControlledEmailExecutiveReport {
	return ControlledEmailExecutiveReport{Rows: SliceControlledEmailOutcomes(events)}
}

func AttachControlledEmail(rep *ObservabilityReport, events []CommercialEvent) {
	if rep == nil {
		return
	}
	rep.ControlledEmail = SliceControlledEmailOutcomes(events)
}

// ControlledEmailEventsFromChains reconstructs the cohort read model from the
// immutable Postgres-backed commercial timeline. It does not query a second
// ledger and it applies the same synthetic exclusion as the executive view.
func ControlledEmailEventsFromChains(chains []Chain, includeSynthetic bool) []CommercialEvent {
	var events []CommercialEvent
	for _, chain := range chains {
		if !includeSynthetic && (chain.Synthetic || chain.Label == LabelSynthetic) {
			continue
		}
		for _, receipt := range chain.Commercial.Timeline {
			if !isControlledEmailReceipt(receipt) {
				continue
			}
			events = append(events, CommercialEvent{
				EventID: receipt.EventID, Type: receipt.Type,
				OccurredAt: receipt.OccurredAt, IngestedAt: receipt.IngestedAt,
				OrganizationID: chain.Keys.OrganizationID, Synthetic: chain.Synthetic,
				EmailRouteClass: receipt.EmailRouteClass, Source: receipt.Source,
				ProviderName: receipt.ProviderName, CohortID: receipt.CohortID,
				PolicyVersion: receipt.PolicyVersion, BounceClass: receipt.BounceClass,
				ReplyClass: receipt.ReplyClass, AccountPublicID: receipt.AccountPublicID,
				EntityPublicID: receipt.TouchpointID, SMTPStatus: receipt.SMTPStatus,
				EnhancedStatus: receipt.EnhancedStatus, Diagnostic: receipt.Diagnostic,
			})
		}
	}
	return events
}

func isControlledEmailReceipt(receipt CommercialReceipt) bool {
	switch strings.ToLower(strings.TrimSpace(receipt.Type)) {
	case EventEmailAttempted, EventProviderAccepted, EventDelivered, EventHardBounce,
		EventSoftBounce, EventReply, EventOptOut, EventSpamComplaint, EventNoReply:
		return true
	default:
		return receipt.CohortID != "" || receipt.EmailRouteClass != ""
	}
}

func FormatControlledEmailReport(rep ControlledEmailExecutiveReport) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "route_class\tcohort_id\tattempted\tprovider_accepted\tdelivered\thard_bounce\tsoft_bounce\treply\tpositive_reply\trouted_or_forwarded\topt_out\tspam_complaint\tmeeting\tproposal\tqualified_pipeline\tobserved_revenue\tunknown\n")
	rows := append([]ControlledEmailOutcomeSlice{}, rep.Rows...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].RouteClass < rows[j].RouteClass })
	for _, r := range rows {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			orUnknown(r.RouteClass), orUnknown(r.CohortID), metric(r.Attempted), metric(r.ProviderAccepted), metric(r.Delivered),
			metric(r.HardBounce), metric(r.SoftBounce), metric(r.Reply), metric(r.PositiveReply), metric(r.RoutedForwarded),
			metric(r.OptOut), metric(r.SpamComplaint), metric(r.Meeting), metric(r.Proposal), metric(r.QualifiedPipeline), metric(r.ObservedRevenue), metric(r.Unknown))
	}
	for _, r := range rows {
		if observedPositive(r.Attempted) && observedPositive(r.Reply) {
			fmt.Fprintf(&b, "rate reply/attempted %s=%d/%d\n", orUnknown(r.RouteClass), *r.Reply, *r.Attempted)
		}
		if observedPositive(r.ProviderAccepted) && observedPositive(r.HardBounce) {
			fmt.Fprintf(&b, "rate hard_bounce/accepted %s=%d/%d\n", orUnknown(r.RouteClass), *r.HardBounce, *r.ProviderAccepted)
		}
	}
	return b.String()
}

func metric(value *int) string {
	if value == nil {
		return Unknown
	}
	return fmt.Sprintf("%d", *value)
}

func observedPositive(value *int) bool {
	return value != nil && *value > 0
}
