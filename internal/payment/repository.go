package payment

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return NewRepositoryWithClock(pool, time.Now)
}

// NewRepositoryWithClock allows time-dependent repository
// behavior to be controlled in tests.
func NewRepositoryWithClock(
	pool *pgxpool.Pool,
	now func() time.Time,
) *Repository {
	if now == nil {
		now = time.Now
	}

	return &Repository{
		pool: pool,
		now:  now,
	}
}

// CreatePending creates one local payment for an existing booking.
//
// The booking row is locked before checking its payment state.
// This coordinates payment initiation with cancellation and expiry.
func (r *Repository) CreatePending(
	ctx context.Context,
	input InitiateInput,
) (*Payment, error) {
	input.BookingID = strings.TrimSpace(input.BookingID)
	input.CustomerID = strings.TrimSpace(input.CustomerID)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)

	if input.BookingID == "" {
		return nil, ErrInvalidBookingID
	}

	if input.CustomerID == "" {
		return nil, ErrInvalidCustomerID
	}

	if input.IdempotencyKey == "" {
		return nil, ErrInvalidIdempotencyKey
	}

	if len(input.IdempotencyKey) > 128 {
		return nil, ErrInvalidIdempotencyKey
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin payment creation transaction: %w", err)
	}

	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	// First, check whether this idempotency key has already
	// been used by this customer.
	existing, err := scanPayment(tx.QueryRow(
		ctx,
		`
		SELECT
			id::text,
			booking_id::text,
			customer_id::text,
			amount_paise,
			currency,
			provider,
			provider_order_id,
			idempotency_key,
			status,
			fulfillment_status,
			created_at,
			updated_at
		FROM payments
		WHERE customer_id = $1
		  AND idempotency_key = $2
		`,
		input.CustomerID,
		input.IdempotencyKey,
	))

	if err == nil {
		if existing.BookingID != input.BookingID {
			return nil, ErrIdempotencyKeyConflict
		}

		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit existing payment lookup: %w", err)
		}

		return existing, nil
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("find payment by idempotency key: %w", err)
	}

	// Lock the booking before checking its payment eligibility.
	// The expiry worker and cancellation operation also lock
	// this row before changing its state.
	var (
		bookingAmount    int64
		bookingStatus    string
		paymentExpiresAt *time.Time
	)

	err = tx.QueryRow(
		ctx,
		`
		SELECT
			total_amount_paise,
			status,
			payment_expires_at
		FROM bookings
		WHERE id = $1
		  AND customer_id = $2
		FOR UPDATE
		`,
		input.BookingID,
		input.CustomerID,
	).Scan(
		&bookingAmount,
		&bookingStatus,
		&paymentExpiresAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrBookingNotFound
		}

		return nil, fmt.Errorf("lock booking for payment: %w", err)
	}

	if bookingStatus != BookingStatusPendingPayment {
		return nil, ErrBookingNotPayable
	}

	now := r.now().UTC()

	if paymentExpiresAt == nil || !paymentExpiresAt.After(now) {
		return nil, ErrBookingNotPayable
	}

	if bookingAmount <= 0 {
		return nil, ErrInvalidPaymentAmount
	}

	// A booking can have only one payment record.
	// A retry with the same idempotency key returns it.
	existing, err = scanPayment(tx.QueryRow(
		ctx,
		`
		SELECT
			id::text,
			booking_id::text,
			customer_id::text,
			amount_paise,
			currency,
			provider,
			provider_order_id,
			idempotency_key,
			status,
			fulfillment_status,
			created_at,
			updated_at
		FROM payments
		WHERE booking_id = $1
		`,
		input.BookingID,
	))

	if err == nil {
		if existing.IdempotencyKey != input.IdempotencyKey {
			return nil, ErrPaymentAlreadyExists
		}

		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit existing booking payment lookup: %w", err)
		}

		return existing, nil
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("find payment by booking: %w", err)
	}

	// The amount comes directly from the locked booking.
	// It is never accepted from the customer.
	created, err := scanPayment(tx.QueryRow(
		ctx,
		`
		INSERT INTO payments (
			booking_id,
			customer_id,
			amount_paise,
			currency,
			provider,
			idempotency_key,
			status,
			fulfillment_status
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING
			id::text,
			booking_id::text,
			customer_id::text,
			amount_paise,
			currency,
			provider,
			provider_order_id,
			idempotency_key,
			status,
			fulfillment_status,
			created_at,
			updated_at
		`,
		input.BookingID,
		input.CustomerID,
		bookingAmount,
		CurrencyINR,
		ProviderRazorpay,
		input.IdempotencyKey,
		StatusCreated,
		FulfillmentPending,
	))

	if err != nil {
		// Protect against the same idempotency key being used
		// concurrently for another booking.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) &&
			pgErr.Code == "23505" &&
			pgErr.ConstraintName == "uq_payment_customer_idempotency" {
			return nil, ErrIdempotencyKeyConflict
		}

		return nil, fmt.Errorf("insert payment: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit payment creation: %w", err)
	}

	return created, nil
}

