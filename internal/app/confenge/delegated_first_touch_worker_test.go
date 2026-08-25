package confenge

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
)

type delegatedFirstTouchProcessorStub struct {
	remaining int
	calls     atomic.Int32
}

func (s *delegatedFirstTouchProcessorStub) ProcessDelegatedFirstTouchOnce(context.Context) (bool, error) {
	s.calls.Add(1)
	if s.remaining == 0 {
		return false, nil
	}
	s.remaining--
	return true, errors.New("held item must not stop the remaining burst")
}

func TestDelegatedFirstTouchWorkerDrainsHoldsUntilQueuedOrIdle(t *testing.T) {
	processor := &delegatedFirstTouchProcessorStub{remaining: 3}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		NewDelegatedFirstTouchWorker(processor, time.Hour).Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for processor.calls.Load() < 4 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	if processor.calls.Load() != 4 {
		t.Fatalf("worker calls=%d want 4", processor.calls.Load())
	}
}

func TestComposeDelegatedRoutingCopyAvoidsRecentBody(t *testing.T) {
	acc := &models.OutreachAccount{ID: uuid.MustParse("21982eb9-878a-4c67-8a80-25ff6ca4d1f7"), NomeFantasia: "Empresa Alfa"}
	subject, first := composeDelegatedRoutingCopy(acc, nil)
	if subject == "" || first == "" || !strings.Contains(first, "Empresa Alfa") {
		t.Fatalf("incomplete first copy: subject=%q body=%q", subject, first)
	}
	_, second := composeDelegatedRoutingCopy(acc, []string{first})
	if second == "" || second == first {
		t.Fatalf("composer did not avoid recent body")
	}
	if _, duplicate := NearDuplicate(second, []string{first}); duplicate {
		t.Fatal("composer returned a near-duplicate body")
	}
}

func TestDelegatedFirstTouchAutorunRequiresNarrowGateAndOperatorBinding(t *testing.T) {
	cfg := Config{Enabled: true, RequireHumanApproval: true, DelegatedFirstTouchAutorunEnabled: true,
		DefaultDailyLimit: 50, MaxInitialEmailWords: 120}
	if err := cfg.ValidateStartup("test"); err == nil || !strings.Contains(err.Error(), EnvDelegatedFirstTouch) {
		t.Fatalf("autorun without delegated gate: %v", err)
	}
	cfg.DelegatedFirstTouchEnabled = true
	if err := cfg.ValidateStartup("test"); err == nil || !strings.Contains(err.Error(), "operator") {
		t.Fatalf("autorun without operator binding: %v", err)
	}
	cfg.OperatorUserID, cfg.OperatorOrgID = uuid.New(), uuid.New()
	if err := cfg.ValidateStartup("test"); err != nil {
		t.Fatalf("valid delegated autorun config: %v", err)
	}
}
