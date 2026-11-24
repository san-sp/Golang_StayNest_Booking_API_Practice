package payment

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"staynest/internal/booking"
	"staynest/internal/payment/razorpay"
)

func TestPaymentLifecycleWebhookAndBrowserVerificationAreIdempotent(
	t *testing.T,
) {
	testCases := []struct {
		name         string
		webhookFirst bool
	}{
		{
			name:         "webhook arrives before browser verification",
			webhookFirst: true,
		},
		{
			name:         "browser verification arrives before webhook",
			webhookFirst: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			repo, pool := setupPaymentRepository(t)
			fixture := createPaymentTestFixture(t, pool)

			ctx := context.Background()

			_, testPayment := createPaymentWithProviderOrderForTest(
				t,
				repo,
				fixture,
			)

			orderID := *testPayment.ProviderOrderID

			providerPaymentID := fmt.Sprintf(
				"pay_lifecycle_%d",
				time.Now().UnixNano(),
			)

			eventID := fmt.Sprintf(
				"evt_lifecycle_%d",
				time.Now().UnixNano(),
			)

			provider := &fakePaymentProvider{
				fetchedPayment: &razorpay.ProviderPayment{
					ID:       providerPaymentID,
					OrderID:  orderID,
					Amount:   testPayment.AmountPaise,
					Currency: testPayment.Currency,
					Status:   StatusCaptured,
					Captured: true,
				},
				signatureValid: true,
			}

			orderService := &OrderService{
				repo:     repo,
				provider: provider,
				keyID:    "test_key",
			}

			paymentService, err := NewService(
				repo,
				orderService,
				provider,
			)
			if err != nil {
				t.Fatalf("NewService() error = %v", err)
			}

			webhookService, err := NewWebhookService(
				repo,
				testWebhookSecret,
			)
			if err != nil {
				t.Fatalf("NewWebhookService() error = %v", err)
			}

			verify := func() {
				t.Helper()

				_, err := paymentService.Verify(
					ctx,
					VerifyInput{
						PaymentID:         testPayment.ID,
						CustomerID:        fixture.customerID,
						ProviderOrderID:   orderID,
						ProviderPaymentID: providerPaymentID,
						Signature:         "valid-signature",
					},
				)

				if err != nil {
					t.Fatalf("payment verification failed: %v", err)
				}
			}

			processWebhook := func() {
				t.Helper()

				body := createCapturedWebhookPayload(
					t,
					testPayment,
					providerPaymentID,
				)

				err := webhookService.Process(
					ctx,
					eventID,
					signWebhookPayload(body, testWebhookSecret),
					body,
				)

				if err != nil {
					t.Fatalf("webhook processing failed: %v", err)
				}
			}

			if tc.webhookFirst {
				processWebhook()
				verify()
			} else {
				verify()
				processWebhook()
			}

			// Exactly one payment attempt must exist.
			var attemptCount int

			err = pool.QueryRow(ctx, `
				SELECT COUNT(*)
				FROM payment_attempts
				WHERE payment_id = $1
				  AND provider_payment_id = $2
			`, testPayment.ID, providerPaymentID).Scan(&attemptCount)

			if err != nil {
				t.Fatalf("count payment attempts: %v", err)
			}

			if attemptCount != 1 {
				t.Fatalf(
					"expected exactly one payment attempt, got %d",
					attemptCount,
				)
			}

			// Payment must be captured and fulfilled.
			var paymentStatus string
			var fulfillmentStatus string

			err = pool.QueryRow(ctx, `
				SELECT status, fulfillment_status
				FROM payments
				WHERE id = $1
			`, testPayment.ID).Scan(
				&paymentStatus,
				&fulfillmentStatus,
			)

			if err != nil {
				t.Fatalf("retrieve payment: %v", err)
			}

			if paymentStatus != StatusCaptured {
				t.Errorf(
					"expected payment status %q, got %q",
					StatusCaptured,
					paymentStatus,
				)
			}

			if fulfillmentStatus != FulfillmentFulfilled {
				t.Errorf(
					"expected fulfillment status %q, got %q",
					FulfillmentFulfilled,
					fulfillmentStatus,
				)
			}

			// Booking must be confirmed exactly as expected.
			var bookingStatus string

			err = pool.QueryRow(ctx, `
				SELECT status
				FROM bookings
				WHERE id = $1
			`, testPayment.BookingID).Scan(&bookingStatus)

			if err != nil {
				t.Fatalf("retrieve booking: %v", err)
			}

			if bookingStatus != "confirmed" {
				t.Errorf(
					"expected booking status confirmed, got %q",
					bookingStatus,
				)
			}

			// Webhook event must be processed.
			var eventStatus string

			err = pool.QueryRow(ctx, `
				SELECT processing_status
				FROM payment_webhook_events
				WHERE provider_event_id = $1
			`, eventID).Scan(&eventStatus)

			if err != nil {
				t.Fatalf("retrieve webhook event: %v", err)
			}

			if eventStatus != WebhookStatusProcessed {
				t.Errorf(
					"expected webhook status %q, got %q",
					WebhookStatusProcessed,
					eventStatus,
				)
			}
		})
	}
}

