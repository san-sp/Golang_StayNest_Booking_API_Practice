package razorpay

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCreateRefundSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}

			if r.URL.Path != "/payments/pay_test123/refund" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}

			username, password, ok := r.BasicAuth()
			if !ok || username != "test-key-id" || password != "test-secret" {
				t.Error("invalid Basic Authentication")
			}

			if got := r.Header.Get("X-Refund-Idempotency"); got != "refund-test-123" {
				t.Errorf("unexpected idempotency key: %s", got)
			}

			if got := r.Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("unexpected Content-Type: %s", got)
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id": "rfnd_test123",
				"payment_id": "pay_test123",
				"amount": 250000,
				"currency": "INR",
				"status": "processed",
				"receipt": "refund-test",
				"created_at": 1730000000
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

	refund, err := client.CreateRefund(
		context.Background(),
		&CreateRefundInput{
			PaymentID:      "pay_test123",
			AmountPaise:    250000,
			IdempotencyKey: "refund-test-123",
		},
	)
	if err != nil {
		t.Fatalf("CreateRefund() error = %v", err)
	}

	if refund.ID != "rfnd_test123" {
		t.Errorf("unexpected refund ID: %s", refund.ID)
	}

	if refund.PaymentID != "pay_test123" {
		t.Errorf("unexpected payment ID: %s", refund.PaymentID)
	}

	if refund.Amount != 250000 {
		t.Errorf("unexpected amount: %d", refund.Amount)
	}

	if refund.Status != "processed" {
		t.Errorf("unexpected status: %s", refund.Status)
	}
}

func TestCreateRefundRejectsInvalidInput(t *testing.T) {
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
		input *CreateRefundInput
	}{
		{
			name:  "nil input",
			input: nil,
		},
		{
			name: "empty payment ID",
			input: &CreateRefundInput{
				PaymentID:      " ",
				AmountPaise:    100,
				IdempotencyKey: "refund-test-123",
			},
		},
		{
			name: "amount below minimum",
			input: &CreateRefundInput{
				PaymentID:      "pay_test123",
				AmountPaise:    99,
				IdempotencyKey: "refund-test-123",
			},
		},
		{
			name: "idempotency key too short",
			input: &CreateRefundInput{
				PaymentID:      "pay_test123",
				AmountPaise:    100,
				IdempotencyKey: "short",
			},
		},
		{
			name: "invalid idempotency key characters",
			input: &CreateRefundInput{
				PaymentID:      "pay_test123",
				AmountPaise:    100,
				IdempotencyKey: "refund:test:123",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.CreateRefund(
				context.Background(),
				tt.input,
			)

			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestCreateRefundHandlesAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)

			_, _ = w.Write([]byte(`{
				"error": {
					"code": "BAD_REQUEST_ERROR",
					"description": "Refund cannot be created"
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

	_, err = client.CreateRefund(
		context.Background(),
		&CreateRefundInput{
			PaymentID:      "pay_test123",
			AmountPaise:    250000,
			IdempotencyKey: "refund-test-123",
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

func TestCreateRefundRejectsInvalidResponse(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "missing refund ID",
			body: `{"payment_id":"pay_test123","amount":250000}`,
		},
		{
			name: "payment ID mismatch",
			body: `{"id":"rfnd_test123","payment_id":"pay_wrong","amount":250000}`,
		},
		{
			name: "amount mismatch",
			body: `{"id":"rfnd_test123","payment_id":"pay_test123","amount":100}`,
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

			_, err = client.CreateRefund(
				context.Background(),
				&CreateRefundInput{
					PaymentID:      "pay_test123",
					AmountPaise:    250000,
					IdempotencyKey: "refund-test-123",
				},
			)

			if err == nil {
				t.Fatal("expected response validation error")
			}
		})
	}
}

func TestFetchRefundSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("expected GET, got %s", r.Method)
			}

			if r.URL.Path != "/refunds/rfnd_test123" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}

			username, password, ok := r.BasicAuth()
			if !ok || username != "test-key-id" || password != "test-secret" {
				t.Error("invalid Basic Authentication")
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id": "rfnd_test123",
				"payment_id": "pay_test123",
				"amount": 250000,
				"currency": "INR",
				"status": "processed"
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

	refund, err := client.FetchRefund(
		context.Background(),
		"rfnd_test123",
	)
	if err != nil {
		t.Fatalf("FetchRefund() error = %v", err)
	}

	if refund.ID != "rfnd_test123" {
		t.Errorf("unexpected refund ID: %s", refund.ID)
	}

	if refund.PaymentID != "pay_test123" {
		t.Errorf("unexpected payment ID: %s", refund.PaymentID)
	}

	if refund.Amount != 250000 {
		t.Errorf("unexpected amount: %d", refund.Amount)
	}
}

func TestFetchRefundHandlesAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)

			_, _ = w.Write([]byte(`{
				"error": {
					"code": "BAD_REQUEST_ERROR",
					"description": "Refund not found"
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

	_, err = client.FetchRefund(
		context.Background(),
		"rfnd_missing",
	)

	var apiErr *APIError

	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %v", err)
	}

	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("unexpected status code: %d", apiErr.StatusCode)
	}
}

func TestFetchRefundRejectsInvalidResponse(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "refund ID mismatch",
			body: `{"id":"rfnd_wrong"}`,
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

			_, err = client.FetchRefund(
				context.Background(),
				"rfnd_test123",
			)

			if err == nil {
				t.Fatal("expected response validation error")
			}
		})
	}
}

