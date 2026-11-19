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

type fakeCustomerPasswordChanger struct {
	err error

	called     bool
	gotID      string
	gotCurrent string
	gotNew     string
}

func (f *fakeCustomerPasswordChanger) ChangePassword(
	ctx context.Context,
	id string,
	currentPassword string,
	newPassword string,
) error {
	f.called = true
	f.gotID = id
	f.gotCurrent = currentPassword
	f.gotNew = newPassword

	return f.err
}

func TestChangeCustomerPasswordSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeCustomerPasswordChanger{}

	router := gin.New()
	router.POST(
		"/api/v1/auth/change-password",
		func(c *gin.Context) {
			c.Set("customer_id", "customer-123")
			c.Next()
		},
		ChangeCustomerPassword(service),
	)

	body := `{
		"current_password": "OldPassword123",
		"new_password": "NewPassword456"
	}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/change-password",
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

	expected := `{"message":"password changed successfully"}`
	if strings.TrimSpace(rec.Body.String()) != expected {
		t.Fatalf("expected %s, got %s", expected, rec.Body.String())
	}

	if !service.called {
		t.Fatal("expected password service to be called")
	}

	if service.gotID != "customer-123" {
		t.Errorf("unexpected customer ID: %q", service.gotID)
	}

	if service.gotCurrent != "OldPassword123" {
		t.Error("expected current password to be passed to service")
	}

	if service.gotNew != "NewPassword456" {
		t.Error("expected new password to be passed to service")
	}
}

func TestChangeCustomerPasswordRejectsMissingIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeCustomerPasswordChanger{}

	router := gin.New()
	router.POST(
		"/api/v1/auth/change-password",
		ChangeCustomerPassword(service),
	)

	body := `{
		"current_password": "OldPassword123",
		"new_password": "NewPassword456"
	}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/change-password",
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

func TestChangeCustomerPasswordRejectsMalformedJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeCustomerPasswordChanger{}

	router := gin.New()
	router.POST(
		"/api/v1/auth/change-password",
		func(c *gin.Context) {
			c.Set("customer_id", "customer-123")
			c.Next()
		},
		ChangeCustomerPassword(service),
	)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/change-password",
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

func TestChangeCustomerPasswordHandlesServiceErrors(t *testing.T) {
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
			name:       "new password too short",
			serviceErr: customer.ErrPasswordTooShort,
			wantStatus: http.StatusBadRequest,
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

			service := &fakeCustomerPasswordChanger{
				err: tt.serviceErr,
			}

			router := gin.New()
			router.POST(
				"/api/v1/auth/change-password",
				func(c *gin.Context) {
					c.Set("customer_id", "customer-123")
					c.Next()
				},
				ChangeCustomerPassword(service),
			)

			body := `{
				"current_password": "OldPassword123",
				"new_password": "NewPassword456"
			}`

			req := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/auth/change-password",
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
				t.Fatal("expected password service to be called")
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