// GetByIDForCustomer retrieves a payment and verifies its ownership.
func (r *Repository) GetByIDForCustomer(
	ctx context.Context,
	paymentID string,
	customerID string,
) (*Payment, error) {
	paymentID = strings.TrimSpace(paymentID)
	customerID = strings.TrimSpace(customerID)

	if paymentID == "" {
		return nil, ErrPaymentNotFound
	}

	if customerID == "" {
		return nil, ErrInvalidCustomerID
	}

	payment, err := scanPayment(r.pool.QueryRow(
		ctx,
		`
        SELECT
            id::text,
            booking_id::text,
            customer_id::text,
            amount_paise,
            currency,
            provider,
            provider_order_id,
            idempotency_key,
            status,
            fulfillment_status,
            created_at,
            updated_at
        FROM payments
        WHERE id = $1
        `,
		paymentID,
	))

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPaymentNotFound
		}

		return nil, fmt.Errorf("get payment by ID: %w", err)
	}

	if payment.CustomerID != customerID {
		return nil, ErrPaymentOwnershipMismatch
	}

	return payment, nil
}

// AcquireProviderOrderLease atomically acquires permission to create
// or recover the Razorpay order associated with a payment.
func (r *Repository) AcquireProviderOrderLease(
	ctx context.Context,
	paymentID string,
	leaseDuration time.Duration,
) (string, error) {
	paymentID = strings.TrimSpace(paymentID)

	if paymentID == "" {
		return "", ErrPaymentNotFound
	}

	if leaseDuration <= 0 {
		return "", ErrInvalidOrderLeaseDuration
	}

	var leaseToken string

	err := r.pool.QueryRow(
		ctx,
		`
        UPDATE payments
        SET
            provider_order_lease_token = gen_random_uuid(),
            provider_order_lease_expires_at =
                NOW() + ($2 * INTERVAL '1 second'),
            updated_at = NOW()
        WHERE id = $1
          AND provider_order_id IS NULL
          AND (
              provider_order_lease_token IS NULL
              OR provider_order_lease_expires_at <= NOW()
          )
        RETURNING provider_order_lease_token::text
        `,
		paymentID,
		leaseDuration.Seconds(),
	).Scan(&leaseToken)

	if err == nil {
		return leaseToken, nil
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("acquire provider order lease: %w", err)
	}

	// The UPDATE did not acquire a lease. Determine why.
	var (
		providerOrderID pgtype.Text
		leaseTokenValue pgtype.Text
	)

	err = r.pool.QueryRow(
		ctx,
		`
        SELECT
            provider_order_id,
            provider_order_lease_token::text
        FROM payments
        WHERE id = $1
        `,
		paymentID,
	).Scan(
		&providerOrderID,
		&leaseTokenValue,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrPaymentNotFound
		}

		return "", fmt.Errorf("inspect provider order lease: %w", err)
	}

	if providerOrderID.Valid {
		return "", ErrProviderOrderAlreadyExists
	}

	return "", ErrOrderCreationInProgress
}

