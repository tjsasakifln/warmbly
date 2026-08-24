package confenge

import (
	"fmt"
	"strings"
	"time"

	"github.com/warmbly/warmbly/internal/models"
)

// previewAbsent is printed wherever a field the founder is asked to judge has
// no value. An empty string reads as "nothing was needed here"; the founder has
// to be able to see the difference between that and "nothing was recorded".
const previewAbsent = "<none recorded>"

const (
	// DefaultPreviewSamplesPerClass keeps the review a representative sample.
	// The authorization stays cohort-bounded, so this is a judgement aid, not a
	// per-message approval queue.
	DefaultPreviewSamplesPerClass = 2
	// DefaultPreviewFullBodies is kept for CLI compatibility only. Every sampled
	// member now renders its whole body, so this bounds nothing.
	DefaultPreviewFullBodies = 2
)

// CohortPreviewOptions tunes the founder-facing render only. It cannot change
// what was admitted; the frozen snapshot is already decided by the time it runs.
type CohortPreviewOptions struct {
	SamplesPerClass int
	// FullBodies is accepted for CLI compatibility and controls nothing: every
	// sampled member's whole body is always rendered.
	FullBodies int
	// ShowMailbox reveals the real destination address. Default off, because the
	// same rendered text ends up in aggregated logs; a founder deciding whether
	// to authorize a send legitimately needs the real address and asks for it.
	ShowMailbox bool
}

func (o CohortPreviewOptions) normalized() CohortPreviewOptions {
	if o.SamplesPerClass <= 0 {
		o.SamplesPerClass = DefaultPreviewSamplesPerClass
	}
	if o.FullBodies < 0 {
		o.FullBodies = 0
	}
	return o
}

func orAbsent(s string) string {
	if strings.TrimSpace(s) == "" {
		return previewAbsent
	}
	return strings.TrimSpace(s)
}

// assertedBool renders a stored boolean the way a reviewer can trust. These
// columns default to false, and the direct-feed path has no field to set some of
// them at all, so a printed "false" would read as an upstream denial that never
// happened. "not_asserted" is true in both cases.
func assertedBool(b bool) string {
	if b {
		return "true"
	}
	return "not_asserted"
}

func absentDate(t *time.Time) string {
	if t == nil || t.IsZero() {
		return previewAbsent
	}
	return t.UTC().Format("2006-01-02")
}

// admissionEvidence answers "why was this company admitted", in the ladder's own
// terms. PrepareControlledCohort admits on controlled-route eligibility and copy
// QA alone; the target-fit, moment and priority values below are evidence the
// feed carried, not gates this path applied, so they are labelled feed_* and are
// never presented as the reason the account got in.
//
// copy_qa names the gate that actually ran. Cohort v1 asserted a bare
// "copy_qa=passed" next to copy carrying ",." and a repeated three-word run,
// so the label now records which gate cleared the member.
func admissionEvidence(acc *models.OutreachAccount, class string) []string {
	out := []string{
		"admitted_by=controlled_route_eligible",
		"route_class=" + orAbsent(class),
		"copy_qa=passed_editorial_gate",
		"composer=" + ComposerVersion,
	}
	if acc == nil {
		return append(out, "feed_target_fit="+previewAbsent, "feed_moment="+previewAbsent, "feed_priority="+previewAbsent)
	}
	tier := strings.TrimSpace(acc.TargetFitSendTier)
	fitClass := strings.TrimSpace(acc.TargetFitClass)
	if tier == "" && fitClass == "" && len(acc.TargetFitReasons) == 0 {
		out = append(out, "feed_target_fit="+previewAbsent)
	} else {
		out = append(out,
			fmt.Sprintf("feed_target_fit_tier=%s class=%s eligible=%s fresh=%s",
				orAbsent(tier), orAbsent(fitClass), assertedBool(acc.TargetFitEligible), assertedBool(acc.TargetFitFresh)),
			"feed_target_fit_reasons="+orAbsent(strings.Join(acc.TargetFitReasons, ",")),
		)
	}
	if strings.TrimSpace(acc.MomentCode) == "" && strings.TrimSpace(acc.MomentSummary) == "" {
		out = append(out, "feed_moment="+previewAbsent)
	} else {
		out = append(out, fmt.Sprintf("feed_moment_code=%s confidence=%s observed_at=%s",
			orAbsent(acc.MomentCode), orAbsent(acc.MomentConfidence), absentDate(acc.MomentObservedAt)))
	}
	if strings.TrimSpace(acc.PriorityTier) == "" && acc.PriorityRank == 0 {
		out = append(out, "feed_priority="+previewAbsent)
	} else {
		out = append(out, fmt.Sprintf("feed_priority_tier=%s rank=%d confidence=%s",
			orAbsent(acc.PriorityTier), acc.PriorityRank, orAbsent(acc.PriorityConfidence)))
	}
	return out
}

