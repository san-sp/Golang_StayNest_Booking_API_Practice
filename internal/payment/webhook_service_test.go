package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"staynest/internal/booking"
	"sync"
	"testing"
	"time"
)

const testWebhookSecret = "staynest-test-webhook-secret"

func signWebhookPayload(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)

	return hex.EncodeToString(mac.Sum(nil))
}

func createCapturedWebhookPayload(
	t *testing.T,
	payment *Payment,
	providerPaymentID string,
) []byte {
	t.Helper()

	payload := map[string]any{
		"event": "payment.captured",
		"payload": map[string]any{
			"payment": map[string]any{
				"entity": map[string]any{
					"id":       providerPaymentID,
					"order_id": *payment.ProviderOrderID,
					"amount":   payment.AmountPaise,
					"currency": payment.Currency,
					"status":   "captured",
					"captured": true,
				},
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal webhook payload: %v", err)
	}

	return body
}

func createWebhookPayload(
	t *testing.T,
	eventType string,
	entity map[string]any,
) []byte {
	t.Helper()

	payload := map[string]any{
		"event": eventType,
		"payload": map[string]any{
			"payment": map[string]any{
				"entity": entity,
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal webhook payload: %v", err)
	}

	return body
}

func assertWebhookStatus(
	t *testing.T,
	pool interface {
		QueryRow(context.Context, string, ...any) interface {
			Scan(...any) error
		}
	},
	eventID string,
	expectedStatus string,
) {
	t.Helper()

	var status string

	err := pool.QueryRow(
		context.Background(),
		`
		SELECT processing_status
		FROM payment_webhook_events
		WHERE provider_event_id = $1
		`,
		eventID,
	).Scan(&status)

	if err != nil {
		t.Fatalf("failed to retrieve webhook status: %v", err)
	}

	if status != expectedStatus {
		t.Fatalf("expected webhook status %q, got %q", expectedStatus, status)
	}
}

func TestWebhookServiceProcessesCapturedPayment(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	testBooking, testPayment := createPaymentWithProviderOrderForTest(
		t,
		repo,
		fixture,
	)

	service, err := NewWebhookService(repo, testWebhookSecret)
	if err != nil {
		t.Fatalf("failed to create webhook service: %v", err)
	}

	eventID := fmt.Sprintf("evt_service_%d", time.Now().UnixNano())
	providerPaymentID := fmt.Sprintf("pay_service_%d", time.Now().UnixNano())

	body := createCapturedWebhookPayload(
		t,
		testPayment,
		providerPaymentID,
	)

	signature := signWebhookPayload(body, testWebhookSecret)

	err = service.Process(
		context.Background(),
		eventID,
		signature,
		body,
	)
	if err != nil {
		t.Fatalf("failed to process captured webhook: %v", err)
	}

	// The booking must be confirmed.
	var bookingStatus string

	err = pool.QueryRow(
		context.Background(),
		`SELECT status FROM bookings WHERE id = $1`,
		testBooking.ID,
	).Scan(&bookingStatus)

	if err != nil {
		t.Fatalf("failed to retrieve booking status: %v", err)
	}

	if bookingStatus != "confirmed" {
		t.Fatalf(
			"expected booking status confirmed, got %q",
			bookingStatus,
		)
	}

	// The payment must be captured and fulfilled.
	var paymentStatus string
	var fulfillmentStatus string

	err = pool.QueryRow(
		context.Background(),
		`
		SELECT status, fulfillment_status
		FROM payments
		WHERE id = $1
		`,
		testPayment.ID,
	).Scan(&paymentStatus, &fulfillmentStatus)

	if err != nil {
		t.Fatalf("failed to retrieve payment status: %v", err)
	}

	if paymentStatus != StatusCaptured {
		t.Fatalf("expected payment captured, got %q", paymentStatus)
	}

	if fulfillmentStatus != FulfillmentFulfilled {
		t.Fatalf(
			"expected fulfillment %q, got %q",
			FulfillmentFulfilled,
			fulfillmentStatus,
		)
	}

	// The webhook event must be marked processed.
	var eventStatus string
	var storedPaymentID string

	err = pool.QueryRow(
		context.Background(),
		`
		SELECT processing_status, payment_id::text
		FROM payment_webhook_events
		WHERE provider_event_id = $1
		`,
		eventID,
	).Scan(&eventStatus, &storedPaymentID)

	if err != nil {
		t.Fatalf("failed to retrieve webhook event: %v", err)
	}

	if eventStatus != WebhookStatusProcessed {
		t.Fatalf(
			"expected webhook status %q, got %q",
			WebhookStatusProcessed,
			eventStatus,
		)
	}

	if storedPaymentID != testPayment.ID {
		t.Fatalf(
			"expected payment ID %q, got %q",
			testPayment.ID,
			storedPaymentID,
		)
	}
}

func TestWebhookServiceHandlesDuplicateDelivery(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	testBooking, testPayment := createPaymentWithProviderOrderForTest(
		t,
		repo,
		fixture,
	)

	service, err := NewWebhookService(repo, testWebhookSecret)
	if err != nil {
		t.Fatalf("failed to create webhook service: %v", err)
	}

	eventID := fmt.Sprintf("evt_duplicate_%d", time.Now().UnixNano())
	providerPaymentID := fmt.Sprintf("pay_duplicate_%d", time.Now().UnixNano())

	body := createCapturedWebhookPayload(
		t,
		testPayment,
		providerPaymentID,
	)

	signature := signWebhookPayload(body, testWebhookSecret)

	// First delivery.
	if err := service.Process(
		context.Background(),
		eventID,
		signature,
		body,
	); err != nil {
		t.Fatalf("first delivery failed: %v", err)
	}

	// Second delivery of the exact same event.
	if err := service.Process(
		context.Background(),
		eventID,
		signature,
		body,
	); err != nil {
		t.Fatalf("duplicate delivery failed: %v", err)
	}

	// Only one captured payment attempt should exist.
	var attemptCount int

	err = pool.QueryRow(
		context.Background(),
		`
		SELECT COUNT(*)
		FROM payment_attempts
		WHERE payment_id = $1
		  AND provider_payment_id = $2
		`,
		testPayment.ID,
		providerPaymentID,
	).Scan(&attemptCount)

	if err != nil {
		t.Fatalf("failed to count payment attempts: %v", err)
	}

	if attemptCount != 1 {
		t.Fatalf("expected one payment attempt, got %d", attemptCount)
	}

	// The booking must still have exactly one confirmed state.
	var bookingStatus string

	err = pool.QueryRow(
		context.Background(),
		`SELECT status FROM bookings WHERE id = $1`,
		testBooking.ID,
	).Scan(&bookingStatus)

	if err != nil {
		t.Fatalf("failed to retrieve booking status: %v", err)
	}

	if bookingStatus != "confirmed" {
		t.Fatalf("expected confirmed booking, got %q", bookingStatus)
	}

	// Only one event should exist for this provider event ID.
	var eventCount int

	err = pool.QueryRow(
		context.Background(),
		`
		SELECT COUNT(*)
		FROM payment_webhook_events
		WHERE provider = $1
		  AND provider_event_id = $2
		`,
		ProviderRazorpay,
		eventID,
	).Scan(&eventCount)

	if err != nil {
		t.Fatalf("failed to count webhook events: %v", err)
	}

	if eventCount != 1 {
		t.Fatalf("expected one webhook event, got %d", eventCount)
	}
}

func TestWebhookServiceRejectsInvalidSignature(t *testing.T) {
	repo, pool := setupPaymentRepository(t)

	service, err := NewWebhookService(repo, testWebhookSecret)
	if err != nil {
		t.Fatalf("failed to create webhook service: %v", err)
	}

	eventID := fmt.Sprintf("evt_invalid_%d", time.Now().UnixNano())

	body := []byte(`{"event":"payment.captured"}`)

	err = service.Process(
		context.Background(),
		eventID,
		"invalid-signature",
		body,
	)

	if !errors.Is(err, ErrInvalidWebhookSignature) {
		t.Fatalf(
			"expected ErrInvalidWebhookSignature, got %v",
			err,
		)
	}

	// Invalid signatures must not create database records.
	var eventCount int

	err = pool.QueryRow(
		context.Background(),
		`
		SELECT COUNT(*)
		FROM payment_webhook_events
		WHERE provider_event_id = $1
		`,
		eventID,
	).Scan(&eventCount)

	if err != nil {
		t.Fatalf("failed to count webhook events: %v", err)
	}

	if eventCount != 0 {
		t.Fatalf("expected zero webhook events, got %d", eventCount)
	}
}

func TestWebhookServiceRejectsMalformedSignedPayload(t *testing.T) {
	repo, pool := setupPaymentRepository(t)

	service, err := NewWebhookService(repo, testWebhookSecret)
	if err != nil {
		t.Fatalf("failed to create webhook service: %v", err)
	}

	eventID := fmt.Sprintf("evt_malformed_%d", time.Now().UnixNano())

	// The signature is valid, but the JSON is malformed.
	body := []byte(`{"event": "payment.captured"`)

	signature := signWebhookPayload(body, testWebhookSecret)

	err = service.Process(
		context.Background(),
		eventID,
		signature,
		body,
	)

	if !errors.Is(err, ErrInvalidWebhookPayload) {
		t.Fatalf("expected ErrInvalidWebhookPayload, got %v", err)
	}

	var eventCount int

	err = pool.QueryRow(
		context.Background(),
		`
		SELECT COUNT(*)
		FROM payment_webhook_events
		WHERE provider_event_id = $1
		`,
		eventID,
	).Scan(&eventCount)

	if err != nil {
		t.Fatalf("failed to count webhook events: %v", err)
	}

	if eventCount != 0 {
		t.Fatalf("expected zero webhook events, got %d", eventCount)
	}
}

func TestWebhookServiceRejectsInvalidCapturedPaymentData(t *testing.T) {
	testCases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "missing payment ID",
			mutate: func(entity map[string]any) {
				entity["id"] = ""
			},
		},
		{
			name: "missing order ID",
			mutate: func(entity map[string]any) {
				entity["order_id"] = ""
			},
		},
		{
			name: "invalid amount",
			mutate: func(entity map[string]any) {
				entity["amount"] = 0
			},
		},
		{
			name: "missing currency",
			mutate: func(entity map[string]any) {
				entity["currency"] = ""
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			repo, pool := setupPaymentRepository(t)
			fixture := createPaymentTestFixture(t, pool)

			_, testPayment := createPaymentWithProviderOrderForTest(
				t,
				repo,
				fixture,
			)

			service, err := NewWebhookService(repo, testWebhookSecret)
			if err != nil {
				t.Fatalf("failed to create webhook service: %v", err)
			}

			entity := map[string]any{
				"id":       fmt.Sprintf("pay_invalid_%d", time.Now().UnixNano()),
				"order_id": *testPayment.ProviderOrderID,
				"amount":   testPayment.AmountPaise,
				"currency": testPayment.Currency,
				"status":   "captured",
				"captured": true,
			}

			tc.mutate(entity)

			body := createWebhookPayload(
				t,
				"payment.captured",
				entity,
			)

			eventID := fmt.Sprintf("evt_invalid_data_%d", time.Now().UnixNano())

			err = service.Process(
				context.Background(),
				eventID,
				signWebhookPayload(body, testWebhookSecret),
				body,
			)

			if !errors.Is(err, ErrInvalidWebhookPayload) {
				t.Fatalf("expected ErrInvalidWebhookPayload, got %v", err)
			}

			var eventStatus string

			err = pool.QueryRow(
				context.Background(),
				`
				SELECT processing_status
				FROM payment_webhook_events
				WHERE provider_event_id = $1
				`,
				eventID,
			).Scan(&eventStatus)

			if err != nil {
				t.Fatalf("failed to retrieve webhook event: %v", err)
			}

			if eventStatus != WebhookStatusFailed {
				t.Fatalf(
					"expected webhook status %q, got %q",
					WebhookStatusFailed,
					eventStatus,
				)
			}
		})
	}
}

func TestWebhookServiceRejectsUncapturedPayment(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	_, testPayment := createPaymentWithProviderOrderForTest(
		t,
		repo,
		fixture,
	)

	service, err := NewWebhookService(repo, testWebhookSecret)
	if err != nil {
		t.Fatalf("failed to create webhook service: %v", err)
	}

	entity := map[string]any{
		"id":       fmt.Sprintf("pay_uncaptured_%d", time.Now().UnixNano()),
		"order_id": *testPayment.ProviderOrderID,
		"amount":   testPayment.AmountPaise,
		"currency": testPayment.Currency,
		"status":   "authorized",
		"captured": false,
	}

	body := createWebhookPayload(t, "payment.captured", entity)
	eventID := fmt.Sprintf("evt_uncaptured_%d", time.Now().UnixNano())

	err = service.Process(
		context.Background(),
		eventID,
		signWebhookPayload(body, testWebhookSecret),
		body,
	)

	if !errors.Is(err, ErrPaymentNotCaptured) {
		t.Fatalf("expected ErrPaymentNotCaptured, got %v", err)
	}

	var eventStatus string

	err = pool.QueryRow(
		context.Background(),
		`
		SELECT processing_status
		FROM payment_webhook_events
		WHERE provider_event_id = $1
		`,
		eventID,
	).Scan(&eventStatus)

	if err != nil {
		t.Fatalf("failed to retrieve webhook event: %v", err)
	}

	if eventStatus != WebhookStatusFailed {
		t.Fatalf(
			"expected webhook status %q, got %q",
			WebhookStatusFailed,
			eventStatus,
		)
	}
}

func TestWebhookServiceRejectsUnknownProviderOrder(t *testing.T) {
	repo, pool := setupPaymentRepository(t)

	service, err := NewWebhookService(repo, testWebhookSecret)
	if err != nil {
		t.Fatalf("failed to create webhook service: %v", err)
	}

	entity := map[string]any{
		"id":       fmt.Sprintf("pay_unknown_%d", time.Now().UnixNano()),
		"order_id": "order_unknown_test",
		"amount":   10000,
		"currency": "INR",
		"status":   "captured",
		"captured": true,
	}

	body := createWebhookPayload(t, "payment.captured", entity)
	eventID := fmt.Sprintf("evt_unknown_order_%d", time.Now().UnixNano())

	err = service.Process(
		context.Background(),
		eventID,
		signWebhookPayload(body, testWebhookSecret),
		body,
	)

	if !errors.Is(err, ErrPaymentNotFound) {
		t.Fatalf("expected ErrPaymentNotFound, got %v", err)
	}

	var eventStatus string

	err = pool.QueryRow(
		context.Background(),
		`
		SELECT processing_status
		FROM payment_webhook_events
		WHERE provider_event_id = $1
		`,
		eventID,
	).Scan(&eventStatus)

	if err != nil {
		t.Fatalf("failed to retrieve webhook event: %v", err)
	}

	if eventStatus != WebhookStatusFailed {
		t.Fatalf(
			"expected webhook status %q, got %q",
			WebhookStatusFailed,
			eventStatus,
		)
	}
}

func TestWebhookServiceAcknowledgesUnsupportedEvent(t *testing.T) {
	repo, pool := setupPaymentRepository(t)

	service, err := NewWebhookService(repo, testWebhookSecret)
	if err != nil {
		t.Fatalf("failed to create webhook service: %v", err)
	}

	eventID := fmt.Sprintf("evt_unsupported_%d", time.Now().UnixNano())

	body := createWebhookPayload(
		t,
		"payment.failed",
		map[string]any{},
	)

	err = service.Process(
		context.Background(),
		eventID,
		signWebhookPayload(body, testWebhookSecret),
		body,
	)

	if err != nil {
		t.Fatalf("expected unsupported event to be acknowledged, got %v", err)
	}

	var eventType string
	var eventStatus string

	err = pool.QueryRow(
		context.Background(),
		`
		SELECT event_type, processing_status
		FROM payment_webhook_events
		WHERE provider_event_id = $1
		`,
		eventID,
	).Scan(&eventType, &eventStatus)

	if err != nil {
		t.Fatalf("failed to retrieve webhook event: %v", err)
	}

	if eventType != "payment.failed" {
		t.Fatalf("expected event type payment.failed, got %q", eventType)
	}

	if eventStatus != WebhookStatusProcessed {
		t.Fatalf(
			"expected webhook status %q, got %q",
			WebhookStatusProcessed,
			eventStatus,
		)
	}
}

func TestWebhookServiceHandlesCapturedPaymentAfterExpiry(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	ctx := context.Background()
	now := time.Now().UTC()

	// Create a booking and its associated payment.
	testBooking, testPayment := createPaymentWithProviderOrderForTest(
		t,
		repo,
		fixture,
	)

	// Simulate the payment deadline passing.
	_, err := pool.Exec(ctx, `
		UPDATE bookings
		SET payment_expires_at = $1
		WHERE id = $2
	`, now.Add(-time.Minute), testBooking.ID)
	if err != nil {
		t.Fatalf("failed to expire payment deadline: %v", err)
	}

	// Run the actual booking expiry repository operation.
	bookingRepo := booking.NewRepository(pool)

	expiredCount, err := bookingRepo.ExpirePendingBookings(ctx, now, 10)
	if err != nil {
		t.Fatalf("ExpirePendingBookings() error = %v", err)
	}

	if expiredCount != 1 {
		t.Fatalf("expected 1 expired booking, got %d", expiredCount)
	}

	service, err := NewWebhookService(repo, testWebhookSecret)
	if err != nil {
		t.Fatalf("failed to create webhook service: %v", err)
	}

	eventID := fmt.Sprintf("evt_late_payment_%d", time.Now().UnixNano())
	providerPaymentID := fmt.Sprintf("pay_late_payment_%d", time.Now().UnixNano())

	body := createCapturedWebhookPayload(
		t,
		testPayment,
		providerPaymentID,
	)

	// Razorpay reports a successful capture after booking expiry.
	err = service.Process(
		ctx,
		eventID,
		signWebhookPayload(body, testWebhookSecret),
		body,
	)
	if err != nil {
		t.Fatalf("failed to process late payment webhook: %v", err)
	}

	// Payment must be captured, but fulfillment must require a refund.
	var paymentStatus string
	var fulfillmentStatus string

	err = pool.QueryRow(ctx, `
		SELECT status, fulfillment_status
		FROM payments
		WHERE id = $1
	`, testPayment.ID).Scan(&paymentStatus, &fulfillmentStatus)
	if err != nil {
		t.Fatalf("failed to retrieve payment: %v", err)
	}

	if paymentStatus != StatusCaptured {
		t.Errorf("expected payment status %q, got %q",
			StatusCaptured, paymentStatus)
	}

	if fulfillmentStatus != FulfillmentRefundRequired {
		t.Errorf("expected fulfillment status %q, got %q",
			FulfillmentRefundRequired, fulfillmentStatus)
	}

	// The expired booking must never be confirmed.
	var bookingStatus string

	err = pool.QueryRow(ctx, `
		SELECT status
		FROM bookings
		WHERE id = $1
	`, testBooking.ID).Scan(&bookingStatus)
	if err != nil {
		t.Fatalf("failed to retrieve booking: %v", err)
	}

	if bookingStatus != booking.StatusExpired {
		t.Errorf("expected booking status %q, got %q",
			booking.StatusExpired, bookingStatus)
	}

	// A refund must have been created.
	var refundStatus string

	err = pool.QueryRow(ctx, `
		SELECT status
		FROM payment_refunds
		WHERE payment_id = $1
	`, testPayment.ID).Scan(&refundStatus)
	if err != nil {
		t.Fatalf("failed to retrieve refund: %v", err)
	}

	if refundStatus != RefundStatusPending {
		t.Errorf("expected refund status %q, got %q",
			RefundStatusPending, refundStatus)
	}

	// The expiry worker must have restored the reserved inventory.
	var availableRooms int

	err = pool.QueryRow(ctx, `
		SELECT available_rooms
		FROM room_inventory
		WHERE room_type_id = $1
		  AND stay_date = $2
	`, testBooking.RoomTypeID, testBooking.CheckIn).Scan(&availableRooms)
	if err != nil {
		t.Fatalf("failed to retrieve inventory: %v", err)
	}

	if availableRooms != 5 {
		t.Errorf("expected 5 available rooms after expiry, got %d",
			availableRooms)
	}

	// The webhook itself must be marked processed.
	var eventStatus string

	err = pool.QueryRow(ctx, `
		SELECT processing_status
		FROM payment_webhook_events
		WHERE provider_event_id = $1
	`, eventID).Scan(&eventStatus)
	if err != nil {
		t.Fatalf("failed to retrieve webhook event: %v", err)
	}

	if eventStatus != WebhookStatusProcessed {
		t.Errorf("expected webhook status %q, got %q",
			WebhookStatusProcessed, eventStatus)
	}
}

func TestWebhookServiceHandlesConcurrentDuplicateDeliveries(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	ctx := context.Background()

	testBooking, testPayment := createPaymentWithProviderOrderForTest(
		t,
		repo,
		fixture,
	)

	service, err := NewWebhookService(repo, testWebhookSecret)
	if err != nil {
		t.Fatalf("failed to create webhook service: %v", err)
	}

	eventID := fmt.Sprintf("evt_concurrent_%d", time.Now().UnixNano())
	providerPaymentID := fmt.Sprintf("pay_concurrent_%d", time.Now().UnixNano())

	body := createCapturedWebhookPayload(
		t,
		testPayment,
		providerPaymentID,
	)

	signature := signWebhookPayload(body, testWebhookSecret)

	const deliveries = 10

	start := make(chan struct{})
	results := make(chan error, deliveries)

	var wg sync.WaitGroup

	// Simulate multiple simultaneous deliveries of the same event.
	for i := 0; i < deliveries; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			// Wait until all goroutines are ready.
			<-start

			results <- service.Process(
				ctx,
				eventID,
				signature,
				body,
			)
		}()
	}

	// Release all deliveries simultaneously.
	close(start)

	wg.Wait()
	close(results)

	// Every delivery should be handled successfully.
	for err := range results {
		if err != nil {
			t.Errorf("concurrent webhook delivery failed: %v", err)
		}
	}

	// Exactly one webhook event should exist.
	var eventCount int

	err = pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM payment_webhook_events
		WHERE provider = $1
		  AND provider_event_id = $2
	`, ProviderRazorpay, eventID).Scan(&eventCount)
	if err != nil {
		t.Fatalf("failed to count webhook events: %v", err)
	}

	if eventCount != 1 {
		t.Fatalf("expected one webhook event, got %d", eventCount)
	}

	// Exactly one payment attempt should exist.
	var attemptCount int

	err = pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM payment_attempts
		WHERE payment_id = $1
		  AND provider_payment_id = $2
	`, testPayment.ID, providerPaymentID).Scan(&attemptCount)
	if err != nil {
		t.Fatalf("failed to count payment attempts: %v", err)
	}

	if attemptCount != 1 {
		t.Fatalf("expected one payment attempt, got %d", attemptCount)
	}

	// Booking must be confirmed.
	var bookingStatus string

	err = pool.QueryRow(ctx, `
		SELECT status
		FROM bookings
		WHERE id = $1
	`, testBooking.ID).Scan(&bookingStatus)
	if err != nil {
		t.Fatalf("failed to retrieve booking status: %v", err)
	}

	if bookingStatus != booking.StatusConfirmed {
		t.Errorf("expected booking status %q, got %q",
			booking.StatusConfirmed, bookingStatus)
	}

	// Payment must be captured and fulfilled.
	var paymentStatus string
	var fulfillmentStatus string

	err = pool.QueryRow(ctx, `
		SELECT status, fulfillment_status
		FROM payments
		WHERE id = $1
	`, testPayment.ID).Scan(&paymentStatus, &fulfillmentStatus)
	if err != nil {
		t.Fatalf("failed to retrieve payment status: %v", err)
	}

	if paymentStatus != StatusCaptured {
		t.Errorf("expected payment status %q, got %q",
			StatusCaptured, paymentStatus)
	}

	if fulfillmentStatus != FulfillmentFulfilled {
		t.Errorf("expected fulfillment status %q, got %q",
			FulfillmentFulfilled, fulfillmentStatus)
	}

	// Webhook event must be processed.
	var eventStatus string

	err = pool.QueryRow(ctx, `
		SELECT processing_status
		FROM payment_webhook_events
		WHERE provider_event_id = $1
	`, eventID).Scan(&eventStatus)
	if err != nil {
		t.Fatalf("failed to retrieve webhook status: %v", err)
	}

	if eventStatus != WebhookStatusProcessed {
		t.Errorf("expected webhook status %q, got %q",
			WebhookStatusProcessed, eventStatus)
	}
}
