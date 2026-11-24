package payment

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"staynest/internal/payment/razorpay"
)

type fakePaymentProvider struct {
	fetchedPayment *razorpay.ProviderPayment
	fetchErr       error
	signatureValid bool
	fetchCalls     int
	signatureCalls int
}

func (f *fakePaymentProvider) FetchPayment(
	ctx context.Context,
	paymentID string,
) (*razorpay.ProviderPayment, error) {
	f.fetchCalls++

	if f.fetchErr != nil {
		return nil, f.fetchErr
	}

	if f.fetchedPayment == nil {
		return nil, nil
	}

	result := *f.fetchedPayment
	return &result, nil
}

func (f *fakePaymentProvider) VerifyPaymentSignature(
	orderID string,
	paymentID string,
	signature string,
) bool {
	f.signatureCalls++
	return f.signatureValid
}

// These methods also allow the fake to satisfy OrderProvider.
func (f *fakePaymentProvider) CreateOrder(
	ctx context.Context,
	input razorpay.CreateOrderInput,
) (*razorpay.Order, error) {
	return nil, errors.New("CreateOrder should not be called during verification tests")
}

func (f *fakePaymentProvider) ListOrdersByReceipt(
	ctx context.Context,
	receipt string,
) ([]razorpay.Order, error) {
	return nil, errors.New("ListOrdersByReceipt should not be called during verification tests")
}

