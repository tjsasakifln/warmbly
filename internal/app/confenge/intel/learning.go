package intel

import (
	"strings"
	"time"
)

// EmitLearning records a LEARNING CANDIDATE from a human correction or
// a recorded outcome. It never writes extra-cli, web-cfg, or SmartLic.
func EmitLearning(store Store, in LearningInput) LearningCandidate {
	now := time.Now().UTC()
	target := inferLearningTarget(in)
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = Unknown
	}
	if target == "" {
		target = Unknown
	}
	cand := LearningCandidate{
		OrganizationID:  strings.TrimSpace(in.Keys.OrganizationID),
		Kind:            LearningKind,
		Target:          target,
		Source:          normalizeLearningFrom(in.From),
		Reason:          reason,
		Status:          LearningPending,
		Identity:        ChainIdentity(in.Keys),
		MetricKey:       MetricKey(in.Keys),
		AssetID:         strings.TrimSpace(in.Keys.AssetID),
		OfferID:         strings.TrimSpace(in.Keys.OfferID),
		ActionID:        strings.TrimSpace(in.Keys.ActionID),
		OutcomeID:       strings.TrimSpace(in.Keys.OutcomeID),
		LeadID:          strings.TrimSpace(in.Keys.LeadID),
		CorrectionCodes: append([]string{}, in.CorrectionCodes...),
		OutcomeType:     strings.ToUpper(strings.TrimSpace(in.OutcomeType)),
		UpstreamWrites:  []string{},
		Synthetic:       in.Synthetic,
		At:              now,
	}
	if isWonType(in.OutcomeType) && !in.HumanConfirmed {
		cand.OutcomeType = OutcomeUnknown
		cand.Reason = "WON without human confirmation stays UNKNOWN"
		if cand.Target == TargetOffer {
			cand.Target = Unknown
		}
	}
	if store == nil {
		return cand
	}
	saved, _ := store.PutLearning(cand)
	return saved
}

func normalizeLearningFrom(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case LearningFromCorrection, "human_correction", "edit", "reject":
		return LearningFromCorrection
	case LearningFromOutcome:
		return LearningFromOutcome
	default:
		return Unknown
	}
}

func inferLearningTarget(in LearningInput) string {
	codes := append([]string{}, in.CorrectionCodes...)
	if in.Reason != "" {
		codes = append(codes, in.Reason)
	}
	for _, c := range codes {
		switch strings.ToLower(strings.TrimSpace(c)) {
		case "wrong_service", "wrong_offer", "offer", "unclear_value", "bad_cta":
			return TargetOffer
		case "weak_hook", "generic_copy", "awkward_tone", "unsupported_claim", "content":
			return TargetContent
		case "invalid_route", "channel_preference", "preferred_channel", "distribution":
			return TargetDistribution
		case "wrong_person", "wrong_recipient", "wrong_hook", "demand":
			return TargetDemand
		case "asset", "wrong_asset":
			return TargetAsset
		}
	}
	if strings.TrimSpace(in.Keys.OfferID) != "" && normalizeLearningFrom(in.From) == LearningFromOutcome {
		switch strings.ToUpper(strings.TrimSpace(in.OutcomeType)) {
		case OutcomeLost, OutcomeWon, OutcomeClient, OutcomeProposal:
			return TargetOffer
		case OutcomeQualifiedConversation, OutcomeMeeting:
			return TargetDemand
		}
	}
	if strings.TrimSpace(in.Keys.AssetID) != "" && normalizeLearningFrom(in.From) == LearningFromCorrection {
		return TargetAsset
	}
	if normalizeLearningFrom(in.From) == LearningFromOutcome {
		switch strings.ToUpper(strings.TrimSpace(in.OutcomeType)) {
		case OutcomeLost, OutcomeNoResponse:
			return TargetOffer
		case OutcomeQualifiedConversation, OutcomeMeeting, OutcomeReplied:
			return TargetDemand
		case OutcomeWon, OutcomeClient:
			if in.HumanConfirmed {
				return TargetOffer
			}
			return Unknown
		}
	}
	if strings.TrimSpace(in.Keys.AssetID) == "" && strings.TrimSpace(in.Keys.OfferID) == "" && len(in.CorrectionCodes) == 0 && strings.TrimSpace(in.Reason) == "" {
		return Unknown
	}
	return Unknown
}
