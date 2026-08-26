package booking

import (
	"context"
	"log"
	"time"
)

type pastBookingCompleter interface {
	CompletePastBookings(
		ctx context.Context,
		now time.Time,
	) (int64, error)
}

type CompletionWorker struct {
	service  pastBookingCompleter
	interval time.Duration
	timeout  time.Duration
	logger   *log.Logger
	now      func() time.Time
}

func NewCompletionWorker(
	service pastBookingCompleter,
	interval time.Duration,
	logger *log.Logger,
) *CompletionWorker {
	return &CompletionWorker{
		service:  service,
		interval: interval,
		timeout:  30 * time.Second,
		logger:   logger,
		now:      time.Now,
	}
}

func (w *CompletionWorker) Run(ctx context.Context) {
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

func (w *CompletionWorker) runOnce(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, w.timeout)
	defer cancel()

	affected, err := w.service.CompletePastBookings(ctx, w.now())
	if err != nil {
		if parent.Err() != nil {
			return
		}
		w.logger.Printf("complete past bookings: %v", err)
		return
	}

	if affected > 0 {
		w.logger.Printf("completed %d past bookings", affected)
	}
}
