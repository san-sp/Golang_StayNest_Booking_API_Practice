package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"staynest/internal/payment"
)

type fakePaymentWebhookService struct {
	processFunc func(
		ctx context.Context,
		eventID string,
		signature string,
		body []byte,
	) error

	calls     int
	eventID   string
	signature string
	body      []byte
}

func (f *fakePaymentWebhookService) Process(
	ctx context.Context,
	eventID string,
	signature string,
	body []byte,
) error {
	f.calls++
	f.eventID = eventID
	f.signature = signature
	f.body = append([]byte(nil), body...)

	if f.processFunc != nil {
		return f.processFunc(ctx, eventID, signature, body)
	}

	return nil
}

func newWebhookTestRequest(
	body string,
	eventID string,
	signature string,
) (*httptest.ResponseRecorder, *fakePaymentWebhookService) {
	gin.SetMode(gin.TestMode)

	service := &fakePaymentWebhookService{}

	router := gin.New()
	router.POST(
		"/api/v1/webhooks/razorpay",
		RazorpayWebhook(service),
	)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/webhooks/razorpay",
		strings.NewReader(body),
	)

	if eventID != "" {
		req.Header.Set("x-razorpay-event-id", eventID)
	}

	if signature != "" {
		req.Header.Set("X-Razorpay-Signature", signature)
	}

	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	return rec, service
}

func TestRazorpayWebhookSuccess(t *testing.T) {
	body := `{"event":"payment.captured"}`

	rec, service := newWebhookTestRequest(
		body,
		"evt_http_success",
		"test-signature",
	)

	if rec.Code != http.StatusOK {
		t.Fatalf(
			"expected status %d, got %d: %s",
			http.StatusOK,
			rec.Code,
			rec.Body.String(),
		)
	}

	if service.calls != 1 {
		t.Fatalf("expected one service call, got %d", service.calls)
	}

	if service.eventID != "evt_http_success" {
		t.Errorf("unexpected event ID: %q", service.eventID)
	}

	if service.signature != "test-signature" {
		t.Errorf("unexpected signature: %q", service.signature)
	}

	if string(service.body) != body {
		t.Errorf(
			"expected original request body %q, got %q",
			body,
			string(service.body),
		)
	}
}

func TestRazorpayWebhookRejectsMissingEventID(t *testing.T) {
	rec, service := newWebhookTestRequest(
		`{"event":"payment.captured"}`,
		"",
		"test-signature",
	)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusBadRequest,
			rec.Code,
		)
	}

	if service.calls != 0 {
		t.Errorf("expected zero service calls, got %d", service.calls)
	}
}

func TestRazorpayWebhookRejectsMissingSignature(t *testing.T) {
	rec, service := newWebhookTestRequest(
		`{"event":"payment.captured"}`,
		"evt_missing_signature",
		"",
	)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusBadRequest,
			rec.Code,
		)
	}

	if service.calls != 0 {
		t.Errorf("expected zero service calls, got %d", service.calls)
	}
}

func TestRazorpayWebhookRejectsInvalidSignature(t *testing.T) {
	service := &fakePaymentWebhookService{
		processFunc: func(
			ctx context.Context,
			eventID string,
			signature string,
			body []byte,
		) error {
			return payment.ErrInvalidWebhookSignature
		},
	}

	router := gin.New()
	router.POST(
		"/api/v1/webhooks/razorpay",
		RazorpayWebhook(service),
	)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/webhooks/razorpay",
		strings.NewReader(`{"event":"payment.captured"}`),
	)

	req.Header.Set("x-razorpay-event-id", "evt_invalid_signature")
	req.Header.Set("X-Razorpay-Signature", "invalid-signature")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf(
			"expected status %d, got %d: %s",
			http.StatusBadRequest,
			rec.Code,
			rec.Body.String(),
		)
	}

	if service.calls != 1 {
		t.Errorf("expected one service call, got %d", service.calls)
	}
}