func TestPaymentLifecycleRecoversWhenBrowserVerificationProviderFails(
	t *testing.T,
) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	ctx := context.Background()

	_, testPayment := createPaymentWithProviderOrderForTest(
		t,
		repo,
		fixture,
	)

	orderID := *testPayment.ProviderOrderID

	providerPaymentID := fmt.Sprintf(
		"pay_recovery_%d",
		time.Now().UnixNano(),
	)

	eventID := fmt.Sprintf(
		"evt_recovery_%d",
		time.Now().UnixNano(),
	)

	providerErr := errors.New("simulated temporary Razorpay outage")

	// Browser verification cannot reach Razorpay.
	provider := &fakePaymentProvider{
		fetchErr:       providerErr,
		signatureValid: true,
	}

	orderService := &OrderService{
		repo:     repo,
		provider: provider,
		keyID:    "test_key",
	}

	paymentService, err := NewService(
		repo,
		orderService,
		provider,
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	_, err = paymentService.Verify(
		ctx,
		VerifyInput{
			PaymentID:         testPayment.ID,
			CustomerID:        fixture.customerID,
			ProviderOrderID:   orderID,
			ProviderPaymentID: providerPaymentID,
			Signature:         "valid-signature",
		},
	)

	if !errors.Is(err, providerErr) {
		t.Fatalf(
			"expected provider error %v, got %v",
			providerErr,
			err,
		)
	}

	// Verification failure must not capture or fulfill the payment.
	var paymentStatus string
	var fulfillmentStatus string

	err = pool.QueryRow(ctx, `
        SELECT status, fulfillment_status
        FROM payments
        WHERE id = $1
    `, testPayment.ID).Scan(
		&paymentStatus,
		&fulfillmentStatus,
	)
	if err != nil {
		t.Fatalf("retrieve payment after verification failure: %v", err)
	}

	if paymentStatus != StatusCreated {
		t.Fatalf(
			"expected payment status to remain %q, got %q",
			StatusCreated,
			paymentStatus,
		)
	}

	if fulfillmentStatus != FulfillmentPending {
		t.Fatalf(
			"expected fulfillment status to remain %q, got %q",
			FulfillmentPending,
			fulfillmentStatus,
		)
	}

	var attemptCount int

	err = pool.QueryRow(ctx, `
        SELECT COUNT(*)
        FROM payment_attempts
        WHERE payment_id = $1
    `, testPayment.ID).Scan(&attemptCount)
	if err != nil {
		t.Fatalf("count payment attempts after verification failure: %v", err)
	}

	if attemptCount != 0 {
		t.Fatalf(
			"expected zero payment attempts before webhook, got %d",
			attemptCount,
		)
	}

	// Razorpay later sends the authoritative captured-payment webhook.
	webhookService, err := NewWebhookService(
		repo,
		testWebhookSecret,
	)
	if err != nil {
		t.Fatalf("NewWebhookService() error = %v", err)
	}

	body := createCapturedWebhookPayload(
		t,
		testPayment,
		providerPaymentID,
	)

	err = webhookService.Process(
		ctx,
		eventID,
		signWebhookPayload(body, testWebhookSecret),
		body,
	)
	if err != nil {
		t.Fatalf("webhook processing failed: %v", err)
	}

	// The webhook must successfully fulfill the payment.
	err = pool.QueryRow(ctx, `
        SELECT status, fulfillment_status
        FROM payments
        WHERE id = $1
    `, testPayment.ID).Scan(
		&paymentStatus,
		&fulfillmentStatus,
	)
	if err != nil {
		t.Fatalf("retrieve payment after webhook: %v", err)
	}

	if paymentStatus != StatusCaptured {
		t.Errorf(
			"expected payment status %q, got %q",
			StatusCaptured,
			paymentStatus,
		)
	}

	if fulfillmentStatus != FulfillmentFulfilled {
		t.Errorf(
			"expected fulfillment status %q, got %q",
			FulfillmentFulfilled,
			fulfillmentStatus,
		)
	}

	// Booking must now be confirmed.
	var bookingStatus string

	err = pool.QueryRow(ctx, `
        SELECT status
        FROM bookings
        WHERE id = $1
    `, testPayment.BookingID).Scan(&bookingStatus)
	if err != nil {
		t.Fatalf("retrieve booking: %v", err)
	}

	if bookingStatus != "confirmed" {
		t.Errorf(
			"expected booking status confirmed, got %q",
			bookingStatus,
		)
	}

	// Exactly one payment attempt must have been recorded.
	err = pool.QueryRow(ctx, `
        SELECT COUNT(*)
        FROM payment_attempts
        WHERE payment_id = $1
          AND provider_payment_id = $2
    `, testPayment.ID, providerPaymentID).Scan(&attemptCount)
	if err != nil {
		t.Fatalf("count payment attempts: %v", err)
	}

	if attemptCount != 1 {
		t.Errorf(
			"expected exactly one payment attempt, got %d",
			attemptCount,
		)
	}

	// Webhook event must be marked processed.
	var eventStatus string

	err = pool.QueryRow(ctx, `
        SELECT processing_status
        FROM payment_webhook_events
        WHERE provider_event_id = $1
    `, eventID).Scan(&eventStatus)
	if err != nil {
		t.Fatalf("retrieve webhook event: %v", err)
	}

	if eventStatus != WebhookStatusProcessed {
		t.Errorf(
			"expected webhook status %q, got %q",
			WebhookStatusProcessed,
			eventStatus,
		)
	}
}

