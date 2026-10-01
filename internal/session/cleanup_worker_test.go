package session

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

type fakeExpiredRefreshTokenCleaner struct {
	mu             sync.Mutex
	calls          int
	deleted        int64
	err            error
	started        chan struct{}
	startedOnce    sync.Once
	blockUntilDone bool
	contextErr     chan error
}

func (f *fakeExpiredRefreshTokenCleaner) DeleteExpiredRefreshTokens(ctx context.Context) (int64, error) {
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
	return f.deleted, f.err
}

func (f *fakeExpiredRefreshTokenCleaner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestCleanupWorkerRunsImmediatelyAndStopsOnCancellation(t *testing.T) {
	started := make(chan struct{})
	service := &fakeExpiredRefreshTokenCleaner{started: started}
	worker := NewCleanupWorker(service, time.Hour, log.New(io.Discard, "", 0))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Run(ctx)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not perform startup cleanup")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after cancellation")
	}
	if calls := service.callCount(); calls != 1 {
		t.Errorf("cleanup calls = %d, want 1", calls)
	}
}

func TestCleanupWorkerRunOnceLogging(t *testing.T) {
	tests := []struct {
		name       string
		deleted    int64
		err        error
		wantLog    string
		wantNoLogs bool
	}{
		{name: "deleted tokens", deleted: 3, wantLog: "deleted 3 expired refresh tokens"},
		{name: "nothing deleted", wantNoLogs: true},
		{name: "service failure", err: errors.New("database unavailable"), wantLog: "delete expired refresh tokens: database unavailable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			service := &fakeExpiredRefreshTokenCleaner{deleted: tt.deleted, err: tt.err}
			worker := NewCleanupWorker(service, time.Hour, log.New(&output, "", 0))
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

func TestCleanupWorkerDoesNotLogNormalShutdown(t *testing.T) {
	var output bytes.Buffer
	service := &fakeExpiredRefreshTokenCleaner{err: context.Canceled}
	worker := NewCleanupWorker(service, time.Hour, log.New(&output, "", 0))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	worker.runOnce(ctx)
	if got := output.String(); got != "" {
		t.Errorf("log output = %q, want empty", got)
	}
}

func TestCleanupWorkerRunOnceAppliesTimeout(t *testing.T) {
	contextErr := make(chan error, 1)
	service := &fakeExpiredRefreshTokenCleaner{
		blockUntilDone: true,
		contextErr:     contextErr,
	}
	worker := NewCleanupWorker(service, time.Hour, log.New(io.Discard, "", 0))
	worker.timeout = 10 * time.Millisecond
	worker.runOnce(context.Background())

	select {
	case err := <-contextErr:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("service context error = %v, want context.DeadlineExceeded", err)
		}
	default:
		t.Fatal("service did not observe cleanup timeout")
	}
}
