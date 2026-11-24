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

type CreateOrderInput struct {
	Amount   int64
	Currency string
	Receipt  string
	Notes    map[string]string
}

type Order struct {
	ID        string `json:"id"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	Receipt   string `json:"receipt"`
	Status    string `json:"status"`
	CreatedAt int64  `json:"created_at"`
}

type createOrderRequest struct {
	Amount   int64             `json:"amount"`
	Currency string            `json:"currency"`
	Receipt  string            `json:"receipt,omitempty"`
	Notes    map[string]string `json:"notes,omitempty"`
}

type apiErrorResponse struct {
	Error struct {
		Code        string `json:"code"`
		Description string `json:"description"`
		Source      string `json:"source"`
		Step        string `json:"step"`
		Reason      string `json:"reason"`
	} `json:"error"`
}

type APIError struct {
	StatusCode  int
	Code        string
	Description string
	Source      string
	Step        string
	Reason      string
}

type orderCollection struct {
	Count int     `json:"count"`
	Items []Order `json:"items"`
}

func (e *APIError) Error() string {
	if e.Description != "" {
		return fmt.Sprintf(
			"Razorpay API error: HTTP %d: %s",
			e.StatusCode,
			e.Description,
		)
	}

	return fmt.Sprintf(
		"Razorpay API error: HTTP %d",
		e.StatusCode,
	)
}

func (c *Client) CreateOrder(
	ctx context.Context,
	input CreateOrderInput,
) (*Order, error) {
	input.Currency = strings.TrimSpace(input.Currency)
	input.Receipt = strings.TrimSpace(input.Receipt)

	if input.Amount <= 0 {
		return nil, errors.New("order amount must be positive")
	}

	if input.Currency != "INR" {
		return nil, errors.New("order currency must be INR")
	}

	if input.Receipt == "" {
		return nil, errors.New("order receipt is required")
	}

	if len(input.Receipt) > 40 {
		return nil, errors.New("order receipt cannot exceed 40 characters")
	}

	payload := createOrderRequest{
		Amount:   input.Amount,
		Currency: input.Currency,
		Receipt:  input.Receipt,
		Notes:    input.Notes,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal Razorpay order request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/orders",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("create Razorpay order request: %w", err)
	}

	req.SetBasicAuth(c.keyID, c.keySecret)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send Razorpay order request: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(
		io.LimitReader(resp.Body, 1<<20),
	)
	if err != nil {
		return nil, fmt.Errorf("read Razorpay response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr apiErrorResponse

		if err := json.Unmarshal(responseBody, &apiErr); err != nil {
			return nil, fmt.Errorf(
				"Razorpay returned HTTP %d",
				resp.StatusCode,
			)
		}

		return nil, &APIError{
			StatusCode:  resp.StatusCode,
			Code:        apiErr.Error.Code,
			Description: apiErr.Error.Description,
			Source:      apiErr.Error.Source,
			Step:        apiErr.Error.Step,
			Reason:      apiErr.Error.Reason,
		}
	}

	var order Order

	if err := json.Unmarshal(responseBody, &order); err != nil {
		return nil, fmt.Errorf("decode Razorpay order response: %w", err)
	}

	if order.ID == "" {
		return nil, errors.New("Razorpay response missing order ID")
	}

	if order.Amount != input.Amount {
		return nil, errors.New("Razorpay order amount mismatch")
	}

	if order.Currency != input.Currency {
		return nil, errors.New("Razorpay order currency mismatch")
	}

	if order.Receipt != input.Receipt {
		return nil, errors.New("Razorpay order receipt mismatch")
	}

	return &order, nil
}

func (c *Client) ListOrdersByReceipt(
	ctx context.Context,
	receipt string,
) ([]Order, error) {
	receipt = strings.TrimSpace(receipt)

	if receipt == "" {
		return nil, errors.New("order receipt is required")
	}

	if len(receipt) > 40 {
		return nil, errors.New("order receipt cannot exceed 40 characters")
	}

	query := url.Values{}
	query.Set("receipt", receipt)
	query.Set("count", "100")

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		c.baseURL+"/orders?"+query.Encode(),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create Razorpay order lookup request: %w", err)
	}

	req.SetBasicAuth(c.keyID, c.keySecret)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send Razorpay order lookup request: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read Razorpay response: %w", err)
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
			"Razorpay order lookup returned HTTP %d",
			resp.StatusCode,
		)
	}

	var collection orderCollection

	if err := json.Unmarshal(responseBody, &collection); err != nil {
		return nil, fmt.Errorf("decode Razorpay order lookup response: %w", err)
	}

	orders := make([]Order, 0, len(collection.Items))

	for _, order := range collection.Items {
		if order.Receipt == receipt {
			orders = append(orders, order)
		}
	}

	return orders, nil
}
