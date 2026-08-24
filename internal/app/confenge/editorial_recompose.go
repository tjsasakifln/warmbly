package confenge

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/warmbly/warmbly/internal/models"
)

// RECOMPOSE re-runs message planning, composition and QA over a frozen version
// using the current composer. It reads each member's own preserved fact, so
// recipient, account, route class, evidence, provenance and source are carried
// across untouched. Re-reading upstream would be a new CREATE, not a recompose.

// RecomposeReport is the founder-facing accounting of one recompose.
type RecomposeReport struct {
	ParentMembers   int               `json:"parent_members"`
	KeptMembers     int               `json:"kept_members"`
	ExcludedMembers int               `json:"excluded_members"`
	Exclusions      []CohortExclusion `json:"exclusions"`
	ByReasonCode    map[string]int    `json:"by_reason_code"`
	ComposerBefore  string            `json:"composer_before"`
	ComposerAfter   string            `json:"composer_after"`
}

var errRecomposeNilParent = errors.New("recompose requires a parent snapshot")

// RecomposeManifest returns a new snapshot whose copy was written by the
// current composer, plus the accounting of everything it refused. It never
// mutates the parent.
func RecomposeManifest(parent *FrozenCohortSnapshot) (*FrozenCohortSnapshot, RecomposeReport, error) {
	report := RecomposeReport{ByReasonCode: map[string]int{}}
	if parent == nil {
		return nil, report, errRecomposeNilParent
	}
	raw, err := json.Marshal(parent)
	if err != nil {
		return nil, report, err
	}
	next := &FrozenCohortSnapshot{}
	if err = json.Unmarshal(raw, next); err != nil {
		return nil, report, err
	}
	report.ParentMembers = len(parent.Members)
	report.ComposerBefore = strings.TrimSpace(parent.ComposerVersion)
	report.ComposerAfter = ComposerVersion

	kept := make([]FrozenCohortMember, 0, len(next.Members))
	qaCodes := make(map[string][]string, len(next.Members))
	for i := range next.Members {
		m := next.Members[i]
		composed, reasons := recomposeMember(&m)
		if len(reasons) > 0 {
			report.Exclusions = append(report.Exclusions, CohortExclusion{
				AccountRef: m.AccountRef,
				ReasonCode: primaryExclusionReason(reasons),
				Mailbox:    m.Mailbox,
				RouteClass: m.RouteClass,
			})
			for _, r := range reasons {
				report.ByReasonCode[r]++
			}
			continue
		}
		qaCodes[m.AccountRef] = nil
		kept = append(kept, composed)
	}

	// Corpus QA runs on the survivors: a message can be individually clean and
	// still be the same mail merge as its neighbour.
	if findings := CorpusQA(kept); len(findings) > 0 {
		filtered := kept[:0]
		for _, m := range kept {
			codes := findings[m.AccountRef]
			if len(codes) == 0 {
				filtered = append(filtered, m)
				continue
			}
			report.Exclusions = append(report.Exclusions, CohortExclusion{
				AccountRef: m.AccountRef,
				ReasonCode: primaryExclusionReason(codes),
				Mailbox:    m.Mailbox,
				RouteClass: m.RouteClass,
			})
			for _, r := range codes {
				report.ByReasonCode[r]++
			}
		}
		kept = filtered
	}

	next.Members = kept
	next.ComposerVersion = ComposerVersion
	report.KeptMembers = len(kept)
	report.ExcludedMembers = report.ParentMembers - report.KeptMembers
	sort.Slice(report.Exclusions, func(i, j int) bool {
		if report.Exclusions[i].ReasonCode != report.Exclusions[j].ReasonCode {
			return report.Exclusions[i].ReasonCode < report.Exclusions[j].ReasonCode
		}
		return report.Exclusions[i].AccountRef < report.Exclusions[j].AccountRef
	})
	return next, report, nil
}

// recomposeMember rewrites one member's copy from its own preserved facts.
func recomposeMember(m *FrozenCohortMember) (FrozenCohortMember, []string) {
	// The record, not the last composer's sentence: re-digesting our own prose
	// is what made a second recomposition throw the lead away.
	sourceFact := firstNonEmpty(m.SourceFact, m.ObservedFact)
	acc := &models.OutreachAccount{
		NomeFantasia:  m.Company,
		FactToMention: sourceFact,
		ServiceCode:   m.ServiceCode,
		MomentCode:    m.MomentCode,
	}
	cand := &models.OutreachContactCandidate{
		Email:          m.Mailbox,
		MailboxPurpose: m.MailboxPurpose,
	}
	composed, reasons := ComposeEditorialInitial(acc, cand, m.RouteClass)
	if len(reasons) > 0 {
		return *m, reasons
	}
	sender, err := ResolveSenderIdentity()
	if err != nil {
		return *m, []string{"sender_identity_unresolved"}
	}
	qa := EditorialQA(composed.Subject, composed.Body, EditorialQAContext{
		RouteClass:      m.RouteClass,
		RawFact:         sourceFact,
		SenderFirstName: sender.FirstName,
		PersonProven:    !m.PersonUnknown && composerRouteNamesPerson(m.RouteClass),
	})
	if len(qa) > 0 {
		return *m, qa
	}

	out := *m
	out.Subject = composed.Subject
	out.BodyText = composed.Body
	out.Greeting = composed.Greeting
	out.ObservedFact = composed.ObservedFact
	out.SourceFact = sourceFact
	out.FactSource = composed.FactSource
	out.CTA = composed.CTA
	out.CTASource = composed.CTASource
	out.ComposerVersion = ComposerVersion
	out.ContentHash = hashControlledContent(out.Mailbox, out.RouteClass, out.Subject, out.BodyText)
	out.AdmissionReasons = recomposedAdmissionReasons(out.RouteClass)
	return out, nil
}

func composerRouteNamesPerson(class string) bool {
	return strings.EqualFold(strings.TrimSpace(class), RouteClassDirectPerson)
}

// recomposedAdmissionReasons records that copy QA was computed, not asserted.
func recomposedAdmissionReasons(class string) []string {
	return []string{
		"admitted_by=controlled_route_eligible",
		"route_class=" + orAbsent(class),
		"copy_qa=passed_editorial_gate",
		"composer=" + ComposerVersion,
	}
}

// primaryExclusionReason picks the code the founder should read first.
func primaryExclusionReason(codes []string) string {
	if len(codes) == 0 {
		return "insufficient_human_quality"
	}
	for _, c := range codes {
		if c == "needs_enrichment" {
			return c
		}
	}
	return codes[0]
}
