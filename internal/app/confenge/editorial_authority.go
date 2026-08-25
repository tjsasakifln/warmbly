package confenge

import "strings"

// Editorial authority answers the one question a version number cannot: was
// this frozen text written by the composer this build ships? The moment the
// answer is no the text is history, and history is readable but never
// operational. This is the only place that decision is made; every approve,
// authorize, queue and dispatch path asks here instead of re-deriving it.

const (
	// EditorialStateCurrent means the current composer and policy produced this
	// text, so it may still be reviewed, approved, authorized and queued.
	EditorialStateCurrent = "CURRENT"
	// EditorialStateSuperseded means an earlier composer or policy wrote it. It
	// stays readable for audit and is refused by every operational path.
	EditorialStateSuperseded = "LEGACY_SUPERSEDED"
)

// Reason codes stamped on superseded copy and returned by every refusal.
const (
	ReasonComposerSuperseded = "composer_superseded"
	ReasonComposerUnstamped  = "composer_unstamped"
	ReasonPolicySuperseded   = "policy_superseded"
	// A member whose stamp is older than the snapshot header's.
	ReasonMemberComposerSuperseded = "member_composer_superseded"
)

// EditorialLegacyNotice is the line a reader must see before the text itself.
const EditorialLegacyNotice = "Versão histórica, mantida apenas para auditoria. Não é enviável e não aceita aprovação."

// EditorialAuthority is the operational verdict on one frozen artifact.
type EditorialAuthority struct {
	State                  string   `json:"editorial_state"`
	Actionable             bool     `json:"actionable"`
	ReasonCodes            []string `json:"reason_codes"`
	ComposerVersion        string   `json:"composer_version"`
	CurrentComposerVersion string   `json:"current_composer_version"`
	PolicyVersion          string   `json:"policy_version,omitempty"`
	CurrentPolicyVersion   string   `json:"current_policy_version,omitempty"`
	Notice                 string   `json:"notice,omitempty"`
}

// ComposerSuperseded fails closed: an unstamped artifact is superseded, because
// nothing in it proves the current composer wrote the text.
func ComposerSuperseded(stamped string) bool {
	return strings.TrimSpace(stamped) != ComposerVersion
}

// EvaluateCohortEditorialAuthority judges a frozen cohort artifact, which
// stamps both the composer and the bounded-cohort policy.
func EvaluateCohortEditorialAuthority(composerVersion, policyVersion string) EditorialAuthority {
	a := newEditorialAuthority(composerVersion)
	a.PolicyVersion = strings.TrimSpace(policyVersion)
	a.CurrentPolicyVersion = BoundedCohortPolicyV1
	if a.PolicyVersion != BoundedCohortPolicyV1 {
		a.ReasonCodes = append(a.ReasonCodes, ReasonPolicySuperseded)
	}
	return sealEditorialAuthority(a)
}

// EvaluateDraftEditorialAuthority judges a draft or touchpoint, where the
// composer that wrote the text is identified by the stored prompt version.
// The cadence policy stamped on a touchpoint is a schedule, not a composer, so
// it is deliberately not read here.
func EvaluateDraftEditorialAuthority(promptVersion string) EditorialAuthority {
	a := EditorialAuthority{
		ReasonCodes:            []string{},
		ComposerVersion:        strings.TrimSpace(promptVersion),
		CurrentComposerVersion: PromptVersion,
	}
	switch {
	case a.ComposerVersion == "":
		a.ReasonCodes = append(a.ReasonCodes, ReasonComposerUnstamped)
	case !CurrentComposerPrompt(a.ComposerVersion):
		a.ReasonCodes = append(a.ReasonCodes, ReasonComposerSuperseded)
	}
	return sealEditorialAuthority(a)
}

func newEditorialAuthority(composerVersion string) EditorialAuthority {
	a := EditorialAuthority{
		ReasonCodes:            []string{},
		ComposerVersion:        strings.TrimSpace(composerVersion),
		CurrentComposerVersion: ComposerVersion,
	}
	switch {
	case a.ComposerVersion == "":
		a.ReasonCodes = append(a.ReasonCodes, ReasonComposerUnstamped)
	case a.ComposerVersion != ComposerVersion:
		a.ReasonCodes = append(a.ReasonCodes, ReasonComposerSuperseded)
	}
	return a
}

func sealEditorialAuthority(a EditorialAuthority) EditorialAuthority {
	if len(a.ReasonCodes) == 0 {
		a.State = EditorialStateCurrent
		a.Actionable = true
		return a
	}
	a.State = EditorialStateSuperseded
	a.Actionable = false
	a.Notice = EditorialLegacyNotice
	return a
}

// Blocker names the refusal for an operational action, or "" when the artifact
// is still current. The action verb is carried so the audit trail records what
// was attempted, not only that something was refused.
func (a EditorialAuthority) Blocker(action string) string {
	if a.Actionable {
		return ""
	}
	return action + " refused on " + EditorialStateSuperseded + " copy (" +
		strings.Join(a.ReasonCodes, ",") + "): composer " + editorialStampOrNone(a.ComposerVersion) +
		" wrote this text, current is " + a.CurrentComposerVersion +
		". Recompose the cohort and decide on the current version."
}

func editorialStampOrNone(v string) string {
	if strings.TrimSpace(v) == "" {
		return "(unstamped)"
	}
	return v
}

// SnapshotEditorialAuthority judges a frozen snapshot as a whole. A snapshot is
// current only when the snapshot stamp and every member stamp are current, so a
// member left behind by a partial recompose cannot ride a current header.
func SnapshotEditorialAuthority(snap *FrozenCohortSnapshot) EditorialAuthority {
	if snap == nil {
		return sealEditorialAuthority(newEditorialAuthority(""))
	}
	a := EvaluateCohortEditorialAuthority(snap.ComposerVersion, snap.PolicyVersion)
	if !a.Actionable {
		return a
	}
	for i := range snap.Members {
		if ComposerSuperseded(snap.Members[i].ComposerVersion) {
			a.ReasonCodes = append(a.ReasonCodes, ReasonMemberComposerSuperseded)
			a.ComposerVersion = snap.Members[i].ComposerVersion
			return sealEditorialAuthority(a)
		}
	}
	return a
}
