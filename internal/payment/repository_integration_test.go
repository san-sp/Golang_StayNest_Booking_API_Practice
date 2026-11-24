package payment

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"staynest/internal/booking"
	"staynest/internal/database"
)

type paymentTestFixture struct {
	pool       *pgxpool.Pool
	customerID string
	hotelID    string
	roomTypeID string
}

const lockKey int64 = 2026112101

func lockIntegrationTestDatabase(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()

	const lockKey int64 = 2026112101

	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("failed to acquire integration test connection: %v", err)
	}

	_, err = conn.Exec(
		context.Background(),
		`SELECT pg_advisory_lock($1)`,
		lockKey,
	)
	if err != nil {
		conn.Release()
		t.Fatalf("failed to acquire integration test lock: %v", err)
	}

	t.Cleanup(func() {
		_, err := conn.Exec(
			context.Background(),
			`SELECT pg_advisory_unlock($1)`,
			lockKey,
		)
		if err != nil {
			t.Errorf("failed to release integration test lock: %v", err)
		}

		conn.Release()
	})
}

func setupPaymentRepository(
	t *testing.T,
) (*Repository, *pgxpool.Pool) {
	t.Helper()

	databaseURL := os.Getenv("STAYNEST_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("STAYNEST_TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	pool, err := database.NewPool(ctx, databaseURL)
	if err != nil {
		t.Fatalf("failed to connect to database: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
	})

	lockIntegrationTestDatabase(t, pool)

	return NewRepository(pool), pool
}

func createPaymentTestFixture(
	t *testing.T,
	pool *pgxpool.Pool,
) *paymentTestFixture {
	t.Helper()

	ctx := context.Background()

	fixture := &paymentTestFixture{
		pool: pool,
	}

	err := pool.QueryRow(ctx, `
		INSERT INTO customers (
			name,
			email,
			password_hash
		)
		VALUES ($1, $2, $3)
		RETURNING id::text
	`,
		"Payment Integration Customer",
		fmt.Sprintf("payment-test-%d@example.com", time.Now().UnixNano()),
		"test-password-hash",
	).Scan(&fixture.customerID)

	if err != nil {
		t.Fatalf("failed to create test customer: %v", err)
	}

	err = pool.QueryRow(ctx, `
		INSERT INTO hotels (
			name,
			description,
			address,
			city,
			country,
			timezone
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id::text
	`,
		"Payment Test Hotel",
		"Hotel for payment integration tests",
		"123 Payment Test Street",
		"Thiruvananthapuram",
		"India",
		"Asia/Kolkata",
	).Scan(&fixture.hotelID)

	if err != nil {
		t.Fatalf("failed to create test hotel: %v", err)
	}

	err = pool.QueryRow(ctx, `
		INSERT INTO room_types (
			hotel_id,
			name,
			description,
			max_occupancy,
			price_per_night_paise
		)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id::text
	`,
		fixture.hotelID,
		fmt.Sprintf("Payment Test Room %d", time.Now().UnixNano()),
		"Room for payment integration tests",
		4,
		int64(250000),
	).Scan(&fixture.roomTypeID)

	if err != nil {
		t.Fatalf("failed to create test room type: %v", err)
	}

	checkIn := time.Date(
		2027, time.January, 15,
		0, 0, 0, 0,
		time.UTC,
	)

	_, err = pool.Exec(ctx, `
		INSERT INTO room_inventory (
			room_type_id,
			stay_date,
			total_rooms,
			available_rooms
		)
		VALUES ($1, $2, $3, $3)
	`,
		fixture.roomTypeID,
		checkIn,
		5,
	)

	if err != nil {
		t.Fatalf("failed to create test inventory: %v", err)
	}

	// Remove payment records before deleting their referenced bookings.
	t.Cleanup(func() {
		cleanupCtx := context.Background()

		_, err := pool.Exec(cleanupCtx, `
			DELETE FROM payment_refunds
			WHERE payment_id IN (
				SELECT id FROM payments
				WHERE booking_id IN (
					SELECT id FROM bookings WHERE room_type_id = $1
				)
			)
		`, fixture.roomTypeID)
		if err != nil {
			t.Errorf("failed to clean up payment refunds: %v", err)
		}

		_, err = pool.Exec(cleanupCtx, `
			DELETE FROM payment_webhook_events
			WHERE payment_id IN (
				SELECT id FROM payments
				WHERE booking_id IN (
					SELECT id FROM bookings WHERE room_type_id = $1
				)
			)
		`, fixture.roomTypeID)
		if err != nil {
			t.Errorf("failed to clean up webhook events: %v", err)
		}

		_, err = pool.Exec(cleanupCtx, `
			DELETE FROM payment_attempts
			WHERE payment_id IN (
				SELECT id FROM payments
				WHERE booking_id IN (
					SELECT id FROM bookings WHERE room_type_id = $1
				)
			)
		`, fixture.roomTypeID)
		if err != nil {
			t.Errorf("failed to clean up payment attempts: %v", err)
		}

		_, err = pool.Exec(cleanupCtx, `
			DELETE FROM payments
			WHERE booking_id IN (
				SELECT id FROM bookings WHERE room_type_id = $1
			)
		`, fixture.roomTypeID)
		if err != nil {
			t.Errorf("failed to clean up payments: %v", err)
		}

		_, err = pool.Exec(cleanupCtx,
			`DELETE FROM bookings WHERE room_type_id = $1`,
			fixture.roomTypeID,
		)
		if err != nil {
			t.Errorf("failed to clean up bookings: %v", err)
		}

		_, err = pool.Exec(cleanupCtx,
			`DELETE FROM room_inventory WHERE room_type_id = $1`,
			fixture.roomTypeID,
		)
		if err != nil {
			t.Errorf("failed to clean up inventory: %v", err)
		}

		_, err = pool.Exec(cleanupCtx,
			`DELETE FROM room_types WHERE id = $1`,
			fixture.roomTypeID,
		)
		if err != nil {
			t.Errorf("failed to clean up room type: %v", err)
		}

		_, err = pool.Exec(cleanupCtx,
			`DELETE FROM hotels WHERE id = $1`,
			fixture.hotelID,
		)
		if err != nil {
			t.Errorf("failed to clean up hotel: %v", err)
		}

		_, err = pool.Exec(cleanupCtx,
			`DELETE FROM customers WHERE id = $1`,
			fixture.customerID,
		)
		if err != nil {
			t.Errorf("failed to clean up customer: %v", err)
		}
	})

	return fixture
}

// createPendingTestBooking creates a real booking through the booking repository.
func (f *paymentTestFixture) createPendingTestBooking(
	t *testing.T,
	expiresAt time.Time,
) *booking.Booking {
	t.Helper()

	checkIn := time.Date(
		2027, time.January, 15,
		0, 0, 0, 0,
		time.UTC,
	)

	checkOut := checkIn.AddDate(0, 0, 1)

	bookingRepo := booking.NewRepository(f.pool)

	input := booking.Booking{
		BookingReference: fmt.Sprintf(
			"STN-PAY-%d",
			time.Now().UnixNano(),
		),
		CustomerID:       f.customerID,
		RoomTypeID:       f.roomTypeID,
		CheckIn:          checkIn,
		CheckOut:         checkOut,
		RoomsCount:       1,
		GuestsCount:      2,
		Status:           booking.StatusPendingPayment,
		PaymentExpiresAt: &expiresAt,
	}

	created, err := bookingRepo.Create(context.Background(), input)
	if err != nil {
		t.Fatalf("failed to create pending test booking: %v", err)
	}

	return created
}

func createPaymentForLeaseTest(
	t *testing.T,
	repo *Repository,
	fixture *paymentTestFixture,
) *Payment {
	t.Helper()

	testBooking := fixture.createPendingTestBooking(
		t,
		time.Now().UTC().Add(time.Hour),
	)

	payment, err := repo.CreatePending(
		context.Background(),
		InitiateInput{
			BookingID:  testBooking.ID,
			CustomerID: fixture.customerID,
			IdempotencyKey: fmt.Sprintf(
				"order-lease-%d",
				time.Now().UnixNano(),
			),
		},
	)

	if err != nil {
		t.Fatalf("failed to create payment for lease test: %v", err)
	}

	return payment
}

func createPendingRefundForClaimTest(
	t *testing.T,
	pool *pgxpool.Pool,
	payment *Payment,
	nextAttemptAt time.Time,
) string {
	t.Helper()

	var refundID string

	providerPaymentID := fmt.Sprintf(
		"pay_test_%s",
		strings.ReplaceAll(payment.ID, "-", ""),
	)

	err := pool.QueryRow(context.Background(), `
		INSERT INTO payment_refunds (
			payment_id,
			provider,
			provider_payment_id,
			idempotency_key,
			amount_paise,
			currency,
			status,
			next_attempt_at
		)
		VALUES ($1, 'razorpay', $2, $3, $4, $5, 'pending', $6)
		RETURNING id::text
	`,
		payment.ID,
		providerPaymentID,
		"refund-"+payment.ID,
		payment.AmountPaise,
		payment.Currency,
		nextAttemptAt,
	).Scan(&refundID)

	if err != nil {
		t.Fatalf("failed to create pending refund: %v", err)
	}

	return refundID
}

func setRefundProcessingLeaseForClaimTest(
	t *testing.T,
	pool *pgxpool.Pool,
	refundID string,
	providerRefundID string,
	leaseExpiresAt time.Time,
) string {
	t.Helper()

	var leaseToken string

	err := pool.QueryRow(context.Background(), `
		UPDATE payment_refunds
		SET
			status = 'processing',
			provider_refund_id = $2,
			processing_lease_token = gen_random_uuid(),
			processing_lease_expires_at = $3,
			updated_at = NOW()
		WHERE id = $1
		RETURNING processing_lease_token::text
	`,
		refundID,
		providerRefundID,
		leaseExpiresAt,
	).Scan(&leaseToken)

	if err != nil {
		t.Fatalf("failed to set refund processing lease: %v", err)
	}

	return leaseToken
}

func TestRepositoryCreatePending(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	expiresAt := time.Now().UTC().Add(15 * time.Minute)
	testBooking := fixture.createPendingTestBooking(t, expiresAt)

	input := InitiateInput{
		BookingID:      testBooking.ID,
		CustomerID:     fixture.customerID,
		IdempotencyKey: "payment-create-001",
	}

	created, err := repo.CreatePending(context.Background(), input)
	if err != nil {
		t.Fatalf("CreatePending() error = %v", err)
	}

	if created.ID == "" {
		t.Fatal("expected payment ID to be populated")
	}

	if created.BookingID != testBooking.ID {
		t.Errorf("expected booking ID %q, got %q",
			testBooking.ID, created.BookingID)
	}

	if created.CustomerID != fixture.customerID {
		t.Errorf("expected customer ID %q, got %q",
			fixture.customerID, created.CustomerID)
	}

	// One room × one night × 250000 paise.
	if created.AmountPaise != 250000 {
		t.Errorf("expected amount 250000, got %d", created.AmountPaise)
	}

	if created.Currency != CurrencyINR {
		t.Errorf("expected currency %q, got %q",
			CurrencyINR, created.Currency)
	}

	if created.Provider != ProviderRazorpay {
		t.Errorf("expected provider %q, got %q",
			ProviderRazorpay, created.Provider)
	}

	if created.Status != StatusCreated {
		t.Errorf("expected status %q, got %q",
			StatusCreated, created.Status)
	}

	if created.FulfillmentStatus != FulfillmentPending {
		t.Errorf("expected fulfillment status %q, got %q",
			FulfillmentPending, created.FulfillmentStatus)
	}

	if created.IdempotencyKey != input.IdempotencyKey {
		t.Errorf("expected idempotency key %q, got %q",
			input.IdempotencyKey, created.IdempotencyKey)
	}

	if created.ProviderOrderID != nil {
		t.Error("expected provider order ID to be nil before Razorpay order creation")
	}
}

func TestRepositoryCreatePendingReturnsExistingPaymentForSameKey(
	t *testing.T,
) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	testBooking := fixture.createPendingTestBooking(
		t,
		time.Now().UTC().Add(15*time.Minute),
	)

	input := InitiateInput{
		BookingID:      testBooking.ID,
		CustomerID:     fixture.customerID,
		IdempotencyKey: "payment-retry-001",
	}

	first, err := repo.CreatePending(context.Background(), input)
	if err != nil {
		t.Fatalf("first CreatePending() error = %v", err)
	}

	second, err := repo.CreatePending(context.Background(), input)
	if err != nil {
		t.Fatalf("second CreatePending() error = %v", err)
	}

	if first.ID != second.ID {
		t.Errorf("expected same payment ID %q, got %q",
			first.ID, second.ID)
	}

	var paymentCount int

	err = pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM payments
		WHERE booking_id = $1
	`, testBooking.ID).Scan(&paymentCount)

	if err != nil {
		t.Fatalf("failed to count payments: %v", err)
	}

	if paymentCount != 1 {
		t.Errorf("expected exactly 1 payment, got %d", paymentCount)
	}
}

func TestRepositoryCreatePendingRejectsDifferentKeyForSameBooking(
	t *testing.T,
) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	testBooking := fixture.createPendingTestBooking(
		t,
		time.Now().UTC().Add(15*time.Minute),
	)

	first, err := repo.CreatePending(context.Background(), InitiateInput{
		BookingID:      testBooking.ID,
		CustomerID:     fixture.customerID,
		IdempotencyKey: "payment-original-key",
	})
	if err != nil {
		t.Fatalf("first CreatePending() error = %v", err)
	}

	_, err = repo.CreatePending(context.Background(), InitiateInput{
		BookingID:      testBooking.ID,
		CustomerID:     fixture.customerID,
		IdempotencyKey: "payment-different-key",
	})

	if !errors.Is(err, ErrPaymentAlreadyExists) {
		t.Fatalf("expected ErrPaymentAlreadyExists, got %v", err)
	}

	var paymentCount int

	err = pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM payments
		WHERE booking_id = $1
	`, testBooking.ID).Scan(&paymentCount)

	if err != nil {
		t.Fatalf("failed to count payments: %v", err)
	}

	if paymentCount != 1 {
		t.Errorf("expected exactly 1 payment, got %d", paymentCount)
	}

	if first.ID == "" {
		t.Fatal("expected original payment to remain available")
	}
}

