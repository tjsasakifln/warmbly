package confenge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
)

func TestMergePauseProvenanceAPIFileAndResume(t *testing.T) {
	actor := uuid.New()
	pausedAt := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	api := dispatch.ControlState{
		Paused: true, PauseReason: "operator_hold", PausedBy: &actor, PausedAt: &pausedAt, PauseSource: PauseSourceAPI,
	}

	got := MergePauseProvenance(api, killSwitchFile{Present: true, Source: PauseSourceAPI, Reason: "kill_switch_file:operator_hold", PausedBy: &actor, PausedAt: &pausedAt}, false, "", false)
	if !got.Paused || got.PauseSource != PauseSourceAPI || got.PausedBy == nil || *got.PausedBy != actor {
		t.Fatalf("api: %+v", got)
	}

	fileOnly := MergePauseProvenance(dispatch.ControlState{}, killSwitchFile{Present: true, Source: PauseSourceKillSwitch, Reason: "kill_switch_file:deploy_preflight"}, false, "", false)
	if !fileOnly.Paused || fileOnly.PauseSource != PauseSourceKillSwitch || fileOnly.PausedBy != nil {
		t.Fatalf("file-only attributed to Control Center: %+v", fileOnly)
	}

	both := MergePauseProvenance(api, killSwitchFile{Present: true, Source: PauseSourceKillSwitch, Reason: "kill_switch_file:deploy_preflight"}, false, "", false)
	if !both.Paused || both.PauseSource != PauseSourceKillSwitch {
		t.Fatalf("most restrictive lost: %+v", both)
	}
	if both.PausedBy == nil || *both.PausedBy != actor {
		t.Fatalf("api actor dropped while still active: %+v", both)
	}

	unreadable := MergePauseProvenance(dispatch.ControlState{}, killSwitchFile{Unreadable: true, Source: PauseSourceKillSwitch, Reason: "kill_switch_unreadable"}, false, "", false)
	if !unreadable.Paused || unreadable.PauseSource != PauseSourceKillSwitch {
		t.Fatalf("unreadable failed open: %+v", unreadable)
	}
}

func TestPauseDispatchProvenanceAndResumeCannotBypassFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kill")
	t.Setenv(EnvKillSwitchPath, path)
	if err := os.WriteFile(path, []byte("paused\nreason=deploy_preflight\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := dispatch.NewMemoryStore()
	svc := &service{
		cfg:      Config{Enabled: true},
		governor: dispatch.NewGovernor(dispatch.DefaultConfig(), store, nil),
	}
	org, user := uuid.New(), uuid.New()
	if xerr := svc.PauseDispatch(context.Background(), org, user, "operator_hold"); xerr != nil {
		t.Fatal(xerr)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "deploy_preflight") {
		t.Fatalf("API pause overwrote independent file: %s", body)
	}
	st, xerr := svc.DispatchStatus(context.Background(), org)
	if xerr != nil || !st.Paused {
		t.Fatalf("status=%+v err=%v", st, xerr)
	}
	if st.PauseSource != PauseSourceKillSwitch {
		t.Fatalf("pause_source=%s", st.PauseSource)
	}
	if xerr := svc.ResumeDispatch(context.Background(), org, user); xerr != nil {
		t.Fatal(xerr)
	}
	if !FileKillSwitchActive() {
		t.Fatal("resume bypassed remaining file kill switch")
	}
	st, xerr = svc.DispatchStatus(context.Background(), org)
	if xerr != nil || !st.Paused || st.PauseSource != PauseSourceKillSwitch || st.PausedBy != nil {
		t.Fatalf("file-only after resume: %+v err=%v", st, xerr)
	}
}

func TestPauseDispatchRecordsAPIActorWhenFileAbsent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvKillSwitchPath, filepath.Join(dir, "kill"))
	store := dispatch.NewMemoryStore()
	svc := &service{
		cfg:      Config{Enabled: true},
		governor: dispatch.NewGovernor(dispatch.DefaultConfig(), store, nil),
	}
	org, user := uuid.New(), uuid.New()
	if xerr := svc.PauseDispatch(context.Background(), org, user, "operator_hold"); xerr != nil {
		t.Fatal(xerr)
	}
	st, xerr := svc.DispatchStatus(context.Background(), org)
	if xerr != nil || !st.Paused || st.PauseSource != PauseSourceAPI || st.PausedBy == nil || *st.PausedBy != user {
		t.Fatalf("%+v err=%v", st, xerr)
	}
	if xerr := svc.ResumeDispatch(context.Background(), org, user); xerr != nil {
		t.Fatal(xerr)
	}
	if FileKillSwitchActive() {
		t.Fatal("API-sourced file survived API resume")
	}
}
