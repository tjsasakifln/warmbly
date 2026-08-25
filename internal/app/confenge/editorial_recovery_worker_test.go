package confenge

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type editorialRecoveryProcessorStub struct {
	remaining int
	calls     atomic.Int32
}

func (s *editorialRecoveryProcessorStub) ProcessEditorialRecoveryOnce(context.Context) (bool, error) {
	s.calls.Add(1)
	if s.remaining == 0 {
		return false, nil
	}
	s.remaining--
	return true, errors.New("deferred touchpoint must not stop the remaining burst")
}

func TestEditorialRecoveryWorkerDrainsBurstUntilIdle(t *testing.T) {
	processor := &editorialRecoveryProcessorStub{remaining: 3}
	ctx, cancel := context.WithCancel(context.Background())
	worker := NewEditorialRecoveryWorker(processor, time.Hour)
	done := make(chan struct{})
	go func() {
		worker.Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for processor.calls.Load() < 4 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	if processor.calls.Load() != 4 {
		t.Fatalf("worker calls=%d want 4 (three processed plus idle)", processor.calls.Load())
	}
}