// SaveProviderOrderID saves the Razorpay order ID only when
// the caller still owns the payment's order creation lease.
func (r *Repository) SaveProviderOrderID(
	ctx context.Context,
	paymentID string,
	leaseToken string,
	providerOrderID string,
) error {
	paymentID = strings.TrimSpace(paymentID)
	leaseToken = strings.TrimSpace(leaseToken)
	providerOrderID = strings.TrimSpace(providerOrderID)

	if paymentID == "" {
		return ErrPaymentNotFound
	}

	if leaseToken == "" {
		return ErrInvalidOrderLeaseToken
	}

	if providerOrderID == "" {
		return ErrInvalidProviderOrderID
	}

	result, err := r.pool.Exec(
		ctx,
		`
        UPDATE payments
        SET
            provider_order_id = $3,
            provider_order_lease_token = NULL,
            provider_order_lease_expires_at = NULL,
            updated_at = NOW()
        WHERE id = $1
          AND provider_order_lease_token = $2
          AND provider_order_id IS NULL
        `,
		paymentID,
		leaseToken,
		providerOrderID,
	)

	if err != nil {
		return fmt.Errorf("save provider order ID: %w", err)
	}

	if result.RowsAffected() == 1 {
		return nil
	}

	// No row was updated. Check whether the same order was
	// already saved, or whether this request lost its lease.
	var existingOrderID pgtype.Text

	err = r.pool.QueryRow(
		ctx,
		`
        SELECT provider_order_id
        FROM payments
        WHERE id = $1
        `,
		paymentID,
	).Scan(&existingOrderID)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrPaymentNotFound
		}

		return fmt.Errorf("check existing provider order: %w", err)
	}

	if existingOrderID.Valid &&
		existingOrderID.String == providerOrderID {
		return nil
	}

	return ErrOrderCreationLeaseLost
}

// ReleaseProviderOrderLease releases a lease if the caller still owns it.
// It does nothing if the lease has already been replaced or released.
func (r *Repository) ReleaseProviderOrderLease(
	ctx context.Context,
	paymentID string,
	leaseToken string,
) error {
	paymentID = strings.TrimSpace(paymentID)
	leaseToken = strings.TrimSpace(leaseToken)

	if paymentID == "" || leaseToken == "" {
		return nil
	}

	_, err := r.pool.Exec(
		ctx,
		`
        UPDATE payments
        SET
            provider_order_lease_token = NULL,
            provider_order_lease_expires_at = NULL,
            updated_at = NOW()
        WHERE id = $1
          AND provider_order_lease_token = $2
          AND provider_order_id IS NULL
        `,
		paymentID,
		leaseToken,
	)

	if err != nil {
		return fmt.Errorf("release provider order lease: %w", err)
	}

	return nil
}

