package intel

import "strings"

// ControlledEmailOutcomeSlice is one (route class, source, provider, cohort, policy) bucket.
type ControlledEmailOutcomeSlice struct {
	RouteClass        string `json:"route_class"`
	Source            string `json:"source"`
	Provider          string `json:"provider"`
	CohortID          string `json:"cohort_id"`
	PolicyVersion     string `json:"policy_version"`
	Attempted         int    `json:"attempted"`
	ProviderAccepted  int    `json:"provider_accepted"`
	Delivered         int    `json:"delivered"`
	HardBounce        int    `json:"hard_bounce"`
	SoftBounce        int    `json:"soft_bounce"`
	Reply             int    `json:"reply"`
	PositiveReply     int    `json:"positive_reply"`
	RoutedForwarded   int    `json:"routed_forwarded_reply"`
	OptOut            int    `json:"opt_out"`
	SpamComplaint     int    `json:"spam_complaint"`
	Meeting           int    `json:"meeting"`
	Proposal          int    `json:"proposal"`
	QualifiedPipeline int    `json:"qualified_pipeline"`
	ObservedRevenue   int    `json:"observed_revenue"`
	Unknown           int    `json:"unknown"`
}

func orUnknown(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return OutcomeUnknown
	}
	return v
}

func SliceControlledEmailOutcomes(events []CommercialEvent) []ControlledEmailOutcomeSlice {
	idx := map[string]int{}
	var out []ControlledEmailOutcomeSlice
	keyOf := func(ev CommercialEvent) string {
		return strings.Join([]string{
			orUnknown(ev.EmailRouteClass),
			orUnknown(ev.Source),
			orUnknown(ev.ProviderName),
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
			Provider:      orUnknown(ev.ProviderName),
			CohortID:      orUnknown(ev.CohortID),
			PolicyVersion: orUnknown(ev.PolicyVersion),
		})
		idx[k] = len(out) - 1
		return &out[len(out)-1]
	}
	for _, ev := range events {
		row := bump(ev)
		typ := strings.ToLower(strings.TrimSpace(ev.Type))
		switch {
		case typ == EventActionExecuted || typ == "attempted" || typ == "email_attempted":
			row.Attempted++
		case typ == "provider_accepted" || typ == "accepted":
			row.ProviderAccepted++
		case typ == "delivered":
			row.Delivered++
		case typ == "hard_bounce" || strings.EqualFold(ev.BounceClass, "HARD"):
			row.HardBounce++
		case typ == "soft_bounce" || strings.EqualFold(ev.BounceClass, "SOFT"):
			row.SoftBounce++
		case typ == EventReply || typ == "reply":
			row.Reply++
			if strings.EqualFold(ev.ReplyClass, "POSITIVE") {
				row.PositiveReply++
			}
			if strings.EqualFold(ev.ReplyClass, "ROUTED") || strings.EqualFold(ev.ReplyClass, "FORWARDED") {
				row.RoutedForwarded++
			}
		case typ == "opt_out" || ev.Suppression:
			row.OptOut++
		case typ == "spam_complaint":
			row.SpamComplaint++
		case typ == EventMeeting || typ == "meeting":
			row.Meeting++
		case typ == EventProposal || typ == "proposal":
			row.Proposal++
		case typ == EventPipelineCreated || typ == "qualified_pipeline":
			row.QualifiedPipeline++
		case typ == EventRevenueEvidenced:
			row.ObservedRevenue++
		case typ == "no_reply":
			row.Unknown++
		default:
			if typ == "" {
				row.Unknown++
			}
		}
	}
	return out
}

// NonReplyDoesNotInvalidateMailbox is the explicit contract: no-reply is UNKNOWN, not bounce.
func NonReplyDoesNotInvalidateMailbox(ev CommercialEvent) bool {
	typ := strings.ToLower(strings.TrimSpace(ev.Type))
	return typ == "no_reply" || typ == EventUnknownState
}
