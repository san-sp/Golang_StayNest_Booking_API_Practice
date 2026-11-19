package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"staynest/internal/customer"
)

type fakeCustomerAccountDeactivator struct {
	err error

	called         bool
	gotID          string
	gotCurrentPass string
}

func (f *fakeCustomerAccountDeactivator) Deactivate(
	ctx context.Context,
	id string,
	currentPassword string,
) error {
	f.called = true
	f.gotID = id
	f.gotCurrentPass = currentPassword

	return f.err
}

func TestDeactivateCustomerSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeCustomerAccountDeactivator{}

	router := gin.New()
	router.DELETE(
		"/api/v1/customers/me",
		func(c *gin.Context) {
			c.Set("customer_id", "customer-123")
			c.Next()
		},
		DeactivateCustomer(service),
	)

	body := `{
		"current_password": "CurrentPassword123"
	}`

	req := httptest.NewRequest(
		http.MethodDelete,
		"/api/v1/customers/me",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf(
			"expected status 200, got %d: %s",
			http.StatusOK,
			rec.Body.String(),
		)
	}

	expected := `{"message":"account deactivated successfully"}`
	if strings.TrimSpace(rec.Body.String()) != expected {
		t.Fatalf("expected %s, got %s", expected, rec.Body.String())
	}

	if !service.called {
		t.Fatal("expected deactivation service to be called")
	}

	if service.gotID != "customer-123" {
		t.Errorf("unexpected customer ID: %q", service.gotID)
	}

	if service.gotCurrentPass != "CurrentPassword123" {
		t.Error("expected current password to be passed to service")
	}
}

func TestDeactivateCustomerRejectsMissingIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeCustomerAccountDeactivator{}

	router := gin.New()
	router.DELETE(
		"/api/v1/customers/me",
		DeactivateCustomer(service),
	)

	body := `{"current_password":"CurrentPassword123"}`

	req := httptest.NewRequest(
		http.MethodDelete,
		"/api/v1/customers/me",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", rec.Code)
	}

	if service.called {
		t.Fatal("service must not be called without authenticated identity")
	}
}

func TestDeactivateCustomerRejectsMalformedJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeCustomerAccountDeactivator{}

	router := gin.New()
	router.DELETE(
		"/api/v1/customers/me",
		func(c *gin.Context) {
			c.Set("customer_id", "customer-123")
			c.Next()
		},
		DeactivateCustomer(service),
	)

	req := httptest.NewRequest(
		http.MethodDelete,
		"/api/v1/customers/me",
		strings.NewReader(`{"current_password":`),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}

	if service.called {
		t.Fatal("service must not be called for malformed JSON")
	}
}

func TestDeactivateCustomerHandlesServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		serviceErr error
		wantStatus int
	}{
		{
			name:       "incorrect current password",
			serviceErr: customer.ErrInvalidCurrentPassword,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "customer not found",
			serviceErr: customer.ErrCustomerNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "database error",
			serviceErr: errors.New("database connection failed"),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)

			service := &fakeCustomerAccountDeactivator{
				err: tt.serviceErr,
			}

			router := gin.New()
			router.DELETE(
				"/api/v1/customers/me",
				func(c *gin.Context) {
					c.Set("customer_id", "customer-123")
					c.Next()
				},
				DeactivateCustomer(service),
			)

			body := `{"current_password":"CurrentPassword123"}`

			req := httptest.NewRequest(
				http.MethodDelete,
				"/api/v1/customers/me",
				strings.NewReader(body),
			)
			req.Header.Set("Content-Type", "application/json")

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf(
					"expected status %d, got %d: %s",
					tt.wantStatus,
					rec.Code,
					rec.Body.String(),
				)
			}

			if !service.called {
				t.Fatal("expected deactivation service to be called")
			}

			if strings.Contains(
				rec.Body.String(),
				"database connection failed",
			) {
				t.Fatal("internal error details must not be exposed")
			}
		})
	}
}