// scanPayment maps a PostgreSQL payment row into the domain model.
func scanPayment(row pgx.Row) (*Payment, error) {
	var (
		payment         Payment
		providerOrderID pgtype.Text
	)

	err := row.Scan(
		&payment.ID,
		&payment.BookingID,
		&payment.CustomerID,
		&payment.AmountPaise,
		&payment.Currency,
		&payment.Provider,
		&providerOrderID,
		&payment.IdempotencyKey,
		&payment.Status,
		&payment.FulfillmentStatus,
		&payment.CreatedAt,
		&payment.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	if providerOrderID.Valid {
		payment.ProviderOrderID = &providerOrderID.String
	}

	return &payment, nil
}

// FulfillCapturedPayment records a captured Razorpay payment and
// confirms its booking atomically.
//
// Lock order:
//  1. Payment row
//  2. Booking row
//
// Cancellation and expiry also lock the booking row. This ensures
// that only one operation can decide the booking's final state.
func (r *Repository) FulfillCapturedPayment(
	ctx context.Context,
	input CapturedPaymentInput,
) (*Payment, error) {
	input.PaymentID = strings.TrimSpace(input.PaymentID)
	input.CustomerID = strings.TrimSpace(input.CustomerID)
	input.ProviderOrderID = strings.TrimSpace(input.ProviderOrderID)
	input.ProviderPaymentID = strings.TrimSpace(input.ProviderPaymentID)
	input.Currency = strings.TrimSpace(input.Currency)

	if input.PaymentID == "" {
		return nil, ErrPaymentNotFound
	}

	if input.CustomerID == "" {
		return nil, ErrInvalidCustomerID
	}

	if input.ProviderOrderID == "" {
		return nil, ErrInvalidProviderOrderID
	}

	if input.ProviderPaymentID == "" || len(input.ProviderPaymentID) > 100 {
		return nil, ErrInvalidProviderPaymentID
	}

	if input.AmountPaise <= 0 {
		return nil, ErrInvalidPaymentAmount
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin payment fulfillment transaction: %w", err)
	}

	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	// Lock the payment first.
	payment, err := scanPayment(tx.QueryRow(
		ctx,
		`
        SELECT
            id::text,
            booking_id::text,
            customer_id::text,
            amount_paise,
            currency,
            provider,
            provider_order_id,
            idempotency_key,
            status,
            fulfillment_status,
            created_at,
            updated_at
        FROM payments
        WHERE id = $1
        FOR UPDATE
        `,
		input.PaymentID,
	))

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPaymentNotFound
		}

		return nil, fmt.Errorf("lock payment for fulfillment: %w", err)
	}

	if payment.CustomerID != input.CustomerID {
		return nil, ErrPaymentOwnershipMismatch
	}

	if payment.Provider != ProviderRazorpay {
		return nil, ErrProviderOrderMismatch
	}

	if payment.ProviderOrderID == nil ||
		*payment.ProviderOrderID != input.ProviderOrderID {
		return nil, ErrProviderOrderMismatch
	}

	if payment.AmountPaise != input.AmountPaise {
		return nil, ErrPaymentAmountMismatch
	}

	if payment.Currency != input.Currency {
		return nil, ErrPaymentCurrencyMismatch
	}

	now := r.now().UTC()

	// Lock the booking second.
	var (
		bookingStatus    string
		paymentExpiresAt *time.Time
	)

	err = tx.QueryRow(
		ctx,
		`
        SELECT status, payment_expires_at
        FROM bookings
        WHERE id = $1
          AND customer_id = $2
        FOR UPDATE
        `,
		payment.BookingID,
		payment.CustomerID,
	).Scan(
		&bookingStatus,
		&paymentExpiresAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrBookingNotFound
		}

		return nil, fmt.Errorf("lock booking for payment fulfillment: %w", err)
	}

	// Check whether this Razorpay payment ID already exists.
	var (
		existingAttemptID string
		existingPaymentID string
		existingAmount    int64
		existingCurrency  string
	)

	err = tx.QueryRow(
		ctx,
		`
        SELECT
            id::text,
            payment_id::text,
            amount_paise,
            currency
        FROM payment_attempts
        WHERE provider = $1
          AND provider_payment_id = $2
        FOR UPDATE
        `,
		ProviderRazorpay,
		input.ProviderPaymentID,
	).Scan(
		&existingAttemptID,
		&existingPaymentID,
		&existingAmount,
		&existingCurrency,
	)

	attemptExists := err == nil

	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("find existing payment attempt: %w", err)
	}

	if attemptExists {
		if existingPaymentID != payment.ID {
			return nil, ErrProviderPaymentIDConflict
		}

		if existingAmount != input.AmountPaise {
			return nil, ErrPaymentAmountMismatch
		}

		if existingCurrency != input.Currency {
			return nil, ErrPaymentCurrencyMismatch
		}

		_, err = tx.Exec(
			ctx,
			`
            UPDATE payment_attempts
            SET status = $1,
                updated_at = $2
            WHERE id = $3
            `,
			StatusCaptured,
			now,
			existingAttemptID,
		)

		if err != nil {
			return nil, fmt.Errorf("update captured payment attempt: %w", err)
		}
	} else {
		_, err = tx.Exec(
			ctx,
			`
            INSERT INTO payment_attempts (
                payment_id,
                provider,
                provider_payment_id,
                amount_paise,
                currency,
                status
            )
            VALUES ($1, $2, $3, $4, $5, $6)
            `,
			payment.ID,
			ProviderRazorpay,
			input.ProviderPaymentID,
			input.AmountPaise,
			input.Currency,
			StatusCaptured,
		)

		if err != nil {
			var pgErr *pgconn.PgError

			if errors.As(err, &pgErr) &&
				pgErr.Code == "23505" &&
				pgErr.ConstraintName == "uq_payment_attempts_provider_payment" {
				return nil, ErrProviderPaymentIDConflict
			}

			return nil, fmt.Errorf("insert captured payment attempt: %w", err)
		}
	}

	// A retry for an already processed captured payment is idempotent.
	if payment.Status == StatusCaptured &&
		attemptExists &&
		payment.FulfillmentStatus != FulfillmentPending {
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit repeated payment fulfillment: %w", err)
		}

		return payment, nil
	}

	// A different captured payment ID for an already captured
	// payment is an exceptional situation requiring investigation.
	// Do not silently confirm or overwrite the previous fulfillment.
	if payment.Status == StatusCaptured && !attemptExists {
		updated, err := scanPayment(tx.QueryRow(
			ctx,
			`
            UPDATE payments
            SET fulfillment_status = $1,
                updated_at = $2
            WHERE id = $3
            RETURNING
                id::text,
                booking_id::text,
                customer_id::text,
                amount_paise,
                currency,
                provider,
                provider_order_id,
                idempotency_key,
                status,
                fulfillment_status,
                created_at,
                updated_at
            `,
			FulfillmentReviewRequired,
			now,
			payment.ID,
		))

		if err != nil {
			return nil, fmt.Errorf("mark duplicate capture for review: %w", err)
		}

		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit duplicate capture review: %w", err)
		}

		return updated, nil
	}

	fulfillmentStatus := FulfillmentReviewRequired

	// Only a pending booking whose payment deadline has not passed
	// can be confirmed.
	if bookingStatus == BookingStatusPendingPayment &&
		paymentExpiresAt != nil &&
		paymentExpiresAt.After(now) {

		tag, err := tx.Exec(
			ctx,
			`
            UPDATE bookings
            SET status = 'confirmed',
                updated_at = $1
            WHERE id = $2
              AND status = $3
              AND payment_expires_at > $1
            `,
			now,
			payment.BookingID,
			BookingStatusPendingPayment,
		)

		if err != nil {
			return nil, fmt.Errorf("confirm booking after payment: %w", err)
		}

		if tag.RowsAffected() != 1 {
			return nil, errors.New("booking changed during payment fulfillment")
		}

		fulfillmentStatus = FulfillmentFulfilled
	} else if bookingStatus == "cancelled" ||
		bookingStatus == "expired" ||
		bookingStatus == BookingStatusPendingPayment {

		// The payment was captured, but the booking cannot be fulfilled.
		// Queue a full refund for a background refund worker.
		tag, err := tx.Exec(
			ctx,
			`
            INSERT INTO payment_refunds (
                payment_id,
                provider,
                provider_payment_id,
                idempotency_key,
                amount_paise,
                currency,
                status
            )
            VALUES ($1, $2, $3, $4, $5, $6, $7)
            ON CONFLICT (payment_id) DO NOTHING
            `,
			payment.ID,
			ProviderRazorpay,
			input.ProviderPaymentID,
			"refund:"+payment.ID,
			input.AmountPaise,
			input.Currency,
			RefundStatusPending,
		)

		if err != nil {
			return nil, fmt.Errorf("create required payment refund: %w", err)
		}

		fulfillmentStatus = FulfillmentRefundRequired

		if tag.RowsAffected() == 0 {
			// There is already a refund for this payment.
			// Verify that it refers to the same captured attempt.
			var (
				refundPaymentID string
				refundAmount    int64
				refundCurrency  string
			)

			err := tx.QueryRow(
				ctx,
				`
                SELECT provider_payment_id, amount_paise, currency
                FROM payment_refunds
                WHERE payment_id = $1
                `,
				payment.ID,
			).Scan(
				&refundPaymentID,
				&refundAmount,
				&refundCurrency,
			)

			if err != nil {
				return nil, fmt.Errorf("inspect existing payment refund: %w", err)
			}

			if refundPaymentID != input.ProviderPaymentID ||
				refundAmount != input.AmountPaise ||
				refundCurrency != input.Currency {
				fulfillmentStatus = FulfillmentReviewRequired
			}
		}
	}

	// Record the captured money and its fulfillment outcome.
	updated, err := scanPayment(tx.QueryRow(
		ctx,
		`
        UPDATE payments
        SET status = $1,
            fulfillment_status = $2,
            updated_at = $3
        WHERE id = $4
        RETURNING
            id::text,
            booking_id::text,
            customer_id::text,
            amount_paise,
            currency,
            provider,
            provider_order_id,
            idempotency_key,
            status,
            fulfillment_status,
            created_at,
            updated_at
        `,
		StatusCaptured,
		fulfillmentStatus,
		now,
		payment.ID,
	))

	if err != nil {
		return nil, fmt.Errorf("update captured payment: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit payment fulfillment: %w", err)
	}

	return updated, nil
}

// GetByProviderOrderID retrieves a local payment using its
// payment provider's order ID.
func (r *Repository) GetByProviderOrderID(
	ctx context.Context,
	providerOrderID string,
) (*Payment, error) {
	providerOrderID = strings.TrimSpace(providerOrderID)

	if providerOrderID == "" {
		return nil, ErrInvalidProviderOrderID
	}

	payment, err := scanPayment(r.pool.QueryRow(
		ctx,
		`
		SELECT
			id::text,
			booking_id::text,
			customer_id::text,
			amount_paise,
			currency,
			provider,
			provider_order_id,
			idempotency_key,
			status,
			fulfillment_status,
			created_at,
			updated_at
		FROM payments
		WHERE provider = $1
		  AND provider_order_id = $2
		`,
		ProviderRazorpay,
		providerOrderID,
	))

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPaymentNotFound
		}

		return nil, fmt.Errorf("get payment by provider order ID: %w", err)
	}

	return payment, nil
}
