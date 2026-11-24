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

	"staynest/internal/customer"
)

type fakeCustomerRegistrar struct {
	created *customer.Customer
	err     error

	gotName     string
	gotEmail    string
	gotPassword string
}

func (f *fakeCustomerRegistrar) Register(
	ctx context.Context,
	name string,
	email string,
	password string,
) (*customer.Customer, error) {
	f.gotName = name
	f.gotEmail = email
	f.gotPassword = password

	return f.created, f.err
}

func TestRegisterCustomerSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeCustomerRegistrar{
		created: &customer.Customer{
			ID:           "test-customer-id",
			Name:         "Alex Smith",
			Email:        "alex@example.com",
			PasswordHash: "secret-password-hash",
		},
	}

	router := gin.New()
	router.POST("/api/v1/auth/register", RegisterCustomer(service))

	body := `{
		"name": "Alex Smith",
		"email": "alex@example.com",
		"password": "MySecretPassword123"
	}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/register",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d: %s",
			http.StatusCreated, rec.Code, rec.Body.String())
	}

	var response registerCustomerResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if response.Message != "customer registered successfully" {
		t.Errorf("unexpected message: %q", response.Message)
	}

	if response.Customer.ID != "test-customer-id" {
		t.Errorf("unexpected customer ID: %q", response.Customer.ID)
	}

	if response.Customer.Email != "alex@example.com" {
		t.Errorf("unexpected email: %q", response.Customer.Email)
	}

	if service.gotPassword != "MySecretPassword123" {
		t.Fatal("expected password to be passed to the service")
	}

	if strings.Contains(rec.Body.String(), "secret-password-hash") {
		t.Fatal("response must not expose password hash")
	}

	if strings.Contains(rec.Body.String(), "password") {
		t.Fatal("response must not expose a password field")
	}
}

func TestRegisterCustomerRejectsMalformedJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeCustomerRegistrar{}
	router := gin.New()
	router.POST("/api/v1/auth/register", RegisterCustomer(service))

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/register",
		strings.NewReader(`{"name":`),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}

	if service.gotName != "" || service.gotEmail != "" || service.gotPassword != "" {
		t.Fatal("service should not be called for malformed JSON")
	}
}

func TestRegisterCustomerMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		serviceErr error
		wantStatus int
	}{
		{
			name:       "invalid name",
			serviceErr: customer.ErrInvalidName,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid email",
			serviceErr: customer.ErrInvalidEmail,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "short password",
			serviceErr: customer.ErrPasswordTooShort,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "duplicate email",
			serviceErr: customer.ErrEmailAlreadyExists,
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
			gin.SetMode(gin.TestMode)

			service := &fakeCustomerRegistrar{
				err: tt.serviceErr,
			}

			router := gin.New()
			router.POST("/api/v1/auth/register", RegisterCustomer(service))

			body := `{
				"name": "Alex",
				"email": "alex@example.com",
				"password": "Password123"
			}`

			req := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/auth/register",
				strings.NewReader(body),
			)
			req.Header.Set("Content-Type", "application/json")

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d: %s",
					tt.wantStatus, rec.Code, rec.Body.String())
			}

			if tt.wantStatus == http.StatusInternalServerError &&
				strings.Contains(rec.Body.String(), "database connection failed") {
				t.Fatal("internal error details must not be exposed")
			}
		})
	}
}

func TestRegisterCustomerRejectsUnknownFields(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeCustomerRegistrar{}
	router := gin.New()
	router.POST("/api/v1/auth/register", RegisterCustomer(service))

	body := `{
		"name": "Alex",
		"email": "alex@example.com",
		"password": "Password123",
		"role": "admin"
	}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/register",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s",
			rec.Code, rec.Body.String())
	}

	if service.gotName != "" || service.gotEmail != "" ||
		service.gotPassword != "" {
		t.Fatal("registration service must not be called")
	}
}

func TestRegisterCustomerRejectsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeCustomerRegistrar{}
	router := gin.New()
	router.POST("/api/v1/auth/register", RegisterCustomer(service))

	body := `{"name":"` + strings.Repeat("a", maxJSONBodySize) + `"}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/register",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status 413, got %d: %s",
			rec.Code, rec.Body.String())
	}

	if service.gotName != "" || service.gotEmail != "" ||
		service.gotPassword != "" {
		t.Fatal("registration service must not be called")
	}
}