func TestRazorpayWebhookRejectsMalformedPayload(t *testing.T) {
	service := &fakePaymentWebhookService{
		processFunc: func(
			ctx context.Context,
			eventID string,
			signature string,
			body []byte,
		) error {
			return payment.ErrInvalidWebhookPayload
		},
	}

	router := gin.New()
	router.POST(
		"/api/v1/webhooks/razorpay",
		RazorpayWebhook(service),
	)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/webhooks/razorpay",
		strings.NewReader(`{"event":`),
	)

	req.Header.Set("x-razorpay-event-id", "evt_malformed")
	req.Header.Set("X-Razorpay-Signature", "test-signature")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusBadRequest,
			rec.Code,
		)
	}
}

func TestRazorpayWebhookRejectsOversizedBody(t *testing.T) {
	body := strings.Repeat("a", maxWebhookBodySize+1)

	rec, service := newWebhookTestRequest(
		body,
		"evt_oversized",
		"test-signature",
	)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusRequestEntityTooLarge,
			rec.Code,
		)
	}

	if service.calls != 0 {
		t.Errorf("expected zero service calls, got %d", service.calls)
	}
}

func TestRazorpayWebhookReturnsInternalServerError(t *testing.T) {
	service := &fakePaymentWebhookService{
		processFunc: func(
			ctx context.Context,
			eventID string,
			signature string,
			body []byte,
		) error {
			return errors.New("database connection failed")
		},
	}

	router := gin.New()
	router.POST(
		"/api/v1/webhooks/razorpay",
		RazorpayWebhook(service),
	)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/webhooks/razorpay",
		strings.NewReader(`{"event":"payment.captured"}`),
	)

	req.Header.Set("x-razorpay-event-id", "evt_processing_error")
	req.Header.Set("X-Razorpay-Signature", "test-signature")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusInternalServerError,
			rec.Code,
		)
	}
}

func TestRazorpayWebhookReturnsInternalServerErrorForUnknownPayment(t *testing.T) {
	service := &fakePaymentWebhookService{
		processFunc: func(
			ctx context.Context,
			eventID string,
			signature string,
			body []byte,
		) error {
			return payment.ErrPaymentNotFound
		},
	}

	router := gin.New()
	router.POST(
		"/api/v1/webhooks/razorpay",
		RazorpayWebhook(service),
	)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/webhooks/razorpay",
		strings.NewReader(`{"event":"payment.captured"}`),
	)

	req.Header.Set("x-razorpay-event-id", "evt_unknown_payment")
	req.Header.Set("X-Razorpay-Signature", "test-signature")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusInternalServerError,
			rec.Code,
		)
	}
}

func TestRazorpayWebhookAcknowledgesDuplicateDelivery(t *testing.T) {
	service := &fakePaymentWebhookService{}

	router := gin.New()
	router.POST(
		"/api/v1/webhooks/razorpay",
		RazorpayWebhook(service),
	)

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/webhooks/razorpay",
			strings.NewReader(`{"event":"payment.captured"}`),
		)

		req.Header.Set("x-razorpay-event-id", "evt_duplicate_http")
		req.Header.Set("X-Razorpay-Signature", "test-signature")

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf(
				"delivery %d: expected status %d, got %d",
				i+1,
				http.StatusOK,
				rec.Code,
			)
		}
	}

	if service.calls != 2 {
		t.Errorf("expected two service calls, got %d", service.calls)
	}
}

func TestRazorpayWebhookRejectsNilService(t *testing.T) {
	rec, _ := newWebhookTestRequest(
		`{"event":"payment.captured"}`,
		"evt_nil_service",
		"test-signature",
	)

	// Test the nil-service handler separately.
	router := gin.New()
	router.POST(
		"/api/v1/webhooks/razorpay",
		RazorpayWebhook(nil),
	)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/webhooks/razorpay",
		strings.NewReader(`{"event":"payment.captured"}`),
	)

	req.Header.Set("x-razorpay-event-id", "evt_nil_service")
	req.Header.Set("X-Razorpay-Signature", "test-signature")

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusInternalServerError,
			rec.Code,
		)
	}
}
