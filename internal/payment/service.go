package payment

import (
	"context"
	"fmt"
	"strings"

	"staynest/internal/payment/razorpay"
)

// PaymentVerificationProvider defines the Razorpay operations
// required to independently verify a captured payment.
type PaymentVerificationProvider interface {
	FetchPayment(
		ctx context.Context,
		paymentID string,
	) (*razorpay.ProviderPayment, error)

	VerifyPaymentSignature(
		orderID string,
		paymentID string,
		signature string,
	) bool
}

type Service struct {
	repo         *Repository
	orderService *OrderService
	provider     PaymentVerificationProvider
}

func NewService(
	repo *Repository,
	orderService *OrderService,
	provider PaymentVerificationProvider,
) (*Service, error) {
	if repo == nil || orderService == nil || provider == nil {
		return nil, ErrInvalidPaymentServiceConfig
	}

	return &Service{
		repo:         repo,
		orderService: orderService,
		provider:     provider,
	}, nil
}

// Initiate creates or retrieves a local payment and ensures that
// its corresponding Razorpay order exists.
func (s *Service) Initiate(
	ctx context.Context,
	input InitiateInput,
) (*CheckoutDetails, error) {
	if s == nil || s.repo == nil || s.orderService == nil {
		return nil, ErrInvalidPaymentServiceConfig
	}

	payment, err := s.repo.CreatePending(ctx, input)
	if err != nil {
		return nil, err
	}

	checkout, err := s.orderService.CreateOrder(
		ctx,
		payment.ID,
		payment.CustomerID,
	)
	if err != nil {
		return nil, err
	}

	return checkout, nil
}

// Verify independently verifies a Razorpay payment before
// fulfilling the corresponding local payment and booking.
func (s *Service) Verify(
	ctx context.Context,
	input VerifyInput,
) (*Payment, error) {
	if s == nil || s.repo == nil || s.provider == nil {
		return nil, ErrInvalidPaymentServiceConfig
	}

	input.PaymentID = strings.TrimSpace(input.PaymentID)
	input.CustomerID = strings.TrimSpace(input.CustomerID)
	input.ProviderOrderID = strings.TrimSpace(input.ProviderOrderID)
	input.ProviderPaymentID = strings.TrimSpace(input.ProviderPaymentID)
	input.Signature = strings.TrimSpace(input.Signature)

	if input.ProviderOrderID == "" {
		return nil, ErrInvalidProviderOrderID
	}

	if input.ProviderPaymentID == "" {
		return nil, ErrInvalidProviderPaymentID
	}

	if input.Signature == "" {
		return nil, ErrInvalidPaymentSignature
	}

	// Retrieve the local payment using the authenticated customer's ID.
	localPayment, err := s.repo.GetByIDForCustomer(
		ctx,
		input.PaymentID,
		input.CustomerID,
	)
	if err != nil {
		return nil, err
	}

	// Never trust the order ID supplied by the browser.
	// The stored order ID is our source of truth.
	if localPayment.ProviderOrderID == nil ||
		strings.TrimSpace(*localPayment.ProviderOrderID) == "" {
		return nil, ErrInvalidProviderOrderID
	}

	storedOrderID := strings.TrimSpace(*localPayment.ProviderOrderID)

	if input.ProviderOrderID != storedOrderID {
		return nil, ErrProviderOrderMismatch
	}

	// Verify the Checkout signature using our stored order ID.
	if !s.provider.VerifyPaymentSignature(
		storedOrderID,
		input.ProviderPaymentID,
		input.Signature,
	) {
		return nil, ErrInvalidPaymentSignature
	}

	// Independently retrieve the payment from Razorpay.
	providerPayment, err := s.provider.FetchPayment(
		ctx,
		input.ProviderPaymentID,
	)
	if err != nil {
		return nil, fmt.Errorf("fetch Razorpay payment: %w", err)
	}

	if providerPayment == nil ||
		strings.TrimSpace(providerPayment.ID) != input.ProviderPaymentID {
		return nil, ErrInvalidProviderPaymentID
	}

	// Verify that Razorpay associates this payment with our order.
	if strings.TrimSpace(providerPayment.OrderID) != storedOrderID {
		return nil, ErrProviderOrderMismatch
	}

	if providerPayment.Amount != localPayment.AmountPaise {
		return nil, ErrPaymentAmountMismatch
	}

	if !strings.EqualFold(
		strings.TrimSpace(providerPayment.Currency),
		localPayment.Currency,
	) {
		return nil, ErrPaymentCurrencyMismatch
	}

	// Both fields must indicate a captured payment.
	if !providerPayment.Captured ||
		!strings.EqualFold(
			strings.TrimSpace(providerPayment.Status),
			StatusCaptured,
		) {
		return nil, ErrPaymentNotCaptured
	}

	// Only independently verified provider data reaches the repository.
	return s.repo.FulfillCapturedPayment(
		ctx,
		CapturedPaymentInput{
			PaymentID:         localPayment.ID,
			CustomerID:        localPayment.CustomerID,
			ProviderOrderID:   storedOrderID,
			ProviderPaymentID: providerPayment.ID,
			AmountPaise:       providerPayment.Amount,
			Currency:          providerPayment.Currency,
		},
	)
}
