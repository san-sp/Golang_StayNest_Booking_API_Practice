package razorpay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateOrderSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}

			if r.URL.Path != "/orders" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}

			username, password, ok := r.BasicAuth()
			if !ok || username != "test-key-id" || password != "test-secret" {
				t.Error("invalid Basic Authentication")
			}

			var input createOrderRequest

			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Errorf("failed to decode request: %v", err)
				return
			}

			if input.Amount != 250000 {
				t.Errorf("expected amount 250000, got %d", input.Amount)
			}

			if input.Currency != "INR" {
				t.Errorf("expected INR, got %s", input.Currency)
			}

			if input.Receipt != "payment-123" {
				t.Errorf("unexpected receipt: %s", input.Receipt)
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)

			_, _ = w.Write([]byte(`{
				"id": "order_test123",
				"amount": 250000,
				"currency": "INR",
				"receipt": "payment-123",
				"status": "created",
				"created_at": 1795000000
			}`))
		},
	))
	defer server.Close()

	client, err := newClient(
		"test-key-id",
		"test-secret",
		server.URL,
		server.Client(),
	)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	order, err := client.CreateOrder(
		context.Background(),
		CreateOrderInput{
			Amount:   250000,
			Currency: "INR",
			Receipt:  "payment-123",
		},
	)

	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	if order.ID != "order_test123" {
		t.Errorf("unexpected order ID: %s", order.ID)
	}

	if order.Amount != 250000 {
		t.Errorf("unexpected amount: %d", order.Amount)
	}

	if order.Status != "created" {
		t.Errorf("unexpected status: %s", order.Status)
	}
}

func TestCreateOrderHandlesAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)

			_, _ = w.Write([]byte(`{
				"error": {
					"code": "BAD_REQUEST_ERROR",
					"description": "Invalid amount",
					"source": "business",
					"step": "payment_initiation",
					"reason": "invalid_amount"
				}
			}`))
		},
	))
	defer server.Close()

	client, err := newClient(
		"test-key-id",
		"test-secret",
		server.URL,
		server.Client(),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.CreateOrder(
		context.Background(),
		CreateOrderInput{
			Amount:   250000,
			Currency: "INR",
			Receipt:  "payment-123",
		},
	)

	var apiErr *APIError

	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %v", err)
	}

	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("unexpected status code: %d", apiErr.StatusCode)
	}

	if apiErr.Code != "BAD_REQUEST_ERROR" {
		t.Errorf("unexpected error code: %s", apiErr.Code)
	}
}

func TestCreateOrderRejectsInvalidInput(t *testing.T) {
	client, err := newClient(
		"test-key-id",
		"test-secret",
		"https://example.invalid",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		input CreateOrderInput
	}{
		{
			name: "zero amount",
			input: CreateOrderInput{
				Amount:   0,
				Currency: "INR",
				Receipt:  "payment-123",
			},
		},
		{
			name: "negative amount",
			input: CreateOrderInput{
				Amount:   -100,
				Currency: "INR",
				Receipt:  "payment-123",
			},
		},
		{
			name: "invalid currency",
			input: CreateOrderInput{
				Amount:   1000,
				Currency: "USD",
				Receipt:  "payment-123",
			},
		},
		{
			name: "empty receipt",
			input: CreateOrderInput{
				Amount:   1000,
				Currency: "INR",
			},
		},
		{
			name: "receipt exceeds 40 characters",
			input: CreateOrderInput{
				Amount:   1000,
				Currency: "INR",
				Receipt:  "this-receipt-is-longer-than-forty-characters",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.CreateOrder(
				context.Background(),
				tt.input,
			)

			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestNewClientRejectsMissingCredentials(t *testing.T) {
	tests := []struct {
		name   string
		keyID  string
		secret string
	}{
		{
			name:   "missing key ID",
			secret: "test-secret",
		},
		{
			name:  "missing key secret",
			keyID: "test-key-id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewClient(
				tt.keyID,
				tt.secret,
				nil,
			)

			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestListOrdersByReceiptSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("expected GET, got %s", r.Method)
			}

			if r.URL.Path != "/orders" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}

			if r.URL.Query().Get("receipt") != "payment-123" {
				t.Errorf("unexpected receipt query: %s", r.URL.RawQuery)
			}

			if r.URL.Query().Get("count") != "100" {
				t.Errorf("unexpected count: %s", r.URL.Query().Get("count"))
			}

			username, password, ok := r.BasicAuth()
			if !ok || username != "test-key-id" || password != "test-secret" {
				t.Error("invalid Basic Authentication")
			}

			w.Header().Set("Content-Type", "application/json")

			_, _ = w.Write([]byte(`{
				"entity": "collection",
				"count": 2,
				"items": [
					{
						"id": "order_exact",
						"amount": 250000,
						"currency": "INR",
						"receipt": "payment-123",
						"status": "created"
					},
					{
						"id": "order_partial",
						"amount": 250000,
						"currency": "INR",
						"receipt": "payment-123-extra",
						"status": "created"
					}
				]
			}`))
		},
	))
	defer server.Close()

	client, err := newClient(
		"test-key-id",
		"test-secret",
		server.URL,
		server.Client(),
	)
	if err != nil {
		t.Fatal(err)
	}

	orders, err := client.ListOrdersByReceipt(
		context.Background(),
		"payment-123",
	)
	if err != nil {
		t.Fatalf("ListOrdersByReceipt() error = %v", err)
	}

	if len(orders) != 1 {
		t.Fatalf("expected 1 exact match, got %d", len(orders))
	}

	if orders[0].ID != "order_exact" {
		t.Errorf("unexpected order ID: %s", orders[0].ID)
	}
}

func TestListOrdersByReceiptReturnsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")

			_, _ = w.Write([]byte(`{
				"entity": "collection",
				"count": 0,
				"items": []
			}`))
		},
	))
	defer server.Close()

	client, err := newClient(
		"test-key-id",
		"test-secret",
		server.URL,
		server.Client(),
	)
	if err != nil {
		t.Fatal(err)
	}

	orders, err := client.ListOrdersByReceipt(
		context.Background(),
		"payment-456",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(orders) != 0 {
		t.Fatalf("expected 0 orders, got %d", len(orders))
	}
}

func TestListOrdersByReceiptHandlesAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)

			_, _ = w.Write([]byte(`{
				"error": {
					"code": "BAD_REQUEST_ERROR",
					"description": "Invalid API credentials"
				}
			}`))
		},
	))
	defer server.Close()

	client, err := newClient(
		"test-key-id",
		"test-secret",
		server.URL,
		server.Client(),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.ListOrdersByReceipt(
		context.Background(),
		"payment-123",
	)

	var apiErr *APIError

	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %v", err)
	}

	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("unexpected status code: %d", apiErr.StatusCode)
	}
}

func TestListOrdersByReceiptRejectsInvalidReceipt(t *testing.T) {
	client, err := newClient(
		"test-key-id",
		"test-secret",
		"https://example.invalid",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		receipt string
	}{
		{
			name:    "empty receipt",
			receipt: "",
		},
		{
			name:    "whitespace receipt",
			receipt: "   ",
		},
		{
			name:    "receipt exceeds 40 characters",
			receipt: strings.Repeat("a", 41),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.ListOrdersByReceipt(
				context.Background(),
				tt.receipt,
			)

			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
