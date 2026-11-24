package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"staynest/internal/payment/razorpay"
)

// WebhookService processes authenticated Razorpay webhook events.
type WebhookService struct {
	repo          *Repository
	webhookSecret string
}

// NewWebhookService creates a webhook service.
func NewWebhookService(
	repo *Repository,
	webhookSecret string,
) (*WebhookService, error) {
	webhookSecret = strings.TrimSpace(webhookSecret)

	if repo == nil || webhookSecret == "" {
		return nil, ErrInvalidWebhookServiceConfig
	}

	return &WebhookService{
		repo:          repo,
		webhookSecret: webhookSecret,
	}, nil
}

type razorpayWebhookPayload struct {
	Event   string `json:"event"`
	Payload struct {
		Payment struct {
			Entity razorpayWebhookPayment `json:"entity"`
		} `json:"payment"`
	} `json:"payload"`
}

type razorpayWebhookPayment struct {
	ID       string `json:"id"`
	OrderID  string `json:"order_id"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	Status   string `json:"status"`
	Captured bool   `json:"captured"`
}

// Process verifies and processes a Razorpay webhook delivery.
//
// The original request body must be passed unchanged because
// Razorpay calculates its signature using the raw request bytes.
func (s *WebhookService) Process(
	ctx context.Context,
	eventID string,
	signature string,
	body []byte,
) error {
	if s == nil || s.repo == nil || s.webhookSecret == "" {
		return ErrInvalidWebhookServiceConfig
	}

	eventID = strings.TrimSpace(eventID)

	if eventID == "" {
		return ErrInvalidWebhookPayload
	}

	// Authenticate the original request body before trusting its contents.
	if !razorpay.VerifyWebhookSignature(
		body,
		signature,
		s.webhookSecret,
	) {
		return ErrInvalidWebhookSignature
	}

	var payload razorpayWebhookPayload

	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("%w: invalid JSON: %v", ErrInvalidWebhookPayload, err)
	}

	eventType := strings.TrimSpace(payload.Event)

	if eventType == "" || len(eventType) > 100 {
		return ErrInvalidWebhookPayload
	}

	// Persist the event before processing it.
	// Duplicate deliveries retrieve the original stored payload.
	event, _, err := s.repo.RecordWebhookEvent(
		ctx,
		eventID,
		eventType,
		json.RawMessage(body),
	)
	if err != nil {
		return err
	}

	// A previously completed delivery needs no further work.
	if event.ProcessingStatus == WebhookStatusProcessed {
		return nil
	}

	// We currently process payment.captured.
	// Other valid Razorpay events are recorded and acknowledged.
	if event.EventType != "payment.captured" {
		return s.repo.MarkWebhookEventProcessed(
			ctx,
			event.ProviderEventID,
			"",
		)
	}

	providerPayment := payload.Payload.Payment.Entity

	providerPayment.ID = strings.TrimSpace(providerPayment.ID)
	providerPayment.OrderID = strings.TrimSpace(providerPayment.OrderID)
	providerPayment.Currency = strings.TrimSpace(providerPayment.Currency)
	providerPayment.Status = strings.TrimSpace(providerPayment.Status)

	if providerPayment.ID == "" ||
		providerPayment.OrderID == "" ||
		providerPayment.Amount <= 0 ||
		providerPayment.Currency == "" {
		return s.failEvent(ctx, eventID, ErrInvalidWebhookPayload)
	}

	if !providerPayment.Captured ||
		!strings.EqualFold(providerPayment.Status, StatusCaptured) {
		return s.failEvent(ctx, eventID, ErrPaymentNotCaptured)
	}

	// Find the local payment using the Razorpay order ID.
	localPayment, err := s.repo.GetByProviderOrderID(
		ctx,
		providerPayment.OrderID,
	)
	if err != nil {
		return s.failEvent(ctx, eventID, err)
	}

	// Use provider-signed payment information and our locally
	// stored customer/payment information.
	_, err = s.repo.FulfillCapturedPayment(
		ctx,
		CapturedPaymentInput{
			PaymentID:         localPayment.ID,
			CustomerID:        localPayment.CustomerID,
			ProviderOrderID:   providerPayment.OrderID,
			ProviderPaymentID: providerPayment.ID,
			AmountPaise:       providerPayment.Amount,
			Currency:          providerPayment.Currency,
		},
	)
	if err != nil {
		return s.failEvent(ctx, eventID, err)
	}

	// Fulfillment succeeded. Now mark the event processed.
	return s.repo.MarkWebhookEventProcessed(
		ctx,
		eventID,
		localPayment.ID,
	)
}

// failEvent records a processing failure so a later delivery can retry.
func (s *WebhookService) failEvent(
	ctx context.Context,
	eventID string,
	cause error,
) error {
	if err := s.repo.MarkWebhookEventFailed(ctx, eventID); err != nil {
		return fmt.Errorf(
			"%w; additionally failed to record webhook failure: %v",
			cause,
			err,
		)
	}

	return cause
}
