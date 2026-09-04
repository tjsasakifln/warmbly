package confenge

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
)

// Hash-bound outbound GO/NO-GO packet for #43.
//
// EvaluateFirstWindowReadiness never infers GO_FOR_CONTROLLED_EMAIL_PILOT.
// This packet is the human-decision artifact: when every gate is green it
// offers exactly two choices and mutates nothing. Incomplete, UNKNOWN,
// kill-switch or suppression evidence is NO_GO with the exact reason.

const (
	OutboundGOPacketReady             = "READY"
	OutboundGOPacketNoGo              = "NO_GO"
	OutboundHumanGOForControlledPilot = ReleaseGOForControlledEmailPilot
	OutboundHumanNOGO                 = ReleaseNOGO
	OutboundGOReasonKillSwitch        = "kill_switch_engaged"
	OutboundGOReasonIncomplete        = "evidence_incomplete"
)

// OutboundGOPacket is the short, hash-bound artifact a human uses to choose
// GO_FOR_CONTROLLED_EMAIL_PILOT vs NO_GO. Evaluating it never sends mail.
type OutboundGOPacket struct {
	State          string   `json:"state"`
	ExactReason    string   `json:"exact_reason,omitempty"`
	Blockers       []string `json:"blockers,omitempty"`
	HumanChoices   []string `json:"human_choices,omitempty"`
	BindingHash    string   `json:"binding_hash"`
	CurrentVerdict string   `json:"current_verdict"`
	SMTPSent       bool     `json:"smtp_sent"`
	// Frozen live values. The evaluator copies them; it never retunes them.
	WarmblyReleaseSHA   string   `json:"warmbly_release_sha"`
	PolicyID            string   `json:"policy_id"`
	SourceRunID         string   `json:"source_run_id"`
	SourceSnapshotHash  string   `json:"source_snapshot_hash"`
	MailboxSet          []string `json:"mailbox_set"`
	GlobalSendsPerHour  int      `json:"global_sends_per_hour"`
	MinWaitSeconds      int      `json:"min_wait_seconds"`
	BusinessWindowStart string   `json:"business_window_start"`
	BusinessWindowEnd   string   `json:"business_window_end"`
	SuppressionCount    int      `json:"suppression_count"`
	KillSwitchEngaged   bool     `json:"kill_switch_engaged"`
	Queued              int      `json:"queued"`
}

// OutboundGOBindingHash covers SHA / policy / source-run / mailbox / rate /
// window / suppression / kill-switch / queue. Same snapshot, same hash.
func OutboundGOBindingHash(snap FirstWindowReadinessSnapshot) string {
	mailboxes := append([]string{}, snap.MailboxSet...)
	sort.Strings(mailboxes)
	caps := make([]string, 0, len(snap.MailboxRateCaps))
	for _, cap := range snap.MailboxRateCaps {
		caps = append(caps, strconv.Itoa(cap))
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		"outbound_go_packet/1.0",
		strings.TrimSpace(snap.WarmblyReleaseSHA),
		firstNonEmpty(snap.PolicyID, snap.PolicyVersion),
		strings.TrimSpace(snap.SourceRunID),
		strings.TrimSpace(snap.SourceSnapshotHash),
		strings.Join(mailboxes, ","),
		strconv.Itoa(snap.GlobalSendsPerHour),
		strings.Join(caps, ","),
		strconv.Itoa(snap.MinWaitSeconds),
		strings.TrimSpace(snap.BusinessTimezone),
		strings.TrimSpace(snap.BusinessWindowStart),
		strings.TrimSpace(snap.BusinessWindowEnd),
		strconv.Itoa(snap.SuppressionCount),
		strconv.FormatBool(snap.KillSwitchEngaged),
		strconv.Itoa(snap.Queued),
	}, "\n")))
	return hex.EncodeToString(sum[:])
}

// EvaluateOutboundGOPacket is deterministic for a given snapshot. It never
// mutates provider state, cap, cadence or window.
func EvaluateOutboundGOPacket(snap FirstWindowReadinessSnapshot) OutboundGOPacket {
	readiness := EvaluateFirstWindowReadiness(snap)
	packet := OutboundGOPacket{
		BindingHash:         OutboundGOBindingHash(snap),
		CurrentVerdict:      FirstWindowCurrentVerdictNoGoSMTP,
		SMTPSent:            snap.ProviderMutationCount > 0,
		WarmblyReleaseSHA:   snap.WarmblyReleaseSHA,
		PolicyID:            firstNonEmpty(snap.PolicyID, snap.PolicyVersion),
		SourceRunID:         snap.SourceRunID,
		SourceSnapshotHash:  snap.SourceSnapshotHash,
		MailboxSet:          append([]string{}, snap.MailboxSet...),
		GlobalSendsPerHour:  snap.GlobalSendsPerHour,
		MinWaitSeconds:      snap.MinWaitSeconds,
		BusinessWindowStart: snap.BusinessWindowStart,
		BusinessWindowEnd:   snap.BusinessWindowEnd,
		SuppressionCount:    snap.SuppressionCount,
		KillSwitchEngaged:   snap.KillSwitchEngaged,
		Queued:              snap.Queued,
	}
	var blockers []string
	for _, b := range readiness.Blockers {
		blockers = appendUnique(blockers, b)
	}
	if snap.KillSwitchEngaged {
		blockers = appendUnique(blockers, OutboundGOReasonKillSwitch)
	}
	if snap.SMTPReady == EvidenceUnknown {
		blockers = appendUnique(blockers, "smtp_unknown")
	}
	if snap.IMAPReplyIngestReady == EvidenceUnknown {
		blockers = appendUnique(blockers, "imap_reply_ingest_unknown")
	}
	if snap.OutcomeObservabilityReady == EvidenceUnknown {
		blockers = appendUnique(blockers, OutboundGOReasonIncomplete)
	}
	if snap.ProviderMutationCount > 0 {
		blockers = appendUnique(blockers, "provider_mutation_nonzero")
	}
	sort.Strings(blockers)
	packet.Blockers = blockers
	if snap.KillSwitchEngaged {
		packet.State = OutboundGOPacketNoGo
		packet.ExactReason = OutboundGOReasonKillSwitch
		return packet
	}
	if len(blockers) > 0 {
		packet.State = OutboundGOPacketNoGo
		packet.ExactReason = blockers[0]
		return packet
	}
	packet.State = OutboundGOPacketReady
	packet.HumanChoices = []string{OutboundHumanGOForControlledPilot, OutboundHumanNOGO}
	return packet
}
