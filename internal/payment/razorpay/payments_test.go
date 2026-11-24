package razorpay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchPaymentSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("expected GET, got %s", r.Method)
			}

			if r.URL.Path != "/payments/pay_test123" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}

			username, password, ok := r.BasicAuth()
			if !ok || username != "test-key-id" || password != "test-secret" {
				t.Error("invalid Basic Authentication")
			}

			w.Header().Set("Content-Type", "application/json")

			_, _ = w.Write([]byte(`{
				"id": "pay_test123",
				"order_id": "order_test123",
				"amount": 250000,
				"currency": "INR",
				"status": "captured",
				"captured": true
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

	payment, err := client.FetchPayment(
		context.Background(),
		"pay_test123",
	)
	if err != nil {
		t.Fatalf("FetchPayment() error = %v", err)
	}

	if payment.ID != "pay_test123" {
		t.Errorf("unexpected payment ID: %s", payment.ID)
	}

	if payment.OrderID != "order_test123" {
		t.Errorf("unexpected order ID: %s", payment.OrderID)
	}

	if payment.Amount != 250000 {
		t.Errorf("unexpected amount: %d", payment.Amount)
	}

	if !payment.Captured {
		t.Error("expected captured payment")
	}
}

func TestFetchPaymentHandlesAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)

			_, _ = w.Write([]byte(`{
				"error": {
					"code": "BAD_REQUEST_ERROR",
					"description": "Payment not found"
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

	_, err = client.FetchPayment(
		context.Background(),
		"pay_missing",
	)

	var apiErr *APIError

	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %v", err)
	}

	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("unexpected status code: %d", apiErr.StatusCode)
	}
}

func TestFetchPaymentRejectsInvalidResponse(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "missing payment ID",
			body: `{"order_id":"order_test123"}`,
		},
		{
			name: "missing order ID",
			body: `{"id":"pay_test123"}`,
		},
		{
			name: "invalid JSON",
			body: `{"id":`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					_, _ = w.Write([]byte(tt.body))
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

			_, err = client.FetchPayment(
				context.Background(),
				"pay_test123",
			)

			if err == nil {
				t.Fatal("expected response validation error")
			}
		})
	}
}

func TestFetchPaymentRejectsEmptyID(t *testing.T) {
	client, err := newClient(
		"test-key-id",
		"test-secret",
		"https://example.invalid",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.FetchPayment(context.Background(), " ")

	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestVerifyPaymentSignature(t *testing.T) {
	client, err := newClient(
		"test-key-id",
		"test-secret",
		"https://example.invalid",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	// This expected signature is generated using the test secret.
	const validSignature = "8accdc514ff957c3ea16c5b6d9245c798af57d5fa57d86a79d5488f30b4e669c"

	tests := []struct {
		name      string
		orderID   string
		paymentID string
		signature string
		want      bool
	}{
		{
			name:      "valid signature",
			orderID:   "order_test123",
			paymentID: "pay_test123",
			signature: validSignature,
			want:      true,
		},
		{
			name:      "wrong order ID",
			orderID:   "order_wrong",
			paymentID: "pay_test123",
			signature: validSignature,
			want:      false,
		},
		{
			name:      "wrong payment ID",
			orderID:   "order_test123",
			paymentID: "pay_wrong",
			signature: validSignature,
			want:      false,
		},
		{
			name:      "invalid signature",
			orderID:   "order_test123",
			paymentID: "pay_test123",
			signature: "invalid",
			want:      false,
		},
		{
			name:      "empty signature",
			orderID:   "order_test123",
			paymentID: "pay_test123",
			signature: "",
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := client.VerifyPaymentSignature(
				tt.orderID,
				tt.paymentID,
				tt.signature,
			)

			if got != tt.want {
				t.Errorf("VerifyPaymentSignature() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProviderPaymentJSONFields(t *testing.T) {
	body := []byte(`{
		"id": "pay_test123",
		"order_id": "order_test123",
		"amount": 250000,
		"currency": "INR",
		"status": "captured",
		"captured": true,
		"error_code": "",
		"error_description": ""
	}`)

	var payment ProviderPayment

	if err := json.Unmarshal(body, &payment); err != nil {
		t.Fatal(err)
	}

	if payment.Status != "captured" || !payment.Captured {
		t.Fatal("provider payment fields were not decoded correctly")
	}
}
