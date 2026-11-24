package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

func newWebhookTestEventID() string {
	return fmt.Sprintf("evt_test_%d", time.Now().UnixNano())
}

func TestRepositoryRecordWebhookEvent(t *testing.T) {
	repo, _ := setupPaymentRepository(t)

	ctx := context.Background()
	eventID := newWebhookTestEventID()

	payload := json.RawMessage(`{
		"event": "payment.captured",
		"payload": {
			"payment": {
				"entity": {
					"id": "pay_test_123"
				}
			}
		}
	}`)

	// First delivery must insert a new event.
	event, inserted, err := repo.RecordWebhookEvent(
		ctx,
		eventID,
		"payment.captured",
		payload,
	)
	if err != nil {
		t.Fatalf("failed to record webhook event: %v", err)
	}

	if !inserted {
		t.Fatal("expected first delivery to insert a new event")
	}

	if event.ProviderEventID != eventID {
		t.Fatalf("expected event ID %q, got %q", eventID, event.ProviderEventID)
	}

	if event.ProcessingStatus != WebhookStatusReceived {
		t.Fatalf(
			"expected status %q, got %q",
			WebhookStatusReceived,
			event.ProcessingStatus,
		)
	}

	// Second delivery with the same event ID and equivalent JSON
	// must retrieve the original event rather than insert another.
	duplicatePayload := json.RawMessage(
		`{"payload":{"payment":{"entity":{"id":"pay_test_123"}}},"event":"payment.captured"}`,
	)

	duplicate, inserted, err := repo.RecordWebhookEvent(
		ctx,
		eventID,
		"payment.captured",
		duplicatePayload,
	)
	if err != nil {
		t.Fatalf("failed to retrieve duplicate webhook event: %v", err)
	}

	if inserted {
		t.Fatal("expected duplicate delivery not to insert another event")
	}

	if duplicate.ID != event.ID {
		t.Fatalf(
			"expected original event ID %q, got %q",
			event.ID,
			duplicate.ID,
		)
	}

	if duplicate.ProcessingStatus != WebhookStatusReceived {
		t.Fatalf(
			"expected duplicate status %q, got %q",
			WebhookStatusReceived,
			duplicate.ProcessingStatus,
		)
	}
}

func TestRepositoryRecordWebhookEventRejectsConflictingPayload(
	t *testing.T,
) {
	repo, _ := setupPaymentRepository(t)

	ctx := context.Background()
	eventID := newWebhookTestEventID()

	originalPayload := json.RawMessage(
		`{"event":"payment.captured","payment_id":"pay_original"}`,
	)

	_, inserted, err := repo.RecordWebhookEvent(
		ctx,
		eventID,
		"payment.captured",
		originalPayload,
	)
	if err != nil {
		t.Fatalf("failed to record original event: %v", err)
	}

	if !inserted {
		t.Fatal("expected original event to be inserted")
	}

	// Same event ID, different payload.
	conflictingPayload := json.RawMessage(
		`{"event":"payment.captured","payment_id":"pay_different"}`,
	)

	_, inserted, err = repo.RecordWebhookEvent(
		ctx,
		eventID,
		"payment.captured",
		conflictingPayload,
	)

	if !errors.Is(err, ErrWebhookEventConflict) {
		t.Fatalf(
			"expected ErrWebhookEventConflict, got %v",
			err,
		)
	}

	if inserted {
		t.Fatal("conflicting event must not be inserted")
	}
}

func TestRepositoryRecordWebhookEventRejectsConflictingEventType(
	t *testing.T,
) {
	repo, _ := setupPaymentRepository(t)

	ctx := context.Background()
	eventID := newWebhookTestEventID()

	payload := json.RawMessage(`{"event":"payment.captured"}`)

	_, _, err := repo.RecordWebhookEvent(
		ctx,
		eventID,
		"payment.captured",
		payload,
	)
	if err != nil {
		t.Fatalf("failed to record original event: %v", err)
	}

	_, inserted, err := repo.RecordWebhookEvent(
		ctx,
		eventID,
		"payment.failed",
		payload,
	)

	if !errors.Is(err, ErrWebhookEventConflict) {
		t.Fatalf(
			"expected ErrWebhookEventConflict, got %v",
			err,
		)
	}

	if inserted {
		t.Fatal("conflicting event type must not be inserted")
	}
}