func TestFetchRefundRejectsEmptyID(t *testing.T) {
	client, err := newClient(
		"test-key-id",
		"test-secret",
		"https://example.invalid",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.FetchRefund(
		context.Background(),
		" ",
	)

	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestCreateRefundDoesNotModifyInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{
				"id": "rfnd_test123",
				"payment_id": "pay_test123",
				"amount": 250000
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

	input := &CreateRefundInput{
		PaymentID:      " pay_test123 ",
		AmountPaise:    250000,
		IdempotencyKey: " refund-test-123 ",
	}

	_, err = client.CreateRefund(context.Background(), input)
	if err != nil {
		t.Fatalf("CreateRefund() error = %v", err)
	}

	if input.PaymentID != " pay_test123 " {
		t.Errorf("CreateRefund modified PaymentID: %q", input.PaymentID)
	}

	if input.IdempotencyKey != " refund-test-123 " {
		t.Errorf("CreateRefund modified IdempotencyKey: %q", input.IdempotencyKey)
	}
}

func TestCreateRefundRejectsInvalidKeyCharacters(t *testing.T) {
	client, err := newClient(
		"test-key-id",
		"test-secret",
		"https://example.invalid",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	invalidKeys := []string{
		"refund:test-123",
		"refund test 123",
		"refund/test-123",
		"refund.test-123",
	}

	for _, key := range invalidKeys {
		t.Run(strings.ReplaceAll(key, "/", "_"), func(t *testing.T) {
			_, err := client.CreateRefund(
				context.Background(),
				&CreateRefundInput{
					PaymentID:      "pay_test123",
					AmountPaise:    100,
					IdempotencyKey: key,
				},
			)

			if err == nil {
				t.Fatalf("expected validation error for key %q", key)
			}
		})
	}
}

func assertRefundRequestCancelled(
	t *testing.T,
	request func(context.Context, *Client) error,
) {
	t.Helper()

	requestStarted := make(chan struct{})
	handlerDone := make(chan struct{})
	releaseHandler := make(chan struct{})

	var releaseOnce sync.Once

	release := func() {
		releaseOnce.Do(func() {
			close(releaseHandler)
		})
	}

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			close(requestStarted)

			// Wait for either actual request cancellation
			// or explicit test cleanup.
			select {
			case <-r.Context().Done():
			case <-releaseHandler:
			}

			close(handlerDone)
		},
	))

	defer func() {
		release()
		server.Close()
	}()

	client, err := newClient(
		"test-key-id",
		"test-secret",
		server.URL,
		server.Client(),
	)
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	result := make(chan error, 1)

	go func() {
		result <- request(ctx, client)
	}()

	// Wait until the HTTP request reaches the server.
	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP request did not reach the test server")
	}

	// Cancel the request while it is in flight.
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf(
				"expected context.Canceled, got %v",
				err,
			)
		}

	case <-time.After(2 * time.Second):
		t.Fatal("HTTP request did not stop after cancellation")
	}

	// Release the server handler so cleanup cannot hang.
	release()

	select {
	case <-handlerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("server handler did not exit")
	}
}

func TestCreateRefundRespectsContextCancellation(t *testing.T) {
	assertRefundRequestCancelled(
		t,
		func(ctx context.Context, client *Client) error {
			_, err := client.CreateRefund(
				ctx,
				&CreateRefundInput{
					PaymentID:      "pay_test123",
					AmountPaise:    250000,
					IdempotencyKey: "refund-test-123",
				},
			)

			return err
		},
	)
}

func TestFetchRefundRespectsContextCancellation(t *testing.T) {
	assertRefundRequestCancelled(
		t,
		func(ctx context.Context, client *Client) error {
			_, err := client.FetchRefund(
				ctx,
				"rfnd_test123",
			)

			return err
		},
	)
}
