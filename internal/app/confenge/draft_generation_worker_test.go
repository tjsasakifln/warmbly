package confenge

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type draftGenerationProcessorStub struct {
	remaining int
	calls     atomic.Int32
}

func (s *draftGenerationProcessorStub) ProcessDraftGenerationOnce(context.Context) (bool, error) {
	s.calls.Add(1)
	if s.remaining == 0 {
		return false, nil
	}
	s.remaining--
	return true, errors.New("deferred account must not stop the remaining burst")
}

func TestDraftGenerationRetryDelay(t *testing.T) {
	tests := []struct {
		attempts int
		want     time.Duration
	}{
		{attempts: 0, want: 15 * time.Minute},
		{attempts: 1, want: 15 * time.Minute},
		{attempts: 2, want: 30 * time.Minute},
		{attempts: 7, want: 16 * time.Hour},
		{attempts: 8, want: 24 * time.Hour},
		{attempts: 99, want: 24 * time.Hour},
	}
	for _, tt := range tests {
		if got := draftGenerationRetryDelay(tt.attempts); got != tt.want {
			t.Fatalf("attempts=%d: got %s want %s", tt.attempts, got, tt.want)
		}
	}
}

func TestDraftGenerationWorkerDrainsBurstUntilIdle(t *testing.T) {
	processor := &draftGenerationProcessorStub{remaining: 3}
	ctx, cancel := context.WithCancel(context.Background())
	worker := NewDraftGenerationWorker(processor, time.Hour)
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
