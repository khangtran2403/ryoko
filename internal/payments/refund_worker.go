package payments

import (
	"context"
	"log"
	"time"
)

type pendingRefundProcessor interface {
	ProcessPendingRefunds(context.Context) (RefundBatchResult, error)
}

type RefundWorker struct {
	service  pendingRefundProcessor
	interval time.Duration
	timeout  time.Duration
	logger   *log.Logger
}

func NewRefundWorker(
	service pendingRefundProcessor,
	interval time.Duration,
	timeout time.Duration,
	logger *log.Logger,
) *RefundWorker {
	if service == nil {
		panic("payment refund service must not be nil")
	}
	if interval <= 0 {
		panic("payment refund interval must be positive")
	}
	if timeout <= 0 {
		panic("payment refund timeout must be positive")
	}
	if logger == nil {
		panic("payment refund logger must not be nil")
	}
	return &RefundWorker{service: service, interval: interval, timeout: timeout, logger: logger}
}

func (w *RefundWorker) Run(ctx context.Context) {
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

func (w *RefundWorker) runOnce(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, w.timeout)
	defer cancel()
	result, err := w.service.ProcessPendingRefunds(ctx)
	if err != nil {
		if parent.Err() != nil {
			return
		}
		w.logger.Printf(
			"process pending refunds: attempted=%d refunded=%d failed=%d: %v",
			result.Attempted,
			result.Refunded,
			result.Failed,
			err,
		)
		return
	}
	if result.Refunded > 0 {
		w.logger.Printf("refunded %d payments", result.Refunded)
	}
}