func TestServiceVerify(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	_, testPayment := createPaymentWithProviderOrderForTest(
		t,
		repo,
		fixture,
	)

	orderID := *testPayment.ProviderOrderID
	providerPaymentID := "pay_service_test"

	input := VerifyInput{
		PaymentID:         testPayment.ID,
		CustomerID:        fixture.customerID,
		ProviderOrderID:   orderID,
		ProviderPaymentID: providerPaymentID,
		Signature:         "valid-signature",
	}

	validProviderPayment := &razorpay.ProviderPayment{
		ID:       providerPaymentID,
		OrderID:  orderID,
		Amount:   testPayment.AmountPaise,
		Currency: testPayment.Currency,
		Status:   StatusCaptured,
		Captured: true,
	}

	providerError := errors.New("simulated Razorpay API error")

	tests := []struct {
		name               string
		input              VerifyInput
		providerPayment    *razorpay.ProviderPayment
		fetchErr           error
		signatureValid     bool
		expectedErr        error
		expectedFetches    int
		expectedSignatures int
	}{
		{
			name:               "invalid signature",
			input:              input,
			providerPayment:    validProviderPayment,
			signatureValid:     false,
			expectedErr:        ErrInvalidPaymentSignature,
			expectedFetches:    0,
			expectedSignatures: 1,
		},
		{
			name: "wrong order ID",
			input: func() VerifyInput {
				invalid := input
				invalid.ProviderOrderID = "order_wrong"
				return invalid
			}(),
			providerPayment:    validProviderPayment,
			signatureValid:     true,
			expectedErr:        ErrProviderOrderMismatch,
			expectedFetches:    0,
			expectedSignatures: 0,
		},
		{
			name:  "wrong amount",
			input: input,
			providerPayment: &razorpay.ProviderPayment{
				ID:       providerPaymentID,
				OrderID:  orderID,
				Amount:   testPayment.AmountPaise + 100,
				Currency: testPayment.Currency,
				Status:   StatusCaptured,
				Captured: true,
			},
			signatureValid:     true,
			expectedErr:        ErrPaymentAmountMismatch,
			expectedFetches:    1,
			expectedSignatures: 1,
		},
		{
			name:  "wrong currency",
			input: input,
			providerPayment: &razorpay.ProviderPayment{
				ID:       providerPaymentID,
				OrderID:  orderID,
				Amount:   testPayment.AmountPaise,
				Currency: "USD",
				Status:   StatusCaptured,
				Captured: true,
			},
			signatureValid:     true,
			expectedErr:        ErrPaymentCurrencyMismatch,
			expectedFetches:    1,
			expectedSignatures: 1,
		},
		{
			name:  "payment not captured",
			input: input,
			providerPayment: &razorpay.ProviderPayment{
				ID:       providerPaymentID,
				OrderID:  orderID,
				Amount:   testPayment.AmountPaise,
				Currency: testPayment.Currency,
				Status:   StatusAuthorized,
				Captured: false,
			},
			signatureValid:     true,
			expectedErr:        ErrPaymentNotCaptured,
			expectedFetches:    1,
			expectedSignatures: 1,
		},
		{
			name:               "Razorpay API error",
			input:              input,
			fetchErr:           providerError,
			signatureValid:     true,
			expectedErr:        providerError,
			expectedFetches:    1,
			expectedSignatures: 1,
		},
		{
			name:               "successful captured payment",
			input:              input,
			providerPayment:    validProviderPayment,
			signatureValid:     true,
			expectedFetches:    1,
			expectedSignatures: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &fakePaymentProvider{
				fetchedPayment: tt.providerPayment,
				fetchErr:       tt.fetchErr,
				signatureValid: tt.signatureValid,
			}

			orderService := &OrderService{
				repo:     repo,
				provider: provider,
				keyID:    "test_key",
			}

			service, err := NewService(
				repo,
				orderService,
				provider,
			)
			if err != nil {
				t.Fatalf("NewService() error = %v", err)
			}

			result, err := service.Verify(
				context.Background(),
				tt.input,
			)

			if tt.expectedErr != nil {
				if !errors.Is(err, tt.expectedErr) {
					t.Fatalf(
						"expected error %v, got %v",
						tt.expectedErr,
						err,
					)
				}

				if result != nil {
					t.Fatalf("expected nil payment result, got %+v", result)
				}
			} else {
				if err != nil {
					t.Fatalf("Verify() unexpected error: %v", err)
				}

				if result == nil {
					t.Fatal("Verify() returned nil payment")
				}

				if result.Status != StatusCaptured {
					t.Errorf(
						"expected payment status %q, got %q",
						StatusCaptured,
						result.Status,
					)
				}

				if result.FulfillmentStatus != FulfillmentFulfilled {
					t.Errorf(
						"expected fulfillment status %q, got %q",
						FulfillmentFulfilled,
						result.FulfillmentStatus,
					)
				}
			}

			if provider.fetchCalls != tt.expectedFetches {
				t.Errorf(
					"expected %d FetchPayment calls, got %d",
					tt.expectedFetches,
					provider.fetchCalls,
				)
			}

			if provider.signatureCalls != tt.expectedSignatures {
				t.Errorf(
					"expected %d signature verification calls, got %d",
					tt.expectedSignatures,
					provider.signatureCalls,
				)
			}
		})
	}

	// Confirm that the successful test actually confirmed the booking.
	var bookingStatus string

	err := pool.QueryRow(
		context.Background(),
		`SELECT status FROM bookings WHERE id = $1`,
		testPayment.BookingID,
	).Scan(&bookingStatus)

	if err != nil {
		t.Fatalf("failed to retrieve booking status: %v", err)
	}

	if bookingStatus != "confirmed" {
		t.Errorf(
			"expected booking status confirmed, got %q",
			bookingStatus,
		)
	}

	// Ensure only one payment attempt was recorded.
	var attemptCount int

	err = pool.QueryRow(
		context.Background(),
		`SELECT COUNT(*) FROM payment_attempts WHERE payment_id = $1`,
		testPayment.ID,
	).Scan(&attemptCount)

	if err != nil {
		t.Fatalf("failed to retrieve payment attempts: %v", err)
	}

	if attemptCount != 1 {
		t.Errorf("expected 1 payment attempt, got %d", attemptCount)
	}

	_ = fmt.Sprintf("%d", time.Now().UnixNano())
}
