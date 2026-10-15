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

type fakePendingPaymentExpirer struct {
	mu             sync.Mutex
	calls          int
	expired        int64
	err            error
	started        chan struct{}
	startedOnce    sync.Once
	blockUntilDone bool
	contextErr     chan error
}

func (f *fakePendingPaymentExpirer) ExpirePendingPayments(ctx context.Context) (int64, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.started != nil {
		f.startedOnce.Do(func() { close(f.started) })
	}
	if f.blockUntilDone {
		<-ctx.Done()
		if f.contextErr != nil {
			f.contextErr <- ctx.Err()
		}
		return 0, ctx.Err()
	}
	return f.expired, f.err
}

func (f *fakePendingPaymentExpirer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestExpirationWorkerRunsImmediatelyAndStops(t *testing.T) {
	started := make(chan struct{})
	service := &fakePendingPaymentExpirer{started: started}
	worker := NewExpirationWorker(service, time.Hour, log.New(io.Discard, "", 0))
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
		t.Fatal("worker did not stop after cancellation")
	}
	if service.callCount() != 1 {
		t.Errorf("service calls = %d, want 1", service.callCount())
	}
}

func TestExpirationWorkerRunOnceLogging(t *testing.T) {
	tests := []struct {
		name       string
		expired    int64
		err        error
		wantLog    string
		wantNoLogs bool
	}{
		{name: "expired payments", expired: 3, wantLog: "expired 3 pending payments"},
		{name: "nothing expired", wantNoLogs: true},
		{name: "service failure", err: errors.New("database unavailable"), wantLog: "expire pending payments: database unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			worker := NewExpirationWorker(
				&fakePendingPaymentExpirer{expired: tt.expired, err: tt.err},
				time.Hour,
				log.New(&output, "", 0),
			)
			worker.runOnce(context.Background())
			got := output.String()
			if tt.wantNoLogs && got != "" {
				t.Errorf("log output = %q, want empty", got)
			}
			if tt.wantLog != "" && !strings.Contains(got, tt.wantLog) {
				t.Errorf("log output = %q, want substring %q", got, tt.wantLog)
			}
		})
	}
}

func TestExpirationWorkerAppliesTimeoutAndIgnoresShutdownError(t *testing.T) {
	contextErr := make(chan error, 1)
	worker := NewExpirationWorker(
		&fakePendingPaymentExpirer{blockUntilDone: true, contextErr: contextErr},
		time.Hour,
		log.New(io.Discard, "", 0),
	)
	worker.timeout = 10 * time.Millisecond
	worker.runOnce(context.Background())
	if err := <-contextErr; !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("service context error = %v, want deadline exceeded", err)
	}

	var output bytes.Buffer
	cancelledWorker := NewExpirationWorker(
		&fakePendingPaymentExpirer{err: context.Canceled},
		time.Hour,
		log.New(&output, "", 0),
	)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelledWorker.runOnce(ctx)
	if output.Len() != 0 {
		t.Errorf("shutdown log = %q, want empty", output.String())
	}
}
