package worker

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/warmbly/warmbly/internal/app/confenge"
	"github.com/warmbly/warmbly/internal/models"
)

func TestHandleSendEmailConfengeKillSwitchBlocksBeforeMailboxTransport(t *testing.T) {
	t.Setenv(confenge.EnvEnabled, "true")
	t.Setenv(confenge.EnvOperatorMode, "true")
	t.Setenv(confenge.EnvKillSwitchPath, filepath.Join(t.TempDir(), "engaged"))
	if err := confenge.EngageKillSwitch(); err != nil {
		t.Fatal(err)
	}
	err := (&WorkerService{}).HandleSendEmail(context.Background(), models.SendEmail{})
	if err == nil || !strings.Contains(err.Error(), "sending paused") {
		t.Fatalf("worker transport must fail closed while paused: %v", err)
	}
}

func TestHandleSendEmailConfengeKillSwitchBlocksWithoutOperatorMode(t *testing.T) {
	t.Setenv(confenge.EnvEnabled, "true")
	t.Setenv(confenge.EnvOperatorMode, "false")
	t.Setenv(confenge.EnvRequireHuman, "true")
	t.Setenv(confenge.EnvAutoSend, "false")
	t.Setenv(confenge.EnvGreenAutorun, "false")
	t.Setenv(confenge.EnvKillSwitchPath, filepath.Join(t.TempDir(), "engaged"))
	if err := confenge.EngageKillSwitch(); err != nil {
		t.Fatal(err)
	}
	err := (&WorkerService{}).HandleSendEmail(context.Background(), models.SendEmail{})
	if err == nil || !strings.Contains(err.Error(), "sending paused") {
		t.Fatalf("worker without operator mode must still honor kill switch: %v", err)
	}
}

func TestHandleSendEmailConfengeUnsafeAutomationBlocksBeforeMailboxTransport(t *testing.T) {
	t.Setenv(confenge.EnvEnabled, "true")
	t.Setenv(confenge.EnvOperatorMode, "true")
	t.Setenv(confenge.EnvRequireHuman, "true")
	t.Setenv(confenge.EnvAutoSend, "true")
	t.Setenv(confenge.EnvKillSwitchPath, filepath.Join(t.TempDir(), "not-engaged"))
	err := (&WorkerService{}).HandleSendEmail(context.Background(), models.SendEmail{})
	if err == nil || !strings.Contains(err.Error(), "unsafe authorization") {
		t.Fatalf("worker transport must reject unsafe operator automation: %v", err)
	}
}

func TestHandleSendEmailConfengeKillSwitchE2ETwice(t *testing.T) {
	t.Setenv(confenge.EnvEnabled, "true")
	t.Setenv(confenge.EnvOperatorMode, "false")
	t.Setenv(confenge.EnvRequireHuman, "true")
	t.Setenv(confenge.EnvAutoSend, "false")
	t.Setenv(confenge.EnvGreenAutorun, "false")
	t.Setenv(confenge.EnvKillSwitchPath, filepath.Join(t.TempDir(), "e2e"))
	if err := confenge.EngageKillSwitch(); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		err := (&WorkerService{}).HandleSendEmail(context.Background(), models.SendEmail{})
		if err == nil || !strings.Contains(err.Error(), "sending paused") {
			t.Fatalf("run %d: worker must stay paused: %v", i, err)
		}
	}
}

func TestHandleSendEmailConfengeUnsafeAutomationBlocksWithoutOperatorMode(t *testing.T) {
	t.Setenv(confenge.EnvEnabled, "true")
	t.Setenv(confenge.EnvOperatorMode, "")
	t.Setenv(confenge.EnvRequireHuman, "true")
	t.Setenv(confenge.EnvAutoSend, "false")
	t.Setenv(confenge.EnvGreenAutorun, "true")
	t.Setenv(confenge.EnvKillSwitchPath, filepath.Join(t.TempDir(), "not-engaged"))
	err := (&WorkerService{}).HandleSendEmail(context.Background(), models.SendEmail{})
	if err == nil || !strings.Contains(err.Error(), "unsafe authorization") {
		t.Fatalf("isolated env must not reactivate green autorun at worker: %v", err)
	}
}
