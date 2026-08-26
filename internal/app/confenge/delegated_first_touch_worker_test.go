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
	calls atomic.Int32
}

type delegatedFirstTouchAlwaysProcesses struct{ calls atomic.Int32 }

func (s *delegatedFirstTouchAlwaysProcesses) ProcessDelegatedFirstTouchOnce(context.Context) (bool, error) {
	s.calls.Add(1)
	return true, nil
}

func (s *delegatedFirstTouchProcessorStub) ProcessDelegatedFirstTouchOnce(context.Context) (bool, error) {
	s.calls.Add(1)
	return false, errors.New("capacity or policy blocker")
}

func TestDelegatedFirstTouchWorkerSleepsWhenProcessorReportsBlocker(t *testing.T) {
	processor := &delegatedFirstTouchProcessorStub{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		NewDelegatedFirstTouchWorker(processor, time.Hour).Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for processor.calls.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(10 * time.Millisecond)
	cancel()
	<-done
	if processor.calls.Load() != 1 {
		t.Fatalf("worker calls=%d want one attempt before sleep", processor.calls.Load())
	}
}

func TestDelegatedFirstTouchWorkerSleepsAfterBoundedBurst(t *testing.T) {
	processor := &delegatedFirstTouchAlwaysProcesses{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		NewDelegatedFirstTouchWorker(processor, time.Hour).Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for processor.calls.Load() < delegatedFirstTouchMaxBurst && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(10 * time.Millisecond)
	cancel()
	<-done
	if processor.calls.Load() != delegatedFirstTouchMaxBurst {
		t.Fatalf("worker calls=%d want bounded burst=%d", processor.calls.Load(), delegatedFirstTouchMaxBurst)
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
	if err := cfg.ValidateStartup("test"); err == nil || !strings.Contains(err.Error(), EnvDelegatedFirstTouchRunwayDays) {
		t.Fatalf("autorun without capacity runway: %v", err)
	}
	cfg.DelegatedFirstTouchRunwayDays = 30
	if err := cfg.ValidateStartup("test"); err != nil {
		t.Fatalf("valid delegated autorun config: %v", err)
	}
}

func TestDelegatedEntryUsesSourceObservationDateNotImportTimestamp(t *testing.T) {
	sourceDate := time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)
	importedAt := time.Date(2026, 8, 25, 20, 52, 0, 0, time.UTC)
	acc := &models.OutreachAccount{
		ID: uuid.New(), CNPJ14: "12345678000190", SourceRunID: "run-current",
	}
	cand := &models.OutreachContactCandidate{
		ID: uuid.New(), AccountID: acc.ID, SourceURL: "https://empresa.example/contato",
		SourceDate: &sourceDate, UpdatedAt: importedAt,
	}

	entry := delegatedEntryFromCurrentState(acc, cand, uuid.New(), "Assunto", "Corpo")
	if got := entry.WebSources[0].ObservedAt; !got.Equal(sourceDate) {
		t.Fatalf("web observation=%s want source date %s; import timestamp must not refresh evidence", got, sourceDate)
	}

	cand.SourceDate = nil
	entry = delegatedEntryFromCurrentState(acc, cand, uuid.New(), "Assunto", "Corpo")
	if got := entry.WebSources[0].ObservedAt; !got.IsZero() {
		t.Fatalf("missing source date must fail closed, got observation %s", got)
	}
}
