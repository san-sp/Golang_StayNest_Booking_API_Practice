package booking

import (
	"context"
	"log/slog"
	"time"
)

const (
	DefaultExpiryInterval  = 30 * time.Second
	DefaultExpiryBatchSize = 100
)

// BookingExpiryRepository defines the operation required by the expiry worker.
type BookingExpiryRepository interface {
	ExpirePendingBookings(
		ctx context.Context,
		now time.Time,
		limit int,
	) (int, error)
}

type ExpiryWorker struct {
	repo      BookingExpiryRepository
	interval  time.Duration
	batchSize int
}

func NewExpiryWorker(
	repo BookingExpiryRepository,
	interval time.Duration,
	batchSize int,
) *ExpiryWorker {
	if interval <= 0 {
		interval = DefaultExpiryInterval
	}

	if batchSize <= 0 {
		batchSize = DefaultExpiryBatchSize
	}

	return &ExpiryWorker{
		repo:      repo,
		interval:  interval,
		batchSize: batchSize,
	}
}

// Run starts the expiry worker and blocks until the context is cancelled.
func (w *ExpiryWorker) Run(ctx context.Context) {
	slog.Info(
		"booking expiry worker started",
		"interval", w.interval,
		"batch_size", w.batchSize,
	)

	// Process overdue bookings immediately on startup.
	w.process(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("booking expiry worker stopped")
			return

		case <-ticker.C:
			w.process(ctx)
		}
	}
}

func (w *ExpiryWorker) process(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	now := time.Now().UTC()

	expiredCount, err := w.repo.ExpirePendingBookings(
		ctx,
		now,
		w.batchSize,
	)
	if err != nil {
		if ctx.Err() != nil {
			return
		}

		slog.Error(
			"failed to expire pending bookings",
			"error", err,
		)
		return
	}

	if expiredCount > 0 {
		slog.Info(
			"pending bookings expired",
			"count", expiredCount,
		)
	}
}