func TestPaymentLifecycleLateCaptureAndRefund(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	ctx := context.Background()
	now := time.Now().UTC()

	testBooking, testPayment := createPaymentWithProviderOrderForTest(
		t,
		repo,
		fixture,
	)

	// 1. Expire the booking using the actual expiry repository operation.
	_, err := pool.Exec(ctx, `
        UPDATE bookings
        SET payment_expires_at = $1
        WHERE id = $2
    `, now.Add(-time.Minute), testBooking.ID)
	if err != nil {
		t.Fatalf("expire payment deadline: %v", err)
	}

	bookingRepo := booking.NewRepository(pool)

	expiredCount, err := bookingRepo.ExpirePendingBookings(ctx, now, 10)
	if err != nil {
		t.Fatalf("ExpirePendingBookings() error = %v", err)
	}

	if expiredCount != 1 {
		t.Fatalf("expected 1 expired booking, got %d", expiredCount)
	}

	// 2. Razorpay reports that payment was captured after booking expiry.
	eventID := fmt.Sprintf("evt_lifecycle_late_%d", time.Now().UnixNano())
	providerPaymentID := fmt.Sprintf("pay_lifecycle_late_%d", time.Now().UnixNano())

	webhookService, err := NewWebhookService(repo, testWebhookSecret)
	if err != nil {
		t.Fatalf("NewWebhookService() error = %v", err)
	}

	body := createCapturedWebhookPayload(
		t,
		testPayment,
		providerPaymentID,
	)

	err = webhookService.Process(
		ctx,
		eventID,
		signWebhookPayload(body, testWebhookSecret),
		body,
	)
	if err != nil {
		t.Fatalf("process late payment webhook: %v", err)
	}

	// 3. Payment must be captured, but fulfillment must require a refund.
	var paymentStatus string
	var fulfillmentStatus string

	err = pool.QueryRow(ctx, `
        SELECT status, fulfillment_status
        FROM payments
        WHERE id = $1
    `, testPayment.ID).Scan(&paymentStatus, &fulfillmentStatus)
	if err != nil {
		t.Fatalf("retrieve payment: %v", err)
	}

	if paymentStatus != StatusCaptured {
		t.Errorf("expected payment status %q, got %q",
			StatusCaptured, paymentStatus)
	}

	if fulfillmentStatus != FulfillmentRefundRequired {
		t.Errorf("expected fulfillment status %q, got %q",
			FulfillmentRefundRequired, fulfillmentStatus)
	}

	// 4. Confirm that the expired booking was not resurrected.
	var bookingStatus string

	err = pool.QueryRow(ctx, `
        SELECT status
        FROM bookings
        WHERE id = $1
    `, testBooking.ID).Scan(&bookingStatus)
	if err != nil {
		t.Fatalf("retrieve booking: %v", err)
	}

	if bookingStatus != booking.StatusExpired {
		t.Errorf("expected booking status %q, got %q",
			booking.StatusExpired, bookingStatus)
	}

	// 5. The late capture must have created a pending refund.
	var refundStatus string

	err = pool.QueryRow(ctx, `
        SELECT status
        FROM payment_refunds
        WHERE payment_id = $1
    `, testPayment.ID).Scan(&refundStatus)
	if err != nil {
		t.Fatalf("retrieve refund: %v", err)
	}

	if refundStatus != RefundStatusPending {
		t.Fatalf("expected refund status %q, got %q",
			RefundStatusPending, refundStatus)
	}

	// 6. Run the actual refund batch processor.
	refundProvider := &mockRefundProvider{
		createResult: &razorpay.Refund{
			ID:        "rfnd_lifecycle_late_001",
			PaymentID: providerPaymentID,
			Amount:    testPayment.AmountPaise,
			Currency:  testPayment.Currency,
			Status:    "processed",
		},
	}

	refundService := newTestRefundService(t, repo, refundProvider)

	err = refundService.ProcessPendingBatch(ctx)
	if err != nil {
		t.Fatalf("ProcessPendingBatch() error = %v", err)
	}

	// 7. Verify that the refund succeeded and its provider ID was persisted.
	var (
		finalRefundStatus string
		providerRefundID  *string
	)

	err = pool.QueryRow(ctx, `
        SELECT status, provider_refund_id
        FROM payment_refunds
        WHERE payment_id = $1
    `, testPayment.ID).Scan(&finalRefundStatus, &providerRefundID)
	if err != nil {
		t.Fatalf("retrieve processed refund: %v", err)
	}

	if finalRefundStatus != RefundStatusSucceeded {
		t.Errorf("expected refund status %q, got %q",
			RefundStatusSucceeded, finalRefundStatus)
	}

	if providerRefundID == nil ||
		*providerRefundID != "rfnd_lifecycle_late_001" {
		t.Errorf("unexpected provider refund ID: %v", providerRefundID)
	}

	// Exactly one refund must have been requested from the provider.
	createCalls, fetchCalls := refundProvider.counts()

	if createCalls != 1 {
		t.Errorf("expected 1 CreateRefund call, got %d", createCalls)
	}

	if fetchCalls != 0 {
		t.Errorf("expected 0 FetchRefund calls, got %d", fetchCalls)
	}

	// 8. The room inventory must remain released.
	var availableRooms int

	err = pool.QueryRow(ctx, `
        SELECT available_rooms
        FROM room_inventory
        WHERE room_type_id = $1
          AND stay_date = $2
    `, testBooking.RoomTypeID, testBooking.CheckIn).Scan(&availableRooms)
	if err != nil {
		t.Fatalf("retrieve inventory: %v", err)
	}

	if availableRooms != 5 {
		t.Errorf("expected 5 available rooms, got %d", availableRooms)
	}

	// 9. Webhook must remain successfully processed.
	var eventStatus string

	err = pool.QueryRow(ctx, `
        SELECT processing_status
        FROM payment_webhook_events
        WHERE provider_event_id = $1
    `, eventID).Scan(&eventStatus)
	if err != nil {
		t.Fatalf("retrieve webhook event: %v", err)
	}

	if eventStatus != WebhookStatusProcessed {
		t.Errorf("expected webhook status %q, got %q",
			WebhookStatusProcessed, eventStatus)
	}
}