func TestRepositoryCreatePendingRejectsReusedKeyForDifferentBooking(
	t *testing.T,
) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	expiresAt := time.Now().UTC().Add(15 * time.Minute)

	firstBooking := fixture.createPendingTestBooking(t, expiresAt)
	secondBooking := fixture.createPendingTestBooking(t, expiresAt)

	key := "payment-shared-idempotency-key"

	_, err := repo.CreatePending(context.Background(), InitiateInput{
		BookingID:      firstBooking.ID,
		CustomerID:     fixture.customerID,
		IdempotencyKey: key,
	})
	if err != nil {
		t.Fatalf("first CreatePending() error = %v", err)
	}

	_, err = repo.CreatePending(context.Background(), InitiateInput{
		BookingID:      secondBooking.ID,
		CustomerID:     fixture.customerID,
		IdempotencyKey: key,
	})

	if !errors.Is(err, ErrIdempotencyKeyConflict) {
		t.Fatalf("expected ErrIdempotencyKeyConflict, got %v", err)
	}
}

type createPendingResult struct {
	payment *Payment
	err     error
}

func runConcurrentCreatePending(
	repo *Repository,
	inputs []InitiateInput,
) []createPendingResult {
	start := make(chan struct{})
	results := make(chan createPendingResult, len(inputs))

	var wg sync.WaitGroup

	for _, input := range inputs {
		wg.Add(1)

		go func(input InitiateInput) {
			defer wg.Done()

			<-start

			payment, err := repo.CreatePending(context.Background(), input)

			results <- createPendingResult{
				payment: payment,
				err:     err,
			}
		}(input)
	}

	close(start)

	wg.Wait()
	close(results)

	var outcomes []createPendingResult

	for result := range results {
		outcomes = append(outcomes, result)
	}

	return outcomes
}

func TestRepositoryCreatePendingConcurrentSameKey(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	expiresAt := time.Now().UTC().Add(time.Hour)
	testBooking := fixture.createPendingTestBooking(t, expiresAt)

	input := InitiateInput{
		BookingID:      testBooking.ID,
		CustomerID:     fixture.customerID,
		IdempotencyKey: fmt.Sprintf("concurrent-same-%d", time.Now().UnixNano()),
	}

	results := runConcurrentCreatePending(repo, []InitiateInput{
		input,
		input,
	})

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	for i, result := range results {
		if result.err != nil {
			t.Fatalf("request %d returned error: %v", i, result.err)
		}

		if result.payment == nil {
			t.Fatalf("request %d returned nil payment", i)
		}
	}

	if results[0].payment.ID != results[1].payment.ID {
		t.Fatalf(
			"expected same payment ID, got %s and %s",
			results[0].payment.ID,
			results[1].payment.ID,
		)
	}

	var count int

	err := pool.QueryRow(
		context.Background(),
		`SELECT COUNT(*) FROM payments WHERE booking_id = $1`,
		testBooking.ID,
	).Scan(&count)

	if err != nil {
		t.Fatalf("failed to count payments: %v", err)
	}

	if count != 1 {
		t.Fatalf("expected 1 payment record, got %d", count)
	}
}

func TestRepositoryCreatePendingConcurrentDifferentKeys(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	expiresAt := time.Now().UTC().Add(time.Hour)
	testBooking := fixture.createPendingTestBooking(t, expiresAt)

	results := runConcurrentCreatePending(repo, []InitiateInput{
		{
			BookingID:      testBooking.ID,
			CustomerID:     fixture.customerID,
			IdempotencyKey: fmt.Sprintf("concurrent-key-a-%d", time.Now().UnixNano()),
		},
		{
			BookingID:      testBooking.ID,
			CustomerID:     fixture.customerID,
			IdempotencyKey: fmt.Sprintf("concurrent-key-b-%d", time.Now().UnixNano()),
		},
	})

	var successCount int
	var conflictCount int

	for i, result := range results {
		switch {
		case result.err == nil:
			if result.payment == nil {
				t.Fatalf("request %d returned nil payment", i)
			}
			successCount++

		case errors.Is(result.err, ErrPaymentAlreadyExists):
			conflictCount++

		default:
			t.Fatalf("request %d returned unexpected error: %v", i, result.err)
		}
	}

	if successCount != 1 || conflictCount != 1 {
		t.Fatalf(
			"expected 1 success and 1 conflict, got %d successes and %d conflicts",
			successCount,
			conflictCount,
		)
	}

	var count int

	err := pool.QueryRow(
		context.Background(),
		`SELECT COUNT(*) FROM payments WHERE booking_id = $1`,
		testBooking.ID,
	).Scan(&count)

	if err != nil {
		t.Fatalf("failed to count payments: %v", err)
	}

	if count != 1 {
		t.Fatalf("expected 1 payment record, got %d", count)
	}
}

func TestRepositoryCreatePendingConcurrentSameKeyDifferentBookings(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	expiresAt := time.Now().UTC().Add(time.Hour)

	firstBooking := fixture.createPendingTestBooking(t, expiresAt)
	secondBooking := fixture.createPendingTestBooking(t, expiresAt)

	idempotencyKey := fmt.Sprintf(
		"concurrent-shared-key-%d",
		time.Now().UnixNano(),
	)

	results := runConcurrentCreatePending(repo, []InitiateInput{
		{
			BookingID:      firstBooking.ID,
			CustomerID:     fixture.customerID,
			IdempotencyKey: idempotencyKey,
		},
		{
			BookingID:      secondBooking.ID,
			CustomerID:     fixture.customerID,
			IdempotencyKey: idempotencyKey,
		},
	})

	var successCount int
	var conflictCount int

	for i, result := range results {
		switch {
		case result.err == nil:
			if result.payment == nil {
				t.Fatalf("request %d returned nil payment", i)
			}
			successCount++

		case errors.Is(result.err, ErrIdempotencyKeyConflict):
			conflictCount++

		default:
			t.Fatalf("request %d returned unexpected error: %v", i, result.err)
		}
	}

	if successCount != 1 || conflictCount != 1 {
		t.Fatalf(
			"expected 1 success and 1 idempotency conflict, got %d successes and %d conflicts",
			successCount,
			conflictCount,
		)
	}

	var count int

	err := pool.QueryRow(
		context.Background(),
		`SELECT COUNT(*) FROM payments WHERE customer_id = $1 AND idempotency_key = $2`,
		fixture.customerID,
		idempotencyKey,
	).Scan(&count)

	if err != nil {
		t.Fatalf("failed to count payments: %v", err)
	}

	if count != 1 {
		t.Fatalf("expected 1 payment record for idempotency key, got %d", count)
	}
}

func TestRepositoryCreatePendingRejectsExpiredBooking(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	expiredAt := time.Now().UTC().Add(-time.Minute)
	testBooking := fixture.createPendingTestBooking(t, expiredAt)

	_, err := repo.CreatePending(
		context.Background(),
		InitiateInput{
			BookingID:      testBooking.ID,
			CustomerID:     fixture.customerID,
			IdempotencyKey: fmt.Sprintf("expired-%d", time.Now().UnixNano()),
		},
	)

	if !errors.Is(err, ErrBookingNotPayable) {
		t.Fatalf("expected ErrBookingNotPayable, got %v", err)
	}
}

