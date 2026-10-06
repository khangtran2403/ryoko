package oauth

import (
	"context"
	"log"
	"time"
)

type expiredLoginCodeCleaner interface {
	DeleteExpiredLoginCodes(ctx context.Context) (int64, error)
}

type CleanupWorker struct {
	service  expiredLoginCodeCleaner
	interval time.Duration
	timeout  time.Duration
	logger   *log.Logger
}

func NewCleanupWorker(service expiredLoginCodeCleaner, interval time.Duration, logger *log.Logger) *CleanupWorker {
	return &CleanupWorker{
		service: service, interval: interval,
		timeout: 30 * time.Second, logger: logger,
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
	deleted, err := w.service.DeleteExpiredLoginCodes(ctx)
	if err != nil {
		if parent.Err() == nil {
			w.logger.Printf("delete expired OAuth login codes: %v", err)
		}
		return
	}
	if deleted > 0 {
		w.logger.Printf("deleted %d expired OAuth login codes", deleted)
	}
}
