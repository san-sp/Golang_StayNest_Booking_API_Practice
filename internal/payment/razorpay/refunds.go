package razorpay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type CreateRefundInput struct {
	PaymentID      string
	AmountPaise    int64
	IdempotencyKey string
}

type Refund struct {
	ID             string `json:"id"`
	PaymentID      string `json:"payment_id"`
	Amount         int64  `json:"amount"`
	Currency       string `json:"currency"`
	Status         string `json:"status"`
	Receipt        string `json:"receipt"`
	CreatedAt      int64  `json:"created_at"`
	SpeedRequested string `json:"speed_requested"`
	SpeedProcessed string `json:"speed_processed"`
}

type createRefundRequest struct {
	Amount int64 `json:"amount"`
}

// CreateRefund creates an idempotent normal refund through Razorpay.
func (c *Client) CreateRefund(
	ctx context.Context,
	input *CreateRefundInput,
) (*Refund, error) {
	if input == nil {
		return nil, errors.New("refund input is required")
	}

	paymentID := strings.TrimSpace(input.PaymentID)
	idempotencyKey := strings.TrimSpace(input.IdempotencyKey)

	if paymentID == "" {
		return nil, errors.New("payment ID is required")
	}

	if input.AmountPaise < 100 {
		return nil, errors.New("refund amount must be at least 100 paise")
	}

	if len(idempotencyKey) < 10 {
		return nil, errors.New("refund idempotency key must contain at least 10 characters")
	}

	for _, ch := range idempotencyKey {
		if !((ch >= 'a' && ch <= 'z') ||
			(ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') ||
			ch == '-' || ch == '_') {
			return nil, errors.New("refund idempotency key contains invalid characters")
		}
	}

	payload, err := json.Marshal(createRefundRequest{
		Amount: input.AmountPaise,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal refund request: %w", err)
	}

	escapedPaymentID := url.PathEscape(paymentID)

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/payments/"+escapedPaymentID+"/refund",
		bytes.NewReader(payload),
	)
	if err != nil {
		return nil, fmt.Errorf("create refund request: %w", err)
	}

	req.SetBasicAuth(c.keyID, c.keySecret)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Refund-Idempotency", idempotencyKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send refund request: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(
		io.LimitReader(resp.Body, 1<<20),
	)
	if err != nil {
		return nil, fmt.Errorf("read refund response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr apiErrorResponse

		if json.Unmarshal(responseBody, &apiErr) == nil {
			return nil, &APIError{
				StatusCode:  resp.StatusCode,
				Code:        apiErr.Error.Code,
				Description: apiErr.Error.Description,
				Source:      apiErr.Error.Source,
				Step:        apiErr.Error.Step,
				Reason:      apiErr.Error.Reason,
			}
		}

		return nil, fmt.Errorf(
			"Razorpay refund returned HTTP %d",
			resp.StatusCode,
		)
	}

	var refund Refund

	if err := json.Unmarshal(responseBody, &refund); err != nil {
		return nil, fmt.Errorf("decode refund response: %w", err)
	}

	if refund.ID == "" {
		return nil, errors.New("Razorpay response missing refund ID")
	}

	if refund.PaymentID != paymentID {
		return nil, errors.New("Razorpay refund payment ID mismatch")
	}

	if refund.Amount != input.AmountPaise {
		return nil, errors.New("Razorpay refund amount mismatch")
	}

	return &refund, nil
}

// FetchRefund retrieves a refund directly from Razorpay.
func (c *Client) FetchRefund(
	ctx context.Context,
	refundID string,
) (*Refund, error) {
	refundID = strings.TrimSpace(refundID)

	if refundID == "" {
		return nil, errors.New("refund ID is required")
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		c.baseURL+"/refunds/"+url.PathEscape(refundID),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create refund lookup request: %w", err)
	}

	req.SetBasicAuth(c.keyID, c.keySecret)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send refund lookup request: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(
		io.LimitReader(resp.Body, 1<<20),
	)
	if err != nil {
		return nil, fmt.Errorf("read refund lookup response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr apiErrorResponse

		if json.Unmarshal(responseBody, &apiErr) == nil {
			return nil, &APIError{
				StatusCode:  resp.StatusCode,
				Code:        apiErr.Error.Code,
				Description: apiErr.Error.Description,
				Source:      apiErr.Error.Source,
				Step:        apiErr.Error.Step,
				Reason:      apiErr.Error.Reason,
			}
		}

		return nil, fmt.Errorf(
			"Razorpay refund lookup returned HTTP %d",
			resp.StatusCode,
		)
	}

	var refund Refund

	if err := json.Unmarshal(responseBody, &refund); err != nil {
		return nil, fmt.Errorf("decode refund lookup response: %w", err)
	}

	if refund.ID != refundID {
		return nil, errors.New("Razorpay refund ID mismatch")
	}

	return &refund, nil
}
