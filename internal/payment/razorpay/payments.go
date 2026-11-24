package razorpay

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type ProviderPayment struct {
	ID               string `json:"id"`
	OrderID          string `json:"order_id"`
	Amount           int64  `json:"amount"`
	Currency         string `json:"currency"`
	Status           string `json:"status"`
	Captured         bool   `json:"captured"`
	ErrorCode        string `json:"error_code"`
	ErrorDescription string `json:"error_description"`
	ErrorSource      string `json:"error_source"`
	ErrorStep        string `json:"error_step"`
	ErrorReason      string `json:"error_reason"`
}

// FetchPayment retrieves payment details directly from Razorpay.
func (c *Client) FetchPayment(
	ctx context.Context,
	paymentID string,
) (*ProviderPayment, error) {
	paymentID = strings.TrimSpace(paymentID)

	if paymentID == "" {
		return nil, errors.New("payment ID is required")
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		c.baseURL+"/payments/"+paymentID,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create Razorpay payment request: %w", err)
	}

	req.SetBasicAuth(c.keyID, c.keySecret)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send Razorpay payment request: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read Razorpay payment response: %w", err)
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
			"Razorpay payment lookup returned HTTP %d",
			resp.StatusCode,
		)
	}

	var payment ProviderPayment

	if err := json.Unmarshal(responseBody, &payment); err != nil {
		return nil, fmt.Errorf(
			"decode Razorpay payment response: %w",
			err,
		)
	}

	if payment.ID == "" {
		return nil, errors.New("Razorpay response missing payment ID")
	}

	if payment.OrderID == "" {
		return nil, errors.New("Razorpay response missing order ID")
	}

	return &payment, nil
}

// VerifyPaymentSignature validates the Razorpay Standard Checkout signature.
func (c *Client) VerifyPaymentSignature(
	orderID string,
	paymentID string,
	signature string,
) bool {
	orderID = strings.TrimSpace(orderID)
	paymentID = strings.TrimSpace(paymentID)
	signature = strings.TrimSpace(signature)

	if orderID == "" || paymentID == "" || signature == "" {
		return false
	}

	providedSignature, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}

	message := orderID + "|" + paymentID

	mac := hmac.New(sha256.New, []byte(c.keySecret))
	_, _ = mac.Write([]byte(message))

	expectedSignature := mac.Sum(nil)

	return hmac.Equal(providedSignature, expectedSignature)
}
