package payments

import (
	"context"
	"log"
	"time"
)

type pendingPaymentReconciler interface {
	ReconcilePendingPayments(context.Context) (ReconciliationBatchResult, error)
}

type ReconciliationWorker struct {
	service  pendingPaymentReconciler
	interval time.Duration
	timeout  time.Duration
	logger   *log.Logger
}

func NewReconciliationWorker(
	service pendingPaymentReconciler,
	interval time.Duration,
	logger *log.Logger,
) *ReconciliationWorker {
	if service == nil {
		panic("payment reconciliation service must not be nil")
	}
	if interval <= 0 {
		panic("payment reconciliation interval must be positive")
	}
	if logger == nil {
		panic("payment reconciliation logger must not be nil")
	}
	return &ReconciliationWorker{
		service:  service,
		interval: interval,
		timeout:  30 * time.Second,
		logger:   logger,
	}
}

func (w *ReconciliationWorker) Run(ctx context.Context) {
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

func (w *ReconciliationWorker) runOnce(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, w.timeout)
	defer cancel()

	result, err := w.service.ReconcilePendingPayments(ctx)
	if err != nil {
		if parent.Err() != nil {
			return
		}
		w.logger.Printf(
			"reconcile pending payments: checked=%d reconciled=%d unchanged=%d failed=%d: %v",
			result.Checked,
			result.Reconciled,
			result.Unchanged,
			result.Failed,
			err,
		)
		return
	}
	if result.Reconciled > 0 {
		w.logger.Printf(
			"reconciled %d of %d pending payments",
			result.Reconciled,
			result.Checked,
		)
	}
}
