package payment

import (
	"context"
	"errors"
	"testing"
	"time"
)

type testRefundProcessor struct {
	expiredCalls chan struct{}
	pendingCalls chan struct{}

	expiredErr error
	pendingErr error
}

func newTestRefundProcessor() *testRefundProcessor {
	return &testRefundProcessor{
		expiredCalls: make(chan struct{}, 10),
		pendingCalls: make(chan struct{}, 10),
	}
}

func (p *testRefundProcessor) ProcessExpiredBatch(ctx context.Context) error {
	p.expiredCalls <- struct{}{}
	return p.expiredErr
}

func (p *testRefundProcessor) ProcessPendingBatch(ctx context.Context) error {
	p.pendingCalls <- struct{}{}
	return p.pendingErr
}

func waitForCall(t *testing.T, calls <-chan struct{}) {
	t.Helper()

	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for worker call")
	}
}

func waitForWorkerExit(t *testing.T, done <-chan struct{}) {
	t.Helper()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after context cancellation")
	}
}

func TestRefundWorkerProcessesImmediately(t *testing.T) {
	processor := newTestRefundProcessor()
	processor.expiredErr = errors.New("temporary expired-refund error")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	worker := NewRefundWorker(processor, time.Hour)

	go func() {
		defer close(done)
		worker.Run(ctx)
	}()

	// The first batch should run immediately, without waiting for the ticker.
	waitForCall(t, processor.expiredCalls)
	waitForCall(t, processor.pendingCalls)

	cancel()
	waitForWorkerExit(t, done)
}

func TestRefundWorkerRepeatsUntilCancelled(t *testing.T) {
	processor := newTestRefundProcessor()
	processor.pendingErr = errors.New("temporary pending-refund error")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	worker := NewRefundWorker(processor, 10*time.Millisecond)

	go func() {
		defer close(done)
		worker.Run(ctx)
	}()

	// Two calls to each method prove that the worker processes
	// immediately and then processes another batch on a later tick.
	waitForCall(t, processor.expiredCalls)
	waitForCall(t, processor.pendingCalls)
	waitForCall(t, processor.expiredCalls)
	waitForCall(t, processor.pendingCalls)

	cancel()
	waitForWorkerExit(t, done)
}

func TestRefundWorkerDoesNotProcessWhenAlreadyCancelled(t *testing.T) {
	processor := newTestRefundProcessor()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	worker := NewRefundWorker(processor, time.Millisecond)

	go func() {
		defer close(done)
		worker.Run(ctx)
	}()

	waitForWorkerExit(t, done)

	if len(processor.expiredCalls) != 0 {
		t.Fatal("worker processed expired refunds after cancellation")
	}

	if len(processor.pendingCalls) != 0 {
		t.Fatal("worker processed pending refunds after cancellation")
	}
}
