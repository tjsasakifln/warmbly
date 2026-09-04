package confenge

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestEvaluateOutboundGOPacketAllGreenOffersHumanChoicesWithoutSending(t *testing.T) {
	now := time.Date(2026, 9, 3, 16, 0, 0, 0, time.UTC)
	snap := readyFirstWindowSnapshot(now)
	snap.KillSwitchEngaged = false
	snap.PauseSource = PauseSourceDurable
	snap.PauseState = TransportPaused
	beforeCap, beforeGap, beforeStart, beforeEnd := snap.GlobalSendsPerHour, snap.MinWaitSeconds, snap.BusinessWindowStart, snap.BusinessWindowEnd

	packet := EvaluateOutboundGOPacket(snap)
	if packet.State != OutboundGOPacketReady {
		t.Fatalf("all-green packet state=%s reason=%s blockers=%v", packet.State, packet.ExactReason, packet.Blockers)
	}
	if len(packet.HumanChoices) != 2 || packet.HumanChoices[0] != OutboundHumanGOForControlledPilot || packet.HumanChoices[1] != OutboundHumanNOGO {
		t.Fatalf("human choices=%v", packet.HumanChoices)
	}
	if packet.CurrentVerdict != FirstWindowCurrentVerdictNoGoSMTP {
		t.Fatalf("current_verdict=%q", packet.CurrentVerdict)
	}
	if packet.SMTPSent || packet.BindingHash == "" {
		t.Fatalf("smtp_sent=%v hash=%q", packet.SMTPSent, packet.BindingHash)
	}
	if packet.GlobalSendsPerHour != beforeCap || packet.MinWaitSeconds != beforeGap ||
		packet.BusinessWindowStart != beforeStart || packet.BusinessWindowEnd != beforeEnd {
		t.Fatal("evaluator retuned cap, cadence or window")
	}
	if EvaluateFirstWindowReadiness(snap).Verdict == FirstWindowGOForControlledPilot {
		t.Fatal("readiness evaluator inferred GO")
	}
	second := EvaluateOutboundGOPacket(snap)
	if second.BindingHash != packet.BindingHash {
		t.Fatal("binding hash is not deterministic")
	}
	if snap.GlobalSendsPerHour != beforeCap || snap.MinWaitSeconds != beforeGap {
		t.Fatal("evaluating the packet mutated the snapshot")
	}
	raw, err := json.MarshalIndent(packet, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("OUTBOUND_GO_PACKET %s", raw)
}

func TestEvaluateOutboundGOPacketFailClosedReasons(t *testing.T) {
	now := time.Date(2026, 9, 3, 16, 0, 0, 0, time.UTC)
	t.Run("kill_switch", func(t *testing.T) {
		snap := readyFirstWindowSnapshot(now)
		packet := EvaluateOutboundGOPacket(snap)
		if packet.State != OutboundGOPacketNoGo || packet.ExactReason != OutboundGOReasonKillSwitch {
			t.Fatalf("kill switch: %+v", packet)
		}
		if len(packet.HumanChoices) != 0 {
			t.Fatal("NO_GO packet offered a GO choice")
		}
	})
	t.Run("incomplete_sha", func(t *testing.T) {
		snap := readyFirstWindowSnapshot(now)
		snap.KillSwitchEngaged = false
		snap.PauseSource = PauseSourceDurable
		snap.WarmblyReleaseSHA = ""
		packet := EvaluateOutboundGOPacket(snap)
		if packet.State != OutboundGOPacketNoGo || packet.ExactReason != "warmbly_release_sha_missing" {
			t.Fatalf("missing sha: %+v", packet)
		}
	})
	t.Run("smtp_unknown", func(t *testing.T) {
		snap := readyFirstWindowSnapshot(now)
		snap.KillSwitchEngaged = false
		snap.PauseSource = PauseSourceDurable
		snap.SMTPReady = EvidenceUnknown
		packet := EvaluateOutboundGOPacket(snap)
		if packet.State != OutboundGOPacketNoGo {
			t.Fatalf("smtp unknown was not NO_GO: %+v", packet)
		}
		if packet.ExactReason != "smtp_not_ready" && packet.ExactReason != "smtp_unknown" {
			t.Fatalf("smtp unknown reason=%q", packet.ExactReason)
		}
	})
	t.Run("hash_bound", func(t *testing.T) {
		snap := readyFirstWindowSnapshot(now)
		snap.KillSwitchEngaged = false
		snap.PauseSource = PauseSourceDurable
		first := OutboundGOBindingHash(snap)
		snap.SourceRunID = "other-run"
		if OutboundGOBindingHash(snap) == first {
			t.Fatal("source-run change did not rebind the hash")
		}
	})
	t.Run("queue_bound", func(t *testing.T) {
		snap := readyFirstWindowSnapshot(now)
		snap.KillSwitchEngaged = false
		snap.PauseSource = PauseSourceDurable
		first := OutboundGOBindingHash(snap)
		snap.Queued = 12
		if OutboundGOBindingHash(snap) == first {
			t.Fatal("queue change did not rebind the hash")
		}
	})
}

func TestEvaluateOutboundGOPacketFromPausedProductionShape(t *testing.T) {
	now := time.Date(2026, 9, 4, 2, 34, 45, 0, time.UTC)
	snap := readyFirstWindowSnapshot(now)
	snap.WarmblyReleaseSHA = "cc11a9ab22d54e08ceb24efc9b555e0ac9c25b23"
	snap.SourceRunID = "run-fcb040592784ef71"
	snap.SourceSnapshotHash = "50286da97b4fadc4c8664892cbaaca2c53613ab75f2443d97e102973e57c4b4c"
	snap.MembershipHash = "4fcc74e79eaf58eb3447070984902ce27d757279a08aa84434c771f889bcfad0"
	snap.GlobalSendsPerHour = 6
	snap.MailboxRateCaps = []int{50}
	snap.MinWaitSeconds = 600
	snap.SuppressionCount = 14
	snap.Queued = 0
	snap.KillSwitchEngaged = true
	snap.PauseSource = PauseSourceKillSwitch
	beforeCap, beforeGap, beforeStart, beforeEnd := snap.GlobalSendsPerHour, snap.MinWaitSeconds, snap.BusinessWindowStart, snap.BusinessWindowEnd
	packet := EvaluateOutboundGOPacket(snap)
	if packet.CurrentVerdict != FirstWindowCurrentVerdictNoGoSMTP || packet.SMTPSent {
		t.Fatalf("live-shaped packet verdict=%s smtp=%v", packet.CurrentVerdict, packet.SMTPSent)
	}
	if packet.BindingHash == "" {
		t.Fatal("live-shaped packet missing binding hash")
	}
	if packet.GlobalSendsPerHour != beforeCap || packet.MinWaitSeconds != beforeGap ||
		packet.BusinessWindowStart != beforeStart || packet.BusinessWindowEnd != beforeEnd {
		t.Fatal("live-shaped evaluator retuned cap, cadence or window")
	}
	if packet.State != OutboundGOPacketNoGo || packet.ExactReason != OutboundGOReasonKillSwitch {
		t.Fatalf("kill-switch pack should stay NO_GO: %+v", packet)
	}
	raw, err := json.MarshalIndent(packet, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("OUTBOUND_GO_PACKET %s", raw)
}

func TestEvaluateOutboundGOPacketJSONHasNoPII(t *testing.T) {
	now := time.Date(2026, 9, 3, 16, 0, 0, 0, time.UTC)
	snap := readyFirstWindowSnapshot(now)
	snap.KillSwitchEngaged = false
	snap.PauseSource = PauseSourceDurable
	raw, err := json.Marshal(EvaluateOutboundGOPacket(snap))
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ToLower(string(raw))
	for _, needle := range []string{"@", "password", "secret", "cnpj"} {
		if strings.Contains(body, needle) && needle != "@" {
			t.Fatalf("packet leaked %q: %s", needle, body)
		}
	}
	if strings.Contains(body, "ana@") || strings.Contains(body, "gmail.com") {
		t.Fatalf("packet leaked an email: %s", body)
	}
}
