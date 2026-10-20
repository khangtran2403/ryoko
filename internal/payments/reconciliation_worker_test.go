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

type fakePendingPaymentReconciler struct {
	mu      sync.Mutex
	calls   int
	result  ReconciliationBatchResult
	err     error
	started chan struct{}
	once    sync.Once
}

func (f *fakePendingPaymentReconciler) ReconcilePendingPayments(
	context.Context,
) (ReconciliationBatchResult, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.started != nil {
		f.once.Do(func() { close(f.started) })
	}
	return f.result, f.err
}

func (f *fakePendingPaymentReconciler) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestReconciliationWorkerRunsImmediatelyAndStops(t *testing.T) {
	started := make(chan struct{})
	service := &fakePendingPaymentReconciler{started: started}
	worker := NewReconciliationWorker(service, time.Hour, log.New(io.Discard, "", 0))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Run(ctx)
	}()

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
	if service.callCount() != 1 {
		t.Errorf("calls = %d, want 1", service.callCount())
	}
}

func TestReconciliationWorkerLogging(t *testing.T) {
	tests := []struct {
		name       string
		result     ReconciliationBatchResult
		err        error
		want       string
		wantSilent bool
	}{
		{name: "reconciled", result: ReconciliationBatchResult{Checked: 3, Reconciled: 2, Unchanged: 1}, want: "reconciled 2 of 3"},
		{name: "unchanged", result: ReconciliationBatchResult{Checked: 2, Unchanged: 2}, wantSilent: true},
		{name: "failure", result: ReconciliationBatchResult{Checked: 2, Failed: 1}, err: errors.New("provider unavailable"), want: "checked=2 reconciled=0 unchanged=0 failed=1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			worker := NewReconciliationWorker(
				&fakePendingPaymentReconciler{result: test.result, err: test.err},
				time.Hour,
				log.New(&output, "", 0),
			)
			worker.runOnce(context.Background())
			if test.wantSilent && output.Len() != 0 {
				t.Errorf("log = %q, want empty", output.String())
			}
			if test.want != "" && !strings.Contains(output.String(), test.want) {
				t.Errorf("log = %q, want %q", output.String(), test.want)
			}
		})
	}
}
