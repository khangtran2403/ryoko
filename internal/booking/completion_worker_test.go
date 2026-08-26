package booking

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

type fakePastBookingCompleter struct {
	mu             sync.Mutex
	calls          []time.Time
	affected       int64
	err            error
	started        chan struct{}
	startedOnce    sync.Once
	blockUntilDone bool
	contextErr     chan error
}

func (f *fakePastBookingCompleter) CompletePastBookings(
	ctx context.Context,
	now time.Time,
) (int64, error) {
	f.mu.Lock()
	f.calls = append(f.calls, now)
	f.mu.Unlock()

	if f.started != nil {
		f.startedOnce.Do(func() {
			close(f.started)
		})
	}

	if f.blockUntilDone {
		<-ctx.Done()
		if f.contextErr != nil {
			f.contextErr <- ctx.Err()
		}
		return 0, ctx.Err()
	}

	return f.affected, f.err
}

func (f *fakePastBookingCompleter) recordedCalls() []time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()

	calls := make([]time.Time, len(f.calls))
	copy(calls, f.calls)
	return calls
}

func TestCompletionWorkerRunImmediatelyAndStopsOnCancellation(t *testing.T) {
	started := make(chan struct{})
	service := &fakePastBookingCompleter{started: started}
	worker := NewCompletionWorker(
		service,
		time.Hour,
		log.New(io.Discard, "", 0),
	)
	fixedNow := time.Date(2030, time.January, 2, 3, 4, 5, 0, time.FixedZone("test", 7*60*60))
	worker.now = func() time.Time { return fixedNow }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Run(ctx)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not perform its startup completion pass")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after context cancellation")
	}

	calls := service.recordedCalls()
	if len(calls) != 1 {
		t.Fatalf("completion calls = %d, want 1", len(calls))
	}
	if !calls[0].Equal(fixedNow) || calls[0].Location() != fixedNow.Location() {
		t.Errorf("completion time = %v, want %v", calls[0], fixedNow)
	}
}

func TestCompletionWorkerRunOnceLogging(t *testing.T) {
	tests := []struct {
		name       string
		affected   int64
		err        error
		wantLog    string
		wantNoLogs bool
	}{
		{
			name:     "completed bookings",
			affected: 3,
			wantLog:  "completed 3 past bookings",
		},
		{
			name:       "nothing completed",
			affected:   0,
			wantNoLogs: true,
		},
		{
			name:    "service failure",
			err:     errors.New("database unavailable"),
			wantLog: "complete past bookings: database unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			service := &fakePastBookingCompleter{
				affected: tt.affected,
				err:      tt.err,
			}
			worker := NewCompletionWorker(
				service,
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

func TestCompletionWorkerDoesNotLogNormalShutdownAsFailure(t *testing.T) {
	var output bytes.Buffer
	service := &fakePastBookingCompleter{err: context.Canceled}
	worker := NewCompletionWorker(
		service,
		time.Hour,
		log.New(&output, "", 0),
	)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	worker.runOnce(ctx)

	if got := output.String(); got != "" {
		t.Errorf("log output = %q, want empty during normal shutdown", got)
	}
}

func TestCompletionWorkerRunOnceAppliesTimeout(t *testing.T) {
	contextErr := make(chan error, 1)
	service := &fakePastBookingCompleter{
		blockUntilDone: true,
		contextErr:     contextErr,
	}
	worker := NewCompletionWorker(
		service,
		time.Hour,
		log.New(io.Discard, "", 0),
	)
	worker.timeout = 10 * time.Millisecond

	worker.runOnce(context.Background())

	select {
	case err := <-contextErr:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("service context error = %v, want context.DeadlineExceeded", err)
		}
	default:
		t.Fatal("service did not observe the worker timeout")
	}
}