func TestRepositoryCreatePendingRejectsCancelledBooking(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	expiresAt := time.Now().UTC().Add(time.Hour)
	testBooking := fixture.createPendingTestBooking(t, expiresAt)

	_, err := pool.Exec(
		context.Background(),
		`UPDATE bookings
		 SET status = $1, cancelled_at = NOW()
		 WHERE id = $2`,
		booking.StatusCancelled,
		testBooking.ID,
	)
	if err != nil {
		t.Fatalf("failed to cancel booking: %v", err)
	}

	_, err = repo.CreatePending(
		context.Background(),
		InitiateInput{
			BookingID:      testBooking.ID,
			CustomerID:     fixture.customerID,
			IdempotencyKey: fmt.Sprintf("cancelled-%d", time.Now().UnixNano()),
		},
	)

	if !errors.Is(err, ErrBookingNotPayable) {
		t.Fatalf("expected ErrBookingNotPayable, got %v", err)
	}
}

func TestRepositoryCreatePendingRejectsWrongCustomer(t *testing.T) {
	repo, pool := setupPaymentRepository(t)

	ownerFixture := createPaymentTestFixture(t, pool)
	otherFixture := createPaymentTestFixture(t, pool)

	expiresAt := time.Now().UTC().Add(time.Hour)
	testBooking := ownerFixture.createPendingTestBooking(t, expiresAt)

	_, err := repo.CreatePending(
		context.Background(),
		InitiateInput{
			BookingID:      testBooking.ID,
			CustomerID:     otherFixture.customerID,
			IdempotencyKey: fmt.Sprintf("wrong-owner-%d", time.Now().UnixNano()),
		},
	)

	if !errors.Is(err, ErrBookingNotFound) {
		t.Fatalf("expected ErrBookingNotFound, got %v", err)
	}
}

func TestRepositoryCreatePendingRejectsInvalidInput(t *testing.T) {
	repo, _ := setupPaymentRepository(t)

	tests := []struct {
		name    string
		input   InitiateInput
		wantErr error
	}{
		{
			name: "empty booking ID",
			input: InitiateInput{
				CustomerID:     "customer-id",
				IdempotencyKey: "valid-key",
			},
			wantErr: ErrInvalidBookingID,
		},
		{
			name: "empty customer ID",
			input: InitiateInput{
				BookingID:      "booking-id",
				IdempotencyKey: "valid-key",
			},
			wantErr: ErrInvalidCustomerID,
		},
		{
			name: "empty idempotency key",
			input: InitiateInput{
				BookingID:  "booking-id",
				CustomerID: "customer-id",
			},
			wantErr: ErrInvalidIdempotencyKey,
		},
		{
			name: "idempotency key exceeds 128 characters",
			input: InitiateInput{
				BookingID:      "booking-id",
				CustomerID:     "customer-id",
				IdempotencyKey: strings.Repeat("a", 129),
			},
			wantErr: ErrInvalidIdempotencyKey,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := repo.CreatePending(context.Background(), tt.input)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestRepositoryCreatePendingRejectsWhitespaceInput(t *testing.T) {
	repo, _ := setupPaymentRepository(t)

	tests := []struct {
		name    string
		input   InitiateInput
		wantErr error
	}{
		{
			name: "whitespace booking ID",
			input: InitiateInput{
				BookingID:      "   ",
				CustomerID:     "customer-id",
				IdempotencyKey: "valid-key",
			},
			wantErr: ErrInvalidBookingID,
		},
		{
			name: "whitespace customer ID",
			input: InitiateInput{
				BookingID:      "booking-id",
				CustomerID:     "   ",
				IdempotencyKey: "valid-key",
			},
			wantErr: ErrInvalidCustomerID,
		},
		{
			name: "whitespace idempotency key",
			input: InitiateInput{
				BookingID:      "booking-id",
				CustomerID:     "customer-id",
				IdempotencyKey: "   ",
			},
			wantErr: ErrInvalidIdempotencyKey,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := repo.CreatePending(context.Background(), tt.input)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestRepositoryAcquireAndReleaseProviderOrderLease(
	t *testing.T,
) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)
	payment := createPaymentForLeaseTest(t, repo, fixture)

	ctx := context.Background()

	token, err := repo.AcquireProviderOrderLease(
		ctx,
		payment.ID,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("AcquireProviderOrderLease() error = %v", err)
	}

	if token == "" {
		t.Fatal("expected a non-empty lease token")
	}

	_, err = repo.AcquireProviderOrderLease(
		ctx,
		payment.ID,
		time.Minute,
	)
	if !errors.Is(err, ErrOrderCreationInProgress) {
		t.Fatalf(
			"expected ErrOrderCreationInProgress, got %v",
			err,
		)
	}

	if err := repo.ReleaseProviderOrderLease(
		ctx,
		payment.ID,
		token,
	); err != nil {
		t.Fatalf("ReleaseProviderOrderLease() error = %v", err)
	}

	newToken, err := repo.AcquireProviderOrderLease(
		ctx,
		payment.ID,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("expected lease acquisition after release: %v", err)
	}

	if newToken == token {
		t.Fatal("expected a new lease token")
	}
}

func TestRepositoryAcquireProviderOrderLeaseConcurrent(
	t *testing.T,
) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)
	payment := createPaymentForLeaseTest(t, repo, fixture)

	const workers = 10

	start := make(chan struct{})

	type result struct {
		token string
		err   error
	}

	results := make(chan result, workers)

	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			<-start

			token, err := repo.AcquireProviderOrderLease(
				context.Background(),
				payment.ID,
				time.Minute,
			)

			results <- result{
				token: token,
				err:   err,
			}
		}()
	}

	close(start)

	wg.Wait()
	close(results)

	var successCount int
	var conflictCount int

	for result := range results {
		switch {
		case result.err == nil:
			if result.token == "" {
				t.Fatal("successful acquisition returned empty token")
			}
			successCount++

		case errors.Is(result.err, ErrOrderCreationInProgress):
			conflictCount++

		default:
			t.Fatalf("unexpected acquisition error: %v", result.err)
		}
	}

	if successCount != 1 || conflictCount != workers-1 {
		t.Fatalf(
			"expected 1 success and %d conflicts, got %d successes and %d conflicts",
			workers-1,
			successCount,
			conflictCount,
		)
	}
}

func TestRepositoryAcquireProviderOrderLeaseAfterExpiry(
	t *testing.T,
) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)
	payment := createPaymentForLeaseTest(t, repo, fixture)

	ctx := context.Background()

	oldToken, err := repo.AcquireProviderOrderLease(
		ctx,
		payment.ID,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("first acquisition failed: %v", err)
	}

	_, err = pool.Exec(ctx, `
        UPDATE payments
        SET provider_order_lease_expires_at = NOW() - INTERVAL '1 second'
        WHERE id = $1
    `, payment.ID)

	if err != nil {
		t.Fatalf("failed to expire lease: %v", err)
	}

	newToken, err := repo.AcquireProviderOrderLease(
		ctx,
		payment.ID,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("expected expired lease to be acquired: %v", err)
	}

	if newToken == oldToken {
		t.Fatal("expected a different token after lease expiration")
	}
}

func TestRepositorySaveProviderOrderID(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)
	payment := createPaymentForLeaseTest(t, repo, fixture)

	ctx := context.Background()

	token, err := repo.AcquireProviderOrderLease(
		ctx,
		payment.ID,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("lease acquisition failed: %v", err)
	}

	providerOrderID := "order_test_123"

	err = repo.SaveProviderOrderID(
		ctx,
		payment.ID,
		token,
		providerOrderID,
	)
	if err != nil {
		t.Fatalf("SaveProviderOrderID() error = %v", err)
	}

	saved, err := repo.GetByIDForCustomer(
		ctx,
		payment.ID,
		fixture.customerID,
	)
	if err != nil {
		t.Fatalf("GetByIDForCustomer() error = %v", err)
	}

	if saved.ProviderOrderID == nil {
		t.Fatal("expected provider order ID to be saved")
	}

	if *saved.ProviderOrderID != providerOrderID {
		t.Fatalf(
			"expected provider order ID %q, got %q",
			providerOrderID,
			*saved.ProviderOrderID,
		)
	}

	_, err = repo.AcquireProviderOrderLease(
		ctx,
		payment.ID,
		time.Minute,
	)

	if !errors.Is(err, ErrProviderOrderAlreadyExists) {
		t.Fatalf(
			"expected ErrProviderOrderAlreadyExists, got %v",
			err,
		)
	}
}

func TestRepositorySaveProviderOrderIDRejectsStaleToken(
	t *testing.T,
) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)
	payment := createPaymentForLeaseTest(t, repo, fixture)

	ctx := context.Background()

	oldToken, err := repo.AcquireProviderOrderLease(
		ctx,
		payment.ID,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("first lease acquisition failed: %v", err)
	}

	_, err = pool.Exec(ctx, `
        UPDATE payments
        SET provider_order_lease_expires_at = NOW() - INTERVAL '1 second'
        WHERE id = $1
    `, payment.ID)

	if err != nil {
		t.Fatalf("failed to expire lease: %v", err)
	}

	newToken, err := repo.AcquireProviderOrderLease(
		ctx,
		payment.ID,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("second lease acquisition failed: %v", err)
	}

	err = repo.SaveProviderOrderID(
		ctx,
		payment.ID,
		oldToken,
		"order_stale_123",
	)

	if !errors.Is(err, ErrOrderCreationLeaseLost) {
		t.Fatalf(
			"expected ErrOrderCreationLeaseLost, got %v",
			err,
		)
	}

	err = repo.SaveProviderOrderID(
		ctx,
		payment.ID,
		newToken,
		"order_current_123",
	)

	if err != nil {
		t.Fatalf("current lease owner should save order ID: %v", err)
	}
}

func TestRepositoryReleaseProviderOrderLeaseRejectsWrongToken(
	t *testing.T,
) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)
	payment := createPaymentForLeaseTest(t, repo, fixture)

	ctx := context.Background()

	token, err := repo.AcquireProviderOrderLease(
		ctx,
		payment.ID,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("lease acquisition failed: %v", err)
	}

	err = repo.ReleaseProviderOrderLease(
		ctx,
		payment.ID,
		"00000000-0000-0000-0000-000000000000",
	)
	if err != nil {
		t.Fatalf("wrong-token release should be harmless: %v", err)
	}

	_, err = repo.AcquireProviderOrderLease(
		ctx,
		payment.ID,
		time.Minute,
	)

	if !errors.Is(err, ErrOrderCreationInProgress) {
		t.Fatalf(
			"expected active lease to remain, got %v",
			err,
		)
	}

	if err := repo.ReleaseProviderOrderLease(
		ctx,
		payment.ID,
		token,
	); err != nil {
		t.Fatalf("correct-token release failed: %v", err)
	}
}

