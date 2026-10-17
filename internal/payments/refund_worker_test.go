package payments

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakePendingRefundProcessor struct {
	mu          sync.Mutex
	calls       int
	result      RefundBatchResult
	err         error
	started     chan struct{}
	startedOnce sync.Once
}

func (f *fakePendingRefundProcessor) ProcessPendingRefunds(context.Context) (RefundBatchResult, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.started != nil {
		f.startedOnce.Do(func() { close(f.started) })
	}
	return f.result, f.err
}

func TestRefundWorkerRunsImmediatelyAndStops(t *testing.T) {
	started := make(chan struct{})
	service := &fakePendingRefundProcessor{started: started}
	worker := NewRefundWorker(service, time.Hour, log.New(io.Discard, "", 0))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); worker.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not run immediately")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
	service.mu.Lock()
	calls := service.calls
	service.mu.Unlock()
	if calls != 1 {
		t.Errorf("service calls = %d, want 1", calls)
	}
}

func TestRefundWorkerLogsResults(t *testing.T) {
	tests := []struct {
		name    string
		result  RefundBatchResult
		err     error
		wantLog string
	}{
		{name: "refunded", result: RefundBatchResult{Attempted: 2, Refunded: 2}, wantLog: "refunded 2 payments"},
		{name: "nothing", result: RefundBatchResult{}},
		{name: "failure", result: RefundBatchResult{Attempted: 2, Refunded: 1, Failed: 1}, err: errors.New("provider unavailable"), wantLog: "attempted=2 refunded=1 failed=1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			worker := NewRefundWorker(
				&fakePendingRefundProcessor{result: tt.result, err: tt.err},
				time.Hour,
				log.New(&output, "", 0),
			)
			worker.runOnce(context.Background())
			if !strings.Contains(output.String(), tt.wantLog) {
				t.Errorf("log = %q, want substring %q", output.String(), tt.wantLog)
			}
		})
	}
}
