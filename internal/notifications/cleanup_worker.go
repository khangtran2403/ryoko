package notification

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

type sentEmailCleaner interface {
	DeleteSentEmailOutboxBefore(context.Context, pgtype.Timestamptz) (int64, error)
}

type CleanupWorker struct {
	queries   sentEmailCleaner
	retention time.Duration
	interval  time.Duration
	timeout   time.Duration
	logger    *log.Logger
	now       func() time.Time
}

func NewCleanupWorker(
	queries sentEmailCleaner,
	retention time.Duration,
	interval time.Duration,
	timeout time.Duration,
	logger *log.Logger,
) *CleanupWorker {
	if queries == nil {
		panic("notification cleanup queries must not be nil")
	}
	if retention <= 0 {
		panic("notification retention must be positive")
	}
	if interval <= 0 {
		panic("notification cleanup interval must be positive")
	}
	if timeout <= 0 {
		panic("notification cleanup timeout must be positive")
	}
	if logger == nil {
		panic("notification cleanup logger must not be nil")
	}

	return &CleanupWorker{
		queries:   queries,
		retention: retention,
		interval:  interval,
		timeout:   timeout,
		logger:    logger,
		now:       time.Now,
	}
}

func (w *CleanupWorker) Run(ctx context.Context) {
	w.runOnce(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

func (w *CleanupWorker) runOnce(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, w.timeout)
	defer cancel()

	cutoff := w.now().UTC().Add(-w.retention)
	deleted, err := w.queries.DeleteSentEmailOutboxBefore(
		ctx,
		pgtype.Timestamptz{Time: cutoff, Valid: true},
	)
	if err != nil {
		if parent.Err() != nil {
			return
		}
		w.logger.Printf("delete expired sent email outbox messages: %v", err)
		return
	}
	if deleted > 0 {
		w.logger.Printf("deleted %d expired sent email outbox messages", deleted)
	}
}
