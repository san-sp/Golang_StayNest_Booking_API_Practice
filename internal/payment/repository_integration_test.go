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
