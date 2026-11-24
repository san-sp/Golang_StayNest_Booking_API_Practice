package payment

import (
	"encoding/json"
	"time"
)

// Payment statuses describe the state of money.
const (
	StatusCreated    = "created"
	StatusAuthorized = "authorized"
	StatusCaptured   = "captured"
	StatusFailed     = "failed"
)

// Fulfillment statuses describe whether the booking was fulfilled.
const (
	FulfillmentPending        = "pending"
	FulfillmentFulfilled      = "fulfilled"
	FulfillmentRefundRequired = "refund_required"
	FulfillmentReviewRequired = "review_required"
)

// Refund statuses describe the refund lifecycle.
const (
	RefundStatusPending        = "pending"
	RefundStatusProcessing     = "processing"
	RefundStatusSucceeded      = "succeeded"
	RefundStatusFailed         = "failed"
	RefundStatusReviewRequired = "review_required"
)

// Webhook processing statuses.
const (
	WebhookStatusReceived   = "received"
	WebhookStatusProcessing = "processing"
	WebhookStatusProcessed  = "processed"
	WebhookStatusFailed     = "failed"
)

const (
	ProviderRazorpay = "razorpay"
	CurrencyINR      = "INR"
)

const BookingStatusPendingPayment = "pending_payment"

// Payment represents the payment associated with a booking.
type Payment struct {
	ID                string
	BookingID         string
	CustomerID        string
	AmountPaise       int64
	Currency          string
	Provider          string
	ProviderOrderID   *string
	IdempotencyKey    string
	Status            string
	FulfillmentStatus string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// PaymentAttempt represents an individual attempt to pay.
type PaymentAttempt struct {
	ID                 string
	PaymentID          string
	Provider           string
	ProviderPaymentID  *string
	AmountPaise        int64
	Currency           string
	Status             string
	FailureCode        *string
	FailureDescription *string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// WebhookEvent represents an event received from a payment provider.
type WebhookEvent struct {
	ID               string
	PaymentID        *string
	Provider         string
	ProviderEventID  string
	EventType        string
	ProcessingStatus string
	Payload          json.RawMessage
	ProcessedAt      *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Refund represents a refund requested for a captured payment.
type Refund struct {
	ID                       string
	PaymentID                string
	Provider                 string
	ProviderPaymentID        string
	ProviderRefundID         *string
	IdempotencyKey           string
	AmountPaise              int64
	Currency                 string
	Status                   string
	AttemptCount             int
	LastError                *string
	CreatedAt                time.Time
	UpdatedAt                time.Time
	ProcessingLeaseToken     *string
	ProcessingLeaseExpiresAt *time.Time
	NextAttemptAt            time.Time
}

// InitiateInput contains the values required to initiate payment.
// CustomerID comes from the authenticated customer, not the request body.
type InitiateInput struct {
	BookingID      string
	CustomerID     string
	IdempotencyKey string
}

// VerifyInput contains the payment details returned by Razorpay Checkout.
// These values must be independently verified with Razorpay.
type VerifyInput struct {
	PaymentID         string
	CustomerID        string
	ProviderOrderID   string
	ProviderPaymentID string
	Signature         string
}

// CapturedPaymentInput contains independently verified Razorpay
// payment details required for transactional booking fulfillment.
type CapturedPaymentInput struct {
	PaymentID         string
	CustomerID        string
	ProviderOrderID   string
	ProviderPaymentID string
	AmountPaise       int64
	Currency          string
}

// CheckoutDetails contains the information the frontend needs
// to open Razorpay Checkout.
type CheckoutDetails struct {
	PaymentID       string
	ProviderOrderID string
	AmountPaise     int64
	Currency        string
	KeyID           string
}
