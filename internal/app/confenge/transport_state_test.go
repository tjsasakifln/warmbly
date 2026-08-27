package confenge

import (
	"os"
	"path/filepath"
	"testing"
)

// The controls used to live in two projections that could disagree:
// Config.SendingAllowed saw the env flag and the kill-switch file, the dispatch
// governor saw the env flag, the durable control row and the business window.
// These tests pin the one rule that now governs both.

func withKillSwitch(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "kill-switch")
	if body != "" {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write kill switch: %v", err)
		}
	}
	t.Setenv(EnvKillSwitchPath, path)
}

func sourceState(state TransportState, name string) TransportSourceState {
	for _, s := range state.Sources {
		if s.Source == name {
			return s
		}
	}
	return TransportSourceState{Source: name, State: "MISSING"}
}

func TestAllSourcesActiveIsTheOnlyWayToBeActive(t *testing.T) {
	withKillSwitch(t, "")
	got := resolveTransportState([]TransportSourceState{
		{Source: TransportSourceEnvironment, State: TransportActive},
		{Source: TransportSourceFileKillSwitch, State: TransportActive},
		{Source: TransportSourceDurableControl, State: TransportActive},
		{Source: TransportSourceSendWindow, State: TransportActive},
	})
	if got.State != TransportActive || !got.Active {
		t.Fatalf("expected ACTIVE, got %+v", got)
	}
	if len(got.Blockers) != 0 {
		t.Fatalf("expected no blockers, got %v", got.Blockers)
	}
}

func TestAnyPausedSourceMakesTheWholeStatePaused(t *testing.T) {
	for _, name := range []string{
		TransportSourceEnvironment,
		TransportSourceFileKillSwitch,
		TransportSourceDurableControl,
		TransportSourceSendWindow,
	} {
		sources := []TransportSourceState{
			{Source: TransportSourceEnvironment, State: TransportActive},
			{Source: TransportSourceFileKillSwitch, State: TransportActive},
			{Source: TransportSourceDurableControl, State: TransportActive},
			{Source: TransportSourceSendWindow, State: TransportActive},
		}
		for i := range sources {
			if sources[i].Source == name {
				sources[i].State = TransportPaused
				sources[i].Reason = "test"
			}
		}
		got := resolveTransportState(sources)
		if got.State != TransportPaused || got.Active {
			t.Fatalf("%s paused but resolved %+v", name, got)
		}
		if len(got.Blockers) != 1 || got.Blockers[0] != name+":test" {
			t.Fatalf("%s: expected a named blocker, got %v", name, got.Blockers)
		}
	}
}

func TestUnknownSourceIsNeverActive(t *testing.T) {
	got := resolveTransportState([]TransportSourceState{
		{Source: TransportSourceEnvironment, State: TransportActive},
		{Source: TransportSourceDurableControl, State: TransportUnknown, Reason: "durable_control_unreadable"},
	})
	if got.State != TransportUnknown || got.Active {
		t.Fatalf("UNKNOWN must not be ACTIVE, got %+v", got)
	}
}

func TestPausedOutranksUnknown(t *testing.T) {
	got := resolveTransportState([]TransportSourceState{
		{Source: TransportSourceEnvironment, State: TransportPaused, Reason: "env_paused"},
		{Source: TransportSourceDurableControl, State: TransportUnknown},
	})
	if got.State != TransportPaused {
		t.Fatalf("expected PAUSED to win, got %+v", got)
	}
}

// The live drift that motivated this: the kill-switch file said "paused" while
// CONFENGE_SENDING_PAUSED was false, and the status endpoint reported the env
// flag alone.
func TestFileKillSwitchOverridesAPermissiveEnvironment(t *testing.T) {
	withKillSwitch(t, "paused\nreason=deploy_preflight\n")
	cfg := Config{SendingPaused: false}
	got := cfg.TransportState()
	if got.Active || got.State != TransportPaused {
		t.Fatalf("file kill switch must pause transport, got %+v", got)
	}
	if src := sourceState(got, TransportSourceFileKillSwitch); src.Reason != "kill_switch_file:paused" {
		t.Fatalf("the reason must name the file's own words, got %q", src.Reason)
	}
	if cfg.SendingAllowed() {
		t.Fatal("SendingAllowed and TransportState must agree")
	}
}

func TestAbsentKillSwitchWithPermissiveEnvironmentIsActive(t *testing.T) {
	withKillSwitch(t, "")
	cfg := Config{SendingPaused: false}
	got := cfg.TransportState()
	if !got.Active {
		t.Fatalf("expected ACTIVE, got %+v", got)
	}
	if got.Active != cfg.SendingAllowed() {
		t.Fatal("SendingAllowed and TransportState must agree")
	}
}

func TestEnvironmentPauseIsReportedWithItsReason(t *testing.T) {
	withKillSwitch(t, "")
	got := Config{SendingPaused: true}.TransportState()
	if got.Active {
		t.Fatalf("expected paused, got %+v", got)
	}
	if src := sourceState(got, TransportSourceEnvironment); src.Reason != "env_paused" {
		t.Fatalf("expected env_paused, got %q", src.Reason)
	}
}

func TestUnreadableKillSwitchIsUnknownNotPermissive(t *testing.T) {
	dir := t.TempDir()
	// A path whose parent is a regular file makes Stat fail with something
	// other than NotExist, which must never read as "no kill switch".
	blocker := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Setenv(EnvKillSwitchPath, filepath.Join(blocker, "kill-switch"))
	got := Config{SendingPaused: false}.TransportState()
	if got.Active {
		t.Fatalf("an unreadable control must never be ACTIVE, got %+v", got)
	}
	if src := sourceState(got, TransportSourceFileKillSwitch); src.State != TransportUnknown {
		t.Fatalf("expected UNKNOWN, got %+v", src)
	}
	if FileKillSwitchActive() != true {
		t.Fatal("the legacy fail-closed helper must still refuse transport")
	}
}

func TestConfigOnlyResolutionNeverClaimsToKnowTheDurableControl(t *testing.T) {
	withKillSwitch(t, "")
	got := Config{}.TransportState()
	for _, s := range got.Sources {
		if s.Source == TransportSourceDurableControl || s.Source == TransportSourceSendWindow {
			t.Fatalf("config-only resolution must not invent %s", s.Source)
		}
	}
}
