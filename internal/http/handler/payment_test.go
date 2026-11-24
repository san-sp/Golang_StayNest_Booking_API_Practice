package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"staynest/internal/payment"
)

type fakePaymentService struct {
	input           payment.InitiateInput
	checkout        *payment.CheckoutDetails
	err             error
	called          bool
	verifyInput     payment.VerifyInput
	verifiedPayment *payment.Payment
	verifyErr       error
	verifyCalled    bool
}

func (f *fakePaymentService) Initiate(
	_ context.Context,
	input payment.InitiateInput,
) (*payment.CheckoutDetails, error) {
	f.called = true
	f.input = input
	return f.checkout, f.err
}

func newPaymentHandlerTestRouter(
	service PaymentService,
	authenticated bool,
) *gin.Engine {
	gin.SetMode(gin.TestMode)

	r := gin.New()

	r.POST("/bookings/:id/payments", func(c *gin.Context) {
		if authenticated {
			c.Set("customer_id", "customer-123")
		}
		c.Next()
	}, InitiatePayment(service))

	r.POST("/payments/verify", func(c *gin.Context) {
		if authenticated {
			c.Set("customer_id", "customer-123")
		}
		c.Next()
	}, VerifyPayment(service))

	return r
}

func (f *fakePaymentService) Verify(
	_ context.Context,
	input payment.VerifyInput,
) (*payment.Payment, error) {
	f.verifyCalled = true
	f.verifyInput = input
	return f.verifiedPayment, f.verifyErr
}

func TestInitiatePaymentSuccess(t *testing.T) {
	service := &fakePaymentService{
		checkout: &payment.CheckoutDetails{
			PaymentID:       "payment-123",
			ProviderOrderID: "order_123",
			AmountPaise:     150000,
			Currency:        "INR",
			KeyID:           "rzp_test_key",
		},
	}

	r := newPaymentHandlerTestRouter(service, true)

	body := `{"customer_id":"attacker-customer"}`
	req := httptest.NewRequest(
		http.MethodPost,
		"/bookings/booking-123/payments",
		strings.NewReader(body),
	)
	req.Header.Set("Idempotency-Key", "checkout-request-123")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s",
			http.StatusOK, rec.Code, rec.Body.String())
	}

	if !service.called {
		t.Fatal("expected payment service to be called")
	}

	if service.input.BookingID != "booking-123" {
		t.Errorf("expected booking ID booking-123, got %q",
			service.input.BookingID)
	}

	if service.input.CustomerID != "customer-123" {
		t.Errorf("expected authenticated customer ID customer-123, got %q",
			service.input.CustomerID)
	}

	if service.input.IdempotencyKey != "checkout-request-123" {
		t.Errorf("unexpected idempotency key: %q",
			service.input.IdempotencyKey)
	}

	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	expected := map[string]any{
		"payment_id":        "payment-123",
		"provider_order_id": "order_123",
		"amount_paise":      float64(150000),
		"currency":          "INR",
		"key_id":            "rzp_test_key",
	}

	for key, want := range expected {
		if got := response[key]; got != want {
			t.Errorf("response[%q] = %v, want %v", key, got, want)
		}
	}
}

func TestInitiatePaymentRequiresAuthentication(t *testing.T) {
	service := &fakePaymentService{}
	r := newPaymentHandlerTestRouter(service, false)

	req := httptest.NewRequest(
		http.MethodPost,
		"/bookings/booking-123/payments",
		nil,
	)
	req.Header.Set("Idempotency-Key", "request-123")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d",
			http.StatusUnauthorized, rec.Code)
	}

	if service.called {
		t.Fatal("payment service must not be called without authentication")
	}
}

func TestInitiatePaymentRequiresIdempotencyKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{name: "missing key", key: ""},
		{name: "key too long", key: strings.Repeat("a", 129)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakePaymentService{}
			r := newPaymentHandlerTestRouter(service, true)

			req := httptest.NewRequest(
				http.MethodPost,
				"/bookings/booking-123/payments",
				nil,
			)
			if tt.key != "" {
				req.Header.Set("Idempotency-Key", tt.key)
			}

			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d",
					http.StatusBadRequest, rec.Code)
			}

			if service.called {
				t.Fatal("payment service must not be called for an invalid idempotency key")
			}
		})
	}
}

func TestInitiatePaymentMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		serviceErr error
		wantStatus int
	}{
		{
			name:       "invalid booking ID",
			serviceErr: payment.ErrInvalidBookingID,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "booking not found",
			serviceErr: payment.ErrBookingNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "booking not payable",
			serviceErr: payment.ErrBookingNotPayable,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "idempotency conflict",
			serviceErr: payment.ErrIdempotencyKeyConflict,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "order creation in progress",
			serviceErr: payment.ErrOrderCreationInProgress,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "unexpected error",
			serviceErr: errors.New("database connection failed"),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakePaymentService{err: tt.serviceErr}
			r := newPaymentHandlerTestRouter(service, true)

			req := httptest.NewRequest(
				http.MethodPost,
				"/bookings/booking-123/payments",
				nil,
			)
			req.Header.Set("Idempotency-Key", "request-123")

			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d: %s",
					tt.wantStatus, rec.Code, rec.Body.String())
			}

			if !service.called {
				t.Fatal("expected payment service to be called")
			}
		})
	}
}

func TestInitiatePaymentRejectsNilCheckout(t *testing.T) {
	service := &fakePaymentService{}
	r := newPaymentHandlerTestRouter(service, true)

	req := httptest.NewRequest(
		http.MethodPost,
		"/bookings/booking-123/payments",
		nil,
	)
	req.Header.Set("Idempotency-Key", "request-123")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d",
			http.StatusInternalServerError, rec.Code)
	}

	if !service.called {
		t.Fatal("expected payment service to be called")
	}
}

func TestVerifyPaymentSuccess(t *testing.T) {
	service := &fakePaymentService{
		verifiedPayment: &payment.Payment{
			ID:                "payment-123",
			BookingID:         "booking-123",
			CustomerID:        "customer-123",
			Status:            payment.StatusCaptured,
			FulfillmentStatus: payment.FulfillmentFulfilled,
		},
	}

	r := newPaymentHandlerTestRouter(service, true)

	body := `{
		"payment_id": "payment-123",
		"provider_order_id": "order_123",
		"provider_payment_id": "pay_123",
		"signature": "valid-signature"
	}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/payments/verify",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s",
			http.StatusOK, rec.Code, rec.Body.String())
	}

	if !service.verifyCalled {
		t.Fatal("expected Verify service to be called")
	}

	if service.verifyInput.PaymentID != "payment-123" {
		t.Errorf("unexpected payment ID: %q", service.verifyInput.PaymentID)
	}

	if service.verifyInput.CustomerID != "customer-123" {
		t.Errorf("expected authenticated customer ID, got %q",
			service.verifyInput.CustomerID)
	}

	if service.verifyInput.ProviderOrderID != "order_123" {
		t.Errorf("unexpected provider order ID: %q",
			service.verifyInput.ProviderOrderID)
	}

	if service.verifyInput.ProviderPaymentID != "pay_123" {
		t.Errorf("unexpected provider payment ID: %q",
			service.verifyInput.ProviderPaymentID)
	}

	if service.verifyInput.Signature != "valid-signature" {
		t.Error("unexpected payment signature")
	}

	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	expected := map[string]any{
		"payment_id":         "payment-123",
		"booking_id":         "booking-123",
		"status":             payment.StatusCaptured,
		"fulfillment_status": payment.FulfillmentFulfilled,
	}

	for key, want := range expected {
		if got := response[key]; got != want {
			t.Errorf("response[%q] = %v, want %v", key, got, want)
		}
	}

	if _, exists := response["customer_id"]; exists {
		t.Error("response must not expose customer ID")
	}

	if _, exists := response["signature"]; exists {
		t.Error("response must not expose payment signature")
	}
}

func TestVerifyPaymentRequiresAuthentication(t *testing.T) {
	service := &fakePaymentService{}
	r := newPaymentHandlerTestRouter(service, false)

	body := `{
		"payment_id": "payment-123",
		"provider_order_id": "order_123",
		"provider_payment_id": "pay_123",
		"signature": "signature"
	}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/payments/verify",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d",
			http.StatusUnauthorized, rec.Code)
	}

	if service.verifyCalled {
		t.Fatal("Verify must not be called without authentication")
	}
}

func TestVerifyPaymentRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "malformed JSON",
			body: `{"payment_id":`,
		},
		{
			name: "missing payment ID",
			body: `{
				"provider_order_id": "order_123",
				"provider_payment_id": "pay_123",
				"signature": "signature"
			}`,
		},
		{
			name: "missing order ID",
			body: `{
				"payment_id": "payment-123",
				"provider_payment_id": "pay_123",
				"signature": "signature"
			}`,
		},
		{
			name: "missing provider payment ID",
			body: `{
				"payment_id": "payment-123",
				"provider_order_id": "order_123",
				"signature": "signature"
			}`,
		},
		{
			name: "missing signature",
			body: `{
				"payment_id": "payment-123",
				"provider_order_id": "order_123",
				"provider_payment_id": "pay_123"
			}`,
		},
		{
			name: "empty fields",
			body: `{
				"payment_id": " ",
				"provider_order_id": "order_123",
				"provider_payment_id": "pay_123",
				"signature": "signature"
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakePaymentService{}
			r := newPaymentHandlerTestRouter(service, true)

			req := httptest.NewRequest(
				http.MethodPost,
				"/payments/verify",
				strings.NewReader(tt.body),
			)
			req.Header.Set("Content-Type", "application/json")

			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d: %s",
					http.StatusBadRequest, rec.Code, rec.Body.String())
			}

			if service.verifyCalled {
				t.Fatal("Verify must not be called for invalid requests")
			}
		})
	}
}

func TestVerifyPaymentRejectsClientSuppliedCustomerID(t *testing.T) {
	service := &fakePaymentService{}
	r := newPaymentHandlerTestRouter(service, true)

	body := `{
		"payment_id": "payment-123",
		"customer_id": "attacker-customer",
		"provider_order_id": "order_123",
		"provider_payment_id": "pay_123",
		"signature": "signature"
	}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/payments/verify",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s",
			rec.Code, rec.Body.String())
	}

	if service.verifyCalled {
		t.Fatal("Verify must not be called when client supplies customer ID")
	}
}

func TestVerifyPaymentRejectsUnknownFields(t *testing.T) {
	service := &fakePaymentService{}
	r := newPaymentHandlerTestRouter(service, true)

	body := `{
		"payment_id": "payment-123",
		"provider_order_id": "order_123",
		"provider_payment_id": "pay_123",
		"signature": "signature",
		"unexpected_field": "unexpected-value"
	}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/payments/verify",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s",
			rec.Code, rec.Body.String())
	}

	if service.verifyCalled {
		t.Fatal("Verify must not be called for unknown fields")
	}
}

func TestVerifyPaymentRejectsOversizedBody(t *testing.T) {
	service := &fakePaymentService{}
	r := newPaymentHandlerTestRouter(service, true)

	body := `{"payment_id":"` +
		strings.Repeat("a", maxJSONBodySize)

	req := httptest.NewRequest(
		http.MethodPost,
		"/payments/verify",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status 413, got %d: %s",
			rec.Code, rec.Body.String())
	}

	if service.verifyCalled {
		t.Fatal("Verify must not be called for oversized requests")
	}
}

func TestVerifyPaymentMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		serviceErr error
		wantStatus int
	}{
		{
			name:       "invalid signature",
			serviceErr: payment.ErrInvalidPaymentSignature,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "payment not found",
			serviceErr: payment.ErrPaymentNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "ownership mismatch",
			serviceErr: payment.ErrPaymentOwnershipMismatch,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "order mismatch",
			serviceErr: payment.ErrProviderOrderMismatch,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "amount mismatch",
			serviceErr: payment.ErrPaymentAmountMismatch,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "currency mismatch",
			serviceErr: payment.ErrPaymentCurrencyMismatch,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "payment not captured",
			serviceErr: payment.ErrPaymentNotCaptured,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "unexpected provider error",
			serviceErr: errors.New("Razorpay API unavailable"),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakePaymentService{
				verifyErr: tt.serviceErr,
			}

			r := newPaymentHandlerTestRouter(service, true)

			body := `{
				"payment_id": "payment-123",
				"provider_order_id": "order_123",
				"provider_payment_id": "pay_123",
				"signature": "signature"
			}`

			req := httptest.NewRequest(
				http.MethodPost,
				"/payments/verify",
				strings.NewReader(body),
			)
			req.Header.Set("Content-Type", "application/json")

			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d: %s",
					tt.wantStatus, rec.Code, rec.Body.String())
			}

			if !service.verifyCalled {
				t.Fatal("expected Verify service to be called")
			}
		})
	}
}

func TestVerifyPaymentRejectsNilResult(t *testing.T) {
	service := &fakePaymentService{}
	r := newPaymentHandlerTestRouter(service, true)

	body := `{
		"payment_id": "payment-123",
		"provider_order_id": "order_123",
		"provider_payment_id": "pay_123",
		"signature": "signature"
	}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/payments/verify",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d",
			http.StatusInternalServerError, rec.Code)
	}

	if !service.verifyCalled {
		t.Fatal("expected Verify service to be called")
	}
}