// selectInitialRouteRationale is SelectInitialRoute plus the record of why the
// winner won. "Why this mailbox and not the other one for the same company" is
// not answerable from the chosen route alone.
//
// Rejected mailboxes are redacted here and stay redacted: nothing is ever sent
// to them, so the preview has no reason to widen their exposure the way
// --show-mailbox legitimately does for the one destination under review.
func selectInitialRouteRationale(cands []models.OutreachContactCandidate, allowed map[string]bool, now time.Time) (*models.OutreachContactCandidate, string, []string) {
	if allowed == nil {
		allowed = defaultPilotRouteClasses
	}
	var eligible []models.OutreachContactCandidate
	var rejected []string
	lastReason := "no_controlled_eligible_route"
	for i := range cands {
		c := cands[i]
		if reason := controlledPrepareBlock(&c, allowed, now); reason != "" {
			lastReason = reason
			rejected = append(rejected, fmt.Sprintf("not_chosen=%s reason=%s", RedactMailbox(c.Email), reason))
			continue
		}
		eligible = append(eligible, c)
	}
	if len(eligible) == 0 {
		return nil, lastReason, rejected
	}
	var best *models.OutreachContactCandidate
	bestScore := -1
	for i := range eligible {
		c := &eligible[i]
		if score := routeSelectionScore(c); score > bestScore {
			bestScore = score
			best = c
		}
	}
	reasons := []string{
		"chosen_route_class=" + orAbsent(CandidateRouteClass(best)),
		fmt.Sprintf("chosen_selection_score=%d", bestScore),
		fmt.Sprintf("preferred_initial=%v", CandidatePreferredInitial(best)),
		fmt.Sprintf("proven_person_name=%v", provenPersonName(best)),
		fmt.Sprintf("eligible_routes=%d rejected_routes=%d", len(eligible), len(rejected)),
	}
	for i := range eligible {
		c := &eligible[i]
		if c == best {
			continue
		}
		score := routeSelectionScore(c)
		why := "lower_selection_score"
		if score == bestScore {
			// A tie is broken by input order, not by merit. Saying "lower score"
			// here would tell the founder a comparison happened that did not.
			why = "tied_selection_score_first_seen_wins"
		}
		reasons = append(reasons, fmt.Sprintf("not_chosen=%s route_class=%s score=%d reason=%s",
			RedactMailbox(c.Email), orAbsent(CandidateRouteClass(c)), score, why))
	}
	return best, "", append(reasons, rejected...)
}

// selectPreviewSamples spreads the sample across route classes so no class is
// judged by another class's copy. Members arrive sorted, so the pick is stable
// between the frozen snapshot and any later render of it.
func selectPreviewSamples(members []FrozenCohortMember, perClass int) map[string][]CohortClassSample {
	if perClass <= 0 {
		perClass = DefaultPreviewSamplesPerClass
	}
	out := map[string][]CohortClassSample{}
	for i := range members {
		m := members[i]
		if len(out[m.RouteClass]) >= perClass {
			continue
		}
		out[m.RouteClass] = append(out[m.RouteClass], sampleFromMember(&m))
	}
	return out
}

// sampleFromMember is the single projection from frozen member to reviewable
// excerpt. Keeping it in one place is what stops the sample and the member from
// disagreeing about what was composed.
func sampleFromMember(m *FrozenCohortMember) CohortClassSample {
	return CohortClassSample{
		AccountRef:       m.AccountRef,
		Company:          m.Company,
		RouteClass:       m.RouteClass,
		RedactedMailbox:  RedactMailbox(m.Mailbox),
		Mailbox:          m.Mailbox,
		MailboxPurpose:   m.MailboxPurpose,
		Greeting:         m.Greeting,
		PersonUnknown:    m.PersonUnknown,
		Subject:          m.Subject,
		ObservedFact:     m.ObservedFact,
		FactSource:       m.FactSource,
		CTA:              m.CTA,
		CTASource:        m.CTASource,
		BodyText:         m.BodyText,
		AdmissionReasons: append([]string(nil), m.AdmissionReasons...),
		RouteReasons:     append([]string(nil), m.RouteReasons...),
	}
}

func sampleMailbox(s CohortClassSample, show bool) string {
	if show {
		return orAbsent(s.Mailbox)
	}
	return orAbsent(s.RedactedMailbox)
}

func writeReasonList(b *strings.Builder, label string, reasons []string) {
	if len(reasons) == 0 {
		fmt.Fprintf(b, "      %s: %s\n", label, previewAbsent)
		return
	}
	fmt.Fprintf(b, "      %s:\n", label)
	for _, r := range reasons {
		fmt.Fprintf(b, "        - %s\n", r)
	}
}

// quoteOrAbsent keeps quoted copy quoted so trailing spaces and stray newlines
// are visible, while an absent value stays an unquoted marker: "" would read as
// deliberate empty copy rather than a missing field.
func quoteOrAbsent(s string) string {
	if strings.TrimSpace(s) == "" {
		return previewAbsent
	}
	return fmt.Sprintf("%q", s)
}
