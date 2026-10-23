package notification

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

type fakePendingEmailProcessor struct {
	mu      sync.Mutex
	calls   int
	result  BatchResult
	err     error
	started chan struct{}
	once    sync.Once
	process func(context.Context) (BatchResult, error)
}

func (f *fakePendingEmailProcessor) ProcessPendingEmails(
	ctx context.Context,
) (BatchResult, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.started != nil {
		f.once.Do(func() { close(f.started) })
	}
	if f.process != nil {
		return f.process(ctx)
	}
	return f.result, f.err
}

func (f *fakePendingEmailProcessor) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestWorkerRunsImmediatelyAndStops(t *testing.T) {
	started := make(chan struct{})
	service := &fakePendingEmailProcessor{started: started}
	worker := NewWorker(service, time.Hour, time.Minute, log.New(io.Discard, "", 0))
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

func TestWorkerAppliesRunTimeout(t *testing.T) {
	service := &fakePendingEmailProcessor{
		process: func(ctx context.Context) (BatchResult, error) {
			<-ctx.Done()
			return BatchResult{Claimed: 1}, ctx.Err()
		},
	}
	var output bytes.Buffer
	worker := NewWorker(service, time.Hour, 20*time.Millisecond, log.New(&output, "", 0))

	started := time.Now()
	worker.runOnce(context.Background())
	if elapsed := time.Since(started); elapsed < 15*time.Millisecond || elapsed > time.Second {
		t.Errorf("run duration = %v, want configured timeout", elapsed)
	}
	if !strings.Contains(output.String(), context.DeadlineExceeded.Error()) {
		t.Errorf("log = %q, want deadline error", output.String())
	}
}

func TestWorkerLogging(t *testing.T) {
	tests := []struct {
		name       string
		result     BatchResult
		err        error
		want       string
		wantSilent bool
	}{
		{
			name:   "sent",
			result: BatchResult{Claimed: 3, Sent: 3},
			want:   "sent 3 of 3 claimed outbox emails",
		},
		{
			name:       "empty",
			result:     BatchResult{},
			wantSilent: true,
		},
		{
			name:   "delivery failure",
			result: BatchResult{Claimed: 3, Sent: 1, Retried: 1, Failed: 1},
			err:    errors.New("SMTP unavailable"),
			want:   "claimed=3 sent=1 retried=1 failed=1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			worker := NewWorker(
				&fakePendingEmailProcessor{result: test.result, err: test.err},
				time.Hour,
				time.Minute,
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

func TestNewWorkerRejectsInvalidConfiguration(t *testing.T) {
	service := &fakePendingEmailProcessor{}
	logger := log.New(io.Discard, "", 0)
	tests := []struct {
		name  string
		build func()
	}{
		{name: "nil service", build: func() { NewWorker(nil, time.Minute, time.Minute, logger) }},
		{name: "invalid interval", build: func() { NewWorker(service, 0, time.Minute, logger) }},
		{name: "invalid timeout", build: func() { NewWorker(service, time.Minute, 0, logger) }},
		{name: "nil logger", build: func() { NewWorker(service, time.Minute, time.Minute, nil) }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("NewWorker() did not panic")
				}
			}()
			test.build()
		})
	}
}