func TestRepositoryWebhookEventStatusTransitions(t *testing.T) {
	repo, _ := setupPaymentRepository(t)

	ctx := context.Background()
	eventID := newWebhookTestEventID()

	payload := json.RawMessage(`{"event":"payment.captured"}`)

	_, inserted, err := repo.RecordWebhookEvent(
		ctx,
		eventID,
		"payment.captured",
		payload,
	)
	if err != nil {
		t.Fatalf("failed to record webhook event: %v", err)
	}

	if !inserted {
		t.Fatal("expected event to be inserted")
	}

	// A processing failure should be recorded.
	if err := repo.MarkWebhookEventFailed(ctx, eventID); err != nil {
		t.Fatalf("failed to mark webhook event failed: %v", err)
	}

	event, inserted, err := repo.RecordWebhookEvent(
		ctx,
		eventID,
		"payment.captured",
		payload,
	)
	if err != nil {
		t.Fatalf("failed to retrieve failed event: %v", err)
	}

	if inserted {
		t.Fatal("expected existing event")
	}

	if event.ProcessingStatus != WebhookStatusFailed {
		t.Fatalf(
			"expected status %q, got %q",
			WebhookStatusFailed,
			event.ProcessingStatus,
		)
	}

	// A successful retry should mark the event processed.
	if err := repo.MarkWebhookEventProcessed(ctx, eventID, ""); err != nil {
		t.Fatalf("failed to mark webhook event processed: %v", err)
	}

	event, _, err = repo.RecordWebhookEvent(
		ctx,
		eventID,
		"payment.captured",
		payload,
	)
	if err != nil {
		t.Fatalf("failed to retrieve processed event: %v", err)
	}

	if event.ProcessingStatus != WebhookStatusProcessed {
		t.Fatalf(
			"expected status %q, got %q",
			WebhookStatusProcessed,
			event.ProcessingStatus,
		)
	}

	if event.ProcessedAt == nil {
		t.Fatal("expected processed_at to be set")
	}

	// A later failure must never downgrade a processed event.
	if err := repo.MarkWebhookEventFailed(ctx, eventID); err != nil {
		t.Fatalf("failed to mark already processed event failed: %v", err)
	}

	event, _, err = repo.RecordWebhookEvent(
		ctx,
		eventID,
		"payment.captured",
		payload,
	)
	if err != nil {
		t.Fatalf("failed to retrieve final event: %v", err)
	}

	if event.ProcessingStatus != WebhookStatusProcessed {
		t.Fatalf(
			"processed event was downgraded: expected %q, got %q",
			WebhookStatusProcessed,
			event.ProcessingStatus,
		)
	}
}

func TestRepositoryGetByProviderOrderID(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	_, createdPayment := createPaymentWithProviderOrderForTest(
		t,
		repo,
		fixture,
	)

	retrieved, err := repo.GetByProviderOrderID(
		context.Background(),
		*createdPayment.ProviderOrderID,
	)
	if err != nil {
		t.Fatalf("failed to retrieve payment: %v", err)
	}

	if retrieved.ID != createdPayment.ID {
		t.Fatalf(
			"expected payment ID %q, got %q",
			createdPayment.ID,
			retrieved.ID,
		)
	}

	if retrieved.CustomerID != createdPayment.CustomerID {
		t.Fatalf(
			"expected customer ID %q, got %q",
			createdPayment.CustomerID,
			retrieved.CustomerID,
		)
	}
}

func TestRepositoryGetByProviderOrderIDNotFound(t *testing.T) {
	repo, _ := setupPaymentRepository(t)

	_, err := repo.GetByProviderOrderID(
		context.Background(),
		"order_does_not_exist",
	)

	if !errors.Is(err, ErrPaymentNotFound) {
		t.Fatalf("expected ErrPaymentNotFound, got %v", err)
	}
}
