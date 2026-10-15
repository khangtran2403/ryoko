package payments

import (
	"context"
	"log"
	"time"
)

type pendingPaymentExpirer interface {
	ExpirePendingPayments(ctx context.Context) (int64, error)
}

type ExpirationWorker struct {
	service  pendingPaymentExpirer
	interval time.Duration
	timeout  time.Duration
	logger   *log.Logger
}

func NewExpirationWorker(service pendingPaymentExpirer, interval time.Duration, logger *log.Logger) *ExpirationWorker {
	if service == nil {
		panic("payment expiration service must not be nil")
	}
	if interval <= 0 {
		panic("payment expiration interval must be positive")
	}
	if logger == nil {
		panic("payment expiration logger must not be nil")
	}
	return &ExpirationWorker{
		service:  service,
		interval: interval,
		timeout:  30 * time.Second,
		logger:   logger,
	}
}

func (w *ExpirationWorker) Run(ctx context.Context) {
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

func (w *ExpirationWorker) runOnce(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, w.timeout)
	defer cancel()

	expired, err := w.service.ExpirePendingPayments(ctx)
	if err != nil {
		if parent.Err() != nil {
			return
		}
		w.logger.Printf("expire pending payments: %v", err)
		return
	}
	if expired > 0 {
		w.logger.Printf("expired %d pending payments", expired)
	}
}