func TestRepositoryGetByIDForCustomerOwnership(t *testing.T) {
	repo, pool := setupPaymentRepository(t)

	ownerFixture := createPaymentTestFixture(t, pool)
	otherFixture := createPaymentTestFixture(t, pool)

	payment := createPaymentForLeaseTest(
		t,
		repo,
		ownerFixture,
	)

	ctx := context.Background()

	_, err := repo.GetByIDForCustomer(
		ctx,
		payment.ID,
		otherFixture.customerID,
	)

	if !errors.Is(err, ErrPaymentOwnershipMismatch) {
		t.Fatalf(
			"expected ErrPaymentOwnershipMismatch, got %v",
			err,
		)
	}

	retrieved, err := repo.GetByIDForCustomer(
		ctx,
		payment.ID,
		ownerFixture.customerID,
	)

	if err != nil {
		t.Fatalf("owner should retrieve payment: %v", err)
	}

	if retrieved.ID != payment.ID {
		t.Fatalf("expected payment ID %q, got %q", payment.ID, retrieved.ID)
	}
}

func createPaymentWithProviderOrderForTest(
	t *testing.T,
	repo *Repository,
	fixture *paymentTestFixture,
) (*booking.Booking, *Payment) {
	t.Helper()

	ctx := context.Background()

	testBooking := fixture.createPendingTestBooking(
		t,
		time.Now().UTC().Add(time.Hour),
	)

	createdPayment, err := repo.CreatePending(ctx, InitiateInput{
		BookingID:      testBooking.ID,
		CustomerID:     fixture.customerID,
		IdempotencyKey: fmt.Sprintf("fulfillment-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("failed to create payment: %v", err)
	}

	orderID := fmt.Sprintf("order_test_%d", time.Now().UnixNano())

	_, err = fixture.pool.Exec(ctx, `
        UPDATE payments
        SET provider_order_id = $1
        WHERE id = $2
    `, orderID, createdPayment.ID)
	if err != nil {
		t.Fatalf("failed to assign provider order ID: %v", err)
	}

	createdPayment.ProviderOrderID = &orderID

	return testBooking, createdPayment
}

func TestRepositoryFulfillCapturedPayment(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	testBooking, testPayment := createPaymentWithProviderOrderForTest(
		t,
		repo,
		fixture,
	)

	input := CapturedPaymentInput{
		PaymentID:         testPayment.ID,
		CustomerID:        fixture.customerID,
		ProviderOrderID:   *testPayment.ProviderOrderID,
		ProviderPaymentID: "pay_fulfillment_success",
		AmountPaise:       testPayment.AmountPaise,
		Currency:          testPayment.Currency,
	}

	result, err := repo.FulfillCapturedPayment(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatalf("FulfillCapturedPayment() error = %v", err)
	}

	if result.Status != StatusCaptured {
		t.Errorf("expected payment status %q, got %q",
			StatusCaptured, result.Status)
	}

	if result.FulfillmentStatus != FulfillmentFulfilled {
		t.Errorf("expected fulfillment status %q, got %q",
			FulfillmentFulfilled, result.FulfillmentStatus)
	}

	var bookingStatus string

	err = pool.QueryRow(context.Background(), `
        SELECT status FROM bookings WHERE id = $1
    `, testBooking.ID).Scan(&bookingStatus)
	if err != nil {
		t.Fatalf("failed to retrieve booking status: %v", err)
	}

	if bookingStatus != booking.StatusConfirmed {
		t.Errorf("expected booking status %q, got %q",
			booking.StatusConfirmed, bookingStatus)
	}

	// Booking creation already reserved one room.
	// Payment confirmation must not reserve it again.
	var availableRooms int

	err = pool.QueryRow(context.Background(), `
        SELECT available_rooms
        FROM room_inventory
        WHERE room_type_id = $1
    `, fixture.roomTypeID).Scan(&availableRooms)
	if err != nil {
		t.Fatalf("failed to retrieve inventory: %v", err)
	}

	if availableRooms != 4 {
		t.Errorf("expected 4 available rooms, got %d", availableRooms)
	}

	// Repeating the same verification must not create another attempt.
	_, err = repo.FulfillCapturedPayment(context.Background(), input)
	if err != nil {
		t.Fatalf("repeated fulfillment error = %v", err)
	}

	var attemptCount int

	err = pool.QueryRow(context.Background(), `
        SELECT COUNT(*)
        FROM payment_attempts
        WHERE payment_id = $1
    `, testPayment.ID).Scan(&attemptCount)
	if err != nil {
		t.Fatalf("failed to count payment attempts: %v", err)
	}

	if attemptCount != 1 {
		t.Errorf("expected 1 payment attempt, got %d", attemptCount)
	}
}

func TestRepositoryFulfillCapturedPaymentAfterCancellation(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	testBooking, testPayment := createPaymentWithProviderOrderForTest(
		t,
		repo,
		fixture,
	)

	bookingRepo := booking.NewRepository(pool)

	_, err := bookingRepo.Cancel(
		context.Background(),
		fixture.customerID,
		testBooking.ID,
	)
	if err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}

	input := CapturedPaymentInput{
		PaymentID:         testPayment.ID,
		CustomerID:        fixture.customerID,
		ProviderOrderID:   *testPayment.ProviderOrderID,
		ProviderPaymentID: "pay_after_cancellation",
		AmountPaise:       testPayment.AmountPaise,
		Currency:          testPayment.Currency,
	}

	result, err := repo.FulfillCapturedPayment(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatalf("FulfillCapturedPayment() error = %v", err)
	}

	if result.FulfillmentStatus != FulfillmentRefundRequired {
		t.Errorf("expected fulfillment status %q, got %q",
			FulfillmentRefundRequired, result.FulfillmentStatus)
	}

	var bookingStatus string

	err = pool.QueryRow(context.Background(), `
        SELECT status FROM bookings WHERE id = $1
    `, testBooking.ID).Scan(&bookingStatus)
	if err != nil {
		t.Fatalf("failed to retrieve booking status: %v", err)
	}

	if bookingStatus != booking.StatusCancelled {
		t.Errorf("expected cancelled booking, got %q", bookingStatus)
	}

	var refundStatus string

	err = pool.QueryRow(context.Background(), `
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

	var availableRooms int

	err = pool.QueryRow(context.Background(), `
        SELECT available_rooms
        FROM room_inventory
        WHERE room_type_id = $1
    `, fixture.roomTypeID).Scan(&availableRooms)
	if err != nil {
		t.Fatalf("failed to retrieve inventory: %v", err)
	}

	if availableRooms != 5 {
		t.Errorf("expected 5 available rooms after cancellation, got %d",
			availableRooms)
	}
}

func TestRepositoryFulfillCapturedPaymentAfterExpiry(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	ctx := context.Background()
	now := time.Now().UTC()

	// Initially create a payable booking.
	testBooking := fixture.createPendingTestBooking(
		t,
		now.Add(time.Hour),
	)

	testPayment, err := repo.CreatePending(ctx, InitiateInput{
		BookingID:      testBooking.ID,
		CustomerID:     fixture.customerID,
		IdempotencyKey: fmt.Sprintf("expiry-payment-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("failed to create payment: %v", err)
	}

	// Simulate the payment deadline passing.
	_, err = pool.Exec(ctx, `
    UPDATE bookings
    SET payment_expires_at = $1
    WHERE id = $2
`, now.Add(-time.Minute), testBooking.ID)

	if err != nil {
		t.Fatalf("failed to expire payment deadline: %v", err)
	}

	orderID := fmt.Sprintf("order_expiry_%d", time.Now().UnixNano())

	_, err = pool.Exec(ctx, `
    UPDATE payments
    SET provider_order_id = $1
    WHERE id = $2
`, orderID, testPayment.ID)

	if err != nil {
		t.Fatalf("failed to assign provider order ID: %v", err)
	}

	// Run the real booking expiry worker.
	bookingRepo := booking.NewRepository(pool)

	expiredCount, err := bookingRepo.ExpirePendingBookings(ctx, now, 10)
	if err != nil {
		t.Fatalf("ExpirePendingBookings() error = %v", err)
	}

	if expiredCount != 1 {
		t.Fatalf("expected 1 expired booking, got %d", expiredCount)
	}

	// Razorpay reports that payment was captured after expiry.
	result, err := repo.FulfillCapturedPayment(ctx, CapturedPaymentInput{
		PaymentID:         testPayment.ID,
		CustomerID:        fixture.customerID,
		ProviderOrderID:   orderID,
		ProviderPaymentID: "pay_after_expiry",
		AmountPaise:       testPayment.AmountPaise,
		Currency:          testPayment.Currency,
	})
	if err != nil {
		t.Fatalf("FulfillCapturedPayment() error = %v", err)
	}

	if result.Status != StatusCaptured {
		t.Errorf("expected payment status %q, got %q",
			StatusCaptured, result.Status)
	}

	if result.FulfillmentStatus != FulfillmentRefundRequired {
		t.Errorf("expected fulfillment status %q, got %q",
			FulfillmentRefundRequired, result.FulfillmentStatus)
	}

	var bookingStatus string

	err = pool.QueryRow(ctx, `
        SELECT status FROM bookings WHERE id = $1
    `, testBooking.ID).Scan(&bookingStatus)
	if err != nil {
		t.Fatalf("failed to retrieve booking status: %v", err)
	}

	if bookingStatus != booking.StatusExpired {
		t.Errorf("expected booking status %q, got %q",
			booking.StatusExpired, bookingStatus)
	}

	var availableRooms int

	err = pool.QueryRow(ctx, `
        SELECT available_rooms
        FROM room_inventory
        WHERE room_type_id = $1
    `, fixture.roomTypeID).Scan(&availableRooms)
	if err != nil {
		t.Fatalf("failed to retrieve inventory: %v", err)
	}

	if availableRooms != 5 {
		t.Errorf("expected 5 available rooms after expiry, got %d",
			availableRooms)
	}

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
}

func TestRepositoryFulfillCapturedPaymentAfterDeadlineBeforeExpiryWorker(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	ctx := context.Background()
	now := time.Now().UTC()

	// Create a booking whose payment deadline is still in the future.
	testBooking := fixture.createPendingTestBooking(
		t,
		now.Add(time.Hour),
	)

	// Create the payment while the booking is still payable.
	testPayment, err := repo.CreatePending(ctx, InitiateInput{
		BookingID:      testBooking.ID,
		CustomerID:     fixture.customerID,
		IdempotencyKey: fmt.Sprintf("late-payment-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("failed to create payment: %v", err)
	}

	orderID := fmt.Sprintf("order_late_%d", time.Now().UnixNano())

	_, err = pool.Exec(ctx, `
        UPDATE payments
        SET provider_order_id = $1
        WHERE id = $2
    `, orderID, testPayment.ID)
	if err != nil {
		t.Fatalf("failed to assign provider order ID: %v", err)
	}

	// Simulate the deadline passing.
	_, err = pool.Exec(ctx, `
        UPDATE bookings
        SET payment_expires_at = $1
        WHERE id = $2
    `, now.Add(-time.Minute), testBooking.ID)
	if err != nil {
		t.Fatalf("failed to expire payment deadline: %v", err)
	}

	// Deliberately DO NOT run ExpirePendingBookings().
	// Razorpay reports that the payment was captured.
	result, err := repo.FulfillCapturedPayment(ctx, CapturedPaymentInput{
		PaymentID:         testPayment.ID,
		CustomerID:        fixture.customerID,
		ProviderOrderID:   orderID,
		ProviderPaymentID: "pay_late_before_worker",
		AmountPaise:       testPayment.AmountPaise,
		Currency:          testPayment.Currency,
	})
	if err != nil {
		t.Fatalf("failed to fulfill captured payment: %v", err)
	}

	if result.Status != StatusCaptured {
		t.Fatalf("expected payment status %q, got %q", StatusCaptured, result.Status)
	}

	if result.FulfillmentStatus != FulfillmentRefundRequired {
		t.Fatalf(
			"expected fulfillment status %q, got %q",
			FulfillmentRefundRequired,
			result.FulfillmentStatus,
		)
	}

	// The expiry worker hasn't run, so the booking is still pending.
	var bookingStatus string
	err = pool.QueryRow(ctx, `
        SELECT status FROM bookings WHERE id = $1
    `, testBooking.ID).Scan(&bookingStatus)
	if err != nil {
		t.Fatalf("failed to fetch booking status: %v", err)
	}

	if bookingStatus != BookingStatusPendingPayment {
		t.Fatalf("expected booking to remain pending_payment, got %q", bookingStatus)
	}

	// Inventory is still reserved until the expiry worker restores it.
	var availableRooms int
	err = pool.QueryRow(ctx, `
        SELECT available_rooms
        FROM room_inventory
        WHERE room_type_id = $1 AND stay_date = $2
    `, testBooking.RoomTypeID, testBooking.CheckIn).Scan(&availableRooms)
	if err != nil {
		t.Fatalf("failed to fetch inventory: %v", err)
	}

	if availableRooms != 4 {
		t.Fatalf("expected 4 available rooms before expiry worker, got %d", availableRooms)
	}

	// A refund requirement must have been recorded.
	var refundStatus string
	err = pool.QueryRow(ctx, `
        SELECT status
        FROM payment_refunds
        WHERE payment_id = $1
    `, testPayment.ID).Scan(&refundStatus)
	if err != nil {
		t.Fatalf("failed to fetch refund: %v", err)
	}

	if refundStatus != RefundStatusPending {
		t.Fatalf("expected refund status %q, got %q", RefundStatusPending, refundStatus)
	}
}

func TestRepositoryFulfillCapturedPaymentConcurrently(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	ctx := context.Background()
	now := time.Now().UTC()

	testBooking := fixture.createPendingTestBooking(
		t,
		now.Add(time.Hour),
	)

	testPayment, err := repo.CreatePending(ctx, InitiateInput{
		BookingID:      testBooking.ID,
		CustomerID:     fixture.customerID,
		IdempotencyKey: fmt.Sprintf("concurrent-payment-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("failed to create payment: %v", err)
	}

	orderID := fmt.Sprintf("order_concurrent_%d", time.Now().UnixNano())

	_, err = pool.Exec(ctx, `
        UPDATE payments
        SET provider_order_id = $1
        WHERE id = $2
    `, orderID, testPayment.ID)
	if err != nil {
		t.Fatalf("failed to assign provider order ID: %v", err)
	}

	input := CapturedPaymentInput{
		PaymentID:         testPayment.ID,
		CustomerID:        fixture.customerID,
		ProviderOrderID:   orderID,
		ProviderPaymentID: "pay_concurrent_test",
		AmountPaise:       testPayment.AmountPaise,
		Currency:          testPayment.Currency,
	}

	// Both requests wait until they can start together.
	start := make(chan struct{})
	results := make(chan error, 2)

	for i := 0; i < 2; i++ {
		go func() {
			<-start

			_, err := repo.FulfillCapturedPayment(ctx, input)
			results <- err
		}()
	}

	close(start)

	// Both requests must complete successfully.
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatalf("concurrent fulfillment failed: %v", err)
		}
	}

	// Exactly one payment attempt must exist.
	var attemptCount int
	err = pool.QueryRow(ctx, `
        SELECT COUNT(*)
        FROM payment_attempts
        WHERE payment_id = $1
    `, testPayment.ID).Scan(&attemptCount)
	if err != nil {
		t.Fatalf("failed to count payment attempts: %v", err)
	}

	if attemptCount != 1 {
		t.Fatalf("expected 1 payment attempt, got %d", attemptCount)
	}

	// Booking must be confirmed.
	var bookingStatus string
	err = pool.QueryRow(ctx, `
        SELECT status
        FROM bookings
        WHERE id = $1
    `, testBooking.ID).Scan(&bookingStatus)
	if err != nil {
		t.Fatalf("failed to fetch booking status: %v", err)
	}

	if bookingStatus != "confirmed" {
		t.Fatalf("expected booking status confirmed, got %q", bookingStatus)
	}

	// Inventory must not be decremented twice.
	var availableRooms int
	err = pool.QueryRow(ctx, `
        SELECT available_rooms
        FROM room_inventory
        WHERE room_type_id = $1 AND stay_date = $2
    `, testBooking.RoomTypeID, testBooking.CheckIn).Scan(&availableRooms)
	if err != nil {
		t.Fatalf("failed to fetch inventory: %v", err)
	}

	if availableRooms != 4 {
		t.Fatalf("expected 4 available rooms, got %d", availableRooms)
	}

	// No refund should be created for a successful fulfillment.
	var refundCount int
	err = pool.QueryRow(ctx, `
        SELECT COUNT(*)
        FROM payment_refunds
        WHERE payment_id = $1
    `, testPayment.ID).Scan(&refundCount)
	if err != nil {
		t.Fatalf("failed to count refunds: %v", err)
	}

	if refundCount != 0 {
		t.Fatalf("expected no refunds, got %d", refundCount)
	}
}

func TestRepositoryClaimPendingRefunds(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	now := time.Now().UTC()

	payment1 := createPaymentForLeaseTest(t, repo, fixture)
	payment2 := createPaymentForLeaseTest(t, repo, fixture)
	payment3 := createPaymentForLeaseTest(t, repo, fixture)

	refund1 := createPendingRefundForClaimTest(
		t, pool, payment1, now.Add(-time.Minute),
	)
	refund2 := createPendingRefundForClaimTest(
		t, pool, payment2, now.Add(-time.Minute),
	)
	futureRefund := createPendingRefundForClaimTest(
		t, pool, payment3, now.Add(time.Hour),
	)

	claimed, err := repo.ClaimPendingRefunds(
		context.Background(),
		10,
		5*time.Minute,
	)
	if err != nil {
		t.Fatalf("ClaimPendingRefunds() error = %v", err)
	}

	if len(claimed) != 2 {
		t.Fatalf("expected 2 claimed refunds, got %d", len(claimed))
	}

	claimedIDs := make(map[string]bool)

	for _, refund := range claimed {
		claimedIDs[refund.ID] = true

		if refund.Status != "processing" {
			t.Errorf(
				"expected status processing, got %q",
				refund.Status,
			)
		}

		if refund.AttemptCount != 1 {
			t.Errorf(
				"expected attempt count 1, got %d",
				refund.AttemptCount,
			)
		}

		if refund.ProcessingLeaseToken == nil ||
			*refund.ProcessingLeaseToken == "" {
			t.Error("expected processing lease token")
		}

		if refund.ProcessingLeaseExpiresAt == nil {
			t.Error("expected processing lease expiration")
		} else if !refund.ProcessingLeaseExpiresAt.After(time.Now()) {
			t.Error("expected lease expiration to be in the future")
		}
	}

	if !claimedIDs[refund1] {
		t.Errorf("expected refund %s to be claimed", refund1)
	}

	if !claimedIDs[refund2] {
		t.Errorf("expected refund %s to be claimed", refund2)
	}

	if claimedIDs[futureRefund] {
		t.Errorf("future refund %s should not have been claimed", futureRefund)
	}

	var futureStatus string

	err = pool.QueryRow(
		context.Background(),
		`SELECT status FROM payment_refunds WHERE id = $1`,
		futureRefund,
	).Scan(&futureStatus)

	if err != nil {
		t.Fatalf("failed to read future refund status: %v", err)
	}

	if futureStatus != "pending" {
		t.Errorf(
			"expected future refund to remain pending, got %q",
			futureStatus,
		)
	}
}

func TestRepositoryClaimPendingRefundsConcurrent(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	const workerCount = 5

	for i := 0; i < workerCount; i++ {
		payment := createPaymentForLeaseTest(t, repo, fixture)

		createPendingRefundForClaimTest(
			t,
			pool,
			payment,
			time.Now().UTC().Add(-time.Minute),
		)
	}

	claimedCh := make(chan Refund, workerCount)
	errCh := make(chan error, workerCount)

	var wg sync.WaitGroup

	for i := 0; i < workerCount; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			refunds, err := repo.ClaimPendingRefunds(
				context.Background(),
				1,
				5*time.Minute,
			)
			if err != nil {
				errCh <- err
				return
			}

			for _, refund := range refunds {
				claimedCh <- refund
			}
		}()
	}

	wg.Wait()
	close(claimedCh)
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent ClaimPendingRefunds() error: %v", err)
	}

	claimedIDs := make(map[string]bool)

	for refund := range claimedCh {
		if claimedIDs[refund.ID] {
			t.Errorf("refund %s was claimed more than once", refund.ID)
		}

		claimedIDs[refund.ID] = true
	}

	if len(claimedIDs) != workerCount {
		t.Errorf(
			"expected %d uniquely claimed refunds, got %d",
			workerCount,
			len(claimedIDs),
		)
	}
}

func TestRepositoryClaimPendingRefundsValidation(t *testing.T) {
	repo := &Repository{}
	ctx := context.Background()

	_, err := repo.ClaimPendingRefunds(
		ctx,
		0,
		time.Minute,
	)
	if !errors.Is(err, ErrInvalidRefundClaimBatchSize) {
		t.Errorf(
			"expected ErrInvalidRefundClaimBatchSize, got %v",
			err,
		)
	}

	_, err = repo.ClaimPendingRefunds(
		ctx,
		1,
		0,
	)
	if !errors.Is(err, ErrInvalidRefundLeaseDuration) {
		t.Errorf(
			"expected ErrInvalidRefundLeaseDuration, got %v",
			err,
		)
	}
}

func TestRepositoryClaimExpiredRefunds(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	now := time.Now().UTC()

	// Create an expired processing refund.
	expiredPayment := createPaymentForLeaseTest(t, repo, fixture)
	expiredRefundID := createPendingRefundForClaimTest(
		t, pool, expiredPayment, now.Add(-time.Minute),
	)

	providerRefundID := "rfnd_test_" +
		strings.ReplaceAll(expiredPayment.ID, "-", "")

	originalToken := setRefundProcessingLeaseForClaimTest(
		t,
		pool,
		expiredRefundID,
		providerRefundID,
		now.Add(-time.Minute),
	)

	// Create an active processing refund.
	activePayment := createPaymentForLeaseTest(t, repo, fixture)
	activeRefundID := createPendingRefundForClaimTest(
		t, pool, activePayment, now.Add(-time.Minute),
	)

	activeProviderRefundID := "rfnd_active_" +
		strings.ReplaceAll(activePayment.ID, "-", "")

	activeToken := setRefundProcessingLeaseForClaimTest(
		t,
		pool,
		activeRefundID,
		activeProviderRefundID,
		now.Add(time.Hour),
	)

	// Create an eligible pending refund. It must not be claimed here.
	pendingPayment := createPaymentForLeaseTest(t, repo, fixture)
	pendingRefundID := createPendingRefundForClaimTest(
		t, pool, pendingPayment, now.Add(-time.Minute),
	)

	claimed, err := repo.ClaimExpiredRefunds(
		context.Background(),
		10,
		5*time.Minute,
	)
	if err != nil {
		t.Fatalf("ClaimExpiredRefunds() error = %v", err)
	}

	if len(claimed) != 1 {
		t.Fatalf("expected 1 reclaimed refund, got %d", len(claimed))
	}

	refund := claimed[0]

	if refund.ID != expiredRefundID {
		t.Errorf(
			"expected refund %s, got %s",
			expiredRefundID,
			refund.ID,
		)
	}

	if refund.Status != "processing" {
		t.Errorf("expected processing status, got %q", refund.Status)
	}

	if refund.AttemptCount != 1 {
		t.Errorf("expected attempt count 1, got %d", refund.AttemptCount)
	}

	if refund.ProcessingLeaseToken == nil {
		t.Fatal("expected new processing lease token")
	}

	if *refund.ProcessingLeaseToken == originalToken {
		t.Error("expected a new lease token after reclaiming")
	}

	if refund.ProcessingLeaseExpiresAt == nil {
		t.Fatal("expected processing lease expiration")
	}

	if !refund.ProcessingLeaseExpiresAt.After(time.Now()) {
		t.Error("expected new lease expiration to be in the future")
	}

	if refund.ProviderRefundID == nil ||
		*refund.ProviderRefundID != providerRefundID {
		t.Errorf(
			"expected provider refund ID %q, got %v",
			providerRefundID,
			refund.ProviderRefundID,
		)
	}

	if refund.IdempotencyKey != "refund-"+expiredPayment.ID {
		t.Errorf(
			"original idempotency key changed: %q",
			refund.IdempotencyKey,
		)
	}

	// Verify the active lease was not reclaimed.
	var storedActiveToken string
	var activeAttemptCount int

	err = pool.QueryRow(context.Background(), `
		SELECT
			processing_lease_token::text,
			attempt_count
		FROM payment_refunds
		WHERE id = $1
	`, activeRefundID).Scan(
		&storedActiveToken,
		&activeAttemptCount,
	)
	if err != nil {
		t.Fatalf("failed to read active refund: %v", err)
	}

	if storedActiveToken != activeToken {
		t.Error("active refund lease token should not change")
	}

	if activeAttemptCount != 0 {
		t.Errorf(
			"expected active refund attempt count 0, got %d",
			activeAttemptCount,
		)
	}

	// Verify the pending refund was not reclaimed.
	var pendingStatus string

	err = pool.QueryRow(context.Background(), `
		SELECT status
		FROM payment_refunds
		WHERE id = $1
	`, pendingRefundID).Scan(&pendingStatus)
	if err != nil {
		t.Fatalf("failed to read pending refund: %v", err)
	}

	if pendingStatus != "pending" {
		t.Errorf(
			"expected pending refund to remain pending, got %q",
			pendingStatus,
		)
	}
}

func TestRepositoryClaimExpiredRefundsConcurrent(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	const workerCount = 5

	for i := 0; i < workerCount; i++ {
		payment := createPaymentForLeaseTest(t, repo, fixture)

		refundID := createPendingRefundForClaimTest(
			t,
			pool,
			payment,
			time.Now().UTC().Add(-time.Minute),
		)

		providerRefundID := "rfnd_concurrent_" +
			strings.ReplaceAll(payment.ID, "-", "")

		setRefundProcessingLeaseForClaimTest(
			t,
			pool,
			refundID,
			providerRefundID,
			time.Now().UTC().Add(-time.Minute),
		)
	}

	claimedCh := make(chan Refund, workerCount)
	errCh := make(chan error, workerCount)

	var wg sync.WaitGroup

	for i := 0; i < workerCount; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			refunds, err := repo.ClaimExpiredRefunds(
				context.Background(),
				1,
				5*time.Minute,
			)
			if err != nil {
				errCh <- err
				return
			}

			for _, refund := range refunds {
				claimedCh <- refund
			}
		}()
	}

	wg.Wait()
	close(claimedCh)
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent ClaimExpiredRefunds() error: %v", err)
	}

	claimedIDs := make(map[string]bool)

	for refund := range claimedCh {
		if claimedIDs[refund.ID] {
			t.Errorf("refund %s was reclaimed more than once", refund.ID)
		}

		claimedIDs[refund.ID] = true
	}

	if len(claimedIDs) != workerCount {
		t.Errorf(
			"expected %d uniquely reclaimed refunds, got %d",
			workerCount,
			len(claimedIDs),
		)
	}
}

func createClaimedRefundForTest(
	t *testing.T,
	repo *Repository,
	pool *pgxpool.Pool,
	fixture *paymentTestFixture,
) (Refund, string) {
	t.Helper()

	payment := createPaymentForLeaseTest(t, repo, fixture)

	refundID := createPendingRefundForClaimTest(
		t,
		pool,
		payment,
		time.Now().UTC().Add(-time.Minute),
	)

	claimed, err := repo.ClaimPendingRefunds(
		context.Background(),
		1,
		5*time.Minute,
	)
	if err != nil {
		t.Fatalf("failed to claim test refund: %v", err)
	}

	if len(claimed) != 1 {
		t.Fatalf("expected 1 claimed refund, got %d", len(claimed))
	}

	if claimed[0].ID != refundID {
		t.Fatalf(
			"expected claimed refund %s, got %s",
			refundID,
			claimed[0].ID,
		)
	}

	if claimed[0].ProcessingLeaseToken == nil {
		t.Fatal("expected processing lease token")
	}

	return claimed[0], *claimed[0].ProcessingLeaseToken
}

func TestRepositoryMarkRefundSucceeded(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, leaseToken := createClaimedRefundForTest(
		t,
		repo,
		pool,
		fixture,
	)

	providerRefundID := "rfnd_success_test_001"

	err := repo.MarkRefundSucceeded(
		context.Background(),
		refund.ID,
		leaseToken,
		providerRefundID,
	)
	if err != nil {
		t.Fatalf("MarkRefundSucceeded() error = %v", err)
	}

	var (
		status                   string
		storedProviderRefundID   *string
		processingLeaseToken     *string
		processingLeaseExpiresAt *time.Time
		lastError                *string
	)

	err = pool.QueryRow(context.Background(), `
		SELECT
			status,
			provider_refund_id,
			processing_lease_token,
			processing_lease_expires_at,
			last_error
		FROM payment_refunds
		WHERE id = $1
	`, refund.ID).Scan(
		&status,
		&storedProviderRefundID,
		&processingLeaseToken,
		&processingLeaseExpiresAt,
		&lastError,
	)
	if err != nil {
		t.Fatalf("failed to read updated refund: %v", err)
	}

	if status != RefundStatusSucceeded {
		t.Errorf("expected status succeeded, got %q", status)
	}

	if storedProviderRefundID == nil ||
		*storedProviderRefundID != providerRefundID {
		t.Errorf(
			"expected provider refund ID %q, got %v",
			providerRefundID,
			storedProviderRefundID,
		)
	}

	if processingLeaseToken != nil {
		t.Error("expected processing lease token to be cleared")
	}

	if processingLeaseExpiresAt != nil {
		t.Error("expected processing lease expiration to be cleared")
	}

	if lastError != nil {
		t.Errorf("expected last error to be cleared, got %q", *lastError)
	}
}

func TestRepositoryMarkRefundSucceededWithExpiredLease(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, leaseToken := createClaimedRefundForTest(
		t,
		repo,
		pool,
		fixture,
	)

	_, err := pool.Exec(context.Background(), `
		UPDATE payment_refunds
		SET processing_lease_expires_at = NOW() - INTERVAL '1 minute'
		WHERE id = $1
	`, refund.ID)
	if err != nil {
		t.Fatalf("failed to expire refund lease: %v", err)
	}

	err = repo.MarkRefundSucceeded(
		context.Background(),
		refund.ID,
		leaseToken,
		"rfnd_expired_test_001",
	)

	if !errors.Is(err, ErrRefundLeaseLost) {
		t.Fatalf(
			"expected ErrRefundLeaseLost, got %v",
			err,
		)
	}

	var status string

	err = pool.QueryRow(
		context.Background(),
		`SELECT status FROM payment_refunds WHERE id = $1`,
		refund.ID,
	).Scan(&status)
	if err != nil {
		t.Fatalf("failed to read refund status: %v", err)
	}

	if status != RefundStatusProcessing {
		t.Errorf(
			"expected status processing, got %q",
			status,
		)
	}
}

func TestRepositoryMarkRefundSucceededWithStaleLease(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, workerAToken := createClaimedRefundForTest(
		t,
		repo,
		pool,
		fixture,
	)

	_, err := pool.Exec(context.Background(), `
		UPDATE payment_refunds
		SET processing_lease_expires_at = NOW() - INTERVAL '1 minute'
		WHERE id = $1
	`, refund.ID)
	if err != nil {
		t.Fatalf("failed to expire worker A lease: %v", err)
	}

	reclaimed, err := repo.ClaimExpiredRefunds(
		context.Background(),
		1,
		5*time.Minute,
	)
	if err != nil {
		t.Fatalf("worker B failed to reclaim refund: %v", err)
	}

	if len(reclaimed) != 1 {
		t.Fatalf("expected worker B to reclaim 1 refund, got %d", len(reclaimed))
	}

	if reclaimed[0].ProcessingLeaseToken == nil {
		t.Fatal("expected worker B lease token")
	}

	workerBToken := *reclaimed[0].ProcessingLeaseToken

	if workerAToken == workerBToken {
		t.Fatal("expected worker B to receive a different lease token")
	}

	// Worker A wakes up and tries to complete the refund.
	err = repo.MarkRefundSucceeded(
		context.Background(),
		refund.ID,
		workerAToken,
		"rfnd_stale_worker_test",
	)

	if !errors.Is(err, ErrRefundLeaseLost) {
		t.Fatalf(
			"expected ErrRefundLeaseLost for worker A, got %v",
			err,
		)
	}

	// Worker B must still own the refund.
	var storedToken string

	err = pool.QueryRow(context.Background(), `
		SELECT processing_lease_token::text
		FROM payment_refunds
		WHERE id = $1
	`, refund.ID).Scan(&storedToken)
	if err != nil {
		t.Fatalf("failed to read current lease token: %v", err)
	}

	if storedToken != workerBToken {
		t.Error("worker A modified worker B's lease")
	}
}

func TestRepositoryMarkRefundSucceededWithProviderRefundIDConflict(
	t *testing.T,
) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, leaseToken := createClaimedRefundForTest(
		t,
		repo,
		pool,
		fixture,
	)

	existingProviderRefundID := "rfnd_existing_test_001"

	_, err := pool.Exec(context.Background(), `
		UPDATE payment_refunds
		SET provider_refund_id = $2
		WHERE id = $1
	`,
		refund.ID,
		existingProviderRefundID,
	)
	if err != nil {
		t.Fatalf("failed to set existing provider refund ID: %v", err)
	}

	err = repo.MarkRefundSucceeded(
		context.Background(),
		refund.ID,
		leaseToken,
		"rfnd_different_test_002",
	)

	if !errors.Is(err, ErrProviderRefundIDConflict) {
		t.Fatalf(
			"expected ErrProviderRefundIDConflict, got %v",
			err,
		)
	}

	var (
		status                 string
		storedProviderRefundID string
	)

	err = pool.QueryRow(context.Background(), `
		SELECT status, provider_refund_id
		FROM payment_refunds
		WHERE id = $1
	`, refund.ID).Scan(
		&status,
		&storedProviderRefundID,
	)
	if err != nil {
		t.Fatalf("failed to read refund: %v", err)
	}

	if status != RefundStatusProcessing {
		t.Errorf("expected processing status, got %q", status)
	}

	if storedProviderRefundID != existingProviderRefundID {
		t.Errorf(
			"expected provider refund ID %q, got %q",
			existingProviderRefundID,
			storedProviderRefundID,
		)
	}
}

func TestRepositoryRecordProviderRefundID(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, leaseToken := createClaimedRefundForTest(
		t, repo, pool, fixture,
	)

	providerRefundID := "rfnd_record_test_001"

	err := repo.RecordProviderRefundID(
		context.Background(),
		refund.ID,
		leaseToken,
		providerRefundID,
	)
	if err != nil {
		t.Fatalf("RecordProviderRefundID() error = %v", err)
	}

	var (
		status                   string
		storedProviderRefundID   *string
		processingLeaseToken     *string
		processingLeaseExpiresAt *time.Time
	)

	err = pool.QueryRow(context.Background(), `
		SELECT
			status,
			provider_refund_id,
			processing_lease_token,
			processing_lease_expires_at
		FROM payment_refunds
		WHERE id = $1
	`, refund.ID).Scan(
		&status,
		&storedProviderRefundID,
		&processingLeaseToken,
		&processingLeaseExpiresAt,
	)
	if err != nil {
		t.Fatalf("failed to read refund: %v", err)
	}

	if status != RefundStatusProcessing {
		t.Errorf("expected processing status, got %q", status)
	}

	if storedProviderRefundID == nil ||
		*storedProviderRefundID != providerRefundID {
		t.Errorf(
			"expected provider refund ID %q, got %v",
			providerRefundID,
			storedProviderRefundID,
		)
	}

	if processingLeaseToken == nil ||
		*processingLeaseToken != leaseToken {
		t.Error("expected processing lease token to remain unchanged")
	}

	if processingLeaseExpiresAt == nil {
		t.Error("expected processing lease expiration to remain set")
	}
}

func TestRepositoryRecordProviderRefundIDWithStaleLease(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, workerAToken := createClaimedRefundForTest(
		t,
		repo,
		pool,
		fixture,
	)

	_, err := pool.Exec(context.Background(), `
		UPDATE payment_refunds
		SET processing_lease_expires_at = NOW() - INTERVAL '1 minute'
		WHERE id = $1
	`, refund.ID)
	if err != nil {
		t.Fatalf("failed to expire worker A lease: %v", err)
	}

	reclaimed, err := repo.ClaimExpiredRefunds(
		context.Background(),
		1,
		5*time.Minute,
	)
	if err != nil {
		t.Fatalf("worker B failed to reclaim refund: %v", err)
	}

	if len(reclaimed) != 1 {
		t.Fatalf(
			"expected worker B to reclaim 1 refund, got %d",
			len(reclaimed),
		)
	}

	if reclaimed[0].ProcessingLeaseToken == nil {
		t.Fatal("expected worker B lease token")
	}

	workerBToken := *reclaimed[0].ProcessingLeaseToken

	if workerAToken == workerBToken {
		t.Fatal("expected worker B to receive a different lease token")
	}

	err = repo.RecordProviderRefundID(
		context.Background(),
		refund.ID,
		workerAToken,
		"rfnd_stale_worker_test",
	)

	if !errors.Is(err, ErrRefundLeaseLost) {
		t.Fatalf(
			"expected ErrRefundLeaseLost for worker A, got %v",
			err,
		)
	}

	var (
		storedToken string
		storedID    *string
	)

	err = pool.QueryRow(context.Background(), `
		SELECT
			processing_lease_token::text,
			provider_refund_id
		FROM payment_refunds
		WHERE id = $1
	`, refund.ID).Scan(&storedToken, &storedID)
	if err != nil {
		t.Fatalf("failed to read refund: %v", err)
	}

	if storedToken != workerBToken {
		t.Error("worker A modified worker B's lease")
	}

	if storedID != nil {
		t.Errorf(
			"expected provider refund ID to remain NULL, got %q",
			*storedID,
		)
	}
}

func TestRepositoryRecordProviderRefundIDIdempotent(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, leaseToken := createClaimedRefundForTest(
		t, repo, pool, fixture,
	)

	providerRefundID := "rfnd_record_idempotent_001"

	for i := 0; i < 2; i++ {
		err := repo.RecordProviderRefundID(
			context.Background(),
			refund.ID,
			leaseToken,
			providerRefundID,
		)
		if err != nil {
			t.Fatalf("attempt %d: RecordProviderRefundID() error = %v", i+1, err)
		}
	}

	var storedProviderRefundID string

	err := pool.QueryRow(context.Background(), `
		SELECT provider_refund_id
		FROM payment_refunds
		WHERE id = $1
	`, refund.ID).Scan(&storedProviderRefundID)
	if err != nil {
		t.Fatalf("failed to read provider refund ID: %v", err)
	}

	if storedProviderRefundID != providerRefundID {
		t.Errorf(
			"expected provider refund ID %q, got %q",
			providerRefundID,
			storedProviderRefundID,
		)
	}
}

func TestRepositoryRecordProviderRefundIDWithConflict(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, leaseToken := createClaimedRefundForTest(
		t, repo, pool, fixture,
	)

	existingProviderRefundID := "rfnd_existing_record_001"

	_, err := pool.Exec(context.Background(), `
		UPDATE payment_refunds
		SET provider_refund_id = $2
		WHERE id = $1
	`, refund.ID, existingProviderRefundID)
	if err != nil {
		t.Fatalf("failed to set existing provider refund ID: %v", err)
	}

	err = repo.RecordProviderRefundID(
		context.Background(),
		refund.ID,
		leaseToken,
		"rfnd_different_record_002",
	)

	if !errors.Is(err, ErrProviderRefundIDConflict) {
		t.Fatalf(
			"expected ErrProviderRefundIDConflict, got %v",
			err,
		)
	}

	var storedProviderRefundID string

	err = pool.QueryRow(context.Background(), `
		SELECT provider_refund_id
		FROM payment_refunds
		WHERE id = $1
	`, refund.ID).Scan(&storedProviderRefundID)
	if err != nil {
		t.Fatalf("failed to read provider refund ID: %v", err)
	}

	if storedProviderRefundID != existingProviderRefundID {
		t.Errorf(
			"expected provider refund ID %q, got %q",
			existingProviderRefundID,
			storedProviderRefundID,
		)
	}
}

func TestRepositoryRecordProviderRefundIDWithExpiredLease(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, leaseToken := createClaimedRefundForTest(
		t, repo, pool, fixture,
	)

	_, err := pool.Exec(context.Background(), `
		UPDATE payment_refunds
		SET processing_lease_expires_at = NOW() - INTERVAL '1 minute'
		WHERE id = $1
	`, refund.ID)
	if err != nil {
		t.Fatalf("failed to expire lease: %v", err)
	}

	err = repo.RecordProviderRefundID(
		context.Background(),
		refund.ID,
		leaseToken,
		"rfnd_expired_record_001",
	)

	if !errors.Is(err, ErrRefundLeaseLost) {
		t.Fatalf("expected ErrRefundLeaseLost, got %v", err)
	}
}

func TestRepositoryMarkRefundFailed(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, leaseToken := createClaimedRefundForTest(
		t, repo, pool, fixture,
	)

	failureReason := "Razorpay rejected the refund"

	err := repo.MarkRefundFailed(
		context.Background(),
		refund.ID,
		leaseToken,
		failureReason,
	)
	if err != nil {
		t.Fatalf("MarkRefundFailed() error = %v", err)
	}

	var (
		status                   string
		lastError                *string
		processingLeaseToken     *string
		processingLeaseExpiresAt *time.Time
	)

	err = pool.QueryRow(context.Background(), `
		SELECT
			status,
			last_error,
			processing_lease_token,
			processing_lease_expires_at
		FROM payment_refunds
		WHERE id = $1
	`, refund.ID).Scan(
		&status,
		&lastError,
		&processingLeaseToken,
		&processingLeaseExpiresAt,
	)
	if err != nil {
		t.Fatalf("failed to read refund: %v", err)
	}

	if status != RefundStatusFailed {
		t.Errorf("expected failed status, got %q", status)
	}

	if lastError == nil || *lastError != failureReason {
		t.Errorf("unexpected failure reason: %v", lastError)
	}

	if processingLeaseToken != nil {
		t.Error("expected lease token to be cleared")
	}

	if processingLeaseExpiresAt != nil {
		t.Error("expected lease expiration to be cleared")
	}
}

func TestRepositoryMarkRefundReviewRequired(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, leaseToken := createClaimedRefundForTest(
		t, repo, pool, fixture,
	)

	reason := "Unexpected Razorpay refund status"

	err := repo.MarkRefundReviewRequired(
		context.Background(),
		refund.ID,
		leaseToken,
		reason,
	)
	if err != nil {
		t.Fatalf("MarkRefundReviewRequired() error = %v", err)
	}

	var (
		status                   string
		lastError                *string
		processingLeaseToken     *string
		processingLeaseExpiresAt *time.Time
	)

	err = pool.QueryRow(context.Background(), `
		SELECT
			status,
			last_error,
			processing_lease_token,
			processing_lease_expires_at
		FROM payment_refunds
		WHERE id = $1
	`, refund.ID).Scan(
		&status,
		&lastError,
		&processingLeaseToken,
		&processingLeaseExpiresAt,
	)
	if err != nil {
		t.Fatalf("failed to read refund: %v", err)
	}

	if status != RefundStatusReviewRequired {
		t.Errorf("expected review_required, got %q", status)
	}

	if lastError == nil || *lastError != reason {
		t.Errorf("unexpected last error: %v", lastError)
	}

	if processingLeaseToken != nil {
		t.Error("expected lease token to be cleared")
	}

	if processingLeaseExpiresAt != nil {
		t.Error("expected lease expiration to be cleared")
	}
}

func TestRepositoryMarkRefundReviewRequiredWithExpiredLease(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, leaseToken := createClaimedRefundForTest(
		t, repo, pool, fixture,
	)

	_, err := pool.Exec(context.Background(), `
		UPDATE payment_refunds
		SET processing_lease_expires_at =
			NOW() - INTERVAL '1 minute'
		WHERE id = $1
	`, refund.ID)
	if err != nil {
		t.Fatalf("failed to expire lease: %v", err)
	}

	err = repo.MarkRefundReviewRequired(
		context.Background(),
		refund.ID,
		leaseToken,
		"Unexpected provider response",
	)

	if !errors.Is(err, ErrRefundLeaseLost) {
		t.Fatalf("expected ErrRefundLeaseLost, got %v", err)
	}
}

func TestRepositoryMarkRefundReviewRequiredWithStaleLease(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, workerAToken := createClaimedRefundForTest(
		t, repo, pool, fixture,
	)

	_, err := pool.Exec(context.Background(), `
		UPDATE payment_refunds
		SET processing_lease_expires_at =
			NOW() - INTERVAL '1 minute'
		WHERE id = $1
	`, refund.ID)
	if err != nil {
		t.Fatalf("failed to expire worker A lease: %v", err)
	}

	reclaimed, err := repo.ClaimExpiredRefunds(
		context.Background(),
		1,
		5*time.Minute,
	)
	if err != nil {
		t.Fatalf("worker B failed to reclaim refund: %v", err)
	}

	if len(reclaimed) != 1 {
		t.Fatalf("expected 1 reclaimed refund, got %d", len(reclaimed))
	}

	if reclaimed[0].ProcessingLeaseToken == nil {
		t.Fatal("expected worker B lease token")
	}

	workerBToken := *reclaimed[0].ProcessingLeaseToken

	err = repo.MarkRefundReviewRequired(
		context.Background(),
		refund.ID,
		workerAToken,
		"Stale worker response",
	)

	if !errors.Is(err, ErrRefundLeaseLost) {
		t.Fatalf("expected ErrRefundLeaseLost, got %v", err)
	}

	var (
		status      string
		storedToken string
	)

	err = pool.QueryRow(context.Background(), `
		SELECT status, processing_lease_token::text
		FROM payment_refunds
		WHERE id = $1
	`, refund.ID).Scan(&status, &storedToken)
	if err != nil {
		t.Fatalf("failed to read refund: %v", err)
	}

	if status != RefundStatusProcessing {
		t.Errorf("expected processing status, got %q", status)
	}

	if storedToken != workerBToken {
		t.Error("worker A modified worker B's lease")
	}
}

func TestRepositoryMarkRefundFailedWithExpiredLease(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, leaseToken := createClaimedRefundForTest(
		t, repo, pool, fixture,
	)

	_, err := pool.Exec(context.Background(), `
		UPDATE payment_refunds
		SET processing_lease_expires_at =
			NOW() - INTERVAL '1 minute'
		WHERE id = $1
	`, refund.ID)
	if err != nil {
		t.Fatalf("failed to expire lease: %v", err)
	}

	err = repo.MarkRefundFailed(
		context.Background(),
		refund.ID,
		leaseToken,
		"Provider rejected refund",
	)

	if !errors.Is(err, ErrRefundLeaseLost) {
		t.Fatalf("expected ErrRefundLeaseLost, got %v", err)
	}
}

func TestRepositoryMarkRefundFailedWithStaleLease(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, workerAToken := createClaimedRefundForTest(
		t, repo, pool, fixture,
	)

	_, err := pool.Exec(context.Background(), `
		UPDATE payment_refunds
		SET processing_lease_expires_at =
			NOW() - INTERVAL '1 minute'
		WHERE id = $1
	`, refund.ID)
	if err != nil {
		t.Fatalf("failed to expire worker A lease: %v", err)
	}

	reclaimed, err := repo.ClaimExpiredRefunds(
		context.Background(),
		1,
		5*time.Minute,
	)
	if err != nil {
		t.Fatalf("failed to reclaim refund: %v", err)
	}

	if len(reclaimed) != 1 {
		t.Fatalf("expected 1 reclaimed refund, got %d", len(reclaimed))
	}

	err = repo.MarkRefundFailed(
		context.Background(),
		refund.ID,
		workerAToken,
		"Stale worker failure",
	)

	if !errors.Is(err, ErrRefundLeaseLost) {
		t.Fatalf("expected ErrRefundLeaseLost, got %v", err)
	}
}

func TestRepositoryMarkRefundFailedValidation(t *testing.T) {
	repo := &Repository{}

	err := repo.MarkRefundFailed(
		context.Background(),
		"refund-id",
		"lease-token",
		"   ",
	)

	if !errors.Is(err, ErrRefundLeaseLost) {
		t.Fatalf("expected ErrRefundLeaseLost, got %v", err)
	}
}

func TestRepositoryScheduleRefundRetry(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, leaseToken := createClaimedRefundForTest(
		t, repo, pool, fixture,
	)

	nextAttemptAt := time.Now().UTC().
		Add(10 * time.Minute).
		Truncate(time.Microsecond)
	failureReason := "Razorpay temporarily unavailable"

	err := repo.ScheduleRefundRetry(
		context.Background(),
		refund.ID,
		leaseToken,
		failureReason,
		nextAttemptAt,
	)
	if err != nil {
		t.Fatalf("ScheduleRefundRetry() error = %v", err)
	}

	var (
		status                   string
		lastError                *string
		storedNextAttemptAt      time.Time
		processingLeaseToken     *string
		processingLeaseExpiresAt *time.Time
		idempotencyKey           string
		providerPaymentID        string
	)

	err = pool.QueryRow(context.Background(), `
		SELECT
			status,
			last_error,
			next_attempt_at,
			processing_lease_token,
			processing_lease_expires_at,
			idempotency_key,
			provider_payment_id
		FROM payment_refunds
		WHERE id = $1
	`, refund.ID).Scan(
		&status,
		&lastError,
		&storedNextAttemptAt,
		&processingLeaseToken,
		&processingLeaseExpiresAt,
		&idempotencyKey,
		&providerPaymentID,
	)
	if err != nil {
		t.Fatalf("failed to read scheduled refund: %v", err)
	}

	if status != RefundStatusPending {
		t.Errorf("expected pending status, got %q", status)
	}

	if lastError == nil || *lastError != failureReason {
		t.Errorf("unexpected failure reason: %v", lastError)
	}

	if !storedNextAttemptAt.Equal(nextAttemptAt) {
		t.Errorf(
			"expected next attempt at %v, got %v",
			nextAttemptAt,
			storedNextAttemptAt,
		)
	}

	if processingLeaseToken != nil {
		t.Error("expected processing lease token to be cleared")
	}

	if processingLeaseExpiresAt != nil {
		t.Error("expected processing lease expiration to be cleared")
	}

	if idempotencyKey != refund.IdempotencyKey {
		t.Error("refund idempotency key changed during retry scheduling")
	}

	if providerPaymentID != refund.ProviderPaymentID {
		t.Error("provider payment ID changed during retry scheduling")
	}
}

func TestRepositoryScheduleRefundRetryWithExpiredLease(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, leaseToken := createClaimedRefundForTest(
		t, repo, pool, fixture,
	)

	_, err := pool.Exec(context.Background(), `
		UPDATE payment_refunds
		SET processing_lease_expires_at =
			NOW() - INTERVAL '1 minute'
		WHERE id = $1
	`, refund.ID)
	if err != nil {
		t.Fatalf("failed to expire refund lease: %v", err)
	}

	err = repo.ScheduleRefundRetry(
		context.Background(),
		refund.ID,
		leaseToken,
		"Temporary provider error",
		time.Now().UTC().Add(10*time.Minute),
	)

	if !errors.Is(err, ErrRefundLeaseLost) {
		t.Fatalf("expected ErrRefundLeaseLost, got %v", err)
	}
}

func TestRepositoryScheduleRefundRetryWithStaleLease(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, workerAToken := createClaimedRefundForTest(
		t, repo, pool, fixture,
	)

	_, err := pool.Exec(context.Background(), `
		UPDATE payment_refunds
		SET processing_lease_expires_at =
			NOW() - INTERVAL '1 minute'
		WHERE id = $1
	`, refund.ID)
	if err != nil {
		t.Fatalf("failed to expire worker A lease: %v", err)
	}

	reclaimed, err := repo.ClaimExpiredRefunds(
		context.Background(),
		1,
		5*time.Minute,
	)
	if err != nil {
		t.Fatalf("failed to reclaim refund: %v", err)
	}

	if len(reclaimed) != 1 {
		t.Fatalf("expected 1 reclaimed refund, got %d", len(reclaimed))
	}

	err = repo.ScheduleRefundRetry(
		context.Background(),
		refund.ID,
		workerAToken,
		"Stale worker retry",
		time.Now().UTC().Add(10*time.Minute),
	)

	if !errors.Is(err, ErrRefundLeaseLost) {
		t.Fatalf("expected ErrRefundLeaseLost, got %v", err)
	}
}

func TestRepositoryScheduleRefundRetryValidation(t *testing.T) {
	repo := &Repository{}
	ctx := context.Background()

	err := repo.ScheduleRefundRetry(
		ctx,
		"refund-id",
		"lease-token",
		"temporary error",
		time.Time{},
	)

	if !errors.Is(err, ErrRefundLeaseLost) {
		t.Fatalf("expected ErrRefundLeaseLost, got %v", err)
	}

	err = repo.ScheduleRefundRetry(
		ctx,
		"refund-id",
		"lease-token",
		"   ",
		time.Now().UTC().Add(time.Minute),
	)

	if !errors.Is(err, ErrRefundLeaseLost) {
		t.Fatalf("expected ErrRefundLeaseLost, got %v", err)
	}
}
