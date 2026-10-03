package passwordreset

import (
	"context"
	"log"
	"time"
)

type expiredRequestCleaner interface {
	DeleteExpiredPasswordResetRequests(ctx context.Context) (int64, error)
}

type CleanupWorker struct {
	service  expiredRequestCleaner
	interval time.Duration
	timeout  time.Duration
	logger   *log.Logger
}

func NewCleanupWorker(
	service expiredRequestCleaner,
	interval time.Duration,
	logger *log.Logger,
) *CleanupWorker {
	return &CleanupWorker{
		service:  service,
		interval: interval,
		timeout:  30 * time.Second,
		logger:   logger,
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

	deleted, err := w.service.DeleteExpiredPasswordResetRequests(ctx)
	if err != nil {
		if parent.Err() != nil {
			return
		}
		w.logger.Printf("delete expired password reset requests: %v", err)
		return
	}
	if deleted > 0 {
		w.logger.Printf("deleted %d expired password reset requests", deleted)
	}
}
