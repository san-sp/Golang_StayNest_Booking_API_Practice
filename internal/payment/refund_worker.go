package payment

import (
	"context"
	"log/slog"
	"time"
)

const DefaultRefundWorkerInterval = 15 * time.Second

type RefundProcessor interface {
	ProcessPendingBatch(ctx context.Context) error
	ProcessExpiredBatch(ctx context.Context) error
}

type RefundWorker struct {
	service  RefundProcessor
	interval time.Duration
}

func NewRefundWorker(
	service RefundProcessor,
	interval time.Duration,
) *RefundWorker {
	if interval <= 0 {
		interval = DefaultRefundWorkerInterval
	}

	return &RefundWorker{
		service:  service,
		interval: interval,
	}
}

func (w *RefundWorker) Run(ctx context.Context) {
	slog.Info(
		"refund worker started",
		"interval", w.interval,
	)

	w.process(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("refund worker stopped")
			return

		case <-ticker.C:
			w.process(ctx)
		}
	}
}

func (w *RefundWorker) process(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	if err := w.service.ProcessExpiredBatch(ctx); err != nil {
		if ctx.Err() != nil {
			return
		}

		slog.Error(
			"failed to process expired refund leases",
			"error", err,
		)
	}

	if ctx.Err() != nil {
		return
	}

	if err := w.service.ProcessPendingBatch(ctx); err != nil {
		if ctx.Err() != nil {
			return
		}

		slog.Error(
			"failed to process pending refunds",
			"error", err,
		)
	}
}
