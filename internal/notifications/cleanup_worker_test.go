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

	"github.com/jackc/pgx/v5/pgtype"
)

type fakeSentEmailCleaner struct {
	mu           sync.Mutex
	calls        int
	cutoff       pgtype.Timestamptz
	deleted      int64
	err          error
	started      chan struct{}
	startedOnce  sync.Once
	deleteBefore func(context.Context, pgtype.Timestamptz) (int64, error)
}

func (f *fakeSentEmailCleaner) DeleteSentEmailOutboxBefore(
	ctx context.Context,
	cutoff pgtype.Timestamptz,
) (int64, error) {
	f.mu.Lock()
	f.calls++
	f.cutoff = cutoff
	f.mu.Unlock()
	if f.started != nil {
		f.startedOnce.Do(func() { close(f.started) })
	}
	if f.deleteBefore != nil {
		return f.deleteBefore(ctx, cutoff)
	}
	return f.deleted, f.err
}

func (f *fakeSentEmailCleaner) snapshot() (int, pgtype.Timestamptz) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.cutoff
}

func TestCleanupWorkerRunsImmediatelyAndStops(t *testing.T) {
	started := make(chan struct{})
	queries := &fakeSentEmailCleaner{started: started}
	worker := NewCleanupWorker(
		queries,
		30*24*time.Hour,
		time.Hour,
		time.Minute,
		log.New(io.Discard, "", 0),
	)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Run(ctx)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("cleanup worker did not run immediately")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup worker did not stop")
	}
	if calls, _ := queries.snapshot(); calls != 1 {
		t.Errorf("cleanup calls = %d, want 1", calls)
	}
}

func TestCleanupWorkerUsesRetentionCutoff(t *testing.T) {
	queries := &fakeSentEmailCleaner{}
	worker := NewCleanupWorker(
		queries,
		30*24*time.Hour,
		time.Hour,
		time.Minute,
		log.New(io.Discard, "", 0),
	)
	fixedNow := time.Date(2030, time.January, 31, 12, 0, 0, 0, time.FixedZone("test", 7*60*60))
	worker.now = func() time.Time { return fixedNow }

	worker.runOnce(context.Background())

	_, cutoff := queries.snapshot()
	want := fixedNow.UTC().Add(-30 * 24 * time.Hour)
	if !cutoff.Valid || !cutoff.Time.Equal(want) {
		t.Errorf("cutoff = %+v, want %v", cutoff, want)
	}
}

func TestCleanupWorkerAppliesRunTimeout(t *testing.T) {
	observedErr := make(chan error, 1)
	queries := &fakeSentEmailCleaner{
		deleteBefore: func(ctx context.Context, _ pgtype.Timestamptz) (int64, error) {
			<-ctx.Done()
			observedErr <- ctx.Err()
			return 0, ctx.Err()
		},
	}
	worker := NewCleanupWorker(
		queries,
		30*24*time.Hour,
		time.Hour,
		20*time.Millisecond,
		log.New(io.Discard, "", 0),
	)

	worker.runOnce(context.Background())

	select {
	case err := <-observedErr:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("cleanup context error = %v, want deadline exceeded", err)
		}
	default:
		t.Fatal("cleanup query did not observe its timeout")
	}
}

func TestCleanupWorkerLogging(t *testing.T) {
	tests := []struct {
		name       string
		deleted    int64
		err        error
		want       string
		wantSilent bool
	}{
		{name: "deleted", deleted: 3, want: "deleted 3 expired sent email outbox messages"},
		{name: "empty", wantSilent: true},
		{name: "query failure", err: errors.New("database unavailable"), want: "database unavailable"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			worker := NewCleanupWorker(
				&fakeSentEmailCleaner{deleted: test.deleted, err: test.err},
				30*24*time.Hour,
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

func TestCleanupWorkerDoesNotLogShutdownCancellation(t *testing.T) {
	var output bytes.Buffer
	worker := NewCleanupWorker(
		&fakeSentEmailCleaner{err: context.Canceled},
		30*24*time.Hour,
		time.Hour,
		time.Minute,
		log.New(&output, "", 0),
	)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	worker.runOnce(ctx)
	if output.Len() != 0 {
		t.Errorf("log = %q, want empty", output.String())
	}
}

func TestNewCleanupWorkerRejectsInvalidConfiguration(t *testing.T) {
	queries := &fakeSentEmailCleaner{}
	logger := log.New(io.Discard, "", 0)
	tests := []struct {
		name  string
		build func()
	}{
		{name: "nil queries", build: func() { NewCleanupWorker(nil, time.Hour, time.Hour, time.Minute, logger) }},
		{name: "invalid retention", build: func() { NewCleanupWorker(queries, 0, time.Hour, time.Minute, logger) }},
		{name: "invalid interval", build: func() { NewCleanupWorker(queries, time.Hour, 0, time.Minute, logger) }},
		{name: "invalid timeout", build: func() { NewCleanupWorker(queries, time.Hour, time.Hour, 0, logger) }},
		{name: "nil logger", build: func() { NewCleanupWorker(queries, time.Hour, time.Hour, time.Minute, nil) }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("NewCleanupWorker() did not panic")
				}
			}()
			test.build()
		})
	}
}
