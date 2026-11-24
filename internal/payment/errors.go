package payment

import "errors"

var (
	ErrInvalidBookingID      = errors.New("booking ID is required")
	ErrInvalidCustomerID     = errors.New("customer ID is required")
	ErrInvalidIdempotencyKey = errors.New("idempotency key is required")

	ErrPaymentNotFound          = errors.New("payment not found")
	ErrBookingNotFound          = errors.New("booking not found")
	ErrBookingNotPayable        = errors.New("booking cannot be paid")
	ErrPaymentAlreadyCaptured   = errors.New("payment has already been captured")
	ErrPaymentAlreadyExists     = errors.New("payment already exists")
	ErrPaymentOwnershipMismatch = errors.New("payment does not belong to customer")

	ErrInvalidProviderOrderID   = errors.New("provider order ID is required")
	ErrInvalidProviderPaymentID = errors.New("provider payment ID is required")
	ErrInvalidPaymentSignature  = errors.New("invalid payment signature")
	ErrPaymentAmountMismatch    = errors.New("payment amount does not match")
	ErrPaymentCurrencyMismatch  = errors.New("payment currency does not match")
	ErrProviderOrderMismatch    = errors.New("provider order does not match")
	ErrPaymentNotCaptured       = errors.New("provider payment has not been captured")

	ErrInvalidWebhookSignature = errors.New("invalid webhook signature")
	ErrInvalidWebhookPayload   = errors.New("invalid webhook payload")
	ErrDuplicateWebhookEvent   = errors.New("duplicate webhook event")

	ErrRefundNotFound      = errors.New("refund not found")
	ErrRefundAlreadyExists = errors.New("refund already exists")
	ErrInvalidRefundAmount = errors.New("invalid refund amount")

	ErrIdempotencyKeyConflict = errors.New(
		"idempotency key was already used for another booking",
	)
	ErrInvalidPaymentAmount = errors.New("invalid payment amount")

	ErrProviderOrderAlreadyExists = errors.New(
		"provider order has already been created",
	)
	ErrOrderCreationInProgress = errors.New(
		"provider order creation is already in progress",
	)
	ErrOrderCreationLeaseLost = errors.New(
		"provider order creation lease has been lost",
	)
	ErrInvalidOrderLeaseDuration = errors.New(
		"invalid provider order lease duration",
	)
	ErrInvalidOrderLeaseToken = errors.New(
		"provider order lease token is required",
	)

	ErrMultipleProviderOrdersFound = errors.New(
		"multiple provider orders found for payment receipt",
	)

	ErrInvalidOrderServiceConfig = errors.New(
		"invalid payment order service configuration",
	)

	ErrInvalidPaymentServiceConfig = errors.New(
		"invalid payment service configuration",
	)

	ErrProviderPaymentIDConflict = errors.New(
		"provider payment ID is already associated with another payment",
	)

	ErrWebhookEventConflict = errors.New(
		"webhook event ID was received with a different payload",
	)
	ErrWebhookEventNotFound        = errors.New("webhook event not found")
	ErrInvalidWebhookServiceConfig = errors.New(
		"invalid payment webhook service configuration",
	)
	ErrInvalidRefundClaimBatchSize = errors.New(
		"refund claim batch size must be positive",
	)

	ErrInvalidRefundLeaseDuration = errors.New(
		"refund lease duration must be positive",
	)

	ErrRefundLeaseLost = errors.New(
		"refund processing lease has been lost",
	)
	ErrProviderRefundIDConflict = errors.New(
		"provider refund ID conflicts with the existing refund",
	)

	ErrInvalidRefundServiceConfig = errors.New(
		"invalid payment refund service configuration",
	)

	ErrInvalidRefundBatchSize = errors.New(
		"refund processing batch size must be positive",
	)

	ErrInvalidRefundRetryDelay = errors.New(
		"refund retry delay must be positive",
	)

	ErrInvalidProviderRefund = errors.New(
		"invalid provider refund response",
	)
)
