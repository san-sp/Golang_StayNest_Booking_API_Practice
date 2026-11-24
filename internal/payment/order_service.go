package payment

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"staynest/internal/payment/razorpay"
)

const providerOrderLeaseDuration = 60 * time.Second

const providerOrderLeaseCleanupTimeout = 5 * time.Second

// OrderProvider defines the Razorpay operations required by OrderService.
// Using an interface allows us to test the service without calling Razorpay.
type OrderProvider interface {
	CreateOrder(
		ctx context.Context,
		input razorpay.CreateOrderInput,
	) (*razorpay.Order, error)

	ListOrdersByReceipt(
		ctx context.Context,
		receipt string,
	) ([]razorpay.Order, error)
}

type OrderService struct {
	repo     *Repository
	provider OrderProvider
	keyID    string
}

func NewOrderService(
	repo *Repository,
	provider OrderProvider,
	keyID string,
) (*OrderService, error) {
	keyID = strings.TrimSpace(keyID)

	if repo == nil || provider == nil || keyID == "" {
		return nil, ErrInvalidOrderServiceConfig
	}

	return &OrderService{
		repo:     repo,
		provider: provider,
		keyID:    keyID,
	}, nil
}

// CreateOrder creates or recovers the Razorpay order associated with
// an existing payment.
//
// It never blindly creates another provider order after an ambiguous
// API outcome. It first reconciles using the payment UUID as receipt.
func (s *OrderService) CreateOrder(
	ctx context.Context,
	paymentID string,
	customerID string,
) (details *CheckoutDetails, retErr error) {
	payment, err := s.repo.GetByIDForCustomer(
		ctx,
		paymentID,
		customerID,
	)
	if err != nil {
		return nil, err
	}

	// An order may already have been created by an earlier request.
	if payment.ProviderOrderID != nil {
		return s.checkoutDetails(payment, *payment.ProviderOrderID), nil
	}

	leaseToken, err := s.repo.AcquireProviderOrderLease(
		ctx,
		payment.ID,
		providerOrderLeaseDuration,
	)
	if err != nil {
		if errors.Is(err, ErrProviderOrderAlreadyExists) {
			// Another request may have saved the order between
			// our initial lookup and lease acquisition.
			payment, getErr := s.repo.GetByIDForCustomer(
				ctx,
				payment.ID,
				customerID,
			)
			if getErr != nil {
				return nil, getErr
			}

			if payment.ProviderOrderID != nil {
				return s.checkoutDetails(
					payment,
					*payment.ProviderOrderID,
				), nil
			}
		}

		return nil, err
	}

	// Always attempt lease cleanup, even if the incoming request
	// context has been cancelled.
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			providerOrderLeaseCleanupTimeout,
		)
		defer cancel()

		releaseErr := s.repo.ReleaseProviderOrderLease(
			cleanupCtx,
			payment.ID,
			leaseToken,
		)

		if releaseErr != nil {
			retErr = errors.Join(
				retErr,
				fmt.Errorf("release provider order lease: %w", releaseErr),
			)
		}
	}()

	// First look for an order created by a previous request whose
	// response was lost or whose database save failed.
	order, err := s.findExistingOrder(ctx, payment)
	if err != nil {
		return nil, err
	}

	if order != nil {
		return s.saveOrderAndBuildCheckout(
			ctx,
			payment,
			leaseToken,
			order,
		)
	}

	// No existing order was found. Create one using the local
	// payment UUID as Razorpay's unique receipt.
	order, createErr := s.provider.CreateOrder(
		ctx,
		razorpay.CreateOrderInput{
			Amount:   payment.AmountPaise,
			Currency: payment.Currency,
			Receipt:  payment.ID,
		},
	)

	if createErr != nil {
		// The request may have reached Razorpay even though
		// we did not receive its response.
		existingOrder, lookupErr := s.findExistingOrder(
			ctx,
			payment,
		)

		if lookupErr != nil {
			return nil, errors.Join(
				fmt.Errorf("create Razorpay order: %w", createErr),
				fmt.Errorf("reconcile Razorpay order: %w", lookupErr),
			)
		}

		if existingOrder == nil {
			return nil, fmt.Errorf(
				"create Razorpay order: %w",
				createErr,
			)
		}

		order = existingOrder
	}

	return s.saveOrderAndBuildCheckout(
		ctx,
		payment,
		leaseToken,
		order,
	)
}

// findExistingOrder searches Razorpay using the payment UUID.
// It returns nil when no matching order exists.
func (s *OrderService) findExistingOrder(
	ctx context.Context,
	payment *Payment,
) (*razorpay.Order, error) {
	orders, err := s.provider.ListOrdersByReceipt(
		ctx,
		payment.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("find Razorpay order by receipt: %w", err)
	}

	switch len(orders) {
	case 0:
		return nil, nil

	case 1:
		if err := validateProviderOrder(
			payment,
			&orders[0],
		); err != nil {
			return nil, err
		}

		return &orders[0], nil

	default:
		return nil, ErrMultipleProviderOrdersFound
	}
}

func validateProviderOrder(
	payment *Payment,
	order *razorpay.Order,
) error {
	if order == nil || strings.TrimSpace(order.ID) == "" {
		return ErrInvalidProviderOrderID
	}

	if order.Receipt != payment.ID {
		return ErrProviderOrderMismatch
	}

	if order.Amount != payment.AmountPaise {
		return ErrPaymentAmountMismatch
	}

	if order.Currency != payment.Currency {
		return ErrPaymentCurrencyMismatch
	}

	return nil
}

func (s *OrderService) saveOrderAndBuildCheckout(
	ctx context.Context,
	payment *Payment,
	leaseToken string,
	order *razorpay.Order,
) (*CheckoutDetails, error) {
	if err := validateProviderOrder(payment, order); err != nil {
		return nil, err
	}

	err := s.repo.SaveProviderOrderID(
		ctx,
		payment.ID,
		leaseToken,
		order.ID,
	)
	if err != nil {
		return nil, err
	}

	return s.checkoutDetails(payment, order.ID), nil
}

func (s *OrderService) checkoutDetails(
	payment *Payment,
	providerOrderID string,
) *CheckoutDetails {
	return &CheckoutDetails{
		PaymentID:       payment.ID,
		ProviderOrderID: providerOrderID,
		AmountPaise:     payment.AmountPaise,
		Currency:        payment.Currency,
		KeyID:           s.keyID,
	}
}
