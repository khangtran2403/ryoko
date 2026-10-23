package notification

import (
	"context"
	"log"
	"time"
)

type pendingEmailProcessor interface {
	ProcessPendingEmails(context.Context) (BatchResult, error)
}

type Worker struct {
	service  pendingEmailProcessor
	interval time.Duration
	timeout  time.Duration
	logger   *log.Logger
}

func NewWorker(service pendingEmailProcessor, interval time.Duration, timeout time.Duration, logger *log.Logger) *Worker {
	if service == nil {
		panic("notification outbox service must not be nil")
	}
	if interval <= 0 {
		panic("notification outbox interval must be positive")
	}
	if timeout <= 0 {
		panic("notification outbox timeout must be positive")
	}
	if logger == nil {
		panic("notification outbox logger must not be nil")
	}
	return &Worker{
		service:  service,
		interval: interval,
		timeout:  timeout,
		logger:   logger,
	}
}

func (w *Worker) Run(ctx context.Context) {
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

func (w *Worker) runOnce(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, w.timeout)
	defer cancel()

	result, err := w.service.ProcessPendingEmails(ctx)
	if err != nil {
		if parent.Err() != nil {
			return
		}
		w.logger.Printf(
			"process email outbox: claimed=%d sent=%d retried=%d failed=%d: %v",
			result.Claimed,
			result.Sent,
			result.Retried,
			result.Failed,
			err,
		)
		return
	}
	if result.Sent > 0 {
		w.logger.Printf(
			"sent %d of %d claimed outbox emails",
			result.Sent,
			result.Claimed,
		)
	}
}
